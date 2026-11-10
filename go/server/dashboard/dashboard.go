package dashboard

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"order_server/go/global"
	"order_server/go/models"

	"github.com/sirupsen/logrus"
)

// ---------------- 看板数据快照 ----------------
// 旧实现每 10s 无条件执行 `SELECT * FROM address_infos WHERE deleted_at IS NULL`，
// 把整张表（含 address / product_specs / remarks 大字段）反复经网络拉回内存，
// 这就是慢查询日志里反复出现的那条慢 SQL；而且无论用户是否打开看板都在跑。
//
// 现在的加载策略（由首页触发，见 App.WarmupDashboard）：
//  1. 进入首页（startup / 登录成功 / 前端首页挂载）时异步做「唯一一次」全量加载，
//     用 Omit("Remarks") 生成明确的列清单投影，不 SELECT *；
//     用户之后点开看板只读内存，绝不在点击时查库。
//  2. 之后只做索引增量：按自增主键水位拉新增行（PRIMARY 范围扫描）、
//     借 idx_addr_updated_id(updated_at,id) 反向索引扫描最近变更的若干行做探测，
//     再按主键精确回拉变化的行。两条都不全表扫描，也不依赖本机与数据库时钟同步。
//  3. 订单入库 CreateOrder 主动 MarkDirty；看板活跃期间每 probeInterval 低频探测，
//     长时间无人访问则停止探测，后台彻底零 SQL。
//  4. 聚合结果按 (周期/范围/数据版本/商品归属指纹) 缓存，前端 10s 轮询只命中缓存；
//     GeneratedAt 只在底层数据真正变化时才变，前端据此跳过无意义的图表全量重绘。
const (
	probeInterval  = 30 * time.Second
	activeTTL      = 5 * time.Minute
	recentLimit    = 200 // 最近变更探测的行数上限：固定开销，与表大小无关
	aggCacheLimit  = 16
	updatedIdxName = "idx_addr_updated_id"
)

// rowStamp 一行在「最近变更探测」里的指纹：更新时刻 + 是否已软删。
type rowStamp struct {
	updatedAt time.Time
	deleted   bool
}

var (
	// stateMu 保护内存快照；写时整体替换 slice，读时只取 slice header，锁外聚合无竞态。
	stateMu       sync.RWMutex
	stateRows     []models.AddressInfo // 按 id 升序，保证同值排序稳定
	stateMaxID    uint
	stateRecent   map[uint]rowStamp // 上一轮「最近变更」探测的指纹快照
	stateLoaded   bool              // 显式就绪标志：空库也算加载完成，不会永远 Loading
	stateVersion  uint64            // 数据版本，只有底层数据变化才 +1
	stateLoadedAt time.Time

	refreshMu  sync.Mutex // 串行化所有 DB 刷新，杜绝首次加载与增量重叠
	warmupOnce sync.Once
	refreshReq = make(chan struct{}, 1) // 缓冲 1：多次 MarkDirty 自动合并成一次增量
	workerStop = make(chan struct{})    // 关闭信号：StopWorker 关闭它，worker 据此退出
	stopOnce   sync.Once
	lastActive atomic.Int64             // 最近一次有人活跃（首页/看板）的 UnixNano
	updatedIdx atomic.Int32             // 0 未知 / 1 索引存在 / 2 索引缺失

	aggMu    sync.Mutex
	aggCache = map[string]DashboardResponse{}
)

func markActive() { lastActive.Store(time.Now().UnixNano()) }

func isActive() bool {
	v := lastActive.Load()
	return v > 0 && time.Since(time.Unix(0, v)) <= activeTTL
}

// EnsureRunning 幂等启动看板后台加载 worker。
// 触发时机是「进入首页」：App startup（Guest 模式）、Login 成功、前端首页挂载。
// 正常路径下看板数据在用户点开「经营看板」之前就已就绪，点击时零 SQL。
func EnsureRunning() {
	markActive()
	warmupOnce.Do(func() { go worker() })
}

// MarkDirty 订单确认入库后调用，只触发一次轻量的索引增量同步，不阻塞调用方。
func MarkDirty() {
	EnsureRunning()
	select {
	case refreshReq <- struct{}{}:
	default: // 已有待处理请求，合并即可
	}
}

