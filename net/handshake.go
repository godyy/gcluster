package net

import (
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"time"

	"github.com/godyy/gcluster/net/internal/protocol/pb"
	pkgerrors "github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

// HandshakeConfig 握手配置.
type HandshakeConfig struct {
	// Token 建立连接时，确认彼此是否合法.
	Token string

	// Timeout 建立连接进行握手过程时的读写超时时间.
	Timeout time.Duration
}

func (c *HandshakeConfig) init() error {
	if c == nil {
		return errors.New("HandshakeConfig nil")
	}

	if c.Token == "" {
		return errors.New("HandshakeConfig: Token empty")
	}

	if c.Timeout <= 0 {
		return errors.New("HandshakeConfig: Timeout must > 0")
	}

	return nil
}

// _handshakeHelper 握手助手.
type _handshakeHelper struct {
	protoPbType map[protoType]reflect.Type // 协议类型到pb类型映射.
	pbProtoType map[reflect.Type]protoType // pb类型到协议类型映射.
}

var handshakeHelper = &_handshakeHelper{
	protoPbType: make(map[protoType]reflect.Type),
	pbProtoType: make(map[reflect.Type]protoType),
}

// init 初始化，负责注册协议对应的pb.
func (m *_handshakeHelper) init() {
	m.regProtoPb(protoTypeHSApply, &pb.HSApply{})
	m.regProtoPb(protoTypeHSAccepted, &pb.HSAccepted{})
	m.regProtoPb(protoTypeHSCompleted, &pb.HSCompleted{})
	m.regProtoPb(protoTypeHSRejected, &pb.HSRejected{})
	m.regProtoPb(protoTypeHSCompletedAck, &pb.HSCompletedAck{})
}

// regProtoPb 注册协议对应的pb.
func (m *_handshakeHelper) regProtoPb(pt protoType, p proto.Message) {
	if pbType, ok := m.protoPbType[pt]; ok {
		panic(fmt.Sprintf("proto-type \"%s\" -> pb-type \"%s\" already exists", pt, pbType.Name()))
	}

	pbType := reflect.TypeOf(p)
	if protoType, ok := m.pbProtoType[pbType]; ok {
		panic(fmt.Sprintf("pb-type \"%s\" -> proto-type \"%s\" already exists", pbType.Name(), protoType))
	}

	m.protoPbType[pt] = pbType
	m.pbProtoType[pbType] = pt
}

// createPb 根据协议类型创建对应的pb.
func (m *_handshakeHelper) createPb(pt protoType) (proto.Message, bool) {
	pbType, ok := m.protoPbType[pt]
	if !ok {
		return nil, false
	}
	return reflect.New(pbType.Elem()).Interface().(proto.Message), true
}

// getProtoType 根据pb获取协议类型.
func (m *_handshakeHelper) getProtoType(p proto.Message) (protoType, bool) {
	if p == nil {
		return protoTypeUnknown, false
	}
	protoType, ok := m.pbProtoType[reflect.TypeOf(p)]
	return protoType, ok
}

// readProto 接收proto逻辑封装.
func (m *_handshakeHelper) readProto(conn net.Conn, timeout time.Duration) (proto.Message, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	// head.
	var head packetHead
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return nil, pkgerrors.WithMessage(err, "read head")
	}
	protoType := head.protoType()
	payloadLen := head.payloadLen()

	// create proto by proto-type.
	p, ok := m.createPb(protoType)
	if !ok || p == nil {
		return nil, fmt.Errorf("wrong proto-type %d", protoType)
	}

	// decode proto.
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, pkgerrors.WithMessagef(err, "proto-type \"%s\" read payload", protoType)
	}
	if err := proto.Unmarshal(payload, p); err != nil {
		return nil, pkgerrors.WithMessagef(err, "proto-type \"%s\" decode payload", protoType)
	}

	return p, nil
}

// writeProto 发送proto逻辑封装.
func (m *_handshakeHelper) writeProto(conn net.Conn, p proto.Message, timeout time.Duration) error {
	protoType, valid := m.getProtoType(p)
	if !valid {
		return fmt.Errorf("wrong pb-type \"%s\"", reflect.TypeOf(p).Elem().Name())
	}

	// encode proto.
	payload, err := proto.Marshal(p)
	if err != nil {
		return pkgerrors.WithMessagef(err, "proto-type \"%s\" encode payload", protoType)
	}

	_ = conn.SetWriteDeadline(time.Now().Add(timeout))

	// head
	var head packetHead
	head.setProtoType(protoType)
	head.setPayloadLen(uint32(len(payload)))
	if _, err := conn.Write(head[:]); err != nil {
		return pkgerrors.WithMessage(err, "writeFull head")
	}

	// payload
	if _, err := conn.Write(payload); err != nil {
		return pkgerrors.WithMessage(err, "writeFull payload")
	}

	return nil
}

// writeApply 发送握手请求.
func (m *_handshakeHelper) writeApply(conn net.Conn, nodeId string, token string, ts int64, timeout time.Duration) error {
	return handshakeHelper.writeProto(
		conn,
		&pb.HSApply{
			NodeId: nodeId,
			Token:  token,
			Time:   ts,
		},
		timeout,
	)
}

// writeAccepted 发送握手被接受.
func (m *_handshakeHelper) writeAccepted(conn net.Conn, nodeId string, token string, timeout time.Duration) error {
	return handshakeHelper.writeProto(
		conn,
		&pb.HSAccepted{NodeId: nodeId, Token: token},
		timeout,
	)
}

// writeCompleted 发送握手完成.
func (m *_handshakeHelper) writeCompleted(conn net.Conn, timeout time.Duration) error {
	return handshakeHelper.writeProto(
		conn,
		&pb.HSCompleted{},
		timeout,
	)
}

// writeRejected 发送握手被拒绝.
func (m *_handshakeHelper) writeRejected(conn net.Conn, reason pb.HSRejectedReason, timeout time.Duration) error {
	return handshakeHelper.writeProto(
		conn,
		&pb.HSRejected{Reason: reason},
		timeout,
	)
}

// writeCompletedAck 发送握手完成确认.
func (m *_handshakeHelper) writeCompletedAck(conn net.Conn, timeout time.Duration) error {
	return handshakeHelper.writeProto(
		conn,
		&pb.HSCompletedAck{},
		timeout,
	)
}

// readHandshakeProto 读取握手协议.
func readHandshakeProto[Pt proto.Message](conn net.Conn, timeout time.Duration) (v Pt, err error) {
	var (
		p  proto.Message
		ok bool
	)

	if p, err = handshakeHelper.readProto(conn, timeout); err != nil {
		return
	}

	v, ok = p.(Pt)
	if !ok {
		pt, _ := handshakeHelper.getProtoType(p)
		err = fmt.Errorf("wrong proto-type \"%s\"", pt)
	}

	return
}

func init() {
	handshakeHelper.init()
}
