package net

import (
	"context"
	"fmt"
)

// sessionPacketHandlers session 数据包处理函数.
var sessionPacketHandlers = map[protoType]func(s *session, p packet) error{
	protoTypeHeartbeat: wrapSessionPacketHandler(sessionHandleHeartbeat),
	protoTypeCloseReq:  wrapSessionPacketHandler(sessionHandleCloseReq),
	protoTypeCloseResp: wrapSessionPacketHandler(sessionHandleCloseResp),
	protoTypeRaw:       wrapSessionPacketHandler(sessionHandleRaw),
}

// wrapSessionPacketHandler 包装 session 数据包处理函数.
func wrapSessionPacketHandler[P packet](h func(s *session, p P) error) func(s *session, p packet) error {
	return func(s *session, p packet) error {
		pp, ok := p.(P)
		if !ok {
			return fmt.Errorf("session packet handler, unknown packet type %T", p)
		}
		return h(s, pp)
	}
}

// sessionHandleHeartbeat 处理心跳包.
func sessionHandleHeartbeat(s *session, p *heartbeatPacket) error {
	ping := p.ping()
	if ping {
		s.logger.Debug("handle ping")
		p.setPong()
		return s.send(context.Background(), p, false)
	} else {
		s.logger.Debug("receive pong")
	}

	return nil
}

// sessionHandleCloseReq 处理关闭请求包.
func sessionHandleCloseReq(s *session, p *closeReqPacket) error {
	// 锁定状态.
	if err := s.lockState(stateStarted, false); err != nil {
		s.logger.ErrorFields("handler close req, lock state failed", lfdError(err))
		return nil
	}

	// 若 session 未失效, 回复对端连接不能关闭.
	if !s.inactive() {
		defer s.unlockState(false)
		s.logger.Info("handle close req but not inactive")
		resp := &closeRespPacket{}
		resp.setPass(false)
		_ = s.sendDirect(context.Background(), resp, false)
		return nil
	}

	// session 失效, 关闭.
	s.logger.Debug("close inactive")
	s.closeBaseLocked(ErrInactiveClosed, true)
	return nil
}

// sessionHandleCloseResp 处理关闭回复包.
func sessionHandleCloseResp(s *session, p *closeRespPacket) error {
	// 关闭请求未通过.
	if !p.pass() {
		s.logger.Debug("handle close resp but not pass")
		return nil
	}

	// 关闭 session.
	s.close(ErrInactiveClosed)
	return nil
}

// sessionHandleRaw 处理 Raw 包.
func sessionHandleRaw(s *session, p *RawPacket) error {
	s.mutex.RLock()
	s.refreshActiveTime()
	s.mutex.RUnlock()
	return s.sm.onSessionPacket(s, p)
}
