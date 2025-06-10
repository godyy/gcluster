package net

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godyy/glog"
	"github.com/godyy/gnet"
)

// SessionConfig Session 配置
type SessionConfig struct {
	// PendingPacketQueueSize 待发送数据包队列大小, 表示可同时排队的包数量上限.
	// 默认值为 100.
	PendingPacketQueueSize int

	// MaxPacketLength 表示支持的最大数据包长度.
	MaxPacketLength int

	// ReadBufSize 表示读取批处理缓冲区的大小.
	ReadBufSize int

	// WriteBufSize 表示发送批处理缓冲区的大小.
	WriteBufSize int

	// ReadWriteTimeout 读写超时.
	ReadWriteTimeout time.Duration

	// TickInterval Tick 间隔. Tick 用于定期处理 Session 的生命周期逻辑.
	// 默认值和上限均为 ReadWriteTimeout/3.
	TickInterval time.Duration

	// HeartbeatTimeout 心跳超时. 当 Session 属于主动端时, 心跳超时用于定期通知
	// 对端保持连接的活跃. 每次 Tick 时会检查最近一次活跃(发送或接收Raw数据包)的流逝
	// 时间, 若达到或超过 HeartbeatTimeout, 则发送心跳包.
	// 默认值和上限均为 ReadWriteTimeout/2.
	HeartbeatTimeout time.Duration

	// InactiveTimeout 不活跃超时. 当 Session 属于被动端时, 不活跃超时用于定期检查
	// 连接的活跃状态. 每次 Tick 时会检查最近一次活跃(发送或接收Raw数据包)的流逝
	// 时间, 若达到或超过 InactiveTimeout, 则发送关闭请求.
	// 值不能等于或低于 ReadWriteTimeout.
	InactiveTimeout time.Duration
}

func (c *SessionConfig) init() error {
	if c == nil {
		return errors.New("SessionConfig nil")
	}

	if c.PendingPacketQueueSize <= 0 {
		c.PendingPacketQueueSize = 100
	}

	if c.MaxPacketLength <= 0 || c.MaxPacketLength >= math.MaxInt32 {
		return fmt.Errorf("SessionConfig: MaxPacketLength must > 0 and < %d", math.MaxInt32)
	}

	if c.ReadBufSize <= 0 {
		return errors.New("SessionConfig: ReadBufSize must > 0")
	}

	if c.WriteBufSize <= 0 {
		return errors.New("SessionConfig: WriteBufSize must > 0")
	}

	if c.ReadWriteTimeout <= 0 {
		return errors.New("SessionConfig: ReadWriteTimeout must > 0")
	}

	if c.TickInterval <= 0 {
		c.TickInterval = c.ReadWriteTimeout / 3
	} else if c.TickInterval > c.ReadWriteTimeout/3 {
		return errors.New("SessionConfig: TickInterval must <= ReadWriteTimeout/3")
	}

	if c.HeartbeatTimeout <= 0 {
		c.HeartbeatTimeout = c.ReadWriteTimeout / 2
	} else if c.HeartbeatTimeout > c.ReadWriteTimeout/2 {
		return errors.New("SessionConfig: HeartbeatTimeout must <= ReadWriteTimeout/2")
	}

	if c.TickInterval >= c.HeartbeatTimeout {
		return errors.New("SessionConfig: TickInterval must < HeartbeatTimeout")
	}

	if c.InactiveTimeout <= c.ReadWriteTimeout {
		return errors.New("SessionConfig: InactiveTimeout must > ReadWriteTimeout")
	}

	return nil
}

// session 网络会话.
type session struct {
	svc          *Service    // Service.
	remoteNodeId string      // 远端节点ID.
	activeEnd    bool        // 是否主动的一端（需要发送心跳请求）.
	logger       glog.Logger // 日志工具.

	mutex             sync.RWMutex  // RWMutex for following.
	state             int8          // 状态.
	core              *gnet.Session // 核心实现.
	pendingPackets    chan packet   // 待发送数据包队列.
	chClosed          chan struct{} // 关闭 chan.
	tickTimerId       TimerId       // Tick 定时器ID.
	lastActiveTime    int64         // 最近一次活跃的时间（发送/接收消息）.
	lastHeartbeatTime int64         // 最近一次心跳的时间.
}

