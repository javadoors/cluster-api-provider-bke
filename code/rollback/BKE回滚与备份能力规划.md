# BKE 回滚与备份能力规划

## 一、概述

本规划基于 OpenShift 集群的回滚和备份能力设计，结合 BKE 状态机架构特点，制定适合 BKE 的回滚和备份能力方案。

### 1.1 设计目标

| 目标 | 说明 | 优先级 |
|------|------|--------|
| **安装失败处理** | 安装失败时支持清理和重建 | P0 |
| **升级回滚能力** | 升级失败时支持自动/手动回滚 | P0 |
| **扩缩容回滚** | 扩缩容失败时支持回滚到原状态 | P1 |
| **配置备份** | 集群配置、证书、etcd 数据备份 | P0 |
| **灾难恢复** | 支持从备份恢复集群 | P1 |

### 1.2 与 OpenShift 的差异

| 维度 | OpenShift | BKE | 说明 |
|------|-----------|-----|------|
| **状态管理** | ClusterVersion CRD | ClusterStatus 字段 | BKE 使用状态机管理 |
| **回滚触发** | spec vs status 对比 | 状态转换引擎 | BKE 使用 Engine 驱动 |
| **失败记录** | RolledBack 状态 | LastInProgressState | BKE 记录失败前状态 |
| **重试机制** | CVO 调谐循环 | StatusManagerV2 | BKE 集中管理重试 |

---

## 二、回滚能力规划

BKE 采用双机制架构：**PhaseFlow**（阶段流）和 **ClusterVersion**（版本管理），回滚能力需要基于这两套机制分别设计。

### 2.1 双机制架构概述

```
┌─────────────────────────────────────────────────────────────┐
│                    BKE 双机制架构                              │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  机制一：PhaseFlow（阶段流）                                  │
│  ├─ 适用场景：安装、扩缩容、删除、配置变更                      │
│  ├─ 核心组件：PhaseFlow、Phase、PhaseContext                  │
│  ├─ 状态管理：ClusterStatus 字段（状态机）                    │
│  ├─ 执行逻辑：NeedExecute() → Execute() → ReportStatus()    │
│  └─ 回滚策略：状态机回滚 + Phase 重试                         │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  机制二：ClusterVersion（版本管理）                           │
│  ├─ 适用场景：集群升级、版本回滚                              │
│  ├─ 核心组件：ClusterVersion CRD、UpgradeHistory            │
│  ├─ 状态管理：ClusterVersion.status（版本状态）              │
│  ├─ 执行逻辑：声明式 DAG → 逐跳升级 → 状态同步              │
│  └─ 回滚策略：版本回滚 + 历史记录恢复                         │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 PhaseFlow 回滚能力

#### 2.2.1 PhaseFlow 回滚机制

PhaseFlow 通过状态机管理集群状态，回滚基于以下机制：

```go
// PhaseFlow 回滚核心设计
type PhaseFlowRollback struct {
    engine        *statemachine.Engine
    statusManager *StatusManagerV2
    phaseHistory  []PhaseExecutionRecord
}

