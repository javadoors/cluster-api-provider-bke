# KEP-7: LTS 版本升级方案 - 从 25.12(LTS) 到 26.12(LTS)

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | LTS 版本升级方案 - 从 25.12(LTS) 到 26.12(LTS) |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-09 |
| **依赖** | KEP-6 声明式升级框架、KEP-10 ReleaseImage 安装组件声明式定义 |

---

## 1. 摘要

本提案设计从 25.12(LTS) 升级到 26.12(LTS) 的完整方案。由于 25.12 和 26.03 版本不支持声明式升级，需要通过预处理来实现多 Hop 升级。升级路径为：25.12(LTS) -> 26.03 -> 26.06 -> 26.09 -> 26.12(LTS)，其中 26.06 及之后版本支持声明式升级。

---

## 2. 动机

### 2.1 版本兼容性问题

| 版本 | 升级方式 | 兼容性 |
|------|---------|--------|
| 25.12(LTS) | PhaseFlow | 管理集群存在不兼容变更 |
| 26.03 | PhaseFlow | 管理集群存在不兼容变更 |
| 26.06 | 声明式升级 | 支持多 Hop 升级 |
| 26.09 | 声明式升级 | 支持多 Hop 升级 |
| 26.12(LTS) | 声明式升级 | 支持多 Hop 升级 |

### 2.2 升级约束

1. **25.12(LTS)->26.03**: 管理集群存在不兼容变更，需要预处理
2. **26.03->26.06**: 管理集群存在不兼容变更，需要预处理
3. **26.06 及之后**: 支持声明式升级方案
4. **ReleaseImage 构建**: 26.03 需要基于声明式升级方案构建 ReleaseImage
5. **管理集群升级**: 25.12(LTS) 管理集群需要先升级以支持 26.06 的声明式升级方案
6. **26.06->26.12(LTS)**: 支持通过多 Hop 声明式升级方案直接升级（26.06 -> 26.09 -> 26.12）
7. **bkeadm 一键升级**: 在 bkeadm 中增加命令支持一键式从 25.12(LTS) 升级到 26.12(LTS)

---

## 3. 设计方案

### 3.1 整体升级路径

```
25.12(LTS) --[预处理]--> 26.03 --[预处理]--> 26.06 --> 26.09 --> 26.12(LTS)
   |                      |                      |          |          |
   |                      |                      |          |          |
PhaseFlow            PhaseFlow              声明式升级    声明式升级   声明式升级
(不兼容变更)         (不兼容变更)         (多Hop支持)   (多Hop支持)  (多Hop支持)
```

### 3.2 升级阶段划分

#### 阶段 1: 25.12(LTS) -> 26.03 (预处理)

**目标**: 解决管理集群不兼容变更

**预处理内容**:
1. **CRD 迁移**: 处理 CRD 版本升级
2. **API 兼容性**: 处理 API 字段变更
3. **数据迁移**: 处理数据存储格式变更
4. **配置迁移**: 处理配置格式变更

**升级方式**: PhaseFlow (传统方式)

#### 阶段 2: 26.03 -> 26.06 (预处理)

**目标**: 解决管理集群不兼容变更，为声明式升级做准备

**预处理内容**:
1. **声明式升级框架引入**: 引入 DAG 调度器
2. **ReleaseImage 构建**: 构建支持声明式升级的 ReleaseImage
3. **ExecutorRegistry 扩展**: 扩展执行器注册表

**升级方式**: PhaseFlow (传统方式)

#### 阶段 3: 26.06 -> 26.09 (声明式升级)

**目标**: 使用声明式升级方案

**升级方式**: 声明式升级 (DAG)

**特点**:
- 支持多 Hop 升级
- 支持并行执行

#### 阶段 4: 26.09 -> 26.12(LTS) (声明式升级)

**目标**: 使用声明式升级方案升级到 LTS 版本

**升级方式**: 声明式升级 (DAG)

### 3.3 多 Hop 升级流程

#### 3.3.1 升级路径定义 (UpgradePath CR)

```yaml
# UpgradePath CR 定义升级路径
apiVersion: config.openfuyao.cn/v1alpha1
kind: UpgradePath
metadata:
  name: lts-upgrade-path
spec:
  versions:
    - version: "25.12"
      lts: true
    - version: "26.03"
    - version: "26.06"
    - version: "26.09"
    - version: "26.12"
      lts: true
  
  paths:
    - from: "25.12"
      to: "26.03"
      handler: "Hop2512To2603"          # 处理器名称
      preprocessing: true
      preprocessingSteps:
        - name: CRD 迁移
        - name: API 兼容性处理
        - name: 数据迁移
        - name: 配置迁移
    - from: "26.03"
      to: "26.06"
      handler: "Hop2603To2606"          # 处理器名称
      preprocessing: true
      preprocessingSteps:
        - name: 声明式升级框架引入
        - name: ExecutorRegistry 扩展
    - from: "26.06"
      to: "26.09"
      handler: "Hop2606To2609"          # 处理器名称
      preprocessing: false
    - from: "26.09"
      to: "26.12"
      handler: "Hop2609To2612"          # 处理器名称
      preprocessing: false
```

