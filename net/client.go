package net

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/godyy/gcluster/net/internal/protocol/pb"
	"github.com/godyy/gutils/log"
)

// ClientConfig 客户端配置
type ClientConfig struct {
	// NodeId 节点ID.
	NodeId string

	// Handshake 握手配置.
	Handshake HandshakeConfig

	// Session 会话配置.
	Session SessionConfig

	// Dialer 网络拨号器.
	Dialer Dialer
}

func (c *ClientConfig) init() error {
	if c == nil {
		return errors.New("client config nil")
	}

	if c.NodeId == "" {
		return errors.New("ClientConfig.NodeId not specified")
	}

	if err := c.Handshake.init(); err != nil {
		return err
	}

	if err := c.Session.init(); err != nil {
		return err
	}

	if c.Dialer == nil {
		return errors.New("ClientConfig.Dialer not specified")
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
	sessionHandler SessionHandler,
	options ...SessionManagerOption,
) (*Client, error) {
	if err := cfg.init(); err != nil {
		return nil, err
	}

	if sessionHandler == nil {
		return nil, errors.New("sessionHandler nil")
	}

	c := &Client{
		sessionManager: newSessionManager(
			cfg.Dialer,
			sessionHandler,
		),
		cfg:        cfg,
		connectors: &sync.Map{},
	}

	// 本地会话
	c.sessions.add(newSessionLocal(c))

	// 选项.
	for _, opt := range options {
		opt(c)
	}

	// 初始化日志工具.
	c.initLogger()

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
			lfdRemoteAddr(ctor.addr),
			lfdRemoteNodeId(ctor.remoteNodeId),
			lfdError(err))
		ctor.onError(ErrConnectRemote)
		return
	}

	// 发送握手请求.
	if err := handshakeHelper.writeApply(conn, c.NodeId(), c.config().Handshake.Token, time.Now().Unix(), c.config().Handshake.Timeout); err != nil {
		c.logger.ErrorFields("write handshake apply",
			lfdNetRemoteAddr(conn.RemoteAddr()),
			lfdRemoteNodeId(ctor.remoteNodeId),
			lfdError(err))
		ctor.onError(ErrRequestHandshake)
		return
	}

	// 读取握手反馈.
	p, err := handshakeHelper.readProto(conn, c.config().Handshake.Timeout)
	if err != nil {
		c.logger.ErrorFields("read handshake apply response",
			lfdNetRemoteAddr(conn.RemoteAddr()),
			lfdRemoteNodeId(ctor.remoteNodeId),
			lfdError(err))
		ctor.onError(ErrReadHandshakeResp)
		return
	}

	// 处理握手反馈.
	switch resp := p.(type) {
	case *pb.HSAccepted:
		// 握手被接受.
		if resp.NodeId != ctor.remoteNodeId {
			c.logger.ErrorFields("handshake been accepted, but receive wrong nodeId",
				lfdNetRemoteAddr(conn.RemoteAddr()),
				lfdRemoteNodeId(ctor.remoteNodeId),
				lfdReceivedNodeId(resp.NodeId))
			_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_NodeIdOrTokenWrong, c.config().Handshake.Timeout)
			ctor.onError(ErrRemoteIdOrTokenWrong)
			return
		}

		// 检查对端的 token 是否合法.
		if resp.Token != c.config().Handshake.Token {
			c.logger.ErrorFields("handshake been accepted, but receive wrong token",
				lfdNetRemoteAddr(conn.RemoteAddr()),
				lfdRemoteNodeId(ctor.remoteNodeId))
			_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_NodeIdOrTokenWrong, c.config().Handshake.Timeout)
			ctor.onError(ErrRemoteIdOrTokenWrong)
			return
		}

		// 回复握手完成.
		if err := handshakeHelper.writeCompleted(conn, c.config().Handshake.Timeout); err != nil {
			c.logger.ErrorFields("write handshake completed",
				lfdNetRemoteAddr(conn.RemoteAddr()),
				lfdRemoteNodeId(ctor.remoteNodeId),
				lfdError(err))
			ctor.onError(ErrReplyHandshake)
			return
		}

		// 读取握手完成确认.
		if _, err := readHandshakeProto[*pb.HSCompletedAck](conn, c.config().Handshake.Timeout); err != nil {
			c.logger.ErrorFields("invalid handshake completed response",
				lfdNetRemoteAddr(conn.RemoteAddr()),
				lfdRemoteNodeId(ctor.remoteNodeId),
				lfdError(err))
			ctor.onError(ErrReadHandshakeResp)
		} else {
			c.logger.InfoFields("connect service success",
				lfdNetRemoteAddr(conn.RemoteAddr()),
				lfdRemoteNodeId(ctor.remoteNodeId))
			if session, err := c.createSession(ctor.remoteNodeId, conn); err != nil {
				ctor.onError(err)
			} else {
				ctor.onSuccess(session)
			}
		}

	case *pb.HSRejected:
		// 握手被拒绝.
		c.logger.ErrorFields("handshake been rejected",
			lfdNetRemoteAddr(conn.RemoteAddr()),
			lfdRemoteNodeId(ctor.remoteNodeId),
			lfdReason(resp.Reason.String()))
		ctor.onError(ErrHandshakeRejected)

	default:
		pt, _ := handshakeHelper.getProtoType(resp)
		c.logger.ErrorFields("invalid handshake apply response",
			lfdNetRemoteAddr(conn.RemoteAddr()),
			lfdRemoteNodeId(ctor.remoteNodeId),
			lfdProtoTypeNumber(pt))
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

	session := newSession(c, remoteNodeId, true, conn, c.rootLogger)
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

// initLogger 初始化日志工具.
func (c *Client) initLogger() {
	c.initRootLogger()

	if c.logger != nil {
		return
	}

	c.logger = c.rootLogger.Named("Client").WithFields(lfdNodeId(c.cfg.NodeId))
}

// setLogger 设置日志工具.
func (c *Client) setLogger(logger log.Logger) {
	c.setRootLogger(logger)
	c.logger = c.rootLogger.Named("Client").WithFields(lfdNodeId(c.cfg.NodeId))
}
