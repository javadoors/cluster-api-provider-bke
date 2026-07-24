# OpenShift 集群安装与扩容回滚能力洞察报告

## 一、OpenShift 集群生命周期管理架构

### 1.1 核心组件

| 组件 | 职责 | 关键 CRD |
|------|------|---------|
| **Cluster Version Operator (CVO)** | 集群版本管理，驱动升级/回滚 | `ClusterVersion` |
| **Machine Config Operator (MCO)** | 节点配置管理，驱动节点级变更 | `MachineConfig`, `MachineConfigPool` |
| **Cluster API Provider** | 基础设施管理，驱动节点扩缩容 | `Machine`, `MachineSet`, `MachineDeployment` |

### 1.2 状态管理模型

```
ClusterVersion (集群级)
  ├─ desired.version: 目标版本
  ├─ status.desired: 当前目标
  ├─ status.history: 升级历史
  └─ status.conditions: 状态条件

MachineConfigPool (节点池级)
  ├─ spec.configuration: 目标配置
  ├─ status.configuration: 当前配置
  ├─ status.machineCount: 节点数量
  └─ status.unavailableMachineCount: 不可用节点数
```

## 二、集群安装机制

### 2.1 安装流程

```
1. 安装程序 (openshift-install)
   ├─ 生成 Ignition 配置
   ├─ 创建 Bootstrap 节点
   └─ 创建控制平面节点

2. Bootstrap 阶段
   ├─ 启动临时控制平面
   ├─ 创建 etcd 集群
   └─ 启动 CVO

3. CVO 接管
   ├─ 应用 ClusterVersion
   ├─ 部署核心 Operator
   └─ 部署工作负载

4. Bootstrap 完成
   └─ 销毁 Bootstrap 节点
```

### 2.2 安装回滚能力

**关键洞察**：OpenShift 安装过程**不支持自动回滚**，原因：

| 因素 | 说明 |
|------|------|
| **状态不可逆** | etcd 数据、证书、网络配置一旦创建无法简单回滚 |
| **基础设施耦合** | 云资源（VM、网络、存储）已创建 |
| **时间窗口** | 安装失败通常在早期阶段，重建比回滚更快 |

**推荐做法**：安装失败时销毁集群重新安装，而非回滚。

## 三、扩容机制

### 3.1 扩容流程

```
1. 修改 MachineDeployment/MachineSet
   └─ replicas: 3 → 5

2. Machine Controller 创建 Machine
   ├─ 调用 Cloud Provider API
   └─ 创建 VM/实例

3. Node Controller 批准 CSR
   └─ 节点加入集群

4. MCO 应用配置
   ├─ 应用 MachineConfig
   └─ 节点配置完成
```

### 3.2 扩容回滚能力

**支持回滚**，机制如下：

```yaml
# 回滚 MachineDeployment
apiVersion: machine.openshift.io/v1beta1
kind: MachineDeployment
metadata:
  name: worker-us-east-1a
spec:
  replicas: 3  # 从 5 回滚到 3
```

**回滚流程**：
1. 减少 `replicas` 数量
2. Machine Controller 删除多余的 Machine
3. Cloud Provider 销毁 VM/实例
4. Node 从集群中移除

**关键设计**：
- **声明式回滚**：通过修改期望状态触发回滚
- **优雅删除**：先 cordon → drain → delete
- **数据保留**：PVC 数据可选择保留或删除

## 四、升级与回滚机制

### 4.1 升级流程

```
1. 设置目标版本
   └─ oc adm upgrade --to=4.12.0

2. CVO 验证升级路径
   ├─ 检查当前版本
   ├─ 检查目标版本
   └─ 验证升级图

3. CVO 执行升级
   ├─ 更新 ClusterVersion.status
   ├─ 按顺序更新 Operator
   └─ 等待 Operator 就绪

4. MCO 更新节点
   ├─ 生成新的 MachineConfig
   ├─ 逐节点更新配置
   └─ 重启节点应用配置

5. 升级完成
   └─ 更新 ClusterVersion.status.history
```

### 4.2 回滚机制

**OpenShift 4.x 支持两种回滚方式**：

#### 4.2.1 自动回滚（Operator 级别）

```yaml
# ClusterVersion 配置
apiVersion: config.openshift.io/v1
kind: ClusterVersion
metadata:
  name: version
spec:
  clusterID: xxx
  channel: stable-4.12
  desiredUpdate:
    version: 4.12.0
    image: quay.io/openshift-release-dev/ocp-release:4.12.0
    force: false
  autoRollback: true  # 启用自动回滚
  rollbackTimeout: 30m  # 回滚超时时间
```

**自动回滚触发条件**：
- Operator 更新后健康检查失败
- 节点配置应用后节点 NotReady
- 升级超时（默认 30 分钟）

**自动回滚流程**：
```
1. 检测到升级失败
2. 回滚 ClusterVersion 到上一版本
3. CVO 回滚 Operator 到上一版本
4. MCO 回滚节点配置
5. 等待所有组件就绪
6. 更新 ClusterVersion.status.history
```

#### 4.2.2 手动回滚

```bash
# 查看升级历史
oc get clusterversion version -o jsonpath='{.status.history}'

# 手动回滚到指定版本
oc adm upgrade --to=4.11.0 --allow-not-recommended

# 或者修改 ClusterVersion
oc edit clusterversion version
# 修改 spec.channel 和 spec.desiredUpdate
```

**手动回滚限制**：
- 只能回滚到**相邻的上一版本**
- 不能跨多个版本回滚（如 4.12 → 4.10）
- 需要 `--allow-not-recommended` 标志

### 4.3 回滚版本获取机制

**核心问题**：如何确定可以回滚到哪个版本？

#### 4.3.1 升级历史数据结构

OpenShift ClusterVersion 的 `status.history` 字段存储了完整的升级历史：

```go
type UpdateHistory struct {
    // state 记录升级状态
    // - Completed: 升级成功完成
    // - Partial: 升级进行中或部分完成
    // - Accepted: 升级已被接受但尚未开始
    // - RolledBack: 升级失败并已回滚（OpenShift 4.14+）
    State UpdateState `json:"state"`
    
    // version 是目标版本
    Version string `json:"version"`
    
    // image 是发布镜像
    Image string `json:"image"`
    
    // startedTime 是升级开始时间
    StartedTime metav1.Time `json:"startedTime"`
    
    // completionTime 是升级完成时间
    // - state=Completed 时：升级成功完成时间
    // - state=RolledBack 时：回滚决策时间（非回滚完成时间）
    // - state=Partial 时：不存在
    CompletionTime *metav1.Time `json:"completionTime,omitempty"`
    
    // verified 表示发布镜像是否已验证
    Verified bool `json:"verified"`
    
    // acceptedRisks 记录升级过程中接受的风险
    AcceptedRisks string `json:"acceptedRisks,omitempty"`
}

type UpdateState string

const (
    // CompletedUpdateState 表示升级已成功完成
    CompletedUpdateState UpdateState = "Completed"
    
    // PartialUpdateState 表示升级正在进行或部分完成
    // 包括：升级中、升级失败、回滚中
    PartialUpdateState UpdateState = "Partial"
    
    // AcceptedUpdateState 表示升级已被接受但尚未开始
    AcceptedUpdateState UpdateState = "Accepted"
    
    // RolledBackUpdateState 表示升级失败并已回滚（OpenShift 4.14+）
    // 这是一个终态，表示该升级尝试已被放弃
    RolledBackUpdateState UpdateState = "RolledBack"
)
```

**回滚记录的字段设计**：

**1. state 字段**

回滚记录有两种形态：

| 形态 | state 值 | 含义 | 是否终态 |
|------|---------|------|---------|
| **失败的升级记录** | `RolledBack` | 该升级尝试失败并已回滚 | 是（不可转换） |
| **回滚执行记录** | `Partial` → `Completed` | 正在执行回滚 → 回滚完成 | 否 → 是 |

**2. completionTime 字段**

`completionTime` 在不同状态下的含义：

| state | completionTime | 含义 |
|-------|----------------|------|
| `Completed` | 升级成功完成时间 | 升级流程结束时间 |
| `RolledBack` | 回滚决策时间 | CVO 决定回滚的时间点 |
| `Partial` | 不存在 | 操作尚未完成 |

**3. version 字段**

回滚记录中的 `version` 字段含义：

| 记录类型 | version 含义 | 示例 |
|---------|-------------|------|
| 失败的升级记录 | 失败的升级目标版本 | `4.12.0`（升级失败） |
| 回滚执行记录 | 回滚目标版本 | `4.11.18`（回滚到此版本） |

**4. 回滚记录的完整字段说明**

