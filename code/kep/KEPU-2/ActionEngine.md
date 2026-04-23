# 六、ActionEngine 设计思路及执行流程
```
type ActionStep struct {
    Name        string            `json:"name"`
    Type        ActionType        `json:"type"`

    // ===== Script 类型 =====
    // 内联脚本内容（支持模板变量），与 ScriptSource 互斥
    Script      string            `json:"script,omitempty"`
    // 脚本来源（Remote/Local），与 Script 互斥
    ScriptSource *SourceSpec      `json:"scriptSource,omitempty"`

    // ===== Manifest 类型 =====
    // 内联 Kubernetes YAML 清单（支持模板变量），与 ManifestSource 互斥
    Manifest    string            `json:"manifest,omitempty"`
    // 清单来源（Remote/Local），与 Manifest 互斥
    ManifestSource *SourceSpec    `json:"manifestSource,omitempty"`

    // ===== Chart 类型 =====
    Chart       *ChartAction      `json:"chart,omitempty"`

    // ===== Kubectl 类型 =====
    Kubectl     *KubectlAction    `json:"kubectl,omitempty"`

    // 条件判断：仅当条件满足时执行
    Condition   string            `json:"condition,omitempty"`

    // 失败处理策略
    OnFailure   FailurePolicy     `json:"onFailure,omitempty"`

    // 重试次数
    Retries     int               `json:"retries,omitempty"`

    // 节点选择（覆盖 ComponentVersion 级别的 nodeSelector）
    NodeSelector *NodeSelector    `json:"nodeSelector,omitempty"`
}

// SourceSpec 定义内容来源，支持三种方式：Inline、Remote、Local
// 与 ActionStep 中的内联字段（Script/Manifest）互斥：
//   - Script != "" || ScriptSource != nil（二选一）
//   - Manifest != "" || ManifestSource != nil（二选一）
type SourceSpec struct {
    // 来源类型：Inline / Remote / Local
    Type     SourceType `json:"type"`

    // 远程源 URL（Type=Remote 时必填）
    // 支持 HTTP/HTTPS，运行时由 ActionEngine 下载到节点临时目录
    URL      string     `json:"url,omitempty"`

    // 本地文件路径（Type=Local 时必填）
    // 路径相对于 ReleaseImage 包根目录，或节点绝对路径
    Path     string     `json:"path,omitempty"`

    // 内容校验和（可选，Type=Remote 时推荐填写）
    // 格式："sha256:<hex>" 或 "md5:<hex>"
    Checksum string     `json:"checksum,omitempty"`

    // 内联内容（Type=Inline 时必填，支持模板变量）
    Content  string     `json:"content,omitempty"`
}

type SourceType string

const (
    // Inline：内容直接嵌入 YAML，适用于短脚本和清单
    SourceInline  SourceType = "Inline"
    // Remote：从 HTTP/HTTPS URL 下载，适用于大型脚本和外部托管的清单
    SourceRemote  SourceType = "Remote"
    // Local：从 ReleaseImage 包或节点本地文件系统读取
    SourceLocal   SourceType = "Local"
)
              
oldStr: type ActionStep struct {
    Name        string            `json:"name"`
    Type        ActionType        `json:"type"`

    // Script 类型：shell 脚本内容（支持模板变量）
    Script      string            `json:"script,omitempty"`

    // Manifest 类型：Kubernetes YAML 清单（支持模板变量）
    Manifest    string            `json:"manifest,omitempty"`

    // Chart 类型：Helm Chart 配置
    Chart       *ChartAction      `json:"chart,omitempty"`

    // Kubectl 类型：kubectl 操作
    Kubectl     *KubectlAction    `json:"kubectl,omitempty"`

    // 条件判断：仅当条件满足时执行
    Condition   string            `json:"condition,omitempty"`

    // 失败处理策略
    OnFailure   FailurePolicy     `json:"onFailure,omitempty"`

    // 重试次数
    Retries     int               `json:"retries,omitempty"`

    // 节点选择（覆盖 ComponentVersion 级别的 nodeSelector）
    NodeSelector *NodeSelector    `json:"nodeSelector,omitempty"`
}
```
**第二步：增强 ChartAction，支持 Chart 来源的 Inline/Remote/Local**
```
type ChartAction struct {
    // 方式一：传统 Helm 仓库（Type 未指定或 Type=HTTPRepo）
    RepoURL     string            `json:"repoURL,omitempty"`
    ChartName   string            `json:"chartName,omitempty"`
    Version     string            `json:"version,omitempty"`

    // 方式二：OCI 仓库（ChartSource.Type=OCI）
    // 通过 ChartSource.OCIRef 指定

    // 方式三：本地 Chart 包（ChartSource.Type=Local）
    // 通过 ChartSource.Path 指定

    // Chart 来源（与 RepoURL/ChartName 互斥，优先使用）
    ChartSource *ChartSourceSpec  `json:"chartSource,omitempty"`

    ReleaseName string           `json:"releaseName"`
    Namespace   string           `json:"namespace"`
    Values      string           `json:"values,omitempty"`
    ValuesFrom  []ValuesFromSource `json:"valuesFrom,omitempty"`
}

// ChartSourceSpec 定义 Helm Chart 的来源方式
type ChartSourceSpec struct {
    Type ChartSourceType `json:"type"`

    // HTTPRepo：传统 Helm Chart 仓库
    HTTPRepo *HTTPRepoSource `json:"httpRepo,omitempty"`

    // OCI：OCI 兼容仓库
    OCI *OCISource `json:"oci,omitempty"`

    // Local：本地 Chart 包路径
    Local *LocalChartSource `json:"local,omitempty"`
}

type ChartSourceType string

const (
    ChartSourceHTTPRepo ChartSourceType = "HTTPRepo"
    ChartSourceOCI      ChartSourceType = "OCI"
    ChartSourceLocal    ChartSourceType = "Local"
)

type HTTPRepoSource struct {
    RepoURL   string `json:"repoURL"`
    ChartName string `json:"chartName"`
    Version   string `json:"version,omitempty"`
    Username  string `json:"username,omitempty"`
    Password  string `json:"password,omitempty"`
}

type OCISource struct {
    // OCI 引用，格式：registry.example.com/namespace/chart:version
    Ref      string `json:"ref"`
    Username string `json:"username,omitempty"`
    Password string `json:"password,omitempty"`
    CACert   string `json:"caCert,omitempty"`
}

type LocalChartSource struct {
    // 相对于 ReleaseImage 包根目录的路径，或节点绝对路径
    Path     string `json:"path"`
    Checksum string `json:"checksum,omitempty"`
}

// ValuesFromSource 定义 Helm Values 的外部来源
type ValuesFromSource struct {
    Type     ValuesFromType `json:"type"`
    URL      string         `json:"url,omitempty"`
    Path     string         `json:"path,omitempty"`
    Checksum string         `json:"checksum,omitempty"`
}

type ValuesFromType string

const (
    ValuesFromURL  ValuesFromType = "URL"
    ValuesFromFile ValuesFromType = "File"
)
              
oldStr: type ChartAction struct {
    RepoURL    string            `json:"repoURL"`
    ChartName  string            `json:"chartName"`
    Version    string            `json:"version"`
    ReleaseName string          `json:"releaseName"`
    Namespace  string            `json:"namespace"`
    Values     string            `json:"values,omitempty"`
}
```
**第三步：在文档末尾追加 ActionEngine 设计思路及执行流程、Source 使用示例、NodeConfig 组件列表组装机制**

