
# `ensure_cluster_api_obj.go`详细业务流程梳理
## EnsureClusterAPIObj 业务流程梳理
### 一、整体定位
[ensure_cluster_api_obj.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster_api_obj.go) 是 BKECluster 部署流程中的一个关键阶段，负责**创建并等待 Cluster API 对象就绪**。它通过生成 CAPI（Cluster API）标准资源 YAML 并 Apply 到管理集群，使 BKECluster 与 CAPI 体系对接，从而让 CAPI 控制器接管集群生命周期管理。
### 二、阶段定义
```
阶段名称: EnsureClusterAPIObj
超时时间: 5 分钟
轮询间隔: 2 秒
```
### 三、核心流程（Execute 方法）
```
Execute()
  │
  ├── 1. 判断是否需要创建 CAPI 对象
  │     └── if BKECluster.OwnerReferences == nil → 调用 reconcileCreateClusterAPIObj()
  │
  ├── 2. 轮询等待 CAPI 对象就绪（5分钟超时，2秒间隔）
  │     └── wait.PollImmediateUntil → reconcileClusterAPIObj()
  │
  ├── 3. 检查集群是否被完全控制
  │     └── if !FullyControlled → Requeue（等待下次调谐）
  │
  └── 4. 返回成功
```
### 四、详细子流程
#### 4.1 reconcileCreateClusterAPIObj — 创建 CAPI 对象
这是核心创建逻辑，流程如下：
```
reconcileCreateClusterAPIObj()
  │
  ├── 1. 检查 ClusterAPIObj 条件
  │     └── 如果已存在 ClusterAPIObjCondition → 返回等待错误（防止重复创建）
  │
  ├── 2. 创建 BKE 配置
  │     └── bkeinit.NewBkeConfigFromClusterConfig(bkeCluster.Spec.ClusterConfig)
  │         将 BKECluster.Spec.ClusterConfig 转换为 BkeConfig 对象
  │
  ├── 3. 准备外部 etcd 配置（仅 Bocloud 类型集群）
  │     └── prepareExternalEtcdConfig()
  │         ├── 判断是否为 BocloudCluster（通过 annotation "bke.bocloud.com/cluster-from" == "bocloud"）
  │         ├── 创建 ExternalEtcd 配置模板（etcdCAFile/CertFile/KeyFile 设为占位值 "fake*"）
  │         ├── 获取所有节点，筛选 etcd 节点
  │         └── 构建 etcd 端点列表：https://<nodeIP>:2379，逗号分隔
  │
  ├── 4. 创建 CAPI 对象
  │     └── createClusterAPIObj()
  │         ├── a. 生成 CAPI YAML 文件
  │         │     └── cfg.GenerateClusterAPIConfigFIle(name, namespace, externalEtcd)
  │         │         使用 Go embed 嵌入的 bke-cluster.tmpl 模板渲染
  │         │
  │         ├── b. 创建本地 K8s 客户端
  │         │     └── kube.NewClientFromRestConfig() → 使用管理集群的 RestConfig
  │         │
  │         └── c. Apply YAML 到管理集群
  │               └── localClient.ApplyYaml(task)
  │                   task 设置: 操作=CreateAddon, 等待就绪=true
  │
  └── 5. 设置条件标记
        └── ClusterAPIObjCondition = False (NotReady), "cluster api obj create success"
        同时调用 mergecluster.SyncStatusUntilComplete() 同步状态
```
#### 4.2 生成的 CAPI YAML 包含的资源
根据 [bke-cluster.tmpl](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/tmpl/bke-cluster.tmpl) 模板，生成的 YAML 包含 **5 个 CAPI 标准资源**：

