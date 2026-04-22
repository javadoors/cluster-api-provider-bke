# EnsureCluster Phase 业务流程
## EnsureCluster Phase 业务流程
### 一、整体流程图
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     EnsureCluster.Execute() 执行流程                        │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
                    ┌────────────────────────────────┐
                    │ 1. 前置校验                    │
                    │   ├── Cluster != nil ?         │
                    │   └── ControlPlaneInitialized? │
                    └───────────────┬────────────────┘
                                    │ 通过
                                    ▼
                    ┌──────────────────────────────────┐
                    │ 2. 获取远程集群 Client           │
                    │   getRemoteClient()              │
                    └───────────────┬──────────────────┘
                                    │
                                    ▼
                    ┌───────────────────────────────────┐
                    │ 3. 设置告警标签（仅 BKE 集群）    │
                    │   setAlertLabel()                 │
                    └───────────────┬───────────────────┘
                                    │
                                    ▼
                    ┌───────────────────────────────────┐
                    │ 4. 设置裸金属标签（仅 richrunc）  │
                    │   setBareMetalLabel()             │
                    └───────────────┬───────────────────┘
                                    │
                                    ▼
                    ┌──────────────────────────────────┐
                    │ 5. 设置节点标签                  │
                    │   setNodeLabel()                 │
                    └───────────────┬──────────────────┘
                                    │
                                    ▼
                    ┌─────────────────────────────────┐
                    │ 6. 确保 K8s Token 存在          │
                    │   ensureK8sToken()              │
                    └───────────────┬─────────────────┘
                                    │
                                    ▼
                    ┌──────────────────────────────────┐
                    │ 7. 特殊状态检查                  │
                    │   isClusterInSpecialState()      │
                    │   ├── 扩缩容中 → 直接返回        │
                    │   ├── 初始化中 → 直接返回        │
                    │   ├── 暂停中   → 直接返回        │
                    │   └── 升级中   → 直接返回        │
                    └───────────────┬──────────────────┘
                                    │ 非特殊状态
                                    ▼
                    ┌──────────────────────────────────┐
                    │ 8. 后置处理完成检查              │
                    │   GetNeedPostProcessNodes > 0?   │
                    │   └── 未完成 → 10s 后重试        │
                    └───────────────┬──────────────────┘
                                    │ 已完成
                                    ▼
                    ┌──────────────────────────────────┐
                    │ 9. 集群健康检查                  │
                    │   ensureClusterReady()           │
                    │   └── 失败 → 10s 后重试          │
                    └───────────────┬──────────────────┘
                                    │ 成功
                                    ▼
                    ┌──────────────────────────────────┐
                    │ 10. 定时 5min 重新调谐           │
                    │   RequeueAfter: 5min             │
                    └──────────────────────────────────┘
```
### 二、各步骤详解
#### 步骤 1：前置校验
```go
if e.Ctx.Cluster == nil {
    return ctrl.Result{}, errors.Errorf("cluster is nil")
}
if !conditions.IsTrue(e.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
    return ctrl.Result{}, errors.Errorf("cluster is not init")
}
```

| 校验项 | 条件 | 失败行为 |
|--------|------|---------|
| Cluster 对象存在 | `Cluster != nil` | 返回错误 |
| 控制面已初始化 | `ControlPlaneInitialized == True` | 返回错误 |
#### 步骤 2：获取远程集群 Client
```go
remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
e.remoteClient = remoteClient
```
- 通过 BKECluster 的 kubeconfig 创建远程集群的 Kubernetes Client
- 后续所有对目标集群的操作都通过此 Client 执行
#### 步骤 3：设置告警标签（仅 BKE 创建的集群）
```go
if clusterutil.IsBKECluster(e.Ctx.BKECluster) {
    e.setAlertLabel()  // ignore error
}
```
**setAlertLabel() 逻辑**：
```
1. 查找已有 alert 标签的节点
   └── LabelSelector: alert = "enabled"
       ├── 已存在 → 直接返回
       └── 不存在 → 继续

