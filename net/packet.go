package net

import (
	"encoding/binary"

	"github.com/godyy/gnet"
)

// packetHead 数据包包头结构定义.
type packetHead [6]byte

func (ph *packetHead) protoType() protoType {
	return protoType((*ph)[0])
}

func (ph *packetHead) setProtoType(pt protoType) {
	(*ph)[0] = byte(pt)
}

func (ph *packetHead) payloadLen() uint32 {
	return binary.BigEndian.Uint32((*ph)[2:])
}

func (ph *packetHead) setPayloadLen(n uint32) {
	binary.BigEndian.PutUint32((*ph)[2:], n)
}

// packet 数据包接口封装.
type packet interface {
	gnet.Packet
	protoType() protoType
}

// heartbeatPacketLength 心跳包的长度.
const heartbeatPacketLength = 1

type heartbeatPacket [heartbeatPacketLength]byte

func (p *heartbeatPacket) Data() []byte {
	return (*p)[:]
}

func (p *heartbeatPacket) protoType() protoType {
	return protoTypeHeartbeat
}

func (p *heartbeatPacket) ping() bool {
	return (*p)[0] != 0
}

func (p *heartbeatPacket) setPing() {
	(*p)[0] = 1
}

func (p *heartbeatPacket) setPong() {
	(*p)[0] = 0
}

// closeReqPacketLength 关闭请求数据包的长度.
const closeReqPacketLength = 0

// closeReqPacket 关闭请求数据包.
type closeReqPacket []byte

func (p *closeReqPacket) Data() []byte {
	return nil
}

func (p *closeReqPacket) protoType() protoType {
	return protoTypeCloseReq
}

// closeRespPacketLength 关闭回复数据包的长度.
const closeRespPacketLength = 1

// closeRespPacket 关闭回复数据包.
type closeRespPacket [1]byte

func (p *closeRespPacket) Data() []byte {
	return (*p)[:]
}

func (p *closeRespPacket) protoType() protoType {
	return protoTypeCloseResp
}

func (p *closeRespPacket) setPass(pass bool) {
	if pass {
		(*p)[0] = 1
	} else {
		(*p)[0] = 0
	}
}

func (p *closeRespPacket) pass() bool {
	return (*p)[0] != 0
}

// rawPacket 定义 Raw 数据包.
type rawPacket []byte

func (p rawPacket) Data() []byte {
	return p
}

func (p rawPacket) protoType() protoType {
	return protoTypeRaw
}
