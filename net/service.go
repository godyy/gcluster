package net

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/godyy/glog"
	pkgerrors "github.com/pkg/errors"
)

// ServiceConfig Service 配置.
type ServiceConfig struct {
	// NodeId 指定 Service 的节点ID.
	NodeId string

	// Addr 指定 Service 的服务地址.
	Addr string

	// Handshake 握手配置.
	Handshake HandshakeConfig

	// Session 指定 Service 之间连接建立成功后，构造 Session 所使用的相关参数.
	Session SessionConfig

	// Dialer 网络拨号器.
	Dialer Dialer

	// ListenerCreator 网络监听器构造器.
	ListenerCreator ListenerCreator

	// TimerSystem 定时器系统.
	TimerSystem TimerSystem

	// ExpectedConcurrentSessions 预期同时存在的 Session 数量.
	// 默认值为 10.
	ExpectedConcurrentSessions int
}

func (c *ServiceConfig) init() error {
	if c == nil {
		return errors.New("service config nil")
	}

	if c.NodeId == "" {
		return errors.New("ServiceConfig.NodeId not specified")
	}

	if c.Addr == "" {
		return errors.New("ServiceConfig.Addr not specified")
	}

	if err := c.Handshake.init(); err != nil {
		return err
	}

	if err := c.Session.init(); err != nil {
		return err
	}

	if c.Dialer == nil {
		return errors.New("ServiceConfig.Dialer not specified")
	}

	if c.ListenerCreator == nil {
		return errors.New("ServiceConfig.ListenerCreator not specified")
	}

	if c.TimerSystem == nil {
		return errors.New("ServiceConfig.TimerSystem not specified")
	}

	if c.ExpectedConcurrentSessions <= 0 {
		c.ExpectedConcurrentSessions = 10
	}

	return nil
}

// Service 用于启动本地网络服务. 服务与服务之间可通过维护 Session 进行通信.
// Session 可由本地发起，也可由远端发起. 若两端同时发起 Session, 最终只会保
// 留最先建立的 Session 进行通信.
type Service struct {
	cfg            *ServiceConfig // 服务配置.
	sessionHandler SessionHandler // Session 事件处理器.
	pm             PacketManager  // 数据包管理器, 可选.
	listener       net.Listener   // 网络监听器.
	rootLogger     glog.Logger    // 根日志工具, 所有其它日志工具均是从其复制而来.
	logger         glog.Logger    // 日志工具.

	mutex          sync.RWMutex  // RWMutex for following.
	state          int8          // 状态
	sessions       *sessionMap   // 已联通的 Session.
	establishments *sync.Map     // 正在建立的 Session.
	cTickSessions  chan string   // 需要 Tick 的 Session.
	cClosed        chan struct{} // 已关闭信号.
}

func CreateService(cfg *ServiceConfig, sessionHandler SessionHandler, options ...ServiceOption) (*Service, error) {
	if err := cfg.init(); err != nil {
		return nil, err
	}

	if sessionHandler == nil {
		return nil, errors.New("sessionHandler nil")
	}

	s := &Service{
		cfg:            cfg,
		sessionHandler: sessionHandler,
		state:          stateInit,
		sessions:       newSessionMap(),
		establishments: &sync.Map{},
		cTickSessions:  make(chan string, cfg.ExpectedConcurrentSessions),
		cClosed:        make(chan struct{}),
	}

	// 本地会话
	s.sessions.add(newSessionLocal(s))

	// 选项
	for _, opt := range options {
		opt(s)
	}

	// 初始化日志工具.
	s.initLogger()

	return s, nil
}

// NodeId Service 的节点ID.
func (s *Service) NodeId() string {
	return s.cfg.NodeId
}

// Addr Service 网络地址.
func (s *Service) Addr() string {
	return s.cfg.Addr
}

// config 返回 Service 配置.

func (s *Service) config() *ServiceConfig {
	return s.cfg
}

// getSessionConfig 返回 Session 配置.
func (s *Service) getSessionConfig() *SessionConfig {
	return &s.cfg.Session
}

// getState 返回 Service 状态.
func (s *Service) getState() int8 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.state
}

// isClosed 返回 Service 是否关闭.
func (s *Service) isClosed() bool {
	return s.getState() >= stateClosed
}

// initLogger 初始化日志工具.
func (s *Service) initLogger() {
	s.initRootLogger()

	if s.logger != nil {
		return
	}

	s.logger = s.rootLogger.Named("Service").WithFields(lfdNodeId(s.cfg.NodeId))
}

// setLogger 设置日志工具.
func (s *Service) setLogger(logger glog.Logger) {
	s.setRootLogger(logger)
	s.logger = s.rootLogger.Named("Service").WithFields(lfdNodeId(s.cfg.NodeId))
}

