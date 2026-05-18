# install & upgrade phase

## `FullPhasesRegisFunc` 完整 Phase 列表（共 26 个）

`FullPhasesRegisFunc = CommonPhases + DeployPhases + PostDeployPhases`

### 一、CommonPhases（5 个，通用阶段）

| 序号 | Phase 名称 | 函数 | 中文说明 |
|------|-----------|------|----------|
| 1 | `EnsureFinalizer` | `NewEnsureFinalizer` | 部署任务创建（Finalizer 管理） |
| 2 | `EnsurePaused` | `NewEnsurePaused` | 集群管理暂停 |
| 3 | `EnsureClusterManage` | `NewEnsureClusterManage` | 纳管现有集群 |
| 4 | `EnsureDeleteOrReset` | `NewEnsureDeleteOrReset` | 集群删除/重置 |
| 5 | `EnsureDryRun` | `NewEnsureDryRun` | DryRun 部署 |

### 二、DeployPhases（11 个，部署阶段）

| 序号 | Phase 名称 | 函数 | 中文说明 |
|------|-----------|------|----------|
| 6 | `EnsureBKEAgent` | `NewEnsureBKEAgent` | 推送 Agent 到节点 |
| 7 | `EnsureNodesEnv` | `NewEnsureNodesEnv` | 节点环境准备 |
| 8 | `EnsureClusterAPIObj` | `NewEnsureClusterAPIObj` | ClusterAPI 对接 |
| 9 | `EnsureCerts` | `NewEnsureCerts` | 集群证书创建 |
| 10 | `EnsureLoadBalance` | `NewEnsureLoadBalance` | 集群入口配置（HA） |
| 11 | `EnsureMasterInit` | `NewEnsureMasterInit` | Master 初始化 |
| 12 | `EnsureMasterJoin` | `NewEnsureMasterJoin` | Master 加入集群 |
| 13 | `EnsureWorkerJoin` | `NewEnsureWorkerJoin` | Worker 加入集群 |
| 14 | `EnsureAddonDeploy` | `NewEnsureAddonDeploy` | 集群组件部署 |
| 15 | `EnsureNodesPostProcess` | `NewEnsureNodesPostProcess` | 后置脚本处理 |
| 16 | `EnsureAgentSwitch` | `NewEnsureAgentSwitch` | Agent 监听切换 |

### 三、PostDeployPhases（10 个，部署后阶段）

| 序号 | Phase 名称 | 函数 | 中文说明 |
|------|-----------|------|----------|
| 17 | `EnsureProviderSelfUpgrade` | `NewEnsureProviderSelfUpgrade` | Provider 自升级 |
| 18 | `EnsureAgentUpgrade` | `NewEnsureAgentUpgrade` | Agent 升级 |
| 19 | `EnsureContainerdUpgrade` | `NewEnsureContainerdUpgrade` | Containerd 升级 |
| 20 | `EnsureEtcdUpgrade` | `NewEnsureEtcdUpgrade` | Etcd 升级 |
| 21 | `EnsureWorkerUpgrade` | `NewEnsureWorkerUpgrade` | Worker 节点升级 |
| 22 | `EnsureMasterUpgrade` | `NewEnsureMasterUpgrade` | Master 节点升级 |
| 23 | `EnsureWorkerDelete` | `NewEnsureWorkerDelete` | Worker 删除（缩容） |
| 24 | `EnsureMasterDelete` | `NewEnsureMasterDelete` | Master 删除（缩容） |
| 25 | `EnsureComponentUpgrade` | `NewEnsureComponentUpgrade` | openFuyao 核心组件升级 |
| 26 | `EnsureCluster` | `NewEnsureCluster` | 集群健康检查 |

## 安装 vs 升级 实际执行的 Phase

所有 26 个 Phase 都会经过 `NeedExecute(old, new)` 判断，**只有条件满足的才会实际执行**：

### 新建集群安装时（典型执行路径）

