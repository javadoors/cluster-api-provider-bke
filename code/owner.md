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

