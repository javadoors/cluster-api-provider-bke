# `ensure_cluster_manage.go` 详细规格与特性
          
## `ensure_cluster_manage.go` 详细规格与特性及流程

### 一、核心作用

#### 1. 纳管已存在的集群

```
┌─────────────────────────────────────────────────────────────────────┐
│  EnsureClusterManage Phase                                          │
│  纳管已存在的集群，转换为 BKE 管理模式                                   │
└─────────────────────────────────────────────────────────────────────┘
        │
        ├── Bocloud 托管集群 → 完全纳管（创建 CAPI 对象）
        │
        ├── 其他类型集群 → 仅收集信息
        │
        └── BKE 自建集群 → 不执行（跳过）
```

### 二、Phase 结构

```go
type EnsureClusterManage struct {
    phaseframe.BasePhase
    remoteClient kube.RemoteKubeClient  // 远程集群客户端
}
```

### 三、执行条件判断

```go
func (e *EnsureClusterManage) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 基础判断
    if !e.BasePhase.NormalNeedExecute(old, new) {
        return false
    }

    // 2. BKE 自建集群不执行
    if clusterutil.IsBKECluster(new) {
        return false
    }

    // 3. 已完全控制不执行
    if clusterutil.FullyControlled(new) {
        return false
    }

    e.SetStatus(bkev1beta1.PhaseWaiting)
    return true
}
```

**执行条件**：

| 条件 | 说明 |
|------|------|
| `!IsBKECluster` | 非 BKE 自建集群 |
| `!FullyControlled` | 未完全控制 |
| `NormalNeedExecute` | 基础条件满足 |

### 四、执行流程

#### Execute 主流程

```go
func (e *EnsureClusterManage) Execute() (ctrl.Result, error) {
    // 1. 获取远程客户端
    if err := e.getRemoteClient(); err != nil {
        return ctrl.Result{}, err
    }

    // 2. 集群基础信息收集
    if err := e.collectBaseInfo(); err != nil {
        return ctrl.Result{}, err
    }

    // 3. 推送 Agent
    if err := e.pushAgent(); err != nil {
        return ctrl.Result{}, err
    }

    // 4. 使用 Agent 收集更多信息
    if err := e.collectAgentInfo(); err != nil {
        return ctrl.Result{}, err
    }

    // 5. 其他类型集群到此结束
    if !clusterutil.IsBocloudCluster(e.Ctx.BKECluster) {
        return ctrl.Result{}, nil
    }

    // 6. CAPI 对象未创建，等待
    if e.Ctx.BKECluster.OwnerReferences == nil {
        return ctrl.Result{Requeue: true}, nil
    }

    // 7. Bocloud 集群管理准备
    if err := e.bocloudClusterManagePrepare(); err != nil {
        return ctrl.Result{}, err
    }

    // 8. 伪引导
    if err := e.reconcileFakeBootstrap(); err != nil {
        return ctrl.Result{}, err
    }

    // 9. 兼容性补丁
    if err := e.compatibilityPatch(); err != nil {
        return ctrl.Result{}, err
    }

    // 10. 标记完全控制
    clusterutil.MarkClusterFullyControlled(e.Ctx.BKECluster)

    return ctrl.Result{Requeue: true}, nil
}
```

### 五、详细流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│  Execute()                                                          │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  1. getRemoteClient()                                               │
│     • 获取远程集群客户端                                               │
│     • 连接目标集群                                                    │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. collectBaseInfo()                                               │
│     • 收集集群基础信息                                                 │
│     • K8s 版本、节点信息、网络配置                                       │
│     • 标记 NodesEnvCondition = True                                 │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  3. pushAgent()                                                     │
│     • 使用 DaemonSet 推送 Agent                                     │
│     • 等待 Launcher Pod 完成                                        │
│     • Ping Agent 验证                                               │
│     • 标记 BKEAgentCondition = True                                 │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  4. collectAgentInfo()                                              │
│     • 使用 Agent 收集证书                                           │
│     • 获取容器运行时配置                                             │
│     • 标记 NodesInfoCondition = True                                │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  5. IsBocloudCluster?                                               │
└─────────────────────────────────────────────────────────────────────┘
        │
        ├── 否 → 结束（其他类型集群）
        │
        └── 是 ↓
