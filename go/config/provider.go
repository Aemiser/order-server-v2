// Package config 是 ports.ConfigProvider 的落地实现之一：基于内存的命名空间配置，
// 支持「模块自注册 + 热更新回调」，替掉未来主程序里写死的 cfgSections。
//
// 第三方想接数据库/文件/env，只需另建一个包实现 ports.ConfigProvider 并注入 compose 等消费方，
// 不碰本文件。主程序后续把 global 的配置快照接到这里（或自实现），即完成 Phase 3 接线。
package config

import (
	"context"
	"errors"
	"sync"

	"order_server/go/kernel"
)

// ErrNotFound 表示命名空间或键不存在。
var ErrNotFound = errors.New("config: 键不存在")

// Provider 是 ports.ConfigProvider 的内存实现。
type Provider struct {
	mu       sync.RWMutex
	data     map[string]map[string]string // namespace -> key -> value
	watchers map[string][]func()
	logger   kernel.Logger
}

// NewMemory 构造内存配置提供者。logger 为 nil 时退化为无副作用实现。
func NewMemory(logger kernel.Logger) *Provider {
	if logger == nil {
		logger = kernel.NoopLogger{}
	}
	return &Provider{
		data:     map[string]map[string]string{},
		watchers: map[string][]func(){},
		logger:   logger,
	}
}

// Get 按命名空间 + 键读取配置。
func (p *Provider) Get(_ context.Context, namespace, key string) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ns, ok := p.data[namespace]
	if !ok {
		return "", ErrNotFound
	}
	v, ok := ns[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set 写入配置，并通知该命名空间的监听者（热更新广播）。
func (p *Provider) Set(namespace, key, value string) {
	p.mu.Lock()
	ns := p.data[namespace]
	if ns == nil {
		ns = map[string]string{}
		p.data[namespace] = ns
	}
	ns[key] = value
	watchers := append([]func(){}, p.watchers[namespace]...)
	p.mu.Unlock()
	p.notify(watchers)
}

// Publish 用一批键值整体替换某个命名空间，并触发该命名空间的 Watch 回调。
// 供主程序把「一轮配置热更新」投影到 ConfigProvider：旧键会被清掉，
// 监听者拿到的始终是同一轮的一致快照，不会看到半新半旧的配置。
func (p *Provider) Publish(namespace string, values map[string]string) {
	next := make(map[string]string, len(values))
	for k, v := range values {
		next[k] = v
	}
	p.mu.Lock()
	p.data[namespace] = next
	watchers := append([]func(){}, p.watchers[namespace]...)
	p.mu.Unlock()
	p.notify(watchers)
}

// notify 逐个触发监听者；单个回调 panic 不影响其余，也不冒泡到配置装载流程。
func (p *Provider) notify(watchers []func()) {
	for _, w := range watchers {
		func(fn func()) {
			defer func() { _ = recover() }()
			fn()
		}(w)
	}
}

// Watch 注册命名空间级热更新回调。onChange 为空时忽略。
func (p *Provider) Watch(_ context.Context, namespace string, onChange func()) {
	if onChange == nil {
		return
	}
	p.mu.Lock()
	p.watchers[namespace] = append(p.watchers[namespace], onChange)
	p.mu.Unlock()
}
