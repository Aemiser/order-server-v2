package config

// 配置节自注册：把「有哪些 cfg_* 表参与变更检测」从 core 的硬编码清单里解放出来。
//
// 第三方模块只需在自己的 init() 里调用 Register 注册一节（表名 + SQL 别名 +
// 参与 MD5 的业务列），就能接入逐表 MD5 快比对、增量拉取与热更新广播，
// 不需要修改 core 的轮询 / 基线 / 比对逻辑，也不需要在 SQL 里手写列名。
//
// 注意：这里只描述「变更检测」元数据，不承载任何业务加载逻辑，包本身零业务依赖。

import (
	"fmt"
	"strings"
	"sync"
)

// Section 描述一个配置节的变更检测元数据。
// Name 同时作为 ConfigProvider 的命名空间；Columns 必须包含 id 且顺序稳定，
// 因为逐行 MD5 的 CONCAT_WS 列顺序直接决定哈希值。
type Section struct {
	Name    string
	Alias   string
	Columns []string
}

var (
	sectionMu    sync.RWMutex
	sectionList  []Section
	sectionIndex = map[string]int{}
	aliasIndex   = map[string]int{}
)

// validIdent 拒绝任何可能拼进 SQL 的非法标识符，防止注册项变成注入点。
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

// Register 注册一个配置节。注册顺序即 SQL 列顺序、逐表基线顺序与变更摘要顺序。
// 重名 / 别名冲突 / 非法标识符一律报错，避免「加了一节但静默漏检」。
func Register(s Section) error {
	if !validIdent(s.Name) || !validIdent(s.Alias) {
		return fmt.Errorf("config: 配置节名或别名非法: %q / %q", s.Name, s.Alias)
	}
	if len(s.Columns) == 0 {
		return fmt.Errorf("config: 配置节 %s 缺少 MD5 列清单", s.Name)
	}
	if s.Columns[0] != "id" {
		return fmt.Errorf("config: 配置节 %s 的列清单必须以 id 开头", s.Name)
	}
	for _, c := range s.Columns {
		if !validIdent(c) {
			return fmt.Errorf("config: 配置节 %s 含非法列名 %q", s.Name, c)
		}
	}

	sectionMu.Lock()
	defer sectionMu.Unlock()
	if _, ok := sectionIndex[s.Name]; ok {
		return fmt.Errorf("config: 配置节 %s 已注册", s.Name)
	}
	if _, ok := aliasIndex[s.Alias]; ok {
		return fmt.Errorf("config: SQL 别名 %s 已被占用", s.Alias)
	}
	cp := Section{Name: s.Name, Alias: s.Alias, Columns: append([]string(nil), s.Columns...)}
	sectionList = append(sectionList, cp)
	sectionIndex[cp.Name] = len(sectionList) - 1
	aliasIndex[cp.Alias] = len(sectionList) - 1
	return nil
}

// MustRegister 与 Register 相同，但注册失败直接 panic。
// 用于内置节：清单写错属于编译期就该暴露的程序员错误，不该静默降级。
func MustRegister(s Section) {
	if err := Register(s); err != nil {
		panic(err)
	}
}

// Sections 返回当前已注册配置节的有序副本（调用方修改不影响注册表）。
func Sections() []Section {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	out := make([]Section, len(sectionList))
	for i, s := range sectionList {
		out[i] = Section{Name: s.Name, Alias: s.Alias, Columns: append([]string(nil), s.Columns...)}
	}
	return out
}

// Names 返回有序的配置节名（表名）列表。
func Names() []string {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	out := make([]string, len(sectionList))
	for i, s := range sectionList {
		out[i] = s.Name
	}
	return out
}

// SectionByAlias 把 SQL 结果列别名还原成配置节，供动态结果集扫描使用。
func SectionByAlias(alias string) (Section, bool) {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	i, ok := aliasIndex[alias]
	if !ok {
		return Section{}, false
	}
	s := sectionList[i]
	return Section{Name: s.Name, Alias: s.Alias, Columns: append([]string(nil), s.Columns...)}, true
}

// MD5Expr 生成单个配置节的逐表 MD5 子查询：
// 逐行 MD5（覆盖全部业务列，含 enabled 与 id）→ 按 id 排序拼接 → 整体 MD5。
// deleted_at IS NULL 与 GORM Find 的默认过滤保持一致。
func MD5Expr(s Section) string {
	return "MD5((SELECT IFNULL(GROUP_CONCAT(t.h ORDER BY t.id SEPARATOR ''), '') FROM " +
		"(SELECT id, MD5(CONCAT_WS(CHAR(31), " + strings.Join(s.Columns, ", ") + ")) AS h " +
		"FROM " + s.Name + " WHERE deleted_at IS NULL) t)) AS " + s.Alias
}

// RevisionSQL 生成完整的「一次 RTT 拿到库里存的整体 MD5 + 实算整体 MD5 +
// 每节逐表 MD5」查询。别名避开 MySQL 保留字 STORED；cfg_revision 尚未建表时
// 子查询会报错，调用方按既有约定退回全量内容指纹比对。
func RevisionSQL() string {
	ss := Sections()
	if len(ss) == 0 {
		// 没有任何配置节时不拼出语法错误的 SQL；返回空值让调用方走降级路径。
		return "SELECT '' AS stored_md5, '' AS actual_md5"
	}
	exprs := make([]string, 0, len(ss)+1)
	aliases := make([]string, 0, len(ss))
	exprs = append(exprs, " IFNULL((SELECT cfg_md5 FROM cfg_revision WHERE id = 1), '') AS stored_md5")
	for _, s := range ss {
		exprs = append(exprs, " "+MD5Expr(s))
		aliases = append(aliases, s.Alias)
	}
	inner := "SELECT\n" + strings.Join(exprs, ",\n")
	list := strings.Join(aliases, ", ")
	return "SELECT\n stored_md5,\n MD5(CONCAT_WS('|', " + list + ")) AS actual_md5,\n " + list +
		"\n FROM (" + inner + ") x"
}
