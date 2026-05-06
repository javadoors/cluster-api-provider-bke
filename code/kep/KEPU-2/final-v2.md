# KEPU-2: 基于 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 的声明式集群版本管理
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-2 |
| **标题** | 声明式集群版本管理：YAML 配置驱动的安装、升级与扩缩容 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **修订日期** | 2026-05-06 |
| **依赖** | KEPU-1（整体架构重构） |

## 1. 摘要
本提案设计基于六个 CRD（ClusterVersion、ReleaseImage、ComponentVersion、ComponentVersionBinding、UpgradePath、NodeConfig）的声明式集群版本管理方案。

**核心变化**：各 Phase 的安装、升级、卸载逻辑通过 YAML 配置声明，由通用 ActionEngine 解释执行。支持两种执行方式：
- **纯声明式**：通过 Script/Manifest/Chart/Kubectl 等 ActionType 直接编写
- **混合式**：通过 Inline ActionType 引用 ComponentVersionFactory 中已有的 Phase 实现

**设计原则**：
- **配置即代码**：组件生命周期（安装/升级/卸载/健康检查）全部声明在 ComponentVersion YAML 中
- **通用引擎**：ActionEngine 是唯一的执行器，解释 YAML 中的 Action 定义并执行
- **渐进式迁移**：支持通过 Inline ActionType 复用现有 Phase 代码，逐步演进到纯声明式
- **模板化**：脚本和 manifest 支持模板变量（`{{.Version}}`、`{{.NodeIP}}` 等），运行时渲染
- **关注点分离**：ComponentVersion 定义"能力"（能做什么），ComponentVersionBinding 表达"意图"（要做什么）

## 2. 动机
### 2.1 现有架构问题
| 问题 | 现状 | 影响 |
|------|------|------|
| **版本概念缺失** | BKECluster 仅记录 KubernetesVersion/EtcdVersion/ContainerdVersion/OpenFuyaoVersion | 无整体版本概念，无法回答"集群当前是什么版本" |
| **发布清单缺失** | 组件版本散落在 BKECluster Spec 各字段 | 无法追溯某个版本包含哪些组件及版本 |
| **命令式编排** | 各 Phase 通过 `NeedExecute` + 固定顺序执行 | 无法并行、无法跳过、无法回滚 |
| **组件独立演进受限** | Phase 之间硬编码依赖，升级路径固定 | 无法独立升级单个组件，无法 A/B 测试 |
| **安装逻辑与编排耦合** | Phase 代码既包含编排逻辑又包含安装逻辑 | 无法复用安装逻辑，无法独立测试 |
| **扩缩容与升级耦合** | EnsureWorkerDelete/EnsureMasterDelete 与升级 Phase 混在一起 | 缩容逻辑无法独立演进 |
| **升级卸载旧组件缺失** | 升级时直接覆盖安装，无旧版本卸载流程 | 旧版本残留文件/配置可能导致冲突 |
| **迁移成本高** | 新架构要求完全重写所有组件逻辑 | 实施难度大，风险高 |

### 2.2 OpenShift CVO 的启发
OpenShift 的 Cluster Version Operator（CVO）采用以下架构：
```
ClusterVersion (集群版本)
    └── desiredUpdate.release (Release Image 引用)
            └── Release Payload (容器镜像)
                    └── release-manifests/ (组件manifest清单)
```
**借鉴点**：
1. ClusterVersion 作为集群版本的全局入口
2. ReleaseImage 作为版本清单的载体（不可变）
3. 组件 manifest 声明式定义，CVO 自动编排
4. 升级路径显式声明，支持兼容性检查

**差异点**：
1. OpenShift 使用容器镜像作为 Release Payload，我们使用 ReleaseImage CRD
2. OpenShift 的 manifest 是原生 Kubernetes 资源，我们需要支持 Script/Manifest/Helm/Kubectl/Inline 五种执行方式
3. OpenShift 的组件是 Operator，我们的组件包括节点级和集群级两种
4. 我们需要支持升级时先卸载旧组件再安装新组件的流程
5. **新增**：我们支持通过 Inline ActionType 在渐进式迁移阶段复用现有 Phase 代码

## 3. 目标
### 3.1 主要目标
1. 定义 ClusterVersion、ReleaseImage、ComponentVersion、ComponentVersionBinding、UpgradePath、NodeConfig 六个 CRD 及其关联关系
2. 实现 ClusterVersion 控制器：管理集群版本生命周期（安装→升级→回滚），编排 ComponentVersionBinding，集成 UpgradePath 验证
3. 实现 ReleaseImage 控制器：验证发布清单，生成 ComponentVersion 列表
4. 实现 ComponentVersion 控制器：定义组件能力目录（installAction/upgradeAction/uninstallAction）
5. 实现 ComponentVersionBinding 控制器：执行组件生命周期操作（安装/升级/卸载/健康检查）
6. 实现 UpgradePath 控制器：管理升级路径的生命周期（验证/阻止/废弃/发现）
7. 实现 NodeConfig 控制器：管理节点组件的安装/升级/卸载，触发 ComponentVersionBinding 节点级操作
8. 将现有 PhaseFrame 各 Phase 改造为支持 ComponentVersion 接口
9. 建立 ComponentVersionFactory：注册改造后的 Phase 实现供 Inline ActionType 引用
10. **新增**：支持通过 Inline ActionType 在渐进式迁移阶段复用现有 Phase 代码
11. 实现升级时先卸载旧组件再安装新组件的完整流程

### 3.2 非目标
1. 不实现 OpenShift 式的 Release Image 容器镜像载体（使用 CRD 替代）
2. 不实现多集群版本管理（仅单集群）
3. 不实现 OS 级别的版本管理（由 OSProvider 独立负责）
4. 不实现版本包的构建与发布流程（由 CI/CD 独立负责）
5. 不修改现有 BKECluster CRD 的 Spec 定义

