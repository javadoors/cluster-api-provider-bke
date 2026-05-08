# KEPU-2: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级

| 字段 | 值 |
|------|-----|
| **KEPU 编号** | KEPU-2 |
| **标题** | 声明式集群版本管理：基于 ClusterVersion/ReleaseImage/UpgradePath 的版本升级方案 |
| **状态** | Provisional |
| **类型** | Enhancement (Feature) |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **依赖** | 现有 PhaseFrame 架构、bke-manifests 镜像构建流程 |

## 1. 摘要
本提案引入 `ClusterVersion`、`ReleaseImage` 与 `UpgradePath` 三个核心 CRD，借鉴 OpenShift CVO 的声明式版本管理理念，结合 `bke-manifests` 作为组件清单物理载体，实现 openFuyao 集群的版本升级。方案保持现有 `BKEClusterReconciler` 的 Phase 编排逻辑不变，通过 OCI 镜像分发版本清单与升级路径，并在现有 Phase 的 `NeedExecute()` 接口中注入版本比对逻辑，实现“有变更则升级，无变更则跳过”的幂等执行。版本声明、清单校验、路径管控与执行编排解耦，支持组件独立演进与平滑迁移。

## 2. 动机
| 问题 | 现状 | 影响 |
|------|------|------|
| **版本概念缺失** | 版本信息散落在 `BKECluster.Spec` 各字段 | 无法回答“集群当前是什么版本”，升级缺乏统一声明入口 |
| **命令式编排** | 升级逻辑硬编码在 Phase 中，依赖手动状态判断 | 无法并行、失败难定位、回滚成本高、升级路径固定 |
| **清单分散** | 组件版本与部署文件未集中管理 | 升级时难以追溯版本包含的组件及对应配置 |
| **耦合度高** | 版本管理与集群生命周期强绑定 | 新增组件或修改升级策略需侵入核心控制器 |

## 3. 目标与非目标
### 3.1 主要目标
1. 定义 `ClusterVersion`、`ReleaseImage`、`UpgradePath` CRD 及关联关系，实现 1:1 绑定与不可变清单。
2. 建立 `bke-manifests` 到 `ComponentVersion` 的逻辑映射，支持多版本组件清单解析与校验。
3. 实现版本声明控制器、清单验证控制器与升级路径管控器，职责清晰、互不耦合。
4. 增强 `BKEClusterReconciler`：监听版本变更、计算组件差异、按依赖顺序驱动 Phase。
5. 在指定 Phase 的 `NeedExecute()` 中注入版本比对逻辑，实现声明式决策与命令式执行的解耦。
6. 提供平滑迁移方案，通过 Feature Gate 实现新旧架构灰度切换与零停机过渡。

### 3.2 非目标
1. 不替换 CAPI 核心控制器（KCP/MD）的节点生命周期管理。
2. 不修改 `bke-manifests` 现有构建与分发流程。
3. 不在此阶段实现 UI/CLI 交互层或多集群版本同步。
4. 不重写现有 Phase 核心执行逻辑，仅增强触发决策层。

## 4. 提案

### 4.1 资源定义与关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，生命周期管理)                                        │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1 (OwnerReference)
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  (集群版本声明入口，记录当前版本/目标版本/升级历史)                 │
│  spec.desiredVersion ────────────────┐                           │
│  status.currentVersion               │                           │
│  status.currentReleaseImageRef ──────┼─────┐                     │
└──────────────────────────────────────┼─────┼─────────────────────┘
                                       │ 1:1 │
                                       ▼     │
┌────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                             │
│  (发布版本清单，不可变，定义该版本包含哪些组件及版本)              │
│  spec.version: v2.6.0                                          │
│  spec.imageRef: registry/openfuyao-release:v2.6.0 (OCI)        │
│  spec.components:                                              │
│    - name: etcd          ────────┐                             │
│      version: v3.5.12            │                             │
│      manifestPath: /etcd/v3.5.12 │                             │
│    - name: bkeagent-deployer ────┤                             │
│      version: v1.2.0             │                             │
│    - name: openfuyao-core ───────┤                             │
│      version: v2.6.0             │                             │
└──────────────────────────────────┼─────────────────────────────┘
                                   │ 来源于
                                   ▼
