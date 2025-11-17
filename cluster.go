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
	// OnNodeBytes 处理节点字节数据.
	// 当节点字节数据到达时，会调用此方法.
	OnNodeBytes(remoteNodeId string, data []byte) error
}

// AgentConfig Agent 配置.
type AgentConfig struct {
	// Center 数据中心.
	Center center.Center

	// Net 网络配置.
	Net *net.ServiceConfig

	// Handler 处理器.
	Handler AgentHandler
}

func (c *AgentConfig) init() error {
	if c == nil {
		return errors.New("AgentConfig nil")
	}

	if c.Center == nil {
		return errors.New("AgentConfig: Center not specified")
	}

	if c.Net == nil {
		return errors.New("AgentConfig: Net not specified")
	}

	if c.Handler == nil {
		return errors.New("AgentConfig: Handler not specified")
	}

	return nil
}

// Agent gcluster Agent.
// 提供访问集群的相关功能.
type Agent struct {
	center  center.Center // 数据中心.
	service *net.Service  // Service.
	handler AgentHandler  // 处理器.
}

// Start 启动, 接入集群.
func (a *Agent) Start() error {
	if err := a.service.Start(); err != nil {
		return err
	}
	return nil
}

// Close 关闭，中断与集群的连接.
func (a *Agent) Close() error {
	return a.service.Close()
}

// NodeId 节点 ID.
func (a *Agent) NodeId() string { return a.service.NodeId() }

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
	if session := a.service.GetSession(nodeId); session != nil {
		return session, nil
	}
	node, err := a.getNode(nodeId)
	if err != nil {
		return nil, err
	}
	return a.service.Connect(nodeId, node.GetNodeAddr())
}

// Send2Node 向集群中 nodeId 指向的节点发送字节数据.
func (a *Agent) Send2Node(ctx context.Context, nodeId string, data []byte) error {
	// 优先判断ctx是否取消
	if err := ctx.Err(); err != nil {
		return err
	}

	session, err := a.ConnectNode(nodeId)
	if err != nil {
		return pkgerrors.WithMessage(err, "connect node")
	}

	return session.Send(ctx, data)
}

// OnSessionBytes 处理从 session 接收到的字节数据.
// 内部调用，外部无需访问.
func (a *Agent) OnSessionBytes(session net.Session, data []byte) error {
	return a.handler.OnNodeBytes(session.RemoteNodeId(), data)
}

// CreateAgent 创建 Agent.
func CreateAgent(cfg *AgentConfig, options ...Option) (*Agent, error) {
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

	service, err := net.CreateService(cfg.Net, agent, optSet.smOptions...)
	if err != nil {
		return nil, err
	}

	agent.service = service

	return agent, nil
}
