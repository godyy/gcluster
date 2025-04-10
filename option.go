package gcluster

import (
	"github.com/godyy/gcluster/net"
	"github.com/godyy/gutils/log"
)

// optionSet 选项集合.
type optionSet struct {
	smOptions []net.SessionManagerOption // SessionManager 选项.
}

// Option 选项.
type Option func(*optionSet)

// WithLogger 日志工具选项.
func WithLogger(logger log.Logger) Option {
	return func(opts *optionSet) {
		opts.smOptions = append(opts.smOptions, net.WithLogger(logger.Named("gcluster")))
	}
}

// WithSessionManagerOptions SessionManager 选项.
func WithSessionManagerOptions(smOptions ...net.SessionManagerOption) Option {
	return func(opts *optionSet) {
		opts.smOptions = append(opts.smOptions, smOptions...)
	}
}
