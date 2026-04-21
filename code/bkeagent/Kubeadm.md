# Kubeadm Plugin 规格及执行的 Shell 命令梳理
> **注意**：`ensure_bke_agent.go` 文件本身并不直接包含 Kubeadm Plugin 的逻辑。它的职责是通过 SSH 将 BKE Agent 推送到目标节点并启动。Kubeadm Plugin 是由 BKE Agent 在远端节点上**本地执行**的。以下梳理涵盖 `ensure_bke_agent.go` 中执行的 SSH 命令，以及它间接触发的 Kubeadm Plugin 的完整规格和 Shell 命令。
## 一、`ensure_bke_agent.go` 中直接执行的 SSH Shell 命令
### 1. PreCommand（前置命令）— [ensure_bke_agent.go:555-566](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L555-L566)
| 顺序 | Shell 命令 | 用途 |
|------|-----------|------|
| 1 | `chmod 777 /usr/local/bin/` | 放开二进制目录写权限 |
| 2 | `chmod 777 /etc/systemd/system/` | 放开 systemd 目录写权限 |
| 3 | `systemctl stop bkeagent 2>&1 >/dev/null \|\| true` | 停止旧的 bkeagent 服务 |
| 4 | `systemctl disable bkeagent 2>&1 >/dev/null \|\| true` | 禁用旧的 bkeagent 自启动 |
| 5 | `systemctl daemon-reload 2>&1 >/dev/null \|\| true` | 重载 systemd 配置 |
| 6 | `rm -rf /usr/local/bin/bkeagent* 2>&1 >/dev/null \|\| true` | 删除旧的 bkeagent 二进制 |
| 7 | `rm -f /etc/systemd/system/bkeagent.service 2>&1 >/dev/null \|\| true` | 删除旧的 service 文件 |
| 8 | `rm -rf /etc/openFuyao/bkeagent 2>&1 >/dev/null \|\| true` | 清理旧的 bkeagent 配置目录 |
### 2. HostCustomCmdFunc（按主机架构定制的命令）— [ssh.go:68-80](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/ssh.go#L68-L80)
| 顺序 | Shell 命令 / 文件上传 | 用途 |
|------|----------------------|------|
| 1 | `echo {hostname} > /etc/openFuyao/bkeagent/node` | 写入节点 hostname 标识 |
| 2 | 上传 `/bkeagent_linux_{arch}` → `/usr/local/bin/` | 上传对应架构的 bkeagent 二进制 |
### 3. StartCommand（启动命令）— [ensure_bke_agent.go:582-596](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L582-L596)
| 顺序 | Shell 命令 / 文件上传 | 用途 |
|------|----------------------|------|
| 1 | 上传 `bkeagent.service` → `/etc/systemd/system/` | 上传 systemd 服务文件 |
| 2 | 上传 `trust-chain.crt` → `/etc/openFuyao/certs/` | 上传证书链 |
| 3 | 上传 `global-ca.crt` / `global-ca.key` → `/etc/openFuyao/certs/`（仅 cluster-api 场景） | 上传全局 CA |
| 4 | 上传 CSR 配置文件 → `/etc/openFuyao/certs/cert_config/` | 上传证书签名请求配置 |
| 5 | `mkdir -p -m 755 /etc/openFuyao/certs` | 创建证书目录 |
| 6 | `mv -f /usr/local/bin/bkeagent_* /usr/local/bin/bkeagent` | 重命名二进制文件 |
| 7 | `mkdir -p -m 777 /etc/openFuyao/bkeagent` | 创建配置目录 |
| 8 | `chmod +x /usr/local/bin/bkeagent` | 赋予执行权限 |
| 9 | `echo -e {kubeconfig} > /etc/openFuyao/bkeagent/config` | 写入 kubeconfig |
| 10 | `systemctl daemon-reload 2>&1 >/dev/null` | 重载 systemd |
| 11 | `systemctl enable bkeagent 2>&1 >/dev/null` | 设置开机自启 |
| 12 | `systemctl restart bkeagent 2>&1 >/dev/null` | 启动 bkeagent 服务 |
### 4. PostCommand（后置命令）— [ensure_bke_agent.go:537-543](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L537-L543)
| 顺序 | Shell 命令 | 用途 |
|------|-----------|------|
| 1 | `chmod 755 /usr/local/bin/` | 恢复二进制目录权限 |
| 2 | `chmod 755 /etc/systemd/system/` | 恢复 systemd 目录权限 |
## 二、Kubeadm Plugin 规格
### 插件定义 — [kubeadm.go:49-62](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L49-L62)
```go
type KubeadmPlugin struct {
    k8sClient      client.Client
    localK8sClient *kubernetes.Clientset
    exec           exec.Executor
    boot           *mfutil.BootScope
    isManager      bool
    clusterName    string
    controlPlaneEndpoint string
    GableNameSpace string
}
```
### 参数规格（Param）— [kubeadm.go:74-112](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L74-L112)
| 参数名 | 必填 | 默认值 | 可选值 | 说明 |
|--------|------|--------|--------|------|
| `phase` | ✅ | `initControlPlane` | `initControlPlane`, `joinControlPlane`, `joinWorker`, `upgradeControlPlane`, `upgradeWorker`, `upgradeEtcd` | 执行阶段 |
| `bkeConfig` | ❌ | `""` | `NameSpace:Name` | BKEConfig ConfigMap 引用 |
| `backUpEtcd` | ❌ | `false` | `true`, `false` | 升级前是否备份 etcd |
| `clusterType` | ❌ | `bke` | `konk`, `bocloud` | 集群类型 |
| `etcdVersion` | ❌ | `""` | 如 `3.6.4` | etcd 版本号（仅 etcd 升级用） |
### Phase 路由 — [kubeadm.go:117-147](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L117-L147)
```
phase=initControlPlane  → k.initControlPlane()
phase=joinControlPlane  → k.joinControlPlane()
phase=joinWorker        → k.joinWorker()
phase=upgradeControlPlane → k.upgradeControlPlane(backUpEtcd, clusterType)
phase=upgradeWorker     → k.upgradeWorker()
phase=upgradeEtcd       → k.upgradeEtcd(backUpEtcd, clusterType)
```
## 三、各 Phase 执行的子插件及 Shell 命令
### Phase 1: `initControlPlane` — [kubeadm.go:169-196](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L169-L196)
| 步骤 | 子插件 | 说明 | 关键 Shell 命令 |
|------|--------|------|-----------------|
| 1 | `Downloader` | 安装 kubectl 二进制 | 下载 `kubectl-{version}-{arch}` → `/usr/bin/kubectl`，`chmod 755` |
| 2 | `Cert` | 初始化控制面证书 | 从管理集群 Secret 加载证书，生成 TLS 证书和 kubeconfig |
| 3 | `Manifests` | 生成静态 Pod YAML | 渲染 `kube-apiserver`, `kube-controller-manager`, `kube-scheduler`, `etcd` 的 static pod manifest |
| 4 | `RunKubelet` | 安装并启动 kubelet | 见下方详细命令 |
| 5 | — | 上传 kubelet 配置到管理集群 | 创建 ConfigMap |
| 6 | — | 上传全局 CA（仅管理集群） | — |
### Phase 2: `joinControlPlane` — [kubeadm.go:200-226](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L200-L226)
| 步骤 | 子插件 | 说明 |
|------|--------|------|
| 1 | `Downloader` | 安装 kubectl |
| 2 | `Cert` | 加载控制面证书（`loadTargetClusterCert=true`, `tlsScope=tls-server`） |
| 3 | `RunKubelet` | 安装并启动 kubelet |
| 4 | `Manifests` | 生成 static pod manifest |
| 5 | — | 上传全局 CA（仅管理集群） |
### Phase 3: `joinWorker` — [kubeadm.go:227-246](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L227-L246)
| 步骤 | 子插件 | 说明 |
|------|--------|------|
| 1 | `Cert` | 加载 CA 证书 + 生成 kubelet/kube-proxy kubeconfig（`loadCACert=true`, `caCertNames=ca,proxy`） |
| 2 | `RunKubelet` | 安装并启动 kubelet |
| 3 | `Downloader` | 安装 kubectl |
### Phase 4: `upgradeControlPlane` — [kubeadm.go:278-338](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L278-L338)
| 步骤 | 子插件 | 说明 |
|------|--------|------|
| 1 | `Backup` | 备份 etcd（可选）+ 备份 `/etc/kubernetes` |
| 2 | `K8sEnvInit` | 预拉取镜像（`scope=image`） |
| 3 | `Manifests` | 逐个升级组件 static pod（`check=true`） |
| 4 | `RunKubelet` | 升级 kubelet |
| 5 | `Downloader` | 升级 kubectl |
### Phase 5: `upgradeWorker` — [kubeadm.go:339-352](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L339-L352)
| 步骤 | 子插件 | 说明 |
|------|--------|------|
| 1 | `RunKubelet` | 升级 kubelet |
| 2 | `Downloader` | 升级 kubectl |
### Phase 6: `upgradeEtcd` — [kubeadm.go:353-390](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L353-L390)
| 步骤 | 子插件 | 说明 |
|------|--------|------|
| 1 | `Backup` | 备份 etcd + `/etc/kubernetes` |
| 2 | `K8sEnvInit` | 预拉取镜像 |
| 3 | `Manifests` | 升级 etcd static pod |
## 四、子插件执行的具体 Shell 命令
### 4.1 Cert Plugin — [certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/certs/certs.go)
Cert Plugin 主要操作文件系统（读写证书、kubeconfig），**不直接执行 shell 命令**。核心操作：
- 从 K8s Secret 读取证书 → 写入本地 `/etc/kubernetes/pki/`
- 生成 TLS 证书和 kubeconfig 文件
- 复制 admin kubeconfig 到用户 home 目录
### 4.2 Manifests Plugin — [manifests.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/manifests/manifests.go)
| Shell 命令 | 用途 |
|-----------|------|
| `mkdir -p -m 700 {etcdDataDir}` | 创建 etcd 数据目录 |
| `id etcd` | 检查 etcd 用户是否存在 |
| `useradd -r -c "etcd user" -s /sbin/nologin etcd -d {etcdDataDir}` | 创建 etcd 用户 |
| `chown -R etcd:etcd {etcdDataDir}` | 设置 etcd 数据目录属主 |
| `if [ -f {kubeletServicePath} ]; then systemctl restart kubelet; fi` | 重启 kubelet 以拉起 static pod |
### 4.3 RunKubelet Plugin — [run.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubelet/run.go)
| Shell 命令 | 用途 |
|-----------|------|
| 下载 `kubelet-{version}-{arch}` → `/usr/bin/kubelet`，`chmod 755` | 安装 kubelet 二进制 |
| `systemctl daemon-reload && systemctl enable kubelet` | 重载 systemd 并设置开机自启 |
| `systemctl restart kubelet` | 启动 kubelet |
### 4.4 K8sEnvInit Plugin — [env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/env.go) + [init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go)
#### kernel scope
| Shell 命令 | 用途 |
|-----------|------|
| `modprobe {module}` | 加载内核模块（`ip_vs`, `ip_vs_wrr`, `ip_vs_rr`, `ip_vs_sh`, `fuse`, `rbd`, `br_netfilter`, `nf_conntrack` 等） |
| `echo '{module}' >> /etc/modules` | Ubuntu 持久化模块加载 |
| 写入 `/etc/sysctl.d/k8s.conf` | 配置内核参数（`net.ipv4.ip_forward=1`, `vm.max_map_count=262144` 等） |
| `sudo sysctl -p /etc/sysctl.d/k8s.conf` | 应用内核参数 |
| `echo '* hard nofile 65536' >> /etc/security/limits.conf` | 设置文件描述符限制 |
| `echo '* soft nofile 65536' >> /etc/security/limits.conf` | 设置文件描述符限制 |
#### swap scope
| Shell 命令 | 用途 |
|-----------|------|
| `sudo sed -ri 's/.*swap.*/#&/' /etc/fstab` | 注释 fstab 中的 swap 行 |
| `sudo swapoff -a` | 立即禁用所有 swap |
| 写入 `vm.swappiness=0` 到 `/etc/sysctl.d/k8s-swap.conf` | 持久化禁用 swap |
#### firewall scope
| Shell 命令 | 用途 |
|-----------|------|
| `systemctl stop firewalld` | 停止 firewalld |
| `systemctl disable firewalld` | 禁用 firewalld |
| `systemctl stop ufw` | 停止 ufw（Ubuntu） |
| `systemctl disable ufw` | 禁用 ufw |
#### selinux scope
| Shell 命令 | 用途 |
|-----------|------|
| `sudo setenforce 0` | 临时关闭 SELinux |
| 替换 `/etc/selinux/config` 中 `SELINUX=disabled` | 永久关闭 SELinux |
#### time scope
| Shell 命令 | 用途 |
|-----------|------|
| `sudo ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime` | 设置时区为上海 |
#### hosts scope
| Shell 命令 | 用途 |
|-----------|------|
| `sudo hostnamectl set-hostname {name}` | 设置主机名（方法1） |
| `sudo hostname {name}` | 设置主机名（方法2） |
| `sudo echo HOSTNAME={name} >> /etc/sysconfig/network` | 设置主机名（方法3） |
| `sudo sed -i 's/HOSTNAME=.*/HOSTNAME={name}/g' /etc/sysconfig/network` | 修改主机名（方法3备选） |
| `sudo echo {name} > /proc/sys/kernel/hostname` | 设置主机名（方法4） |
| `sudo echo {name} > /etc/hostname` | 设置主机名（方法5） |
| 写入 `/etc/hosts` | 配置集群节点 hosts 映射 |
#### runtime scope
| Shell 命令 | 用途 |
|-----------|------|
| `systemctl daemon-reload` | 重载 systemd |
| `systemctl reload {containerd\|docker}` | 重载容器运行时 |
#### image scope
| 操作 | 用途 |
|------|------|
| 通过 containerd/docker 客户端拉取镜像 | 预拉取 Kubernetes 组件镜像 |
#### registry scope
| Shell 命令 | 用途 |
|-----------|------|
| 配置 containerd/docker 的 insecure-registries | 允许 HTTP 拉取私有仓库镜像 |
#### iptables scope
| 操作 | 用途 |
|------|------|
| 配置 iptables 规则 | 设置网络转发规则 |
#### extra scope
| Shell 命令 | 用途 |
|-----------|------|
| `umask 0022` | 设置文件默认权限 |
## 五、整体调用关系图
```
ensure_bke_agent.go (Controller 侧, SSH 推送)
  │
  ├── PreCommand: chmod / systemctl stop/disable / rm -rf
  ├── HostCustomCmdFunc: echo hostname / 上传 bkeagent 二进制
  ├── StartCommand: mkdir / mv / chmod / echo kubeconfig / systemctl enable+restart
  └── PostCommand: chmod 755 恢复权限
        │
        ▼
BKE Agent 启动后，在远端节点本地执行 Kubeadm Plugin
  │
  ├── initControlPlane
  │     ├── Downloader → 下载 kubectl
  │     ├── Cert → 加载/生成证书 + kubeconfig
  │     ├── Manifests → 生成 static pod + 创建 etcd 用户/目录 + restart kubelet
  │     └── RunKubelet → 下载 kubelet + 生成 service/config + systemctl enable+restart
  │
  ├── joinControlPlane
  │     ├── Downloader → 下载 kubectl
  │     ├── Cert → 加载证书
  │     ├── RunKubelet → 安装 kubelet
  │     └── Manifests → 生成 static pod
  │
  ├── joinWorker
  │     ├── Cert → 加载 CA + 生成 kubeconfig
  │     ├── RunKubelet → 安装 kubelet
  │     └── Downloader → 下载 kubectl
  │
  ├── upgradeControlPlane
  │     ├── Backup → 备份 etcd + /etc/kubernetes
  │     ├── K8sEnvInit → 预拉取镜像
  │     ├── Manifests → 逐个升级组件 (apiserver→controller-manager→scheduler)
  │     ├── RunKubelet → 升级 kubelet
  │     └── Downloader → 升级 kubectl
  │
  ├── upgradeWorker
  │     ├── RunKubelet → 升级 kubelet
  │     └── Downloader → 升级 kubectl
  │
  └── upgradeEtcd
        ├── Backup → 备份 etcd
        ├── K8sEnvInit → 预拉取镜像
        └── Manifests → 升级 etcd static pod
```
## 六、关键路径常量
| 常量 | 值 | 来源 |
|------|----|----|
| 证书目录 | `/etc/openFuyao/certs` | [ensure_bke_agent.go:45](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L45) |
| 信任链 | `/etc/openFuyao/certs/trust-chain.crt` | [ensure_bke_agent.go:47](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L47) |
| CSR 配置目录 | `/etc/openFuyao/certs/cert_config` | [ensure_bke_agent.go:49](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L49) |
| Agent 配置 | `/etc/openFuyao/bkeagent/config` | [ensure_bke_agent.go:590](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L590) |
| Agent 二进制 | `/usr/local/bin/bkeagent` | [ensure_bke_agent.go:587](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L587) |
| K8s PKI 目录 | `/etc/kubernetes/pki` | pkiutil 默认值 |
| K8s 配置目录 | `/etc/kubernetes` | pkiutil 默认值 |
| Kubelet 配置 | `/var/lib/kubelet/config.yaml` | kubelet 默认值 |
| Sysctl 配置 | `/etc/sysctl.d/k8s.conf` | [env.go:73](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/env.go#L73) |
        
