# BKE 回滚与备份能力设计提案

## 一、现状分析

### 1.1 当前回滚能力现状

**关键发现：BKE 当前没有任何自动化回滚能力**

通过对 BKE 代码库的深入分析，发现：

| 机制 | 回滚能力 | 失败处理方式 |
|------|---------|-------------|
| **PhaseFlow** | ❌ 无 | 设置 `*Failed` 状态，需人工介入 |
| **ClusterVersion** | ❌ 无 | 设置 `Failed` 状态，需人工介入 |
| **声明式 DAG** | ❌ 无 | 记录错误到 `DeclarativeUpgrade.LastError`，需人工介入 |

**PhaseFlow 失败处理**：
- 当 Phase 执行失败时，设置 `PhaseStatus = PhaseFailed`
- 设置 `ClusterStatus = *Failed`（如 `ClusterUpgradeFailed`）
- 执行停止，返回错误
- **唯一恢复手段**：通过 `bke.bocloud.com/retry` 注解触发重试

**ClusterVersion 失败处理**：
- 升级路径验证失败：设置 `Phase = PreCheckFailed`
- ReleaseImage 拉取失败：设置 `Phase = PreCheckFailed`
- **没有自动回滚机制**：升级失败后需要用户手动触发降级

### 1.2 BKE 双机制架构

BKE 采用双机制架构管理集群生命周期：

```
┌─────────────────────────────────────────────────────────────┐
│                    BKE 双机制架构                              │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  机制一：PhaseFlow（阶段流）                                  │
│  ├─ 职责：操作类任务（安装、扩缩容、删除、纳管、Addon部署）     │
│  ├─ 核心组件：PhaseFlow、Phase、PhaseContext                  │
│  ├─ 状态管理：ClusterStatus 字段（状态机）                    │
│  ├─ 执行逻辑：NeedExecute() → Execute() → ReportStatus()    │
│  └─ 失败处理：设置 *Failed 状态，需人工介入                   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  机制二：ClusterVersion（版本管理）                           │
│  ├─ 职责：版本生命周期管理（路径验证、镜像管理）               │
│  ├─ 核心组件：ClusterVersion CRD、UpgradePath CRD           │
│  ├─ 状态管理：ClusterVersion.status.phase                   │
│  ├─ 执行逻辑：验证路径 → 拉取镜像 → 设置 upgrade-ready 注解 │
│  └─ 失败处理：设置 PreCheckFailed，需人工介入               │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  执行器：声明式 DAG（升级执行）                               │
│  ├─ 职责：执行升级/降级操作                                   │
│  ├─ 触发条件：检测到 upgrade-ready 注解                       │
│  ├─ 执行逻辑：构建 DAG → 拓扑排序 → 逐组件执行              │
│  └─ 失败处理：记录错误，需人工介入                           │
└─────────────────────────────────────────────────────────────┘
```

#### 1.3.1 双机制详细说明

**机制一：PhaseFlow（阶段流）**

PhaseFlow 是 BKE 的核心编排引擎，负责管理集群的**操作类任务**。它通过一系列有序的 Phase（阶段）来完成复杂的集群操作。

**核心职责**：
- **安装**：执行 CommonPhases + DeployPhases + PostDeployPhases，完成集群初始化
- **扩缩容**：执行 Master/Worker Join/Delete 阶段，调整节点数量
- **删除**：执行 DeletePhases，清理集群资源
- **纳管**：执行 EnsureClusterManage，导入已有集群
- **Addon 部署**：执行 EnsureAddonDeploy，部署附加组件

**工作原理**：
1. **CalculatePhase()**：根据集群当前状态，计算需要执行的 Phase 列表
2. **Execute()**：按顺序执行每个 Phase
   - **NeedExecute()**：判断该 Phase 是否需要执行
   - **Execute()**：执行 Phase 的核心逻辑
   - **ReportStatus()**：更新 Phase 执行状态
3. **状态管理**：通过 `ClusterStatus` 字段跟踪集群状态（如 `Ready`、`ScalingUp`、`Failed` 等）

**关键特点**：
- **幂等性**：每个 Phase 通过位标志（StateCode）、Kubernetes Conditions 等机制实现幂等性，支持重试
- **状态机驱动**：使用状态机引擎（`statemachine.Engine`）管理状态转换
- **失败处理**：Phase 失败时设置 `*Failed` 状态，需人工介入

**机制二：ClusterVersion（版本管理）**

ClusterVersion 是 BKE 的版本生命周期管理组件，负责管理集群的**版本升级**。它通过独立的 CRD 来跟踪和管理版本信息。

**核心职责**：
- **路径验证**：验证升级路径是否合法（通过 `UpgradePath` CRD）
- **镜像管理**：拉取和管理 ReleaseImage（OCI 镜像）
- **状态跟踪**：跟踪集群版本状态（`Pending`、`Upgrading`、`Ready`、`Failed` 等）
- **升级协调**：设置 `upgrade-ready` 注解，触发 BKECluster 执行升级

**工作原理**：
1. **用户设置目标版本**：`kubectl patch clusterversion --type merge -p '{"spec":{"desiredVersion":"v26.06"}}'`
2. **ClusterVersion Controller 验证**：
   - 查找升级路径（`UpgradePath` CRD）
   - 拉取 ReleaseImage（OCI 镜像）
   - 验证兼容性
3. **设置升级就绪注解**：`cvo.openfuyao.cn/upgrade-ready=<hop-target>`
4. **BKECluster Controller 检测到注解**：触发声明式 DAG 执行升级
5. **升级完成**：清除注解，更新 `ClusterVersion.status`

**关键特点**：
- **声明式**：用户只需设置目标版本，系统自动完成升级
- **路径验证**：通过 `UpgradePath` CRD 确保升级路径合法
- **不直接执行**：ClusterVersion 只负责验证和准备，实际升级由声明式 DAG 执行

**执行器：声明式 DAG**

声明式 DAG 是升级/降级的实际执行器，负责按拓扑顺序执行组件升级。

**工作原理**：
1. **构建 DAG**：根据组件依赖关系构建有向无环图（DAG）
2. **拓扑排序**：确定组件执行顺序（如 Agent → Containerd → etcd → Master → Worker）
3. **逐组件执行**：按顺序执行每个组件的升级逻辑
4. **失败处理**：记录错误到 `DeclarativeUpgrade.LastError`

#### 1.3.2 双机制协作关系

**职责边界**：

| 操作类型 | 负责机制 | 说明 |
|---------|---------|------|
| **安装** | PhaseFlow | 执行安装 Phase 列表 |
| **扩缩容** | PhaseFlow | 执行 Join/Delete Phase |
| **删除** | PhaseFlow | 执行 Delete Phase |
| **纳管** | PhaseFlow | 执行 EnsureClusterManage |
| **Addon 部署** | PhaseFlow | 执行 EnsureAddonDeploy |
| **版本升级** | ClusterVersion + 声明式 DAG | ClusterVersion 验证路径，DAG 执行升级 |
| **版本降级** | ClusterVersion + 声明式 DAG | ClusterVersion 验证路径，DAG 执行降级 |

**协作流程（以升级为例）**：

```
1. 用户设置目标版本
   └─ kubectl patch clusterversion --type merge -p '{"spec":{"desiredVersion":"v26.06"}}'

2. ClusterVersion Controller（机制二）：
   ├─ 验证升级路径（UpgradePath CRD）
   ├─ 拉取 ReleaseImage（OCI 镜像）
   ├─ 设置注解：cvo.openfuyao.cn/upgrade-ready=v26.06
   └─ 不负责执行升级

3. BKECluster Controller 检测到注解：
   ├─ shouldUseDeclarativeUpgrade() 返回 true
   └─ executeUpgradeDAG() 执行声明式 DAG

4. 声明式 DAG（执行器）执行升级：
   ├─ EnsurePreUpgradeResources
   ├─ EnsureAgentUpgrade
   ├─ EnsureContainerdUpgrade
   ├─ EnsureEtcdUpgrade
   ├─ EnsureMasterUpgrade
   └─ EnsureWorkerUpgrade

5. 升级完成：
   ├─ 清除 upgrade-ready 注解
   ├─ 更新 ClusterVersion.status（CompleteUpgradeHop）
   └─ PhaseFlow 跳过已完成的升级 Phase
```

**为什么需要双机制？**

1. **职责分离**：
   - PhaseFlow 专注于**操作编排**（安装、扩缩容、删除等）
   - ClusterVersion 专注于**版本管理**（升级路径验证、镜像管理）
   - 两者职责清晰，互不干扰

2. **灵活性**：
   - PhaseFlow 可以独立管理操作类任务，不受版本升级影响
   - ClusterVersion 可以独立管理版本生命周期，不受操作任务影响
   - 两者可以并行工作（如：在扩容的同时进行版本升级）

3. **可观测性**：
   - PhaseFlow 通过 `ClusterStatus` 字段跟踪操作状态
   - ClusterVersion 通过 `ClusterVersion.status` 跟踪版本状态
   - 两者状态独立，便于问题排查

4. **回滚能力**：
   - PhaseFlow 负责操作类任务的回滚（扩缩容、配置变更等）
   - ClusterVersion 负责版本升级的回滚（通过声明式 DAG 执行降级）
   - 两者协同提供完整的回滚能力

### 1.3 升级流程中的职责分工

```
升级流程：

1. 用户设置 ClusterVersion.spec.desiredVersion

2. ClusterVersion Controller：
   ├─ 验证升级路径（UpgradePath CRD）
   ├─ 拉取 ReleaseImage（OCI 镜像）
   ├─ 设置注解：cvo.openfuyao.cn/upgrade-ready=<hop-target>
   └─ 不负责执行升级

3. BKECluster Controller 检测到注解：
   ├─ shouldUseDeclarativeUpgrade() 返回 true
   └─ executeUpgradeDAG() 执行声明式 DAG

4. 声明式 DAG 执行升级：
   ├─ EnsurePreUpgradeResources
   ├─ EnsureAgentUpgrade
   ├─ EnsureContainerdUpgrade
   ├─ EnsureEtcdUpgrade
   ├─ EnsureMasterUpgrade
   └─ EnsureWorkerUpgrade

5. 升级完成：
   ├─ 清除 upgrade-ready 注解
   ├─ 更新 ClusterVersion.status（CompleteUpgradeHop）
   └─ PhaseFlow 跳过已完成的升级 Phase
```

**关键洞察**：
- ClusterVersion **不负责执行升级**，只负责验证和准备
- 实际升级由 **声明式 DAG** 执行
- PhaseFlow 在声明式升级期间被跳过

---

## 二、升级能力设计提案

### 2.1 升级能力概述

#### 2.1.1 当前升级机制

BKE 采用 **ClusterVersion + 声明式 DAG** 双机制协作完成集群升级：

- **ClusterVersion**：负责版本生命周期管理（升级路径验证、ReleaseImage 拉取）
- **声明式 DAG**：负责按拓扑顺序执行组件升级（Agent → Containerd → etcd → Master → Worker）

升级流程：
1. 用户设置 `ClusterVersion.spec.desiredVersion`
2. ClusterVersion Controller 验证升级路径（UpgradePath CRD）
3. 拉取目标版本 ReleaseImage（OCI 镜像）
4. 设置 `cvo.openfuyao.cn/upgrade-ready` 注解
5. BKECluster Controller 检测到注解，触发声明式 DAG 执行升级
6. 升级完成，清除注解，更新 `ClusterVersion.status`

#### 2.1.2 升级类型

| 升级类型 | 说明 | 示例 | 适用场景 |
|---------|------|------|---------|
| **相邻版本升级** | 升级到下一个季度版本 | 26.03 → 26.06 | 常规升级，变更较小 |
| **LTS 版本升级** | 升级到下一个 LTS 版本（逐跳） | 25.12 → 26.12 | 生产环境大版本升级 |
| **补丁升级** | 同一版本内的补丁更新 | 26.06.1 → 26.06.2 | 安全修复、Bug 修复 |

### 2.2 版本命名与发布策略

#### 2.2.1 版本命名规范

- **格式**：`YY.MM`（年份后两位 + 月份）
- **示例**：`25.12`、`26.03`、`26.06`、`26.09`、`26.12`

#### 2.2.2 版本发布周期

**每季度发布一个版本**：

| 季度 | 发布月份 | 版本示例 |
|------|---------|---------|
| Q1 | 3月 | 26.03 |
| Q2 | 6月 | 26.06 |
| Q3 | 9月 | 26.09 |
| Q4 | 12月 | 26.12 |

#### 2.2.3 LTS 版本策略

- **每年最后一个版本（12月）为 LTS（长期支持）版本**
- 示例：25.12、26.12、27.12 均为 LTS 版本
- LTS 版本提供 3 年支持周期，适合生产环境

#### 2.2.4 版本支持策略

| 版本类型 | 发布周期 | 支持周期 | 说明 |
|---------|---------|---------|------|
| 标准版本 | 每季度 | 6 个月 | 适合追求新特性的用户 |
| LTS 版本 | 每年12月 | 3 年 | 适合追求稳定性的生产环境 |

### 2.3 升级路径设计

#### 2.3.1 UpgradePath CRD

UpgradePath CRD 定义了合法的升级路径。每个 UpgradePath 资源描述了一个合法的版本跳转：

- **起始版本**（from）
- **目标版本**（to）
- **约束条件**（如需要先完成数据迁移）

ClusterVersion Controller 在升级前会查找对应的 UpgradePath 资源，验证升级路径是否合法。

#### 2.3.2 路径一：相邻版本升级

```
26.03 → 26.06 → 26.09 → 26.12
```

- 升级间隔短（3个月）
- 变更较小，风险较低
- 适合追求新特性的用户

#### 2.3.3 路径二：LTS 版本升级

```
25.12 → 26.03 → 26.06 → 26.09 → 26.12 → 27.03 → ... → 27.12
```

- 需要逐跳升级（不能跳过中间版本）
- 升级间隔长（12个月），每一跳变更较小
- 适合追求稳定性的生产环境用户

#### 2.3.4 路径验证规则

1. 目标版本必须在 UpgradePath CRD 中存在合法路径
2. 只能升级到相邻版本或逐跳升级到目标版本
3. 目标版本的 ReleaseImage 必须可用
4. 当前版本与目标版本之间不能有不可逆的数据格式变更

#### 2.3.5 兼容性矩阵

| 当前版本 | 可升级版本 | 说明 |
|---------|-----------|------|
| 26.03 | 26.06 | 相邻版本升级 |
| 26.06 | 26.09, 26.03（回滚） | 相邻版本升级/回滚 |
| 26.09 | 26.12, 26.06（回滚） | 相邻版本升级/回滚 |
| 26.12（LTS） | 27.03, 26.09（回滚） | LTS 版本升级/回滚 |
| 25.12（LTS） | 26.03, 26.12（LTS，需逐跳） | LTS 版本升级（逐跳：25.12 → 26.03 → 26.06 → 26.09 → 26.12） |

### 2.4 升级流程

#### 2.4.1 预检阶段（Pre-Check）

升级前必须完成以下检查，任何一项不通过则阻止升级：

**集群健康检查**：

| 检查项 | 检查内容 | 通过条件 |
|-------|---------|---------|
| 节点状态 | 所有节点处于 Ready 状态 | 无 NotReady 节点 |
| etcd 集群 | etcd 集群健康 | 所有 etcd 成员正常，仲裁满足 |
| 控制面组件 | API Server、Controller Manager、Scheduler 正常 | 所有组件 Healthy |
| BKE Agent | 所有节点上的 BKE Agent 正常 | Agent 心跳正常 |

**备份验证**：

| 检查项 | 检查内容 | 通过条件 |
|-------|---------|---------|
| etcd 备份 | 最近的 etcd 快照是否可用 | 备份文件存在且可恢复 |
| 配置备份 | BKECluster/BKENode 配置是否已备份 | 备份文件存在且完整 |
| 证书备份 | 证书和密钥是否已备份 | 备份文件存在且有效 |

**升级路径验证**：

