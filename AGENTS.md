# gcluster 代码库指引（给 Agent/自动化协作）

## 项目定位

本仓库提供一个轻量的集群通信抽象：

- **gcluster.Agent**（[cluster.go](file:///Users/godyy/work/go/godyy/gcluster/cluster.go)）对外暴露“按 NodeId 连接/发送”的能力
- **center.Center**（[center.go](file:///Users/godyy/work/go/godyy/gcluster/center/center.go)）由业务侧实现，用于按 NodeId 查询节点地址
- **net.Service / net.Session**（目录 [net](file:///Users/godyy/work/go/godyy/gcluster/net)）负责网络监听、建连、握手、会话收发、心跳与超时等

数据流：

1. Agent 通过 Center 拿到目标节点地址
2. Agent 通过 net.Service 建立到目标节点的 Session
3. 业务侧实现的 AgentHandler 接收来自远端节点的字节数据

## 常用入口

- 创建并启动：
  - gcluster：CreateAgent / (*Agent).Start（[cluster.go](file:///Users/godyy/work/go/godyy/gcluster/cluster.go)）
  - net：CreateService / (*Service).Start（[service.go](file:///Users/godyy/work/go/godyy/gcluster/net/service.go)）
- 发消息：(*Agent).Send2Node（[cluster.go](file:///Users/godyy/work/go/godyy/gcluster/cluster.go)）
- 收消息：Agent.OnSessionBytes → AgentHandler.OnNodeBytes（[cluster.go](file:///Users/godyy/work/go/godyy/gcluster/cluster.go)）

## 配置要点

### 身份与地址

- ServiceConfig.NodeId：本节点标识
- ServiceConfig.Addr：监听地址（示例测试使用 `:50001` 这种形式）

见 [service.go](file:///Users/godyy/work/go/godyy/gcluster/net/service.go)。

### 握手

握手参数在 ServiceConfig.Handshake：

- Token：两端一致，用于校验连接合法性
- Timeout：握手读写超时

实现见 [handshake.go](file:///Users/godyy/work/go/godyy/gcluster/net/handshake.go)。

### Session 行为

会话相关参数在 ServiceConfig.Session（结构体定义见 [session.go](file:///Users/godyy/work/go/godyy/gcluster/net/session.go)），包括：

- 包长度、读写超时、Tick/心跳/Inactive 等
- 读写缓冲、批量写等

## 协议与代码生成（protobuf）

握手等内部协议位于：

- proto：目录 [net/internal/protocol/protos](file:///Users/godyy/work/go/godyy/gcluster/net/internal/protocol/protos)
- 生成代码：目录 [net/internal/protocol/pb](file:///Users/godyy/work/go/godyy/gcluster/net/internal/protocol/pb)

重新生成（需要本机安装 `protoc` 与 `protoc-gen-go`）：

```bash
make -C net protos
```

等价于：

```bash
make -C net/internal/protocol protos
```

Makefile 位置：

- [net/Makefile](file:///Users/godyy/work/go/godyy/gcluster/net/Makefile)
- [net/internal/protocol/Makefile](file:///Users/godyy/work/go/godyy/gcluster/net/internal/protocol/Makefile)

## 测试与验证

全量测试：

```bash
go test ./...
```

关注的测试：

- Agent 基本连通：TestAgent（[cluster_test.go](file:///Users/godyy/work/go/godyy/gcluster/cluster_test.go)）
- 并发建连压测类测试：TestConcurrentConnect（同上文件）可能耗时较长、占用端口较多
- TestConcurrentConnect 默认使用 `:40000` 起的一段固定端口，若机器上已被占用会直接失败（`bind: address already in use`）
- net 下还有辅助命令：`make -C net test_concurrent_connect`（见 [net/Makefile](file:///Users/godyy/work/go/godyy/gcluster/net/Makefile)）

## 代码修改惯例（本仓库内）

- 以 “gcluster 对外 API + net 内部实现” 分层来改：
  - 对外行为调整优先改 [cluster.go](file:///Users/godyy/work/go/godyy/gcluster/cluster.go)
  - 网络行为（握手/收发/超时/定时器）优先改 net 目录下对应文件
- 如果改动涉及协议（`*.proto` 或握手 pb），务必同步更新生成代码并跑 `go test ./...`
- 日志通过 Option 注入：gcluster.WithLogger（[option.go](file:///Users/godyy/work/go/godyy/gcluster/option.go)）
