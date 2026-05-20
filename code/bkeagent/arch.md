# bkeagent 完整架构

## bkeagent 架构图
```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              管理集群 (Management Cluster)                               │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐    │
│  │  BKEClusterReconciler (capbke)                                                  │    │
│  │  ┌───────────────────────────────────────────────────────────────────────────┐  │    │
│  │  │ PhaseFrame: EnsureBKEAgent                                               │  │    │
│  │  │  1. 生成低权限/完整 kubeconfig                                            │  │    │
│  │  │  2. 渲染 bkeagent.service.tmpl                                           │  │    │
│  │  │  3. SSH 批量推送到所有节点                                                 │  │    │
│  │  │     ├─ bkeagent 二进制 → /usr/local/bin/bkeagent                          │  │    │
│  │  │     ├─ bkeagent.service → /etc/systemd/system/bkeagent.service            │  │    │
│  │  │     ├─ kubeconfig → /etc/openFuyao/bkeagent/config                        │  │    │
│  │  │     ├─ 证书链 → /etc/openFuyao/certs/                                     │  │    │
│  │  │     └─ 全局 CA (cluster-api 场景)                                         │  │    │
│  │  │  4. systemctl enable && restart bkeagent                                  │  │    │
│  │  └───────────────────────────────────────────────────────────────────────────┘  │    │
│  │                                                                                 │    │
│  │  BKEMachineReconciler (capbke)                                                  │    │
│  │  ┌───────────────────────────────────────────────────────────────────────────┐  │    │
│  │  │ 创建 Command CR 下发任务到 bkeagent                                       │  │    │
│  │  │  • spec.nodeName / spec.nodeSelector → 目标节点选择                       │  │    │
│  │  │  • spec.commands[] → 有序指令列表                                         │  │    │
│  │  │  • spec.suspend → 暂停/恢复                                              │  │    │
│  │  │  • spec.backoffLimit → 失败重试                                           │  │    │
│  │  │  • spec.activeDeadlineSecond → 超时控制                                   │  │    │
│  │  │  • spec.ttlSecondsAfterFinished → 完成后自动清理                          │  │    │
│  │  └───────────────────────────────────────────────────────────────────────────┘  │    │
│  └─────────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐    │
│  │  Command CR (bkeagent.bocloud.com/v1beta1)                                      │    │
│  │  ┌─────────────────────────────────────────────────────────────────────────┐    │    │
│  │  │ spec:                                                                   │    │    │
│  │  │   nodeName: "master-01"                                                 │    │    │
│  │  │   commands:                                                             │    │    │
│  │  │     - id: "step1"                                                       │    │    │
│  │  │       command: ["env", "ipv4"]                                          │    │    │
│  │  │       type: BuiltIn                                                     │    │    │
│  │  │     - id: "step2"                                                       │    │    │
│  │  │       command: ["kubeadm", "init", ...]                                 │    │    │
│  │  │       type: BuiltIn                                                     │    │    │
│  │  │     - id: "step3"                                                       │    │    │
│  │  │       command: ["iptables --list"]                                      │    │    │
│  │  │       type: Shell                                                       │    │    │
│  │  │ status:                                                                 │    │    │
│  │  │   "master-01/10.0.0.1":                                                 │    │    │
│  │  │     phase: Completed                                                    │    │    │
│  │  │     conditions: [...]                                                   │    │    │
│  │  └─────────────────────────────────────────────────────────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────────────────┘

                                    │
                                    │ Watch Command CR
                                    │ (kubeconfig 连接)
                                    ▼

┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                     业务集群节点 (Worker/Master Node)                                    │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐    │
│  │                    bkeagent 进程 (systemd 托管)                                  │    │
│  │                                                                                 │    │
│  │  ┌───────────────────────────────────────────────────────────────────────────┐  │    │
│  │  │  入口: cmd/bkeagent/main.go                                               │  │    │
│  │  │                                                                           │  │    │
│  │  │  1. enableCrdHasInstalled() → 确保 Command CRD 已安装                     │  │    │
│  │  │  2. startHealthServer(port) → HTTP /healthz 健康检查                      │  │    │
│  │  │  3. ctrl.Manager → controller-runtime Manager                            │  │    │
│  │  │  4. NewJob() → 初始化三种执行器                                            │  │    │
│  │  │  5. CommandReconciler → 注册 Watch Command CR                             │  │    │
│  │  │  6. mgr.Start() → 启动事件循环                                            │  │    │
│  │  └───────────────────────────────────────────────────────────────────────────┘  │    │
│  │                                                                                 │    │
│  │  ┌───────────────────────────────────────────────────────────────────────────┐  │    │
│  │  │  CommandReconciler (controllers/bkeagent/command_controller.go)            │  │    │
│  │  │                                                                           │  │    │
│  │  │  Watch 过滤:                                                              │  │    │
│  │  │  ├─ spec.nodeName == 本机 hostname                                        │  │    │
│  │  │  └─ spec.nodeSelector 匹配本机标签                                        │  │    │
│  │  │                                                                           │  │    │
│  │  │  Reconcile 流程:                                                          │  │    │
│  │  │  ┌─────────────────────────────────────────────────────────────────────┐  │  │    │
│  │  │  │ 1. fetchCommand          获取 Command 对象                           │  │  │    │
│  │  │  │ 2. ensureStatusInitialized  初始化 status[本机key]                   │  │  │    │
│  │  │  │ 3. handleFinalizer        添加/清理 finalizer                        │  │  │    │
│  │  │  │ 4. handleSuspend          暂停处理 (SafeClose stopChan)              │  │  │    │
│  │  │  │ 5. shouldSkipOldTask      跳过旧版本 (generation 检查)               │  │  │    │
│  │  │  │ 6. createAndStartTask     创建 Task → go startTask()                 │  │  │    │
│  │  │  └─────────────────────────────────────────────────────────────────────┘  │  │    │
│  │  │                                                                           │  │    │
│  │  │  startTask (goroutine):                                                   │  │    │
│  │  │  ┌─────────────────────────────────────────────────────────────────────┐  │  │    │
│  │  │  │ for each command in spec.commands:                                  │  │  │    │
│  │  │  │   ├─ 检查 stopChan (可被新版本任务中断)                              │  │  │    │
│  │  │  │   ├─ 检查 activeDeadlineSecond 超时                                 │  │  │    │
│  │  │  │   ├─ 跳过已完成的 condition (断点续执行)                             │  │  │    │
│  │  │  │   ├─ executeByType(type, command) → 三种执行器                      │  │  │    │
│  │  │  │   ├─ executeWithRetry → backoffLimit 重试                           │  │  │    │
│  │  │  │   ├─ backoffIgnore → 失败跳过                                       │  │  │    │
│  │  │  │   └─ syncStatusUntilComplete → 实时更新 status                       │  │  │    │
│  │  │  │ finalizeTaskStatus → 统计 Succeeded/Failed/Phase                    │  │  │    │
│  │  │  └─────────────────────────────────────────────────────────────────────┘  │  │    │
│  │  │                                                                           │  │    │
│  │  │  TTL 清理:                                                                │  │    │
│  │  │  ┌─────────────────────────────────────────────────────────────────────┐  │  │    │
│  │  │  │ ttlSecondAfterFinished (后台 goroutine)                             │  │  │    │
│  │  │  │ 定期扫描 Task 列表，完成后按 TTL 延迟删除 Command CR                │  │  │    │
│  │  │  └─────────────────────────────────────────────────────────────────────┘  │  │    │
│  │  └───────────────────────────────────────────────────────────────────────────┘  │    │
│  │                                                                                 │    │
│  │  ┌───────────────────────────────────────────────────────────────────────────┐  │    │
│  │  │  Job 执行器 (pkg/job/job.go)                                               │  │    │
│  │  │                                                                           │  │    │
│  │  │  ┌──────────────────┐  ┌──────────────────┐  ┌────────────────────────┐  │  │    │
│  │  │  │  BuiltIn 执行器   │  │  Shell 执行器     │  │  Kubernetes 执行器     │  │  │    │
│  │  │  │  (plugin 注册表) │  │  (原生 Shell)     │  │  (ConfigMap/Secret)   │  │  │    │
│  │  │  │                  │  │                  │  │                        │  │  │    │
│  │  │  │  pluginRegistry: │  │  Shell.Execute() │  │  K8s.Execute()        │  │  │    │
│  │  │  │  ┌─────────────┐│  │  /bin/sh -c ...  │  │  格式:                 │  │  │    │
│  │  │  │  │ containerd  ││  │                  │  │  type:ns/name:op:path  │  │  │    │
│  │  │  │  │ env         ││  │                  │  │  op: ro|rx|rw          │  │  │    │
│  │  │  │  │ switchcluster│  │                  │  │  ro: 读资源→写文件      │  │  │    │
│  │  │  │  │ certs       ││  │                  │  │  rx: 读资源→执行脚本    │  │  │    │
│  │  │  │  │ kubelet     ││  │                  │  │  rw: 读文件→写资源      │  │  │    │
│  │  │  │  │ kubeadm     ││  │                  │  │                        │  │  │    │
│  │  │  │  │ ha          ││  │                  │  │                        │  │  │    │
│  │  │  │  │ downloader  ││  │                  │  │                        │  │  │    │
│  │  │  │  │ reset       ││  │                  │  │                        │  │  │    │
│  │  │  │  │ ping        ││  │                  │  │                        │  │  │    │
│  │  │  │  │ backup      ││  │                  │  │                        │  │  │    │
│  │  │  │  │ docker      ││  │                  │  │                        │  │  │    │
│  │  │  │  │ collect     ││  │                  │  │                        │  │  │    │
│  │  │  │  │ manifests   ││  │                  │  │                        │  │  │    │
│  │  │  │  │ shutdown    ││  │                  │  │                        │  │  │    │
│  │  │  │  │ selfupdate  ││  │                  │  │                        │  │  │    │
│  │  │  │  │ cri-docker  ││  │                  │  │                        │  │  │    │
│  │  │  │  │ preprocess  ││  │                  │  │                        │  │  │    │
│  │  │  │  │ postprocess ││  │                  │  │                        │  │  │    │
│  │  │  │  └─────────────┘│  │                  │  │                        │  │  │    │
│  │  │  └──────────────────┘  └──────────────────┘  └────────────────────────┘  │  │    │
│  │  │                                                                           │  │    │
│  │  │  底层执行:                                                                 │  │    │
│  │  │  ┌─────────────────────────────────────────────────────────────────────┐  │  │    │
│  │  │  │  CommandExecutor (pkg/executor/exec)                                │  │  │    │
│  │  │  │  • ExecuteCommand            同步执行                               │  │  │    │
│  │  │  │  • ExecuteCommandWithOutput   带输出执行                             │  │  │    │
│  │  │  │  • ExecuteCommandWithTimeout  带超时执行                             │  │  │    │
│  │  │  │  • ExecuteCommandResidentBinary 常驻进程启动                         │  │  │    │
│  │  │  └─────────────────────────────────────────────────────────────────────┘  │  │    │
│  │  └───────────────────────────────────────────────────────────────────────────┘  │    │
│  └─────────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐    │
│  │  bkeagent-launcher (InitContainer)                                              │    │
│  │  ┌───────────────────────────────────────────────────────────────────────────┐  │    │
│  │  │  入口: cmd/bkeagent-launcher/main.go                                      │  │    │
│  │  │                                                                           │  │    │
│  │  │  运行环境: 容器内 (container=true)                                         │  │    │
│  │  │  通信方式: nsenter -t 1 -m -u -i -n -p (进入宿主命名空间)                  │  │    │
│  │  │                                                                           │  │    │
│  │  │  startPre():                                                              │  │    │
│  │  │  ├─ systemctl stop bkeagent (停止旧服务)                                  │  │    │
│  │  │  ├─ getHostname() (获取宿主机名)                                          │  │    │
│  │  │  ├─ prepareBkeagentBinary() (复制二进制到宿主机)                           │  │    │
│  │  │  ├─ prepareBkeagentService() (渲染 service 模板到宿主机)                   │  │    │
│  │  │  ├─ prepareKubeconfig() (验证并复制 kubeconfig 到宿主机)                   │  │    │
│  │  │  └─ prepareNodeFile() (写入节点名到宿主机)                                │  │    │
│  │  │                                                                           │  │    │
│  │  │  start():                                                                 │  │    │
│  │  │  ├─ systemctl daemon-reload                                               │  │    │
│  │  │  ├─ systemctl start bkeagent                                              │  │    │
│  │  │  └─ systemctl enable bkeagent                                             │  │    │
│  │  │                                                                           │  │    │
│  │  │  startPost():                                                             │  │    │
│  │  │  └─ HTTP :3377/readyz → pingBKEAgent() (就绪探针)                        │  │    │
│  │  └───────────────────────────────────────────────────────────────────────────┘  │    │
│  └─────────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐    │
│  │  宿主机文件系统布局                                                              │    │
│  │  /usr/local/bin/bkeagent                    ← 二进制                           │    │
│  │  /etc/systemd/system/bkeagent.service       ← systemd 服务                    │    │
│  │  /etc/openFuyao/bkeagent/                                                    │    │
│  │  ├── config                                 ← kubeconfig (连接管理集群)        │    │
│  │  ├── node                                   ← 节点名                           │    │
│  │  └── launcher/                              ← launcher 工作目录                │    │
│  │      ├── bkeagent                           ← 二进制备份                       │    │
│  │      ├── bkeagent.service                   ← 服务文件备份                     │    │
│  │      ├── config                             ← kubeconfig 备份                  │    │
│  │      └── node                               ← 节点名备份                       │    │
│  │  /etc/openFuyao/certs/                      ← 证书链                           │    │
│  └─────────────────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### bkeagent 两种部署模式对比

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        模式 1: SSH 推送 (PhaseFrame)                                 │
│                                                                                     │
│  管理集群 BKEClusterReconciler                                                      │
│       │                                                                             │
│       │ EnsureBKEAgent Phase                                                        │
│       │                                                                             │
│       ├─ 1. 生成 kubeconfig (低权限/完整)                                           │
│       ├─ 2. 渲染 bkeagent.service.tmpl                                              │
│       ├─ 3. SSH 连接目标节点                                                         │
│       │     ├─ 上传 bkeagent 二进制                                                  │
│       │     ├─ 上传 bkeagent.service                                                 │
│       │     ├─ 写入 kubeconfig                                                      │
│       │     ├─ 上传证书链                                                            │
│       │     └─ systemctl enable && restart bkeagent                                  │
│       └─ 4. PingBKEAgent 验证就绪                                                    │
│                                                                                     │
│  适用场景: 首次安装集群、批量推送升级                                                   │
│  kubeconfig 权限:                                                                    │
│    • 无 cluster-api addon → 低权限 (仅操作 Command CR)                               │
│    • 有 cluster-api addon → 完整权限 (操作所有资源)                                   │
└─────────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────────┐
│                     模式 2: Launcher 容器 (DaemonSet)                                │
│                                                                                     │
│  管理集群                                                                            │
│       │                                                                             │
│       │ 部署 bkeagent-launcher DaemonSet                                             │
│       │                                                                             │
│       ├─ InitContainer (bkeagent-launcher)                                          │
│       │     ├─ 检测 container=true (确保在容器中运行)                                 │
│       │     ├─ nsenter 进入宿主命名空间                                               │
│       │     ├─ 复制 bkeagent 二进制 → /usr/local/bin/                                │
│       │     ├─ 渲染 bkeagent.service → /etc/systemd/system/                          │
│       │     ├─ 复制 kubeconfig → /etc/openFuyao/bkeagent/config                      │
│       │     └─ systemctl start && enable bkeagent                                    │
│       │                                                                             │
│       └─ Container (等待)                                                            │
│             └─ HTTP :3377/readyz → 检查 bkeagent 服务状态                             │
│                                                                                     │
│  适用场景: 已有集群纳管、通过 K8s 调度部署                                              │
│  特点: 不需要 SSH，利用 K8s 调度 + nsenter 操作宿主机                                  │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### bkeagent 核心数据流

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                          Command CR 生命周期                                      │
│                                                                                  │
│  管理集群                              业务集群节点                               │
│  ─────────                             ─────────────                             │
│                                                                                  │
│  BKEMachineReconciler                 CommandReconciler                           │
│       │                                     │                                    │
│       │ 创建 Command CR                      │ Watch 到匹配的 Command              │
│       ├────────────────────────────────────►│                                    │
│       │                                     │                                    │
│       │                                     ├─ 初始化 status[本机key]              │
│       │                                     │  Phase: Running                     │
│       │                                     │                                    │
│       │                                     ├─ 创建 Task (goroutine)              │
│       │                                     │  ┌────────────────────────────┐     │
│       │                                     │  │ 顺序执行 spec.commands:    │     │
│       │                                     │  │                            │     │
│       │                                     │  │ cmd1 ──► cmd2 ──► cmd3    │     │
│       │                                     │  │  │        │         │      │     │
│       │                                     │  │  ▼        ▼         ▼      │     │
│       │                                     │  │ BuiltIn  Shell    K8s     │     │
│       │                                     │  │  │        │         │      │     │
│       │                                     │  │  ▼        ▼         ▼      │     │
│       │                                     │  │ CommandExecutor          │     │
│       │                                     │  │ (os/exec)                │     │
│       │                                     │  └────────────────────────────┘     │
│       │                                     │                                    │
│       │  读取 status 实时更新                 ├─ 每条指令完成后 syncStatus          │
│       │◄────────────────────────────────────│  conditions[].phase=Complete       │
│       │                                     │                                    │
│       │                                     ├─ 全部完成: finalizeTaskStatus       │
│       │                                     │  Phase: Completed                   │
│       │                                     │                                    │
│       │                                     ├─ TTL 到期后自动删除 Command CR       │
│       │                                     │                                    │
│       │  根据 status 决定下一步操作            │                                    │
│       │  (继续/重试/失败处理)                 │                                    │
│       └─────────────────────────────────────┘                                    │
│                                                                                  │
│  关键特性:                                                                       │
│  ┌──────────────────────────────────────────────────────────────────────────┐    │
│  │ 1. 断点续执行: conditions 记录每条指令状态，重启后跳过已完成指令           │    │
│  │ 2. 多节点并发: status 按 "hostname/ip" 分区，每节点独立执行               │    │
│  │ 3. 版本控制: generation 检查，新版本自动中断旧任务                         │    │
│  │ 4. 暂停/恢复: suspend 字段控制，SafeClose stopChan 优雅停止               │    │
│  │ 5. 失败重试: backoffLimit + backoffDelay + backoffIgnore                  │    │
│  │ 6. 超时控制: activeDeadlineSecond，超时自动终止                           │    │
│  │ 7. 自动清理: ttlSecondsAfterFinished，完成后延迟删除 CR                    │    │
│  └──────────────────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────────────────┘
```