**设计要点**:
- 每个 path 定义一个 Hop (从 from 到 to)
- handler 字段指定该 Hop 使用的处理器名称
- 处理器在代码中注册，通过 handler 名称动态加载
- preprocessing 和 preprocessingSteps 定义预处理步骤

#### 3.3.2 升级流程

```txt
Step 1: 25.12(LTS) -> 26.03 (预处理)
  1. 执行预处理步骤:
     - CRD 迁移
     - API 兼容性处理
     - 数据迁移
     - 配置迁移
  2. 使用 PhaseFlow 升级到 26.03
  3. 后处理: 验证升级结果

Step 2: 26.03 -> 26.06 (预处理)
  1. 检查 26.03 ReleaseImage
     - 检查是否存在 26.03 的 ReleaseImage
     - 如果存在，可以使用声明式升级
     - 如果不存在，使用 PhaseFlow 升级
  2. 执行预处理步骤:
     - 声明式升级框架引入
     - ExecutorRegistry 扩展
  3. 升级:
     - 如果存在 26.03 ReleaseImage: 使用声明式升级
     - 如果不存在: 使用 PhaseFlow 升级
  4. 后处理: 验证升级结果

Step 3: 26.06 -> 26.12(LTS) (多 Hop 声明式升级)
  1. 使用声明式升级方案
  2. 自动执行多 Hop 升级: 26.06 -> 26.09 -> 26.12(LTS)
  3. 后处理: 验证升级结果
```

### 3.4 多 Hop 声明式升级方案 (26.06 -> 26.12)

#### 3.4.1 升级路径

```
26.06 --> 26.09 --> 26.12(LTS)
  |          |          |
  |          |          |
声明式升级  声明式升级  声明式升级
```

#### 3.4.2 升级流程

```
Step 1: 26.06 -> 26.09 (声明式升级)
  1. 使用声明式升级方案
  2. 后处理: 验证升级结果

Step 2: 26.09 -> 26.12(LTS) (声明式升级)
  1. 使用声明式升级方案
  2. 后处理: 验证升级结果
```

#### 3.4.3 升级特点

- **自动化**: 自动执行多个 Hop 升级，无需手动干预
- **并行执行**: 支持并行执行升级任务

### 3.5 bkeadm 一键升级命令

#### 3.5.1 命令设计

```bash
# 一键升级命令
bkeadm upgrade lts --from 25.12 --to 26.12

# 参数说明:
# --from: 源版本 (默认: 当前版本)
# --to: 目标版本 (默认: 最新 LTS 版本)
# --skip-preprocessing: 跳过预处理步骤 (默认: false)
# --dry-run: 模拟升级，不实际执行 (默认: false)
```

#### 3.5.2 命令执行流程

**设计思路**

BKE CVO 采用声明式升级路径管理，核心设计原则：

1. **升级路径动态获取**: Hop 信息从 UpgradePath CR 中动态获取，而非硬编码在代码中
2. **处理器注册机制**: 每个版本跳转 (如 25.12 -> 26.03) 对应一个处理器，通过注册表动态加载
3. **三阶段执行模型**: 每个 Hop 执行分为三个阶段：预处理 (PreProcess) -> 升级 (Upgrade) -> 后处理 (PostProcess)

**执行流程**

```
bkeadm upgrade lts --from 25.12 --to 26.12
  |
  +-> Step 1: 校验输入版本
  |     - 从 UpgradePath CR 中获取升级路径定义
  |     - 校验输入版本:
  |       * 验证 from 版本和 to 版本都是 LTS 版本 (lts: true)
  |       * 验证 from 版本和 to 版本在 UpgradePath 中存在
  |       * 验证 from 版本到 to 版本存在有效的升级路径
  |     - 如果校验失败，返回错误
  |
  +-> Step 2: 获取升级路径
  |     - 从 UpgradePath CR 中获取升级路径定义
  |     - 解析路径中的每个 Hop (from -> to)
  |     - 为每个 Hop 加载对应的处理器 (Handler)
  |
  +-> Step 3: 逐 Hop 执行升级
  |     |
  |     +-> Hop 1: 25.12 -> 26.03
  |     |     ├── PreProcess: CRD 迁移, API 兼容性, 数据迁移, 配置迁移
  |     |     ├── Upgrade:    PhaseFlow 升级
  |     |     └── PostProcess: 验证集群状态, 验证 CRD, 验证 API
  |     |
  |     +-> Hop 2: 26.03 -> 26.06
  |     |     ├── PreProcess: 声明式升级框架引入, ExecutorRegistry 扩展
  |     |     ├── Upgrade:    PhaseFlow 升级
  |     |     └── PostProcess: 验证集群状态, 验证声明式框架就绪
  |     |
  |     +-> Hop 3: 26.06 -> 26.09
  |     |     ├── PreProcess: (无)
  |     |     ├── Upgrade:    声明式升级
  |     |     └── PostProcess: 验证集群状态
  |     |
  |     +-> Hop 4: 26.09 -> 26.12
  |           ├── PreProcess: (无)
  |           ├── Upgrade:    声明式升级
  |           └── PostProcess: 验证集群状态, 验证 LTS 版本
  |
  +-> Step 4: 最终验证
        - 验证版本为 26.12(LTS)
        - 验证集群状态正常
        - 验证所有组件健康
```

