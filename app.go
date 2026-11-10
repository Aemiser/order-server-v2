package main

import (
	"context"
	"fmt"
	"order_server/go/core"
	"order_server/go/global"
	"order_server/go/kernel"
	"order_server/go/models/enum"
	"order_server/go/schemaservice"
	"order_server/go/server/auth"
	"order_server/go/server/checkout"
	dashboard "order_server/go/server/dashboard"
	alog "order_server/go/server/log"
	"order_server/go/server/order"
	"order_server/go/server/version"
	"order_server/go/server/wechatorder"
	ordertutil "order_server/go/utils/order"
	"order_server/go/wechatapi"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 模板工作台事件桥：schemaservice 的进度/结果事件统一经 Wails runtime 推给前端。
	schemaservice.SetEmitter(func(event string, data any) {
		runtime.EventsEmit(ctx, event, data)
	})
	// 版本检查放到登录成功之后，未登录时不弹任何业务弹窗
	// 启动后在后台检查微信密钥缓存：没有就自动获取并缓存（逻辑已内嵌进软件，无需外部脚本）。
	go a.startupEnsureWechatKey()
	// 监视「自动密钥目录」：外部取密钥工具的输出文件一落盘就自动导入缓存，
	// 无需打开配置页手动粘贴。
	go a.startKeyDropWatcher()
	// 启动即后台检查新版本，并按内嵌的间隔轮询：发现新版本自动异步下载，
	// 顶栏显示状态，下载完成后右下角轻量提醒重启，不阻塞主界面。
	go version.StartPeriodicCheck(ctx, version.CheckInterval)
	// 数据库 cfg_* 配置热更新：每 60 秒让数据库分别算一次五张表的 MD5，
	// 哪张表变了就只拉哪张表，其余复用已加载的行；在数据库里改了提示词 /
	// AI 模型 / 商品归属 / 通用设置时，已启动的窗口也会自动整体替换内存快照，
	// 并推事件通知界面，无需重启。
	core.StartCfgWatcher(ctx, core.DefaultCfgWatchInterval, func(ch core.CfgChange) {
		runtime.EventsEmit(ctx, "cfg-reloaded", ch)
	})
	// 免登录（Guest）模式下，主界面在启动后立即可用：此时就异步预热一次看板数据
	// （整个进程唯一一次全量加载，之后只走索引增量），用户之后切到「经营看板」秒开。
	// 登录模式下预热延后到登录成功之后；前端首页挂载还会再幂等唤醒一次。
	if auth.GuestMode() {
		go dashboard.EnsureRunning()
	}
}

// bringToFront 把已有主窗口恢复到前台（含从最小化恢复、置前、聚焦）。
// 由 Wails 的 OnSecondInstanceLaunch 在第一个实例内回调，第二个进程随后自行退出。
// 小写命名：避免被 Wails 反射绑定成前端可调用接口。
func (a *App) bringToFront() {
	if a.ctx == nil {
		return
	}
	logrus.Info("检测到重复启动，已激活当前窗口")
	runtime.WindowShow(a.ctx)
}

// startKeyDropWatcher 自动密钥目录的后台监视（不需要登录，只处理本机文件）。
func (a *App) startKeyDropWatcher() {
	defer kernel.RecoverLogrus(nil, "startKeyDropWatcher")
	if !wechatapi.Enabled {
		return
	}
	cfg := wechatorder.LoadConfig()
	wechatapi.StartKeyDropWatcher(cfg.DataDir, func(source string) {
		if a.ctx == nil {
			return
		}
		runtime.EventsEmit(a.ctx, "wechat-key-status", map[string]interface{}{
			"stage":   "ok",
			"level":   "ok",
			"source":  source,
			"message": "已自动导入密钥文件并缓存，后续导出与识别全程自动",
		})
	})
}

