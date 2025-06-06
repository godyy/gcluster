package net

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/godyy/glog"
)

// Dialer 负责连接地址为 addr 的服务.
type Dialer func(addr string) (net.Conn, error)

// ListenerCreator 负责创建地址为 addr 的网络监听器.
type ListenerCreator func(addr string) (net.Listener, error)

// Session 网络会话.
type Session interface {
	// RemoteNodeId 远端节点ID.
	RemoteNodeId() string

	// SendRaw 发送 Raw 数据包.
	SendRaw(ctx context.Context, p *RawPacket) error

	// close 关闭会话.
	close(err error)

	// keepActive 保持活跃.
	keepActive() error
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

// SessionManager Session 管理器接口封装.
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
}

// sessionManagerImpl Session 管理器内部实现接口封装.
type sessionManagerImpl interface {
	SessionManager

	// base 返回内部 *sessionManager.
	base() *sessionManager

	// setLogger 设置日志工具.
	setLogger(logger glog.Logger)

	// getSessionConfig 返回 Session 配置.
	getSessionConfig() *SessionConfig

	// onSessionPacket Session 数据包回调.
	onSessionPacket(session Session, p *RawPacket) error

	// onSessionClosed Session 关闭回调.
	onSessionClosed(session Session, err error)

	// getRawPacket 分配长度为 size 的 RawPacket.
	getRawPacket(size int) *RawPacket

	// putRawPacket 回收 RawPacket.
	putRawPacket(p *RawPacket)
}

// sessionManager Session 管理器内部实现.
type sessionManager struct {
	dialer         Dialer         // 连接器，负责连接其它服务.
	sessionHandler SessionHandler // Session 事件处理器.
	pm             PacketManager  // 数据包管理器, 可选.
	rootLogger     glog.Logger    // 根日志工具, 所有其它日志工具均是从其复制而来.
	logger         glog.Logger    // 日志工具.

	mutex    sync.RWMutex // RWMutex for following.
	state    int8         // 状态
	sessions *sessionMap  // 已联通的 Session.
}

func newSessionManager(dialer Dialer, sessionHandler SessionHandler) sessionManager {
	return sessionManager{
		dialer:         dialer,
		sessions:       newSessionMap(),
		sessionHandler: sessionHandler,
		state:          stateInit,
	}
}

func (sm *sessionManager) base() *sessionManager {
	return sm
}

func (sm *sessionManager) initRootLogger() {
	if sm.rootLogger != nil {
		return
	}
	sm.rootLogger = createStdLogger(glog.DebugLevel)
}

func (sm *sessionManager) setRootLogger(logger glog.Logger) {
	sm.rootLogger = logger
}

func (sm *sessionManager) getState() int8 {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.state
}

// lockState 锁定状态.
func (sm *sessionManager) lockState(need int8, read bool) error {
	if read {
		sm.mutex.RLock()
	} else {
		sm.mutex.Lock()
	}

	state := sm.state
	if state == need {
		return nil
	}

	if read {
		sm.mutex.RUnlock()
	} else {
		sm.mutex.Unlock()
	}

	switch state {
	case stateInit:
		return ErrNotStarted
	case stateStarted:
		return ErrStarted
	case stateClosed:
		return ErrClosed
	default:
		panic(fmt.Sprintf("invalid session manager state %d", state))
	}
}

// unlockState 解锁状态.
func (sm *sessionManager) unlockState(read bool) {
	if read {
		sm.mutex.RUnlock()
	} else {
		sm.mutex.Unlock()
	}
}

// start 启动逻辑.
func (sm *sessionManager) start(f func() error) error {
	if err := sm.lockState(stateInit, false); err != nil {
		return err
	}
	defer sm.unlockState(false)

	if f != nil {
		if err := f(); err != nil {
			return err
		}
	}

	sm.state = stateStarted
	sm.logger.Info("started")
	return nil
}

// close 关闭逻辑.
func (sm *sessionManager) close(f func()) error {
	if err := sm.lockState(stateStarted, false); err != nil {
		return err
	}
	sm.state = stateClosed
	sm.unlockState(false)

	if f != nil {
		f()
	}
	sm.sessions.close(ErrClosed)

	sm.logger.Info("closed")
	return nil
}

// connect 连接逻辑.
func (sm *sessionManager) connect(nodeId, addr string, f func(nodeId, addr string) (Session, error)) (session Session, err error) {
	session = sm.GetSession(nodeId)
	if session != nil {
		return
	}

	if session, err = f(nodeId, addr); err == nil {
		return
	}

	session = sm.GetSession(nodeId)
	if session != nil {
		err = nil
	}

	return
}

// GetSession 获取连接 nodeId 指向 Service 的 Session.
func (sm *sessionManager) GetSession(nodeId string) Session {
	if err := sm.lockState(stateStarted, true); err != nil {
		return nil
	}
	defer sm.unlockState(true)

	if session := sm.sessions.get(nodeId); session != nil && session.keepActive() == nil {
		return session
	}

	return nil
}

// onSessionPacket 处理 Session 数据包.
func (sm *sessionManager) onSessionPacket(session Session, p *RawPacket) error {
	return sm.sessionHandler.OnSessionPacket(session, p)
}

// onSessionClosed 处理 Session 关闭.
func (sm *sessionManager) onSessionClosed(session Session, err error) {
	sm.sessions.compareAndDelete(session)
}

// getRawPacket 获取 RawPacket.
func (sm *sessionManager) getRawPacket(size int) *RawPacket {
	if sm.pm != nil {
		return sm.pm.GetRawPacket(size)
	}
	return NewRawPacketWithSize(size)
}

// putRawPacket 回收 RawPacket.
func (sm *sessionManager) putRawPacket(p *RawPacket) {
	if sm.pm != nil {
		sm.pm.PutRawPacket(p)
	}
}

// SessionHandler Session 事件处理器.
type SessionHandler interface {
	// OnSessionPacket 处理 Session 数据包.
	OnSessionPacket(Session, *RawPacket) error
}