**版本校验逻辑说明**:

Step 1 的版本校验确保：
1. **LTS 版本验证**: from 和 to 版本都必须是 LTS 版本 (lts: true)
2. **版本存在性验证**: from 和 to 版本都必须在 UpgradePath CR 的 versions 列表中存在
3. **路径有效性验证**: 从 from 版本到 to 版本必须存在有效的升级路径 (通过 paths 可以遍历)

如果任一验证失败，命令将返回错误并终止执行。

#### 3.5.3 升级版本处理器框架

```go
// pkg/upgrade/hop/handler.go

package hop

// Handler 定义单个 Hop 升级的处理器接口
// 每个版本跳转 (如 25.12 -> 26.03) 注册一个 Handler
type Handler interface {
    // FromVersion 源版本
    FromVersion() string
    // ToVersion 目标版本
    ToVersion() string
    // PreProcess 升级前预处理 (CRD 迁移, API 兼容性处理等)
    PreProcess(ctx context.Context) error
    // Upgrade 执行升级
    Upgrade(ctx context.Context) error
    // PostProcess 升级后处理 (验证集群状态, 验证组件健康等)
    PostProcess(ctx context.Context) error
}

// Registry 升级处理器注册表
type Registry struct {
    handlers map[string]Handler // key: "fromVersion->toVersion"
}

func NewRegistry() *Registry {
    return &Registry{
        handlers: make(map[string]Handler),
    }
}

// Register 注册一个 Hop 处理器
func (r *Registry) Register(h Handler) {
    key := fmt.Sprintf("%s->%s", h.FromVersion(), h.ToVersion())
    r.handlers[key] = h
}

// GetHandler 获取指定版本跳转的处理器
func (r *Registry) GetHandler(from, to string) (Handler, bool) {
    key := fmt.Sprintf("%s->%s", from, to)
    h, ok := r.handlers[key]
    return h, ok
}

// GetUpgradePath 获取从 from 到 to 的完整升级路径
func (r *Registry) GetUpgradePath(from, to string) ([]Handler, error) {
    var path []Handler
    current := from
    for current != to {
        // 查找从 current 出发的下一个 hop
        next, err := r.findNextHop(current, to)
        if err != nil {
            return nil, fmt.Errorf("无法找到从 %s 到 %s 的升级路径: %w", current, to, err)
        }
        handler, ok := r.GetHandler(current, next)
        if !ok {
            return nil, fmt.Errorf("未注册处理器: %s -> %s", current, next)
        }
        path = append(path, handler)
        current = next
    }
    return path, nil
}

// findNextHop 查找从 current 到 target 的下一个 hop
func (r *Registry) findNextHop(current, target string) (string, error) {
    for key := range r.handlers {
        parts := strings.Split(key, "->")
        if parts[0] == current {
            return parts[1], nil
        }
    }
    return "", fmt.Errorf("no next hop from %s", current)
}
```

#### 3.5.4 具体 Hop 处理器实现示例

```go
// pkg/upgrade/hop/handlers/v25_12_to_v26_03.go

package handlers

// Hop2512To2603 25.12(LTS) -> 26.03 升级处理器
type Hop2512To2603 struct{}

func (h *Hop2512To2603) FromVersion() string { return "25.12" }
func (h *Hop2512To2603) ToVersion() string   { return "26.03" }

func (h *Hop2512To2603) PreProcess(ctx context.Context) error {
    // 1. CRD 迁移
    if err := migrateCRDs(ctx); err != nil {
        return fmt.Errorf("CRD 迁移失败: %w", err)
    }
    // 2. API 兼容性处理
    if err := migrateAPICompatibility(ctx); err != nil {
        return fmt.Errorf("API 兼容性处理失败: %w", err)
    }
    // 3. 数据迁移
    if err := migrateData(ctx); err != nil {
        return fmt.Errorf("数据迁移失败: %w", err)
    }
    // 4. 配置迁移
    if err := migrateConfig(ctx); err != nil {
        return fmt.Errorf("配置迁移失败: %w", err)
    }
    return nil
}

func (h *Hop2512To2603) Upgrade(ctx context.Context) error {
    // 使用 PhaseFlow 升级到 26.03
    return executePhaseFlowUpgrade(ctx, "26.03")
}

func (h *Hop2512To2603) PostProcess(ctx context.Context) error {
    // 1. 验证集群状态
    if err := verifyClusterStatus(ctx, "26.03"); err != nil {
        return fmt.Errorf("集群状态验证失败: %w", err)
    }
    // 2. 验证 CRD 迁移结果
    if err := verifyCRDMigration(ctx); err != nil {
        return fmt.Errorf("CRD 迁移验证失败: %w", err)
    }
    // 3. 验证 API 兼容性
    if err := verifyAPICompatibility(ctx); err != nil {
        return fmt.Errorf("API 兼容性验证失败: %w", err)
    }
    return nil
}
```