// startAutoRunner 已停用：本功能只做订单识别，结果进入数据预览区，
// 不在后台定时运行、不自动入库、不自动生成文件。
// 保留入口是为了让「登录成功 / 保存配置」两处调用点在旧配置文件下也确保调度器关闭。
func (a *App) startAutoRunner() {
	wechatorder.StopAutoRunner()
}

// startupEnsureWechatKey 启动后若本地无私钥缓存且配置允许，就在后台自动获取并缓存微信密钥，
// 这样用户点「自动获取今日订单」时无需每次手动粘贴密钥。微信未运行时静默跳过，不弹错误。
func (a *App) startupEnsureWechatKey() {
	defer kernel.RecoverLogrus(nil, "startupEnsureWechatKey")
	// 默认开源构建不含微信能力，别起后台任务发一串必然失败的状态事件。
	if !wechatapi.Enabled {
		return
	}
	cfg := wechatorder.LoadConfig()
	// 没有任何自动获取手段（也没手动密钥）时，无需启动后台任务
	if !cfg.AutoKey && cfg.KeyProviderPath == "" && strings.TrimSpace(cfg.DataKey) == "" {
		return
	}
	// 已有缓存则无需操作
	if v, ok := wechatapi.KeyCacheInfo()["exists"]; ok && v == true {
		return
	}
	// 仅当能探测到微信数据目录时才在后台尝试（微信未运行则静默跳过）
	if strings.TrimSpace(cfg.DataDir) == "" {
		if d := wechatapi.Detect(); d["success"] != true {
			return
		}
	}
	go func() {
		res, err := wechatapi.ResolveAndCacheKey(wechatapi.Options{
			DataDir:          cfg.DataDir,
			Platform:         cfg.Platform,
			Version:          cfg.Version,
			AutoKey:          cfg.AutoKey,
			KeyProviderPath:  cfg.KeyProviderPath,
			ProviderTimeout:  time.Duration(cfg.ProviderTimeout) * time.Second,
			AutoKeyOnFailure: cfg.AutoKeyOnFailure,
			DataKey:          cfg.DataKey,
		}, func(msg string) {
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "wechat-key-status", map[string]interface{}{
					"stage":   "acquiring",
					"message": msg,
				})
			}
		})
		if a.ctx == nil {
			return
		}
		if err != nil {
			runtime.EventsEmit(a.ctx, "wechat-key-status", map[string]interface{}{
				"stage":   "failed",
				"level":   "warn",
				"message": "启动自动获取微信密钥失败：" + err.Error(),
			})
			return
		}
		runtime.EventsEmit(a.ctx, "wechat-key-status", map[string]interface{}{
			"stage":   "ok",
			"level":   "ok",
			"source":  res.Source,
			"message": "已自动获取并缓存微信密钥（来源：" + res.Source + "），可直接点「自动获取今日订单」",
		})
	}()
}

// AutoFetchWechatKey 手动触发一次密钥自动获取（启动检查失败或想刷新密钥时使用）。
func (a *App) AutoFetchWechatKey() map[string]interface{} {
	if !a.requireAuth() {
		return map[string]interface{}{"success": false, "message": "会话已失效，请重新登录"}
	}
	cfg := wechatorder.LoadConfig()
	res, err := wechatapi.ResolveAndCacheKey(wechatapi.Options{
		DataDir:          cfg.DataDir,
		Platform:         cfg.Platform,
		Version:          cfg.Version,
		AutoKey:          cfg.AutoKey,
		KeyProviderPath:  cfg.KeyProviderPath,
		ProviderTimeout:  time.Duration(cfg.ProviderTimeout) * time.Second,
		AutoKeyOnFailure: cfg.AutoKeyOnFailure,
		DataKey:          cfg.DataKey,
	}, func(msg string) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "wechat-key-status", map[string]interface{}{
				"stage": "acquiring", "message": msg,
			})
		}
	})
	if err != nil {
		return map[string]interface{}{"success": false, "message": err.Error()}
	}
	return map[string]interface{}{
		"success": true,
		"source":  res.Source,
		"message": "密钥已获取并缓存（来源：" + res.Source + "）",
	}
}

