package net

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/godyy/gutils/log"
)

type testServiceHandler struct {
	onNewSession    func(Session)
	onSessionPacket func(Session, *RawPacket) error
	onSessionClosed func(Session, error)
}

func (h *testServiceHandler) OnNewSession(s Session) {
	if h.onNewSession != nil {
		h.onNewSession(s)
	}
}

func (h *testServiceHandler) OnSessionPacket(s Session, data *RawPacket) error {
	if h.onSessionPacket != nil {
		return h.onSessionPacket(s, data)
	}
	return nil
}

func (h *testServiceHandler) OnSessionClosed(s Session, err error) {
	if h.onSessionClosed != nil {
		h.onSessionClosed(s, err)
	}
}

type testListener struct {
	net.Listener
	accept func(conn net.Conn)
}

func (t *testListener) Accept() (net.Conn, error) {
	conn, err := t.Listener.Accept()
	if err != nil {
		return nil, err
	}
	t.accept(conn)
	return conn, nil
}

func TestServiceConnect(t *testing.T) {
	nodeId1 := "node1"
	nodeId2 := "node2"
	s1Addr := ":50001"
	s2Addr := ":50002"

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

	testHandler := &testServiceHandler{}

	s1Cfg := &ServiceConfig{
		NodeId: nodeId1,
		Addr:   s1Addr,
		Handshake: HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionCfg,
		Dialer:          dialer,
		ListenerCreator: createListener,
	}

	s2Cfg := &ServiceConfig{
		NodeId: nodeId2,
		Addr:   s2Addr,
		Handshake: HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionCfg,
		Dialer:          dialer,
		ListenerCreator: createListener,
	}

	s1, err := CreateService(s1Cfg, testHandler, WithLogger(logger))
	if err != nil {
		t.Fatalf("create service 1, %v", err)
	}
	s2, err := CreateService(s2Cfg, testHandler, WithLogger(logger))
	if err != nil {
		t.Fatalf("create service 2, %v", err)
	}

	if err := s1.Start(); err != nil {
		t.Fatalf("node1 start failed: %v", err)
	}

	if err := s2.Start(); err != nil {
		t.Fatalf("node2 start failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	if _, err := s1.Connect(nodeId2, s2Addr); err != nil {
		t.Fatal("node1 connect node2", err)
	}

	time.Sleep(6 * time.Second)

	_ = s1.Close()
	_ = s2.Close()
}

func TestServiceSession(t *testing.T) {
	node1Name := "node1"
	node2Name := "node2"
	node1Addr := ":50001"
	node2Addr := ":50002"
	connects := new(atomic.Int64)
	packetId := new(atomic.Int64)
	sends := new(atomic.Int64)
	receives := new(atomic.Int64)
	wg := &sync.WaitGroup{}

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
		ReadBufSize:            10 * 1024,
		WriteBufSize:           10 * 1024,
		ReadWriteTimeout:       60 * time.Second,
		HeartbeatInterval:      15 * time.Second,
		InactiveTimeout:        5 * time.Minute,
	}

	dialer := func(addr string) (net.Conn, error) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		tcpConn := conn.(*net.TCPConn)
		if err := tcpConn.SetReadBuffer(128 * 1024); err != nil {
			return nil, err
		}
		if err := tcpConn.SetWriteBuffer(128 * 1024); err != nil {
			return nil, err
		}
		return conn, nil
	}

	createListener := func(addr string) (net.Listener, error) {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, err
		}
		return &testListener{
			Listener: l,
			accept: func(conn net.Conn) {
				tcpConn := conn.(*net.TCPConn)
				_ = tcpConn.SetReadBuffer(128 * 1024)
				_ = tcpConn.SetWriteBuffer(128 * 1024)
			},
		}, nil
	}

	s1Cfg := &ServiceConfig{
		NodeId: node1Name,
		Addr:   node1Addr,
		Handshake: HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionCfg,
		Dialer:          dialer,
		ListenerCreator: createListener,
	}
	s2Cfg := &ServiceConfig{
		NodeId: node2Name,
		Addr:   node2Addr,
		Handshake: HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionCfg,
		Dialer:          dialer,
		ListenerCreator: createListener,
	}

	s1, err := CreateService(s1Cfg, &testServiceHandler{
		onSessionPacket: func(s Session, p *RawPacket) error {
			receives.Add(1)
			wg.Done()
			return nil
		},
	}, WithLogger(logger))
	if err != nil {
		t.Fatalf("create service 1, %v", err)
	}

	s2, err := CreateService(s2Cfg, &testServiceHandler{
		onSessionPacket: func(s Session, p *RawPacket) error {
			receives.Add(1)
			wg.Done()
			return nil
		},
	}, WithLogger(logger))
	if err != nil {
		t.Fatalf("create service 2, %v", err)
	}

	if err := s1.Start(); err != nil {
		t.Fatal("start node1", err)
	}

	if err := s2.Start(); err != nil {
		t.Fatal("start node2", err)
	}

	time.Sleep(1 * time.Second)

	const n = 2000
	const k = 1000

	go func() {
		for i := 0; i < n; i++ {
			go func() {
				for j := 0; j < k; j++ {
					connects.Add(1)
					session, err := s1.Connect(node2Name, node2Addr)
					if err != nil {
						logger.Errorf("node1 connect node2: %s", err)
					} else {
						p := NewRawPacketWithCap(8)
						_ = p.WriteInt64(packetId.Add(1))
						if err := session.SendRaw(context.Background(), p); err != nil {
							// logger.Errorf("%s send to %s No.%d: %s", service1.NodeId(), service2.NodeId(), i, err)
						} else {
							sends.Add(1)
						}
					}
				}
			}()
		}
	}()
	wg.Add(n * k)

	go func() {
		for i := 0; i < n; i++ {
			go func() {
				for j := 0; j < k; j++ {
					connects.Add(1)
					session, err := s2.Connect(node1Name, node1Addr)
					if err != nil {
						logger.Errorf("node2 connect node1: %s", err)
					} else {
						p := NewRawPacketWithCap(8)
						_ = p.WriteInt64(packetId.Add(1))
						if err := session.SendRaw(context.Background(), p); err != nil {
							// logger.Errorf("%s send to %s No.%d: %s", service2.NodeId(), service1.NodeId(), i, err)
						} else {
							sends.Add(1)
						}
					}
				}
			}()
		}
	}()
	wg.Add(n * k)

	chWg := make(chan struct{})
	go func() {
		wg.Wait()
		close(chWg)
	}()
	chNotify := make(chan os.Signal, 1)
	signal.Notify(chNotify, syscall.SIGINT)
	select {
	case <-chNotify:
	case <-chWg:
	}

	_ = s1.Close()
	_ = s2.Close()
	logger.Warnln("connects", connects.Load())
	logger.Warnln("packetId", packetId.Load())
	logger.Warnln("sends", sends.Load())
	logger.Warnln("receives", receives.Load())
}

