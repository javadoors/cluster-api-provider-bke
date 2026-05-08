# KEPU-5: 基于 ClusterVersion 与 ReleaseImage 的声明式集群版本升级方案

| 字段 | 值 |
|------|-----|
| **KEPU 编号** | KEPU-5 |
| **标题** | 基于 ClusterVersion/ReleaseImage 的声明式 openFuyao 集群版本升级 |
| **状态** | Provisional |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **依赖** | 现有 PhaseFrame 架构、bke-manifests 镜像构建流程 |

## 1. 摘要
本提案引入 `ClusterVersion` 与 `ReleaseImage` 两个核心 CRD，借鉴 OpenShift CVO（Cluster Version Operator）的声明式版本管理理念，结合 `bke-manifests` 作为组件清单源，实现 openFuyao 集群的声明式版本升级。通过版本清单比对、差异计算与 Phase 驱动执行，解耦版本编排与组件安装逻辑，支持组件独立演进、平滑升级与安全回滚。

## 2. 动机
### 2.1 现有架构痛点
| 问题 | 现状 | 影响 |
|------|------|------|
| **版本概念缺失** | 版本信息散落在 `BKECluster.Spec` 各字段 | 无法回答“集群当前是什么版本”，升级缺乏统一入口 |
| **命令式编排** | 升级逻辑硬编码在 `Ensure*Upgrade` Phase 中 | 无法并行、无法跳过、升级失败难定位、回滚成本高 |
| **组件强耦合** | 各 Phase 相互依赖，升级路径固定 | 无法独立升级单个组件，新组件接入需修改核心调度代码 |
| **清单分散** | 组件版本与部署文件未集中管理 | 升级时难以追溯版本包含的组件及对应配置 |

### 2.2 OpenShift CVO 启发
OpenShift 采用 `ClusterVersion → ReleaseImage → Release Payload` 架构，实现：
1. **声明式入口**：仅修改 `desiredUpdate` 触发全量升级流程。
2. **不可变清单**：`ReleaseImage` 定义该版本包含的所有组件及版本，创建后不可变。
3. **组件解耦**：CVO 仅负责编排，具体组件由独立 Operator 执行。

**差异化适配**：我们不使用容器镜像作为 Payload，而是复用现有 `bke-manifests` 镜像作为 `ComponentVersion` 的物理载体，通过控制器解析清单并驱动现有 Phase 执行，实现最小化重构。

## 3. 目标与非目标
### 3.1 主要目标
1. 定义 `ClusterVersion`、`ReleaseImage` CRD 及其关联关系。
2. 实现 `ClusterVersion Controller`：版本编排、差异计算、升级状态追踪与回滚管理。
3. 实现 `ReleaseImage Controller`：清单校验、组件兼容性检查、状态维护。
4. 建立 `bke-manifests` 到 `ComponentVersion` 的映射机制。
5. 重构升级流程：通过版本比对自动触发 `EnsureProviderSelfUpgrade`、`EnsureAgentUpgrade`、`EnsureComponentUpgrade`、`EnsureEtcdUpgrade`。
6. 支持组件独立演进，各控制器职责清晰、互不耦合。

### 3.2 非目标
1. 不实现多集群版本同步或跨集群升级。
2. 不替换 CAPI 核心控制器（KCP/MD）的节点生命周期管理。
3. 不修改 `bke-manifests` 现有构建与分发流程。
4. 不在此阶段实现 UI/CLI 交互层。

## 4. 范围与约束
### 4.1 范围
| 范围 | 说明 |
|------|------|
| CRD 定义与注册 | `ClusterVersion`、`ReleaseImage` API 定义 |
| 控制器实现 | 版本编排器、清单验证器 |
| 清单解析 | `bke-manifests` 挂载与 `ComponentVersion` 映射 |
| 升级驱动 | 差异计算 → Phase 触发 → 状态同步 |
| 回滚机制 | 历史版本记录、失败自动/手动回滚 |

