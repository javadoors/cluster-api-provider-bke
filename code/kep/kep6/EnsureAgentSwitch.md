# `/etc/openFuyao/bkeagent/cluster` 文件的作用

## `/etc/openFuyao/bkeagent/cluster` 文件的作用

根据代码分析，这个文件的作用是：

### 1. **存储集群名称**
文件内容就是集群的名称（clusterName），例如 `my-cluster`。

### 2. **由 SwitchClusterPlugin 写入**
在 `pkg/job/builtin/switchcluster/switch.go:120` 中：
```go
err = ioutil.WriteFile(cluster, []byte(runtimeParam["clusterName"]), RwRR)
```
当 bkeagent 需要切换到另一个集群时，会写入新的集群名称。

### 3. **已被废弃**
虽然曾经有 `utils.ClusterName()` 函数读取这个文件（`utils/utils.go:84-97`），但该函数已被标记为 `Deprecated`：
```go
// ClusterName
// Deprecated
func ClusterName() (string, error) {
    clusterFilePath := filepath.Join(Workspace, "cluster")
    // ...
}
```

### 4. **当前状态**
- **写入**：`SwitchClusterPlugin` 仍然会写入这个文件
- **读取**：**没有任何代码读取这个文件**
- **实际使用**：bkeagent 主程序只使用 `utils.HostName()`，不使用 `ClusterName()`

### 5. **结论**
`/etc/openFuyao/bkeagent/cluster` 是一个**遗留文件**，在当前的 bkeagent 实现中已经不再使用。它可能是早期版本用于标识 bkeagent 应该监听哪个集群，但现在的实现中 bkeagent 通过 kubeconfig 文件（`/etc/openFuyao/bkeagent/config`）来确定监听哪个集群，不再需要这个文件。

**建议**：在 KEP-6 的 BKEAgentSwitch 组件设计中，可以考虑不再写入这个文件，或者保留它用于调试/审计目的。

# `host *bkessh.Host` 数据来源

## `host *bkessh.Host` 数据来源完整分析

### 1. 数据结构定义

**文件**: `pkg/remote/host.go:20-28`
```go
type Host struct {
    User               string
    Password           string
    Address            string
    Port               string
    SSHKey             interface{}
    Extra              map[string]string  // 包含 hostname 和 arch
    ExtraCustomCmdFunc func(host *Host) Command
}
```

### 2. 完整数据流

#### 阶段 1: BKENode CRD (Kubernetes API)

```
BKENode CRD
├── Spec.IP: "10.0.0.1"
├── Spec.Hostname: "node-01"  ← 初始可能为空
├── Spec.Username: "root"
├── Spec.Password: "encrypted_password"
└── Spec.Port: "22"
```

#### 阶段 2: Ping 命令获取 Hostname

**文件**: `pkg/job/builtin/ping/main.go:36-37`
```go
func (Ping) Execute(commands []string) ([]string, error) {
    return []string{"pong", utils.HostName()}, nil
}
```
- bkeagent 启动后执行 ping 命令
- `utils.HostName()` 从 `/etc/openFuyao/bkeagent/node` 读取 hostname
- 返回 `["pong", "node-01"]`

#### 阶段 3: 更新 BKENode CRD

**文件**: `pkg/phaseframe/phaseutil/agent.go:269-270`
```go
func updateNodeHostname(bkenodes []confv1beta1.BKENode, nodeIndex int, hostname string) {
    bkenodes[nodeIndex].Spec.Hostname = hostname
}
```
- `processCommandOutput` 处理 ping 命令输出
- 如果 `Spec.Hostname` 为空，则从 ping 输出中提取 hostname
- 更新 BKENode CRD

#### 阶段 4: 转换为 Nodes

**文件**: `common/cluster/node/node.go:56-65`
```go
func ConvertBKENodeListToNodes(bkeNodeList *v1beta1.BKENodeList) Nodes {
    nodes := make(Nodes, 0, len(bkeNodeList.Items))
    for _, bkeNode := range bkeNodeList.Items {
        nodes = append(nodes, bkeNode.ToNode())  // 保留 Hostname 字段
    }
    return nodes
}
```

#### 阶段 5: 转换为 bkessh.Host

**文件**: `pkg/phaseframe/phaseutil/ssh.go:27-44`
```go
func NodeToRemoteHost(nodes bkenode.Nodes) []bkessh.Host {
    var hosts []bkessh.Host
    for _, node := range nodes.Decrypt() {
        host := bkessh.Host{
            User:     node.Username,
            Address:  node.IP,
            Port:     node.Port,
            Password: node.Password,
            SSHKey:   nil,
            Extra: map[string]string{
                "hostname": node.Hostname,  // ← 来自 BKENode.Spec.Hostname
                "arch":     "unknown",       // ← 初始值
            },
        }
        hosts = append(hosts, host)
    }
    return hosts
}
```