## 4. 范围
### 4.1 在范围内
| 范围 | 说明 |
|------|------|
| CRD 定义与注册 | 六个核心 CRD 的 API 定义 |
| 控制器实现 | 六个控制器的 Reconcile 逻辑 |
| ActionEngine | 通用执行引擎，解释执行 YAML 中的 Action 定义 |
| **Inline ActionType 支持** | **支持引用 ComponentVersionFactory 中的 Phase 实现** |
| **Phase 改造** | **改造现有 Phase 使其兼容 ComponentVersion 接口** |
| **ComponentVersionFactory** | **注册和管理 Phase 实现的工厂** |
| Phase→ComponentVersion 迁移 | 20+ 个 Phase 到 ComponentVersion 的映射与迁移 |
| DAG 调度 | 组件依赖图与调度算法 |
| 版本升级流程 | PreCheck→UninstallOld→Upgrade→PostCheck→Rollback |
| 扩缩容流程 | NodeConfig 增删触发组件安装/卸载 |
| 关注点分离 | ComponentVersion（能力定义）与 ComponentVersionBinding（运行时意图）分离 |
| **渐进式迁移支持** | **Feature Gate 控制迁移过程，支持新旧共存** |

### 4.2 不在范围内
| 范围 | 原因 |
|------|------|
| 版本包构建 | CI/CD 流程独立 |
| 多集群管理 | 超出单集群版本管理范围 |
| OS 版本管理 | 由 OSProvider 独立负责 |
| UI/CLI 交互 | 仅定义 API，不涉及前端 |

## 5. 约束
| 约束 | 说明 |
|------|------|
| **向后兼容** | 必须支持从现有 PhaseFrame 平滑迁移，不能破坏现有集群 |
| **Feature Gate** | 新架构通过 Feature Gate 开关控制，默认关闭 |
| **单集群单 ClusterVersion** | 每个集群仅允许一个 ClusterVersion 实例 |
| **1:1 ReleaseImage** | 每个 ClusterVersion 仅关联一个 ReleaseImage |
| **ReleaseImage 不可变** | 创建后不可修改，确保版本清单一致性 |
| **组件不可降级** | 默认不允许组件版本降级，除非显式设置 allowDowngrade |
| **离线环境** | 必须支持离线环境，所有资源通过 CRD 定义 |
| **性能** | 控制器 Reconcile 周期不超过 30s，升级单节点不超过 10min |
| **ActionEngine 唯一执行路径** | 不绕过引擎直接操作 |
| **Inline ActionType 隔离** | Inline Phase 执行结果与声明式 Action 结果格式统一 |

## 6. 场景
### 6.1 场景一：全新集群安装（使用纯声明式）
```
用户创建 BKECluster
    → BKEClusterReconciler 创建 ClusterVersion（引用 ReleaseImage）
        → ClusterVersion 控制器解析 ReleaseImage
            → 为每个组件创建 ComponentVersionBinding（spec.desiredVersion = 组件版本）
            → DAGScheduler 计算安装顺序，按序更新 Binding
                → ComponentVersionBinding 控制器检测 desiredVersion != installedVersion
                    → 通过 componentVersionRef 找到 ComponentVersion
                    → 从 ComponentVersion.versions[] 中查找对应版本的 installAction
                    → 执行 installAction（YAML 声明：Script/Manifest/Chart/Kubectl）
                    → 健康检查（YAML 声明）
                → Node 级组件：NodeConfig 控制器在对应节点上触发安装
                → 全部组件完成 → ClusterVersion 更新 currentVersion
```

### 6.2 场景二：渐进式迁移（使用 Inline ActionType 复用现有 Phase）
```
用户通过 Inline ActionType 引用现有 Phase 实现
    → 开启 Feature Gate: DeclarativeOrchestration=true
    → ComponentVersion 中使用 Inline ActionType
    → ActionEngine 查询 ComponentVersionFactory
    → 工厂返回对应 Phase 的改造后实现
    → Phase 实现执行并返回标准化结果
    → ActionEngine 根据结果更新 ComponentVersionBinding.status
    → 逐步将 Inline Phase 改写为纯声明式
```

### 6.3 场景三：集群版本升级（含旧组件卸载）
```
用户修改 ClusterVersion.spec.desiredVersion = "v2.6.0"
    │
    ├── ClusterVersion Controller
    │   ├── 查找新 ReleaseImage → 解析新组件版本列表
    │   └── 按升级 DAG 逐步更新 ComponentVersionBinding.spec.desiredVersion
    │       （仅修改 desiredVersion 字段，不触碰 ComponentVersion）
    │
    ├── ComponentVersionBinding Controller（监听 desiredVersion 变化）
    │   ├── 检测 spec.desiredVersion != status.installedVersion
    │   ├── 通过 spec.componentVersionRef 找到 ComponentVersion
    │   ├── 从 ComponentVersion.spec.versions[] 中查找对应版本的 upgradeAction
    │   ├── 查找旧版本：
    │   │   └── ClusterVersion.status.currentReleaseRef
    │   │       → 旧 ReleaseImage
    │   │         → 旧 ComponentVersion
    │   │           → 旧版本 uninstallAction
    │   ├── 执行旧版本 uninstallAction（YAML 声明或 Inline）
    │   ├── 执行新版本 upgradeAction（YAML 声明或 Inline）
    │   └── 健康检查（YAML 声明）
    │
    └── 全部组件完成 → ClusterVersion 更新 currentVersion
```

