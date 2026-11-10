package kernel

import "sync"

// EventSink 是事件 / 进度广播端口。前端进度、AI 流式输出、流水线阶段完成等都走这个接口，
// 从而把「产生事件的业务」与「Wails EventsEmit 的具体实现」解耦——
// 业务模块不 import Wails runtime，也让 CLI / 测试能注入自己的实现。
type EventSink interface {
	// Emit 广播一个事件。payload 通常是可 JSON 序列化的结构。
	Emit(event string, payload interface{})
	// On 注册一个事件处理器（注册一般发生在启动期，非并发安全；广播线程安全）。
	On(event string, handler func(payload interface{}))
}

// InProcEventBus 是 EventSink 的进程内参考实现。
type InProcEventBus struct {
	mu       sync.RWMutex
	handlers map[string][]func(interface{})
}

// NewEventBus 构造一个进程内事件总线。
func NewEventBus() *InProcEventBus {
	return &InProcEventBus{handlers: make(map[string][]func(interface{}))}
}

// Emit 线程安全地广播事件给所有订阅者。
func (b *InProcEventBus) Emit(event string, payload interface{}) {
	b.mu.RLock()
	subs := b.handlers[event]
	b.mu.RUnlock()
	for _, h := range subs {
		h(payload)
	}
}

// On 注册事件处理器（非并发安全，应在启动期完成注册）。
func (b *InProcEventBus) On(event string, handler func(interface{})) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[event] = append(b.handlers[event], handler)
}