┌─────────────────────────────────────────────────────────────────────┐
│  6. OwnerReferences == nil?                                         │
└─────────────────────────────────────────────────────────────────────┘
        │
        ├── 是 → Requeue（等待 CAPI 对象创建）
        │
        └── 否 ↓
┌─────────────────────────────────────────────────────────────────────┐
│  7. bocloudClusterManagePrepare()                                   │
│     • 管理前准备                                                     │
│     • 备份数据、分发证书                                             │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  8. reconcileFakeBootstrap()                                        │
│     • 伪引导 Master 节点                                            │
│     • 伪引导 Worker 节点                                            │
│     • 创建 CAPI Machine 对象                                        │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  9. compatibilityPatch()                                            │
│     • 兼容性补丁                                                     │
│     • 设置 etcd Pod 注解                                            │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  10. MarkClusterFullyControlled()                                   │
│      • 标记完全控制                                                  │
│      • 设置 annotation: bke.bocloud.com/full-management = "true"    │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
    Requeue
```

### 六、核心功能详解

#### 1. collectBaseInfo() - 集群基础信息收集

```go
func (e *EnsureClusterManage) collectBaseInfo() error {
    // 检查是否已收集
    if clusterutil.ClusterBaseInfoHasCollected(bkeCluster) {
        return nil
    }

    // 使用远程客户端收集信息
    collectRes, warns, errs := e.remoteClient.Collect()

    // 更新 BKECluster
    patchFunc := func(bkeCluster *bkev1beta1.BKECluster) {
        clusterutil.MarkClusterBaseInfoCollected(bkeCluster)
        bkeCluster.Spec.ClusterConfig.Cluster.Networking = collectRes.Networking
        bkeCluster.Spec.ControlPlaneEndpoint = collectRes.ControlPlaneEndpoint
        bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion = collectRes.KubernetesVersion
        bkeCluster.Status.KubernetesVersion = collectRes.KubernetesVersion
        bkeCluster.Spec.ClusterConfig.Cluster.ContainerRuntime = collectRes.ContainerRuntime
        // ...
    }

    return mergecluster.SyncStatusUntilComplete(c, bkeCluster, patchFunc)
}
```

**收集的信息**：

| 信息类型 | 说明 |
|---------|------|
| `Networking` | 集群网络配置（ServiceCIDR、PodCIDR） |
| `ControlPlaneEndpoint` | 控制平面端点 |
| `KubernetesVersion` | Kubernetes 版本 |
| `ContainerRuntime` | 容器运行时（containerd/docker） |
| `Nodes` | 节点信息 |
| `EtcdCertificatesDir` | etcd 证书目录 |

#### 2. pushAgent() - 推送 Agent

```go
func (e *EnsureClusterManage) pushAgent() error {
    // 1. 检查是否需要推送
    if !e.checkAgentNeedPush(nodes) {
        return nil
    }

    // 2. 获取本地 kubeconfig
    localKubeConfig, err := phaseutil.GetLocalKubeConfig(ctx, c)

    // 3. 创建 Launcher DaemonSet
    launcherAddonT := &bkeaddon.AddonTransfer{
        Addon: &v1beta1.Product{
            Name: "bkeagent",
            Param: map[string]string{
                "clusterName":     bkeCluster.Name,
                "kubeconfig":      string(localKubeConfig),
                "launcherImage":   lancherImage,
                // ...
            },
        },
        Operate: bkeaddon.CreateAddon,
    }

    // 4. 安装 Launcher
    if err = e.remoteClient.InstallAddon(bkeCluster, launcherAddonT, nil, nil, nodes); err != nil {
        return err
    }

    // 5. 等待 Launcher Pod 完成
    if err := e.waitForLauncherPodsComplete(ctx, bkeCluster); err != nil {
        // Continue even if wait fails
    }

    // 6. 删除 Launcher DaemonSet
    launcherAddonT.Operate = bkeaddon.RemoveAddon
    e.remoteClient.InstallAddon(bkeCluster, launcherAddonT, nil, nil, nodes)

    // 7. Ping Agent 验证
    err, successNodes, failedNodes := phaseutil.PingBKEAgent(ctx, c, scheme, bkeCluster)

    // 8. 标记 Agent 已推送
    condition.ConditionMark(bkeCluster, bkev1beta1.BKEAgentCondition, 
        confv1beta1.ConditionTrue, constant.BKEAgentReadyReason, "")

    return nil
}
```

**流程**：
```
创建 Launcher DaemonSet
        ↓
