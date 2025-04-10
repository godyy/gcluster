package net

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/godyy/gcluster/net/internal/protocol/pb"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// session 的建立分为 active 和 passive 两种方式。
// 两种 session 可能会同时发起建立，但为了优化连接数，
// 只会同时存在一个 session，谁先建立成功谁就得到保留。

// createSession 创建 Session.
func (s *Service) createSession(remoteNodeId string, active bool, conn net.Conn) (Session, error) {
	s.mutex.RLock()
	if s.state >= stateClosed {
		s.mutex.RUnlock()
		_ = conn.Close()
		return nil, ErrClosed
	}

	session := newSession(s, remoteNodeId, active, conn, s.logger)
	if err := session.start(); err != nil {
		s.mutex.RUnlock()
		_ = conn.Close()
		return nil, err
	}

	s.sessions.add(session)
	s.mutex.RUnlock()

	return session, nil
}

// listen 网络监听逻辑.
func (s *Service) listen() {
	for !s.isClosed() {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				s.logger.Info("listening stop")
				break
			}

			s.logger.ErrorFields("listening failed", zap.Error(err))
			continue
		}

		go s.onPassiveConn(conn)
	}

	_ = s.listener.Close()
}

// onPassiveConn 处理被动连接.
func (s *Service) onPassiveConn(conn net.Conn) {
	// 接收握手请求.
	hsApply, err := readHandshakeProto[*pb.HSApply](conn, s.config().Handshake.Timeout)
	if err != nil {
		s.logger.ErrorFields("read handshake apply",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.Error(err))
		_ = conn.Close()
		return
	}

	// 若token不匹配,拒绝握手.
	if hsApply.Token != s.config().Handshake.Token {
		s.logger.ErrorFields("handshake token wrong", zap.Any("remoteAddr", conn.RemoteAddr()), zap.String("remoteNodeId", hsApply.NodeId))
		_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_NodeIdOrTokenWrong, s.config().Handshake.Timeout)
		_ = conn.Close()
		return
	}

	remoteNodeId := hsApply.NodeId

	// 若重复建立，直接拒绝
	if s.sessions.get(remoteNodeId) != nil {
		s.logger.ErrorFields("session established", zap.Any("remoteAddr", conn.RemoteAddr()), zap.String("remoteNodeId", hsApply.NodeId))
		_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_SessionEstablished, s.config().Handshake.Timeout)
		_ = conn.Close()
		return
	}

	establishment := s.getOrCreateEstablishment(remoteNodeId)
	if s.startPassiveEstablish(establishment, conn, hsApply.Time) {
		_, err := establishment.wait()
		if err != nil {
			s.logger.ErrorFields("session passive establish failed", zap.Any("remoteAddr", conn.RemoteAddr()), zap.String("remoteNodeId", hsApply.NodeId), zap.Error(err))
		}
	} else {
		s.logger.ErrorFields("session passive establishing", zap.Any("remoteAddr", conn.RemoteAddr()), zap.String("remoteNodeId", hsApply.NodeId))
	}
}

// getOrCreateEstablishment 获取或创建 establishment.
func (s *Service) getOrCreateEstablishment(remoteNodeId string) *establishment {
	value, ok := s.establishments.Load(remoteNodeId)
	if ok {
		return value.(*establishment)
	} else {
		ec := establishment{
			remoteNodeId: remoteNodeId,
			chEnd:        make(chan struct{}),
		}
		if value, ok = s.establishments.LoadOrStore(remoteNodeId, &ec); ok {
			close(ec.chEnd)
			return value.(*establishment)
		} else {
			return &ec
		}
	}
}

// Connect 连接指定节点.
func (s *Service) Connect(nodeId string, addr string) (Session, error) {
	return s.connect(nodeId, addr, s.onConnect)
}

func (s *Service) onConnect(nodeId, addr string) (Session, error) {
	establishment := s.getOrCreateEstablishment(nodeId)
	s.startActiveEstablish(establishment, addr)
	return establishment.wait()
}

