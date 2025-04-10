package net

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/godyy/gcluster/net/internal/protocol/pb"
	"github.com/godyy/gutils/log"
	pkgerrors "github.com/pkg/errors"
	"go.uber.org/zap"
)

// ClientConfig 客户端配置
type ClientConfig struct {
	// NodeId 节点ID.
	NodeId string

	// Handshake 握手配置.
	Handshake HandshakeConfig

	// Session 会话配置.
	Session SessionConfig
}

func (c *ClientConfig) check() error {
	if c == nil {
		return pkgerrors.New("client config nil")
	}

	if c.NodeId == "" {
		return pkgerrors.New("ClientConfig.NodeId not specified")
	}

	if err := c.Handshake.check(); err != nil {
		return err
	}

	if err := c.Session.check(); err != nil {
		return err
	}

	return nil
}

// Client 客户端.
type Client struct {
	sessionManager
	cfg        *ClientConfig // 服务配置.
	connectors *sync.Map     // 连接器.
}

func CreateClient(
	cfg *ClientConfig,
	dialer Dialer,
	sessionHandler SessionHandler,
	logger log.Logger,
	options ...SessionManagerOption,
) (*Client, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}

	if dialer == nil {
		return nil, errors.New("dialer nil")
	}

	if sessionHandler == nil {
		return nil, errors.New("sessionHandler nil")
	}

	if logger == nil {
		return nil, errors.New("logger nil")
	}

	c := &Client{
		sessionManager: newSessionManager(dialer, sessionHandler, logger.Named("Client").WithFields(zap.String("nodeId", cfg.NodeId))),
		cfg:            cfg,
		connectors:     &sync.Map{},
	}

	// 本地会话
	c.sessions.add(newSessionLocal(c))

	for _, opt := range options {
		opt(c)
	}

	return c, nil
}

// Start 启动 Client.
func (c *Client) Start() error {
	return c.start(nil)
}

// Close 关闭 Client.
func (c *Client) Close() error {
	return c.close(nil)
}

func (c *Client) config() *ClientConfig {
	return c.cfg
}

// NodeId 返回客户端的节点ID.
func (c *Client) NodeId() string {
	return c.config().NodeId
}

// Connect 连接指定节点.
func (c *Client) Connect(nodeId string, addr string) (Session, error) {
	return c.connect(nodeId, addr, c.onConnect)
}

func (c *Client) onConnect(nodeId, addr string) (Session, error) {
	ctor, create := c.getOrCreateConnection(nodeId, addr)
	if create {
		c.doConnect(ctor)
	}
	return ctor.wait()
}

// connection 连接 Service.
type connection struct {
	remoteNodeId string        // 远端节点ID.
	addr         string        // 远端地址.
	session      Session       // 成功建立的网络会话.
	err          error         // 导致网络会话建立失败的错误.
	chEnd        chan struct{} // 结束信号 chan.
}

func (c *connection) onSuccess(session Session) {
	c.session = session
	close(c.chEnd)
}

func (c *connection) onError(err error) {
	c.err = err
	close(c.chEnd)
}

func (c *connection) wait() (Session, error) {
	<-c.chEnd
	return c.session, c.err
}

// getOrCreateConnection 获取或创建 connection.
func (c *Client) getOrCreateConnection(remoteNodeId string, addr string) (*connection, bool) {
	if v, ok := c.connectors.Load(remoteNodeId); ok {
		return v.(*connection), false
	} else {
		ctor := &connection{
			remoteNodeId: remoteNodeId,
			addr:         addr,
			chEnd:        make(chan struct{}),
		}
		if v, ok = c.connectors.LoadOrStore(remoteNodeId, ctor); ok {
			close(ctor.chEnd)
			return v.(*connection), false
		} else {
			return ctor, true
		}
	}
}

