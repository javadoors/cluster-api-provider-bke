# 梳理出完整的升级流程(特别关注KubeadmControlPlane部分)

## 一、升级流程总览

### 1. 升级阶段顺序
```
EnsureAgentUpgrade → EnsureContainerdUpgrade → EnsureEtcdUpgrade → 
EnsureWorkerUpgrade → EnsureMasterUpgrade → EnsureComponentUpgrade
```

**各阶段职责**:
- **EnsureAgentUpgrade**: 升级BKE Agent到目标版本
- **EnsureContainerdUpgrade**: 升级Containerd运行时
- **EnsureEtcdUpgrade**: 滚动升级Etcd集群(独立阶段)
- **EnsureWorkerUpgrade**: 滚动升级Worker节点
- **EnsureMasterUpgrade**: 滚动升级Master节点(核心阶段)
- **EnsureComponentUpgrade**: 升级openFuyao核心组件

## 二、KubeadmControlPlane与升级的关系

### 1. Cluster API对象架构
**文件位置**: [pkg/phaseframe/phaseutil/clusterapi.go](file:///c:/Users/z00820145/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/clusterapi.go)

```go
type ClusterAPIObjs struct {
    Cluster             *clusterv1beta1.Cluster
    KubeadmControlPlane *controlv1beta1.KubeadmControlPlane  // ← 关键对象
    MachineDeployment   *clusterv1beta1.MachineDeployment
    Machines            []*clusterv1beta1.Machine
}
```

### 2. KubeadmControlPlane的作用
**在BKE架构中,KubeadmControlPlane的作用有限**:

| 功能 | Cluster API标准用法 | BKE实际用法 |
|------|-------------------|------------|
| **控制平面管理** | KubeadmControlPlane控制器管理Master生命周期 | ❌ **不使用**,由BKE自行管理 |
| **版本升级** | 修改KubeadmControlPlane.Spec.Version触发升级 | ❌ **不使用**,由EnsureMasterUpgrade Phase直接执行 |
| **Machine创建** | KubeadmControlPlane自动创建Machine | ✅ 使用,但仅作为占位符 |
| **证书管理** | KubeadmControlPlane管理证书 | ❌ **不使用**,由EnsureCerts Phase管理 |

**关键结论**: BKE虽然创建了KubeadmControlPlane对象,但**升级操作完全由BKE自己的Phase流程控制**,不依赖KubeadmControlPlane控制器。

### 3. Cluster API对象创建流程
**文件位置**: [pkg/phaseframe/phases/ensure_cluster_api_obj.go](file:///c:/Users/z00820145/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster_api_obj.go)

```
EnsureClusterAPIObj Phase
│
├── 1. 生成Cluster API配置文件
│   └── BkeConfig.GenerateClusterAPIConfigFile()
│       ├── 生成Cluster对象
│       ├── 生成KubeadmControlPlane对象
│       ├── 生成MachineDeployment对象
│       └── 生成KubeadmConfigTemplate对象
│
├── 2. Apply YAML到管理集群
│   └── localClient.ApplyYaml(cluster-api-yaml)
│
└── 3. 等待OwnerRef设置
    └── Cluster控制器设置OwnerRef到BKECluster
```

**生成的KubeadmControlPlane对象示例**:
```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
metadata:
  name: my-cluster
  namespace: default
spec:
  version: v1.27.0  # ← 版本字段存在,但BKE不通过修改此字段触发升级
  replicas: 3
  machineTemplate:
    infrastructureRef:
      apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
      kind: BKEMachine
      name: my-cluster-controlplane
  kubeadmConfigSpec:
    # ... kubeadm配置
```

## 三、Master升级详细流程

### 1. EnsureMasterUpgrade Phase完整流程
**文件位置**: [pkg/phaseframe/phases/ensure_master_upgrade.go](file:///c:/Users/z00820145/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go)

```
┌─────────────────────────────────────────────────────────────┐
│  NeedExecute(old, new) — 判断是否需要执行                    │
└─────────────────────────────────────────────────────────────┘
         │
         ├── 1. DefaultNeedExecute → 检查spec变更
         ├── 2. ControlPlane是否已初始化
         └── 3. 是否有需要升级的Master节点
             └── GetNeedUpgradeMasterNodesWithBKENodes()
                 ├── 过滤Master角色节点
                 └── 比较Status.KubernetesVersion vs Spec.KubernetesVersion
         │
         ▼ 需要执行
┌─────────────────────────────────────────────────────────────┐
│  Execute() → reconcileMasterUpgrade()                       │
└─────────────────────────────────────────────────────────────┘
         │
         ├── Spec.KubernetesVersion != Status.KubernetesVersion?
         │   ├── 是 → rolloutUpgrade()
         │   └── 否 → 跳过
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│  rolloutUpgrade() — 主升级流程                               │
└─────────────────────────────────────────────────────────────┘
         │
         ├── Step 1: getNeedUpgradeNodes() — 获取升级节点列表
         │   ├── NodeFetcher.GetBKENodesWrapperForCluster()
         │   ├── GetNeedUpgradeMasterNodesWithBKENodes() — 版本过滤
         │   └── 检查Agent Ready状态
         │       ├── Agent不Ready → 跳过该节点
         │       └── 全部不Ready → 返回错误
         │
         ├── Step 2: 检查etcd配置,决定是否备份
         │   ├── NodeFetcher.GetNodesForBKECluster()
         │   ├── 提取etcd节点列表
         │   ├── etcd节点存在 → needBackupEtcd=true
         │   │   └── backEtcdNode = etcdNodes[0]
         │   └── etcd节点不存在 → needBackupEtcd=false
         │
         ├── Step 3: ensureEtcdAdvertiseClientUrlsAnnotation()
         │   └── 确保每个etcd Pod有advertise-client-urls注解
         │
         ├── Step 4: upgradeMasterNodesWithParams() — 逐节点升级
         │   └── 详见下方"单节点升级流程"
         │
         ├── Step 5: 更新集群状态版本号
         │   └── bkeCluster.Status.KubernetesVersion = Spec.KubernetesVersion
         │
         └── Step 6: updateAddonVersions() — 更新Addon版本
             ├── upgradeKubeProxy() — 更新kube-proxy镜像
             └── 更新kubectl版本(硬编码v1.25)
```

### 2. 单节点升级流程
```
对needUpgradeNodes中每个节点(串行):
│
├── 1. GetRemoteNodeByBKENode() — 获取远端Node资源
│
├── 2. 版本预检
│   └── NodeInfo.KubeletVersion == 目标版本? → 跳过该节点
│
├── 3. 标记节点状态为Upgrading
│   ├── SetNodeStateWithMessageForCluster(NodeUpgrading)
│   └── SyncStatusUntilComplete()
│
├── 4. upgradeNode() — 执行升级
│   │
│   ├── 4a. executeNodeUpgradeWithParams() — 下发升级命令
│   │   ├── 创建Upgrade Command (Phase=UpgradeControlPlane)
│   │   ├── 判断是否需要备份etcd
│   │   │   └── NeedBackupEtcd && 当前节点IP == backEtcdNode.IP
│   │   │       → upgrade.BackUpEtcd = true
│   │   ├── upgrade.New() — 创建命令资源
│   │   ├── upgrade.Wait() — 等待命令完成
│   │   └── 失败时: LogCommandFailed + MarkNodeStatusByCommandErrs
│   │
│   └── 4b. waitForNodeHealthCheckWithParams() — 等待健康检查
│       ├── NewRemoteClientByBKECluster()
│       └── waitForWorkerNodeHealthCheck() — 轮询检查
│           ├── 每2秒轮询,超时5分钟
│           ├── 获取远端Node资源
│           └── NodeHealthCheck()检查:
│               ├── Step1: 节点Ready状态
│               ├── Step2: Kubelet版本 == 目标版本
│               └── Step3: Master节点额外检查组件健康
│                   └── CheckComponentHealth: kube-apiserver等
│
├── 5a. 升级成功 → 标记NodeNotReady("Upgrading success")
│        └── SyncStatusUntilComplete()
│
└── 5b. 升级失败 → 标记NodeUpgradeFailed(err.Error())
     ├── SyncStatusUntilComplete()
     └── ⚠️ 立即返回错误,中断整个升级流程
```

### 3. Kubeadm插件执行层
**文件位置**: [pkg/job/builtin/kubeadm/kubeadm.go](file:///c:/Users/z00820145/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go)

```
upgradeControlPlane(backUpEtcd, clusterType)
│
├── Step 0: clusterType == "bocloud" 检查
│   └── (注释说明,但无实际代码)
│
├── Step 1: prepareUpgrade() — 升级前准备
│   ├── 1.1 backupEtcd() → 如果backUpEtcd=true则备份etcd
│   ├── 1.2 backupClusterEtc() → 备份集群配置
│   ├── 1.3 upgradePrePullImageCommand() → 预拉取镜像
│   └── 1.4 getBeforeUpgradeComponentPodHash() → 获取组件Pod Hash
│
├── Step 2: 设置boot参数
│   └── k.boot.Extra["upgradeWithOpenFuyao"] = ...
│
├── Step 3: 逐个升级控制平面组件(不含etcd)
│   │
│   ├── 组件1: kube-apiserver
│   │   ├── needUpgradeComponent() → 检查是否需要升级
│   │   ├── upgradeControlPlaneManifestCommand() → 生成新manifest
│   │   └── waitComponentReady() → 等待组件就绪
│   │
│   ├── 组件2: kube-controller-manager
│   │   └── (同上流程)
│   │
│   └── 组件3: kube-scheduler
│       └── (同上流程)
│
├── Step 4: 升级kubelet
│   └── installKubeletCommand()
│
└── Step 5: 升级kubectl
    └── installKubectlCommand()
```

**关键点**:
- **etcd不在upgradeControlPlane中升级**,有独立的upgradeEtcd函数
- 控制平面组件通过**静态Pod方式升级**:生成新manifest YAML,kubelet自动重启Pod
- kubelet/kubectl通过**二进制升级 + systemd重启**

## 四、Etcd升级流程(独立阶段)

**文件位置**: [pkg/phaseframe/phases/ensure_etcd_upgrade.go](file:///c:/Users/z00820145/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go)

```
EnsureEtcdUpgrade Phase
│
├── NeedExecute(old, new)
│   ├── Spec.EtcdVersion == Status.EtcdVersion? → 跳过
│   ├── Spec.EtcdVersion为空? → 跳过
│   └── Spec.EtcdVersion != Status.EtcdVersion → 需要升级
│
└── Execute() → reconcileEtcdUpgrade()
    │
    ├── filterUpgradeableNodes() — 过滤可升级etcd节点
    │   ├── 获取BKENodes列表
    │   ├── GetNeedUpgradeEtcdsWithBKENodes() — 版本过滤
    │   └── 检查Agent Ready状态
    │
    ├── determineBackupNode() — 确定备份节点
    │   ├── etcd节点数为0? → 不需要备份
    │   └── 选择第一个etcd节点作为备份节点
    │
    ├── upgradeNodes() — 逐节点滚动升级
    │   │
    │   ├── 节点1: master1
    │   │   ├── getEtcdImageVersion() — 获取当前版本
    │   │   ├── 版本比较 → 需要升级?
    │   │   ├── markNodeUpgrading() — 标记升级中
    │   │   ├── createUpgradeCommand() — 创建升级命令
    │   │   │   └── Phase=UpgradeEtcd
    │   │   ├── waitCommandComplete() — 等待命令完成
    │   │   └── waitForEtcdHealthCheck() — 等待健康检查
    │   │
    │   ├── 节点2: master2 (同上)
    │   └── 节点3: master3 (同上)
    │
    └── finalizeUpgrade()
        └── Status.EtcdVersion = Spec.EtcdVersion
```

## 五、Worker升级流程

**文件位置**: [pkg/phaseframe/phases/ensure_worker_upgrade.go](file:///c:/Users/z00820145/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go)

```
EnsureWorkerUpgrade Phase
│
├── NeedExecute(old, new)
│   ├── 集群状态正常?
│   ├── ControlPlane已初始化?
│   └── 有需要升级的Worker节点?
│
└── Execute() → reconcileWorkerUpgrade()
    │
    ├── prepareUpgradeNodes() — 准备升级节点
    │   ├── 获取BKENodes
    │   ├── GetNeedUpgradeWorkerNodesWithBKENodes() — 版本过滤
    │   └── 检查Agent Ready状态
    │
    └── rolloutUpgrade() — 滚动升级
        │
        ├── 创建Drainer (用于驱逐Pod)
        │   └── NewDrainer(clientSet, nil, false, log)
        │
        └── processNodeUpgrade() — 处理节点升级
            │
            ├── 对每个节点:
            │   ├── GetRemoteNodeByBKENode()
            │   ├── 版本预检 → 已是目标版本? 跳过
            │   ├── 标记NodeUpgrading
            │   ├── upgradeNode()
            │   │   ├── drainNode() — 驱逐节点上的Pod
            │   │   ├── createUpgradeCommand() — Phase=UpgradeWorker
            │   │   ├── waitCommandComplete()
            │   │   └── waitForWorkerNodeHealthCheck()
            │   │
            │   ├── 成功 → 标记NodeNotReady("Upgrading success")
            │   └── 失败 → 记录failedUpgradeNodes,继续下一个
            │       ⚠️ 与Master不同,Worker失败不中断流程
            │
            └── 返回结果
                ├── len(failedUpgradeNodes) == 0 → 成功
                └── len(failedUpgradeNodes) > 0 → 返回错误,等待重试
```

## 六、关键设计对比

| 维度 | Master升级 | Worker升级 | Etcd升级 |
|------|-----------|-----------|---------|
| **升级策略** | 串行逐节点 | 串行逐节点 | 串行逐节点 |
| **失败策略** | **Fail-Fast**(立即中断) | **Best-Effort**(继续下一个) | **Fail-Fast**(立即中断) |
| **etcd备份** | 首个etcd节点备份 | 不备份 | 首个etcd节点备份 |
| **健康检查** | Ready + 版本 + **组件健康** | Ready + 版本 | **etcd健康检查** |
| **版本号更新** | ✅ 升级后更新Status.KubernetesVersion | ❌ 不更新 | ✅ 升级后更新Status.EtcdVersion |
| **Addon更新** | ✅ kube-proxy + kubectl | ❌ 不涉及 | ❌ 不涉及 |
| **Drain操作** | 未使用 | ✅ 驱逐Pod | 未使用 |
| **Phase标识** | UpgradeControlPlane | UpgradeWorker | UpgradeEtcd |

## 七、升级命令执行流程

```
Upgrade Command创建
│
├── 创建Command CR对象
│   ├── spec.nodeName = 节点IP
│   ├── spec.commands = [Kubeadm命令]
│   │   └── ["Kubeadm", "phase=UpgradeControlPlane", 
│   │       "bkeConfig=ns:name", "backUpEtcd=true"]
│   ├── spec.backoffLimit = 0 (不重试)
│   └── spec.activeDeadlineSecond = 600
│
├── Agent监听Command
│   ├── 发现新的Command CR
│   ├── 解析commands字段
│   └── 调用KubeadmPlugin.Execute()
│
├── KubeadmPlugin执行
│   ├── 解析phase参数
│   ├── phase=UpgradeControlPlane?
│   │   └── upgradeControlPlane(backUpEtcd, clusterType)
│   └── 返回执行结果
│
└── Controller等待
    ├── 轮询Command.Status.Phase
    │   ├── Pending → 继续等待
    │   ├── Running → 继续等待
    │   ├── Completed → 成功
    │   └── Failed → 失败
    └── 更新节点状态
```

## 八、总结

### 1. KubeadmControlPlane的实际作用
在BKE架构中,KubeadmControlPlane对象**仅作为Cluster API生态的占位符存在**,实际的升级操作完全由BKE自己的Phase流程控制:

- ✅ **创建**: 在EnsureClusterAPIObj阶段创建
- ❌ **升级**: 不通过修改KubeadmControlPlane.Spec.Version触发
- ❌ **管理**: 不依赖KubeadmControlPlane控制器
- ✅ **Machine创建**: 利用Cluster API的Machine创建机制

### 2. 升级流程的核心特点
- **分阶段执行**: Agent → Containerd → Etcd → Worker → Master → Component
- **滚动升级**: 所有节点逐个升级,确保集群可用性
- **Fail-Fast策略**: Master/Etcd失败立即中断,Worker失败继续下一个
- **健康检查严格**: Master额外检查组件健康,Etcd检查集群健康
- **版本号统一**: Master升级完成后统一更新集群版本号

### 3. 与标准Cluster API的差异
BKE实现了自己的升级逻辑,而不是使用Cluster API标准的KubeadmControlPlane升级机制,主要原因是:
- 需要更精细的控制(如etcd独立升级)
- 需要支持Bocloud集群纳管场景
- 需要自定义健康检查逻辑
- 需要集成openFuyao组件升级
        
