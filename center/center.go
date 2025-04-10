package center

// Node 节点信息
type Node interface {
	// GetNodeId 节点ID.
	GetNodeId() string

	// GetNodeAddr 获取节点地址.
	GetNodeAddr() string
}

// Center 数据中心.
type Center interface {
	// GetNode 获取节点信息.
	GetNode(nodeId string) (Node, error)
}