**关键设计点**：etcd 逐节点升级的本质不是"脚本复杂度"问题，而是"编排语义"问题——需要 ActionEngine 理解"对每个节点执行完整步骤序列并等待确认"这一语义。通过增强 Rolling 策略的 `waitForCompletion` 字段，可以在不引入新 ActionType 的前提下完整支持 etcd 逐节点升级。
## 6.1 设计思路
### 6.1.1 核心定位
ActionEngine 是声明式集群管理的**唯一执行器**，其核心职责是：**解释 ComponentVersion YAML 中的 Action 定义，并按策略在目标节点上执行**。
```
┌─────────────────────┐     ┌──────────────────────┐     ┌──────────────────────┐
│  ComponentVersion   │     │    ActionEngine      │     │   Target Nodes       │
│  (YAML 声明)        │────▶│                      │────▶│                      │
│  installAction      │     │  1. 模板渲染         │     │  Agent / kubelet     │
│  upgradeAction      │     │  2. 来源解析         │     │  执行脚本/应用清单   │
│  uninstallAction    │     │  3. 条件求值         │     │  安装 Chart          │
│  healthCheck        │     │  4. 策略调度         │     │  kubectl 操作        │
└─────────────────────┘     │  5. 步骤执行         │     └──────────────────────┘
                            │  6. 结果收集         │
                            └──────────────────────┘
```
### 6.1.2 设计原则
| 原则 | 说明 |
|------|------|
| **YAML 即全部** | 所有组件行为由 YAML 声明，引擎不包含任何组件特定逻辑 |
| **单一职责** | ActionEngine 只负责"解释执行"，不负责"编排调度"（编排由 ComponentVersion Controller 负责） |
| **幂等执行** | 同一 Action 多次执行结果一致，支持安全重试 |
| **可观测** | 每个步骤的执行状态、输出、耗时均记录到 ComponentVersion Status |
| **来源无关** | 内容来源（Inline/Remote/Local）对执行逻辑透明，解析后统一为可执行内容 |
### 6.1.3 架构分层
```
┌─────────────────────────────────────────────────────────┐
│                  ComponentVersion Controller            │
│  (编排层：决定何时执行哪个 Action，管理 DAG 依赖)       │
└──────────────────────────┬──────────────────────────────┘
                           │ 调用
                           ▼
┌─────────────────────────────────────────────────────────┐
│                      ActionEngine                       │
│  ┌───────────┐  ┌───────────┐  ┌───────────────────┐    │
│  │ Renderer  │  │ Resolver  │  │   Executor        │    │
│  │ 模板渲染  │  │ 来源解析  │  │   步骤执行        │    │
│  └───────────┘  └───────────┘  └───────────────────┘    │
│  ┌───────────┐  ┌───────────┐  ┌───────────────────┐    │
│  │ Evaluator │  │ Scheduler │  │   Collector       │    │
│  │ 条件求值  │  │ 策略调度  │  │   结果收集        │    │
│  └───────────┘  └───────────┘  └───────────────────┘    │
└─────────────────────────────────────────────────────────┘
                           │ 下发
                           ▼
┌─────────────────────────────────────────────────────────┐
│                    Node Agent / API Server              │
│  Script → Agent Command                                 │
│  Manifest → kubelet static pod / kubectl apply          │
│  Chart → helm install                                   │
│  Kubectl → API Server                                   │
└─────────────────────────────────────────────────────────┘
```
### 6.1.4 五大子模块职责
| 子模块 | 职责 | 输入 | 输出 |
|--------|------|------|------|
| **Renderer** | 模板变量渲染 | ActionStep + TemplateContext | 渲染后的内容 |
| **Resolver** | 来源解析与内容获取 | SourceSpec | 可执行内容（字符串） |
| **Evaluator** | 条件表达式求值 | condition 字符串 + TemplateContext | bool |
| **Scheduler** | 按策略调度节点执行 | ActionStrategy + NodeConfig 列表 | 执行计划 |
| **Executor** | 实际执行步骤并收集结果 | 渲染后的 ActionStep + 目标节点 | 执行结果 |
## 6.2 完整执行流程
### 6.2.1 总体流程
```
ComponentVersion Controller
    │
    │ 检测到 spec 变化（版本变更/状态驱动）
    ▼
┌────────────────────────────────────────────────────────────────┐
│ Step 1: 确定待执行 Action                                      │
│   - 根据 ComponentVersion Status 决定执行 install/upgrade/...  │
│   - 读取对应的 ActionSpec                                      │
└──────────────────────────┬─────────────────────────────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────┐
│ Step 2: 匹配目标节点                                         │
│   - 根据 ComponentVersion.nodeSelector 筛选 NodeConfig 列表  │
│   - Scope=Cluster 时无需节点匹配，在控制面执行               │
└──────────────────────────┬───────────────────────────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────┐
│ Step 3: 执行 preCheck                                        │
│   - 渲染模板 → 解析来源 → 执行 → 等待结果                    │
│   - preCheck 失败则中止，更新 Status                         │
└──────────────────────────┬───────────────────────────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────┐
│ Step 4: 按策略执行 steps                                     │
│   ┌────────────────────────────────────────────────────┐     │
│   │ Scheduler 根据 ActionStrategy 生成执行计划：       │     │
│   │                                                    │     │
│   │ Parallel: 所有节点同时执行                         │     │
│   │ Serial:   逐节点执行，完成一个再执行下一个         │     │
│   │ Rolling:  按批次执行，每批 batchSize 个节点        │     │
│   │          waitForCompletion=true 时，每节点执行     │     │
│   │          steps+postCheck 后再处理下一个            │     │
│   └────────────────────────────────────────────────────┘     │
│                                                              │
│   对每个节点的每个 step：                                    │
│   ┌──────────────────────────────────────────────────┐       │
│   │ 4a. Renderer: 渲染模板变量                       │       │
│   │ 4b. Resolver: 解析来源（Inline/Remote/Local）    │       │
│   │ 4c. Evaluator: 求值 condition，跳过或执行        │       │
│   │ 4d. Executor: 下发执行                           │       │
│   │ 4e. Collector: 收集执行结果，存入步骤上下文      │       │
│   └──────────────────────────────────────────────────┘       │
└──────────────────────────┬───────────────────────────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────┐
│ Step 5: 执行 postCheck                                       │
│   - 渲染模板 → 解析来源 → 执行 → 等待结果（含重试）          │
│   - postCheck 失败根据 failurePolicy 决定是否中止            │
└──────────────────────────┬───────────────────────────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────┐
│ Step 6: 更新 ComponentVersion Status                         │
│   - 记录每个步骤的状态、输出、耗时                           │
│   - 记录成功/失败节点列表                                    │
│   - 更新组件整体状态（Installed / Upgraded / Failed）        │
└──────────────────────────────────────────────────────────────┘
```
### 6.2.2 单步骤执行流程（Step 4a-4e 详细）
```
ActionStep 输入
    │
    ▼
┌─────────────────────────────────────┐
│ Renderer: 模板渲染                  │
│                                     │
│ 输入：step.Script/step.Manifest     │
│       + TemplateContext             │
│ 处理：Go text/template 渲染         │
│ 输出：渲染后的内容字符串            │
│                                     │
│ 示例：                              │
│  "{{.ImageRepo}}/etcd:{{.Version}}" │
│   → "repo.openfuyao.cn/etcd:v3.5.12"│
└──────────────────┬──────────────────┘
                   ▼
┌─────────────────────────────────────┐
│ Resolver: 来源解析                  │
│                                     │
│ 根据 step.Type 决定解析路径：       │
│                                     │
│ Script 类型：                       │
│   step.Script != ""  → 直接使用     │
│   step.ScriptSource  → 按类型解析   │
│     Inline  → ScriptSource.Content  │
│     Remote  → HTTP GET下载到临时文件│
│     Local   → 读取节点文件          │
│                                     │
│ Manifest 类型：                     │
│   step.Manifest != ""  → 直接使用   │
│   step.ManifestSource → 按类型解析  │
│     Inline  → ManifestSource.Content│
│     Remote  → HTTP GET 下载         │
│     Local   → 读取节点文件          │
│                                     │
│ Chart 类型：                        │
│   step.Chart.ChartSource → 按类型   │
│     HTTPRepo → helm repo add + pull │
│     OCI      → helm pull oci://     │
│     Local    → 直接使用本地 tgz     │
│                                     │
│ 输出：可执行内容（脚本字符串/       │
│       清单字符串/Chart 包路径）     │
└──────────────────┬──────────────────┘
                   ▼
┌─────────────────────────────────────┐
│ Evaluator: 条件求值                 │
│                                     │
│ step.Condition != "" 时求值：       │
│   "{{.NodeRole}} == master"         │
│   "{{.Steps.check.stdout}} == SKIP" │
│                                     │
│ 求值结果为 false → 跳过此步骤       │
│ 求值结果为 true  → 继续执行         │
└──────────────────┬──────────────────┘
                   ▼
┌─────────────────────────────────────┐
│ Executor: 下发执行                  │
│                                     │
│ 根据 step.Type 选择执行通道：       │
│                                     │
│ Script:                             │
│   → 生成 Agent Command（脚本内容）  │
│   → 下发到目标节点 Agent            │
│   → 等待命令完成，收集stdout/stderr │
│                                     │
│ Manifest:                           │
│   → 写入 /etc/kubernetes/manifests/ │
│     (static pod)                    │
│   → 或通过 kubectl apply (workload) │
│   → 等待资源 Ready                  │
│                                     │
│ Chart:                              │
│   → helm install/upgrade            │
│   → 等待 Release 状态               │
│                                     │
│ Kubectl:                            │
│   → 直接调用 kube-apiserver         │
│   → 等待操作完成                    │
└──────────────────┬──────────────────┘
                   ▼
┌─────────────────────────────────────┐
│ Collector: 结果收集                 │
│                                     │
│ 记录：                              │
│   - 步骤状态：Succeeded/ Failed     │
│   - 标准输出：stdout(存入步骤上下文)│
│   - 错误输出：stderr                │
│   - 执行耗时                        │
│   - 重试次数                        │
│                                     │
│ 步骤上下文供后续步骤引用：          │
│   {{.Steps.<stepName>.stdout}}      │
│   {{.Steps.<stepName>.exitCode}}    │
│   {{.Steps.<stepName>.succeeded}}   │
└─────────────────────────────────────┘
```
### 6.2.3 Resolver 来源解析详细流程
```
SourceSpec 输入
    │
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Type = Inline                                                │
│                                                             │
│ 1. 读取 SourceSpec.Content                                 │
│ 2. 对 Content 执行模板渲染                                  │
│ 3. 返回渲染后的内容字符串                                   │
│                                                             │
│ 优点：无网络依赖，内容可见                                   │
│ 缺点：YAML 体积大，不适合长脚本                             │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Type = Remote                                               │
│                                                             │
│ 1. 对 SourceSpec.URL 执行模板渲染                           │
│ 2. HTTP GET 下载到节点临时目录 /tmp/action-<random>         │
│ 3. 校验 Checksum（如指定）：sha256sum / md5sum              │
│ 4. 校验失败 → 返回错误                                      │
│ 5. 读取文件内容（Script/Manifest）或返回文件路径（Chart）   │
│ 6. 对文件内容执行模板渲染                                   │
│                                                             │
│ 优点：YAML 精简，内容可独立更新                             │
│ 缺点：依赖网络，需校验完整性                                │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Type = Local                                                │
│                                                             │
│ 1. 判断 Path 是否为相对路径                                 │
│    - 相对路径 → 拼接 ReleaseImage 包根目录                  │
│    - 绝对路径 → 直接使用                                    │
│ 2. 读取文件内容                                             │
│ 3. 校验 Checksum（如指定）                                  │
│ 4. 对文件内容执行模板渲染                                   │
│                                                             │
│ 优点：离线可用，无网络依赖                                  │
│ 缺点：需预分发文件到节点                                    │
└─────────────────────────────────────────────────────────────┘
```
### 6.2.4 Scheduler 策略调度详细流程
```
ActionStrategy 输入 + NodeConfig 列表
    │
    ▼
┌─────────────────────────────────────────────────────────────┐
│ ExecutionMode = Parallel                                    │
│                                                             │
│ 所有节点同时执行全部 steps                                  │
│ 适用场景：节点环境初始化、Agent 安装                        │
│                                                             │
│ Node1: [step1 → step2 → step3] ──┐                          │
│ Node2: [step1 → step2 → step3] ──┤ 同时执行                 │
│ Node3: [step1 → step2 → step3] ──┘                          │
│                                                             │
│ postCheck: 所有节点完成后统一执行                           │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ ExecutionMode = Serial                                      │
│                                                             │
│ 逐节点执行，一个节点全部完成后再执行下一个                  │
│ 适用场景：有严格顺序要求的操作                              │
│                                                             │
│ Node1: [step1 → step2 → step3 → postCheck] ──┐              │
│                                              │ 完成后       │
│ Node2: [step1 → step2 → step3 → postCheck] ──┤ 再执行       │
│                                              │ 下一个       │
│ Node3: [step1 → step2 → step3 → postCheck] ──┘              │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ ExecutionMode = Rolling + waitForCompletion=false           │
│                                                             │
│ 按批次执行，每批内并行，批次间等待                          │
│ 适用场景：Worker 节点滚动升级                               │
│                                                             │
│ Batch1: [Node1, Node2] 同时执行 steps  ──┐                  │
│                                          │ 批次间隔         │
│ Batch2: [Node3, Node4] 同时执行 steps  ──┤                  │
│                                          │ 批次间隔         │
│ Batch3: [Node5]       同时执行 steps   ──┘                  │
│                                                             │
│ postCheck: 每批完成后执行                                   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ ExecutionMode = Rolling + waitForCompletion=true            │
│                                                             │
│ 逐节点执行，每个节点完成 steps + postCheck 后再处理下一个   │
│ 适用场景：etcd 逐节点升级、Master 节点升级                  │
│                                                             │
│ Node1: [step1 → step2 → step3 → postCheck ✓] ──┐            │
│                                                │ 确认成功   │
│ Node2: [step1 → step2 → step3 → postCheck ✓] ──┤ 再处理     │
│                                                │ 下一个     │
│ Node3: [step1 → step2 → step3 → postCheck ✓] ──┘            │
│                                                             │
│ 任一节点 postCheck 失败 → 根据 failurePolicy 决定：         │
│   FailFast: 立即停止，不再处理后续节点                      │
│   Continue: 跳过失败节点，继续处理下一个                    │
└─────────────────────────────────────────────────────────────┘
```
## 6.3 ActionEngine 核心接口设计
```go
// ActionEngine 通用执行引擎接口
type ActionEngine interface {
    // Execute 执行一个完整的 ActionSpec
    Execute(ctx context.Context, action *ActionSpec, targets []TargetNode) (*ActionResult, error)
}

// TargetNode 执行目标节点
type TargetNode struct {
    NodeConfig  *v1alpha1.NodeConfig
    TemplateCtx *TemplateContext
}

// ActionResult 执行结果
type ActionResult struct {
    Status      ActionStatus      `json:"status"`
    NodeResults []NodeActionResult `json:"nodeResults"`
    Duration    time.Duration     `json:"duration"`
}

type ActionStatus string

const (
    ActionSucceeded ActionStatus = "Succeeded"
    ActionFailed    ActionStatus = "Failed"
    ActionPartial   ActionStatus = "Partial"
)

type NodeActionResult struct {
    NodeName    string           `json:"nodeName"`
    Status      ActionStatus     `json:"status"`
    StepResults []StepResult     `json:"stepResults"`
    Error       string           `json:"error,omitempty"`
}

type StepResult struct {
    StepName  string `json:"stepName"`
    Status    string `json:"status"`
    Stdout    string `json:"stdout,omitempty"`
    Stderr    string `json:"stderr,omitempty"`
    ExitCode  int    `json:"exitCode"`
    Duration  string `json:"duration"`
    Skipped   bool   `json:"skipped,omitempty"`
    Retries   int    `json:"retries,omitempty"`
}

// SourceResolver 来源解析器接口
type SourceResolver interface {
    Resolve(ctx context.Context, spec *SourceSpec, tmplCtx *TemplateContext) (string, error)
}

// StepExecutor 步骤执行器接口
type StepExecutor interface {
    ExecuteScript(ctx context.Context, script string, node TargetNode) (*StepResult, error)
    ExecuteManifest(ctx context.Context, manifest string, node TargetNode) (*StepResult, error)
    ExecuteChart(ctx context.Context, chart *ChartAction, node TargetNode) (*StepResult, error)
    ExecuteKubectl(ctx context.Context, kubectl *KubectlAction, tmplCtx *TemplateContext) (*StepResult, error)
}
```
# 七、Source 类型使用示例
## 7.1 Script 来源示例
### 7.1.1 Inline（内联脚本）
最常用方式，脚本直接嵌入 YAML，适用于较短的脚本：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.2
spec:
  componentName: containerd
  version: v1.7.2
  installAction:
    steps:
      - name: install-containerd
        type: Script
        script: |
          #!/bin/bash
          set -e
          mkdir -p /etc/containerd
          tar -xzf /tmp/containerd-{{.Version}}-linux-amd64.tar.gz -C /usr/bin
          chmod +x /usr/bin/containerd /usr/bin/containerd-shim-runc-v2
          systemctl daemon-reload
          systemctl enable containerd
          systemctl restart containerd