// whichEstablishEarlier 根据 establishment 判断 active 表示的连接是否更早开始建立.
// 若更早，调用 earlierCb; 否则，调用 otherwiseCb.
func (s *Service) whichEstablishEarlier(es *establishment, active bool, earlierCb, otherwiseCb func()) bool {
	es.mutex.Lock()
	defer es.mutex.Unlock()

	var earlier bool

	switch es.whichStartEarlier(s.NodeId() < es.remoteNodeId) {
	case 1:
		// 主动建立的连接更先
		earlier = active
	case 2:
		// 被动建立的连接更先
		earlier = !active
	default:
		earlier = false
	}

	if earlier {
		if earlierCb != nil {
			earlierCb()
		}
	} else {
		if otherwiseCb != nil {
			otherwiseCb()
		}
	}

	return earlier
}

// startActiveEstablish 启动主动连接建立过程，连接 remoteAddr 指定的远端.
func (s *Service) startActiveEstablish(es *establishment, remoteAddr string) bool {
	es.mutex.Lock()

	if es.startActive(time.Now().UnixNano()) {
		es.mutex.Unlock()
		s.logger.DebugFields("start active establish",
			zap.String("remoteAddr", remoteAddr),
			zap.String("remoteNodeId", es.remoteNodeId))
		s.activeEstablishConnect(es, remoteAddr)
		return true
	}

	es.mutex.Unlock()
	return false
}

// activeEstablishConnect 主动连接建立过程.
func (s *Service) activeEstablishConnect(es *establishment, remoteAddr string) {

	// 发起连接.
	conn, err := s.dialer(remoteAddr)
	if err != nil {
		s.logger.ErrorFields("connect remote failed",
			zap.String("remoteAddr", remoteAddr),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Error(err))
		s.activeEstablishFail(es, nil, ErrConnectRemote)
		return
	}

	// 连接成功，开始握手.
	s.activeEstablishHandshake(es, conn)
}