#### 阶段 6: 获取架构信息

**文件**: `pkg/remote/multicli.go:187-229`
```go
func (c *MultiCli) RegisterHostsInfo() map[string]error {
    checkCommand := Command{
        Cmds: Commands{
            CheckArchCommand,  // "echo $(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
        },
    }
    
    stdErrs, stdOuts := c.Run(checkCommand)
    
    for _, remotecli := range c.remotes {
        host := remotecli.host
        if v, ok := stdout[host.Address]; ok {
            if v[0].Command == CheckArchCommand && len(v) > 0 {
                host.Extra["arch"] = v[0].Out  // ← 更新为实际架构
            }
        }
    }
}
```
- 通过 SSH 执行 `uname -m` 命令
- 更新 `host.Extra["arch"]` 为 "amd64" 或 "arm64"

### 3. 使用场景

#### 3.1 写入 `/etc/openFuyao/bkeagent/node`

**文件**: `pkg/phaseframe/phaseutil/ssh.go:68-80`
```go
func HostCustomCmdFunc(host *bkessh.Host) bkessh.Command {
    c := []string{
        fmt.Sprintf("echo %s > /etc/openFuyao/bkeagent/node", host.Extra["hostname"]),
    }
    expectAgent := fmt.Sprintf("/bkeagent_linux_%s", host.Extra["arch"])
    f := []bkessh.File{
        {Src: expectAgent, Dst: "/usr/local/bin/"},
    }
    return bkessh.Command{
        Cmds:   c,
        FileUp: f,
    }
}
```

#### 3.2 调用位置

**文件**: `pkg/phaseframe/phases/ensure_bke_agent.go:523`
```go
multiCli.RegisterHostsCustomCmdFunc(phaseutil.HostCustomCmdFunc)
```

### 4. 数据流总结图

```
BKENode CRD (K8s API)
    ↓ (1) GetBKENodesFromClient
BKENodeList
    ↓ (2) ConvertBKENodeListToNodes
Nodes []Node
    ↓ (3) NodeToRemoteHost
[]bkessh.Host
    ├─ Extra["hostname"] = node.Hostname
    └─ Extra["arch"] = "unknown"
    ↓ (4) RegisterHostsInfo
[]bkessh.Host
    ├─ Extra["hostname"] = "node-01"
    └─ Extra["arch"] = "amd64"  ← SSH 执行 uname -m
    ↓ (5) HostCustomCmdFunc
SSH Command
    ├─ echo "node-01" > /etc/openFuyao/bkeagent/node
    └─ upload /bkeagent_linux_amd64 → /usr/local/bin/
```

### 5. Hostname 来源的两种情况

#### 情况 1: 首次安装（Hostname 为空）

1. BKENode CRD 创建时 `Spec.Hostname` 为空
2. bkeagent 推送到节点后启动
3. bkeagent 执行 ping 命令，返回 `utils.HostName()`
4. `utils.HostName()` 从 `/etc/openFuyao/bkeagent/node` 读取
5. 如果文件不存在，使用系统 hostname 并创建文件
6. 控制器收到 ping 响应，更新 BKENode CRD 的 `Spec.Hostname`

#### 情况 2: 升级（Hostname 已存在）

1. BKENode CRD 已有 `Spec.Hostname`
2. 直接使用现有的 hostname
3. 不覆盖已设置的 hostname（见 `agent.go:244` 注释）

### 6. 关键代码位置

| 步骤 | 文件 | 行号 |
|------|------|------|
| Host 结构定义 | `pkg/remote/host.go` | 20-28 |
| NodeToRemoteHost | `pkg/phaseframe/phaseutil/ssh.go` | 27-44 |
| HostCustomCmdFunc | `pkg/phaseframe/phaseutil/ssh.go` | 68-80 |
| RegisterHostsInfo | `pkg/remote/multicli.go` | 187-229 |
| 更新 BKENode Hostname | `pkg/phaseframe/phaseutil/agent.go` | 269-270 |
| Ping 命令实现 | `pkg/job/builtin/ping/main.go` | 36-37 |
| utils.HostName | `utils/utils.go` | 99-131 |

# bkeagent重构为Binary组件后，EnsureAgentSwitch的内容也需要考虑，请一并给出设计方案

基于对 `EnsureAgentSwitch` 的完整分析，我来给出 bkeagent 重构为 Binary 组件后的设计方案。

## 一、EnsureAgentSwitch 功能分析

### 1.1 核心功能

EnsureAgentSwitch 负责切换 bkeagent 的监听目标集群：

| 组件 | 作用 |
|------|------|
| **注解** | `bke.bocloud.com/bkeagent-listener` 标记目标（`current` / `bkecluster`） |
| **Condition** | `SwitchBKEAgentCondition` 标记切换完成 |
| **Command** | `SwitchCluster` 命令通知 bkeagent 切换 |

