package schemaservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"order_server/go/ports"
	csvsource "order_server/go/sources/csv"
)

// ---------------------------------------------------------------------------
// 聊天来源注册表
//
// 与 config.Section 同一范式：内置来源在 init 自注册，第三方加来源只需
// 在自己的 init 里 RegisterSource，一行接入，不必改 schemaservice 任何文件。
// ---------------------------------------------------------------------------

// SourceFactory 按一次运行的参数构造聊天来源。
type SourceFactory func(opt RunOptions) (ports.ChatSource, error)

var (
	sourceMu        sync.RWMutex
	sourceFactories = map[string]SourceFactory{}
)

// RegisterSource 注册一个来源；名字大小写不敏感，重复注册会报错（避免静默覆盖）。
func RegisterSource(name string, f SourceFactory) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return errors.New("数据源名称不能为空")
	}
	if f == nil {
		return fmt.Errorf("数据源 %q 的工厂函数不能为空", name)
	}
	sourceMu.Lock()
	defer sourceMu.Unlock()
	if _, exists := sourceFactories[name]; exists {
		return fmt.Errorf("数据源 %q 已注册", name)
	}
	sourceFactories[name] = f
	return nil
}

// MustRegisterSource 供 init 使用：注册失败直接 panic，让配置错误在启动期暴露。
func MustRegisterSource(name string, f SourceFactory) {
	if err := RegisterSource(name, f); err != nil {
		panic(fmt.Sprintf("schemaservice: %v", err))
	}
}

// Sources 返回已注册来源名（稳定排序），供前端下拉与错误提示使用。
func Sources() []string {
	sourceMu.RLock()
	defer sourceMu.RUnlock()
	out := make([]string, 0, len(sourceFactories))
	for name := range sourceFactories {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// sourceName 空值默认 wechat，保持旧调用（前端未带 source 字段）的行为不变。
func sourceName(opt RunOptions) string {
	name := strings.ToLower(strings.TrimSpace(opt.Source))
	if name == "" {
		return "wechat"
	}
	return name
}

func init() {
	MustRegisterSource("wechat", func(RunOptions) (ports.ChatSource, error) {
		return newWechatSource()
	})
	MustRegisterSource("file", newFileSource)
}

// newFileSource 校验 CSV / TSV / JSONL 文件路径，再交给 csvsource 适配器。
// 文件源忽略日期窗口：文件由调用方自行准备，导出的是文件内全部消息。
func newFileSource(opt RunOptions) (ports.ChatSource, error) {
	path := strings.TrimSpace(opt.DataDir)
	if path == "" {
		return nil, errors.New("文件数据源需要填写文件路径（CSV / TSV / JSONL）")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv", ".jsonl", ".json":
	default:
		return nil, fmt.Errorf("不支持的文件类型 %q（仅支持 .csv / .tsv / .jsonl / .json）", filepath.Ext(path))
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("访问文件失败: %w", err)
	}
	if st.IsDir() {
		return nil, errors.New("数据源路径必须指向文件，不能是目录")
	}
	return csvsource.New("file"), nil
}

// resolveSource 优先用测试/宿主注入的 sourceFactory，否则走注册表。
func (s *Service) resolveSource(opt RunOptions) (ports.ChatSource, error) {
	if s.sourceFactory != nil {
		return s.sourceFactory(opt)
	}
	name := sourceName(opt)
	sourceMu.RLock()
	factory := sourceFactories[name]
	sourceMu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("未知数据源 %q（已注册：%s）", name, strings.Join(Sources(), ", "))
	}
	return factory(opt)
}
