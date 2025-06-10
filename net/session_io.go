package net

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/godyy/gnet"
	"github.com/godyy/gutils/buffer"
	"github.com/godyy/gutils/buffer/bytes"
	pkgerrors "github.com/pkg/errors"
)

// sessionPacketReadWriter 实现数据包的读写功能.
type sessionPacketReadWriter struct {
	svc                 *Service           // Service.
	readBuffer          *bytes.FixedBuffer // 读取缓冲区.
	writeBuffer         *bytes.FixedBuffer // 发送缓冲区.
	batchWriteCount     int                // 批量发送计数.
	batchWriteStartTime time.Time          // 批量发送开始时间.
}

// newSessionPacketReadWriter 创建 sessionPacketReadWriter.
func newSessionPacketReadWriter(svc *Service) *sessionPacketReadWriter {
	sc := svc.getSessionConfig()
	return &sessionPacketReadWriter{
		svc:         svc,
		readBuffer:  bytes.NewFixedBuffer(sc.ReadBufSize),
		writeBuffer: bytes.NewFixedBuffer(sc.WriteBufSize),
	}
}

// readToBuffer 读取数据到缓冲区.
func (rw *sessionPacketReadWriter) readToBuffer(cr gnet.ConnReader) error {
	if err := cr.SetReadDeadline(time.Now().Add(rw.svc.getSessionConfig().ReadWriteTimeout)); err != nil {
		return pkgerrors.WithMessage(err, "set read deadline")
	}
	_, err := rw.readBuffer.ReadFrom(cr)
	return err
}

// readFull 自 cr 读取足够 p 长度的数据.
func (rw *sessionPacketReadWriter) readFull(cr gnet.ConnReader, p []byte) error {
	for len(p) > 0 {
		n, err := rw.readBuffer.Read(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if err := rw.readToBuffer(cr); err != nil {
					return err
				}
				continue
			}
			return err
		}
	}
	return nil
}

// SessionReadPacket 实现 gnet.SessionPacketReadWriter.
func (rw *sessionPacketReadWriter) SessionReadPacket(cr gnet.ConnReader) (gnet.Packet, error) {
	// head.
	var head packetHead
	if err := rw.readFull(cr, head[:]); err != nil {
		return nil, pkgerrors.WithMessage(err, "session read packet head")
	}
	protoType := head.protoType()
	payloadLen := head.payloadLen()

	// 获取协议类型对应的处理函数.
	handler, ok := sessionReadPacketHandlers[protoType]
	if !ok {
		return nil, fmt.Errorf("session read packet, unknown proto-type %d", protoType)
	}

	return handler(rw, cr, payloadLen)
}

// writeFromBuffer 从缓冲区发送数据.
func (rw *sessionPacketReadWriter) writeFromBuffer(cw gnet.ConnWriter) error {
	if err := cw.SetWriteDeadline(time.Now().Add(rw.svc.getSessionConfig().ReadWriteTimeout)); err != nil {
		return pkgerrors.WithMessage(err, "set writeFull deadline")
	}
	for rw.writeBuffer.Readable() > 0 {
		if _, err := rw.writeBuffer.WriteTo(cw); err != nil {
			return err
		}
	}
	rw.batchWriteCount = 0
	rw.batchWriteStartTime = time.Time{}
	return nil
}

