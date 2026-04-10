
# BKECluster控制器组件安装部署顺序与清单
## 一、Phase执行流程总览
根据 [list.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\list.go) 的定义，Phase执行分为三大类：
```
┌─────────────────────────────────────────────────────────────┐
│                    Phase执行流程                             │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. CommonPhases (通用阶段)                                  │
│     ├─ EnsureFinalizer                                       │
│     ├─ EnsurePaused                                          │
│     ├─ EnsureClusterManage                                   │
│     ├─ EnsureDeleteOrReset                                   │
│     └─ EnsureDryRun                                          │
│                                                              │
│  2. DeployPhases (部署阶段)                                  │
│     ├─ EnsureBKEAgent                                        │
│     ├─ EnsureNodesEnv                                        │
│     ├─ EnsureClusterAPIObj                                   │
│     ├─ EnsureCerts                                           │
│     ├─ EnsureLoadBalance                                     │
│     ├─ EnsureMasterInit                                      │
│     ├─ EnsureMasterJoin                                      │
│     ├─ EnsureWorkerJoin                                      │
│     ├─ EnsureAddonDeploy                                     │
│     ├─ EnsureNodesPostProcess                                │
│     └─ EnsureAgentSwitch                                     │
│                                                              │
│  3. PostDeployPhases (部署后阶段)                            │
│     ├─ EnsureProviderSelfUpgrade                             │
│     ├─ EnsureAgentUpgrade                                    │
│     ├─ EnsureContainerdUpgrade                               │
│     ├─ EnsureEtcdUpgrade                                     │
│     ├─ EnsureWorkerUpgrade                                   │
│     ├─ EnsureMasterUpgrade                                   │
│     ├─ EnsureWorkerDelete                                    │
│     ├─ EnsureMasterDelete                                    │
│     ├─ EnsureComponentUpgrade                                │
│     └─ EnsureCluster                                         │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```
## 二、详细部署顺序与组件清单
### 2.1 阶段一：通用阶段
| 序号 | Phase名称 | 中文名称 | 主要功能 | 执行条件 |
|-----|----------|---------|---------|---------|
| 1 | EnsureFinalizer | 部署任务创建 | 添加Finalizer，确保资源清理 | 首次创建时 |
| 2 | EnsurePaused | 集群管理暂停 | 检查集群是否暂停 | 集群暂停时 |
| 3 | EnsureClusterManage | 纳管现有集群 | 纳管已存在的Kubernetes集群 | 纳管场景 |
| 4 | EnsureDeleteOrReset | 集群删除 | 删除或重置集群 | 集群删除时 |
| 5 | EnsureDryRun | DryRun部署 | 模拟部署验证 | DryRun模式 |
### 2.2 阶段二：部署阶段
| 序号 | Phase名称 | 中文名称 | 主要功能 | 安装组件 | 执行条件 |
|-----|----------|---------|---------|---------|---------|
| 1 | **EnsureBKEAgent** | 推送Agent | 推送BKE Agent到节点 | • BKE Agent<br>• 信任证书链<br>• Kubeconfig | 节点未安装Agent |
| 2 | **EnsureNodesEnv** | 节点环境准备 | 准备节点运行环境 | • 系统参数配置<br>• 时间同步<br>• 必要工具<br>• Containerd<br>• 网络配置 | 节点环境未就绪 |
| 3 | **EnsureClusterAPIObj** | ClusterAPI对接 | 创建Cluster API对象 | • Cluster资源<br>• KubeadmControlPlane<br>• MachineDeployment | 首次部署 |
| 4 | **EnsureCerts** | 集群证书创建 | 生成集群证书 | • CA证书<br>• API Server证书<br>• Etcd证书<br>• Front Proxy证书<br>• Service Account密钥 | 证书不存在或过期 |
| 5 | **EnsureLoadBalance** | 集群入口配置 | 配置负载均衡器 | • HAProxy/Nginx<br>• Keepalived<br>• 负载均衡配置 | 多Master场景 |
| 6 | **EnsureMasterInit** | Master初始化 | 初始化第一个Master节点 | • kubeadm init<br>• kube-apiserver<br>• kube-controller-manager<br>• kube-scheduler<br>• etcd<br>• kubelet<br>• kube-proxy<br>• CoreDNS | 第一个Master节点 |
| 7 | **EnsureMasterJoin** | Master加入 | 其他Master节点加入集群 | • kubeadm join<br>• kube-apiserver<br>• kube-controller-manager<br>• kube-scheduler<br>• etcd<br>• kubelet<br>• kube-proxy | 其他Master节点 |
| 8 | **EnsureWorkerJoin** | Worker加入 | Worker节点加入集群 | • kubeadm join<br>• kubelet<br>• kube-proxy | Worker节点 |
| 9 | **EnsureAddonDeploy** | 集群组件部署 | 部署集群Addon | • CNI插件<br>• CSI插件<br>• 监控组件<br>• 日志组件<br>• 其他Addon | 集群就绪后 |
| 10 | **EnsureNodesPostProcess** | 后置脚本处理 | 执行后置脚本 | • 用户自定义脚本<br>• 集群初始化后配置 | 节点加入后 |
| 11 | **EnsureAgentSwitch** | Agent监听切换 | 切换Agent监听模式 | • Agent状态切换<br>• 监听模式配置 | 部署完成后 |
### 2.3 阶段三：部署后阶段
| 序号 | Phase名称 | 中文名称 | 主要功能 | 执行条件 |
|-----|----------|---------|---------|---------|
| 1 | EnsureProviderSelfUpgrade | Provider自升级 | 升级Provider自身 | Provider版本变更 |
| 2 | EnsureAgentUpgrade | Agent升级 | 升级BKE Agent | Agent版本变更 |
| 3 | EnsureContainerdUpgrade | Containerd升级 | 升级Containerd | Containerd版本变更 |
| 4 | EnsureEtcdUpgrade | Etcd升级 | 升级Etcd集群 | Etcd版本变更 |
| 5 | EnsureWorkerUpgrade | Worker升级 | 升级Worker节点 | K8s版本变更 |
| 6 | EnsureMasterUpgrade | Master升级 | 升级Master节点 | K8s版本变更 |
| 7 | EnsureWorkerDelete | Worker删除 | 删除Worker节点 | 节点缩容 |
| 8 | EnsureMasterDelete | Master删除 | 删除Master节点 | Master缩容 |
| 9 | EnsureComponentUpgrade | openFuyao核心组件升级 | 升级核心组件 | 组件版本变更 |
| 10 | EnsureCluster | 集群健康检查 | 定期健康检查 | 周期性执行 |
## 三、关键Phase详细说明
### 3.1 EnsureBKEAgent（推送Agent）
**文件位置**：[ensure_bke_agent.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_bke_agent.go)

