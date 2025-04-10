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

// RawPacket 定义供用户使用的 Raw 数据包.
type RawPacket struct {
	gnet.Buffer
}

func (p *RawPacket) protoType() protoType {
	return protoTypeRaw
}

// NewRawPacketWithSize 创建指定大小的 RawPacket.
func NewRawPacketWithSize(size int) *RawPacket {
	p := &RawPacket{}
	p.SetBuf(make([]byte, size))
	return p
}

// NewRawPacketWithCap 创建指定容量的 RawPacket.
func NewRawPacketWithCap(cap int) *RawPacket {
	p := &RawPacket{}
	p.SetBuf(make([]byte, 0, cap))
	return p
}

// PacketManager 数据包管理器. 负责分配和回收数据包.
type PacketManager interface {
	// GetRawPacket 获取指定大小的 RawPacket.
	GetRawPacket(size int) *RawPacket

	// PutRawPacket 回收 RawPacket.
	PutRawPacket(p *RawPacket)
}