// activeEstablishHandshake 主动连接握手过程.
func (s *Service) activeEstablishHandshake(es *establishment, conn net.Conn) {
	es.mutex.Lock()
	if !es.startActiveHandshake() {
		es.mutex.Unlock()
		return
	}
	es.mutex.Unlock()

	s.logger.DebugFields("active establish handshaking...",
		zap.Any("remoteAddr", conn.RemoteAddr()),
		zap.String("remoteNodeId", es.remoteNodeId))

	// 发送握手请求.
	if err := handshakeHelper.writeApply(conn, s.NodeId(), s.config().Handshake.Token, es.activeStartTime(), s.config().Handshake.Timeout); err != nil {
		s.logger.ErrorFields("write handshake apply",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Error(err))
		s.activeEstablishFail(es, conn, ErrRequestHandshake)
		return
	}

	// 读取握手反馈.
	p, err := handshakeHelper.readProto(conn, s.config().Handshake.Timeout)
	if err != nil {
		s.logger.ErrorFields("read handshake apply response",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Error(err))
		s.activeEstablishFail(es, conn, ErrReadHandshakeResp)
		return
	}

	// 处理握手反馈.
	switch resp := p.(type) {
	case *pb.HSAccepted:
		// 握手被接受.
		if resp.NodeId != es.remoteNodeId {
			s.logger.ErrorFields("handshake been accepted, but receive wrong nodeId",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", es.remoteNodeId),
				zap.String("receivedNodeId", resp.NodeId))
			_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_NodeIdOrTokenWrong, s.config().Handshake.Timeout)
			s.activeEstablishFail(es, conn, ErrRemoteIdOrTokenWrong)
			return
		}

		// 检查对端的 token 是否合法.
		if resp.Token != s.config().Handshake.Token {
			s.logger.ErrorFields("handshake been accepted, but receive wrong token",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", es.remoteNodeId))
			_ = handshakeHelper.writeRejected(conn, pb.HSRejectedReason_NodeIdOrTokenWrong, s.config().Handshake.Timeout)
			s.activeEstablishFail(es, conn, ErrRemoteIdOrTokenWrong)
			return
		}

		if s.whichEstablishEarlier(es, false, nil, func() { es.activeAboutSuccess() }) {
			s.logger.InfoFields("passive establish earlier, stop active establish",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", es.remoteNodeId))
			s.activeEstablishFail(es, conn, ErrPassiveHandshakeEarlier)
		} else {
			s.activeEstablishAboutSuccess(es, conn)
		}

	case *pb.HSRejected:
		// 握手被拒绝.
		s.logger.ErrorFields("handshake been rejected",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.String("reason", resp.Reason.String()))
		s.activeEstablishFail(es, conn, ErrHandshakeRejected)

	default:
		pt, _ := handshakeHelper.getProtoType(resp)
		s.logger.ErrorFields("invalid handshake apply response",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Int8("protoType", int8(pt)))
		s.activeEstablishFail(es, conn, ErrReadHandshakeResp)
	}
}

// activeEstablishAboutSuccess 主动连接即将成功过程.
func (s *Service) activeEstablishAboutSuccess(es *establishment, conn net.Conn) {
	// 回复握手完成.
	if err := handshakeHelper.writeCompleted(conn, s.config().Handshake.Timeout); err != nil {
		s.logger.ErrorFields("write handshake completed",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Error(err))
		s.activeEstablishFail(es, conn, ErrReplyHandshake)
		return
	}

	// 读取握手完成确认.
	if _, err := readHandshakeProto[*pb.HSCompletedAck](conn, s.config().Handshake.Timeout); err != nil {
		s.logger.ErrorFields("invalid handshake completed response",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Error(err))
		s.activeEstablishFail(es, conn, ErrReadHandshakeResp)
	} else {
		s.activeEstablishSuccess(es, conn)
	}
}

// activeEstablishSuccess 主动连接成功过程.
func (s *Service) activeEstablishSuccess(es *establishment, conn net.Conn) {
	es.mutex.Lock()
	defer es.mutex.Unlock()

	if !es.activeSuccess(conn) {
		_ = conn.Close()
		return
	}

	s.logger.DebugFields("active establish success",
		zap.Any("remoteAddr", conn.RemoteAddr()),
		zap.String("remoteNodeId", es.remoteNodeId))

	if es.isPassiveEstablishing() {
		return
	}

	s.establishEnd(es)
}

// activeEstablishFail 主动连接失败过程.
func (s *Service) activeEstablishFail(es *establishment, conn net.Conn, err error) {
	var remoteAddr net.Addr
	if conn != nil {
		remoteAddr = conn.RemoteAddr()
		_ = conn.Close()
	}

	es.mutex.Lock()
	defer es.mutex.Unlock()

	if !es.activeFail(err) {
		return
	}

	s.logger.DebugFields("active establish fail",
		zap.Any("remoteAddr", remoteAddr),
		zap.String("remoteNodeId", es.remoteNodeId))

	if es.isPassiveEstablishing() {
		return
	}

	s.establishEnd(es)
}

// startPassiveEstablish 启动被动连接建立过程.
func (s *Service) startPassiveEstablish(es *establishment, conn net.Conn, applyTime int64) bool {
	es.mutex.Lock()

	if es.startPassive(applyTime) {
		es.mutex.Unlock()
		s.logger.DebugFields("start passive establish",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId))
		s.passiveEstablishHandshake(es, conn)
		return true
	} else {
		es.mutex.Unlock()
		return false
	}
}

