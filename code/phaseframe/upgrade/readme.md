# Upgrade
```go
// PostDeployPhases post deploy phases
	PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
		NewEnsureProviderSelfUpgrade, // bke-controller-manager 
		NewEnsureAgentUpgrade, // bkeagent-deployer
		NewEnsureContainerdUpgrade,
		NewEnsureEtcdUpgrade,
		NewEnsureWorkerUpgrade,
		NewEnsureMasterUpgrade,
		NewEnsureWorkerDelete,
		NewEnsureMasterDelete,
		NewEnsureComponentUpgrade,
		NewEnsureCluster,
	}
```
## 升级流程
### NewEnsureProviderSelfUpgrade(管理集群:单实例)
- bke-controller-manager 自升级：修改Deployment的yaml文件
```go
EnsureProviderSelfUpgradeName confv1beta1.BKEClusterPhase = "EnsureProviderSelfUpgrade"
providerNamespace                                         = "cluster-system"
providerDeploymentName                                    = "bke-controller-manager"
providerContainerName                                     = "manager"
providerImageName                                         = "cluster-api-provider-bke"
```
### NewEnsureAgentUpgrade(目标集群: 全节点滚动更新-K8s 原生)
- 修改bkeagent-deployer的Daemonset的yaml文件
### NewEnsureContainerdUpgrade(目标集群: 全节点并行)
二进制安装
- resetContainerd (保留数据目录，仅重置containerd配置)
- redeployContainerd(安装二进制)
### NewEnsureEtcdUpgrade(目标集群: etcd角色节点，逐节点串行滚动)
etcd 集群的滚动升级:节点滚动，确保集群始终有法定人数（quorum）可用。静态Pod升级。
- 备份首节点数据
- 逐节点升级
- 检查
### NewEnsureWorkerUpgrade（目标集群:node/work角色节点，串行逐节点）
升级kubelet/kubectl而不drain节点
- 升级kubelet
- 升级kubectl
### NewEnsureMasterUpgrade（目标集群:master角色节点，串行逐节点）
master节点也不drain节点
- prepareUpgrade
  - 备份etcd数据（首节点）
  - 备份集群配置
  - 预拉取镜像
- 重新生成apiserver/controller-manager/scheduler的静态Pod yaml文件
- 升级kubelet
- 升级kubectl
### NewEnsureWorkerDelete
- initialSetup
  - 暂停 MachineDeployment的运行，添加注解cluster.x-k8s.io/paused
- 对Machine添加cluster.x-k8s.io/delete-machine注解，标识删除
- processDrainAndMark
  - 对节点进行驱逐
  - 标识Machine为删除，添加cluster.x-k8s.io/delete-machine=""注解
- finalizeDeletion
  - 缩容MachineDeployment的副本数：触发控制器执行删除
  - 恢复MachineDeployment的运行，删除注解cluster.x-k8s.io/paused
### NewEnsureMasterDelete

### NewEnsureComponentUpgrade

### NewEnsureCluster

