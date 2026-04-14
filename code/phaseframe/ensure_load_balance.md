# EnsureLoadBalance 业务流程梳理
## 一、Phase 概览
[ensure_load_balance.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go) 是 BKECluster 控制器中的一个 Phase，负责为集群配置 **控制平面高可用负载均衡**，通过 HAProxy + Keepalived 实现 VIP 漂移和 API Server 负载分发。
## 二、整体业务流程
```
┌──────────────────────────────────────────────────────────────────────┐
│                    EnsureLoadBalance.Execute()                       │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │              ConfiguringLoadBalancer()                       │    │
│  │                                                              │    │
│  │  1. 获取所有节点，筛选 Master 节点                           │    │
│  │  2. 检查所有 Master 节点 Agent 是否就绪                      │    │
│  │  3. 判断是否需要配置外部负载均衡器                           │    │
│  │     ├── 是 → configureExternalLoadBalancer()                 │    │
│  │     │         ├── createLoadBalancerCommand()                │    │
│  │     │         └── executeAndHandleLoadBalancer()             │    │
│  │     └── 否 → 标记 Ready=true，跳过负载均衡配置               │    │
│  └──────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────┘
```
## 三、详细流程分析
### 3.1 NeedExecute —— 判断是否需要执行
```
NeedExecute(old, new)
│
├── DefaultNeedExecute() 返回 false → 不执行
│
├── Status.Ready == false → 需要执行（集群尚未就绪）
│
├── ControlPlaneEndpoint.Host 是某个节点 IP → 不执行
│   （说明使用单节点 API，无需负载均衡）
│
├── 存在 master 节点变更（新增/删除）→ 需要执行
│   GetNeedLoadBalanceNodesWithBKENodes()
│   = merge(GetNeedJoinMasterNodes, GetNeedDeleteMasterNodes)
│
└── 否则 → 不执行
```
关键判断函数 [AvailableLoadBalancerEndPoint](file:///d:/code/github/cluster-api-provider-bke/utils/capbke/clusterutil/util.go#L28)：
```go
func AvailableLoadBalancerEndPoint(endPoint APIEndpoint, nodes Nodes) bool {
    // Endpoint 有效 + Host 不在任何节点 IP 中 → 需要外部负载均衡
    if endPoint.IsValid() {
        if nodes.Filter(FilterOptions{"IP": host}).Length() == 0 {
            return true  // VIP 不属于任何节点 → 需要配置 HA
        }
    }
    return false  // VIP 在节点上 → 不需要 HA
}
```
### 3.2 ConfiguringLoadBalancer —— 核心配置流程
```
ConfiguringLoadBalancer()
│
├── Step 1: 获取 Master 节点列表
│   allNodes → nodes.Master()
│   若无 Master 节点 → 返回错误
│
├── Step 2: 检查 Master 节点 Agent 就绪状态
│   逐节点检查 NodeEnvFlag
│   未就绪 → 返回错误 "master node X agent is not ready"
│
├── Step 3: 判断负载均衡类型
│   ├── AvailableLoadBalancerEndPoint() == true
│   │   且 无 extraLoadBalanceIP
│   │   → configureExternalLoadBalancer()  // 配置 HAProxy+Keepalived
│   │
│   └── 否则
│       → 标记 ControlPlaneEndPointSetCondition=True
│       → 标记 Ready=true（使用已有外部 LB 或单节点模式）
│
└── Step 4: SyncStatusUntilComplete() 同步状态
```
### 3.3 configureExternalLoadBalancer —— 配置外部负载均衡
```
configureExternalLoadBalancer(nodes)
│
├── createLoadBalancerCommand(nodes)
│   │
│   │  构建 command.HA 对象：
│   │  ├── MasterNodes: Master 节点列表
│   │  ├── ControlPlaneEndpointPort: API Server 端口
│   │  ├── ControlPlaneEndpointVIP: VIP 地址
│   │  ├── ThirdImageRepo: 第三方镜像仓库（HAProxy）
│   │  ├── FuyaoImageRepo: Fuyao 镜像仓库（Keepalived）
│   │  ├── ManifestsDir: Static Pod 清单目录
│   │  └── VirtualRouterId: VRRP 虚拟路由 ID
│   │
│   └── loadBalanceCommand.New() → 创建 Command CR
│
└── executeAndHandleLoadBalancer(loadBalanceCommand)
    │
    ├── loadBalanceCommand.Wait() → 等待命令执行完成
    │   返回 successNodes, failedNodes
    │
    ├── 处理失败节点
    │   SetNodeState(NodeInitFailed, "Failed to configure load balancer")
    │
    ├── 处理成功节点
    │   SetNodeState(NodeInitializing, "Load balancer configured")
    │   MarkNodeStateFlag(NodeHAFlag)  ← 标记 HA 已配置
    │
    ├── SyncStatusUntilComplete()
    │
    └── 判断结果
        ├── 有失败节点 → 返回错误
        └── 全部成功 →
            ConditionMark(ControlPlaneEndPointSetCondition=True)
            Status.Ready = true
```
## 四、Agent 端 HA 插件执行流程
当 Command CR 创建后，BKEAgent 在各 Master 节点上执行 [HA 插件](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/ha/ha.go)：
```
HA.Execute(commands)
│
├── Step 1: initIPVS() — 加载内核模块
│   调用 K8sEnvInit 插件，scope=kernel
│   加载 ip_vs, ip_vs_wrr 等内核模块
│
├── Step 2: prepareRendCfg() — 准备渲染配置
│   ├── 解析 haNodes（hostname:IP 列表）
│   ├── findVIPInterface() — 查找本机 VIP 所在网卡
│   ├── 设置 isMasterHa 标志
│   └── 设置 keepalived 参数（advertInt, authPass）
│
├── Step 3: 获取 HA 组件列表
│   ├── Master HA → [HAProxy, Keepalived]
│   └── Ingress HA → [Keepalived]
│
├── Step 4: GenerateHAManifestYaml() — 渲染并写入文件
│   │
│   ├── HAProxy 组件渲染
│   │   ├── haproxy.cfg.tmpl → /etc/openFuyao/haproxy/haproxy.cfg
│   │   │   配置前端监听 controlPlaneEndpointPort
│   │   │   后端 roundrobin 到各 Master 的 6443 端口
│   │   │   健康检查: GET /healthz (HTTPS)
│   │   └── haproxy.yaml.tmpl → {manifestsDir}/haproxy.yaml
│   │       Static Pod: 使用第三方镜像仓库的 HAProxy 镜像
│   │
│   └── Keepalived 组件渲染
│       ├── check-master.sh.tmpl → /etc/openFuyao/keepalived/check-master.sh
│       │   健康检查脚本：curl https://localhost:{port} 和 https://{vip}:{port}
│       ├── keepalived.master.conf.tmpl → 实例配置
│       │   VRRP 实例：MASTER/BACKUP 选举
│       │   priority: MASTER=100, BACKUP 递减
│       │   virtual_ipaddress: VIP 地址
│       │   track_script: check_apiserver
│       ├── keepalived.base.conf.tmpl → 基础配置
│       ├── keepalived.conf → /etc/openFuyao/keepalived/keepalived.conf
│       └── keepalived.yaml.tmpl → {manifestsDir}/keepalived.yaml
│           Static Pod: 使用 Fuyao 镜像仓库的 Keepalived 镜像
│           securityContext: NET_ADMIN, NET_BROADCAST, NET_RAW
│
└── Step 5: Wait() — 等待 VIP 就绪（可选）
    仅 MASTER 节点等待
    轮询检查 VIP 是否绑定到本机网卡
    超时: 5 分钟
```
## 五、生成的文件清单
| 文件 | 路径 | 来源模板 | 说明 |
|------|------|---------|------|
| HAProxy 配置 | `/etc/openFuyao/haproxy/haproxy.cfg` | [haproxy.cfg.tmpl](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/tmpl/haproxy/haproxy.cfg.tmpl) | 前端监听+后端负载分发 |
| HAProxy Pod | `{manifestsDir}/haproxy.yaml` | [haproxy.yaml.tmpl](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/tmpl/haproxy/haproxy.yaml.tmpl) | Static Pod 清单 |
| Keepalived 检查脚本 | `/etc/openFuyao/keepalived/check-master.sh` | [check-master.sh.tmpl](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/tmpl/keepalived/check-master.sh.tmpl) | API Server 健康检查 |
| Keepalived 实例配置 | 合并到 keepalived.conf | [keepalived.master.conf.tmpl](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/tmpl/keepalived/keepalived.master.conf.tmpl) | VRRP 实例+VIP |
| Keepalived 基础配置 | 合并到 keepalived.conf | keepalived.base.conf.tmpl | 全局配置 |
| Keepalived 主配置 | `/etc/openFuyao/keepalived/keepalived.conf` | — | 最终合并配置 |
| Keepalived Pod | `{manifestsDir}/keepalived.yaml` | [keepalived.yaml.tmpl](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/tmpl/keepalived/keepalived.yaml.tmpl) | Static Pod 清单 |
## 六、Keepalived MASTER/BACKUP 选举机制
[KeepalivedInstanceIsMaster](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/render.go#L601) 的选举逻辑：
```go
func KeepalivedInstanceIsMaster(nodes []HANode) bool {
    ips, _ := bkenet.GetAllInterfaceIP()
    // 如果本机 IP 等于节点列表中第一个节点的 IP → MASTER
    for _, ip := range ips {
        if strings.Contains(ip, nodes[0].IP) {
            return true
        }
    }
    return false
}
```
- **MASTER**：节点列表中的第一个节点（`nodes[0]`），priority=100
- **BACKUP**：其余节点，priority 递减（100 - 递减步长 × 索引）
- **weight**：`节点数 × 倍数`，用于 VRRP 脚本权重
- **virtual_router_id**：默认 51，可通过 `masterVirtualRouterId` 自定义
## 七、HAProxy 负载分发机制
[haproxy.cfg.tmpl](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/tmpl/haproxy/haproxy.cfg.tmpl) 的核心配置：
```
frontend apiserver
    bind *:{controlPlaneEndpointPort}    ← 监听 VIP 端口
    mode tcp
    default_backend apiserver

backend apiserver
    option httpchk GET /healthz          ← HTTPS 健康检查
    http-check expect status 200
    balance roundrobin                   ← 轮询负载均衡
    server {hostname} {IP}:6443 check    ← 各 Master 节点后端
```
流量路径：`Client → VIP:{port} → HAProxy → roundrobin → Master:6443`
## 八、业务流程总结图
```
                          Controller 侧                              Agent 侧
                     ────────────────────────                   ────────────────────────
                    │                        │                 │                        │
  BKECluster CR ──→ │ NeedExecute()          │                 │                        │
                    │  ├ Ready=false?        │                 │                        │
                    │  ├ VIP 非节点 IP?       │                 │                        │
                    │  └ Master 有变更?       │                 │                        │
                    │          ↓ Yes          │                 │                        │
                    │ ConfiguringLoadBalancer │                 │                        │
                    │  ├ 获取 Master 节点     │                 │                        │
                    │  ├ 检查 Agent 就绪      │                 │                        │
                    │  ├ 判断 LB 类型         │                 │                        │
                    │  │  ├ 外部 LB ──────────┼── Command CR ──→│ HA Plugin Execute()    │
                    │  │  │                   │                 │  ├ 加载 ip_vs 内核模块  │
                    │  │  │                   │                 │  ├ 查找 VIP 网卡        │
                    │  │  │                   │                 │  ├ 渲染 HAProxy 配置+Pod│
                    │  │  │                   │                 │  ├ 渲染 Keepalived 配置 │
                    │  │  │                   │                 │  │  ├ check-master.sh   │
                    │  │  │                   │                 │  │  ├ VRRP 实例配置      │
                    │  │  │                   │                 │  │  └ keepalived.yaml   │
                    │  │  │                   │                 │  └ 可选: 等待 VIP 就绪   │
                    │  │  │  ← Wait 结果 ─────┼─────────────────│                        │
                    │  │  │                   │                 │                        │
                    │  │  ├ 处理成功/失败节点   │                │   Kubelet 监听 manifestsDir
                    │  │  ├ 标记 NodeHAFlag   │                 │   自动拉起 HAProxy + Keepalived
                    │  │  └ Ready=true        │                 │        ↓                │
                    │  │                      │                 │  VIP 绑定到 MASTER 节点  │
                    │  └ 内部/单节点 LB        │                 │  HAProxy 代理到各 Master │
                    │     Ready=true          │                 │                        │
                    └────────────────────────┘                  └────────────────────────┘
```
## 九、关键设计要点
1. **双模式判断**：根据 `ControlPlaneEndpoint.Host` 是否为节点 IP，自动判断是否需要配置 HAProxy+Keepalived
2. **Static Pod 模式**：HAProxy 和 Keepalived 以 Static Pod 方式运行，由 Kubelet 直接管理，不依赖集群 API
3. **配置与清单分离**：HAProxy/Keepalived 的配置文件写入 `/etc/openFuyao/` 目录，Pod 清单写入 `manifestsDir`，通过 Volume 挂载关联
4. **滚动更新支持**：当 Master 节点发生变更（新增/删除）时，NeedExecute 返回 true，触发重新配置 HA
5. **VIP 健康检查**：Keepalived 通过 `check-master.sh` 检查本地 API Server 和 VIP 可达性，失败时降低优先级触发 VIP 漂移

      
#  HAProxy/Keepalived 安装在所有 Master 节点上
## HAProxy/Keepalived 安装在所有 Master 节点上
### 代码证据链
**1. Controller 侧——节点筛选**
[ensure_load_balance.go:104-106](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go#L104)：
```go
allNodes, _ := nodeFetcher.GetNodesForBKECluster(e.Ctx, bkeCluster)
nodes := allNodes.Master()  // ← 只取 Master 节点
```
**2. Command 构建——指定目标节点**
[ensure_load_balance.go:155](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go#L155)：
```go
loadBalanceCommand := command.HA{
    MasterNodes: nodes.Master(),  // ← Master 节点列表传入
    ...
}
```
**3. Agent 命令分发——NodeSelector 绑定**
[loadbalance.go:86](file:///d:/code/github/cluster-api-provider-bke/pkg/command/loadbalance.go#L86)：
```go
commandSpec.NodeSelector = getNodeSelector(l.MasterNodes)  // ← 命令只派发到 Master 节点
```
**4. HA 插件参数——haNodes 包含所有 Master**
[loadbalance.go:95-97](file:///d:/code/github/cluster-api-provider-bke/pkg/command/loadbalance.go#L95)：
```go
haNodesParam := l.getHaNodesParam(l.MasterNodes)  // ← "hostname:IP,hostname:IP,..."
```
### 安装拓扑示意
```
                    ┌─────────────────┐
                    │   VIP (浮动)     │
                    │  ControlPlane   │
                    │  Endpoint.Host  │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
    ┌─────────▼────────┐ ┌──▼───────────┐ ┌▼──────────────┐
    │   Master Node 1  │ │ Master Node 2│ │ Master Node 3 │
    │                  │ │              │ │               │
    │  ┌────────────┐  │ │ ┌──────────┐ │ │ ┌───────────┐ │
    │  │  HAProxy   │  │ │ │ HAProxy  │ │ │ │ HAProxy   │ │
    │  │  (Static   │  │ │ │ (Static  │ │ │ │ (Static   │ │
    │  │   Pod)     │  │ │ │  Pod)    │ │ │ │  Pod)     │ │
    │  └────────────┘  │ │ └──────────┘ │ │ └───────────┘ │
    │  ┌────────────┐  │ │ ┌──────────┐ │ │ ┌───────────┐ │
    │  │ Keepalived │  │ │ │Keepalived│ │ │ │Keepalived │ │
    │  │  MASTER    │  │ │ │ BACKUP   │ │ │ │ BACKUP    │ │
    │  │ priority=100│ │ │ │priority=90│ │ │ │priority=80│ │
    │  │ VIP ← 绑定  │ │ │ │          │ │ │ │           │ │
    │  └────────────┘  │ │ └──────────┘ │ │ └───────────┘ │
    │       ↓          │ │      ↓       │ │      ↓        │
    │  :6443 ──────────┼─┼── :6443 ────┼─┼── :6443       │
    │  kube-apiserver  │ │ kube-apiserver│ │ kube-apiserver│
    └──────────────────┘ └──────────────┘ └───────────────┘
```
### 关键设计要点
| 要点 | 说明 |
|------|------|
| **安装范围** | 所有 Role 包含 `master` 的节点 |
| **安装方式** | Static Pod（由 Kubelet 监听 manifestsDir 自动拉起） |
| **HAProxy 角色** | 每个 Master 上都运行，将 `VIP:port` 流量 roundrobin 到所有 Master 的 6443 |
| **Keepalived 角色** | MASTER/BACKUP 选举，VIP 绑定在 MASTER 节点上 |
| **故障转移** | MASTER 故障时，BACKUP 中 priority 最高的接管 VIP |
| **非 Master 节点** | Worker 节点不安装 HAProxy/Keepalived，直接通过 VIP 访问 API Server |

# HAProxy/Keepalived 安装范围设计缺陷分析与重构建议
## 一、当前设计
```
安装范围: allNodes.Master() → 所有 Role 包含 "master" 的节点
```
核心代码 [ensure_load_balance.go:104](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go#L104)：
```go
nodes := allNodes.Master()
```
[node.go:127-131](file:///d:/code/github/cluster-api-provider-bke/common/cluster/node/node.go#L127) 中 `Master()` 的定义：
```go
func (n Nodes) Master() Nodes {
    master := n.Filter(FilterOptions{"Role": MasterNodeRole})        // role="master"
    masterWorker := n.Filter(FilterOptions{"Role": MasterWorkerNodeRole}) // role="master/node"
    return append(master, masterWorker...)
}
```
## 二、缺陷分析
### 缺陷 1：Master 与 etcd 角色解耦后，非 etcd 的 Master 节点也安装了 HAProxy/Keepalived
BKE 支持灵活的节点角色组合（`master`、`etcd`、`node`），但 HA 安装范围硬编码为 `Master()`，不考虑 etcd 角色分布：
```
场景：5 节点集群
  Node1: role=["master","etcd"]        ← 安装 HA ✓ 合理
  Node2: role=["master","etcd"]        ← 安装 HA ✓ 合理
  Node3: role=["master","etcd"]        ← 安装 HA ✓ 合理
  Node4: role=["master"]               ← 安装 HA ✗ 不合理！无 etcd，不承载控制平面
  Node5: role=["node"]                 ← 不安装 HA ✓

问题：Node4 不是 etcd 节点，其上可能没有完整的控制平面组件，
      但仍然安装了 HAProxy/Keepalived，浪费资源且 HAProxy 后端
      会把流量转发到 Node4:6443，而 Node4 可能无法提供稳定的 API 服务
```
### 缺陷 2：HAProxy 后端列表包含所有 Master，而非仅包含健康控制平面节点
[haproxy.cfg.tmpl](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/tmpl/haproxy/haproxy.cfg.tmpl) 中：
```
backend apiserver
    balance roundrobin
    {{- range $node := .nodes }}
    server {{ .Hostname }} {{ .IP }}:6443 check check-ssl verify none
    {{- end }}
```
`haNodes` 参数传入的是**所有 Master 节点**，HAProxy 会将流量 roundrobin 到所有 Master 的 6443 端口。如果某个 Master 节点尚未完成初始化（kube-apiserver 未运行），HAProxy 健康检查会失败但仍会消耗重试开销。
### 缺陷 3：Keepalived MASTER 选举基于节点列表顺序，而非节点健康状态
[render.go:601-611](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/render.go#L601)：
```go
func KeepalivedInstanceIsMaster(nodes []HANode) bool {
    // 固定取 nodes[0] 作为 MASTER
    for _, ip := range ips {
        if strings.Contains(ip, nodes[0].IP) {
            return true
        }
    }
    return false
}
```
- **问题**：MASTER 身份由节点在列表中的位置决定（第一个节点），而非节点实际负载或健康状态
- **风险**：如果 `nodes[0]` 对应的节点负载最高或最不稳定，VIP 仍会绑定在该节点上
- **无抢占**：当前配置没有 `nopreempt`，当原 MASTER 恢复后会抢占 VIP，可能造成不必要的切换
### 缺陷 4：Master 节点增删时全量重配置，无增量更新能力
[ensure_load_balance.go:82-89](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go#L82)：
```go
nodes := phaseutil.GetNeedLoadBalanceNodesWithBKENodes(e.Ctx, e.Ctx.Client, new, bkeNodes)
```
当有 Master 节点变更时，NeedExecute 返回 true，触发对所有 Master 节点的全量重配置。这意味着：
- 新增 1 个 Master → 所有 Master 都重新渲染 HAProxy/Keepalived 配置
- 删除 1 个 Master → 所有 Master 都重新渲染
- Keepalived 重启可能导致 VIP 短暂漂移
### 缺陷 5：无 HA 状态监控与自愈能力
- Keepalived 的 `check-master.sh` 只检查本地 API Server 可达性，不检查集群整体健康
- 没有 HAProxy 的 metrics 采集和监控
- VIP 丢失后没有自动恢复机制（仅依赖 Keepalived 自身 VRRP 协议）
- 缺少 HA 配置状态的条件上报（当前只有 `ControlPlaneEndPointSetCondition`）
### 缺陷 6：VirtualRouterId 默认值冲突风险
```go
"virtualRouterId": {Key: "virtualRouterId", Value: "51", Required: false, Default: "51"}
```
默认 VRID=51，在同一网段部署多个 BKE 集群时，VRRP 广播可能冲突。虽然 webhook 中有冲突检测逻辑，但默认值本身就有风险。
### 缺陷 7：安全配置不足
- Keepalived auth_pass 硬编码为 `22222222`
- HAProxy 以 root 运行（hostNetwork + 无 securityContext）
- Keepalived 拥有 NET_ADMIN/NET_RAW 等高权能，无进一步限制
## 三、重构建议
### 3.1 核心重构：HA 安装范围从 Master 改为 Etcd 节点
**设计理由**：BKE 采用 Stacked etcd 拓扑，etcd 节点必然运行完整的控制平面组件（apiserver/scheduler/controller-manager/etcd），是真正承载 API 服务的节点。纯 Master 节点（不含 etcd 角色）可能不运行完整的控制平面。
```go
// 当前
nodes := allNodes.Master()

// 重构后
nodes := allNodes.Etcd()
// 或更灵活：取 Master ∩ Etcd 交集
nodes := allNodes.Master().Filter(bkenode.FilterOptions{"Role": bkenode.EtcdNodeRole})
```
**影响分析**：

| 场景 | 当前行为 | 重构后行为 |
|------|---------|-----------|
| master+etcd (典型 3 节点) | 安装在 3 节点 | 安装在 3 节点（无变化） |
| master+etcd (5 节点) | 安装在 5 节点 | 安装在 5 节点（无变化） |
| 3 master+etcd + 2 pure master | 安装在 5 节点 | 安装在 3 节点（优化） |
| pure master (无 etcd) | 安装 HA ✗ | 不安装 ✓ |
### 3.2 HAProxy 后端列表优化：仅包含已就绪的控制平面节点
**当前**：haNodes = 所有 Master 节点
**重构**：haNodes = 已完成初始化的控制平面节点
```go
// 重构 createLoadBalancerCommand
func (e *EnsureLoadBalance) createLoadBalancerCommand(nodes bkenode.Nodes) (*command.HA, error) {
    // 仅包含已就绪的节点作为 HAProxy 后端
    readyNodes := e.filterReadyControlPlaneNodes(nodes)
    
    loadBalanceCommand := command.HA{
        // HA 安装范围：所有 etcd 节点（无论是否就绪）
        InstallNodes: nodes,
        // HAProxy 后端：仅已就绪的控制平面节点
        BackendNodes: readyNodes,
        ...
    }
}
```
对应 HA 插件参数拆分：
```
当前: haNodes=hostname:IP,hostname:IP,...  (安装范围 = 后端列表)
重构: installNodes=hostname:IP,...          (Keepalived 安装范围)
      backendNodes=hostname:IP,...          (HAProxy 后端列表)
```
### 3.3 Keepalived 选举策略优化
**方案 A：基于优先级的非抢占模式**
```
vrrp_instance apiserver {
    state BACKUP           ← 所有节点初始状态都是 BACKUP
    nopreempt              ← 禁止抢占
    priority {{ .priority }} ← 基于节点健康评分动态计算
}
```
**方案 B：基于 etcd 健康状态的动态优先级**
```go
// 重构 Keepalived 优先级计算
func computePriority(node HANode, etcdHealth bool) int {
    base := 100
    if !etcdHealth {
        base -= 50  // etcd 不健康，大幅降低优先级
    }
    // 可扩展：根据节点负载、延迟等调整
    return base
}
```
### 3.4 增量更新机制
**当前**：全量重渲染 → Keepalived 重启 → VIP 漂移
**重构**：检测配置变化 → 仅更新变化部分 → 优雅重载
```
HAProxy:  修改 haproxy.cfg → 发送 SIGUSR2 优雅重载（不中断连接）
Keepalived: 修改 keepalived.conf → 发送 SIGHUP 重载配置（VIP 不漂移）
Static Pod: 仅在 Pod YAML 变化时才重建
```
实现思路：
```go
// Agent 端 HA 插件增加配置比对
func (h *HA) Execute(commands []string) ([]string, error) {
    // 1. 渲染新配置到临时文件
    // 2. 比对新旧配置
    // 3. 如果只有 haproxy.cfg 变化 → SIGUSR2 重载
    // 4. 如果只有 keepalived.conf 变化 → SIGHUP 重载
    // 5. 如果 Pod YAML 变化 → 替换 Static Pod 文件
    // 6. 如果无变化 → 跳过
}
```
### 3.5 HA 状态监控与自愈
```go
// 新增 HA 健康检查 Phase 或增强现有 Phase
type EnsureLoadBalanceHealth struct {
    phaseframe.BasePhase
}

func (e *EnsureLoadBalanceHealth) Execute() (ctrl.Result, error) {
    // 1. 检查 VIP 是否在预期节点上
    // 2. 检查 HAProxy 后端是否全部健康
    // 3. 检查 Keepalived 状态
    // 4. 异常时触发重新配置
}
```
### 3.6 安全加固
```yaml
# HAProxy Static Pod 安全加固
spec:
  containers:
  - name: haproxy
    securityContext:
      runAsUser: 99          # 非 root
      runAsGroup: 99
      readOnlyRootFilesystem: true
    resources:
      limits:
        memory: "128Mi"
        cpu: "200m"

# Keepalived auth_pass 动态生成
# 当前: auth_pass 22222222
# 重构: auth_pass {{ .keepalivedAuthPass }}  ← 集群级随机生成
```
## 四、重构优先级
| 优先级 | 缺陷 | 重构项 | 影响范围 | 风险 |
|--------|------|--------|---------|------|
| **P0** | 安装范围不合理 | HA 安装范围改为 Etcd 节点 | ensure_load_balance.go, loadbalance.go, ha.go | 低 |
| **P0** | 后端列表不精确 | HAProxy 后端仅包含就绪节点 | loadbalance.go, ha.go, haproxy.cfg.tmpl | 中 |
| **P1** | 全量重配置 | 增量更新 + 优雅重载 | ha.go | 中 |
| **P1** | 选举策略不合理 | 非抢占模式 + 动态优先级 | keepalived.master.conf.tmpl, render.go | 低 |
| **P2** | 安全配置不足 | 非 root 运行 + 随机 auth_pass | haproxy.yaml.tmpl, keepalived.master.conf.tmpl | 低 |
| **P2** | 无状态监控 | HA 健康检查 Phase | 新增 Phase | 中 |
| **P3** | VRID 冲突 | 集群级 VRID 分配器 | webhook, defaults.go | 低 |
## 五、P0 重构详细设计
### 5.1 安装范围重构
**涉及文件**：

| 文件 | 修改内容 |
|------|---------|
| [ensure_load_balance.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go) | `nodes.Master()` → `nodes.Etcd()` 或交集 |
| [loadbalance.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/loadbalance.go) | `MasterNodes` 字段语义拆分 |
| [ha.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/ha/ha.go) | 参数解析支持 installNodes/backendNodes |

**ensure_load_balance.go 修改**：
```go
// 当前
nodes := allNodes.Master()

// 重构方案 1：直接使用 Etcd 节点
nodes := allNodes.Etcd()

// 重构方案 2（更保守）：取 Master ∩ Etcd 交集
etcdNodes := allNodes.Etcd()
masterNodes := allNodes.Master()
nodes := masterNodes.Filter(bkenode.FilterOptions{"Role": bkenode.EtcdNodeRole})
// 若交集为空，回退到 Master 节点（兼容旧集群）
if len(nodes) == 0 {
    nodes = masterNodes
}
```
**loadbalance.go 修改**：
```go
type HA struct {
    BaseCommand
    InstallNodes  bkenode.Nodes  // Keepalived 安装范围（etcd 节点）
    BackendNodes  bkenode.Nodes  // HAProxy 后端列表（已就绪的控制平面节点）
    // ... 其他字段不变
}

func (l *HA) New() error {
    // ...
    if l.isMasterHa {
        l.setupMasterHACommand(commandSpec, ...)
        commandSpec.NodeSelector = getNodeSelector(l.InstallNodes)  // 安装范围
    }
    // ...
}

func (l *HA) setupMasterHACommand(...) {
    installNodesParam := l.getHaNodesParam(l.InstallNodes)   // Keepalived 节点
    backendNodesParam := l.getBackendNodesParam(l.BackendNodes) // HAProxy 后端
    // ...
    commandSpec.Commands = []agentv1beta1.ExecCommand{
        {
            Command: []string{
                "HA",
                installNodesParam,      // installNodes=...
                backendNodesParam,      // backendNodes=...
                // ...
            },
        },
    }
}
```
**ha.go 修改**：
```go
func (h *HA) Param() map[string]plugin.PluginParam {
    return map[string]plugin.PluginParam{
        // ...
        "installNodes": {Key: "installNodes", Value: "", Required: true, Description: "keepalived install nodes"},
        "backendNodes": {Key: "backendNodes", Value: "", Required: true, Description: "haproxy backend nodes"},
        // 保留 haNodes 兼容旧版本
        "haNodes": {Key: "haNodes", Value: "", Required: false, Description: "deprecated, use installNodes+backendNodes"},
    }
}

func (h *HA) Execute(commands []string) ([]string, error) {
    parseCommands, _ := plugin.ParseCommands(h, commands)
    
    // 兼容旧参数
    installNodesStr := parseCommands["installNodes"]
    backendNodesStr := parseCommands["backendNodes"]
    if installNodesStr == "" {
        installNodesStr = parseCommands["haNodes"]  // 回退
    }
    if backendNodesStr == "" {
        backendNodesStr = parseCommands["haNodes"]  // 回退
    }
    
    installNodes, _ := h.parseHANodes(installNodesStr)
    backendNodes, _ := h.parseHANodes(backendNodesStr)
    
    cfg["installNodes"] = installNodes  // Keepalived 使用
    cfg["backendNodes"] = backendNodes  // HAProxy 使用
    cfg["nodes"] = installNodes         // Keepalived 选举使用
    // ...
}
```
**haproxy.cfg.tmpl 修改**：
```
backend apiserver
    balance roundrobin
    {{- range $node := .backendNodes }}    ← 使用 backendNodes 替代 nodes
    server {{ .Hostname }} {{ .IP }}:6443 check check-ssl verify none
    {{- end }}
```
### 5.2 后端节点就绪过滤
在 Controller 侧过滤已就绪的控制平面节点：
```go
func (e *EnsureLoadBalance) filterReadyControlPlaneNodes(nodes bkenode.Nodes) bkenode.Nodes {
    var readyNodes bkenode.Nodes
    for _, node := range nodes {
        // 检查节点是否已完成 Master 初始化
        nodeStateFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(
            e.Ctx, bkeCluster, node.IP, bkev1beta1.MasterInitFlag)
        if nodeStateFlag {
            readyNodes = append(readyNodes, node)
        }
    }
    // 如果没有就绪节点，回退到全部节点（避免 HAProxy 无后端）
    if len(readyNodes) == 0 {
        return nodes
    }
    return readyNodes
}
```
## 六、重构风险与兼容性
| 风险点 | 影响 | 缓解措施 |
|--------|------|---------|
| 旧集群升级 | 已安装 HA 的纯 Master 节点需清理 | 增加迁移逻辑：检测旧 HA Pod → 清理非 etcd 节点上的 HA |
| 参数兼容性 | 旧 Agent 不识别 installNodes/backendNodes | 保留 haNodes 参数回退，Agent 版本检测 |
| VIP 漂移 | 重配置时 Keepalived 重启 | 增量更新 + SIGHUP 重载 |
| 单节点集群 | 1 Master 无 etcd 角色 | 回退逻辑：交集为空时使用 Master 节点 |

# 纠正：HA 安装范围不应改为 etcd 节点
## 一、我之前的错误
我建议将 HA 安装范围从 `Master()` 改为 `Etcd()`，这是**错误的**。原因如下：
### BKE 控制平面组件部署逻辑
通过代码追踪，BKE 的控制平面组件部署在**所有 Master 节点**上，而非仅 etcd 节点：
1. **Kubeadm 插件的 `initControlPlane()` 和 `joinControlPlane()`** 在 Master 节点上执行
2. **Static Pod 渲染**（[manifest.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/manifest.go#L82)）中，只有 etcd 组件做了角色过滤：
   ```go
   // 只有 etcd 做了角色检查
   if component.Name == Etcd && !flag {
       continue  // 非 etcd 节点跳过 etcd 渲染
   }
   // kube-apiserver、kube-controller-manager、kube-scheduler 在所有 Master 上都渲染
   ```
3. **组件列表**（[componentlist.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/componentlist.go#L75)）：
   - `GetDefaultComponentList()` = `[APIServer, Scheduler, Controller, Etcd]`
   - `GetComponentListWithOutEtcd()` = `[APIServer, Scheduler, Controller]`

**结论**：所有 Master 节点都运行 `kube-apiserver`，HAProxy 需要将流量转发到这些节点。因此 HA 安装在 Master 节点上是**正确的**。
## 二、当前设计的真正缺陷
既然"安装在 Master 节点上"本身是合理的，那真正的缺陷是什么？
### 缺陷 1：Master 角色与 etcd 角色可以分离，但 HA 没有考虑这种场景
BKE 支持 `master` 和 `etcd` 角色独立配置：
```
场景 A（典型 Stacked）: 3 节点，role=["master","etcd"]
  → 所有节点都运行 apiserver + etcd → HA 安装在 Master 上 ✓

场景 B（角色分离）: 
  Node1-3: role=["master","etcd"]     → 运行 apiserver + etcd
  Node4-5: role=["master"]            → 只运行 apiserver，不运行 etcd
  → HA 安装在所有 5 个 Master 上 ✓ 合理（因为都运行 apiserver）

场景 C（极端）:
  Node1-3: role=["etcd"]              → 只运行 etcd，不运行 apiserver
  Node4-5: role=["master"]            → 只运行 apiserver，不运行 etcd
  → HA 安装在 Node4-5 上 → 但 HAProxy 后端也只包含 Node4-5 ✓ 合理
```
**实际上，在 BKE 的 Stacked etcd 拓扑下，`master` 角色就意味着运行 apiserver**，所以当前设计在逻辑上是正确的。
### 缺陷 2（真正的缺陷）：HAProxy 后端列表 = Keepalived 安装范围，二者耦合
这才是核心问题。当前 `haNodes` 参数同时用于两个目的：
```
haNodes → Keepalived: 用于 MASTER/BACKUP 选举
haNodes → HAProxy:    用于后端服务器列表
```
但在某些场景下，这两个范围应该不同：

| 场景 | Keepalived 安装范围 | HAProxy 后端列表 |
|------|-------------------|-----------------|
| 典型 3 Master | 3 节点 | 3 节点 |
| 新增 Master 未就绪 | 3 节点（已安装 Keepalived） | 2 节点（新节点 apiserver 未就绪） |
| Master 正在删除 | 3 节点（Keepalived 仍运行） | 2 节点（待删除节点不应接收流量） |

**当前代码**：HAProxy 后端包含所有 Master，包括尚未就绪的节点。虽然 HAProxy 有 `check` 健康检查会自动剔除不可达后端，但：
- 健康检查有间隔（默认 2s × 3 次失败 = 6s 延迟）
- 在此期间，部分流量会被转发到不可达节点，导致请求失败
### 缺陷 3（真正的缺陷）：Master 节点增删时全量重配置
当新增/删除 Master 节点时：
1. 所有 Master 节点重新渲染 HAProxy + Keepalived 配置
2. Static Pod 文件更新 → Kubelet 检测到变化 → 重启 HAProxy/Keepalived
3. Keepalived 重启 → VIP 可能短暂漂移
4. 集群入口短暂不可用

**这是最严重的问题**，因为 Master 扩缩容是运维常见操作，不应该导致服务中断。
### 缺陷 4（真正的缺陷）：Keepalived 选举策略不合理
- MASTER 身份固定为 `nodes[0]`，不考虑节点实际负载
- 无 `nopreempt` 配置，MASTER 恢复后会抢占 VIP，造成不必要切换
- `check-master.sh` 只检查本地 API Server，不检查 etcd 健康状态
- `auth_pass` 硬编码 `22222222`
### 缺陷 5（真正的缺陷）：无 HA 配置状态上报
Controller 侧只标记了 `ControlPlaneEndPointSetCondition` 和 `NodeHAFlag`，缺少：
- VIP 当前绑定在哪个节点
- HAProxy 后端健康状态
- Keepalived 集群状态
## 三、修正后的重构建议
### 3.1 P0：拆分 HAProxy 后端列表与 Keepalived 安装范围
**目标**：HAProxy 后端仅包含已就绪的 Master 节点，Keepalived 安装在所有 Master 节点
```go
// ensure_load_balance.go - createLoadBalancerCommand
func (e *EnsureLoadBalance) createLoadBalancerCommand(nodes bkenode.Nodes) (*command.HA, error) {
    // 过滤已就绪的 Master 节点作为 HAProxy 后端
    readyMasterNodes := e.filterReadyMasterNodes(nodes)
    if len(readyMasterNodes) == 0 {
        readyMasterNodes = nodes  // 回退：无就绪节点时使用全部
    }
    
    loadBalanceCommand := command.HA{
        InstallNodes: nodes,            // Keepalived 安装范围：所有 Master
        BackendNodes: readyMasterNodes, // HAProxy 后端：仅已就绪 Master
        // ...
    }
}
```
### 3.2 P0：增量更新机制
**目标**：Master 增删时，不重启 Keepalived，仅优雅重载 HAProxy
```
当前流程:
  Master 变更 → 全量重渲染 → Static Pod 重启 → VIP 漂移 → 服务中断

重构后流程:
  Master 变更 → 检测变更类型
    ├── 仅后端变化 → 更新 haproxy.cfg → SIGUSR2 优雅重载（不中断连接）
    ├── 仅 VIP 变化 → 更新 keepalived.conf → SIGHUP 重载（VIP 不漂移）
    └── Pod 镜像变化 → 替换 Static Pod YAML（罕见）
```
### 3.3 P1：Keepalived 选举策略优化
```
当前:
  state MASTER/BACKUP（固定，基于节点顺序）
  无 nopreempt

重构:
  state BACKUP（所有节点初始状态）
  nopreempt（禁止抢占）
  priority 基于动态计算（etcd 健康、节点负载）
```
### 3.4 P2：安全加固
- `auth_pass` 从硬编码改为集群级随机生成
- HAProxy 添加 `securityContext` 限制
- HAProxy 添加 `resources` 限制
## 四、修正后的优先级
| 优先级 | 缺陷 | 重构项 |
|--------|------|--------|
| **P0** | 后端列表包含未就绪节点 | 拆分 installNodes/backendNodes |
| **P0** | 全量重配置导致 VIP 漂移 | 增量更新 + 优雅重载 |
| **P1** | 选举策略不合理 | 非抢占模式 + 动态优先级 |
| **P2** | 安全配置不足 | 随机 auth_pass + securityContext |
| **P2** | 无状态监控 | HA 状态上报 |

**总结**：当前"HA 安装在所有 Master 节点"的设计是正确的，因为所有 Master 都运行 `kube-apiserver`。真正的缺陷在于 HAProxy 后端列表与 Keepalived 安装范围耦合、全量重配置导致服务中断、以及选举策略不合理。
        