func worker() {
	loadFull()
	t := time.NewTicker(probeInterval)
	defer t.Stop()
	for {
		select {
		case <-refreshReq:
			loadIncremental()
		case <-t.C:
			// 长时间没人看板就不探测数据库；订单入库仍会通过 MarkDirty 主动唤醒。
			if isActive() {
				loadIncremental()
			}
		case <-workerStop:
			// 进程关闭：退出无限循环，避免关窗口后 worker 仍在跑。
			return
		}
	}
}

// StopWorker 通知后台 worker 退出。幂等（多次调用安全）。
func StopWorker() {
	stopOnce.Do(func() {
		if workerStop != nil {
			close(workerStop)
		}
	})
}

func loadFull() {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	loadFullLocked()
}

// loadFullLocked 整个进程生命周期只跑一次：投影全表 + 建立水位。
func loadFullLocked() {
	db := global.Db
	if db == nil {
		return
	}
	started := time.Now()
	var rows []models.AddressInfo
	// Omit 用字段名，列名交给 GORM 生成（实际为 product_specs 等 snake_case），
	// 避免硬编码列名出错；生成的是明确列清单而非 SELECT *，remarks 大字段不回传。
	// 软删条件 deleted_at IS NULL 由 GORM 模型自动附加。
	if err := db.Model(&models.AddressInfo{}).Omit("Remarks").Find(&rows).Error; err != nil {
		logrus.Errorf("看板数据预热失败: %v", err)
		return
	}
	sortRowsByID(rows)

	maxID := uint(0)
	for _, r := range rows {
		if r.ID > maxID {
			maxID = r.ID
		}
	}
	// 变更探测基线直接由本次全量结果在内存里生成，不额外发 SQL；
	// 补录历史订单的 updated_at 是过去日期也没关系，探测是相对快照比对而非时间阈值。
	publish(rows, maxID, topRecent(rows))
	logrus.Infof("看板数据预热完成: %d 行, 耗时 %s", len(rows), time.Since(started))
}

func loadIncremental() {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	db := global.Db
	if db == nil {
		return
	}

	stateMu.RLock()
	loaded := stateLoaded
	maxID := stateMaxID
	oldRows := stateRows
	oldRecent := stateRecent
	stateMu.RUnlock()
	if !loaded {
		loadFullLocked() // 首次加载失败后的兜底重试（此时已持有 refreshMu）
		return
	}

	// ① 新增行：自增主键水位之后，走 PRIMARY 范围扫描，返回行数 = 新建订单数。
	// Unscoped 是为了连「插入后立刻软删」的行也能推进水位，避免每轮重复拉。
	var fresh []models.AddressInfo
	if err := db.Unscoped().Model(&models.AddressInfo{}).Omit("Remarks").
		Where("id > ?", maxID).Find(&fresh).Error; err != nil {
		logrus.Warnf("看板增量同步（新增）失败: %v", err)
		return
	}

	// ② 最近变更探测：只取 3 个轻列、固定 recentLimit 行，
	// 走 idx_addr_updated_id(updated_at,id) 反向索引扫描（无 filesort、与表大小无关）。
	// 与上一轮指纹比对，只对指纹变化的 id 按主键精确回拉完整行；
	// 索引缺失时宁可跳过这一步，也不发一条会退化成全表扫描的范围查询。
	recent := oldRecent
	var changed []models.AddressInfo
	if hasUpdatedIndex() {
		var probe []models.AddressInfo
		if err := db.Unscoped().Model(&models.AddressInfo{}).
			Select("id, updated_at, deleted_at").
			Order("updated_at DESC, id DESC").Limit(recentLimit).
			Find(&probe).Error; err != nil {
			logrus.Warnf("看板增量同步（变更探测）失败: %v", err)
		} else {
			recent = make(map[uint]rowStamp, len(probe))
			stale := make([]uint, 0, len(probe))
			for _, r := range probe {
				st := rowStamp{updatedAt: r.UpdatedAt, deleted: r.DeletedAt.Valid}
				recent[r.ID] = st
				prev, seen := oldRecent[r.ID]
				if !seen || !sameStamp(prev, st) {
					stale = append(stale, r.ID)
				}
			}
			if len(stale) > 0 {
				if err := db.Unscoped().Model(&models.AddressInfo{}).Omit("Remarks").
					Where("id IN ?", stale).Find(&changed).Error; err != nil {
					logrus.Warnf("看板增量同步（变更行）失败: %v", err)
				}
			}
		}
	}

	newMaxID := maxID
	for _, r := range fresh {
		if r.ID > newMaxID {
			newMaxID = r.ID
		}
	}

	merged := make(map[uint]models.AddressInfo, len(oldRows)+len(fresh)+len(changed))
	for _, r := range oldRows {
		merged[r.ID] = r
	}
	dirty := false
	apply := func(r models.AddressInfo) {
		if r.ID == 0 {
			return
		}
		if r.DeletedAt.Valid { // 软删：从内存快照移除
			if _, exists := merged[r.ID]; exists {
				delete(merged, r.ID)
				dirty = true
			}
			return
		}
		old, exists := merged[r.ID]
		if !exists || !sameRow(old, r) {
			merged[r.ID] = r
			dirty = true
		}
	}
	for _, r := range fresh {
		apply(r)
	}
	for _, r := range changed {
		apply(r)
	}

	if !dirty {
		stateMu.Lock()
		stateMaxID = newMaxID
		stateRecent = recent
		stateMu.Unlock()
		return
	}

	rows := make([]models.AddressInfo, 0, len(merged))
	for _, r := range merged {
		rows = append(rows, r)
	}
	sortRowsByID(rows)
	publish(rows, newMaxID, recent)
	logrus.Infof("看板增量同步完成: %d -> %d 行", len(oldRows), len(rows))
}