// passiveEstablishHandshake 被动连接握手过程.
func (s *Service) passiveEstablishHandshake(es *establishment, conn net.Conn) {
	s.logger.DebugFields("passive establish handshaking",
		zap.Any("remoteAddr", conn.RemoteAddr()),
		zap.String("remoteNodeId", es.remoteNodeId))

	// 回复握手确认.
	if err := handshakeHelper.writeAccepted(conn, s.NodeId(), s.config().Handshake.Token, s.config().Handshake.Timeout); err != nil {
		s.logger.ErrorFields("write handshake accepted",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Error(err))
		s.passiveEstablishFail(es, conn, ErrReplyHandshake)
		return
	}

	// 读取回复.
	p, err := handshakeHelper.readProto(conn, s.config().Handshake.Timeout)
	if err != nil {
		s.logger.ErrorFields("read handshake accepted response",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Error(err))
		s.passiveEstablishFail(es, conn, ErrReadHandshakeResp)
		return
	}

	// 处理回复.
	switch resp := p.(type) {
	case *pb.HSCompleted:
		s.logger.DebugFields("read handshake completed",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId))

		// 握手完成.
		if s.whichEstablishEarlier(es, true, nil, nil) {
			s.logger.InfoFields("active establish earlier, stop passive establish",
				zap.Any("remoteAddr", conn.RemoteAddr()),
				zap.String("remoteNodeId", es.remoteNodeId))
			s.passiveEstablishFail(es, conn, ErrActiveHandshakeEarlier)
		} else {
			// 回复完成确认.
			if err := handshakeHelper.writeCompletedAck(conn, s.config().Handshake.Timeout); err != nil {
				s.logger.ErrorFields("write handshake completed ack",
					zap.Any("remoteAddr", conn.RemoteAddr()),
					zap.String("remoteNodeId", es.remoteNodeId),
					zap.Error(err))
				s.passiveEstablishFail(es, conn, ErrReplyHandshake)
				return
			}

			// 成功.
			s.passiveEstablishSuccess(es, conn)
		}

	case *pb.HSRejected:
		// 被拒绝.
		s.logger.ErrorFields("handshake accepted rejected",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Any("reason", resp.Reason))
		s.passiveEstablishFail(es, conn, ErrHandshakeRejected)

	default:
		protoType, _ := handshakeHelper.getProtoType(resp)
		s.logger.ErrorFields("invalid handshake accepted response",
			zap.Any("remoteAddr", conn.RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId),
			zap.Int8("protoType", int8(protoType)))
		s.passiveEstablishFail(es, conn, ErrReadHandshakeResp)
	}
}

// passiveEstablishSuccess 被动连接成功过程.
func (s *Service) passiveEstablishSuccess(es *establishment, conn net.Conn) {
	es.mutex.Lock()
	defer es.mutex.Unlock()

	if !es.passiveSuccess(conn) {
		return
	}

	s.logger.DebugFields("passive establish success",
		zap.Any("remoteAddr", conn.RemoteAddr()),
		zap.String("remoteNodeId", es.remoteNodeId))

	if es.isActiveEstablishing() {
		return
	}

	s.establishEnd(es)
}

// passiveEstablishFail 被动连接失败过程.
func (s *Service) passiveEstablishFail(es *establishment, conn net.Conn, err error) {
	var remoteAddr net.Addr
	if conn != nil {
		remoteAddr = conn.RemoteAddr()
		_ = conn.Close()
	}

	es.mutex.Lock()
	defer es.mutex.Unlock()

	if !es.passiveFail(err) {
		return
	}

	s.logger.DebugFields("passive establish fail",
		zap.Any("remoteAddr", remoteAddr),
		zap.String("remoteNodeId", es.remoteNodeId))

	if es.isActiveEstablishing() {
		return
	}

	s.establishEnd(es)
}

