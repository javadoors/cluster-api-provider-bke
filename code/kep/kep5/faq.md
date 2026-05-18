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