### 6.4 场景四：单组件独立升级
```
用户修改 ComponentVersionBinding.spec.desiredVersion
    → ComponentVersionBinding 控制器检测 desiredVersion != installedVersion
        → 通过 componentVersionRef 找到 ComponentVersion
        → 从 ComponentVersion.spec.versions[] 中查找目标版本的 upgradeAction
        → 执行 PreCheck（YAML 声明或 Inline）
        → 查找旧版本 uninstallAction（通过 ClusterVersion.currentReleaseRef）
        → 执行旧版本 uninstallAction（YAML 声明或 Inline）
        → 执行新版本 upgradeAction（YAML 声明或 Inline）
        → 执行 PostCheck / 健康检查（YAML 声明或 Inline）
        → 更新 ComponentVersionBinding.status.installedVersion
```

### 6.5 场景五：节点扩缩容、升级回滚、纳管现有集群
[参考原提案内容，支持 Inline ActionType]

## 7. 提案

### 7.1 核心改进：Inline ActionType 支持

#### 7.1.1 Phase 改造架构

**改造目标**：使现有 Phase 实现兼容 ComponentVersion 接口

```
现有 PhaseFrame:
┌──────────────────────────────────┐
│  Phase 接口                       │
│  - NeedExecute()                 │
│  - Execute() → error             │
│  - Recover() → error             │
└──────────────────────────────────┘
           │
           └─ 直接调用 BKECluster.Status 修改
           └─ 无法提供标准化的执行结果

改造后设计:
┌──────────────────────────────────────────────────┐
│  ComponentVersionExecutor 接口                    │
│  - Execute(ctx, executor) → (result, error)     │
│  - Rollback(ctx, executor) → (result, error)    │
│  - HealthCheck(ctx, executor) → (result, error) │
│  - Config 结构：可从 TemplateContext 获取       │
├──────────────────────────────────────────────────┤
│  返回结果 (ComponentVersionExecutionResult):     │
│  - Phase: Installing/Installed/Failed           │
│  - Message: 执行信息                            │
│  - NodeStatuses: 节点级状态                      │
│  - Duration: 执行耗时                           │
│  - Output: 执行输出                             │
└──────────────────────────────────────────────────┘
```

#### 7.1.2 改造流程

**步骤 1：定义 ComponentVersionExecutor 接口**

```go
// pkg/cvo/executor.go
package cvo

import "context"

// ComponentVersionExecutor 定义 Phase 改造后的接口
type ComponentVersionExecutor interface {
    // 执行组件安装/升级/卸载操作
    Execute(ctx context.Context, executor *ComponentVersionExecutionContext) (*ComponentVersionExecutionResult, error)
    
    // 执行回滚操作
    Rollback(ctx context.Context, executor *ComponentVersionExecutionContext) (*ComponentVersionExecutionResult, error)
    
    // 执行健康检查
    HealthCheck(ctx context.Context, executor *ComponentVersionExecutionContext) (*ComponentVersionExecutionResult, error)
}

type ComponentVersionExecutionContext struct {
    // 组件信息
    ComponentName  string
    Version        string
    
    // 集群信息
    ClusterRef     *ClusterReference
    BKECluster     *BKECluster
    
    // 节点信息
    NodeConfigs    []*NodeConfig
    NodeSelector   *NodeSelector
    
    // 模板上下文
    TemplateContext *TemplateContext
    
    // 执行工具
    Logger         logr.Logger
    Recorder       record.EventRecorder
    Client         client.Client
}

type ComponentVersionExecutionResult struct {
    // 执行阶段
    Phase ComponentPhase
    
    // 执行消息
    Message string
    
    // 节点级状态
    NodeStatuses map[string]*NodeExecutionStatus
    
    // 执行耗时
    Duration time.Duration
    
    // 执行输出
    Output string
    
    // 错误信息
    Error string
}

type NodeExecutionStatus struct {
    NodeName string
    Phase    ComponentPhase
    Message  string
    Error    string
}
```

**步骤 2：改造现有 Phase 实现**

```go
// phaseframe/ensure_bke_agent.go - 改造示例
package phaseframe

import (
    "context"
    cvov1 "cluster-api-provider-bke/api/cvo/v1beta1"
    "cluster-api-provider-bke/pkg/cvo"
)

type EnsureBKEAgent struct {
    phase *Phase  // 原有 Phase 实现
}

// 实现 ComponentVersionExecutor 接口
func (e *EnsureBKEAgent) Execute(
    ctx context.Context, 
    executor *cvo.ComponentVersionExecutionContext,
) (*cvo.ComponentVersionExecutionResult, error) {
    // 1. 调用原有 Phase 的 Execute()
    if err := e.phase.Execute(executor.BKECluster); err != nil {
        return &cvo.ComponentVersionExecutionResult{
            Phase:   cvov1.ComponentInstallFailed,
            Message: "Failed to execute EnsureBKEAgent",
            Error:   err.Error(),
        }, err
    }
    
    // 2. 检查执行结果
    if executor.BKECluster.Status.Phase == "Failed" {
        return &cvo.ComponentVersionExecutionResult{
            Phase:   cvov1.ComponentInstallFailed,
            Message: executor.BKECluster.Status.Message,
        }, nil
    }
    
    // 3. 收集节点级状态
    nodeStatuses := make(map[string]*cvo.NodeExecutionStatus)
    for _, nc := range executor.NodeConfigs {
        nodeStatuses[nc.Spec.NodeName] = &cvo.NodeExecutionStatus{
            NodeName: nc.Spec.NodeName,
            Phase:    cvov1.ComponentInstalled,
            Message:  "BKEAgent installed successfully",
        }
    }
    
    // 4. 返回标准化结果
    return &cvo.ComponentVersionExecutionResult{
        Phase:        cvov1.ComponentInstalled,
        Message:      "BKEAgent installation completed",
        NodeStatuses: nodeStatuses,
        Duration:     time.Since(time.Now()), // 实际应记录开始时间
    }, nil
}

func (e *EnsureBKEAgent) Rollback(
    ctx context.Context,
    executor *cvo.ComponentVersionExecutionContext,
) (*cvo.ComponentVersionExecutionResult, error) {
    // 实现回滚逻辑
    // ...
    return &cvo.ComponentVersionExecutionResult{
        Phase:   cvov1.ComponentInstalled,
        Message: "BKEAgent rollback completed",
    }, nil
}

func (e *EnsureBKEAgent) HealthCheck(
    ctx context.Context,
    executor *cvo.ComponentVersionExecutionContext,
) (*cvo.ComponentVersionExecutionResult, error) {
    // 实现健康检查逻辑
    // ...
    return &cvo.ComponentVersionExecutionResult{
        Phase:   cvov1.ComponentHealthy,
        Message: "BKEAgent health check passed",
    }, nil
}
```

