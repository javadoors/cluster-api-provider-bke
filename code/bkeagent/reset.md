# Reset Plugin 的规格和执行流程
## Reset Plugin 规格及执行 Shell 命令梳理
### 1. Reset Plugin 概述
Reset Plugin 是 BKEAgent 内置的 Job 插件，运行在目标节点上，用于**重置/清理节点上的 Kubernetes 集群组件**。它定义在 [reset.go](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/reset.go) 中，实现 `plugin.Plugin` 接口。
### 2. 插件规格
| 属性 | 值 |
|------|-----|
| **插件名** | `Reset` |
| **触发方式** | 通过 BKEAgent 的 `CommandBuiltIn` 类型命令触发 |
| **执行位置** | 目标节点本地（由 BKEAgent 进程执行） |
#### 参数定义
| 参数 | Key | 必填 | 默认值 | 说明 |
|------|-----|------|--------|------|
| `bkeConfig` | `bkeConfig` | 否 | `""` | BKECluster 配置引用，格式 `namespace:name` |
| `scope` | `scope` | 否 | `cert,manifests,containerd-cfg,container,kubelet,containerRuntime,source,extra` | 清理范围 |
| `extra` | `extra` | 否 | `""` | 额外清理的文件/目录/IP，逗号分隔 |
#### scope 可选值
| scope 名称 | 说明 |
|------------|------|
| `cert` | 清理集群证书目录 |
| `manifests` | 清理 kubelet manifests 目录、HAProxy/Keepalived 配置 |
| `containerd-cfg` | 清理 containerd 配置文件和二进制 |
| `container` | 清理所有容器（Pod） |
| `kubelet` | 清理 kubelet 服务、二进制、配置目录 |
| `containerRuntime` | 清理容器运行时（Docker 或 Containerd） |
| `source` | 重置软件源 |
| `extra` | 额外清理（iptables、calico、kubectl 等） |
| `global-cert` | 清理全局证书目录 `/etc/openFuyao/certs` |
### 3. 调用链路
```
BKECluster.Spec.Reset = true
  └── EnsureDeleteOrReset Phase (ensure_delete_or_reset.go)
       └── Reset Command (pkg/command/cleannode.go)
            └── 生成 agentv1beta1.Command CR:
                 spec.commands[0].command = ["Reset", "bkeConfig=ns:name", "scope=...", "extra=..."]
                 spec.commands[0].type = CommandBuiltIn
            └── BKEAgent 接收 Command → 调用 ResetPlugin.Execute()
                 └── executeCleanPhases() → 按顺序执行各 CleanPhase
```
### 4. 各 CleanPhase 执行的具体 Shell 命令
#### 4.1 `kubelet` Phase — [KubeletCleanBin](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L79)
| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 停止 kubelet 服务 | `systemctl stop kubelet && systemctl disable kubelet` | 停止并禁用 kubelet systemd 服务 |
| 卸载 kubelet 挂载点 | `for m in $(sudo tac /proc/mounts \| sudo awk '{print $2}'\|sudo grep /var/lib/kubelet);do sudo umount $m\|\|true; done` | 递归卸载 kubelet 目录下的所有挂载点 |
| 清理文件/目录 | `/var/lib/kubelet` (目录), `/usr/local/bin/kubelet` (文件), kubelet service 文件, `/etc/cni/net.d/10-calico.conflist`, kubelet 预创建目录 | 删除 kubelet 相关文件和目录 |
#### 4.2 `containerd-cfg` Phase — [ContainerdCfgClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L268)
| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 停止 containerd | `systemctl stop containerd` | 停止 containerd 服务 |
| 禁用 containerd | `systemctl disable containerd` | 禁用 containerd 开机自启 |
| 清理文件 | `/usr/bin/containerd`, `/usr/bin/containerd-stress`, `/usr/bin/containerd-shim-shimless-v2`, `/usr/bin/containerd-shim-runc-v2`, `/usr/bin/crictl`, `/etc/crictl.yaml`, `/usr/bin/ctr`, `/usr/bin/nerdctl`, `/usr/lib/systemd/system/containerd.service`, `/usr/local/sbin/runc` | 删除 containerd 相关二进制和配置 |
| 清理目录 | `/usr/local/beyondvm`, `/etc/containerd/` | 删除 containerd 数据和配置目录 |
#### 4.3 `container` Phase — [ContainerClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L294)
**Containerd 运行时：**

| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 列出所有 Pod | `crictl pods -q` | 获取所有 Pod ID |
| 停止 Pod | `crictl stopp <pod_id>` | 停止单个 Pod |
| 删除 Pod | `crictl rmp <pod_id>` | 删除单个 Pod |
| 强制删除 Pod | `crictl rmp -f <pod_id>` | 停止/删除失败时强制删除 |

**Docker 运行时：**

| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 列出 k8s 容器 | `docker ps -a --filter name=k8s_ -q \| grep -v kubelet` | 获取非 kubelet 的 k8s 容器 |
| 停止容器 | `docker stop <container_id>` | 停止单个容器 |
| 删除容器 | `docker rm --volumes <container_id>` | 删除单个容器 |
| 强制删除容器 | `docker rm -f --volumes <container_id>` | 停止/删除失败时强制删除 |
#### 4.4 `containerRuntime` Phase — [ContainerRuntimeClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L327)
**AllInOne 模式跳过此 Phase。**

**Docker 运行时清理：**

| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 列出所有容器 | `docker ps -a -q` | 获取所有容器 ID |
| 强制删除所有容器 | `docker rm -f --volumes <id>` | 逐个强制删除 |
| 清理 Docker 数据 | `docker system prune -a -f --volumes` | 清理所有 Docker 镜像、容器、卷 |
| 停止 Docker | `systemctl stop docker` | 停止 Docker 服务 |
| 禁用 Docker | `systemctl disable docker` | 禁用 Docker 开机自启 |
| 移除 Docker 软件源 | `httprepo.RepoRemove("docker*", "containerd.io")` | 移除 Docker RPM/DEB 源 |
| K8s ≥ 1.24 额外清理 | `systemctl stop cri-dockerd && systemctl stop cri-dockerd.socket` | 停止 cri-dockerd |
| | `systemctl disable cri-dockerd && systemctl disable cri-dockerd.socket` | 禁用 cri-dockerd |
| 清理目录 | Docker data-root, `/etc/docker`, `/var/lib/cni`, `/etc/cni`, `/opt/cni` | 删除 Docker 和 CNI 数据 |
| 清理文件 | `/usr/bin/cri-dockerd`, `/etc/systemd/system/cri-dockerd.service`, `/etc/systemd/system/cri-dockerd.socket` | 删除 cri-dockerd 相关文件 |

**Containerd 运行时清理：**

| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 强制删除 crictl Pod | `crictl rmp -f <pod_id>` | 强制删除所有 crictl 管理的 Pod |
| 强制删除 nerdctl 容器 | `nerdctl rm -f --volumes <id>` | 强制删除所有 nerdctl 容器 |
| 停止 containerd | `systemctl stop containerd` | 停止 containerd 服务 |
| 禁用 containerd | `systemctl disable containerd` | 禁用 containerd 开机自启 |
| 清理文件 | `/usr/bin/containerd`, `/usr/bin/containerd-shim`, `/usr/bin/containerd-shim-runc-v2`, `/usr/bin/crictl`, `/etc/crictl.yaml`, `/usr/bin/ctr`, `/usr/bin/nerdctl`, `/usr/bin/containerd-stress`, `/usr/lib/systemd/system/containerd.service`, `/usr/local/sbin/runc` | 删除 containerd 相关二进制 |
| 清理目录 | `/usr/local/beyondvm`, `/etc/containerd/`, containerd data-root, `/etc/systemd/system/containerd.service.d`, `/var/lib/cni`, `/etc/cni`, `/opt/cni`, `/var/lib/nerdctl`, `/etc/docker/certs.d` | 删除 containerd 和 CNI 数据 |
#### 4.5 `cert` Phase — [CertClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L60)
| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 清理目录 | PKI 证书目录（默认 `/etc/kubernetes/pki`）, BKECluster 配置的 CertificatesDir | 删除集群证书 |
#### 4.6 `manifests` Phase — [ManifestsClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L437)
| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 清理目录 | HAProxy 配置路径, Keepalived 配置路径, kubelet manifests 目录 | 删除静态 Pod manifests 和 LB 配置 |
| 清理文件 | Audit Policy 文件 | 删除审计策略文件 |
#### 4.7 `source` Phase — [SourceClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L431)
| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 重置软件源 | `source.ResetSource()` → 内部调用系统包管理器移除自定义源 | 恢复系统默认软件源 |
| 更新源缓存 | `httprepo.RepoUpdate()` | 更新软件源缓存 |
#### 4.8 `extra` Phase — [ExtraToClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L447)
| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 清理文件 | `/usr/bin/calicoctl` | 删除 calicoctl 二进制 |
| 清理目录 | `/etc/calico` | 删除 calico 配置 |
| 清理 iptables（非 AllInOne） | `iptables -F -t raw && iptables -F -t filter && iptables -t nat -F && iptables -t mangle -F && iptables -X -t nat && iptables -X -t raw && iptables -X -t mangle && iptables -X -t filter` | 清空所有 iptables 规则 |
| 清理目录（非 AllInOne） | `/etc/openFuyao/addons` | 删除 addon 配置 |
| 清理文件（非 AllInOne） | `/usr/bin/kubectl` | 删除 kubectl 二进制 |
| 清理 IP 地址 | `ip addr del <addr> dev <interface>` | 删除 extra 参数中指定的 IP 地址 |
| 清理额外文件/目录 | 由 `extra` 参数指定 | 删除用户指定的额外路径 |
#### 4.9 `global-cert` Phase — [GlobalCertClean](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/clean.go#L72)
| 步骤 | Shell 命令 | 说明 |
|------|-----------|------|
| 清理目录 | `/etc/openFuyao/certs` | 删除全局证书目录 |
### 5. 执行顺序
Reset Plugin 按 [DefaultCleanPhases()](file:///D:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/cleanphases.go#L124) 定义的顺序执行：
```
1. kubelet           → 停止 kubelet，卸载挂载点，清理二进制和配置
2. containerd-cfg    → 停止 containerd，清理 containerd 配置和二进制
3. container         → 停止并删除所有容器
4. containerRuntime  → 彻底清理容器运行时（Docker/Containerd）
5. cert              → 清理集群 PKI 证书
6. manifests         → 清理静态 Pod manifests 和 LB 配置
7. source            → 重置系统软件源
8. extra             → 清理额外文件、iptables、IP 地址
9. global-cert       → 清理全局证书目录
```
### 6. 与 EnsureDeleteOrReset Phase 的关系
[EnsureDeleteOrReset](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_delete_or_reset.go) Phase 在 `BKECluster.Spec.Reset=true` 或集群删除时触发，它通过 [Reset Command](file:///D:/code/github/cluster-api-provider-bke/pkg/command/cleannode.go) 下发 `CommandBuiltIn` 类型的命令到 BKEAgent，BKEAgent 收到后调用 `ResetPlugin.Execute()` 执行上述清理流程。命令格式为：
```go
// cleannode.go 中的命令构造
commandSpec.Commands = []agentv1beta1.ExecCommand{
    {
        ID: "reset",
        Command: []string{
            "Reset",                                          // 插件名
            "bkeConfig=namespace:bkecluster-name",            // BKECluster 引用
            "scope=cert,manifests,kubelet,container,containerRuntime,source,extra,global-cert", // 清理范围
            "extra=/path/to/clean,192.168.1.100",             // 额外清理项
        },
        Type:          agentv1beta1.CommandBuiltIn,
        BackoffIgnore: false,
    },
}
```
        
