# KEP-10: ReleaseImage 安装组件声明式定义设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-10 |
| **标题** | ReleaseImage 安装组件声明式定义与 DAG 驱动安装流程设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **依赖** | KEP-5 声明式升级框架、KEP-6 三层状态机设计、KEP-9 Static Pod 类型设计 |

---

## 1. 摘要

本提案设计 ReleaseImage 安装组件的声明式定义，使安装流程与升级流程统一为 DAG 驱动。当前 ReleaseImage 的 `spec.install.components` 仅包含 `{name, version}` 两个字段，缺少执行模式（inline/manifest/staticpod）、inline handler、依赖关系等声明式元数据；且安装流程完全由硬编码的 `DeployPhases` PhaseFlow 驱动，未消费 ReleaseImage 的安装组件列表。本提案将 `ReleaseImageInstallComponent` 与 `ReleaseImageUpgradeComponent` 统一抽象为 `ReleaseImageComponent`，新增 `inline` 字段，新增安装组件目录（`DeclarativeInstallCatalog`）和安装 DAG 构建器，使安装流程也能享受声明式组件管理的优势：依赖管理、并行执行、版本追踪、可观测性。

## 2. 动机

### 2.1 现状问题

| 问题 | 说明 | 影响 |
|------|------|------|
| **安装/升级结构不对称** | `ReleaseImageInstallComponent` 仅 `{name, version}`，`ReleaseImageUpgradeComponent` 有 `inline.handler` | 安装无法声明执行模式 |
| **安装无 DAG** | 安装完全由硬编码 `DeployPhases` 列表驱动 | 无依赖管理、无并行、无断点续传 |
| **安装组件不消费 ReleaseImage** | DeployPhases 从 `BKECluster.Spec` 读取版本，不从 `ReleaseImage.Spec.Install` 读取 | 版本管理脱节 |
| **无安装组件目录** | `DeclarativeUpgradeCatalog` 仅定义升级组件映射，无安装等价物 | 新增安装组件需修改 PhaseFlow 代码 |
| **安装与升级逻辑割裂** | 安装走 PhaseFlow，升级走 DAG，两套机制维护成本高 | 代码重复、行为不一致 |

### 2.2 安装 vs 升级能力对比

| 维度 | 安装（PhaseFlow） | 升级（DAG） |
|------|-------------------|------------|
| **编排方式** | 硬编码 Phase 列表 | ReleaseImage bundle 动态构建 DAG |
| **组件来源** | DeployPhases 构造函数 | `ReleaseImage.Spec.Upgrade.Components` |
| **执行元数据** | 无 | `UpgradeComponentSpec.{Mode, ManifestPath, InlineHandler}` |
| **ReleaseImage 字段** | `Spec.Install` 未被消费 | `Spec.Upgrade.Components` 驱动 DAG |
| **依赖管理** | 隐式 Phase 顺序 | `ComponentVersion.spec.dependencies` + 拓扑排序 |
| **版本决策** | 无（总是执行） | `VersionContext.Decide()` → Skip/Upgrade |
| **组件目录** | 无 | `DeclarativeUpgradeCatalog` |
| **并行执行** | 无（串行 Phase） | 同批次并行（MaxParallel=8） |
| **断点续传** | 无 | `DeclarativeUpgradeStatus` 追踪已完成 |
| **可观测性** | PhaseStatus | `ClusterComponentStatuses` + Events |

### 2.3 设计目标

1. **结构对称**：`ReleaseImageInstallComponent` 与 `ReleaseImageUpgradeComponent` 统一抽象为 `ReleaseImageComponent`
2. **统一 DAG**：安装流程也通过 DAG 驱动，复用 Scheduler/ExecutorRegistry/VersionContext
3. **统一目录**：`DeclarativeInstallCatalog` 与 `DeclarativeUpgradeCatalog` 结构一致
4. **渐进迁移**：通过 Feature Gate 控制，PhaseFlow 与 DAG 安装路径可并存
5. **向后兼容**：迁移期间 ReleaseImage 可同时包含旧格式和声明式格式的安装组件

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **CRD 扩展** | `ReleaseImageComponent` 新增 `inline` 字段（统一安装和升级） |
| **安装组件目录** | `DeclarativeInstallCatalog` 定义安装组件映射 |
| **安装 DAG 构建** | `BuildInstallDAG` 从 ReleaseImage bundle 构建安装 DAG |
| **安装 DAG 执行** | 复用 `Scheduler.ExecuteDAG`，新增 `DecisionInstall` |
| **Feature Gate** | `DeclarativeInstallEnabled` 控制新旧路径切换 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 旧格式 `{name, version}` 的 `ReleaseImageComponent` 必须继续支持 |
| **PhaseFlow 共存** | 迁移期间 PhaseFlow 与 DAG 安装路径可并存，通过 Feature Gate 切换 |
| **复用升级框架** | 安装 DAG 复用 Scheduler、ExecutorRegistry、InlineRunner 等已有组件 |
| **幂等性** | 安装操作必须幂等，支持 Reconcile 重入 |
| **依赖正确性** | 安装 DAG 的依赖关系必须反映实际安装顺序约束 |