// doConnect 连接逻辑.
func (c *Client) doConnect(ctor *connection) {
	// 发起连接.
	conn, err := c.dialer(ctor.addr)
	if err != nil {
		c.logger.ErrorFields("connect remote failed",
			zap.String("remoteAddr", ctor.addr),
			zap.String("remoteNodeId", ctor.remoteNodeId),
			zap.Error(err))
		ctor.onError(ErrConnectRemote)
		return
	}

	// 发送握手请求.
	if err := handshakeHelper.writeApply(conn, c.NodeId(), c.config().Handshake.Token, time.Now().Unix(), c.config().Handshake.Timeout); err != nil {
		c.logger.ErrorFields("write handshake apply",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", ctor.remoteNodeId),
			zap.Error(err))
		ctor.onError(ErrRequestHandshake)
		return
	}

	// 读取握手反馈.
	p, err := handshakeHelper.readProto(conn, c.config().Handshake.Timeout)
	if err != nil {
		c.logger.ErrorFields("read handshake apply response",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", ctor.remoteNodeId),
			zap.Error(err))
		ctor.onError(ErrReadHandshakeResp)
		return
	}

	// 处理握手反馈.
	switch resp := p.(type) {
	case *pb.HSAccepted:
		// 握手被接受.
		if resp.NodeId != ctor.remoteNodeId {
			c.logger.ErrorFields("handshake been accepted, but receive wrong nodeId",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", ctor.remoteNodeId),
				zap.String("receivedNodeId", resp.NodeId))
			_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_NodeIdOrTokenWrong, c.config().Handshake.Timeout)
			ctor.onError(ErrRemoteIdOrTokenWrong)
			return
		}

		// 检查对端的 token 是否合法.
		if resp.Token != c.config().Handshake.Token {
			c.logger.ErrorFields("handshake been accepted, but receive wrong token",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", ctor.remoteNodeId))
			_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_NodeIdOrTokenWrong, c.config().Handshake.Timeout)
			ctor.onError(ErrRemoteIdOrTokenWrong)
			return
		}

		// 回复握手完成.
		if err := handshakeHelper.writeCompleted(conn, c.config().Handshake.Timeout); err != nil {
			c.logger.ErrorFields("write handshake completed",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", ctor.remoteNodeId),
				zap.Error(err))
			ctor.onError(ErrReplyHandshake)
			return
		}

		// 读取握手完成确认.
		if _, err := readHandshakeProto[*pb.HSCompletedAck](conn, c.config().Handshake.Timeout); err != nil {
			c.logger.ErrorFields("invalid handshake completed response",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", ctor.remoteNodeId),
				zap.Error(err))
			ctor.onError(ErrReadHandshakeResp)
		} else {
			c.logger.InfoFields("connect service success",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", ctor.remoteNodeId))
			if session, err := c.createSession(ctor.remoteNodeId, conn); err != nil {
				ctor.onError(err)
			} else {
				ctor.onSuccess(session)
			}
		}

	case *pb.HSRejected:
		// 握手被拒绝.
		c.logger.ErrorFields("handshake been rejected",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", ctor.remoteNodeId),
			zap.String("reason", resp.Reason.String()))
		ctor.onError(ErrHandshakeRejected)

	default:
		pt, _ := handshakeHelper.getProtoType(resp)
		c.logger.ErrorFields("invalid handshake apply response",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", ctor.remoteNodeId),
			zap.Int8("protoType", int8(pt)))
		ctor.onError(ErrReadHandshakeResp)
	}
}

// createSession 创建 Session.
func (c *Client) createSession(remoteNodeId string, conn net.Conn) (Session, error) {
	c.mutex.RLock()
	if c.state >= stateClosed {
		c.mutex.RUnlock()
		_ = conn.Close()
		return nil, ErrClosed
	}

	session := newSession(c, remoteNodeId, true, conn, c.logger)
	if err := session.start(); err != nil {
		c.mutex.RUnlock()
		_ = conn.Close()
		return nil, err
	}

	c.sessions.add(session)
	c.mutex.RUnlock()

	return session, nil
}

func (c *Client) getSessionConfig() *SessionConfig {
	return &c.cfg.Session
}