```
### 7.1.2 Remote（远程脚本）
脚本托管在 HTTP 服务器，运行时下载执行。适用于：
- 脚本较长，不适合内联
- 脚本需要独立更新，不想修改 ComponentVersion YAML
- 多个组件共享同一脚本
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.2
spec:
  componentName: containerd
  version: v1.7.2
  installAction:
    steps:
      - name: install-containerd
        type: Script
        scriptSource:
          type: Remote
          url: "https://repo.openfuyao.cn/scripts/containerd/{{.Version}}/install.sh"
          checksum: "sha256:a3f2b7c8d9e1f0..."
```
ActionEngine 执行流程：
1. 渲染 URL 模板 → `https://repo.openfuyao.cn/scripts/containerd/v1.7.2/install.sh`
2. HTTP GET 下载到 `/tmp/action-<random>/install.sh`
3. 校验 `sha256:a3f2b7c8d9e1f0...`
4. 对下载内容执行模板渲染
5. 下发到节点 Agent 执行
### 7.1.3 Local（本地脚本）
脚本随 ReleaseImage 包分发，或预置在节点文件系统。适用于：
- 离线交付场景
- 脚本需要与二进制包一起打包分发
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.2
spec:
  componentName: containerd
  version: v1.7.2
  installAction:
    steps:
      - name: install-containerd
        type: Script
        scriptSource:
          type: Local
          path: "scripts/containerd/install.sh"
          checksum: "sha256:b4c3d8e9f2a1..."
