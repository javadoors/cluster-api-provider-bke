
# EnsureBKEAgent 业务流程梳理
> 梳理D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_bke_agent.go的业务流程​
## 一、总体流程概览
```
Execute()
  │
  ├── 1. loadLocalKubeConfig()        — 加载管理集群 KubeConfig
  │
  ├── 2. getNeedPushNodes()           — 获取需要推送 Agent 的节点列表
  │
  ├── 3. pushAgent()                  — SSH 推送并启动 BKEAgent
  │     ├── prepareServiceFile()      — 准备 systemd service 文件
  │     ├── performAgentPush()        — 执行 SSH 推送
  │     │     └── sshPushAgent()
  │     │           ├── RegisterHosts()        — 注册 SSH 连接
  │     │           ├── RegisterHostsInfo()    — 获取目标机器架构
  │     │           ├── executePreCommand()    — 前置清理命令
  │     │           ├── executeStartCommand()  — 上传文件+启动服务
  │     │           └── PostCommand            — 后置权限恢复
  │     └── handlePushResults()       — 处理推送结果
  │
  └── 4. pingAgent()                  — 验证 Agent 可达性并收集节点信息
        ├── PingBKEAgent()            — 下发 Ping Command
        ├── updateNodeStatus()        — 更新节点状态标记
        ├── validateAndHandleNodesField() — 校验节点字段（hostname唯一性等）
        └── checkAllOrPushedAgentsFailed() — 检查是否全部失败
```
## 二、阶段入口判断：NeedExecute
```go
func (e *EnsureBKEAgent) NeedExecute(old, new *bkev1beta1.BKECluster) bool
```
**判断逻辑**：
1. 先调用 `BasePhase.DefaultNeedExecute()` 检查基础条件
2. 通过 `NodeFetcher` 获取集群关联的所有 `BKENode` CRD
3. 调用 `phaseutil.HasNodesNeedingPhase(bkeNodes, NodeAgentPushedFlag)` 检查是否存在**尚未标记 `NodeAgentPushedFlag`** 的节点
4. 如果存在需要推送的节点，设置状态为 `PhaseWaiting` 并返回 `true`
## 三、步骤 1：loadLocalKubeConfig — 加载管理集群 KubeConfig
**代码位置**：[ensure_bke_agent.go:119-167](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L119-L167)

**业务逻辑**：
```
是否配置了 cluster-api addon？
  │
  ├── 否（没有 cluster-api）
  │     ├── 尝试 GetLeastPrivilegeKubeConfig()  — 获取最小权限 kubeconfig
  │     │     ├── 成功 → 创建 RBAC 资源（ServiceAccount/ClusterRole/ClusterRoleBinding）
  │     │     └── 失败 → 回退到 GetLocalKubeConfig()（使用管理集群 admin kubeconfig）
  │     └── 最终使用最小权限或管理集群 kubeconfig
  │
  └── 是（有 cluster-api）
        └── 直接使用 GetLocalKubeConfig() — 管理集群 admin kubeconfig
```
**关键点**：
- KubeConfig 指向的是**管理集群**，不是目标集群
- 没有 cluster-api addon 时优先使用最小权限，减少安全风险
- 有 cluster-api addon 时直接用管理集群 admin 权限（后续 Agent 切换阶段会处理）
## 四、步骤 2：getNeedPushNodes — 获取需要推送的节点
**代码位置**：[ensure_bke_agent.go:170-195](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L170-L195)

