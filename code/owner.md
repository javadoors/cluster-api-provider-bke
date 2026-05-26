# cluster-api-provider-bke 模块划分方案

## 一、项目整体架构认知

该项目是一个 **Kubernetes Cluster API Provider**，遵循 CAPI 标准实现，由两个核心二进制组成：

| 二进制 | 运行位置 | 职责 |
|--------|----------|------|
| `capbke` | 管理集群 | BKECluster/BKEMachine 的 Reconciler，编排集群生命周期 |
| `bkeagent` | 工作节点 | Command 的 Reconciler，执行节点级操作（安装/升级/重置等） |

两者通过 **Command CRD** 实现异步通信，形成"管理面-数据面"架构。

## 二、模块划分（8 大模块）

### 模块 1：API 类型定义层（API Types）
| 属性 | 值 |
|------|-----|
| **路径** | `api/` |
| **Owner** | API Owner |
| **职责** | 所有 CRD 类型定义、DeepCopy 生成、状态枚举、条件定义 |

**子模块划分：**

| 子模块 | 路径 | 说明 |
|--------|------|------|
| capbke-api | `api/capbke/v1beta1/` | BKECluster、BKEMachine、BKEMachineTemplate、BKEClusterTemplate、BKENode、ContainerdConfig 类型 |
| bkeagent-api | `api/bkeagent/v1beta1/` | Command 类型、Condition 定义 |
| bkecommon-api | `api/bkecommon/v1beta1/` | 共享类型：BKEClusterSpec/Status、BKENode、KubeletConfig、ContainerdConfig |

**耦合关系：** `bkecommon-api` 被 `capbke-api` 和 `bkeagent-api` 共同依赖，是唯一共享层。`capbke-api` 和 `bkeagent-api` 之间无直接依赖。

**演进原则：** API 变更需走 KEPS 流程，保证向后兼容；所有类型变更需经 API Owner 审阅。

### 模块 2：控制器层（Controllers）
| 属性 | 值 |
|------|-----|
| **路径** | `controllers/` |
| **Owner** | Controller Owner |
| **职责** | Reconciler 主循环、Watch 配置、Predicate 过滤、事件处理 |

**子模块划分：**

| 子模块 | 路径 | 说明 |
|--------|------|------|
| capbke-controllers | `controllers/capbke/` | BKEClusterReconciler、BKEMachineReconciler |
| bkeagent-controllers | `controllers/bkeagent/` | CommandReconciler |

**关键文件：**
- [bkecluster_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go) — BKECluster 调谐主循环
- [bkemachine_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkemachine_controller.go) — BKEMachine 调谐主循环
- [command_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go) — Command 调谐主循环

**耦合关系：** 控制器层是"薄层"，仅负责调谐逻辑编排，核心业务委托给 PhaseFrame 和 Job 模块。依赖 API 模块、PhaseFrame 模块、Command 模块。

### 模块 3：PhaseFrame 编排引擎（Lifecycle Engine）
| 属性 | 值 |
|------|-----|
| **路径** | `pkg/phaseframe/` |
| **Owner** | Lifecycle Owner |
| **职责** | 集群生命周期阶段编排框架，定义 Phase 接口、执行流程、Hook 机制 |

**子模块划分：**

| 子模块 | 路径 | 说明 |
|--------|------|------|
| phaseframe-core | `pkg/phaseframe/` (interface.go, base.go, context.go) | Phase 接口定义、BasePhase 实现、PhaseContext 上下文 |
| phases | `pkg/phaseframe/phases/` | 所有 Phase 实现（30+ 个阶段） |
| phaseutil | `pkg/phaseframe/phaseutil/` | Phase 执行所需的工具函数集 |

**Phase 分类：**

| 类别 | Phase 示例 | 说明 |
|------|-----------|------|
| 集群初始化 | `ensure_master_init`, `ensure_master_join`, `ensure_worker_join` | 集群创建流程 |
| 集群升级 | `ensure_master_upgrade`, `ensure_worker_upgrade`, `ensure_etcd_upgrade`, `ensure_containerd_upgrade`, `ensure_component_upgrade` | 集群升级流程 |
| 集群删除 | `ensure_master_delete`, `ensure_worker_delete`, `ensure_delete_or_reset` | 集群删除流程 |
| 集群维护 | `ensure_certs`, `ensure_bke_agent`, `ensure_agent_switch`, `ensure_addon_deploy`, `ensure_load_balance` | 集群运维操作 |
| 控制流 | `ensure_paused`, `ensure_finalizer`, `ensure_dry_run`, `ensure_cluster_api_obj`, `ensure_provider_self_upgrade` | 编排控制 |

**耦合关系：** 依赖 API 模块、Kube 模块、Remote 模块、Certs 模块、Command 模块。被 Controllers 模块调用。PhaseUtil 是 Phases 和 Core 之间的桥梁层。

### 模块 4：Job 执行引擎（Job Engine）
| 属性 | 值 |
|------|-----|
| **路径** | `pkg/job/`, `pkg/executor/` |
| **Owner** | Job Engine Owner |
| **职责** | 节点级任务的注册、调度与执行，支持内置任务、K8s 任务、Shell 任务 |

**子模块划分：**