| 检查项 | 检查内容 | 通过条件 |
|-------|---------|---------|
| 路径合法性 | UpgradePath CRD 中是否存在合法路径 | 路径存在 |
| ReleaseImage 可用性 | 目标版本镜像是否可拉取 | 镜像可访问 |
| 前置条件 | 是否需要先完成其他升级 | 前置条件已满足 |

**资源检查**：

| 检查项 | 检查内容 | 通过条件 |
|-------|---------|---------|
| 磁盘空间 | 各节点磁盘空间是否充足 | 剩余空间 > 30% |
| 内存 | 各节点内存是否充足 | 可用内存 > 20% |
| CPU | 各节点 CPU 是否充足 | 可用 CPU > 20% |
| 网络连通性 | 节点间网络是否正常 | 网络延迟 < 100ms |

**其他检查**：

| 检查项 | 检查内容 | 通过条件 |
|-------|---------|---------|
| 证书有效期 | TLS 证书是否在有效期内 | 有效期 > 30天 |
| 配置一致性 | 集群配置是否与预期一致 | 无未预期的配置漂移 |
| 无进行中的操作 | 是否有其他操作正在进行 | 无扩缩容/升级/回滚进行中 |

#### 2.4.2 升级执行阶段

预检通过后，ClusterVersion Controller 设置 `upgrade-ready` 注解，BKECluster Controller 触发声明式 DAG 执行升级：

1. 解析目标版本 ReleaseImage
2. 构建升级 DAG（Agent → Containerd → etcd → Master → Worker）
3. 按拓扑顺序执行组件升级
4. 每个组件升级完成后执行健康检查
5. 健康检查通过后继续下一个组件
6. 健康检查失败则暂停升级，等待人工介入

#### 2.4.3 升级后验证阶段（Post-Upgrade Verification）

升级完成后执行以下验证：

| 验证项 | 验证内容 | 预期结果 |
|-------|---------|---------|
| 组件版本 | 所有组件版本一致 | 均为目标版本 |
| 集群健康 | 所有节点 Ready | 节点状态正常 |
| etcd 健康 | etcd 集群正常 | 仲裁满足，成员健康 |
| API Server | API Server 可访问 | 响应正常 |
| 业务应用 | 关键 Pod 运行正常 | Pod 状态 Running |
| 回滚能力 | 确认可以回滚到上一版本 | 回滚路径存在 |

### 2.5 升级策略与组件顺序

#### 2.5.1 组件升级顺序

```
Agent → Containerd → etcd → Master → Worker
```

**顺序说明**：
- **Agent 优先**：Agent 负责协调升级过程，必须先升级
- **Containerd 次之**：容器运行时是基础组件，需要在 kubelet 之前升级
- **etcd 第三**：数据存储层需要在控制面组件之前升级
- **Master 第四**：控制面组件需要在 Worker 之前升级
- **Worker 最后**：工作节点最后升级，确保业务影响最小

#### 2.5.2 各组件升级策略

| 组件 | 升级方式 | 说明 |
|------|---------|------|
| **BKE Agent** | 滚动升级 | SSH 推送新版本二进制，逐个节点重启服务 |
| **Containerd** | 滚动升级 | 驱逐容器后升级，升级后重新调度 |
| **etcd** | 逐节点升级 | 逐个 etcd 成员升级，确保仲裁不丢失 |
| **Master 组件** | 滚动升级 | 逐个 Master 节点升级静态 Pod（API Server、Controller Manager、Scheduler） |
| **Worker 组件** | 滚动升级 | 驱逐 Pod → 停止 kubelet → 升级 → 重启 |

#### 2.5.3 组件版本倾斜策略

| 约束 | 说明 |
|------|------|
| **Kubernetes 版本倾斜** | kubelet 可以与 API Server 相差 ±1 个小版本 |
| **etcd 兼容性** | etcd 数据格式可能在不同版本间变化，升级前需确认兼容性 |
| **Containerd CRI** | CRI API 在 containerd 和 kubelet 之间需要兼容 |
| **证书有效性** | 证书必须对所有组件有效 |
| **Agent 版本** | Agent 应该与管理集群 API 兼容 |

### 2.6 停机窗口分析

#### 2.6.1 各组件升级停机影响

| 组件 | 控制面影响 | 工作节点影响 | 业务应用影响 |
|------|-----------|-------------|-------------|
| **BKE Agent** | 无 | 无 | 无 |
| **Containerd** | 无 | 节点上容器短暂重启 | 该节点上的 Pod 短暂不可用 |
| **etcd** | etcd 逐个成员升级，集群仍可用 | 无直接影响 | API Server 可能短暂延迟增加 |
| **Master 组件** | API Server 逐个升级 | 无直接影响 | 多副本时 API 仍可用，单副本时短暂不可用 |
| **Worker 组件** | 无 | 逐个节点升级 | Pod 被驱逐后在其他节点重建 |

#### 2.6.2 停机时间最小化策略

1. **Pod 干扰预算（PDB）**：为关键业务应用配置 PDB，确保升级期间最小可用副本数
2. **滚动升级**：逐个组件升级，避免同时升级多个组件
3. **多副本部署**：Master 和 etcd 至少 3 节点，确保升级期间集群可用
4. **业务应用多副本**：关键业务应用配置多副本和反亲和性，避免单点故障
5. **维护窗口**：对于单副本控制面，建议在维护窗口期间升级

#### 2.6.3 停机窗口汇总

| 升级场景 | 控制面影响 | 业务影响 | 建议 |
|---------|-----------|---------|------|
| **多副本集群升级** | 无停机 | 无停机（滚动升级） | 正常升级 |
| **单副本集群升级** | 短暂不可用 | 短暂不可用 | 建议维护窗口 |
| **LTS 跨版本升级** | 多次短暂不可用 | 多次短暂不可用 | 建议维护窗口，充分测试 |

### 2.7 升级能力增强工作量评估

| 任务 | 工作量(人月) | 说明 |
|------|-------------|------|
| 升级前置检查自动化 | 0.5 | 实现 Pre-Check 自动化（集群健康、备份验证、路径验证） |
| 升级后验证自动化 | 0.5 | 实现 Post-Upgrade 自动化验证（组件健康、版本一致性） |
| UpgradePath CRD 增强 | 0.5 | 支持跨版本路径验证、兼容性矩阵管理 |
| 停机窗口最小化策略 | 0.5 | PDB 集成、滚动升级策略优化 |
| 升级文档与最佳实践 | 0.3 | 升级操作手册、升级前检查清单 |
| **小计** | **2.3** | |

---

## 三、回滚能力设计提案

### 3.1 设计目标

为 BKE 构建完整的回滚能力，覆盖以下场景：

| 场景 | 优先级 | 回滚策略 | 负责机制 |
|------|--------|---------|---------|
| **升级失败** | P0 | 版本回滚（降级） | ClusterVersion（验证路径）+ 声明式 DAG（执行降级） |
| **扩缩容失败** | P1 | 状态回滚 + 资源清理 | PhaseFlow（状态机回滚 + 资源清理） |
| **配置变更失败** | P1 | 配置回滚 | PhaseFlow（状态机回滚 + 配置恢复） |
| **删除失败** | P1 | 不支持回滚（不可逆操作） | 重试删除 / 强制删除 / 手动清理 |
| **安装失败** | P0 | 清理重建（状态不可逆） | 不支持自动回滚，需人工清理重建 |

**机制职责划分**：
- **PhaseFlow**：负责操作类任务（扩缩容、配置变更）的回滚，通过状态机引擎实现状态转换和资源清理
- **ClusterVersion**：负责版本升级的回滚验证，设置回滚就绪注解，不直接执行回滚
- **声明式 DAG**：负责执行降级操作，按相反顺序降级所有组件

**重要说明**：
- **删除操作不支持回滚**：删除是不可逆操作，已删除的资源无法恢复。删除失败后只能通过重试删除、强制删除或手动清理来完成删除操作，最终状态是 `Deleted`，而不是 `Ready`
- **安装操作不支持回滚**：安装过程创建的基础设施状态不可逆，安装失败后需要清理重建

#### 3.1.1 升级失败场景说明

**负责机制**：
- **ClusterVersion**：验证回滚路径（v26.06 → v26.05），拉取旧版本 ReleaseImage
- **声明式 DAG**：执行降级操作，按相反顺序降级所有组件（Worker → Master → etcd → Containerd → Agent）

**场景描述**：
集群从版本 v26.05 升级到 v26.06 过程中，某个组件升级失败，导致集群处于不一致状态。

**失败原因**：
- ReleaseImage 拉取失败（网络问题、镜像损坏）
- 组件升级失败（Agent、Containerd、etcd、Master、Worker）
- 升级路径验证失败（不兼容的版本组合）
- 资源不足（CPU、内存、磁盘空间不足）
- 健康检查失败（升级后组件无法正常启动）

**当前状态**：
- ClusterVersion.status.phase = `Failed` 或 `PreCheckFailed`
- ClusterStatus = `ClusterUpgradeFailed`
- 部分组件可能已升级到新版本，部分仍在旧版本
- 集群可能处于不可用状态

**回滚策略**：

BKE 提供两种回滚方案，可根据实际场景选择：

##### 方案一：降级 DAG（推荐用于复杂场景）

**设计思路**：
- 参考升级 DAG 的设计，实现专门的降级 DAG
- 为每个组件实现特定的降级逻辑（数据迁移、配置回滚等）
- 按相反顺序执行降级（Worker → Master → etcd → Containerd → Agent）

**执行流程**：
1. ClusterVersion 验证回滚路径（v26.06 → v26.05）
2. 拉取旧版本 ReleaseImage
3. BKECluster 控制器执行降级 DAG
4. 按相反顺序降级所有组件：
   - EnsureWorkerDowngrade：停止 kubelet → 回滚配置 → 重新安装旧版本 → 启动 → 验证
   - EnsureMasterDowngrade：停止控制面组件 → 回滚配置 → 重新安装 → 启动 → 验证
   - EnsureEtcdDowngrade：备份数据 → 停止 etcd → 回滚数据 → 重新安装 → 启动 → 验证
   - EnsureContainerdDowngrade：停止 containerd → 回滚配置 → 重新安装 → 启动 → 验证
   - EnsureAgentDowngrade：停止 Agent → 回滚配置 → 重新安装 → 启动 → 验证
5. 更新 ClusterVersion.status.currentVersion = v26.05
6. ClusterStatus = `Ready`

**核心代码示例**：
```go
// bkecluster_rollback_dag.go

func (r *BKEClusterReconciler) executeRollbackDAG(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) (ctrl.Result, error) {
    // 1. 解析 ReleaseImage
    releaseBundle, err := r.resolveReleaseBundle(ctx, targetVersion)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. 构建降级 DAG（与升级 DAG 顺序相反）
    dag := r.buildRollbackDAG(releaseBundle)
    // DAG 节点顺序：Worker → Master → etcd → Containerd → Agent
    
    // 3. 执行降级 DAG
    if err := r.executeDAG(ctx, bkeCluster, dag); err != nil {
        bkeCluster.Status.DeclarativeUpgrade.LastError = err.Error()
        return ctrl.Result{}, err
    }
    
    // 4. 完成降级
    return r.completeDeclarativeRollback(ctx, bkeCluster, targetVersion)
}

// 降级处理函数示例
func EnsureWorkerDowngrade(ctx context.Context, cluster *bkev1beta1.BKECluster) error {
    // 1. 停止 Worker 节点上的 kubelet
    // 2. 回滚 Worker 节点配置
    // 3. 重新安装旧版本 kubelet
    // 4. 启动 kubelet
    // 5. 验证 Worker 节点状态
    return nil
}
```

**优点**：
- ✅ 精确控制每个组件的降级过程
- ✅ 可以针对每个组件实现特定的降级逻辑（如数据迁移、配置回滚）
- ✅ 可以处理组件间的依赖关系
- ✅ 可以实现部分降级（只降级需要降级的组件）
- ✅ 降级过程可观测、可控制

**缺点**：
- ❌ 实现复杂，需要为每个组件编写降级代码
- ❌ 需要维护降级 DAG 的拓扑关系
- ❌ 测试工作量大
- ❌ 某些组件可能不支持降级（如 etcd 数据格式变更不可逆）

**适用场景**：
- 组件版本间有数据格式变更，需要特殊处理
- 需要精确控制降级过程
- 对降级时间有严格要求

##### 方案二：复用升级流程执行降级（推荐用于快速交付）

**设计思路**：
- 参考 OpenShift CVO 机制：回滚本质是"降级"，复用现有升级流程
- 设置目标版本为旧版本，CVO 按正常升级流程反向执行
- 不需要为每个组件编写专门的降级代码
- 通过重新应用旧版本的 manifest 和配置来实现降级

**执行流程**：
1. ClusterVersion 验证回滚路径（v26.06 → v26.05）
2. 拉取旧版本 ReleaseImage
3. BKECluster 控制器执行重新部署
4. 复用部署逻辑，重新部署所有组件：
   - deployAgent：部署旧版本 Agent
   - deployContainerd：部署旧版本 Containerd
   - deployEtcd：部署旧版本 etcd
   - deployMaster：部署旧版本 Master 组件
   - deployWorker：部署旧版本 Worker 组件
5. 等待所有组件就绪
6. 更新 ClusterVersion.status.currentVersion = v26.05
7. ClusterStatus = `Ready`

**核心代码示例**：
```go
// bkecluster_controller.go

func (r *BKEClusterReconciler) executeRollback(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) (ctrl.Result, error) {
    // 1. 解析旧版本 ReleaseImage
    releaseBundle, err := r.resolveReleaseBundle(ctx, targetVersion)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. 复用升级流程执行降级（复用部署逻辑）
    if err := r.applyReleaseBundle(ctx, bkeCluster, releaseBundle); err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 等待所有组件就绪
    if err := r.waitForComponentsReady(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    // 4. 完成回滚
    return r.completeRollback(ctx, bkeCluster, targetVersion)
}

// applyReleaseBundle 复用部署逻辑
func (r *BKEClusterReconciler) applyReleaseBundle(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    releaseBundle *ReleaseBundle,
) error {
    // 1. 部署 Agent
    if err := r.deployAgent(ctx, bkeCluster, releaseBundle.Agent); err != nil {
        return err
    }
    
    // 2. 部署 Containerd
    if err := r.deployContainerd(ctx, bkeCluster, releaseBundle.Containerd); err != nil {
        return err
    }
    
    // 3. 部署 etcd
    if err := r.deployEtcd(ctx, bkeCluster, releaseBundle.Etcd); err != nil {
        return err
    }
    
    // 4. 部署 Master 组件
    if err := r.deployMaster(ctx, bkeCluster, releaseBundle.Master); err != nil {
        return err
    }
    
    // 5. 部署 Worker 组件
    if err := r.deployWorker(ctx, bkeCluster, releaseBundle.Worker); err != nil {
        return err
    }
    
    return nil
}
```

**优点**：
- ✅ 实现简单，复用现有部署逻辑
- ✅ 不需要为每个组件编写降级代码
- ✅ 与升级流程对称，易于理解和维护
- ✅ 实现工作量小，测试工作量小
- ✅ 可以处理大部分回滚场景

**缺点**：
- ❌ 可能需要更长时间（需要重新部署所有组件）
- ❌ 某些状态可能无法完全回滚（如 etcd 数据格式变更不可逆）
- ❌ 需要确保旧版本的 ReleaseImage 可用
- ❌ 某些组件可能不支持"重新部署"（如已经修改了用户配置）

**适用场景**：
- 组件版本间没有数据格式变更
- 对回滚时间要求不严格
- 希望快速实现回滚能力

##### 方案对比与建议

| 维度 | 方案一：降级 DAG | 方案二：重新部署 |
|------|----------------|----------------|
| **实现复杂度** | 高（需要为每个组件编写降级逻辑） | 低（复用部署逻辑） |
| **实现工作量** | 大（预计 2-3 周） | 小（预计 1 周） |
| **回滚时间** | 快（只降级需要降级的组件） | 慢（需要重新部署所有组件） |
| **精确度** | 高（可以精确控制每个组件的降级） | 中（重新部署可能覆盖用户配置） |
| **可靠性** | 中（降级逻辑需要充分测试） | 高（复用已验证的部署逻辑） |
| **适用场景** | 组件版本间有数据格式变更 | 组件版本间没有数据格式变更 |
| **维护成本** | 高（需要维护降级 DAG） | 低（与升级流程对称） |