| Phase | 是否执行 | 说明 |
|-------|---------|------|
| `EnsureFinalizer` | ✅ | 始终执行 |
| `EnsurePaused` | ❌ | 未设置暂停注解时跳过 |
| `EnsureClusterManage` | ❌ | 非纳管场景跳过 |
| `EnsureDeleteOrReset` | ❌ | 非删除场景跳过 |
| `EnsureDryRun` | ❌ | 非 DryRun 场景跳过 |
| `EnsureBKEAgent` | ✅ | 首次推送 Agent |
| `EnsureNodesEnv` | ✅ | 节点环境准备 |
| `EnsureClusterAPIObj` | ✅ | 创建 CAPI 资源 |
| `EnsureCerts` | ✅ | 生成证书 |
| `EnsureLoadBalance` | ✅ | 配置 HA |
| `EnsureMasterInit` | ✅ | 初始化首个 Master |
| `EnsureMasterJoin` | ✅ | 其余 Master 加入（HA 时） |
| `EnsureWorkerJoin` | ✅ | Worker 加入 |
| `EnsureAddonDeploy` | ✅ | 部署 Addons |
| `EnsureNodesPostProcess` | ✅ | 后置处理 |
| `EnsureAgentSwitch` | ✅ | Agent 切换监听 |
| `EnsureProviderSelfUpgrade` | ❌ | 安装时通常跳过 |
| `EnsureAgentUpgrade` | ❌ | 安装时版本一致，跳过 |
| `EnsureContainerdUpgrade` | ❌ | 安装时版本一致，跳过 |
| `EnsureEtcdUpgrade` | ❌ | 安装时版本一致，跳过 |
| `EnsureWorkerUpgrade` | ❌ | 安装时版本一致，跳过 |
| `EnsureMasterUpgrade` | ❌ | 安装时版本一致，跳过 |
| `EnsureWorkerDelete` | ❌ | 非缩容跳过 |
| `EnsureMasterDelete` | ❌ | 非缩容跳过 |
| `EnsureComponentUpgrade` | ❌ | 安装时版本一致，跳过 |
| `EnsureCluster` | ✅ | 健康检查 |

### 升级时（典型执行路径）

| Phase | 是否执行 | 说明 |
|-------|---------|------|
| `EnsureFinalizer` | ✅ | 始终执行 |
| `EnsurePaused` | ❌ | 未设置暂停注解时跳过 |
| `EnsureClusterManage` | ❌ | 非纳管场景跳过 |
| `EnsureDeleteOrReset` | ❌ | 非删除场景跳过 |
| `EnsureDryRun` | ❌ | 非 DryRun 场景跳过 |
| `EnsureBKEAgent` | ❌ | Agent 已存在，跳过 |
| `EnsureNodesEnv` | ❌ | 环境已就绪，跳过 |
| `EnsureClusterAPIObj` | ❌ | CAPI 资源已存在，跳过 |
| `EnsureCerts` | ❌ | 证书已存在，跳过 |
| `EnsureLoadBalance` | ❌ | HA 已配置，跳过 |
| `EnsureMasterInit` | ❌ | 已初始化，跳过 |
| `EnsureMasterJoin` | ❌ | 非扩容跳过 |
| `EnsureWorkerJoin` | ❌ | 非扩容跳过 |
| `EnsureAddonDeploy` | ⚠️ | 若 addon 版本变化则执行 |
| `EnsureNodesPostProcess` | ❌ | 通常跳过 |
| `EnsureAgentSwitch` | ❌ | 已切换，跳过 |
| `EnsureProviderSelfUpgrade` | ✅ | Provider 版本变化时执行 |
| `EnsureAgentUpgrade` | ✅ | Agent 版本变化时执行 |
| `EnsureContainerdUpgrade` | ✅ | Containerd 版本变化时执行 |
| `EnsureEtcdUpgrade` | ✅ | Etcd 版本变化时执行 |
| `EnsureWorkerUpgrade` | ✅ | K8s 版本变化时执行 |
| `EnsureMasterUpgrade` | ✅ | K8s 版本变化时执行 |
| `EnsureWorkerDelete` | ❌ | 非缩容跳过 |
| `EnsureMasterDelete` | ❌ | 非缩容跳过 |
| `EnsureComponentUpgrade` | ✅ | 核心组件版本变化时执行 |
| `EnsureCluster` | ✅ | 健康检查 |

# 安装与升级时都会执行哪些phase

## FullPhasesRegisFunc 的组成

根据 [list.go]中的定义，`FullPhasesRegisFunc` 由三部分按顺序拼接：

```go
FullPhasesRegisFunc = CommonPhases + DeployPhases + PostDeployPhases
```

共 **27 个 Phase**，按注册顺序如下：