| 子模块 | 路径 | 说明 |
|--------|------|------|
| job-core | `pkg/job/job.go` | Job 接口定义、Task 生命周期管理 |
| executor | `pkg/executor/` | 命令执行器（exec、containerd、docker） |
| builtin-jobs | `pkg/job/builtin/` | 内置任务插件（kubeadm、containerd、reset、backup 等） |
| k8s-jobs | `pkg/job/k8s/` | Kubernetes 资源操作任务 |
| shell-jobs | `pkg/job/shell/` | Shell 命令执行任务 |

**Builtin Job 插件清单：**

| 插件 | 路径 | 功能 |
|------|------|------|
| kubeadm | `pkg/job/builtin/kubeadm/` | kubeadm 初始化/加入/证书/环境准备/kubelet 配置/manifest 渲染 |
| containerruntime | `pkg/job/builtin/containerruntime/` | containerd/cri-docker/docker 运行时安装配置 |
| reset | `pkg/job/builtin/reset/` | 节点重置与清理 |
| backup | `pkg/job/builtin/backup/` | etcd 备份 |
| ha | `pkg/job/builtin/ha/` | 高可用配置 |
| downloader | `pkg/job/builtin/downloader/` | 二进制/镜像下载 |
| selfupdate | `pkg/job/builtin/selfupdate/` | Agent 自更新 |
| switchcluster | `pkg/job/builtin/switchcluster/` | 集群切换 |
| postprocess/preprocess | `pkg/job/builtin/postprocess/`, `preprocess/` | 前后置处理 |

**耦合关系：** 仅被 bkeagent-controllers 调用。依赖 Executor 模块和 API 模块。插件通过 `plugin.Plugin` 接口注册，新增插件无需修改核心代码。

### 模块 5：基础设施服务层（Infrastructure Services）
| 属性 | 值 |
|------|-----|
| **路径** | `pkg/remote/`, `pkg/kube/`, `pkg/certs/`, `pkg/command/`, `pkg/mergecluster/`, `pkg/statusmanage/` |
| **Owner** | Infra Owner |
| **职责** | 提供跨模块共享的基础设施能力 |

**子模块划分：**

| 子模块 | 路径 | 功能 | 主要消费者 |
|--------|------|------|-----------|
| remote | `pkg/remote/` | SSH/SFTP 远程操作、MultiCLI 多节点并发执行 | PhaseFrame |
| kube | `pkg/kube/` | 远程 K8s 客户端、Addon 安装、Helm/YAML 部署、健康检查 | PhaseFrame |
| certs | `pkg/certs/` | 证书生成/轮转、Kubeconfig 管理 | PhaseFrame |
| command | `pkg/command/` | Command CRD 创建/等待/删除封装 | Controllers、PhaseFrame |
| mergecluster | `pkg/mergecluster/` | BKECluster 状态合并与同步 | Controllers、PhaseFrame |
| statusmanage | `pkg/statusmanage/` | BKECluster/BKENode 状态管理与失败计数 | Controllers |

**耦合关系：** 被 Controllers 和 PhaseFrame 依赖。依赖 API 模块和 Utils 模块。各子模块之间尽量无依赖。

### 模块 6：工具与公共库（Common & Utils）
| 属性 | 值 |
|------|-----|
| **路径** | `common/`, `utils/`, `testutils/`, `version/` |
| **Owner** | Common Lib Owner |
| **职责** | 提供无业务逻辑的通用工具函数和类型 |

**子模块划分：**

| 子模块 | 路径 | 说明 |
|--------|------|------|
| common-cluster | `common/cluster/` | 集群相关通用逻辑：addon 比较、node 比较、image helper、初始化默认值、校验 |
| common-ntp | `common/ntp/` | NTP 时间同步客户端/服务端 |
| common-security | `common/security/` | 安全相关工具 |
| common-source | `common/source/` | 数据源工具 |
| common-template | `common/template/` | 模板渲染函数 |
| common-utils | `common/utils/` | 通用工具（网络、杂项） |
| common-versionutil | `common/versionutil/` | 版本比较工具 |
| common-warehouse | `common/warehouse/` | 仓库注册表 |
| utils-bkeagent | `utils/bkeagent/` | Agent 专用工具：pkiutil、mfutil、etcd、kubeclient、download、resetutil、initsystem 等 |
| utils-capbke | `utils/capbke/` | Provider 专用工具：annotation、label、condition、predicates、patchutil、clusterutil、nodeutil、clustertracker、config、constant、addonutil、scriptshelper |
| utils-logger | `utils/logger/` | 日志工厂 |
| testutils | `testutils/` | 测试辅助：fake client、http server、log mock |
| version | `version/` | 版本信息 |

**耦合关系：** 纯工具层，不依赖任何业务模块。被所有其他模块依赖。`utils-bkeagent` 仅被 bkeagent 侧使用，`utils-capbke` 仅被 capbke 侧使用，两者不应交叉引用。

### 模块 7：Webhook 与准入控制（Webhooks）
| 属性 | 值 |
|------|-----|
| **路径** | `webhooks/` |
| **Owner** | Webhook Owner |
| **职责** | CRD 的默认值设置（Defaulting）和校验（Validation） |

**子模块划分：**

| 子模块 | 路径 | 说明 |
|--------|------|------|
| capbke-webhooks | `webhooks/capbke/` | BKECluster、BKENode 的 Defaulting/Validation Webhook |

**耦合关系：** 依赖 API 模块、Common-Cluster（校验逻辑）。仅被 capbke 入口注册。