Launcher Pod 启动
        ↓
停止旧 Agent
        ↓
启动新 Agent
        ↓
启动 HTTP Server
        ↓
删除 Launcher DaemonSet
        ↓
Ping Agent 验证
```

#### 3. collectAgentInfo() - 使用 Agent 收集信息

```go
func (e *EnsureClusterManage) collectAgentInfo() error {
    // 检查是否已收集
    if clusterutil.ClusterAgentInfoHasCollected(bkeCluster) {
        return nil
    }

    // 创建收集命令
    collectCommand := command.Collect{
        BaseCommand:         createBaseCommand(baseCommandParams),
        Node:                &bkeNodes.Master()[0],
        EtcdCertificatesDir: etcdCertDir,
        K8sCertificatesDir:  k8sCertDir,
    }

    // 执行收集
    if err := collectCommand.New(); err != nil {
        return err
    }

    // 等待完成
    err, _, failedNode := collectCommand.Wait()

    // 获取容器运行时配置
    if err = e.getContainerRuntimeConfigFromCollectCommand(collectCommand.Command); err != nil {
        return err
    }

    return nil
}
```
**收集的信息**：

| 信息类型 | 说明 |
|---------|------|
| `Certificates` | K8s 证书、etcd 证书 |
| `ContainerRuntimeConfig` | 容器运行时配置 |
| `CgroupDriver` | Cgroup 驱动 |
| `DataRoot` | 数据目录 |

#### 4. reconcileFakeBootstrap() - 伪引导

```go
func (e *EnsureClusterManage) reconcileFakeBootstrap() error {
    // 伪引导 Master 节点
    if err := e.fakeBootstrapMaster(); err != nil {
        return err
    }

    // 伪引导 Worker 节点
    if err := e.fakeBootstrapWorker(); err != nil {
        return err
    }

    return nil
}
```
**伪引导含义**：
```
伪引导 = 将现有集群的节点映射为 CAPI Machine 对象

目的：
• 使现有集群可以被 CAPI 管理
• 不重新安装节点，仅创建映射关系
• 为后续的扩缩容、升级做准备
```

#### 5. fakeBootstrapMaster() - 伪引导 Master

```go
func (e *EnsureClusterManage) fakeBootstrapMaster() error {
    // 1. 更新 KCP Replicas = 当前 Master 数量
    expectKcpReplicas := int32(bkeNodes.Master().Length())
    if err := e.updateKubeadmControlPlaneReplicas(ctx, c, expectKcpReplicas); err != nil {
        return err
    }

    // 2. 设置 BKECluster.Status.Ready = true
    if err := e.waitForClusterInfrastructureReady(ctx, c, bkeCluster); err != nil {
        return err
    }

    // 3. 等待所有 Master 节点伪引导完成
    successJoinMasterNodes, err := e.waitForMasterNodesBootstrap(ctx, c, bkeCluster, 
        bkeNodes.Master(), expectKcpReplicas, log)

    // 4. 标记节点引导成功
    e.markNodesBootstrapSuccess(ctx, successJoinMasterNodes, log)

    return nil
}
```

**流程**：
```
1. 设置 KCP.Replicas = 当前 Master 数量
        ↓
2. 设置 BKECluster.Status.Ready = true
        ↓
3. 等待 Cluster.InfrastructureReady = true
        ↓
4. 等待 ControlPlaneInitialized = true
        ↓
5. 等待所有 Machine 创建并就绪
        ↓