```yaml
# 失败的升级记录（标记为 RolledBack）
- state: RolledBack              # 状态：已回滚
  version: "4.12.0"              # 失败的升级目标版本
  image: "quay.io/.../4.12.0"    # 失败的发布镜像
  startedTime: "2024-01-15T10:00:00Z"      # 升级开始时间
  completionTime: "2024-01-15T11:00:00Z"   # 回滚决策时间
  verified: true                 # 镜像已验证
  acceptedRisks: ""              # 无接受的风险

# 回滚执行记录
- state: Completed               # 状态：回滚完成
  version: "4.11.18"             # 回滚目标版本
  image: "quay.io/.../4.11.18"   # 回滚使用的镜像
  startedTime: "2024-01-15T11:00:00Z"      # 回滚开始时间
  completionTime: "2024-01-15T12:00:00Z"   # 回滚完成时间
  verified: true                 # 镜像已验证
  acceptedRisks: ""              # 无接受的风险
```

**5. 回滚记录与升级记录的对比**

| 字段 | 升级记录 | 回滚记录（失败） | 回滚记录（执行） |
|------|---------|-----------------|-----------------|
| `state` | `Completed` | `RolledBack` | `Partial` → `Completed` |
| `version` | 升级目标版本 | 失败的升级目标版本 | 回滚目标版本 |
| `image` | 升级镜像 | 失败的升级镜像 | 回滚镜像 |
| `startedTime` | 升级开始时间 | 升级开始时间 | 回滚开始时间 |
| `completionTime` | 升级完成时间 | 回滚决策时间 | 回滚完成时间 |
| `verified` | 是否验证 | 是否验证 | 是否验证 |

**6. 回滚记录的创建时机**

```
T0: 升级到 4.12.0 开始
    创建升级记录：history[0] = {state: Partial, version: 4.12.0, startedTime: T0}

T1: 升级失败
    升级记录保持：history[0] = {state: Partial, version: 4.12.0}

T2: CVO 触发自动回滚
    修改升级记录：history[0] = {state: RolledBack, version: 4.12.0, completionTime: T2}
    创建回滚记录：history[1] = {state: Partial, version: 4.11.18, startedTime: T2}

T3: 回滚执行中
    回滚记录保持：history[1] = {state: Partial, version: 4.11.18}

T4: 回滚完成
    更新回滚记录：history[1] = {state: Completed, version: 4.11.18, completionTime: T4}
```

**7. 回滚记录的关键设计点**

1. **分离失败记录和回滚记录**：失败的升级记录标记为 `RolledBack`，回滚执行创建新记录
2. **completionTime 的双重含义**：对于 `RolledBack` 记录，`completionTime` 是回滚决策时间；对于 `Completed` 记录，是完成时间
3. **保留完整历史**：即使升级失败，也保留完整的升级历史记录，便于审计和问题排查
4. **版本一致性**：回滚记录的 `version` 字段指向回滚目标版本，而非失败的升级版本

#### 4.3.2 升级历史示例

**成功升级的历史记录：**

```yaml
status:
  history:
  - state: Completed
    version: 4.12.0
    image: quay.io/openshift-release-dev/ocp-release:4.12.0-x86_64
    startedTime: "2024-01-15T10:00:00Z"
    completionTime: "2024-01-15T11:30:00Z"
    verified: true
  - state: Completed
    version: 4.11.18
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
    startedTime: "2023-12-01T08:00:00Z"
    completionTime: "2023-12-01T09:30:00Z"
    verified: true
```

**升级失败时的历史记录：**

```yaml
status:
  history:
  - state: Partial          # 升级失败，state 保持为 Partial
    version: 4.12.0
    image: quay.io/openshift-release-dev/ocp-release:4.12.0-x86_64
    startedTime: "2024-01-15T10:00:00Z"
    # completionTime 不存在，因为升级未完成
    verified: true
  - state: Completed
    version: 4.11.18
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
    startedTime: "2023-12-01T08:00:00Z"
    completionTime: "2023-12-01T09:30:00Z"
    verified: true
  conditions:
  - type: Failing
    status: "True"
    reason: UpgradeFailed
    message: "Unable to apply 4.12.0: Operator health check failed"
    lastTransitionTime: "2024-01-15T10:45:00Z"
```

**Partial 状态的详细含义：**

**字面含义**：Partial = 部分的、不完整的

**在 OpenShift 中的含义**：
- 升级已开始但尚未完成
- 没有 `completionTime`（完成时间）
- 可能是正常状态（升级中）或异常状态（升级失败）

**Partial 的两种场景**：

| 场景 | state | conditions | 含义 |
|------|-------|------------|------|
| **升级进行中** | `Partial` | `Progressing=True` | 正常状态，升级正在执行 |
| **升级失败** | `Partial` | `Failing=True` | 异常状态，升级失败 |

**为什么使用 Partial 而不是 Failed？**

1. **升级是渐进过程**：OpenShift 升级涉及多个组件，可能部分成功、部分失败
2. **保留失败记录**：即使失败，也需要保留记录用于回滚和审计
3. **允许手动干预**：用户可以选择重试升级或触发回滚
4. **状态一致性**：Partial 表示"未完成"，不预设结果（成功/失败）

**状态转换图**：

```
Accepted → Partial (升级中) → Completed (成功)
                         ↓
                    Partial (失败) → RolledBack (回滚)
```

**关键区别：**

| 状态 | history[0].state | completionTime | conditions |
|------|------------------|----------------|------------|
| 升级成功 | `Completed` | 有值 | `Available=True` |
| 升级失败 | `Partial` | 无值 | `Failing=True` |
| 升级中 | `Partial` | 无值 | `Progressing=True` |

**CVO 如何检测升级失败：**

```go
func (cvo *ClusterVersionOperator) isUpgradeFailed(cv *configv1.ClusterVersion) bool {
    if len(cv.Status.History) == 0 {
        return false
    }
    
    latest := cv.Status.History[0]
    
    // 条件 1: state 为 Partial（未完成）
    if latest.State != configv1.PartialUpdateState {
        return false
    }
    
    // 条件 2: 检查 Failing condition
    for _, cond := range cv.Status.Conditions {
        if cond.Type == "Failing" && cond.Status == "True" {
            return true
        }
    }
    
    // 条件 3: 检查是否超时
    if time.Since(latest.StartedTime.Time) > cvo.upgradeTimeout {
        return true
    }
    
    return false
}
```

#### 4.3.3 版本选择算法

**CVO 通过以下算法确定可回滚版本**：

```go
// GetRollbackTarget 获取可回滚的目标版本
func (cvo *ClusterVersionOperator) GetRollbackTarget(cv *configv1.ClusterVersion) (string, error) {
    // 1. 获取升级历史
    history := cv.Status.History
    
    // 2. 查找最新的 Completed 状态记录（当前版本）
    var currentVersion string
    for _, h := range history {
        if h.State == configv1.CompletedUpdateState {
            currentVersion = h.Version
            break
        }
    }
    
    if currentVersion == "" {
        return "", fmt.Errorf("no completed upgrade found")
    }
    
    // 3. 查找上一条 Completed 状态记录（可回滚版本）
    var rollbackVersion string
    foundCurrent := false
    for _, h := range history {
        if h.State == configv1.CompletedUpdateState {
            if foundCurrent {
                // 这是上一条 Completed 记录
                rollbackVersion = h.Version
                break
            }
            if h.Version == currentVersion {
                foundCurrent = true
            }
        }
    }
    
    if rollbackVersion == "" {
        return "", fmt.Errorf("no rollback target found")
    }
    
    // 4. 验证回滚版本是否在升级图中
    if !cvo.isVersionInUpgradeGraph(rollbackVersion) {
        return "", fmt.Errorf("rollback version %s not in upgrade graph", rollbackVersion)
    }
    
    return rollbackVersion, nil
}
```

#### 4.3.4 版本验证机制

**CVO 在回滚前会进行以下验证**：

1. **升级图验证**：检查目标版本是否在官方升级图中
   ```bash
   # 查看可用升级路径
   oc adm upgrade --allow-explicit-upgrade --to-image=<image>
   ```

2. **发布镜像验证**：验证目标版本的发布镜像签名
   ```go
   if !verified {
       return fmt.Errorf("release image not verified")
   }
   ```

3. **兼容性验证**：检查目标版本与当前组件的兼容性
   ```go
   if !cvo.isCompatible(currentComponents, targetVersion) {
       return fmt.Errorf("version not compatible with current components")
   }
   ```

### 4.4 回滚时 ClusterVersion 的目标版本

**核心问题**：回滚时 ClusterVersion 的 `spec.desiredUpdate.version` 是什么？