// newSession 构造 session.
func newSession(svc *Service, remoteNodeId string, activeEnd bool, conn net.Conn, logger glog.Logger) *session {
	s := &session{
		remoteNodeId:   remoteNodeId,
		svc:            svc,
		activeEnd:      activeEnd,
		pendingPackets: make(chan packet, svc.getSessionConfig().PendingPacketQueueSize),
		state:          stateInit,
		chClosed:       make(chan struct{}),
		logger:         logger.Named("session").WithFields(lfdRemoteNodeId(remoteNodeId), lfdActiveEnd(activeEnd)),
		tickTimerId:    TimerIdNone,
	}

	packetReadWriter := newSessionPacketReadWriter(svc)
	s.core = gnet.NewSession(conn, packetReadWriter, packetReadWriter, s)

	return s
}

// RemoteNodeId 远端节点ID.
func (s *session) RemoteNodeId() string {
	return s.remoteNodeId
}

// lockState 锁定状态.
func (s *session) lockState(need int8, read bool) error {
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
		return ErrSessionNotStarted
	case stateStarted:
		return ErrSessionStarted
	case stateClosed:
		return ErrSessionClosed
	default:
		panic(fmt.Sprintf("invalid session state %d", state))
	}
}

// unlockState 解锁状态.
func (s *session) unlockState(read bool) {
	if read {
		s.mutex.RUnlock()
	} else {
		s.mutex.Unlock()
	}
}

// start 启动会话
func (s *session) start() error {
	if err := s.lockState(stateInit, false); err != nil {
		return err
	}
	defer s.unlockState(false)

	// 启动 core. 若出错，直接关闭 session 并返回错误.
	if err := s.core.Start(); err != nil {
		s.core = nil
		close(s.chClosed)
		s.state = stateClosed
		return err
	}

	s.refreshActiveTime()
	s.tickTimerId = s.svc.startSessionTicker(s)
	s.state = stateStarted

	s.logger.Info("started")

	return nil
}

// close 关闭会话.
func (s *session) close(err error) {
	s.closeBaseLocked(err, false)
}

// closeBaseLocked 基于是否已锁定状态关闭会话.
func (s *session) closeBaseLocked(err error, locked bool) {
	// 若未锁定状态，则先锁定.
	if !locked {
		if err := s.lockState(stateStarted, false); err != nil {
			return
		}
	}

	// 保证重入时逻辑的正确性，优先更新状态.
	// 为了避免死锁，先解锁. 因为关闭 core 时，可能会触发 SessionOnClosed 回调，
	// 既而导致重入 close.
	s.state = stateClosed

	s.unlockState(false)

	// 执行关闭逻辑.
	// 将 core 置为空是为了解除环引用，确保不回造成内存泄漏.
	// 因为 gnet.Session 的实现为了逻辑简单，并未在关闭时
	// 重置 handler.
	_ = s.core.Close()
	s.core = nil
	close(s.chClosed)
	s.svc.stopSessionTicker(s.tickTimerId)
	s.tickTimerId = TimerIdNone

	s.logger.InfoFields("closed", lfdError(err))

	// 通知 Service session 关闭.
	s.svc.onSessionClosed(s, err)
}

// tick Tick 逻辑.
func (s *session) tick() {
	if err := s.lockState(stateStarted, true); err != nil {
		return
	}
	defer s.unlockState(true)

	if s.activeEnd {
		s.tickActiveEnd()
	} else {
		s.tickPassiveEnd()
	}
}

// tickActiveEnd 主动端 Tick 逻辑.
func (s *session) tickActiveEnd() {
	nowNano := time.Now().UnixNano()
	if time.Duration(nowNano-atomic.LoadInt64(&s.lastActiveTime)) < s.svc.getSessionConfig().HeartbeatTimeout {
		s.lastHeartbeatTime = 0
		return
	}

	if s.lastHeartbeatTime != 0 && time.Duration(nowNano-s.lastHeartbeatTime) < s.svc.getSessionConfig().HeartbeatTimeout {
		return
	}

	s.lastHeartbeatTime = nowNano

	s.logger.Debug("send heartbeat")
	p := &heartbeatPacket{}
	p.setPing()
	if err := s.send(context.Background(), p, false); err != nil {
		s.logger.ErrorFields("send heartbeat", lfdError(err))
	}
}

