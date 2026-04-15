# EnsureMasterJoin 业务流程
## EnsureMasterJoin 业务流程梳理
### 一、整体定位
[ensure_master_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go) 负责在**首个 Master 节点初始化完成后**，将**其余 Master 节点**加入集群。它通过调整 CAPI 的 `KubeadmControlPlane` 副本数来触发 CAPI 机器控制器创建新的 Machine/BKEMachine，进而驱动 Agent 在目标节点上执行 kubeadm join 流程。
### 二、核心流程图
```
NeedExecute 判断
    │
    ├─ 首次创建集群（masterInited=false, nodes=1）→ 不执行（由 EnsureMasterInit 处理）
    ├─ 集群已初始化且无待加入节点（masterInited=true, nodes=0）→ 不执行
    ├─ Master 未初始化且无待加入节点（masterInited=false, nodes=0）→ 不执行
    └─ 集群已初始化且有待加入 Master 节点 → 执行
         │
         ▼
    Execute
         │
         ▼
    reconcileMasterJoin
         │
         ├─ 1. checkPreconditions（前置条件检查）
         │     ├─ Agent 是否 Ready
         │     └─ ControlPlane 是否已初始化
         │
         ├─ 2. getJoinableNodes（获取可加入节点）
         │     ├─ 获取需要加入的 Master 节点列表
         │     ├─ 过滤已关联 Machine 的节点（标记为已 Boot）
         │     └─ 返回真正需要加入的节点
         │
         ├─ 3. [博云特殊配置] DistributeKubeProxyKubeConfig
         │
         ├─ 4. scaleAndJoinMasterNodes（扩缩容并等待加入）
         │     ├─ 获取 KubeadmControlPlane 对象
         │     ├─ 计算目标副本数 = 当前副本数 + 待加入节点数
         │     ├─ 上限不超过 BKECluster 的 Master 节点总数
         │     ├─ 更新 KCP 副本数并 Resume（移除 Paused 注解）
         │     ├─ defer: 失败时回滚副本数
         │     └─ waitMasterJoin
         │           ├─ 超时 = 节点数 × 单节点超时时间
         │           └─ 轮询检查 Machine.Status.NodeRef 是否不为空
         │
         └─ 5. 刷新 BKECluster 状态
```
### 三、详细流程分析
#### 3.1 NeedExecute — 执行条件判断
```go
func (e *EnsureMasterJoin) NeedExecute(old, new *BKECluster) bool
```
**判断逻辑**：

| 场景 | masterInited | 待加入 Master 节点数 | 结果 | 原因 |
|------|-------------|---------------------|------|------|
| 首次创建集群 | false | 1 | ❌ 不执行 | 首个 Master 由 EnsureMasterInit 处理 |
| 集群已初始化，无新节点 | true | 0 | ❌ 不执行 | 无需加入 |
| Master 未初始化，无节点 | false | 0 | ❌ 不执行 | 集群尚未就绪 |
| **集群已初始化，有新 Master** | **true** | **≥1** | **✅ 执行** | 正常扩容场景 |