```
ActionEngine 执行流程：
1. 判断 `scripts/containerd/install.sh` 为相对路径
2. 拼接 ReleaseImage 包根目录 → `/var/lib/bke/release/v1.28.0/scripts/containerd/install.sh`
3. 校验 `sha256:b4c3d8e9f2a1...`
4. 读取文件内容，执行模板渲染
5. 下发到节点 Agent 执行

**绝对路径示例**（节点预置脚本）：
```yaml
      - name: pre-check
        type: Script
        scriptSource:
          type: Local
          path: "/usr/local/bke/scripts/pre-check.sh"
```
## 7.2 Manifest 来源示例
### 7.2.1 Inline（内联清单）
最常用方式，Kubernetes 清单直接嵌入 YAML：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
spec:
  componentName: etcd
  version: v3.5.12
  installAction:
    steps:
      - name: generate-etcd-manifest
        type: Manifest
        manifest: |
          apiVersion: v1
          kind: Pod
          metadata:
            name: etcd-{{.NodeHostname}}
            namespace: kube-system
          spec:
            containers:
            - name: etcd
              image: {{.ImageRepo}}/etcd:{{.Version}}
              command:
              - /bin/sh
              - -c
              - |
                exec etcd \
                  --data-dir={{.EtcdDataDir}} \
                  --name={{.NodeHostname}}
```
### 7.2.2 Remote（远程清单）
清单托管在 HTTP 服务器，适用于：
- 清单较大（如完整的 Deployment + Service + ConfigMap）
- 清单由外部系统生成
- 多个集群复用同一清单
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.10.0
spec:
  componentName: addon
  version: v1.10.0
  installAction:
    steps:
      - name: deploy-coredns
        type: Manifest
        manifestSource:
          type: Remote
          url: "https://repo.openfuyao.cn/manifests/coredns/{{.Version}}/coredns.yaml"
          checksum: "sha256:e5f4a3b2c1d0..."