| # | 分组 | Phase 名称 | 中文名 |
|---|------|-----------|--------|
| 1 | Common | EnsureFinalizer | 部署任务创建 |
| 2 | Common | EnsurePaused | 集群管理暂停 |
| 3 | Common | EnsureClusterManage | 纳管现有集群 |
| 4 | Common | EnsureDeleteOrReset | 集群删除 |
| 5 | Common | EnsureDryRun | DryRun部署 |
| 6 | Deploy | EnsureBKEAgent | 推送Agent |
| 7 | Deploy | EnsureNodesEnv | 节点环境准备 |
| 8 | Deploy | EnsureClusterAPIObj | ClusterAPI对接 |
| 9 | Deploy | EnsureCerts | 集群证书创建 |
| 10 | Deploy | EnsureLoadBalance | 集群入口配置 |
| 11 | Deploy | EnsureMasterInit | Master初始化 |
| 12 | Deploy | EnsureMasterJoin | Master加入 |
| 13 | Deploy | EnsureWorkerJoin | Worker加入 |
| 14 | Deploy | EnsureAddonDeploy | 集群组件部署 |
| 15 | Deploy | EnsureNodesPostProcess | 后置脚本处理 |
| 16 | Deploy | EnsureAgentSwitch | Agent监听切换 |
| 17 | PostDeploy | EnsureProviderSelfUpgrade | provider自升级 |
| 18 | PostDeploy | EnsureAgentUpgrade | Agent升级 |
| 19 | PostDeploy | EnsureContainerdUpgrade | Containerd升级 |
| 20 | PostDeploy | EnsureEtcdUpgrade | Etcd升级 |
| 21 | PostDeploy | EnsureWorkerUpgrade | Worker升级 |
| 22 | PostDeploy | EnsureMasterUpgrade | Master升级 |
| 23 | PostDeploy | EnsureWorkerDelete | Worker删除 |
| 24 | PostDeploy | EnsureMasterDelete | Master删除 |
| 25 | PostDeploy | EnsureComponentUpgrade | openFuyao核心组件升级 |
| 26 | PostDeploy | EnsureCluster | 集群健康检查 |

## 安装场景（首次部署）

安装时 `BKECluster.Status` 中各版本字段为空（如 `OpenFuyaoVersion=""`, `KubernetesVersion=""`, `EtcdVersion=""` 等），`NeedExecute()` 的判断逻辑决定了哪些 Phase 会实际执行：

### ✅ 会执行的 Phase（按顺序）

| # | Phase 名称 | 触发原因 |
|---|-----------|---------|
| 1 | **EnsureFinalizer** | 默认需要执行 |
| 2 | **EnsureBKEAgent** | 首次部署，Agent 未推送 |
| 3 | **EnsureNodesEnv** | 首次部署，节点环境未准备 |
| 4 | **EnsureClusterAPIObj** | 首次部署，CAPI 对象未创建 |
| 5 | **EnsureCerts** | 首次部署，证书未创建 |
| 6 | **EnsureLoadBalance** | 首次部署，LB 未配置 |
| 7 | **EnsureMasterInit** | 首次部署，Master 未初始化 |
| 8 | **EnsureMasterJoin** | 首次部署（多 Master 时） |
| 9 | **EnsureWorkerJoin** | 首次部署，Worker 未加入 |
| 10 | **EnsureAddonDeploy** | 首次部署，Addon 未部署 |
| 11 | **EnsureNodesPostProcess** | 首次部署，后置脚本未执行 |
| 12 | **EnsureAgentSwitch** | 首次部署，Agent 监听未切换 |
| 13 | **EnsureProviderSelfUpgrade** | 仅当安装补丁版本（patch > 0）时执行 |
| 14 | **EnsureComponentUpgrade** | 仅当安装补丁版本（patch > 0）时执行 |
| 15 | **EnsureCluster** | 最终健康检查 |

### ❌ 不会执行的 Phase

| Phase 名称 | 跳过原因 |
|-----------|---------|
| EnsurePaused | 非暂停操作 |
| EnsureClusterManage | 非纳管操作 |
| EnsureDeleteOrReset | 非删除/重置操作 |
| EnsureDryRun | 非DryRun操作 |
| EnsureAgentUpgrade | `Status.OpenFuyaoVersion == ""`，且非补丁版本时跳过 |
| EnsureContainerdUpgrade | `Status.ContainerdVersion == ""` → `isContainerdNeedUpgrade` 返回 false |
| EnsureEtcdUpgrade | `Status.EtcdVersion == ""` → NeedExecute 返回 false |
| EnsureWorkerUpgrade | 无需升级的 Worker 节点 |
| EnsureMasterUpgrade | 无需升级的 Master 节点 |
| EnsureWorkerDelete | 非缩容操作 |
| EnsureMasterDelete | 非缩容操作 |

