package cluster

import (
	"github.com/godyy/gcluster/center"
	"github.com/godyy/gcluster/net"
	"github.com/godyy/gutils/log"
	pkg_errors "github.com/pkg/errors"
)

// AgentHandler Agent Handler.
type AgentHandler interface {
	// OnNodePacket 处理节点数据包.
	// 当节点数据包到达时，会调用此方法.
	OnNodePacket(remoteNodeId string, p *net.RawPacket) error
}

// Agent cluster Agent.
// 提供访问集群的相关功能.
type Agent struct {
	center     center.Center      // 数据中心.
	sessionMgr net.SessionManager // 网络 session 管理器
	handler    AgentHandler       // 处理器.
}

// Start 启动, 接入集群.
func (a *Agent) Start() error {
	if err := a.sessionMgr.Start(); err != nil {
		return err
	}
	return nil
}

// Close 关闭，中断与集群的连接.
func (a *Agent) Close() error {
	return a.sessionMgr.Close()
}

// getNode 通过 nodeId 获取集群中的节点信息.
func (a *Agent) getNode(nodeId string) (center.Node, error) {
	node, err := a.center.GetNode(nodeId)
	if err != nil {
		return nil, pkg_errors.WithMessage(err, "get node info from center")
	}
	return node, nil
}

// ConnectNode 连接集群中 nodeId 指向的节点.
func (a *Agent) ConnectNode(nodeId string) (net.Session, error) {
	if session := a.sessionMgr.GetSession(nodeId); session != nil {
		return session, nil
	}
	node, err := a.getNode(nodeId)
	if err != nil {
		return nil, err
	}
	return a.sessionMgr.Connect(nodeId, node.GetNodeAddr())
}

// Send2Node 向集群中 nodeId 指向的节点发送数据包.
func (a *Agent) Send2Node(nodeId string, p *net.RawPacket) error {
	session, err := a.ConnectNode(nodeId)
	if err != nil {
		return pkg_errors.WithMessage(err, "connect node")
	}
	return session.SendRaw(p)
}

// OnSessionPacket 处理从 session 接收到的数据包.
// 内部调用，外部无需访问.
func (a *Agent) OnSessionPacket(session net.Session, p *net.RawPacket) error {
	return a.handler.OnNodePacket(session.RemoteNodeId(), p)
}

// CreateClient 创建 client 端 Agent.
func CreateClient(
	center center.Center,
	cfg *net.ClientConfig,
	dialer net.Dialer,
	handler AgentHandler,
	logger log.Logger) (*Agent, error) {

	if handler == nil {
		return nil, pkg_errors.New("cluster: handler nil")
	}

	agent := &Agent{
		center:  center,
		handler: handler,
	}

	sessionMgr, err := net.CreateClient(cfg, dialer, agent, logger)
	if err != nil {
		return nil, err
	}

	agent.sessionMgr = sessionMgr

	return agent, nil
}

// CreateService 创建 service 端 Agent.
func CreateService(
	center center.Center,
	cfg *net.ServiceConfig,
	dialer net.Dialer,
	createListener net.CreateListener,
	handler AgentHandler,
	logger log.Logger,
) (*Agent, error) {

	if handler == nil {
		return nil, pkg_errors.New("cluster: handler nil")
	}

	agent := &Agent{
		center:  center,
		handler: handler,
	}

	sessionMgr, err := net.CreateService(cfg, dialer, createListener, agent, logger)
	if err != nil {
		return nil, err
	}

	agent.sessionMgr = sessionMgr

	return agent, nil
}
