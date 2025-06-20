package net

import "github.com/godyy/glog"

// ServiceOption SessionManager 选项.
type ServiceOption func(*Service)

// WithServiceBytesManager BytesManager 选项.
func WithServiceBytesManager(bm BytesManager) ServiceOption {
	return func(s *Service) {
		s.bm = bm
	}
}

// WithServiceLogger 日志工具选项.
func WithServiceLogger(logger glog.Logger) ServiceOption {
	return func(s *Service) {
		s.setLogger(logger.Named("net"))
	}
}
