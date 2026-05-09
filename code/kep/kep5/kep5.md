# KEP-2: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-2 |
| **标题** | 声明式集群版本管理：基于 ClusterVersion/ReleaseImage/UpgradePath 的 DAG 驱动升级方案 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **依赖** | 现有 PhaseFrame 架构、bke-manifests 镜像构建流程、CAPI v1beta1 |

## 1. 摘要
本提案引入 `ClusterVersion`、`ReleaseImage` 与 `UpgradePath` 三个核心 CRD，借鉴 OpenShift CVO 声明式版本管理理念，结合 OCI 镜像分发版本清单与升级路径，实现 openFuyao 集群的版本升级。方案保持 `BKEClusterReconciler` 为升级主调度器，通过解析 `ReleaseImage` 中的组件依赖构建独立的 **安装 DAG** 与 **升级 DAG**，按拓扑顺序调用 Phase。Phase 的升级决策完全复用现有 `NeedExecute()` 接口，通过注入版本上下文比对当前与目标版本。`bke-manifests` 提供 `ComponentVersion` 清单，支持叶子/组合组件、兼容性约束、依赖拓扑、升级策略及 `inline` 代码引用。架构彻底解耦，支持组件独立演进与从旧版本的平滑迁移。

## 2. 动机
### 2.1 现状痛点
| 问题 | 现状 | 影响 |
|------|------|------|
| **版本概念缺失** | 版本信息散落在 `BKECluster.Spec` 各字段 | 无法回答“集群当前是什么版本”，升级缺乏统一声明入口 |
| **命令式编排** | Phase 执行顺序硬编码，依赖手动状态判断 | 无法并行、失败难定位、回滚成本高、升级路径固定 |
| **清单分散** | 组件版本与部署文件未集中管理 | 升级时难以追溯版本包含的组件及对应配置 |
| **兼容性盲区** | 组件间版本依赖无集中校验 | 易出现 K8s 与 Etcd/Containerd 版本不兼容导致集群不可用 |
| **耦合度高** | 版本管理与集群生命周期强绑定 | 新增组件或修改升级策略需侵入核心控制器 |

### 2.2 设计原则
1. **声明式入口**：仅修改 `ClusterVersion.Spec.DesiredVersion` 触发全量升级流程。
2. **清单不可变**：`ReleaseImage` 创建后 `Spec` 不可修改，确保版本一致性。
3. **DAG 驱动**：安装与升级顺序由组件依赖图动态计算，支持并行/串行/批量策略。
4. **接口复用**：不新增升级判断接口，完全复用现有 `NeedExecute()`。
5. **OCI 驱动**：版本清单与升级路径通过 OCI 镜像分发，支持离线缓存与版本追溯。

## 3. 目标与非目标
### 3.1 主要目标
1. 定义 `ClusterVersion`、`ReleaseImage`、`UpgradePath` CRD 及 1:1 关联关系。
2. 设计 `ComponentVersion` 数据结构，支持叶子/组合组件、`inline` 模式、兼容性/依赖约束、升级策略。
3. 实现基于 OCI 的 `ReleaseImage` 与 `UpgradePath` 动态拉取、解析与校验。
4. 在 `BKEClusterReconciler` 中实现独立的安装/升级 DAG 构建与调度引擎。
5. 改造 4 个核心 Phase 为 YAML 清单，其余 Phase 增加 `Version()` 接口供上下文比对。
6. 提供从旧版本（无新 CRD）到新版本的平滑迁移方案。

### 3.2 非目标
1. 不替换 CAPI 核心控制器（KCP/MD）的节点生命周期管理。
2. 不修改 `bke-manifests` 现有构建与分发流程。
3. 不在此阶段实现 UI/CLI 交互层或多集群版本同步。
4. 不重写现有 Phase 核心执行逻辑，仅增强触发决策层与上下文注入。

## 4. 提案设计

