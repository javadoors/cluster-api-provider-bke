# 统一升级框架设计方案

## 1. 问题背景

### 1.1 场景描述

在 BKE 版本升级过程中，存在两种需要特殊处理的场景：

**场景 1：不兼容的 CRD 变更**
- 新版本引入了不兼容的 CRD 结构变更
- 需要在升级前执行迁移脚本，转换现有 CRD 数据
- 例如：字段重命名、类型变更、必填字段新增等

**场景 2：架构变更导致的自升级**
- `install-service` 和 `cluster-api-provider-bke` 存在大的架构变更
- 需要先完成这两个组件的自升级，才能继续后续升级流程
- 例如：API 接口变更、控制逻辑重构、依赖库升级等

### 1.2 核心诉求

将这两种场景**统一纳入现有升级框架**，实现：
- 一次升级到目标版本，无需额外手动步骤
- 自动化处理不兼容变更
- 保证升级过程的原子性和可回滚性

---

## 2. 现有升级框架分析

### 2.1 框架架构

BKE 采用**双模式升级架构**：

```
┌─────────────────────────────────────────────────────────┐
│              BKEClusterReconciler                       │
│         executePhaseFlow() 入口                         │
└──────────────────┬──────────────────────────────────────┘
                   │
        ┌──────────┴──────────┐
        │                     │
        ▼                     ▼
┌──────────────┐      ┌──────────────┐
│  DAG 模式    │      │ PhaseFlow    │
│ (声明式)     │      │  (传统)      │
└──────────────┘      └──────────────┘
        │                     │
        └──────────┬──────────┘
                   ▼
        ┌──────────────────────┐
        │  执行升级流程        │
        └──────────────────────┘
```

### 2.2 DAG 模式特点

**核心组件：**
- `UpgradeDAG`：有向无环图，定义组件依赖关系
- `ComponentNode`：升级组件节点，包含版本、依赖、失败策略
- `Scheduler`：拓扑排序 + 并行执行调度器

**执行流程：**
```
1. 解析 ReleaseImage Bundle
2. 构建 VersionContext（当前版本 vs 目标版本）
3. BuildDAGFromBundle() 构建依赖图
4. TopologicalBatches() 拓扑排序
5. ExecuteDAG() 按批次并行执行
```

**关键特性：**
- 支持组件间依赖声明
- 同批次无依赖组件可并行执行（最大 8 并发）
- 支持 FailFast/Continue 失败策略
- 隐式依赖：`pre-upgrade-resources` 自动成为所有组件的前置依赖

### 2.3 已有扩展点

| 机制 | 位置 | 能力 | 局限 |
|------|------|------|------|
| `EnsurePreUpgradeResources` | DAG 第一批 | 预创建 CRD/ConfigMap/Secret | 只做"创建"，不做"迁移" |
| `EnsureProviderSelfUpgrade` | PostDeployPhases | Provider 镜像升级 | 不处理 install-service |
| DAG 依赖编排 | ComponentVersion.spec.dependencies | 定义组件执行顺序 | 无"脚本执行"类型 |
| Hook 机制 | PreHook/PostHook | Phase 前后执行自定义逻辑 | 粒度是 Phase 级别 |
| 兼容性检查 | ReleaseImage 阶段 | semver 约束检查 | 只检查版本，不处理变更 |

### 2.4 缺失能力

1. **无脚本执行 Phase**：无法在升级过程中执行迁移脚本
2. **无 install-service 升级组件**：DAG 目录中没有 `install-service` 组件
3. **无不兼容变更处理**：`EnsurePreUpgradeResources` 只做资源创建，不做数据迁移

---

## 3. 设计方案

### 3.1 核心思路

利用 DAG 模式的**组件化编排能力**，将两种场景抽象为新的 DAG 组件，通过依赖关系确保执行顺序。

**目标 DAG 执行顺序：**

```
Batch 1: [pre-upgrade-resources]          ← 预创建资源（已有）
Batch 2: [pre-upgrade-migration]          ← 不兼容 CRD 迁移脚本（新增）
Batch 3: [provider, install-service]      ← 自升级（provider 已有，install-service 新增）
Batch 4: [bkeagent, etcd, ...]            ← 正常升级组件
```

### 3.2 场景 1：不兼容 CRD 变更