**步骤 3：建立 ComponentVersionFactory**

```go
// pkg/cvo/factory.go
package cvo

import (
    "fmt"
)

// ComponentVersionFactory 管理所有已改造的 Phase 实现
type ComponentVersionFactory struct {
    executors map[string]map[string]ComponentVersionExecutor  // map[componentName][version]executor
}

var DefaultFactory = &ComponentVersionFactory{
    executors: make(map[string]map[string]ComponentVersionExecutor),
}

// Register 注册一个 Phase 实现
func (f *ComponentVersionFactory) Register(
    componentName string,
    version string,
    actionType string,  // "install", "upgrade", "rollback", "uninstall", "healthCheck"
    executor ComponentVersionExecutor,
) error {
    key := fmt.Sprintf("%s:%s:%s", componentName, version, actionType)
    
    if f.executors[key] == nil {
        f.executors[key] = make(map[string]ComponentVersionExecutor)
    }
    
    f.executors[key][actionType] = executor
    return nil
}

// Get 获取一个 Phase 实现
func (f *ComponentVersionFactory) Get(
    componentName string,
    version string,
    actionType string,
) (ComponentVersionExecutor, error) {
    key := fmt.Sprintf("%s:%s:%s", componentName, version, actionType)
    
    executor, ok := f.executors[key]
    if !ok {
        return nil, fmt.Errorf("executor not found for %s", key)
    }
    
    return executor, nil
}

// InitializeBuiltinExecutors 初始化所有内置 Phase 实现
func InitializeBuiltinExecutors() error {
    // 注册所有已改造的 Phase
    DefaultFactory.Register("bkeAgent", "v1.0.0", "install", &phaseframe.EnsureBKEAgent{})
    DefaultFactory.Register("bkeAgent", "v1.0.0", "upgrade", &phaseframe.EnsureBKEAgent{})
    // ... 注册其他 Phase
    
    return nil
}
```

#### 7.1.3 ActionEngine 中的 Inline 支持

```go
// pkg/actionengine/engine.go
type ActionEngine struct {
    factory *cvo.ComponentVersionFactory
}

type ActionType string

const (
    ActionScript   ActionType = "Script"
    ActionManifest ActionType = "Manifest"
    ActionChart    ActionType = "Chart"
    ActionKubectl  ActionType = "Kubectl"
    ActionInline   ActionType = "Inline"  // 新增
)

// Execute 执行一个 Action
func (e *ActionEngine) Execute(
    ctx context.Context,
    action *cvov1.ActionSpec,
    context *TemplateContext,
) (*ActionExecutionResult, error) {
    
    // 按步骤执行
    for _, step := range action.Steps {
        result, err := e.executeStep(ctx, step, context)
        if err != nil {
            if action.Strategy.FailurePolicy == cvov1.FailFast {
                return result, err
            }
            // 记录错误继续执行
        }
    }
    
    return &ActionExecutionResult{
        Phase: cvov1.ComponentInstalled,
        Message: "Action executed successfully",
    }, nil
}

// executeStep 执行单个步骤
func (e *ActionEngine) executeStep(
    ctx context.Context,
    step *cvov1.ActionStep,
    context *TemplateContext,
) (*ActionExecutionResult, error) {
    
    // 条件判断
    if step.Condition != "" {
        shouldExecute, err := e.evaluator.Evaluate(step.Condition, context)
        if err != nil || !shouldExecute {
            return &ActionExecutionResult{
                Phase:   cvov1.ComponentSkipped,
                Message: "Step skipped due to condition",
            }, nil
        }
    }
    
    switch step.Type {
    case cvov1.ActionScript:
        return e.executeScriptStep(ctx, step, context)
    case cvov1.ActionManifest:
        return e.executeManifestStep(ctx, step, context)
    case cvov1.ActionChart:
        return e.executeChartStep(ctx, step, context)
    case cvov1.ActionKubectl:
        return e.executeKubectlStep(ctx, step, context)
    case cvov1.ActionInline:  // 新增处理
        return e.executeInlineStep(ctx, step, context)
    default:
        return nil, fmt.Errorf("unknown action type: %s", step.Type)
    }
}

// executeInlineStep 执行 Inline 步骤
func (e *ActionEngine) executeInlineStep(
    ctx context.Context,
    step *cvov1.ActionStep,
    context *TemplateContext,
) (*ActionExecutionResult, error) {
    
    // 从 Inline.PhaseRef 解析组件名、版本和操作类型
    phaseRef := step.Inline.PhaseRef
    
    // 从工厂获取对应的 Phase 实现
    executor, err := e.factory.Get(
        phaseRef.ComponentName,
        phaseRef.Version,
        phaseRef.ActionType,
    )
    if err != nil {
        return nil, fmt.Errorf("failed to get inline executor: %w", err)
    }
    
    // 构造执行上下文
    executionCtx := &cvo.ComponentVersionExecutionContext{
        ComponentName:   phaseRef.ComponentName,
        Version:         phaseRef.Version,
        TemplateContext: context,
        Logger:          e.logger,
        // ... 其他字段
    }
    
    // 根据操作类型调用对应方法
    var result *cvo.ComponentVersionExecutionResult
    switch phaseRef.ActionType {
    case "install":
        result, err = executor.Execute(ctx, executionCtx)
    case "upgrade":
        result, err = executor.Execute(ctx, executionCtx)
    case "rollback":
        result, err = executor.Rollback(ctx, executionCtx)
    case "uninstall":
        result, err = executor.Execute(ctx, executionCtx)
    case "healthCheck":
        result, err = executor.HealthCheck(ctx, executionCtx)
    default:
        return nil, fmt.Errorf("unknown inline action type: %s", phaseRef.ActionType)
    }
    
    if err != nil {
        return nil, err
    }
    
    // 将 Phase 的执行结果转换为标准的 ActionExecutionResult
    return e.convertPhaseResultToActionResult(result)
}
```