### 模块 8：构建与部署（Build & Deploy）
| 属性 | 值 |
|------|-----|
| **路径** | `cmd/`, `builder/`, `config/`, `Makefile*` |
| **Owner** | Build Owner |
| **职责** | 程序入口、Docker 构建、K8s 部署清单 |

**子模块划分：**

| 子模块 | 路径 | 说明 |
|--------|------|------|
| capbke-entry | `cmd/capbke/` | capbke 控制器入口，注册 Controllers/Webhooks |
| bkeagent-entry | `cmd/bkeagent/` | bkeagent 入口，注册 Command Controller、CRD 安装、NTP |
| bkeagent-launcher | `cmd/bkeagent-launcher/` | Agent 启动器，负责部署 bkeagent 二进制和 systemd 服务 |
| builder | `builder/` | Dockerfile（capbke、bkeagent、bkeagent-deployer、bkeagent-launcher） |
| config | `config/` | Kustomize 部署清单（CRD、RBAC、Manager、Webhook、Prometheus） |

**耦合关系：** 依赖所有模块。是最终的组装层。

## 三、模块依赖关系图
```
┌─────────────────────────────────────────────────────────┐
│                   Build & Deploy (M8)                   │
│  cmd/  builder/  config/  Makefile                      │
└────────────┬──────────────────────────┬─────────────────┘
             │                          │
    ┌────────▼────────┐      ┌─────────▼──────────┐
    │  capbke Entry   │      │  bkeagent Entry    │
    └────────┬────────┘      └─────────┬──────────┘
             │                          │
    ┌────────▼────────┐      ┌─────────▼───────────┐
    │ Controllers(M2) │      │ Controllers(M2)     │
    │ ┌─────────────┐ │      │ ┌────────────────┐  │
    │ │BKECluster   │ │      │ │Command         │  │
    │ │BKEMachine   │ │      │ │Reconciler      │  │
    │ └──────┬──────┘ │      │ └───────┬────────┘  │
    └────────┼────────┘      └─────────┼───────────┘
             │                          │
    ┌────────▼────────┐      ┌─────────▼────────────┐
    │ PhaseFrame (M3) │      │  Job Engine (M4)     │
    │ ┌─────────────┐ │      │ ┌─────────────────┐  │
    │ │Core/Phases/ │ │      │ │Core/Builtin/K8s/│  │
    │ │PhaseUtil    │ │      │ │Shell/Executor   │  │
    │ └──────┬──────┘ │      │ └────────┬────────┘  │
    └────────┼────────┘      └──────────┼───────────┘
             │                          │
    ┌────────▼──────────────────────────▼────────────┐
    │         Infrastructure Services (M5)           │
    │  remote │ kube │ certs │ command │ mergecluster│
    │                │ statusmanage                  │
    └────────────────┬───────────────────────────────┘
                     │
    ┌────────────────▼──────────────────────────────┐
    │         Webhooks (M7)                         │
    └────────────────┬──────────────────────────────┘
                     │
    ┌────────────────▼──────────────────────────────┐
    │         API Types (M1)                        │
    │  capbke-api │ bkeagent-api │ bkecommon-api    │
    └────────────────┬──────────────────────────────┘
                     │
    ┌────────────────▼───────────────────────────────┐
    │         Common & Utils (M6)                    │
    │  common-cluster │ utils-bkeagent │ utils-capbke│
    │  testutils │ version │ logger                  │
    └────────────────────────────────────────────────┘
```
**依赖规则：**
- 箭头方向 = 依赖方向（上层依赖下层）
- **严禁反向依赖**：下层模块不得 import 上层模块
- **严禁跨面依赖**：`utils-bkeagent` 与 `utils-capbke` 不得互相引用
- M5 内部子模块间应尽量避免互相依赖

## 四、模块 Owner 机制
| 模块 | Owner 角色 | 核心职责 | 代码审查规则 |
|------|-----------|---------|-------------|
| M1: API Types | API Owner | CRD Schema 设计、版本兼容性、字段语义 | 所有 API 变更必须 API Owner APPROVE |
| M2: Controllers | Controller Owner | Reconciler 逻辑、Watch/Predicate 配置、事件处理 | Controller Owner + 相关模块 Owner 共审 |
| M3: PhaseFrame | Lifecycle Owner | Phase 接口、编排流程、Phase 实现、升级策略 | Lifecycle Owner 主审，Infra Owner 协审 |
| M4: Job Engine | Job Engine Owner | Job 插件注册、执行器、内置任务 | Job Engine Owner 主审，新增插件需 API Owner 知会 |
| M5: Infrastructure | Infra Owner | 远程操作、K8s 客户端、证书、Command 封装 | Infra Owner 主审 |
| M6: Common & Utils | Common Lib Owner | 工具函数质量、无业务逻辑、接口稳定性 | Common Lib Owner 审阅，新增导出函数需文档注释 |
| M7: Webhooks | Webhook Owner | 默认值逻辑、校验规则 | Webhook Owner + API Owner 共审 |
| M8: Build & Deploy | Build Owner | 构建流程、部署清单、版本发布 | Build Owner 主审 |

## 五、模块间接口契约
为确保低耦合，各模块间通过明确接口交互：