**推荐实施策略**：

**阶段一（P0）**：实现方案二（重新部署）
- 快速交付回滚能力
- 覆盖大部分回滚场景
- 实现工作量小，风险低

**阶段二（P1）**：根据实际使用情况，决定是否实现方案一
- 如果发现某些场景方案二无法满足（如 etcd 数据格式变更），再实现方案一
- 方案一可以作为方案二的补充，处理特殊场景

**回滚目标**：
- 所有组件恢复到 v26.05 版本
- 集群状态恢复到 `Ready`
- 业务应用恢复正常运行

**影响范围**：
- 控制面组件（API Server、etcd、Controller Manager、Scheduler）
- 节点组件（kubelet、containerd、BKE Agent）
- 业务应用（短暂不可用，降级完成后恢复）

**预计恢复时间**：15-30 分钟

#### 3.1.2 扩缩容失败场景说明

**负责机制**：
- **PhaseFlow**：通过状态机引擎执行回滚转换（`ScaleFailed → Ready`），调用清理动作删除失败资源、恢复节点池配置

**场景描述**：
集群扩容（增加 Worker 节点）或缩容（删除 Worker 节点）过程中，操作失败，导致节点处于不一致状态。

**失败原因**：
- 云资源创建失败（VM、网络、存储）
- 节点初始化失败（kubelet 启动失败、证书签发失败）
- 节点加入集群失败（CSR 审批失败、网络不通）
- 节点删除失败（Pod 驱逐超时、PVC 删除失败）
- MachineDeployment 更新失败（副本数不一致）

**当前状态**：
- ClusterStatus = `ClusterScaleFailed`
- LastInProgressState = `WorkerScalingUp` 或 `WorkerScalingDown`
- 可能存在未就绪的节点（扩容失败）
- 可能存在待删除的节点（缩容失败）

**回滚策略**：
1. 触发 PhaseFlow 回滚（通过 `bke.bocloud.com/rollback=true` 注解）
2. 状态机引擎执行回滚转换：`ScaleFailed → Ready`
3. 执行资源清理动作：
   - 扩容失败：删除未就绪的节点、清理相关 ConfigMap/Secret
   - 缩容失败：恢复 MachineDeployment 副本数、重新加入节点
4. 清除回滚注解
5. ClusterStatus = `Ready`

**回滚目标**：
- 节点数量恢复到操作前状态
- 所有节点处于 `Ready` 状态
- MachineDeployment 副本数一致

**影响范围**：
- 新增/删除的节点
- MachineDeployment 资源
- 业务应用（节点上的 Pod 可能被驱逐）

**预计恢复时间**：5-15 分钟

#### 3.1.3 配置变更失败场景说明

**负责机制**：
- **PhaseFlow**：通过状态机引擎执行回滚转换（`ManageFailed → Ready`），从备份恢复配置文件、删除错误配置、重启受影响组件

**场景描述**：
集群配置变更（如纳管新集群、修改集群参数）过程中，配置应用失败，导致集群配置不一致。

**失败原因**：
- 配置验证失败（参数不合法、冲突的配置）
- 配置应用失败（ConfigMap/Secret 创建失败）
- 组件重启失败（配置变更后组件无法启动）
- 网络配置失败（Service、Ingress 配置错误）

**当前状态**：
- ClusterStatus = `ClusterManageFailed`
- 部分配置可能已应用，部分未应用
- 集群可能处于部分可用状态

**回滚策略**：
1. 触发 PhaseFlow 回滚（通过 `bke.bocloud.com/rollback=true` 注解）
2. 状态机引擎执行回滚转换：`ManageFailed → Ready`
3. 执行配置恢复动作：
   - 从备份恢复配置文件
   - 删除错误的 ConfigMap/Secret
   - 重启受影响的组件
4. 清除回滚注解
5. ClusterStatus = `Ready`

**回滚目标**：
- 配置恢复到变更前状态
- 所有组件正常运行
- 集群状态恢复到 `Ready`

**影响范围**：
- ConfigMap、Secret 资源
- 受影响的组件（可能需要重启）
- 业务应用（配置变更后可能需要重新加载）

**预计恢复时间**：3-10 分钟

#### 3.1.4 安装失败场景说明

**负责机制**：
- **不支持自动回滚**：安装过程创建的基础设施（etcd、证书、网络）状态不可逆，无法通过状态机回滚
- **人工清理重建**：提供自动化清理脚本，删除所有已创建资源后重新安装

**场景描述**：
集群安装过程中，某个阶段失败，导致集群无法完成初始化。

**失败原因**：
- Bootstrap 节点初始化失败
- etcd 集群初始化失败
- 控制面组件启动失败（API Server、Controller Manager、Scheduler）
- 证书签发失败（CA 证书、服务证书）
- 网络配置失败（CNI 插件初始化失败）
- 节点加入失败（Worker 节点无法加入集群）

**当前状态**：
- ClusterStatus = `ClusterInitializationFailed`
- 部分组件可能已安装，部分未安装
- 集群处于不可用状态

**回滚策略**：
**不支持自动回滚**，原因：
- 安装过程创建的基础设施（etcd、证书、网络）状态不可逆
- 主机已安装组件和配置，无法简单回滚
- 重建比回滚更快、更可靠

**推荐处理方式**：
1. **清理并重建**（推荐）：
   - 执行清理脚本，删除所有已创建的资源
   - 验证资源完全删除
   - 重新执行安装流程
   - 预计时间：10-30 分钟

2. **部分恢复**（特定场景）：
   - 适用条件：控制面已就绪、etcd 集群健康、节点已加入
   - 诊断失败原因
   - 修复问题
   - 重试失败的步骤
   - 预计时间：5-15 分钟

**自动化清理脚本范围**：

清理脚本负责删除安装过程中创建的所有资源，具体范围如下：

| 资源类型 | 清理范围 | 清理方式 | 说明 |
|---------|---------|---------|------|
| **Kubernetes 资源** | BKECluster CR | `kubectl delete bkecluster` | 删除集群 CR 及其关联资源 |
| | BKENode CR | `kubectl delete bkenode --all` | 删除所有节点 CR |
| | 相关 ConfigMap | 自动级联删除 | 集群配置、证书配置等 |
| | 相关 Secret | 自动级联删除 | 证书、密钥、Token 等 |
| | 相关 Service | 自动级联删除 | API Server Service 等 |
| **云资源（可选）** | VM/实例 | 通过云 API 删除 | 如果使用了云主机，可删除 |
| | 负载均衡器 | 通过云 API 删除 | 如果创建了 LB，可删除 |
| | 存储卷 | 通过云 API 删除 | 如果创建了云盘，可删除 |
| | 网络资源 | 通过云 API 删除 | 如果创建了 VPC 等，可删除 |
| **本地资源** | 证书文件 | `rm -rf /etc/bke/${CLUSTER_NAME}/pki/` | CA 证书、服务证书、密钥 |
| | 配置文件 | `rm -rf /etc/bke/${CLUSTER_NAME}/config/` | kubelet 配置、containerd 配置等 |
| | etcd 数据 | `rm -rf /var/lib/etcd/*` | etcd 数据目录 |
| | 日志文件 | `rm -rf /var/log/bke/*` | 组件日志 |

**清理脚本不负责的范围**（需手动处理）：

| 资源类型 | 说明 | 处理方式 |
|---------|------|---------|
| **业务应用数据** | 如果安装过程中已部署测试应用 | 手动删除应用及其 PVC |
| **外部依赖** | 外部 DNS 记录、外部负载均衡 | 手动清理外部系统配置 |
| **监控数据** | Prometheus、Grafana 中的监控数据 | 可选清理，不影响重新安装 |
| **备份数据** | 如果已创建备份 | 保留或删除（根据需求） |

**清理脚本执行流程**：

```
1. 预检查
   ├─ 确认集群名称和区域
   ├─ 检查是否有正在运行的安装任务
   └─ 提示用户确认清理操作

2. 停止集群组件
   ├─ 停止所有节点上的 kubelet
   ├─ 停止所有节点上的 containerd
   └─ 停止所有节点上的 BKE Agent

3. 删除 Kubernetes 资源
   ├─ 删除 BKECluster CR（触发级联删除）
   ├─ 等待关联资源删除完成
   └─ 验证资源已删除

4. 删除云资源（可选，如果使用了云资源）
   ├─ 删除 VM/实例
   ├─ 删除负载均衡器
   ├─ 删除存储卷
   ├─ 删除网络资源
   └─ 等待云资源删除完成

5. 清理本地资源
   ├─ 删除证书文件
   ├─ 删除配置文件
   ├─ 删除 etcd 数据
   └─ 删除日志文件

6. 验证清理完成
   ├─ 检查 Kubernetes 资源是否已删除
   ├─ 检查云资源是否已删除（可选）
   ├─ 检查本地文件是否已清理
   └─ 输出清理报告
```

**清理脚本示例**：

```bash
#!/bin/bash
# bke-install-cleanup.sh

CLUSTER_NAME=$1
REGION=$2

echo "Starting cleanup for cluster: ${CLUSTER_NAME}"

# 1. 删除 BKECluster 资源（触发级联删除）
kubectl delete bkecluster ${CLUSTER_NAME} -n bke-system --wait=true

# 2. 删除节点资源
kubectl delete bkenode --all -n bke-system --wait=true

# 3. 删除云资源（可选，如果使用了云资源）
# - 删除 VM/实例
# - 删除负载均衡器
# - 删除存储卷
# - 删除网络资源

# 4. 删除证书和配置
rm -rf /etc/bke/${CLUSTER_NAME}
rm -rf /var/lib/etcd/*
rm -rf /var/log/bke/*

# 5. 验证清理完成
echo "Verifying cleanup..."
kubectl get bkecluster ${CLUSTER_NAME} -n bke-system
if [ $? -eq 0 ]; then
    echo "ERROR: Cluster still exists"
    exit 1
fi

echo "Cleanup completed successfully"
```

**影响范围**：
- 所有已创建的基础设施资源
- 云资源（VM、网络、存储）
- 证书和配置文件

**预计恢复时间**：10-30 分钟（清理重建）

### 3.2 PhaseFlow 回滚设计

#### 3.2.1 设计思路

##### 重试 vs 回滚的本质区别

BKE 当前已实现完善的**重试机制**，但**回滚机制**是新增能力。理解两者的本质区别是设计回滚能力的前提：

| 维度 | 重试（Retry） | 回滚（Rollback） |
|------|--------------|-----------------|
| **目标** | 重新尝试**同一个失败的操作** | **撤销**操作，返回到之前的稳定状态 |
| **方向** | **向前** - 继续朝原始目标前进 | **向后** - 撤销变更，返回安全状态 |
| **状态转换** | `*Failed → InProgress`（如 `ScaleFailed → WorkerScalingUp`） | `*Failed → Ready`（如 `ScaleFailed → Ready`） |
| **数据/资源影响** | 保留部分进度（位标志、条件） | 丢弃部分进度，清理已创建资源 |
| **适用场景** | 瞬时故障、外部依赖问题 | 根本性失败、状态不一致 |
| **当前状态** | ✅ 已实现（三层重试架构） | ❌ 未实现（本提案设计） |

##### BKE 三层重试架构

BKE 当前已实现三层重试机制，理解这个架构有助于明确回滚的定位：

```
┌─────────────────────────────────────────────────────────────────────┐
│  L1: controller-runtime workqueue rate limiter                      │
│  ├─ 范围：单次 reconcile 错误                                       │
│  ├─ 触发：自动（错误返回时）                                        │
│  ├─ 次数：无限（FastSlowRateLimiter 退避）                          │
│  └─ 目的：处理瞬时错误（网络抖动、API Server 短暂不可用）            │
├─────────────────────────────────────────────────────────────────────┤
│  L2: StatusManager 失败计数器                                       │
│  ├─ 范围：BKECluster/BKENode 状态级别                               │
│  ├─ 触发：自动（*Failed 状态）                                      │
│  ├─ 次数：默认 10 次（ReconcileAllowedFailedCount）                 │
│  ├─ 行为：状态伪装（显示正常状态）+ 自动重新入队                    │
│  └─ 超限：设置 NodeFailedFlag，停止自动协调                         │
├─────────────────────────────────────────────────────────────────────┤
│  L3: Retry 注解（手动）                                             │
│  ├─ 范围：失败节点级别                                              │
│  ├─ 触发：手动（kubectl annotate bke.bocloud.com/retry=...）        │
│  ├─ 动作：清除 NodeFailedFlag + 重置 StatusManager 缓存             │
│  └─ 结果：Phase 重新评估 NeedExecute()，从检查点恢复                │
└─────────────────────────────────────────────────────────────────────┘
```

**重试的工作原理**：

1. **Phase 幂等性设计**：每个 Phase 通过位标志（StateCode）、Kubernetes Conditions、资源存在性检查等机制实现幂等性
2. **检查点恢复**：重试时跳过已完成的子步骤，只重新执行失败的子步骤及后续步骤
3. **状态伪装**：前 10 次失败时，StatusManager 将 `*Failed` 状态伪装为上一个正常状态，对外表现为"仍在进行中"

**示例**：`EnsureBKEAgent` Phase 通过 `NodeAgentPushedFlag` 位标志记录 Agent 推送状态：
```go
if node.StateCode&bkev1beta1.NodeAgentPushedFlag != 0 {
    return false  // 已推送，跳过
}
```

##### 回滚的定位：重试失败后的最后手段

回滚不是重试的替代品，而是**重试失败后的最后手段**。决策流程如下：

```
操作失败
  ↓
L1 自动重试（瞬时故障）
  ↓ 仍失败
L2 自动重试（最多 10 次）
  ↓ 仍失败
L3 手动重试（修复根因后）
  ↓ 仍失败
回滚（放弃操作，返回安全状态）
```

**什么时候重试？**
- 失败是**瞬时的**（网络超时、临时资源短缺）
- 失败是**外部的**（SSH 连接失败、包下载错误）
- 部分进度**有效且可复用**（etcd 已初始化、证书已生成）
- 已经**修复了根本原因**（修复网络、释放磁盘空间、纠正配置）
- 操作处于**早中期**，重新执行是安全的

**什么时候回滚？**
- 失败是**根本性的**（版本不兼容、配置错误）
- 部分状态**不一致或损坏**（组件升级一半）
- 需要**快速返回已知良好状态**
- 重试多次仍然失败
- 操作处于**后期**，部分进度不可用

##### 回滚的核心原则

1. **清理副作用**：回滚必须清理操作过程中创建的所有资源（节点、ConfigMap、Secret 等）
2. **恢复一致状态**：回滚后集群必须处于一致状态，不能有残留的中间状态
3. **幂等性**：回滚操作本身必须是幂等的，可以安全地重复执行
4. **可观测性**：回滚过程必须有清晰的日志和事件记录，便于问题排查

#### 3.2.2 支持的回滚场景

**PhaseFlow 回滚场景总览**：

| 场景 | 失败状态 | 回滚目标状态 | 回滚策略 | 预计时间 |
|------|---------|-------------|---------|---------|
| 扩容失败 | ClusterScaleFailed | ClusterReady | 清理失败资源，恢复节点池 | 5-15分钟 |
| 缩容失败 | ClusterScaleFailed | ClusterReady | 恢复节点，重新加入集群 | 5-15分钟 |
| 配置变更失败 | ClusterManageFailed | ClusterReady | 恢复配置文件，重启组件 | 3-10分钟 |
| 删除失败 | ClusterDeleteFailed | - | ❌ 不支持回滚 | - |
| 安装失败 | ClusterInitializationFailed | - | ❌ 不支持回滚 | - |

**支持回滚的场景**：