#### 7.1.4 ComponentVersion CRD 中的 Inline ActionType

```go
// api/cvo/v1beta1/action_types.go - 增强

type ActionStep struct {
    Name          string            `json:"name"`
    Type          ActionType        `json:"type"`
    Script        string            `json:"script,omitempty"`
    ScriptSource  *SourceSpec       `json:"scriptSource,omitempty"`
    Manifest      string            `json:"manifest,omitempty"`
    ManifestSource *SourceSpec      `json:"manifestSource,omitempty"`
    Chart         *ChartAction      `json:"chart,omitempty"`
    Kubectl       *KubectlAction    `json:"kubectl,omitempty"`
    Inline        *InlineAction     `json:"inline,omitempty"`           // 新增
    Condition     string            `json:"condition,omitempty"`
    OnFailure     FailurePolicy     `json:"onFailure,omitempty"`
    Retries       int               `json:"retries,omitempty"`
    NodeSelector  *NodeSelector     `json:"nodeSelector,omitempty"`
}

type ActionType string

const (
    ActionScript   ActionType = "Script"
    ActionManifest ActionType = "Manifest"
    ActionChart    ActionType = "Chart"
    ActionKubectl  ActionType = "Kubectl"
    ActionInline   ActionType = "Inline"    // 新增
)

// InlineAction 用于引用 ComponentVersionFactory 中的 Phase 实现
type InlineAction struct {
    // PhaseRef 引用组件版本化的 Phase 实现
    PhaseRef InlinePhaseReference `json:"phaseRef"`
}

type InlinePhaseReference struct {
    // ComponentName 组件名称，对应 ComponentVersionFactory 中的 key
    ComponentName string `json:"componentName"`
    
    // Version 组件版本
    Version string `json:"version"`
    
    // ActionType 操作类型：install/upgrade/rollback/uninstall/healthCheck
    ActionType string `json:"actionType"`
    
    // Params 传递给 Phase 实现的参数（可选）
    Params *runtime.RawExtension `json:"params,omitempty"`
}
```

#### 7.1.5 YAML 使用示例

**纯声明式方式**：
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
spec:
  componentName: etcd
  scope: Node
  versions:
    - version: v3.5.12
      installAction:
        steps:
          - name: install-etcd
            type: Script
            script: |
              # 安装 etcd
              apt-get install -y etcd=3.5.12
```

**使用 Inline ActionType 复用现有 Phase**：
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: kubernetes-v1.29.0
spec:
  componentName: kubernetes
  scope: Node
  versions:
    - version: v1.29.0
      installAction:
        steps:
          - name: install-kubernetes-via-phase
            type: Inline
            inline:
              phaseRef:
                componentName: kubernetes
                version: v1.29.0
                actionType: install  # 调用 ComponentVersionFactory 中注册的 Phase
      upgradeFrom:
        - fromVersion: v1.28.0
          upgradeAction:
            steps:
              - name: upgrade-kubernetes-via-phase
                type: Inline
                inline:
                  phaseRef:
                    componentName: kubernetes
                    version: v1.29.0
                    actionType: upgrade
```

**混合方式**（部分声明 + 部分 Inline）：
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: addon-v1.2.0
spec:
  componentName: addon
  scope: Cluster
  versions:
    - version: v1.2.0
      installAction:
        steps:
          # 第一步：使用现有 Phase 进行 Addon 预检
          - name: pre-check
            type: Inline
            inline:
              phaseRef:
                componentName: addon
                version: v1.2.0
                actionType: precheck
          
          # 第二步：使用声明式 Helm 安装具体组件
          - name: install-calico
            type: Chart
            chart:
              repository: https://projectcalico.docs.tigera.io/charts
              name: tigera-operator
              version: v3.26.0
              namespace: calico-system
          
          # 第三步：使用现有 Phase 进行后处理
          - name: post-process
            type: Inline
            inline:
              phaseRef:
                componentName: addon
                version: v1.2.0
                actionType: postprocess
```

### 7.2 改造后的 Phase 注册示例

```go
// phaseframe/register.go - 新增

package phaseframe

import (
    "cluster-api-provider-bke/pkg/cvo"
)

