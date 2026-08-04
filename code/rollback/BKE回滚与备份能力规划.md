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
- **没有回滚机制**：`ClusterUpgradeRecord.RolledBack` 状态已定义但从未使用

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

## 二、回滚能力设计提案

### 2.1 设计目标

为 BKE 构建完整的回滚能力，覆盖以下场景：

| 场景 | 优先级 | 回滚策略 |
|------|--------|---------|
| **升级失败** | P0 | 版本回滚（降级） |
| **扩缩容失败** | P1 | 状态回滚 + 资源清理 |
| **配置变更失败** | P1 | 配置回滚 |
| **安装失败** | P0 | 清理重建（状态不可逆） |

#### 2.1.1 升级失败场景说明

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
1. ClusterVersion 验证回滚路径（v26.06 → v26.05）
2. 拉取旧版本 ReleaseImage
3. 设置 `cvo.openfuyao.cn/rollback-ready=v26.05` 注解
4. BKECluster 控制器执行降级 DAG
5. 按相反顺序降级所有组件（Worker → Master → etcd → Containerd → Agent）
6. 更新 ClusterVersion.status.currentVersion = v26.05
7. ClusterStatus = `Ready`

**回滚目标**：
- 所有组件恢复到 v26.05 版本
- 集群状态恢复到 `Ready`
- 业务应用恢复正常运行

**影响范围**：
- 控制面组件（API Server、etcd、Controller Manager、Scheduler）
- 节点组件（kubelet、containerd、BKE Agent）
- 业务应用（短暂不可用，降级完成后恢复）

**预计恢复时间**：15-30 分钟

#### 2.1.2 扩缩容失败场景说明

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

#### 2.1.3 配置变更失败场景说明

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

#### 2.1.4 安装失败场景说明

**场景描述**：
集群安装过程中，某个阶段失败，导致集群无法完成初始化。

**失败原因**：
- Bootstrap 节点创建失败
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
- 云资源（VM、网络、存储）已创建，无法简单回滚
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

**影响范围**：
- 所有已创建的基础设施资源
- 云资源（VM、网络、存储）
- 证书和配置文件

**预计恢复时间**：10-30 分钟（清理重建）

### 2.2 PhaseFlow 回滚设计

#### 2.2.1 设计原则

**基于现有状态机引擎设计回滚规则**

PhaseFlow 已集成状态机引擎（`statemachine.Engine`），当前支持的触发器：
- `TriggerPhaseComplete`：Phase 执行成功
- `TriggerError`：Phase 执行失败
- `TriggerRetry`：重试操作

**需要新增**：
- `TriggerRollback`：回滚操作触发器

#### 2.2.2 回滚状态转换规则

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
    
    // DeleteFailed → Ready（删除失败回滚）
    e.AddTransition(statemachine.Transition{
        FromState: bkev1beta1.ClusterDeleteFailed,
        ToState:   bkev1beta1.ClusterReady,
        Trigger:   "Rollback",
        Condition: isDeleteRollbackComplete,
        Action:    retryDeleteOperation,
    })
}
```

#### 2.2.3 回滚触发机制

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

#### 2.2.4 回滚执行流程

```
PhaseFlow 回滚流程（以扩缩容失败为例）：

1. 检测到扩缩容失败
   └─ ClusterStatus: WorkerScalingUp → ScaleFailed
   └─ PhaseStatus[WorkerScalingUp] = PhaseFailed

2. 触发回滚
   └─ 用户执行：bkectl rollback cluster my-cluster
   └─ 或自动触发：重试次数超限

3. PhaseFlow 检测回滚注解
   └─ 检测到 bke.bocloud.com/rollback=true
   └─ 调用状态机引擎：engine.Transition(cluster, "Rollback", nil)

4. 状态机执行回滚转换
   └─ ScaleFailed → Ready
   └─ 执行 Action：cleanupFailedScaleResources
      ├─ 删除未就绪的 Worker 节点
      ├─ 清理相关 ConfigMap/Secret
      └─ 恢复节点池配置

5. 清除回滚注解
   └─ 删除 bke.bocloud.com/rollback 注解
   └─ 集群恢复到 Ready 状态
```

### 2.3 ClusterVersion 回滚设计

#### 2.3.1 设计原则

**ClusterVersion 不负责执行回滚，只负责触发**

- ClusterVersion 验证回滚路径（类似升级路径验证）
- 设置 `cvo.openfuyao.cn/rollback-ready` 注解
- 由 BKECluster 控制器执行降级 DAG

#### 2.3.2 回滚触发流程

```
ClusterVersion 回滚流程：

1. 用户设置回滚目标
   └─ kubectl patch clusterversion --type merge \
       -p '{"spec":{"desiredVersion":"v26.05"}}'