---

## 4. 设计方案

### 4.1 统一 ReleaseImageComponent 结构

将 `ReleaseImageInstallComponent` 与 `ReleaseImageUpgradeComponent` 统一为 `ReleaseImageComponent`：

```go
// api/v1alpha1/releaseimage_types.go

// ReleaseImageComponent 统一安装和升级组件定义
type ReleaseImageComponent struct {
    Name    string                      `json:"name,omitempty"`
    Version string                      `json:"version,omitempty"`
    Inline  *ReleaseImageInlineSpec     `json:"inline,omitempty"`
}

// ReleaseImageInlineSpec inline 执行规格
type ReleaseImageInlineSpec struct {
    Handler string `json:"handler,omitempty"`
    Version string `json:"version,omitempty"`
}
```

### 4.2 安装组件目录

新增 `DeclarativeInstallCatalog` 定义安装组件映射：

```go
// pkg/upgrade/install_catalog.go

// DeclarativeInstallCatalog 安装组件目录
var DeclarativeInstallCatalog = []ReleaseImageComponent{
    {Name: "bkeagent", Version: "v2.7.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureBKEAgent", Version: "v1.0.0"}},
    {Name: "nodes-env", Version: "v1.0.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureNodesEnv", Version: "v1.0.0"}},
    {Name: "cluster-api-obj", Version: "v1.0.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureClusterAPIObj", Version: "v1.0.0"}},
    {Name: "certs", Version: "v1.0.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureCerts", Version: "v1.0.0"}},
    {Name: "load-balance", Version: "v1.0.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureLoadBalance", Version: "v1.0.0"}},
    {Name: "kubernetes-master", Version: "v1.29.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureMasterInit", Version: "v1.0.0"}},
    {Name: "kubernetes-worker", Version: "v1.29.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureWorkerJoin", Version: "v1.0.0"}},
    {Name: "kube-proxy", Version: "v1.29.0"},
    {Name: "coredns", Version: "v1.11.3"},
    {Name: "nodes-postprocess", Version: "v1.0.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureNodesPostProcess", Version: "v1.0.0"}},
    {Name: "agent-switch", Version: "v1.0.0", Inline: &ReleaseImageInlineSpec{Handler: "EnsureAgentSwitch", Version: "v1.0.0"}},
}
```

### 4.3 安装 DAG 构建

新增 `BuildInstallDAG` 函数从 ReleaseImage bundle 构建安装 DAG：

```go
// pkg/upgrade/build.go

// BuildInstallDAG 构建安装 DAG
func BuildInstallDAG(bundle *releasemanifest.Bundle) (*topology.UpgradeDAG, error) {
    // 1. 从 bundle 获取安装组件
    components := getInstallComponents(bundle)
    
    // 2. 构建 DAG 节点
    nodes := make([]*topology.ComponentNode, 0, len(components))
    for _, comp := range components {
        node := &topology.ComponentNode{
            Name:    comp.Name,
            Version: comp.Version,
            Inline:  comp.Inline,
        }
        nodes = append(nodes, node)
    }
    
    // 3. 构建依赖关系
    graph := topology.NewGraph()
    for _, node := range nodes {
        graph.AddNode(node)
    }
    
    // 4. 添加依赖边
    addInstallDependencies(graph, bundle)
    
    // 5. 返回 DAG
    return topology.NewUpgradeDAG(graph), nil
}
```

### 4.4 安装 DAG 执行

复用 `Scheduler.ExecuteDAG`，新增 `DecisionInstall` 决策：

```go
// pkg/upgrade/context.go

// Decision 升级决策
type Decision string

const (
    DecisionSkip    Decision = "Skip"
    DecisionUpgrade Decision = "Upgrade"
    DecisionInstall Decision = "Install"  // 新增
)

// Decide 决策函数
func Decide(vc *VersionContext, name string) Decision {
    current := vc.GetCurrent(name)
    target := vc.GetTarget(name)
    
    // 新增：如果当前版本为空，则为安装
    if current == "" && target != "" {
        return DecisionInstall
    }
    
    // 如果版本相同，则跳过
    if current == target {
        return DecisionSkip
    }
    
    // 否则为升级
    return DecisionUpgrade
}
```

