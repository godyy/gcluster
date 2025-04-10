package net

import "github.com/godyy/gutils/log"

// SessionManagerOption SessionManager 选项.
type SessionManagerOption func(sessionManagerImpl)

// WithPacketManager PacketManager 选项.
func WithPacketManager(pm PacketManager) SessionManagerOption {
	return func(sm sessionManagerImpl) {
		sm.base().pm = pm
	}
}

// WithLogger 日志工具选项.
func WithLogger(logger log.Logger) SessionManagerOption {
	return func(sm sessionManagerImpl) {
		sm.setLogger(logger.Named("net"))
	}
}