// 触发 PhaseFlow 回滚
func (r *PhaseFlowRollback) TriggerRollback(cluster *BKECluster) error {
    // 1. 获取失败前的状态（LastInProgressState）
    lastState := cluster.Status.LastInProgressState
    if lastState == "" {
        return fmt.Errorf("no rollback target found")
    }
    
    // 2. 通过状态机引擎回滚到上一状态
    return r.engine.Transition(
        cluster,
        nil,
        TriggerRollback,
        nil,
    )
}
```

#### 2.2.2 PhaseFlow 回滚场景

| 场景 | 当前状态 | 回滚目标 | 回滚机制 |
|------|---------|---------|---------|
| **安装失败** | Initializing/InitializationFailed | 清理并重建 | 不支持自动回滚 |
| **扩容失败** | WorkerScalingUp/ScaleFailed | Ready | 状态机回滚 + 节点清理 |
| **缩容失败** | WorkerScalingDown/ScaleFailed | Ready | 状态机回滚 + 节点恢复 |
| **删除失败** | Deleting/DeleteFailed | Ready | 状态机回滚 + 重试删除 |
| **配置变更失败** | Managing/ManageFailed | Ready | 状态机回滚 + 配置恢复 |

#### 2.2.3 PhaseFlow 回滚状态转换

```go
// PhaseFlow 回滚转换规则
func registerPhaseFlowRollbackTransitions(e *statemachine.Engine) {
    // ScaleFailed → Ready（扩缩容回滚）
    e.AddTransition(statemachine.Transition{
        FromState: bkev1beta1.ClusterScaleFailed,
        ToState:   bkev1beta1.ClusterReady,
        Trigger:   statemachine.TriggerRollback,
        Condition: statemachine.IsScaleRollbackComplete,
        Action:    cleanupFailedScaleResources,
    })
    
    // ManageFailed → Ready（配置变更回滚）
    e.AddTransition(statemachine.Transition{
        FromState: bkev1beta1.ClusterManageFailed,
        ToState:   bkev1beta1.ClusterReady,
        Trigger:   statemachine.TriggerRollback,
        Condition: statemachine.IsManageRollbackComplete,
        Action:    restorePreviousConfig,
    })
    
    // DeleteFailed → Ready（删除失败回滚）
    e.AddTransition(statemachine.Transition{
        FromState: bkev1beta1.ClusterDeleteFailed,
        ToState:   bkev1beta1.ClusterReady,
        Trigger:   statemachine.TriggerRollback,
        Condition: statemachine.IsDeleteRollbackComplete,
        Action:    retryDeleteOperation,
    })
}
```

#### 2.2.4 PhaseFlow 回滚流程

```
PhaseFlow 回滚流程（以扩容失败为例）：

1. 检测到扩容失败
   └─ ClusterStatus: WorkerScalingUp → ScaleFailed
   └─ LastInProgressState: WorkerScalingUp
   └─ Phase 执行记录：PhaseStatus[WorkerScalingUp] = Failed

2. 触发回滚
   └─ 用户执行：bkectl rollback cluster my-cluster
   └─ 或自动触发：StatusManager 检测到重试次数超限

3. 状态机回滚
   └─ ClusterStatus: ScaleFailed → Ready
   └─ LastInProgressState: 清空

4. 清理失败资源
   └─ 删除未就绪的 Worker 节点
   └─ 清理相关 ConfigMap/Secret
   └─ 恢复节点池配置

5. 验证回滚成功
   └─ 检查集群状态：Ready
   └─ 检查节点数量：符合期望
   └─ 检查应用健康：正常运行
```

#### 2.2.5 PhaseFlow 回滚数据结构

```go
// Phase 执行记录
type PhaseExecutionRecord struct {
    // Phase 名称
    PhaseName string `json:"phaseName"`
    
    // 执行开始时间
    StartedAt *metav1.Time `json:"startedAt"`
    
    // 执行完成时间
    CompletedAt *metav1.Time `json:"completedAt,omitempty"`
    
    // 执行结果
    Result PhaseResult `json:"result"`
    
    // 失败原因
    FailureReason string `json:"failureReason,omitempty"`
    
    // 重试次数
    RetryCount int `json:"retryCount"`
}

type PhaseResult string

const (
    PhaseResultPending    PhaseResult = "Pending"
    PhaseResultRunning    PhaseResult = "Running"
    PhaseResultCompleted  PhaseResult = "Completed"
    PhaseResultFailed     PhaseResult = "Failed"
    PhaseResultRolledBack PhaseResult = "RolledBack"
)

