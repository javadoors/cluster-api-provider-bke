# ensure_agent_switch.go 业务流程梳理。
## EnsureAgentSwitch 业务流程梳理
### 一、整体定位
[ensure_agent_switch.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_switch.go) 负责将 BKEAgent 的**监听集群**从管理集群切换到目标集群（BKECluster）。在集群创建初期，BKEAgent 监听管理集群接收命令；当集群部署完成后，需要将 Agent 切换为监听目标集群自身，使 Agent 能够独立于管理集群运行。
### 二、核心流程图
```
NeedExecute 判断
    │
    ├─ Annotation = "current" → 不执行（已在监听当前集群）
    ├─ SwitchBKEAgentCondition = True → 不执行（已切换完成）
    └─ Annotation = "bkecluster" 或无注解 → 执行
         │
         ▼
    Execute → reconcileAgentSwitch
         │
         ├─ 情况1：无 BKEAgentListener 注解
         │     → 设置 Annotation = "current"，同步状态
         │     → 跳过切换（Agent 已在监听当前集群）
         │
         ├─ 情况2：Annotation = "current"
         │     → 跳过切换
         │
         ├─ 情况3：Annotation = "bkecluster"
         │     → 执行切换流程
         │     ├─ 获取所有节点
         │     ├─ 创建 Switch 命令（SwitchCluster 插件）
         │     │     ├─ 命令：SwitchCluster
         │     │     ├─ 参数：kubeconfig=<ns>/<name>-kubeconfig, clusterName=<name>
         │     │     └─ 目标：所有节点
         │     ├─ 下发命令（不等待完成）
         │     └─ 标记 SwitchBKEAgentCondition = True
         │
         └─ 情况4：其他值
               → 不做任何操作
```
### 三、详细流程分析
#### 3.1 NeedExecute — 执行条件判断
```go
func (e *EnsureAgentSwitch) NeedExecute(old, new *BKECluster) bool
```
**判断逻辑**：

| 条件 | 结果 | 说明 |
|------|------|------|
| `BKEAgentListenerAnnotationKey = "current"` | ❌ 不执行 | Agent 已在监听当前集群，无需切换 |
| `SwitchBKEAgentCondition = True` | ❌ 不执行 | 切换已完成 |
| `BKEAgentListenerAnnotationKey = "bkecluster"` | ✅ 执行 | 需要切换到 BKECluster |
| 无注解 | ✅ 执行 | 需要初始化注解 |
#### 3.2 reconcileAgentSwitch — 核心协调逻辑
```go
func (e *EnsureAgentSwitch) reconcileAgentSwitch() error
```
根据 `BKEAgentListenerAnnotationKey` 注解的值执行不同逻辑：