// tickPassiveEnd 被动端 Tick 逻辑.
func (s *session) tickPassiveEnd() {
	// 若会话仍有效, 中断.
	if !s.inactive() {
		return
	}

	// 发送关闭请求.
	if err := s.sendDirect(context.Background(), &closeReqPacket{}, false); err != nil {
		s.logger.ErrorFields("send close req", lfdError(err))
	} else {
		s.logger.Debug("send close req")
	}
}

// send 发送数据底层接口.
func (s *session) send(ctx context.Context, p packet, refreshActiveTime bool) error {
	if ctx == nil {
		return errors.New("context nil")
	}
	if p == nil {
		return errors.New("packet nil")
	}

	if len(p.Data()) > s.svc.getSessionConfig().MaxPacketLength {
		return ErrPacketLengthOverflow
	}

	if err := s.lockState(stateStarted, true); err != nil {
		return err
	}
	defer s.unlockState(true)

	return s.sendDirect(ctx, p, refreshActiveTime)
}

// sendDirect 直接发送数据包.
func (s *session) sendDirect(ctx context.Context, p packet, refreshActiveTime bool) error {
	select {
	case s.pendingPackets <- p:
		if refreshActiveTime {
			s.refreshActiveTime()
		}
		return nil
	case <-s.chClosed:
		return ErrSessionClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SendRaw 发送 Raw 数据包.
func (s *session) SendRaw(ctx context.Context, p *RawPacket) error {
	return s.send(ctx, p, true)
}

// keepActive 保持活跃.
func (s *session) keepActive() error {
	if err := s.lockState(stateStarted, true); err != nil {
		return err
	}
	defer s.unlockState(true)

	s.refreshActiveTime()
	return nil
}

// refreshActiveTime 刷新活跃时间.
func (s *session) refreshActiveTime() {
	activeTime := time.Now().UnixNano()
	for {
		lastActiveTime := atomic.LoadInt64(&s.lastActiveTime)
		if lastActiveTime >= activeTime || atomic.CompareAndSwapInt64(&s.lastActiveTime, lastActiveTime, activeTime) {
			break
		}
	}
}

// inactive 返回 session 是否失效.
func (s *session) inactive() bool {
	return time.Duration(time.Now().UnixNano()-atomic.LoadInt64(&s.lastActiveTime)) >= s.svc.getSessionConfig().InactiveTimeout
}

// SessionPendingPacket 实现 gnet.SessionHandler. 返回待发送的数据包.
func (s *session) SessionPendingPacket() (p gnet.Packet, more bool, err error) {
	select {
	case p := <-s.pendingPackets:
		if p == nil {
			return nil, false, ErrSessionClosed
		} else {
			return p, len(s.pendingPackets) > 0, nil
		}
	case <-s.chClosed:
		return nil, false, ErrSessionClosed
	}
}

// SessionOnPacket 实现 gnet.SessionHandler. 接收数据包回调.
func (s *session) SessionOnPacket(_ *gnet.Session, p gnet.Packet) error {
	pp, ok := p.(packet)
	if !ok {
		return fmt.Errorf("session on packet, unknown packet type %T", p)
	}

	handler, exists := sessionPacketHandlers[pp.protoType()]
	if !exists {
		return fmt.Errorf("session on packet, unknown proto-type %d", pp.protoType())
	}

	return handler(s, pp)
}

// SessionOnClosed 实现 gnet.SessionHandler. 连接关闭回调.
func (s *session) SessionOnClosed(_ *gnet.Session, err error) {
	s.close(err)
}

// sessionLocal 本地会话
// 为了便于使用同一套工作流通信，对本地会话做一套封装，
// 便于向本地发送数据，实现数据转发.
type sessionLocal struct {
	svc *Service
}

func newSessionLocal(svc *Service) *sessionLocal {
	return &sessionLocal{
		svc: svc,
	}
}

func (s *sessionLocal) RemoteNodeId() string {
	return s.svc.NodeId()
}

func (s *sessionLocal) SendRaw(_ context.Context, p *RawPacket) error {
	return s.svc.onSessionPacket(s, p)
}

func (s *sessionLocal) start() error {
	return nil
}

func (s *sessionLocal) close(_ error) {}

func (s *sessionLocal) keepActive() error {
	return nil
}

func (s *sessionLocal) tick() {}
