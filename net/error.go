package net

import (
	"errors"

	pkg_errors "github.com/pkg/errors"
)

// ErrStarted 已启动.
var ErrStarted = errors.New("cluster/net: started")

// ErrNotStarted 未启动.
var ErrNotStarted = errors.New("cluster/net: not started")

// ErrClosed 已关闭.
var ErrClosed = errors.New("cluster/net: closed")

// ErrInactiveClosed 因不活跃而关闭.
var ErrInactiveClosed = pkg_errors.New("cluster/net: inactive closed")

// ErrPacketLengthOverflow 数据包长度溢出.
var ErrPacketLengthOverflow = errors.New("cluster/net: packet length overflow")

// ErrConnectRemote 连接远端失败.
var ErrConnectRemote = errors.New("cluster/net: connect remote failed")

// ErrRequestHandshake 请求握手失败.
var ErrRequestHandshake = errors.New("cluster/net: request handshake failed")

// ErrReplyHandshake 回复握手失败.
var ErrReplyHandshake = errors.New("cluster/net: reply handshake failed")

// ErrReadHandshakeResp 读取握手响应失败.
var ErrReadHandshakeResp = errors.New("cluster/net: read handshake response failed")

// ErrHandshakeRejected 握手被拒绝.
var ErrHandshakeRejected = errors.New("cluster/net: handshake rejected")

// ErrRemoteIdOrTokenWrong 远端id或token错误.
var ErrRemoteIdOrTokenWrong = errors.New("cluster/net: remote id or token wrong")

// ErrActiveHandshakeEarlier 主动握手更早.
var ErrActiveHandshakeEarlier = errors.New("cluster/net: active handshake earlier")

// ErrPassiveHandshakeEarlier 被动握手更早.
var ErrPassiveHandshakeEarlier = errors.New("cluster/net: passive handshake earlier")
