# NewEnsureProviderSelfUpgrade（bke-controller-manager自身的升级）
通过yaml安装的，只需要封装成ComponentVersion即可。

# NewEnsureAgentUpgrade（bkeagent-deployer组件的升级）
bkeagent-deployer安装会安装，在引导集群安装管理集群时不使用bkeagent-deployer安装，而是通过引导集群远程拷贝到控制节点进行的二进制安装。

bkeagent是个镜像，镜像里只包含bkeagent的二进制镜像，通过oci下载bkeagent，下载后进行组件安装。就能直接封装成ComponentVersion结构，去掉bkeagent-deployer。

# NewEnsureContainerdUpgrade（containerd运行时组件升级）
通过卸载再部署，需要梳理bkeagent中reset/containerd执行的脚本命令，确认可以封装成ComponentVersion。
// TODO

# NewEnsureEtcdUpgrade(ETCD升级，有状态)








