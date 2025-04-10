package net

import (
	"errors"
	"net"
	"sync"

	"github.com/godyy/gutils/log"
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

	return nil
}

// Service 用于启动本地网络服务. 服务与服务之间可通过维护 Session 进行通信.
// Session 可由本地发起，也可由远端发起. 若两端同时发起 Session, 最终只会保
// 留最先建立的 Session 进行通信.
type Service struct {
	sessionManager
	cfg            *ServiceConfig // 服务配置.
	listener       net.Listener   // 网络监听器.
	establishments *sync.Map      // 正在建立的 Session.
}

func CreateService(cfg *ServiceConfig, sessionHandler SessionHandler, options ...SessionManagerOption) (*Service, error) {
	if err := cfg.init(); err != nil {
		return nil, err
	}

	if sessionHandler == nil {
		return nil, errors.New("sessionHandler nil")
	}

	s := &Service{
		sessionManager: newSessionManager(
			cfg.Dialer,
			sessionHandler,
		),
		cfg:            cfg,
		establishments: &sync.Map{},
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

// Start 启动 Service.
func (s *Service) Start() error {
	return s.start(func() error {
		listener, err := s.cfg.ListenerCreator(s.cfg.Addr)
		if err != nil {
			return pkgerrors.WithMessage(err, "create listener")
		}
		s.listener = listener
		go s.listen()
		return nil
	})
}

// Close 关闭 Service.
func (s *Service) Close() error {
	return s.close(func() {
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
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
func (s *Service) setLogger(logger log.Logger) {
	s.setRootLogger(logger)
	s.logger = s.rootLogger.Named("Service").WithFields(lfdNodeId(s.cfg.NodeId))
}