**执行流程**：
```
EnsureBKEAgent
    │
    ├─ 1. 加载本地Kubeconfig
    │      └─ 读取管理集群的kubeconfig
    │
    ├─ 2. 获取需要推送Agent的节点
    │      └─ 检查节点是否已安装Agent
    │
    ├─ 3. 推送Agent到节点
    │      ├─ 推送信任证书链
    │      │   └─ /etc/openFuyao/certs/trust-chain.crt
    │      ├─ 推送Agent二进制
    │      ├─ 推送Agent配置
    │      │   └─ /etc/openFuyao/agent/config.yaml
    │      └─ 启动Agent服务
    │          └─ systemctl start bke-agent
    │
    └─ 4. Ping Agent验证
           └─ 获取节点Hostname等信息
```
**安装组件清单**：

| 组件 | 路径 | 说明 |
|------|------|------|
| 信任证书链 | `/etc/openFuyao/certs/trust-chain.crt` | Registry证书链 |
| Agent配置 | `/etc/openFuyao/agent/config.yaml` | Agent配置文件 |
| Agent二进制 | `/usr/local/bin/bke-agent` | Agent可执行文件 |
| Agent服务 | `/etc/systemd/system/bke-agent.service` | Systemd服务文件 |
### 3.2 EnsureNodesEnv（节点环境准备）
**文件位置**：[ensure_nodes_env.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_nodes_env.go)

**执行流程**：
```
EnsureNodesEnv
    │
    ├─ 1. 系统参数配置
    │      ├─ 关闭Swap
    │      ├─ 配置内核参数
    │      │   ├─ net.bridge.bridge-nf-call-iptables = 1
    │      │   ├─ net.ipv4.ip_forward = 1
    │      │   └─ vm.max_map_count = 262144
    │      └─ 加载内核模块
    │          ├─ br_netfilter
    │          └─ overlay
    │
    ├─ 2. 时间同步
    │      └─ 配置NTP服务
    │
    ├─ 3. 安装必要工具
    │      ├─ lxcfs
    │      ├─ nfs-utils
    │      ├─ etcdctl
    │      ├─ helm
    │      ├─ calicoctl
    │      └─ 其他工具
    │
    ├─ 4. 安装Containerd
    │      ├─ 安装containerd包
    │      ├─ 配置containerd
    │      │   └─ /etc/containerd/config.toml
    │      └─ 启动containerd服务
    │
    └─ 5. 网络配置
           └─ 配置节点网络
```
**安装组件清单**：

| 组件 | 说明 |
|------|------|
| Containerd | 容器运行时 |
| lxcfs | 容器资源视图 |
| nfs-utils | NFS客户端工具 |
| etcdctl | Etcd命令行工具 |
| helm | Kubernetes包管理器 |
| calicoctl | Calico命令行工具 |
| runc | 容器运行时 |
### 3.3 EnsureMasterInit（Master初始化）
**文件位置**：[ensure_master_init.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_master_init.go)

**执行流程**：
```
EnsureMasterInit
    │
    ├─ 1. 验证Master节点
    │      ├─ 检查节点数量
    │      └─ 检查节点状态
    │
    ├─ 2. 准备kubeadm配置
    │      ├─ 生成kubeadm-config.yaml
    │      └─ 配置集群参数
    │
    ├─ 3. 执行kubeadm init
    │      ├─ 初始化控制平面
    │      ├─ 生成kubeconfig
    │      └─ 安装CoreDNS
    │
    ├─ 4. 启动控制平面组件
    │      ├─ kube-apiserver
    │      ├─ kube-controller-manager
    │      ├─ kube-scheduler
    │      └─ etcd
    │
    ├─ 5. 配置kubelet
    │      ├─ 启动kubelet
    │      └─ 配置kube-proxy
    │
    └─ 6. 等待控制平面就绪
           └─ 检查组件状态
```
**安装组件清单**：