// hasUpdatedIndex 缓存索引探测结果，避免每轮增量都发一次 SHOW INDEX。
// flags.FlagDB 启动时会幂等补建 idx_addr_updated_id；若建失败，本次进程
// 只保留主键增量（永远不会退化成全表扫描）。
func hasUpdatedIndex() bool {
	switch updatedIdx.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	db := global.Db
	if db == nil {
		return false
	}
	ok := db.Migrator().HasIndex(&models.AddressInfo{}, updatedIdxName)
	if ok {
		updatedIdx.Store(1)
	} else {
		updatedIdx.Store(2)
		logrus.Warnf("缺少索引 %s，已跳过「更新/软删」增量；重启或手工执行 ALTER TABLE 补建后恢复", updatedIdxName)
	}
	return ok
}

func publish(rows []models.AddressInfo, maxID uint, recent map[uint]rowStamp) {
	stateMu.Lock()
	stateRows = rows
	stateMaxID = maxID
	stateRecent = recent
	stateLoaded = true
	stateVersion++
	stateLoadedAt = time.Now()
	stateMu.Unlock()
	clearAggCache()
}

func clearAggCache() {
	aggMu.Lock()
	aggCache = make(map[string]DashboardResponse, aggCacheLimit)
	aggMu.Unlock()
}

func getAgg(key string) (DashboardResponse, bool) {
	aggMu.Lock()
	defer aggMu.Unlock()
	resp, ok := aggCache[key]
	return resp, ok
}

func putAgg(key string, resp DashboardResponse) {
	aggMu.Lock()
	defer aggMu.Unlock()
	if len(aggCache) >= aggCacheLimit {
		aggCache = make(map[string]DashboardResponse, aggCacheLimit)
	}
	aggCache[key] = resp
}

func sortRowsByID(rows []models.AddressInfo) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
}

// topRecent 从全量结果里生成「最近变更」指纹基线（启动预热时在内存里算，不额外查库）。
// 排序规则必须与探测 SQL 的 ORDER BY updated_at DESC, id DESC 一致。
func topRecent(rows []models.AddressInfo) map[uint]rowStamp {
	idx := make([]int, len(rows))
	for i := range rows {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool {
		a, b := rows[idx[i]], rows[idx[j]]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return a.ID > b.ID
	})
	if len(idx) > recentLimit {
		idx = idx[:recentLimit]
	}
	out := make(map[uint]rowStamp, len(idx))
	for _, i := range idx {
		out[rows[i].ID] = rowStamp{updatedAt: rows[i].UpdatedAt, deleted: rows[i].DeletedAt.Valid}
	}
	return out
}

