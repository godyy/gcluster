package net

import (
	"errors"
)

// ErrStarted 已启动.
var ErrStarted = errors.New("started")

// ErrNotStarted 未启动.
var ErrNotStarted = errors.New("not started")

// ErrClosed 已关闭.
var ErrClosed = errors.New("closed")

// ErrInactiveClosed 因不活跃而关闭.
var ErrInactiveClosed = errors.New("inactive closed")

// ErrPacketLengthOverflow 数据包长度溢出.
var ErrPacketLengthOverflow = errors.New("packet length overflow")

// ErrConnectRemote 连接远端失败.
var ErrConnectRemote = errors.New("connect remote failed")

// ErrRequestHandshake 请求握手失败.
var ErrRequestHandshake = errors.New("request handshake failed")

// ErrReplyHandshake 回复握手失败.
var ErrReplyHandshake = errors.New("reply handshake failed")

// ErrReadHandshakeResp 读取握手响应失败.
var ErrReadHandshakeResp = errors.New("read handshake response failed")

// ErrHandshakeRejected 握手被拒绝.
var ErrHandshakeRejected = errors.New("handshake rejected")

// ErrRemoteIdOrTokenWrong 远端id或token错误.
var ErrRemoteIdOrTokenWrong = errors.New("remote id or token wrong")

// ErrActiveHandshakeEarlier 主动握手更早.
var ErrActiveHandshakeEarlier = errors.New("active handshake earlier")

// ErrPassiveHandshakeEarlier 被动握手更早.
var ErrPassiveHandshakeEarlier = errors.New("passive handshake earlier")