| 组件 | 说明 |
|------|------|
| kube-apiserver | Kubernetes API服务器 |
| kube-controller-manager | 控制器管理器 |
| kube-scheduler | 调度器 |
| etcd | 分布式键值存储 |
| kubelet | 节点代理 |
| kube-proxy | 网络代理 |
| CoreDNS | 集群DNS |
### 3.4 EnsureAddonDeploy（集群组件部署）
**文件位置**：[ensure_addon_deploy.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_addon_deploy.go)

**执行流程**：
```
EnsureAddonDeploy
    │
    ├─ 1. 连接目标集群
    │      └─ 获取目标集群kubeconfig
    │
    ├─ 2. 获取Addon列表
    │      └─ 从BKECluster.Spec.Addons读取
    │
    ├─ 3. 部署Addon
    │      ├─ 渲染Addon Manifest
    │      ├─ 应用Manifest到目标集群
    │      └─ 等待Addon就绪
    │
    └─ 4. 记录Addon状态
           └─ 保存Addon部署记录
```
**安装组件清单**：

| Addon类型 | 组件示例 | 说明 |
|----------|---------|------|
| CNI | Calico, Flannel, Cilium | 容器网络接口 |
| CSI | Ceph CSI, NFS CSI | 容器存储接口 |
| 监控 | Prometheus, Grafana | 集群监控 |
| 日志 | Elasticsearch, Fluentd | 日志收集 |
| 其他 | Ingress Controller | 其他组件 |
## 四、场景化Phase执行顺序
### 4.1 集群初始化场景
```
集群初始化Phase执行顺序：
┌─────────────────────────────────────────────────────────────┐
│ 1. EnsureFinalizer         - 添加Finalizer                  │
├─────────────────────────────────────────────────────────────┤
│ 2. EnsureBKEAgent          - 推送Agent到所有节点            │
├─────────────────────────────────────────────────────────────┤
│ 3. EnsureNodesEnv          - 准备所有节点环境               │
├─────────────────────────────────────────────────────────────┤
│ 4. EnsureClusterAPIObj     - 创建Cluster API对象           │
├─────────────────────────────────────────────────────────────┤
│ 5. EnsureCerts             - 生成集群证书                   │
├─────────────────────────────────────────────────────────────┤
│ 6. EnsureLoadBalance       - 配置负载均衡器                 │
├─────────────────────────────────────────────────────────────┤
│ 7. EnsureMasterInit        - 初始化第一个Master             │
├─────────────────────────────────────────────────────────────┤
│ 8. EnsureMasterJoin        - 其他Master加入集群             │
├─────────────────────────────────────────────────────────────┤
│ 9. EnsureWorkerJoin        - Worker节点加入集群             │
├─────────────────────────────────────────────────────────────┤
│ 10. EnsureAddonDeploy      - 部署集群Addon                  │
├─────────────────────────────────────────────────────────────┤
│ 11. EnsureNodesPostProcess - 执行后置脚本                   │
├─────────────────────────────────────────────────────────────┤
│ 12. EnsureAgentSwitch      - 切换Agent监听模式              │
└─────────────────────────────────────────────────────────────┘
```
### 4.2 集群升级场景
```
集群升级Phase执行顺序：
┌─────────────────────────────────────────────────────────────┐
│ 1. EnsureProviderSelfUpgrade - Provider自升级               │
├─────────────────────────────────────────────────────────────┤
│ 2. EnsureAgentUpgrade        - Agent升级                    │
├─────────────────────────────────────────────────────────────┤
│ 3. EnsureContainerdUpgrade   - Containerd升级               │
├─────────────────────────────────────────────────────────────┤
│ 4. EnsureEtcdUpgrade         - Etcd升级                     │
├─────────────────────────────────────────────────────────────┤
│ 5. EnsureMasterUpgrade       - Master节点升级               │
├─────────────────────────────────────────────────────────────┤
│ 6. EnsureWorkerUpgrade       - Worker节点升级               │
├─────────────────────────────────────────────────────────────┤
│ 7. EnsureComponentUpgrade    - 核心组件升级                 │
└─────────────────────────────────────────────────────────────┘
```
### 4.3 集群扩容场景
```
Master扩容：
┌─────────────────────────────────────────────────────────────┐
│ 1. EnsureBKEAgent      - 推送Agent到新Master节点            │
├─────────────────────────────────────────────────────────────┤
│ 2. EnsureNodesEnv      - 准备新Master节点环境               │
├─────────────────────────────────────────────────────────────┤
│ 3. EnsureMasterJoin    - 新Master加入集群                   │
└─────────────────────────────────────────────────────────────┘

Worker扩容：
┌─────────────────────────────────────────────────────────────┐
│ 1. EnsureBKEAgent      - 推送Agent到新Worker节点            │
├─────────────────────────────────────────────────────────────┤
│ 2. EnsureNodesEnv      - 准备新Worker节点环境               │
├─────────────────────────────────────────────────────────────┤
│ 3. EnsureWorkerJoin    - 新Worker加入集群                   │
└─────────────────────────────────────────────────────────────┘
```
### 4.4 集群缩容场景
```
Master缩容：
┌─────────────────────────────────────────────────────────────┐
│ EnsureMasterDelete    - 删除Master节点                      │
└─────────────────────────────────────────────────────────────┘

Worker缩容：
┌─────────────────────────────────────────────────────────────┐
│ EnsureWorkerDelete    - 删除Worker节点                      │
└─────────────────────────────────────────────────────────────┘
```
### 4.5 集群删除场景
```
集群删除Phase执行顺序：
┌─────────────────────────────────────────────────────────────┐
│ 1. EnsurePaused        - 检查集群是否暂停                   │
├─────────────────────────────────────────────────────────────┤
│ 2. EnsureDeleteOrReset - 删除或重置集群                     │
│    ├─ 删除Worker节点                                         │
│    ├─ 删除Master节点                                         │
│    ├─ 清理负载均衡器                                         │
│    ├─ 清理证书                                               │
│    └─ 清理其他资源                                           │
└─────────────────────────────────────────────────────────────┘
```
## 五、组件依赖关系图
```
┌─────────────────────────────────────────────────────────────┐
│                    组件依赖关系                              │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  BKEAgent                                                    │
│      │                                                       │
│      └─> Containerd                                          │
│             │                                                │
│             └─> Kubernetes Components                        │
│                    │                                         │
│                    ├─> kube-apiserver                        │
│                    ├─> kube-controller-manager               │
│                    ├─> kube-scheduler                        │
│                    ├─> etcd                                  │
│                    ├─> kubelet                               │
│                    └─> kube-proxy                            │
│                           │                                  │
│                           └─> Addons                         │
│                                  │                           │
│                                  ├─> CNI (Calico/Flannel)    │
│                                  ├─> CSI                     │
│                                  ├─> Monitoring              │
│                                  └─> Logging                 │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```
## 六、总结
### 6.1 关键要点
1. **分层设计**：Phase分为通用、部署、部署后三个层次
2. **顺序执行**：Phase按照定义的顺序依次执行
3. **条件触发**：每个Phase根据条件判断是否需要执行
4. **幂等性**：每个Phase支持重复执行而不产生副作用
### 6.2 核心组件清单
| 类别 | 组件 | 安装阶段 |
|------|------|---------|
| **基础设施** | BKE Agent | EnsureBKEAgent |
| **容器运行时** | Containerd | EnsureNodesEnv |
| **控制平面** | kube-apiserver, kube-controller-manager, kube-scheduler, etcd | EnsureMasterInit/Join |
| **节点组件** | kubelet, kube-proxy | EnsureMasterInit/Join/WorkerJoin |
| **网络组件** | CNI (Calico/Flannel) | EnsureAddonDeploy |
| **存储组件** | CSI | EnsureAddonDeploy |
| **监控组件** | Prometheus, Grafana | EnsureAddonDeploy |
| **日志组件** | Elasticsearch, Fluentd | EnsureAddonDeploy |
### 6.3 执行时间估算
| 阶段 | Phase | 预计时间 |
|------|-------|---------|
| Agent推送 | EnsureBKEAgent | 1-3分钟/节点 |
| 环境准备 | EnsureNodesEnv | 3-5分钟/节点 |
| Master初始化 | EnsureMasterInit | 5-10分钟 |
| Master加入 | EnsureMasterJoin | 3-5分钟/节点 |
| Worker加入 | EnsureWorkerJoin | 2-3分钟/节点 |
| Addon部署 | EnsureAddonDeploy | 5-15分钟 |