┌──────────────────────────────────────────────────────────────────┐
│                    bke-manifests (ComponentVersion)              │
│  (组件清单镜像，包含各组件多版本的部署YAML/二进制/配置)             │
│  逻辑映射：ReleaseImage.spec.components → bke-manifests 路径      │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│                       UpgradePath                                │
│  (升级路径管控，独立于 ReleaseImage，可动态更新)                   │
│  spec.imageRef: registry/openfuyao-upgradepath:latest (OCI)      │
│  paths:                                                          │
│    - from: v2.5.0, to: v2.6.0, blocked: false, preCheck: ...     │
└──────────────────────────────────────────────────────────────────┘
```

### 4.2 CRD 核心属性
```go
// ClusterVersion
type ClusterVersionSpec struct {
    DesiredVersion string `json:"desiredVersion"`
    Pause          bool   `json:"pause,omitempty"`
    AllowDowngrade bool   `json:"allowDowngrade,omitempty"`
}
type ClusterVersionStatus struct {
    CurrentVersion         string              `json:"currentVersion,omitempty"`
    CurrentReleaseImageRef string              `json:"currentReleaseImageRef,omitempty"`
    Phase                  ClusterVersionPhase `json:"phase,omitempty"`
    History                []UpgradeHistory    `json:"history,omitempty"`
    Conditions             []metav1.Condition  `json:"conditions,omitempty"`
}

// ReleaseImage
type ReleaseImageSpec struct {
    Version      string          `json:"version"`
    ImageRef     string          `json:"imageRef"` // OCI 镜像地址
    Components   []ComponentRef  `json:"components"`
}
type ComponentRef struct {
    Name         string `json:"name"`
    Version      string `json:"version"`
    Scope        string `json:"scope"`
    ManifestPath string `json:"manifestPath"`
}