// writeFull 将 p 通过 cw 完整的发送出去.
func (rw *sessionPacketReadWriter) writeFull(cw gnet.ConnWriter, p []byte) error {
	if rw.batchWriteStartTime.IsZero() {
		rw.batchWriteStartTime = time.Now()
	}

	for len(p) > 0 {
		n, err := rw.writeBuffer.Write(p)
		if n > 0 {
			p = p[n:]
			if len(p) == 0 {
				rw.batchWriteCount++
			}
		}
		if errors.Is(err, buffer.ErrBufferFull) {
			if err = rw.writeFromBuffer(cw); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if rw.batchWriteCount >= rw.svc.getSessionConfig().BatchWriteLimit ||
			time.Since(rw.batchWriteStartTime) >= rw.svc.getSessionConfig().BatchWriteTimeLmit {
			if err = rw.writeFromBuffer(cw); err != nil {
				return err
			}
		}
	}
	return nil
}

// SessionWritePacket 实现 gnet.SessionPacketReadWriter.
func (rw *sessionPacketReadWriter) SessionWritePacket(cw gnet.ConnWriter, p gnet.Packet, more bool) error {
	pp, ok := p.(packet)
	if !ok {
		return fmt.Errorf("session write packet, unknown packet type %T", p)
	}

	// 检查协议类型.
	if !protoTypesForCommunicating[pp.protoType()] {
		return fmt.Errorf("session write packet, unknown proto-type %d", pp.protoType())
	}

	// 若是 Raw 数据包, 执行回收逻辑.
	if pp.protoType() == protoTypeRaw {
		defer rw.svc.putRawPacket(pp.(*RawPacket))
	}

	// payload.
	payload := pp.Data()
	payloadLen := uint32(len(payload))

	// head.
	var head packetHead
	head.setProtoType(pp.protoType())
	head.setPayloadLen(payloadLen)
	if err := rw.writeFull(cw, head[:]); err != nil {
		return pkgerrors.WithMessage(err, "session writeFull packet head")
	}

	// write payload.
	if payloadLen > 0 {
		if err := rw.writeFull(cw, payload); err != nil {
			return pkgerrors.WithMessage(err, "session writeFull packet payload")
		}
	}

	if !more && rw.writeBuffer.Readable() > 0 {
		if err := rw.writeFromBuffer(cw); err != nil {
			return pkgerrors.WithMessage(err, "session writeFull from buffer")
		}
	}

	return nil
}

// sessionReadPacketHandlers session 读取数据包处理函数.
var sessionReadPacketHandlers = map[protoType]func(rw *sessionPacketReadWriter, cr gnet.ConnReader, payloadLen uint32) (gnet.Packet, error){
	protoTypeHeartbeat: sessionReadPacketHeartbeat,
	protoTypeCloseReq:  sessionReadPacketCloseReq,
	protoTypeCloseResp: sessionReadPacketCloseResp,
	protoTypeRaw:       sessionReadPacketRaw,
}

// sessionReadPacketHeartbeat 读取心跳数据包.
func sessionReadPacketHeartbeat(rw *sessionPacketReadWriter, cr gnet.ConnReader, payloadLen uint32) (gnet.Packet, error) {
	if payloadLen != heartbeatPacketLength {
		return nil, fmt.Errorf("session read packet, heartbeat packet length %d wrong", payloadLen)
	}
	p := &heartbeatPacket{}
	if err := rw.readFull(cr, p[:]); err != nil {
		return nil, pkgerrors.WithMessage(err, "session read heartbeat packet payload")
	}
	return p, nil
}

// sessionReadPacketCloseReq 读取关闭请求数据包.
func sessionReadPacketCloseReq(rw *sessionPacketReadWriter, cr gnet.ConnReader, payloadLen uint32) (gnet.Packet, error) {
	if payloadLen != closeReqPacketLength {
		return nil, fmt.Errorf("session read packet, close req packet length %d wrong", payloadLen)
	}
	return &closeReqPacket{}, nil
}

// sessionReadPacketCloseResp 读取关闭回复数据包.
func sessionReadPacketCloseResp(rw *sessionPacketReadWriter, cr gnet.ConnReader, payloadLen uint32) (gnet.Packet, error) {
	if payloadLen != closeRespPacketLength {
		return nil, fmt.Errorf("session read packet, close resp packet length %d wrong", payloadLen)
	}
	p := &closeRespPacket{}
	if err := rw.readFull(cr, p[:]); err != nil {
		return nil, pkgerrors.WithMessage(err, "session read close resp packet payload")
	}
	return p, nil
}

// sessionReadPacketRaw 读取 Raw 数据包.
func sessionReadPacketRaw(rw *sessionPacketReadWriter, cr gnet.ConnReader, payloadLen uint32) (gnet.Packet, error) {
	if payloadLen > uint32(rw.svc.getSessionConfig().MaxPacketLength) {
		return nil, fmt.Errorf("session read raw packet, packet length %d over limited", payloadLen)
	}
	p := rw.svc.getRawPacket(int(payloadLen))
	if err := rw.readFull(cr, p.Data()); err != nil {
		return nil, pkgerrors.WithMessage(err, "session read raw packet body")
	}
	return p, nil
}