### bkeagent 内置插件全景

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                    BuiltIn Plugin Registry (18 个插件)                            │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  集群初始化与生命周期                                                       │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │  │
│  │  │ kubeadm      │  │ kubelet      │  │ certs        │  │ manifests    │  │  │
│  │  │ init/join    │  │ 服务配置      │  │ 证书分发/轮换 │  │ 清单渲染     │  │  │
│  │  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘  │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │  │
│  │  │ env          │  │ ha           │  │ switchcluster│  │ reset        │  │  │
│  │  │ 环境初始化    │  │ HAProxy+Keep │  │ 切换管理集群  │  │ 集群重置     │  │  │
│  │  │ (centos/ipv4)│  │ alived 配置  │  │ kubeconfig   │  │ 清理阶段     │  │  │
│  │  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘  │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  容器运行时                                                                  │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                     │  │
│  │  │ containerd   │  │ docker       │  │ cri-docker   │                     │  │
│  │  │ 配置/启动     │  │ 配置/启动     │  │ CRI-Dockerd  │                     │  │
│  │  └──────────────┘  └──────────────┘  └──────────────┘                     │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  运维操作                                                                    │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │  │
│  │  │ ping         │  │ backup       │  │ collect      │  │ selfupdate   │  │  │
│  │  │ 健康检查      │  │ etcd 备份    │  │ 信息采集      │  │ Agent 自更新 │  │  │
│  │  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘  │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                     │  │
│  │  │ shutdown     │  │ downloader   │  │ preprocess   │                     │  │
│  │  │ 节点关机      │  │ 文件下载      │  │ 前置处理      │                     │  │
│  │  └──────────────┘  └──────────────┘  └──────────────┘                     │  │
│  │  ┌──────────────┐                                                          │  │
│  │  │ postprocess  │                                                          │  │
│  │  │ 后置处理      │                                                          │  │
│  │  └──────────────┘                                                          │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  插件接口:                                                                       │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  type Plugin interface {                                                   │  │
│  │      Name() string                                                         │  │
│  │      Param() map[string]PluginParam                                        │  │
│  │      Execute(commands []string) ([]string, error)                          │  │
│  │  }                                                                         │  │
│  │                                                                            │  │
│  │  调用方式: ["pluginName", "key1=value1", "key2=value2"]                     │  │
│  │  示例:     ["env", "ipv4", "centos"]                                       │  │
│  │            ["kubeadm", "init", "config=ns/name"]                           │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────────┘
```
**架构要点总结**：
1. **双进程架构**：`bkeagent-launcher`（容器内 InitContainer）负责安装，`bkeagent`（宿主机 systemd 服务）负责运行。Launcher 通过 `nsenter` 突破容器隔离操作宿主机。
2. **声明式命令模型**：管理集群通过 `Command CR` 下发指令，bkeagent Watch 匹配本机的 Command 并顺序执行。status 按 `hostname/ip` 分区，支持多节点并发执行同一 Command。
3. **三层执行器**：`BuiltIn`（18 个内置插件，覆盖 kubeadm/容器运行时/HA/证书等）、`Shell`（原生命令执行）、`Kubernetes`（ConfigMap/Secret 读写执行）。
4. **可靠性设计**：断点续执行（conditions 记录每条指令状态）、版本控制（generation 检查自动中断旧任务）、暂停/恢复（stopChan）、失败重试（backoffLimit）、超时控制（activeDeadlineSecond）、自动清理（TTL）。

# 优化建议和重构方案。

## bkeagent 耦合问题分析与重构方案

### 一、当前问题诊断

通过深入分析代码，我识别出以下 **7 大耦合问题**：

#### 问题 1：插件注册表是全局可变状态，无隔离

[builtin.go:41](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\builtin.go#L41) 中 `pluginRegistry` 是包级全局变量：

```go
var pluginRegistry = map[string]plugin.Plugin{}
```

- 所有插件在 `New()` 中硬编码注册，无法动态加载/卸载
- 无法为不同 Command 提供不同的插件集合
- 测试时无法隔离，全局状态会污染
- 无法实现插件的热更新

#### 问题 2：插件内部直接访问集群资源，绕过依赖注入

[plugin/interface.go](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\plugin\interface.go) 中 `GetBkeConfig`、`GetBKECluster`、`GetClusterData` 等函数直接通过硬编码的 kubeconfig 路径创建 Kubernetes 客户端：

```go
var kubeconfig = fmt.Sprintf("%s/%s", utils.Workspace, "config")