```
### 7.2.3 Local（本地清单）
清单随 ReleaseImage 包分发，适用于离线场景：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.10.0
spec:
  componentName: addon
  version: v1.10.0
  installAction:
    steps:
      - name: deploy-coredns
        type: Manifest
        manifestSource:
          type: Local
          path: "manifests/coredns/coredns.yaml"
          checksum: "sha256:f6a5b4c3d2e1..."
```
## 7.3 Chart 来源示例
### 7.3.1 HTTPRepo（传统 Helm 仓库）
最常用方式，从 Helm Chart 仓库拉取：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: csi-driver-v1.8.0
spec:
  componentName: addon
  version: v1.8.0
  installAction:
    steps:
      - name: install-csi-driver
        type: Chart
        chart:
          chartSource:
            type: HTTPRepo
            httpRepo:
              repoURL: "https://charts.openfuyao.cn/csi-driver"
              chartName: csi-driver
              version: 1.8.0
          releaseName: csi-driver
          namespace: kube-system
          values: |
            image:
              repository: {{.ImageRepo}}/csi-driver
              tag: {{.Version}}
            nodeDriverRegistrar:
              image:
                repository: {{.ImageRepo}}/csi-node-driver-registrar
```
### 7.3.2 OCI（OCI 兼容仓库）
从 OCI Registry 拉取 Chart，适用于：
- Chart 存储在 Harbor 等 OCI 兼容仓库
- 与容器镜像统一管理
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: csi-driver-v1.8.0
spec:
  componentName: addon
  version: v1.8.0
  installAction:
    steps:
      - name: install-csi-driver
        type: Chart
        chart:
          chartSource:
            type: OCI
            oci:
              ref: "registry.openfuyao.cn/charts/csi-driver:1.8.0"
              username: "{{.ChartPullUsername}}"
              password: "{{.ChartPullPassword}}"
          releaseName: csi-driver
          namespace: kube-system
          values: |
            image:
              repository: {{.ImageRepo}}/csi-driver
              tag: {{.Version}}
```
### 7.3.3 Local（本地 Chart 包）
Chart 随 ReleaseImage 包分发，适用于离线场景：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: csi-driver-v1.8.0
spec:
  componentName: addon
  version: v1.8.0
  installAction:
    steps:
      - name: install-csi-driver
        type: Chart
        chart:
          chartSource:
            type: Local
            local:
              path: "charts/csi-driver-1.8.0.tgz"
              checksum: "sha256:c7d6e5f4a3b2..."
          releaseName: csi-driver
          namespace: kube-system
          values: |
            image:
              repository: {{.ImageRepo}}/csi-driver
              tag: {{.Version}}
```
### 7.3.4 Helm Values 外部来源
当 Values 文件较大或需要独立管理时，可通过 `valuesFrom` 引用外部文件：
```yaml
      - name: install-csi-driver
        type: Chart
        chart:
          chartSource:
            type: HTTPRepo
            httpRepo:
              repoURL: "https://charts.openfuyao.cn/csi-driver"
              chartName: csi-driver
              version: 1.8.0
          releaseName: csi-driver
          namespace: kube-system
          valuesFrom:
            - type: URL
              url: "https://repo.openfuyao.cn/values/csi-driver/{{.Version}}/values.yaml"
              checksum: "sha256:d8e7f6a5b4c3..."
            - type: File
              path: "values/csi-driver-overrides.yaml"