### 1.2 触发场景

```
EnsureAddonDeploy 部署 cluster-api addon
    ↓
markBKEAgentSwitchPending() 设置注解 "bkecluster"
    ↓
EnsureAgentSwitch 检测到注解
    ↓
创建 SwitchCluster Command
    ↓
bkeagent 执行切换：
    - 从 Secret 获取新 kubeconfig
    - 写入 /etc/openFuyao/bkeagent/config
    - 写入 /etc/openFuyao/bkeagent/node
    - 写入 /etc/openFuyao/bkeagent/cluster
    - 30 秒后退出，systemd 重启
```

### 1.3 当前实现的关键代码

**控制器端** (`ensure_agent_switch.go:72-103`)：
```go
func (e *EnsureAgentSwitch) reconcileAgentSwitch() error {
    listener, ok := annotation.HasAnnotation(bkeCluster, common.BKEAgentListenerAnnotationKey)
    switch {
    case !ok:
        // 跳过，已监听当前集群
    case listener == common.BKEAgentListenerBkecluster:
        // 创建 Switch Command
        switchCommand := createSwitchCommand(ctx, c, bkeCluster, scheme, nodes)
        switchCommand.New()
        // 标记完成
        condition.ConditionMark(bkeCluster, SwitchBKEAgentCondition, ConditionTrue, ...)
    }
}
```
**bkeagent 端** (`switchcluster/switch.go:83-131`)：
```go
func (s *SwitchClusterPlugin) Execute(commands []string) ([]string, error) {
    // 1. 从 Secret 获取 kubeconfig
    config := &corev1.Secret{}
    s.K8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, config)
    
    // 2. 写入配置文件
    ioutil.WriteFile(kubeconfig, value, RwRR)    // /etc/openFuyao/bkeagent/config
    ioutil.WriteFile(node, []byte(nodeName), RwRR)    // /etc/openFuyao/bkeagent/node
    ioutil.WriteFile(cluster, []byte(clusterName), RwRR)  // /etc/openFuyao/bkeagent/cluster
    
    // 3. 30 秒后退出，systemd 重启
    time.AfterFunc(30*time.Second, func() { os.Exit(1) })
}
```

## 二、重构设计方案

### 2.1 方案选择

| 方案 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| **A. 保留 Command 机制** | AgentSwitch 仍使用 Command CRD | 改动最小 | 架构不一致 |
| **B. 完全 SSH 化** | 将切换逻辑写入 installScript | 架构统一 | 需要处理 cluster-api 依赖 |
| **C. 独立组件** | 创建 `bkeagent-switch` Binary 组件 | 职责分离 | 组件数量增加 |

**推荐方案：B（完全 SSH 化）+ 条件安装**

### 2.2 设计要点

#### 2.2.1 ComponentVersion 扩展

在 bkeagent 的 `BinarySpec` 中增加 `switchConfig` 字段：
```yaml
# bke-manifests/bkeagent/v2.6.0/component.yaml
spec:
  name: bkeagent
  type: binary
  version: v2.6.0
  
  binary:
    # 新增：监听切换配置
    switchConfig:
      # 是否启用监听切换
      enabled: false
      # 目标集群 kubeconfig Secret 引用
      targetKubeconfigSecret:
        name: "{{clusterName}}-kubeconfig"
        namespace: "{{clusterNamespace}}"
      # 目标集群名称
      targetClusterName: "{{clusterName}}"
    
    # 现有字段...
    artifacts:
      - name: bkeagent
        url: "{{imageRegistry}}/bkeagent/{{version}}/bkeagent_linux_{{arch}}"
    
    installScript: |
      #!/bin/bash
      # ... 现有安装逻辑 ...
      
      # 新增：监听切换逻辑
      {{if .switchConfig.enabled}}
      # 从 Secret 获取 kubeconfig（通过 ConfigRenderer 预先读取）
      echo -e "{{.switchConfig.kubeconfigContent}}" > /etc/openFuyao/bkeagent/config
      echo "{{.nodeHostname}}" > /etc/openFuyao/bkeagent/node
      echo "{{.switchConfig.targetClusterName}}" > /etc/openFuyao/bkeagent/cluster
      
      # 重启 bkeagent
      systemctl restart bkeagent
      {{end}}
```

#### 2.2.2 ConfigRenderer 扩展