// requireAuth 是所有业务接口的统一门禁：登录页只是第一层，
// 这里防止有人绕过界面直接调用 window.go.main.App.* 的绑定方法。
func (a *App) requireAuth() bool {
	if auth.IsLoggedIn() {
		return true
	}
	logrus.Warn("拒绝未登录的接口调用")
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "auth_required")
	}
	return false
}

// ---------------- 应用元信息 ----------------

// GetAppVersion 返回当前程序版本号，供登录页角标、侧栏版本标识等处展示。
// 版本号不是业务数据、登录页也需要读取，因此不走 requireAuth 门禁。
// 界面上所有版本号都由这一个入口供数，避免像登录页那样写死后与 global.Version 脱节。
func (a *App) GetAppVersion() string {
	return global.Version
}

// GetChangelog 返回编译进 exe 的全部版本更新日志，供前端「版本更新历史」展示。
// 数据源为内嵌的 CHANGELOG.md，离线可用、零网络请求；属于公开元数据，不要求登录。
func (a *App) GetChangelog() []version.ReleaseInfo {
	return version.GetChangelog()
}

// ---------------- 登录与账号 ----------------

// GetLoginRequired 返回「启动需要登录」开关的当前状态，供应用设置页展示。
func (a *App) GetLoginRequired() bool {
	return !auth.GuestMode()
}

// SetLoginRequired 切换「启动需要登录」开关：写回配置文件并立即作用于当前进程。
// 关闭后后端自动持有访客会话，前端刷新一次登录态即可直接进入主页；
// 开启后当前访客会话立即失效，刷新登录态会回到登录页。
func (a *App) SetLoginRequired(required bool) string {
	if err := core.SetAuthRequireLogin(required); err != nil {
		return "保存失败：" + err.Error()
	}
	auth.SetGuestMode(!required)
	if required {
		return "已开启启动登录，下次启动需输入账号密码；当前会话刷新后将回到登录页"
	}
	return "已关闭启动登录，下次启动将直接进入主页"
}

func (a *App) GetAuthState() auth.AuthState {
	return auth.GetAuthState()
}

func (a *App) Login(username, password string) auth.LoginResult {
	result := auth.Login(username, password)
	if result.OK {
		// 登录成功后确保后台定时调度处于关闭状态（本功能只做手动识别）
		go a.startAutoRunner()
		// 进入主页：异步预热看板数据（幂等；startup 已预热时这里不会重复查库）。
		go dashboard.EnsureRunning()
	}
	return result
}

func (a *App) Logout() {
	auth.Logout()
	wechatorder.StopAutoRunner()
}

func (a *App) ChangePassword(oldPassword, newPassword string) string {
	return auth.ChangePassword(oldPassword, newPassword)
}

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	if !a.requireAuth() {
		return ""
	}
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

// ---------------- 经营看板 ----------------

// GetDashboard 汇总经营看板所需的全部指标：总订单数 / 总销量(斤) / 复购率 / 客户数、
// 日周月趋势、爆品 Top、客户贡献 Top、省份订单分布。数据来源为已确认入库的订单。
// 销售额暂时以「总销量(斤)」口径呈现（订单表未存单价），后续可接单价配置升级为金额。
func (a *App) GetDashboard(req dashboard.DashboardRequest) dashboard.DashboardResponse {
	if !a.requireAuth() {
		return dashboard.DashboardResponse{}
	}
	return dashboard.GetDashboard(req)
}

// WarmupDashboard 首页挂载时调用：幂等地唤醒一次后台异步预热，立即返回。
// 这是「进入首页就加载」的前端触发点，点开「经营看板」时只读内存、不查库。
// 不返回任何业务数据，因此不加登录门禁；后端 startup / Login 已有同样的幂等唤醒。
func (a *App) WarmupDashboard() {
	dashboard.EnsureRunning()
}