#### 3.2.1 新增组件：`pre-upgrade-migration`

**组件定义：**

```yaml
apiVersion: v1alpha1
kind: ComponentVersion
metadata:
  name: pre-upgrade-migration
spec:
  # 内联处理器
  inline:
    handler: EnsurePreUpgradeMigration
  # 依赖：在 pre-upgrade-resources 之后执行
  dependencies:
    - name: pre-upgrade-resources
  # 迁移脚本定义
  migration:
    # 从哪个版本开始需要执行迁移
    fromVersion: ">= 1.0.0 < 2.0.0"
    # 迁移步骤
    steps:
      - name: "migrate-crd-fields"
        # 脚本来源：ConfigMap 或内嵌
        source:
          configMap:
            name: migration-scripts
            namespace: cluster-system
            key: migrate-v2.sh
        # 执行条件
        when: "crdExists('bkeclusters.v1beta1.bke.bocloud.com')"
        # 超时
        timeout: 5m
      - name: "cleanup-deprecated-resources"
        source:
          configMap:
            name: migration-scripts
            namespace: cluster-system
            key: cleanup-v1.sh
```

#### 3.2.2 Phase 实现

**文件：** `pkg/phaseframe/phases/ensure_pre_upgrade_migration.go`

```go
const (
    EnsurePreUpgradeMigrationName = "EnsurePreUpgradeMigration"
)

type EnsurePreUpgradeMigration struct {
    phaseframe.BasePhase
    componentVersion *v1alpha1.ComponentVersion
}

func (e *EnsurePreUpgradeMigration) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    // 检查是否有迁移步骤需要执行
    return e.hasMigrationSteps()
}

func (e *EnsurePreUpgradeMigration) Execute() (ctrl.Result, error) {
    ctx, c, bkeCluster, _, log := e.Ctx.Untie()
    
    for _, step := range e.componentVersion.Spec.Migration.Steps {
        // 1. 评估执行条件
        if step.When != "" {
            shouldRun, err := e.evaluateCondition(ctx, c, step.When)
            if err != nil {
                return ctrl.Result{}, err
            }
            if !shouldRun {
                log.Info("skip migration step %q: condition not met", step.Name)
                continue
            }
        }
        
        // 2. 获取脚本内容
        script, err := e.resolveScript(ctx, c, step.Source)
        if err != nil {
            return ctrl.Result{}, err
        }
        
        // 3. 执行脚本
        log.Info("executing migration step: %s", step.Name)
        if err := e.executeMigrationScript(ctx, c, script, step.Timeout); err != nil {
            return ctrl.Result{}, fmt.Errorf("migration step %q failed: %w", step.Name, err)
        }
        
        log.Info("migration step %q completed", step.Name)
    }
    
    return ctrl.Result{}, nil
}
```

#### 3.2.3 脚本执行方式

通过 **Command CRD** 执行脚本（与 EnsureNodesEnv 一致）：

```go
func (e *EnsurePreUpgradeMigration) executeMigrationScript(
    ctx context.Context, c client.Client, script string, timeout time.Duration,
) error {
    // 创建 Command CRD
    cmd := &agentv1beta1.Command{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("migration-%s", uuid.New().String()[:8]),
            Namespace: "cluster-system",
            Labels: map[string]string{
                "bke.bocloud.com/phase": "pre-upgrade-migration",
            },
        },
        Spec: agentv1beta1.CommandSpec{
            Commands: []agentv1beta1.ExecCommand{
                {
                    ID:      "execute-migration",
                    Command: []string{"bash", "-c", script},
                    Type:    agentv1beta1.CommandBuiltIn,
                },
            },
            Timeout: int64(timeout.Seconds()),
        },
    }
    
    if err := c.Create(ctx, cmd); err != nil {
        return err
    }
    
    // 等待执行完成
    return phaseutil.WaitCommandComplete(ctx, c, cmd, timeout)
}
```

#### 3.2.4 条件评估

支持简单的条件表达式：

```go
func (e *EnsurePreUpgradeMigration) evaluateCondition(
    ctx context.Context, c client.Client, condition string,
) (bool, error) {
    // 支持的条件类型：
    // - crdExists('name.group')
    // - resourceExists('kind', 'namespace', 'name')
    // - versionCompare(current, target, operator)
    
    if strings.HasPrefix(condition, "crdExists(") {
        // 解析 CRD 名称
        crdName := extractCRDName(condition)
        return e.crdExists(ctx, c, crdName)
    }
    
    // 默认执行
    return true, nil
}
```