#### 4.4.1 目标版本确定规则

**回滚目标版本 = 上一个成功升级的版本**

```
升级前状态：
  spec.desiredUpdate.version: 4.11.18  (当前运行版本)
  status.history[0].version: 4.11.18
  status.history[0].state: Completed

升级到 4.12.0：
  spec.desiredUpdate.version: 4.12.0   (目标版本)
  status.history[0].version: 4.12.0
  status.history[0].state: Partial     (升级中)
  status.history[1].version: 4.11.18
  status.history[1].state: Completed

升级失败触发回滚：
  spec.desiredUpdate.version: 4.11.18  (回滚目标 = 上一个成功版本)
  status.history[0].version: 4.12.0
  status.history[0].state: RolledBack  (标记为已回滚)
  status.history[1].version: 4.11.18
  status.history[1].state: Completed   (回滚到此版本)
```

#### 4.4.2 目标版本设置时机

**自动回滚时**：

```go
// CVO 检测到升级失败后自动设置回滚目标
func (cvo *ClusterVersionOperator) handleUpgradeFailure(cv *configv1.ClusterVersion) error {
    // 1. 获取回滚目标版本
    rollbackVersion, err := cvo.GetRollbackTarget(cv)
    if err != nil {
        return err
    }
    
    // 2. 设置回滚目标
    cv.Spec.DesiredUpdate = &configv1.Update{
        Version: rollbackVersion,
        Image:   cvo.getReleaseImage(rollbackVersion),
        Force:   false,
    }
    
    // 3. 更新 ClusterVersion 对象
    return cvo.client.Update(context.TODO(), cv)
}
```

**手动回滚时**：

```bash
# 用户手动设置回滚目标
oc adm upgrade --to=4.11.18 --allow-not-recommended

# 这会修改 ClusterVersion.spec.desiredUpdate
kubectl get clusterversion version -o yaml
# spec:
#   desiredUpdate:
#     version: 4.11.18
#     image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
```

#### 4.4.3 ClusterVersion 状态变化

**升级前**：
```yaml
apiVersion: config.openshift.io/v1
kind: ClusterVersion
metadata:
  name: version
spec:
  clusterID: xxx
  channel: stable-4.11
  desiredUpdate:
    version: 4.11.18
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
status:
  desired:
    version: 4.11.18
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
  history:
  - state: Completed
    version: 4.11.18
    startedTime: "2023-12-01T08:00:00Z"
    completionTime: "2023-12-01T09:30:00Z"
  - state: Completed
    version: 4.11.0
    startedTime: "2023-10-15T10:00:00Z"
    completionTime: "2023-10-15T11:30:00Z"
```

**升级到 4.12.0（失败）**：
```yaml
spec:
  desiredUpdate:
    version: 4.12.0
    image: quay.io/openshift-release-dev/ocp-release:4.12.0-x86_64
status:
  desired:
    version: 4.12.0
    image: quay.io/openshift-release-dev/ocp-release:4.12.0-x86_64
  history:
  - state: Partial          # 升级失败
    version: 4.12.0
    startedTime: "2024-01-15T10:00:00Z"
  - state: Completed
    version: 4.11.18
    startedTime: "2023-12-01T08:00:00Z"
    completionTime: "2023-12-01T09:30:00Z"
  conditions:
  - type: Failing
    status: "True"
    reason: UpgradeFailed
    message: "Upgrade to 4.12.0 failed: Operator health check failed"
```

**触发自动回滚**：
```yaml
spec:
  desiredUpdate:
    version: 4.11.18        # 回滚目标 = 上一个成功版本
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
status:
  desired:
    version: 4.11.18        # 目标版本已更新
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
  history:
  - state: RolledBack       # 标记为已回滚
    version: 4.12.0
    startedTime: "2024-01-15T10:00:00Z"
    completionTime: "2024-01-15T11:00:00Z"
  - state: Partial          # 正在回滚
    version: 4.11.18
    startedTime: "2024-01-15T11:00:00Z"
  - state: Completed
    version: 4.11.0
  conditions:
  - type: Failing
    status: "False"         # 失败状态已清除
  - type: RollbackInProgress
    status: "True"
    reason: AutomaticRollback
    message: "Rolling back to 4.11.18 due to upgrade failure"
```

**回滚触发详细流程**：

**步骤 1: 检测升级失败**

CVO 在调谐循环中持续检查升级状态，通过以下条件判断升级是否失败：

```go
func (cvo *ClusterVersionOperator) detectUpgradeFailure(cv *configv1.ClusterVersion) bool {
    // 条件 1: 检查最新的升级记录是否为 Partial 状态
    if len(cv.Status.History) == 0 {
        return false
    }
    
    latest := cv.Status.History[0]
    if latest.State != configv1.PartialUpdateState {
        return false
    }
    
    // 条件 2: 检查 Failing condition 是否为 True
    for _, cond := range cv.Status.Conditions {
        if cond.Type == "Failing" && cond.Status == metav1.ConditionTrue {
            return true
        }
    }
    
    // 条件 3: 检查是否超时（默认 30 分钟）
    if time.Since(latest.StartedTime.Time) > cvo.upgradeTimeout {
        return true
    }
    
    return false
}
```

**步骤 2: 获取回滚目标版本**

```go
func (cvo *ClusterVersionOperator) getRollbackTarget(cv *configv1.ClusterVersion) (string, error) {
    // 遍历 history，找到最近的 Completed 状态记录
    // 跳过第一个 Partial 状态记录（失败的升级）
    for i := 1; i < len(cv.Status.History); i++ {
        if cv.Status.History[i].State == configv1.CompletedUpdateState {
            return cv.Status.History[i].Version, nil
        }
    }
    return "", fmt.Errorf("no rollback target found")
}
```

**步骤 3: 标记失败记录为 RolledBack**

```go
func (cvo *ClusterVersionOperator) markAsRolledBack(cv *configv1.ClusterVersion) {
    // 修改第一个记录的 state 为 RolledBack
    cv.Status.History[0].State = configv1.RolledBackUpdateState
    
    // 设置 completionTime（标记回滚决策时间）
    now := metav1.Now()
    cv.Status.History[0].CompletionTime = &now
    
    // 清除 Failing condition
    for i, cond := range cv.Status.Conditions {
        if cond.Type == "Failing" {
            cv.Status.Conditions[i].Status = metav1.ConditionFalse
            cv.Status.Conditions[i].Message = "Upgrade failed, rollback initiated"
            break
        }
    }
    
    // 添加 RollbackInProgress condition
    cv.Status.Conditions = append(cv.Status.Conditions, configv1.ClusterOperatorStatusCondition{
        Type:               "RollbackInProgress",
        Status:             metav1.ConditionTrue,
        Reason:             "AutomaticRollback",
        Message:            fmt.Sprintf("Rolling back to %s due to upgrade failure", rollbackTarget),
        LastTransitionTime: metav1.Now(),
    })
}
```

**步骤 4: 创建新的回滚记录**

```go
func (cvo *ClusterVersionOperator) createRollbackRecord(cv *configv1.ClusterVersion, targetVersion string) {
    // 创建新的回滚记录
    rollbackRecord := configv1.UpdateHistory{
        State:       configv1.PartialUpdateState,  // 初始为 Partial
        Version:     targetVersion,
        Image:       cvo.getReleaseImage(targetVersion),
        StartedTime: metav1.Now(),
        // CompletionTime 不存在，因为回滚尚未完成
    }
    
    // 插入到 history 数组的开头
    // 原来的 RolledBack 记录变成 history[0]
    // 新的回滚记录变成 history[1]
    newHistory := make([]configv1.UpdateHistory, len(cv.Status.History)+1)
    newHistory[0] = cv.Status.History[0]  // RolledBack 记录
    newHistory[1] = rollbackRecord        // 新的回滚记录
    copy(newHistory[2:], cv.Status.History[1:])  // 其他历史记录
    
    cv.Status.History = newHistory
    
    // 更新 desired 状态
    cv.Status.Desired.Version = targetVersion
    cv.Status.Desired.Image = rollbackRecord.Image
}
```

**步骤 5: 更新 ClusterVersion 对象**

