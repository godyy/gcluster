package net

import (
	"net"
	"sync"

	"github.com/godyy/gutils/log"
)

// Dialer 负责连接地址为 addr 的服务.
type Dialer func(addr string) (net.Conn, error)

// CreateListener 负责创建地址为 addr 的网络监听器.
type CreateListener func(addr string) (net.Listener, error)

// Session 网络会话.
type Session interface {
	// RemoteNodeId 远端节点ID.
	RemoteNodeId() string

	// SendRaw 发送 Raw 数据包.
	SendRaw(p *RawPacket) error

	// close 关闭会话.
	close(err error)

	// isClosed 是否关闭.
	isClosed() bool
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

func (s *sessionMap) get(remoteNodeId string) Session {
	v, ok := s.Load(remoteNodeId)
	if !ok {
		return nil
	}
	return v.(Session)
}

func (s *sessionMap) add(session Session) {
	s.Store(session.RemoteNodeId(), session)
}

func (s *sessionMap) compareAndDelete(session Session) bool {
	return s.CompareAndDelete(session.RemoteNodeId(), session)
}

func (s *sessionMap) close(err error) {
	s.Range(func(key, value any) bool {
		value.(Session).close(err)
		s.CompareAndDelete(key, value)
		return true
	})
}

// SessionManager session 管理器.
type SessionManager interface {
	// Start 启动.
	Start() error

	// Close 关闭.
	Close() error

	// NodeId 节点ID.
	NodeId() string

	// Connect 连接指定节点.
	Connect(nodeId string, addr string) (Session, error)

	// GetSession 获取连接 nodeId 指向 Service 的 Session.
	GetSession(nodeId string) Session

	// getSessionConfig 返回 Session 配置.
	getSessionConfig() *SessionConfig

	// onSessionPacket session 数据包回调.
	onSessionPacket(session Session, p *RawPacket) error

	// onSessionClosed session 关闭回调.
	onSessionClosed(session Session, err error)
}

type sessionManager struct {
	dialer   Dialer      // 连接器，负责连接其它服务.
	sessions *sessionMap // 已联通的 session.
	logger   log.Logger  // 日志工具.

	mutex sync.RWMutex // RWMutex for following.
	state int32        // 状态
}

func newSessionManager(dialer Dialer, logger log.Logger) sessionManager {
	return sessionManager{
		dialer:   dialer,
		sessions: newSessionMap(),
		logger:   logger,
		state:    stateInit,
	}
}

func (sm *sessionManager) getState() int32 {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.state
}

func (sm *sessionManager) start(f func() error) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if sm.state >= stateStarted {
		return ErrStarted
	}

	if f != nil {
		if err := f(); err != nil {
			return err
		}
	}

	sm.state = stateStarted
	sm.logger.Info("started")
	return nil
}

func (sm *sessionManager) close(f func()) error {
	sm.mutex.Lock()
	if sm.state >= stateClosed {
		sm.mutex.Unlock()
		return ErrClosed
	}
	sm.state = stateClosed
	sm.mutex.Unlock()

	if f != nil {
		f()
	}
	sm.sessions.close(ErrClosed)

	sm.logger.Info("closed")
	return nil
}

func (sm *sessionManager) connect(nodeId, addr string, f func(nodeId, addr string) (Session, error)) (Session, error) {
	switch sm.getState() {
	case stateInit:
		return nil, ErrNotStarted
	case stateClosed:
		return nil, ErrClosed
	default:
		if session := sm.sessions.get(nodeId); session != nil && !session.isClosed() {
			return session, nil
		}
		if session, err := f(nodeId, addr); err == nil {
			return session, nil
		} else if session = sm.sessions.get(nodeId); session != nil && !session.isClosed() {
			return session, nil
		} else {
			return nil, err
		}
	}
}

func (sm *sessionManager) GetSession(nodeId string) Session {
	if sm.getState() == stateStarted {
		if session := sm.sessions.get(nodeId); session != nil && !session.isClosed() {
			return session
		}
	}
	return nil
}

// SessionHandler Session 事件处理器.
type SessionHandler interface {
	// OnSessionPacket 处理 Session 数据包.
	OnSessionPacket(Session, *RawPacket) error
}
