package gcluster

import (
	"context"
	"fmt"
	stdnet "net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/godyy/gcluster/center"
	"github.com/godyy/gcluster/net"
	"github.com/godyy/glog"
	"github.com/pkg/errors"
)

type testNode struct {
	nodeId string
	addr   string
}

func (t *testNode) GetNodeId() string {
	return t.nodeId
}

func (t *testNode) GetNodeAddr() string {
	return t.addr
}

type testCenter struct {
	nodes map[string]*testNode
}

func (c *testCenter) GetNode(nodeId string) (center.Node, error) {
	node, ok := c.nodes[nodeId]
	if !ok {
		return nil, errors.New("node not found")
	}
	return node, nil
}

func (c *testCenter) addNode(node *testNode) {
	c.nodes[node.nodeId] = node
}

type testAgentHandler struct {
	onNodePacket func(string, *net.RawPacket) error
}

func (t *testAgentHandler) OnNodePacket(remoteNodeId string, packet *net.RawPacket) error {
	if t.onNodePacket != nil {
		return t.onNodePacket(remoteNodeId, packet)
	}
	return nil
}

func TestAgent(t *testing.T) {
	serviceId1 := "service1"
	serviceId2 := "service2"
	serviceAddr1 := ":50001"
	serviceAddr2 := ":50002"

	logger := glog.NewLogger(&glog.Config{
		Level:        glog.DebugLevel,
		EnableCaller: true,
		CallerSkip:   0,
		Development:  true,
		Cores:        []glog.CoreConfig{glog.NewStdCoreConfig()},
	})

	center := &testCenter{nodes: make(map[string]*testNode)}
	center.addNode(&testNode{
		nodeId: serviceId1,
		addr:   serviceAddr1,
	})
	center.addNode(&testNode{
		nodeId: serviceId2,
		addr:   serviceAddr2,
	})

	sessionConfig := net.SessionConfig{
		PendingPacketQueueSize: 10,
		MaxPacketLength:        1024,
		ReadWriteTimeout:       60 * time.Second,
		TickInterval:           1 * time.Second,
		HeartbeatTimeout:       2 * time.Second,
		InactiveTimeout:        5 * time.Minute,
		ReadBufSize:            10 * 1024,
		WriteBufSize:           10 * 1024,
	}

	dialer := func(addr string) (stdnet.Conn, error) {
		return stdnet.Dial("tcp", addr)
	}
	createListener := func(addr string) (stdnet.Listener, error) {
		return stdnet.Listen("tcp", addr)
	}

	service1Config := &net.ServiceConfig{
		NodeId: serviceId1,
		Addr:   serviceAddr1,
		Handshake: net.HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionConfig,
		Dialer:          dialer,
		ListenerCreator: createListener,
		TimerSystem:     net.NewTimerHeap(),
	}
	service1, err := CreateService(&ServiceConfig{
		Center:  center,
		Net:     service1Config,
		Handler: &testAgentHandler{},
	}, WithLogger(logger))
	if err != nil {
		t.Fatal("create service1: ", err)
	}

	service2Config := &net.ServiceConfig{
		NodeId: serviceId2,
		Addr:   serviceAddr2,
		Handshake: net.HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionConfig,
		Dialer:          dialer,
		ListenerCreator: createListener,
		TimerSystem:     net.NewTimerHeap(),
	}
	service2, err := CreateService(&ServiceConfig{
		Center:  center,
		Net:     service2Config,
		Handler: &testAgentHandler{},
	}, WithLogger(logger))
	if err != nil {
		t.Fatal("create service: ", err)
	}

	if err := service1.Start(); err != nil {
		t.Fatal("start service1 agent: ", err)
	}
	if err := service2.Start(); err != nil {
		t.Fatal("start service2 agent: ", err)
	}

	if _, err := service1.ConnectNode(serviceId2); err != nil {
		t.Fatal("service1 connect service2: ", err)
	}

	time.Sleep(5 * time.Second)

	if err := service1.Close(); err != nil {
		t.Fatal("close service1 agent: ", err)
	}
	if err := service2.Close(); err != nil {
		t.Fatal("close service2 agent: ", err)
	}
}

type testListener struct {
	stdnet.Listener
	accept func(conn stdnet.Conn)
}