2. ClusterVersion Controller：
   ├─ 验证回滚路径（v26.06 → v26.05）
   ├─ 拉取旧版本 ReleaseImage
   ├─ 设置注解：cvo.openfuyao.cn/rollback-ready=v26.05
   └─ Phase → RollingBack

3. BKECluster Controller 检测到注解：
   ├─ shouldUseDeclarativeRollback() 返回 true
   └─ executeRollbackDAG() 执行降级 DAG

4. 降级 DAG 执行：
   ├─ EnsureWorkerDowngrade
   ├─ EnsureMasterDowngrade
   ├─ EnsureEtcdDowngrade
   ├─ EnsureContainerdDowngrade
   └─ EnsureAgentDowngrade

5. 降级完成：
   ├─ 清除 rollback-ready 注解
   ├─ 更新 ClusterVersion.status
   │   ├─ CurrentVersion: v26.05
   │   ├─ Phase: Ready
   │   └─ UpgradeHistory: append({From: v26.06, To: v26.05, Status: RolledBack})
   └─ ClusterStatus: Ready
```

#### 2.3.3 降级 DAG 设计

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

### 2.4 双机制协同设计

#### 2.4.1 职责划分

| 场景 | PhaseFlow 职责 | ClusterVersion 职责 | DAG 职责 |
|------|---------------|---------------------|---------|
| **升级失败** | 保持当前状态 | 验证回滚路径，设置注解 | 执行降级 DAG |
| **扩缩容失败** | 执行回滚，清理资源 | 不参与 | 不参与 |
| **配置变更失败** | 执行回滚，恢复配置 | 不参与 | 不参与 |
| **安装失败** | 清理重建 | 不参与 | 不参与 |

#### 2.4.2 协同回滚场景

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
   └─ 设置 rollback-ready 注解

4. BKECluster 执行降级 DAG
   └─ 降级所有组件到 v26.05
   └─ 更新 ClusterVersion.status

5. 集群恢复到 v26.05
   └─ ClusterVersion.status.currentVersion: v26.05
   └─ ClusterStatus: Ready
```

### 2.5 安装失败处理

#### 2.5.1 设计原则

**安装过程不支持自动回滚，与 OpenShift 一致**

| 因素 | 说明 |
|------|------|
| **状态不可逆** | etcd 数据、证书、网络配置一旦创建无法简单回滚 |
| **基础设施耦合** | 云资源（VM、网络、存储）已创建 |
| **时间窗口** | 安装失败通常在早期阶段，重建比回滚更快 |

#### 2.5.2 安装失败处理策略

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

#### 2.5.3 清理脚本设计

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

## 三、备份能力规划

### 3.1 etcd 备份

#### 3.1.1 备份策略

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

#### 3.1.2 备份脚本

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

#### 3.1.3 备份产物

| 文件名 | 格式 | 内容 | 大小（典型值） |
|--------|------|------|----------------|
| `etcd_snapshot_<timestamp>.db.gz` | 压缩快照 | etcd 完整数据 | 50-200 MB |
| `bke_config_<timestamp>.yaml` | YAML | BKE 集群配置 | < 1 MB |
| `certificates_<timestamp>.tar.gz` | 压缩归档 | 证书和密钥 | 1-5 MB |

### 3.2 配置备份

#### 3.2.1 备份内容

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

#### 3.2.2 备份脚本

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

### 3.3 应用数据备份

#### 3.3.1 备份策略

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

#### 3.3.2 Velero 配置

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

### 3.4 备份存储

#### 3.4.1 存储位置

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

#### 3.4.2 备份加密

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

## 四、恢复能力规划

### 4.1 etcd 恢复

#### 4.1.1 恢复流程

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

#### 4.1.2 恢复后验证

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

### 4.2 配置恢复

#### 4.2.1 恢复流程

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

### 4.3 应用数据恢复

#### 4.3.1 Velero 恢复

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

### 4.4 灾难恢复

#### 4.4.1 灾难恢复场景

| 场景 | 恢复策略 | 预计恢复时间 |
|------|---------|-------------|
| **单节点故障** | 自动替换节点 | 10-30 分钟 |
| **控制面故障** | 从备份恢复 etcd | 30-60 分钟 |
| **整个集群故障** | 重建集群 + 恢复数据 | 2-4 小时 |
| **数据丢失** | 从备份恢复数据 | 1-2 小时 |

#### 4.4.2 灾难恢复流程

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

## 五、实施计划

### 5.1 阶段一：基础能力（P0）