// BKECluster Status 扩展
type BKEClusterStatus struct {
    // ... 现有字段 ...
    
    // Phase 执行历史（最多保留 20 条）
    PhaseHistory []PhaseExecutionRecord `json:"phaseHistory,omitempty"`
    
    // 回滚历史
    RollbackHistory []RollbackRecord `json:"rollbackHistory,omitempty"`
}
```

### 2.3 ClusterVersion 回滚能力

#### 2.3.1 ClusterVersion 回滚机制

ClusterVersion 通过版本历史管理升级记录，回滚基于以下机制：

```go
// ClusterVersion 回滚核心设计
type ClusterVersionRollback struct {
    client client.Client
}

// 触发 ClusterVersion 回滚
func (r *ClusterVersionRollback) TriggerRollback(
    ctx context.Context,
    cv *cvv1alpha1.ClusterVersion,
    targetVersion string,
) error {
    // 1. 验证目标版本在升级历史中
    if !r.isVersionInHistory(cv, targetVersion) {
        return fmt.Errorf("target version %s not in upgrade history", targetVersion)
    }
    
    // 2. 设置回滚目标
    orig := cv.DeepCopy()
    cv.Spec.DesiredVersion = targetVersion
    cv.Status.Phase = cvv1alpha1.ClusterVersionPhaseRollingBack
    
    // 3. 记录回滚历史
    now := metav1.Now()
    cv.Status.UpgradeHistory = append(cv.Status.UpgradeHistory, cvv1alpha1.ClusterUpgradeRecord{
        From:        cv.Status.CurrentVersion,
        To:          targetVersion,
        StartedAt:   &now,
        Status:      cvv1alpha1.ClusterUpgradeRecordStatusRollingBack,
    })
    
    // 4. 更新 ClusterVersion
    return r.client.Status().Patch(ctx, cv, client.MergeFrom(orig))
}
```

#### 2.3.2 ClusterVersion 回滚场景

| 场景 | 当前版本 | 回滚目标 | 回滚机制 |
|------|---------|---------|---------|
| **升级失败** | v26.06 | v26.05 | 版本回滚 + 组件降级 |
| **升级后发现问题** | v26.06 | v26.05 | 版本回滚 + 数据迁移 |
| **跨版本回滚** | v26.07 | v26.05 | 逐跳回滚（v26.07→v26.06→v26.05） |

#### 2.3.3 ClusterVersion 回滚流程

```
ClusterVersion 回滚流程：

1. 检测到升级失败
   └─ ClusterVersion.status.phase: Upgrading → Failed
   └─ ClusterVersion.status.currentVersion: v26.05
   └─ ClusterVersion.spec.desiredVersion: v26.06

2. 触发回滚
   └─ 用户执行：bkectl rollback cluster my-cluster --to v26.05
   └─ 或自动触发：升级超时/健康检查失败

3. 版本回滚
   └─ ClusterVersion.spec.desiredVersion: v26.06 → v26.05
   └─ ClusterVersion.status.phase: Failed → RollingBack
   └─ 记录回滚历史：UpgradeHistory = [..., {From: v26.06, To: v26.05, Status: RollingBack}]

4. 执行回滚
   └─ 降级控制面组件到 v26.05
   └─ 降级 Operator 到 v26.05
   └─ 降级节点配置到 v26.05
   └─ 数据迁移（如需要）

5. 完成回滚
   └─ ClusterVersion.status.currentVersion: v26.05
   └─ ClusterVersion.status.phase: RollingBack → Ready
   └─ 更新回滚历史：Status: Succeeded
```

#### 2.3.4 ClusterVersion 回滚数据结构

```go
// ClusterVersion 升级记录
type ClusterUpgradeRecord struct {
    // 升级前版本
    From string `json:"from"`
    
    // 升级后版本
    To string `json:"to"`
    
    // 开始时间
    StartedAt *metav1.Time `json:"startedAt,omitempty"`
    
    // 完成时间
    CompletedAt *metav1.Time `json:"completedAt,omitempty"`
    
    // 升级状态
    Status ClusterUpgradeRecordStatus `json:"status"`
    
    // 失败原因
    FailureReason string `json:"failureReason,omitempty"`
}

type ClusterUpgradeRecordStatus string