func GetBkeConfig(bkeConfigNS string) (*bkev1beta1.BKEConfig, error) {
    c, err := clientutil.NewKubernetesClient(kubeconfig)  // 硬编码路径
    ...
}
```

- kubeadm、env、reset、manifests 等多个插件都通过 `plugin.GetBkeConfig()` 隐式访问集群
- 无法控制访问权限范围
- 无法在测试中替换为 mock client
- kubeconfig 路径散落在多处（[switch.go:38](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\switchcluster\switch.go#L38)、[interface.go:36](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\plugin\interface.go#L36)、[kubelet/run.go:199](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\kubeadm\kubelet\run.go#L199)）

#### 问题 3：kubeadm 插件是"上帝插件"，职责过重

[kubeadm.go](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\kubeadm\kubeadm.go) 承载了 6 种 phase（initControlPlane、joinControlPlane、joinWorker、upgradeControlPlane、upgradeWorker、upgradeEtcd），内部包含：
- 证书管理（调用 certs 子插件）
- 静态 Pod 管理（调用 manifests 子插件）
- kubelet 安装/升级
- etcd 备份
- 组件升级等待与 hash 校验
- BKEConfig 解析与集群类型判断
- Global CA 上传

单个插件近 600 行代码，任何一个子功能变更都可能影响其他 phase。

#### 问题 4：preprocess/postprocess 大量代码重复

[preprocess.go](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\preprocess\preprocess.go) 和 [postprocess.go](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\postprocess\postprocess.go) 结构几乎完全一致：
- `loadConfig()` 逻辑完全相同（全局 > 批次 > 节点优先级）
- `parseConfig()` 逻辑完全相同
- `getAllScripts()` 逻辑完全相同（仅 label key 不同）
- `executeScript()` 逻辑完全相同
- `validateParams()` 逻辑完全相同
- `renderScriptWithParams()` 逻辑完全相同

唯一的区别是 ConfigMap 名称前缀和 label selector。

#### 问题 5：危险操作缺乏安全边界

- [switchcluster/switch.go:128](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\switchcluster\switch.go#L128) 直接调用 `os.Exit(1)` 终止进程
- [shutdown/shutdown.go:40](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\shutdown\shutdown.go#L40) 直接调用 `os.Exit(0)` 关机
- [selfupdate/selfupdate.go](file:///D:\code\github\cluster-api-provider-bke\pkg\job\builtin\selfupdate\selfupdate.go) 通过 nohup 后台执行 update.sh 替换自身二进制
- 这些操作直接操纵进程生命周期，绕过了 CommandReconciler 的任务管理

#### 问题 6：CommandReconciler 与 Job 执行器耦合

[command_controller.go](file:///D:\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go) 中 `CommandReconciler` 直接持有 `job.Job`，而 `Job` 结构体包含了所有执行器和全局 Task Map：

```go
type CommandReconciler struct {
    ...
    Job       job.Job
}
```

- Reconciler 既负责 CR 生命周期管理，又负责任务调度和执行
- Task Map 是内存状态，进程重启后丢失
- 无法替换为分布式任务调度器

#### 问题 7：插件接口过于简化，缺乏生命周期和上下文传递

```go
type Plugin interface {
    Name() string
    Param() map[string]PluginParam
    Execute(commands []string) ([]string, error)
}
```

- 没有 `Init()` / `Shutdown()` 生命周期钩子
- 没有 context 传递（无法取消长时间运行的操作）
- 没有进度回调（无法报告中间状态）
- 没有能力声明（无法描述插件的资源需求、权限需求）

### 二、重构方案

#### 方案总览：分层解耦 + 插件化 + 声明式能力

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                         重构后 bkeagent 架构                                      │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  Layer 1: 核心调度层 (Core Scheduler)                                      │  │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌────────────────────────┐  │  │
│  │  │ CommandReconciler│  │ TaskManager      │  │ PluginRegistry        │  │  │
│  │  │ CR 生命周期管理   │  │ 任务调度/超时/重试│  │ 插件注册/发现/能力查询 │  │  │
│  │  └──────────────────┘  └──────────────────┘  └────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  Layer 2: 执行引擎层 (Execution Engine)                                    │  │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌────────────────────────┐  │  │
│  │  │ PluginExecutor   │  │ ShellExecutor    │  │ K8sResourceExecutor   │  │  │
│  │  │ 统一插件执行      │  │ Shell 命令执行    │  │ ConfigMap/Secret 操作 │  │  │
│  │  └──────────────────┘  └──────────────────┘  └────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  Layer 3: 插件层 (Plugins) — 按域拆分                                      │  │
│  │  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐             │  │
│  │  │ runtime    │ │ kubernetes │ │ infra      │ │ ops        │             │  │
│  │  │ 容器运行时  │ │ K8s 组件   │ │ 基础设施   │ │ 运维操作   │             │  │
│  │  │ containerd │ │ control-   │ │ ha         │ │ backup     │             │  │
│  │  │ docker     │ │ plane      │ │ env        │ │ collect    │             │  │
│  │  │ cri-docker │ │ worker     │ │ downloader │ │ selfupdate │             │  │
│  │  │            │ │ cert       │ │ reset      │ │ script     │             │  │
│  │  │            │ │ kubelet    │ │ ping       │ │            │             │  │
│  │  │            │ │ etcd       │ │            │ │            │             │  │
│  │  └────────────┘ └────────────┘ └────────────┘ └────────────┘             │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  Layer 4: 基础设施层 (Infrastructure)                                      │  │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌────────────────────────┐  │  │
│  │  │ CommandExecutor  │  │ ClusterAccessor  │  │ EventReporter         │  │  │
│  │  │ 底层命令执行      │  │ 集群资源访问代理  │  │ 事件/进度上报          │  │  │
│  │  └──────────────────┘  └──────────────────┘  └────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────────┘
```