```
`valuesFrom` 与 `values` 合并策略：`valuesFrom` 按顺序加载，`values` 最后加载，后者覆盖前者。
## 7.4 来源类型选择指南
| 来源类型 | 适用场景 | 网络要求 | YAML 体积 | 内容更新 |
|----------|----------|----------|-----------|----------|
| Inline | 短脚本（<100行）、简单清单 | 无 | 大 | 需修改 YAML |
| Remote | 长脚本、外部托管清单、共享脚本 | 需外网/内网 | 小 | 独立更新，不改 YAML |
| Local | 离线交付、随包分发、预置脚本 | 无 | 小 | 需更新包 |

**推荐策略**：
- **默认使用 Inline**：简单直接，内容可见
- **脚本超过 100 行时使用 Remote**：保持 YAML 可读性
- **离线交付场景使用 Local**：确保无网络依赖
- **混合使用**：短步骤 Inline + 长步骤 Remote/Local
# 八、NodeConfig 组件列表组装机制
## 8.1 核心问题
NodeConfig 代表一个节点的期望状态，其 `spec.components[]` 列表决定了该节点需要安装/升级哪些组件。**核心问题：这个列表是如何组装的？**
## 8.2 组装流程
```
┌──────────────────────────────────────────────────────────────┐
│ 1. ReleaseImage 定义全量组件列表                             │
│                                                              │
│   ReleaseImage.spec.componentVersions[]:                     │
│     - name: containerd, version: v1.7.2, ref: cv-containerd  │
│     - name: etcd,       version: v3.5.12, ref: cv-etcd       │
│     - name: kubernetes, version: v1.28.0, ref: cv-kubernetes │
│     - name: addon,      version: v1.0.0,   ref: cv-addon     │
│     - ...                                                    │
└──────────────────────────┬───────────────────────────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────┐
│ 2. ClusterVersion 引用 ReleaseImage                          │
│                                                              │
│   ClusterVersion.spec.releaseRef → ReleaseImage              │
│   ClusterVersion.spec.desiredVersion → 触发版本变更          │
└──────────────────────────┬───────────────────────────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────┐
│ 3. NodeConfig Controller 为每个节点组装组件列表              │
│                                                              │
│   对 ReleaseImage 中的每个 ComponentVersion：                │
│     a. 读取 ComponentVersion.spec.nodeSelector               │
│     b. 读取 ComponentVersion.spec.scope                      │
│     c. 与当前节点的角色/标签进行匹配                         │
│     d. 匹配成功 → 加入该 NodeConfig 的 components 列表       │
│     e. 匹配失败 → 跳过                                       │
└──────────────────────────────────────────────────────────────┘
```
## 8.3 匹配规则详解
### 8.3.1 Scope 过滤
| Scope | 含义 | NodeConfig 是否包含 |
|-------|------|---------------------|
| Cluster | 集群级组件，不绑定特定节点 | 所有 NodeConfig 均不包含，由 ClusterVersion Controller 直接处理 |
| Node | 节点级组件，绑定特定节点 | 需进一步通过 nodeSelector 匹配 |

**关键设计**：`Scope=Cluster` 的组件（如 clusterAPI、addon、openFuyao）**不出现在任何 NodeConfig 中**，而是由 ClusterVersion Controller 直接在控制面执行。
### 8.3.2 NodeSelector 匹配
```yaml
# ComponentVersion 定义
spec:
  componentName: etcd
  nodeSelector:
    roles: [master]       # 仅 master 节点

# NodeConfig 定义
spec:
  roles: [master]        # 该节点是 master
  # 匹配结果：✅ etcd 组件加入此 NodeConfig
```

```yaml
# ComponentVersion 定义
spec:
  componentName: containerd
  nodeSelector:
    roles: [master, worker]  # master 和 worker 节点

# NodeConfig 定义
spec:
  roles: [worker]           # 该节点是 worker
  # 匹配结果：✅ containerd 组件加入此 NodeConfig
```

```yaml
# ComponentVersion 定义
spec:
  componentName: etcd
  nodeSelector:
    roles: [master]

# NodeConfig 定义
spec:
  roles: [worker]           # 该节点是 worker
  # 匹配结果：❌ etcd 组件不加入此 NodeConfig
```
### 8.3.3 NodeSelector 扩展匹配
除了 `roles`，nodeSelector 还支持基于节点标签的匹配：
```go
type NodeSelector struct {
    // 节点角色匹配
    Roles []string `json:"roles,omitempty"`

    // 节点标签匹配（k8s label selector 语义）
    MatchLabels map[string]string `json:"matchLabels,omitempty"`

    // 节点标签表达式匹配
    MatchExpressions []metav1.LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}
```
示例：仅匹配带有特定 GPU 标签的 worker 节点：
```yaml
spec:
  componentName: nvidia-driver
  nodeSelector:
    matchLabels:
      gpu: nvidia
```
## 8.4 组装流程伪代码
```go
func (r *NodeConfigReconciler) assembleComponents(
    nodeConfig *v1alpha1.NodeConfig,
    releaseImage *v1alpha1.ReleaseImage,
) ([]ComponentRef, error) {
    var components []ComponentRef

    for _, cvRef := range releaseImage.Spec.ComponentVersions {
        cv := &v1alpha1.ComponentVersion{}
        if err := r.Get(ctx, cvRef.Ref, cv); err != nil {
            return nil, err
        }

        // 规则 1：Scope=Cluster 的组件不加入 NodeConfig
        if cv.Spec.Scope == ScopeCluster {
            continue
        }

        // 规则 2：nodeSelector 匹配
        if cv.Spec.NodeSelector != nil {
            if !matchNodeSelector(nodeConfig, cv.Spec.NodeSelector) {
                continue
            }
        }

        // 规则 3：依赖检查（可选，确保依赖组件已存在）
        if len(cv.Spec.Dependencies) > 0 {
            if !allDependenciesMet(cv.Spec.Dependencies, components) {
                // 依赖未满足，延迟加入（等依赖组件先加入）
                continue
            }
        }

        components = append(components, ComponentRef{
            ComponentName: cv.Spec.ComponentName,
            Version:       cv.Spec.Version,
            State:         ComponentStatePending,
        })
    }

    // 按 DAG 拓扑排序，确保依赖顺序
    components = topologicalSort(components, dependencyGraph)

    return components, nil
}