const (
    ClusterUpgradeRecordStatusSucceeded   ClusterUpgradeRecordStatus = "Succeeded"
    ClusterUpgradeRecordStatusFailed      ClusterUpgradeRecordStatus = "Failed"
    ClusterUpgradeRecordStatusRollingBack ClusterUpgradeRecordStatus = "RollingBack"
    ClusterUpgradeRecordStatusRolledBack  ClusterUpgradeRecordStatus = "RolledBack"
)

// ClusterVersion Status
type ClusterVersionStatus struct {
    // 当前版本
    CurrentVersion string `json:"currentVersion"`
    
    // 集群阶段
    Phase ClusterVersionPhase `json:"phase"`
    
    // 升级历史
    UpgradeHistory []ClusterUpgradeRecord `json:"upgradeHistory,omitempty"`
    
    // 条件
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}
```

#### 2.3.5 ClusterVersion 回滚验证

```go
// 验证回滚目标版本
func (r *ClusterVersionRollback) ValidateRollbackTarget(
    cv *cvv1alpha1.ClusterVersion,
    targetVersion string,
) error {
    // 1. 检查目标版本是否在升级历史中
    if !r.isVersionInHistory(cv, targetVersion) {
        return fmt.Errorf("target version %s not in upgrade history", targetVersion)
    }
    
    // 2. 检查目标版本镜像是否可用
    if !r.isImageAvailable(targetVersion) {
        return fmt.Errorf("target version image not available")
    }
    
    // 3. 检查回滚路径是否支持
    if !r.isRollbackPathSupported(cv.Status.CurrentVersion, targetVersion) {
        return fmt.Errorf("rollback path not supported")
    }
    
    return nil
}

// 检查版本是否在历史中
func (r *ClusterVersionRollback) isVersionInHistory(
    cv *cvv1alpha1.ClusterVersion,
    version string,
) bool {
    for _, record := range cv.Status.UpgradeHistory {
        if record.To == version && record.Status == ClusterUpgradeRecordStatusSucceeded {
            return true
        }
    }
    return false
}
```

### 2.4 双机制协同回滚

#### 2.4.1 协同回滚场景

某些场景需要 PhaseFlow 和 ClusterVersion 协同回滚：

| 场景 | PhaseFlow 回滚 | ClusterVersion 回滚 | 协同策略 |
|------|---------------|---------------------|---------|
| **升级后扩缩容失败** | ScaleFailed → Ready | 保持当前版本 | 仅 PhaseFlow 回滚 |
| **升级失败** | 保持当前状态 | v26.06 → v26.05 | 仅 ClusterVersion 回滚 |
| **升级后配置变更失败** | ManageFailed → Ready | 保持当前版本 | 仅 PhaseFlow 回滚 |
| **升级后删除失败** | DeleteFailed → Ready | 保持当前版本 | 仅 PhaseFlow 回滚 |

#### 2.4.2 协同回滚流程

```
协同回滚流程（升级后扩缩容失败）：

1. 升级到 v26.06 成功
   └─ ClusterVersion.status.currentVersion: v26.06
   └─ ClusterVersion.status.phase: Ready
   └─ ClusterStatus: Ready

2. 扩容 Worker 节点失败
   └─ ClusterStatus: Ready → WorkerScalingUp → ScaleFailed
   └─ LastInProgressState: WorkerScalingUp

3. 触发 PhaseFlow 回滚
   └─ ClusterStatus: ScaleFailed → Ready
   └─ 清理失败的 Worker 节点
   └─ ClusterVersion 保持不变（v26.06）

4. 验证回滚成功
   └─ ClusterStatus: Ready
   └─ ClusterVersion.status.currentVersion: v26.06
   └─ 集群正常运行
```

#### 2.4.3 协同回滚决策树

```
回滚决策树：