#### 重构项 1：插件注册表 — 从全局变量到依赖注入

**现状**：`pluginRegistry` 是包级全局 map，`New()` 硬编码注册 18 个插件

**目标**：每个 `CommandReconciler` 实例持有独立的 `PluginRegistry`，支持动态注册

```go
// pkg/plugin/registry.go
type PluginRegistry struct {
    plugins map[string]Plugin
    mu      sync.RWMutex
}

func NewRegistry() *PluginRegistry {
    return &PluginRegistry{
        plugins: make(map[string]Plugin),
    }
}

func (r *PluginRegistry) Register(p Plugin) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    name := strings.ToLower(p.Name())
    if _, exists := r.plugins[name]; exists {
        return fmt.Errorf("plugin %q already registered", name)
    }
    r.plugins[name] = p
    return nil
}

func (r *PluginRegistry) Get(name string) (Plugin, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    p, ok := r.plugins[strings.ToLower(name)]
    return p, ok
}

func (r *PluginRegistry) List() []Plugin {
    r.mu.RLock()
    defer r.mu.RUnlock()
    var result []Plugin
    for _, p := range r.plugins {
        result = append(result, p)
    }
    return result
}
```

**注册方式改为显式组合**：

```go
// cmd/bkeagent/plugins.go
func registerAllPlugins(registry *plugin.Registry, exec exec.Executor, k8sClient client.Client) error {
    // Runtime 域
    for _, p := range runtime.Plugins(exec) {
        if err := registry.Register(p); err != nil {
            return err
        }
    }
    // Kubernetes 域
    for _, p := range kubernetes.Plugins(exec, k8sClient) {
        if err := registry.Register(p); err != nil {
            return err
        }
    }
    // Infra 域
    for _, p := range infra.Plugins(exec, k8sClient) {
        if err := registry.Register(p); err != nil {
            return err
        }
    }
    // Ops 域
    for _, p := range ops.Plugins(exec, k8sClient) {
        if err := registry.Register(p); err != nil {
            return err
        }
    }
    return nil
}
```
#### 重构项 2：插件接口增强 — 支持生命周期、上下文、能力声明