func matchNodeSelector(nc *v1alpha1.NodeConfig, selector *NodeSelector) bool {
    // Roles 匹配
    if len(selector.Roles) > 0 {
        matched := false
        for _, role := range selector.Roles {
            for _, nodeRole := range nc.Spec.Roles {
                if role == nodeRole {
                    matched = true
                    break
                }
            }
            if matched {
                break
            }
        }
        if !matched {
            return false
        }
    }

    // MatchLabels 匹配
    if len(selector.MatchLabels) > 0 {
        for key, value := range selector.MatchLabels {
            if nc.Labels[key] != value {
                return false
            }
        }
    }

    // MatchExpressions 匹配
    // ... 标准 label selector 语义

    return true
}
```
## 8.5 具体示例
### 8.5.1 Master 节点的 NodeConfig 组件列表
假设 ReleaseImage 包含 16 个 ComponentVersion，Master 节点的组件列表组装结果：
```
ReleaseImage.componentVersions (16个):
  ├── bkeAgent       [Node, roles: master/worker]  → ✅ Master 匹配
  ├── nodesEnv       [Node, roles: master/worker]  → ✅ Master 匹配
  ├── clusterAPI     [Cluster]                     → ❌ Cluster 作用域，不加入
  ├── certs          [Cluster]                     → ❌ Cluster 作用域，不加入
  ├── loadBalancer   [Node, roles: master]         → ✅ Master 匹配
  ├── kubernetes     [Node, roles: master/worker]  → ✅ Master 匹配
  ├── containerd     [Node, roles: master/worker]  → ✅ Master 匹配
  ├── etcd           [Node, roles: master]         → ✅ Master 匹配
  ├── addon          [Cluster]                     → ❌ Cluster 作用域，不加入
  ├── nodesPostProcess [Node, roles: master/worker] → ✅ Master 匹配
  ├── agentSwitch    [Cluster]                     → ❌ Cluster 作用域，不加入
  ├── bkeProvider    [Cluster]                     → ❌ Cluster 作用域，不加入
  ├── openFuyao      [Cluster]                     → ❌ Cluster 作用域，不加入
  ├── clusterManage  [Cluster]                     → ❌ Cluster 作用域，不加入
  ├── nodeDelete     [Node, roles: master/worker]  → ✅ Master 匹配
  └── clusterHealth  [Cluster]                     → ❌ Cluster 作用域，不加入

Master NodeConfig.spec.components (8个):
  1. nodesEnv       (dependencies: [])
  2. bkeAgent       (dependencies: [nodesEnv])
  3. loadBalancer   (dependencies: [nodesEnv])
  4. containerd     (dependencies: [nodesEnv])
  5. etcd           (dependencies: [nodesEnv])
  5. kubernetes     (dependencies: [containerd, etcd])
  7. nodesPostProcess (dependencies: [kubernetes])
  8. nodeDelete     (dependencies: [])
```
### 8.5.2 Worker 节点的 NodeConfig 组件列表
```
ReleaseImage.componentVersions (16个):
  ├── bkeAgent       [Node, roles: master/worker]  → ✅ Worker 匹配
  ├── nodesEnv       [Node, roles: master/worker]  → ✅ Worker 匹配
  ├── clusterAPI     [Cluster]                     → ❌ Cluster 作用域
  ├── certs          [Cluster]                     → ❌ Cluster 作用域
  ├── loadBalancer   [Node, roles: master]         → ❌ Worker 不匹配
  ├── kubernetes     [Node, roles: master/worker]  → ✅ Worker 匹配
  ├── containerd     [Node, roles: master/worker]  → ✅ Worker 匹配
  ├── etcd           [Node, roles: master]         → ❌ Worker 不匹配
  ├── addon          [Cluster]                     → ❌ Cluster 作用域
  ├── nodesPostProcess [Node, roles: master/worker] → ✅ Worker 匹配
  ├── agentSwitch    [Cluster]                     → ❌ Cluster 作用域
  ├── bkeProvider    [Cluster]                     → ❌ Cluster 作用域
  ├── openFuyao      [Cluster]                     → ❌ Cluster 作用域
  ├── clusterManage  [Cluster]                     → ❌ Cluster 作用域
  ├── nodeDelete     [Node, roles: master/worker]  → ✅ Worker 匹配
  └── clusterHealth  [Cluster]                     → ❌ Cluster 作用域

Worker NodeConfig.spec.components (7个):
  1. nodesEnv       (dependencies: [])
  2. bkeAgent       (dependencies: [nodesEnv])
  3. containerd     (dependencies: [nodesEnv])
  4. kubernetes     (dependencies: [containerd])
  5. nodesPostProcess (dependencies: [kubernetes])
  6. nodeDelete     (dependencies: [])
```
## 8.6 组件列表的动态更新
NodeConfig 的组件列表不是静态的，会随以下事件动态更新：

| 事件 | 触发逻辑 |
|------|----------|
| **版本升级** | ClusterVersion.desiredVersion 变更 → 读取新 ReleaseImage → 重新组装组件列表 |
| **节点扩容** | 新 NodeConfig 创建 → 读取当前 ReleaseImage → 组装组件列表 |
| **节点缩容** | NodeConfig.phase=Deleting → 触发 uninstallAction → 组件列表标记为 Deleting |
| **组件新增** | ReleaseImage 新增 ComponentVersion → 重新组装 → 新组件加入列表 |
| **节点标签变更** | NodeConfig labels 变更 → 重新匹配 nodeSelector → 增删组件 |
## 8.7 NodeConfig CRD 定义
```go
type NodeConfigSpec struct {
    // 节点标识
    NodeName    string   `json:"nodeName"`
    NodeIP      string   `json:"nodeIP"`
    Roles       []string `json:"roles"`

    // 引用
    ClusterRef  *ObjectReference `json:"clusterRef,omitempty"`
    ReleaseRef  *ObjectReference `json:"releaseRef,omitempty"`

    // 组件列表（由 Controller 根据上述规则自动组装）
    Components  []ComponentRef   `json:"components,omitempty"`

    // 节点级配置（供模板变量引用）
    NodeOS      NodeOSInfo       `json:"nodeOS,omitempty"`
}