### 3.3 场景 2：架构变更自升级

#### 3.3.1 新增组件：`install-service`

**组件定义：**

```yaml
apiVersion: v1alpha1
kind: ComponentVersion
metadata:
  name: install-service
spec:
  version: "2.0.0"
  # 内联处理器
  inline:
    handler: EnsureInstallServiceUpgrade
  # 依赖：在 provider 之后执行（provider 先升级，才能管理 install-service）
  dependencies:
    - name: provider
  # 镜像定义
  images:
    - name: openfuyao-system-controller
      tag: ["2.0.0"]
      usedPodInfo:
        - podPrefix: openfuyao-system-controller
          namespace: openfuyao-system-controller
```

#### 3.3.2 Phase 实现

**文件：** `pkg/phaseframe/phases/ensure_install_service_upgrade.go`

```go
const (
    EnsureInstallServiceUpgradeName = "EnsureInstallServiceUpgrade"
    
    // 管理集群标识 Deployment
    managementClusterNamespace  = "cluster-system"
    managementClusterDeployment = "bke-controller-manager"
    managementClusterContainer  = "manager"
    
    // install-service Deployment
    installServiceNamespace    = "openfuyao-system-controller"
    installServiceDeployment   = "openfuyao-system-controller"
    installServiceContainer    = "manager"
    
    // 升级超时
    installServiceUpgradeTimeout = 5 * time.Minute
)

type EnsureInstallServiceUpgrade struct {
    phaseframe.BasePhase
}

func (p *EnsureInstallServiceUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // DAG 模式下，优先使用 VersionContext
    if decided, need := p.ComponentVersionDecision("install-service"); decided {
        if !need {
            return false
        }
    } else {
        // 回退到传统逻辑
        return p.isInstallServiceNeedUpgrade(old, new)
    }
    
    // 检查是否是管理集群
    if !p.isManagementCluster() {
        return false
    }
    
    p.SetStatus(bkev1beta1.PhaseWaiting)
    return true
}

func (p *EnsureInstallServiceUpgrade) isManagementCluster() bool {
    ctx, c, _, _, log := p.Ctx.Untie()
    
    target := phaseutil.DeploymentTarget{
        Namespace: managementClusterNamespace,
        Name:      managementClusterDeployment,
        Container: managementClusterContainer,
    }
    _, err := phaseutil.GetDeploymentImage(ctx, c, target)
    if err != nil {
        log.Debug("bke-controller-manager not found, skip (not management cluster)")
        return false
    }
    return true
}

func (p *EnsureInstallServiceUpgrade) Execute() (ctrl.Result, error) {
    ctx, c, bkeCluster, _, log := p.Ctx.Untie()
    
    // 1. 获取目标镜像
    targetImage, err := p.getInstallServiceTargetImage(bkeCluster)
    if err != nil || targetImage == "" {
        log.Error("failed to get target image: %v", err)
        return ctrl.Result{}, fmt.Errorf("failed to get target image: %w", err)
    }
    
    // 2. Patch Deployment 镜像
    target := phaseutil.DeploymentTarget{
        Namespace: installServiceNamespace,
        Name:      installServiceDeployment,
        Container: installServiceContainer,
    }
    
    log.Info("start patching install-service Deployment, target: %s", targetImage)
    if err := phaseutil.PatchDeploymentImage(ctx, c, target, targetImage); err != nil {
        log.Error("patch Deployment failed: %v", err)
        return ctrl.Result{}, fmt.Errorf("patch Deployment failed: %w", err)
    }
    
    // 3. 等待新 Pod 就绪
    log.Info("waiting for install-service to be ready...")
    if err := phaseutil.WaitDeploymentReady(ctx, c, target, targetImage,
        installServiceUpgradeTimeout); err != nil {
        
        // 处理 context canceled（自身被终止的场景）
        if errors.Is(err, context.Canceled) {
            checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            
            currentImage, getErr := phaseutil.GetDeploymentImage(checkCtx, c, target)
            if getErr == nil && currentImage == targetImage {
                log.Info("context canceled but image is updated, consider upgrade successful")
                return ctrl.Result{Requeue: true}, nil
            }
        }
        
        log.Error("wait for Deployment ready failed: %v", err)
        return ctrl.Result{}, fmt.Errorf("wait for Deployment ready failed: %w", err)
    }
    
    log.Info("install-service upgrade completed")
    return ctrl.Result{Requeue: true}, nil
}
```