## 5. 迁移策略

### 5.1 Feature Gate 控制

通过 `DeclarativeInstallEnabled` Feature Gate 控制新旧路径切换：

```go
// pkg/featuregate/features.go

var (
    // DeclarativeInstallEnabled 控制 DAG 安装路径是否启用
    // false: 使用 PhaseFlow 安装（默认）
    // true: 使用 DAG 安装
    DeclarativeInstallEnabled = featuregate.NewFeature()
)
```

### 5.2 迁移阶段

| 阶段 | 目标 | 说明 | Feature Gate |
|------|------|------|-------------|
| **Phase 1** | 结构扩展 | 统一抽象 `ReleaseImageComponent`，新增 `inline` 字段 | 不启用 |
| **Phase 2** | 安装 DAG 实现 | 实现 `BuildInstallDAG`、`executeInstallDAG`、`DeclarativeInstallCatalog` | 灰度启用 |
| **Phase 3** | 验证与切换 | 测试环境验证，逐步切换到 DAG 安装路径 | 正式启用 |
| **Phase 4** | 移除 PhaseFlow | 移除 DeployPhases 硬编码列表，安装完全 DAG 驱动 | 移除 Feature Gate |

### 5.3 向后兼容

迁移期间 ReleaseImage 可同时包含旧格式和声明式格式的安装组件：

```yaml
# ReleaseImage 向后兼容示例
spec:
  install:
    components:
      # 旧格式（继续支持）
      - name: bkeagent
        version: v2.7.0
      
      # 声明式格式（新增）
      - name: nodes-env
        version: v1.0.0
        inline:
          handler: EnsureNodesEnv
          version: v1.0.0
```

### 5.4 安装路径选择

根据 Feature Gate 和组件格式选择安装路径：

```go
// pkg/upgrade/install_path.go

// SelectInstallPath 选择安装路径
func SelectInstallPath(component ReleaseImageComponent) InstallPath {
    // 如果 Feature Gate 启用且组件有 inline 字段，使用 DAG 路径
    if featuregate.DeclarativeInstallEnabled.Enabled() && component.Inline != nil {
        return InstallPathDAG
    }
    
    // 否则使用 PhaseFlow 路径
    return InstallPathPhaseFlow
}
```

## 6. 测试策略

### 6.1 单元测试

| 测试项 | 说明 |
|--------|------|
| `BuildInstallDAG` | 验证从 bundle 构建安装 DAG 的正确性 |
| `Decide` | 验证 `DecisionInstall` 决策的正确性 |
| `SelectInstallPath` | 验证安装路径选择的正确性 |
| `DeclarativeInstallCatalog` | 验证安装组件目录的完整性 |

### 6.2 集成测试

| 测试场景 | 说明 |
|---------|------|
| Feature Gate 关闭 | 验证使用 PhaseFlow 安装路径 |
| Feature Gate 开启 | 验证使用 DAG 安装路径 |
| 向后兼容 | 验证旧格式 ReleaseImage 继续工作 |
| 混合格式 | 验证旧格式和声明式格式混合使用 |

### 6.3 E2E 测试

| 测试场景 | 说明 |
|---------|------|
| 全新安装 | 验证 DAG 安装路径的完整流程 |
| 升级 | 验证 DAG 升级路径的完整流程 |
| 回滚 | 验证安装失败后的回滚机制 |

## 7. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| **DAG 安装路径不稳定** | 安装失败 | Feature Gate 控制，可快速回退到 PhaseFlow |
| **向后兼容问题** | 旧格式 ReleaseImage 无法工作 | 充分的集成测试覆盖 |
| **性能问题** | DAG 构建耗时过长 | 性能测试，优化 DAG 构建算法 |
| **依赖关系错误** | 安装顺序错误 | 依赖关系验证，E2E 测试覆盖 |

## 8. 工作量估算

| 任务 | 工作量 |
|------|--------|
| CRD 扩展 | 2 人日 |
| `BuildInstallDAG` 实现 | 3 人日 |
| `executeInstallDAG` 实现 | 3 人日 |
| `DeclarativeInstallCatalog` 定义 | 1 人日 |
| Feature Gate 实现 | 1 人日 |
| 测试 | 5 人日 |
| **总计** | **15 人日** |

---

## 附录

### A. 参考文档

- KEP-5: 声明式升级框架
- KEP-6: 三层状态机设计
- KEP-9: Static Pod 类型设计