// initRootLogger 初始化根日志工具.
func (s *Service) initRootLogger() {
	if s.rootLogger != nil {
		return
	}
	s.rootLogger = createStdLogger(glog.DebugLevel)
}

// setRootLogger 设置根日志工具.
func (s *Service) setRootLogger(logger glog.Logger) {
	s.rootLogger = logger
}

// lockState 锁定状态.
func (s *Service) lockState(need int8, read bool) error {
	if read {
		s.mutex.RLock()
	} else {
		s.mutex.Lock()
	}

	state := s.state
	if state == need {
		return nil
	}

	if read {
		s.mutex.RUnlock()
	} else {
		s.mutex.Unlock()
	}

	switch state {
	case stateInit:
		return ErrServiceNotStarted
	case stateStarted:
		return ErrServiceStarted
	case stateClosed:
		return ErrServiceClosed
	default:
		panic(fmt.Sprintf("invalid session manager state %d", state))
	}
}

// unlockState 解锁状态.
func (s *Service) unlockState(read bool) {
	if read {
		s.mutex.RUnlock()
	} else {
		s.mutex.Unlock()
	}
}

// Start 启动 Service.
func (s *Service) Start() error {
	if err := s.lockState(stateInit, false); err != nil {
		return err
	}
	defer s.unlockState(false)

	// 创建网络监听器.
	listener, err := s.cfg.ListenerCreator(s.cfg.Addr)
	if err != nil {
		return pkgerrors.WithMessage(err, "create listener")
	}
	s.listener = listener
	go s.listen()
	go s.loop()

	// 更新状态.
	s.state = stateStarted
	s.logger.Info("started")
	return nil
}

// Close 关闭 Service.
func (s *Service) Close() error {
	if err := s.lockState(stateStarted, false); err != nil {
		return err
	}
	s.state = stateClosed
	s.unlockState(false)

	// 关闭监听器.
	if s.listener != nil {
		_ = s.listener.Close()
	}

	// 关闭所有已建立的连接.
	s.sessions.close(ErrServiceClosed)

	s.logger.Info("closed")
	return nil
}

// GetSession 获取连接 nodeId 指向 Service 的 Session.
func (s *Service) GetSession(nodeId string) Session {
	if err := s.lockState(stateStarted, true); err != nil {
		return nil
	}
	defer s.unlockState(true)

	if session := s.sessions.get(nodeId); session != nil && session.keepActive() == nil {
		return session
	}

	return nil
}

// onSessionPacket 处理 Session 数据包.
func (s *Service) onSessionPacket(session Session, p *RawPacket) error {
	return s.sessionHandler.OnSessionPacket(session, p)
}

// onSessionClosed 处理 Session 关闭.
func (s *Service) onSessionClosed(session sessionImpl, err error) {
	s.sessions.compareAndDelete(session)
}

// getRawPacket 获取 RawPacket.
func (s *Service) getRawPacket(size int) *RawPacket {
	if s.pm != nil {
		return s.pm.GetRawPacket(size)
	}
	return NewRawPacketWithSize(size)
}

// putRawPacket 回收 RawPacket.
func (s *Service) putRawPacket(p *RawPacket) {
	if s.pm != nil {
		s.pm.PutRawPacket(p)
	}
}

// startSessionTicker 启动 Session Tick 定时器.
func (s *Service) startSessionTicker(session Session) TimerId {
	if err := s.lockState(stateStarted, true); err != nil {
		return TimerIdNone
	}
	defer s.unlockState(true)

	return s.cfg.TimerSystem.StartTimer(s.cfg.Session.TickInterval, true, session.RemoteNodeId(), s.onSessionTicker)
}

// onSessionTicker 处理 Session Tick 回调.
func (s *Service) onSessionTicker(args *TimerArgs) {
	if err := s.lockState(stateStarted, true); err != nil {
		return
	}
	defer s.unlockState(true)

	select {
	case s.cTickSessions <- args.Args.(string):
	case <-s.cClosed:
	}
}

// stopSessionTicker 停止 Session Tick 定时器.
func (s *Service) stopSessionTicker(tid TimerId) {
	if err := s.lockState(stateStarted, true); err != nil {
		return
	}
	defer s.unlockState(true)

	s.cfg.TimerSystem.StopTimer(tid)
}

// loop 主循环逻辑.
func (s *Service) loop() {
	for {
		select {
		case nodeId := <-s.cTickSessions:
			if session := s.sessions.get(nodeId); session != nil {
				session.tick()
			}
		case <-s.cClosed:
			return
		}
	}
}