**现状**：`Plugin` 接口只有 `Name()/Param()/Execute()`

**目标**：增强接口，支持上下文传递、进度上报、能力声明

```go
// pkg/plugin/interface.go
type Plugin interface {
    Name() string
    Param() map[string]PluginParam
    
    // 能力声明：描述插件需要的权限和资源
    Capabilities() PluginCapabilities
    
    // 带上下文执行：支持取消、超时、进度上报
    Execute(ctx PluginContext, commands []string) ([]string, error)
}

type PluginCapabilities struct {
    // 需要的集群访问权限
    RequiresClusterAccess bool
    // 需要的 K8s 资源权限 (RBAC)
    RequiredRBAC []string
    // 是否为危险操作（影响进程生命周期）
    Dangerous bool
    // 是否支持取消
    Cancelable bool
    // 最大执行时间 (0 = 无限制)
    MaxExecutionTime time.Duration
}

type PluginContext interface {
    context.Context
    
    // 获取集群资源访问代理（替代 plugin.GetBkeConfig 等全局函数）
    ClusterAccessor() ClusterAccessor
    
    // 报告执行进度
    ReportProgress(step string, progress int32)
    
    // 获取节点信息
    NodeInfo() NodeInfo
}

type ClusterAccessor interface {
    GetBKEConfig(ctx context.Context, nsName string) (*bkev1beta1.BKEConfig, error)
    GetBKECluster(ctx context.Context, nsName string) (*bkev1beta1.BKECluster, error)
    GetClusterData(ctx context.Context, nsName string) (*plugin.ClusterData, error)
    GetNodesData(ctx context.Context, nsName string) (bkenode.Nodes, error)
    GetContainerdConfig(ctx context.Context, nsName string) (*bkev1beta1.ContainerdConfigSpec, error)
}
```
**关键改进**：
- `ClusterAccessor` 替代了 `plugin.GetBkeConfig()` 等全局函数，通过依赖注入传入
- `PluginContext` 继承 `context.Context`，支持取消
- `PluginCapabilities` 声明式描述插件能力，调度层可据此做安全检查