// sameStamp 用 Equal 比较时间，避免 time.Location 指针差异造成假变更。
func sameStamp(a, b rowStamp) bool {
	return a.deleted == b.deleted && a.updatedAt.Equal(b.updatedAt)
}

// sameRow 判断增量拉回的行是否真的变了：水位窗口内会重复拉到同样的行，
// 只有内容真正变化才 bump 数据版本，避免每轮探测都触发聚合与图表重绘。
func sameRow(a, b models.AddressInfo) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) || !a.UpdatedAt.Equal(b.UpdatedAt) ||
		a.Recipient != b.Recipient || a.Phone != b.Phone || a.Address != b.Address ||
		a.DeletedAt.Valid != b.DeletedAt.Valid {
		return false
	}
	if len(a.ProductSpecs) != len(b.ProductSpecs) {
		return false
	}
	for i := range a.ProductSpecs {
		if a.ProductSpecs[i] != b.ProductSpecs[i] {
			return false
		}
	}
	return true
}

// belongFingerprint 给商品归属配置算稳定指纹：cfg_product_belongs 热更新后
// 聚合缓存自动失效，不需要额外事件或手动失效。
func belongFingerprint() uint64 {
	belongs := global.GetProductBelongs()
	keys := make([]string, 0, len(belongs))
	for k := range belongs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		for _, alias := range belongs[k] {
			_, _ = h.Write([]byte(strings.TrimSpace(alias)))
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func cacheKey(req DashboardRequest, version uint64, fp uint64) string {
	return fmt.Sprintf("%s|%d|%d|%d|%d", req.Period, req.Start.UnixNano(), req.End.UnixNano(), version, fp)
}

// DashboardRequest 看板查询条件：period 决定趋势分桶粒度（day/week/month），
// start/end 为可选的时间范围过滤（零值表示不限制）。
type DashboardRequest struct {
	Period string    `json:"period"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

type TrendPoint struct {
	Key     string  `json:"key"`     // 分桶展示标签（MM-DD / 2006-01）
	SortKey string  `json:"sortKey"` // 用于排序的时间键（RFC3339）
	Orders  int     `json:"orders"`
	Weight  float64 `json:"weight"` // 斤
}

type NameValue struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"` // 斤
	Orders int     `json:"orders"`
}

type CustomerValue struct {
	Name   string  `json:"name"` // 手机号优先，否则收件人
	Orders int     `json:"orders"`
	Weight float64 `json:"weight"`
}

type ProvinceCount struct {
	Name   string `json:"name"` // 与 ECharts 中国地图 GeoJSON 全名一致
	Orders int    `json:"orders"`
}

type Summary struct {
	TotalOrders     int     `json:"totalOrders"`
	TotalWeight     float64 `json:"totalWeight"` // 斤
	RepurchaseRate  float64 `json:"repurchaseRate"`
	CustomerCount   int     `json:"customerCount"`
	ProductCount    int     `json:"productCount"`
	UnknownProvince int     `json:"unknownProvince"` // 地址未解析出省份的订单数
}

type DashboardResponse struct {
	Summary      Summary         `json:"summary"`
	Trend        []TrendPoint    `json:"trend"`
	TopProducts  []NameValue     `json:"topProducts"`
	TopCustomers []CustomerValue `json:"topCustomers"`
	Province     []ProvinceCount `json:"province"`
	GeneratedAt  string          `json:"generatedAt"`
	// UnmappedProducts 未命中 cfg_product_belongs 的商品，按斤降序全量返回。
	// 这些规格不进爆品榜（但会计入总销量/趋势/客户贡献），前端据此提示用户补配置。
	UnmappedProducts []NameValue `json:"unmappedProducts"`
	// BelongReady=false 表示商品归属配置尚未加载，此时爆品榜退回按原始品名统计，
	// 避免配置读取失败时看板一片空白。
	BelongReady bool `json:"belongReady"`
	// Loading 为 true 表示内存数据尚未就绪（首次进入看板，后台正在加载中），
	// 前端应轮询直到该字段为 false 再渲染，避免阻塞首屏。
	Loading bool `json:"loading"`
}

// provinceRules 地址前缀 -> 省份全名（全名必须与 GeoJSON 的 properties.name 完全一致，
// 否则中国地图无法着色）。规则按"更长/更具体"的写法成对给出（如 广东省 / 广东）。
var provinceRules = []struct {
	prefixes []string
	name     string
}{
	{[]string{"北京市", "北京"}, "北京市"},
	{[]string{"天津市", "天津"}, "天津市"},
	{[]string{"上海市", "上海"}, "上海市"},
	{[]string{"重庆市", "重庆"}, "重庆市"},
	{[]string{"河北省", "河北"}, "河北省"},
	{[]string{"山西省", "山西"}, "山西省"},
	{[]string{"辽宁省", "辽宁"}, "辽宁省"},
	{[]string{"吉林省", "吉林"}, "吉林省"},
	{[]string{"黑龙江省", "黑龙江"}, "黑龙江省"},
	{[]string{"江苏省", "江苏"}, "江苏省"},
	{[]string{"浙江省", "浙江"}, "浙江省"},
	{[]string{"安徽省", "安徽"}, "安徽省"},
	{[]string{"福建省", "福建"}, "福建省"},
	{[]string{"江西省", "江西"}, "江西省"},
	{[]string{"山东省", "山东"}, "山东省"},
	{[]string{"河南省", "河南"}, "河南省"},
	{[]string{"湖北省", "湖北"}, "湖北省"},
	{[]string{"湖南省", "湖南"}, "湖南省"},
	{[]string{"广东省", "广东"}, "广东省"},
	{[]string{"海南省", "海南"}, "海南省"},
	{[]string{"四川省", "四川"}, "四川省"},
	{[]string{"贵州省", "贵州"}, "贵州省"},
	{[]string{"云南省", "云南"}, "云南省"},
	{[]string{"陕西省", "陕西"}, "陕西省"},
	{[]string{"甘肃省", "甘肃"}, "甘肃省"},
	{[]string{"青海省", "青海"}, "青海省"},
	{[]string{"台湾省", "台湾"}, "台湾省"},
	{[]string{"内蒙古自治区", "内蒙古"}, "内蒙古自治区"},
	{[]string{"广西壮族自治区", "广西"}, "广西壮族自治区"},
	{[]string{"西藏自治区", "西藏"}, "西藏自治区"},
	{[]string{"宁夏回族自治区", "宁夏"}, "宁夏回族自治区"},
	{[]string{"新疆维吾尔自治区", "新疆"}, "新疆维吾尔自治区"},
	{[]string{"香港特别行政区", "香港"}, "香港特别行政区"},
	{[]string{"澳门特别行政区", "澳门"}, "澳门特别行政区"},
}

// parseProvince 从整串收货地址里提取省份全名；识别不出返回空串。
func parseProvince(address string) string {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return ""
	}
	for _, rule := range provinceRules {
		for _, p := range rule.prefixes {
			if strings.HasPrefix(addr, p) {
				return rule.name
			}
		}
	}
	return ""
}

// buildBelongIndex 把 cfg_product_belongs 的内存映射（一级属性 -> 别名列表）摊平成
// 「订单里的原始品名 -> 一级属性」索引，每次聚合构建一份快照。
//
// 爆品榜只认这张索引：命中才计入（先筛选），并把别名统一归并到一级属性展示；
// 未命中的规格单独统计给前端提示，不再原样混进榜单。配置源与账单结算
// （checkout.GetDaydata 使用的 global.GetProductBelongs()）保持完全一致。
//
// 确定性处理：先按一级属性名排序并占位，再按同样顺序写入别名且不覆盖已有键。
// 这样即使两个分类配了同一个别名、或某分类名恰好是别家的别名，结果也稳定，
// 不会因为 map 遍历顺序随机导致榜单每次刷新跳动。
func buildBelongIndex() map[string]string {
	// GetProductBelongs 返回的是不可变副本，一次取用即可，避免两次拷贝与竞态。
	belongs := global.GetProductBelongs()
	cfg := make(map[string][]string, len(belongs))
	for k, v := range belongs {
		k = strings.TrimSpace(k)
		if k != "" {
			cfg[k] = v
		}
	}
	cats := make([]string, 0, len(cfg))
	for k := range cfg {
		cats = append(cats, k)
	}
	sort.Strings(cats)

	idx := make(map[string]string, len(cfg)*4)
	for _, cat := range cats {
		idx[cat] = cat
	}
	for _, cat := range cats {
		for _, alias := range cfg[cat] {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			if _, exists := idx[alias]; !exists {
				idx[alias] = cat
			}
		}
	}
	return idx
}

// bucket 把时间按周期分桶，返回展示标签与用于排序的时间锚点。
func bucket(period string, t time.Time) (string, time.Time) {
	loc := t.Location()
	switch period {
	case "week":
		wd := int(t.Weekday())
		if wd == 0 {
			wd = 7
		}
		monday := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(wd - 1))
		return monday.Format("01-02"), monday
	case "month":
		m := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		return m.Format("2006-01"), m
	default:
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		return d.Format("01-02"), d
	}
}