## 升级场景

升级时 `BKECluster.Status` 中已有版本信息，但 `Spec` 中的版本发生了变化（如 `OpenFuyaoVersion`、`KubernetesVersion`、`EtcdVersion`、`ContainerdVersion` 等变更）。

### ✅ 会执行的 Phase（按顺序）

| # | Phase 名称 | 触发原因 |
|---|-----------|---------|
| 1 | **EnsureFinalizer** | 默认需要执行 |
| 2 | **EnsureProviderSelfUpgrade** | `Status.OpenFuyaoVersion != Spec.OpenFuyaoVersion`，且 Deployment 镜像与目标不一致 |
| 3 | **EnsureAgentUpgrade** | `Status.OpenFuyaoVersion != Spec.OpenFuyaoVersion`，且 bkeagent-deployer 版本不一致 |
| 4 | **EnsureContainerdUpgrade** | `Spec.ContainerdVersion > Status.ContainerdVersion`（大版本升级） |
| 5 | **EnsureEtcdUpgrade** | `Spec.EtcdVersion != Status.EtcdVersion`，且两者均非空 |
| 6 | **EnsureWorkerUpgrade** | `Spec.KubernetesVersion != Status.KubernetesVersion`，存在需升级的 Worker 节点 |
| 7 | **EnsureMasterUpgrade** | `Spec.KubernetesVersion != Status.KubernetesVersion`，存在需升级的 Master 节点 |
| 8 | **EnsureComponentUpgrade** | `Status.OpenFuyaoVersion != Spec.OpenFuyaoVersion`，存在需升级组件的节点 |
| 9 | **EnsureCluster** | 最终健康检查 |

### ❌ 不会执行的 Phase

| Phase 名称 | 跳过原因 |
|-----------|---------|
| EnsurePaused | 非暂停操作 |
| EnsureClusterManage | 非纳管操作 |
| EnsureDeleteOrReset | 非删除/重置操作 |
| EnsureDryRun | 非DryRun操作 |
| EnsureBKEAgent | 已部署，非首次 |
| EnsureNodesEnv | 已准备，非首次 |
| EnsureClusterAPIObj | 已创建，非首次 |
| EnsureCerts | 已创建，非首次 |
| EnsureLoadBalance | 已配置，非首次 |
| EnsureMasterInit | 已初始化，非首次 |
| EnsureMasterJoin | 非扩容操作 |
| EnsureWorkerJoin | 非扩容操作 |
| EnsureAddonDeploy | 非Addon变更（除非Addon版本变化） |
| EnsureNodesPostProcess | 非首次 |
| EnsureAgentSwitch | 已切换，非首次 |
| EnsureWorkerDelete | 非缩容操作 |
| EnsureMasterDelete | 非缩容操作 |

## 关键发现：升级 Phase 的执行顺序

根据 `PostDeployPhases` 的注册顺序，升级相关 Phase 的**硬编码执行顺序**为：

```
EnsureProviderSelfUpgrade → EnsureAgentUpgrade → EnsureContainerdUpgrade 
→ EnsureEtcdUpgrade → EnsureWorkerUpgrade → EnsureMasterUpgrade 
→ EnsureComponentUpgrade
```

值得注意的是：
1. **Etcd 升级在 Worker/Master 之前**，这是合理的（Etcd 是基础组件）
2. **Worker 升级在 Master 之前**，这与 KEP-5 中 DAG 驱动的拓扑排序可能不同
3. **`ClusterUpgradePhaseNames`**（用于集群状态计算）中**没有包含 `EnsureEtcdUpgrade`**，这意味着 Etcd 升级时集群状态不会显示为 `ClusterUpgrading`，而会落入 `default` 分支显示 `ClusterUnknown`
4. 所有升级 Phase 的 `NeedExecute()` 逻辑都是**各自独立判断**版本差异，没有统一的版本上下文注入机制，这正是 KEP-5 要解决的问题
        
