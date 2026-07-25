# Phase 集群连接配置机制

## 1. 概述

在 BKE 的 Phase 框架中，连接到目标集群的配置是从 **BKECluster** 资源中获取的。Phase 通过 BKECluster 关联的 Cluster API 资源来获取 kubeconfig 或 token，从而建立与业务集群的连接。

## 2. 连接配置获取流程

### 2.1 核心函数

```go
// pkg/kube/kube.go:158
func NewRemoteClientByBKECluster(
    ctx context.Context, 
    c client.Client, 
    bkeCluster *bkev1beta1.BKECluster,
) (RemoteKubeClient, error)
```

该函数是 Phase 中获取业务集群连接的核心入口，支持两种认证方式。

### 2.2 获取方式（优先级顺序）

#### 方式一：通过 Cluster API 的 kubeconfig secret（推荐）

```go
// 尝试从 Cluster API 获取 kubeconfig
config, err := remote.RESTConfig(ctx, "cluster-cache-tracker", c, util.ObjectKey(bkeCluster))
```

**工作原理**：
- 使用 Cluster API 的 `remote.RESTConfig` 函数
- 从 BKECluster 关联的 Cluster 资源中获取 kubeconfig secret
- Secret 名称格式：`<cluster-name>-kubeconfig`
- 包含完整的 kubeconfig 配置（CA 证书、token、API Server 地址等）

**适用场景**：
- BKE 创建的集群（通过 Phase 安装流程）
- 已纳管的 Bocloud 集群
- 任何已创建 Cluster API 资源的集群

#### 方式二：通过 BKECluster 的 token secret（备用）

```go
// 如果方式一失败，尝试使用 token
if len(errs) != 0 && bkeCluster.Spec.ControlPlaneEndpoint.IsValid() {
    config, err = getRestConfigByToken(ctx, c, bkeCluster)
}
```

**工作原理**：
- 从 BKECluster 的 token secret 获取认证信息
- 使用 `ControlPlaneEndpoint` 作为 API Server 地址
- 通过 ServiceAccount token 进行认证

**适用场景**：
- Cluster API 资源未创建的集群
- 纳管的第三方集群（如 kubeadm 创建的集群）
- kubeconfig secret 不可用的情况

### 2.3 连接配置来源

```
BKECluster
    │
    ├── OwnerReference → Cluster (Cluster API)
    │       │
    │       └── Secret: <cluster-name>-kubeconfig
    │               └── 包含完整的 kubeconfig
    │               └── 优先级：高
    │
    └── Spec.ControlPlaneEndpoint
            │
            └── Secret: <bkecluster-name>-token
                    └── 包含 token 和 CA 证书
                    └── 优先级：低（备用）
```

## 3. 管理集群 vs 业务集群

### 3.1 关键区别

| 维度 | 管理集群 | 业务集群 |
|------|---------|---------|
| **定义** | BKE Controller 运行的集群 | 被 BKE 管理的目标集群 |
| **客户端来源** | `PhaseContext.Client` | `NewRemoteClientByBKECluster` |
| **用途** | 读取 BKECluster、Cluster 等资源 | 操作业务集群的 Node、Pod 等 |
| **认证方式** | Controller 的 ServiceAccount | kubeconfig 或 token |

### 3.2 代码示例

```go
// pkg/phaseframe/phases/ensure_cluster.go

func (e *EnsureCluster) Execute() (ctrl.Result, error) {
    // 1. 获取管理集群客户端（PhaseContext 自带）
    ctx, c, bkeCluster, _, log := e.Ctx.Untie()
    // c 是管理集群的 client，用于读取 BKECluster、Cluster 等资源
    
    // 2. 获取业务集群客户端（需要显式创建）
    remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    e.remoteClient = remoteClient
    // remoteClient 是业务集群的 client，用于操作 Node、Pod 等
    
    // 3. 使用业务集群客户端执行操作
    nodes, err := e.remoteClient.ListNodes(nil)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 4. 使用管理集群客户端更新状态
    if err := c.Update(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}
```

## 4. Phase 中的使用模式

### 4.1 标准模式

