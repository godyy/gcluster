package net

// SessionManagerOption SessionManager 选项.
type SessionManagerOption func(SessionManager)

// WithPacketManager PacketManager 选项.
func WithPacketManager(pm PacketManager) SessionManagerOption {
	return func(sm SessionManager) {
		sm.base().pm = pm
	}
}