2. 查找 worker 角色节点
   └── LabelSelector: node-role = "node"
       ├── 无 worker 节点 → 跳过（仅 warn）
       └── 有 worker 节点 → 选择第一个

3. 给选中的 worker 节点打上 alert 标签
   └── alert = "enabled"
```
**目的**：为告警组件指定一个 worker 节点作为告警采集点。
#### 步骤 4：设置裸金属标签（仅 richrunc 运行时）
```go
if e.Ctx.BKECluster.Spec.ClusterConfig.Cluster.ContainerRuntime.Runtime == "richrunc" {
    e.setBareMetalLabel()  // ignore error
}
```
**setBareMetalLabel() 逻辑**：
```
1. 列出所有节点
2. 遍历每个节点：
   ├── 已有 baremetal 标签 → 跳过
   └── 无 baremetal 标签 → 打上 baremetal=true
```
**目的**：当容器运行时为 richrunc（裸金属容器）时，标记所有节点为裸金属节点。
#### 步骤 5：设置节点标签
```go
e.setNodeLabel()
```
**setNodeLabel() 逻辑**：
```
1. 获取全局标签（BKECluster.Spec.ClusterConfig.Cluster.Labels）
2. 获取节点标签配置（BKECluster.Spec 中的 Node 列表）
3. 构建 节点→标签 映射：
   └── 每个节点的标签 = 节点专属标签 + 全局标签（节点专属优先）
4. 遍历远程集群的所有节点：
   └── 对每个节点应用映射中的标签
       └── 标签已一致 → 跳过
       └── 标签不一致 → 更新（带冲突重试，超时 1min）
```
**标签合并规则**：
- 节点专属标签优先级高于全局标签
- 全局标签作为默认值补充
#### 步骤 6：确保 K8s Token 存在
```go
e.ensureK8sToken()
```

**ensureK8sToken() 逻辑**：
```
1. 查找 K8s Token Secret
   ├── 不存在 → 创建新 Token
   │   ├── 调用远程集群生成 Token
   │   └── 保存为 Secret（归属 BKECluster）
   └── 已存在 → 检查内容
       ├── 无 OwnerReference → 添加 BKECluster 作为 Owner
       ├── Token 为空 → 重新生成
       └── Token 有效 → 标记 Condition
```
**目的**：确保管理面有访问目标集群的长期 Token，用于后续操作。
#### 步骤 7：特殊状态检查
```go
if isClusterInSpecialState(bkeCluster) {
    return ctrl.Result{}, kerrors.NewAggregate(errs)
}
```

**特殊状态列表**：

| 状态 | 含义 |
|------|------|
| `ClusterMasterScalingUp` | Master 扩容中 |
| `ClusterMasterScalingDown` | Master 缩容中 |
| `ClusterWorkerScalingUp` | Worker 扩容中 |
| `ClusterWorkerScalingDown` | Worker 缩容中 |
| `ClusterInitializing` | 集群初始化中 |
| `ClusterPaused` | 集群暂停 |
| `ClusterUpgrading` | 集群升级中 |

**目的**：在扩缩容、初始化、暂停、升级期间跳过健康检查，避免误判。
#### 步骤 8：后置处理完成检查
```go
bkeNodes, _ := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
if phaseutil.GetNeedPostProcessNodesWithBKENodes(bkeCluster, bkeNodes).Length() > 0 {
    condition.ConditionMark(bkeCluster, NodesPostProcessCondition, False)
    return ctrl.Result{RequeueAfter: 10s}, errors.Errorf("postprocess not finished")
}
```
**目的**：确保所有节点的后置脚本（EnsureNodesPostProcess）执行完毕后，才进入健康检查，避免将未完成配置的节点误判为健康。
#### 步骤 9：集群健康检查
```go
e.ensureClusterReady()
```
这是核心步骤，详细流程如下：
```
ensureClusterReady()
    │
    ├── 首次部署检查
    │   └── ClusterHealthState == Deploying && !ClusterEndDeployed
    │       └── 返回 "cluster is deploying"
    │
    └── runHealthChecks() → 连续执行 3 次健康检查
        └── performHealthCheck() × 3
            │
            ├── 1. 获取 BKENodes
            ├── 2. CheckClusterHealth() → 远程集群健康检查
            ├── 3. 更新集群版本状态
            ├── 4. 设置 ClusterStatus = Ready
            └── 5. Report() 上报状态
