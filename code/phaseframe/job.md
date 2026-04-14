# 全面掌握 `pkg/job` 的设计
## `pkg/job` 的作用及设计思路
### 一、核心作用
`pkg/job` 是 **BKEAgent 端的命令执行引擎**，负责将 `Command` CRD 中声明的指令解析、路由并执行到具体操作。它是管理集群"声明式意图"与工作节点"命令式执行"之间的桥梁。
```
管理集群声明意图                Agent 端执行意图
┌──────────────┐            ┌──────────────────┐
│ Command CRD  │  ──Watch──►│   pkg/job        │
│ spec.commands│            │   命令执行引擎    │
│   [{Type,    │            │   解析→路由→执行  │
│     Command}]│            └──────────────────┘
└──────────────┘
```
### 二、分层架构
```
pkg/job/
├── job.go                          ← 顶层入口：Job 聚合 + Task 生命周期
├── builtin/                        ← BuiltIn 类型命令的执行层
│   ├── builtin.go                  ← 插件注册表 + 路由分发
│   ├── plugin/                     ← 插件框架（接口 + 参数解析 + 集群数据获取）
│   │   └── interface.go
│   ├── kubeadm/                    ← K8s 集群相关操作（最大子域）
│   │   ├── env/                    ← 环境初始化/检查
│   │   ├── certs/                  ← 证书管理
│   │   ├── kubelet/                ← Kubelet 配置
│   │   ├── kubeadm.go             ← Kubeadm 操作
│   │   ├── manifests/              ← 静态 Pod 清单
│   │   └── command.go             ← Kubeadm 命令拼接
│   ├── containerruntime/           ← 容器运行时
│   │   ├── containerd/             ← Containerd 安装配置
│   │   ├── docker/                 ← Docker 安装配置
│   │   └── cridocker/             ← cri-dockerd 安装
│   ├── reset/                      ← 节点重置/清理
│   ├── ha/                         ← HA 负载均衡（haproxy+keepalived）
│   ├── switchcluster/              ← 集群切换
│   ├── downloader/                 ← 文件下载
│   ├── collect/                    ← 信息采集
│   ├── backup/                     ← 备份
│   ├── ping/                       ← 连通性检测
│   ├── shutdown/                   ← 节点关机
│   ├── selfupdate/                 ← Agent 自更新
│   ├── preprocess/                 ← 前置处理脚本
│   ├── postprocess/                ← 后置处理脚本
│   └── scriptutil/                 ← 脚本工具（渲染、落盘）
├── k8s/                            ← Kubernetes 类型命令的执行层
│   └── k8s.go                      ← ConfigMap/Secret 读写执行
└── shell/                          ← Shell 类型命令的执行层
    └── shell.go                    ← /bin/sh -c 执行
```
### 三、核心设计思路
#### 1. 三类执行器 — 按命令类型分治
[job.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/job.go) 中 `Job` 聚合了三种执行器：
```go
type Job struct {
    BuiltIn builtin.BuiltIn   // 内置插件执行器
    K8s     k8s.K8s           // K8s 资源操作执行器
    Shell   shell.Shell       // Shell 命令执行器
    Task    map[string]*Task  // 运行中任务的生命周期管理
}
```
三种执行器对应 `CommandSpec.Commands[].Type` 的三种值：