### 4.1 资源属性与关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，生命周期管理)                                        │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1 (OwnerReference)
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  spec.desiredVersion: v2.6.0                                     │
│  status.currentVersion: v2.5.0                                   │
│  status.currentReleaseImageRef: ri-v2.5.0                        │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 1:1 引用
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                               │
│  spec.version: v2.6.0                                            │
│  spec.ociRef: registry/openfuyao-release:v2.6.0                  │
│  spec.install.components: [{name: k8s, ver: v1.29.0}, ...]       │
│  spec.upgrade.components: [{name: provider, ver: v1.2.0}, ...]   │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 按 (name,version) 定位
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│              bke-manifests (ComponentVersion 定义)               │
│  bke-manifests/kubernetes/v1.29.0/component.yaml                 │
│  包含: type, inline, subComponents, compatibility, dependencies  │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│                       UpgradePath                                │
│  spec.ociRef: registry/openfuyao-upgradepath:latest              │
│  paths: [{from: v2.5.0, to: v2.6.0, blocked: false, checks: ...}]│
└──────────────────────────────────────────────────────────────────┘
```

### 4.2 ComponentVersion 数据结构设计
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: kubernetes-v1.29.0
spec:
  type: composite  # leaf | composite
  mode: inline     # inline (调用 Go Phase 代码) | external (YAML/脚本)
  handler: EnsureKubernetesUpgrade  # inline 映射标识
  defaultVersion: v1.0  # inline 组件默认版本（如未显式指定）
  # 组合组件包含的子组件元数据（用于兼容性检查）
  subComponents:
    - name: kube-apiserver
      version: v1.29.0
    - name: kube-controller-manager
      version: v1.29.0
    - name: etcd
      version: v3.5.12
  # 兼容性约束
  compatibility:
    constraints:
      - component: etcd
        rule: ">=3.5.10"
      - component: containerd
        rule: ">=1.7.0"
  # 依赖约束（区分安装与升级）
  dependencies:
    install:
      - name: containerd
        phase: Install
      - name: etcd
        phase: Install
    upgrade:
      - name: etcd
        phase: Upgrade
  # 升级策略
  upgradeStrategy:
    mode: Batch  # Serial | Parallel | Batch
    batchSize: 1
    maxUnavailable: 0
    preCheck: "etcdctl endpoint health --cluster"
    postCheck: "kubectl get nodes --no-headers | grep -v Ready"
```

### 4.3 bke-manifests 目录与 OCI 镜像设计
#### 4.3.1 bke-manifests 目录规范
取消原有硬编码目录，统一按 `(name, version)` 路由：
```
bke-manifests/
├── kubernetes/
│   ├── v1.28.0/
│   │   └── component.yaml      # ComponentVersion 元数据
│   └── v1.29.0/
│       └── component.yaml
├── etcd/
│   └── v3.5.12/
│       └── component.yaml
└── phases/
    └── provider-upgrade/
        └── v1.0.0/
            └── component.yaml  # type: inline, handler: EnsureProviderSelfUpgrade
```

#### 4.3.2 ReleaseImage OCI 样例 (YAML)
```yaml
# registry/openfuyao-release:v2.6.0 (layer/release.yaml)
version: "v2.6.0"
ociRef: "registry/openfuyao-release:v2.6.0"
install:
  components:
    - name: kubernetes
      version: v1.29.0
    - name: etcd
      version: v3.5.12
    - name: bkeagent
      version: v2.6.0
upgrade:
  components:
    - name: provider-upgrade
      version: v1.2.0
    - name: component-upgrade
      version: v1.1.0
    - name: etcd-upgrade
      version: v3.5.12
```
**解析流程**：`ClusterVersionReconciler` 通过 `go-containerregistry` 拉取 OCI Config/Layer → 反序列化为 `ReleaseImageSpec` → 创建/更新 CR。每个版本独立镜像，支持离线 `skopeo copy` 到本地仓库。

#### 4.3.3 UpgradePath OCI 样例 (YAML)
```yaml
# registry/openfuyao-upgradepath:latest (layer/paths.yaml)
paths:
  - from: "v2.5.0"
    to: "v2.6.0"
    blocked: false
    preCheck:
      type: script
      command: "etcdctl endpoint health --cluster"
    postCheck:
      type: api
      resource: nodes
      condition: Ready
  - from: "v2.4.0"
    to: "v2.6.0"
    blocked: true
    reason: "跨大版本升级存在 etcd 数据迁移风险，请先升级至 v2.5.0"
```
仅保留 `latest` 标签，控制器按需拉取并缓存至内存。路径规则可动态热更新，无需重建 ReleaseImage。