```go
func (cvo *ClusterVersionOperator) executeRollback(cv *configv1.ClusterVersion) error {
    // 1. 获取回滚目标
    targetVersion, err := cvo.getRollbackTarget(cv)
    if err != nil {
        return err
    }
    
    // 2. 标记失败记录为 RolledBack
    cvo.markAsRolledBack(cv)
    
    // 3. 创建新的回滚记录
    cvo.createRollbackRecord(cv, targetVersion)
    
    // 4. 更新 spec.desiredUpdate（触发回滚执行）
    cv.Spec.DesiredUpdate = &configv1.Update{
        Version: targetVersion,
        Image:   cvo.getReleaseImage(targetVersion),
    }
    
    // 5. 更新 ClusterVersion 对象到 API Server
    if err := cvo.client.Status().Update(context.TODO(), cv); err != nil {
        return err
    }
    
    // 6. 发送事件
    cvo.recorder.Eventf(cv, corev1.EventTypeWarning, "UpgradeFailed",
        "Upgrade to %s failed, initiating automatic rollback to %s",
        cv.Status.History[0].Version, targetVersion)
    
    return nil
}
```

**状态转换时序图**：

```
时间线                          ClusterVersion 状态变化
────────────────────────────────────────────────────────────────
T0: 升级到 4.12.0 开始          history[0] = {state: Partial, version: 4.12.0}
                                
T1: 升级失败                    history[0] = {state: Partial, version: 4.12.0}
                                conditions = [{type: Failing, status: True}]
                                
T2: CVO 检测到失败              调用 executeRollback()
                                
T3: 标记失败记录                history[0] = {state: RolledBack, version: 4.12.0, completionTime: T3}
                                history[1] = {state: Partial, version: 4.11.18}  ← 新创建
                                conditions = [{type: RollbackInProgress, status: True}]
                                spec.desiredUpdate.version = 4.11.18
                                
T4: 回滚执行中                  history[0] = {state: RolledBack, version: 4.12.0}
                                history[1] = {state: Partial, version: 4.11.18}
                                
T5: 回滚完成                    history[0] = {state: RolledBack, version: 4.12.0}
                                history[1] = {state: Completed, version: 4.11.18, completionTime: T5}
                                conditions = [{type: Available, status: True}]
```

**关键点总结**：

| 步骤 | 操作 | history 变化 |
|------|------|-------------|
| 1. 检测失败 | 检查 `Partial` + `Failing=True` | 无变化 |
| 2. 获取目标 | 遍历 history 找 `Completed` 记录 | 无变化 |
| 3. 标记 RolledBack | 修改 `history[0].state` | `history[0]`: `Partial` → `RolledBack` |
| 4. 创建回滚记录 | 在 `history[0]` 后插入新记录 | 新增 `history[1]`: `{state: Partial, version: 4.11.18}` |
| 5. 更新 spec | 修改 `spec.desiredUpdate.version` | 无变化 |
| 6. 执行回滚 | CVO 执行回滚操作 | `history[1]`: `Partial` → `Completed` |

**为什么需要两个步骤（标记 + 创建）？**

1. **保留失败记录**：将失败的升级标记为 `RolledBack`，保留完整的审计历史
2. **记录回滚决策**：`completionTime` 记录回滚决策时间，而非回滚完成时间
3. **触发回滚执行**：创建新的 `Partial` 记录，表示回滚正在进行
4. **状态一致性**：`spec.desiredUpdate.version` 与 `history[1].version` 一致，触发回滚执行

**回滚完成**：
```yaml
spec:
  desiredUpdate:
    version: 4.11.18        # 保持回滚目标
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
status:
  desired:
    version: 4.11.18
    image: quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64
  history:
  - state: RolledBack       # 失败的升级
    version: 4.12.0
    startedTime: "2024-01-15T10:00:00Z"
    completionTime: "2024-01-15T11:00:00Z"
  - state: Completed        # 回滚成功
    version: 4.11.18
    startedTime: "2024-01-15T11:00:00Z"
    completionTime: "2024-01-15T12:00:00Z"
  - state: Completed
    version: 4.11.0
  conditions:
  - type: Available
    status: "True"
    reason: AsExpected
    message: "Cluster version is 4.11.18"
  - type: RollbackInProgress
    status: "False"         # 回滚已完成
```

#### 4.4.4 目标版本选择规则

**CVO 遵循以下规则选择回滚目标**：

| 规则 | 说明 | 示例 |
|------|------|------|
| **最近成功原则** | 选择最近的 `state=Completed` 版本 | 4.12.0 失败 → 回滚到 4.11.18 |
| **升级图验证** | 目标版本必须在官方升级图中 | 不能回滚到不在升级图中的版本 |
| **镜像验证** | 目标版本的发布镜像必须可用且已验证 | 镜像签名验证通过 |
| **兼容性检查** | 目标版本与当前组件兼容 | 不能回滚到不兼容的版本 |
| **单一回滚** | 只能回滚一个版本，不能跨多个版本 | 4.12.0 → 4.11.18，不能直接到 4.11.0 |

#### 4.4.5 特殊情况处理

**情况 1：没有可回滚版本**

```yaml
status:
  history:
  - state: Partial        # 只有失败的升级记录
    version: 4.12.0
  - state: Failed         # 没有 Completed 状态
    version: 4.11.18
```

**处理**：CVO 无法自动回滚，需要用户手动干预
```bash
# 用户需要手动指定回滚目标
oc adm upgrade --to=4.11.18 --allow-explicit-upgrade --force
```

**情况 2：回滚目标版本不可用**

```go
// 发布镜像无法拉取
if !cvo.isImageAvailable(rollbackImage) {
    return fmt.Errorf("rollback image %s not available", rollbackImage)
}
```

**处理**：CVO 会重试或等待用户介入

**情况 3：多次升级失败**

```yaml
status:
  history:
  - state: Partial        # 第三次升级失败
    version: 4.13.0
  - state: RolledBack     # 第二次升级失败并回滚
    version: 4.12.0
  - state: Completed      # 当前稳定版本
    version: 4.11.18
```

**处理**：回滚目标仍然是最近的 `Completed` 版本（4.11.18）

### 4.5 回滚触发机制

**核心问题**：当回滚目标版本（4.11.18）与 status 中的实际版本（4.11.18）一致时，CVO 如何触发回滚执行？

#### 4.5.1 关键对比：spec.desiredUpdate vs status.history

**CVO 通过对比 `spec.desiredUpdate.version` 与 `status.history[0].version` 来判断是否需要执行升级/回滚**

```
升级前：
  spec.desiredUpdate.version: 4.11.18
  status.history[0].version: 4.11.18
  status.history[0].state: Completed
  → 一致，无需操作

升级到 4.12.0：
  spec.desiredUpdate.version: 4.12.0  ← 用户设置目标
  status.history[0].version: 4.12.0
  status.history[0].state: Partial    ← 升级中
  → 目标与当前尝试版本一致，继续升级

升级失败触发回滚：
  spec.desiredUpdate.version: 4.11.18 ← CVO 修改为目标
  status.history[0].version: 4.12.0   ← 失败的版本
  status.history[0].state: RolledBack ← 标记为已回滚
  → 目标 (4.11.18) != 当前尝试 (4.12.0)，触发回滚

回滚执行中：
  spec.desiredUpdate.version: 4.11.18
  status.history[0].version: 4.12.0   ← 已标记为 RolledBack
  status.history[1].version: 4.11.18
  status.history[1].state: Partial    ← 正在回滚到此版本
  → 目标与回滚尝试版本一致，继续回滚

回滚完成：
  spec.desiredUpdate.version: 4.11.18
  status.history[0].version: 4.12.0   ← RolledBack
  status.history[1].version: 4.11.18
  status.history[1].state: Completed  ← 回滚成功
  → 一致，无需操作
```

#### 4.5.2 CVO 调谐循环逻辑