| Type | 执行器 | 命令格式 | 典型场景 |
|------|--------|----------|----------|
| `BuiltIn` | `builtin.Task` | `[插件名, key=value, ...]` | 环境初始化、重置、HA部署 |
| `Shell` | `shell.Task` | `[cmd, arg1, arg2, ...]` | 自定义Shell命令 |
| `Kubernetes` | `k8s.Task` | `[type:ns/name:op:path]` | ConfigMap/Secret读写 |
#### 2. 插件注册表 — 开放封闭原则
[builtin.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/builtin.go) 的核心设计：
```go
var pluginRegistry = map[string]plugin.Plugin{}

func New(exec exec.Executor, k8sClient client.Client) BuiltIn {
    // 注册所有插件
    pluginRegistry[strings.ToLower(env.New(exec,nil).Name())] = env.New(exec,nil)
    pluginRegistry[strings.ToLower(reset.New().Name())] = reset.New()
    pluginRegistry[strings.ToLower(ha.New(exec).Name())] = ha.New(exec)
    // ... 共 18 个插件
}

func (t *Task) Execute(execCommands []string) ([]string, error) {
    // Command[0] 作为路由 key
    if v, ok := pluginRegistry[strings.ToLower(execCommands[0])]; ok {
        return v.Execute(execCommands)
    }
    return nil, errors.Errorf("Instruction not found")
}
```
**设计优势**：
- **对扩展开放**：新增功能只需实现 `Plugin` 接口并在 `New()` 中注册一行
- **对修改封闭**：路由逻辑不变，已有插件不受影响
- **大小写不敏感**：`strings.ToLower` 确保命令名容错
#### 3. Plugin 接口 — 统一契约
[interface.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/plugin/interface.go) 定义了插件三要素：
```go
type Plugin interface {
    Name() string                                    // 身份标识（路由key）
    Param() map[string]PluginParam                   // 参数契约（自描述）
    Execute(commands []string) ([]string, error)     // 执行入口
}
```
**`Param()` 自描述机制**是关键设计——每个插件声明自己需要什么参数、哪些必填、默认值是什么。`ParseCommands` 统一做校验和填充：
```go
func ParseCommands(plugin Plugin, commands []string) (map[string]string, error) {
    // 1. 解析 commands[1:] 为 key=value
    // 2. 与 plugin.Param() 比对
    //    - 有传入值 → 使用传入值
    //    - 无传入值 + Required → 报错
    //    - 无传入值 + 非Required → 使用 Default
}
```
#### 4. 集群数据获取 — 按需加载
插件通过 `bkeConfig=ns:name` 参数按需获取集群配置，而不是在初始化时全量注入：
```go
// 插件内部按需获取
if envParamMap["bkeConfig"] != "" {
    ep.bkeConfig = plugin.GetBkeConfig(envParamMap["bkeConfig"])
    ep.nodes = plugin.GetClusterData(envParamMap["bkeConfig"]).Nodes
    ep.currenNode = ep.nodes.CurrentNode()
}
```
`plugin` 包提供了统一的集群数据获取工具：

| 函数 | 作用 |
|------|------|
| `GetBkeConfig(ns:name)` | 获取 BKEConfig（集群配置） |
| `GetClusterData(ns:name)` | 获取 ClusterData（集群+节点列表） |
| `GetNodesData(ns:name)` | 获取节点列表 |
| `GetContainerdConfig(ns:name)` | 获取 Containerd 配置 |