**业务逻辑**：
```
1. 通过 NodeFetcher 获取集群关联的所有 BKENode CRD
2. 调用 GetNeedPushAgentNodesWithBKENodes() 过滤：
   - 排除已标记 NodeAgentPushedFlag 的节点
   - 排除预约节点（AppointmentNodes）
3. 为每个需要推送的节点设置状态：NodeInitializing + "Pushing bkeagent"
4. 同步状态到管理集群
5. 缓存到 e.needPushNodes
```
**过滤条件**（[util.go:245-253](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/util.go#L245-L253)）：
```go
// 节点未标记 NodeAgentPushedFlag → 需要推送
return !GetNodeStateFlag(bn, ip, bkev1beta1.NodeAgentPushedFlag)
```
## 五、步骤 3：pushAgent — SSH 推送并启动 BKEAgent
**代码位置**：[ensure_bke_agent.go:197-278](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L197-L278)
### 5.1 prepareServiceFile — 准备 systemd service 文件
**代码位置**：[ensure_bke_agent.go:281-312](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L281-L312)
```
1. 创建临时目录
2. 读取 /bkeagent.service.tmpl 模板文件
3. 替换模板中的参数：
   - --ntpserver=  → 替换为 BKECluster.Spec.ClusterConfig.Cluster.NTPServer
   - --health-port= → 替换为 BKECluster.Spec.ClusterConfig.Cluster.AgentHealthPort
4. 写入临时文件 servicePath
5. 返回 servicePath（defer 清理临时目录）
```
### 5.2 performAgentPush → sshPushAgent — 执行 SSH 推送
**代码位置**：[ensure_bke_agent.go:420-499](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L420-L499)

**完整 SSH 推送流程**：
```
sshPushAgent()
  │
  ├── 1. 创建 MultiCli（并发 SSH 客户端）
  │
  ├── 2. RegisterHosts(hosts)           — 建立 SSH 连接
  │     └── 失败的节点记录到 pushAgentErrs
  │
  ├── 3. RegisterHostsInfo()            — 获取目标机器系统架构（amd64/arm64）
  │     └── 无法识别架构的节点记录到 pushAgentErrs
  │
  ├── 4. executePreCommand()            — 前置清理命令
  │     │  并发执行以下命令：
  │     │  ├── chmod 777 /usr/local/bin/
  │     │  ├── chmod 777 /etc/systemd/system/
  │     │  ├── systemctl stop bkeagent      (忽略错误)
  │     │  ├── systemctl disable bkeagent   (忽略错误)
  │     │  ├── systemctl daemon-reload      (忽略错误)
  │     │  ├── rm -rf /usr/local/bin/bkeagent*
  │     │  ├── rm -f /etc/systemd/system/bkeagent.service
  │     │  └── rm -rf /etc/openFuyao/bkeagent
  │     └── 失败节点从可用列表移除
  │
  ├── 5. executeStartCommand()          — 上传文件+启动服务
  │     │
  │     ├── 5a. prepareFileUploadList() — 准备上传文件列表
  │     │     ├── bkeagent.service      → /etc/systemd/system/
  │     │     ├── trust-chain.crt       → /etc/openFuyao/certs/
  │     │     ├── GlobalCA证书+密钥     → /etc/openFuyao/certs/  (仅 cluster-api addon)
  │     │     └── CSR配置文件(17个)     → /etc/openFuyao/certs/cert_config/
  │     │
  │     ├── 5b. 执行启动命令：
  │     │     ├── mkdir -p -m 755 /etc/openFuyao/certs
  │     │     ├── mv -f /usr/local/bin/bkeagent_* /usr/local/bin/bkeagent
  │     │     ├── mkdir -p -m 777 /etc/openFuyao/bkeagent
  │     │     ├── chmod +x /usr/local/bin/bkeagent
  │     │     ├── echo -e <kubeconfig> > /etc/openFuyao/bkeagent/config
  │     │     ├── systemctl daemon-reload
  │     │     ├── systemctl enable bkeagent
  │     │     └── systemctl restart bkeagent
  │     │
  │     └── 5c. 过滤 stderr：
  │           ├── "Created symlink" → 忽略（正常）
  │           └── "Failed to execute operation: File exists" → 忽略（正常）
  │
  └── 6. PostCommand — 后置权限恢复
        ├── chmod 755 /usr/local/bin/
        └── chmod 755 /etc/systemd/system/
```
**上传文件清单**：

| 文件 | 目标路径 | 条件 |
|------|---------|------|
| `bkeagent.service` | `/etc/systemd/system/` | 始终 |
| `trust-chain.crt` | `/etc/openFuyao/certs/` | 文件存在时 |
| `GlobalCA cert + key` | `/etc/openFuyao/certs/` | 仅 cluster-api addon |
| 17个 CSR 配置文件 | `/etc/openFuyao/certs/cert_config/` | 文件存在时 |

CSR 配置文件列表：
```
cluster-ca-policy.json, cluster-ca-csr.json, sign-policy.json,
api-server-csr.json, api-server-etcd-client-csr.json,
front-proxy-client-csr.json, api-server-kubelet-client-csr.json,
front-proxy-ca-csr.json, etcd-ca-csr.json, etcd-server-csr.json,
etcd-healthcheck-client-csr.json, etcd-peer-csr.json,
admin-kubeconfig-csr.json, kubelet-kubeconfig-csr.json,
controller-manager-csr.json, scheduler-csr.json, kube-proxy-csr.json
```
### 5.3 handlePushResults — 处理推送结果
**代码位置**：[ensure_bke_agent.go:315-358](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L315-L358)
```
handlePushResults()
  │
  ├── 全部失败？
  │     └── 是 → 同步状态 + 返回错误 "Failed to push agent to nodes"
  │
  ├── 部分成功：
  │     ├── 成功节点 → 标记 NodeAgentPushedFlag（避免重复推送）
  │     ├── 同步状态到管理集群
  │     └── Master 节点失败？
  │           ├── 是 → 返回错误 "Push agent to master node failed"
  │           └── 否 → 记录日志，继续（Worker 失败可容忍）
  │
  └── 全部成功 → 返回 nil
```
**关键容错策略**：
- **Master 节点失败**：直接报错，终止流程
- **Worker 节点失败**：仅记录日志，继续后续流程（标记为 NeedSkip）
## 六、步骤 4：pingAgent — 验证 Agent 可达性
**代码位置**：[ensure_bke_agent.go:549-597](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L549-L597)
### 6.1 PingBKEAgent — 下发 Ping Command
**代码位置**：[agent.go:40-82](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/agent.go#L40-L82)
```
1. 获取所有已标记 NodeAgentPushedFlag 的节点（即推送成功的节点）
2. 计算超时时间：节点数 × 5秒/节点
3. 创建 command.Ping 对象：
   - Command 类型：BuiltIn
   - Command 内容：["Ping"]
   - BackoffDelay：3秒（重试间隔）
   - RemoveAfterWait：true（执行完自动删除）
4. 下发 Ping Command 到管理集群
5. 等待所有节点响应
6. 从 Command Status 的 StdOut 中提取节点主机名信息
7. 更新未设置 hostname 的 BKENode 的 Spec.Hostname
```
**Ping Command 的工作机制**：
- 在管理集群创建 `Command` CRD，NodeSelector 包含目标节点 IP
- 目标节点上的 BKEAgent Watch 到该 Command，执行内置 `Ping` 命令
- Ping 命令返回节点的主机名和 IP 信息（格式：`hostname/ip`）
- Controller 从 Command.Status 的 StdOut 中解析主机名
### 6.2 updateNodeStatus — 更新节点状态
**代码位置**：[ensure_bke_agent.go:599-628](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L599-L628)
```
失败节点：
  ├── 设置状态：NodeInitFailed + "Failed ping bkeagent"
  ├── 取消标记：NodeAgentPushedFlag（下次重新推送）
  └── 设置 NeedSkip：true（跳过后续阶段）

成功节点：
  ├── 设置状态消息："BKEAgent is ready"
  ├── 标记：NodeAgentPushedFlag
  └── 标记：NodeAgentReadyFlag
```
### 6.3 validateAndHandleNodesField — 校验节点字段
**代码位置**：[ensure_bke_agent.go:630-649](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L630-L649)
```
1. 获取所有节点信息
2. 根据 BKECluster 类型选择校验规则：
   ├── BKECluster → ValidateNodesFields()（标准校验）
   └── BocloudCluster → ValidateNonStandardNodesFields()（非标准校验）
3. 校验失败 → handleValidationFailure()
   ├── 设置 BKEConfigCondition = False
   ├── hostname 不唯一？
   │     ├── 设置 HostNameNotUniqueReason 条件
   │     ├── 取消所有 needPushNodes 的 AgentPushed/AgentReady 标记
   │     └── 设置节点状态为 NodeInitFailed
   └── 同步状态并返回错误
```
### 6.4 checkAllOrPushedAgentsFailed — 最终检查
**代码位置**：[ensure_bke_agent.go:681-705](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L681-L705)
```
1. 所有节点 ping 都失败 → 返回错误
2. 本次需要推送的节点全部 ping 失败 → 返回错误
3. 部分成功 → 返回 nil（容忍部分 Worker 失败）
```
## 七、节点状态变迁图
```
初始状态
  │
  ▼
[NeedExecute 检测到未标记 NodeAgentPushedFlag 的节点]
  │
  ▼
NodeInitializing + "Pushing bkeagent"     ← getNeedPushNodes()
  │
  ▼
┌─────────────── SSH 推送 ───────────────┐
  │                                       │
  │ 推送成功                               │ 推送失败
  ▼                                       ▼
NodeAgentPushedFlag ✓              NodeInitFailed + NeedSkip ✓
  │                                （下次 NeedExecute 时跳过）
  ▼
┌─────────────── Ping 验证 ──────────────┐
  │                                       │
  │ Ping 成功                              │ Ping 失败
  ▼                                       ▼
NodeAgentReadyFlag ✓               NodeInitFailed
"BKEAgent is ready"                NodeAgentPushedFlag ✗（取消标记）
                                   NeedSkip ✓
  │
  ▼
[进入下一阶段 EnsureNodesEnv]
```
## 八、容错与重试机制总结
| 场景 | 处理策略 |
|------|---------|
| SSH 连接失败 | 节点标记为 `NodeInitFailed` + `NeedSkip`，从可用列表移除 |
| 架构识别失败 | 节点标记为 `NodeInitFailed`，从可用列表移除 |
| 前置命令失败 | 节点从可用列表移除，不参与后续推送 |
| Agent 启动失败 | 节点标记为 `NodeInitFailed` + `NeedSkip` |
| Master 推送失败 | **直接报错终止**，整个阶段返回 error |
| Worker 推送失败 | **容忍**，记录日志继续后续流程 |
| Ping 全部失败 | 返回错误，触发 Reconcile 重试 |
| Ping 部分失败（Worker） | 容忍，标记 `NeedSkip`，继续 |
| Hostname 不唯一 | 取消所有推送节点的标记，返回错误 |
| systemctl enable 输出 "Created symlink" | 忽略，视为正常 |
| 下次 Reconcile | `NeedSkip` 的节点被 `GetNeedPushAgentNodesWithBKENodes` 过滤掉，不再重复推送 |
