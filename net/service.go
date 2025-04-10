package net

import (
	"errors"
	"net"
	"sync"

	"github.com/godyy/gutils/log"
	pkg_errors "github.com/pkg/errors"
	"go.uber.org/zap"
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
}

func (c *ServiceConfig) check() error {
	if c == nil {
		return pkg_errors.New("cluster/net: service config nil")
	}

	if c.NodeId == "" {
		return pkg_errors.New("cluster/net: ServiceConfig.NodeId not specified")
	}

	if c.Addr == "" {
		return pkg_errors.New("cluster/net: ServiceConfig.Addr not specified")
	}

	if err := c.Handshake.check(); err != nil {
		return err
	}

	if err := c.Session.check(); err != nil {
		return err
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
	createListener CreateListener // 网络监听器构造器.
	handler        SessionHandler // 服务处理器.
	establishments *sync.Map      // 正在建立的 session.
}

func CreateService(
	cfg *ServiceConfig,
	dialer Dialer,
	createListener CreateListener,
	handler SessionHandler,
	logger log.Logger,
) (*Service, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}

	if dialer == nil {
		return nil, errors.New("cluster/net: dialer nil")
	}

	if createListener == nil {
		return nil, errors.New("cluster/net: createListener nil")
	}

	if handler == nil {
		return nil, errors.New("cluster/net: handler nil")
	}

	if logger == nil {
		return nil, errors.New("cluster/net: logger nil")
	}

	s := &Service{
		sessionManager: newSessionManager(dialer, logger.Named("Service").WithFields(zap.String("nodeId", cfg.NodeId))),
		cfg:            cfg,
		createListener: createListener,
		handler:        handler,
		establishments: &sync.Map{},
	}

	// 本地会话
	s.sessions.add(newSessionLocal(s))

	return s, nil
}

// Start 启动 Service.
func (s *Service) Start() error {
	return s.start(func() error {
		listener, err := s.createListener(s.cfg.Addr)
		if err != nil {
			return pkg_errors.WithMessage(err, "cluster/net: create listener")
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

// onSessionPacket session 数据包回调.
func (s *Service) onSessionPacket(session Session, p *RawPacket) error {
	return s.handler.OnSessionPacket(session, p)
}

// onSessionClosed session 关闭回调.
func (s *Service) onSessionClosed(session Session, _ error) {
	s.sessions.compareAndDelete(session)
}