6. 标记节点引导成功
```

#### 6. fakeBootstrapWorker() - 伪引导 Worker

```go
func (e *EnsureClusterManage) fakeBootstrapWorker() error {
    workerNum := bkeNodes.Worker().Length()
    if workerNum == 0 {
        return nil
    }

    // 1. 更新 MachineDeployment Replicas = 当前 Worker 数量
    md, err := phaseutil.GetClusterAPIMachineDeployment(ctx, c, e.Ctx.Cluster)
    md.Spec.Replicas = &expectMDReplicas
    if err = phaseutil.ResumeClusterAPIObj(ctx, c, md); err != nil {
        return err
    }

    // 2. 等待所有 Worker 节点伪引导完成
    err = wait.PollImmediateUntil(pollInterval, func() (bool, error) {
        return waitForNodesBootstrap(ctx, c, bkeCluster, workerNodes, 
            successJoinWorkerNodes, expectMDReplicas, log) == nil, nil
    }, ctxWithTimeout.Done())

    // 3. 标记节点引导成功
    e.markNodesBootstrapSuccess(ctx, successJoinWorkerNodes, log)

    return nil
}
```

#### 7. compatibilityPatch() - 兼容性补丁

```go
func (e *EnsureClusterManage) compatibilityPatch() error {
    // 获取所有 etcd Pod
    etcdPods, err := clientSet.CoreV1().Pods(metav1.NamespaceSystem).List(ctx, 
        metav1.ListOptions{LabelSelector: "component=etcd"})

    // 为每个 etcd Pod 设置注解
    for _, pod := range etcdPods.Items {
        nodeName := pod.Spec.NodeName
        nodes := bkeNodes.Filter(bkenode.FilterOptions{"Hostname": nodeName})

        // 设置 etcd advertise-client-urls 注解
        annotation.SetAnnotation(&pod, 
            annotation.EtcdAdvertiseClientUrlsAnnotationKey,
            phaseutil.GetClientURLByIP(nodes[0].IP))

        // 更新 etcd Pod
        _, err = clientSet.CoreV1().Pods(metav1.NamespaceSystem).Update(ctx, &pod, 
            metav1.UpdateOptions{})
    }

    return nil
}
```
**目的**：
- ✅ 兼容 ansible 部署的集群
- ✅ 为 etcd Pod 设置 `bkeagent.bocloud.com/etcd.advertise-client-urls` 注解
- ✅ 确保后续升级正常

### 七、Condition 管理

#### Condition 设置

| Condition | 状态 | 原因 | 设置时机 |
|-----------|------|------|---------|
| `NodesEnvCondition` | True | NodesEnvReady | collectBaseInfo 后 |
| `NodesInfoCondition` | True | NodesInfoReady | 收集到节点后 |
| `BKEAgentCondition` | True | BKEAgentReady | pushAgent 后 |

### 八、Annotation 管理

#### 关键 Annotation

| Annotation | 说明 | 设置时机 |
|-----------|------|---------|
| `bke.bocloud.com/cluster-collected` | 信息收集标记 | collectBaseInfo 后 |
| `etcd-cert-dir` | etcd 证书目录 | collectBaseInfo 后 |
| `bke.bocloud.com/full-management` | 完全控制标记 | Execute 结束时 |

### 九、集群类型处理

#### 不同集群类型的处理

| 集群类型 | 处理方式 | 说明 |
|---------|---------|------|
| **BKE 自建集群** | 跳过 | NeedExecute 返回 false |
| **Bocloud 托管集群** | 完全纳管 | 执行所有步骤，包括伪引导 |
| **其他类型集群** | 仅收集信息 | 执行到 collectAgentInfo 后结束 |

### 十、设计原理

#### 1. 为什么需要伪引导？

```
┌─────────────────────────────────────────────────────────────────────┐
│  现有集群                                                            │
│  • 已有节点                                                          │
│  • 已有 K8s 组件                                                     │
│  • 无 CAPI 对象                                                      │
└─────────────────────────────────────────────────────────────────────┘
        │
        │ 伪引导
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  CAPI 管理的集群                                                     │
│  • 创建 Cluster 对象                                                 │
│  • 创建 KCP 对象                                                     │
│  • 创建 Machine 对象                                                 │
│  • 节点映射为 Machine                                                │
└─────────────────────────────────────────────────────────────────────┘
        │
        │ 后续管理
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  BKE 管理能力                                                        │
│  • 扩缩容                                                            │
│  • 升级                                                               │
│  • 节点维护                                                           │
└─────────────────────────────────────────────────────────────────────┘
```

#### 2. 为什么需要 Agent？

```
┌─────────────────────────────────────────────────────────────────────┐
│  BKE Agent 作用                                                     │
└─────────────────────────────────────────────────────────────────────┘

