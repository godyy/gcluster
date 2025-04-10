package net

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godyy/gnet"
	"github.com/godyy/gutils/buffer"
	"github.com/godyy/gutils/buffer/bytes"
	"github.com/godyy/gutils/log"
	pkg_errors "github.com/pkg/errors"
	"go.uber.org/zap"
)

// SessionConfig session 配置
type SessionConfig struct {
	PendingPacketQueueSize int           // 待发送数据包队列大小.
	MaxPacketLength        int           // 最大数据包长度.
	ReadBufSize            int           // 读取缓冲区大小.
	WriteBufSize           int           // 发送缓冲区大小.
	ReadWriteTimeout       time.Duration // 读写超时.
	HeartbeatInterval      time.Duration // 心跳间隔.
	InactiveTimeout        time.Duration // 不活跃超时.
}

func (c *SessionConfig) check() error {
	if c == nil {
		return errors.New("cluster/net: SessionConfig nil")
	}

	if c.PendingPacketQueueSize <= 0 {
		return errors.New("cluster/net: SessionConfig.PendingPacketQueueSize must > 0")
	}

	if c.MaxPacketLength <= 0 || c.MaxPacketLength >= math.MaxInt32 {
		return fmt.Errorf("cluster/net: SessionConfig.MaxPacketLength must > 0 and < %d", math.MaxInt32)
	}

	if c.ReadBufSize <= 0 {
		return errors.New("cluster/net: SessionConfig.ReadBufSize must > 0")
	}

	if c.WriteBufSize <= 0 {
		return errors.New("cluster/net: SessionConfig.WriteBufSize must > 0")
	}

	if c.ReadWriteTimeout <= 0 {
		return errors.New("cluster/net: SessionConfig.ReadWriteTimeout must > 0")
	}

	if c.HeartbeatInterval <= 0 {
		return errors.New("cluster/net: SessionConfig.HeartbeatInterval must > 0")
	}

	if c.InactiveTimeout <= 0 {
		return errors.New("cluster/net: SessionConfig.InactiveTimeout must > 0")
	}

	return nil
}

// session 网络会话.
type session struct {
	manager        SessionManager // manager.
	remoteNodeId   string         // 远端节点ID.
	active         bool           // 是否发起方（需要发送心跳请求）.
	pendingPackets chan packet    // 待发送数据包队列.
	lastActiveTime int64          // 最近一次活跃的时间（发送/接收消息）.
	logger         log.Logger     // 日志工具.

	mutex    sync.RWMutex  // RWMutex for following.
	state    int32         // 状态.
	core     *gnet.Session // 核心实现.
	chClosed chan struct{} // 关闭 chan.
}

// newSession 构造 session.
func newSession(manager SessionManager, remoteNodeId string, active bool, conn net.Conn, logger log.Logger) *session {
	s := &session{
		remoteNodeId:   remoteNodeId,
		manager:        manager,
		active:         active,
		pendingPackets: make(chan packet, manager.getSessionConfig().PendingPacketQueueSize),
		state:          stateInit,
		chClosed:       make(chan struct{}),
		logger:         logger.WithFields(zap.String("remoteNodeId", remoteNodeId), zap.Bool("active", active)),
	}

	packetReadWriter := newSessionPacketReadWriter(manager.getSessionConfig())
	s.core = gnet.NewSession(conn, packetReadWriter, packetReadWriter, s)

	return s
}

// RemoteNodeId 远端节点ID.
func (s *session) RemoteNodeId() string {
	return s.remoteNodeId
}

// start 启动会话
func (s *session) start() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if atomic.LoadInt32(&s.state) > stateInit {
		return ErrStarted
	}

	// 启动 core. 若出错，直接关闭 session 并返回错误.
	if err := s.core.Start(); err != nil {
		s.core = nil
		close(s.chClosed)
		atomic.StoreInt32(&s.state, stateClosed)
		return err
	}

	s.refreshActiveTime()
	atomic.StoreInt32(&s.state, stateStarted)
	go s.tick()

	s.logger.Info("session started")

	return nil
}

// tick session 生命周期逻辑.
func (s *session) tick() {
	heartbeatTicker := time.NewTicker(s.manager.getSessionConfig().HeartbeatInterval)

	closed := false
	for !closed {
		select {
		case <-heartbeatTicker.C:
			s.tickHeartbeat()
		case <-s.chClosed:
			closed = true
		}
	}

	heartbeatTicker.Stop()
}

// tickHeartbeat 心跳周期逻辑.
func (s *session) tickHeartbeat() {
	// 发起方负责发送心跳.
	// 被发起方负责处理失效逻辑.

	if s.active {
		s.logger.Debug("session send heartbeat")
		p := &heartbeatPacket{}
		p.setPing()
		if err := s.send(p); err != nil {
			s.logger.ErrorFields("session send heartbeat", zap.Error(err))
		}
	} else if time.Duration(time.Now().UnixNano()-atomic.LoadInt64(&s.lastActiveTime)) >= s.manager.getSessionConfig().InactiveTimeout {
		s.logger.Debug("session inactive timeout")
		s.close(ErrInactiveClosed)
	}
}