#### 重构项 3：拆分 kubeadm 上帝插件
**现状**：kubeadm 插件 600 行，承载 6 种 phase

**目标**：按职责拆分为独立插件，每个插件只做一件事
```
kubeadm/ (当前)                    kubernetes/ (重构后)
├── kubeadm.go (600行)     →      ├── controlplane/
│   ├── initControlPlane          │   ├── init.go      (initControlPlane)
│   ├── joinControlPlane          │   └── join.go      (joinControlPlane)
│   ├── upgradeControlPlane       │   └── upgrade.go   (upgradeControlPlane)
│   └── upgradeEtcd               ├── worker/
│   ├── joinWorker                │   ├── join.go      (joinWorker)
│   └── upgradeWorker             │   └── upgrade.go   (upgradeWorker)
├── certs/                        ├── etcd/
├── kubelet/                      │   └── upgrade.go   (upgradeEtcd)
├── env/                          ├── certs/           (保持)
└── manifests/                    ├── kubelet/         (保持)
                                  ├── env/             (保持)
                                  └── manifests/       (保持)
```

每个子插件独立实现 `Plugin` 接口：

```go
// pkg/plugin/kubernetes/controlplane/init.go
type InitControlPlanePlugin struct {
    exec           exec.Executor
    clusterAccessor plugin.ClusterAccessor
}

func (p *InitControlPlanePlugin) Name() string { return "init-control-plane" }

func (p *InitControlPlanePlugin) Execute(ctx plugin.PluginContext, commands []string) ([]string, error) {
    paramMap, err := plugin.ParseCommands(p, commands)
    if err != nil {
        return nil, err
    }
    
    config, err := ctx.ClusterAccessor().GetBKEConfig(ctx, paramMap["bkeConfig"])
    if err != nil {
        return nil, err
    }
    
    // 1. 安装证书
    ctx.ReportProgress("installing-certs", 10)
    if err := p.installCerts(ctx, config); err != nil {
        return nil, err
    }
    
    // 2. 生成静态 Pod
    ctx.ReportProgress("generating-manifests", 30)
    if err := p.generateManifests(ctx, config); err != nil {
        return nil, err
    }
    
    // 3. 安装 kubelet
    ctx.ReportProgress("installing-kubelet", 60)
    ...
    
    return nil, nil
}
```