### 4.2 约束
| 约束 | 说明 |
|------|------|
| **1:1 映射** | 单集群活跃状态下，1 个 `ClusterVersion` 对应 1 个 `ReleaseImage` |
| **清单不可变** | `ReleaseImage` 创建后 `Spec` 不可修改，确保版本一致性 |
| **向后兼容** | 必须支持从现有 `BKECluster` 平滑迁移，Feature Gate 控制开关 |
| **幂等执行** | 同一版本多次触发结果一致，支持安全重试 |
| **Phase 复用** | 升级执行层复用现有 Phase 逻辑，仅替换触发与编排方式 |

## 5. 场景
### 5.1 场景一：补丁版本升级
用户修改 `ClusterVersion.Spec.DesiredVersion = "v2.5.1"` → 控制器解析目标 `ReleaseImage` → 比对发现仅 `openfuyao-system-controller` 版本变更 → 触发 `EnsureComponentUpgrade` → 更新状态。

### 5.2 场景二：跨版本升级（含多组件变更）
`v2.4.0 → v2.6.0` → 解析清单发现 `etcd`、`bkeagent-deployer`、`core-components` 均变更 → 按依赖顺序依次触发 `EnsureProviderSelfUpgrade` → `EnsureAgentUpgrade` → `EnsureEtcdUpgrade` → `EnsureComponentUpgrade` → 全量完成。

### 5.3 场景三：升级失败与回滚
某 Phase 执行失败 → `ClusterVersion` 标记 `UpgradeFailed` → 根据策略自动或手动回退 `DesiredVersion` → 控制器重新计算差异，触发降级流程 → 恢复至上一稳定版本。

## 6. 提案

### 6.1 资源关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，1:1 对应 ClusterVersion)                             │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  (集群版本，记录当前版本/目标版本/升级历史)                         │
│  spec.releaseImageRef ──────────┐                                │
│  spec.desiredVersion            │                                │
│  status.currentReleaseImageRef ─┼─────┐                          │
└────────────────────────────────┼─────┼──────────────────────────┘
                                 │ 1:1 │
                                 ▼     │
┌────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                             │
│  (发布版本清单，不可变，定义该版本包含哪些组件及版本)              │
│                                                                │
│  spec.components:                                              │
│    - name: etcd        ────────┐                               │
│      version: v3.5.12          │                               │
│      sourcePath: /etcd/v3.5.12 │                               │
│    - name: bkeagent-deployer ──┤                               │
│      version: v1.2.0           │                               │
│    - name: openfuyao-core ─────┤                               │
│      version: v2.6.0           │                               │
└────────────────────────────────┼───────────────────────────────┘
                                 │ 来源于
                                 ▼
┌──────────────────────────────────────────────────────────────────┐
│                    bke-manifests (ComponentVersion)              │
│  (组件清单镜像，包含各组件多版本的部署YAML/二进制/配置)             │
│  /etc/bke/manifests/<version>/components/<name>/<ver>/...        │
└──────────────────────────────────────────────────────────────────┘
```

### 6.2 CRD 定义
#### 6.2.1 ClusterVersion
```go
type ClusterVersionSpec struct {
    DesiredVersion    string          `json:"desiredVersion"`
    ReleaseImageRef   string          `json:"releaseImageRef,omitempty"` // 可留空，控制器自动解析
    Pause             bool            `json:"pause,omitempty"`
    AllowDowngrade    bool            `json:"allowDowngrade,omitempty"`
}

type ClusterVersionStatus struct {
    CurrentVersion          string              `json:"currentVersion,omitempty"`
    CurrentReleaseImageRef  string              `json:"currentReleaseImageRef,omitempty"`
    Phase                   ClusterVersionPhase `json:"phase,omitempty"`
    History                 []UpgradeHistory    `json:"history,omitempty"`
    Conditions              []metav1.Condition  `json:"conditions,omitempty"`
}
```

#### 6.2.2 ReleaseImage
```go
type ReleaseImageSpec struct {
    Version     string             `json:"version"`
    Components  []ComponentRef     `json:"components"`
    ValidatedAt *metav1.Time       `json:"validatedAt,omitempty"`
}

type ComponentRef struct {
    Name       string `json:"name"`       // 组件名，如 etcd, bkeagent-deployer
    Version    string `json:"version"`    // 目标版本
    Scope      string `json:"scope"`      // Node/Cluster
    SourcePath string `json:"sourcePath"` // bke-manifests 内相对路径
}