┌─ 检测到失败
│
├─ 失败类型？
│  │
│  ├─ 升级失败（ClusterVersion.phase = Failed）
│  │  └─ 触发 ClusterVersion 回滚
│  │     └─ 回滚到上一版本
│  │
│  ├─ PhaseFlow 失败（ClusterStatus = *Failed）
│  │  │
│  │  ├─ 安装失败（InitializationFailed）
│  │  │  └─ 清理并重建（不支持自动回滚）
│  │  │
│  │  ├─ 扩缩容失败（ScaleFailed）
│  │  │  └─ 触发 PhaseFlow 回滚
│  │  │     └─ 回滚到 Ready 状态
│  │  │
│  │  ├─ 配置变更失败（ManageFailed）
│  │  │  └─ 触发 PhaseFlow 回滚
│  │  │     └─ 恢复上一配置
│  │  │
│  │  └─ 删除失败（DeleteFailed）
│  │     └─ 触发 PhaseFlow 回滚
│  │        └─ 重试删除操作
│  │
│  └─ 未知失败
│     └─ 人工介入
│
└─ 验证回滚成功
```

### 2.5 安装回滚能力

#### 2.5.1 设计原则

**关键洞察**：BKE 安装过程**不支持自动回滚**，与 OpenShift 一致。

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

### 5.2 阶段二：升级回滚（P0）

| 任务 | 工作量 | 优先级 | 说明 |
|------|--------|--------|------|
| 升级历史数据结构 | 5 天 | P0 | 实现 UpgradeHistory 结构 |
| 自动回滚触发 | 8 天 | P0 | 实现自动回滚检测和触发 |
| 手动回滚命令 | 5 天 | P0 | 实现手动回滚 CLI 命令 |
| 回滚状态转换 | 5 天 | P0 | 实现回滚相关的状态转换规则 |

### 5.3 阶段三：扩缩容回滚（P1）

| 任务 | 工作量 | 优先级 | 说明 |
|------|--------|--------|------|
| 扩缩容回滚机制 | 5 天 | P1 | 实现扩缩容失败回滚 |
| 配置版本管理 | 5 天 | P1 | 实现配置版本管理和回滚 |

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
| **升级回滚** | ✅ 支持 | 自动/手动回滚到上一版本 |
| **扩缩容回滚** | ✅ 支持 | 声明式回滚 |
| **配置回滚** | ✅ 支持 | 配置版本管理和回滚 |
| **etcd 备份** | ✅ 支持 | 自动/手动备份 |
| **配置备份** | ✅ 支持 | 集群配置备份 |
| **应用备份** | ✅ 支持 | Velero 集成 |
| **灾难恢复** | ✅ 支持 | 完整的恢复流程 |

### 7.2 与 OpenShift 的对比

| 维度 | OpenShift | BKE | 差异说明 |
|------|-----------|-----|---------|
| **回滚机制** | CVO 调谐循环 | 状态机引擎 | BKE 使用 Engine 驱动 |
| **失败记录** | RolledBack 状态 | LastInProgressState | BKE 记录失败前状态 |
| **备份工具** | cluster-backup.sh | 自定义脚本 | 功能一致 |
| **恢复工具** | cluster-restore.sh | 自定义脚本 | 功能一致 |

### 7.3 关键设计决策

1. **安装不支持回滚**：与 OpenShift 一致，状态不可逆
2. **升级支持自动回滚**：检测到失败时自动触发
3. **扩缩容支持声明式回滚**：通过修改期望状态触发
4. **配置支持版本管理**：保留最近 10 个配置版本
5. **备份支持多种存储**：本地、S3、NFS、OSS

### 7.4 后续工作

1. **实现回滚状态转换规则**：在状态机引擎中添加回滚相关的转换规则
2. **实现升级历史数据结构**：记录升级和回滚的完整历史
3. **实现自动备份脚本**：实现 etcd 和配置的自动备份
4. **实现恢复验证工具**：自动验证备份可恢复性
5. **文档化回滚流程**：编写详细的回滚操作手册

---

**文档版本**：v1.0  
**创建日期**：2024-01-15  
**最后更新**：2024-01-15  
**维护者**：BKE 团队