### 3.4 统一编排：Release Bundle 定义

所有组件的依赖关系在 Release Bundle 中定义：

**文件：** `release-bundle.yaml`

```yaml
apiVersion: v1alpha1
kind: ReleaseBundle
metadata:
  name: bke-v2.0.0
spec:
  version: "2.0.0"
  components:
    # Batch 1: 预创建资源
    - name: pre-upgrade-resources
      version: "2.0.0"
      
    # Batch 2: 不兼容迁移（条件执行）
    - name: pre-upgrade-migration
      version: "2.0.0"
      dependencies:
        - name: pre-upgrade-resources
    
    # Batch 3: 自升级
    - name: provider
      version: "2.0.0"
      inline:
        handler: EnsureProviderSelfUpgrade
      dependencies:
        - name: pre-upgrade-migration
    
    - name: install-service
      version: "2.0.0"
      inline:
        handler: EnsureInstallServiceUpgrade
      dependencies:
        - name: provider
    
    # Batch 4+: 正常升级
    - name: bkeagent
      version: "2.0.0"
      inline:
        handler: EnsureAgentUpgrade
      dependencies:
        - name: provider
    
    - name: etcd
      version: "3.5.12"
      inline:
        handler: EnsureEtcdUpgrade
      dependencies:
        - name: provider
    
    - name: kubernetes-master
      version: "1.29.0"
      inline:
        handler: EnsureMasterUpgrade
      dependencies:
        - name: etcd
```

---

## 4. 执行流程

### 4.1 完整升级流程

```
用户修改 BKECluster.Spec (版本变更到 v2.0.0)
        │
        ▼
ClusterVersionReconciler 设置 upgrade-ready 注解
        │
        ▼
BKEClusterReconciler.executePhaseFlow()
        │
        ├── shouldUseDeclarativeUpgrade? → Yes
        │
        ├── executeUpgradeDAG()
        │     │
        │     ├── 解析 ReleaseImage Bundle (v2.0.0)
        │     ├── 构建 VersionContext
        │     ├── BuildDAGFromBundle()
        │     │     └── 从 ComponentVersion.spec.dependencies 构建边
        │     │     └── 隐式添加 pre-upgrade-resources 依赖
        │     │
        │     ├── TopologicalBatches():
        │     │     Batch 1: [pre-upgrade-resources]
        │     │     Batch 2: [pre-upgrade-migration]
        │     │     Batch 3: [provider, install-service]  ← install-service 依赖 provider
        │     │     Batch 4: [bkeagent, etcd]
        │     │     Batch 5: [kubernetes-master]
        │     │     Batch 6: [kubernetes-worker]
        │     │
        │     └── Scheduler.ExecuteDAG()
        │           ├── Batch 1: 预创建 CRD/ConfigMap/Secret
        │           ├── Batch 2: 执行不兼容迁移脚本
        │           ├── Batch 3: Provider 自升级 → install-service 升级
        │           ├── Batch 4-6: 正常升级流程
        │           └── 完成
        │
        └── PhaseFlow (补充执行 DAG 未覆盖的 Phase)
              └── EnsureCluster (健康检查)
```

### 4.2 DAG 依赖关系图

```
pre-upgrade-resources
        │
        ▼
pre-upgrade-migration (如果有不兼容变更)
        │
        ▼
  ┌─────┴─────┐
  │           │
  ▼           ▼
provider   install-service   ← 同批次，但 install-service 依赖 provider
  │           │
  └─────┬─────┘
        │
        ▼
  bkeagent, etcd, ...
        │
        ▼
  kubernetes-master
        │
        ▼
  kubernetes-worker
```

### 4.3 场景覆盖