```go
// pkg/upgrade/hop/handlers/v26_03_to_v26_06.go

// Hop2603To2606 26.03 -> 26.06 升级处理器
type Hop2603To2606 struct{}

func (h *Hop2603To2606) FromVersion() string { return "26.03" }
func (h *Hop2603To2606) ToVersion() string   { return "26.06" }

func (h *Hop2603To2606) PreProcess(ctx context.Context) error {
    // 1. 部署声明式升级框架
    if err := deployDeclarativeFramework(ctx); err != nil {
        return fmt.Errorf("声明式升级框架部署失败: %w", err)
    }
    // 2. 扩展 ExecutorRegistry
    if err := extendExecutorRegistry(ctx); err != nil {
        return fmt.Errorf("ExecutorRegistry 扩展失败: %w", err)
    }
    return nil
}

func (h *Hop2603To2606) Upgrade(ctx context.Context) error {
    // 使用 PhaseFlow 升级到 26.06
    return executePhaseFlowUpgrade(ctx, "26.06")
}

func (h *Hop2603To2606) PostProcess(ctx context.Context) error {
    // 1. 验证集群状态
    if err := verifyClusterStatus(ctx, "26.06"); err != nil {
        return fmt.Errorf("集群状态验证失败: %w", err)
    }
    // 2. 验证声明式升级框架就绪
    if err := verifyDeclarativeFrameworkReady(ctx); err != nil {
        return fmt.Errorf("声明式升级框架验证失败: %w", err)
    }
    return nil
}
```

```go
// pkg/upgrade/hop/handlers/v26_06_to_v26_09.go

// Hop2606To2609 26.06 -> 26.09 升级处理器 (声明式升级)
type Hop2606To2609 struct{}

func (h *Hop2606To2609) FromVersion() string { return "26.06" }
func (h *Hop2606To2609) ToVersion() string   { return "26.09" }

func (h *Hop2606To2609) PreProcess(ctx context.Context) error {
    // 无需预处理
    return nil
}

func (h *Hop2606To2609) Upgrade(ctx context.Context) error {
    // 使用声明式升级方案
    return executeDeclarativeUpgrade(ctx, "26.09")
}

func (h *Hop2606To2609) PostProcess(ctx context.Context) error {
    // 验证集群状态
    return verifyClusterStatus(ctx, "26.09")
}
```

#### 3.5.5 升级编排器