```go
func (cvo *ClusterVersionOperator) Reconcile() error {
    cv := cvo.getClusterVersion()
    
    // 1. 获取期望版本
    desiredVersion := cv.Spec.DesiredUpdate.Version
    
    // 2. 获取当前状态
    currentHistory := cv.Status.History[0]
    
    // 3. 判断是否需要操作
    if cvo.needsUpgradeOrRollback(desiredVersion, currentHistory) {
        // 4. 判断是升级还是回滚
        actionType := cvo.determineActionType(desiredVersion, currentHistory)
        
        // 5. 执行升级或回滚
        return cvo.executeUpgradeOrRollback(desiredVersion, actionType)
    }
    
    return nil
}

func (cvo *ClusterVersionOperator) needsUpgradeOrRollback(
    desiredVersion string,
    currentHistory configv1.UpdateHistory,
) bool {
    // 情况 1: 当前版本与期望版本一致且已完成 → 无需操作
    if currentHistory.Version == desiredVersion && 
       currentHistory.State == configv1.CompletedUpdateState {
        return false
    }
    
    // 情况 2: 当前版本与期望版本不一致 → 需要升级或回滚
    if currentHistory.Version != desiredVersion {
        return true
    }
    
    // 情况 3: 当前版本与期望版本一致但未完成 → 继续执行
    if currentHistory.State == configv1.PartialUpdateState {
        return true
    }
    
    return false
}

// determineActionType 判断是升级还是回滚
func (cvo *ClusterVersionOperator) determineActionType(
    desiredVersion string,
    currentHistory configv1.UpdateHistory,
) ActionType {
    currentVersion := currentHistory.Version
    
    // 情况 1: 版本相同，继续当前操作
    if currentVersion == desiredVersion {
        if currentHistory.State == configv1.PartialUpdateState {
            // 检查是否有 RolledBack 标记
            // 如果有，说明是回滚操作
            return cvo.inferActionFromHistory(currentHistory)
        }
        return ActionUpgrade // 默认是升级
    }
    
    // 情况 2: 版本不同，通过版本比较判断
    return cvo.compareVersions(desiredVersion, currentVersion)
}

// compareVersions 通过版本比较判断是升级还是回滚
func (cvo *ClusterVersionOperator) compareVersions(desired, current string) ActionType {
    // 使用语义化版本比较
    desiredSemver, err := semver.Parse(desired)
    if err != nil {
        return ActionUpgrade // 解析失败，默认升级
    }
    
    currentSemver, err := semver.Parse(current)
    if err != nil {
        return ActionUpgrade // 解析失败，默认升级
    }
    
    // 版本比较
    if desiredSemver.GT(currentSemver) {
        return ActionUpgrade   // desired > current → 升级
    } else if desiredSemver.LT(currentSemver) {
        return ActionRollback  // desired < current → 回滚
    }
    
    return ActionUpgrade // 版本相同，默认升级
}

// inferActionFromHistory 从历史记录推断操作类型
func (cvo *ClusterVersionOperator) inferActionFromHistory(history configv1.UpdateHistory) ActionType {
    // 检查 history 中是否有 RolledBack 状态的记录
    // 如果有，说明当前操作是回滚
    
    // 查找最近的 RolledBack 记录
    for _, h := range cvo.getClusterVersion().Status.History {
        if h.State == configv1.RolledBackUpdateState {
            return ActionRollback
        }
    }
    
    return ActionUpgrade
}

type ActionType string

const (
    ActionUpgrade   ActionType = "Upgrade"
    ActionRollback  ActionType = "Rollback"
)
```

#### 4.5.3 升级与回滚的判断逻辑

**核心问题**：当 `spec.desiredUpdate.version != status.history[0].version` 时，如何判断是执行升级还是回滚？

**判断方法 1: 版本比较（主要方法）**

CVO 通过比较版本号的大小来判断操作类型：

```go
func (cvo *ClusterVersionOperator) compareVersions(desired, current string) ActionType {
    // 使用语义化版本（Semantic Versioning）比较
    desiredSemver, _ := semver.Parse(desired)
    currentSemver, _ := semver.Parse(current)
    
    if desiredSemver.GT(currentSemver) {
        return ActionUpgrade   // 4.12.0 > 4.11.18 → 升级
    } else if desiredSemver.LT(currentSemver) {
        return ActionRollback  // 4.11.18 < 4.12.0 → 回滚
    }
    
    return ActionUpgrade
}
```

**版本比较示例**：

| 场景 | desired | current | 比较结果 | 操作类型 |
|------|---------|---------|---------|---------|
| 正常升级 | 4.12.0 | 4.11.18 | 4.12.0 > 4.11.18 | `ActionUpgrade` |
| 自动回滚 | 4.11.18 | 4.12.0 | 4.11.18 < 4.12.0 | `ActionRollback` |
| 手动回滚 | 4.11.0 | 4.11.18 | 4.11.0 < 4.11.18 | `ActionRollback` |
| 跨版本升级 | 4.13.0 | 4.11.18 | 4.13.0 > 4.11.18 | `ActionUpgrade` |

**问题 1: current 是如何获取的？**

`current` 版本是从 `status.history` 中获取的，但具体获取逻辑取决于 `history[0].state`：

```go
func (cvo *ClusterVersionOperator) getCurrentVersion(cv *configv1.ClusterVersion) string {
    if len(cv.Status.History) == 0 {
        return ""
    }
    
    latest := cv.Status.History[0]
    
    // 情况 1: 最新记录是 Completed 状态
    // → 直接使用其 version 作为 current
    if latest.State == configv1.CompletedUpdateState {
        return latest.Version
    }
    
    // 情况 2: 最新记录是 Partial 状态（升级中或失败）
    // → 需要查找上一个 Completed 记录作为 current
    if latest.State == configv1.PartialUpdateState {
        // 遍历 history，找到第一个 Completed 记录
        for i := 1; i < len(cv.Status.History); i++ {
            if cv.Status.History[i].State == configv1.CompletedUpdateState {
                return cv.Status.History[i].Version
            }
        }
    }
    
    // 情况 3: 最新记录是 RolledBack 状态
    // → 查找下一个 Completed 记录作为 current
    if latest.State == configv1.RolledBackUpdateState {
        for i := 1; i < len(cv.Status.History); i++ {
            if cv.Status.History[i].State == configv1.CompletedUpdateState {
                return cv.Status.History[i].Version
            }
        }
    }
    
    return ""
}
```

**升级成功时的 history 变化**：

```
升级前：
  history[0] = {state: Completed, version: 4.11.18}  ← current = 4.11.18

升级开始：
  history[0] = {state: Partial, version: 4.12.0}     ← 新记录插入
  history[1] = {state: Completed, version: 4.11.18}  ← 原记录后移
  → current = history[1].version = 4.11.18（保持不变）

升级成功：
  history[0] = {state: Completed, version: 4.12.0}   ← state 更新为 Completed
  history[1] = {state: Completed, version: 4.11.18}
  → current = history[0].version = 4.12.0（更新为新版本）
```

**升级失败时的 history 变化**：

```
升级前：
  history[0] = {state: Completed, version: 4.11.18}  ← current = 4.11.18

升级开始：
  history[0] = {state: Partial, version: 4.12.0}
  history[1] = {state: Completed, version: 4.11.18}
  → current = history[1].version = 4.11.18（保持不变）

升级失败：
  history[0] = {state: Partial, version: 4.12.0}     ← state 保持为 Partial
  history[1] = {state: Completed, version: 4.11.18}
  → current = history[1].version = 4.11.18（仍保持不变）
```

**关键结论**：

| 场景 | history[0].state | current 来源 | current 值 |
|------|------------------|-------------|-----------|
| 稳定状态 | `Completed` | `history[0].version` | 当前运行版本 |
| 升级中 | `Partial` | `history[1].version`（第一个 Completed） | 升级前版本 |
| 升级失败 | `Partial` | `history[1].version`（第一个 Completed） | 升级前版本 |
| 回滚中 | `Partial` | `history[1].version`（第一个 Completed） | 回滚目标版本 |

**current 获取的设计思路**：

**设计原则 1: current 表示"当前稳定运行的版本"**

`current` 的核心语义是**当前稳定运行的版本**，而不是"最新尝试的版本"。这决定了获取逻辑：

- `Completed` 状态表示该版本已稳定运行 → 可以作为 current
- `Partial` 状态表示该版本正在升级中或失败 → 不能作为 current
- `RolledBack` 状态表示该版本已被放弃 → 不能作为 current

**设计原则 2: 升级过程中 current 保持不变**

在升级过程中，虽然 `history[0]` 是新版本（Partial 状态），但集群实际仍在运行旧版本。因此：

- 升级开始前：`current = 4.11.18`（旧版本）
- 升级过程中：`current = 4.11.18`（仍为旧版本，因为新版本尚未稳定）
- 升级成功后：`current = 4.12.0`（新版本已稳定）

**设计原则 3: 通过查找第一个 Completed 记录确定 current**

无论 `history[0]` 是什么状态，`current` 总是从 `history` 中查找第一个 `Completed` 状态的记录。这确保了：

- 升级中：`history[0] = Partial (4.12.0)`，`history[1] = Completed (4.11.18)` → `current = 4.11.18`
- 升级失败：`history[0] = Partial (4.12.0)`，`history[1] = Completed (4.11.18)` → `current = 4.11.18`
- 回滚中：`history[0] = Partial (4.11.18)`，`history[1] = RolledBack (4.12.0)`，`history[2] = Completed (4.11.18)` → `current = 4.11.18`

**设计原则 4: current 用于版本比较判断操作类型**

`current` 的主要用途是与 `desired` 进行版本比较，判断是升级还是回滚：

```go
if desired > current {
    return ActionUpgrade   // 升级到更高版本
} else if desired < current {
    return ActionRollback  // 回滚到更低版本
}
```