```
### 三、CheckClusterHealth 详细流程
```
CheckClusterHealth(cluster, currentVersion, bkeNodes)
    │
    ├── 阶段 1：节点健康检查
    │   └── for each node in remote cluster:
    │       ├── needSkip? → 跳过
    │       ├── NodeReady? → 设置 NodeReady 状态
    │       └── NodeHealthCheck()
    │           ├── checkNodeReady() → NodeReady Condition == True?
    │           ├── checkNodeVersion() → KubeletVersion == expectVersion?
    │           └── IsMasterNode? → CheckComponentHealth()
    │               ├── kube-apiserver-{node} Pod Running?
    │               ├── kube-controller-manager-{node} Pod Running?
    │               └── kube-scheduler-{node} Pod Running?
    │
    └── 阶段 2：组件健康检查
        └── CheckAllComponentsHealth()
            │
            ├── 必检组件（neededComponentChecks）
            │   └── kube-system 命名空间：
            │       ├── calico-kube-controllers  → Running?
            │       ├── calico-node              → Running?
            │       ├── coredns                  → Running?（至少 1 个即可）
            │       ├── etcd-*                   → Running?
            │       ├── kube-apiserver-*         → Running?
            │       ├── kube-controller-manager-*→ Running?
            │       ├── kube-proxy-*             → Running?
            │       └── kube-scheduler-*         → Running?
            │
            └── 扩展组件（extraAddonComponents，按 Addon 配置）
                ├── cluster-api Addon:
                │   ├── cluster-system/capi-controller-manager → Running?
                │   └── cluster-system/bke-controller-manager  → Running?
                │
                └── openfuyao-system-controller Addon:
                    ├── kube-system/metrics-server-           → Running?
                    ├── ingress-nginx/ingress-nginx-controller→ Running?
                    ├── monitoring/:
                    │   ├── alertmanager-main-                → Running?
                    │   ├── blackbox-exporter-                → Running?
                    │   ├── kube-state-metrics-               → Running?
                    │   ├── node-exporter-                    → Running?
                    │   ├── prometheus-k8s-                   → Running?
                    │   └── prometheus-operator-              → Running?
                    ├── openfuyao-system/:
                    │   ├── application-management-service-   → Running?
                    │   ├── console-service-                  → Running?
                    │   ├── console-website-                  → Running?
                    │   ├── local-harbor-*                    → Running?
                    │   ├── marketplace-service-              → Running?
                    │   ├── monitoring-service-               → Running?
                    │   ├── oauth-server-                     → Running?
                    │   ├── oauth-webhook-                    → Running?
                    │   ├── plugin-management-service-        → Running?
                    │   ├── user-management-operator-         → Running?
                    │   └── web-terminal-service-             → Running?
                    └── openfuyao-system-controller/:
                        └── openfuyao-system-controller-      → Running?
```
**组件检查规则**：
- **必检组件**（`neededAddons`）：kubeproxy、calico、coredns — 始终检查
- **扩展组件**：仅当 Addon 在 `extraAddonComponents` 中定义且在 BKECluster.Spec.Addons 中启用时才检查
- **用户自定义 Addon**：不在 `extraAddonComponents` 中的自动跳过
- **coredns 特殊规则**：至少 1 个 Pod Running 即视为健康
### 四、健康检查后处理
```
handleClusterReadyPostCheck()
    │
    ├── 1. 记录集群健康指标
    │   └── metricrecord.ClusterHealthyCountRecord()
    │
    ├── 2. 判断是否允许 Tracker 追踪
    │   ├── ClusterAllowTrackerWithBKENodes()
    │   │   ├── ControlPlaneInitialized == True?
    │   │   └── IsNodeBootFlagSet?（所有节点引导完成）
    │   ├── 允许 → TargetClusterReadyCondition = True
    │   └── 不允许 → TargetClusterReadyCondition = False
    │
    └── 3. 清理 Tracker 注解
        └── ClusterStatus == Ready && 有 Tracker 失败注解
            └── 移除 ClusterTrackerHealthyCheckFailedAnnotationKey