1. 信息收集
   • 证书信息
   • 容器运行时配置
   • 节点状态

2. 命令执行
   • 安装组件
   • 配置修改
   • 节点操作

3. 健康检查
   • HTTP 健康端点
   • 状态上报
```

### 十一、总结

#### 1. 核心作用

| 作用 | 说明 |
|------|------|
| **纳管集群** | 将现有集群转换为 BKE 管理模式 |
| **信息收集** | 收集集群基础信息和 Agent 信息 |
| **推送 Agent** | 在节点上部署 BKE Agent |
| **伪引导** | 创建 CAPI 对象映射现有节点 |
| **兼容性处理** | 兼容不同部署方式的集群 |

#### 2. 关键特性

- ⚠️ **支持多种集群类型**：BKE、Bocloud、其他
- ⚠️ **伪引导机制**：不重新安装，仅创建映射
- ⚠️ **Agent 管理**：推送、验证、健康检查
- ⚠️ **兼容性补丁**：兼容 ansible 部署的集群
- ⚠️ **Condition 管理**：追踪各阶段状态

#### 3. 执行条件

- ✅ 非 BKE 自建集群
- ✅ 未完全控制
- ✅ 基础条件满足

#### 4. 使用场景

- ✅ 纳管已存在的 Bocloud 托管集群
- ✅ 纳管其他类型集群（仅收集信息）
- ✅ 为后续扩缩容、升级做准备

# `ensure_cluster_manage.go` 中"纳管"的含义
        
## `ensure_cluster_manage.go` 中"纳管"的含义详解

### 一、纳管的定义

#### 1. 什么是纳管？

```
┌─────────────────────────────────────────────────────────────────────┐
│  纳管 = 将已存在的集群纳入 BKE 管理体系                                  │
└─────────────────────────────────────────────────────────────────────┘

核心含义：
• 已存在的集群：由其他方式部署的集群（ansible、kubeadm、rke 等）
• 纳入管理：使集群可以被 BKE 统一管理
• 不重新部署：保持集群原有状态，仅建立管理关系
```

### 二、纳管前后对比

#### 1. 纳管前（原始状态）

```
┌─────────────────────────────────────────────────────────────────────┐
│  已存在的集群                                                         │
└─────────────────────────────────────────────────────────────────────┘

特点：
• 集群已运行，节点已就绪
• 无 BKE 相关资源（BKECluster、BKENode）
• 无 CAPI 资源
• 无 BKE Agent
• 无法通过 BKE 进行扩缩容、升级

示例：
┌─────────────────────────────────────────────────────────────────────┐
│  Kubernetes 集群                                                     │
│  • 3 个 Master 节点                                                  │
│  • 5 个 Worker 节点                                                  │
│  • K8s 版本：v1.25.0                                                 │
│  • 部署方式：ansible                                                  │
│                                                                     │
│  问题：                                                              │
│  • 无法通过 BKE 扩缩容                                                │
│  • 无法通过 BKE 升级                                                  │
│  • 无法通过 BKE 监控                                                  │
└─────────────────────────────────────────────────────────────────────┘
```

#### 2. 纳管后（BKE 管理状态）

```
┌─────────────────────────────────────────────────────────────────────┐
│  BKE 管理的集群                                                       │
└─────────────────────────────────────────────────────────────────────┘

特点：
• 集群状态不变（节点、组件保持原样）
• 创建 BKECluster 资源
• 创建 BKENode 资源（映射节点）
• 创建 CAPI 资源
• 部署 BKE Agent
• 可以通过 BKE 进行扩缩容、升级