**节点筛选逻辑**（[GetNeedJoinMasterNodesWithBKENodes](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/util.go#L367)）：
1. **GetNeedJoinNodesWithBKENodes**：筛选没有 `NodeBootFlag` 且没有 `MasterInitFlag` 的节点（即尚未引导和初始化的节点）
2. **.Master()**：只取 Role 包含 master 的节点
3. **预约节点处理**：如果存在 `AppointmentAddNodesAnnotationKey` 注解，通过 [ComputeFinalAddNodes](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/util.go#L451) 从待加入列表中排除预约节点（预约节点会在后续轮次中加入，避免一次性加入过多节点）
#### 3.2 checkPreconditions — 前置条件检查
```go
func (e *EnsureMasterJoin) checkPreconditions(params MasterJoinParams) error
```
检查两个前置条件：
1. **Agent 状态**：`BKECluster.Status.AgentStatus.Ready()` 必须为 true，确保 Agent 已就绪可以接收命令
2. **控制平面初始化状态**：`ControlPlaneInitializedCondition` 必须为 true，确保首个 Master 已初始化完成

如果控制平面未初始化，返回 `nil`（不报错但不继续执行），等待下一轮 Reconcile。
#### 3.3 getJoinableNodes — 获取可加入节点
```go
func (e *EnsureMasterJoin) getJoinableNodes(params MasterJoinParams) (int, []string, error)
```
**核心逻辑**：
1. 调用 `GetNeedJoinMasterNodesWithBKENodes` 获取需要加入的 Master 节点
2. 遍历每个节点，调用 [NodeToMachine](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/clusterapi.go#L182) 检查是否已关联 Machine：
   - **已关联 Machine**：说明该节点已被 CAPI 处理，标记 `NodeBootFlag` 并跳过
   - **未关联 Machine**：加入 `nodesToJoin` 列表，等待后续处理

**防重复机制**：通过检查 Machine 关联关系，避免为同一个节点重复创建 Machine。
#### 3.4 博云特殊配置 — DistributeKubeProxyKubeConfig
```go
if clusterutil.IsBocloudCluster(bkeCluster) {
    phaseutil.DistributeKubeProxyKubeConfig(ctx, c, bkeCluster, e.nodesToJoin, log)
}
```
对于博云集群，在 Master 节点加入前，通过 Agent 命令分发 `kube-proxy.kubeconfig` 到目标节点。这是一个特殊适配逻辑，使用 K8s 类型的命令将 Secret 挂载到节点上。
#### 3.5 scaleAndJoinMasterNodes — 扩容 KCP 并等待加入
```go
func (e *EnsureMasterJoin) scaleAndJoinMasterNodes(params MasterJoinScaleParams) error
```
**这是核心步骤**，通过 CAPI 的声明式机制实现 Master 节点加入：

**步骤 1：获取 KubeadmControlPlane 对象**
```
scope.KubeadmControlPlane  ←  CAPI 关联对象
```
**步骤 2：计算目标副本数**
```
目标副本数 = 当前副本数 + 待加入节点数
上限 = BKECluster 的 Master 节点总数
```
**步骤 3：更新 KCP 副本数并 Resume**
- 设置 `KubeadmControlPlane.Spec.Replicas = &exceptReplicas`
- 调用 `ResumeClusterAPIObj`：移除 `PausedAnnotation`，让 CAPI 控制器恢复对 KCP 的调谐

**步骤 4：失败回滚（defer）**
```go
defer func() {
    if err != nil {
        // 回滚副本数到原始值
        scope.KubeadmControlPlane.Spec.Replicas = currentReplicas
        phaseutil.ResumeClusterAPIObj(...)
    }
}()
```
如果加入过程中发生错误，将 KCP 副本数恢复到扩容前的值，防止 CAPI 继续创建多余的 Machine。
#### 3.6 waitMasterJoin — 等待节点加入
```go
func (e *EnsureMasterJoin) waitMasterJoin(nodesCount int) error
```
**等待策略**：

| 参数 | 值 | 说明 |
|------|-----|------|
| 轮询间隔 | 1 秒 | `wait.PollImmediateUntil(1*time.Second, ...)` |
| 总超时 | 节点数 × 单节点超时 | `time.Duration(nodesCount) * timeOut` |
| 成功条件 | 所有节点的 `Machine.Status.NodeRef != nil` | 表示节点已成功加入集群 |
| 日志间隔 | 每 10 次轮询输出一次 | `pollCount%LogOutputInterval == 0` |

**轮询逻辑**（[waitForNodesJoin](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go#L300)）：
```
for each node in nodesToJoin:
    if already in successJoinNode → skip
    NodeToMachine(node) → machine
    if machine.Status.NodeRef != nil → 标记为成功

if all nodes joined → return true (done)
else → return false (continue polling)
```
**超时处理**：如果超时，返回 `"Wait master join failed"` 错误，触发 defer 中的副本数回滚。
### 四、关键设计要点
#### 4.1 声明式扩容 vs 命令式加入
BKE 不直接在节点上执行 `kubeadm join`，而是通过**调整 KubeadmControlPlane 的 Replicas** 让 CAPI 控制器自动完成 Machine 创建和引导。这是一种声明式的设计：
```
BKE Controller → 修改 KCP.Replicas → CAPI KCP Controller → 创建 Machine
    → BKEMachine Controller → 下发 Command → Agent 执行 kubeadm join
```
#### 4.2 预约节点机制（AppointmentAddNodes）
通过 `AppointmentAddNodesAnnotationKey` 注解，可以控制节点**分批加入**：
- `ComputeFinalAddNodes` 从待加入列表中**排除**预约节点
- 预约节点会在后续轮次中被处理
- 这避免了大量节点同时加入导致 etcd 集群不稳定
#### 4.3 防重复创建
`getJoinableNodes` 中通过 `NodeToMachine` 检查是否已关联 Machine：
- 已关联 → 标记 `NodeBootFlag`，跳过
- 未关联 → 加入待加入列表

这防止了 Reconcile 重入时重复创建 Machine。
#### 4.4 失败回滚
`scaleAndJoinMasterNodes` 使用 defer 机制，在加入失败时将 KCP 副本数恢复到原始值，确保系统状态一致。
### 五、与 EnsureMasterInit 的对比
| 维度 | EnsureMasterInit | EnsureMasterJoin |
|------|-----------------|------------------|
| 触发条件 | 集群未初始化 + 有1个 Master 节点 | 集群已初始化 + 有额外的 Master 节点 |
| 操作方式 | 下发 init Command 到首个 Master | 扩容 KCP Replicas |
| 等待方式 | 轮询 Command 完成状态 | 轮询 Machine.NodeRef |
| 失败处理 | 重试 init 或标记失败 | 回滚 KCP Replicas |
| CAPI 交互 | 创建 KCP（Replicas=1） | 扩容 KCP（增加 Replicas） |