新增 `KubeconfigSecretRef` 渲染模式：
```go
// pkg/binaryinstaller/config_renderer.go

type SwitchConfigSpec struct {
    Enabled bool `json:"enabled"`
    TargetKubeconfigSecret *SecretRefSpec `json:"targetKubeconfigSecret,omitempty"`
    TargetClusterName string `json:"targetClusterName,omitempty"`
}

func (r *ConfigRenderer) renderSwitchConfig(ctx context.Context, switchConfig *SwitchConfigSpec, tmplCtx manifest.TemplateContext) (map[string]string, error) {
    if !switchConfig.Enabled {
        return nil, nil
    }
    
    // 从 Secret 获取 kubeconfig
    secret := &corev1.Secret{}
    if err := r.client.Get(ctx, types.NamespacedName{
        Namespace: switchConfig.TargetKubeconfigSecret.Namespace,
        Name:      switchConfig.TargetKubeconfigSecret.Name,
    }, secret); err != nil {
        return nil, fmt.Errorf("failed to get target kubeconfig secret: %w", err)
    }
    
    return map[string]string{
        "kubeconfigContent": string(secret.Data["value"]),
        "targetClusterName": switchConfig.TargetClusterName,
    }, nil
}
```

#### 2.2.3 触发时机控制

**方案 1：通过注解控制（推荐）**

保留现有注解机制，在 DAG 调度时检查：
```go
// pkg/dagexec/binary_component_executor.go

func (e *BinaryComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
    // ... 现有逻辑 ...
    
    // 检查是否需要启用监听切换
    if shouldEnableAgentSwitch(execCtx.Cluster) {
        cv.Spec.Binary.SwitchConfig.Enabled = true
        cv.Spec.Binary.SwitchConfig.TargetKubeconfigSecret = &SecretRefSpec{
            Name:      fmt.Sprintf("%s-kubeconfig", execCtx.Cluster.Name),
            Namespace: execCtx.Cluster.Namespace,
        }
        cv.Spec.Binary.SwitchConfig.TargetClusterName = execCtx.Cluster.Name
    }
    
    // ... 执行安装 ...
}

func shouldEnableAgentSwitch(cluster *bkev1beta1.BKECluster) bool {
    listener, ok := annotation.HasAnnotation(cluster, common.BKEAgentListenerAnnotationKey)
    return ok && listener == common.BKEAgentListenerBkecluster
}
```

**方案 2：通过 DAG 依赖控制**

在 ReleaseImage 中定义独立的 `bkeagent-switch` 组件：
```yaml
# releaseimage-v2.6.0.yaml
spec:
  install:
    components:
      - name: bkeagent
        version: v2.6.0
      - name: cluster-api
        version: v1.5.0
      - name: bkeagent-switch  # 新增
        version: v2.6.0
        dependencies:
          - name: cluster-api
```

### 2.4 兼容性设计

#### 2.4.1 Feature Gate OFF（旧路径）

```
EnsureAgentSwitch Phase 继续工作
    ↓
创建 SwitchCluster Command
    ↓
bkeagent 通过 Command 机制切换
```

#### 2.4.2 Feature Gate ON（新路径）

```
DAG 调度 bkeagent Binary 组件
    ↓
检查注解，设置 switchConfig.enabled = true
    ↓
BinaryInstaller.Install()
    ↓
installScript 执行切换逻辑
    ↓
标记 SwitchBKEAgentCondition = True
```

#### 2.4.3 状态记录

```go
// 在 NodeComponentStatuses 中记录切换状态
NodeComponentStatuses["bkeagent"][nodeIP] = NodeComponentStatus{
    Version: "v2.6.0",
    Phase:   "Installed",
    // 新增字段
    ListenerTarget: "bkecluster",  // 或 "current"
    LastSwitchTime: &metav1.Time{Time: time.Now()},
}
```

## 三、文档更新计划

| 章节 | 更新内容 |
|------|---------|
| **3.2 Binary 类型字段定义** | 新增 `SwitchConfigSpec` 类型定义 |
| **4.3 BinaryInstaller 接口** | `InstallOptions` 增加 `SwitchConfig` 字段 |
| **4.5 ConfigRenderer** | 新增 `renderSwitchConfig` 方法 |
| **9.4.3.1 BinaryComponentExecutor** | 增加监听切换检测和配置注入逻辑 |
| **12.4.2 bkeagent ComponentVersion** | 增加 `switchConfig` 配置段 |
| **17.1.1 安装样例** | 增加 cluster-api 场景的 bkeagent 配置 |

## 四、关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 切换机制 | SSH 脚本（而非 Command） | 架构统一，Binary 组件完全通过 SSH 执行 |
| 触发控制 | 注解 + DAG 注入 | 保留现有注解语义，DAG 层注入配置 |
| 配置位置 | `BinarySpec.SwitchConfig` | 与 Binary 组件生命周期绑定 |
| kubeconfig 获取 | ConfigRenderer 预读取 | 复用现有 Secret 渲染机制 |
| 状态记录 | `NodeComponentStatuses` 扩展 | 统一状态模型 |