| 接口 | 定义位置 | 消费者 | 提供者 |
|------|---------|--------|--------|
| `Phase` 接口 | `pkg/phaseframe/interface.go` | Controllers | PhaseFrame |
| `Job` 接口 | `pkg/job/job.go` | bkeagent-controllers | Job Engine |
| `plugin.Plugin` 接口 | `pkg/job/builtin/plugin/interface.go` | Job Core | Builtin Jobs |
| `RemoteKubeClient` 接口 | `pkg/kube/kube.go` | PhaseFrame | Kube 模块 |
| `HostRemoteClient` | `pkg/remote/remotecli.go` | PhaseFrame | Remote 模块 |
| `Executor` 接口 | `pkg/executor/exec/exec.go` | Job Engine | Executor |
| `NodeFetcher` | `utils/capbke/nodeutil/fetcher.go` | Controllers | NodeUtil |
| `StatusManager` | `pkg/statusmanage/statusmanager.go` | Controllers | StatusManage |

## 六、演进建议
1. **PhaseFrame 扩展**：新增 Phase 只需在 `pkg/phaseframe/phases/` 下添加文件并注册到 `list.go`，无需修改框架核心代码。
2. **Job 插件扩展**：新增 Builtin Job 插件只需实现 `plugin.Plugin` 接口并在 `builtin.go` 中注册，无需修改 Job Core。
3. **API 版本演进**：新增 API 版本时在 `api/` 下新增目录（如 `v1beta2`），通过 Conversion Webhook 保证兼容。
4. **模块边界强化**：建议引入 `go vet` 或自定义 linter，检测 `utils-bkeagent` 与 `utils-capbke` 之间的非法引用。
5. **CI Owner 检查**：在 CODEOWNERS 文件中按上述模块 Owner 配置，确保 PR 必须经过对应 Owner 审阅。
 
# 功能特性维度 Owner 机制
从功能特性维度重新划分 Owner 机制。之前的划分是"水平分层"（按架构层），现在改为"垂直切分"（按功能特性），每个 Owner 负责一个完整的功能特性纵向贯穿。

## 划分思路
集群生命周期管理可归纳为 **7 大功能特性域**，每个域贯穿 API → Controller → PhaseFrame/Job → Infra → Utils 全栈：
```
┌───────────────────────────────────────────────────────────────────────┐
│                      功能特性域 Owner 矩阵                             │
├───────────┬───────────┬───────────┬───────────┬───────────┬───────────┤
│  集群创建  │  集群升级  │  集群删除 │  证书安全  │  集群运维  │  节点执行 │
│  Owner    │  Owner    │  Owner    │  Owner    │  Owner    │  Owner    │
├───────────┼───────────┼───────────┼───────────┼───────────┼───────────┤
│ API       │ API       │ API       │ API       │ API       │ API       │
│ Controller│ Controller│ Controller│ Controller│ Controller│ Controller│
│ Phases    │ Phases    │ Phases    │ Phases    │ Phases    │ Job       │
│ PhaseUtil │ PhaseUtil │ PhaseUtil │ Certs     │ Kube      │ Executor  │
│ Command   │ Command   │ Command   │ PKIUtil   │ Remote    │ Builtin   │
│ Kube      │ Kube      │ Remote    │ Command   │ Addon     │ Containerd│
│ Remote    │ Remote    │ Reset     │ Merge     │ Status    │ Docker    │
│ Merge     │ Merge     │           │           │ Merge     │ Reset     │
│ Status    │ Status    │           │           │           │ Download  │
└───────────┴───────────┴───────────┴───────────┴───────────┴───────────┘
         ┌──────────────────────────────────────────────┐
         │           基础平台 Owner（横切面）             │
         │ API Schema │ Webhook │ Build │ Common │ 测试 │
         └──────────────────────────────────────────────┘
```
## 七大功能特性域

### 特性域 1：集群创建（Cluster Bootstrap Owner）
**职责**：集群从零到一的创建流程，包括 Master 初始化、Master 加入、Worker 加入