func (t *testListener) Accept() (stdnet.Conn, error) {
	conn, err := t.Listener.Accept()
	if err != nil {
		return nil, err
	}
	t.accept(conn)
	return conn, nil
}

func TestConcurrentConnect(t *testing.T) {
	connects := new(atomic.Int64)
	packetId := new(atomic.Int64)
	sends := new(atomic.Int64)
	receives := new(atomic.Int64)
	wg := &sync.WaitGroup{}

	logger := glog.NewLogger(&glog.Config{
		Level:        glog.WarnLevel,
		EnableCaller: true,
		CallerSkip:   0,
		Development:  true,
		Cores:        []glog.CoreConfig{glog.NewStdCoreConfig()},
	})

	center := &testCenter{nodes: make(map[string]*testNode)}

	sessionCfg := net.SessionConfig{
		PendingPacketQueueSize: 1000,
		MaxPacketLength:        16 * 1024,
		ReadBufSize:            64 * 1024,
		WriteBufSize:           64 * 1024,
		ReadWriteTimeout:       30 * time.Second,
		TickInterval:           5 * time.Second,
		InactiveTimeout:        5 * time.Minute,
	}

	dialer := func(addr string) (stdnet.Conn, error) {
		conn, err := stdnet.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		tcpConn := conn.(*stdnet.TCPConn)
		if err := tcpConn.SetReadBuffer(64 * 1024); err != nil {
			return nil, err
		}
		if err := tcpConn.SetWriteBuffer(64 * 1024); err != nil {
			return nil, err
		}
		return conn, nil
	}

	createListener := func(addr string) (stdnet.Listener, error) {
		l, err := stdnet.Listen("tcp", addr)
		if err != nil {
			return nil, err
		}
		return &testListener{
			Listener: l,
			accept: func(conn stdnet.Conn) {
				tcpConn := conn.(*stdnet.TCPConn)
				_ = tcpConn.SetReadBuffer(64 * 1024)
				_ = tcpConn.SetWriteBuffer(64 * 1024)
			},
		}, nil
	}

	handler := &testAgentHandler{
		onNodePacket: func(_ string, p *net.RawPacket) error {
			receives.Add(1)
			wg.Done()
			return nil
		},
	}

	serviceCount := 40
	services := make([]*Agent, serviceCount)
	serviceIds := make([]string, serviceCount)
	for i := range services {
		serviceCfg := &net.ServiceConfig{
			NodeId: fmt.Sprintf("Node%d", i),
			Addr:   fmt.Sprintf(":%d", 40000+i),
			Handshake: net.HandshakeConfig{
				Token:   "123",
				Timeout: 60 * time.Second,
			},
			Session:                    sessionCfg,
			Dialer:                     dialer,
			ListenerCreator:            createListener,
			TimerSystem:                net.NewTimerHeap(),
			ExpectedConcurrentSessions: 1000,
		}

		s, err := CreateService(&ServiceConfig{
			Center:  center,
			Net:     serviceCfg,
			Handler: handler,
		}, WithLogger(logger))
		if err != nil {
			t.Fatalf("create service %d: %s", i, err)
		}

		services[i] = s
		serviceIds[i] = serviceCfg.NodeId
		if err := services[i].Start(); err != nil {
			t.Fatalf("start service %d: %s", i, err)
		}

		center.addNode(&testNode{
			nodeId: serviceCfg.NodeId,
			addr:   serviceCfg.Addr,
		})
	}

	time.Sleep(2 * time.Second)

	n := 10
	m := 100
	for i := range serviceIds {
		wg.Add(n * m * (serviceCount - 1))
		go func(i int) {
			service := services[i]
			serviceId := serviceIds[i]
			for k := range serviceIds {
				if k == i {
					continue
				}
				go func(a *Agent, targetNodeId string) {
					for i := 0; i < n; i++ {
						connects.Add(1)
						go func() {
							for i := 0; i < m; i++ {
								p := net.NewRawPacketWithCap(8)
								_ = p.WriteInt64(packetId.Add(1))
								if err := a.Send2Node(context.Background(), targetNodeId, p); err != nil {
									logger.Errorf("%s send to %s No.%d: %s", serviceId, targetNodeId, i, err)
								} else {
									sends.Add(1)
								}
							}
						}()
					}
				}(service, serviceIds[k])
			}

		}(i)
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