示例：
┌─────────────────────────────────────────────────────────────────────┐
│  BKE 管理体系                                                        │
│                                                                     │
│  BKECluster: my-cluster                                             │
│    ├── Cluster (CAPI)                                               │
│    ├── KubeadmControlPlane (CAPI)                                   │
│    │   └── Machine x 3 (映射 3 个 Master)                            │
│    ├── MachineDeployment (CAPI)                                     │
│    │   └── Machine x 5 (映射 5 个 Worker)                            │
│    └── BKENode x 8 (映射所有节点)                                     │
│                                                                     │
│  能力：                                                              │
│  • 扩缩容：修改 Replicas                                              │
│  • 升级：修改 KubernetesVersion                                       │
│  • 监控：通过 Agent 上报状态                                           │
└─────────────────────────────────────────────────────────────────────┘
```

### 三、纳管的核心过程

#### 1. 完整流程

```
┌─────────────────────────────────────────────────────────────────────┐
│  纳管流程                                                            │
└─────────────────────────────────────────────────────────────────────┘

步骤 1: 连接集群
    • 获取远程集群客户端
    • 验证集群可访问
        ↓
步骤 2: 信息收集
    • 收集 K8s 版本
    • 收集节点信息
    • 收集网络配置
    • 收集控制平面端点
        ↓
步骤 3: 推送 Agent
    • 创建 Launcher DaemonSet
    • 在每个节点部署 BKE Agent
    • 验证 Agent 可达
        ↓
步骤 4: 深度信息收集
    • 收集证书信息
    • 收集容器运行时配置
    • 收集 etcd 配置
        ↓
步骤 5: 创建 CAPI 对象
    • 创建 Cluster 对象
    • 创建 KubeadmControlPlane 对象
    • 创建 MachineDeployment 对象
        ↓
步骤 6: 伪引导
    • 创建 Machine 对象（映射现有节点）
    • 设置 Machine 状态为已就绪
    • 不重新安装节点
        ↓
步骤 7: 兼容性处理
    • 设置 etcd Pod 注解
    • 处理不同部署方式的差异
        ↓
步骤 8: 标记完全控制
    • 设置 FullyControlled 标记
    • 纳管完成
```

#### 2. 伪引导详解

```
┌─────────────────────────────────────────────────────────────────────┐
│  伪引导 = 创建 Machine 对象映射现有节点                                 │
└─────────────────────────────────────────────────────────────────────┘

为什么叫"伪"引导？
• 真引导：从零开始安装节点，执行 kubeadm join
• 伪引导：节点已存在，仅创建 Machine 对象映射

伪引导过程：
┌─────────────────────────────────────────────────────────────────────┐
│  现有节点                                                            │
│  • master-1 (192.168.1.10)                                          │
│  • master-2 (192.168.1.11)                                          │
│  • master-3 (192.168.1.12)                                          │
└─────────────────────────────────────────────────────────────────────┘
        │
        │ 伪引导
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  创建 Machine 对象                                                   │
│                                                                     │
│  Machine/master-1:                                                  │
│    spec:                                                            │
│      clusterName: my-cluster                                        │
│      bootstrap:                                                     │
│        dataSecretName: master-1-bootstrap  # 已存在的 secret         │
│    status:                                                          │
│      ready: true                    # 直接标记为就绪                  │
│      nodeRef:                                                       │
│        name: master-1               # 关联现有节点                    │
│                                                                     │
│  Machine/master-2: ...                                              │
│  Machine/master-3: ...                                              │
└─────────────────────────────────────────────────────────────────────┘

关键点：
• 不执行 kubeadm join
• 不重新安装节点
• 仅创建 Machine 对象建立映射关系
• 直接标记 Machine 为 ready 状态
```

### 四、纳管的层次

#### 1. 不同集群类型的纳管层次

| 集群类型 | 纳管层次 | 说明 |
|---------|---------|------|
| **BKE 自建集群** | 无需纳管 | 从创建开始就由 BKE 管理 |
| **Bocloud 托管集群** | 完全纳管 | 创建所有 CAPI 对象，支持扩缩容、升级 |
| **其他类型集群** | 信息收集 | 仅收集信息，不创建 CAPI 对象 |

#### 2. 纳管层次详解

```
┌─────────────────────────────────────────────────────────────────────┐
│  纳管层次 1: 信息收集                                                  │
└─────────────────────────────────────────────────────────────────────┘