func TestServiceConcurrentConnect(t *testing.T) {
	connects := new(atomic.Int64)
	packetId := new(atomic.Int64)
	sends := new(atomic.Int64)
	receives := new(atomic.Int64)
	wg := &sync.WaitGroup{}

	logger := log.NewLogger(&log.Config{
		Level:        log.DebugLevel,
		EnableCaller: true,
		CallerSkip:   0,
		Development:  true,
		Cores:        []log.CoreConfig{log.NewStdCoreConfig()},
	})

	sessionCfg := SessionConfig{
		PendingPacketQueueSize: 1000,
		MaxPacketLength:        16 * 1024,
		ReadBufSize:            64 * 1024,
		WriteBufSize:           64 * 1024,
		ReadWriteTimeout:       30 * time.Second,
		HeartbeatInterval:      5 * time.Second,
		InactiveTimeout:        5 * time.Minute,
	}

	dialer := func(addr string) (net.Conn, error) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		tcpConn := conn.(*net.TCPConn)
		if err := tcpConn.SetReadBuffer(64 * 1024); err != nil {
			return nil, err
		}
		if err := tcpConn.SetWriteBuffer(64 * 1024); err != nil {
			return nil, err
		}
		return conn, nil
	}

	createListener := func(addr string) (net.Listener, error) {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, err
		}
		return &testListener{
			Listener: l,
			accept: func(conn net.Conn) {
				tcpConn := conn.(*net.TCPConn)
				_ = tcpConn.SetReadBuffer(64 * 1024)
				_ = tcpConn.SetWriteBuffer(64 * 1024)
			},
		}, nil
	}

	handler := &testServiceHandler{
		onSessionPacket: func(s Session, p *RawPacket) error {
			receives.Add(1)
			wg.Done()
			return nil
		},
	}

	serviceCount := 40
	services := make([]*Service, serviceCount)
	for i := range services {
		serviceCfg := &ServiceConfig{
			NodeId: fmt.Sprintf("Node%d", i),
			Addr:   fmt.Sprintf(":%d", 40000+i),
			Handshake: HandshakeConfig{
				Token:   "123",
				Timeout: 60 * time.Second,
			},
			Session:         sessionCfg,
			Dialer:          dialer,
			ListenerCreator: createListener,
		}

		s, err := CreateService(serviceCfg, handler, WithLogger(logger))
		if err != nil {
			t.Fatalf("create service %d: %s", i, err)
		}
		services[i] = s
		if err := services[i].Start(); err != nil {
			t.Fatalf("start service %d: %s", i, err)
		}
	}

	time.Sleep(2 * time.Second)

	n := 10
	m := 100
	for i := range services {
		wg.Add(n * m * (serviceCount - 1))
		go func(service *Service, i int) {
			for k := range services {
				if k == i {
					continue
				}
				go func(s1, s2 *Service) {
					for i := 0; i < n; i++ {
						connects.Add(1)
						session, err := s1.Connect(s2.NodeId(), s2.Addr())
						if err != nil {
							logger.Errorf("%s connect %s: %s", s1.NodeId(), s2.NodeId(), err)
							return
						}

						go func(session Session) {
							for i := 0; i < m; i++ {
								p := NewRawPacketWithCap(8)
								_ = p.WriteInt64(packetId.Add(1))
								if err := session.SendRaw(context.Background(), p); err != nil {
									logger.Errorf("%s send to %s No.%d: %s", s1.NodeId(), s2.NodeId(), i, err)
								} else {
									sends.Add(1)
								}
							}
						}(session)
					}
				}(service, services[k])
			}

		}(services[i], i)
	}

	chWg := make(chan struct{})
	go func() {
		wg.Wait()
		close(chWg)
	}()
	chNotify := make(chan os.Signal, 1)
	signal.Notify(chNotify, syscall.SIGINT)
	select {
	case <-chNotify:
	case <-chWg:
	}

	for i := range services {
		_ = services[i].Close()
	}

	logger.Warnln("connects", connects.Load())
	logger.Warnln("packetId", packetId.Load())
	logger.Warnln("sends", sends.Load())
	logger.Warnln("receives", receives.Load())
}

