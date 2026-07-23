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
    State UpdateState `json:"state"`
    
    // version 是目标版本
    Version string `json:"version"`
    
    // image 是发布镜像
    Image string `json:"image"`
    
    // startedTime 是升级开始时间
    StartedTime metav1.Time `json:"startedTime"`
    
    // completionTime 是升级完成时间（仅当 state=Completed 时）
    CompletionTime *metav1.Time `json:"completionTime,omitempty"`
    
    // verified 表示发布镜像是否已验证
    Verified bool `json:"verified"`
    
    // acceptedRisks 记录升级过程中接受的风险
    AcceptedRisks string `json:"acceptedRisks,omitempty"`
}

type UpdateState string

const (
    CompletedUpdateState UpdateState = "Completed"
    PartialUpdateState   UpdateState = "Partial"
    AcceptedUpdateState  UpdateState = "Accepted"
)
```

#### 4.3.2 升级历史示例

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
  - state: Completed
    version: 4.11.0
    image: quay.io/openshift-release-dev/ocp-release:4.11.0-x86_64
    startedTime: "2023-10-15T10:00:00Z"
    completionTime: "2023-10-15T11:30:00Z"
    verified: true
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

### 4.4 完整回滚流程

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
