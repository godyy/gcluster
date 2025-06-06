package net

import (
	"errors"
)

// ErrServiceNotStarted 服务未启动.
var ErrServiceNotStarted = errors.New("service not started")

// ErrServiceStarted 服务已启动.
var ErrServiceStarted = errors.New("service started")

// ErrServiceClosed 服务已关闭.
var ErrServiceClosed = errors.New("service closed")

// ErrSessionNotStarted 会话未启动.
var ErrSessionNotStarted = errors.New("session not started")

// ErrSessionStarted 会话已启动.
var ErrSessionStarted = errors.New("session started")

// ErrSessionClosed 会话已关闭.
var ErrSessionClosed = errors.New("session closed")

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