//func TestSessionLocal(t *testing.T) {
//	logger, err := log.CreateLogger(&log.Config{
//		Level:           log.DebugLevel,
//		EnableCaller:    true,
//		CallerSkip:      0,
//		Development:     true,
//		EnableStdOutput: true,
//	})
//	if err != nil {
//		t.Fatal(err)
//	}
//
//	serviceCfg := ServiceConfig{
//		ListeningRetryDelay: 5000,
//		HandshakeToken:      "handshake",
//		HandshakeTimeout:    1000000,
//		Session: SessionCfg{
//			HeartbeatInterval: 5000,
//			InactiveTimeout:   50000,
//			Net: gnet.TcpSessionCfg{
//				ReceiveTimeout:    0,
//				SendTimeout:       0,
//				SendBufferSize:    64 * 1024,
//				ReceiveBufferSize: 64 * 1024,
//				MaxPacketSize:     16 * 1024,
//			},
//		},
//	}
//
//	value := time.Now().Unix()
//	received := false
//
//	service := CreateService(
//		"node1", ":", &serviceCfg,
//		&testServiceHandler{
//			onSessionMsg: func(s Session, p *Msg) error {
//				v, err := p.ReadInt64()
//				if err != nil {
//					return errors.WithMessage(err, "read value")
//				}
//				if v == value {
//					received = true
//				}
//				return nil
//			},
//		},
//		logger,
//	)
//	if err := service.Start(); err != nil {
//		t.Fatal("Service start", err)
//	}
//
//	session, err := service.ConnectLocal()
//	if err != nil {
//		t.Fatal("connect local", err)
//	}
//
//	p := GetMsg(protoTypeRaw)
//	p.WriteInt64(value)
//	if err := session.Send(p); err != nil {
//		t.Fatal("send by local session", err)
//	}
//
//	if !received {
//		t.Fatal("not received")
//	}
//}
