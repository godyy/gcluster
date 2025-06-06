package net

// protoType 协议类型类型.
type protoType int8

// 协议类型枚举值.
const (
	protoTypeUnknown = protoType(0)

	// ==============================以下是内部协议===================================
	protoTypeHSApply        = protoType(1)  // 握手请求
	protoTypeHSAccepted     = protoType(2)  // 握手同意
	protoTypeHSCompleted    = protoType(3)  // 握手完成
	protoTypeHSCompletedAck = protoType(4)  // 握手完成确认
	protoTypeHSRejected     = protoType(5)  // 握手被拒绝
	protoTypeHeartbeat      = protoType(10) // 心跳
	protoTypeCloseReq       = protoType(11) // 关闭请求
	protoTypeCloseResp      = protoType(12) // 关闭回复

	// ==============================以下是用户协议===================================
	protoTypeRaw = protoType(20) // 原生消息，由应用自行处理
)

// protoTypeStrings 协议类型字符串值.
var protoTypeStrings = map[protoType]string{
	protoTypeUnknown:        "Unknown",
	protoTypeHSApply:        "Handshake Apply",
	protoTypeHSAccepted:     "Handshake Accepted",
	protoTypeHSCompleted:    "Handshake Completed",
	protoTypeHSCompletedAck: "Handshake Completed Ack",
	protoTypeHSRejected:     "Handshake Rejected",
	protoTypeHeartbeat:      "Heartbeat",
	protoTypeCloseReq:       "Close Request",
	protoTypeCloseResp:      "Close Response",
	protoTypeRaw:            "Raw",
}

func (pt protoType) String() string {
	return protoTypeStrings[pt]
}

// protoTypesForCommunicating 通信协议类型.
var protoTypesForCommunicating = map[protoType]bool{
	protoTypeHeartbeat: true,
	protoTypeCloseReq:  true,
	protoTypeCloseResp: true,
	protoTypeRaw:       true,
}