// RegisterAllPhases 注册所有改造后的 Phase 实现到 ComponentVersionFactory
func RegisterAllPhases(factory *cvo.ComponentVersionFactory) error {
    // 注册 BKEAgent
    factory.Register("bkeAgent", "v1.0.0", "install", &EnsureBKEAgent{
        phase: NewPhase("EnsureBKEAgent"),
    })
    factory.Register("bkeAgent", "v1.0.0", "upgrade", &EnsureBKEAgent{
        phase: NewPhase("EnsureBKEAgent"),
    })
    
    // 注册 NodesEnv
    factory.Register("nodesEnv", "v1.0.0", "install", &EnsureNodesEnv{
        phase: NewPhase("EnsureNodesEnv"),
    })
    
    // 注册 Containerd
    factory.Register("containerd", "v1.7.2", "install", &EnsureContainerd{
        phase: NewPhase("EnsureContainerd"),
    })
    factory.Register("containerd", "v1.7.2", "upgrade", &EnsureContainerUpgrade{
        phase: NewPhase("EnsureContainerUpgrade"),
    })
    
    // 注册 Etcd
    factory.Register("etcd", "v3.5.12", "install", &EnsureEtcd{
        phase: NewPhase("EnsureEtcd"),
    })
    factory.Register("etcd", "v3.5.12", "upgrade", &EnsureEtcdUpgrade{
        phase: NewPhase("EnsureEtcdUpgrade"),
    })
    
    // 注册 Kubernetes
    factory.Register("kubernetes", "v1.29.0", "install", &EnsureKubernetes{
        phase: NewPhase("EnsureKubernetes"),
    })
    factory.Register("kubernetes", "v1.29.0", "upgrade", &EnsureKubernetesUpgrade{
        phase: NewPhase("EnsureKubernetesUpgrade"),
    })
    
    // 注册其他组件...
    
    return nil
}
```

### 7.3 改造后的 PhaseFrame 架构

```
现有架构：
PhaseFrame
  ├── EnsureFinalizer (Phase)
  ├── EnsurePaused (Phase)
  ├── EnsureBKEAgent (Phase)
  ├── EnsureNodesEnv (Phase)
  ├── EnsureContainerd (Phase)
  ├── EnsureEtcd (Phase)
  ├── EnsureKubernetes (Phase)
  └── ... (其他 Phase)

改造后架构（双轨并行）：
PhaseFrame
  ├── Phase 接口保持不变（支持旧的命令式编排）
  │
  └── ComponentVersionExecutor 适配层
      ├── EnsureBKEAgent (ComponentVersionExecutor)
      ├── EnsureNodesEnv (ComponentVersionExecutor)
      ├── EnsureContainerd (ComponentVersionExecutor)
      ├── EnsureEtcd (ComponentVersionExecutor)
      ├── EnsureKubernetes (ComponentVersionExecutor)
      └── ... (适配的 Phase)
            │
            └── 注册到 ComponentVersionFactory
                │
                └── 通过 Inline ActionType 在 ComponentVersion 中引用
```

### 7.4 迁移路线图

| 阶段 | 时间 | Feature Gate | 行为 | 工作量 |
|------|------|-------------|------|--------|
| **Phase 1** | M1-M2 | 关闭 | 完成 CRD 定义、ActionEngine、Factory 架构 | 15 人天 |
| **Phase 2** | M2-M3 | 半开启 | 改造 5-10 个关键 Phase，支持 Inline ActionType | 10 人天 |
| **Phase 3** | M3-M4 | 可选开启 | 改造全部 Phase，支持新旧 ComponentVersion 并存 | 8 人天 |
| **Phase 4** | M4-M5 | 默认开启 | 所有新集群使用声明式，旧集群支持迁移 | 10 人天 |
| **Phase 5** | M5-M6 | 强制开启 | 完全切换到声明式，保留 Inline 支持遗留 Phase | 5 人天 |

### 7.5 资源关联关系（不变，参考原提案 7.1）

### 7.6 CRD 定义（核心部分，Inline 支持）

#### 7.6.1 ComponentVersion CRD（增强）

```go
// api/cvo/v1beta1/componentversion_types.go

type ComponentVersionSpec struct {
    ComponentName ComponentName    `json:"componentName"`
    Scope         ComponentScope   `json:"scope,omitempty"`
    Dependencies  []DependencySpec `json:"dependencies,omitempty"`
    NodeSelector  *NodeSelector    `json:"nodeSelector,omitempty"`
    Versions      []ComponentVersionEntry `json:"versions"`
}

type ComponentVersionEntry struct {
    Version         string       `json:"version"`
    InstallAction   *ActionSpec  `json:"installAction,omitempty"`
    UpgradeFrom     []UpgradeFromSpec `json:"upgradeFrom,omitempty"`
    RollbackAction  *ActionSpec  `json:"rollbackAction,omitempty"`
    UninstallAction *ActionSpec  `json:"uninstallAction,omitempty"`
    HealthCheck     *HealthCheckSpec `json:"healthCheck,omitempty"`
    Compatibility   *VersionCompatibility `json:"compatibility,omitempty"`
}

type ActionSpec struct {
    Steps       []ActionStep     `json:"steps,omitempty"`
    PreCheck    *ActionStep      `json:"preCheck,omitempty"`
    PostCheck   *ActionStep      `json:"postCheck,omitempty"`
    Timeout     *metav1.Duration `json:"timeout,omitempty"`
    Strategy    ActionStrategy   `json:"strategy,omitempty"`
}

type ActionStep struct {
    Name          string            `json:"name"`
    Type          ActionType        `json:"type"`
    Script        string            `json:"script,omitempty"`
    ScriptSource  *SourceSpec       `json:"scriptSource,omitempty"`
    Manifest      string            `json:"manifest,omitempty"`
    ManifestSource *SourceSpec      `json:"manifestSource,omitempty"`
    Chart         *ChartAction      `json:"chart,omitempty"`
    Kubectl       *KubectlAction    `json:"kubectl,omitempty"`
    Inline        *InlineAction     `json:"inline,omitempty"`      // 新增
    Condition     string            `json:"condition,omitempty"`
    OnFailure     FailurePolicy     `json:"onFailure,omitempty"`
    Retries       int               `json:"retries,omitempty"`
    NodeSelector  *NodeSelector     `json:"nodeSelector,omitempty"`
}

type ActionType string

const (
    ActionScript   ActionType = "Script"
    ActionManifest ActionType = "Manifest"
    ActionChart    ActionType = "Chart"
    ActionKubectl  ActionType = "Kubectl"
    ActionInline   ActionType = "Inline"    // 新增
)

// InlineAction 用于引用 ComponentVersionFactory 中的 Phase 实现
type InlineAction struct {
    // PhaseRef 引用组件版本化的 Phase 实现
    PhaseRef InlinePhaseReference `json:"phaseRef"`
    // Params 传递给 Phase 实现的可选参数
    Params *runtime.RawExtension `json:"params,omitempty"`
}