```
### 五、集群版本状态更新
```go
func (e *EnsureCluster) updateClusterVersionStatus(bkeCluster *bkev1beta1.BKECluster) {
    if bkeCluster.Status.KubernetesVersion == "" {
        bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
    }
    if bkeCluster.Status.EtcdVersion == "" {
        bkeCluster.Status.EtcdVersion = bkeCluster.Spec.ClusterConfig.Cluster.EtcdVersion
    }
    if bkeCluster.Status.OpenFuyaoVersion == "" {
        bkeCluster.Status.OpenFuyaoVersion = bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
    }
    if bkeCluster.Status.ContainerdVersion == "" {
        bkeCluster.Status.ContainerdVersion = bkeCluster.Spec.ClusterConfig.Cluster.ContainerdVersion
    }
}
```
**仅在 Status 字段为空时从 Spec 同步**，避免覆盖已升级的版本信息。
### 六、重调谐策略
| 场景 | 重调谐间隔 | 原因 |
|------|-----------|------|
| 后置处理未完成 | 10s | 等待后置脚本完成 |
| 健康检查失败 | 10s | 快速恢复检测 |
| 健康检查成功 | 5min | 周期性巡检 |
| 特殊状态 | 无（返回错误） | 由状态变更触发重新调谐 |
### 七、状态流转
```
                    ┌──────────────┐
                    │  Deploying   │ ← 首次部署中
                    └──────┬───────┘
                           │ 部署完成 + 健康检查通过
                           ▼
                    ┌──────────────┐
                    │    Ready     │ ← 健康状态
                    └──────┬───────┘
                           │ 健康检查失败
                           ▼
                    ┌──────────────┐
                    │  Unhealthy   │ ← 不健康状态
                    └──────┬───────┘
                           │ 健康检查恢复
                           ▼
                    ┌──────────────┐
                    │    Ready     │
                    └──────────────┘

特殊状态（跳过健康检查）：
  ├── ClusterMasterScalingUp/Down
  ├── ClusterWorkerScalingUp/Down
  ├── ClusterInitializing
  ├── ClusterPaused
  └── ClusterUpgrading
```
### 八、与 ComponentVersion YAML 的映射
EnsureCluster 对应 **clusterHealth ComponentVersion**，其核心是 `healthCheck` 声明：
```yaml
spec:
  componentName: clusterHealth
  healthCheck:
    steps:
      # 阶段 1：节点健康
      - name: check-all-nodes-ready
        type: Kubectl
        kubectl:
          operation: Wait
          resource: nodes
          condition: "Ready"
          timeout: 300s
      # 阶段 2：核心组件
      - name: check-core-components
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "pods"
          namespace: "kube-system"
          condition: "Running"
          selector: "app in (calico-kube-controllers,calico-node,coredns)"
          timeout: 180s
      # 阶段 3：控制面静态 Pod
      - name: check-control-plane-pods
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "pods"
          namespace: "kube-system"
          condition: "Running"
          selector: "component in (etcd,kube-apiserver,kube-controller-manager,kube-scheduler)"
          timeout: 180s
      # 阶段 4：扩展组件（按 Addon 启用情况）
      - name: check-openfuyao-components
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "pods"
          namespace: "openfuyao-system"
          condition: "Running"
          timeout: 180s
```
**关键差异**：
- 现有实现的组件检查列表是**硬编码**在 Go 代码中的（`neededComponentChecks`、`extraAddonComponents`）
- YAML 声明式方案需要将这些检查项**参数化**，支持通过配置动态增减
- 现有实现连续执行 **3 次**健康检查才判定通过，YAML 方案需在 `healthCheck` 中体现重试逻辑
        