func round1(f float64) float64 {
	return math.Round(f*10) / 10
}

// GetDashboard 只读内存快照 + 聚合缓存，绝不在这里查库。
// 数据由首页 WarmupDashboard / App startup / Login 提前异步预热；
// 仅当预热尚未完成的异常场景返回 Loading=true 并补一次幂等唤醒（兜底）。
func GetDashboard(req DashboardRequest) DashboardResponse {
	markActive()
	stateMu.RLock()
	rows := stateRows
	loaded := stateLoaded
	version := stateVersion
	loadedAt := stateLoadedAt
	stateMu.RUnlock()

	if !loaded {
		EnsureRunning()
		return DashboardResponse{Loading: true}
	}

	key := cacheKey(req, version, belongFingerprint())
	if resp, ok := getAgg(key); ok {
		return resp
	}
	resp := aggregate(rows, req)
	resp.GeneratedAt = loadedAt.Format("2006-01-02 15:04:05")
	putAgg(key, resp)
	return resp
}

// aggregate 在内存原始订单列表上做全部聚合（含时间范围过滤），不触碰数据库。
func aggregate(list []models.AddressInfo, req DashboardRequest) DashboardResponse {
	resp := DashboardResponse{}
	period := req.Period
	if period != "week" && period != "month" {
		period = "day"
	}

	startZero := req.Start.IsZero()
	endZero := req.End.IsZero()
	inRange := func(t time.Time) bool {
		if startZero || endZero {
			return true
		}
		return !t.Before(req.Start) && !t.After(req.End)
	}

	belongIdx := buildBelongIndex()
	belongReady := len(belongIdx) > 0
	// productWeight 只收「命中商品归属配置」的规格，且键一律是一级属性，专供爆品榜。
	productWeight := map[string]float64{}
	productOrders := map[string]int{}
	// 未命中配置的规格单独记账，用于前端提示，不参与爆品榜。
	unmappedWeight := map[string]float64{}
	unmappedOrders := map[string]int{}
	customerOrders := map[string]int{}
	customerWeight := map[string]float64{}
	provinceOrders := map[string]int{}
	trendMap := map[string]*TrendPoint{}
	trendSort := map[string]time.Time{}
	unknownProvince := 0
	customerSet := map[string]struct{}{}
	// 总销量/趋势/客户贡献仍按全量规格计算，不受爆品榜的筛选影响。
	totalWeightAll := 0.0
	matchedOrders := 0

	for _, info := range list {
		if !inRange(info.CreatedAt) {
			continue
		}
		matchedOrders++
		// 客户标识：手机号优先，否则收件人
		custKey := strings.TrimSpace(info.Phone)
		if custKey == "" {
			custKey = strings.TrimSpace(info.Recipient)
		}
		if custKey != "" {
			if _, seen := customerOrders[custKey]; !seen {
				customerSet[custKey] = struct{}{}
			}
			customerOrders[custKey]++
		}

		// 省份
		if prov := parseProvince(info.Address); prov == "" {
			unknownProvince++
		} else {
			provinceOrders[prov]++
		}

		// 商品与订单重量：先过 cfg_product_belongs 筛选，命中才归并到一级属性。
		orderWeight := 0.0
		for _, spec := range info.ProductSpecs {
			w := float64(spec.Weight)
			orderWeight += w
			totalWeightAll += w

			name := strings.TrimSpace(spec.Name)
			if name == "" {
				name = "未命名商品"
			}
			if !belongReady {
				// 配置未加载时退回原始品名，保证看板不空白（前端会给出提示）。
				productWeight[name] += w
				productOrders[name]++
				continue
			}
			if cat, ok := belongIdx[name]; ok {
				productWeight[cat] += w
				productOrders[cat]++
			} else {
				unmappedWeight[name] += w
				unmappedOrders[name]++
			}
		}
		if custKey != "" {
			customerWeight[custKey] += orderWeight
		}

		// 趋势分桶
		k, sk := bucket(period, info.CreatedAt)
		if tp, ok := trendMap[k]; ok {
			tp.Orders++
			tp.Weight += orderWeight
		} else {
			trendMap[k] = &TrendPoint{Key: k, SortKey: sk.Format(time.RFC3339), Orders: 1, Weight: orderWeight}
			trendSort[k] = sk
		}
	}

	// 复购率：下单 >=2 次的客户数 / 客户总数
	multiOrder := 0
	for _, n := range customerOrders {
		if n >= 2 {
			multiOrder++
		}
	}
	repurchase := 0.0
	if len(customerSet) > 0 {
		repurchase = float64(multiOrder) / float64(len(customerSet))
	}

	resp.Summary = Summary{
		TotalOrders:     matchedOrders, // 按时间范围过滤后的订单数
		TotalWeight:     round1(totalWeightAll),
		RepurchaseRate:  round1(repurchase*100) / 100,
		CustomerCount:   len(customerSet),
		ProductCount:    len(productWeight), // 有销量的一级属性数（配置未加载时为原始品名数）
		UnknownProvince: unknownProvince,
	}

	// 趋势：按时间锚点升序
	keys := make([]string, 0, len(trendMap))
	for k := range trendMap {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return trendSort[keys[i]].Before(trendSort[keys[j]])
	})
	for _, k := range keys {
		resp.Trend = append(resp.Trend, *trendMap[k])
	}

	resp.TopProducts = topNameValues(productWeight, productOrders, 10)
	// 未命中配置的商品全量返回（n<=0 不截断），前端自行汇总斤数与展示前几种品名。
	resp.UnmappedProducts = topNameValues(unmappedWeight, unmappedOrders, 0)
	resp.BelongReady = belongReady
	resp.TopCustomers = topCustomers(customerOrders, customerWeight, 10)
	resp.Province = topProvinces(provinceOrders)
	return resp
}