| 场景 | pre-upgrade-migration | install-service | 行为 |
|------|----------------------|-----------------|------|
| 普通版本升级（无不兼容变更） | ❌ 不存在 | ✅ 存在 | 跳过迁移，执行 install-service 升级 |
| 不兼容 CRD 变更 | ✅ 存在 | ✅ 存在 | 执行迁移脚本，然后升级 |
| 业务集群（非管理集群） | ✅ 存在 | ✅ 存在 | 执行迁移，跳过 install-service |
| 管理集群但未安装 install-service | ✅ 存在 | ✅ 存在 | 执行迁移，跳过 install-service |
| 架构变更自升级 | ❌ 不存在 | ✅ 存在 | 直接执行 install-service 升级 |

---

## 5. 代码变更清单

### 5.1 新增文件

| 文件路径 | 说明 |
|---------|------|
| `pkg/phaseframe/phases/ensure_pre_upgrade_migration.go` | 不兼容迁移 Phase |
| `pkg/phaseframe/phases/ensure_install_service_upgrade.go` | install-service 升级 Phase |
| `pkg/phaseframe/phases/ensure_pre_upgrade_migration_test.go` | 单元测试 |
| `pkg/phaseframe/phases/ensure_install_service_upgrade_test.go` | 单元测试 |

### 5.2 修改文件

| 文件路径 | 修改内容 |
|---------|---------|
| `pkg/phaseframe/phases/list.go` | 注册新 Phase 到 `DeclarativeInlineUpgradePhases` |
| `pkg/upgrade/catalog.go` | 注册 `pre-upgrade-migration` 和 `install-service` 组件 |
| `pkg/componentfactory/registry.go` | 注册内联处理器 `EnsurePreUpgradeMigration` 和 `EnsureInstallServiceUpgrade` |
| `api/v1alpha1/componentversion_types.go` | 扩展 `MigrationSpec` 定义 |

### 5.3 注册代码示例

**文件：** `pkg/phaseframe/phases/list.go`

```go
var DeclarativeInlineUpgradePhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsurePreUpgradeResources,
    NewEnsurePreUpgradeMigration,      // 新增
    NewEnsureAgentUpgrade,
    NewEnsureContainerdUpgrade,
    NewEnsureEtcdUpgrade,
    NewEnsureMasterUpgrade,
    NewEnsureWorkerUpgrade,
}

var PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureProviderSelfUpgrade,
    NewEnsureInstallServiceUpgrade,    // 新增
    NewEnsureAgentUpgrade,
    NewEnsureContainerdUpgrade,
    NewEnsureEtcdUpgrade,
    NewEnsureWorkerUpgrade,
    NewEnsureMasterUpgrade,
    NewEnsureWorkerDelete,
    NewEnsureMasterDelete,
    NewEnsureComponentUpgrade,
    NewEnsureClusterAPIManagerManifest,
    NewEnsureCluster,
}
```

**文件：** `pkg/componentfactory/registry.go`

```go
func registerInlineHandler(f *ComponentFactory, handler, version string) error {
    switch handler {
    case "EnsurePreUpgradeMigration":
        f.Register(handler, version, phases.NewEnsurePreUpgradeMigration)
    case "EnsureInstallServiceUpgrade":
        f.Register(handler, version, phases.NewEnsureInstallServiceUpgrade)
    case "EnsureEtcdUpgrade":
        f.Register(handler, version, phases.NewEnsureEtcdUpgrade)
    // ... 其他处理器
    }
    return nil
}
```

---

## 6. API 扩展

### 6.1 ComponentVersion 扩展

**文件：** `api/v1alpha1/componentversion_types.go`

```go
type ComponentVersionSpec struct {
    // ... 现有字段
    
    // Migration 定义不兼容迁移脚本
    // +optional
    Migration *MigrationSpec `json:"migration,omitempty"`
}

type MigrationSpec struct {
    // FromVersion 定义从哪个版本开始需要执行迁移
    // 支持 semver 约束语法，如 ">= 1.0.0 < 2.0.0"
    FromVersion string `json:"fromVersion"`
    
    // Steps 定义迁移步骤
    Steps []MigrationStep `json:"steps"`
}

type MigrationStep struct {
    // Name 步骤名称
    Name string `json:"name"`
    
    // Source 脚本来源
    Source MigrationSource `json:"source"`
    
    // When 执行条件（可选）
    // 支持的条件：crdExists('name'), resourceExists('kind', 'ns', 'name')
    // +optional
    When string `json:"when,omitempty"`
    
    // Timeout 超时时间
    // +optional
    Timeout metav1.Duration `json:"timeout,omitempty"`
}

type MigrationSource struct {
    // ConfigMap 从 ConfigMap 加载脚本
    // +optional
    ConfigMap *ConfigMapSource `json:"configMap,omitempty"`
    
    // Inline 内嵌脚本内容
    // +optional
    Inline *string `json:"inline,omitempty"`
}

type ConfigMapSource struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
    Key       string `json:"key"`
}
```

