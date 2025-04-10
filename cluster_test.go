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
	"github.com/godyy/gutils/log"
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
	clientId := "client1"
	serviceId := "service1"
	serviceAddr := ":50001"

	logger := log.NewLogger(&log.Config{
		Level:        log.DebugLevel,
		EnableCaller: true,
		CallerSkip:   0,
		Development:  true,
		Cores:        []log.CoreConfig{log.NewStdCoreConfig()},
	})

	center := &testCenter{nodes: make(map[string]*testNode)}
	center.addNode(&testNode{
		nodeId: serviceId,
		addr:   serviceAddr,
	})

	sessionConfig := net.SessionConfig{
		PendingPacketQueueSize: 10,
		MaxPacketLength:        1024,
		ReadWriteTimeout:       60 * time.Second,
		HeartbeatInterval:      1 * time.Second,
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

	clientConfig := &net.ClientConfig{
		NodeId: clientId,
		Handshake: net.HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session: sessionConfig,
		Dialer:  dialer,
	}
	client, err := CreateClient(&ClientConfig{
		Center:  center,
		Net:     clientConfig,
		Handler: &testAgentHandler{},
	}, WithLogger(logger))
	if err != nil {
		t.Fatal("create client: ", err)
	}

	serviceConfig := &net.ServiceConfig{
		NodeId: serviceId,
		Addr:   serviceAddr,
		Handshake: net.HandshakeConfig{
			Token:   "123",
			Timeout: 5 * time.Second,
		},
		Session:         sessionConfig,
		Dialer:          dialer,
		ListenerCreator: createListener,
	}
	service, err := CreateService(&ServiceConfig{
		Center:  center,
		Net:     serviceConfig,
		Handler: &testAgentHandler{},
	}, WithLogger(logger))
	if err != nil {
		t.Fatal("create service: ", err)
	}

	if err := client.Start(); err != nil {
		t.Fatal("start client agent: ", err)
	}
	if err := service.Start(); err != nil {
		t.Fatal("start service agent: ", err)
	}

	if _, err := client.ConnectNode(serviceId); err != nil {
		t.Fatal("client connect service: ", err)
	}

	time.Sleep(5 * time.Second)

	if err := client.Close(); err != nil {
		t.Fatal("close client agent: ", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal("close service agent: ", err)
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

	logger := log.NewLogger(&log.Config{
		Level:        log.WarnLevel,
		EnableCaller: true,
		CallerSkip:   0,
		Development:  true,
		Cores:        []log.CoreConfig{log.NewStdCoreConfig()},
	})

	center := &testCenter{nodes: make(map[string]*testNode)}

	sessionCfg := net.SessionConfig{
		PendingPacketQueueSize: 1000,
		MaxPacketLength:        16 * 1024,
		ReadBufSize:            64 * 1024,
		WriteBufSize:           64 * 1024,
		ReadWriteTimeout:       30 * time.Second,
		HeartbeatInterval:      5 * time.Second,
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
			Session:         sessionCfg,
			Dialer:          dialer,
			ListenerCreator: createListener,
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