### 4.4 升级路径与兼容性检查设计
- **升级路径管控**：`ClusterVersion` 变更时，按 `{from}-to-{to}` 查找路径。`blocked=true` 拦截升级并记录事件；`deprecated=true` 仅告警放行。支持前置/后置检查（脚本或 API 探针）。
- **兼容性检查矩阵**：
  1. **静态校验**：`ReleaseImageReconciler` 解析 OCI 后，遍历 `ComponentVersion.compatibility.constraints`。若目标组合违反规则，标记 `Status.Phase=Invalid`。
  2. **运行时校验**：`BKEClusterReconciler` 在 DAG 执行前，比对集群实际版本与目标约束。支持 `--skip-compatibility-check` 紧急开关。
  3. **子组件透传**：`composite` 组件的 `subComponents` 自动展开参与全局兼容性校验。

### 4.5 Phase 整改与 NeedExecute 复用设计
**严格不新增 `ShouldUpgrade()` 接口**。改造现有 Phase 的 `NeedExecute(old, new)`：
```go
// pkg/phaseframe/phases/ensure_etcd_upgrade.go
func (p *EnsureEtcdUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    if !p.BasePhase.DefaultNeedExecute(old, new) { return false }

    if featuregate.DeclarativeUpgradeEnabled(new) {
        ctx := p.GetVersionContext(new)
        if ctx == nil { return false } // 上下文未就绪，等待同步

        cur := ctx.Current["etcd"]
        tgt := ctx.Target["etcd"]
        if cur == tgt || tgt == "" {
            p.Log.V(4).Info("Component version unchanged, skipping")
            return false
        }
        p.Log.Info("Declarative Upgrade triggered: %s -> %s", cur, tgt)
        return true
    }
    // 兼容旧逻辑
    return p.isEtcdNeedUpgrade(old, new)
}

// 非 YAML 化 Phase 的 Version() 接口实现
func (p *EnsureEtcdUpgrade) CurrentVersion(c *bkev1beta1.BKECluster) string {
    return c.Status.EtcdVersion
}
```
`BKEClusterReconciler` 在执行前收集所有 Phase 的 `CurrentVersion()` 构建 `VersionContext`，注入到 Phase 上下文中。

#### 4.5.1 各 Phase 整改设计清单
| Phase 名称 | 整改方式 | bke-manifests 映射 | 默认版本 |
|-----------|---------|-------------------|---------|
| `EnsureProviderSelfUpgrade` | 转为 YAML 清单，`mode: inline` | `phases/provider-upgrade/v1.0.0/component.yaml` | `v1.0` |
| `EnsureAgentUpgrade` | 转为 YAML 清单，`mode: inline` | `phases/bkeagent-upgrade/v1.0.0/component.yaml` | `v1.0` |
| `EnsureComponentUpgrade` | 转为 YAML 清单，`mode: inline` | `phases/component-upgrade/v1.0.0/component.yaml` | `v1.0` |
| `EnsureEtcdUpgrade` | 转为 YAML 清单，`mode: inline` | `phases/etcd-upgrade/v1.0.0/component.yaml` | `v1.0` |
| 其他代码 Phase | 增加 `Version()` 接口，复用 `NeedExecute` | 不生成 YAML，直接通过注册表映射 | `v1.0` |

### 4.6 升级流程与 DAG 调度设计
1. **清单解析**：用户更新 `ClusterVersion.Spec.DesiredVersion` → `ClusterVersionReconciler` 拉取目标 OCI → 创建 `ReleaseImage` CR。
2. **路径与兼容性校验**：拉取 `UpgradePath` 校验拦截规则；`ReleaseImageReconciler` 校验兼容性矩阵。通过后写入 BKECluster Annotation `cvo.openfuyao.cn/upgrade-ready=v2.6.0`。
3. **DAG 构建**：`BKEClusterReconciler` 捕获 Annotation → 判断场景（`status.currentVersion == ""` 为安装，否则为升级） → 拉取对应 `install` 或 `upgrade` 组件列表 → 遍历 `ComponentVersion` 依赖字段 → 拓扑排序生成独立 DAG。
4. **版本比对与 Phase 调用**：按 DAG 顺序遍历组件 → 注入 `VersionContext` → 调用 Phase `NeedExecute()` → 返回 `true` 则执行 `Execute()`，否则跳过。
5. **状态同步**：每步完成后记录 `ClusterVersion.Status.History`，全部成功则更新 `CurrentVersion` 与 `Phase=Healthy`。