```go
// pkg/upgrade/orchestrator.go

package upgrade

import (
    "context"
    "fmt"
    "bkeadm/pkg/upgrade/hop"
    upgradev1alpha1 "bkeadm/api/upgrade/v1alpha1"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// Orchestrator 升级编排器
type Orchestrator struct {
    client   client.Client
    registry *hop.Registry
    dryRun   bool
}

func NewOrchestrator(client client.Client, registry *hop.Registry, dryRun bool) *Orchestrator {
    return &Orchestrator{
        client:   client,
        registry: registry,
        dryRun:   dryRun,
    }
}

// Execute 执行从 from 到 to 的完整升级
func (o *Orchestrator) Execute(ctx context.Context, from, to string) error {
    // 1. 从 UpgradePath CR 中获取升级路径
    upgradePath, err := o.getUpgradePath(ctx)
    if err != nil {
        return fmt.Errorf("获取升级路径失败: %w", err)
    }

    // 2. 校验输入版本
    if err := validateVersions(upgradePath, from, to); err != nil {
        return fmt.Errorf("版本校验失败: %w", err)
    }

    // 3. 解析升级路径，获取从 from 到 to 的所有 Hop
    hops, err := o.parseUpgradePath(upgradePath, from, to)
    if err != nil {
        return fmt.Errorf("解析升级路径失败: %w", err)
    }

    fmt.Printf("升级路径: ")
    for i, hop := range hops {
        if i > 0 {
            fmt.Printf(" -> ")
        }
        fmt.Printf("%s", hop.To)
    }
    fmt.Println()

    // 3. 逐 Hop 执行升级
    for i, hop := range hops {
        fmt.Printf("\n=== Hop %d/%d: %s -> %s ===\n",
            i+1, len(hops), hop.From, hop.To)

        if o.dryRun {
            fmt.Printf("[DRY-RUN] 跳过 Hop: %s -> %s\n", hop.From, hop.To)
            continue
        }

        // 3a. 获取该 Hop 的处理器
        handler, err := o.registry.GetHandler(hop.From, hop.To)
        if err != nil {
            return fmt.Errorf("获取处理器失败 (%s -> %s): %w", hop.From, hop.To, err)
        }

        // 3b. 预处理
        fmt.Printf("[PreProcess] 执行预处理...\n")
        if err := handler.PreProcess(ctx); err != nil {
            return fmt.Errorf("预处理失败 (%s -> %s): %w", hop.From, hop.To, err)
        }
        fmt.Printf("[PreProcess] 预处理完成\n")

        // 3c. 升级
        fmt.Printf("[Upgrade] 执行升级...\n")
        if err := handler.Upgrade(ctx); err != nil {
            return fmt.Errorf("升级失败 (%s -> %s): %w", hop.From, hop.To, err)
        }
        fmt.Printf("[Upgrade] 升级完成\n")

        // 3d. 后处理 (验证)
        fmt.Printf("[PostProcess] 执行后处理验证...\n")
        if err := handler.PostProcess(ctx); err != nil {
            return fmt.Errorf("后处理验证失败 (%s): %w", hop.To, err)
        }
        fmt.Printf("[PostProcess] 后处理验证通过\n")
    }

    fmt.Printf("\n升级完成: %s -> %s\n", from, to)
    return nil
}

// getUpgradePath 从集群中获取 UpgradePath CR
func (o *Orchestrator) getUpgradePath(ctx context.Context) (*upgradev1alpha1.UpgradePath, error) {
    upgradePathList := &upgradev1alpha1.UpgradePathList{}
    if err := o.client.List(ctx, upgradePathList); err != nil {
        return nil, fmt.Errorf("列出 UpgradePath 失败: %w", err)
    }
    if len(upgradePathList.Items) == 0 {
        return nil, fmt.Errorf("未找到 UpgradePath CR")
    }
    // 返回第一个 UpgradePath (通常只有一个)
    return &upgradePathList.Items[0], nil
}

// parseUpgradePath 解析升级路径，获取从 from 到 to 的所有 Hop
func (o *Orchestrator) parseUpgradePath(upgradePath *upgradev1alpha1.UpgradePath, from, to string) ([]Hop, error) {
    var hops []Hop
    current := from
    
    for current != to {
        // 在 UpgradePath 中查找从 current 出发的下一个 hop
        next, found := o.findNextHop(upgradePath, current)
        if !found {
            return nil, fmt.Errorf("未找到从 %s 到 %s 的升级路径", current, to)
        }
        hops = append(hops, Hop{From: current, To: next})
        current = next
    }
    
    return hops, nil
}

// findNextHop 在 UpgradePath 中查找从 current 出发的下一个 hop
func (o *Orchestrator) findNextHop(upgradePath *upgradev1alpha1.UpgradePath, current string) (string, bool) {
    for _, path := range upgradePath.Spec.Paths {
        if path.From == current {
            return path.To, true
        }
    }
    return "", false
}

// validateVersions 校验输入版本
// 1. 验证 from 版本和 to 版本都是 LTS 版本
// 2. 验证 from 版本和 to 版本在 UpgradePath 中存在
// 3. 验证 from 版本到 to 版本存在有效的升级路径
func validateVersions(upgradePath *upgradev1alpha1.UpgradePath, from, to string) error {
    // 1. 验证版本在 UpgradePath 中存在
    fromFound := false
    toFound := false
    fromIsLTS := false
    toIsLTS := false
    
    for _, v := range upgradePath.Spec.Versions {
        if v.Version == from {
            fromFound = true
            fromIsLTS = v.LTS
        }
        if v.Version == to {
            toFound = true
            toIsLTS = v.LTS
        }
    }
    
    if !fromFound {
        return fmt.Errorf("from 版本 %s 在 UpgradePath 中不存在", from)
    }
    if !toFound {
        return fmt.Errorf("to 版本 %s 在 UpgradePath 中不存在", to)
    }
    
    // 2. 验证都是 LTS 版本
    if !fromIsLTS {
        return fmt.Errorf("from 版本 %s 不是 LTS 版本", from)
    }
    if !toIsLTS {
        return fmt.Errorf("to 版本 %s 不是 LTS 版本", to)
    }
    
    // 3. 验证存在有效的升级路径
    if !hasValidPath(upgradePath, from, to) {
        return fmt.Errorf("从 %s 到 %s 不存在有效的升级路径", from, to)
    }
    
    return nil
}

// hasValidPath 验证从 from 到 to 是否存在有效的升级路径
func hasValidPath(upgradePath *upgradev1alpha1.UpgradePath, from, to string) bool {
    current := from
    visited := make(map[string]bool)
    
    for current != to {
        if visited[current] {
            // 检测到循环，路径无效
            return false
        }
        visited[current] = true
        
        // 在 paths 中查找从 current 出发的下一个 hop
        found := false
        for _, path := range upgradePath.Spec.Paths {
            if path.From == current {
                current = path.To
                found = true
                break
            }
        }
        
        if !found {
            // 找不到从 current 出发的路径
            return false
        }
    }
    
    return true
}

// Hop 表示一个版本跳转
type Hop struct {
    From string
    To   string
}
```

#### 3.5.6 bkeadm 命令入口