type ComponentRef struct {
    ComponentName ComponentName    `json:"componentName"`
    Version       string           `json:"version"`
    State         ComponentState   `json:"state"`
    Message       string           `json:"message,omitempty"`
    LastUpdated   metav1.Time      `json:"lastUpdated,omitempty"`
}

type ComponentState string

const (
    ComponentPending    ComponentState = "Pending"
    ComponentInstalling ComponentState = "Installing"
    ComponentInstalled  ComponentState = "Installed"
    ComponentUpgrading  ComponentState = "Upgrading"
    ComponentUpgraded   ComponentState = "Upgraded"
    ComponentFailed     ComponentState = "Failed"
    ComponentDeleting   ComponentState = "Deleting"
)

type NodeConfigStatus struct {
    Phase       NodeConfigPhase  `json:"phase,omitempty"`
    Components  []ComponentStatus `json:"components,omitempty"`
    Conditions  []metav1.Condition `json:"conditions,omitempty"`
}

type ComponentStatus struct {
    ComponentName ComponentName    `json:"componentName"`
    Version       string           `json:"version"`
    State         ComponentState   `json:"state"`
    Action        string           `json:"action,omitempty"`
    StepStatus    []StepStatus     `json:"stepStatus,omitempty"`
    Message       string           `json:"message,omitempty"`
    LastUpdated   metav1.Time      `json:"lastUpdated,omitempty"`
}

type StepStatus struct {
    StepName  string `json:"stepName"`
    State     string `json:"state"`
    Stdout    string `json:"stdout,omitempty"`
    Retries   int    `json:"retries,omitempty"`
    Duration  string `json:"duration,omitempty"`
}
```
## 8.8 组件列表组装与执行的关系
```
┌─────────────────────────────────────────────────────────────────┐
│                    ClusterVersion Controller                    │
│                                                                 │
│  管理Scope=Cluster的组件：                                     │
│  clusterAPI, certs, addon, agentSwitch,                         │
│  bkeProvider, openFuyao, clusterManage, clusterHealth           │
│                                                                 │
│  直接调用 ActionEngine 在控制面执行                             │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                    NodeConfig Controller                        │
│                                                                 │
│  管理Scope=Node的组件（每个节点独立）：                         │
│  bkeAgent, nodesEnv, loadBalancer, containerd,                  │
│  etcd, kubernetes, nodesPostProcess, nodeDelete                 │
│                                                                 │
│  调用 ActionEngine 在目标节点上执行                             │
│  按 DAG 依赖顺序逐组件处理                                      │
│  按 ActionStrategy 策略调度多节点执行                           │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                    ActionEngine                                 │
│                                                                 │
│  统一执行入口，不区分 Cluster/Node 作用域                       │
│  区别仅在于：                                                   │
│    Cluster 作用域 → 无目标节点，在控制面直接执行                │
│    Node 作用域   → 有目标节点，下发到 Agent 执行                │
└─────────────────────────────────────────────────────────────────┘
```
**关键设计点**：
1. **NodeConfig 的组件列表由 Controller 自动组装**，用户不需要手动指定
2. **组装依据是 ComponentVersion 的 scope + nodeSelector**，确保每个节点只包含它需要的组件
3. **Scope=Cluster 的组件不进入 NodeConfig**，由 ClusterVersion Controller 直接管理
4. **组件列表按 DAG 拓扑排序**，确保依赖顺序正确
5. **组件列表动态更新**，随版本变更、节点标签变更自动调整
              
oldStr: **关键设计点**：etcd 逐节点升级的本质不是"脚本复杂度"问题，而是"编排语义"问题——需要 ActionEngine 理解"对每个节点执行完整步骤序列并等待确认"这一语义。通过增强 Rolling 策略的 `waitForCompletion` 字段，可以在不引入新 ActionType 的前提下完整支持 etcd 逐节点升级。

以上三个 diff 共同完成了 KEPU-2.md 的增强。下面总结本次修改的核心内容：
## 修改总结
### 一、ActionStep CRD 增强（新增 SourceSpec）
在 `ActionStep` 中新增了 `ScriptSource` 和 `ManifestSource` 字段，与原有的 `Script`/`Manifest` 内联字段互斥，支持三种来源：

| 来源类型 | 说明 | 适用场景 |
|----------|------|----------|
| **Inline** | 内容直接嵌入 YAML | 短脚本（<100行）、简单清单 |
| **Remote** | 从 HTTP/HTTPS URL 下载 | 长脚本、外部托管清单、共享脚本 |
| **Local** | 从 ReleaseImage 包或节点本地读取 | 离线交付、随包分发 |

同时增强了 `ChartAction`，新增 `ChartSource` 字段支持三种 Chart 来源：
- **HTTPRepo**：传统 Helm Chart 仓库
- **OCI**：OCI 兼容仓库（如 Harbor）
- **Local**：本地 Chart 包
### 二、ActionEngine 设计思路及执行流程（第六章）
设计了 ActionEngine 的五层架构：
1. **Renderer**（模板渲染）— Go text/template 渲染模板变量
2. **Resolver**（来源解析）— 统一处理 Inline/Remote/Local 三种来源
3. **Evaluator**（条件求值）— 求值 condition 表达式
4. **Scheduler**（策略调度）— 按 Parallel/Serial/Rolling 策略调度节点执行
5. **Executor**（步骤执行）— 下发到 Agent/API Server 执行

详细定义了完整的执行流程：确定 Action → 匹配节点 → preCheck → 按策略执行 steps → postCheck → 更新 Status。
### 三、Source 类型使用示例（第七章）
为每种来源类型提供了完整的 YAML 示例：
- Script：Inline / Remote / Local
- Manifest：Inline / Remote / Local
- Chart：HTTPRepo / OCI / Local + ValuesFrom
### 四、NodeConfig 组件列表组装机制（第八章）
核心组装规则：
1. **Scope 过滤**：`Scope=Cluster` 的组件不进入 NodeConfig，由 ClusterVersion Controller 直接管理
2. **NodeSelector 匹配**：按 `roles` + `matchLabels` + `matchExpressions` 匹配节点
3. **DAG 拓扑排序**：组件列表按依赖关系排序
4. **动态更新**：随版本变更、节点标签变更自动调整

Master 节点组装 8 个组件，Worker 节点组装 7 个组件（无 etcd 和 loadBalancer）。