1. **扩容失败回滚**
   - 失败状态：ClusterScaleFailed
   - 回滚目标：ClusterReady
   - 回滚动作：删除未就绪节点、清理 ConfigMap/Secret、恢复 MachineDeployment 副本数
   - 适用条件：重试 3 次以上仍失败

2. **缩容失败回滚**
   - 失败状态：ClusterScaleFailed
   - 回滚目标：ClusterReady
   - 回滚动作：恢复 MachineDeployment 副本数、重新加入节点
   - 适用条件：重试 3 次以上仍失败

3. **配置变更失败回滚**
   - 失败状态：ClusterManageFailed
   - 回滚目标：ClusterReady
   - 回滚动作：从备份恢复配置、删除错误配置、重启组件
   - 适用条件：重试 3 次以上仍失败

**不支持回滚的场景**：

1. **删除失败**
   - 原因：删除是不可逆操作，已删除的资源无法恢复
   - 处理方式：重试删除 / 强制删除 / 手动清理
   - 最终状态：Deleted（而非 Ready）

2. **安装失败**
   - 原因：安装过程创建的基础设施（etcd、证书、网络）状态不可逆
   - 处理方式：清理重建
   - 最终状态：重新安装

**回滚决策树**：

```
操作失败
  ↓
判断操作类型
  ├─ 扩容/缩容 → 重试 → 仍失败 → 回滚到 Ready
  ├─ 配置变更 → 重试 → 仍失败 → 回滚到 Ready
  ├─ 删除 → 重试/强制删除/手动清理 → 完成删除
  └─ 安装 → 清理重建
```

#### 3.2.3 回滚场景详细说明

##### 场景 1：扩缩容失败回滚

**失败原因分析**：
- 云资源创建失败（VM、网络、存储）
- 节点初始化失败（kubelet 启动失败、证书签发失败）
- 节点加入集群失败（CSR 审批失败、网络不通）
- MachineDeployment 更新失败（副本数不一致）

**重试策略（先尝试修复并重试）**：
1. 诊断失败原因（查看节点状态、事件日志）
2. 修复根因（修复网络、释放资源、纠正配置）
3. 使用 `bke.bocloud.com/retry` 注解触发重试
4. Phase 从检查点恢复，重新执行失败的子步骤

**回滚策略（重试失败后）**：
1. 触发回滚（`bke.bocloud.com/rollback=true` 注解）
2. 状态机执行回滚转换：`ScaleFailed → Ready`
3. 执行清理动作：
   - **扩容失败**：删除未就绪的节点、清理相关 ConfigMap/Secret、恢复 MachineDeployment 副本数
   - **缩容失败**：恢复 MachineDeployment 副本数、重新加入节点
4. 清除回滚注解
5. 集群恢复到 Ready 状态

**状态转换**：
```
ClusterScaleFailed → ClusterReady
  Trigger: "Rollback"
  Condition: isScaleRollbackComplete
  Action: cleanupFailedScaleResources
```

**预计恢复时间**：5-15 分钟

##### 场景 2：配置变更失败回滚

**失败原因分析**：
- 配置验证失败（参数不合法、冲突的配置）
- 配置应用失败（ConfigMap/Secret 创建失败）
- 组件重启失败（配置变更后组件无法启动）
- 网络配置失败（Service、Ingress 配置错误）

**重试策略**：
1. 诊断失败原因（查看配置差异、组件日志）
2. 修复配置错误
3. 使用 `bke.bocloud.com/retry` 注解触发重试
4. Phase 从检查点恢复，重新应用配置

**回滚策略**：
1. 触发回滚（`bke.bocloud.com/rollback=true` 注解）
2. 状态机执行回滚转换：`ManageFailed → Ready`
3. 执行配置恢复动作：
   - 从备份恢复配置文件
   - 删除错误的 ConfigMap/Secret
   - 重启受影响的组件
4. 清除回滚注解
5. 集群恢复到 Ready 状态

**状态转换**：
```
ClusterManageFailed → ClusterReady
  Trigger: "Rollback"
  Condition: isManageRollbackComplete
  Action: restorePreviousConfig
```

**预计恢复时间**：3-10 分钟

##### 场景 3：删除失败处理

**重要说明**：删除操作是**不可逆操作**，删除失败后**不支持回滚**。已删除的资源无法恢复，因此删除失败不属于回滚场景，而是**重试/恢复场景**。

**失败原因分析**：
- Pod 驱逐超时（PDB 阻止驱逐、Pod 无法终止）
- PVC 删除失败（仍有 Pod 在使用）
- 云资源删除失败（API 调用失败、资源被锁定）
- 网络清理失败（负载均衡器删除失败）

**为什么删除失败不支持回滚？**

1. **删除的不可逆性**：
   - 已删除的资源无法恢复
   - 无法"撤销"删除操作
   - 回滚的本质是"恢复到操作前状态"，但删除操作已部分完成，无法恢复

2. **状态不一致**：
   - 删除失败后，集群处于不一致状态（部分资源已删除，部分未删除）
   - 无法恢复到真正的 Ready 状态
   - 只能继续完成删除或手动清理残留资源

3. **正确的处理方式**：
   - 重试删除（修复问题后继续删除）
   - 强制删除（跳过阻塞资源）
   - 手动清理（人工介入清理残留资源）

**处理策略**：

**策略 1：重试删除（推荐）**
1. 诊断失败原因（查看 Pod 状态、PVC 使用情况、云资源状态）
2. 修复问题：
   - 删除阻塞的 Pod（`kubectl delete pod <pod> --force --grace-period=0`）
   - 解除 PVC 绑定（删除使用 PVC 的 Pod）
   - 解除资源锁定（联系云平台解除锁定）
3. 使用 `bke.bocloud.com/retry` 注解触发重试
4. Phase 从检查点恢复，继续删除操作
5. **最终状态**：集群被完全删除

**策略 2：强制删除**
1. 跳过阻塞的资源
2. 强制删除剩余资源：
   - 强制删除 Pod（`--force --grace-period=0`）
   - 强制删除 PVC（`--force`）
   - 强制删除云资源（通过云 API 强制删除）
3. **最终状态**：集群被完全删除（可能有残留）
4. 手动清理残留资源（如有）

**策略 3：手动清理**
1. 人工介入
2. 手动清理残留资源：
   - 手动删除 Kubernetes 资源
   - 手动删除云资源
   - 手动清理本地文件
3. **最终状态**：集群被完全删除

**状态转换**：
```
DeleteFailed → (重试删除) → Deleting → (删除完成) → Deleted
```

**注意**：删除失败后，状态保持为 `DeleteFailed`，直到删除完成。不会转换到 `Ready` 状态。

**预计处理时间**：5-30 分钟（取决于失败原因和修复复杂度）

#### 3.2.4 回滚状态转换规则

**基于现有状态机引擎设计回滚规则**

PhaseFlow 已集成状态机引擎（`statemachine.Engine`），当前支持的触发器：
- `TriggerPhaseComplete`：Phase 执行成功
- `TriggerError`：Phase 执行失败
- `TriggerRetry`：重试操作

**需要新增**：
- `TriggerRollback`：回滚操作触发器

```go
// 需要新增的回滚转换规则
func registerRollbackTransitions(e *statemachine.Engine) {
    // ScaleFailed → Ready（扩缩容回滚）
    e.AddTransition(statemachine.Transition{
        FromState: bkev1beta1.ClusterScaleFailed,
        ToState:   bkev1beta1.ClusterReady,
        Trigger:   "Rollback",
        Condition: isScaleRollbackComplete,
        Action:    cleanupFailedScaleResources,
    })
    
    // ManageFailed → Ready（配置变更回滚）
    e.AddTransition(statemachine.Transition{
        FromState: bkev1beta1.ClusterManageFailed,
        ToState:   bkev1beta1.ClusterReady,
        Trigger:   "Rollback",
        Condition: isManageRollbackComplete,
        Action:    restorePreviousConfig,
    })
    
    // 注意：删除操作不支持回滚
    // DeleteFailed 状态只能通过重试删除或手动清理来处理
    // 最终状态是 Deleted，而不是 Ready
}
```

**为什么删除操作不支持回滚？**

删除操作是**不可逆操作**，删除失败后无法回滚到 Ready 状态：

1. **已删除的资源无法恢复**：回滚的本质是"撤销操作"，但删除操作已部分完成，已删除的资源无法恢复
2. **状态不一致**：删除失败后，集群处于不一致状态（部分资源已删除，部分未删除），无法恢复到真正的 Ready 状态
3. **正确的处理方式**：只能通过重试删除、强制删除或手动清理来完成删除操作，最终状态是 `Deleted`，而不是 `Ready`

因此，删除失败不属于回滚场景，而是**重试/恢复场景**。

#### 3.2.5 回滚触发机制

**方案一：手动触发（推荐）**

```bash
# 用户通过 CLI 触发回滚
bkectl rollback cluster my-cluster

# 实现：设置 BKECluster 注解
kubectl annotate bkecluster my-cluster bke.bocloud.com/rollback=true
```

**方案二：自动触发**

```go
// 在 StatusManagerV2 中检测重试次数超限
if sr.StatusCount >= maxRetryCount {
    // 自动触发回滚
    setRollbackAnnotation(cluster)
}
```

#### 3.2.6 回滚执行流程

```
PhaseFlow 回滚流程（以扩缩容失败为例）：

1. 检测到扩缩容失败
   └─ ClusterStatus: WorkerScalingUp → ScaleFailed
   └─ PhaseStatus[WorkerScalingUp] = PhaseFailed

2. 尝试重试（L1/L2/L3）
   └─ 自动重试 10 次（L2）
   └─ 手动重试（L3，修复根因后）
   └─ 重试仍然失败

3. 触发回滚
   └─ 用户执行：bkectl rollback cluster my-cluster
   └─ 或自动触发：重试次数超限且无法修复

4. PhaseFlow 检测回滚注解
   └─ 检测到 bke.bocloud.com/rollback=true
   └─ 调用状态机引擎：engine.Transition(cluster, "Rollback", nil)

5. 状态机执行回滚转换
   └─ ScaleFailed → Ready
   └─ 执行 Action：cleanupFailedScaleResources
      ├─ 删除未就绪的 Worker 节点
      ├─ 清理相关 ConfigMap/Secret
      └─ 恢复节点池配置

6. 清除回滚注解
   └─ 删除 bke.bocloud.com/rollback 注解
   └─ 集群恢复到 Ready 状态
```

### 3.3 ClusterVersion 回滚设计

#### 3.4.1 设计思路

##### ClusterVersion 回滚的特殊性

ClusterVersion 回滚是**版本回滚**（降级），与 PhaseFlow 的**操作回滚**有本质区别：

| 维度 | PhaseFlow 操作回滚 | ClusterVersion 版本回滚 |
|------|-------------------|------------------------|
| **回滚对象** | 操作结果（节点、配置等） | 组件版本（Agent、etcd、Master 等） |
| **回滚方向** | 撤销操作，返回 Ready 状态 | 降级组件，返回旧版本 |
| **复杂度** | 中等（清理资源） | 高（组件降级、数据格式兼容） |
| **组件顺序** | 无特定顺序 | 必须按相反顺序降级（Worker → Master → etcd → Containerd → Agent） |
| **数据影响** | 清理创建的资源 | 可能需要数据格式降级 |
| **风险** | 资源残留 | 版本不兼容、数据丢失 |

**为什么版本回滚更复杂？**

1. **组件依赖关系**：组件之间有依赖关系（如 Master 依赖 etcd），降级顺序必须正确
2. **数据格式兼容**：新版本可能修改了数据格式（如 etcd 数据格式），降级时需要数据迁移
3. **证书兼容性**：不同版本可能使用不同的证书格式
4. **配置兼容性**：新版本的配置可能在旧版本中不支持

##### 升级 vs 回滚的本质区别

| 维度 | 升级（Upgrade） | 回滚（Rollback） |
|------|----------------|-----------------|
| **目标** | 将集群升级到新版本 | 将集群降级到旧版本 |
| **方向** | 向前 - 朝新版本前进 | 向后 - 返回旧版本 |
| **组件顺序** | Agent → Containerd → etcd → Master → Worker | Worker → Master → etcd → Containerd → Agent |
| **数据迁移** | 可能需要数据格式升级 | 可能需要数据格式降级 |
| **风险** | 新版本可能有未知问题 | 旧版本可能不兼容新数据 |
| **测试覆盖** | 充分测试 | 部分测试（回滚路径可能未充分验证） |

##### ClusterVersion 的职责边界

ClusterVersion **只负责验证和准备**，不直接执行降级：

1. **验证回滚路径**：通过 `UpgradePath` CRD 验证回滚路径是否合法
2. **拉取旧版本镜像**：拉取目标版本的 ReleaseImage（OCI 镜像）
3. **触发降级 DAG**：由 BKECluster 控制器检测到 desiredVersion 变化后执行降级 DAG

**为什么不直接执行降级？**

- **职责分离**：ClusterVersion 专注版本管理，BKECluster 专注集群操作
- **复用现有逻辑**：降级 DAG 可以复用升级 DAG 的执行框架
- **灵活性**：可以在降级前做额外检查（如备份数据）

##### 回滚的核心原则

1. **路径验证**：必须验证回滚路径合法（通过 UpgradePath CRD）
2. **组件顺序**：必须按相反顺序降级组件（Worker → Master → etcd → Containerd → Agent）
3. **数据兼容**：必须确保数据格式兼容（必要时执行数据迁移）
4. **备份优先**：降级前必须备份当前数据（etcd 快照、配置文件等）
5. **可观测性**：必须有清晰的降级日志和事件记录，便于问题排查

##### 回滚方案选择

BKE 提供两种回滚方案，与 3.1.1 节升级失败回滚方案保持一致：

**方案一：降级 DAG**
- 参考升级 DAG 设计，实现专门的降级 DAG
- 按相反顺序降级组件（Worker → Master → etcd → Containerd → Agent）
- 为每个组件实现特定的降级逻辑（数据迁移、配置回滚等）
- 适用于复杂场景（如数据格式变更）

**方案二：复用升级流程执行降级（参考 OpenShift CVO 机制）**
- 复用现有升级 DAG 流程，将目标版本设为旧版本
- CVO 按正常升级流程反向执行（降级 Operator、降级节点配置等）
- 不需要为每个组件编写专门的降级代码
- 实现简单，工作量小
- 适用于快速交付和大部分回滚场景

**推荐策略**：
- **阶段一（P0）**：实现方案二（快速交付回滚能力）
- **阶段二（P1）**：按需实现方案一（处理特殊场景，如数据格式变更）

#### 3.4.2 支持的回滚场景

**ClusterVersion 回滚场景总览**：

| 场景 | 触发条件 | 回滚策略 | 预计时间 |
|------|---------|---------|---------|
| 升级失败回滚 | 升级过程中组件失败 | 降级到上一版本 | 15-30分钟 |
| 升级后发现问题 | 升级完成后发现严重问题 | 降级到上一版本 | 15-30分钟 |
| 跨版本回滚 | 需要回滚多个版本 | 逐跳降级 | 30-60分钟/跳 |

**支持回滚的场景**：

1. **升级失败回滚**
   - 触发条件：升级过程中组件失败（镜像拉取失败、健康检查失败等）
   - 回滚目标：升级到之前的版本（如 v26.06 → v26.05）
   - 回滚动作：降级所有组件到旧版本
   - 适用条件：UpgradePath CRD 中存在回滚路径

2. **升级后发现问题回滚**
   - 触发条件：升级完成后发现严重问题（bug、性能问题、兼容性问题）
   - 回滚目标：降级到升级前的稳定版本
   - 回滚动作：降级所有组件到旧版本
   - 适用条件：升级后 24 小时内（数据格式未发生不可逆变更）

3. **跨版本回滚**
   - 触发条件：需要回滚多个版本（如 v26.07 → v26.05）
   - 回滚目标：逐跳降级（v26.07 → v26.06 → v26.05）
   - 回滚动作：每跳执行完整的降级流程
   - 适用条件：每一跳都有合法的 UpgradePath

