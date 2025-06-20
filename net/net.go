package net

import (
	"context"
	"net"
	"sync"
)

// Dialer 负责连接地址为 addr 的服务.
type Dialer func(addr string) (net.Conn, error)

// ListenerCreator 负责创建地址为 addr 的网络监听器.
type ListenerCreator func(addr string) (net.Listener, error)

// Session 网络会话.
type Session interface {
	// RemoteNodeId 远端节点ID.
	RemoteNodeId() string

	// Send 发送字节数据.
	Send(ctx context.Context, b []byte) error
}

// sessionImpl Session 内部实现.
type sessionImpl interface {
	Session

	// start 启动会话.
	start() error

	// close 关闭会话.
	close(err error)

	// keepActive 保持活跃.
	keepActive() error

	// tick Tick 逻辑.
	tick()
}

// sessionMap sessionMap.
type sessionMap struct {
	*sync.Map
}

func newSessionMap() *sessionMap {
	return &sessionMap{
		Map: &sync.Map{},
	}
}

func (s *sessionMap) get(remoteNodeId string) sessionImpl {
	v, ok := s.Load(remoteNodeId)
	if !ok {
		return nil
	}
	return v.(sessionImpl)
}

func (s *sessionMap) add(session sessionImpl) {
	s.Store(session.RemoteNodeId(), session)
}

func (s *sessionMap) compareAndDelete(session sessionImpl) bool {
	return s.CompareAndDelete(session.RemoteNodeId(), session)
}

func (s *sessionMap) close(err error) {
	s.Range(func(key, value any) bool {
		value.(sessionImpl).close(err)
		s.CompareAndDelete(key, value)
		return true
	})
}

// SessionHandler Session 事件处理器.
type SessionHandler interface {
	// OnSessionBytes 处理 Session 字节数据.
	OnSessionBytes(s Session, b []byte) error
}

// BytesManager 字节缓冲区管理器.
type BytesManager interface {
	// GetBytes 获取指定大小的字节缓冲区.
	GetBytes(size int) []byte

	// PutBytes 回收字节缓冲区.
	PutBytes(b []byte)
}
