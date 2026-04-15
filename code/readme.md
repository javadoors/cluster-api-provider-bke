# 完整的集群安装组件汇总表
## 集群安装组件汇总表
### 一、节点环境初始化组件
| 组件 | 版本 | 来源 | 安装命令/方式 | 配置文件路径 | 依赖 | 所属Scope | 源码位置 |
|------|------|------|-------------|-------------|------|-----------|---------|
| **内核参数** | - | - | `sysctl -p` 写入 `/etc/sysctl.d/k8s.conf` | `/etc/sysctl.d/k8s.conf` | - | kernel | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **Swap** | - | - | `swapoff -a` + `sed -ri 's/.*swap.*/#&/' /etc/fstab` | `/etc/sysctl.d/k8s-swap.conf` | - | swap | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **Firewall** | - | - | `systemctl stop/disable firewalld` 或 `ufw` | - | - | firewall | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **SELinux** | - | - | `setenforce 0` + 修改 `/etc/selinux/config` | `/etc/selinux/config` | - | selinux | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **时间同步** | - | NTP服务器(默认`cn.pool.ntp.org:123`) | `ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime` | - | bkeConfig.Cluster.NTPServer | time | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **Hosts** | - | - | 写入 `/etc/hosts` (集群节点IP-主机名映射) | `/etc/hosts` | - | hosts | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **IPVS模块** | - | - | `modprobe ip_vs/ip_vs_wrr/ip_vs_rr/ip_vs_sh/br_netfilter/nf_conntrack` | `/etc/sysconfig/modules/ip_vs.modules`(CentOS/Kylin), `/etc/modules`(Ubuntu) | proxyMode=ipvs | kernel | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **Ulimit** | - | - | 写入 `/etc/security/limits.conf` | `/etc/security/limits.conf` | - | kernel | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **HTTP Repo** | - | bkeConfig.YumRepo | `bkesource.SetSource()` + `httprepo.RepoUpdate()` | - | bkeConfig | httpRepo | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **DNS** | - | - | 创建 `/etc/resolv.conf` + CentOS关闭NetworkManager覆盖 | `/etc/resolv.conf` | - | dns | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| **Iptables** | - | - | 开放接入端/输出端/中转端 | - | - | iptables | [env/init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
### 二、容器运行时组件
| 组件 | 版本 | 来源 | 安装命令/方式 | 配置文件路径 | 依赖 | 源码位置 |
|------|------|------|-------------|-------------|------|---------|
| **Containerd** | 由`bkeConfig.Cluster.ContainerdVersion`指定 | HTTP Repo下载`containerd-{version}-linux-{arch}.tar.gz` | 下载tar.gz → 解压到 `/` → 渲染`config.toml` → `systemctl enable/restart containerd` | `/etc/containerd/config.toml`<br>`/etc/containerd/certs.d/{registry}/hosts.toml`<br>`/etc/systemd/system/containerd.service.d/10-override.conf` | HTTP Repo, containerdConfig CR(可选) | [containerd.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/containerd.go) |
| **Docker** | docker-ce(优先)或docker-engine | HTTP Repo(`httprepo.RepoInstall("docker-ce")`) | `httprepo.RepoInstall("docker-ce")` → 配置daemon.json → `systemctl enable/restart docker` | `/etc/docker/daemon.json` | HTTP Repo | [docker.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/docker/docker.go) |
| **CRI-Dockerd** | 0.3.9 | HTTP Repo下载`cri-dockerd-0.3.9-{arch}` | Downloader下载到`/usr/bin/cri-dockerd` → 渲染service/socket → `httprepo.RepoInstall("socat")` → `systemctl enable/restart cri-dockerd` | `/etc/systemd/system/cri-dockerd.service`<br>`/etc/systemd/system/cri-dockerd.socket` | Docker已安装, K8s >= 1.24 | [cri_docker.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/cridocker/cri_docker.go) |
| **Richrunc** | - | HTTP Repo下载`richrunc-{arch}` | Downloader下载 → rename=runc → saveto=`/usr/local/beyondvm` | - | Docker已安装, runtime=richrunc | [docker.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/docker/docker.go) |
| **Runc** | - | containerd tar.gz内置 | 随containerd tar.gz一起解压安装 | - | Containerd | [containerd.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/containerd.go) |
### 三、Kubernetes 核心组件
| 组件 | 版本 | 来源 | 安装命令/方式 | 配置文件路径 | 依赖 | 源码位置 |
|------|------|------|-------------|-------------|------|---------|
| **Kubelet** | 由`bkeConfig.Cluster.KubernetesVersion`指定 | HTTP Repo下载`kubelet-{version}-{arch}` | Downloader下载到`/usr/bin/kubelet` → 生成`config.yaml` → 渲染`kubelet.service` → `systemctl enable/restart kubelet` | `/var/lib/kubelet/config.yaml`<br>`/etc/systemd/system/kubelet.service`<br>`/etc/kubernetes/kubelet.sh` | 容器运行时已安装, 证书已就位 | [kubelet/run.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubelet/run.go) |
| **Kubectl** | 同K8s版本 | HTTP Repo下载`kubectl-{version}-{arch}` | Downloader下载到`/usr/bin/kubectl` chmod=755 | `/usr/bin/kubectl` | - | [command.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/command.go) |
| **kube-apiserver** | 同K8s版本 | 镜像仓库`{repo}/kubernetes/kube-apiserver:{version}` | Static Pod YAML渲染 → Kubelet自动拉起 | `/etc/kubernetes/manifests/kube-apiserver.yaml` | Kubelet运行中, 证书已就位 | [componentlist.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/componentlist.go) |
| **kube-controller-manager** | 同K8s版本 | 镜像仓库`{repo}/kubernetes/kube-controller-manager:{version}` | Static Pod YAML渲染 → Kubelet自动拉起 | `/etc/kubernetes/manifests/kube-controller-manager.yaml` | Kubelet运行中, 证书已就位 | [componentlist.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/componentlist.go) |
| **kube-scheduler** | 同K8s版本 | 镜像仓库`{repo}/kubernetes/kube-scheduler:{version}` | Static Pod YAML渲染 → Kubelet自动拉起 | `/etc/kubernetes/manifests/kube-scheduler.yaml` | Kubelet运行中, 证书已就位 | [componentlist.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/componentlist.go) |
| **etcd** | 默认`3.5.21-of.1` | 镜像仓库`{repo}/kubernetes/etcd:{etcdVersion}` | Static Pod YAML渲染 → 创建etcd用户和数据目录 → Kubelet自动拉起 | `/etc/kubernetes/manifests/etcd.yaml`<br>数据目录: `/var/lib/openFuyao/etcd` | Kubelet运行中, 证书已就位, etcd用户 | [manifests.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/manifests/manifests.go) |
| **Pause** | 默认`3.9` | 镜像仓库`{repo}/kubernetes/pause:{tag}` | 作为Kubelet/容器运行时的sandbox镜像预拉取 | - | 容器运行时已安装 | [exporter.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/imagehelper/exporter.go) |
### 四、证书组件
| 组件 | 版本 | 来源 | 安装命令/方式 | 配置文件路径 | 依赖 | 源码位置 |
|------|------|------|-------------|-------------|------|---------|
| **CA证书(ca/sa/etcd/proxy)** | - | 管理集群Secret | 从管理集群Secret下载到本地PKI目录 | `/etc/kubernetes/pki/` | 管理集群可达 | [certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/certs/certs.go) |
| **TLS服务端证书** | - | 本地生成(使用CA签发) | Cert插件生成apiserver/etcd等TLS证书 | `/etc/kubernetes/pki/` | CA证书已就位 | [certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/certs/certs.go) |
| **KubeConfig文件** | - | 本地生成 | 生成admin/kubelet/kube-proxy/controller-manager/scheduler的kubeconfig | `/etc/kubernetes/*.conf` | CA证书已就位 | [certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/certs/certs.go) |
| **Global CA** | - | 管理集群Secret | 下载trust-chain.crt/global-ca.crt/global-ca.key | `/etc/openFuyao/certs/` | isManager=true | [certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/certs/certs.go) |
### 五、高可用组件
| 组件 | 版本 | 来源 | 安装命令/方式 | 配置文件路径 | 依赖 | 源码位置 |
|------|------|------|-------------|-------------|------|---------|
| **HAProxy** | 默认`2.1.4` | 镜像仓库`{thirdRepo}/haproxy:2.1.4` | Static Pod YAML渲染(含haproxy.cfg) → Kubelet自动拉起 | `/etc/kubernetes/manifests/haproxy.yaml`<br>`/etc/openFuyao/haproxy/haproxy.cfg` | Kubelet运行中, IPVS模块加载 | [ha.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/ha/ha.go) |
| **Keepalived** | 默认`1.3.5` | 镜像仓库`{fuyaoRepo}/keepalived/keepalived:1.3.5` | Static Pod YAML渲染(含keepalived.conf + check脚本) → Kubelet自动拉起 | `/etc/kubernetes/manifests/keepalived.yaml`<br>`/etc/openFuyao/keepalived/keepalived.conf`<br>`/etc/openFuyao/keepalived/check-master.sh`<br>`/etc/openFuyao/keepalived/check-ingress.sh` | Kubelet运行中, VIP网络接口 | [ha.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/ha/ha.go) |
### 六、BKEAgent 组件
| 组件 | 版本 | 来源 | 安装命令/方式 | 配置文件路径 | 依赖 | 源码位置 |
|------|------|------|-------------|-------------|------|---------|
| **BKEAgent** | - | SSH推送二进制 + KubeConfig | SSH推送到节点 → 写入kubeconfig → 创建systemd service → 启动 | `/etc/openFuyao/bkeagent/`<br>`/etc/openFuyao/bkeagent/scripts/`<br>`/etc/openFuyao/bkeagent/bin/` | SSH可达, 管理集群kubeconfig | [ensure_bke_agent.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go) |
| **Registry证书** | - | 管理集群 | SSH推送到节点 | `/etc/openFuyao/certs/trust-chain.crt`<br>`/etc/openFuyao/certs/cert_config/` | - | [ensure_bke_agent.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go) |
### 七、Addon 组件
| 组件 | 版本 | 来源 | 安装命令/方式 | 配置文件路径 | 依赖 | 源码位置 |
|------|------|------|-------------|-------------|------|---------|
| **YAML类型Addon** | 由Addon定义 | Chart仓库下载YAML | 渲染模板 → `kubectl apply`到目标集群 | - | 目标集群可达 | [addon.go](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/addon.go) |
| **Chart类型Addon** | 由Addon定义 | Chart仓库 | `helm install/upgrade`到目标集群 | - | 目标集群可达, Helm | [addon.go](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/addon.go) |
### 八、额外安装脚本
| 组件 | 说明 | 源码位置 |
|------|------|---------|
| `install-lxcfs.sh` | 安装lxcfs | [ensure_nodes_env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go) |
| `install-nfsutils.sh` | 安装nfs工具 | 同上 |
| `install-etcdctl.sh` | 安装etcdctl | 同上 |
| `install-helm.sh` | 安装helm | 同上 |
| `install-calicoctl.sh` | 安装calicoctl | 同上 |
| `update-runc.sh` | 更新runc | 同上 |
| `clean-docker-images.py` | 清理docker镜像 | 同上 |
| `file-downloader.sh` | 文件下载器 | 同上 |
| `package-downloader.sh` | 包下载器 | 同上 |
### 九、关键配置默认值汇总
| 配置项 | 默认值 | 源码位置 |
|--------|--------|---------|
| Kubernetes版本 | `v1.25.6` | [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go) |
| Etcd版本 | `v3.5.21-of.1` | 同上 |
| Pause镜像Tag | `3.9` | 同上 |
| CgroupDriver | `systemd` | 同上 |
| Containerd数据目录 | `/var/lib/containerd` | 同上 |
| Docker数据目录 | `/var/lib/docker` | 同上 |
| Kubelet数据目录 | `/var/lib/kubelet` | 同上 |
| Etcd数据目录 | `/var/lib/openFuyao/etcd` | 同上 |
| 证书目录 | `/etc/kubernetes/pki` | 同上 |
| Manifests目录 | `/etc/kubernetes/manifests` | 同上 |
| 镜像仓库 | `deploy.bocloud.k8s:40443` | 同上 |
| HTTP Repo | `http.bocloud.k8s:40080` | 同上 |
| HAProxy镜像Tag | `2.1.4` | [ha.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/ha/ha.go) |
| Keepalived镜像Tag | `1.3.5` | 同上 |
| CRI-Dockerd版本 | `0.3.9` | [init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) |
| ClusterDNS域 | `cluster.local` | [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go) |
| ClusterDNS IP | `10.96.0.10` | 同上 |
| Service子网 | `10.96.0.0/16` | 同上 |
| Pod子网 | `10.250.0.0/16` | 同上 |
| API Bind Port | `6443` | 同上 |
### 十、组件安装顺序依赖图
```
1. BKEAgent推送 (SSH)
   ↓
2. 节点环境初始化 (Env Plugin)
   ├─ kernel → sysctl参数 + IPVS模块
   ├─ swap → 关闭swap
   ├─ firewall → 关闭防火墙
   ├─ selinux → 关闭SELinux
   ├─ time → 时间同步
   ├─ hosts → 写hosts
   ├─ httpRepo → 配置YUM源
   ├─ runtime → 安装容器运行时(Containerd/Docker+CRI-Dockerd)
   ├─ image → 预拉取镜像(pause/apiserver/controller-manager/scheduler/etcd)
   ├─ dns → 配置DNS
   ├─ iptables → 配置iptables
   └─ registry → 配置镜像仓库
   ↓
3. 负载均衡配置 (HA Plugin)
   ├─ 加载IPVS模块
   ├─ 渲染HAProxy Static Pod
   └─ 渲染Keepalived Static Pod
   ↓
4. 集群引导 (Kubeadm Plugin)
   ├─ InitControlPlane:
   │   ├─ 安装kubectl
   │   ├─ 下载/生成证书
   │   ├─ 渲染Static Pod YAML(apiserver/controller-manager/scheduler/etcd)
   │   ├─ 安装Kubelet(二进制 + config.yaml + service)
   │   └─ 上传Kubelet配置到管理集群
   ├─ JoinControlPlane:
   │   ├─ 安装kubectl
   │   ├─ 下载证书
   │   ├─ 安装Kubelet
   │   ├─ 渲染Static Pod YAML
   │   └─ 上传Global CA
   └─ JoinWorker:
       ├─ 下载证书
       ├─ 安装Kubelet
       └─ 安装kubectl
   ↓
5. Addon部署
   ├─ YAML类型: 渲染模板 → kubectl apply
   └─ Chart类型: helm install/upgrade
```
### 十一、重构关键注意事项
1. **Kubelet双模式配置**: 支持`useDeliveredConfig=true`(从KubeletConfig CR读取)和默认模式(本地生成config.yaml+service)，变量替换支持`${VAR}`和`${EXPR|command|END}`语法
2. **Containerd双模式配置**: 支持通过`containerdConfig` CR配置(新)和Legacy模式(参数拼接)，CR模式支持Script/Service/Main/Registry四部分配置
3. **Kylin系统特殊处理**: Docker环境下需将cgroupDriver改为`cgroupfs`
4. **CentOS 7特殊处理**: Containerd环境下需设置`fs.may_detach_mounts=1`
5. **NodeLocalDNS影响**: 启用nodelocaldns addon时，IPVS模式下clusterDNSIP使用localdns地址
6. **ProviderID必填**: 用于cluster-api兼容性，追加到kubelet config.yaml
7. **Etcd用户创建**: manifests插件会创建`etcd`系统用户并设置数据目录权限
8. **HA双模式**: Master HA(部署HAProxy+Keepalived) vs Ingress HA(仅部署Keepalived)
9. **CRI-Dockerd条件安装**: 仅Docker运行时 + K8s >= 1.24时安装
10. **Deep Restore**: 环境初始化支持`DeepRestore`模式，通过annotation控制
        