**不支持回滚的场景**：

1. **降级到不存在的版本**
   - 原因：ReleaseImage 不存在，无法拉取
   - 处理方式：手动构建 ReleaseImage 或选择其他版本

2. **跳过多个版本直接降级**
   - 原因：必须逐跳降级，确保每一跳的数据兼容性
   - 处理方式：逐跳降级（v26.07 → v26.06 → v26.05）

3. **数据格式不兼容的降级**
   - 原因：新版本修改了数据格式（如 etcd 数据格式），旧版本无法读取
   - 处理方式：执行数据迁移脚本或重建集群

**回滚决策树**：

```
升级失败或发现问题
  ↓
判断问题类型
  ├─ 瞬时故障（网络超时、镜像拉取失败）→ 重试升级
  ├─ 根本性问题（版本不兼容、严重 bug）→ 回滚到上一版本
  └─ 需要回滚多个版本 → 逐跳回滚
```

#### 3.4.3 回滚场景详细说明

##### 场景 1：升级失败回滚

**失败原因分析**：
- ReleaseImage 拉取失败（网络问题、镜像损坏）
- 组件升级失败（Agent、Containerd、etcd、Master、Worker）
- 健康检查失败（升级后组件无法正常启动）
- 数据迁移失败（数据格式不兼容）

**重试策略（先尝试修复并重试）**：
1. 诊断失败原因（查看组件日志、事件记录）
2. 修复根因（修复网络、重新拉取镜像、修复数据）
3. 使用 `bke.bocloud.com/retry` 注解触发重试
4. 升级 DAG 从检查点恢复，继续升级

**回滚策略（重试失败后）**：
1. 用户设置回滚目标版本
   ```bash
   kubectl patch clusterversion --type merge \
       -p '{"spec":{"desiredVersion":"v26.05"}}'
   ```
2. ClusterVersion Controller 检测到 desiredVersion < currentVersion
3. 验证回滚路径
4. 拉取旧版本 ReleaseImage
5. BKECluster Controller 执行降级 DAG
6. 按相反顺序降级所有组件
7. 更新 ClusterVersion.status.currentVersion = v26.05
8. ClusterStatus = `Ready`

**状态转换**：
```
ClusterVersionPhaseFailed → ClusterVersionPhaseUpgrading → ClusterVersionPhaseReady
```

**预计恢复时间**：15-30 分钟

##### 场景 2：升级后发现问题回滚

**问题类型**：
- 新版本有严重 bug
- 性能下降
- 兼容性问题（某些应用无法运行）
- 配置不兼容

**回滚策略**：
1. 备份当前数据（etcd 快照、配置文件）
2. 用户设置回滚目标版本
3. ClusterVersion Controller 验证回滚路径
4. 执行降级 DAG
5. 降级所有组件到旧版本
6. 恢复配置（如有必要）
7. 验证集群健康

**状态转换**：
```
ClusterVersionPhaseReady → ClusterVersionPhaseUpgrading → ClusterVersionPhaseReady
```

**预计恢复时间**：15-30 分钟

##### 场景 3：跨版本回滚

**场景描述**：
需要从 v26.07 回滚到 v26.05，但 UpgradePath 只支持逐跳回滚（v26.07 → v26.06 → v26.05）

**回滚策略**：
1. 第一跳：v26.07 → v26.06
   - 验证回滚路径
   - 拉取 v26.06 ReleaseImage
   - 执行降级 DAG
   - 验证集群健康
2. 第二跳：v26.06 → v26.05
   - 验证回滚路径
   - 拉取 v26.05 ReleaseImage
   - 执行降级 DAG
   - 验证集群健康

**状态转换**：
```
ClusterVersionPhaseReady (v26.07)
  → ClusterVersionPhaseUpgrading → ClusterVersionPhaseReady (v26.06)
  → ClusterVersionPhaseUpgrading → ClusterVersionPhaseReady (v26.05)
```

**预计恢复时间**：30-60 分钟（每跳 15-30 分钟）

#### 3.4.4 回滚状态转换规则

**ClusterVersion 回滚状态转换**：

> **注意**：OpenShift 的 ClusterVersion 没有独立的 `RollingBack` 状态。回滚和升级共用 `Upgrading` 状态，CVO 通过比较 `desiredVersion` 和 `currentVersion` 的大小来判断是升级还是降级。

```go
// ClusterVersion 回滚状态转换规则（参考 OpenShift 实际机制）
ClusterVersionPhaseUpgrading → ClusterVersionPhaseReady (回滚完成，即降级完成)
ClusterVersionPhaseFailed → ClusterVersionPhaseUpgrading (升级失败后触发回滚，重新进入 Upgrading)
ClusterVersionPhaseReady → ClusterVersionPhaseUpgrading (升级后发现问题，触发回滚)
ClusterVersionPhaseUpgrading → ClusterVersionPhaseFailed (回滚失败)
```

**状态转换说明**：

| FromState | ToState | 触发条件 | 说明 |
|-----------|---------|---------|------|
| Ready | Upgrading | 用户设置 desiredVersion < currentVersion | 触发降级（回滚），与升级共用 Upgrading 状态 |
| Failed | Upgrading | 升级失败后用户设置 desiredVersion | 升级失败后触发回滚 |
| Upgrading | Ready | 降级 DAG 执行成功 | 回滚完成 |
| Upgrading | Failed | 降级 DAG 执行失败 | 回滚失败 |

#### 3.4.5 回滚触发机制

**方案一：手动触发（推荐）**

```bash
# 用户通过 kubectl 触发回滚
kubectl patch clusterversion --type merge \
    -p '{"spec":{"desiredVersion":"v26.05"}}'

# ClusterVersion Controller 检测到 desiredVersion < currentVersion
# 执行降级流程
```

#### 3.4.6 方案一：降级 DAG 执行流程

```
ClusterVersion 回滚执行流程（方案一：降级 DAG）：

1. 用户设置回滚目标
   └─ kubectl patch clusterversion --type merge \
       -p '{"spec":{"desiredVersion":"v26.05"}}'

2. ClusterVersion Controller：
   ├─ 验证回滚路径（UpgradePath CRD）
   ├─ 拉取旧版本 ReleaseImage
   └─ Phase → Upgrading（降级与升级共用 Upgrading 状态）

3. BKECluster Controller 检测到 desiredVersion 变化：
   ├─ shouldUseDeclarativeDowngrade() 返回 true
   └─ executeDowngradeDAG() 执行降级 DAG

4. 降级 DAG 执行（按相反顺序）：
   ├─ EnsureWorkerDowngrade
   ├─ EnsureMasterDowngrade
   ├─ EnsureEtcdDowngrade
   ├─ EnsureContainerdDowngrade
   └─ EnsureAgentDowngrade

5. 降级完成：
   ├─ 更新 ClusterVersion.status
   │   ├─ CurrentVersion: v26.05
   │   ├─ Phase: Ready
   │   └─ UpgradeHistory: append({From: v26.06, To: v26.05, Status: Completed})
   └─ ClusterStatus: Ready
```

#### 3.4.7 方案一：降级 DAG 设计

**参考升级 DAG 设计降级 DAG**

```go
// bkecluster_rollback_dag.go

func (r *BKEClusterReconciler) executeRollbackDAG(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) (ctrl.Result, error) {
    // 1. 解析 ReleaseImage
    releaseBundle, err := r.resolveReleaseBundle(ctx, targetVersion)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. 构建降级 DAG（与升级 DAG 顺序相反）
    dag := r.buildRollbackDAG(releaseBundle)
    
    // 3. 执行降级 DAG
    if err := r.executeDAG(ctx, bkeCluster, dag); err != nil {
        // 记录错误
        bkeCluster.Status.DeclarativeUpgrade.LastError = err.Error()
        return ctrl.Result{}, err
    }
    
    // 4. 完成降级
    return r.completeDeclarativeRollback(ctx, bkeCluster, targetVersion)
}
```

**降级 DAG 节点顺序**：

```
升级 DAG 顺序：
Agent → Containerd → etcd → Master → Worker

降级 DAG 顺序（相反）：
Worker → Master → etcd → Containerd → Agent
```

**为什么顺序相反？**

- **升级时**：先升级基础组件（Agent、Containerd），再升级依赖组件（etcd、Master、Worker）
- **降级时**：先降级依赖组件（Worker、Master），再降级基础组件（etcd、Containerd、Agent）
- **原因**：确保降级过程中组件之间的兼容性

#### 3.4.8 方案二：复用升级流程执行降级（参考 OpenShift CVO 机制）

**设计思路**：
- OpenShift 的回滚本质是"降级"：设置 `spec.desiredUpdate` 为旧版本，CVO 按正常升级流程反向执行
- 复用现有升级 DAG 流程，将目标版本设为旧版本
- 不需要为每个组件编写专门的降级代码
- 通过重新应用旧版本的 manifest 和配置来实现降级

**执行流程**：
1. ClusterVersion 验证回滚路径（v26.06 → v26.05）
2. 拉取旧版本 ReleaseImage
3. BKECluster 控制器执行重新部署
4. 复用部署逻辑，重新部署所有组件：
   - deployAgent：部署旧版本 Agent
   - deployContainerd：部署旧版本 Containerd
   - deployEtcd：部署旧版本 etcd
   - deployMaster：部署旧版本 Master 组件
   - deployWorker：部署旧版本 Worker 组件
5. 等待所有组件就绪
6. 更新 ClusterVersion.status.currentVersion = v26.05
7. ClusterStatus = `Ready`

**核心代码示例**：
```go
// bkecluster_controller.go

func (r *BKEClusterReconciler) executeRollback(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) (ctrl.Result, error) {
    // 1. 解析旧版本 ReleaseImage
    releaseBundle, err := r.resolveReleaseBundle(ctx, targetVersion)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. 复用升级流程执行降级（复用部署逻辑）
    if err := r.applyReleaseBundle(ctx, bkeCluster, releaseBundle); err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 等待所有组件就绪
    if err := r.waitForComponentsReady(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    // 4. 完成回滚
    return r.completeRollback(ctx, bkeCluster, targetVersion)
}

// applyReleaseBundle 复用部署逻辑
func (r *BKEClusterReconciler) applyReleaseBundle(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    releaseBundle *ReleaseBundle,
) error {
    // 1. 部署 Agent
    if err := r.deployAgent(ctx, bkeCluster, releaseBundle.Agent); err != nil {
        return err
    }
    
    // 2. 部署 Containerd
    if err := r.deployContainerd(ctx, bkeCluster, releaseBundle.Containerd); err != nil {
        return err
    }
    
    // 3. 部署 etcd
    if err := r.deployEtcd(ctx, bkeCluster, releaseBundle.Etcd); err != nil {
        return err
    }
    
    // 4. 部署 Master 组件
    if err := r.deployMaster(ctx, bkeCluster, releaseBundle.Master); err != nil {
        return err
    }
    
    // 5. 部署 Worker 组件
    if err := r.deployWorker(ctx, bkeCluster, releaseBundle.Worker); err != nil {
        return err
    }
    
    return nil
}
```

**优点**：
- ✅ 实现简单，复用现有部署逻辑
- ✅ 不需要为每个组件编写降级代码
- ✅ 与升级流程对称，易于理解和维护
- ✅ 实现工作量小，测试工作量小
- ✅ 可以处理大部分回滚场景

**缺点**：
- ❌ 可能需要更长时间（需要重新部署所有组件）
- ❌ 某些状态可能无法完全回滚（如 etcd 数据格式变更不可逆）
- ❌ 需要确保旧版本的 ReleaseImage 可用
- ❌ 某些组件可能不支持"重新部署"（如已经修改了用户配置）

**适用场景**：
- 组件版本间没有数据格式变更
- 对回滚时间要求不严格
- 希望快速实现回滚能力

#### 3.4.9 方案对比与建议

**方案对比**：

| 维度 | 方案一：降级 DAG | 方案二：重新部署 |
|------|----------------|----------------|
| **实现复杂度** | 高（需要为每个组件编写降级逻辑） | 低（复用部署逻辑） |
| **实现工作量** | 大（预计 2-3 周） | 小（预计 1 周） |
| **回滚时间** | 快（只降级需要降级的组件） | 慢（需要重新部署所有组件） |
| **精确度** | 高（可以精确控制每个组件的降级） | 中（重新部署可能覆盖用户配置） |
| **可靠性** | 中（降级逻辑需要充分测试） | 高（复用已验证的部署逻辑） |
| **适用场景** | 组件版本间有数据格式变更 | 组件版本间没有数据格式变更 |
| **维护成本** | 高（需要维护降级 DAG） | 低（与升级流程对称） |

**推荐实施策略**：

**阶段一（P0）**：实现方案二（重新部署）
- 快速交付回滚能力
- 覆盖大部分回滚场景
- 实现工作量小，风险低

**阶段二（P1）**：根据实际使用情况，决定是否实现方案一
- 如果发现某些场景方案二无法满足（如 etcd 数据格式变更），再实现方案一
- 方案一可以作为方案二的补充，处理特殊场景

### 3.4 双机制协同设计

#### 3.5.1 职责划分

| 场景 | PhaseFlow 职责 | ClusterVersion 职责 | DAG 职责 |
|------|---------------|---------------------|---------|
| **升级失败** | 保持当前状态 | 验证回滚路径，设置注解 | 执行降级 DAG |
| **扩缩容失败** | 执行回滚，清理资源 | 不参与 | 不参与 |
| **配置变更失败** | 执行回滚，恢复配置 | 不参与 | 不参与 |
| **安装失败** | 清理重建 | 不参与 | 不参与 |

#### 3.5.2 协同回滚场景

**场景：升级后扩缩容失败**

```
1. 升级到 v26.06 成功
   └─ ClusterVersion.status.currentVersion: v26.06
   └─ ClusterStatus: Ready

2. 扩容 Worker 节点失败
   └─ ClusterStatus: Ready → WorkerScalingUp → ScaleFailed

3. 用户决定回滚扩缩容（不回滚版本）
   └─ bkectl rollback cluster my-cluster --phase-only
   └─ 仅触发 PhaseFlow 回滚

4. PhaseFlow 执行回滚
   └─ ScaleFailed → Ready
   └─ 清理失败的 Worker 节点
   └─ ClusterVersion 保持不变（v26.06）

5. 集群恢复到 Ready 状态
   └─ ClusterStatus: Ready
   └─ ClusterVersion.status.currentVersion: v26.06
```

**场景：升级失败需要回滚版本**

```
1. 升级到 v26.06 失败
   └─ ClusterVersion.status.phase: Failed
   └─ ClusterStatus: ClusterUpgradeFailed

2. 用户决定回滚到 v26.05
   └─ kubectl patch clusterversion --type merge \
       -p '{"spec":{"desiredVersion":"v26.05"}}'

3. ClusterVersion 验证回滚路径
   └─ 验证 v26.06 → v26.05 路径

4. BKECluster 执行降级 DAG
   └─ 降级所有组件到 v26.05
   └─ 更新 ClusterVersion.status

5. 集群恢复到 v26.05
   └─ ClusterVersion.status.currentVersion: v26.05
   └─ ClusterStatus: Ready
```

### 3.5 安装失败处理

#### 3.6.1 设计原则

**安装过程不支持自动回滚，与 OpenShift 一致**

| 因素 | 说明 |
|------|------|
| **状态不可逆** | etcd 数据、证书、网络配置一旦创建无法简单回滚 |
| **基础设施耦合** | 云资源（VM、网络、存储）已创建 |
| **时间窗口** | 安装失败通常在早期阶段，重建比回滚更快 |

#### 3.6.2 安装失败处理策略

```yaml
installFailureHandling:
  # 策略 1: 清理并重建（推荐）
  cleanupAndRebuild:
    trigger: "安装失败（任何阶段）"
    actions:
      - "执行清理脚本：删除已创建的资源"
      - "验证资源完全删除"
      - "重新执行安装流程"
    estimatedTime: "10-30 分钟"
    
  # 策略 2: 部分恢复（特定场景）
  partialRecovery:
    trigger: "安装后期阶段失败（如 Addon 部署失败）"
    conditions:
      - "控制面已就绪"
      - "etcd 集群健康"
      - "节点已加入集群"
    actions:
      - "诊断失败原因"
      - "修复问题"
      - "重试失败的步骤"
    estimatedTime: "5-15 分钟"
```

