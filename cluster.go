package gcluster

import (
	"context"
	"errors"

	"github.com/godyy/gcluster/center"
	"github.com/godyy/gcluster/net"
	pkgerrors "github.com/pkg/errors"
)

// AgentHandler Agent Handler.
type AgentHandler interface {
	// OnNodePacket 处理节点数据包.
	// 当节点数据包到达时，会调用此方法.
	OnNodePacket(remoteNodeId string, p *net.RawPacket) error
}

// Agent gcluster Agent.
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

// NodeId 节点 ID.
func (a *Agent) NodeId() string { return a.sessionMgr.NodeId() }

// getNode 通过 nodeId 获取集群中的节点信息.
func (a *Agent) getNode(nodeId string) (center.Node, error) {
	node, err := a.center.GetNode(nodeId)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "get node info from center")
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
func (a *Agent) Send2Node(ctx context.Context, nodeId string, p *net.RawPacket) error {
	session, err := a.ConnectNode(nodeId)
	if err != nil {
		return pkgerrors.WithMessage(err, "connect node")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return session.SendRaw(ctx, p)
}

// OnSessionPacket 处理从 session 接收到的数据包.
// 内部调用，外部无需访问.
func (a *Agent) OnSessionPacket(session net.Session, p *net.RawPacket) error {
	return a.handler.OnNodePacket(session.RemoteNodeId(), p)
}

// ClientConfig 客户端配置.
type ClientConfig struct {
	// Center 数据中心.
	Center center.Center

	// Net 网络配置.
	Net *net.ClientConfig

	// Handler 处理器.
	Handler AgentHandler
}

func (c *ClientConfig) init() error {
	if c == nil {
		return errors.New("ClientConfig nil")
	}

	if c.Center == nil {
		return errors.New("ClientConfig.Center not specified")
	}

	if c.Net == nil {
		return errors.New("ClientConfig.Net not specified")
	}

	if c.Handler == nil {
		return errors.New("ClientConfig.Handler not specified")
	}

	return nil
}

// CreateClient 创建 client 端 Agent.
func CreateClient(cfg *ClientConfig, options ...Option,
) (*Agent, error) {

	if err := cfg.init(); err != nil {
		return nil, err
	}

	var optSet optionSet
	for _, opt := range options {
		opt(&optSet)
	}

	agent := &Agent{
		center:  cfg.Center,
		handler: cfg.Handler,
	}

	sessionMgr, err := net.CreateClient(cfg.Net, agent, optSet.smOptions...)
	if err != nil {
		return nil, err
	}

	agent.sessionMgr = sessionMgr

	return agent, nil
}

// ServiceConfig 服务配置.
type ServiceConfig struct {
	// Center 数据中心.
	Center center.Center

	// Net 网络配置.
	Net *net.ServiceConfig

	// Handler 处理器.
	Handler AgentHandler
}

func (c *ServiceConfig) init() error {
	if c == nil {
		return errors.New("ServiceConfig nil")
	}

	if c.Center == nil {
		return errors.New("ServiceConfig.Center not specified")
	}

	if c.Net == nil {
		return errors.New("ServiceConfig.Net not specified")
	}

	if c.Handler == nil {
		return errors.New("ServiceConfig.Handler not specified")
	}

	return nil
}

// CreateService 创建 service 端 Agent.
func CreateService(cfg *ServiceConfig, options ...Option) (*Agent, error) {
	if err := cfg.init(); err != nil {
		return nil, err
	}

	var optSet optionSet
	for _, opt := range options {
		opt(&optSet)
	}

	agent := &Agent{
		center:  cfg.Center,
		handler: cfg.Handler,
	}

	sessionMgr, err := net.CreateService(cfg.Net, agent, optSet.smOptions...)
	if err != nil {
		return nil, err
	}

	agent.sessionMgr = sessionMgr

	return agent, nil
}