// establishEnd 连接建立结束过程.
func (s *Service) establishEnd(es *establishment) {
	if es.isEnd() {
		return
	}

	var (
		conn   net.Conn
		active bool
		err    error
	)

	switch {
	case es.isActiveSuccess() && (!es.isPassiveStarted() || es.isPassiveFail()):
		s.logger.InfoFields("active establish finally success",
			zap.Any("remoteAddr", es.activeConn().RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId))
		conn = es.activeConn()
		active = true

	case es.isPassiveSuccess() && (!es.isActiveStarted() || es.isActiveFail()):
		s.logger.InfoFields("passive establish finally success",
			zap.Any("remoteAddr", es.passiveConn().RemoteAddr()),
			zap.String("remoteNodeId", es.remoteNodeId))
		conn = es.passiveConn()

	case es.isActiveSuccess() && es.isPassiveSuccess():
		if es.whichStartEarlier(s.NodeId() < es.remoteNodeId) == 1 {
			s.logger.InfoFields("active establish finally success cause earlier than passive",
				zap.Any("remoteAddr", es.activeConn().RemoteAddr()),
				zap.String("remoteNodeId", es.remoteNodeId))
			_ = es.passiveConn().Close()
			conn = es.activeConn()
			active = true
		} else {
			s.logger.InfoFields("passive establish finally success cause earlier than active",
				zap.Any("remoteAddr", es.passiveConn().RemoteAddr()),
				zap.String("remoteNodeId", es.remoteNodeId))
			_ = es.activeConn().Close()
			conn = es.passiveConn()
		}

	case es.isActiveFail() && !es.isPassiveStarted():
		s.logger.InfoFields("active establish finally fail",
			zap.String("remoteNodeId", es.remoteNodeId))
		err = es.activeErr()

	case es.isPassiveFail() && !es.isActiveStarted():
		s.logger.InfoFields("passive establish finally fail",
			zap.String("remoteNodeId", es.remoteNodeId))
		err = es.passiveErr()

	case es.isActiveFail() && es.isPassiveFail():
		s.logger.InfoFields("both active and passive establish finally fail",
			zap.String("remoteNodeId", es.remoteNodeId))
		if es.whichStartEarlier(s.NodeId() < es.remoteNodeId) == 1 {
			err = es.activeErr()
		} else {
			err = es.passiveErr()
		}

	default:
		s.logger.ErrorFields("unknown establish end status",
			zap.Int8("activeState", es.activeState()), zap.Int8("passiveState", es.passiveState()),
			zap.String("remoteNodeId", es.remoteNodeId))
		err = fmt.Errorf("unknown end status, activeState: %d, passiveState: %d", es.activeState(), es.passiveState())
	}

	if err != nil {
		es.end(nil, err)
	} else {
		session, err := s.createSession(es.remoteNodeId, active, conn)
		es.end(session, err)
	}
}

// 建立 session 相关状态.
const (
	establishConnect      = 1 // 连接.
	establishHandshake    = 2 // 握手.
	establishAboutSuccess = 3 // 即将成功.
	establishSuccess      = 4 // 成功.
	establishFail         = 5 // 失败.
)

// establishing 建立中的连接信息.
type establishing struct {
	state     int8     // 状态.
	startTime int64    // 开始的时间.
	conn      net.Conn // 连接对象.
	err       error    // 导致 session 建立失败的错误.
}

func (es *establishing) changeState(state int8) bool {
	if state <= es.state {
		return false
	}

	es.state = state
	return true
}

func (es *establishing) isStarted() bool {
	return es.state > 0
}

func (es *establishing) isEstablishing() bool {
	return es.isStarted() && !es.isEnd()
}

func (es *establishing) isSuccess() bool {
	return es.state == establishSuccess
}

func (es *establishing) isFail() bool {
	return es.state == establishFail
}

func (es *establishing) isEnd() bool {
	return es.state > establishAboutSuccess
}

func (es *establishing) success(conn net.Conn) bool {
	if !es.changeState(establishSuccess) {
		return false
	}

	es.conn = conn
	return true
}

func (es *establishing) fail(err error) bool {
	if !es.changeState(establishFail) {
		return false
	}

	es.err = err
	return true
}

// establishment 状态.
const (
	establishmentStarted = 1 // 开始.
	establishmentEnd     = 2 // 结束.
)

// establishment session 建立上下文.
type establishment struct {
	remoteNodeId string // 远端节点ID.

	mutex   sync.Mutex    // Mutex for following.
	state   int8          // 总体状态.
	active  *establishing // 主动.
	passive *establishing // 被动.
	session Session       // 成功建立的网络会话.
	err     error         // 导致网络会话建立失败的错误.
	chEnd   chan struct{} // 结束信号 chan.
}

func (es *establishment) changeState(state int8) bool {
	if state <= es.state {
		return false
	}

	es.state = state
	return true
}

func (es *establishment) isEnd() bool {
	return es.state == establishmentEnd
}

