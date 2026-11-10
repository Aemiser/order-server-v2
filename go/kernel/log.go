// Package kernel 是后端的可复用基础设施层，零业务依赖、可被任意模块独立引用与替换。
// 它对应重构规划里的「日志 / 异步 / 生命周期 / 事件」四个基础模块，是所有上层业务模块的底座。
//
// 设计原则（开源友好）：
//   - 业务模块只依赖本包导出的接口（Logger / EventSink / Lifecycle / WorkerPool），
//     不依赖具体实现，便于替换为第三方实现或在测试中注入无副作用实现。
//   - 实现与端口分离：logrus 适配器只是参考实现；未来换 zap/slog 不波及业务代码。
package kernel

import (
	"fmt"
	"runtime/debug"

	"github.com/sirupsen/logrus"
)

// Field 是一条结构化日志字段（键值对）。
// 用显式结构而非 map，避免 logrus.WithField 因自定义 Formatter 吞掉 entry.Data 的常见坑。
type Field struct {
	Key   string
	Value string
}

// F 构造一个字段，值会被格式化为字符串（nil 也会安全输出）。
func F(key string, value interface{}) Field {
	return Field{Key: key, Value: fmt.Sprintf("%v", value)}
}

// Logger 是结构化日志端口。业务模块统一依赖它，不直接 import logrus。
// 这样既能在测试中注入无副作用实现，也能整体替换日志后端。
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, err error, fields ...Field)
	// With 返回带固定字段的子 logger（例如 With(F("order_id", id))）。
	With(fields ...Field) Logger
}

// LogrusLogger 是 Logger 的 logrus 参考实现。
type LogrusLogger struct {
	entry *logrus.Entry
}

// NewLogrusLogger 用给定的 logrus 实例构造适配器（通常传入 core.InitLogrus 返回的 logger）。
func NewLogrusLogger(base *logrus.Logger) *LogrusLogger {
	if base == nil {
		base = logrus.StandardLogger()
	}
	return &LogrusLogger{entry: logrus.NewEntry(base)}
}

func (l *LogrusLogger) log(level logrus.Level, msg string, err error, fields []Field) {
	e := l.entry
	if len(fields) > 0 {
		kv := make(logrus.Fields, len(fields))
		for _, f := range fields {
			kv[f.Key] = f.Value
		}
		e = e.WithFields(kv)
	}
	if err != nil {
		e = e.WithError(err)
	}
	e.Log(level, msg)
}

func (l *LogrusLogger) Debug(msg string, fields ...Field) { l.log(logrus.DebugLevel, msg, nil, fields) }
func (l *LogrusLogger) Info(msg string, fields ...Field)  { l.log(logrus.InfoLevel, msg, nil, fields) }
func (l *LogrusLogger) Warn(msg string, fields ...Field)  { l.log(logrus.WarnLevel, msg, nil, fields) }
func (l *LogrusLogger) Error(msg string, err error, fields ...Field) {
	l.log(logrus.ErrorLevel, msg, err, fields)
}

func (l *LogrusLogger) With(fields ...Field) Logger {
	kv := make(logrus.Fields, len(fields))
	for _, f := range fields {
		kv[f.Key] = f.Value
	}
	return &LogrusLogger{entry: l.entry.WithFields(kv)}
}

// NoopLogger 用于测试或不需要日志的上下文，所有方法为空操作。
type NoopLogger struct{}

func (NoopLogger) Debug(string, ...Field)        {}
func (NoopLogger) Info(string, ...Field)         {}
func (NoopLogger) Warn(string, ...Field)         {}
func (NoopLogger) Error(string, error, ...Field) {}
func (NoopLogger) With(...Field) Logger          { return NoopLogger{} }

// Recover 必须在 defer 中调用，用于兜住 goroutine 内的 panic 并至少记录堆栈，
// 避免「静默吞掉、进程崩了还无日志」的情况。
//
// 用法：
//
//	defer kernel.Recover(logger, "startKeyDropWatcher")
//
// logger 为 nil 时退化为 logrus.StandardLogger()，保证任何位置都能用。
func Recover(logger Logger, op string) {
	if r := recover(); r != nil {
		if logger == nil {
			logger = NewLogrusLogger(nil)
		}
		logger.Error("goroutine panic recovered", fmt.Errorf("%v", r),
			F("op", op), F("stack", string(debug.Stack())))
	}
}

// RecoverLogrus 是 Recover 的便捷版：直接吃一个 *logrus.Logger（可为 nil）。
func RecoverLogrus(l *logrus.Logger, op string) {
	if r := recover(); r != nil {
		if l == nil {
			l = logrus.StandardLogger()
		}
		l.WithError(fmt.Errorf("%v", r)).
			WithField("op", op).
			Errorf("goroutine panic recovered\n%s", string(debug.Stack()))
	}
}