// RefreshDashboardData 看板「刷新」按钮调用：只触发一次索引增量同步，绝不重扫全表。
func (a *App) RefreshDashboardData() {
	if !a.requireAuth() {
		return
	}
	dashboard.MarkDirty()
}

// 获取已配置的大模型
func (a *App) GetAIModel() map[string]int {
	if !a.requireAuth() {
		return nil
	}
	return global.GetAIMap()
}

// 订单识别
// 参数使用底层 int（内部再转回枚举）：Go 包名 enum 是 TS 保留字，若直接作为
// Wails 绑定参数类型，生成器会产出非法的 `import {enum} from '../models'`。
func (a *App) OrderRecognition(context string, aimodel int, model int) []ordertutil.ExceladdressInfo {
	if !a.requireAuth() {
		return nil
	}
	return order.OrderRec(context, enum.AIModelType(aimodel), enum.RecPreviewType(model))
}

func (a *App) StartStreamRecognition(inputText string, aimodel int, previewType int) {
	if !a.requireAuth() {
		return
	}
	order.StreamOrderRec(a.ctx, inputText, enum.AIModelType(aimodel), enum.RecPreviewType(previewType), func(data string) error {
		runtime.EventsEmit(a.ctx, "stream-order-data", data)
		return nil
	}, func() {
		runtime.EventsEmit(a.ctx, "stream-order-data", "DONE")
	})
}

//// 获取配置消息
//func (a *App) GetConfig() config.ReConf {
//	return config.GetConf()
//}
//
//// 修改配置消息
//func (a *App) SetConfig(conf config.ReConf) {
//	config.SetConf(conf)
//}

// 查询订单详情
func (a *App) OrderQuery(oq order.OrderQueryRequest) order.OrderQueryResponse {
	if !a.requireAuth() {
		return order.OrderQueryResponse{}
	}
	return order.OrderQuery(oq)
}

// 获取预览数据
func (a *App) GetpreviewData() []ordertutil.ExceladdressInfo {
	if !a.requireAuth() {
		return nil
	}
	return order.GetpreviewData()
}

// 修改预览数据
func (a *App) SetpreviewData(addressList ordertutil.ExceladdressInfo) string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	err := order.SetpreviewData(addressList)
	if err != nil {
		return err.Error()
	}
	return "修改成功"
}

// 删除预览数据
func (a *App) DeletepreviewData(id uint) string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	err := order.DeletepreviewData(id)
	if err == nil {
		return "删除成功"
	}
	return err.Error()
}

// 订单导入数据库
func (a *App) CreateOrder(date time.Time) string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	err := order.CreateOrder(date)
	if err == nil {
		// 新订单已入库：唤醒看板做一次自增主键水位增量同步（不重扫全表）。
		dashboard.MarkDirty()
		return "订单导入成功"
	}
	return err.Error()
}

// 导出预览数据xlsx
func (a *App) SaveToExcel() string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	// 选择文件夹
	folderPath, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "选择保存文件夹",
		DefaultDirectory:     "~",  // 默认打开用户目录
		CanCreateDirectories: true, // 允许用户创建新文件夹
	})
	if err != nil {
		logrus.Errorf("选择文件夹失败: %v", err)
		alog.Logaction(enum.LogErrLevel, "选择文件夹失败", err.Error())
	}
	global.SetSavePathExcel(folderPath)

	order.SaveToExcel()

	// 使用 BrowserOpenURL 打开文件夹
	// 在 Windows 上会打开资源管理器，macOS 上打开 Finder
	runtime.BrowserOpenURL(a.ctx, folderPath)

	return "导出成功"
}

// 获取账单的查询数据
func (a *App) GetChackDaydata(statime time.Time, endtime time.Time) checkout.CheckoutResponse {
	if !a.requireAuth() {
		return checkout.CheckoutResponse{}
	}
	return checkout.GetDaydata(statime, endtime)
}