type InlinePhaseReference struct {
    // ComponentName 组件名称
    ComponentName string `json:"componentName"`
    // Version 组件版本
    Version string `json:"version"`
    // ActionType 操作类型：install/upgrade/rollback/uninstall/healthCheck
    ActionType string `json:"actionType"`
}
```

#### 7.6.2 其他 CRD 定义

[参考原提案 7.2-7.7 的内容，除了 ActionType 的增强外，其他内容保持不变]

## 8. 控制器设计思路

### 8.1 ComponentVersionBinding Controller 中的 Inline 支持

```go
// controllers/cvo/componentversionbinding_controller.go

type ComponentVersionBindingReconciler struct {
    Client            client.Client
    Scheme            *runtime.Scheme
    ActionEngine      *actionengine.ActionEngine
    Factory           *cvo.ComponentVersionFactory  // 新增
}

func (r *ComponentVersionBindingReconciler) Reconcile(
    ctx context.Context,
    req ctrl.Request,
) (ctrl.Result, error) {
    // ... 获取 ComponentVersionBinding 和 ComponentVersion
    
    // 获取要执行的 Action
    action := r.getTargetAction(cv, binding)
    
    // 通过 ActionEngine 执行 Action
    // ActionEngine 会自动处理 Inline ActionType
    result, err := r.ActionEngine.Execute(ctx, action, templateContext)
    
    // 更新 ComponentVersionBinding 状态
    if err != nil {
        binding.Status.Phase = cvov1.ComponentInstallFailed
    } else {
        binding.Status.Phase = cvov1.ComponentInstalled
    }
    
    return ctrl.Result{}, r.Client.Status().Update(ctx, binding)
}
```

## 9. ActionEngine 设计思路

[参考原提案 9.1-9.6 的内容，增加 Inline 步骤的处理]

## 10. 目录结构（增强）

```
cluster-api-provider-bke/
├── api/
│   └── cvo/v1beta1/
│       ├── clusterversion_types.go
│       ├── releaseimage_types.go
│       ├── componentversion_types.go      # 增加 Inline ActionType
│       ├── componentversionbinding_types.go
│       ├── nodeconfig_types.go
│       ├── upgradepath_types.go
│       ├── action_types.go                # 新增：定义 Inline 相关类型
│       └── zz_generated.deepcopy.go
├── controllers/
│   └── cvo/
│       ├── clusterversion_controller.go
│       ├── releaseimage_controller.go
│       ├── componentversion_controller.go
│       ├── componentversionbinding_controller.go
│       ├── nodeconfig_controller.go
│       ├── upgradepath_controller.go
│       └── suite_test.go
├── pkg/
│   ├── actionengine/
│   │   ├── engine.go                     # 增加 Inline 步骤处理
│   │   ├── template.go
│   │   ├── condition.go
│   │   └── executor/
│   │       ├── script_executor.go
│   │       ├── manifest_executor.go
│   │       ├── chart_executor.go
│   │       ├── kubectl_executor.go
│   │       └── inline_executor.go        # 新增：Inline 执行器
│   ├── cvo/
│   │   ├── executor.go                   # 新增：ComponentVersionExecutor 接口
│   │   ├── factory.go                    # 新增：ComponentVersionFactory
│   │   ├── orchestrator.go
│   │   ├── validator.go
│   │   ├── rollback.go
│   │   ├── dag_scheduler.go
│   │   ├── binding_helper.go
│   │   └── upgradepath_helper.go
│   └── phaseframe/
│       ├── types.go
│       ├── register.go                   # 新增：Phase 注册
│       ├── ensure_bke_agent.go           # 改造：实现 ComponentVersionExecutor
│       ├── ensure_nodes_env.go           # 改造
│       ├── ensure_containerd.go          # 改造
│       ├── ensure_etcd.go                # 改造
│       ├── ensure_kubernetes.go          # 改造
│       └── ... (其他 Phase 改造)
├── config/
│   └── components/
│       ├── kubernetes-v1.29.0.yaml       # 可使用 Inline ActionType
│       ├── etcd-v3.5.12.yaml
│       └── ... (其他组件)
└── hack/
    └── migration/
        ├── phase_to_inline.md            # 迁移指南
        └── compatibility_matrix.md       # 兼容性矩阵