**整体状态转换图**：

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        ClusterVersion 状态机                                 │
└─────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────┐
                              │   Accepted   │  ← 用户设置 desiredUpdate
                              └──────┬───────┘
                                     │
                                     │ 创建 Partial 记录
                                     ▼
                              ┌──────────────┐
                    ┌────────│    Partial    │────────┐
                    │        │  (升级中/失败) │        │
                    │        └──────┬───────┘        │
                    │               │                │
          升级成功  │      升级失败  │                │ 用户取消
                    │               │                │
                    ▼               ▼                ▼
             ┌────────────┐  ┌────────────┐  ┌────────────┐
             │  Completed │  │ RolledBack │  │  Partial   │
             │  (成功)    │  │  (已回滚)  │  │  (回滚中)  │
             └────────────┘  └─────┬──────┘  └─────┬──────┘
                                   │               │
                                   │ 创建回滚记录   │ 回滚成功
                                   ▼               │
                            ┌────────────┐         │
                            │  Partial   │◄────────┘
                            │  (回滚中)  │
                            └─────┬──────┘
                                  │
                          回滚成功 │
                                  ▼
                           ┌────────────┐
                           │  Completed │
                           │  (成功)    │
                           └────────────┘

状态说明：
- Accepted:     用户设置 desiredUpdate，等待 CVO 处理
- Partial:      升级/回滚正在进行，或升级失败等待处理
- Completed:    升级/回滚已成功完成（终态）
- RolledBack:   升级失败并已标记为回滚（终态）

history 数组变化：
- Accepted → Partial:     在 history[0] 插入新记录
- Partial → Completed:    更新 history[0].state = Completed
- Partial → RolledBack:   更新 history[0].state = RolledBack，插入新 Partial 记录
- RolledBack → Partial:   在 history[1] 插入回滚记录
- Partial → Completed:    更新 history[1].state = Completed
```

**状态转换规则**：

| 当前状态 | 触发条件 | 目标状态 | history 变化 |
|---------|---------|---------|-------------|
| `Accepted` | CVO 开始处理 | `Partial` | 插入 `history[0] = {Partial, desired}` |
| `Partial` | 升级成功 | `Completed` | 更新 `history[0].state = Completed` |
| `Partial` | 升级失败 | `Partial` | 保持 `history[0].state = Partial` |
| `Partial` | 触发回滚 | `RolledBack` | 更新 `history[0].state = RolledBack`，插入 `history[1] = {Partial, rollback}` |
| `RolledBack` | 回滚成功 | `Completed` | 更新 `history[1].state = Completed` |
| `Completed` | 用户设置新 desired | `Partial` | 插入新 `history[0] = {Partial, desired}` |

**current 值在不同状态下的含义**：

| 状态 | history 示例 | current 值 | 含义 |
|------|-------------|-----------|------|
| `Completed` | `[{Completed, 4.11.18}]` | `4.11.18` | 当前稳定运行的版本 |
| `Partial` (升级中) | `[{Partial, 4.12.0}, {Completed, 4.11.18}]` | `4.11.18` | 仍在运行旧版本 |
| `Partial` (升级失败) | `[{Partial, 4.12.0}, {Completed, 4.11.18}]` | `4.11.18` | 仍在运行旧版本 |
| `RolledBack` | `[{RolledBack, 4.12.0}, {Partial, 4.11.18}, {Completed, 4.11.18}]` | `4.11.18` | 正在回滚到旧版本 |
| `Partial` (回滚中) | `[{RolledBack, 4.12.0}, {Partial, 4.11.18}, {Completed, 4.11.18}]` | `4.11.18` | 正在回滚到旧版本 |
| `Completed` (回滚完成) | `[{RolledBack, 4.12.0}, {Completed, 4.11.18}, {Completed, 4.11.18}]` | `4.11.18` | 已回滚到旧版本 |

**问题 2: RolledBack 状态记录是什么？**

`RolledBack` 是 OpenShift 4.14+ 引入的特殊状态，用于标记**已回滚的升级记录**。

**UpdateState 完整定义**：

```go
type UpdateState string

const (
    // CompletedUpdateState 表示升级已成功完成
    CompletedUpdateState UpdateState = "Completed"
    
    // PartialUpdateState 表示升级正在进行或部分完成
    // 包括：升级中、升级失败、回滚中
    PartialUpdateState UpdateState = "Partial"
    
    // RolledBackUpdateState 表示升级失败并已回滚
    // 这是一个终态，表示该升级尝试已被放弃
    RolledBackUpdateState UpdateState = "RolledBack"
)
```

**RolledBack 的含义**：

1. **标记失败的升级**：将失败的升级记录标记为 `RolledBack`，表示该升级尝试已结束
2. **保留审计历史**：即使升级失败，也保留完整的升级历史记录
3. **区分回滚决策**：`completionTime` 记录回滚决策时间，而非回滚完成时间
4. **触发回滚执行**：标记后创建新的 `Partial` 记录，开始执行回滚

**RolledBack 的设置时机**：

```go
func (cvo *ClusterVersionOperator) markAsRolledBack(cv *configv1.ClusterVersion) {
    // 检查 history[0] 是否为失败的升级
    if len(cv.Status.History) == 0 {
        return
    }
    
    latest := cv.Status.History[0]
    
    // 只有 Partial 状态的记录才能被标记为 RolledBack
    if latest.State != configv1.PartialUpdateState {
        return
    }
    
    // 检查是否确实失败（Failing condition 为 True）
    isFailed := false
    for _, cond := range cv.Status.Conditions {
        if cond.Type == "Failing" && cond.Status == metav1.ConditionTrue {
            isFailed = true
            break
        }
    }
    
    if !isFailed {
        return
    }
    
    // 标记为 RolledBack
    cv.Status.History[0].State = configv1.RolledBackUpdateState
    cv.Status.History[0].CompletionTime = &metav1.Time{Time: time.Now()}
    
    // 发送事件
    cvo.recorder.Eventf(cv, corev1.EventTypeWarning, "UpgradeRolledBack",
        "Upgrade to %s failed and has been rolled back",
        latest.Version)
}
```

**RolledBack 的完整生命周期**：

```
T0: 升级到 4.12.0 开始
    history[0] = {state: Partial, version: 4.12.0}

T1: 升级失败
    history[0] = {state: Partial, version: 4.12.0}
    conditions = [{type: Failing, status: True}]

T2: CVO 检测到失败，触发自动回滚
    history[0] = {state: RolledBack, version: 4.12.0, completionTime: T2}  ← 标记为 RolledBack
    history[1] = {state: Partial, version: 4.11.18}                        ← 创建回滚记录

T3: 回滚执行中
    history[0] = {state: RolledBack, version: 4.12.0}
    history[1] = {state: Partial, version: 4.11.18}

T4: 回滚完成
    history[0] = {state: RolledBack, version: 4.12.0}
    history[1] = {state: Completed, version: 4.11.18, completionTime: T4}
```

**RolledBack 与 Partial 的区别**：

| 状态 | 含义 | completionTime | 是否终态 |
|------|------|----------------|---------|
| `Partial` | 升级进行中或失败 | 无 | 否（可转换） |
| `RolledBack` | 升级失败并已回滚 | 有（回滚决策时间） | 是（不可转换） |
| `Completed` | 升级成功完成 | 有（完成时间） | 是（不可转换） |

**为什么需要 RolledBack 状态？**

1. **保留完整历史**：即使升级失败，也保留记录用于审计和问题排查
2. **区分状态**：区分"正在进行的升级"（Partial）和"已回滚的升级"（RolledBack）
3. **记录决策时间**：`completionTime` 记录回滚决策时间，便于追踪回滚原因
4. **触发回滚**：标记后创建新的 `Partial` 记录，开始执行回滚

**判断方法 2: 历史记录推断（辅助方法）**

当版本相同时（`desired == current`），CVO 通过检查历史记录推断操作类型：

```go
func (cvo *ClusterVersionOperator) inferActionFromHistory(history configv1.UpdateHistory) ActionType {
    // 检查是否有 RolledBack 状态的记录
    for _, h := range cvo.getClusterVersion().Status.History {
        if h.State == configv1.RolledBackUpdateState {
            return ActionRollback  // 有 RolledBack 记录 → 回滚
        }
    }
    return ActionUpgrade
}
```

**判断方法 3: 升级图验证（安全校验）**

CVO 在执行操作前会验证目标版本是否在升级图中：

```go
func (cvo *ClusterVersionOperator) validateUpgradePath(desired, current string) error {
    // 检查目标版本是否在官方升级图中
    if !cvo.isVersionInUpgradeGraph(desired) {
        return fmt.Errorf("version %s not in upgrade graph", desired)
    }
    
    // 检查是否允许从 current 升级到 desired
    if !cvo.isUpgradeAllowed(current, desired) {
        return fmt.Errorf("upgrade from %s to %s not allowed", current, desired)
    }
    
    return nil
}
```

**完整的判断流程**：

```
┌─────────────────────────────────────────────────────────────┐
│              CVO 判断升级/回滚的完整流程                       │
└─────────────────────────────────────────────────────────────┘