// topNameValues 按斤降序取前 n 条；n<=0 表示全量返回。
func topNameValues(weight map[string]float64, orders map[string]int, n int) []NameValue {
	type kv struct {
		name string
		w    float64
		o    int
	}
	arr := make([]kv, 0, len(weight))
	for k, w := range weight {
		arr = append(arr, kv{k, w, orders[k]})
	}
	// 同斤数时按名称兜底排序，保证每次后台刷新的榜单顺序稳定、不闪烁。
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].w != arr[j].w {
			return arr[i].w > arr[j].w
		}
		return arr[i].name < arr[j].name
	})
	if n > 0 && len(arr) > n {
		arr = arr[:n]
	}
	out := make([]NameValue, 0, len(arr))
	for _, e := range arr {
		out = append(out, NameValue{Name: e.name, Value: round1(e.w), Orders: e.o})
	}
	return out
}

func topCustomers(orders map[string]int, weight map[string]float64, n int) []CustomerValue {
	type kv struct {
		name string
		o    int
		w    float64
	}
	arr := make([]kv, 0, len(orders))
	for k, o := range orders {
		arr = append(arr, kv{k, o, weight[k]})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].o > arr[j].o })
	if len(arr) > n {
		arr = arr[:n]
	}
	out := make([]CustomerValue, 0, len(arr))
	for _, e := range arr {
		out = append(out, CustomerValue{Name: e.name, Orders: e.o, Weight: round1(e.w)})
	}
	return out
}

func topProvinces(orders map[string]int) []ProvinceCount {
	arr := make([]ProvinceCount, 0, len(orders))
	for k, o := range orders {
		arr = append(arr, ProvinceCount{Name: k, Orders: o})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].Orders > arr[j].Orders })
	return arr
}