#### 重构项 4：消除 preprocess/postprocess 重复代码

**现状**：preprocess 和 postprocess 代码 90% 相同

**目标**：抽取通用脚本执行引擎，两者仅配置不同

```go
// pkg/plugin/ops/script/engine.go
type ScriptEngine struct {
    exec      exec.Executor
    k8sClient client.Client
    config    ScriptEngineConfig
}

type ScriptEngineConfig struct {
    PluginName       string
    LabelSelector    string
    ConfigPrefix     string   // "preprocess" or "postprocess"
    ScriptStoreDir   string
}

func (e *ScriptEngine) Execute(ctx plugin.PluginContext, commands []string) ([]string, error) {
    paramMap, err := plugin.ParseCommands(e, commands)
    if err != nil {
        return nil, err
    }
    
    nodeIP := paramMap["nodeIP"]
    if nodeIP == "" {
        nodeIP, err = scriptutil.GetCurrentNodeIP()
        if err != nil {
            return nil, err
        }
    }
    
    config, err := e.loadConfig(ctx, nodeIP)
    if err != nil {
        return nil, err
    }
    
    allScripts, err := e.getAllScripts(ctx)
    if err != nil {
        return nil, err
    }
    
    return e.executeScripts(ctx, config, allScripts, nodeIP)
}

// pkg/plugin/ops/script/preprocess.go
func NewPreprocessPlugin(exec exec.Executor, k8sClient client.Client) plugin.Plugin {
    return &ScriptEngine{
        exec:      exec,
        k8sClient: k8sClient,
        config: ScriptEngineConfig{
            PluginName:    "Preprocess",
            LabelSelector: "bke.preprocess.script",
            ConfigPrefix:  "preprocess",
            ScriptStoreDir: filepath.Join(utils.AgentScripts, "preprocess"),
        },
    }
}

// pkg/plugin/ops/script/postprocess.go
func NewPostprocessPlugin(exec exec.Executor, k8sClient client.Client) plugin.Plugin {
    return &ScriptEngine{
        exec:      exec,
        k8sClient: k8sClient,
        config: ScriptEngineConfig{
            PluginName:    "Postprocess",
            LabelSelector: "bke.postprocess.script",
            ConfigPrefix:  "postprocess",
            ScriptStoreDir: filepath.Join(utils.AgentScripts, "postprocess"),
        },
    }
}
```

#### 重构项 5：安全边界 — 危险操作隔离

**现状**：switchcluster 直接 `os.Exit(1)`，shutdown 直接 `os.Exit(0)`

**目标**：危险操作通过信号机制通知调度层，由调度层决定如何处理

```go
// pkg/plugin/signal.go
type PluginSignal string

const (
    SignalRestart   PluginSignal = "Restart"    // 需要重启 agent
    SignalShutdown  PluginSignal = "Shutdown"   // 需要关机
    SignalSwitchCluster PluginSignal = "SwitchCluster" // 需要切换集群
)

type SignalError struct {
    Signal  PluginSignal
    Message string
    Payload map[string]string
}

func (e *SignalError) Error() string {
    return fmt.Sprintf("plugin signal: %s, message: %s", e.Signal, e.Message)
}

// switchcluster 插件改造
func (s *SwitchClusterPlugin) Execute(ctx plugin.PluginContext, commands []string) ([]string, error) {
    ...
    // 不再直接 os.Exit(1)，而是返回 SignalError
    return []string{"The listening cluster switch will take place in 30 seconds"}, 
        &plugin.SignalError{
            Signal:  plugin.SignalSwitchCluster,
            Message: "switching to new cluster",
            Payload: map[string]string{
                "kubeconfig":  newKubeconfigPath,
                "nodeName":    nodeName,
                "clusterName": clusterName,
            },
        }
}
```

**调度层处理信号**：

```go
// command_controller.go startTask 中
result, err := r.executeByType(execCommand.Type, execCommand.Command)
if err != nil {
    if sigErr, ok := err.(*plugin.SignalError); ok {
        r.handlePluginSignal(sigErr)
    }
    ...
}
```

#### 重构项 6：CommandReconciler 职责分离

**现状**：Reconciler 既管 CR 生命周期，又管任务调度和执行

**目标**：拆分为三层

```go
// 1. CommandReconciler — 只管 CR 生命周期
type CommandReconciler struct {
    client.Client
    Scheme    *runtime.Scheme
    Scheduler *TaskScheduler
}

func (r *CommandReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    command := &agentv1beta1.Command{}
    if err := r.Get(ctx, req.NamespacedName, command); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    if !r.shouldProcess(command) {
        return ctrl.Result{}, nil
    }
    
    return r.Scheduler.Schedule(command)
}

// 2. TaskScheduler — 管理任务调度
type TaskScheduler struct {
    Executor  *TaskExecutor
    TaskStore TaskStore  // 可替换为持久化存储
}

func (s *TaskScheduler) Schedule(command *agentv1beta1.Command) (ctrl.Result, error) {
    task := s.TaskStore.Get(command.Namespace, command.Name)
    if task == nil {
        task = s.createTask(command)
    }
    return s.Executor.Execute(task)
}

// 3. TaskExecutor — 管理任务执行
type TaskExecutor struct {
    Registry *plugin.Registry
    Job      job.Job
}
```

