package net

import (
	"net"
	"testing"
	"time"

	"github.com/godyy/gutils/log"
)

type testClientHandler struct {
	onSessionPacket func(Session, *RawPacket) error
}

func (h *testClientHandler) OnSessionPacket(s Session, rawPacket *RawPacket) error {
	if h.onSessionPacket != nil {
		return h.onSessionPacket(s, rawPacket)
	}
	return nil
}

func TestClientConnect(t *testing.T) {
	clientId := "client"
	serviceId := "service"
	serviceAddr := ":50001"

	logger := log.NewLogger(&log.Config{
		Level:        log.DebugLevel,
		EnableCaller: true,
		CallerSkip:   0,
		Development:  true,
		Cores:        []log.CoreConfig{log.NewStdCoreConfig()},
	})

	sessionCfg := SessionConfig{
		PendingPacketQueueSize: 10,
		MaxPacketLength:        1024,
		ReadWriteTimeout:       60 * time.Second,
		HeartbeatInterval:      1 * time.Second,
		InactiveTimeout:        5 * time.Minute,
		ReadBufSize:            10 * 1024,
		WriteBufSize:           10 * 1024,
	}

	dialer := func(addr string) (net.Conn, error) {
		return net.Dial("tcp", addr)
	}

	createListener := func(addr string) (net.Listener, error) {
		return net.Listen("tcp", addr)
	}

	testHandler := &testClientHandler{}
	testServiceHandler := &testServiceHandler{}

	clientCfg := &ClientConfig{
		NodeId: clientId,
		Handshake: HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session: sessionCfg,
		Dialer:  dialer,
	}

	serviceCfg := &ServiceConfig{
		NodeId: serviceId,
		Addr:   serviceAddr,
		Handshake: HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionCfg,
		Dialer:          dialer,
		ListenerCreator: createListener,
	}

	client, err := CreateClient(clientCfg, testHandler, WithLogger(logger))
	if err != nil {
		t.Fatalf("create client, %v", err)
	}
	service, err := CreateService(serviceCfg, testServiceHandler, WithLogger(logger))
	if err != nil {
		t.Fatalf("create service, %v", err)
	}

	if err := client.Start(); err != nil {
		t.Fatalf("client start failed: %v", err)
	}

	if err := service.Start(); err != nil {
		t.Fatalf("service start failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	if _, err := client.Connect(serviceId, serviceAddr); err != nil {
		t.Fatal("client connect service", err)
	}

	time.Sleep(6 * time.Second)

	_ = client.Close()
	_ = service.Close()
}
