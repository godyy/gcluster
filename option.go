package gcluster

import (
	"github.com/godyy/gcluster/net"
	"github.com/godyy/glog"
)

// optionSet 选项集合.
type optionSet struct {
	smOptions []net.ServiceOption // SessionManager 选项.
}

// Option 选项.
type Option func(*optionSet)

// WithLogger 日志工具选项.
func WithLogger(logger glog.Logger) Option {
	return func(opts *optionSet) {
		opts.smOptions = append(opts.smOptions, net.WithServiceLogger(logger.Named("gcluster")))
	}
}

// WithSessionManagerOptions SessionManager 选项.
func WithSessionManagerOptions(smOptions ...net.ServiceOption) Option {
	return func(opts *optionSet) {
		opts.smOptions = append(opts.smOptions, smOptions...)
	}
}
