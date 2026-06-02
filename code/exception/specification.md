# 本工程的完整规格清单(包含异常场景)

## 一、核心CRD资源规格

### 1. BKECluster 集群规格
**文件位置**: [api/capbke/v1beta1/bkecluster_types.go](file://cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_types.go)

**核心字段**:
- **Spec**:
  - `controlPlaneEndpoint`: 控制平面端点
  - `clusterConfig`: 集群配置
    - `cluster`: 集群核心配置
      - `kubernetesVersion`: K8s版本 (v1.27.0 ~ v1.34.2)
      - `etcdVersion`: Etcd版本
      - `containerdVersion`: Containerd版本
      - `containerRuntime`: 容器运行时
      - `networking`: 网络配置
      - `httpRepo/imageRepo/chartRepo`: 仓库配置
      - `ntpServer`: NTP服务器
      - `labels`: 全局节点标签
    - `addons`: 插件列表
    - `customExtra`: 自定义参数
  - `pause`: 暂停标志
  - `dryRun`: 试运行标志
  - `reset`: 重置标志

- **Status**:
  - `phase`: 集群阶段
  - `clusterStatus`: 集群操作状态
  - `clusterHealthState`: 集群健康状态
  - `kubernetesVersion/etcdVersion/containerdVersion`: 版本信息
  - `agentStatus`: Agent状态
  - `addonStatus`: 插件状态
  - `phaseStatus`: 阶段执行状态
  - `conditions`: 条件列表

### 2. BKENode 节点规格
**文件位置**: [api/bkecommon/v1beta1/bkenode_types.go](file:///cluster-api-provider-bke/api/bkecommon/v1beta1/bkenode_types.go)

**核心字段**:
- **Spec**:
  - `ip`: 节点IP (必填)
  - `hostname`: 主机名
  - `port`: SSH端口
  - `username/password`: SSH认证信息
  - `role`: 节点角色
  - `controlPlane`: 控制平面配置覆盖
  - `kubelet`: Kubelet配置覆盖
  - `labels`: 节点标签

- **Status**:
  - `state`: 节点状态
  - `stateCode`: 状态码(位标志)
  - `message`: 状态消息
  - `needSkip`: 是否跳过

### 3. BKEMachine 机器规格
**文件位置**: [api/capbke/v1beta1/bkemachine_types.go](file:///cluster-api-provider-bke/api/capbke/v1beta1/bkemachine_types.go)

**核心字段**:
- **Spec**:
  - `providerID`: 提供者ID
  - `pause`: 暂停标志
  - `dryRun`: 试运行标志

- **Status**:
  - `ready`: 是否就绪
  - `bootstrapped`: 是否已引导
  - `addresses`: 机器地址列表
  - `node`: 节点信息
  - `conditions`: 条件列表

### 4. Command 命令规格
**文件位置**: [api/bkeagent/v1beta1/command_types.go](file:///cluster-api-provider-bke/api/bkeagent/v1beta1/command_types.go)

**核心字段**:
- **Spec**:
  - `nodeName`: 执行节点
  - `suspend`: 挂起标志
  - `commands`: 命令列表
    - `id`: 命令ID (唯一)
    - `command`: 命令内容
    - `type`: 命令类型
    - `backoffIgnore`: 失败是否跳过
    - `backoffDelay`: 重试延迟
  - `backoffLimit`: 最大重试次数
  - `activeDeadlineSecond`: 执行超时(默认600s)
  - `ttlSecondsAfterFinished`: 完成后保留时间
  - `nodeSelector`: 节点选择器

- **Status**:
  - `conditions`: 命令执行条件列表
  - `phase`: 执行阶段
  - `status`: 执行结果
  - `lastStartTime/completionTime`: 时间戳
  - `succeeded/failed`: 成功/失败计数

## 二、集群生命周期阶段规格

**文件位置**: [pkg/phaseframe/phases/list.go](file:///cluster-api-provider-bke/pkg/phaseframe/phases/list.go)

### 1. 集群初始化阶段
```
EnsureFinalizer → EnsureCerts → EnsureClusterAPIObj → EnsureMasterInit → 
EnsureBKEAgent → EnsureNodesEnv → EnsureLoadBalance → EnsureAgentSwitch
```

**各阶段功能**:
- **EnsureFinalizer**: 设置Finalizer,防止资源被误删
- **EnsureCerts**: 创建集群证书
- **EnsureClusterAPIObj**: 对接Cluster API对象
- **EnsureMasterInit**: 初始化第一个Master节点
- **EnsureBKEAgent**: 推送并部署BKE Agent
- **EnsureNodesEnv**: 准备节点环境(系统参数、依赖包等)
- **EnsureLoadBalance**: 配置负载均衡入口
- **EnsureAgentSwitch**: 切换Agent监听模式

### 2. 集群扩容阶段

**Master扩容**:
```
EnsureMasterJoin
```
- 验证Master数量为奇数
- 分发证书到新Master
- 执行kubeadm join
- 配置负载均衡

**Worker扩容**:
```
EnsureWorkerJoin
```
- 分发证书到Worker
- 执行kubeadm join
- 应用节点标签和污点

### 3. 集群缩容阶段

**Master缩容**:
```
EnsureMasterDelete
```
- 验证删除后Master数量仍为奇数
- 驱逐节点上的Pod
- 执行kubeadm reset
- 清理证书和配置

**Worker缩容**:
```
EnsureWorkerDelete
```
- 驱逐节点上的Pod
- 执行kubeadm reset
- 清理节点配置

### 4. 集群升级阶段
```
EnsureAgentUpgrade → EnsureContainerdUpgrade → EnsureEtcdUpgrade → 
EnsureWorkerUpgrade → EnsureMasterUpgrade → EnsureComponentUpgrade
```

**各阶段功能**:
- **EnsureAgentUpgrade**: 升级BKE Agent版本
- **EnsureContainerdUpgrade**: 升级Containerd运行时
- **EnsureEtcdUpgrade**: 升级Etcd版本
- **EnsureWorkerUpgrade**: 升级Worker节点
- **EnsureMasterUpgrade**: 升级Master节点(逐个滚动升级)
- **EnsureComponentUpgrade**: 升级openFuyao核心组件

### 5. 集群删除阶段
```
EnsurePaused → EnsureDeleteOrReset
```
- 暂停集群管理
- 删除所有节点
- 清理集群资源
- 移除Finalizer

### 6. 集群管理阶段
```
EnsureClusterManage
```
- 纳管现有集群
- 收集集群信息
- 对接Cluster API

### 7. 插件部署阶段
```
EnsureAddonDeploy
```
- 部署Helm Chart或YAML插件
- 等待插件就绪
- 记录插件状态

### 8. 后置处理阶段
```
EnsureNodesPostProcess
```
- 执行节点后置脚本
- 应用自定义配置

## 三、异常场景规格

### 1. 节点异常场景

**节点状态定义** ([api/capbke/v1beta1/bkecluster_consts.go:200-245](file:///cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_consts.go#L200-L245)):
- `NodeUnknown`: 未知状态
- `NodeInitializing`: 初始化中
- `NodeInitFailed`: 初始化失败
- `NodeBootStrapping`: 引导中
- `NodeBootStrapFailed`: 引导失败
- `NodeDeleting`: 删除中
- `NodeDeleteFailed`: 删除失败
- `NodeUpgrading`: 升级中
- `NodeUpgradeFailed`: 升级失败
- `NodeReady`: 就绪
- `NodeNotReady`: 未就绪
- `NodeManaging`: 纳管中
- `NodeManageFailed`: 纳管失败

**异常处理**:
- SSH连接失败 → 重试机制(最多3次)
- 命令执行失败 → 根据backoffIgnore决定是否跳过
- 节点NotReady → 触发集群健康检查
- 证书分发失败 → 标记节点失败,停止后续操作

### 2. 集群异常场景

**集群状态定义** ([api/capbke/v1beta1/bkecluster_consts.go:113-150](file:///cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_consts.go#L113-L150)):
- `ClusterReady`: 集群就绪
- `ClusterUnhealthy`: 集群不健康
- `ClusterUnknown`: 集群未知
- `ClusterChecking`: 集群检查中
- `ClusterInitializing`: 初始化中
- `ClusterInitializationFailed`: 初始化失败
- `ClusterUpgrading`: 升级中
- `ClusterUpgradeFailed`: 升级失败
- `ClusterMasterScalingUp/Down`: Master扩缩容中
- `ClusterWorkerScalingUp/Down`: Worker扩缩容中
- `ClusterScaleFailed`: 扩缩容失败
- `ClusterDeployingAddon`: 部署插件中
- `ClusterDeployAddonFailed`: 插件部署失败
- `ClusterDeleting`: 删除中
- `ClusterDeleteFailed`: 删除失败
- `ClusterPaused`: 已暂停
- `ClusterPauseFailed`: 暂停失败
- `ClusterDryRun`: 试运行
- `ClusterDryRunFailed`: 试运行失败
- `ClusterManaging`: 纳管中
- `ClusterManageFailed`: 纳管失败

**异常处理**:
- Master数量为偶数 → 拒绝操作,返回错误
- 无可用Worker节点 → 拒绝集群创建
- 版本不兼容 → 拒绝升级操作
- Etcd不健康 → 阻止Master升级
- API Server不可达 → 标记集群Unhealthy,触发重新协调

### 3. 命令执行异常场景

**命令阶段定义** ([api/bkeagent/v1beta1/command_types.go:33-42](file:///cluster-api-provider-bke/api/bkeagent/v1beta1/command_types.go#L33-L42)):
- `CommandPending`: 待执行
- `CommandRunning`: 执行中
- `CommandCompleted`: 已完成
- `CommandSuspend`: 已挂起
- `CommandSkip`: 已跳过
- `CommandFailed`: 执行失败
- `CommandUnKnown`: 未知状态

**异常处理**:
- 命令超时 → 标记失败,根据backoffIgnore决定是否继续
- 命令不存在 → 直接标记失败
- 重试次数耗尽 → 标记失败,停止后续命令
- 节点不可达 → 标记失败,等待节点恢复

### 4. 验证异常场景

**验证规则** ([common/cluster/validation/validation.go](file:///cluster-api-provider-bke/common/cluster/validation/validation.go)):

**节点验证**:
- 至少需要一个Master或MasterWorker节点
- 至少需要一个Worker或MasterWorker节点
- Master数量必须为奇数
- 节点IP必须唯一
- 节点Hostname必须唯一
- 节点角色不能冲突(如Master+Worker需使用MasterWorker)

**集群配置验证**:
- K8s版本范围: v1.21.0 ~ v1.28.0
- Docker运行时需K8s版本 >= v1.24.0
- 网络配置CIDR格式验证
- 仓库地址可达性验证
- NTP服务器可用性验证

**错误类型** ([common/cluster/validation/error.go](file:///cluster-api-provider-bke/common/cluster/validation/error.go)):
- `NoMasterNodeError`: 无Master节点
- `MasterNodeOddError`: Master数量不为奇数
- `NoEtcdNodeError`: 无Etcd节点
- `NoWorkerNodeError`: 无Worker节点

### 5. 阶段执行异常场景

**阶段状态定义** ([api/capbke/v1beta1/bkecluster_consts.go:152-158](file:///cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_consts.go#L152-L158)):
- `PhaseSucceeded`: 阶段成功
- `PhaseFailed`: 阶段失败
- `PhaseUnknown`: 阶段未知
- `PhaseWaiting`: 等待执行
- `PhaseRunning`: 执行中
- `PhaseSkipped`: 已跳过

**异常处理**:
- 前置阶段失败 → 后续阶段不执行
- 阶段执行超时 → 标记失败,等待重新协调
- 阶段Panic → 捕获异常,记录堆栈,标记失败
- 集群状态变更 → 重新计算待执行阶段

### 6. 条件检查异常场景

**条件类型定义** ([api/capbke/v1beta1/bkecluster_consts.go:67-91](file:///cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_consts.go#L67-L91)):
- `ControlPlaneEndPointSetCondition`: 控制平面端点设置
- `TargetClusterReadyCondition`: 目标集群就绪
- `TargetClusterBootCondition`: 目标集群引导
- `ClusterAddonCondition`: 插件状态
- `NodesInfoCondition`: 节点信息
- `BKEAgentCondition`: Agent状态
- `LoadBalancerCondition`: 负载均衡器
- `NodesEnvCondition`: 节点环境
- `ClusterAPIObjCondition`: Cluster API对象
- `SwitchBKEAgentCondition`: Agent切换
- `ControlPlaneInitializedCondition`: 控制平面初始化
- `BKEConfigCondition`: BKE配置
- `ClusterHealthyStateCondition`: 集群健康状态
- `NodesPostProcessCondition`: 后置处理

**异常处理**:
- 条件检查失败 → 记录原因和消息
- 条件超时 → 标记Unknown,等待重新检查
- 依赖条件不满足 → 阻塞当前操作

## 四、事件和原因规格

**文件位置**: [utils/capbke/constant/constants.go](file:///cluster-api-provider-bke/utils/capbke/constant/constants.go)

### 1. 正常事件原因
- `DryRunReason`: 试运行模式
- `BKEClusterPausedReason`: 集群暂停
- `NodesInfoReadyReason`: 节点信息就绪
- `BKEAgentReadyReason`: Agent就绪
- `LoadBalancerReadyReason`: 负载均衡器就绪
- `NodesEnvReadyReason`: 节点环境就绪
- `TargetClusterReadyReason`: 目标集群就绪
- `AddonDeploySucceededReason`: 插件部署成功
- `MasterInitReason`: Master初始化成功
- `MasterJoinSucceedReason`: Master加入成功
- `WorkerJoinSucceedReason`: Worker加入成功
- `MasterUpgradeSucceedReason`: Master升级成功
- `WorkerUpgradeSucceedReason`: Worker升级成功

### 2. 异常事件原因
- `NodesInfoNotReadyReason`: 节点信息未就绪
- `BKEAgentNotReadyReason`: Agent未就绪
- `LoadBalancerNotReadyReason`: 负载均衡器未就绪
- `NodesEnvNotReadyReason`: 节点环境未就绪
- `TargetClusterNotReadyReason`: 目标集群未就绪
- `AddonDeployFailedReason`: 插件部署失败
- `MasterJoinFailedReason`: Master加入失败
- `WorkerJoinFailedReason`: Worker加入失败
- `MasterUpgradeFailedReason`: Master升级失败
- `WorkerUpgradeFailedReason`: Worker升级失败
- `ClusterDeployFailedReason`: 集群部署失败
- `InternalErrorReason`: 内部错误
- `BKEConfigInvalidReason`: BKE配置无效
- `CommandExecFailedReason`: 命令执行失败
- `LostBKEConfigConfigMapReason`: BKE配置ConfigMap丢失

## 五、特殊场景规格

### 1. 集群纳管场景
- 支持纳管Bocloud集群(由Ansible部署)
- 支持纳管其他集群(如kubeadm部署)
- 纳管时收集集群信息但不执行管理操作
- Bocloud集群需先升级到v1.21.1才能被BKE接管

### 2. 版本兼容性场景
- 支持K8s版本: v1.27.0 ~ v1.34.2
- Docker运行时需K8s >= v1.24.0
- 支持跨版本升级(如v1.21 → v1.27)
- 升级路径验证和规划

### 3. 高可用场景
- Master节点数必须为奇数(1, 3, 5...)
- 支持多Master负载均衡
- Etcd集群嵌入在Master节点
- 证书自动分发和轮换

### 4. 资源限制场景
- 最大保留阶段状态历史: 20个
- 默认命令执行超时: 600秒
- 默认NTP请求超时: 10秒
- 证书过期预警: 提前30天

### 5. 并发控制场景
- 支持暂停集群管理
- 支持DryRun模式(不实际执行)
- 阶段串行执行,避免并发冲突
- 节点操作支持批量并发(默认10个)

这份规格清单涵盖了本工程的所有核心功能、生命周期阶段和异常处理场景,可作为开发和测试的完整参考依据。
        