#### 重构项 7：Task 状态持久化

**现状**：Task Map 纯内存，进程重启丢失

**目标**：Task 状态持久化到 Command CR 的 status 中

```go
// 当前：内存中的 Task
type Task struct {
    StopChan    chan struct{}
    Phase       v1beta1.CommandPhase
    ...
}

// 重构后：Task 状态从 Command CR status 恢复
type TaskState struct {
    Phase           v1beta1.CommandPhase `json:"phase"`
    ResourceVersion string               `json:"resourceVersion"`
    Generation      int64                `json:"generation"`
    CurrentIndex    int                  `json:"currentIndex"` // 当前执行到第几条指令
}

// 从 status 恢复任务
func (s *TaskScheduler) recoverTasks() error {
    commands := &agentv1beta1.CommandList{}
    if err := s.Client.List(s.Ctx, commands); err != nil {
        return err
    }
    for _, cmd := range commands.Items {
        status := cmd.Status[s.nodeKey]
        if status != nil && status.Phase == agentv1beta1.CommandRunning {
            // 从断点恢复
            s.recoverRunningTask(&cmd, status)
        }
    }
    return nil
}
```

### 三、重构优先级与路线图

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        重构路线图 (4 个阶段)                                      │
│                                                                                  │
│  Phase 1: 安全加固 (1-2 周)                                                      │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  P0: 危险操作信号化 (重构项 5)                                              │  │
│  │  • switchcluster/shutdown 不再直接 os.Exit                                  │  │
│  │  • 引入 SignalError 机制                                                    │  │
│  │  • 影响: 3 个文件, 低风险                                                    │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  Phase 2: 依赖注入 (2-3 周)                                                      │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  P1: ClusterAccessor 替代全局 kubeconfig (重构项 2 部分)                    │  │
│  │  • 创建 ClusterAccessor 接口                                                │  │
│  │  • 逐步替换 plugin.GetBkeConfig() 等全局函数                                │  │
│  │  • 影响: ~15 个文件, 中风险                                                  │  │
│  │                                                                            │  │
│  │  P2: PluginRegistry 替代全局 pluginRegistry (重构项 1)                      │  │
│  │  • 创建 Registry 结构体                                                     │  │
│  │  • 迁移注册逻辑到显式组合函数                                                │  │
│  │  • 影响: ~5 个文件, 低风险                                                   │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  Phase 3: 插件拆分 (3-4 周)                                                      │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  P3: 消除 preprocess/postprocess 重复 (重构项 4)                            │  │
│  │  • 抽取 ScriptEngine                                                        │  │
│  │  • 影响: 2 个文件, 低风险                                                    │  │
│  │                                                                            │  │
│  │  P4: 拆分 kubeadm 上帝插件 (重构项 3)                                       │  │
│  │  • 拆分为 controlplane/worker/etcd 独立插件                                 │  │
│  │  • 保持向后兼容: 旧 Command 仍支持 "kubeadm" 名称                           │  │
│  │  • 影响: ~10 个文件, 高风险 (需要充分回归测试)                               │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  Phase 4: 架构升级 (4-6 周)                                                      │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  P5: CommandReconciler 职责分离 (重构项 6)                                  │  │
│  │  • 拆分 Reconciler / Scheduler / Executor                                  │  │
│  │  • 影响: ~8 个文件, 高风险                                                   │  │
│  │                                                                            │  │
│  │  P6: Task 状态持久化 (重构项 7)                                             │  │
│  │  • 从 Command CR status 恢复任务                                            │  │
│  │  • 影响: ~5 个文件, 中风险                                                   │  │
│  │                                                                            │  │
│  │  P7: 增强插件接口 (重构项 2 完整版)                                         │  │
│  │  • 引入 PluginContext / PluginCapabilities                                  │  │
│  │  • 所有插件迁移到新接口                                                      │  │
│  │  • 影响: ~20 个文件, 高风险                                                  │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────────┘
```

### 四、重构前后对比

| 维度 | 重构前 | 重构后 |
|------|--------|--------|
| **插件注册** | 全局变量硬编码 18 个 | Registry 依赖注入，按域组合 |
| **集群访问** | `plugin.GetBkeConfig()` 全局函数，硬编码 kubeconfig | `ClusterAccessor` 接口注入，可 mock |
| **kubeadm 插件** | 600 行，6 种 phase | 拆分为 5 个独立插件，每个 < 200 行 |
| **preprocess/postprocess** | 90% 代码重复 | 共享 `ScriptEngine`，仅配置不同 |
| **危险操作** | 直接 `os.Exit()` | `SignalError` 信号机制，调度层决策 |
| **Reconciler 职责** | CR 管理 + 任务调度 + 执行 | 三层分离：Reconciler / Scheduler / Executor |
| **Task 状态** | 纯内存，重启丢失 | 持久化到 Command CR status，可恢复 |
| **插件接口** | `Execute(commands)` | `Execute(ctx, commands)` + 能力声明 |
| **可测试性** | 全局状态，需 gomonkey | 依赖注入，接口 mock |
| **可扩展性** | 修改 builtin.go 注册 | 实现 Plugin 接口 + Register() |

### 五、向后兼容策略
重构过程中需保持现有 Command CR 的兼容性：
1. **kubeadm 拆分**：保留 `kubeadm` 作为兼容适配器，内部转发到新插件
2. **插件名称映射**：Registry 支持别名，`"kubeadm"` → `"controlplane-init"`
3. **接口迁移**：新接口 `Execute(ctx, commands)` 与旧接口 `Execute(commands)` 共存，旧插件通过适配器包装
4. **渐进式迁移**：每个 Phase 独立完成，不跨 Phase 依赖