// 修改商品归属
func (a *App) SetProductBelong(productBelongMap map[string][]string) {
	if !a.requireAuth() {
		return
	}
	checkout.SetFieldList(productBelongMap)
}

// 保存价格数据
func (a *App) SetPriceData(req []checkout.ProPriceRequest) checkout.CheckoutResponse {
	if !a.requireAuth() {
		return checkout.CheckoutResponse{}
	}
	return checkout.SetPrice(req)
}

// 获取账单的查询数据
func (a *App) GetDaydata(statime time.Time, endtime time.Time) checkout.CheckoutResponse {
	if !a.requireAuth() {
		return checkout.CheckoutResponse{}
	}
	return checkout.GetDaydata(statime, endtime)
}

func (a *App) SaveCheackToExcel() {
	if !a.requireAuth() {
		return
	}
	checkout.Checkout(a.ctx)
}

// DownloadUpdate / RestartApplication 与业务数据无关（只涉及本机可执行文件更新），
// 不要求登录即可调用，以支持「启动即后台下载、随时重启」。
func (a *App) DownloadUpdate() {
	version.DownloadUpdate(a.ctx)
}

func (a *App) RestartApplication() {
	version.RestartApplication(a.ctx)
}

// ---------------- 微信聊天记录自动获取今日订单 ----------------

// GetWechatOrderConfig 读取新增配置页里的配置
func (a *App) GetWechatOrderConfig() wechatorder.Config {
	if !a.requireAuth() {
		return wechatorder.Config{}
	}
	return wechatorder.LoadConfig()
}

// SaveWechatOrderConfig 保存新增配置页里的配置
func (a *App) SaveWechatOrderConfig(cfg wechatorder.Config) string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	if err := wechatorder.SaveConfig(cfg); err != nil {
		return "保存失败：" + err.Error()
	}
	// 数据目录变化后重启密钥监视；调度器保持关闭（本功能只做手动识别）
	go a.startAutoRunner()
	go a.startKeyDropWatcher()
	return "配置已保存；目标订单日期请在「一键识别订单」按钮旁按次选择，粗筛开关在下次识别时生效"
}

// TestWechatExport 只测试聊天记录导出，不调用大模型
func (a *App) TestWechatExport() map[string]interface{} {
	if !a.requireAuth() {
		return map[string]interface{}{}
	}
	return wechatorder.TestExport()
}

// DetectWechatData 探测运行中的微信客户端数据目录与版本，供配置页一键填入
func (a *App) DetectWechatData() map[string]interface{} {
	if !a.requireAuth() {
		return map[string]interface{}{}
	}
	return wechatapi.Detect()
}

// GetWechatKeyCacheInfo 返回密钥缓存状态（不含密钥明文），供配置页展示
func (a *App) GetWechatKeyCacheInfo() map[string]interface{} {
	if !a.requireAuth() {
		return map[string]interface{}{}
	}
	return wechatapi.KeyCacheInfo()
}

// ClearWechatKeyCache 清除本地密钥缓存，下次导出会重新走 keyProvider / 内存扫描
func (a *App) ClearWechatKeyCache() string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	if err := wechatapi.ClearKeyCache(); err != nil {
		return "清除失败：" + err.Error()
	}
	return "密钥缓存已清除，下次导出会重新获取密钥"
}

// FetchTodayOrders 兼容入口：不传日期，按配置默认（每次默认今天）识别，走增量模式。
func (a *App) FetchTodayOrders() wechatorder.AutoOrderResult {
	return a.FetchOrdersByDate("", true)
}

