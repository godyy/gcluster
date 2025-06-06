package net

import "github.com/godyy/glog"

// ServiceOption SessionManager 选项.
type ServiceOption func(*Service)

// WithServicePacketManager PacketManager 选项.
func WithServicePacketManager(pm PacketManager) ServiceOption {
	return func(s *Service) {
		s.pm = pm
	}
}

// WithServiceLogger 日志工具选项.
func WithServiceLogger(logger glog.Logger) ServiceOption {
	return func(s *Service) {
		s.setLogger(logger.Named("net"))
	}
}