func (es *establishment) end(session Session, err error) {
	if !es.changeState(establishmentEnd) {
		return
	}

	es.session = session
	es.err = err
	close(es.chEnd)
}

func (es *establishment) wait() (Session, error) {
	<-es.chEnd
	return es.session, es.err
}

func (es *establishment) startActive(t int64) bool {
	if es.isEnd() || es.isPassiveStarted() {
		return false
	}

	es.changeState(establishmentStarted)

	if es.active == nil {
		es.active = &establishing{}
	}
	if es.active.changeState(establishConnect) {
		es.active.startTime = t
		return true
	}
	return false
}

func (es *establishment) activeState() int8 {
	if es.active == nil {
		return 0
	}
	return es.active.state
}

func (es *establishment) isActiveStarted() bool {
	return es.active != nil && es.active.isStarted()
}

func (es *establishment) isActiveEstablishing() bool {
	return es.active != nil && es.active.isEstablishing()
}

func (es *establishment) isActiveSuccess() bool {
	return es.active != nil && es.active.isSuccess()
}

func (es *establishment) isActiveFail() bool {
	return es.active != nil && es.active.isFail()
}

func (es *establishment) startActiveHandshake() bool {
	if es.isEnd() || es.active == nil {
		return false
	}
	return es.active.changeState(establishHandshake)
}

func (es *establishment) activeAboutSuccess() bool {
	if es.active == nil {
		return false
	}
	return es.active.changeState(establishAboutSuccess)
}

func (es *establishment) activeSuccess(conn net.Conn) bool {
	if es.active == nil {
		return false
	}
	return es.active.success(conn)
}

func (es *establishment) activeFail(err error) bool {
	if es.active == nil {
		return false
	}
	return es.active.fail(err)
}

func (es *establishment) activeStartTime() int64 {
	if es.active == nil {
		return 0
	}
	return es.active.startTime
}

func (es *establishment) activeConn() net.Conn {
	if es.active == nil {
		return nil
	}
	return es.active.conn
}

func (es *establishment) activeErr() error {
	if es.active == nil {
		return nil
	}
	return es.active.err
}

func (es *establishment) startPassive(t int64) bool {
	if es.isEnd() {
		return false
	}

	es.changeState(establishmentStarted)

	if es.passive == nil {
		es.passive = &establishing{}
	}
	if es.passive.changeState(establishHandshake) {
		es.passive.startTime = t
		return true
	}

	return false
}

func (es *establishment) passiveState() int8 {
	if es.passive == nil {
		return 0
	}
	return es.passive.state
}

func (es *establishment) isPassiveStarted() bool {
	return es.passive != nil && es.passive.isStarted()
}

func (es *establishment) isPassiveEstablishing() bool {
	return es.passive != nil && es.passive.isEstablishing()
}

func (es *establishment) isPassiveSuccess() bool {
	return es.passive != nil && es.passive.isSuccess()
}

func (es *establishment) isPassiveFail() bool {
	return es.passive != nil && es.passive.isFail()
}

func (es *establishment) passiveSuccess(conn net.Conn) bool {
	if es.passive == nil {
		return false
	}
	return es.passive.success(conn)
}

func (es *establishment) passiveFail(err error) bool {
	if es.passive == nil {
		return false
	}
	return es.passive.fail(err)
}

func (es *establishment) passiveStartTime() int64 {
	if es.passive == nil {
		return 0
	}
	return es.passive.startTime
}

func (es *establishment) passiveConn() net.Conn {
	if es.passive == nil {
		return nil
	}
	return es.passive.conn
}

func (es *establishment) passiveErr() error {
	if es.passive == nil {
		return nil
	}
	return es.passive.err
}

func (es *establishment) whichStartEarlier(sameTime bool) int {
	if es.isActiveStarted() && es.isPassiveStarted() {
		if es.active.startTime < es.passive.startTime || (es.active.startTime == es.passive.startTime && sameTime) {
			return 1
		} else {
			return 2
		}
	} else if es.isActiveStarted() {
		return 1
	} else if es.isPassiveStarted() {
		return 2
	} else {
		return 0
	}
}