// FetchOrdersByDate 一键识别：按 targetDate 导出前 N 天群聊 → 可选粗筛 → AI 识别订单。
// targetDate 来自「一键识别订单」按钮旁的日期选择器，只作用于本轮，不写配置文件；
// 空串 / 非法格式回退到配置默认（跟随今天）。
// incremental=true（前端默认）走增量模式：只把「目标日期当天数据库里还没有」的订单放进数据预览区，
// 已存在的订单视为本轮已识别过、直接跳过，避免重复；incremental=false 为全量，每次都重新识别全部。
// 结果只回填数据预览区，不写数据库、不生成文件；可重复点击。
func (a *App) FetchOrdersByDate(targetDate string, incremental bool) wechatorder.AutoOrderResult {
	if !a.requireAuth() {
		return wechatorder.AutoOrderResult{}
	}
	return wechatorder.RunOnDate(targetDate, incremental, func(stage string, done, total int, message string) {
		if a.ctx == nil {
			return
		}
		runtime.EventsEmit(a.ctx, "wechat-order-progress", map[string]interface{}{
			"stage":   stage,
			"done":    done,
			"total":   total,
			"message": message,
		})
	})
}

// ---------------- 账单结算页 · 审核（按日期范围复核群聊订单） ----------------
//
// 与「一键识别订单」共用同一套导出 / 粗筛 / AI 识别引擎，但结果完全隔离：
// 只把「疑似漏单」和「存疑订单」放进账单页自己的人工审核区，
// 不覆盖数据预览区、不清空新建订单页的人工审查区、不写数据库。

// AuditCheckoutOrders 按账单页当前选择的日期范围导出群聊、识别订单并逐笔核对账单订单。
// 进度通过 checkout-audit-progress 事件推送（与新建订单页的 wechat-order-progress 分开，
// 两个页面同时打开时不会互相串进度）。
func (a *App) AuditCheckoutOrders(startDate string, endDate string) wechatorder.CheckoutAuditResult {
	if !a.requireAuth() {
		return wechatorder.CheckoutAuditResult{Message: "未登录，无法执行审核"}
	}
	return wechatorder.RunCheckoutAudit(startDate, endDate, func(stage string, done, total int, message string) {
		if a.ctx == nil {
			return
		}
		runtime.EventsEmit(a.ctx, "checkout-audit-progress", map[string]interface{}{
			"stage": stage, "done": done, "total": total, "message": message,
		})
	})
}

// GetCheckoutAuditReviews 返回账单页人工审核区里仍待处理的条目。
// 状态存在 Go 侧，用户切到别的页再回来也能看到上一轮审核结果。
func (a *App) GetCheckoutAuditReviews() []wechatorder.CheckoutAuditItem {
	if !a.requireAuth() {
		return nil
	}
	return wechatorder.CheckoutAuditReviews()
}

// GetCheckoutAuditSummary 返回最近一轮审核的汇总数据。
func (a *App) GetCheckoutAuditSummary() wechatorder.CheckoutAuditResult {
	if !a.requireAuth() {
		return wechatorder.CheckoutAuditResult{}
	}
	return wechatorder.CheckoutAuditSummary()
}

// PromoteCheckoutAuditReviews 把人工确认的漏单追加到「数据预览区」。
// 与新建订单页一致：只进预览区、不入库；前端拿返回的最新列表刷新预览表。
func (a *App) PromoteCheckoutAuditReviews(ids []string) []ordertutil.ExceladdressInfo {
	if !a.requireAuth() {
		return nil
	}
	return wechatorder.PromoteCheckoutAuditReviews(ids)
}

// DiscardCheckoutAuditReviews 忽略指定条目（ids 为空 = 全部），返回剩余待审数据。
func (a *App) DiscardCheckoutAuditReviews(ids []string) []wechatorder.CheckoutAuditItem {
	if !a.requireAuth() {
		return nil
	}
	return wechatorder.DiscardCheckoutAuditReviews(ids)
}

// ---------------- 待人工审查数据（不确认订单） ----------------

// GetWechatReviewOrders 返回当前仍待人工审查的订单数据。
func (a *App) GetWechatReviewOrders() []wechatorder.ReviewOrderItem {
	if !a.requireAuth() {
		return nil
	}
	return wechatorder.ReviewOrders()
}