type ReleaseImageStatus struct {
    Phase      ReleaseImagePhase `json:"phase,omitempty"`
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}
```

### 6.3 控制器架构设计
```
┌─────────────────────────────────────────────────────────────┐
│                    ClusterVersion Controller                │
│  职责：版本编排、差异计算、Phase 调度、状态/历史管理           │
│  输入：ClusterVersion, ReleaseImage, bke-manifests          │
│  输出：Phase 触发指令, ClusterVersion.Status                │
└──────────────────────────┬──────────────────────────────────┘
                           │ 依赖清单有效
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                    ReleaseImage Controller                  │
│  职责：清单校验、组件兼容性检查、bke-manifests 路径验证       │
│  输入：ReleaseImage, bke-manifests ConfigMap/Volume         │
│  输出：ReleaseImage.Status.Phase (Valid/Invalid)            │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                    Phase Execution Bridge                   │
│  职责：将声明式差异映射为现有 Phase 执行                      │
│  映射：EnsureProviderSelfUpgrade, EnsureAgentUpgrade,       │
│        EnsureComponentUpgrade, EnsureEtcdUpgrade            │
└─────────────────────────────────────────────────────────────┘
```

### 6.4 控制器核心逻辑
#### 6.4.1 ReleaseImage Controller
```go
func (r *ReleaseImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    ri := &cvoapi.ReleaseImage{}
    if err := r.Get(ctx, req.NamespacedName, ri); err != nil { return ctrl.Result{}, client.IgnoreNotFound(err) }

    // 1. 校验 bke-manifests 是否存在对应组件路径
    for _, comp := range ri.Spec.Components {
        if !r.manifestStore.Exists(comp.SourcePath) {
            setStatus(ri, cvoapi.ReleaseImageInvalid, "Component path not found")
            return r.UpdateStatus(ctx, ri)
        }
    }

    // 2. 标记为有效
    setStatus(ri, cvoapi.ReleaseImageValid, "Release manifest validated")
    return ctrl.Result{}, r.UpdateStatus(ctx, ri)
}
```

#### 6.4.2 ClusterVersion Controller (升级编排)
```go
func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvoapi.ClusterVersion{}
    r.Get(ctx, req.NamespacedName, cv)
    if cv.Spec.Pause { return ctrl.Result{}, nil }

    // 1. 获取目标 ReleaseImage
    targetRI, err := r.getReleaseImage(ctx, cv.Spec.DesiredVersion)
    if err != nil || targetRI.Status.Phase != cvoapi.ReleaseImageValid {
        return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
    }

    // 2. 获取当前 ReleaseImage (从 Status 或默认初始版本)
    currentRI, _ := r.getReleaseImage(ctx, cv.Status.CurrentVersion)

    // 3. 计算组件差异
    diff := r.calculateComponentDiff(currentRI, targetRI)
    if len(diff) == 0 {
        cv.Status.Phase = cvoapi.ClusterVersionHealthy
        return r.updateStatus(ctx, cv)
    }

    // 4. 按依赖顺序执行 Phase
    if err := r.executeUpgradePhases(ctx, cv, diff); err != nil {
        cv.Status.Phase = cvoapi.ClusterVersionUpgradeFailed
        r.recordHistory(cv, "Failed", err.Error())
        return r.updateStatus(ctx, cv)
    }

    // 5. 升级成功
    cv.Status.CurrentVersion = cv.Spec.DesiredVersion
    cv.Status.CurrentReleaseImageRef = targetRI.Name
    cv.Status.Phase = cvoapi.ClusterVersionHealthy
    r.recordHistory(cv, "Success", "")
    return r.updateStatus(ctx, cv)
}
```

### 6.5 升级流程实现细节
对应需求中的步骤 `a → d`：
```go
func (r *ClusterVersionReconciler) executeUpgradePhases(ctx context.Context, cv *cvoapi.ClusterVersion, diff []ComponentDelta) error {
    // 定义 Phase 与组件的映射关系
    phaseMap := map[string]PhaseExecutor{
        "bke-controller-manager": r.providerUpgradeExecutor,
        "bkeagent-deployer":      r.agentUpgradeExecutor,
        "openfuyao-core":         r.componentUpgradeExecutor,
        "etcd":                   r.etcdUpgradeExecutor,
    }

    // 按预定义依赖图排序
    sortedDeltas := r.sortByDependency(diff)

    for _, d := range sortedDeltas {
        if d.TargetVersion == d.CurrentVersion { continue }

        executor, ok := phaseMap[d.Name]
        if !ok { continue } // 非管控组件跳过或走通用逻辑

        // 触发对应 Phase
        if err := executor(ctx, cv, d); err != nil {
            return fmt.Errorf("phase %s failed for component %s: %w", d.Name, d.Name, err)
        }
    }
    return nil
}
```

## 7. 代码结构与目录规划
```
cluster-api-provider-bke/
├── api/
│   └── cvo/v1beta1/
│       ├── clusterversion_types.go
│       ├── releaseimage_types.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   └── cvo/
│       ├── clusterversion_controller.go   # 版本编排器
│       ├── releaseimage_controller.go     # 清单验证器
│       └── phase_bridge.go                # Phase 映射与执行桥接
├── pkg/
│   ├── manifest/
│   │   └── store.go                       # bke-manifests 解析与路径校验
│   ├── upgrade/
│   │   ├── diff.go                        # 版本差异计算
│   │   ├── dependency.go                  # 组件依赖拓扑排序
│   │   └── rollback.go                    # 回滚策略管理
│   └── phaseframe/                        # 现有 Phase 保持不变
└── config/
    └── cvo/
        ├── crds/
        ├── rbac/
        └── samples/