// close 关闭会话.
func (s *session) close(err error) {
	s.mutex.Lock()
	if atomic.LoadInt32(&s.state) >= stateClosed {
		s.mutex.Unlock()
		return
	}

	// 保证重入时逻辑的正确性，优先更新状态.
	// 为了避免死锁，先解锁. 因为关闭 core 时，可能会触发 SessionOnClosed 回调，
	// 既而导致重入 close.
	atomic.StoreInt32(&s.state, stateClosed)
	s.mutex.Unlock()

	// 执行关闭逻辑.
	// 将 core 置为空是为了解除环引用，确保不回造成内存泄漏.
	// 因为 gnet.Session 的实现为了逻辑简单，并未在关闭时
	// 重置 handler.
	_ = s.core.Close()
	s.core = nil
	close(s.chClosed)

	s.logger.InfoFields("session closed", zap.Error(err))

	// 通知 Service session 关闭.
	s.manager.onSessionClosed(s, err)
}

// isClosed 返回 session 是否已关闭.
func (s *session) isClosed() bool {
	return atomic.LoadInt32(&s.state) >= stateClosed
}

// send 发送数据底层接口.
func (s *session) send(p packet) error {
	if p == nil {
		return errors.New("packet nil")
	}

	if len(p.Data()) > s.manager.getSessionConfig().MaxPacketLength {
		return ErrPacketLengthOverflow
	}

	switch atomic.LoadInt32(&s.state) {
	case stateInit:
		return ErrNotStarted
	case stateClosed:
		return ErrClosed
	default:
		select {
		case s.pendingPackets <- p:
			return nil
		case <-s.chClosed:
			return ErrClosed
		}
	}
}

// SendRaw 发送 Raw 数据包.
func (s *session) SendRaw(p *RawPacket) error {
	err := s.send(p)
	if err == nil {
		s.refreshActiveTime()
	}
	return err
}

// refreshActiveTime 刷新活跃时间.
func (s *session) refreshActiveTime() {
	atomic.StoreInt64(&s.lastActiveTime, time.Now().UnixNano())
}

// SessionPendingPacket 实现 gnet.SessionHandler. 返回待发送的数据包.
func (s *session) SessionPendingPacket() (p gnet.Packet, more bool, err error) {
	select {
	case p := <-s.pendingPackets:
		if p == nil {
			return nil, false, ErrClosed
		} else {
			return p, len(s.pendingPackets) > 0, nil
		}
	case <-s.chClosed:
		return nil, false, ErrClosed
	}
}

// SessionOnPacket 实现 gnet.SessionHandler. 接收数据包回调.
func (s *session) SessionOnPacket(_ *gnet.Session, p gnet.Packet) error {
	switch p := p.(type) {
	case *heartbeatPacket:
		return s.handleHeartbeat(p)
	case *RawPacket:
		return s.handleRaw(p)
	default:
		panic(fmt.Sprintf("cluster/net: session on packet, unknown packet type %T", p))
	}
}

// handleHeartbeat 处理心跳.
func (s *session) handleHeartbeat(p *heartbeatPacket) error {
	ping := p.ping()
	if ping {
		s.logger.Debug("session handle ping")
		p.setPong()
		return s.send(p)
	} else {
		s.logger.Debug("session receive pong")
	}

	return nil
}

// handleRaw 处理数据包.
func (s *session) handleRaw(p *RawPacket) error {
	s.refreshActiveTime()
	return s.manager.onSessionPacket(s, p)
}

// SessionOnClosed 实现 gnet.SessionHandler. 连接关闭回调.
func (s *session) SessionOnClosed(_ *gnet.Session, err error) {
	s.close(err)
}

// sessionLocal 本地会话
// 为了便于使用同一套工作流通信，对本地会话做一套封装，
// 便于向本地发送数据，实现数据转发.
type sessionLocal struct {
	manager SessionManager
}

func newSessionLocal(manager SessionManager) *sessionLocal {
	return &sessionLocal{
		manager: manager,
	}
}

func (s *sessionLocal) RemoteNodeId() string {
	return s.manager.NodeId()
}

func (s *sessionLocal) SendRaw(p *RawPacket) error {
	return s.manager.onSessionPacket(s, p)
}

func (s *sessionLocal) close(_ error) {}

func (s *sessionLocal) isClosed() bool { return false }

// sessionPacketReadWriter 实现数据包的读写功能.
type sessionPacketReadWriter struct {
	sc          *SessionConfig     // session 配置.
	readBuffer  *bytes.FixedBuffer // 读取缓冲区.
	writeBuffer *bytes.FixedBuffer // 发送缓冲区.
}

