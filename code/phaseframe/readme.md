# 设计一个全面的表格，汇总集群安装部署的各组件信息。
## 集群安装部署组件汇总表
### 一、Phase 阶段执行顺序
根据 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go)，集群安装分为以下阶段组：

| 阶段组 | 阶段名称 | 中文名称 | 触发条件 |
|-------|---------|---------|---------|
| **CommonPhases** | EnsureFinalizer | 部署任务创建 | 始终执行 |
| | EnsurePaused | 集群管理暂停 | `spec.pause=true` |
| | EnsureClusterManage | 纳管现有集群 | BOCLOUD 类型集群且未完全纳管 |
| | EnsureDeleteOrReset | 集群删除 | `spec.reset=true` 或资源删除 |
| | EnsureDryRun | DryRun部署 | `spec.dryRun=true` |
| **DeployPhases** | EnsureBKEAgent | 推送Agent | 新节点需要推送 Agent |
| | EnsureNodesEnv | 节点环境准备 | 新节点需要初始化环境 |
| | EnsureClusterAPIObj | ClusterAPI对接 | 集群未 Ready |
| | EnsureCerts | 集群证书创建 | 集群未 Ready |
| | EnsureLoadBalance | 集群入口配置 | VIP 不在节点 IP 列表中 |
| | EnsureMasterInit | Master初始化 | 第一个 Master 节点初始化 |
| | EnsureMasterJoin | Master加入 | 新 Master 节点加入 |
| | EnsureWorkerJoin | Worker加入 | 新 Worker 节点加入 |
| | EnsureAddonDeploy | 集群组件部署 | Addon 列表变更 |
| | EnsureNodesPostProcess | 后置脚本处理 | 节点有后置脚本配置 |
| | EnsureAgentSwitch | Agent监听切换 | Addon 部署完成 |
| **PostDeployPhases** | EnsureProviderSelfUpgrade | provider自升级 | Provider 版本变更 |
| | EnsureAgentUpgrade | Agent升级 | Agent 版本变更 |
| | EnsureContainerdUpgrade | Containerd升级 | Containerd 版本变更 |
| | EnsureEtcdUpgrade | Etcd升级 | Etcd 版本变更 |
| | EnsureWorkerUpgrade | Worker升级 | Worker 节点 K8s 版本变更 |
| | EnsureMasterUpgrade | Master升级 | Master 节点 K8s 版本变更 |
| | EnsureComponentUpgrade | openFuyao核心组件升级 | openFuyao 版本变更 |
| | EnsureWorkerDelete | Worker删除 | Worker 节点删除 |
| | EnsureMasterDelete | Master删除 | Master 节点删除 |
| | EnsureCluster | 集群健康检查 | 始终执行 |
### 二、各阶段安装组件详情
#### 1. EnsureBKEAgent（推送Agent）
| 项目 | 内容 |
|-----|------|
| **安装组件** | BKEAgent 二进制 + systemd 服务 |
| **组件来源** | 管理集群 ConfigMap (`bkeagent-deployer`) |
| **安装命令** | SSH 远程执行：下载二进制 → 创建 systemd 服务 → 启动服务 |
| **安装节点** | 所有节点（Master + Worker + Etcd） |
| **依赖** | SSH 访问权限、节点网络可达 |
| **关键文件** | `/usr/local/bin/bkeagent`<br>`/etc/systemd/system/bkeagent.service`<br>`/etc/openFuyao/certs/trust-chain.crt` |
| **配置参数** | `agentHealthPort`（健康端口）<br>`localKubeConfig`（管理集群 kubeconfig） |
#### 2. EnsureNodesEnv（节点环境准备）
| 项目 | 内容 |
|-----|------|
| **安装组件** | 内核参数、防火墙、SELinux、Swap、时间同步、Hosts、运行时、镜像仓库、端口检查 |
| **组件来源** | BKEAgent 内置 EnvPlugin |
| **安装命令** | BKEAgent 执行 `K8sEnvInit` 插件 |
| **安装节点** | 所有节点 |
| **依赖** | BKEAgent 已就绪 |
| **关键配置** | `/etc/sysctl.d/k8s.conf`（内核参数）<br>`/etc/hosts`（主机名解析）<br>`/etc/selinux/config`（SELinux）<br>`/etc/security/limits.conf`（文件句柄） |
| **额外脚本** | `file-downloader.sh`（通用）<br>`package-downloader.sh`（通用）<br>`install-lxcfs.sh`（可选）<br>`install-nfsutils.sh`（可选）<br>`install-etcdctl.sh`（可选）<br>`install-helm.sh`（可选）<br>`install-calicoctl.sh`（可选）<br>`update-runc.sh`（Docker 运行时） |
#### 3. EnsureClusterAPIObj（ClusterAPI对接）
| 项目 | 内容 |
|-----|------|
| **安装组件** | CAPI 资源：Cluster、KubeadmControlPlane、BKEMachineTemplate |
| **组件来源** | 动态生成 YAML |
| **安装命令** | `kubectl apply -f`（通过 controller-runtime client） |
| **安装节点** | 管理集群 |
| **依赖** | CAPI 已安装（cert-manager + cluster-api + kubeadm-bootstrap + kubeadm-control-plane） |
| **关键资源** | `Cluster.cluster.x-k8s.io`<br>`KubeadmControlPlane.controlplane.cluster.x-k8s.io`<br>`BKEMachineTemplate.bke.bocloud.com` |
#### 4. EnsureCerts（集群证书创建）
| 项目 | 内容 |
|-----|------|
| **安装组件** | CA 证书 + 组件证书（etcd、apiserver、kubelet、admin 等） |
| **组件来源** | 动态生成（使用 `k8s.io/crypto`） |
| **安装命令** | 证书生成 → 上传到管理集群 Secret → 分发到节点 |
| **安装节点** | 管理集群（存储）+ 所有节点（使用） |
| **依赖** | 无 |
| **关键证书** | `ca.crt/ca.key`（根 CA）<br>`etcd/ca.crt`（etcd CA）<br>`front-proxy-ca.crt`（前端代理 CA）<br>`apiserver-etcd-client.crt`<br>`apiserver-kubelet-client.crt`<br>`admin.conf`（管理员 kubeconfig） |
#### 5. EnsureLoadBalance（集群入口配置）
| 项目 | 内容 |
|-----|------|
| **安装组件** | HAProxy + Keepalived（Static Pod 形式） |
| **组件来源** | 镜像仓库（`thirdImageRepo`、`fuyaoImageRepo`） |
| **安装命令** | 生成 Static Pod YAML → 写入 `/etc/kubernetes/manifests/` |
| **安装节点** | 所有 Master 节点 |
| **依赖** | 节点环境已初始化、VIP 可用 |
| **关键文件** | `/etc/kubernetes/manifests/haproxy.yaml`<br>`/etc/kubernetes/manifests/keepalived.yaml` |
| **配置参数** | `ControlPlaneEndpointVIP`（VIP）<br>`ControlPlaneEndpointPort`（端口）<br>`VirtualRouterId`（VRRP ID） |
#### 6. EnsureMasterInit（Master初始化）
| 项目 | 内容 |
|-----|------|
| **安装组件** | etcd + kube-apiserver + kube-controller-manager + kube-scheduler + kubelet |
| **组件来源** | 镜像仓库 + HTTP 文件仓库（kubeadm/kubectl 二进制） |
| **安装命令** | Kubeadm 插件执行 `initControlPlane` 阶段 |
| **安装节点** | 第一个 Master 节点 |
| **依赖** | 证书已生成、负载均衡已配置、节点环境已初始化 |
| **关键文件** | `/etc/kubernetes/admin.conf`<br>`/etc/kubernetes/manifests/etcd.yaml`<br>`/etc/kubernetes/manifests/kube-apiserver.yaml`<br>`/etc/kubernetes/manifests/kube-controller-manager.yaml`<br>`/etc/kubernetes/manifests/kube-scheduler.yaml`<br>`/var/lib/kubelet/config.yaml` |
| **Kubeadm 阶段** | ① Preflight ② Certs ③ Kubeconfig ④ Kubelet-start ⑤ Control-plane ⑥ Etcd ⑦ Upload-config ⑧ Mark-control-plane ⑨ Bootstrap-token |
#### 7. EnsureMasterJoin（Master加入）
| 项目 | 内容 |
|-----|------|
| **安装组件** | etcd + kube-apiserver + kube-controller-manager + kube-scheduler + kubelet |
| **组件来源** | 镜像仓库 + HTTP 文件仓库 |
| **安装命令** | Kubeadm 插件执行 `joinControlPlane` 阶段 |
| **安装节点** | 新加入的 Master 节点 |
| **依赖** | 第一个 Master 已初始化、证书已分发 |
| **关键文件** | 同 MasterInit |
| **Kubeadm 阶段** | ① Preflight ② Certs ③ Kubelet-start ④ Control-plane ⑤ Etcd |
#### 8. EnsureWorkerJoin（Worker加入）
| 项目 | 内容 |
|-----|------|
| **安装组件** | kubelet |
| **组件来源** | 镜像仓库 + HTTP 文件仓库 |
| **安装命令** | Kubeadm 插件执行 `joinWorker` 阶段 |
| **安装节点** | Worker 节点 |
| **依赖** | Master 节点已就绪、Bootstrap Token 有效 |
| **关键文件** | `/etc/kubernetes/kubelet.conf`<br>`/var/lib/kubelet/config.yaml` |
| **Kubeadm 阶段** | ① Preflight ② Kubelet-start |
#### 9. EnsureAddonDeploy（集群组件部署）
| 项目 | 内容 |
|-----|------|
| **安装组件** | kubeproxy、calico/coredns、nfs-csi、bocoperator、cluster-api 等 Addon |
| **组件来源** | `/manifests/kubernetes/<addon>/<version>/*.yaml` 或 Helm Chart 仓库 |
| **安装命令** | YAML 类型：`kubectl apply -f`（Server-Side Apply）<br>Chart 类型：`helm install/upgrade` |
| **安装节点** | 目标集群（Kubernetes API） |
| **依赖** | 集群 API Server 可访问 |
| **默认 Addon** | kubeproxy、calico、coredns、nfs-csi、bocoperator、cluster-api |
| **特殊处理** | etcdbackup：创建备份目录 + etcd 证书 Secret<br>beyondELB：创建 VIP + 节点标签<br>cluster-api：创建 kubeconfig Secret + 标记 Agent 切换<br>openfuyao-system-controller：添加 control-plane 标签 + 下发 patch CM<br>gpu-manager：重建 scheduler static pod |
#### 10. EnsureNodesPostProcess（后置脚本处理）
| 项目 | 内容 |
|-----|------|
| **安装组件** | 用户自定义后置脚本 |
| **组件来源** | ConfigMap（`preprocess-all-config` 或 `preprocess-config-batch-*`） |
| **安装命令** | BKEAgent 执行 `Preprocess` 内置命令 |
| **安装节点** | 有后置脚本配置的节点 |
| **依赖** | 节点环境已初始化 |
| **配置来源** | 全局配置：`user-system/preprocess-all-config`<br>批次配置：`user-system/preprocess-config-batch-<batchId>` |
#### 11. EnsureAgentSwitch（Agent监听切换）
| 项目 | 内容 |
|-----|------|
| **安装组件** | 更新 BKEAgent 配置，切换监听目标集群 |
| **组件来源** | 动态生成 |
| **安装命令** | 创建 Switch Command → 等待 Agent 重启并连接目标集群 |
| **安装节点** | 所有节点 |
| **依赖** | Addon 部署完成、目标集群 API 可访问 |
| **关键变更** | kubeconfig 从管理集群切换到目标集群 |
### 三、节点上安装的核心组件
| 组件 | 版本来源 | 安装位置 | 安装方式 | 依赖 |
|-----|---------|---------|---------|-----|
| **BKEAgent** | ConfigMap | `/usr/local/bin/bkeagent` | SSH 下载 + systemd | 无 |
| **Containerd/Docker** | HTTP 文件仓库 | `/usr/bin/containerd` 或 `/usr/bin/docker` | EnvPlugin | 无 |
| **Kubelet** | HTTP 文件仓库 | `/usr/bin/kubelet` | Kubeadm 插件 | 运行时已安装 |
| **Kubeadm** | HTTP 文件仓库 | `/usr/bin/kubeadm` | Kubeadm 插件（仅用于生成配置） | 无 |
| **Kubectl** | HTTP 文件仓库 | `/usr/bin/kubectl` | Kubeadm 插件 | 无 |
| **HAProxy** | 镜像仓库 | Static Pod | EnsureLoadBalance | 无 |
| **Keepalived** | 镜像仓库 | Static Pod | EnsureLoadBalance | 无 |
| **etcd** | 镜像仓库 | Static Pod | Kubeadm 插件 | 证书 |
| **kube-apiserver** | 镜像仓库 | Static Pod | Kubeadm 插件 | 证书、etcd |
| **kube-controller-manager** | 镜像仓库 | Static Pod | Kubeadm 插件 | 证书 |
| **kube-scheduler** | 镜像仓库 | Static Pod | Kubeadm 插件 | 证书 |
### 四、镜像仓库配置
| 配置项 | 说明 | 示例 |
|-------|------|-----|
| `imageRepo.domain` | 镜像仓库域名 | `registry.bocloud.com` |
| `imageRepo.port` | 镜像仓库端口 | `443` |
| `imageRepo.prefix` | 镜像前缀 | `library` |
| `httpRepo.domain` | HTTP 文件仓库域名 | `files.bocloud.com` |
| `httpRepo.port` | HTTP 文件仓库端口 | `8080` |
| `chartRepo.url` | Helm Chart 仓库地址 | `oci://registry.bocloud.com/charts` |
### 五、关键配置文件路径
| 文件路径 | 用途 | 阶段 |
|---------|-----|-----|
| `/etc/kubernetes/admin.conf` | 管理员 kubeconfig | MasterInit/Join |
| `/etc/kubernetes/kubelet.conf` | Kubelet kubeconfig | WorkerJoin |
| `/etc/kubernetes/manifests/*.yaml` | Static Pod 清单 | MasterInit/Join/LoadBalance |
| `/etc/kubernetes/pki/*.crt` | 证书文件 | EnsureCerts |
| `/var/lib/kubelet/config.yaml` | Kubelet 配置 | Kubeadm 插件 |
| `/etc/sysctl.d/k8s.conf` | 内核参数 | EnsureNodesEnv |
| `/etc/hosts` | 主机名解析 | EnsureNodesEnv |
| `/etc/openFuyao/certs/trust-chain.crt` | 信任证书链 | EnsureBKEAgent |
| `/usr/local/bin/bkeagent` | Agent 二进制 | EnsureBKEAgent |
| `/etc/systemd/system/bkeagent.service` | Agent 服务 | EnsureBKEAgent |
### 六、重构注意事项
#### 1. 状态码依赖
节点状态使用位标志，重构时需保持一致：
```go
const (
    NodeAgentPushedFlag  NodeStateCode = 1 << iota  // Agent 已推送
    NodeAgentReadyFlag                               // Agent 就绪
    NodeEnvFlag                                      // 环境已初始化
    NodeHAFlag                                       // HA 已配置
    NodeInitFlag                                     // 节点已初始化
    NodeFailedFlag                                   // 节点失败
    NodeDeletingFlag                                 // 节点删除中
)
```
#### 2. 阶段状态机
集群状态转换必须保持一致：
```
ClusterUnknown → ClusterInitializing → ClusterRunning
                              ↓
                    ClusterInitializationFailed
```
#### 3. 命令执行模型
- **BuiltIn**: BKEAgent 内置命令（如 `K8sEnvInit`、`Kubeadm`、`Preprocess`）
- **Shell**: Shell 脚本命令
- **K8s**: Kubernetes 资源操作
#### 4. 特殊 Addon 前置/后置处理
重构时必须保留以下 Addon 的特殊处理逻辑：
- `etcdbackup`：创建备份目录 + etcd 证书 Secret
- `beyondELB`：创建 VIP + 节点标签
- `cluster-api`：创建 kubeconfig Secret + 标记 Agent 切换
- `openfuyao-system-controller`：添加 control-plane 标签 + 下发 patch CM + 创建默认用户
- `gpu-manager`：重建 scheduler static pod
- `bocoperator`：注入 SSH 信息、pipeline 服务器信息
- `nodelocaldns`：根据 proxyMode 注入不同参数
#### 5. 参数注入
所有 Addon 都会注入通用参数，重构时需保持参数名称和来源一致：
- 镜像仓库：`imageRepo`、`imageRepoDomain`、`imageRepoPort`
- 网络：`podSubnet`、`serviceSubnet`、`dnsDomain`、`dnsIP`
- 节点：`masterReplicas`、`workerReplicas`
- etcd：`etcdEndpoints`、`etcdIps`
#### 6. Helm Chart 支持
支持两种 Addon 类型，重构时需保持兼容：
- **YAML 类型**：从 `/manifests/kubernetes/` 读取 YAML 文件，使用 Server-Side Apply
- **Chart 类型**：从 OCI 或 HTTP Chart 仓库拉取，使用 Helm SDK 安装