### 4.7 控制器架构与实现
| 控制器 | 核心职责 | 协同方式 |
|--------|---------|----------|
| **ClusterVersionReconciler** | 版本声明管理、ReleaseImage/UpgradePath OCI 拉取与解析、触发 BKECluster 调谐 | 更新 BKECluster Annotation `upgrade-ready` |
| **ReleaseImageReconciler** | 清单校验、兼容性矩阵验证、bke-manifests 路径验证、状态标记 | 独立调谐，更新 `Status.Phase` |
| **UpgradePathReconciler** | 路径规则验证、拦截/废弃状态维护、使用统计 | 提供 `FindUpgradePath()` 接口供 CV 调用 |
| **BKEClusterReconciler** (增强) | 监听版本变更、构建 VersionContext、DAG 调度、Phase 桥接、状态回写 | 直接 Watch CV；调用 Phase 注入上下文；更新 CV Status |

**核心代码片段 (DAG 执行)**：
```go
func (r *BKEClusterReconciler) executeDAG(ctx context.Context, bc *bkev1beta1.BKECluster, dag *topology.DAG) error {
    vCtx := r.buildVersionContext(bc)
    for _, batch := range dag.TopologicalSort() {
        var errs []error
        for _, compName := range batch {
            phase := r.phaseRegistry.Get(compName)
            phase.SetVersionContext(vCtx)
            if !phase.NeedExecute(nil, bc) {
                continue
            }
            if err := phase.Execute(); err != nil {
                errs = append(errs, fmt.Errorf("%s: %w", compName, err))
                if comp.Strategy.FailurePolicy == "FailFast" { return kerrors.NewAggregate(errs) }
            }
        }
        if len(errs) > 0 { return kerrors.NewAggregate(errs) }
    }
    return nil
}
```

### 4.8 平滑升级方案（旧版到新版）
旧版本无 `ClusterVersion`、`ReleaseImage`、`UpgradePath` 及新增控制器。平滑升级分三步：
1. **部署新 CRD 与控制器（FeatureGate 关闭）**：
   - 部署 `cvo.openfuyao.cn` API 资源与控制器，但 `DeclarativeUpgrade=false`。
   - `BKEClusterReconciler` 保持原有逻辑运行，集群状态不受影响。
2. **自动迁移与状态同步**：
   - `BKEClusterReconciler` 检测到旧版集群（无 CV 关联）时，自动创建 `ClusterVersion` 实例，`DesiredVersion` 与 `CurrentVersion` 均填充为当前 `BKECluster.Spec.OpenFuyaoVersion`。
   - 根据当前版本自动生成 `ReleaseImage` CR，组件列表从 `BKECluster.Status` 推导。
   - 建立 `OwnerReference` 关联，完成元数据映射。
3. **开启 FeatureGate 切换**：
   - 运维开启 `DeclarativeUpgrade=true`。
   - 后续 `DesiredVersion` 变更将由新流程接管：CV 控制器拉取 OCI → 构建 DAG → Phase 比对版本执行升级。
   - 若升级失败，可通过 `spec.allowDowngrade=true` 自动回退至迁移时记录的稳定版本。