// newSessionPacketReadWriter 创建 sessionPacketReadWriter.
func newSessionPacketReadWriter(sc *SessionConfig) *sessionPacketReadWriter {
	return &sessionPacketReadWriter{
		sc:          sc,
		readBuffer:  bytes.NewFixedBuffer(sc.ReadBufSize),
		writeBuffer: bytes.NewFixedBuffer(sc.WriteBufSize),
	}
}

// readToBuffer 读取数据到缓冲区.
func (rw *sessionPacketReadWriter) readToBuffer(cr gnet.ConnReader) error {
	if err := cr.SetReadDeadline(time.Now().Add(rw.sc.ReadWriteTimeout)); err != nil {
		return pkg_errors.WithMessage(err, "set read deadline")
	}
	_, err := rw.readBuffer.ReadFrom(cr)
	return err
}

// readFull 自 cr 读取足够 p 长度的数据.
func (rw *sessionPacketReadWriter) readFull(cr gnet.ConnReader, p []byte) error {
	for len(p) > 0 {
		n, err := rw.readBuffer.Read(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if err := rw.readToBuffer(cr); err != nil {
					return err
				}
				continue
			}
			return err
		}
	}
	return nil
}

// SessionReadPacket 实现 gnet.SessionPacketReadWriter.
func (rw *sessionPacketReadWriter) SessionReadPacket(cr gnet.ConnReader) (gnet.Packet, error) {
	// head.
	var head packetHead
	if err := rw.readFull(cr, head[:]); err != nil {
		return nil, pkg_errors.WithMessage(err, "cluster/net: session read packet head")
	}
	protoType := head.protoType()
	payloadLen := head.payloadLen()

	// payload.
	switch protoType {
	case protoTypeHeartbeat:
		if payloadLen != heartbeatPacketLength {
			return nil, fmt.Errorf("cluster/net: session read packet, heartbeat packet length %d wrong", payloadLen)
		}
		p := &heartbeatPacket{}
		if err := rw.readFull(cr, p[:]); err != nil {
			return nil, pkg_errors.WithMessage(err, "cluster/net: session read heartbeat packet payload")
		}
		return p, nil
	case protoTypeRaw:
		if payloadLen > uint32(rw.sc.MaxPacketLength) {
			return nil, fmt.Errorf("cluster/net: session read raw packet, packet length %d over limited", payloadLen)
		}
		p := NewRawPacketWithSize(int(payloadLen))
		if err := rw.readFull(cr, p.Data()); err != nil {
			return nil, pkg_errors.WithMessagef(err, "cluster/net: session read packet body, proto-type \"%s\"", protoType)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("cluster/net: session read packet, unknown proto-type %d", protoType)
	}
}

// writeFromBuffer 从缓冲区发送数据.
func (rw *sessionPacketReadWriter) writeFromBuffer(cw gnet.ConnWriter) error {
	if err := cw.SetWriteDeadline(time.Now().Add(rw.sc.ReadWriteTimeout)); err != nil {
		return pkg_errors.WithMessage(err, "set writeFull deadline")
	}
	for rw.writeBuffer.Readable() > 0 {
		if _, err := rw.writeBuffer.WriteTo(cw); err != nil {
			return err
		}
	}
	return nil
}

// writeFull 将 p 通过 cw 完整的发送出去.
func (rw *sessionPacketReadWriter) writeFull(cw gnet.ConnWriter, p []byte) error {
	for len(p) > 0 {
		n, err := rw.writeBuffer.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			if errors.Is(err, buffer.ErrBufferFull) {
				if err := rw.writeFromBuffer(cw); err != nil {
					return err
				}
				continue
			}
			return err
		}
	}
	return nil
}

// SessionWritePacket 实现 gnet.SessionPacketReadWriter.
func (rw *sessionPacketReadWriter) SessionWritePacket(cw gnet.ConnWriter, p gnet.Packet, more bool) error {
	pp, ok := p.(packet)
	if !ok {
		return fmt.Errorf("cluster/net: session writeFull packet, unknown packet type %T", p)
	}

	// head.
	var head packetHead
	head.setProtoType(pp.protoType())
	head.setPayloadLen(uint32(len(pp.Data())))
	if err := rw.writeFull(cw, head[:]); err != nil {
		return pkg_errors.WithMessage(err, "cluster/net: session writeFull packet head")
	}

	// payload.
	if err := rw.writeFull(cw, pp.Data()); err != nil {
		return pkg_errors.WithMessage(err, "cluster/net: session writeFull packet payload")
	}

	if !more && rw.writeBuffer.Readable() > 0 {
		if err := rw.writeFromBuffer(cw); err != nil {
			return pkg_errors.WithMessage(err, "cluster/net: session writeFull from buffer")
		}
	}

	return nil
}