#### 3.6.3 清理脚本设计

```bash
#!/bin/bash
# bke-install-cleanup.sh

CLUSTER_NAME=$1
REGION=$2

echo "Starting cleanup for cluster: ${CLUSTER_NAME}"

# 1. 删除 BKECluster 资源
kubectl delete bkecluster ${CLUSTER_NAME} -n bke-system

# 2. 删除节点资源
kubectl delete bkenode --all -n bke-system

# 3. 删除云资源（根据实际环境）
# - 删除 VM/实例
# - 删除负载均衡器
# - 删除存储卷
# - 删除网络资源

# 4. 删除证书和配置
rm -rf /etc/bke/${CLUSTER_NAME}

# 5. 验证清理完成
echo "Verifying cleanup..."
kubectl get bkecluster ${CLUSTER_NAME} -n bke-system
if [ $? -eq 0 ]; then
    echo "ERROR: Cluster still exists"
    exit 1
fi

echo "Cleanup completed successfully"
```

---

## 四、备份能力规划

### 4.1 设计思路

#### 4.1.1 备份目标

BKE 备份能力的核心目标：

1. **灾难恢复**：集群故障后能够快速恢复（RTO < 4小时，RPO < 1小时）
2. **版本回滚**：升级失败后能够回滚到之前版本
3. **数据保护**：防止数据丢失（etcd 数据、配置、应用数据）
4. **合规要求**：满足企业数据保护政策（等保2.0、ISO27001 等）

#### 4.1.2 备份范围

| 备份类型 | 备份内容 | 备份频率 | 保留周期 | 优先级 |
|---------|---------|---------|---------|--------|
| **etcd 数据** | 集群状态、配置、Secret 等 | 每日自动 | 7天 | P0 |
| **集群配置** | BKECluster CRD、BKENode CRD、ConfigMap、Secret | 变更时手动 | 7天 | P0 |
| **证书和密钥** | CA 证书、服务证书、密钥 | 变更时手动 | 7天 | P0 |
| **应用数据** | 用户部署的应用、PVC 数据 | 每日自动 | 7天 | P1 |

#### 4.1.3 备份原则

1. **定期自动备份**：etcd 每天自动备份（凌晨 2 点）
2. **多版本保留**：保留最近 7 天的备份
3. **异地存储**：备份存储到异地（防止本地故障）
4. **加密存储**：敏感数据加密备份（AES-256）
5. **定期验证**：定期验证备份可恢复性（每周日凌晨 3 点）

#### 4.1.4 备份方案选择

**方案一：etcdctl snapshot（推荐用于 etcd 备份）**

- **优点**：
  - ✅ 官方工具，稳定可靠
  - ✅ 完整备份 etcd 数据
  - ✅ 恢复速度快
- **缺点**：
  - ❌ 只能备份 etcd 数据
  - ❌ 需要停止 etcd 或使用在线备份
- **适用场景**：etcd 数据备份

**方案二：Velero（推荐用于应用数据备份）**

- **优点**：
  - ✅ Kubernetes 原生工具
  - ✅ 支持多种存储后端（S3、NFS、OSS 等）
  - ✅ 支持 PVC 快照
  - ✅ 支持增量备份
- **缺点**：
  - ❌ 需要额外部署 Velero
  - ❌ 需要配置存储后端
- **适用场景**：应用数据、PVC 数据备份

**方案三：自定义脚本（推荐用于配置备份）**

- **优点**：
  - ✅ 灵活可控
  - ✅ 可以备份 BKE 特定配置
  - ✅ 不依赖第三方工具
- **缺点**：
  - ❌ 需要维护脚本
  - ❌ 需要处理异常场景
- **适用场景**：BKE 特定配置备份（BKECluster、BKENode、证书等）

**推荐策略**：
- **etcd 备份**：使用 etcdctl snapshot（方案一）
- **配置备份**：使用自定义脚本（方案三）
- **应用数据备份**：使用 Velero（方案二）

#### 4.1.5 备份验证策略

**自动验证**：
- 每周日凌晨 3 点自动恢复备份到测试环境
- 验证 etcd 数据完整性
- 验证集群配置正确性
- 验证应用数据可用性
- 清理测试环境

**手动验证**：
- 重大变更前手动验证备份
- 升级前手动验证备份
- 灾难恢复演练时手动验证备份

**验证内容**：
1. **etcd 数据验证**：
   - 恢复 etcd 快照
   - 验证 etcd 集群健康
   - 验证关键数据存在（BKECluster、BKENode 等）

2. **配置验证**：
   - 恢复配置文件
   - 验证配置正确性
   - 验证证书有效性

3. **应用数据验证**：
   - 恢复应用数据
   - 验证应用可访问
   - 验证数据完整性

### 4.2 etcd 备份

#### 4.3.1 备份策略

```yaml
etcdBackupStrategy:
  # 自动备份
  automaticBackup:
    enabled: true
    schedule: "0 2 * * *"  # 每天凌晨 2 点
    retentionDays: 7       # 保留 7 天
    storageLocation: "/backup/etcd"
    
  # 手动备份
  manualBackup:
    trigger: "升级前、重大变更前"
    storageLocation: "/backup/etcd/manual"
    
  # 备份验证
  backupVerification:
    enabled: true
    schedule: "0 3 * * 0"  # 每周日凌晨 3 点
    actions:
      - "恢复备份到测试环境"
      - "验证集群健康"
      - "清理测试环境"
```

#### 4.3.2 备份脚本

```bash
#!/bin/bash
# bke-etcd-backup.sh

BACKUP_DIR="/backup/etcd"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/etcd_snapshot_${TIMESTAMP}.db"

# 1. 创建备份目录
mkdir -p ${BACKUP_DIR}

# 2. 执行 etcd 快照
ETCDCTL_API=3 etcdctl snapshot save ${BACKUP_FILE} \
  --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/bke/pki/etcd/ca.crt \
  --cert=/etc/bke/pki/etcd/server.crt \
  --key=/etc/bke/pki/etcd/server.key

# 3. 验证备份
ETCDCTL_API=3 etcdctl snapshot status ${BACKUP_FILE} --write-out=table

# 4. 压缩备份
gzip ${BACKUP_FILE}

# 5. 清理旧备份（保留最近 7 天）
find ${BACKUP_DIR} -name "etcd_snapshot_*.db.gz" -mtime +7 -delete

echo "Backup completed: ${BACKUP_FILE}.gz"
```

#### 4.3.3 备份产物

| 文件名 | 格式 | 内容 | 大小（典型值） |
|--------|------|------|----------------|
| `etcd_snapshot_<timestamp>.db.gz` | 压缩快照 | etcd 完整数据 | 50-200 MB |
| `bke_config_<timestamp>.yaml` | YAML | BKE 集群配置 | < 1 MB |
| `certificates_<timestamp>.tar.gz` | 压缩归档 | 证书和密钥 | 1-5 MB |

### 4.3 配置备份

#### 4.3.1 备份内容

```yaml
configBackup:
  # BKE 集群配置
  bkeClusterConfig:
    - "BKECluster CRD"
    - "BKENode CRD"
    - "相关 ConfigMap"
    - "相关 Secret"
    
  # 证书和密钥
  certificates:
    - "CA 证书"
    - "API Server 证书"
    - "etcd 证书"
    - "Service Account 密钥"
    
  # 系统配置
  systemConfig:
    - "kubelet 配置"
    - "containerd 配置"
    - "网络插件配置"
```

#### 4.3.2 备份脚本

```bash
#!/bin/bash
# bke-config-backup.sh

CLUSTER_NAME=$1
BACKUP_DIR="/backup/config/${CLUSTER_NAME}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

# 1. 创建备份目录
mkdir -p ${BACKUP_DIR}/${TIMESTAMP}

# 2. 导出 BKE 资源
kubectl get bkecluster ${CLUSTER_NAME} -n bke-system -o yaml > \
  ${BACKUP_DIR}/${TIMESTAMP}/bkecluster.yaml

kubectl get bkenode -n bke-system -o yaml > \
  ${BACKUP_DIR}/${TIMESTAMP}/bkenodes.yaml

# 3. 导出相关 ConfigMap 和 Secret
kubectl get configmap,secret -n bke-system -l cluster=${CLUSTER_NAME} -o yaml > \
  ${BACKUP_DIR}/${TIMESTAMP}/resources.yaml

# 4. 备份证书
tar -czf ${BACKUP_DIR}/${TIMESTAMP}/certificates.tar.gz \
  /etc/bke/${CLUSTER_NAME}/pki/

# 5. 备份系统配置
tar -czf ${BACKUP_DIR}/${TIMESTAMP}/system-config.tar.gz \
  /etc/bke/${CLUSTER_NAME}/config/

# 6. 生成备份清单
cat > ${BACKUP_DIR}/${TIMESTAMP}/manifest.json <<EOF
{
  "cluster_name": "${CLUSTER_NAME}",
  "backup_time": "${TIMESTAMP}",
  "files": [
    "bkecluster.yaml",
    "bkenodes.yaml",
    "resources.yaml",
    "certificates.tar.gz",
    "system-config.tar.gz"
  ]
}
EOF

echo "Config backup completed: ${BACKUP_DIR}/${TIMESTAMP}"
```

### 4.4 应用数据备份

#### 4.5.1 备份策略

```yaml
applicationDataBackup:
  # 使用 Velero 备份
  veleroBackup:
    enabled: true
    schedule: "0 1 * * *"  # 每天凌晨 1 点
    includedNamespaces:
      - "default"
      - "production"
    excludedResources:
      - "events"
    storageLocation: "s3://bke-backups/velero"
    
  # PVC 备份
  pvcBackup:
    enabled: true
    snapshotClass: "bke-snapshot-class"
    retentionDays: 7
```

#### 4.5.2 Velero 配置

```yaml
# Velero 备份配置
apiVersion: velero.io/v1
kind: Schedule
metadata:
  name: daily-backup
  namespace: velero
spec:
  schedule: "0 1 * * *"
  template:
    includedNamespaces:
      - default
      - production
    excludedResources:
      - events
    storageLocation: bke-backup-location
    volumeSnapshotLocations:
      - bke-vsl
    ttl: 168h0m0s  # 7 天
```

### 4.5 备份存储

#### 4.6.1 存储位置

```yaml
backupStorage:
  # 本地存储
  local:
    path: "/backup"
    retentionDays: 7
    
  # 远程存储（推荐）
  remote:
    - type: "s3"
      bucket: "bke-backups"
      region: "us-west-2"
      encryption: true
      
    - type: "nfs"
      server: "nfs.example.com"
      path: "/backup/bke"
      
    - type: "oss"
      bucket: "bke-backups"
      region: "cn-hangzhou"
```

#### 4.6.2 备份加密

```bash
#!/bin/bash
# 加密备份文件

BACKUP_FILE=$1
ENCRYPTION_KEY="/etc/bke/backup-encryption.key"

# 使用 AES-256 加密
openssl enc -aes-256-cbc -salt -in ${BACKUP_FILE} \
  -out ${BACKUP_FILE}.enc \
  -pass file:${ENCRYPTION_KEY}

# 删除未加密文件
rm ${BACKUP_FILE}

echo "Backup encrypted: ${BACKUP_FILE}.enc"
```

---

## 五、恢复能力规划

### 5.1 设计思路

#### 5.1.1 恢复目标

BKE 恢复能力的核心目标：

1. **快速恢复**：最小化业务中断时间（RTO）
2. **数据完整**：最小化数据丢失（RPO）
3. **自动化**：尽可能自动化恢复过程，减少人工干预
4. **可验证**：恢复后必须验证集群和应用健康

#### 5.1.2 恢复范围

| 恢复类型 | 恢复内容 | 恢复方式 | 优先级 |
|---------|---------|---------|--------|
| **etcd 数据恢复** | 集群状态、配置、Secret 等 | etcdctl snapshot restore | P0 |
| **配置恢复** | BKECluster CRD、BKENode CRD、ConfigMap、Secret | kubectl apply | P0 |
| **证书和密钥恢复** | CA 证书、服务证书、密钥 | 解压恢复 | P0 |
| **应用数据恢复** | 用户部署的应用、PVC 数据 | Velero restore | P1 |

#### 5.1.3 恢复原则

1. **控制面优先**：先恢复 etcd 和控制面组件，再恢复工作节点
2. **数据完整性**：确保恢复的数据完整且一致
3. **最小化影响**：恢复过程尽量减少对现有服务的影响
4. **可验证性**：恢复后必须验证集群和应用健康
5. **文档化**：恢复过程必须有清晰的文档和步骤

#### 5.1.4 恢复方案选择

**方案一：etcdctl snapshot restore（推荐用于 etcd 恢复）**

- **优点**：
  - ✅ 官方工具，稳定可靠
  - ✅ 完整恢复 etcd 数据
  - ✅ 恢复速度快
- **缺点**：
  - ❌ 需要停止 etcd
  - ❌ 只能恢复 etcd 数据
- **适用场景**：etcd 数据恢复

**方案二：Velero restore（推荐用于应用数据恢复）**

- **优点**：
  - ✅ Kubernetes 原生工具
  - ✅ 支持多种存储后端
  - ✅ 支持 PVC 恢复
  - ✅ 支持选择性恢复
- **缺点**：
  - ❌ 需要 Velero 已部署
  - ❌ 恢复速度依赖存储后端
- **适用场景**：应用数据、PVC 数据恢复

**方案三：自定义脚本（推荐用于配置恢复）**

- **优点**：
  - ✅ 灵活可控
  - ✅ 可以恢复 BKE 特定配置
  - ✅ 不依赖第三方工具
- **缺点**：
  - ❌ 需要维护脚本
  - ❌ 需要处理异常场景
- **适用场景**：BKE 特定配置恢复

**推荐策略**：
- **etcd 恢复**：使用 etcdctl snapshot restore（方案一）
- **配置恢复**：使用自定义脚本（方案三）
- **应用数据恢复**：使用 Velero restore（方案二）

#### 5.1.5 RTO/RPO 指标

| 恢复场景 | RTO（恢复时间目标） | RPO（恢复点目标） | 恢复策略 |
|---------|-------------------|-----------------|---------|
| **单节点故障** | < 30 分钟 | 0（自动替换） | 自动替换节点 |
| **控制面故障** | < 1 小时 | < 5 分钟 | 从备份恢复 etcd |
| **整个集群故障** | < 4 小时 | < 1 小时 | 重建集群 + 恢复数据 |
| **数据丢失** | < 2 小时 | < 1 小时 | 从备份恢复数据 |

#### 5.1.6 恢复验证策略

**自动验证**：
- 恢复后自动验证 etcd 集群健康
- 恢复后自动验证控制面组件健康
- 恢复后自动验证工作节点健康

**手动验证**：
- 恢复后手动验证应用健康
- 恢复后手动验证业务功能
- 恢复后手动验证数据完整性

**验证内容**：
1. **etcd 验证**：
   - etcd 集群健康状态
   - 关键数据存在性（BKECluster、BKENode 等）

2. **控制面验证**：
   - API Server 可访问性
   - Controller Manager 健康状态
   - Scheduler 健康状态

3. **工作节点验证**：
   - 节点 Ready 状态
   - kubelet 健康状态
   - containerd 健康状态

4. **应用验证**：
   - Pod 运行状态
   - Service 可访问性
   - 数据完整性

### 5.2 etcd 恢复

#### 5.3.1 恢复流程