```go
// cmd/bkeadm/cmd/upgrade/lts.go

package upgrade

import (
    "context"
    "fmt"
    "github.com/spf13/cobra"
    "bkeadm/pkg/upgrade"
    "bkeadm/pkg/upgrade/hop"
    "bkeadm/pkg/upgrade/hop/handlers"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

var ltsCmd = &cobra.Command{
    Use:   "lts",
    Short: "一键升级到 LTS 版本",
    Long:  `一键升级到 LTS 版本，支持从 25.12(LTS) 升级到 26.12(LTS)。`,
    RunE:  runLTSUpgrade,
}

var (
    fromVersion       string
    toVersion         string
    skipPreprocessing bool
    dryRun            bool
)

func init() {
    ltsCmd.Flags().StringVar(&fromVersion, "from", "", "源版本 (默认: 当前版本)")
    ltsCmd.Flags().StringVar(&toVersion, "to", "26.12", "目标版本 (默认: 最新 LTS 版本)")
    ltsCmd.Flags().BoolVar(&skipPreprocessing, "skip-preprocessing", false, "跳过预处理步骤")
    ltsCmd.Flags().BoolVar(&dryRun, "dry-run", false, "模拟升级，不实际执行")
}

func runLTSUpgrade(cmd *cobra.Command, args []string) error {
    ctx := context.Background()
    
    // 1. 获取 Kubernetes 客户端
    client, err := getClient()
    if err != nil {
        return fmt.Errorf("获取客户端失败: %w", err)
    }

    // 2. 校验输入版本
    // 从 UpgradePath CR 中获取升级路径定义
    upgradePath, err := getUpgradePath(client)
    if err != nil {
        return fmt.Errorf("获取升级路径失败: %w", err)
    }
    
    // 校验 from 版本和 to 版本
    if err := validateVersions(upgradePath, fromVersion, toVersion); err != nil {
        return fmt.Errorf("版本校验失败: %w", err)
    }

    fmt.Printf("开始升级: %s -> %s\n", fromVersion, toVersion)

    // 3. 创建处理器注册表
    registry := hop.NewRegistry()
    
    // 4. 注册所有 Hop 处理器
    // 这些处理器可以在运行时从 UpgradePath CR 中动态加载
    // 或者在代码中静态注册
    registry.Register(&handlers.Hop2512To2603{})
    registry.Register(&handlers.Hop2603To2606{})
    registry.Register(&handlers.Hop2606To2609{})
    registry.Register(&handlers.Hop2609To2612{})

    // 5. 创建编排器并执行升级
    orchestrator := upgrade.NewOrchestrator(client, registry, dryRun)
    if err := orchestrator.Execute(ctx, fromVersion, toVersion); err != nil {
        return fmt.Errorf("升级失败: %w", err)
    }

    fmt.Printf("升级成功: %s -> %s\n", fromVersion, toVersion)
    return nil
}

// getUpgradePath 从集群中获取 UpgradePath CR
func getUpgradePath(client client.Client) (*upgradev1alpha1.UpgradePath, error) {
    upgradePathList := &upgradev1alpha1.UpgradePathList{}
    if err := client.List(ctx, upgradePathList); err != nil {
        return nil, fmt.Errorf("列出 UpgradePath 失败: %w", err)
    }
    if len(upgradePathList.Items) == 0 {
        return nil, fmt.Errorf("未找到 UpgradePath CR")
    }
    // 返回第一个 UpgradePath (通常只有一个)
    return &upgradePathList.Items[0], nil
}

// validateVersions 校验输入版本
// 1. 验证 from 版本和 to 版本都是 LTS 版本
// 2. 验证 from 版本和 to 版本在 UpgradePath 中存在
// 3. 验证 from 版本到 to 版本存在有效的升级路径
func validateVersions(upgradePath *upgradev1alpha1.UpgradePath, from, to string) error {
    // 1. 验证版本在 UpgradePath 中存在
    fromFound := false
    toFound := false
    fromIsLTS := false
    toIsLTS := false
    
    for _, v := range upgradePath.Spec.Versions {
        if v.Version == from {
            fromFound = true
            fromIsLTS = v.LTS
        }
        if v.Version == to {
            toFound = true
            toIsLTS = v.LTS
        }
    }
    
    if !fromFound {
        return fmt.Errorf("from 版本 %s 在 UpgradePath 中不存在", from)
    }
    if !toFound {
        return fmt.Errorf("to 版本 %s 在 UpgradePath 中不存在", to)
    }
    
    // 2. 验证都是 LTS 版本
    if !fromIsLTS {
        return fmt.Errorf("from 版本 %s 不是 LTS 版本", from)
    }
    if !toIsLTS {
        return fmt.Errorf("to 版本 %s 不是 LTS 版本", to)
    }
    
    // 3. 验证存在有效的升级路径
    if !hasValidPath(upgradePath, from, to) {
        return fmt.Errorf("从 %s 到 %s 不存在有效的升级路径", from, to)
    }
    
    return nil
}

// hasValidPath 验证从 from 到 to 是否存在有效的升级路径
func hasValidPath(upgradePath *upgradev1alpha1.UpgradePath, from, to string) bool {
    current := from
    visited := make(map[string]bool)
    
    for current != to {
        if visited[current] {
            // 检测到循环，路径无效
            return false
        }
        visited[current] = true
        
        // 在 paths 中查找从 current 出发的下一个 hop
        found := false
        for _, path := range upgradePath.Spec.Paths {
            if path.From == current {
                current = path.To
                found = true
                break
            }
        }
        
        if !found {
            // 找不到从 current 出发的路径
            return false
        }
    }
    
    return true
}

// getClient 获取 Kubernetes 客户端
func getClient() (client.Client, error) {
    // 实现获取 Kubernetes 客户端的逻辑
    // ...
    return nil, nil
}
```