| 维度 | 涉及代码 | 关键文件 |
|------|---------|---------|
| API | BKECluster/BKEMachine Spec 中创建相关字段 | [api/capbke/v1beta1/bkecluster_types.go](file:///d:/code/github/cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_types.go) |
| Controller | BKECluster/BKEMachine Reconciler 中创建分支 | [controllers/capbke/bkecluster_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go) |
| Phases | `ensure_master_init` → `ensure_master_join` → `ensure_worker_join` → `ensure_nodes_env` → `ensure_nodes_postprocess` → `ensure_bke_agent` → `ensure_load_balance` → `ensure_addon_deploy` | [pkg/phaseframe/phases/ensure_master_init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go) 等 |
| PhaseUtil | bkecluster、bkemachine、agent、clusterapi、addon、ssh、localkubeconfig、k8stoken | [pkg/phaseframe/phaseutil/](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/) |
| Command | bootstrap 命令创建与等待 | [pkg/command/bootstrap.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/bootstrap.go) |
| Kube | 集群连接、Addon 安装 | [pkg/kube/kube.go](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/kube.go) |
| Remote | SSH 到节点执行操作 | [pkg/remote/](file:///d:/code/github/cluster-api-provider-bke/pkg/remote/) |
| Job | kubeadm init/join、env 准备、kubelet 配置、containerd 安装、HA 配置 | [pkg/job/builtin/kubeadm/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/) |

**核心流程链**：
```
BKECluster创建 → ensure_master_init → ensure_master_join → 
ensure_worker_join → ensure_nodes_env → ensure_bke_agent → 
ensure_load_balance → ensure_addon_deploy
```
**Owner 审查规则**：创建流程中任何 Phase 新增/修改、kubeadm 参数变更、节点初始化顺序调整，必须 Cluster Bootstrap Owner APPROVE。

### 特性域 2：集群升级（Cluster Upgrade Owner）
**职责**：集群版本升级全流程，包括 Master 升级、Worker 升级、etcd 升级、容器运行时升级、组件升级、Provider 自升级

| 维度 | 涉及代码 | 关键文件 |
|------|---------|---------|
| API | BKECluster Spec 中版本/升级相关字段 | [api/bkecommon/v1beta1/bkecluster_spec.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_spec.go) |
| Controller | BKECluster Reconciler 中升级分支判断 | [controllers/capbke/bkecluster_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go) |
| Phases | `ensure_master_upgrade` → `ensure_worker_upgrade` → `ensure_etcd_upgrade` → `ensure_containerd_upgrade` → `ensure_component_upgrade` → `ensure_agent_upgrade` → `ensure_provider_self_upgrade` | [pkg/phaseframe/phases/ensure_master_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go) 等 |
| PhaseUtil | upgrade、provider、bocloud、agent | [pkg/phaseframe/phaseutil/upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/upgrade.go)、[provider.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/provider.go)、[bocloud.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/bocloud.go) |
| Command | upgrade 命令创建与等待 | [pkg/command/upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/upgrade.go) |
| Job | kubeadm upgrade、containerd 升级、selfupdate | [pkg/job/builtin/kubeadm/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/)、[containerruntime/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/)、[selfupdate/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/selfupdate/) |

**核心流程链**：
```
版本变更检测 → ensure_etcd_upgrade → ensure_master_upgrade → 
ensure_worker_upgrade → ensure_containerd_upgrade → 
ensure_component_upgrade → ensure_agent_upgrade → 
ensure_provider_self_upgrade
```
**Owner 审查规则**：升级顺序变更、版本兼容性逻辑、滚动升级策略、etcd 升级流程修改，必须 Cluster Upgrade Owner APPROVE。

### 特性域 3：集群删除与重置（Cluster Teardown Owner）
**职责**：集群/节点的删除与重置流程，包括 Master 删除、Worker 删除、节点重置清理

| 维度 | 涉及代码 | 关键文件 |
|------|---------|---------|
| API | BKECluster/BKEMachine 删除相关 Finalizer | [api/capbke/v1beta1/bkecluster_types.go](file:///d:/code/github/cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_types.go) |
| Controller | BKECluster/BKEMachine Reconciler 中删除分支、Finalizer 处理 | [controllers/capbke/bkecluster_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go) |
| Phases | `ensure_master_delete` → `ensure_worker_delete` → `ensure_delete_or_reset` → `ensure_finalizer` | [pkg/phaseframe/phases/ensure_master_delete.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_delete.go) 等 |
| Command | cleannode 命令 | [pkg/command/cleannode.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/cleannode.go) |
| Job | reset 清理、shutdown | [pkg/job/builtin/reset/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/reset/)、[shutdown/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/shutdown/) |
| Utils | resetutil 清理工具 | [utils/bkeagent/resetutil/](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/resetutil/) |

**核心流程链**：
```
BKECluster删除 → ensure_worker_delete → ensure_master_delete → 
ensure_delete_or_reset → ensure_finalizer
```
**Owner 审查规则**：删除顺序变更、Finalizer 逻辑修改、节点清理范围调整，必须 Cluster Teardown Owner APPROVE。

### 特性域 4：证书与安全（Certificate & Security Owner）
**职责**：PKI 证书体系管理、证书轮转、Kubeconfig 生成、安全相关配置

| 维度 | 涉及代码 | 关键文件 |
|------|---------|---------|
| API | 证书相关 Spec/Status 字段 | [api/bkecommon/v1beta1/](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/) |
| Phases | `ensure_certs` | [pkg/phaseframe/phases/ensure_certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_certs.go) |
| Certs | 证书生成/轮转/获取 | [pkg/certs/](file:///d:/code/github/cluster-api-provider-bke/pkg/certs/) |
| PKIUtil | 证书工具集（CA、Server、Client 证书、Kubeconfig、AltName、CertList） | [utils/bkeagent/pkiutil/](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/pkiutil/) |
| Job | kubeadm certs 子命令 | [pkg/job/builtin/kubeadm/certs/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/certs/) |
| Security | 安全工具 | [common/security/](file:///d:/code/github/cluster-api-provider-bke/common/security/) |

**Owner 审查规则**：证书类型新增/变更、证书有效期调整、Kubeconfig 生成逻辑、CA 签发流程修改，必须 Certificate & Security Owner APPROVE。涉及安全敏感变更需安全评审。

### 特性域 5：集群运维（Cluster Operations Owner）
**职责**：集群日常运维操作，包括 Addon 管理、Agent 管理、负载均衡、状态管理、集群切换、暂停/恢复

| 维度 | 涉及代码 | 关键文件 |
|------|---------|---------|
| Phases | `ensure_addon_deploy`、`ensure_bke_agent`、`ensure_agent_switch`、`ensure_load_balance`、`ensure_paused`、`ensure_cluster_api_obj`、`ensure_cluster_manage` | [pkg/phaseframe/phases/](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/) |
| PhaseUtil | addon、agent、bkecluster、bkemachine | [pkg/phaseframe/phaseutil/](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phaseutil/) |
| Kube | Addon 安装/对比/更新、Helm/YAML 部署、健康检查 | [pkg/kube/](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/) |
| Command | switchcluster、loadbalance、custom、hosts、env、ping、collect 命令 | [pkg/command/](file:///d:/code/github/cluster-api-provider-bke/pkg/command/) |
| StatusManage | BKECluster/BKENode 状态管理与失败计数 | [pkg/statusmanage/](file:///d:/code/github/cluster-api-provider-bke/pkg/statusmanage/) |
| MergeCluster | BKECluster 状态合并 | [pkg/mergecluster/](file:///d:/code/github/cluster-api-provider-bke/pkg/mergecluster/) |
| Job | HA 配置、switchcluster、collect、backup | [pkg/job/builtin/ha/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/ha/) 等 |
| Common | addon 比较、node 比较、image helper、初始化默认值 | [common/cluster/](file:///d:/code/github/cluster-api-provider-bke/common/cluster/) |

**Owner 审查规则**：Addon 部署策略变更、Agent 升级/切换逻辑、状态管理规则调整、负载均衡配置修改，必须 Cluster Operations Owner APPROVE。

### 特性域 6：节点执行引擎（Node Execution Engine Owner）
**职责**：节点级任务的注册、调度、执行框架，包括命令执行器、容器运行时操作、下载器、任务插件体系

| 维度 | 涉及代码 | 关键文件 |
|------|---------|---------|
| Controller | CommandReconciler | [controllers/bkeagent/command_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go) |
| Job Core | Job 接口、Task 生命周期 | [pkg/job/job.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/job.go) |
| Builtin Jobs | 所有内置任务插件（kubeadm、containerd、docker、cri-docker、reset、backup、download、ping、preprocess、postprocess、selfupdate、shutdown、switchcluster、scriptutil） | [pkg/job/builtin/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/) |
| K8s Job | K8s 资源操作任务 | [pkg/job/k8s/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/k8s/) |
| Shell Job | Shell 命令执行 | [pkg/job/shell/](file:///d:/code/github/cluster-api-provider-bke/pkg/job/shell/) |
| Executor | exec 命令执行器、containerd 操作、docker 操作 | [pkg/executor/](file:///d:/code/github/cluster-api-provider-bke/pkg/executor/) |
| Utils | download、kubeclient、initsystem、mfutil、etcd、httprepo、runtime、mutx | [utils/bkeagent/](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/) |

**Owner 审查规则**：Job 插件接口变更、Executor 行为修改、新增 Builtin Job 插件、容器运行时操作逻辑修改，必须 Node Execution Engine Owner APPROVE。

### 特性域 7：基础平台（Platform Foundation Owner）
**职责**：横切面基础能力，被所有功能特性域共享，不包含业务逻辑

| 维度 | 涉及代码 | 关键文件 |
|------|---------|---------|
| API Schema | 所有 CRD 类型定义、DeepCopy、Condition | [api/](file:///d:/code/github/cluster-api-provider-bke/api/) |
| Webhooks | Defaulting/Validation 准入控制 | [webhooks/](file:///d:/code/github/cluster-api-provider-bke/webhooks/) |
| Validation | 集群/节点校验规则 | [common/cluster/validation/](file:///d:/code/github/cluster-api-provider-bke/common/cluster/validation/) |
| PhaseFrame Core | Phase 接口、BasePhase、PhaseContext、PhaseFlow、List | [pkg/phaseframe/interface.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/interface.go)、[base.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go)、[context.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/context.go) |
| Utils-Capbke | annotation、label、condition、predicates、patchutil、clusterutil、nodeutil、clustertracker、config、constant、addonutil、scriptshelper | [utils/capbke/](file:///d:/code/github/cluster-api-provider-bke/utils/capbke/) |
| Common | ntp、template、source、versionutil、warehouse、utils/net | [common/](file:///d:/code/github/cluster-api-provider-bke/common/) |
| Test Utils | fake client、http server、log mock | [testutils/](file:///d:/code/github/cluster-api-provider-bke/testutils/) |
| Version | 版本信息 | [version/](file:///d:/code/github/cluster-api-provider-bke/version/) |
| Build & Deploy | 程序入口、Dockerfile、Kustomize、Makefile | `cmd/`、`builder/`、`config/`、`Makefile*` |
| Metrics | 指标采集与暴露 | [pkg/metrics/](file:///d:/code/github/cluster-api-provider-bke/pkg/metrics/) |

**Owner 审查规则**：
- API Schema 变更需 Platform Foundation Owner + 相关特性域 Owner 共审
- PhaseFrame Core 接口变更需所有特性域 Owner 知会
- Webhook 变更需 Platform Foundation Owner + 相关特性域 Owner 共审
- 构建与部署变更需 Platform Foundation Owner 主审

## 跨特性域协作矩阵
| 变更场景 | 主审 Owner | 协审 Owner | 原因 |
|---------|-----------|-----------|------|
| 新增/修改 Phase | 对应特性域 Owner | Platform Foundation Owner | Phase 接口由平台定义 |
| API 字段变更 | Platform Foundation Owner | 使用该字段的特性域 Owner | Schema 影响所有消费者 |
| 升级流程中涉及证书轮转 | Cluster Upgrade Owner | Certificate & Security Owner | 证书是升级的子流程 |
| 删除流程中涉及 Agent 关闭 | Cluster Teardown Owner | Node Execution Engine Owner | Agent 关闭通过 Job 执行 |
| 创建流程中涉及 Addon 部署 | Cluster Bootstrap Owner | Cluster Operations Owner | Addon 部署是创建的子流程 |
| 新增 Builtin Job 插件 | Node Execution Engine Owner | 使用该插件的特性域 Owner | 插件由特性域消费 |
| Webhook 校验规则变更 | Platform Foundation Owner | 对应特性域 Owner | 校验逻辑需与业务一致 |
| PhaseFrame Core 接口变更 | Platform Foundation Owner | **所有特性域 Owner** | 框架变更影响全局 |
| 状态管理规则调整 | Cluster Operations Owner | Cluster Bootstrap/Upgrade Owner | 状态影响创建/升级流程 |

## Owner 机制落地建议

### 1. CODEOWNERS 配置示例
```
# 特性域 1：集群创建
/pkg/phaseframe/phases/ensure_master_init.go     @cluster-bootstrap-owner
/pkg/phaseframe/phases/ensure_master_join.go     @cluster-bootstrap-owner
/pkg/phaseframe/phases/ensure_worker_join.go     @cluster-bootstrap-owner
/pkg/phaseframe/phases/ensure_nodes_env.go       @cluster-bootstrap-owner
/pkg/phaseframe/phases/ensure_nodes_postprocess.go @cluster-bootstrap-owner
/pkg/phaseframe/phases/ensure_bke_agent.go       @cluster-bootstrap-owner
/pkg/phaseframe/phases/ensure_load_balance.go    @cluster-bootstrap-owner
/pkg/phaseframe/phases/ensure_addon_deploy.go    @cluster-bootstrap-owner @cluster-ops-owner
/pkg/command/bootstrap.go                        @cluster-bootstrap-owner
/pkg/job/builtin/kubeadm/                        @cluster-bootstrap-owner @node-exec-owner

# 特性域 2：集群升级
/pkg/phaseframe/phases/ensure_master_upgrade.go      @cluster-upgrade-owner
/pkg/phaseframe/phases/ensure_worker_upgrade.go      @cluster-upgrade-owner
/pkg/phaseframe/phases/ensure_etcd_upgrade.go        @cluster-upgrade-owner
/pkg/phaseframe/phases/ensure_containerd_upgrade.go  @cluster-upgrade-owner
/pkg/phaseframe/phases/ensure_component_upgrade.go   @cluster-upgrade-owner
/pkg/phaseframe/phases/ensure_agent_upgrade.go       @cluster-upgrade-owner
/pkg/phaseframe/phases/ensure_provider_self_upgrade.go @cluster-upgrade-owner
/pkg/phaseframe/phaseutil/upgrade.go                 @cluster-upgrade-owner
/pkg/phaseframe/phaseutil/provider.go                @cluster-upgrade-owner
/pkg/phaseframe/phaseutil/bocloud.go                 @cluster-upgrade-owner
/pkg/command/upgrade.go                              @cluster-upgrade-owner
/pkg/job/builtin/selfupdate/                         @cluster-upgrade-owner @node-exec-owner

# 特性域 3：集群删除与重置
/pkg/phaseframe/phases/ensure_master_delete.go   @cluster-teardown-owner
/pkg/phaseframe/phases/ensure_worker_delete.go   @cluster-teardown-owner
/pkg/phaseframe/phases/ensure_delete_or_reset.go @cluster-teardown-owner
/pkg/phaseframe/phases/ensure_finalizer.go       @cluster-teardown-owner
/pkg/command/cleannode.go                        @cluster-teardown-owner
/pkg/job/builtin/reset/                          @cluster-teardown-owner @node-exec-owner
/pkg/job/builtin/shutdown/                       @cluster-teardown-owner @node-exec-owner
/utils/bkeagent/resetutil/                       @cluster-teardown-owner

# 特性域 4：证书与安全
/pkg/phaseframe/phases/ensure_certs.go   @cert-security-owner
/pkg/certs/                              @cert-security-owner
/utils/bkeagent/pkiutil/                 @cert-security-owner
/pkg/job/builtin/kubeadm/certs/          @cert-security-owner @node-exec-owner
/common/security/                        @cert-security-owner

# 特性域 5：集群运维
/pkg/kube/                               @cluster-ops-owner
/pkg/statusmanage/                       @cluster-ops-owner
/pkg/mergecluster/                       @cluster-ops-owner
/pkg/phaseframe/phases/ensure_paused.go  @cluster-ops-owner
/pkg/phaseframe/phases/ensure_cluster_api_obj.go @cluster-ops-owner
/pkg/phaseframe/phases/ensure_cluster_manage.go  @cluster-ops-owner
/pkg/phaseframe/phases/ensure_agent_switch.go    @cluster-ops-owner
/pkg/command/switchcluster.go            @cluster-ops-owner
/pkg/command/loadbalance.go              @cluster-ops-owner
/pkg/command/custom.go                   @cluster-ops-owner
/pkg/command/collect.go                  @cluster-ops-owner
/common/cluster/addon/                   @cluster-ops-owner
/common/cluster/node/                    @cluster-ops-owner
/common/cluster/imagehelper/             @cluster-ops-owner
/common/cluster/initialize/              @cluster-ops-owner

# 特性域 6：节点执行引擎
/controllers/bkeagent/                   @node-exec-owner
/pkg/job/                                @node-exec-owner
/pkg/executor/                           @node-exec-owner
/pkg/job/builtin/containerruntime/       @node-exec-owner
/pkg/job/builtin/downloader/             @node-exec-owner
/pkg/job/builtin/ping/                   @node-exec-owner
/pkg/job/builtin/plugin/                 @node-exec-owner
/pkg/job/builtin/preprocess/             @node-exec-owner
/pkg/job/builtin/postprocess/            @node-exec-owner
/pkg/job/builtin/scriptutil/             @node-exec-owner
/utils/bkeagent/download/                @node-exec-owner
/utils/bkeagent/kubeclient/              @node-exec-owner
/utils/bkeagent/initsystem/              @node-exec-owner
/utils/bkeagent/mfutil/                  @node-exec-owner
/utils/bkeagent/etcd/                    @node-exec-owner
/utils/bkeagent/httprepo/                @node-exec-owner
/utils/bkeagent/runtime/                 @node-exec-owner
/utils/bkeagent/mutx/                    @node-exec-owner
/utils/bkeagent/net/                     @node-exec-owner
/utils/bkeagent/option/                  @node-exec-owner
/utils/bkeagent/clientutil/              @node-exec-owner
/utils/bkeagent/cluster/                 @node-exec-owner

# 特性域 7：基础平台
/api/                                    @platform-owner
/webhooks/                               @platform-owner
/common/cluster/validation/              @platform-owner
/pkg/phaseframe/interface.go             @platform-owner
/pkg/phaseframe/base.go                  @platform-owner
/pkg/phaseframe/context.go               @platform-owner
/pkg/phaseframe/phases/phase_flow.go     @platform-owner
/pkg/phaseframe/phases/list.go           @platform-owner
/pkg/phaseframe/phases/template.go       @platform-owner
/pkg/phaseframe/phases/phase_helpers.go  @platform-owner
/pkg/phaseframe/phases/common.go         @platform-owner
/pkg/phaseframe/phases/ensure_dry_run.go @platform-owner
/utils/capbke/                           @platform-owner
/common/ntp/                             @platform-owner
/common/template/                        @platform-owner
/common/source/                          @platform-owner
/common/versionutil/                     @platform-owner
/common/warehouse/                       @platform-owner
/common/utils/                           @platform-owner
/common/constants.go                     @platform-owner
/testutils/                              @platform-owner
/version/                                @platform-owner
/pkg/metrics/                            @platform-owner
/cmd/                                    @platform-owner
/builder/                                @platform-owner
/config/                                 @platform-owner
/Makefile*                               @platform-owner
```

### 2. PR 标签与路由
| 标签 | 对应 Owner | 自动路由规则 |
|------|-----------|-------------|
| `area/bootstrap` | Cluster Bootstrap Owner | 修改 `ensure_master_init/join`、`ensure_worker_join` 等 Phase |
| `area/upgrade` | Cluster Upgrade Owner | 修改 `ensure_*_upgrade` Phase、upgrade phaseutil |
| `area/teardown` | Cluster Teardown Owner | 修改 `ensure_*_delete` Phase、reset/shutdown Job |
| `area/cert-security` | Certificate & Security Owner | 修改 `pkg/certs/`、`pkiutil/`、`ensure_certs` |
| `area/operations` | Cluster Operations Owner | 修改 `pkg/kube/`、`statusmanage/`、addon 相关 |
| `area/node-exec` | Node Execution Engine Owner | 修改 `pkg/job/`、`pkg/executor/`、bkeagent utils |
| `area/platform` | Platform Foundation Owner | 修改 `api/`、`webhooks/`、PhaseFrame Core |

### 3. 发版责任矩阵
| 发版类型 | 主导 Owner | 协作 Owner |
|---------|-----------|-----------|
| 创建功能发版 | Cluster Bootstrap Owner | Platform、Node Exec |
| 升级功能发版 | Cluster Upgrade Owner | Cert & Security、Node Exec |
| 删除功能发版 | Cluster Teardown Owner | Node Exec |
| 证书轮转发版 | Cert & Security Owner | Cluster Upgrade |
| 运维功能发版 | Cluster Operations Owner | Platform |
| 执行引擎发版 | Node Execution Engine Owner | Platform |
| 框架/平台发版 | Platform Foundation Owner | **所有特性域 Owner** |

## 对比：架构层 Owner vs 功能特性 Owner
| 维度 | 架构层 Owner（前版） | 功能特性 Owner（本版） |
|------|---------------------|----------------------|
| 切分方式 | 水平分层 | 垂直切功能 |
| Owner 视角 | 我负责某一层 | 我负责某一特性全栈 |
| 变更影响 | 一处变更可能涉及多个 Owner 协调 | 一个特性变更通常只涉及一个 Owner |
| 职责边界 | 清晰但割裂 | 端到端完整但需注意横切面 |
| 适合场景 | 框架稳定、业务快速迭代 | 业务特性独立演进、团队按特性分工 |
| 风险 | 跨层协调成本高 | 横切面（API/框架）变更影响面大 |

**推荐**：对于本项目，功能特性 Owner 更适合，因为集群创建/升级/删除是高度独立的业务流程，团队通常按特性分工而非按层分工。Platform Foundation Owner 作为横切面兜底，确保全局一致性。