// PromoteWechatReviewOrders 把人工确认过的待审查订单加入数据预览区。
// 只追加到数据预览区（global.AddressList），不写数据库、不生成任何文件；
// ids 为空表示「全部加入」，重复点击同一条不会重复追加。
func (a *App) PromoteWechatReviewOrders(ids []string) []ordertutil.ExceladdressInfo {
	if !a.requireAuth() {
		return nil
	}
	return wechatorder.PromoteReviewOrders(ids)
}

// DiscardWechatReviewOrders 忽略指定待审查订单，返回剩余待审查数据。
func (a *App) DiscardWechatReviewOrders(ids []string) []wechatorder.ReviewOrderItem {
	if !a.requireAuth() {
		return nil
	}
	return wechatorder.DiscardReviewOrders(ids)
}

// ---------------- 识别状态查询与兼容控制入口 ----------------

// GetWechatAutoStatus 返回自动运行开关、间隔、上次/下次运行与累计数据。
func (a *App) GetWechatAutoStatus() map[string]interface{} {
	if !a.requireAuth() {
		return map[string]interface{}{}
	}
	return wechatorder.AutoStatus()
}

// GetWechatKeyDropStatus 返回「自动密钥目录」路径与已放入的文件。
func (a *App) GetWechatKeyDropStatus() map[string]interface{} {
	if !a.requireAuth() {
		return map[string]interface{}{}
	}
	return wechatapi.KeyDropStatus()
}

// OpenWechatKeyDropDir 在资源管理器里打开自动密钥目录，方便放入取密钥工具的输出。
func (a *App) OpenWechatKeyDropDir() string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	if !wechatapi.Enabled {
		return wechatapi.ErrDisabled.Error()
	}
	dir := wechatapi.KeyDropDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "创建目录失败：" + err.Error()
	}
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, dir)
	}
	return dir
}

// ResetWechatAutoState 清空增量水位线与订单去重指纹，下次运行会全量重新识别。
func (a *App) ResetWechatAutoState() string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	if err := wechatorder.ResetAutoState(); err != nil {
		return "重置失败：" + err.Error()
	}
	return "已重置增量水位线，下次运行将全量识别"
}

// RunWechatFullScan 兼容入口：与「一键识别订单」完全一致，纯识别、只回填预览区。
func (a *App) RunWechatFullScan() wechatorder.AutoOrderResult {
	if !a.requireAuth() {
		return wechatorder.AutoOrderResult{}
	}
	return wechatorder.RunFull(func(stage string, done, total int, message string) {
		if a.ctx == nil {
			return
		}
		runtime.EventsEmit(a.ctx, "wechat-order-progress", map[string]interface{}{
			"stage": stage, "done": done, "total": total, "message": message,
		})
	})
}

// ---------------- 数据库配置热更新 ----------------

// GetCfgSyncStatus 返回配置定时同步的运行状态，供应用设置页展示。
func (a *App) GetCfgSyncStatus() core.CfgWatchStatus {
	if !a.requireAuth() {
		return core.CfgWatchStatus{}
	}
	return core.GetCfgWatchStatus()
}

// RefreshDBCfg 立即拉取一次数据库配置（应用设置页「立即同步」按钮）。
// 不等 60 秒定时器：改完提示词想马上验证时点一下即可。
func (a *App) RefreshDBCfg() core.CfgRefreshResult {
	res := core.CfgRefreshResult{}
	if !a.requireAuth() {
		res.Error = "会话已失效，请重新登录"
		res.Status = core.GetCfgWatchStatus()
		return res
	}
	ch, err := core.RefreshDBCfgNow("manual")
	res.Changed, res.Summary = ch.Changed, ch.Summary
	if err != nil {
		res.Error = err.Error()
		logrus.Warnf("手动同步数据库配置失败: %v", err)
	}
	// 手动同步由按钮自己展示结果，不再重复推 cfg-reloaded 事件。
	res.Status = core.GetCfgWatchStatus()
	return res
}

