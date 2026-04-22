# NewEnsureProviderSelfUpgrade（bke-controller-manager自身的升级）
通过yaml安装的，只需要封装成ComponentVersion即可。
```yaml

```
# NewEnsureAgentUpgrade（bkeagent-deployer组件的升级）
bkeagent-deployer安装会安装，在引导集群安装管理集群时不使用bkeagent-deployer安装，而是通过引导集群远程拷贝到控制节点进行的二进制安装。

bkeagent是个镜像，镜像里只包含bkeagent的二进制镜像，通过oci下载bkeagent，下载后进行组件安装。就能直接封装成ComponentVersion结构，去掉bkeagent-deployer。
```yaml

```
# NewEnsureContainerdUpgrade（containerd运行时组件升级）
通过卸载再部署，需要梳理bkeagent中reset/containerd执行的脚本命令，确认可以封装成ComponentVersion。
// TODO
```yaml

```
1. 卸载对应bkeagent中的reset插件，scope=containerd-cfg（shell脚本可以搞定）
2. 安装对应bkeagent中的containerd插件
## containerd 安装、卸载与升级的具体 shell 命令：
### 卸载（Reset）
来源：[clean.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go) 中 `ContainerdCfgClean` 函数
```bash
# 停止并禁用服务
systemctl stop containerd
systemctl disable containerd

# 删除二进制文件
rm -f /usr/bin/containerd
rm -f /usr/bin/containerd-stress
rm -f /usr/bin/containerd-shim-shimless-v2
rm -f /usr/bin/containerd-shim-runc-v2
rm -f /usr/bin/crictl
rm -f /etc/crictl.yaml
rm -f /usr/bin/ctr
rm -f /usr/bin/nerdctl
rm -f /usr/lib/systemd/system/containerd.service
rm -f /usr/local/sbin/runc

# 删除目录
rm -rf /usr/local/beyondvm
rm -rf /etc/containerd/
```
### 安装（Redeploy）
来源：[containerd.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/containerd.go) 中 `startContainerdService` 函数
```bash
# 启用并启动服务
systemctl enable containerd
systemctl restart containerd
```
> 注：安装过程还包括二进制文件分发、配置文件生成（`config.toml`）等步骤，这些由 containerd plugin 内部逻辑完成，非直接 shell 命令。
### 升级（Upgrade）
来源：[ensure_containerd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go) 中 `rolloutContainerd` 函数

升级 = **先卸载再安装**，按顺序执行：
```bash
# 第一步：卸载（同上）
systemctl stop containerd
systemctl disable containerd
rm -f /usr/bin/containerd /usr/bin/containerd-stress /usr/bin/containerd-shim-shimless-v2 ...
rm -rf /usr/local/beyondvm /etc/containerd/
# 第二步：安装（同上）
systemctl enable containerd
systemctl restart containerd
```
**总结**：升级并非原地替换，而是 **reset → redeploy** 的两阶段操作。先彻底清理旧版 containerd 的服务、二进制和配置，再通过 K8sEnvInit 插件重新部署新版 containerd。

# NewEnsureEtcdUpgrade(ETCD升级，有状态)
1. ETCD备份执行的shell脚本
   ```shell
   ```
2. 逐节点滚动升级shell脚本
   ```shell
   ```
3. etcd为静态Pod（更新yaml文件）
4. etcd健康检查

```yaml

```
1. 安装对应bkeagent中的Kubeadm插件中的UpgradeEtcd阶段

     
## **etcd 安装、升级与卸载**的具体 shell 命令
### 安装
来源：[manifests.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/manifests/manifests.go) 中 `setupEtcdEnvironment` 和 `Execute` 函数
```bash
# 1. 创建 etcd 数据目录
mkdir -p -m 700 /var/lib/etcd
# 2. 创建 etcd 用户（如果不存在）
id etcd || useradd -r -c "etcd user" -s /sbin/nologin etcd -d /var/lib/etcd
# 3. 更改 etcd 数据目录所有者
chown -R etcd:etcd /var/lib/etcd
# 4. 重启 kubelet 以启动 etcd 静态 Pod
if [ -f /etc/systemd/system/kubelet.service ]; then systemctl restart kubelet; fi
```
> 注：etcd 以静态 Pod 形式运行，通过生成 `/etc/kubernetes/manifests/etcd.yaml` 由 kubelet 自动拉起。
### 升级
来源：[kubeadm.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go) 中 `upgradeEtcd` 和 `prepareUpgrade` 函数
```bash
# 1. 备份 etcd 数据（通过 etcd 快照 API，非 shell 命令）
# 调用 etcd snapshot.Save() 生成备份文件到 /var/lib/bke/workspace/etcd-backup/backup-<timestamp>.db

# 2. 备份 /etc/kubernetes 目录
mkdir -p /var/lib/bke/workspace/backup/etc/kubernetes
cp -rf /etc/kubernetes/* /var/lib/bke/workspace/backup/etc/kubernetes/

# 3. 预拉取镜像（可选，通过 K8sEnvInit 插件）

# 4. 重新生成 etcd manifest YAML（更新镜像版本）
# 生成新的 /etc/kubernetes/manifests/etcd.yaml

# 5. 重启 kubelet 应用新的静态 Pod
if [ -f /etc/systemd/system/kubelet.service ]; then systemctl restart kubelet; fi
```
> 升级采用**滚动升级**策略，逐个节点执行，等待 Pod Ready 后再升级下一个节点。
## 卸载/清理
来源：[clean.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go) 中 `ClusterClean` 函数
```bash
# 1. 清理 etcd 数据目录
rm -rf /var/lib/etcd
# 2. 清理 Kubernetes 配置目录（包含 etcd manifest）
rm -rf /etc/kubernetes
```
> etcd 作为静态 Pod 运行，删除 manifest 文件后 kubelet 会自动停止 Pod。集群重置时会彻底清理数据目录。
### 总结
| 操作 | 核心机制 | 关键 Shell 命令 |
|------|----------|-----------------|
| **安装** | Manifests 插件生成静态 Pod YAML | `mkdir`, `useradd`, `chown`, `systemctl restart kubelet` |
| **升级** | 备份 + 重新生成 Manifest + 重启 kubelet | `cp -rf`, `systemctl restart kubelet` |
| **卸载** | 删除 Manifest 和数据目录 | `rm -rf /var/lib/etcd`, `rm -rf /etc/kubernetes` |

**关键特点**：
- etcd 以**静态 Pod** 形式部署，非 systemd 服务
- 升级通过**更新镜像版本**实现，无需手动停止服务
- 备份使用 **etcd 快照 API**，非 shell 命令
- 清理时直接删除数据目录和 manifest 文件

# NewEnsureWorkerUpgrade

# NewEnsureMasterUpgrade
初步分析可以进行重构成ComponentVersion

# NewEnsureWorkerDelete

# NewEnsureMasterDelete

# NewEnsureComponentUpgrade

# NewEnsureCluster