这些函数通过 Agent 本地的 kubeconfig 连接管理集群的 API Server 获取数据。
#### 5. Task 生命周期管理
[job.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/job.go) 中的 `Task` 管理命令执行的生命周期：
```go
type Task struct {
    StopChan                chan struct{}        // 停止信号（支持暂停/取消）
    Phase                   v1beta1.CommandPhase // 当前阶段
    ResourceVersion         string               // 版本控制（防止旧版本覆盖新版本）
    Generation              int64                // 代次控制
    TTLSecondsAfterFinished int                  // 完成后自动清理
    HasAddTimer             bool                 // 是否已设置清理定时器
    Once                    *sync.Once           // 确保 StopChan 只关闭一次
}
```
**关键设计**：
- `StopChan`：支持命令暂停和取消，`SafeClose` 用 `sync.Once` 防止重复关闭
- `ResourceVersion + Generation`：版本控制，确保只执行最新版本的命令
- `TTLSecondsAfterFinished`：命令完成后自动清理，避免资源残留
#### 6. 插件可嵌套调用
插件之间可以互相调用，形成组合能力。例如 `K8sEnvInit` 的 `initRuntime` 内部调用了 `containerd`、`docker`、`cri-docker` 等插件：
```go
func (ep *EnvPlugin) initRuntime() error {
    // ...
    // 直接调用 containerd 插件
    cp := containerdPlugin.New(ep.exec)
    cp.Execute([]string{"Containerd", "url=...", "sandbox=...", ...})

    // 直接调用 docker 插件
    dp := dockerPlugin.New(ep.exec)
    dp.Execute([]string{"Docker", "runtime=...", "dataRoot=...", ...})

    // 直接调用 cri-docker 插件
    cdp := cridocker.New(ep.exec)
    cdp.Execute([]string{"CriDocker", "sandbox=...", "criDockerdUrl=...", ...})
}
```
这种设计让插件既能通过 `pluginRegistry` 被路由调用，也能被其他插件直接实例化调用。
#### 7. Reset 的 Phase 模式
[reset/cleanphases.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/cleanphases.go) 采用了清理阶段模式：
```go
func DefaultCleanPhases() CleanPhases {
    return CleanPhases{
        CleanKubeletPhase(),          // 清理 Kubelet
        CleanContainerdCfgPhase(),    // 清理 Containerd 配置
        CleanContainerPhase(),        // 清理容器
        CleanContainerRuntimePhase(), // 清理容器运行时
        CleanCertPhase(),             // 清理证书
        CleanManifestsPhase(),        // 清理静态 Pod
        CleanSourcePhase(),           // 清理软件源
        CleanExtraPhase(),            // 清理额外文件
        CleanGlobalCertPhase(),       // 清理全局证书
    }
}
```
每个 `CleanPhase` 有 `Name`（与 `scope` 参数对应）和 `CleanFunc`，通过 `scope` 参数选择性执行，实现了清理操作的灵活组合。
#### 8. Preprocess/Postprocess — 用户脚本扩展
[preprocess](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/preprocess/preprocess.go) 和 [postprocess](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/postprocess/postprocess.go) 提供了用户自定义脚本的扩展能力：
- 脚本存储在管理集群的 `ConfigMap` 中（`user-system` 命名空间）
- 支持三级配置优先级：**全局 > 批次 > 节点**（互斥不合并）
- 脚本支持参数模板渲染（`${NODE_IP}`, `${HTTP_REPO}` 等）
- 参数白名单校验防止注入攻击
- 脚本渲染后落盘再执行，确保可审计
### 四、设计思路总结
```
┌─────────────────────────────────────────────────────────────────┐
│                      pkg/job 设计思路                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. 分层分治：Job → 三类执行器 → 具体实现                       │
│     按 CommandType 横向切分，每类有独立的解析和执行逻辑         │
│                                                                 │
│  2. 插件注册表：pluginRegistry 实现路由与实现解耦               │
│     新增功能 = 实现 Plugin 接口 + 注册一行                      │
│                                                                 │
│  3. 自描述参数：Param() 声明参数契约                            │
│     ParseCommands 统一校验+填充，插件无需关心参数解析           │
│                                                                 │
│  4. 按需加载：bkeConfig 参数驱动集群数据获取                    │
│     插件按需从管理集群获取配置，而非初始化时全量注入            │
│                                                                 │
│  5. 可嵌套调用：插件间可直接实例化调用                          │
│     K8sEnvInit 内部调用 Containerd/Docker/CriDocker 插件        │
│                                                                 │
│  6. 生命周期管理：Task + StopChan + Generation                  │
│     支持暂停/取消/版本控制/自动清理                             │
│                                                                 │
│  7. Phase 模式：Reset 的 CleanPhases 按 scope 选择性执行        │
│     灵活组合清理操作                                            │
│                                                                 │
│  8. 用户扩展：Preprocess/Postprocess 支持自定义脚本             │
│     三级配置优先级 + 参数渲染 + 安全校验                        │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```