```go
type EnsureXXX struct {
    phaseframe.BasePhase
    remoteClient kube.RemoteKubeClient  // 业务集群客户端
}

func (e *EnsureXXX) Execute() (ctrl.Result, error) {
    // 1. 获取管理集群上下文
    ctx, c, bkeCluster, _, log := e.Ctx.Untie()
    
    // 2. 创建业务集群客户端
    remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    e.remoteClient = remoteClient
    e.remoteClient.SetLogger(log.NormalLogger)
    
    // 3. 执行业务逻辑
    if err := e.doSomething(); err != nil {
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}

func (e *EnsureXXX) doSomething() error {
    // 使用业务集群客户端
    clientSet, _ := e.remoteClient.KubeClient()
    nodes, err := clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
    if err != nil {
        return err
    }
    
    // 处理节点...
    return nil
}
```

### 4.2 常见使用场景

| Phase | 使用方式 | 说明 |
|-------|---------|------|
| EnsureCluster | 健康检查 | 连接业务集群检查节点状态 |
| EnsureAddonDeploy | 部署 Addon | 连接业务集群部署组件 |
| EnsureMasterUpgrade | 升级 Master | 连接业务集群执行升级 |
| EnsureWorkerUpgrade | 升级 Worker | 连接业务集群执行升级 |
| EnsureNodesEnv | 环境初始化 | 连接业务集群配置节点 |

## 5. 错误处理

### 5.1 常见错误

```go
// 错误 1：Cluster API 资源不存在
config, err := remote.RESTConfig(ctx, "cluster-cache-tracker", c, util.ObjectKey(bkeCluster))
// 错误：failed to get remote cluster config

// 错误 2：kubeconfig secret 不存在
// 错误：secrets "<cluster-name>-kubeconfig" not found

// 错误 3：token secret 不存在
// 错误：secrets "<bkecluster-name>-token" not found

// 错误 4：ControlPlaneEndpoint 无效
// 错误：controlPlaneEndpoint is invalid
```

### 5.2 错误处理建议

```go
remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
if err != nil {
    log.Error("failed to create remote client: %v", err)
    
    // 检查是否是 Cluster API 资源未创建
    if strings.Contains(err.Error(), "not found") {
        // 等待 Cluster API 资源创建
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
    }
    
    return ctrl.Result{}, err
}
```

## 6. 最佳实践

### 6.1 客户端复用

```go
// ✅ 推荐：在 Phase 开始时创建一次，整个 Phase 复用
func (e *EnsureXXX) Execute() (ctrl.Result, error) {
    remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    e.remoteClient = remoteClient
    
    // 在多个方法中使用
    e.doSomething1()
    e.doSomething2()
    
    return ctrl.Result{}, nil
}

// ❌ 不推荐：每次操作都创建新客户端
func (e *EnsureXXX) doSomething1() error {
    remoteClient, _ := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
    // ...
}

func (e *EnsureXXX) doSomething2() error {
    remoteClient, _ := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
    // ...
}
```

### 6.2 日志记录

```go
// ✅ 推荐：设置日志记录器
remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
if err != nil {
    return ctrl.Result{}, err
}
e.remoteClient = remoteClient
e.remoteClient.SetLogger(log.NormalLogger)  // 设置日志
e.remoteClient.SetBKELogger(log)             // 设置 BKE 日志

// ❌ 不推荐：不设置日志
remoteClient, _ := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
// 没有设置日志，难以排查问题
```

### 6.3 错误处理

```go
// ✅ 推荐：详细的错误处理
remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
if err != nil {
    log.Error("failed to create remote client for cluster %s: %v", 
        bkeCluster.Name, err)
    
    // 区分错误类型
    if apierrors.IsNotFound(err) {
        log.Info("cluster resources not ready, will retry")
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
    }
    
    return ctrl.Result{}, errors.Wrap(err, "failed to connect to cluster")
}

// ❌ 不推荐：简单返回错误
remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
if err != nil {
    return ctrl.Result{}, err
}
```

## 7. 总结

Phase 中连接到业务集群的配置是从 **BKECluster** 资源中获取的，通过以下机制：

1. **配置来源**：BKECluster 关联的 Cluster API 资源
2. **主要方式**：Cluster API 的 kubeconfig secret（`<cluster-name>-kubeconfig`）
3. **备用方式**：BKECluster 的 token secret（`<bkecluster-name>-token`）
4. **核心函数**：`kube.NewRemoteClientByBKECluster()`
5. **使用模式**：在 Phase 开始时创建客户端，整个 Phase 复用

理解这一机制对于开发和调试 Phase 至关重要，特别是在处理多集群场景时。