// ---------------- 模板工作台（新架构纵切片：orderschema + compose） ----------------
// 全部走 go/schemaservice，只读写新表（order_templates/orders_v2），
// 不碰 wechatorder/address_infos 旧链路数据。

func (a *App) schemaSvc() *schemaservice.Service {
	if !a.requireAuth() {
		return nil
	}
	svc, err := schemaservice.Get()
	if err != nil {
		logrus.Warnf("模板工作台不可用: %v", err)
		return nil
	}
	return svc
}

// SchemaListTemplates 列模板；onlyPublished=true 只看已发布（用于路由预览）。
func (a *App) SchemaListTemplates(onlyPublished bool) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	list, err := svc.ListTemplates(onlyPublished)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"templates": list}
}

// SchemaSaveTemplate 新建模板或另存新版本（draft 状态）。
func (a *App) SchemaSaveTemplate(in schemaservice.SaveTemplateInput) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	tpl, err := svc.SaveTemplate(in)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"template": tpl}
}

// SchemaPublishTemplate 发布模板最新版（识别路由只认已发布模板）。
func (a *App) SchemaPublishTemplate(id string) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	tpl, err := svc.PublishTemplate(id)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"template": tpl}
}

// SchemaDeprecateTemplate 停用模板（保留历史版本，路由不再命中）。
func (a *App) SchemaDeprecateTemplate(id string) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	tpl, err := svc.DeprecateTemplate(id)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"template": tpl}
}

// SchemaDeleteTemplate 物理删除模板全部版本（需前端确认后调用）。
func (a *App) SchemaDeleteTemplate(id string) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	if err := svc.DeleteTemplate(id); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true}
}

// SchemaInferTemplate 从样例消息归纳模板草案（不落库，前端确认后再保存）。
func (a *App) SchemaInferTemplate(in schemaservice.InferInput) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	res, err := svc.InferTemplate(a.ctx, in)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"template": res.Template, "report": res.Report}
}

// SchemaRunRecognition 异步启动一次识别运行（导出→路由→抽取→入库 orders_v2）。
// 进度走 schema-progress 事件，结果走 schema-result 事件 + SchemaGetLastRun。
func (a *App) SchemaRunRecognition(opt schemaservice.RunOptions) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	if err := svc.StartRun(opt); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true}
}

// SchemaGetRunStatus 当前运行状态与最近一次结果。
func (a *App) SchemaGetRunStatus() map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	return map[string]interface{}{
		"running": svc.IsRunning(),
		"lastRun": svc.GetLastRun(),
	}
}

// SchemaCancelRun 取消当前识别任务（关窗口也会由生命周期自动取消）。
func (a *App) SchemaCancelRun() map[string]interface{} {
	if a.schemaSvc() == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	return map[string]interface{}{"cancelled": schemaservice.CancelRun()}
}

// SchemaListSources 返回已注册的聊天来源，供模板工作台下拉选择。
func (a *App) SchemaListSources() map[string]interface{} {
	if a.schemaSvc() == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	return map[string]interface{}{"sources": schemaservice.Sources()}
}

// SchemaListOrders 查询新链路订单（orders_v2）。
func (a *App) SchemaListOrders(opt schemaservice.OrderQueryOptions) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	list, err := svc.ListOrders(opt)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"orders": list}
}

// SchemaSetOrderStatus 更新订单状态（确认 / 丢弃 / 恢复待审）。
func (a *App) SchemaSetOrderStatus(id uint64, status string) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	o, err := svc.SetOrderStatus(id, status)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"order": o}
}

// SchemaDeleteOrder 物理删除一笔订单（误识别清理）。
func (a *App) SchemaDeleteOrder(id uint64) map[string]interface{} {
	svc := a.schemaSvc()
	if svc == nil {
		return map[string]interface{}{"error": "模板服务未初始化"}
	}
	if err := svc.DeleteOrder(id); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true}
}