```bash
#!/bin/bash
# bke-etcd-restore.sh

BACKUP_FILE=$1
RESTORE_DIR="/var/lib/etcd-restore"

# 1. 停止 etcd
systemctl stop etcd

# 2. 备份当前 etcd 数据
mv /var/lib/etcd /var/lib/etcd-backup-$(date +%Y%m%d_%H%M%S)

# 3. 恢复 etcd 快照
ETCDCTL_API=3 etcdctl snapshot restore ${BACKUP_FILE} \
  --data-dir=${RESTORE_DIR} \
  --name=master-0 \
  --initial-cluster="master-0=https://127.0.0.1:2380" \
  --initial-advertise-peer-urls="https://127.0.0.1:2380"

# 4. 移动恢复的数据到 etcd 目录
mv ${RESTORE_DIR} /var/lib/etcd

# 5. 设置权限
chown -R etcd:etcd /var/lib/etcd
chmod 700 /var/lib/etcd

# 6. 启动 etcd
systemctl start etcd

# 7. 验证恢复
ETCDCTL_API=3 etcdctl endpoint health --cluster

echo "etcd restore completed"
```

#### 5.3.2 恢复后验证

```bash
# 验证 etcd 集群健康
ETCDCTL_API=3 etcdctl endpoint status --write-out=table

# 验证 API Server
kubectl get nodes

# 验证 BKE 资源
kubectl get bkecluster -n bke-system
kubectl get bkenode -n bke-system

# 验证应用
kubectl get pods --all-namespaces
```

### 5.3 配置恢复

#### 5.4.1 恢复流程

```bash
#!/bin/bash
# bke-config-restore.sh

BACKUP_DIR=$1
CLUSTER_NAME=$2

# 1. 恢复 BKE 资源
kubectl apply -f ${BACKUP_DIR}/bkecluster.yaml
kubectl apply -f ${BACKUP_DIR}/bkenodes.yaml

# 2. 恢复 ConfigMap 和 Secret
kubectl apply -f ${BACKUP_DIR}/resources.yaml

# 3. 恢复证书
tar -xzf ${BACKUP_DIR}/certificates.tar.gz -C /

# 4. 恢复系统配置
tar -xzf ${BACKUP_DIR}/system-config.tar.gz -C /

# 5. 重启相关组件
systemctl restart kubelet
systemctl restart containerd

# 6. 验证恢复
kubectl get bkecluster ${CLUSTER_NAME} -n bke-system

echo "Config restore completed"
```

### 5.4 应用数据恢复

#### 5.4.1 Velero 恢复

```bash
# 查看可用备份
velero backup get

# 恢复备份
velero restore create --from-backup daily-backup-20240115010000

# 查看恢复进度
velero restore describe daily-backup-20240115010000-restore

# 验证恢复
kubectl get pods --all-namespaces
```

### 5.5 灾难恢复

#### 5.6.1 设计思路

**灾难恢复目标**

1. **最小化业务中断**：通过快速恢复机制，将业务中断时间控制在可接受范围内
2. **数据零丢失**：确保关键数据（etcd、配置、证书）不丢失
3. **自动化恢复**：尽可能实现自动化恢复，减少人工干预
4. **可验证性**：恢复后必须验证集群和应用健康状态

**灾难恢复原则**

1. **预防优先**：通过定期备份、健康检查、监控告警预防灾难发生
2. **分级恢复**：根据故障影响范围，采用不同的恢复策略
3. **控制面优先**：优先恢复控制面（etcd、API Server），再恢复工作节点
4. **数据一致性**：确保恢复的数据一致性和完整性
5. **最小化影响**：恢复过程尽量减少对现有业务的影响

**灾难恢复策略**

| 故障类型 | 恢复策略 | 自动化程度 | 恢复时间 |
|---------|---------|-----------|---------|
| **单节点故障** | 自动替换节点 | 全自动 | 10-30 分钟 |
| **控制面单节点故障** | etcd 自动恢复 | 半自动 | 30-60 分钟 |
| **控制面多节点故障** | etcd 备份恢复 | 手动 | 1-2 小时 |
| **整个集群故障** | 重建集群 + 恢复数据 | 手动 | 2-4 小时 |
| **数据丢失** | 从备份恢复数据 | 手动 | 1-2 小时 |

**恢复优先级**

1. **P0 - 控制面**：etcd、API Server、Controller Manager、Scheduler
2. **P1 - 工作节点**：kubelet、containerd、BKE Agent
3. **P2 - 应用数据**：PVC 数据、应用配置
4. **P3 - 辅助服务**：监控、日志、告警

**灾难恢复验证**

1. **恢复前验证**：
   - 验证备份文件完整性
   - 验证恢复环境就绪
   - 验证恢复脚本可用性

2. **恢复中验证**：
   - 验证每个组件恢复状态
   - 验证组件间通信正常
   - 验证数据一致性

3. **恢复后验证**：
   - 验证集群健康状态
   - 验证应用健康状态
   - 验证业务功能正常
   - 执行端到端测试

#### 5.6.2 灾难恢复场景

| 场景 | 恢复策略 | 预计恢复时间 |
|------|---------|-------------|
| **单节点故障** | 自动替换节点 | 10-30 分钟 |
| **控制面故障** | 从备份恢复 etcd | 30-60 分钟 |
| **整个集群故障** | 重建集群 + 恢复数据 | 2-4 小时 |
| **数据丢失** | 从备份恢复数据 | 1-2 小时 |

#### 5.6.3 灾难恢复流程

```
灾难恢复流程：

1. 评估灾难范围
   └─ 确定受影响的组件
   └─ 确定恢复策略

2. 准备恢复环境
   └─ 准备新的基础设施（如需要）
   └─ 准备备份文件

3. 恢复控制面
   └─ 恢复 etcd 数据
   └─ 恢复 API Server
   └─ 恢复 Controller Manager
   └─ 恢复 Scheduler

4. 恢复工作节点
   └─ 恢复节点配置
   └─ 加入集群

5. 恢复应用数据
   └─ 恢复 PVC 数据
   └─ 恢复应用配置

6. 验证恢复
   └─ 验证集群健康
   └─ 验证应用健康
   └─ 验证业务功能
```

---

## 六、实施计划

### 6.1 阶段一：基础备份与安装清理（P0）

| 任务 | 工作量(人月) | 优先级 | 说明 |
|------|-------------|--------|------|
| etcd 自动备份 | 0.5 | P0 | 实现定时备份和清理 |
| etcd 恢复脚本 | 0.5 | P0 | 实现 etcd 快照恢复 |
| 配置与证书备份 | 0.3 | P0 | 实现集群配置和证书备份 |
| 配置恢复 | 0.3 | P0 | 实现配置恢复脚本 |
| 安装失败清理 | 0.8 | P0 | 实现安装失败清理脚本 |
| 集成测试与文档 | 0.6 | P0 | 集成测试报告、用户手册 |
| **小计** | **3.0** | | |

### 6.2 阶段二：PhaseFlow 回滚（P0）

| 任务 | 工作量(人月) | 优先级 | 说明 |
|------|-------------|--------|------|
| 回滚流程设计与评审 | 0.5 | P0 | 需求分析、方案设计、技术评审 |
| 回滚触发机制开发 | 0.8 | P0 | 实现手动/自动触发回滚、CLI 命令 |
| 回滚状态转换规则开发 | 1.2 | P0 | 状态机扩展、转换规则、兼容性验证 |
| 资源清理逻辑开发 | 1.0 | P0 | 扩缩容/配置变更/删除的清理逻辑 |
| 回滚验证与测试 | 0.8 | P0 | 单元测试、集成测试、端到端测试 |
| 升级前置检查自动化 | 0.5 | P0 | Pre-Check 自动化 |
| 升级后验证自动化 | 0.5 | P0 | Post-Upgrade 自动化验证 |
| 文档与代码审查 | 0.2 | P0 | API 文档、代码审查记录 |
| **小计** | **5.5** | | |

### 6.3 阶段三：版本回滚 + 应用备份（P1）

| 任务 | 工作量(人月) | 优先级 | 说明 |
|------|-------------|--------|------|
| 版本回滚方案设计 | 0.5 | P1 | 方案二设计、兼容性分析 |
| 版本回滚逻辑开发 | 1.0 | P1 | 复用升级流程执行降级、版本验证 |
| 回滚路径验证开发 | 0.8 | P1 | UpgradePath CRD 扩展、路径验证 |
| 应用数据备份（Velero） | 0.8 | P1 | Velero 集成、备份策略 |
| PVC 快照备份 | 0.5 | P1 | SnapshotClass、测试 |
| UpgradePath CRD 增强 | 0.5 | P1 | 跨版本路径验证、兼容性矩阵 |
| 停机窗口最小化策略 | 0.5 | P1 | PDB 集成、滚动升级优化 |
| 升级文档与最佳实践 | 0.3 | P1 | 升级操作手册 |
| 集成测试与文档 | 0.4 | P1 | 集成测试报告、用户手册 |
| **小计** | **5.3** | | |

### 6.4 阶段四：降级 DAG（P1）

| 任务 | 工作量(人月) | 优先级 | 说明 |
|------|-------------|--------|------|
| 降级 DAG 设计 | 0.8 | P1 | 反向拓扑排序、组件依赖分析 |
| 降级 DAG 开发 | 3.5 | P1 | DAG 编排器、反向执行逻辑 |
| 组件降级逻辑开发 | 1.5 | P1 | etcd/Agent/Containerd/Master/Worker 降级 |
| 回滚历史管理 | 0.5 | P1 | UpgradeHistory 扩展、查询接口 |
| **小计** | **6.3** | | |

### 6.5 阶段五：灾难恢复（P1）

| 任务 | 工作量(人月) | 优先级 | 说明 |
|------|-------------|--------|------|
| 灾难恢复流程设计 | 0.3 | P1 | 灾难场景分析、恢复流程设计 |
| 灾难恢复流程开发 | 1.2 | P1 | 完整流程、自动化脚本 |
| 恢复验证开发 | 0.8 | P1 | 自动化验证、健康检查 |
| 灾难恢复演练 | 0.4 | P1 | 演练脚本、演练执行、问题修复 |
| **小计** | **2.7** | | |

### 6.6 工作量汇总

| 阶段 | 工作量(人月) | 优先级 | 对应里程碑 |
|------|-------------|--------|-----------|
| 阶段一：基础备份与安装清理 | 3.0 | P0 | M1: 2026Q4 |
| 阶段二：PhaseFlow 回滚 | 5.5 | P0 | M2: 2027Q1 |
| 阶段三：版本回滚 + 应用备份 | 5.3 | P1 | M3: 2027Q2 |
| 阶段四：降级 DAG | 6.3 | P1 | M4: 2027Q3 |
| 阶段五：灾难恢复 | 2.7 | P1 | M5: 2027Q4 |
| **总计** | **22.8** | | |

---

## 七、特性规划与里程碑

### 7.1 特性总览

#### 7.1.1 安装清理特性（M1）

| 序号 | 特性 | 人月 | 说明 |
|------|------|------|------|
| I1 | 安装失败清理脚本 | **0.8** | 脚本开发、多环境测试（计入 M1） |
| | **安装清理小计** | **0.8** | |

#### 7.1.2 备份能力特性（M1、M3）

| 序号 | 特性 | 人月 | 说明 |
|------|------|------|------|
| B1 | etcd 自动备份 | **0.5** | 定时任务、清理、监控 |
| B2 | 配置与证书备份 | **0.3** | 备份脚本、验证 |
| B3 | 应用数据备份（Velero） | **0.8** | Velero 集成、配置 |
| B4 | PVC 快照备份 | **0.5** | SnapshotClass、测试 |
| B5 | 备份存储与加密 | **0.5** | 多存储后端、加密 |
| | **备份能力小计** | **2.6** | |

#### 7.1.3 恢复能力特性（M1）

| 序号 | 特性 | 人月 | 说明 |
|------|------|------|------|
| C1 | etcd 恢复 | **0.5** | 恢复脚本、验证 |
| C2 | 配置恢复 | **0.3** | 恢复脚本 |
| C3 | 应用数据恢复 | **0.3** | Velero 恢复 |
| | **恢复能力小计** | **1.1** | |

#### 7.1.4 升级能力特性（M2、M3）

| 序号 | 特性 | 人月 | 说明 |
|------|------|------|------|
| U1 | 升级前置检查自动化 | **0.5** | Pre-Check 自动化（集群健康、备份验证、路径验证） |
| U2 | 升级后验证自动化 | **0.5** | Post-Upgrade 自动化验证（组件健康、版本一致性） |
| U3 | UpgradePath CRD 增强 | **0.5** | 跨版本路径验证、兼容性矩阵管理 |
| U4 | 停机窗口最小化策略 | **0.5** | PDB 集成、滚动升级策略优化 |
| U5 | 升级文档与最佳实践 | **0.3** | 升级操作手册、升级前检查清单 |
| | **升级能力小计** | **2.3** | |

#### 7.1.5 回滚能力特性（M2、M3、M4）

| 序号 | 特性 | 人月 | 说明 |
|------|------|------|------|
| R1 | PhaseFlow 回滚流程设计与评审 | **0.5** | 需求分析、方案设计、技术评审 |
| R2 | PhaseFlow 回滚触发机制开发 | **0.8** | 手动/自动触发、CLI 命令、注解处理 |
| R3 | PhaseFlow 回滚状态转换规则开发 | **1.2** | 状态机扩展、转换规则、兼容性验证 |
| R4 | PhaseFlow 资源清理逻辑开发 | **1.0** | 扩缩容/配置变更/删除的清理逻辑 |
| R5 | PhaseFlow 回滚验证与测试 | **0.8** | 单元测试、集成测试、端到端测试 |
| R7 | ClusterVersion 版本回滚方案设计 | **0.5** | 方案二设计、兼容性分析 |
| R8 | ClusterVersion 版本回滚逻辑开发 | **1.0** | 复用升级流程执行降级、版本验证 |
| R9 | ClusterVersion 回滚路径验证开发 | **0.8** | UpgradePath CRD 扩展、路径验证 |
| R10 | ClusterVersion 降级 DAG 设计 | **0.8** | 反向拓扑排序、组件依赖分析 |
| R11 | ClusterVersion 降级 DAG 开发 | **3.5** | DAG 编排器、反向执行逻辑 |
| R12 | 组件降级逻辑开发 | **1.5** | etcd/Agent/Containerd/Master/Worker 降级 |
| R13 | ClusterVersion 回滚历史管理 | **0.5** | UpgradeHistory 扩展、查询接口 |
| | **回滚能力小计** | **12.9** | |

**关键复杂性**：
- 需要分析每种操作类型（扩缩容、配置变更、删除）的回滚场景
- 需要实现资源清理逻辑，确保无残留
- 需要保证与现有状态机的兼容性
- 需要分析每个组件（Agent、Containerd、Master、Worker）的回滚方案
- 需要保证版本兼容性（Kubernetes 版本倾斜策略）
- 需要处理 etcd 数据格式兼容性问题
- 需要实现反向拓扑排序的 DAG 编排器
- 需要为每个组件实现降级逻辑（etcd 最复杂）
- 需要大量的集成测试验证回滚正确性

#### 7.1.6 灾难恢复特性（M5）

| 序号 | 特性 | 人月 | 说明 |
|------|------|------|------|
| C4 | 灾难恢复流程设计 | **0.3** | 灾难场景分析、恢复流程设计 |
| C5 | 灾难恢复流程开发 | **1.2** | 完整流程、自动化脚本 |
| C6 | 恢复验证开发 | **0.8** | 自动化验证、健康检查 |
| C7 | 灾难恢复演练 | **0.4** | 演练脚本、演练执行、问题修复 |
| | **灾难恢复小计** | **2.7** | |

### 7.2 各组件回滚方案分析

#### 7.3.1 组件清单

| 组件 | 部署方式 | 数据持久化 | 依赖关系 |
|------|---------|-----------|---------|
| **etcd** | 静态 Pod | /var/lib/etcd | 依赖 containerd；被 API Server 依赖 |
| **BKE Agent** | SSH 推送二进制 + systemd | 无状态 | 依赖 containerd；被其他升级阶段依赖 |
| **Containerd** | bkeadm 脚本部署 | 容器镜像缓存 | 依赖 OS；被 kubelet/etcd/API Server 依赖 |
| **Master 组件** | 静态 Pod | 无状态（状态在 etcd） | 依赖 etcd/containerd；被 Worker 依赖 |
| **Worker 组件** | systemd + DaemonSet | Pod 本地数据 | 依赖 containerd/API Server |
| **配置** | ConfigMap/Secret + 本地文件 | etcd + 本地文件系统 | 无 |
| **证书和密钥** | PKI 目录 | 本地文件系统 | 被所有组件依赖（TLS） |