---

## 7. 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 迁移脚本执行方式 | Command CRD | 与现有 EnsureNodesEnv 一致，复用基础设施 |
| 迁移脚本存储 | ConfigMap | 灵活，支持版本管理 |
| install-service 组件位置 | DAG 组件 | 与 provider 同批次，通过依赖确保顺序 |
| 条件执行 | `when` 表达式 | 避免不必要的迁移操作 |
| 失败策略 | FailFast | 迁移失败应立即终止升级 |
| 向后兼容 | 可选组件 | Release Bundle 中不包含则自动跳过 |

---

## 8. 向后兼容性

### 8.1 兼容性保证

- **旧版本 Release Bundle**：如果 Release Bundle 中没有 `pre-upgrade-migration` 或 `install-service` 组件，DAG 自动跳过
- **业务集群**：如果集群不是管理集群，`install-service` 组件的 `NeedExecute` 返回 false
- **传统 PhaseFlow 模式**：不受影响，新 Phase 仅通过 `DeclarativeInlineUpgradePhases` 注册

### 8.2 升级路径

```
v1.x (无迁移组件)
    │
    ▼ 升级到
v2.0 (包含迁移组件)
    │
    ├── 自动执行 pre-upgrade-migration
    ├── 自动执行 install-service 升级
    └── 继续正常升级流程
```

---

## 9. 测试计划

### 9.1 单元测试

| 测试项 | 测试内容 |
|--------|---------|
| `EnsurePreUpgradeMigration.NeedExecute` | 条件评估逻辑 |
| `EnsurePreUpgradeMigration.Execute` | 脚本执行和错误处理 |
| `EnsureInstallServiceUpgrade.NeedExecute` | 管理集群检测逻辑 |
| `EnsureInstallServiceUpgrade.Execute` | 镜像升级和等待逻辑 |

### 9.2 集成测试

| 测试场景 | 预期结果 |
|---------|---------|
| 普通版本升级（无迁移） | 跳过 `pre-upgrade-migration`，执行其他组件 |
| 不兼容 CRD 变更 | 执行迁移脚本，然后继续升级 |
| 管理集群升级 | 执行 `install-service` 升级 |
| 业务集群升级 | 跳过 `install-service` 升级 |
| 迁移脚本执行失败 | 升级终止，返回错误 |

### 9.3 端到端测试

| 测试场景 | 验证点 |
|---------|--------|
| v1.x → v2.0 完整升级 | 一次升级到目标版本，无需手动步骤 |
| 不兼容 CRD 变更场景 | CRD 数据正确迁移，升级成功 |
| 架构变更自升级场景 | install-service 正确升级，后续组件正常升级 |

---

## 10. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 迁移脚本执行失败 | 升级中断 | FailFast 策略，立即终止并返回错误 |
| 迁移脚本超时 | 升级卡住 | 设置合理的超时时间，超时后终止 |
| install-service 升级失败 | 管理面不可用 | 回滚机制，恢复到旧版本镜像 |
| 条件评估错误 | 迁移逻辑错误 | 详细的日志记录，便于排查 |
| DAG 依赖循环 | 升级无法执行 | 构建 DAG 时检测循环依赖 |

---

## 11. 总结

本设计方案通过扩展 DAG 升级框架，将不兼容 CRD 变更和架构变更自升级两种场景统一纳入现有升级流程，实现：

1. **一次升级到目标版本**：无需额外手动步骤
2. **自动化处理不兼容变更**：通过 `pre-upgrade-migration` 组件执行迁移脚本
3. **自动化架构变更自升级**：通过 `install-service` 组件完成自升级
4. **向后兼容**：旧版本 Release Bundle 自动跳过新组件
5. **可扩展性**：未来可通过添加新的 DAG 组件支持更多升级场景