### 3.6 管理集群升级策略

#### 3.6.1 问题描述

25.12(LTS) 管理集群需要先升级以支持 26.06 的声明式升级方案。这意味着：
1. 管理集群需要先升级到 26.03
2. 然后升级到 26.06（支持声明式升级）
3. 最后才能使用声明式升级方案升级工作负载集群

#### 3.6.2 升级顺序

```
管理集群升级:
  25.12(LTS) --[预处理]--> 26.03 --[预处理]--> 26.06
  
工作负载集群升级:
  25.12(LTS) --[预处理]--> 26.03 --[预处理]--> 26.06 --> 26.09 --> 26.12(LTS)
```

#### 3.6.3 升级策略

1. **阶段 1**: 升级管理集群到 26.06
   - 25.12(LTS) -> 26.03 (预处理)
   - 26.03 -> 26.06 (预处理)

2. **阶段 2**: 升级工作负载集群
   - 25.12(LTS) -> 26.03 (预处理)
   - 26.03 -> 26.06 (预处理)
   - 26.06 -> 26.09 (声明式升级)
   - 26.09 -> 26.12(LTS) (声明式升级)

### 3.7 预处理步骤详解

#### 3.7.1 25.12(LTS) -> 26.03 预处理

**CRD 迁移**:
```bash
# 备份现有 CRD
kubectl get crd -o yaml > crd-backup-25.12.yaml

# 升级 CRD
kubectl apply -f crd-update-26.03.yaml

# 验证 CRD 升级
kubectl get crd -o yaml > crd-verify-26.03.yaml
```

**API 兼容性处理**:
```bash
# 检查 API 兼容性
kubectl api-resources --api-group=config.openfuyao.cn

# 应用 API 迁移脚本
kubectl apply -f api-migration-26.03.yaml
```

**数据迁移**:
```bash
# 备份数据
kubectl get bkecluster -o yaml > bkecluster-backup-25.12.yaml

# 应用数据迁移脚本
kubectl apply -f data-migration-26.03.yaml

# 验证数据迁移
kubectl get bkecluster -o yaml > bkecluster-verify-26.03.yaml
```

**配置迁移**:
```bash
# 备份配置
kubectl get configmap -n bke-system -o yaml > config-backup-25.12.yaml

# 应用配置迁移脚本
kubectl apply -f config-migration-26.03.yaml

# 验证配置迁移
kubectl get configmap -n bke-system -o yaml > config-verify-26.03.yaml
```

#### 3.7.2 26.03 -> 26.06 预处理

**声明式升级框架引入**:
```bash
# 部署 DAG 调度器
kubectl apply -f dag-scheduler-26.06.yaml

# 部署执行器注册表
kubectl apply -f executor-registry-26.06.yaml

# 验证框架部署
kubectl get deployment -n bke-system | grep dag-scheduler
```

**ReleaseImage 构建**:
```bash
# 构建 26.06 ReleaseImage
opm render openfuyao-v26.06 > release-image-26.06.yaml

# 验证 ReleaseImage
kubectl apply -f release-image-26.06.yaml
kubectl get releaseimage openfuyao-v26.06
```

**ExecutorRegistry 扩展**:
```bash
# 扩展执行器注册表
kubectl apply -f executor-extension-26.06.yaml

# 验证执行器注册
kubectl get executorregistry -n bke-system
```

---

## 4. 升级执行计划

### 4.1 升级时间表

| 阶段 | 版本 | 升级方式 | 预处理 | 预计时间 |
|------|------|---------|--------|---------|
| 1 | 25.12(LTS) -> 26.03 | PhaseFlow | 是 | 2 周 |
| 2 | 26.03 -> 26.06 | PhaseFlow | 是 | 2 周 |
| 3 | 26.06 -> 26.12(LTS) | 多 Hop 声明式升级 | 否 | 2 周 |

### 4.2 升级检查清单

#### 4.2.1 阶段 1: 25.12(LTS) -> 26.03

- [ ] 备份管理集群数据
- [ ] 执行 CRD 迁移
- [ ] 执行 API 兼容性处理
- [ ] 执行数据迁移
- [ ] 执行配置迁移
- [ ] 验证管理集群升级
- [ ] 备份工作负载集群数据
- [ ] 执行工作负载集群升级
- [ ] 验证工作负载集群升级

#### 4.2.2 阶段 2: 26.03 -> 26.06

- [ ] 部署声明式升级框架
- [ ] 构建 26.06 ReleaseImage
- [ ] 扩展 ExecutorRegistry
- [ ] 验证框架部署
- [ ] 升级管理集群到 26.06
- [ ] 验证管理集群升级
- [ ] 升级工作负载集群到 26.06
- [ ] 验证工作负载集群升级

#### 4.2.3 阶段 3: 26.06 -> 26.12(LTS)