```

## 11. 工作量评估（修订）

| 步骤 | 内容 | 工作量 |
|------|------|--------|
| 第一步 | CRD 定义（6 个）+ ActionEngine 基础 + Inline 支持设计 | 15 人天 |
| 第二步 | ComponentVersionExecutor 接口 + ComponentVersionFactory + 改造 5-10 个关键 Phase | 12 人天 |
| 第三步 | 改造全部 Phase + 注册到 Factory + Inline 执行器实现 | 10 人天 |
| 第四步 | 整合 ActionEngine + 升级全链路 + 扩缩容 + 回滚 | 10 人天 |
| 第五步 | 单元测试 + 集成测试 + E2E + 渐进式迁移验证 | 10 人天 |
| **总计** | | **57 人天** |

## 12. 迁移策略（修订）

| 阶段 | Feature Gate | 行为 | 新旧共存 |
|------|-------------|------|---------|
| Phase 1 | `DeclarativeOrchestration=false` | CRD 可创建，ActionEngine 可启动，Phase 改造完成 | PhaseFrame 和 CVO 并行 |
| Phase 2 | `DeclarativeOrchestration=true`（可选） | ActionEngine 执行 YAML + Inline，新集群可选使用 CVO | 新旧共存 |
| Phase 3 | `DeclarativeOrchestration=true`（默认） | 新集群默认使用 CVO，旧集群支持迁移 | 新旧共存 |
| Phase 4 | 不可逆 | 所有集群统一使用 CVO，保留 Inline 支持遗留 Phase | 仅 CVO |

## 13. 风险评估（修订）

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| YAML 脚本调试困难 | 中 | 提供 dry-run 模式 + 模板渲染预览 + 日志输出 |
| 模板变量缺失/错误 | 中 | 模板校验 + 缺失变量报错 + 默认值机制 |
| Phase 改造兼容性问题 | 中 | 详细的改造指南 + 回归测试 + 逐步改造 |
| Inline Phase 执行失败 | 中 | 标准化错误处理 + 失败重试 + 降级方案 |
| DAG 调度死锁 | 高 | 超时 + 循环依赖检测 + 手动跳过 |
| 迁移过程中的集群状态混乱 | 中 | Feature Gate 严格控制 + 完整的状态管理 + 迁移验证 |

## 14. 最佳实践

### 14.1 何时使用 Inline vs 纯声明式

| 场景 | 使用 Inline | 使用纯声明式 |
|------|------------|----------|
| 现有 Phase 实现完善 | ✅ | ❌ |
| 需要立即上线 CVO | ✅ | ❌ |
| 未来计划完全迁移 | ✅ (过渡) | ✅ (最终目标) |
| 新增组件 | ❌ | ✅ |
| 需要跨节点复杂编排 | ❌ | ✅ |
| 需要完整的模板变量支持 | ❌ | ✅ |

### 14.2 Inline Phase 改造清单

```
[ ] 定义 ComponentVersionExecutor 接口
[ ] 为每个 Phase 创建适配器（EnsureBKEAgent, etc.)
[ ] 实现 Execute/Rollback/HealthCheck 方法
[ ] 处理节点级状态聚合
[ ] 注册到 ComponentVersionFactory
[ ] 编写单元测试
[ ] 编写集成测试
[ ] 创建 YAML 示例
[ ] 文档：改造指南 + 迁移指南
```

## 15. 验收标准（修订）

1. **CRD 和接口验收**：所有 CRD 定义完整，ComponentVersionExecutor 接口明确
2. **Inline 支持验收**：ActionEngine 能正确识别和执行 Inline ActionType
3. **Factory 验收**：ComponentVersionFactory 能正确注册和查询 Phase 实现
4. **Phase 改造验收**：5+ 个关键 Phase 成功改造并通过测试
5. **YAML 声明验收**：纯声明式和 Inline 混合方式的 ComponentVersion 都能正常执行
6. **升级路径验收**：支持新旧 ComponentVersion 并存，渐进式迁移正常
7. **兼容性验收**：Feature Gate 关闭时旧 PhaseFrame 正常运行，Feature Gate 开启时新 CVO 正常运行
8. **文档验收**：提供完整的迁移指南、最佳实践、故障排查手册

## 16. 附录：Phase 改造模板

```go
// phaseframe/template_executor.go

package phaseframe

import (
    "context"
    cvov1 "cluster-api-provider-bke/api/cvo/v1beta1"
    "cluster-api-provider-bke/pkg/cvo"
)

// EnsureXxxxExecutor 是改造后的 Phase 实现模板
type EnsureXxxxExecutor struct {
    phase *Phase  // 原有 Phase 实现的引用
}

// Execute 实现 ComponentVersionExecutor.Execute
func (e *EnsureXxxxExecutor) Execute(
    ctx context.Context,
    executor *cvo.ComponentVersionExecutionContext,
) (*cvo.ComponentVersionExecutionResult, error) {
    
    // 1. 调用原有 Phase 的 Execute 方法
    if err := e.phase.Execute(executor.BKECluster); err != nil {
        return &cvo.ComponentVersionExecutionResult{
            Phase:   cvov1.ComponentInstallFailed,
            Message: "Failed to execute EnsureXxxx",
            Error:   err.Error(),
        }, err
    }
    
    // 2. 检查执行结果状态
    if executor.BKECluster.Status.Phase == "Failed" {
        return &cvo.ComponentVersionExecutionResult{
            Phase:   cvov1.ComponentInstallFailed,
            Message: executor.BKECluster.Status.Message,
        }, nil
    }
    
    // 3. 收集节点级状态（如果是 Node scope）
    nodeStatuses := make(map[string]*cvo.NodeExecutionStatus)
    for _, nc := range executor.NodeConfigs {
        nodeStatuses[nc.Spec.NodeName] = &cvo.NodeExecutionStatus{
            NodeName: nc.Spec.NodeName,
            Phase:    cvov1.ComponentInstalled,
            Message:  "Component installed successfully",
        }
    }
    
    // 4. 返回标准化结果
    return &cvo.ComponentVersionExecutionResult{
        Phase:        cvov1.ComponentInstalled,
        Message:      "Component installation completed",
        NodeStatuses: nodeStatuses,
    }, nil
}

// Rollback 实现 ComponentVersionExecutor.Rollback
func (e *EnsureXxxxExecutor) Rollback(
    ctx context.Context,
    executor *cvo.ComponentVersionExecutionContext,
) (*cvo.ComponentVersionExecutionResult, error) {
    // 实现回滚逻辑
    // ...
    return &cvo.ComponentVersionExecutionResult{
        Phase:   cvov1.ComponentInstalled,
        Message: "Component rollback completed",
    }, nil
}

// HealthCheck 实现 ComponentVersionExecutor.HealthCheck
func (e *EnsureXxxxExecutor) HealthCheck(
    ctx context.Context,
    executor *cvo.ComponentVersionExecutionContext,
) (*cvo.ComponentVersionExecutionResult, error) {
    // 实现健康检查逻辑
    // ...
    return &cvo.ComponentVersionExecutionResult{
        Phase:   cvov1.ComponentHealthy,
        Message: "Component health check passed",
    }, nil
}
```

---

**文档完成日期**：2026-05-06  
**版本**：v2.0 (含 Inline ActionType 支持)