步骤 1: 检查是否需要操作
  └─ desired == current && state == Completed → 无需操作
  └─ desired != current → 需要操作
  
步骤 2: 判断操作类型
  ├─ 方法 1: 版本比较（主要）
  │   └─ desired > current → ActionUpgrade
  │   └─ desired < current → ActionRollback
  │
  ├─ 方法 2: 历史记录推断（辅助）
  │   └─ 有 RolledBack 记录 → ActionRollback
  │   └─ 无 RolledBack 记录 → ActionUpgrade
  │
  └─ 方法 3: 升级图验证（安全）
      └─ 目标版本不在升级图中 → 拒绝操作
      └─ 不允许的路径 → 拒绝操作
  
步骤 3: 执行操作
  └─ ActionUpgrade → 执行升级流程
  └─ ActionRollback → 执行回滚流程
```

**关键洞察**：

1. **版本比较是主要方法**：通过语义化版本比较，`desired > current` 为升级，`desired < current` 为回滚
2. **历史记录是辅助方法**：当版本相同时，通过检查 `RolledBack` 状态推断操作类型
3. **升级图是安全校验**：确保操作路径在官方支持的范围内
4. **强制标志可覆盖**：用户可以通过 `--force` 标志强制执行不支持的操作

#### 4.5.4 回滚触发流程详解

**步骤 1: 升级失败检测**

```go
func (cvo *ClusterVersionOperator) detectUpgradeFailure(cv *configv1.ClusterVersion) bool {
    // 检查最新的升级记录
    if len(cv.Status.History) == 0 {
        return false
    }
    
    latest := cv.Status.History[0]
    
    // 检查是否是失败的升级
    if latest.State != configv1.PartialUpdateState {
        return false
    }
    
    // 检查是否超时
    if time.Since(latest.StartedTime.Time) > cvo.upgradeTimeout {
        return true
    }
    
    // 检查 Operator 健康状态
    for _, op := range cvo.getOperators() {
        if !op.isHealthy() {
            return true
        }
    }
    
    // 检查节点状态
    for _, node := range cvo.getNodes() {
        if !node.isReady() {
            return true
        }
    }
    
    return false
}
```

**步骤 2: 触发自动回滚**

```go
func (cvo *ClusterVersionOperator) handleUpgradeFailure(cv *configv1.ClusterVersion) error {
    // 1. 检查是否启用自动回滚
    if !cv.Spec.AutoRollback {
        return fmt.Errorf("auto rollback disabled, manual intervention required")
    }
    
    // 2. 获取回滚目标版本
    rollbackVersion, err := cvo.GetRollbackTarget(cv)
    if err != nil {
        return err
    }
    
    // 3. 标记当前升级为 RolledBack
    cv.Status.History[0].State = configv1.RolledBackUpdateState
    cv.Status.History[0].CompletionTime = &metav1.Time{Time: time.Now()}
    
    // 4. 设置回滚目标（关键步骤）
    cv.Spec.DesiredUpdate = &configv1.Update{
        Version: rollbackVersion,
        Image:   cvo.getReleaseImage(rollbackVersion),
    }
    
    // 5. 更新 ClusterVersion 对象
    if err := cvo.client.Update(context.TODO(), cv); err != nil {
        return err
    }
    
    // 6. 发送事件
    cvo.recorder.Eventf(cv, corev1.EventTypeWarning, "UpgradeFailed",
        "Upgrade to %s failed, rolling back to %s",
        cv.Status.History[0].Version, rollbackVersion)
    
    return nil
}
```

**步骤 3: 回滚执行**

```go
func (cvo *ClusterVersionOperator) executeUpgradeOrRollback(targetVersion string) error {
    cv := cvo.getClusterVersion()
    
    // 1. 检查是否是回滚（目标版本 < 当前版本）
    isRollback := cvo.isRollback(targetVersion, cv.Status.History[0].Version)
    
    // 2. 创建新的升级/回滚记录
    newHistory := configv1.UpdateHistory{
        State:       configv1.PartialUpdateState,
        Version:     targetVersion,
        Image:       cvo.getReleaseImage(targetVersion),
        StartedTime: metav1.Time{Time: time.Now()},
    }
    
    // 3. 插入到历史记录开头
    cv.Status.History = append([]configv1.UpdateHistory{newHistory}, cv.Status.History...)
    
    // 4. 更新状态
    cv.Status.Desired.Version = targetVersion
    cv.Status.Desired.Image = newHistory.Image
    
    // 5. 开始执行升级/回滚
    if isRollback {
        cvo.recorder.Eventf(cv, corev1.EventTypeNormal, "RollbackStarted",
            "Starting rollback to %s", targetVersion)
    } else {
        cvo.recorder.Eventf(cv, corev1.EventTypeNormal, "UpgradeStarted",
            "Starting upgrade to %s", targetVersion)
    }
    
    // 6. 执行实际的升级/回滚操作
    return cvo.performUpgradeOrRollback(targetVersion)
}
```

#### 4.5.4 状态转换图

```
┌─────────────────────────────────────────────────────────────┐
│                     升级/回滚状态机                           │
└─────────────────────────────────────────────────────────────┘

状态 1: 稳定状态
  spec.desiredUpdate.version = 4.11.18
  status.history[0].version = 4.11.18
  status.history[0].state = Completed
  → CVO: 无需操作

状态 2: 用户触发升级
  spec.desiredUpdate.version = 4.12.0  ← 用户修改
  status.history[0].version = 4.11.18
  status.history[0].state = Completed
  → CVO: 检测到不一致，开始升级

状态 3: 升级进行中
  spec.desiredUpdate.version = 4.12.0
  status.history[0].version = 4.12.0
  status.history[0].state = Partial
  → CVO: 继续升级

状态 4: 升级失败
  spec.desiredUpdate.version = 4.12.0
  status.history[0].version = 4.12.0
  status.history[0].state = Partial
  → CVO: 检测到失败，触发自动回滚

状态 5: 触发回滚（关键转换）
  spec.desiredUpdate.version = 4.11.18  ← CVO 修改
  status.history[0].version = 4.12.0
  status.history[0].state = RolledBack  ← 标记为已回滚
  → CVO: 检测到不一致 (4.11.18 != 4.12.0)，开始回滚

状态 6: 回滚进行中
  spec.desiredUpdate.version = 4.11.18
  status.history[0].version = 4.12.0 (RolledBack)
  status.history[1].version = 4.11.18
  status.history[1].state = Partial
  → CVO: 继续回滚

状态 7: 回滚完成
  spec.desiredUpdate.version = 4.11.18
  status.history[0].version = 4.12.0 (RolledBack)
  status.history[1].version = 4.11.18
  status.history[1].state = Completed
  → CVO: 一致，无需操作
```

#### 4.5.5 关键洞察

**回滚触发的本质是：`spec.desiredUpdate.version` 与 `status.history[0].version` 的不一致**

| 场景 | spec.desiredUpdate | status.history[0] | 是否触发 |
|------|-------------------|-------------------|---------|
| 稳定状态 | 4.11.18 | 4.11.18 (Completed) | ❌ 否 |
| 升级开始 | 4.12.0 | 4.11.18 (Completed) | ✅ 是 |
| 升级中 | 4.12.0 | 4.12.0 (Partial) | ✅ 是（继续） |
| 升级失败 | 4.12.0 | 4.12.0 (Partial) | ✅ 是（失败处理） |
| **触发回滚** | **4.11.18** | **4.12.0 (RolledBack)** | **✅ 是** |
| 回滚中 | 4.11.18 | 4.11.18 (Partial) | ✅ 是（继续） |
| 回滚完成 | 4.11.18 | 4.11.18 (Completed) | ❌ 否 |

**关键点**：
1. 升级失败时，CVO 将 `status.history[0].state` 标记为 `RolledBack`
2. CVO 修改 `spec.desiredUpdate.version` 为回滚目标版本（4.11.18）
3. 此时 `spec.desiredUpdate.version (4.11.18)` != `status.history[0].version (4.12.0)`
4. CVO 检测到不一致，触发回滚执行
5. 回滚执行时，创建新的历史记录 `status.history[1]`，版本为 4.11.18

### 4.6 完整回滚流程

#### 4.4.1 手动回滚流程

```
步骤 1: 查看升级历史
  └─ oc get clusterversion version -o yaml
     └─ 查看 status.history 字段