**注解值定义**（[constants.go](file:///d:/code/github/cluster-api-provider-bke/common/constants.go#L22)）：

| 常量 | 值 | 含义 |
|------|-----|------|
| `BKEAgentListenerAnnotationKey` | `bke.bocloud.com/bkeagent-listener` | 注解键 |
| `BKEAgentListenerCurrent` | `current` | Agent 监听当前所在集群 |
| `BKEAgentListenerBkecluster` | `bkecluster` | Agent 需要切换到监听 BKECluster |

**四种情况**：

| 情况 | 注解值 | 处理 |
|------|--------|------|
| 无注解 | — | 设置注解为 `current`，同步状态，跳过切换 |
| `current` | Agent 已在监听当前集群 | 跳过切换 |
| `bkecluster` | 需要切换 | 执行切换流程 |
| 其他 | 未知值 | 不做任何操作 |
#### 3.3 切换流程（Annotation = "bkecluster"）
当注解值为 `bkecluster` 时，执行以下步骤：

**步骤 1：获取所有节点**
```go
nodes, err := e.Ctx.GetNodes()
```

**步骤 2：创建 Switch 命令**
```go
switchCommand := createSwitchCommand(ctx, c, bkeCluster, scheme, bkenode.Nodes(nodes))
```
命令详情（[switchcluster.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/switchcluster.go#L39)）：

| 参数 | 值 | 说明 |
|------|-----|------|
| 命令名 | `switch-cluster-<clusterName>-<timestamp>` | 带时间戳的唯一名称 |
| 命令类型 | `CommandBuiltIn` | 内置插件 |
| 命令内容 | `["SwitchCluster", "kubeconfig=<ns>/<name>-kubeconfig", "clusterName=<name>"]` | SwitchCluster 插件 |
| 目标节点 | 所有节点 | 全部节点都需要切换 |
| Unique | true | 不允许重复命令 |

**步骤 3：下发命令**
```go
switchCommand.New()
```
**注意**：这里只调用 `New()` 创建并下发命令，**不调用 `Wait()` 等待完成**。因为切换后 Agent 会重启，如果等待会导致超时。

**步骤 4：标记切换完成**
```go
condition.ConditionMark(bkeCluster, bkev1beta1.SwitchBKEAgentCondition, confv1beta1.ConditionTrue, ...)
```
### 四、SwitchCluster 插件执行流程（Agent 端）
当 Agent 收到 `SwitchCluster` 命令后，由 [SwitchClusterPlugin](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/switchcluster/switch.go) 执行：
```
SwitchClusterPlugin.Execute
    │
    ├─ 1. 解析命令参数
    │     ├─ kubeconfig（必填）：格式 ns/secret，指向目标集群的 kubeconfig Secret
    │     ├─ nodeName（可选）：节点名称，默认 os.Hostname
    │     └─ clusterName（可选）：集群名称
    │
    ├─ 2. 从管理集群读取 kubeconfig Secret
    │     └─ Get Secret <namespace>/<name>
    │
    ├─ 3. 写入文件到 Agent 工作目录
    │     ├─ <workspace>/config    ← kubeconfig 内容
    │     ├─ <workspace>/node      ← nodeName
    │     └─ <workspace>/cluster   ← clusterName
    │
    └─ 4. 30 秒后退出进程
          └─ os.Exit(1)
              └─ systemd Restart=on-failure 重启 Agent
                  └─ Agent 使用新的 kubeconfig 连接目标集群
```
**关键机制**：
1. **写入新 kubeconfig**：将目标集群的 kubeconfig 写入 `<workspace>/config`，覆盖原有的管理集群 kubeconfig
2. **写入节点/集群标识**：写入 `node` 和 `cluster` 文件，Agent 重启后使用这些信息注册到目标集群
3. **延迟退出**：使用 `time.AfterFunc(30s, os.Exit(1))` 延迟 30 秒退出
4. **systemd 重启**：Agent 的 systemd service 配置了 `Restart=on-failure` 和 `SuccessExitStatus=0`，退出码 1 触发重启
5. **重新连接**：Agent 重启后读取新的 kubeconfig，连接到目标集群
### 五、触发时机分析
**谁设置了 `BKEAgentListenerAnnotationKey = "bkecluster"`？**

在 [ensure_addon_deploy.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go) 的 `handleClusterAPI` 前置操作中：
```go
func (e *EnsureAddonDeploy) markBKEAgentSwitchPending() error {
    annotation.SetAnnotation(bkeCluster, common.BKEAgentListenerAnnotationKey, common.BKEAgentListenerBkecluster)
    // ...
}
```
即当部署 `cluster-api` Addon 时，会将注解设置为 `bkecluster`，表示 Agent 需要切换到监听 BKECluster。这确保了在 CAPI 基础设施部署到目标集群后，Agent 才切换到目标集群。
### 六、状态流转图
```
集群创建初期
    │
    ├─ BKEAgentListenerAnnotationKey 未设置（默认 "current"）
    │  Agent 监听管理集群
    │
    ▼
部署 cluster-api Addon（EnsureAddonDeploy）
    │
    ├─ markBKEAgentSwitchPending()
    │  设置 Annotation = "bkecluster"
    │
    ▼
EnsureAgentSwitch Phase
    │
    ├─ 检测到 Annotation = "bkecluster"
    ├─ 下发 SwitchCluster 命令到所有节点
    ├─ 标记 SwitchBKEAgentCondition = True
    │
    ▼
Agent 端执行 SwitchCluster
    │
    ├─ 写入目标集群 kubeconfig
    ├─ 30 秒后 os.Exit(1)
    ├─ systemd 重启 Agent
    │
    ▼
Agent 重启后
    │
    ├─ 使用新 kubeconfig 连接目标集群
    ├─ Agent 监听目标集群（BKECluster）
    │
    ▼
后续 Reconcile
    │
    ├─ NeedExecute 检测到 SwitchBKEAgentCondition = True
    └─ 不再执行切换
```
### 七、关键设计要点
#### 7.1 异步切换（不等待完成）
`reconcileAgentSwitch` 中只调用 `switchCommand.New()` 下发命令，**不调用 `Wait()` 等待完成**。这是因为：
- SwitchCluster 执行后 Agent 会退出并重启
- 如果等待，Controller 会因 Agent 断开而超时
- 切换是"即发即忘"的操作，通过 Condition 标记完成
#### 7.2 注解驱动的状态机
通过 `BKEAgentListenerAnnotationKey` 注解实现简单的状态机：

| 状态 | 注解值 | 含义 |
|------|--------|------|
| 初始 | 无 / `current` | Agent 监听管理集群 |
| 待切换 | `bkecluster` | 需要切换到目标集群 |
| 已切换 | `current` + Condition=True | 切换完成 |
#### 7.3 延迟退出确保命令响应
Agent 在写入文件后延迟 30 秒退出，确保：
- 命令执行结果能被上报
- 文件写入完成
- systemd 有足够时间捕获退出状态
#### 7.4 切换时序保证
切换只在 `cluster-api` Addon 部署后触发，确保目标集群已具备 CAPI 基础设施，Agent 切换后能正常工作。
### 八、与其他 Phase 的关系
| Phase | 与 Agent Switch 的关系 |
|-------|----------------------|
| **EnsureAddonDeploy** | 部署 `cluster-api` Addon 时设置 `Annotation = "bkecluster"`，触发切换 |
| **EnsureAgentSwitch** | 检测注解，下发 SwitchCluster 命令 |
| **EnsureBKEAgent** | Agent 初始安装时监听管理集群 |
| **EnsureNodesPostProcess** | 在 Agent 切换前执行，确保后置处理在管理集群上下文中完成 |

Agent 切换是集群从"被管理"到"自治"的关键转折点，切换后 Agent 直接与目标集群通信，不再依赖管理集群中转。
        