// UpgradePath
type UpgradePathSpec struct {
    ImageRef string `json:"imageRef"` // OCI 镜像地址
    Paths    []PathRule `json:"paths"`
}
type PathRule struct {
    FromVersion string    `json:"fromVersion"`
    ToVersion   string    `json:"toVersion"`
    Blocked     bool      `json:"blocked,omitempty"`
    PreCheck    *HookSpec `json:"preCheck,omitempty"`
}
```

### 4.3 OCI 镜像来源设计
#### 4.3.1 ReleaseImage 镜像设计
- **镜像 Tag**：与 openFuyao 版本一致，如 `v2.6.0`。
- **结构**：
  ```
  registry/openfuyao-release:v2.6.0
  ├── config.json (包含组件清单元数据)
  └── layers/ (可选，包含 release.yaml 等配置文件)
  ```
- **解析逻辑**：`ClusterVersionReconciler` 通过 `go-containerregistry` 库拉取镜像 Config，反序列化为 `ReleaseImageSpec`，创建或更新对应的 `ReleaseImage` CR。

#### 4.3.2 UpgradePath 镜像设计
- **镜像 Tag**：固定为 `latest`，仅保留最新规则集。
- **结构**：
  ```
  registry/openfuyao-upgradepath:latest
  ├── config.json (包含升级路径规则)
  └── layers/
  ```
- **解析逻辑**：`UpgradePathReconciler` 定期或按需拉取，更新内存缓存或 `UpgradePath` CR，供升级流程查询。

### 4.4 控制器架构与职责划分
| 控制器 | 核心职责 | 协同方式 |
|--------|---------|----------|
| **ClusterVersionReconciler** | 版本声明管理、ReleaseImage OCI 拉取与解析、升级路径校验、触发 BKECluster 调谐 | 更新 BKECluster Annotation `cvo.openfuyao.cn/upgrade-ready` |
| **UpgradePathReconciler** | 路径规则验证、拦截/废弃状态维护、使用统计 | 提供 `FindUpgradePath()` 接口供 CV 控制器调用 |
| **BKEClusterReconciler** (增强) | 监听版本变更、计算组件差异、按依赖顺序驱动 Phase、协调升级生命周期 | 直接 Watch CV；调用 Phase 注入 `VersionContext` |

#### 协同机制
`ClusterVersionReconciler` 不直接执行升级，仅负责声明与校验。当 `DesiredVersion` 变更且路径/清单有效时，通过 Annotation 通知 `BKEClusterReconciler`。`BKEClusterReconciler` 捕获变更，拉取清单，计算差异后按序调用 PhaseFrame。

### 4.5 Phase 集成逻辑（复用 NeedExecute）
**不引入新接口**，直接改造现有 Phase 的 `NeedExecute(old, new)` 方法：
```go
// pkg/phaseframe/phases/ensure_provider_self_upgrade.go
func (p *EnsureProviderSelfUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 基础检查
    if !p.BasePhase.DefaultNeedExecute(old, new) { return false }

    // 2. 检查是否开启声明式升级
    if featuregate.DeclarativeUpgradeEnabled(new) {
        // 3. 获取版本上下文
        ctx := p.GetVersionContext(new)
        if ctx == nil { return false } // 上下文未就绪，等待

        // 4. 版本比对决策
        curVer := ctx.CurrentComponents["bke-controller-manager"]
        tgtVer := ctx.TargetComponents["bke-controller-manager"]
        
        // 版本未变化或目标为空，不执行
        if curVer == tgtVer || tgtVer == "" {
            return false
        }
        
        p.Log.Info("Declarative upgrade triggered: %s -> %s", curVer, tgtVer)
        return true
    }

    // 5. 兼容旧逻辑
    return p.isProviderNeedUpgrade(old, new)
}
```
各 Phase 通过统一接口获取版本上下文，实现**声明式决策 + 命令式执行**的解耦。

### 4.6 平滑升级与重构方案
| 阶段 | Feature Gate | 行为 | 风险控制 |
|------|-------------|------|----------|
| **Phase 1 (Alpha)** | `DeclarativeUpgrade=false` | CRD 注册，控制器只读监听，不干预现有流程 | 零侵入，仅日志验证 |
| **Phase 2 (Beta)** | `DeclarativeUpgrade=true` (可选) | Phase 注入版本比对逻辑，新旧路径双写验证 | 灰度集群验证，支持一键回滚 |
| **Phase 3 (GA)** | `DeclarativeUpgrade=true` (默认) | BKECluster 控制器全面接管，旧版 Spec 字段标记废弃 | 完整 E2E 覆盖，自动化回滚 |
| **Phase 4** | 不可逆 | 清理硬编码调度逻辑，Phase 全面转为声明式执行器 | 架构彻底解耦 |

## 5. 架构图与流程图

### 5.1 控制器协同架构图
```
用户/CI ──▶ ClusterVersion.Spec.DesiredVersion = "v2.6.0"
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│ ClusterVersionReconciler                                    │
│  • 拉取 ReleaseImage OCI (v2.6.0) 解析为 CR                   │
│  • 拉取 UpgradePath OCI (latest) 校验路径合法性               │
│  • 写入 BKECluster Annotation: upgrade-ready=v2.6.0         │
└──────────────────────────┬──────────────────────────────────┘
                           │ 触发调谐
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ BKEClusterReconciler (增强)                                 │
│  • 捕获 Annotation 变更                                     │
│  • 构建 VersionContext (Current vs Target Components)       │
│  • 依赖排序: Provider → Agent → Etcd → Core                 │
│  • 依次调用 Phase，注入 VersionContext                      │
└──────────────────────────┬──────────────────────────────────┘
                           │ 执行升级
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ PhaseFrame (Provider/Agent/Etcd/Component)                  │
│  • NeedExecute() 中比对版本                                  │
│  • 版本不同 → 执行原有逻辑                                   │
│  • 版本相同 → 跳过，标记 Succeeded                          │
│  • 执行结果回写 ClusterVersion.Status.History               │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 升级调谐流程图
```
Start
  │
  ├─▶ ClusterVersion.DesiredVersion 变更? ──No──▶ Return
  │
  │ Yes
  ▼
┌─────────────────────────────────────┐
│ 1. 拉取目标 ReleaseImage OCI        │
│    解析为 CR，检查 Status == Valid  │
└──────────────┬──────────────────────┘
               │ Valid
               ▼
┌─────────────────────────────────────┐
│ 2. 查找 UpgradePath (OCI latest)    │
│    Blocked? → Abort, Event          │
│    Valid  → Continue                │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ 3. 获取当前 ReleaseImage            │
│    (从 CV.Status 读取)               │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ 4. 构建 VersionContext              │
│    对比组件版本 → 生成 Delta 列表    │
└──────────────┬──────────────────────┘
               │ 有变更
               ▼
┌─────────────────────────────────────┐
│ 5. 依赖拓扑排序                      │
│    Provider → Agent → Etcd → Core   │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ 6. 循环执行 Phase                   │
│    ├─ Inject VersionContext         │
│    ├─ NeedExecute() == false? Skip  │
│    ├─ Execute()                     │
│    └─ 失败? → 标记 Failed, 终止     │
└──────────────┬──────────────────────┘
               │ 全部成功
               ▼
┌─────────────────────────────────────┐
│ 7. 更新状态                         │
│    CurrentVersion = DesiredVersion  │
│    Phase = Healthy                  │
│    记录 History                     │
└─────────────────────────────────────┘
```