步骤 2: 确定回滚目标
  └─ 找到上一条 state=Completed 的记录
  └─ 记录其 version 字段（如 4.11.18）

步骤 3: 验证回滚路径
  └─ oc adm upgrade --allow-explicit-upgrade --to-image=<image>
  └─ 确认回滚路径可用

步骤 4: 触发回滚
  └─ oc adm upgrade --to=4.11.18 --allow-not-recommended
  └─ 或修改 ClusterVersion.spec.desiredUpdate

步骤 5: 监控回滚进度
  └─ oc get clusterversion version -w
  └─ 查看 status.history 中新增的回滚记录

步骤 6: 验证回滚完成
  └─ 确认 status.history[0].version = 4.11.18
  └─ 确认 status.history[0].state = Completed
  └─ 确认所有节点已回滚到 4.11.18
```

#### 4.4.2 自动回滚流程

```
步骤 1: 升级开始
  └─ CVO 开始执行升级
  └─ 更新 status.history[0].state = Partial

步骤 2: 检测到失败
  └─ Operator 健康检查失败
  └─ 或节点 NotReady
  └─ 或升级超时

步骤 3: 触发自动回滚
  └─ CVO 调用 GetRollbackTarget()
  └─ 获取可回滚版本（如 4.11.18）
  └─ 更新 spec.desiredUpdate.version = 4.11.18

步骤 4: 执行回滚
  └─ CVO 按照正常升级流程执行回滚
  └─ 回滚 Operator 到 4.11.18
  └─ MCO 回滚节点配置到 4.11.18

步骤 5: 更新历史
  └─ 更新 status.history[0].state = RolledBack
  └─ 新增 status.history[1].state = Completed (4.11.18)

步骤 6: 通知用户
  └─ 发送事件：UpgradeFailedAndRolledBack
  └─ 记录回滚原因和目标版本
```

#### 4.4.3 回滚状态转换

```
升级前：
  status.history:
  - state: Completed, version: 4.11.18  ← 当前版本
  - state: Completed, version: 4.11.0

升级中（失败）：
  status.history:
  - state: Partial, version: 4.12.0  ← 升级失败
  - state: Completed, version: 4.11.18
  - state: Completed, version: 4.11.0

回滚中：
  status.history:
  - state: Partial, version: 4.12.0  ← 标记为 RolledBack
  - state: Partial, version: 4.11.18  ← 正在回滚
  - state: Completed, version: 4.11.0

回滚完成：
  status.history:
  - state: RolledBack, version: 4.12.0  ← 已回滚
  - state: Completed, version: 4.11.18  ← 当前版本
  - state: Completed, version: 4.11.0
```

### 4.5 回滚数据模型

```go
type UpgradeHistory struct {
    FromVersion   string        // 升级前版本
    ToVersion     string        // 升级后版本
    StartedAt     *metav1.Time  // 开始时间
    CompletedAt   *metav1.Time  // 完成时间
    Result        UpgradeResult // 结果（Completed/Failed/Aborted）
    FailedStep    *UpgradeStep  // 失败的步骤
    RollbackTo    string        // 回滚到的版本
}
```

## 五、关键设计洞察

### 5.1 回滚能力对比

| 场景 | 是否支持回滚 | 回滚方式 | 复杂度 |
|------|------------|---------|--------|
| **安装失败** | ❌ 不支持 | 重建集群 | 低 |
| **扩容失败** | ✅ 支持 | 减少 replicas | 低 |
| **升级失败** | ✅ 支持 | 自动/手动回滚 | 高 |
| **配置变更失败** | ✅ 支持 | 回滚 MachineConfig | 中 |

### 5.2 回滚粒度

```
集群级回滚
  ├─ ClusterVersion 回滚
  ├─ Operator 回滚
  └─ 节点配置回滚

节点级回滚
  ├─ MachineConfig 回滚
  └─ 节点重启应用配置

资源级回滚
  ├─ Machine/MachineSet 回滚
  └─ 云资源销毁
```

### 5.3 回滚状态机

```
Installing → Installed → Upgrading → UpgradeFailed → RollingBack → RolledBack
                                    ↓
                              Healthy (升级成功)
```

### 5.4 关键设计原则

| 原则 | 说明 | OpenShift 实现 |
|------|------|---------------|
| **声明式** | 通过期望状态触发回滚 | 修改 `spec.desiredUpdate` |
| **渐进式** | 逐组件、逐节点回滚 | CVO 按顺序回滚 Operator |
| **可观测** | 完整的状态和事件记录 | `status.history` + Events |
| **安全** | 回滚前验证 | 健康检查 + 超时控制 |
| **幂等** | 多次回滚结果一致 | 基于期望状态的收敛 |

## 六、对 BKE 的借鉴意义

### 6.1 已借鉴的设计

从代码分析看，BKE 已借鉴 OpenShift 的核心设计：

| OpenShift | BKE 对应 | 说明 |
|-----------|---------|------|
| `ClusterVersion` | `ClusterVersion` | 集群版本管理 |
| `ReleaseImage` | `ReleaseImage` | 发布版本清单 |
| `UpgradeStrategy.AutoRollback` | `UpgradeStrategy.AutoRollback` | 自动回滚 |
| `UpgradeHistory` | `UpgradeHistory` | 升级历史 |
| `ClusterVersionRollingBack` | `ClusterVersionRollingBack` | 回滚状态 |

### 6.2 建议增强的能力

基于 OpenShift 经验，建议 BKE 增强以下能力：

#### 6.2.1 安装失败处理

```go
// 建议：增加安装失败处理机制
type InstallFailureHandler struct {
    // 自动重试策略
    RetryPolicy RetryPolicy
    
    // 清理策略
    CleanupStrategy CleanupStrategy
    
    // 通知策略
    NotificationStrategy NotificationStrategy
}
```

#### 6.2.2 扩容回滚优化

```go
// 建议：增强扩容回滚能力
type ScaleRollbackSpec struct {
    // 优雅删除策略
    GracefulDeletion bool
    
    // 数据保留策略
    RetainPVC bool
    
    // 回滚超时
    Timeout *metav1.Duration
    
    // 回滚钩子
    PreRollbackHook  *Hook
    PostRollbackHook *Hook
}
```

#### 6.2.3 升级回滚增强

```go
// 建议：增强升级回滚能力
type UpgradeRollbackSpec struct {
    // 自动回滚条件
    AutoRollbackConditions []RollbackCondition
    
    // 回滚策略
    Strategy RollbackStrategy
    
    // 回滚验证
    Validation RollbackValidation
    
    // 回滚历史保留
    HistoryRetention int
}

type RollbackCondition struct {
    // 条件类型（HealthCheck/Timeout/ErrorThreshold）
    Type RollbackConditionType
    
    // 阈值
    Threshold int
    
    // 时间窗口
    TimeWindow *metav1.Duration
}
```

### 6.3 实施建议

| 优先级 | 能力 | 工作量 | 价值 |
|--------|------|--------|------|
| **P0** | 升级失败自动回滚 | 中 | 高 |
| **P0** | 扩容失败自动回滚 | 低 | 高 |
| **P1** | 回滚历史审计 | 低 | 中 |
| **P1** | 回滚钩子机制 | 中 | 中 |
| **P2** | 跨版本回滚 | 高 | 低 |
| **P2** | 部分回滚 | 高 | 低 |

## 七、总结

### 7.1 OpenShift 回滚能力特点

1. **安装不支持回滚**：设计哲学是"快速失败，重建集群"
2. **扩容支持回滚**：通过声明式 API 减少 replicas
3. **升级支持回滚**：自动/手动回滚到相邻版本
4. **配置支持回滚**：通过 MachineConfig 版本管理

### 7.2 核心设计洞察

1. **声明式优于命令式**：通过修改期望状态触发回滚，而非调用回滚 API
2. **渐进式回滚**：逐组件、逐节点回滚，降低风险
3. **完整的历史记录**：`status.history` 提供完整的升级/回滚审计
4. **安全优先**：回滚前验证、超时控制、健康检查

### 7.3 对 BKE 的启示

1. **安装失败**：建议实现自动重试和清理机制，而非回滚
2. **扩容失败**：实现声明式回滚，减少 replicas 即可
3. **升级失败**：实现自动回滚，参考 OpenShift 的 `AutoRollback` 设计
4. **状态管理**：完善 `UpgradeHistory`，记录完整的升级/回滚历史

---

**报告完成**。此报告基于 OpenShift 4.x 架构和 BKE 代码分析，提供了完整的集群安装与扩容回滚能力洞察。