| 任务 | 工作量 | 优先级 | 说明 |
|------|--------|--------|------|
| etcd 自动备份 | 5 天 | P0 | 实现定时备份和清理 |
| etcd 恢复脚本 | 3 天 | P0 | 实现 etcd 快照恢复 |
| 配置备份 | 3 天 | P0 | 实现集群配置备份 |
| 安装失败清理 | 5 天 | P0 | 实现安装失败清理脚本 |

### 5.2 阶段二：PhaseFlow 回滚（P0）

| 任务 | 工作量 | 优先级 | 说明 |
|------|--------|--------|------|
| 回滚触发机制 | 5 天 | P0 | 实现手动/自动触发回滚 |
| 回滚状态转换规则 | 8 天 | P0 | 实现 *Failed → Ready 转换 |
| 资源清理逻辑 | 5 天 | P0 | 实现失败资源清理 |
| 回滚验证测试 | 5 天 | P0 | 实现回滚后验证 |

### 5.3 阶段三：ClusterVersion 回滚（P1）

| 任务 | 工作量 | 优先级 | 说明 |
|------|--------|--------|------|
| 回滚路径验证 | 5 天 | P1 | 实现回滚路径验证逻辑 |
| 降级 DAG 设计 | 8 天 | P1 | 实现降级 DAG 执行器 |
| 降级组件实现 | 10 天 | P1 | 实现各组件降级逻辑 |
| 回滚历史管理 | 3 天 | P1 | 实现回滚历史记录 |

### 5.4 阶段四：应用备份（P1）

| 任务 | 工作量 | 优先级 | 说明 |
|------|--------|--------|------|
| Velero 集成 | 5 天 | P1 | 集成 Velero 备份应用数据 |
| PVC 快照 | 3 天 | P1 | 实现 PVC 快照备份 |

### 5.5 阶段五：灾难恢复（P2）

| 任务 | 工作量 | 优先级 | 说明 |
|------|--------|--------|------|
| 灾难恢复流程 | 8 天 | P2 | 实现完整的灾难恢复流程 |
| 恢复验证 | 5 天 | P2 | 实现恢复后的自动验证 |

---

## 六、最佳实践

### 6.1 备份最佳实践

| 实践 | 说明 |
|------|------|
| **定期备份** | 每天自动备份 etcd 和应用数据 |
| **备份验证** | 每周验证备份可恢复性 |
| **异地存储** | 备份文件存储到异地（至少 2 个位置） |
| **备份加密** | 敏感数据备份必须加密 |
| **备份监控** | 监控备份任务成功率和存储空间 |

### 6.2 回滚最佳实践

| 实践 | 说明 |
|------|------|
| **升级前备份** | 升级前必须执行完整备份 |
| **小步升级** | 避免跨多个版本升级 |
| **灰度发布** | 先在测试环境验证，再升级到生产 |
| **回滚演练** | 定期演练回滚流程 |
| **文档记录** | 记录每次升级和回滚的详细信息 |

### 6.3 升级前检查清单

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

## 七、总结

### 7.1 核心能力

| 能力 | 支持情况 | 说明 |
|------|---------|------|
| **安装回滚** | ❌ 不支持 | 安装失败时清理并重建 |
| **升级回滚** | ✅ 支持（设计中） | 自动/手动回滚到上一版本 |
| **扩缩容回滚** | ✅ 支持（设计中） | 状态机回滚 + 资源清理 |
| **配置回滚** | ✅ 支持（设计中） | 配置版本管理和回滚 |
| **etcd 备份** | ✅ 支持 | 自动/手动备份 |
| **配置备份** | ✅ 支持 | 集群配置备份 |
| **应用备份** | ✅ 支持 | Velero 集成 |
| **灾难恢复** | ✅ 支持 | 完整的恢复流程 |

### 7.2 关键设计决策

1. **安装不支持回滚**：与 OpenShift 一致，状态不可逆
2. **PhaseFlow 基于状态机引擎回滚**：利用现有 `statemachine.Engine`
3. **ClusterVersion 不负责执行回滚**：只负责验证路径，由 DAG 执行
4. **降级 DAG 参考升级 DAG 设计**：顺序相反，逻辑类似
5. **备份支持多种存储**：本地、S3、NFS、OSS

### 7.3 后续工作

1. **实现 PhaseFlow 回滚机制**：添加 `TriggerRollback` 触发器和转换规则
2. **实现 ClusterVersion 回滚验证**：验证回滚路径，设置 `rollback-ready` 注解
3. **实现降级 DAG**：参考升级 DAG 实现降级执行器
4. **实现自动备份脚本**：实现 etcd 和配置的自动备份
5. **实现恢复验证工具**：自动验证备份可恢复性
6. **文档化回滚流程**：编写详细的回滚操作手册

---

**文档版本**：v2.0（设计提案）  
**创建日期**：2024-01-15  
**最后更新**：2024-01-15  
**维护者**：BKE 团队