## 5. 工作量评估与风险
| 阶段 | 任务内容 | 工作量 (人天) | 说明 |
|------|---------|:-------------:|------|
| **1. CRD 与 API** | CV/RI/UP/ComponentVersion 定义、Webhook、DeepCopy | 7 | 熟悉 kubebuilder、验证规则、不可变约束 |
| **2. OCI 解析层** | 镜像拉取、Config 解析、兼容性校验引擎、缓存机制 | 10 | 处理网络异常、鉴权、离线 fallback |
| **3. 控制器开发** | CV/RI/UP 控制器逻辑、状态机、Annotation 协同机制 | 12 | controller-runtime 调谐循环与 Watch 配置 |
| **4. DAG 引擎** | 依赖图构建、拓扑排序、安装/升级 DAG 分离、并发调度 | 10 | 需处理环检测、超时中断、批次策略 |
| **5. Phase 适配** | 4 个 Phase YAML 化、`Version()` 接口实现、`NeedExecute` 改造 | 7 | 调试上下文传递，保持原有逻辑兼容 |
| **6. 迁移与测试** | 旧版平滑迁移逻辑、单元测试、集成测试、E2E | 12 | 覆盖多场景、异常流、兼容性校验 |
| **总计** | | **58 人天** | 含代码评审、联调缓冲与文档编写 |

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| OCI 镜像拉取失败或解析错误 | 升级阻塞 | 指数退避重试；本地 ConfigMap fallback；`ReleaseImage` 提前标记 `Invalid` |
| 依赖图存在环路 | DAG 构建死锁 | 拓扑排序前执行环检测算法；超时强制中断并记录 `CycleDetected` 事件 |
| 兼容性校验误报/漏报 | 升级中断或集群不稳定 | 提供 `--skip-compatibility-check` 紧急开关；规则支持热更新；记录详细审计日志 |
| Phase 上下文注入丢失 | 升级决策错误 | `NeedExecute` 增加 nil 保护；单元测试覆盖上下文生命周期；Feature Gate 灰度 |

## 6. 测试计划与验收标准
| 测试类型 | 覆盖场景 |
|----------|---------|
| **单元测试** | DAG 拓扑排序、兼容性矩阵校验、`NeedExecute` 分支逻辑、OCI 解析 |
| **集成测试** | CV ↔ RI ↔ BKECluster 状态联动、Annotation 触发机制、Phase 注册表映射 |
| **E2E 测试** | 补丁升级、跨版本升级、单组件独立升级、失败中断与回滚、OCI 缺失降级 |
| **兼容性测试** | Feature Gate 关闭时旧流程正常运行；新旧版本混合状态平滑过渡 |

**验收标准**：
- **Alpha**: CRD 注册，控制器可启动，日志验证清单解析与路径查找正确。
- **Beta**: 接管升级流程，与旧 Phase 并行运行，结果对比验证，E2E 通过率 >95%。
- **GA**: 全量切换，移除旧版本硬编码调度逻辑，支持生产环境灰度发布。

## 7. 架构图与流程图
### 7.1 控制器协同架构图
```
用户/CI ──▶ ClusterVersion.Spec.DesiredVersion = "v2.6.0"
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│ ClusterVersionReconciler                                    │
│  • 拉取 ReleaseImage/UpgradePath OCI 解析为 CR               │
│  • 校验 CompatibilityMatrix                                 │
│  • 写入 BKECluster Annotation: upgrade-ready=v2.6.0         │
└──────────────────────────┬──────────────────────────────────┘
                           │ 触发调谐
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ BKEClusterReconciler (增强)                                 │
│  • 捕获 Annotation 变更                                     │
│  • 判断 Install/Upgrade 场景                                │
│  • 构建 VersionContext (Current vs Target Components)       │
│  • 解析 ComponentVersion 依赖 → 构建独立 DAG                │
│  • 按拓扑顺序调用 Phase，注入 VersionContext                │
└──────────────────────────┬──────────────────────────────────┘
                           │ 执行升级
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ PhaseFrame (Provider/Agent/Etcd/Component/Inline)           │
│  • NeedExecute() 中比对版本                                  │
│  • 版本不同 → 执行原有逻辑                                   │
│  • 版本相同 → 跳过，标记 Succeeded                          │
│  • 执行结果回写 ClusterVersion.Status.History               │
└─────────────────────────────────────────────────────────────┘
```

### 7.2 DAG 驱动升级流程图
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
│ 3. 场景判断: Install or Upgrade?    │
│    选择对应 Dependency 边集          │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ 4. 构建 DAG (拓扑排序+环检测)       │
│    Provider → Agent → Etcd → Core   │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ 5. 循环执行 DAG 批次                 │
│    ├─ 注入 VersionContext           │
│    ├─ NeedExecute() == false? Skip  │
│    ├─ Execute()                     │
│    └─ 失败? → 标记 Failed, 终止     │
└──────────────┬──────────────────────┘
               │ 全部成功
               ▼
┌─────────────────────────────────────┐
│ 6. 更新状态                         │
│    CurrentVersion = DesiredVersion  │
│    Phase = Healthy                  │
│    记录 History                     │
└─────────────────────────────────────┘
```
