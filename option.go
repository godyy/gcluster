package gcluster

import "github.com/godyy/gcluster/net"

// optionSet 选项集合.
type optionSet struct {
	smOptions []net.SessionManagerOption // SessionManager 选项.
}

// Option 选项.
type Option func(*optionSet)

// WithSessionManagerOptions SessionManager 选项.
func WithSessionManagerOptions(smOptions ...net.SessionManagerOption) Option {
	return func(opts *optionSet) {
		opts.smOptions = smOptions
	}
}