# openfuyao-system-controller 组件安装部署顺序与清单
## 一、整体架构概览
```
┌─────────────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster (管理集群)                   │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │            openfuyao-system-controller Namespace             │   │
│  │  ┌────────────────────────────────────────────────────────┐  │   │
│  │  │         openfuyao-system-controller (Deployment)       │  │   │
│  │  │  ┌──────────────────────────────────────────────────┐  │  │   │
│  │  │  │  InitContainer: Installer                        │  │  │   │
│  │  │  │  ├─ entrypoint.sh                                │  │  │   │
│  │  │  │  ├─ install.sh (安装)                            │  │  │   │
│  │  │  │  └─ uninstall.sh (卸载)                          │  │  │   │
│  │  │  └──────────────────────────────────────────────────┘  │  │   │
│  │  └────────────────────────────────────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                      │
│                              │ Helm/kubectl                         │
│                              ▼                                      │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                   openfuyao-system Namespace                 │   │
│  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐             │   │
│  │  │oauth-server │ │console-svc  │ │monitoring   │ ...         │   │
│  │  └─────────────┘ └─────────────┘ └─────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                     monitoring Namespace                     │   │
│  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐             │   │
│  │  │ prometheus  │ │alertmanager │ │node-exporter│ ...         │   │
│  │  └─────────────┘ └─────────────┘ └─────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    ingress-nginx Namespace                   │   │
│  │  ┌─────────────────────────────────────────────────────────┐ │   │
│  │  │              ingress-nginx-controller                   │ │   │
│  │  └─────────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```
## 二、组件安装顺序详解
根据 [install.sh:1926-1964](file:///D:\code\github\openfuyao-system-controller\install.sh#L1926) 的main函数，组件安装顺序如下：
### 2.1 安装流程总览
```
┌─────────────────────────────────────────────────────────────────────┐
│                    安装流程（按顺序执行）                            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  阶段0: 环境准备                                                     │
│    ├─ set_kubeconfig                 # 设置kubeconfig               │
│    ├─ kubectl create ns              # 创建命名空间                 │
│    ├─ install_yq                     # 安装yq工具                   │
│    ├─ install_jq                     # 安装jq工具                   │
│    ├─ install_helm                   # 安装helm                     │
│    ├─ install_cfssl                  # 安装cfssl证书工具            │
│    ├─ generate_var                   # 生成变量                     │
│    ├─ create_root_ca                 # 创建根CA证书                 │
│    ├─ add_helm_repo                  # 添加Helm仓库                 │
│    └─ reboot_pods                    # 重启Pod                      │
│                                                                      │
│  阶段1: 基础设施层                                                   │
│    ├─ install_ingress_nginx          # Ingress控制器                │
│    └─ install_helm_chart_repository  # 本地Harbor镜像仓库           │
│                                                                      │
│  阶段2: 监控层                                                       │
│    ├─ install_kube_prometheus        # Prometheus监控栈             │
│    └─ install_monitoring_service     # 监控服务                     │
│                                                                      │
│  阶段3: 业务层                                                       │
│    ├─ install_console_website        # 控制台前端                   │
│    ├─ install_console_service        # 控制台服务                   │
│    ├─ install_marketplace_service    # 应用市场服务                 │
│    ├─ install_application_management_service  # 应用管理服务        │
│    ├─ install_oauth_webhook_and_oauth_server  # OAuth认证服务       │
│    ├─ install_plugin_management_service       # 插件管理服务        │
│    ├─ install_user_management_operator        # 用户管理Operator    │
│    └─ install_web_terminal_service            # Web终端服务         │
│                                                                      │
│  阶段4: 应用层                                                       │
│    ├─ install_installer_website      # 安装向导前端                 │
│    ├─ install_installer_service      # 安装向导服务                 │
│    └─ install_metrics_server         # Metrics Server               │
│                                                                      │
│  阶段5: 后置处理                                                     │
│    ├─ create_default_user            # 创建默认用户                 │
│    └─ postinstall.sh                 # 后置脚本                     │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```
## 三、详细组件清单
### 3.1 阶段0：环境准备
| 序号 | 组件/工具 | 说明 | 安装方式 |
|-----|----------|------|---------|
| 1 | kubeconfig | 设置Kubernetes访问配置 | 环境变量设置 |
| 2 | openfuyao-system namespace | 管理面命名空间 | kubectl create ns |
| 3 | yq | YAML处理工具 | 二进制安装 |
| 4 | jq | JSON处理工具 | 二进制安装 |
| 5 | helm | Kubernetes包管理器 | 二进制安装 |
| 6 | cfssl | CloudFlare SSL证书工具 | 二进制安装 |
| 7 | Root CA | 根证书颁发机构 | cfssl生成 |
| 8 | Helm Repo | Helm仓库配置 | helm repo add |
### 3.2 阶段1：基础设施层
#### 3.2.1 Ingress-Nginx
**文件位置**：[resource/ingress-nginx/ingress-nginx.yaml](file:///D:\code\github\openfuyao-system-controller\resource\ingress-nginx\ingress-nginx.yaml)

**安装函数**：[install_ingress_nginx()](file:///D:\code\github\openfuyao-system-controller\install.sh#L1275)

| 属性 | 值 |
|------|-----|
| 命名空间 | ingress-nginx |
| 类型 | DaemonSet |
| 组件 | ingress-nginx-controller |
| 功能 | 集群入口流量管理、TLS终止、负载均衡 |

**安装内容**：
```
ingress-nginx/
├─ ingress-nginx-controller    # Ingress控制器
├─ ingress-nginx-service       # Service (NodePort/LoadBalancer)
├─ ingress-nginx-tls-secret    # TLS证书Secret
└─ ingress-nginx-front-tls     # 前端TLS证书
```
#### 3.2.2 Local Harbor
**安装函数**：[install_helm_chart_repository()](file:///D:\code\github\openfuyao-system-controller\install.sh#L478)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| Chart版本 | 1.11.4 |
| 镜像版本 | v2.7.0 |
| 功能 | 本地镜像仓库、Helm Chart仓库 |

**安装内容**：
```
local-harbor/
├─ harbor-core              # Harbor核心服务
├─ harbor-portal            # Harbor Web界面
├─ harbor-registry          # 镜像仓库
├─ harbor-chartmuseum       # Chart仓库
├─ harbor-jobservice        # Job服务
├─ harbor-database          # PostgreSQL数据库
├─ harbor-redis             # Redis缓存
└─ harbor-trivy             # 镜像扫描服务
```

**存储配置**：
```bash
HARBOR_REGISTRY_PV_SIZE="10Gi"
HARBOR_JOBSERVICE_PV_SIZE="10Gi"
HARBOR_DATABASE_PV_SIZE="10Gi"
HARBOR_CHARTMUSEUM_PV_SIZE="10Gi"
HARBOR_REDIS_PV_SIZE="10Gi"
```
### 3.3 阶段2：监控层
#### 3.3.1 Kube-Prometheus
**文件位置**：[resource/kube-prometheus/](file:///D:\code\github\openfuyao-system-controller\resource\kube-prometheus)

**安装函数**：[install_kube_prometheus()](file:///D:\code\github\openfuyao-system-controller\install.sh#L628)

| 属性 | 值 |
|------|-----|
| 命名空间 | monitoring |
| 类型 | 多资源部署 |
| 功能 | 集群监控、告警、可视化 |

**安装内容**：
```
kube-prometheus/
├─ setup/                           # CRD定义
│   ├─ prometheusCustomResourceDefinition.yaml
│   ├─ alertmanagerCustomResourceDefinition.yaml
│   ├─ servicemonitorCustomResourceDefinition.yaml
│   └─ ... (其他CRD)
│
├─ prometheusOperator/              # Prometheus Operator
│   ├─ deployment.yaml
│   ├─ service.yaml
│   ├─ clusterRole.yaml
│   └─ serviceMonitor.yaml
│
├─ prometheus/                      # Prometheus实例
│   ├─ prometheus.yaml
│   ├─ service.yaml
│   ├─ clusterRole.yaml
│   └─ serviceMonitor.yaml
│
├─ alertmanager/                    # Alertmanager
│   ├─ alertmanager.yaml
│   ├─ service.yaml
│   ├─ secret.yaml
│   └─ serviceMonitor.yaml
│
├─ nodeExporter/                    # Node Exporter
│   ├─ daemonset.yaml
│   ├─ service.yaml
│   └─ serviceMonitor.yaml
│
├─ kubeStateMetrics/                # Kube State Metrics
│   ├─ deployment.yaml
│   ├─ service.yaml
│   └─ serviceMonitor.yaml
│
├─ blackboxExporter/                # Blackbox Exporter
│   ├─ deployment.yaml
│   ├─ service.yaml
│   └─ serviceMonitor.yaml
│
└─ kubernetes-components-service/   # Kubernetes组件监控
    ├─ etcd-service.yaml
    ├─ kube-apiserver-service.yaml
    ├─ kube-controller-manager-service.yaml
    ├─ kube-scheduler-service.yaml
    └─ kube-proxy-service.yaml
```
**监控组件清单**：

| 组件 | 类型 | 说明 |
|------|------|------|
| prometheus-operator | Deployment | Prometheus管理器 |
| prometheus | StatefulSet | 时序数据库 |
| alertmanager | StatefulSet | 告警管理器 |
| node-exporter | DaemonSet | 节点指标采集 |
| kube-state-metrics | Deployment | K8s对象指标 |
| blackbox-exporter | Deployment | 黑盒探测 |
| grafana | Deployment | 可视化面板 (可选) |
#### 3.3.2 Monitoring Service
**安装函数**：[install_monitoring_service()](file:///D:\code\github\openfuyao-system-controller\install.sh#L1100)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 监控服务API、查询代理 |

**安装内容**：
```
monitoring-service/
├─ monitoring-service        # 监控服务
├─ oauth-proxy               # OAuth代理
└─ service                   # Service
```
### 3.4 阶段3：业务层
#### 3.4.1 Console Website
**安装函数**：[install_console_website()](file:///D:\code\github\openfuyao-system-controller\install.sh#L138)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 控制台前端界面 |
#### 3.4.2 Console Service
**安装函数**：[install_console_service()](file:///D:\code\github\openfuyao-system-controller\install.sh#L186)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 控制台后端API服务 |

**依赖服务**：
```
serverHost:
  monitoring: "http://monitoring-service.openfuyao-system.svc.cluster.local:80"
  consoleWebsite: "http://console-website.openfuyao-system.svc.cluster.local:80"
```

**安全配置**：
```bash
symmetricKey:
  tokenKey: "$(openssl rand -base64 32)"
  secretKey: "$(openssl rand -base64 32)"
```
#### 3.4.3 Marketplace Service
**安装函数**：[install_marketplace_service()](file:///D:\code\github\openfuyao-system-controller\install.sh#L1130)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 应用市场服务、模板管理 |

**配置**：
```yaml
enableOAuth: true
config:
  enableHttps: false
  insecureSkipVerify: true
images:
  core:
    repository: "${REGISTRY}/marketplace-service"
  oauthProxy:
    repository: "${REGISTRY}/oauth-proxy"
```
#### 3.4.4 Application Management Service
**安装函数**：[install_application_management_service()](file:///D:\code\github\openfuyao-system-controller\install.sh#L1195)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 应用生命周期管理 |
#### 3.4.5 OAuth Webhook & OAuth Server
**安装函数**：[install_oauth_webhook_and_oauth_server()](file:///D:\code\github\openfuyao-system-controller\install.sh#L734)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | Kubernetes认证集成、OAuth2服务 |

**关键步骤**：
```
OAuth安装流程：
├─ 1. generate_oauth_webhook_tls_cert()  # 生成Webhook证书
├─ 2. modify_kubernetes_manifests()      # 修改kube-apiserver配置
│     └─ 添加 --authentication-token-webhook-config-file
├─ 3. install_oauth_webhook()            # 安装Webhook
└─ 4. install_oauth_server()             # 安装OAuth Server
```

**OAuth Webhook配置**：
```yaml
# /etc/kubernetes/webhook/auth-webhook-config.yaml
apiVersion: v1
kind: Config
clusters:
  - name: oauth-webhook
    cluster:
      server: https://oauth-webhook.openfuyao-system.svc.cluster.local:443/webhook
```

**OAuth Server配置**：
```bash
signing_key: "$(openssl rand -base64 32)"
encryption_key: "$(openssl rand -base64 32)"
jwt_private_key: "$(openssl rand -base64 64)"
```
#### 3.4.6 Plugin Management Service
**安装函数**：[install_plugin_management_service()](file:///D:\code\github\openfuyao-system-controller\install.sh#L1175)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 插件管理、插件生命周期 |
#### 3.4.7 User Management Operator
**安装函数**：[install_user_management_operator()](file:///D:\code\github\openfuyao-system-controller\install.sh#L1225)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 用户管理Operator、默认用户创建 |

**默认用户配置**：
```yaml
# resource/user-manager/default-user.yaml
apiVersion: user.openfuyao.cn/v1beta1
kind: User
metadata:
  name: admin
spec:
  username: admin
  role: admin
```
#### 3.4.8 Web Terminal Service
**安装函数**：[install_web_terminal_service()](file:///D:\code\github\openfuyao-system-controller\install.sh#L1250)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | Web终端、kubectl访问 |
### 3.5 阶段4：应用层
#### 3.5.1 Installer Website
**安装函数**：[install_installer_website()](file:///D:\code\github\openfuyao-system-controller\install.sh#L46)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 安装向导前端界面 |
| 前置条件 | bke-controller-manager和capi-controller-manager已运行 |

**检查条件**：
```bash
if kubectl get pods -n cluster-system | grep -q "bke-controller-manager" && \
   kubectl get pods -n cluster-system | grep -q "capi-controller-manager"; then
    # 继续安装
fi
```
#### 3.5.2 Installer Service
**安装函数**：[install_installer_service()](file:///D:\code\github\openfuyao-system-controller\install.sh#L85)

| 属性 | 值 |
|------|-----|
| 命名空间 | openfuyao-system |
| 类型 | Helm Chart |
| 功能 | 安装向导后端服务 |
| 前置条件 | bke-controller-manager和capi-controller-manager已运行 |
#### 3.5.3 Metrics Server
**文件位置**：[resource/metrics-server/metrics-server.yaml](file:///D:\code\github\openfuyao-system-controller\resource\metrics-server\metrics-server.yaml)

**安装函数**：[install_metrics_server()](file:///D:\code\github\openfuyao-system-controller\install.sh#L575)

| 属性 | 值 |
|------|-----|
| 命名空间 | kube-system |
| 类型 | Deployment |
| 功能 | 资源指标采集、HPA支持 |

**前置配置**：
```bash
# 创建front-proxy CA证书Secret
if [ -f "/etc/kubernetes/pki/front-proxy-ca.crt" ]; then
    kubectl create secret generic front-proxy-ca-cert \
        --from-file=front-proxy-ca.crt=/etc/kubernetes/pki/front-proxy-ca.crt \
        --namespace="kube-system"
fi
```
## 四、组件依赖关系图
```
┌──────────────────────────────────────────────────────────────────────┐
│                         组件依赖关系                                 │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                    应用层                                   │     │
│  │  installer-website ──────> installer-service                │     │
│  │          │                        │                         │     │
│  │          └────────────────────────┘                         │     │
│  │                    │                                        │     │
│  │                    │ 依赖 bke-controller-manager            │     │
│  │                    │ 依赖 capi-controller-manager           │     │
│  └─────────────────────────────────────────────────────────────┘     │
│                              ▲                                       │
│                              │                                       │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                    业务层                                   │     │
│  │  console-website ──────> console-service                    │     │
│  │          │                        │                         │     │
│  │          │                        ├──> monitoring-service   │     │
│  │          │                        ├──> oauth-server         │     │
│  │          │                        └──> local-harbor         │     │
│  │          │                                                  │     │
│  │  marketplace-service ───> oauth-proxy                       │     │
│  │  application-management ─> oauth-proxy                      │     │
│  │  plugin-management ──────> oauth-proxy                      │     │
│  │  web-terminal-service ───> kubectl-openfuyao                │     │
│  └─────────────────────────────────────────────────────────────┘     │
│                              ▲                                       │
│                              │                                       │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                    监控层                                   │     │
│  │  monitoring-service ──────> prometheus                      │     │
│  │          │                        │                         │     │
│  │          │                        ├──> node-exporter        │     │
│  │          │                        ├──> kube-state-metrics   │     │
│  │          │                        └──> alertmanager         │     │
│  │          │                                                  │     │
│  │  metrics-server ──────────> front-proxy-ca-cert             │     │
│  └─────────────────────────────────────────────────────────────┘     │
│                              ▲                                       │
│                              │                                       │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                    认证层                                   │     │
│  │  oauth-server ────────────> oauth-webhook                   │     │
│  │          │                        │                         │     │
│  │          │                        └──> kube-apiserver       │     │
│  │          │                            (webhook config)      │     │
│  │          │                                                  │     │
│  │  user-management-operator ─> default-user                   │     │
│  └─────────────────────────────────────────────────────────────┘     │
│                              ▲                                       │
│                              │                                       │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                 基础设施层                                  │     │
│  │  ingress-nginx ───────────> TLS证书                         │     │
│  │          │                                                  │     │
│  │  local-harbor ────────────> PV/PVC                          │     │
│  │          │                        │                         │     │
│  │          │                        ├──> registry (10Gi)      │     │
│  │          │                        ├──> chartmuseum (10Gi)   │     │
│  │          │                        ├──> database (10Gi)      │     │
│  │          │                        └──> redis (10Gi)         │     │
│  └─────────────────────────────────────────────────────────────┘     │
│                              ▲                                       │
│                              │                                       │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                    环境准备                                 │     │
│  │  yq, jq, helm, cfssl ─────> 工具链                          │     │
│  │  Root CA ─────────────────> 证书体系                        │     │
│  │  Helm Repo ───────────────> 仓库配置                        │     │
│  └─────────────────────────────────────────────────────────────┘     │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```
## 五、命名空间规划
| 命名空间 | 用途 | 组件数量 |
|----------|------|---------|
| openfuyao-system-controller | 控制器部署 | 1 (openfuyao-system-controller) |
| openfuyao-system | 管理面服务 | 15+ (所有业务服务) |
| monitoring | 监控系统 | 6+ (Prometheus栈) |
| ingress-nginx | Ingress控制器 | 1 (ingress-nginx-controller) |
| kube-system | 系统组件 | 1 (metrics-server) |
| session-secret | Session存储 | 0 (仅命名空间) |
## 六、镜像清单
### 6.1 核心镜像
| 镜像名称 | 版本 | 说明 |
|----------|------|------|
| openfuyao-system-controller | latest | 控制器镜像 |
| console-service | latest | 控制台服务 |
| console-website | latest | 控制台前端 |
| oauth-server | latest | OAuth服务 |
| oauth-webhook | latest | OAuth Webhook |
| monitoring-service | latest | 监控服务 |
| marketplace-service | latest | 应用市场服务 |
| application-management-service | latest | 应用管理服务 |
| plugin-management-service | latest | 插件管理服务 |
| user-management-operator | latest | 用户管理Operator |
| web-terminal-service | latest | Web终端服务 |
| installer-website | latest | 安装向导前端 |
| installer-service | latest | 安装向导服务 |
| oauth-proxy | latest | OAuth代理 |
| kubectl-openfuyao | latest | Kubectl工具镜像 |
### 6.2 第三方镜像
| 镜像名称 | 版本 | 说明 |
|----------|------|------|
| harbor | v2.7.0 | Harbor镜像仓库 |
| busybox | 1.36.1 | 基础工具镜像 |
| prometheus | latest | Prometheus |
| alertmanager | latest | Alertmanager |
| node-exporter | latest | Node Exporter |
| kube-state-metrics | latest | Kube State Metrics |
| prometheus-operator | latest | Prometheus Operator |
| configmap-reload | latest | ConfigMap重载 |
| kube-rbac-proxy | latest | RBAC代理 |
| blackbox-exporter | latest | Blackbox Exporter |
| ingress-nginx-controller | latest | Ingress控制器 |
## 七、存储需求
### 7.1 Harbor存储
| 组件 | PV大小 | PVC大小 | 存储类 |
|------|--------|---------|--------|
| Registry | 10Gi | 10Gi | Local/Default |
| Chartmuseum | 10Gi | 10Gi | Local/Default |
| Database | 10Gi | 10Gi | Local/Default |
| Redis | 10Gi | 10Gi | Local/Default |
| Jobservice | 10Gi | 10Gi | Local/Default |
### 7.2 Prometheus存储
| 组件 | 存储大小 | 说明 |
|------|---------|------|
| Prometheus | 50Gi (默认) | 时序数据存储 |
| Alertmanager | 10Gi (默认) | 告警数据存储 |
## 八、网络配置
### 8.1 Service地址
```bash
LOCAL_HARBOR_HOST="https://local-harbor.openfuyao-system.svc.cluster.local"
OAUTH_SERVER_HOST="https://oauth-server.openfuyao-system.svc.cluster.local:9096"
CONSOLE_SERVICE_HOST="https://console-service.openfuyao-system.svc.cluster.local:443"
MONITORING_HOST="https://monitoring-service.openfuyao-system.svc.cluster.local:443"
CONSOLE_WEBSITE_HOST="https://console-website.openfuyao-system.svc.cluster.local:80"
```
### 8.2 Ingress配置
所有服务通过Ingress-Nginx暴露，支持：
- HTTP/HTTPS
- TLS终止
- 基于域名的路由
- OAuth认证集成
## 九、安全配置
### 9.1 证书体系
```
证书层次结构：
├─ Root CA (openfuyao-system-root-ca)
│   ├─ ingress-nginx-tls
│   ├─ ingress-nginx-front-tls
│   ├─ oauth-webhook-tls
│   ├─ console-service-tls
│   ├─ console-website-tls
│   ├─ monitoring-service-tls
│   └─ ... (其他服务证书)
```
### 9.2 密钥管理
| Secret名称 | 命名空间 | 用途 |
|-----------|---------|------|
| harbor-credentials | openfuyao-system-controller | Harbor凭据 |
| openfuyao-system-root-ca | openfuyao-system | 根CA证书 |
| ingress-nginx-tls | ingress-nginx | Ingress TLS |
| etcd-certs | monitoring | Etcd证书 |
| front-proxy-ca-cert | kube-system | Front Proxy CA |
## 十、总结
### 10.1 安装特点
1. **分层架构**：基础设施层→认证层→监控层→业务层→应用层
2. **依赖管理**：严格按照依赖顺序安装组件
3. **幂等性**：每个组件安装前检查是否已安装
4. **证书自动化**：自动生成和管理服务证书
5. **在线/离线支持**：支持在线和离线两种部署模式
### 10.2 关键指标
| 指标 | 数值 |
|------|------|
| 总组件数 | 20+ |
| 总命名空间 | 6 |
| 总镜像数 | 25+ |
| 总存储需求 | 80Gi+ |
| 预计安装时间 | 30-60分钟 |
### 10.3 安装时间估算
| 阶段 | 组件数 | 预计时间 |
|------|--------|---------|
| 环境准备 | 8 | 5分钟 |
| 基础设施层 | 2 | 10分钟 |
| 监控层 | 3 | 10分钟 |
| 业务层 | 8 | 15分钟 |
| 应用层 | 3 | 5分钟 |
| 后置处理 | 2 | 5分钟 |
| **总计** | **26** | **50分钟** |