- [ ] 使用多 Hop 声明式升级方案升级管理集群 (26.06 -> 26.09 -> 26.12)
- [ ] 验证管理集群升级
- [ ] 使用多 Hop 声明式升级方案升级工作负载集群
- [ ] 验证工作负载集群升级

---

## 5. 风险与缓解

### 5.1 风险评估

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **预处理失败** | 升级失败 | 中 | 详细测试预处理步骤 |
| **数据丢失** | 业务中断 | 低 | 完整备份，测试恢复流程 |
| **兼容性问题** | 升级失败 | 中 | 充分测试，准备兼容性补丁 |
| **性能问题** | 性能下降 | 低 | 性能测试，准备优化方案 |
| **时间延误** | 项目延期 | 中 | 预留缓冲时间，准备应急预案 |

### 5.2 缓解措施

1. **备份策略**: 在每个阶段前完整备份集群数据
2. **测试策略**: 在测试环境充分验证每个升级步骤
3. **监控策略**: 升级过程中持续监控集群状态
5. **应急预案**: 准备应急预案，处理意外情况

---

## 6. 测试策略

### 6.1 测试环境

| 环境 | 用途 | 配置 |
|------|------|------|
| 开发环境 | 开发测试 | 单节点集群 |
| 测试环境 | 集成测试 | 3 节点集群 |
| 预生产环境 | 预生产测试 | 生产环境配置 |
| 生产环境 | 生产环境 | 生产环境配置 |

### 6.2 测试用例

#### 6.2.1 预处理测试

| 测试用例 | 描述 | 预期结果 |
|---------|------|---------|
| CRD 迁移测试 | 测试 CRD 迁移脚本 | CRD 成功迁移 |
| API 兼容性测试 | 测试 API 兼容性处理 | API 兼容性问题解决 |
| 数据迁移测试 | 测试数据迁移脚本 | 数据成功迁移 |
| 配置迁移测试 | 测试配置迁移脚本 | 配置成功迁移 |

#### 6.2.2 升级测试

| 测试用例 | 描述 | 预期结果 |
|---------|------|---------|
| 25.12 -> 26.03 升级 | 测试 PhaseFlow 升级 | 升级成功 |
| 26.03 -> 26.06 升级 | 测试 PhaseFlow 升级 | 升级成功 |
| 26.06 -> 26.12 多 Hop 升级 | 测试多 Hop 声明式升级 | 升级成功 |
| bkeadm 一键升级 | 测试 bkeadm upgrade lts 命令 | 一键升级成功 |

#### 6.2.3 升级后验证测试

| 测试用例 | 描述 | 预期结果 |
|---------|------|---------|
| 25.12 -> 26.03 升级后验证 | 验证升级后集群状态 | 集群状态正常 |
| 26.03 -> 26.06 升级后验证 | 验证升级后集群状态 | 集群状态正常 |
| 26.06 -> 26.12 多 Hop 升级后验证 | 验证升级后集群状态 | 集群状态正常 |
| bkeadm 一键升级后验证 | 验证升级后集群状态 | 集群状态正常 |

---

## 7. 工作量估算

### 7.1 工作量分解

| 任务 | 工作量 | 说明 |
|------|--------|------|
| **预处理开发** | 4 周 | 开发预处理脚本和工具 |
| **多 Hop 声明式升级** | 3 周 | 实现 26.06 -> 26.12 多 Hop 升级 |
| **bkeadm 一键升级命令** | 2 周 | 实现 bkeadm upgrade lts 命令 |
| **测试** | 4 周 | 测试环境和预生产环境测试 |
| **升级执行** | 4 周 | 生产环境升级执行 |
| **文档** | 2 周 | 编写升级文档和操作手册 |
| **总计** | 19 周 | 约 4.5 个月 |

### 7.2 人力资源

| 角色 | 人数 | 职责 |
|------|------|------|
| 架构师 | 1 | 架构设计和方案评审 |
| 开发工程师 | 2 | 预处理开发和测试 |
| 测试工程师 | 2 | 测试用例设计和执行 |
| 运维工程师 | 2 | 升级执行和监控 |
| 项目经理 | 1 | 项目管理和协调 |

---

## 8. 附录

### 8.1 参考文档

- KEP-6: 声明式升级框架
- KEP-10: ReleaseImage 安装组件声明式定义
- OpenShift CVO 架构分析
- BKE CVO 架构分析
- 多 Hop 声明式升级方案
- bkeadm 一键升级命令设计

### 8.2 术语表

| 术语 | 定义 |
|------|------|
| **LTS** | Long Term Support，长期支持版本 |
| **PhaseFlow** | 传统升级方式，基于硬编码的 Phase 列表 |
| **声明式升级** | 基于 DAG 的升级方式，支持多 Hop 升级 |
| **预处理** | 升级前的准备工作，包括 CRD 迁移、API 兼容性处理等 |
| **ReleaseImage** | 发布镜像，包含组件版本和升级信息 |
| **多 Hop 升级** | 通过多个中间版本进行升级 |
| **bkeadm 一键升级** | 通过 bkeadm upgrade lts 命令实现一键式 LTS 版本升级 |

---

**文档版本**: v1.0
**维护者**: openFuyao Team