## 6. 工作量评估
| 阶段 | 任务内容 | 工作量 (人天) | 说明 |
|------|---------|:-------------:|------|
| **1. CRD 与基础框架** | `ClusterVersion`/`ReleaseImage`/`UpgradePath` 定义、Webhook、DeepCopy | 6 | 包含 API 规范、验证规则、不可变约束 |
| **2. OCI 解析层** | 镜像拉取模块、Config 解析、Store 接口、缓存机制 | 6 | 兼容现有镜像目录结构 |
| **3. 控制器开发** | CV/UP 控制器核心逻辑、状态机、事件协同机制 | 10 | 独立调谐，解耦设计 |
| **4. BKECluster 增强** | 差异计算模块、依赖排序、Phase 桥接、升级历史记录 | 10 | 核心编排逻辑，保证幂等 |
| **5. Phase 适配** | 4 个 Phase 改造 `NeedExecute`，注入版本比对逻辑 | 5 | 保持原有逻辑，仅替换入口决策 |
| **6. 测试与验证** | 单元测试、集成测试、E2E 升级/回滚、Feature Gate 灰度 | 8 | 覆盖多场景与异常流 |
| **总计** | | **45 人天** | 按 2 名高级开发并行估算 |

## 7. 风险与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| OCI 镜像拉取失败或解析错误 | 升级阻塞 | 控制器增加健康检查与重试；支持本地缓存 fallback；`ReleaseImage` 提前标记 Invalid |
| Phase 执行中途失败 | 集群状态不一致 | 记录详细 `History`；支持 `AllowDowngrade` 自动回滚；提供手动跳过接口 |
| 控制器协同死锁 | 升级卡死 | 采用 Annotation 单向通知机制；设置全局超时时间强制中断；依赖图增加拓扑环检测 |
| 现有 Phase 逻辑不兼容声明式触发 | 行为异常 | 通过 Feature Gate `DeclarativeUpgrade` 灰度；保留旧版 `BKECluster.Spec` 字段过渡期 |

## 8. 测试计划
| 测试类型 | 覆盖场景 |
|----------|---------|
| **单元测试** | `CalculateComponentDiff` 逻辑、依赖排序算法、`NeedExecute` 决策、路径拦截 |
| **集成测试** | `ClusterVersion` ↔ `ReleaseImage` ↔ `BKECluster` 状态联动、Annotation 触发机制 |
| **E2E 测试** | 补丁升级、跨版本升级、单组件独立升级、失败中断与回滚、OCI 缺失降级 |
| **兼容性测试** | Feature Gate 关闭时旧流程正常运行；新旧版本混合状态平滑过渡；并发调谐幂等性 |

## 9. 验收标准
1. **声明式入口**：修改 `ClusterVersion.Spec.DesiredVersion` 可自动触发完整升级流程。
2. **清单驱动**：`ReleaseImage` 成功解析 OCI 镜像组件路径，状态正确标记 `Valid`。
3. **差异升级**：仅对版本发生变更的组件触发对应 Phase，未变更组件跳过。
4. **Phase 覆盖**：4 个指定 Phase 均被正确调度，`NeedExecute` 逻辑生效。
5. **路径管控**：`UpgradePath` 拦截/放行机制生效，前置/后置检查可配置。
6. **状态可观测**：`ClusterVersion.Status` 准确反映当前版本、升级阶段、历史记录与条件。
7. **架构解耦**：版本声明、清单校验、路径管控、升级编排四层独立，新增组件无需修改核心控制器代码。

## 10. 演进路线与毕业标准
| 阶段 | Feature Gate | 毕业标准 |
|------|-------------|----------|
| **Alpha** | `DeclarativeUpgrade=false` | CRD 注册，控制器可启动，日志验证清单解析与路径查找正确 |
| **Beta** | `DeclarativeUpgrade=true` (可选) | 接管升级流程，与旧 Phase 并行运行，结果对比验证，E2E 通过率 >95% |
| **GA** | `DeclarativeUpgrade=true` (默认) | 全量切换，移除旧版本硬编码调度逻辑，支持生产环境灰度发布 |
| **Post-GA** | 不可逆 | 清理废弃代码，Phase 全面转为声明式执行器，支持热插拔组件 |
