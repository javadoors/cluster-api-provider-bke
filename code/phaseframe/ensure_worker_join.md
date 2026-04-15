# ensure_worker_join.go业务流程梳理
## EnsureWorkerJoin 业务流程梳理
### 一、整体定位
[ensure_worker_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go) 负责在**控制平面初始化完成后**，将 **Worker 节点**加入集群。它通过调整 CAPI 的 `MachineDeployment` 副本数来触发 CAPI 控制器创建新的 Machine/BKEMachine，进而驱动 Agent 在目标节点上执行 kubeadm join 流程。
### 二、核心流程图
```
NeedExecute 判断
    │
    ├─ 控制平面未初始化 → 不执行
    ├─ 无待加入 Worker 节点 → 不执行
    └─ 控制平面已初始化 + 有待加入 Worker → 执行
         │
         ▼
    Execute → reconcileWorkerJoin
         │
         ├─ 1. 检查控制平面是否已初始化
         │
         ├─ 2. getExceptJoinNodes（获取期望加入的节点）
         │     ├─ GetNeedJoinWorkerNodesWithBKENodes（筛选未 Boot/Init 的 Worker）
         │     ├─ 过滤 NeedSkip 节点（之前失败的节点）
         │     └─ 过滤未完成环境初始化的节点（NodeEnvFlag / NodeAgentReadyFlag）
         │
         ├─ 3. getJoinableNodesInfo（获取可加入节点信息）
         │     ├─ 检查 NodeToMachine 排除已关联 Machine 的节点
         │     └─ 标记已关联节点为 NodeBootFlag
         │
         ├─ 4. [博云特殊配置] DistributeKubeProxyKubeConfig
         │
         ├─ 5. scaleMachineDeployment（扩容 MachineDeployment）
         │     ├─ 计算目标副本数 = 当前副本数 + 待加入节点数
         │     ├─ 上限不超过 Worker 节点总数
         │     ├─ 更新 MD 副本数并 Resume
         │     ├─ defer: 失败时回滚副本数
         │     └─ waitWorkerJoin
         │           ├─ pollWorkerJoinStatus（轮询节点加入状态）
         │           │     ├─ 并发检查每个节点状态
         │           │     ├─ 成功：Machine.NodeRef != nil
         │           │     ├─ 失败：NodeFailedFlag / NodeBootStrapFailed / NodeInitFailed
         │           │     └─ 标记失败节点为 NeedSkip
         │           ├─ categorizeJoinedNodes（分类成功/失败节点）
         │           ├─ updateSuccessNodesStatus（更新成功节点状态）
         │           ├─ handleFailedNodes（处理失败节点）
         │           └─ determineDeploymentResult（决定部署结果）
         │
         └─ 完成
```
### 三、详细流程分析
#### 3.1 NeedExecute — 执行条件判断
```go
func (e *EnsureWorkerJoin) NeedExecute(old, new *BKECluster) bool
```
**判断逻辑**：
1. `DefaultNeedExecute`：基础条件检查
2. [fetchBKENodesIfCPInitialized](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_helpers.go#L24)：检查控制平面是否已初始化，若未初始化返回 `false`
3. [GetNeedJoinWorkerNodesWithBKENodes](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/util.go#L399)：筛选需要加入的 Worker 节点
   - 筛选没有 `NodeBootFlag` 且没有 `MasterInitFlag` 的节点
   - 只取 Role 包含 worker 的节点
   - 处理预约节点（排除 `AppointmentAddNodesAnnotationKey` 中的节点）

| 场景 | 结果 |
|------|------|
| 控制平面未初始化 | ❌ 不执行 |
| 无待加入 Worker 节点 | ❌ 不执行 |
| **控制平面已初始化 + 有待加入 Worker** | **✅ 执行** |
#### 3.2 getExceptJoinNodes — 获取期望加入的节点
```go
func (e *EnsureWorkerJoin) getExceptJoinNodes() bkenode.Nodes
```
这是 Worker Join 特有的**三层过滤**机制，比 Master Join 更严格：

| 过滤层 | 条件 | 说明 |
|--------|------|------|
| 第1层 | `GetNeedJoinWorkerNodesWithBKENodes` | 筛选未 Boot/Init 的 Worker 节点 |
| 第2层 | `GetNodeStateNeedSkip` | 排除之前标记为 NeedSkip 的失败节点 |
| 第3层 | `NodeEnvFlag` + `NodeAgentReadyFlag` | 排除环境未就绪或 Agent 未 Ready 的节点 |

**NeedSkip 机制**：这是 Worker Join 独有的容错设计。当某个 Worker 节点加入失败后，会被标记为 `NeedSkip=true`，后续 Reconcile 会自动跳过该节点，避免反复尝试失败的节点阻塞整个集群部署。
#### 3.3 getJoinableNodesInfo — 获取可加入节点信息
```go
func (e *EnsureWorkerJoin) getJoinableNodesInfo(exceptJoinNodes) ([]string, int, error)
```
与 Master Join 逻辑一致：
1. 遍历期望加入的节点
2. 调用 `NodeToMachine` 检查是否已关联 Machine
   - **已关联**：标记 `NodeBootFlag`，跳过（防重复创建）
   - **未关联**：加入 `nodesToJoin` 列表
3. 如果所有节点都已加入，同步 BKECluster 状态并返回
#### 3.4 handleBocloudClusterConfig — 博云特殊配置
与 Master Join 一致，对博云集群分发 `kube-proxy.kubeconfig`。
#### 3.5 scaleMachineDeployment — 扩容 MachineDeployment
```go
func (e *EnsureWorkerJoin) scaleMachineDeployment(params ScaleMachineDeploymentParams) error
```
**核心步骤**：

**步骤 1：计算目标副本数**
```
目标副本数 = 当前副本数 + 待加入节点数
上限 = BKECluster 的 Worker 节点总数
```
**步骤 2：更新 MachineDeployment 副本数并 Resume**
- 设置 `MachineDeployment.Spec.Replicas = &exceptReplicas`
- 调用 `ResumeClusterAPIObj`：移除 `PausedAnnotation`，让 CAPI 控制器恢复调谐

**步骤 3：失败回滚（defer）**
```go
defer func() {
    if scaleErr != nil {
        params.Scope.MachineDeployment.Spec.Replicas = currentReplicas
        phaseutil.ResumeClusterAPIObj(...)
    }
}()
```
#### 3.6 waitWorkerJoin — 等待节点加入（核心差异点）
```go
func (e *EnsureWorkerJoin) waitWorkerJoin() error
```
这是 Worker Join 与 Master Join **最大的差异**。Master Join 简单地等待所有节点加入或超时，而 Worker Join 实现了**精细化的部分成功处理**：

**步骤 1：pollWorkerJoinStatus — 并发轮询节点状态**
```go
func (e *EnsureWorkerJoin) pollWorkerJoinStatus(ctxTimeout) (*sync.Map, error)
```
- 轮询间隔：1 秒
- 超时：单节点超时时间（不乘以节点数，与 Master Join 不同）
- 使用 `sync.Map` 并发安全地记录成功/失败节点
- 每次轮询刷新 BKECluster 状态

**步骤 2：checkAllNodesStatus — 并发检查每个节点**
```go
func (e *EnsureWorkerJoin) checkAllNodesStatus(successJoinNode, failedJoinNode *sync.Map)
```
使用 `sync.WaitGroup` 并发检查所有节点状态，每个节点独立判断：

| 状态条件 | 处理 |
|----------|------|
| `NodeFailedFlag = true` | 标记 NeedSkip，加入失败列表 |
| `NodeBootStrapFailed` / `NodeInitFailed` | 标记 NeedSkip，加入失败列表 |
| `Machine.NodeRef != nil` | 加入成功列表 |
| 其他 | 继续等待 |

**步骤 3：categorizeJoinedNodes — 分类节点**

将节点分为 `successNodes` 和 `failedNodes` 两组。

**步骤 4：updateSuccessNodesStatus — 更新成功节点状态**
```go
func (e *EnsureWorkerJoin) updateSuccessNodesStatus(c, successNodes) error
```
- 刷新 BKECluster 状态
- 将成功节点状态设为 `NodeNotReady`（"Join worker nodes success"）
- 同步状态到 API Server

**步骤 5：handleFailedNodes — 处理失败节点**
```go
func (e *EnsureWorkerJoin) handleFailedNodes(c, successNodes, failedNodes)
```
- 输出失败节点摘要日志
- 标记失败节点为 `NeedSkip=true`（后续 Reconcile 自动跳过）
- 输出故障排查指引（查看 Agent 日志、删除 BKENode 重新添加）
- 同步状态

**步骤 6：determineDeploymentResult — 决定部署结果**
```go
func (e *EnsureWorkerJoin) determineDeploymentResult(successNodes, failedNodes, pollErr) error
```

| 场景 | 结果 | 说明 |
|------|------|------|
| 有成功节点 | `return nil` | **集群继续部署**，失败节点被跳过 |
| 全部失败 + 非超时错误 | `return pollErr` | 返回错误，触发回滚 |
| 全部失败 + 超时 | `return nil` | **不阻塞集群**，记录警告继续 |

**关键设计**：Worker 节点失败**不阻塞集群部署**。即使所有 Worker 都超时失败，集群控制平面仍然可用，用户可以后续修复并重新添加。
### 四、与 EnsureMasterJoin 的关键差异
| 维度 | EnsureMasterJoin | EnsureWorkerJoin |
|------|-----------------|------------------|
| **CAPI 对象** | KubeadmControlPlane | MachineDeployment |
| **节点过滤** | 仅筛选未 Boot/Init 的 Master | 三层过滤（未 Boot/Init + NeedSkip + 环境就绪） |
| **等待超时** | 节点数 × 单节点超时 | 单节点超时 |
| **并发检查** | 串行遍历 | 并发检查（sync.WaitGroup + sync.Map） |
| **失败处理** | 全部回滚 | **部分成功继续**，失败节点标记 NeedSkip |
| **失败影响** | 阻塞集群 | 不阻塞集群 |
| **节点状态检测** | 仅检查 Machine.NodeRef | 检查 NodeFailedFlag + NodeBootStrapFailed + NodeInitFailed + Machine.NodeRef |
| **NeedSkip 机制** | 无 | 有（失败节点自动跳过） |
### 五、关键设计要点
#### 5.1 NeedSkip 容错机制
Worker Join 独有的容错设计：
```
节点加入失败 → 标记 NeedSkip=true → 后续 Reconcile 自动跳过 → 集群继续部署
                                                    ↓
                              用户修复后删除 BKENode 重新添加
```
这确保了单个 Worker 节点的故障不会阻塞整个集群的部署流程。
#### 5.2 部分成功策略
与 Master Join 的"全有或全无"策略不同，Worker Join 采用**部分成功策略**：
- 只要有 1 个 Worker 成功加入，就认为部署可以继续
- 即使全部超时失败，也不返回错误（控制平面仍然可用）
- 失败节点被标记为 NeedSkip，用户可以后续修复
#### 5.3 三层节点过滤
Worker Join 比 Master Join 多了两层过滤：
1. **NeedSkip 过滤**：排除之前失败的节点
2. **环境就绪过滤**：确保节点环境已初始化（`NodeEnvFlag`）且 Agent 已就绪（`NodeAgentReadyFlag`）

这避免了将命令下发到未准备好的节点上。
#### 5.4 并发状态检查
使用 `sync.WaitGroup` + `sync.Map` 并发检查所有节点的加入状态，提高了大规模 Worker 节点加入时的检查效率。
        