#### 7.3.2 各组件回滚方案

| 组件 | 回滚方案 | 复杂度 | 工作量(人月) | 关键挑战 |
|------|---------|--------|-------------|---------|
| **BKE Agent** | SSH 重新推送旧版本二进制 + 重启服务 | 简单 | 0.2 | SSH 连通性；服务状态清理 |
| **证书和密钥** | 从备份恢复 PKI 目录 | 简单 | 0.1 | 证书链验证；信任存储更新 |
| **Containerd** | 重置 + 降级到旧版本（复用现有逻辑） | 中等 | 0.3 | 需要驱逐容器；镜像缓存失效 |
| **Master 组件** | 停止静态 Pod → 替换二进制/清单 → 重启 | 中等 | 0.5 | API Server 可用性；证书兼容性 |
| **Worker 组件** | 驱逐 Pod → 停止 kubelet → 替换二进制 → 重启 | 中等 | 0.3 | Pod 驱逐；节点可用性 |
| **etcd** | 恢复快照 + 降级二进制 | **复杂** | 0.6 | 数据格式兼容性；集群仲裁 |
| **配置** | 从备份恢复 ConfigMap/Secret + 重新应用本地配置 | 中等 | 0.3 | 配置漂移；Schema 兼容性 |
| **DAG 编排器** | 反向拓扑排序执行 | **复杂** | 0.5 | 反向执行逻辑；错误处理 |

#### 7.3.3 回滚顺序（反向拓扑）

```
升级顺序：
pre-upgrade-resources → bkeagent + containerd → etcd → master → worker → kube-proxy + coredns

回滚顺序（反向）：
kube-proxy + coredns → worker → master → etcd → bkeagent + containerd → pre-upgrade-resources
```

#### 7.3.4 兼容性约束

| 约束 | 说明 |
|------|------|
| **Kubernetes 版本倾斜** | kubelet 可以与 API Server 相差 ±1 个小版本 |
| **etcd 兼容性** | etcd 数据格式可能在不同版本间变化；回滚需要快照 |
| **Containerd CRI** | CRI API 在 containerd 和 kubelet 之间需要兼容 |
| **证书有效性** | 证书必须对所有组件有效 |
| **Agent 版本** | Agent 应该与管理集群 API 兼容 |

### 7.3 季度里程碑规划

**评估原则**：
- 工作量评估已包含：需求变更、技术难点、调试时间、集成测试、文档、代码审查、联调等隐性工作量

#### 7.4.1 里程碑总览

| 季度 | 里程碑 | 工作量(人月) | 核心交付 |
|------|--------|-------------|---------|
| **2026Q4** | M1: 基础备份与安装清理 | **3.0** | etcd 备份/恢复、配置备份、安装清理 |
| **2027Q1** | M2: PhaseFlow 回滚 + 升级验证 | **5.5** | 回滚触发、状态转换、资源清理、升级前后验证 |
| **2027Q2** | M3: 版本回滚 + 升级路径增强 | **5.3** | 版本回滚（方案二）、UpgradePath CRD 增强、停机窗口优化 |
| **2027Q3** | M4: 降级 DAG | **6.3** | 降级 DAG（方案一）、逐组件降级逻辑 |
| **2027Q4** | M5: 灾难恢复 | **2.7** | 灾难恢复流程、恢复验证、演练 |
| **总计** | | **22.8** | |

#### 7.4.2 2026Q4（M1）：基础备份与安装清理（3.0 人月）

| 特性 | 人月 | 交付物 | 验收标准 |
|------|------|--------|---------|
| B1: etcd 自动备份 | 0.5 | 定时备份脚本、清理脚本、监控告警 | 每日自动备份，保留7天 |
| B2: 配置与证书备份 | 0.3 | 配置备份脚本、证书备份脚本 | 配置和证书可完整备份 |
| C1: etcd 恢复 | 0.5 | etcd 恢复脚本、验证脚本 | etcd 数据可完整恢复 |
| C2: 配置恢复 | 0.3 | 配置恢复脚本 | 配置可完整恢复 |
| R6: 安装失败清理脚本 | 0.8 | 清理脚本、多环境测试报告 | 安装失败可清理重建 |
| 集成测试与文档 | 0.6 | 集成测试报告、用户手册 | 所有功能验证通过 |

**总计：3.0 人月**

#### 7.4.3 2027Q1（M2）：PhaseFlow 回滚 + 升级验证（5.5 人月）

| 特性 | 人月 | 交付物 | 验收标准 |
|------|------|--------|---------|
| R1: 回滚流程设计与评审 | 0.5 | 设计文档、评审纪要 | 方案评审通过 |
| R2: 回滚触发机制开发 | 0.8 | 触发机制、CLI 命令 | 手动/自动触发回滚 |
| R3: 回滚状态转换规则开发 | 1.2 | 状态转换规则、单元测试 | 扩缩容/配置变更可回滚 |
| R4: 资源清理逻辑开发 | 1.0 | 清理逻辑、边界测试 | 失败资源可清理 |
| R5: 回滚验证与测试 | 0.8 | 端到端测试、测试报告 | 回滚后集群健康 |
| U1: 升级前置检查自动化 | 0.5 | Pre-Check 自动化 | 升级前自动验证集群健康 |
| U2: 升级后验证自动化 | 0.5 | Post-Upgrade 验证 | 升级后自动验证组件版本 |
| 文档与代码审查 | 0.2 | API 文档、代码审查记录 | 文档完整、代码质量达标 |

**总计：5.5 人月**

**关键复杂性**：
- 需要分析每种操作类型（扩缩容、配置变更、删除）的回滚场景
- 需要实现资源清理逻辑，确保无残留
- 需要保证与现有状态机的兼容性
- 需要大量的集成测试验证回滚正确性

#### 7.4.4 2027Q2（M3）：版本回滚 + 升级路径增强（5.3 人月）

| 特性 | 人月 | 交付物 | 验收标准 |
|------|------|--------|---------|
| R7: 版本回滚方案设计 | 0.5 | 设计文档、兼容性分析 | 方案评审通过 |
| R8: 版本回滚逻辑开发 | 1.0 | 重新部署逻辑、测试报告 | 升级失败可回滚到旧版本 |
| R9: 回滚路径验证开发 | 0.8 | 路径验证逻辑、单元测试 | 回滚路径可验证 |
| U3: UpgradePath CRD 增强 | 0.5 | 跨版本路径验证、兼容性矩阵 | 升级路径可管理 |
| U4: 停机窗口最小化策略 | 0.5 | PDB 集成、滚动升级优化 | 升级停机影响最小化 |
| B3: 应用数据备份（Velero） | 0.8 | Velero 集成、备份策略 | 应用数据可自动备份 |
| B4: PVC 快照备份 | 0.5 | SnapshotClass、备份脚本 | PVC 数据可快照备份 |
| U5: 升级文档与最佳实践 | 0.3 | 升级操作手册 | 升级流程文档化 |
| 集成测试与文档 | 0.4 | 集成测试报告、用户手册 | 版本回滚和应用备份验证通过 |

**总计：5.3 人月**

**关键复杂性**：
- 需要分析每个组件（Agent、Containerd、Master、Worker）的回滚方案
- 需要保证版本兼容性（Kubernetes 版本倾斜策略）
- 需要处理 etcd 数据格式兼容性问题
- 需要集成 Velero 并配置备份策略
- 需要验证回滚后集群健康

#### 7.4.5 2027Q3（M4）：降级 DAG（6.3 人月）

| 特性 | 人月 | 交付物 | 验收标准 |
|------|------|--------|---------|
| R10: 降级 DAG 设计 | 0.8 | 设计文档、依赖分析 | 方案评审通过 |
| R11: 降级 DAG 开发 | 3.5 | 降级 DAG、逐组件降级逻辑 | 复杂场景可降级 |
| R12: 组件降级逻辑开发 | 1.5 | etcd/Agent/Containerd/Master/Worker 降级 | 各组件可独立降级 |
| R13: 回滚历史管理 | 0.5 | 历史记录、查询接口 | 回滚历史可查询 |

**总计：6.3 人月**

**关键复杂性**：
- 需要实现反向拓扑排序的 DAG 编排器
- 需要为每个组件实现降级逻辑（etcd 最复杂）
- 需要处理组件间的依赖关系和兼容性

#### 7.4.6 2027Q4（M5）：灾难恢复（2.7 人月）

| 特性 | 人月 | 交付物 | 验收标准 |
|------|------|--------|---------|
| C4: 灾难恢复流程设计 | 0.3 | 设计文档、场景分析 | 方案评审通过 |
| C5: 灾难恢复流程开发 | 1.2 | 完整流程、演练脚本 | 整个集群故障可恢复 |
| C6: 恢复验证开发 | 0.8 | 自动化验证、报告生成 | 恢复后可自动验证 |
| C7: 灾难恢复演练 | 0.4 | 演练报告、问题修复 | 演练通过 |

**总计：2.7 人月**

**关键复杂性**：
- 需要实现完整的灾难恢复流程
- 需要进行多次灾难恢复演练验证

### 7.4 里程碑依赖关系

```
2026Q4 (M1)
  ├─ 基础备份能力 ──────────────────────┐
  │   ├─ etcd 备份/恢复                 │
  │   ├─ 配置备份/恢复                  │
  │   └─ 安装失败清理                   │
  │                                     │
  └─ 输出：备份基础设施 ────────────────┼─→ 2027Q1 (M2)
                                        │     ├─ PhaseFlow 回滚
                                        │     │   ├─ 回滚触发机制
                                        │     │   ├─ 状态转换规则
                                        │     │   └─ 资源清理逻辑
                                        │     │
                                        │     └─ 输出：操作回滚能力 ──→ 2027Q2 (M3)
                                        │                               ├─ 版本回滚（方案二）
                                        │                               ├─ 应用备份（Velero）
                                        │                               │
                                        │                               └─ 输出：版本回滚能力 ──→ 2027Q3 (M4)
                                        │                                                       ├─ 降级 DAG（方案一）
                                        │                                                       │
                                        │                                                       └─ 输出：降级能力 ──→ 2027Q4 (M5)
                                        │                                                                          └─ 灾难恢复
                                        │
  备份能力 ─────────────────────────────┘
```

**关键依赖**：
1. **M1 → M2**：PhaseFlow 回滚需要备份基础设施支持（回滚前需要备份当前状态）
2. **M2 → M3**：版本回滚需要 PhaseFlow 回滚能力支持（版本回滚后可能需要操作回滚）
3. **M3 → M4**：降级 DAG 需要版本回滚能力支持（降级 DAG 是版本回滚的增强版）
4. **M4 → M5**：灾难恢复需要降级能力支持（灾难恢复可能涉及版本降级）

### 7.5 工作量汇总

| 类别 | 人月 | 占比 |
|------|------|------|
| 回滚能力 | 12.9 | 57% |
| 安装清理 | 0.8 | 4% |
| 备份能力 | 2.6 | 11% |
| 恢复能力 | 1.1 | 5% |
| 升级能力 | 2.3 | 10% |
| 灾难恢复 | 2.7 | 12% |
| **能力小计** | **22.4** | |
| 集成测试与文档（M1、M3） | -0.6 | |
| **总计** | **22.8** | **100%** |

**说明**：
- 总工作量 **22.8 人月**，分散在 5 个季度
- 回滚能力占比最高（57%），是核心能力
- 每个里程碑都有明确的交付物和验收标准
- 集成测试与文档（M1: 0.6）为跨特性活动，在能力特性表中不单独列出

---

## 八、最佳实践

### 8.1 备份最佳实践

| 实践 | 说明 |
|------|------|
| **定期备份** | 每天自动备份 etcd 和应用数据 |
| **备份验证** | 每周验证备份可恢复性 |
| **异地存储** | 备份文件存储到异地（至少 2 个位置） |
| **备份加密** | 敏感数据备份必须加密 |
| **备份监控** | 监控备份任务成功率和存储空间 |

### 8.2 回滚最佳实践

| 实践 | 说明 |
|------|------|
| **升级前备份** | 升级前必须执行完整备份 |
| **小步升级** | 避免跨多个版本升级 |
| **灰度发布** | 先在测试环境验证，再升级到生产 |
| **回滚演练** | 定期演练回滚流程 |
| **文档记录** | 记录每次升级和回滚的详细信息 |

### 8.3 升级前检查清单

```yaml
preUpgradeChecklist:
  # 1. 集群健康检查
  clusterHealth:
    - "所有节点处于 Ready 状态"
    - "所有 Operator 处于 Available 状态"
    - "etcd 集群健康"
    
  # 2. 备份验证
  backupVerification:
    - "etcd 备份已完成且可恢复"
    - "应用数据备份已完成"
    - "配置备份已完成"
    
  # 3. 升级路径验证
  upgradePathValidation:
    - "目标版本在支持的升级路径中"
    - "升级镜像可访问"
    - "升级前置条件已满足"
    
  # 4. 回滚计划
  rollbackPlan:
    - "回滚目标版本已确定"
    - "回滚流程已文档化"
    - "回滚演练已完成"
```

---

## 九、总结

### 9.1 核心能力

| 能力 | 支持情况 | 说明 |
|------|---------|------|
| **升级能力** | ✅ 支持（设计中） | 相邻版本升级、LTS 逐跳升级、Pre/Post-Check 自动化 |
| **安装回滚** | ❌ 不支持 | 安装失败时清理并重建 |
| **升级回滚** | ✅ 支持（设计中） | 自动/手动回滚到上一版本 |
| **扩缩容回滚** | ✅ 支持（设计中） | 状态机回滚 + 资源清理 |
| **配置回滚** | ✅ 支持（设计中） | 配置版本管理和回滚 |
| **etcd 备份** | ✅ 支持 | 自动/手动备份 |
| **配置备份** | ✅ 支持 | 集群配置备份 |
| **应用备份** | ✅ 支持 | Velero 集成 |
| **灾难恢复** | ✅ 支持 | 完整的恢复流程 |

### 9.2 关键设计决策

1. **安装不支持回滚**：与 OpenShift 一致，状态不可逆
2. **PhaseFlow 基于状态机引擎回滚**：利用现有 `statemachine.Engine`
3. **ClusterVersion 不负责执行回滚**：只负责验证路径，由 DAG 执行
4. **降级 DAG 参考升级 DAG 设计**：顺序相反，逻辑类似
5. **备份支持多种存储**：本地、S3、NFS、OSS
6. **升级采用逐跳策略**：只能升级到相邻版本，LTS 升级需逐跳进行
7. **升级前后自动化验证**：Pre-Check 阻止不满足条件的升级，Post-Upgrade 确认升级成功

### 9.3 后续工作

1. **实现升级前置检查自动化**：Pre-Check 自动验证集群健康、备份状态、升级路径
2. **实现升级后验证自动化**：Post-Upgrade 自动验证组件版本、集群健康
3. **实现 PhaseFlow 回滚机制**：添加 `TriggerRollback` 触发器和转换规则
4. **实现 ClusterVersion 回滚验证**：验证回滚路径，设置降级目标版本
5. **实现降级 DAG**：参考升级 DAG 实现降级执行器
6. **实现自动备份脚本**：实现 etcd 和配置的自动备份
7. **实现恢复验证工具**：自动验证备份可恢复性
8. **增强 UpgradePath CRD**：支持跨版本路径验证、兼容性矩阵管理
9. **文档化升级与回滚流程**：编写详细的升级和回滚操作手册

---

**文档版本**：v2.0（设计提案）  
**创建日期**：2024-01-15  
**最后更新**：2024-01-15  
**维护者**：BKE 团队