| 资源 | Kind | 用途 |
|------|------|------|
| Cluster | cluster.x-k8s.io/v1beta1 | CAPI 集群对象，关联 controlPlaneRef 和 infrastructureRef |
| KubeadmControlPlane | controlplane.cluster.x-k8s.io/v1beta1 | 控制面管理，定义 master 副本数、kubeadm 配置 |
| BKEMachineTemplate (controlplane) | bke.bocloud.com/v1beta1 | 控制面机器模板 |
| MachineDeployment | cluster.x-k8s.io/v1beta1 | Worker 节点部署，定义 worker 副本数 |
| BKEMachineTemplate (worker) | bke.bocloud.com/v1beta1 | Worker 机器模板 |

**关键设计要点**：
- `masterReplicas` 固定为 **1**，`workerReplicas` 固定为 **0**，避免安装过程中 CAPI 控制器干扰节点管理，由 BKE 自己的控制器去调整实际副本数
- KubeadmControlPlane 上标注了 `skip-kube-proxy: "true"` 和 `skip-coredns: "true"`，跳过 CAPI 默认安装的 kube-proxy 和 coredns，由 BKE 的 Addon 机制自行管理
- `controlPlaneEndpoint` 设为 `"fake"`，因为 BKE 使用自己的负载均衡机制
- Worker 的 `dataSecretName` 设为 `"fake"`，因为 BKE 不使用 CAPI 的 bootstrap 机制
#### 4.3 reconcileClusterAPIObj — 等待 CAPI 对象就绪
```
reconcileClusterAPIObj()
  │
  ├── 1. 获取合并后的 BKECluster
  │     └── mergecluster.GetCombinedBKECluster() → 合并管理集群和工作集群的状态
  │
  ├── 2. 更新 ClusterAPIObj 条件
  │     └── 如果当前条件为 False（刚创建完成）
  │         → 标记为 True（Ready），同步状态
  │
  └── 3. 获取 OwnerCluster
        └── 如果 BKECluster.OwnerReferences != nil（CAPI 控制器已设置）
            ├── util.GetOwnerCluster() → 获取关联的 CAPI Cluster 对象
            └── 设置 e.Ctx.Cluster = cluster（供后续阶段使用）
```
### 五、NeedExecute 判断逻辑
```
NeedExecute(old, new)
  │
  ├── 1. BasePhase.NormalNeedExecute() → 基础判断（状态变化等）
  │     如果不需要执行 → return false
  │
  ├── 2. 如果 new.OwnerReferences != nil → return false
  │     （已有 OwnerRef 说明 CAPI 对象已创建并被关联）
  │
  └── 3. 设置阶段状态为 "Waiting" → return true
```
### 六、FullyControlled 判断逻辑
[clusterutil.FullyControlled()](file:///d:/code/github/cluster-api-provider-bke/utils/capbke/clusterutil/helper.go#L27-L39) 决定集群是否被完全控制：

| 集群类型 | 判断条件 | 结果 |
|----------|----------|------|
| BKE 集群 | annotation `bke.bocloud.com/cluster-from` 未设置或为 "bke" | **true**（完全控制） |
| Other 集群 | annotation 为其他值 | **false**（不完全控制） |
| Bocloud 集群 | annotation 为 "bocloud" 且 `KONKFullManagementClusterAnnotationKey` == "true" | **true** |
| Bocloud 集群 | annotation 为 "bocloud" 但上述 key 不为 "true" | **false** |
### 七、整体流程图
```
┌─────────────────────────────────────────────────────┐
│              EnsureClusterAPIObj.Execute()           │
├─────────────────────────────────────────────────────┤
│                                                     │
│  OwnerReferences == nil?                            │
│       │                                             │
│      YES → reconcileCreateClusterAPIObj()           │
│       │    ├── 检查条件（防重复创建）                 │
│       │    ├── 创建 BkeConfig                       │
│       │    ├── 准备外部 etcd 配置（Bocloud集群）      │
│       │    ├── 生成 CAPI YAML（5个资源）             │
│       │    ├── Apply 到管理集群                      │
│       │    └── 标记 ClusterAPIObjCondition=False     │
│       │                                             │
│      NO  → 跳过创建                                 │
│                                                     │
│  轮询等待（5min超时，2s间隔）                         │
│       │                                             │
│       └── reconcileClusterAPIObj()                  │
│            ├── 获取合并后的 BKECluster               │
│            ├── 更新条件为 Ready                      │
│            └── 获取 OwnerCluster                    │
│                                                     │
│  FullyControlled?                                   │
│       │                                             │
│      NO  → Requeue                                 │
│      YES → 返回成功                                 │
│                                                     │
└─────────────────────────────────────────────────────┘
```
### 八、设计要点总结
1. **与 CAPI 体系对接**：本阶段的核心目的是将 BKECluster 注册为 CAPI 的基础设施提供者（infrastructureRef），使 CAPI 控制器能够识别和管理该集群
2. **防御性副本数设置**：初始创建时 master=1、worker=0，避免 CAPI 控制器在安装过程中干扰节点管理，BKE 通过自己的控制器逐步调整
3. **跳过 CAPI 默认组件**：通过 annotation 跳过 kube-proxy 和 coredns，这些由 BKE 的 Addon 机制统一管理
4. **外部 etcd 支持**：为 Bocloud 类型集群提供外部 etcd 配置能力，使用占位证书（fake*），后续由证书管理阶段替换
5. **状态同步保障**：每次关键操作后都通过 `mergecluster.SyncStatusUntilComplete()` 确保状态被正确持久化

# `EnvPlugin`详细设计分析
## EnvPlugin 设计思路
### 一、整体定位
[EnvPlugin](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/env.go) 是 BKEAgent 内置插件 `K8sEnvInit` 的核心实现，负责在节点加入 Kubernetes 集群**之前**完成操作系统层面的环境初始化和检查。它运行在目标节点上，通过 BKEAgent 的 BuiltIn Command 机制触发执行。
### 二、核心设计原则
#### 1. 插件化契约 — 自描述参数
EnvPlugin 严格遵循 `plugin.Plugin` 接口契约：
```go
type Plugin interface {
    Name() string                                    // 插件身份
    Param() map[string]PluginParam                   // 参数自描述
    Execute(commands []string) ([]string, error)     // 执行入口
}
```
**参数自描述**是核心设计：`Param()` 方法不仅定义了参数名，还声明了每个参数的取值范围、是否必填、默认值和描述信息。这使得：
- 调用方无需查阅文档即可构造合法命令
- `ParseCommands()` 可自动完成参数解析、校验和默认值填充
- 参数契约与实现代码同源，避免文档与代码不一致
#### 2. Scope 驱动 — 细粒度控制
EnvPlugin 采用 **scope（作用域）** 机制控制初始化和检查的范围：
```
scope = "kernel,firewall,selinux,swap,time,hosts,runtime,image,node,ports"
```
每个 scope 对应一个独立的初始化/检查函数，用户可以：
- 精确指定需要初始化的子系统
- 跳过不需要的步骤（如离线环境跳过 image 拉取）
- 在不同节点类型上执行不同的 scope 组合
#### 3. Init + Check 双阶段模型
EnvPlugin 将环境准备分为两个阶段：

| 阶段 | 入口 | 作用 | 失败策略 |
|------|------|------|----------|
| **Init** | `initK8sEnv()` | 主动修改系统配置 | 关键错误中断，非关键错误忽略 |
| **Check** | `checkK8sEnv()` | 验证环境是否满足要求 | 记录警告，不中断 |

关键设计：**`init=true` 时自动触发 `check`**，确保初始化后立即验证结果。
#### 4. 按需加载集群配置
EnvPlugin 通过 `bkeConfig` 参数按需从管理集群获取配置：
```go
if envParamMap["bkeConfig"] != "" {
    cfg, err := plugin.GetBkeConfig(envParamMap["bkeConfig"])    // 获取 BKEConfig
    clusterData, err := plugin.GetClusterData(envParamMap["bkeConfig"])  // 获取集群节点数据
    cNode, err := ep.nodes.CurrentNode()  // 定位当前节点
}
```
这实现了**配置与执行解耦**：插件本身不持有集群状态，运行时动态获取，保证使用最新配置。
### 三、架构分层
```
┌─────────────────────────────────────────────────┐
│                  EnvPlugin                       │
├─────────────────────────────────────────────────┤
│  Execute() — 入口：参数解析 + 流程编排            │
├──────────────┬──────────────────────────────────┤
│  init.go     │  check.go — 初始化/检查实现       │
├──────────────┴──────────────────────────────────┤
│  machine.go  — 主机信息抽象                       │
│  hostfile.go — /etc/hosts 文件操作封装            │
│  utils.go    — 文件读写工具（catAndSearch/Replace）│
│  centos.go   — CentOS 特有逻辑（NetworkManager）  │
└─────────────────────────────────────────────────┘
```
### 四、参数规格
| 参数 | 取值 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `init` | true,false | 否 | true | 是否执行环境初始化 |
| `check` | true,false | 否 | true | 是否执行环境检查（init=true 时自动执行） |
| `sudo` | true,false | 否 | true | 是否使用 sudo 执行命令 |
| `scope` | 逗号分隔的 scope 列表 | 否 | kernel,firewall,selinux,swap,time,hosts,runtime,image,node,ports | 初始化/检查范围 |
| `backup` | true,false | 否 | true | 修改文件前是否备份 |
| `extraHosts` | hostname1:ip1,hostname2:ip2 | 否 | 空 | 额外的 hosts 映射 |
| `hostPort` | 逗号分隔的端口列表 | 否 | 10259,10257,10250,2379,2380,2381,10248 | 需要检查可用性的端口 |
| `bkeConfig` | ns:name | 否 | 空 | BKEConfig 资源引用，用于动态加载集群配置 |
### 五、Scope 详细说明
#### 5.1 Init Scope（初始化作用域）
| Scope | 函数 | 作用 | 关键操作 |
|-------|------|------|----------|
| `kernel` | `initKernelParam()` | 内核参数调优 | 写入 `/etc/sysctl.d/k8s.conf`，加载内核模块，设置 ulimit，配置 IPVS |
| `firewall` | `initFirewall()` | 关闭防火墙 | 停止并禁用 firewalld/ufw |
| `selinux` | `initSelinux()` | 关闭 SELinux | `setenforce 0`，修改 `/etc/selinux/config` |
| `swap` | `initSwap()` | 关闭交换分区 | 注释 fstab 中的 swap 行，`swapoff -a`，设置 `vm.swappiness=0` |
| `time` | `initTime()` | 时间同步 | 设置时区为 Asia/Shanghai，配置 NTP |
| `hosts` | `initHost()` | 配置主机名和 hosts | 设置 hostname，写入集群节点和仓库的 hosts 映射 |
| `image` | `initImage()` | 拉取容器镜像 | 通过 docker/containerd 客户端拉取所需镜像 |
| `runtime` | `initRuntime()` | 初始化容器运行时 | 安装/配置 containerd 或 docker + cri-dockerd |
| `dns` | `initDNS()` | 配置 DNS | 关闭 NetworkManager 自动覆盖 resolv.conf（CentOS） |
| `httpRepo` | `initHttpRepo()` | 配置 YUM 源 | 设置离线仓库地址 |
| `iptables` | `initIptables()` | 配置 iptables | 设置 INPUT/OUTPUT/FORWARD 为 ACCEPT |
| `registry` | `initRegistry()` | 配置镜像仓库 | 记录仓库端口信息 |
#### 5.2 Check Scope（检查作用域）
| Scope | 函数 | 作用 |
|-------|------|------|
| `kernel` | `checkKernelParam()` | 验证内核参数是否正确 |
| `firewall` | `checkFirewall()` | 验证防火墙已关闭 |
| `selinux` | `checkSelinux()` | 验证 SELinux 已关闭 |
| `swap` | `checkSwap()` | 验证 swap 已关闭 |
| `time` | `checkTime()` | 验证时间同步 cron 正在运行 |
| `hosts` | `checkHost()` | 验证 hosts 文件配置正确 |
| `ports` | `checkHostPort()` | 验证关键端口可用 |
| `node` | `checkNodeInfo()` | 验证节点资源（CPU≥2, 内存≥阈值） |
| `runtime` | `checkRuntime()` | 验证容器运行时可用 |
| `dns` | `checkDNS()` | 验证 DNS 可用 |
| `httpRepo` | 跳过 | 初始化阶段已验证 |
### 六、关键设计细节
#### 6.1 Machine 抽象 — 操作系统感知
[Machine](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/machine.go) 通过 `gopsutil` 库采集主机信息：
```go
type Machine struct {
    Hostname string
    hostArch string    // runtime.GOARCH
    hostOS   string    // runtime.GOOS
    platform string    // centos/ubuntu/kylin/...
    version  string    // 7/8/...
    kernel   string    // 内核版本
    cpuNum   int
    memSize  int       // GB
}
```
EnvPlugin 大量使用 `platform` 和 `version` 进行**操作系统差异化处理**：
- CentOS 7 + containerd → 设置 `fs.may_detach_mounts=1`
- Ubuntu → 模块写入 `/etc/modules`
- CentOS/Kylin → 模块写入 `/etc/sysconfig/modules/ip_vs.modules`
- Kylin → 额外配置 `rc.local`
- 不同平台安装不同的 lxcfs 包
#### 6.2 内核参数动态构建
内核参数在 `init()` 阶段动态构建，而非硬编码：
```go
func init() {
    // 1. 根据 IP 模式（ipv4/ipv6）选择基础参数
    for k, v := range kernelParam[DefaultIpMode] { ... }
    // 2. 叠加通用参数
    for k, v := range defaultKernelParam { ... }
    // 3. 检测默认网卡，设置 rp_filter
    face, _ := netutil.GetV4Interface()
    execKernelParam[fmt.Sprintf("net.ipv4.conf.%s.rp_filter", face)] = "0"
}
```
运行时还会根据配置动态追加：
- `setupCentos7DetachMounts()` → CentOS 7 + containerd 追加 `fs.may_detach_mounts`
- `setupIPVSConfig()` → proxyMode=ipvs 时追加 `net.ipv4.vs.conntrack` 和相关模块
#### 6.3 容器运行时安装策略
`initRuntime()` 实现了**智能运行时管理**：
```
当前运行时 vs 配置运行时
    │
    ├── 相同 → 仅修改配置文件并重启
    ├── 不同 → 下载并安装指定运行时
    │         ├── containerd → 调用 containerd 插件
    │         └── docker → 调用 docker 插件 + cri-dockerd
    └── 未指定 → 使用当前运行时，仅修改配置
```
#### 6.4 Hosts 文件管理
`initHost()` 的 hosts 来源有三个：
1. **extraHosts 参数**：用户显式指定的映射
2. **clusterHosts**：从 bkeConfig 动态构建的集群节点映射
3. **仓库域名**：镜像仓库和 HTTP 仓库的域名→IP 映射

使用 [HostsFile](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/hostfile.go) 封装库操作，保证 hosts 文件格式正确。
#### 6.5 错误处理策略
EnvPlugin 采用了**分级错误处理**：

| 场景 | 策略 | 示例 |
|------|------|------|
| 关键初始化失败 | 返回错误，中断流程 | swapoff 失败、hosts 写入失败 |
| 非关键初始化失败 | 记录警告，继续执行 | 内核参数设置失败（`ignore`）、umask 设置失败 |
| 检查失败 | 记录警告，不中断 | 内核参数检查、节点资源不足 |
| 未知 scope | 记录警告，跳过 | 未知 scope 名称 |
### 七、Execute 执行流程
```
Execute(commands)
  │
  ├── 1. plugin.ParseCommands(ep, commands)
  │     解析命令参数 → 校验必填项 → 填充默认值
  │
  ├── 2. 加载运行时参数
  │     sudo, scope, backup, extraHosts, hostPort
  │
  ├── 3. 按需加载集群配置（bkeConfig 参数）
  │     ├── plugin.GetBkeConfig() → 获取 BKEConfig
  │     ├── plugin.GetClusterData() → 获取节点列表
  │     └── nodes.CurrentNode() → 定位当前节点
  │
  ├── 4. init=true → initK8sEnv()
  │     遍历 scope 列表 → processInitScope()
  │     ├── kernel:  写内核参数 + 加载模块 + 设置 ulimit
  │     ├── swap:    关闭 swap
  │     ├── firewall: 关闭防火墙
  │     ├── selinux: 关闭 SELinux
  │     ├── time:    配置时区和 NTP
  │     ├── hosts:   设置 hostname + 写 hosts
  │     ├── image:   拉取容器镜像
  │     ├── runtime: 安装/配置容器运行时
  │     ├── dns:     配置 DNS
  │     ├── httpRepo: 配置 YUM 源
  │     ├── iptables: 配置 iptables 规则
  │     └── registry: 配置镜像仓库
  │
  └── 5. check=true 或 init=true → checkK8sEnv()
        遍历 scope 列表 → processCheckScope()
        验证各子系统状态
```
### 八、设计亮点总结
1. **自描述参数契约**：参数定义与代码同源，自动解析校验，消除文档与实现的不一致
2. **Scope 驱动**：细粒度控制初始化范围，支持灵活组合，适应不同部署场景
3. **Init + Check 双阶段**：先修改后验证，init 自动触发 check 保证可靠性
4. **操作系统感知**：通过 Machine 抽象自动适配 CentOS/Ubuntu/Kylin 等不同平台
5. **按需配置加载**：通过 bkeConfig 参数动态获取集群配置，避免硬编码和状态过期
6. **内核参数动态构建**：根据 IP 模式、网卡、运行时、代理模式动态组装内核参数
7. **容器运行时智能管理**：自动检测当前运行时，按需安装/配置/切换
8. **分级错误处理**：关键错误中断、非关键错误降级、检查失败仅告警

# EnsureClusterAPIObj完整的 CAPI YAML 清单梳理
## EnsureClusterAPIObj 部署的 CAPI YAML 清单
[ensure_cluster_api_obj.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster_api_obj.go) 通过 [bke-cluster.tmpl](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/tmpl/bke-cluster.tmpl) 模板渲染，一次性 Apply **5 个 CAPI 资源**。以下逐一列出完整清单及模板变量来源。
### 清单 1：Cluster（CAPI 集群对象）
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: {{.name}}            # ← BKECluster.Name
  namespace: {{.namespace}}   # ← BKECluster.Namespace
spec:
  clusterNetwork:
    services:
      cidrBlocks: [ "{{.servicesCIDR}}" ]    # ← BKEConfig.Networking.ServiceSubnet
    pods:
      cidrBlocks: [ "{{.podsCIDR}}" ]        # ← BKEConfig.Networking.PodSubnet
    serviceDomain: "{{.serviceDomain}}"       # ← BKEConfig.Networking.DNSDomain
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1beta1
    kind: KubeadmControlPlane
    name: {{.name}}-controlplane
    namespace: {{.namespace}}
  infrastructureRef:
    apiVersion: bke.bocloud.com/v1beta1
    kind: BKECluster
    name: {{.name}}
    namespace: {{.namespace}}
```
**作用**：CAPI 的顶层集群对象，声明集群网络配置，引用控制面和基础设施提供者。

**关键设计**：
- `infrastructureRef` 指向 BKECluster 自身，使 BKECluster 成为 CAPI 的基础设施提供者
- `controlPlaneRef` 指向下面的 KubeadmControlPlane
### 清单 2：KubeadmControlPlane（控制面管理）
```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
metadata:
  name: {{.name}}-controlplane
  namespace: {{.namespace}}
  annotations:
    controlplane.cluster.x-k8s.io/skip-kube-proxy: "true"   # 跳过 CAPI 自动安装 kube-proxy
    controlplane.cluster.x-k8s.io/skip-coredns: "true"       # 跳过 CAPI 自动安装 coredns
spec:
  replicas: {{.masterReplicas}}       # ← 固定为 1
  version: {{.kubernetesVersion}}     # ← BKEConfig.Cluster.KubernetesVersion
  machineTemplate:
    infrastructureRef:
      apiVersion: bke.bocloud.com/v1beta1
      kind: BKEMachineTemplate
      name: {{.name}}-machine-controlplane
      namespace: {{.namespace}}
  kubeadmConfigSpec:
    clusterConfiguration:
      clusterName: {{.name}}
      controlPlaneEndpoint: "fake"    # ← 占位值，BKE 使用自己的负载均衡
      networking:
        dnsDomain: {{.serviceDomain}}
        podSubnet: {{.podsCIDR}}
        serviceSubnet: {{.servicesCIDR}}
      kubernetesVersion: {{.kubernetesVersion}}
      apiServer:
        certSANs:
          {{.SANS}}                   # ← 自动生成：127.0.0.1 + localhost + DNS IP + 用户配置的 SANs
      imageRepository: {{.repo}}      # ← domain:port/prefix
      {{- if eq .externalEtcd "true"}}
      etcd:
        external:
          endpoints:                   # ← etcd 节点 https://<IP>:2379 列表
            - https://<etcdIP1>:2379
            - https://<etcdIP2>:2379
          caFile: {{.etcdCAFile}}      # ← 占位值 "fakeCaCert"
          certFile: {{.etcdCertFile}}  # ← 占位值 "fakeCertFile"
          keyFile: {{.etcdKeyFile}}    # ← 占位值 "fakeKeyFile"
      {{- end}}
    initConfiguration:
      nodeRegistration:
        kubeletExtraArgs:
          cgroup-driver: systemd
    joinConfiguration:
      nodeRegistration:
        kubeletExtraArgs:
          cgroup-driver: systemd
```
**作用**：定义控制面的 kubeadm 配置，CAPI 控制器据此初始化/加入 master 节点。

**关键设计**：
| 设计点 | 值 | 原因 |
|--------|------|------|
| `replicas` | **1** | 避免安装过程中 CAPI 干扰，由 BKE 控制器调整实际副本数 |
| `controlPlaneEndpoint` | `"fake"` | BKE 使用自己的负载均衡机制，不使用 CAPI 默认端点 |
| `skip-kube-proxy` | `"true"` | kube-proxy 由 BKE Addon 机制管理 |
| `skip-coredns` | `"true"` | coredns 由 BKE Addon 机制管理 |
| `cgroup-driver` | `systemd` | 统一使用 systemd cgroup 驱动 |
| 外部 etcd 证书 | `"fake*"` | 占位值，后续由证书管理阶段替换 |
### 清单 3：BKEMachineTemplate（控制面机器模板）
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKEMachineTemplate
metadata:
  name: {{.name}}-machine-controlplane
  namespace: {{.namespace}}
spec:
  template:
    spec: { }    # ← 空规格，BKE 不通过 CAPI 管理机器
```
**作用**：KubeadmControlPlane 的 infrastructureRef 引用的机器模板。

**关键设计**：`spec: { }` 为空，因为 BKE 不通过 CAPI 的 Machine 机制管理实际节点，节点生命周期由 BKE 自己的 BKENode/BKEAgent 体系管理。
### 清单 4：MachineDeployment（Worker 节点部署）
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineDeployment
metadata:
  namespace: {{.namespace}}
  name: {{.name}}-worker
spec:
  clusterName: {{.name}}
  replicas: {{.workerReplicas}}     # ← 固定为 0
  selector:
    matchLabels:
      cluster.x-k8s.io/cluster-name: {{.name}}
  template:
    spec:
      version: {{.workerVersion}}   # ← BKEConfig.Cluster.KubernetesVersion
      clusterName: {{.name}}
      bootstrap:
        dataSecretName: "fake"      # ← 占位值，BKE 不使用 CAPI bootstrap
      infrastructureRef:
        apiVersion: bke.bocloud.com/v1beta1
        kind: BKEMachineTemplate
        name: {{.name}}-machine-worker
```
**作用**：CAPI 的 Worker 节点部署对象。

**关键设计**：
| 设计点 | 值 | 原因 |
|--------|------|------|
| `replicas` | **0** | Worker 节点由 BKE 控制器管理，不通过 CAPI 扩缩 |
| `dataSecretName` | `"fake"` | BKE 不使用 CAPI 的 bootstrap token 机制，节点通过 BKEAgent 加入 |
### 清单 5：BKEMachineTemplate（Worker 机器模板）
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKEMachineTemplate
metadata:
  name: {{.name}}-machine-worker
  namespace: {{.namespace}}
spec:
  template:
    spec: { }    # ← 空规格，同控制面模板
```
**作用**：MachineDeployment 的 infrastructureRef 引用的机器模板，同样为空规格。
### 模板变量汇总

| 变量 | 来源 | 默认值/说明 |
|------|------|-------------|
| `name` | BKECluster.Name | 集群名称 |
| `namespace` | BKECluster.Namespace | 命名空间 |
| `repo` | `ImageRepo.Domain:Port/Prefix` | 镜像仓库地址 |
| `masterReplicas` | **硬编码 1** | 安装阶段固定为 1 |
| `workerReplicas` | **硬编码 0** | 安装阶段固定为 0 |
| `kubernetesVersion` | BKEConfig.Cluster.KubernetesVersion | K8s 版本 |
| `workerVersion` | BKEConfig.Cluster.KubernetesVersion | 同 K8s 版本 |
| `servicesCIDR` | BKEConfig.Networking.ServiceSubnet | Service 网段 |
| `podsCIDR` | BKEConfig.Networking.PodSubnet | Pod 网段 |
| `serviceDomain` | BKEConfig.Networking.DNSDomain | 集群 DNS 域名 |
| `SANS` | 自动生成 | 127.0.0.1 + localhost + 10.96.0.10 + 用户配置的 CertSANs |
| `externalEtcd` | `"false"` / `"true"` | 仅 Bocloud 类型集群为 true |
| `etcdEndpoints` | etcd 节点 IP 列表 | `https://<IP>:2379` 逗号分隔 |
| `etcdCAFile` | `"fakeCaCert"` | 占位值 |
| `etcdCertFile` | `"fakeCertFile"` | 占位值 |
| `etcdKeyFile` | `"fakeKeyFile"` | 占位值 |
### 资源关系图
```
Cluster
  ├── controlPlaneRef ──→ KubeadmControlPlane
  │                         ├── machineTemplate.infrastructureRef ──→ BKEMachineTemplate (controlplane)
  │                         └── kubeadmConfigSpec (集群配置)
  └── infrastructureRef ──→ BKECluster (自身)

MachineDeployment (worker)
  ├── bootstrap.dataSecretName = "fake"
  └── template.infrastructureRef ──→ BKEMachineTemplate (worker)
```
### 整体设计意图
BKE 创建这些 CAPI 资源的核心目的是**与 CAPI 体系对接**，但通过多处"占位"和"最小化"设计，确保 CAPI 控制器不会干扰 BKE 自身的节点管理：
1. **副本数最小化**：master=1, worker=0，避免 CAPI 自动扩缩节点
2. **跳过默认组件**：kube-proxy 和 coredns 由 BKE Addon 管理
3. **端点占位**：controlPlaneEndpoint="fake"，BKE 使用自己的负载均衡
4. **Bootstrap 占位**：dataSecretName="fake"，BKE 使用 BKEAgent 加入节点
5. **机器模板空规格**：spec: {}，BKE 不依赖 CAPI 管理机器生命周期
6. **外部 etcd 证书占位**：fake* 证书，后续由 EnsureCerts 阶段替换