适用：所有类型集群

操作：
• collectBaseInfo()：收集基础信息
• pushAgent()：推送 Agent
• collectAgentInfo()：收集深度信息

目的：
• 了解集群状态
• 为后续管理做准备
• 不改变集群配置

┌─────────────────────────────────────────────────────────────────────┐
│  纳管层次 2: 完全纳管                                                  │
└─────────────────────────────────────────────────────────────────────┘

适用：Bocloud 托管集群

操作：
• 信息收集（层次 1）
• bocloudClusterManagePrepare()：管理准备
• reconcileFakeBootstrap()：伪引导
• compatibilityPatch()：兼容性处理
• MarkClusterFullyControlled()：标记完全控制

目的：
• 创建 CAPI 对象
• 建立管理关系
• 支持扩缩容、升级
```

### 五、纳管的关键标记

#### 1. FullyControlled 标记

```go
// 标记完全控制
clusterutil.MarkClusterFullyControlled(e.Ctx.BKECluster)

// 实现
func MarkClusterFullyControlled(bkeCluster client.Object) {
    annotation.SetAnnotation(bkeCluster, 
        annotation.KONKFullManagementClusterAnnotationKey, "true")
}
```
**含义**：
```yaml
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/full-management: "true"  # ← 完全控制标记
```
**作用**：
- ✅ 标识集群已完全纳管
- ✅ 其他 Phase 可以正常执行（扩缩容、升级）
- ✅ NeedExecute 判断会跳过已纳管的集群

#### 2. ClusterFrom 标记

```go
// 判断集群类型
func IsBocloudCluster(bkeCluster client.Object) bool {
    v, ok := annotation.HasAnnotation(bkeCluster, common.BKEClusterFromAnnotationKey)
    return ok && v == common.BKEClusterFromAnnotationValueBocloud
}
```

**含义**：
```yaml
# Bocloud 托管集群
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/cluster-from: "bocloud"

# BKE 自建集群
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/cluster-from: "bke"

# 其他类型集群
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/cluster-from: "other"
```

### 六、纳管的意义

#### 1. 统一管理

```
┌─────────────────────────────────────────────────────────────────────┐
│  纳管前：多套管理工具                                                  │
└─────────────────────────────────────────────────────────────────────┘

集群 A (ansible 部署) → 使用 ansible 管理
集群 B (kubeadm 部署) → 使用 kubeadm 管理
集群 C (rke 部署)     → 使用 rke 管理

问题：
• 管理工具分散
• 操作方式不统一
• 无法统一监控

┌─────────────────────────────────────────────────────────────────────┐
│  纳管后：统一 BKE 管理                                                 │
└─────────────────────────────────────────────────────────────────────┘

集群 A → BKE 管理
集群 B → BKE 管理
集群 C → BKE 管理

优势：
• 统一管理界面
• 统一操作方式
• 统一监控告警
• 统一升级策略
```

#### 2. 能力增强

```
┌─────────────────────────────────────────────────────────────────────┐
│  纳管后获得的能力                                                      │
└─────────────────────────────────────────────────────────────────────┘

1. 扩缩容
   • 通过修改 Replicas 扩缩容
   • 自动管理节点生命周期

2. 升级
   • 通过修改 KubernetesVersion 升级
   • 自动处理升级流程

3. 监控
   • 通过 Agent 上报状态
   • 统一监控告警

4. 维护
   • 节点维护模式
   • 自动驱逐 Pod

5. 备份恢复
   • etcd 备份
   • 集群恢复
