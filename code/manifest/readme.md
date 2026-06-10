# 各资源标签与注解

## 各资源标签与注解详解

### 一、BKECluster 资源

#### 1. 标签

| 标签键 | 标签值 | 作用 | 代码位置 |
|-------|--------|------|---------|
| `kubernetes.customized/bocloud_custom_version` | 版本号 | 标识 Bocloud 版本 | [label/constants.go:52](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L52) |

#### 2. 注解

| 注解键 | 注解值 | 作用 | 代码位置 |
|-------|--------|------|---------|
| **集群管理类** |
| `bke.bocloud.com/cluster-from` | `bke`/`bocloud`/`other` | 标识集群来源：BKE 自建、Bocloud 托管、其他 | [common/constants.go:30](file:////cluster-api-provider-bke/common/constants.go#L30) |
| `bke.bocloud.com/dryrun` | `true`/`false` | 干运行模式，不执行实际操作 | [annotation/helper.go:22](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L22) |
| `bke.bocloud.com/pause` | - | 暂停 BKECluster 调谐 | [annotation/helper.go:23](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L23) |
| `bke.bocloud.com/full-management` | `true`/`false` | Bocloud 集群完全管理标志 | [annotation/helper.go:26](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L26) |
| **节点管理类** |
| `bke.bocloud.com/appointment-deleted-nodes` | `ip1,ip2,...` | 预约删除的节点列表 | [annotation/helper.go:49](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L49) |
| `bke.bocloud.com/appointment-add-nodes` | `ip1,ip2,...` | 预约添加的节点列表 | [annotation/helper.go:50](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L50) |
| `bke.bocloud.com/deep-restore-node` | `true`/`false` | 深度恢复节点标志 | [annotation/helper.go:41](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L41) |
| `bke.bocloud.com/master-schedulable` | `true`/`false` | Master 节点是否可调度 | [annotation/helper.go:43](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L43) |
| **配置与状态类** |
| `bke.bocloud.com/last-update-configuration` | JSON | 最后一次更新配置的快照 | [annotation/helper.go:27](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L27) |
| `bke.bocloud.com/status-record` | JSON | 状态记录 | [annotation/helper.go:32](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L32) |
| `bke.bocloud.com/collectd` | `base`/`agent`/... | 集群数据收集配置 | [annotation/helper.go:25](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L25) |
| **Agent 管理类** |
| `bke.bocloud.com/bkeagent-listener` | `current`/`bkecluster` | Agent 监听模式 | [common/constants.go:23](file:////cluster-api-provider-bke/common/constants.go#L23) |
| **超时配置类** |
| `bke.bocloud.com/node-boot-wait-timeout` | `10m` | 节点引导超时时间 | [annotation/helper.go:54](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L54) |
| `bke.bocloud.com/addon-boot-wait-timeout` | - | Addon 引导超时时间 | [annotation/helper.go:57](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L57) |
| **删除控制类** |
| `bke.bocloud.com/ignore-namespace-delete` | `true`/`false` | 删除时忽略命名空间 | [annotation/helper.go:45](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L45) |
| `bke.bocloud.com/ignore-target-cluster-delete` | `true`/`false` | 删除时忽略目标集群 | [annotation/helper.go:47](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L47) |
| **事件与阶段类** |
| `bke.bocloud.com/event` | - | BKE 事件标志 | [common/constants.go:20](file:////cluster-api-provider-bke/common/constants.go#L20) |
| `bke.bocloud.com/complete` | - | BKE 完成事件标志 | [common/constants.go:23](file:////cluster-api-provider-bke/common/constants.go#L23) |
| `bke.bocloud.com/at-precheck-phase` | - | 标记处于预检查阶段 | [annotation/helper.go:28](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L28) |
| **健康检查类** |
| `bke.bocloud.com/cluster-tracker-healthy-check-failed` | - | 集群健康检查失败标志 | [annotation/helper.go:55](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L55) |
| **重试控制类** |
| `bke.bocloud.com/retry` | - | 重试标志 | [annotation/helper.go:38](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L38) |

### 二、BKEMachine 资源

#### 1. 标签

| 标签键 | 标签值 | 作用 | 代码位置 |
|-------|--------|------|---------|
| `bke.bocloud.com/worker-node` | 节点主机 | Worker 节点绑定标志 | [label/constants.go:22](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L22) |
| `bke.bocloud.com/master-node` | 节点主机 | Master 节点绑定标志 | [label/constants.go:26](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L26) |

#### 2. 注解

| 注解键 | 注解值 | 作用 | 代码位置 |
|-------|--------|------|---------|
| `bke.bocloud.com/providerID` | ProviderID | 节点 ProviderID | [annotation/helper.go:29](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L29) |
| `bke.bocloud.com/command-reconciled` | - | Command 已调谐标志 | [annotation/helper.go:24](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L24) |


### 三、BKENode 资源

#### 1. 标签

| 标签键 | 标签值 | 作用 | 代码位置 |
|-------|--------|------|---------|
| `bke.bocloud.com/cluster-name` | 集群名称 | 关联的 BKECluster 名称 | [bkecluster_controller.go:278](file:////cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L278) |

### 四、Kubernetes Node 资源

#### 1. 标签

| 标签键 | 标签值 | 作用 | 代码位置 |
|-------|--------|------|---------|
| `node-role.kubernetes.io/master` | - | Master 节点角色 | [label/constants.go:29](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L29) |
| `node-role.kubernetes.io/node` | - | Worker 节点角色 | [label/constants.go:31](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L31) |
| `node-role.kubernetes.io/control-plane` | `control-plane` | 控制平面角色 | [label/constants.go:33](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L33) |
| `node-role.kubernetes.io/worker` | `worker` | Worker 角色标签 | [label/constants.go:36](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L36) |
| `kubernetes.io/alert` | `elastalert` | 告警标签 | [label/constants.go:40](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L40) |
| `kubernetes.customized/bocloud_custom_bare_metal` | - | 裸金属节点标志 | [label/constants.go:45](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L45) |
| `nodetype` | `loadbalance` | Beyond ELB 节点类型 | [label/constants.go:48](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L48) |
| `kubernetes.customized/bocloud_custom_version` | 版本号 | Bocloud 版本标签 | [label/constants.go:52](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L52) |
| `bke.bocloud.com/scripts` | - | 脚本标签 | [label/constants.go:55](file:////cluster-api-provider-bke/utils/capbke/label/constants.go#L55) |

#### 2. 注解

| 注解键 | 注解值 | 作用 | 代码位置 |
|-------|--------|------|---------|
| `kubeadm.kubernetes.io/etcd.advertise-client-urls` | URL 列表 | etcd 广播地址 | [annotation/helper.go:30](file:////cluster-api-provider-bke/utils/capbke/annotation/helper.go#L30) |

### 五、CAPI 资源

#### 1. 注解

| 注解键 | 注解值 | 作用 | 使用场景 |
|-------|--------|------|---------|
| `cluster.x-k8s.io/paused` | - | 暂停 CAPI 控制器调谐 | Pause/Resume 机制 |
| `cluster.x-k8s.io/delete-machine` | - | 标记 Machine 待删除 | 缩容场景 |
| `cluster.x-k8s.io/deletion-priority` | 优先级数值 | 删除优先级 | 缩容场景 |

### 六、关键注解使用示例

#### 1. 预约删除节点

```yaml
# 缩容场景：预约删除 2 个 Master
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/appointment-deleted-nodes: "192.168.1.11,192.168.1.12"
```
**作用**：指定要删除的节点，Phase 会优先删除这些节点。

#### 2. 预约添加节点

```yaml
# 扩容场景：分批添加节点
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/appointment-add-nodes: "192.168.1.13,192.168.1.14"
```
**作用**：控制节点分批加入，避免一次性加入过多节点。

#### 3. Agent 监听模式

```yaml
# Agent 监听当前集群
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/bkeagent-listener: "current"

# Agent 监听 BKECluster
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/bkeagent-listener: "bkecluster"
```
**作用**：
- `current`：Agent 已在监听当前集群，无需切换
- `bkecluster`：Agent 需要切换到监听 BKECluster

#### 4. 集群来源标识

```yaml
# BKE 自建集群
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/cluster-from: "bke"

# Bocloud 托管集群
BKECluster:
  metadata:
    annotations:
      bke.bocloud.com/cluster-from: "bocloud"
      bke.bocloud.com/full-management: "true"
```
**作用**：
- `bke`：BKE 自建集群，完全控制
- `bocloud`：Bocloud 托管集群，根据 `full-management` 决定控制程度

#### 5. 节点绑定

```yaml
# Master 节点绑定
BKEMachine:
  metadata:
    labels:
      bke.bocloud.com/master-node: "192.168.1.10"

# Worker 节点绑定
BKEMachine:
  metadata:
    labels:
      bke.bocloud.com/worker-node: "192.168.1.20"
```
**作用**：标识 BKEMachine 已绑定到具体节点，避免重复绑定。

### 七、注解设置默认值

```go
// SetBKEClusterDefaultAnnotation 设置 BKECluster 默认注解
func SetBKEClusterDefaultAnnotation(bkeCluster client.Object) {
    // 删除时忽略目标集群（默认 true）
    SetAnnotation(bkeCluster, DeleteIgnoreTargetClusterAnnotationKey, "true")
    
    // 删除时忽略命名空间（默认 true）
    SetAnnotation(bkeCluster, DeleteIgnoreNamespaceAnnotationKey, "true")
    
    // Agent 监听模式（默认 current）
    SetAnnotation(bkeCluster, BKEAgentListenerAnnotationKey, "current")
    
    // 集群来源（默认空）
    SetAnnotation(bkeCluster, BKEClusterFromAnnotationKey, "")
    
    // 深度恢复节点（默认 true）
    SetAnnotation(bkeCluster, DeepRestoreNodeAnnotationKey, "true")
    
    // Master 可调度（默认 false）
    SetAnnotation(bkeCluster, MasterSchedulableAnnotationKey, "false")
    
    // 节点引导超时（默认 10m）
    SetAnnotation(bkeCluster, NodeBootWaitTimeOutAnnotationKey, "10m")
}
```

### 八、总结

#### 1. 注解分类

| 分类 | 主要注解 | 作用 |
|------|---------|------|
| **集群管理** | `cluster-from`, `dryrun`, `pause`, `full-management` | 控制集群管理行为 |
| **节点管理** | `appointment-deleted-nodes`, `appointment-add-nodes` | 控制节点扩缩容 |
| **配置状态** | `last-update-configuration`, `status-record` | 记录配置和状态 |
| **Agent 管理** | `bkeagent-listener` | 控制 Agent 行为 |
| **删除控制** | `ignore-namespace-delete`, `ignore-target-cluster-delete` | 控制删除行为 |
| **超时配置** | `node-boot-wait-timeout`, `addon-boot-wait-timeout` | 配置超时时间 |

#### 2. 标签分类

| 分类 | 主要标签 | 作用 |
|------|---------|------|
| **节点角色** | `node-role.kubernetes.io/*` | 标识节点角色 |
| **节点绑定** | `bke.bocloud.com/master-node`, `worker-node` | 标识节点绑定关系 |
| **集群关联** | `bke.bocloud.com/cluster-name` | 关联 BKECluster |
| **版本标识** | `kubernetes.customized/bocloud_custom_version` | 标识版本 |
| **特殊节点** | `nodetype`, `kubernetes.customized/bare_metal` | 标识特殊节点类型 |

#### 3. 设计特点

- ✅ **注解用于控制行为**：暂停、删除、超时等
- ✅ **标签用于标识关系**：角色、绑定、关联
- ✅ **默认值机制**：确保关键注解存在
- ✅ **CAPI 标准兼容**：使用 CAPI 标准注解

# 各资源间的 OwnerReference 关系
        
## 各资源间的 OwnerReference 关系

### 一、OwnerReference 关系总览图

```
┌─────────────────────────────────────────────────────────────────────┐
│  CAPI Cluster                                                       │
│  (clusterv1.Cluster)                                                │
└─────────────────────────────────────────────────────────────────────┘
        │ OwnerRef                 │ Spec.InfrastructureRef
        │ (Controller)             │
        ▼                          ▼
┌──────────────────────┐    ┌──────────────────────────────────────┐
│  BKECluster          │    │  KubeadmControlPlane (KCP)           │
│  (bkev1beta1.        │    │  (controlplanev1.                    │
│   BKECluster)        │    │   KubeadmControlPlane)               │
└──────────────────────┘    └──────────────────────────────────────┘
        │ OwnerRef                     │ OwnerRef
        │ (Controller)                 │ (Controller)
        │                              │
        ├──────────────────────────────┼──────────────────────┐
        │                              │                      │
        ▼                              ▼                      ▼
┌──────────────────────┐    ┌──────────────────────┐  ┌─────────────────┐
│  Secret              │    │  ConfigMap           │  │  Command        │
│  (证书/k8sToken)      │    │  (证书配置)           │  │  (执行命令)      │
└──────────────────────┘    └──────────────────────┘  └─────────────────┘
        │
        │ OwnerRef (Controller)
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  BKEMachine                                                         │
│  (bkev1beta1.BKEMachine)                                            │
└─────────────────────────────────────────────────────────────────────┘
        │ OwnerRef (Controller)
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Command                                                            │
│  (agentv1beta1.Command)                                             │
│  (节点引导命令)                                                      │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  CAPI Machine                                                       │
│  (clusterv1.Machine)                                                │
└─────────────────────────────────────────────────────────────────────┘
        │ Spec.InfrastructureRef
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  BKEMachine                                                         │
│  (bkev1beta1.BKEMachine)                                            │
└─────────────────────────────────────────────────────────────────────┘
```

### 二、详细 OwnerReference 关系

#### 1. Cluster → BKECluster（CAPI 标准）

**关系类型**：`OwnerReference` (Controller)
```yaml
BKECluster:
  metadata:
    ownerReferences:
      - apiVersion: cluster.x-k8s.io/v1beta1
        kind: Cluster
        name: <cluster-name>
        uid: <cluster-uid>
        controller: true  # 标识为 Controller
        blockOwnerDeletion: true
```
**设置方式**：由 CAPI Cluster Controller 自动设置

**代码位置**：[ensure_cluster_api_obj.go:227](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster_api_obj.go#L227)
```go
// 获取关联的 CAPI Cluster
cluster, err := util.GetOwnerCluster(ctx, c, bkeCluster.ObjectMeta)
```
**作用**：
- ✅ 标识 BKECluster 属于哪个 Cluster
- ✅ Cluster 删除时级联删除 BKECluster
- ✅ 用于获取关联的 CAPI Cluster 对象

#### 2. Cluster → KubeadmControlPlane（CAPI 标准）

**关系类型**：`Spec.ControlPlaneRef`
```yaml
Cluster:
  spec:
    controlPlaneRef:
      apiVersion: controlplane.cluster.x-k8s.io/v1beta1
      kind: KubeadmControlPlane
      name: <kcp-name>
      namespace: <namespace>
```
**设置方式**：由 EnsureClusterAPIObj Phase 创建时设置

**作用**：
- ✅ 标识集群的控制平面
- ✅ KCP Controller 管理控制平面节点

#### 3. Cluster → BKECluster（CAPI 标准）

**关系类型**：`Spec.InfrastructureRef`
```yaml
Cluster:
  spec:
    infrastructureRef:
      apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
      kind: BKECluster
      name: <bkecluster-name>
      namespace: <namespace>
```
**设置方式**：由 EnsureClusterAPIObj Phase 创建时设置

**作用**：
- ✅ 标识集群的基础设施
- ✅ BKECluster Controller 管理基础设施

#### 4. Machine → BKEMachine（CAPI 标准）

**关系类型**：`Spec.InfrastructureRef`
```yaml
Machine:
  spec:
    infrastructureRef:
      apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
      kind: BKEMachine
      name: <bkemachine-name>
      namespace: <namespace>
```
**设置方式**：由 KCP Controller 创建 Machine 时设置

**作用**：
- ✅ 标识 Machine 的基础设施实现
- ✅ BKEMachine Controller 管理节点引导

#### 5. BKECluster → Secret（证书）

**关系类型**：`OwnerReference` (Controller)
```yaml
Secret:
  metadata:
    name: <cert-name>
    namespace: <namespace>
    ownerReferences:
      - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: BKECluster
        name: <bkecluster-name>
        uid: <bkecluster-uid>
        controller: true
        blockOwnerDeletion: true
```
**设置方式**：由证书生成器设置

**代码位置**：[certs/generator.go:469](file:////cluster-api-provider-bke/pkg/certs/generator.go#L469)
```go
controllerRef := metav1.NewControllerRef(k.bkeCluster, k.bkeCluster.GroupVersionKind())
secret.SetOwnerReferences([]metav1.OwnerReference{*controllerRef})
```
**作用**：
- ✅ BKECluster 删除时级联删除证书 Secret
- ✅ 避免证书泄露

#### 6. BKECluster → ConfigMap（证书配置）

**关系类型**：`OwnerReference` (Controller)
```yaml
ConfigMap:
  metadata:
    name: <cert-config-name>
    namespace: <namespace>
    ownerReferences:
      - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: BKECluster
        name: <bkecluster-name>
        uid: <bkecluster-uid>
        controller: true
        blockOwnerDeletion: true
```
**设置方式**：由证书生成器设置

**代码位置**：[certs/config.go:699](file:////cluster-api-provider-bke/pkg/certs/config.go#L699)
```go
cm.SetOwnerReferences([]metav1.OwnerReference{*controllerRef})
```
**作用**：
- ✅ BKECluster 删除时级联删除证书 ConfigMap
- ✅ 自动清理资源

#### 7. BKECluster → Secret（k8sToken）

**关系类型**：`OwnerReference` (Controller)
```yaml
Secret:
  metadata:
    name: k8s-token
    namespace: <namespace>
    ownerReferences:
      - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: BKECluster
        name: <bkecluster-name>
        uid: <bkecluster-uid>
        controller: true
        blockOwnerDeletion: true
```
**设置方式**：由 EnsureCluster Phase 设置

**代码位置**：[ensure_cluster.go:261](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L261)
```go
if err = controllerutil.SetControllerReference(bkeCluster, secret, scheme); err != nil {
    return err
}
```
**作用**：
- ✅ BKECluster 删除时级联删除 k8sToken Secret
- ✅ 自动清理资源

#### 8. BKECluster → ConfigMap（BKECluster 配置）

**关系类型**：`OwnerReference` (Controller)
```yaml
ConfigMap:
  metadata:
    name: bke-cluster
    namespace: cluster-system
    ownerReferences:
      - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: BKECluster
        name: <bkecluster-name>
        uid: <bkecluster-uid>
        controller: true
        blockOwnerDeletion: true
```
**设置方式**：由 MergeCluster 设置

**代码位置**：[mergecluster/bkecluster.go:577](file:////cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L577)
```go
if err := controllerutil.SetControllerReference(bkeCluster, newCm, scheme); err != nil {
    return err
}
```
**作用**：
- ✅ BKECluster 删除时级联删除配置 ConfigMap
- ✅ 自动清理资源

#### 9. BKEMachine → Command（节点引导命令）

**关系类型**：`OwnerReference` (Controller)
```yaml
Command:
  metadata:
    name: <command-name>
    namespace: <namespace>
    ownerReferences:
      - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: BKEMachine
        name: <bkemachine-name>
        uid: <bkemachine-uid>
        controller: true
        blockOwnerDeletion: true
```
**设置方式**：由 BaseCommand 设置

**代码位置**：[command/command.go:262](file:////cluster-api-provider-bke/pkg/command/command.go#L262)
```go
if err := controllerutil.SetControllerReference(b.OwnerObj, command, b.Scheme); err != nil {
    return errors.Wrapf(err, "failed to set controller reference")
}
```
**作用**：
- ✅ BKEMachine 删除时级联删除 Command
- ✅ Command Controller 通过 OwnerReference 找到 BKEMachine

#### 10. BKECluster → Command（集群级命令）

**关系类型**：`OwnerReference` (Controller)
```yaml
Command:
  metadata:
    name: <command-name>
    namespace: <namespace>
    ownerReferences:
      - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: BKECluster
        name: <bkecluster-name>
        uid: <bkecluster-uid>
        controller: true
        blockOwnerDeletion: true
```
**设置方式**：由 BaseCommand 设置（OwnerObj = BKECluster）

**作用**：
- ✅ BKECluster 删除时级联删除 Command
- ✅ 用于集群级操作（如升级、配置同步等）

### 三、OwnerReference 设置方式总结

| 设置方式 | 代码位置 | 使用场景 |
|---------|---------|---------|
| **CAPI Controller 自动设置** | CAPI Cluster Controller | Cluster → BKECluster |
| **controllerutil.SetControllerReference** | BKE 代码 | BKECluster → Secret/ConfigMap/Command |
| **metav1.NewControllerRef** | BKE 代码 | BKECluster → Secret (证书) |
| **Spec.InfrastructureRef** | CAPI 标准 | Cluster → BKECluster, Machine → BKEMachine |
| **Spec.ControlPlaneRef** | CAPI 标准 | Cluster → KCP |

### 四、OwnerReference 的作用

#### 1. 级联删除

```
删除 Cluster
    ↓
级联删除 BKECluster (OwnerRef)
    ↓
级联删除 Secret/ConfigMap/Command (OwnerRef)
```

#### 2. 对象关联

```go
// 通过 OwnerReference 获取关联对象
cluster, err := util.GetOwnerCluster(ctx, client, bkeCluster.ObjectMeta)

// 通过 InfrastructureRef 获取关联对象
bkeMachine := machine.Spec.InfrastructureRef
```

#### 3. 控制器触发

```go
// BKEMachine Controller Watch Command
Watches(
    &agentv1beta1.Command{},
    handler.EnqueueRequestForOwner(..., &bkev1beta1.BKEMachine{}),
)
// Command 变化时，通过 OwnerReference 找到 BKEMachine 并触发调谐
```

### 五、完整关系链

```
Cluster (CAPI)
    │
    ├── OwnerRef → BKECluster
    │                │
    │                ├── OwnerRef → Secret (证书)
    │                ├── OwnerRef → Secret (k8sToken)
    │                ├── OwnerRef → ConfigMap (证书配置)
    │                ├── OwnerRef → ConfigMap (BKECluster 配置)
    │                └── OwnerRef → Command (集群级命令)
    │
    ├── ControlPlaneRef → KubeadmControlPlane
    │                        │
    │                        └── OwnerRef → Machine
    │                                        │
    │                                        └── InfrastructureRef → BKEMachine
    │                                                                │
    │                                                                └── OwnerRef → Command (节点引导)
    │
    └── InfrastructureRef → BKECluster (同上)
```

### 六、总结

**OwnerReference 设计原则**：
- ✅ **层级关系**：父资源拥有子资源
- ✅ **级联删除**：父资源删除时自动删除子资源
- ✅ **控制器关联**：通过 OwnerReference 触发控制器调谐
- ✅ **CAPI 标准**：遵循 CAPI 的 InfrastructureRef 和 ControlPlaneRef 规范

**关键点**：
- ⚠️ BKECluster 的 OwnerReference 由 CAPI Cluster Controller 设置
- ⚠️ BKEMachine 的 OwnerReference 不直接指向 Machine（通过 InfrastructureRef 关联）
- ⚠️ Command 的 OwnerReference 可以指向 BKECluster 或 BKEMachine

