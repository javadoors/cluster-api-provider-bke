# install-service 升级设计方案

> 本文档描述如何在 BKE 集群升级流程中，判断集群是否为管理集群并安装了 `install-service`（`openfuyao-system-controller`），然后执行 install-service 的升级逻辑。

---

## 目录

- [一、问题分析](#一问题分析)
- [二、现有架构分析](#二现有架构分析)
- [三、设计方案](#三设计方案)
- [四、实现细节](#四实现细节)
- [五、升级流程](#五升级流程)
- [六、风险与缓解措施](#六风险与缓解措施)

---

## 一、问题分析

### 1.1 背景

`install-service`（`openfuyao-system-controller`）是管理集群的核心组件，负责：
- 初始化安装管理面组件（Console、监控、OAuth 等）
- 管理管理面组件的生命周期
- 提供安装向导功能

当 BKE 版本升级时，如果集群是管理集群且安装了 `install-service`，需要同步升级该组件。

### 1.2 核心挑战

**如何判断当前集群是管理集群且安装了 install-service？**

当前架构中：
- BKECluster Spec/Status 中没有直接的"管理集群"标识字段
- 集群类型通过 annotation `bke.bocloud.com/cluster-from` 区分来源（bke/bocloud/other）
- 没有显式的"管理集群 vs 业务集群"判断机制

### 1.3 设计目标

1. 准确识别管理集群（安装了 `bke-controller-manager` 的集群）
2. 仅在管理集群上执行 install-service 升级
3. 与现有 Phase 框架集成，遵循相同的执行模式
4. 零配置，无需修改 BKECluster 创建流程

---

## 二、现有架构分析

### 2.1 管理集群的特征

管理集群的核心特征是运行着 `bke-controller-manager` Deployment：

```
命名空间：cluster-system
Deployment：bke-controller-manager
容器：manager
镜像：cluster-api-provider-bke:<version>
```

**判断依据**：
- 如果 `cluster-system/bke-controller-manager` 存在 → 管理集群
- 如果不存在 → 业务集群

### 2.2 install-service 的特征

```
命名空间：openfuyao-system-controller
Deployment：openfuyao-system-controller
容器：manager（待确认）
镜像：openfuyao-system-controller:<version>
```

### 2.3 现有的集群类型判断机制

| 判断方式 | 位置 | 说明 |
|---------|------|------|
| `clusterutil.IsBKECluster()` | `clusterutil/helper.go` | 判断集群来源是否为 "bke" |
| `clusterutil.IsBocloudCluster()` | `clusterutil/helper.go` | 判断集群来源是否为 "bocloud" |
| `clusterutil.FullyControlled()` | `clusterutil/helper.go` | 判断是否被 BKE 完全接管 |
| `EnsureProviderSelfUpgrade` | `ensure_provider_self_upgrade.go` | 通过检测 Deployment 是否存在来判断 |

### 2.4 EnsureProviderSelfUpgrade 的设计模式

`EnsureProviderSelfUpgrade` 采用 **Deployment 探测** 模式：

```go
func (p *EnsureProviderSelfUpgrade) isProviderNeedUpgrade(
    old, new *bkev1beta1.BKECluster,
) bool {
    // 1. 检查 Deployment 是否存在
    target := getProviderDeploymentTarget()  // cluster-system/bke-controller-manager
    currentImage, err := phaseutil.GetDeploymentImage(ctx, c, target)
    if err != nil {
        // Deployment 不存在，跳过
        return false
    }
    
    // 2. 获取目标镜像
    targetImage, err := p.getProviderTargetImage(new)
    if err != nil || targetImage == "" {
        return false
    }
    
    // 3. 比较镜像
    return currentImage != targetImage
}
```

**核心思想**：不判断"是否是管理集群"，而是直接检查目标 Deployment 是否存在。如果不存在，自然跳过。

---

## 三、设计方案

### 3.1 方案选择：Deployment 探测

采用与 `EnsureProviderSelfUpgrade` 一致的设计模式：

**判断逻辑**：
1. 检测 `cluster-system/bke-controller-manager` 是否存在
2. 如果存在 → 管理集群，继续检查 install-service 是否需要升级
3. 如果不存在 → 业务集群，跳过 install-service 升级

### 3.2 判断流程图

```
NeedExecute
    │
    ├── 1. DefaultNeedExecute (通用检查)
    │       ├── 非删除、非暂停、非 DryRun、非 Failed
    │       └── IsBKECluster 或 FullyControlled
    │
    ├── 2. 检查 bke-controller-manager Deployment 是否存在
    │       ├── 不存在 → 返回 false (非管理集群，跳过)
    │       └── 存在 → 继续
    │
    ├── 3. 检查 openfuyao-system-controller Deployment 是否存在
    │       ├── 不存在 → 返回 false (未安装 install-service，跳过)
    │       └── 存在 → 继续
    │
    ├── 4. 版本变更检查
    │       ├── Spec.OpenFuyaoVersion == Status.OpenFuyaoVersion → 跳过
    │       └── 版本变化 → 继续
    │
    ├── 5. 镜像比较
    │       ├── 获取当前 install-service 镜像
    │       ├── 从 PatchConfig 获取目标镜像
    │       └── 镜像相同 → 跳过，镜像不同 → 需要升级
    │
    └── 返回 true
```

### 3.3 方案对比

| 方案 | 优势 | 风险 | 推荐度 |
|------|------|------|--------|
| **A. Deployment 探测** | 与 ProviderSelfUpgrade 一致；零配置；自然跳过 | 依赖 Deployment 名称硬编码 | ⭐⭐⭐⭐⭐ |
| B. Annotation 标记 | 显式声明；语义清晰 | 需修改创建流程；需补标 annotation | ⭐⭐⭐ |
| C. Condition 推断 | 复用已有机制 | 需扩展推断逻辑；语义不完全匹配 | ⭐⭐ |

**推荐方案 A**，理由：
1. 与 `EnsureProviderSelfUpgrade` 的设计模式完全一致，架构统一
2. 零配置，无需修改 BKECluster 创建流程
3. 非管理集群自然跳过，不产生副作用
4. 检测的是实际运行状态，而非声明式配置，更可靠

---

## 四、实现细节

### 4.1 Phase 定义

```go
const (
    EnsureInstallServiceUpgradeName confv1beta1.BKEClusterPhase = "EnsureInstallServiceUpgrade"
    
    // 管理集群标识 Deployment
    managementClusterNamespace  = "cluster-system"
    managementClusterDeployment = "bke-controller-manager"
    managementClusterContainer  = "manager"
    
    // install-service Deployment
    installServiceNamespace    = "openfuyao-system-controller"
    installServiceDeployment   = "openfuyao-system-controller"
    installServiceContainer    = "manager"  // 待确认
    
    // 升级超时
    installServiceUpgradeTimeout = 5 * time.Minute
)

type EnsureInstallServiceUpgrade struct {
    phaseframe.BasePhase
}

func NewEnsureInstallServiceUpgrade(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    base := phaseframe.NewBasePhase(ctx, EnsureInstallServiceUpgradeName)
    return &EnsureInstallServiceUpgrade{BasePhase: base}
}
```

### 4.2 NeedExecute 实现

```go
func (p *EnsureInstallServiceUpgrade) NeedExecute(
    old, new *bkev1beta1.BKECluster,
) bool {
    if !p.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    if !p.isInstallServiceNeedUpgrade(old, new) {
        return false
    }
    
    p.SetStatus(bkev1beta1.PhaseWaiting)
    return true
}

func (p *EnsureInstallServiceUpgrade) isInstallServiceNeedUpgrade(
    old, new *bkev1beta1.BKECluster,
) bool {
    ctx, c, _, _, log := p.Ctx.Untie()
    
    // 1. 检查是否是管理集群（bke-controller-manager 是否存在）
    managementTarget := phaseutil.DeploymentTarget{
        Namespace: managementClusterNamespace,
        Name:      managementClusterDeployment,
        Container: managementClusterContainer,
    }
    _, err := phaseutil.GetDeploymentImage(ctx, c, managementTarget)
    if err != nil {
        log.Debug("bke-controller-manager not found, skip (not management cluster)")
        return false
    }
    
    // 2. 检查 install-service 是否存在
    installServiceTarget := phaseutil.DeploymentTarget{
        Namespace: installServiceNamespace,
        Name:      installServiceDeployment,
        Container: installServiceContainer,
    }
    currentImage, err := phaseutil.GetDeploymentImage(ctx, c, installServiceTarget)
    if err != nil {
        log.Debug("openfuyao-system-controller not found, skip (not installed)")
        return false
    }
    
    // 3. 版本未变化，跳过
    if new.Status.OpenFuyaoVersion == new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
        log.Debug("openFuyaoVersion unchanged, skip")
        return false
    }
    
    // 4. 获取目标镜像
    targetImage, err := p.getInstallServiceTargetImage(new)
    if err != nil || targetImage == "" {
        log.Debug("failed to get target image, skip")
        return false
    }
    
    // 5. 比较镜像
    if currentImage == targetImage {
        log.Debug("install-service image already up to date, skip")
        return false
    }
    
    log.Info("install-service upgrade needed, current: %s, target: %s",
        currentImage, targetImage)
    return true
}
```

### 4.3 Execute 实现

```go
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

### 4.4 镜像查找逻辑

```go
func (p *EnsureInstallServiceUpgrade) getInstallServiceTargetImage(
    bkeCluster *bkev1beta1.BKECluster,
) (string, error) {
    _, c, _, _, log := p.Ctx.Untie()
    
    // 从 PatchConfig 中查找 install-service 镜像
    patchCfg, err := p.getPatchConfig(bkeCluster)
    if err != nil {
        return "", err
    }
    
    // 遍历 Repos 查找 install-service 镜像
    for _, repo := range patchCfg.Repos {
        for _, subImage := range repo.SubImages {
            if image, found := p.findInstallServiceImage(subImage); found {
                log.Info("found install-service target image: %s", image)
                return image, nil
            }
        }
    }
    
    return "", fmt.Errorf("install-service image not found in patch config")
}

func (p *EnsureInstallServiceUpgrade) findInstallServiceImage(
    subImage phaseutil.SubImage,
) (string, bool) {
    for _, image := range subImage.Images {
        // 匹配方式 1：镜像名包含 "openfuyao-system-controller"
        if strings.Contains(image.Name, "openfuyao-system-controller") {
            if len(image.Tag) == 0 {
                continue
            }
            fullImage := fmt.Sprintf("%s/%s:%s",
                strings.TrimSuffix(subImage.SourceRepo, "/"),
                strings.TrimPrefix(image.Name, "/"),
                image.Tag[0])
            return fullImage, true
        }
        
        // 匹配方式 2：UsedPodInfo 匹配
        for _, podInfo := range image.UsedPodInfo {
            if podInfo.PodPrefix == "openfuyao-system-controller" &&
                podInfo.NameSpace == installServiceNamespace {
                if len(image.Tag) == 0 {
                    continue
                }
                fullImage := fmt.Sprintf("%s/%s:%s",
                    strings.TrimSuffix(subImage.SourceRepo, "/"),
                    strings.TrimPrefix(image.Name, "/"),
                    image.Tag[0])
                return fullImage, true
            }
        }
    }
    return "", false
}
```

### 4.5 Phase 注册

在 `pkg/phaseframe/phases/list.go` 中注册：

```go
var PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureProviderSelfUpgrade,
    NewEnsureInstallServiceUpgrade,  // 新增：在 ProviderSelfUpgrade 之后
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

---

## 五、升级流程

### 5.1 完整升级流程

```
BKE 版本升级触发
    │
    ├── 1. EnsureProviderSelfUpgrade
    │       ├── 检测 bke-controller-manager 是否存在
    │       ├── 升级 bke-controller-manager 镜像
    │       └── 自身重启，新版本接管
    │
    ├── 2. EnsureInstallServiceUpgrade (新增)
    │       ├── 检测 bke-controller-manager 是否存在（判断管理集群）
    │       ├── 检测 openfuyao-system-controller 是否存在（判断已安装）
    │       ├── 升级 openfuyao-system-controller 镜像
    │       └── 等待新 Pod 就绪
    │
    ├── 3. EnsureAgentUpgrade
    │       └── 升级 BKE Agent
    │
    └── ... 其他升级 Phase
```

### 5.2 判断逻辑时序图

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│  PhaseFlow  │         │  NeedExecute│         │   API Server│
└──────┬──────┘         └──────┬──────┘         └──────┬──────┘
       │                       │                       │
       │ NeedExecute?          │                       │
       ├──────────────────────>│                       │
       │                       │                       │
       │                       │ GetDeploymentImage    │
       │                       │ (bke-controller-mgr)  │
       │                       ├──────────────────────>│
       │                       │                       │
       │                       │<──────────────────────┤
       │                       │ 返回镜像或错误         │
       │                       │                       │
       │                       │ [不存在]              │
       │                       │ → return false        │
       │                       │                       │
       │<──────────────────────┤                       │
       │ return false          │                       │
       │ (跳过 Phase)          │                       │
       │                       │                       │
       │                       │ [存在]                │
       │                       │                       │
       │                       │ GetDeploymentImage    │
       │                       │ (install-service)     │
       │                       ├──────────────────────>│
       │                       │                       │
       │                       │<──────────────────────┤
       │                       │ 返回当前镜像           │
       │                       │                       │
       │                       │ 比较镜像              │
       │                       │                       │
       │<──────────────────────┤                       │
       │ return true           │                       │
       │ (执行 Phase)          │                       │
       │                       │                       │
```

### 5.3 场景分析

| 场景 | bke-controller-manager | install-service | 行为 |
|------|------------------------|-----------------|------|
| 管理集群，已安装 install-service | ✅ 存在 | ✅ 存在 | 执行升级 |
| 管理集群，未安装 install-service | ✅ 存在 | ❌ 不存在 | 跳过 |
| 业务集群 | ❌ 不存在 | ❌ 不存在 | 跳过 |
| 纳管的 bocloud 集群 | ❌ 不存在 | ❌ 不存在 | 跳过 |

---

## 六、风险与缓解措施

### 6.1 风险识别

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Deployment 名称变更 | 判断失效 | 低 | 使用常量定义，便于维护 |
| install-service 容器名不确定 | 镜像获取失败 | 中 | 实现前确认实际容器名 |
| PatchConfig 中无 install-service 镜像 | 升级失败 | 低 | 添加详细的错误日志 |
| 升级过程中 install-service 被终止 | 升级中断 | 中 | 处理 context canceled 场景 |
| 多副本场景下的竞态条件 | 重复升级 | 低 | 依赖 Leader Election |

### 6.2 待确认信息

在实现前需要确认：

1. **install-service 的容器名**
   - 当前假设为 `manager`，需要确认实际值
   - 确认方式：`kubectl get deployment openfuyao-system-controller -n openfuyao-system-controller -o jsonpath='{.spec.template.spec.containers[*].name}'`

2. **install-service 在 PatchConfig 中的定义**
   - 确认镜像名是否为 `openfuyao-system-controller`
   - 确认 UsedPodInfo 中的 PodPrefix 和 Namespace

3. **install-service 的启动时间**
   - 确认 `installServiceUpgradeTimeout` 是否足够（当前设为 5 分钟）

4. **是否需要处理自举升级**
   - install-service 升级是否会导致自身重启
   - 如果会，需要类似 `EnsureProviderSelfUpgrade` 的 context canceled 处理

### 6.3 测试计划

| 测试类型 | 测试内容 | 预期结果 |
|---------|---------|---------|
| 单元测试 | NeedExecute 判断逻辑 | 正确识别管理集群和业务集群 |
| 集成测试 | 管理集群升级流程 | install-service 成功升级 |
| 集成测试 | 业务集群升级流程 | Phase 被跳过 |
| 集成测试 | 管理集群未安装 install-service | Phase 被跳过 |
| 端到端测试 | 完整升级流程 | 所有组件成功升级 |

---

## 七、总结

### 7.1 核心设计

- **判断依据**：通过检测 `cluster-system/bke-controller-manager` 是否存在来判断是否是管理集群
- **设计模式**：与 `EnsureProviderSelfUpgrade` 一致，采用 Deployment 探测
- **执行条件**：管理集群 + 已安装 install-service + 版本变化 + 镜像不同

### 7.2 关键优势

1. **零配置**：无需修改 BKECluster 创建流程或添加 annotation
2. **自然跳过**：非管理集群或未安装 install-service 的集群自动跳过
3. **架构统一**：与现有 Phase 框架和 ProviderSelfUpgrade 模式一致
4. **可靠性高**：检测实际运行状态，而非声明式配置

### 7.3 后续工作

1. 确认 install-service 的容器名和镜像定义
2. 实现 `EnsureInstallServiceUpgrade` Phase
3. 编写单元测试和集成测试
4. 在测试环境验证升级流程
5. 更新升级文档
