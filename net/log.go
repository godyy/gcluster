package net

import (
	"net"

	"github.com/godyy/glog"
	"go.uber.org/zap"
)

// createStdLogger 创建面前表顺输出的 logger.
func createStdLogger(level glog.Level) glog.Logger {
	return glog.NewLogger(&glog.Config{
		Level:        level,
		EnableCaller: true,
		CallerSkip:   0,
		Development:  true,
		Cores:        []glog.CoreConfig{glog.NewStdCoreConfig()},
	}).Named("net")
}

func lfdError(err error) zap.Field {
	return zap.NamedError("error", err)
}

func lfdRemoteNodeId(nodeId string) zap.Field {
	return zap.String("remoteNodeId", nodeId)
}

func lfdRemoteAddr(addr string) zap.Field {
	return zap.String("remoteAddr", addr)
}

func lfdNetRemoteAddr(addr net.Addr) zap.Field {
	return zap.Any("remoteAddr", addr)
}

func lfdNodeId(nodeId string) zap.Field {
	return zap.String("nodeId", nodeId)
}

func lfdReceivedNodeId(nodeId string) zap.Field {
	return zap.String("receivedNodeId", nodeId)
}

func lfdReason(reason string) zap.Field {
	return zap.String("reason", reason)
}

func lfdProtoTypeNumber(protoType protoType) zap.Field {
	return zap.Int8("protoType", int8(protoType))
}

func lfdActiveState(state int8) zap.Field {
	return zap.Int8("activeState", state)
}

func lfdPassiveState(state int8) zap.Field {
	return zap.Int8("passiveState", state)
}

func lfdActiveEnd(activeEnd bool) zap.Field {
	return zap.Bool("activeEnd", activeEnd)
}
