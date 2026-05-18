# bkeagent二进制构建链路

## bkeagent 二进制构建入口

根据 [Makefile.bkeagent](file:///D:\code\github\cluster-api-provider-bke\Makefile.bkeagent) 中的构建命令：

```bash
go build -o bin/bkeagent-${HOST_ARCH} ./cmd/bkeagent
```

构建入口是 `./cmd/bkeagent`，即 [cmd/bkeagent/main.go](file:///D:\code\github\cluster-api-provider-bke\cmd\bkeagent\main.go)。

Docker 镜像构建使用 [builder/bkeagent/Dockerfile](file:///D:\code\github\cluster-api-provider-bke\builder\bkeagent\Dockerfile)，同样指定 `ARG PKG=./cmd/bkeagent`。

## bkeagent 二进制包含的代码模块

从 `cmd/bkeagent/main.go` 的 import 链路出发，逐层追踪所有被编译进 bkeagent 二进制的代码：

### 1️⃣ 入口层 — `cmd/bkeagent/`

| 文件 | 功能 |
|------|------|
| [main.go](file:///D:\code\github\cluster-api-provider-bke\cmd\bkeagent\main.go) | 主入口：flag 解析、Manager 创建、Controller 注册、健康检查服务器、NTP 同步 |
| [ntp.go](file:///D:\code\github\cluster-api-provider-bke\cmd\bkeagent\ntp.go) | NTP 时间同步逻辑 |
| [crds.go](file:///D:\code\github\cluster-api-provider-bke\cmd\bkeagent\crds.go) | 启动时自动安装 CRD（`bkeagent.bocloud.com_commands`） |

### 2️⃣ API 层 — `api/bkeagent/v1beta1/`

| 文件 | 功能 |
|------|------|
| [command_types.go](file:///D:\code\github\cluster-api-provider-bke\api\bkeagent\v1beta1\command_types.go) | Command CRD 类型定义 |
| [condition.go](file:///D:\code\github\cluster-api-provider-bke\api\bkeagent\v1beta1\condition.go) | Condition 工具 |
| [groupversion_info.go](file:///D:\code\github\cluster-api-provider-bke\api\bkeagent\v1beta1\groupversion_info.go) | GroupVersion 元信息 |
| [zz_generated.deepcopy.go](file:///D:\code\github\cluster-api-provider-bke\api\bkeagent\v1beta1\zz_generated.deepcopy.go) | 自动生成的 DeepCopy 方法 |

同时通过 `api/bkecommon/v1beta1` 间接引入了 BKECluster、BKENode、BKEConfig 等公共类型。

### 3️⃣ Controller 层 — `controllers/bkeagent/`

| 文件 | 功能 |
|------|------|
| [command_controller.go](file:///D:\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go) | Command Reconciler：监听 Command CR，分发到 Job 执行 |

### 4️⃣ Job 调度层 — `pkg/job/`

| 文件 | 功能 |
|------|------|
| [job.go](file:///D:\code\github\cluster-api-provider-bke\pkg\job\job.go) | Job 结构体，聚合 BuiltIn / K8s / Shell 三类任务执行器 |

### 5️⃣ 内置插件层 — `pkg/job/builtin/`（核心！）

这是 bkeagent 最核心的部分，注册了 **17 个内置插件**，每个插件对应一种节点操作指令：

| 插件名 | 包路径 | 功能 |
|--------|--------|------|
| **containerd** | `builtin/containerruntime/containerd/` | Containerd 安装/配置/升级 |
| **env** | `builtin/kubeadm/env/` | 节点环境准备（内核参数、依赖包、主机名等） |
| **switchcluster** | `builtin/switchcluster/` | 集群切换（Agent 监听切换） |
| **certs** | `builtin/kubeadm/certs/` | 证书管理（生成/更新） |
| **kubelet** | `builtin/kubeadm/kubelet/` | Kubelet 配置/服务管理 |
| **kubeadm** | `builtin/kubeadm/` | Kubeadm 核心操作：init / join / upgrade（Master/Worker/Etcd） |
| **ha** | `builtin/ha/` | 高可用配置（Keepalived + HAProxy） |
| **downloader** | `builtin/downloader/` | 镜像/文件下载 |
| **reset** | `builtin/reset/` | 节点重置/清理 |
| **ping** | `builtin/ping/` | 节点连通性检测 |
| **backup** | `builtin/backup/` | Etcd 备份 |
| **docker** | `builtin/containerruntime/docker/` | Docker 安装/配置 |
| **collect** | `builtin/collect/` | 信息收集/诊断 |
| **manifests** | `builtin/kubeadm/manifests/` | 静态 Pod 清单渲染 |
| **shutdown** | `builtin/shutdown/` | 节点关机 |
| **selfupdate** | `builtin/selfupdate/` | Agent 自升级 |
| **cridocker** | `builtin/containerruntime/cridocker/` | CRI-Dockerd 安装 |
| **preprocess** | `builtin/preprocess/` | 前置脚本处理 |
| **postprocess** | `builtin/postprocess/` | 后置脚本处理 |

其中 **kubeadm** 插件是最复杂的，支持以下 phase 参数：

```
initControlPlane / joinControlPlane / joinWorker 
/ upgradeControlPlane / upgradeWorker / upgradeEtcd
```

### 6️⃣ 其他 Job 类型

| 类型 | 包路径 | 功能 |
|------|--------|------|
| **K8s** | `pkg/job/k8s/` | 从 K8s 读取 ConfigMap/Secret 写入节点文件 |
| **Shell** | `pkg/job/shell/` | 执行 Shell 命令 |

### 7️⃣ 执行器层 — `pkg/executor/exec/`

| 文件 | 功能 |
|------|------|
| [exec.go](file:///D:\code\github\cluster-api-provider-bke\pkg\executor\exec\exec.go) | 命令执行器接口，封装本地命令执行 |

### 8️⃣ 工具层 — `utils/bkeagent/`

| 包 | 功能 |
|-----|------|
| `utils/bkeagent/mfutil/` | Manifest 渲染（etcd/apiserver/controller-manager/scheduler/haproxy/keepalived 静态 Pod 模板） |
| `utils/bkeagent/pkiutil/` | PKI 证书工具（生成 CSR、签发证书、创建 Kubeconfig） |
| `utils/bkeagent/log/` | 日志工具 |
| `utils/bkeagent/kubeclient/` | K8s Client 工具 |
| `utils/bkeagent/clientutil/` | K8s Dynamic Client 工具 |
| `utils/bkeagent/etcd/` | Etcd 操作工具 |
| `utils/bkeagent/download/` | 下载工具 |
| `utils/bkeagent/initsystem/` | Init 系统（systemd）检测 |
| `utils/bkeagent/httprepo/` | HTTP 仓库工具 |
| `utils/bkeagent/option/` | 全局选项（Platform/Version） |
| `utils/bkeagent/mutx/` | 互斥锁（IDLocker） |
| `utils/bkeagent/net/` | 网络工具 |
| `utils/bkeagent/cluster/` | 集群工具 |
| `utils/bkeagent/resetutil/` | 重置清理工具 |

### 9️⃣ 公共层

| 包 | 功能 |
|-----|------|
| `common/cluster/node/` | 节点数据结构 |
| `common/cluster/validation/` | BKEConfig 校验 |
| `common/ntp/` | NTP 时间同步 |
| `common/utils/net/` | 网络工具 |
| `pkg/crontab/` | Crontab 管理（NTP 定时同步） |
| `version/` | 版本信息（通过 -ldflags 注入） |
| `config/` | 嵌入的 CRD YAML（bkeagent.bocloud.com_commands.yaml） |

## 总结：bkeagent 二进制代码架构图

```
cmd/bkeagent/main.go                    ← 入口
├── api/bkeagent/v1beta1/               ← Command CRD 类型
├── controllers/bkeagent/               ← Command Reconciler
│   └── pkg/job/                        ← Job 调度器
│       ├── builtin/                    ← 17 个内置插件
│       │   ├── kubeadm/               ← 核心：init/join/upgrade
│       │   │   ├── env/               ← 节点环境准备
│       │   │   ├── certs/             ← 证书管理
│       │   │   ├── kubelet/           ← Kubelet 管理
│       │   │   └── manifests/         ← 静态Pod清单
│       │   ├── containerruntime/       ← 容器运行时
│       │   │   ├── containerd/
│       │   │   ├── docker/
│       │   │   └── cridocker/
│       │   ├── ha/                     ← 高可用
│       │   ├── backup/                 ← Etcd备份
│       │   ├── reset/                  ← 节点重置
│       │   ├── selfupdate/             ← Agent自升级
│       │   ├── switchcluster/          ← 集群切换
│       │   ├── downloader/             ← 下载器
│       │   ├── ping/                   ← 连通检测
│       │   ├── collect/                ← 信息收集
│       │   ├── shutdown/               ← 关机
│       │   ├── preprocess/             ← 前置脚本
│       │   └── postprocess/            ← 后置脚本
│       ├── k8s/                        ← K8s资源读取
│       ├── shell/                      ← Shell命令执行
│       └── executor/exec/              ← 命令执行器
├── utils/bkeagent/                     ← 节点侧工具集
│   ├── mfutil/ (+ tmpl/)              ← Manifest模板渲染
│   ├── pkiutil/                        ← PKI证书工具
│   ├── etcd/                           ← Etcd操作
│   └── ...                             ← 其他工具
├── pkg/crontab/                        ← Crontab管理
├── common/                             ← 公共库
└── version/                            ← 版本信息
```

**关键点**：bkeagent 是部署在**集群节点上**的 Agent，它通过 Command CR 接收来自 installer-service（capbke controller）的指令，然后在节点本地执行 kubeadm、容器运行时配置、HA 配置等操作。**它不包含 installer-service 中的 Phase 框架和升级编排逻辑**。
        