```

### 七、纳管的约束

#### 1. 执行条件

```go
func (e *EnsureClusterManage) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. BKE 自建集群不执行
    if clusterutil.IsBKECluster(new) {
        return false  // BKE 自建集群从创建开始就由 BKE 管理
    }

    // 2. 已完全控制不执行
    if clusterutil.FullyControlled(new) {
        return false  // 已纳管，无需重复执行
    }

    return true
}
```

**约束**：

| 条件 | 说明 |
|------|------|
| `!IsBKECluster` | 非 BKE 自建集群才执行 |
| `!FullyControlled` | 未完全控制才执行 |

#### 2. CAPI 对象依赖

```go
// CAPI 对象未创建，等待
if e.Ctx.BKECluster.OwnerReferences == nil {
    return ctrl.Result{Requeue: true}, nil
}
```

**含义**：
- ⚠️ 纳管需要先创建 CAPI 对象（由 `EnsureClusterAPIObj` Phase 完成）
- ⚠️ `OwnerReferences` 存在表示 CAPI 对象已创建
- ⚠️ 未创建则 Requeue 等待

### 八、纳管示例

#### 示例：纳管 ansible 部署的集群

```
┌─────────────────────────────────────────────────────────────────────┐
│  原始集群                                                            │
└─────────────────────────────────────────────────────────────────────┘

集群信息：
• 名称：prod-cluster
• 部署方式：ansible
• K8s 版本：v1.24.0
• 节点：3 master + 5 worker
• 无 BKE 相关资源

┌─────────────────────────────────────────────────────────────────────┐
│  纳管过程                                                            │
└─────────────────────────────────────────────────────────────────────┘

步骤 1: 创建 BKECluster
    BKECluster/prod-cluster:
      metadata:
        annotations:
          bke.bocloud.com/cluster-from: "bocloud"
      spec:
        clusterConfig:
          cluster:
            kubernetesVersion: "v1.24.0"

步骤 2: 信息收集
    • K8s 版本：v1.24.0
    • 节点信息：8 个节点
    • 网络配置：ServiceCIDR、PodCIDR

步骤 3: 推送 Agent
    • 在每个节点部署 BKE Agent
    • Agent 上报节点状态

步骤 4: 创建 CAPI 对象
    Cluster/prod-cluster
    KubeadmControlPlane/prod-cluster (replicas: 3)
    MachineDeployment/prod-cluster-worker (replicas: 5)

步骤 5: 伪引导
    Machine/prod-cluster-master-1 → 映射 master-1
    Machine/prod-cluster-master-2 → 映射 master-2
    Machine/prod-cluster-master-3 → 映射 master-3
    Machine/prod-cluster-worker-1 → 映射 worker-1
    ...

步骤 6: 兼容性处理
    • 设置 etcd Pod 注解

步骤 7: 标记完全控制
    BKECluster/prod-cluster:
      metadata:
        annotations:
          bke.bocloud.com/full-management: "true"

┌─────────────────────────────────────────────────────────────────────┐
│  纳管后                                                              │
└─────────────────────────────────────────────────────────────────────┘

能力：
• 扩容：修改 MachineDeployment.Replicas → 10
• 缩容：修改 MachineDeployment.Replicas → 3
• 升级：修改 KubernetesVersion → v1.25.0
• 监控：通过 Agent 监控节点状态
```

### 九、总结

#### 1. 纳管的核心含义

| 含义 | 说明 |
|------|------|
| **不重新部署** | 保持集群原有状态 |
| **建立管理关系** | 创建 CAPI 对象映射现有节点 |
| **统一管理** | 纳入 BKE 管理体系 |
| **能力增强** | 获得扩缩容、升级等能力 |

#### 2. 纳管的关键步骤

| 步骤 | 作用 |
|------|------|
| **信息收集** | 了解集群状态 |
| **推送 Agent** | 建立管理通道 |
| **创建 CAPI 对象** | 建立管理关系 |
| **伪引导** | 映射现有节点 |
| **兼容性处理** | 处理差异 |
| **标记完全控制** | 标识纳管完成 |

#### 3. 纳管的价值

- ✅ **统一管理**：多套集群统一管理
- ✅ **能力增强**：获得扩缩容、升级能力
- ✅ **运维简化**：统一操作方式
- ✅ **监控统一**：统一监控告警