```

## 8. 风险评估与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| `bke-manifests` 拉取失败或路径错误 | 升级阻塞 | 控制器增加健康检查与重试；支持本地缓存 fallback |
| Phase 执行中途失败 | 集群状态不一致 | 记录详细 `History`；支持 `AllowDowngrade` 自动回滚至上一 `Valid` 版本 |
| 组件依赖死锁 | 升级卡死 | 依赖图增加拓扑环检测；设置全局超时时间强制中断 |
| 现有 Phase 逻辑不兼容声明式触发 | 行为异常 | 通过 Feature Gate `DeclarativeUpgrade` 灰度；保留旧版 `BKECluster.Spec` 字段过渡 |

## 9. 测试计划
| 测试类型 | 覆盖场景 |
|----------|---------|
| **单元测试** | `calculateComponentDiff` 逻辑、依赖排序算法、清单路径校验 |
| **集成测试** | `ClusterVersion` 与 `ReleaseImage` 状态联动、Phase 桥接执行 |
| **E2E 测试** | 补丁升级、跨版本升级、单组件升级、失败回滚、`bke-manifests` 缺失降级 |
| **兼容性测试** | Feature Gate 关闭时旧流程正常运行；新旧版本混合状态平滑过渡 |

## 10. 验收标准
1. **声明式入口**：修改 `ClusterVersion.Spec.DesiredVersion` 可自动触发完整升级流程。
2. **清单驱动**：`ReleaseImage` 成功解析 `bke-manifests` 组件路径，状态正确标记 `Valid`。
3. **差异升级**：仅对版本发生变更的组件触发对应 Phase，未变更组件跳过。
4. **Phase 覆盖**：`EnsureProviderSelfUpgrade`、`EnsureAgentUpgrade`、`EnsureComponentUpgrade`、`EnsureEtcdUpgrade` 均被正确调度。
5. **状态可观测**：`ClusterVersion.Status` 准确反映当前版本、升级阶段、历史记录与条件。
6. **架构解耦**：新增控制器不侵入现有 `PhaseFrame` 核心逻辑，组件可独立迭代。

## 11. 演进路线
| 阶段 | Feature Gate | 行为 |
|------|-------------|------|
| **Alpha** | `DeclarativeUpgrade=false` | CRD 注册，控制器可启动但不接管升级，仅记录日志 |
| **Beta** | `DeclarativeUpgrade=true` (可选) | 接管升级流程，与旧 Phase 并行运行，结果对比验证 |
| **GA** | `DeclarativeUpgrade=true` (默认) | 全量切换，移除旧版本硬编码调度逻辑 |
| **Post-GA** | 不可逆 | 清理废弃代码，Phase 全面转为声明式执行器 |
