# KEP-8: K8s 1.34 → 1.36 升级方案设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-8 |
| **标题** | Kubernetes v1.34 → v1.36 声明式升级方案设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-25 |
| **依赖** | KEP-5 声明式升级框架、KEP-6 三层状态机设计、KEP-5-2 升级前预检 |

---

## 1. 摘要

本提案设计 Kubernetes v1.34 → v1.36 的声明式升级方案，基于 openFuyao 已有的 DAG 驱动升级框架（KEP-5），通过 ReleaseImage 定义版本清单、ComponentVersion 声明组件依赖、UpgradePath 管控升级路径，实现逐版本（v1.34→v1.35→v1.36）滚动升级。升级过程通过 BKEClusterReconciler 构建 DAG、Scheduler 按拓扑批次执行、BKEAgent 在节点上执行 kubeadm 升级命令，覆盖 containerd、etcd、控制面、kubelet、kube-proxy、coredns 等全部配套组件。

## 2. 动机

### 2.1 版本迭代需求

K8s 1.34→1.36 跨越 2 个小版本，涉及：

| 维度 | 变更 | 影响 |
|------|------|------|
| **API 废弃** | `flowcontrol.apiserver.k8s.io/v1beta3`（1.35）、`discovery.k8s.io/v1beta1`（1.36） | 需扫描集群中使用的废弃 API |
| **etcd 版本** | v3.5.18 → v3.5.20 | 需同步升级 etcd |
| **containerd 版本** | v1.7.20 → v1.7.24 | 需同步升级容器运行时 |
| **kubelet 倾斜策略** | kubelet 最多比 apiserver 旧 2 个版本 | 必须先升级 master 再升级 worker |

### 2.2 现有框架能力

openFuyao 声明式升级框架已具备以下能力：

- **ReleaseImage CRD**：定义版本清单（`spec.install.components` + `spec.upgrade.components`）
- **ComponentVersion CRD**：声明组件类型（inline/yaml/helm）、依赖关系、升级策略
- **UpgradePath CRD**：管控版本间升级路径合法性
- **DAG 调度器**：从 ReleaseImage bundle 动态构建 DAG，按拓扑批次并行执行
- **VersionContext**：通过 `current != target` 字符串比较判断是否需要升级
- **BKEAgent 命令机制**：通过 `Command` CR 向节点发送 `Kubeadm Upgrade*` 内置命令

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **K8s 版本** | v1.34 → v1.35 → v1.36（逐版本升级，2 跳） |
| **配套组件** | containerd、etcd、kubelet、apiserver、controller-manager、scheduler、kube-proxy、coredns、bkeagent |
| **升级路径** | 逐版本升级（v1.34→v1.35→v1.36），不支持跳版本 |
| **回滚策略** | 仅支持集群级回滚 |
| **PreCheck/PostCheck** | 引用 KEP-5-2 的 CheckPolicy CRD 设计 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **逐版本升级** | kubeadm 官方建议逐版本升级，不支持跨多个版本 |
| **版本一致性** | 运行态必须与 ReleaseImage 保持一致，不支持部分回滚 |
| **先 master 后 worker** | kubelet 不能比 apiserver 新，必须先升级控制面 |
| **BKEAgent 就绪** | 节点 BKEAgent 必须 Ready 才能执行升级命令 |
| **幂等性** | 所有升级操作必须幂等，支持 Reconcile 重入 |

## 4. 版本兼容性矩阵

### 4.1 K8s 与配套组件版本对应关系

| 目标版本 | K8s | containerd | etcd | kubelet | 说明 |
|---------|-----|-----------|------|---------|------|
| **v1.34** | v1.34.x | v1.7.20 | v3.5.18 | v1.34.x | 当前版本 |
| **v1.35** | v1.35.x | v1.7.22 | v3.5.19 | v1.35.x | 第一跳 |
| **v1.36** | v1.36.x | v1.7.24 | v3.5.20 | v1.36.x | 目标版本（最新稳定版） |

> **注意**：截至 2026 年 8 月，K8s 最新稳定版本为 v1.36。v1.37 尚未发布。

### 4.2 版本倾斜策略约束

| 组件 | 约束规则 | 影响 |
|------|---------|------|
| **kubelet** | kubelet 不能比 apiserver 新，最多旧 2 个版本 | 必须先升级 master，再升级 worker |
| **kube-proxy** | 版本必须与 apiserver 一致 | 与 master 同步升级 |
| **kubectl** | 版本与 apiserver 相差不超过 1 个版本 | 与 master 同步升级 |
| **etcd** | 每个 K8s 版本有推荐的 etcd 版本 | 需要与 K8s 版本配套升级 |
| **containerd** | 需要支持目标 K8s 版本的 CRI | 需要与 K8s 版本配套升级 |

### 4.3 API 废弃清单（需 PreCheck 扫描）

| 版本 | 废弃 API | 替代 API | 影响范围 |
|------|---------|---------|---------|
| **v1.35** | `flowcontrol.apiserver.k8s.io/v1beta3` | `v1` | FlowSchema, PriorityLevelConfiguration |
| **v1.36** | `discovery.k8s.io/v1beta1` | `v1` | EndpointSlice |

## 5. 升级策略设计

### 5.1 升级路径

**推荐方案：逐版本升级**

```
v1.34 → v1.35 → v1.36
```

**理由**：
1. kubeadm 官方建议逐版本升级
2. 每个中间版本的 API 变更需要逐步适配
3. etcd 数据格式需要逐步迁移
4. 降低升级风险，每跳都可以验证和回滚

### 5.2 ReleaseImage 结构设计

每个版本对应一个 ReleaseImage CR，包含 `spec.install.components` 和 `spec.upgrade.components`：

```yaml
# ReleaseImage v1.35.0
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v1.35.0
spec:
  version: "v1.35.0"
  digest: "sha256:..."
  install:
    components:
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.22
      - name: etcd
        version: v3.5.19
      - name: kubernetes-master
        version: v1.35.0
      - name: kubernetes-worker
        version: v1.35.0
      - name: kube-proxy
        version: v1.35.0
      - name: coredns
        version: v1.11.2
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: v1.0.0
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.22
      - name: etcd
        version: v3.5.19
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0.0
      - name: kubernetes-master
        version: v1.35.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.35.0
        inline:
          handler: EnsureWorkerUpgrade
          version: v1.0.0
      - name: kube-proxy
        version: v1.35.0
      - name: coredns
        version: v1.11.2
```

### 5.3 DAG 构建

DAG 从 ReleaseImage bundle 动态构建，依赖关系来自 `ComponentVersion.spec.dependencies`：

```
DAG 构建流程 (pkg/topology/build.go + pkg/upgrade/bundle.go):

1. 从 ReleaseImage.Spec.Upgrade.Components 提取升级组件列表
2. 对每个组件，从 ComponentVersion.spec.dependencies 解析依赖关系
3. 隐式规则：pre-upgrade-resources 作为所有组件的前置依赖
4. 拓扑排序生成执行批次（Kahn 算法）
5. 同批次内组件可并行执行（MaxParallel=8）
```

**典型 DAG 结构（v1.34 → v1.35 单跳）**：

```
Batch 1: [pre-upgrade-resources]     ← 隐式前置依赖，最先执行
    └─ 创建升级所需的 CRD/RBAC/ConfigMap 资源

Batch 2: [bkeagent, containerd]      ← 无相互依赖，并行执行
    ├─ bkeagent: SSH 推送二进制
    └─ containerd: ENV 命令（reset + redeploy）

Batch 3: [etcd]                      ← 依赖 pre-upgrade-resources
    └─ etcd: Upgrade CR → Kubeadm UpgradeEtcd

Batch 4: [kubernetes-master]         ← 依赖 etcd
    └─ master: Upgrade CR → Kubeadm UpgradeControlPlane

Batch 5: [kubernetes-worker]         ← 依赖 kubernetes-master（倾斜策略）
    └─ worker: Upgrade CR → Kubeadm UpgradeWorker

Batch 6: [kube-proxy, coredns]       ← 依赖 kubernetes-master
    ├─ kube-proxy: YAML Apply（SSA + Prune）
    └─ coredns: YAML Apply（SSA + Prune）
```

> **注意**：实际 DAG 结构由 ReleaseImage bundle 中的 `ComponentVersion.spec.dependencies` 决定，而非硬编码。上图为典型配置。

### 5.4 多跳升级编排

多跳升级通过 `ClusterVersionReconciler` 逐跳触发，而非自动连续执行：

```
用户操作：修改 ClusterVersion.Spec.DesiredVersion = v1.36.0

ClusterVersionReconciler 编排：
┌─────────────────────────────────────────────────────────────────────┐
│  Hop 1: v1.34 → v1.35                                              │
│  1. ClusterVersionReconciler 设置 annotation:                      │
│     cvo.openfuyao.cn/upgrade-ready = v1.35.0                       │
│  2. BKEClusterReconciler 检测到 annotation:                       │
│     └─ executeUpgradeDAG()                                          │
│        ├─ 解析 ReleaseImage v1.35.0（Status.Phase 必须为 Valid）  │
│        ├─ 构建 VersionContext（Target from bundle, Current from 状态）│
│        ├─ 同步目标版本到 BKECluster.Spec                           │
│        ├─ 构建 DAG + 执行 DAG                                      │
│        └─ 成功后清除 annotation + CompleteUpgradeHop               │
│  3. ClusterVersionReconciler 检测到 hop 完成:                      │
│     └─ 更新 Status.CurrentVersion = v1.35.0                       │
└─────────────────────────────────────────────────────────────────────┘
                                    ↓
┌─────────────────────────────────────────────────────────────────────┐
│  Hop 2: v1.35 → v1.36                                              │
│  1. ClusterVersionReconciler 设置 annotation:                      │
│     cvo.openfuyao.cn/upgrade-ready = v1.36.0                       │
│  2. BKEClusterReconciler 检测到 annotation:                       │
│     └─ executeUpgradeDAG()（同上流程）                             │
│  3. ClusterVersionReconciler 更新:                                 │
│     └─ Status.CurrentVersion = v1.36.0                             │
└─────────────────────────────────────────────────────────────────────┘
```

**关键设计**：
- 每跳由 `ClusterVersionReconciler` 通过 `cvo.openfuyao.cn/upgrade-ready` annotation 触发
- BKEClusterReconciler 执行完一跳后清除 annotation，控制权交回 ClusterVersionReconciler
- 用户可在任意跳之间暂停（设置 `BKECluster.Spec.Pause = true`）
- 如果某一跳失败，`StatusManager` 提供 10 次重试，超过后设置终端 `*Failed` 状态

## 6. 组件升级方案

### 6.1 bkeagent 升级

**执行方式**：SSH 直接推送二进制（非 Command CR）

**原因**：BKEAgent 无法自升级（chicken-and-egg 问题），必须由 Controller 直接推送

**执行流程**（`EnsureAgentUpgrade` → `upgradeBKEAgentViaSSH`）：

```
1. 获取所有节点（BKENodes）
2. 解析目标版本（VersionContext.GetTarget("bkeagent")）
3. SSH 发现每个节点的 CPU 架构（amd64/arm64）
4. 从 HTTP 制品仓库下载对应架构的 bkeagent 二进制
5. SSH 推送二进制到每个节点
6. Ping 验证升级后的 Agent 响应
7. Master 节点失败：立即返回错误（阻塞）
   Worker 节点失败：记录失败数量，不阻塞
```

### 6.2 containerd 升级

**执行方式**：ENV 命令（`NewConatinerdReset` + `NewConatinerdRedeploy`）

**与 master/worker/etcd 的区别**：不使用 `Upgrade` CR 发送 `Kubeadm` 命令，而是使用 `ENV` 命令批量操作多节点

**执行流程**（`EnsureContainerdUpgrade` → `rolloutContainerd`）：

```
1. 同步目标版本到 BKECluster.Spec（SyncUpgradeTargetsToClusterSpec）
2. 解析目标版本（VersionContext.GetTarget("containerd")）
3. 筛选需要升级的节点（GetNeedUpgradeContainerdNodesWithBKENodes）
4. 第一阶段: NewConatinerdReset（ENV 命令）
   └─ 批量重置 containerd（停止 + 清理 + 配置更新）
   └─ 包含 sandbox_image 更新、镜像仓库证书等
5. 第二阶段: NewConatinerdRedeploy（ENV 命令）
   └─ 批量重新部署 containerd（安装新版本 + 启动）
6. 等待命令完成（2s 轮询，5min 超时）
7. 更新 BKECluster.Status.ContainerdVersion = targetVersion
```

### 6.3 etcd 升级

**执行方式**：`Upgrade` CR → BKEAgent 执行 `Kubeadm UpgradeEtcd` 内置命令

**执行流程**（`EnsureEtcdUpgrade` → `rolloutUpgrade`）：

```
1. 筛选 etcd 节点（跳过 BKEAgent 未就绪的节点）
2. 确定备份节点（etcdNodes[0]）
3. 逐节点滚动升级（阻塞式，失败则停止）:
   a. 标记节点状态为 EtcdUpgrading
   b. 同步目标版本到 BKECluster.Spec
   c. 创建 Upgrade Command CR:
      ├─ Phase: UpgradeEtcd
      ├─ EtcdVersion: v3.5.19（从 VersionContext 获取）
      ├─ BackUpEtcd: true（仅备份节点）
      └─ NodeSelector: 单节点 IP
   d. BKEAgent 接收命令，执行 Kubeadm UpgradeEtcd
   e. 等待命令完成（2s 轮询，5min 超时）
   f. 等待 etcd 健康检查:
      ├─ 轮询 etcd Static Pod 状态（Running + Ready）
      ├─ 从容器镜像 tag 提取版本号
      └─ 与目标版本比较（etcdVersionsMatch，去除 v 前缀后比较）
   g. 失败: 标记 EtcdUpgradeFailed，阻塞
   h. 成功: 标记 EtcdUpgrading（"Upgrading success"）
4. 更新 BKECluster.Status.EtcdVersion = targetVersion
```

### 6.4 控制面组件升级

**执行方式**：`Upgrade` CR → BKEAgent 执行 `Kubeadm UpgradeControlPlane` 内置命令

**升级范围**：apiserver、controller-manager、scheduler（作为控制面单元统一升级）

**执行流程**（`EnsureMasterUpgrade` → `rolloutUpgrade`）：

```
1. 设置 DeployAction annotation（DeployActionK8sUpgrade）
2. 同步目标版本到 BKECluster.Spec（syncLegacyTargetKubernetesVersion）
3. 筛选需要升级的 Master 节点:
   ├─ 获取 BKENodes，过滤 Master 角色
   ├─ 跳过 BKEAgent 未就绪的节点
   └─ 跳过 kubelet 版本已匹配目标版本的节点（读取 Node.Status.NodeInfo.KubeletVersion）
4. 确定 etcd 备份节点（第一个 etcd 节点）
5. 为 etcd 节点设置 EtcdAdvertiseClientUrlsAnnotation
6. 逐节点滚动升级（阻塞式，失败则停止）:
   a. 获取远端 Node 资源
   b. 跳过 kubelet 版本已匹配的节点
   c. 标记节点状态为 NodeUpgrading
   d. 创建 Upgrade Command CR:
      ├─ Phase: UpgradeControlPlane
      ├─ BackUpEtcd: true（仅备份节点）
      └─ NodeSelector: 单节点 IP
   e. BKEAgent 执行 Kubeadm UpgradeControlPlane:
      ├─ 升级 kubelet 二进制
      ├─ 更新 Static Pod manifest（apiserver/controller-manager/scheduler 镜像版本）
      └─ Kubelet 自动重建 Static Pod
   f. 等待命令完成（2s 轮询，5min 超时）
   g. 等待节点健康检查:
      ├─ 轮询 Node 状态（2s 间隔，5min 超时）
      └─ 验证 Node Ready + KubeletVersion 匹配目标版本
   h. 失败: 标记 NodeUpgradeFailed，阻塞
   i. 成功: 标记 NodeNotReady（"Upgrading success"）
7. 更新 BKECluster.Status.KubernetesVersion = targetVersion
8. 更新 addon 版本（kubectl 版本对齐）
```

### 6.5 kubelet（Worker）升级

**执行方式**：`Upgrade` CR → BKEAgent 执行 `Kubeadm UpgradeWorker` 内置命令

**与 Master 升级的关键区别**：非阻塞（失败继续下一个节点），需要 drain 节点

**执行流程**（`EnsureWorkerUpgrade` → `rolloutUpgrade`）：

```
1. 设置 DeployAction annotation
2. 同步目标版本到 BKECluster.Spec
3. 筛选需要升级的 Worker 节点:
   ├─ 获取 BKENodes，过滤 Worker 角色
   ├─ 跳过 BKEAgent 未就绪的节点
   └─ 跳过 kubelet 版本已匹配的节点
4. 创建 drainer（force=true, ignore-daemonsets, delete-emptydir, 20s 超时）
5. 逐节点滚动升级（非阻塞，失败继续）:
   a. 获取远端 Node 资源
   b. 跳过 kubelet 版本已匹配的节点
   c. 标记节点状态为 NodeUpgrading
   d. drain 节点（驱逐 Pod）
   e. 创建 Upgrade Command CR:
      ├─ Phase: UpgradeWorker
      ├─ BackUpEtcd: false
      └─ NodeSelector: 单节点 IP
   f. BKEAgent 执行 Kubeadm UpgradeWorker:
      └─ 升级 kubelet 二进制 + 配置
   g. 等待命令完成（2s 轮询，5min 超时）
   h. 等待节点健康检查（2s 间隔，5min 超时）
   i. 失败: 标记 NodeUpgradeFailed，记录失败节点，继续下一个
   j. 成功: 标记 NodeNotReady
   k. uncordon 节点
6. 如果有失败节点，返回错误（Reconcile 重试时跳过已升级节点）
```

### 6.6 kube-proxy / coredns 升级

**执行方式**：YAML 组件类型，通过 YamlInstaller SSA Apply 应用完整资源清单

**执行流程**（DAG Scheduler → `executeManifest`）：

```
kube-proxy (type: yaml):
1. 从 ManifestStore 加载 ComponentVersion（bke-manifests/kube-proxy/v1.36.0/）
2. 检查是否需要升级（manifestNeedsUpgrade: 比较当前 vs 目标镜像 tag）
3. YamlInstaller.Apply:
   a. 加载完整资源清单（DaemonSet + ConfigMap + RBAC）
   b. 按文件名排序应用（01-crd → 02-rbac → 03-configmap → 04-daemonset）
   c. 使用 SSA Apply 更新资源（ServerSideApply 策略）
   d. Prune 不再需要的旧版本资源（按 label selector）
4. 健康检查（如果 ComponentVersion.spec.yaml.healthCheck.enabled）:
   a. PodReady: 验证 kube-proxy Pod Ready
   b. EndpointReady: 验证 Service Endpoint 就绪

coredns (type: yaml):
1. 从 ManifestStore 加载 ComponentVersion（bke-manifests/coredns/v1.11.x/）
2. 同上流程，应用 Deployment + ConfigMap + Service + RBAC
3. 健康检查: 验证 coredns Pod Ready + DNS 解析正常
```

### 6.7 升级组件清单汇总

| 组件 | 执行模式 | 执行方式 | 滚动策略 | 失败处理 |
|------|---------|---------|---------|---------|
| **pre-upgrade-resources** | inline | PhaseHandler | 一次性 | 阻塞（FailFast） |
| **bkeagent** | inline | SSH 推送二进制 | 全节点 | Master 阻塞，Worker 继续 |
| **containerd** | inline | ENV 命令（reset + redeploy） | 批量节点 | 阻塞 |
| **etcd** | inline | Upgrade CR → Kubeadm UpgradeEtcd | 逐节点阻塞 | 阻塞 |
| **kubernetes-master** | inline | Upgrade CR → Kubeadm UpgradeControlPlane | 逐节点阻塞 | 阻塞 |
| **kubernetes-worker** | inline | Upgrade CR → Kubeadm UpgradeWorker | 逐节点非阻塞 | 继续（记录失败） |
| **kube-proxy** | manifest | YamlInstaller SSA Apply | DaemonSet 滚动 | 阻塞 |
| **coredns** | manifest | YamlInstaller SSA Apply | Deployment 滚动 | 阻塞 |

## 7. 升级执行流程

### 7.1 触发机制

```
1. 用户修改 ClusterVersion.Spec.DesiredVersion = v1.36.0
2. ClusterVersionReconciler 检测到版本变更
3. 校验 UpgradePath（v1.34 → v1.35 路径合法、未被阻断）
4. 设置 annotation: cvo.openfuyao.cn/upgrade-ready = v1.35.0（第一跳）
5. BKEClusterReconciler 检测到 annotation
```

### 7.2 DAG 执行流程（单跳）

```
BKEClusterReconciler.executeUpgradeDAG():

1. 解析 hop 目标版本（从 annotation 读取）
2. 解析 ReleaseImage CR:
   ├─ 通过 clusterversion.ResolveReleaseImageForVersion 获取
   └─ 验证 Status.Phase == Valid（否则 requeue 30s）
3. 解析 OCI bundle（releaseStore.ResolveRelease）
4. 解析当前版本 bundle（用于构建 VersionContext.Current）
5. 构建 VersionContext:
   ├─ Target: 从目标 bundle 的 ReleaseImage.Spec.Upgrade.Components
   └─ Current: 从当前 bundle 或 BKECluster.Status
6. 同步目标版本到 BKECluster.Spec（SyncUpgradeTargetsToClusterSpec）
7. 构建 DAG（BuildDAGFromBundle + BundleDependencyResolver）
8. 初始化 DeclarativeUpgradeStatus（重置完成列表）
9. 构建 ComponentFactory（注册 inline handler）
10. 构建 Scheduler:
    ├─ InlineRunner: InlinePhaseRunnerAdapter（桥接 PhaseRunner）
    ├─ ManifestStore: BundleStore
    ├─ ManifestApplier: ClusterApplier
    └─ MaxParallelPerBatch: 8
11. Scheduler.ExecuteDAG():
    for each batch in dag.TopologicalBatches():
      executeBatchParallel (errgroup + semaphore):
        for each component in batch:
          ├─ shouldSkipComponent: 跳过已完成的（DeclarativeUpgradeStatus.IsCompleted）
          ├─ componentNeedsUpgrade: VersionContext.NeedsUpgrade（current != target）
          ├─ 分发到执行器:
          │   ├─ inline: InlineComponentExecutor → PhaseRunner → Phase.Execute()
          │   └─ manifest: YamlComponentExecutor → YamlInstaller.Apply()
          └─ 更新组件状态（markCompleted / markFailed）
      persistBatchResults: 更新 DeclarativeUpgradeStatus
      如果 failFast: 停止执行
12. 成功: completeDeclarativeUpgrade
    ├─ 清除 upgrade-ready annotation
    └─ clusterversion.CompleteUpgradeHop
13. 失败: handleDeclarativeUpgradeDAGFailure
    ├─ 设置 ClusterUpgradeFailed
    ├─ 记录 LastError
    └─ StatusManager 判断重试预算（10 次后 abort）
```

### 7.3 版本比较机制

```go
// VersionContext 使用简单字符串比较，非语义化版本比较
func (vc *VersionContext) NeedsUpgrade(name string) bool {
    target := vc.Target[name]
    if target == "" { return false }  // 空目标 = 不需要升级
    return vc.Current[name] != target // 字符串不等 = 需要升级
}

// 决策逻辑
func Decide(vc *VersionContext, name string) Decision {
    if vc == nil { return DecisionUpgrade }      // nil VC = 不阻塞
    if vc.Current[name] == vc.Target[name] { return DecisionSkip }
    if vc.Target[name] == "" { return DecisionSkip }
    return DecisionUpgrade
}
```

### 7.4 PreCheck / PostCheck

引用 KEP-5-2 的 CheckPolicy CRD 设计：

| 检查阶段 | 检查项 | 阻断条件 |
|---------|--------|---------|
| **PreCheck** | 集群健康、etcd 健康、API 兼容性、资源充足、备份验证、升级路径合法 | 有阻断项则停止升级 |
| **PostCheck** | 版本验证、集群健康、etcd 健康、API 可用、DNS 正常、应用健康 | 有失败项则触发告警 |

详见 `kep5-2-precheck-postcheck-design-v2.md`。

## 8. 回滚策略

### 8.1 回滚范围

**仅支持集群级回滚**：
- 不支持组件级回滚
- 不支持节点级回滚
- 回滚时整个集群回退到该跳的起始版本

### 8.2 回滚触发

| 场景 | 触发方式 | 说明 |
|------|---------|------|
| **升级过程中失败** | 自动（FailurePolicy） | StatusManager 10 次重试后设置终端 Failed 状态 |
| **升级完成后发现问题** | 手动（修改 DesiredVersion 回退） | 回滚到指定版本 |

### 8.3 回滚执行

回滚通过修改 `ClusterVersion.Spec.DesiredVersion` 回退触发，复用升级 DAG（VersionContext 的 `target < current` 触发回滚语义）：

```
单跳回滚（v1.35 → v1.34）:
1. 用户修改 DesiredVersion = v1.34.0
2. ClusterVersionReconciler 设置 upgrade-ready = v1.34.0
3. BKEClusterReconciler 执行 executeUpgradeDAG:
   ├─ 加载 ReleaseImage v1.34.0
   ├─ VersionContext: Current=v1.35, Target=v1.34
   ├─ DAG 执行（各组件 current != target，触发"升级"）
   └─ 实际是降级操作（各执行器通过版本比较判断方向）
4. etcd 数据从升级前备份恢复
5. 更新 CurrentVersion = v1.34.0

多跳回滚（v1.36 → v1.34）:
  Hop 2 回滚: v1.36 → v1.35
  Hop 1 回滚: v1.35 → v1.34
```

### 8.4 etcd 数据回滚

- **升级前备份**：每个 etcd 节点升级前通过 `etcdctl snapshot save` 备份
- **回滚时恢复**：如果 etcd 数据格式不兼容，从 snapshot 恢复
- **数据丢失风险**：升级后产生的数据在回滚时会丢失

## 9. 可观测性

### 9.1 状态追踪

```bash
# 查询集群升级状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.declarativeUpgrade}'
# 输出:
# {
#   "targetVersion": "v1.35.0",
#   "completed": ["pre-upgrade-resources", "bkeagent", "containerd"],
#   "lastError": ""
# }

# 查询组件状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.clusterComponentStatuses}'
# 输出:
# {
#   "etcd": {"phase": "Pending", "version": "v3.5.18"},
#   "containerd": {"phase": "Installed", "version": "v1.7.22"}
# }
```

### 9.2 事件与指标

| 类型 | 来源 | 说明 |
|------|------|------|
| **StateTransition events** | BKECluster Controller | 状态转换事件 |
| **OperationStarted/Completed/Failed** | DAG Scheduler | 操作事件 |
| **ComponentInstalled/Upgraded/Failed** | ComponentStatusUpdater | 组件事件 |
| **bke_cluster_phase** | Prometheus | 集群状态指标 |
| **bke_component_phase** | Prometheus | 组件状态指标 |

## 10. 工作量评估

### 10.1 开发工作量

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **ReleaseImage 定义** | 定义 v1.35/v1.36 两个版本的 ReleaseImage + ComponentVersion | 3 |
| **UpgradePath 配置** | 配置 v1.34→v1.35→v1.36 升级路径 + 兼容性规则 | 1 |
| **bkeagent 升级适配** | 适配 v1.35/v1.36 版本的 bkeagent SSH 推送 | 2 |
| **containerd 升级适配** | 适配 v1.7.22/v1.7.24 的 ENV 命令（reset + redeploy） | 3 |
| **etcd 升级适配** | 适配 v3.5.19/v3.5.20 的 Upgrade CR + 健康检查 | 3 |
| **控制面升级适配** | 适配 v1.35/v1.36 的 Upgrade CR + kubeadm 验证 | 4 |
| **kubelet 升级适配** | 适配 v1.35/v1.36 的 Upgrade CR + drain/uncordon | 3 |
| **kube-proxy/coredns 清单** | 编写各版本的完整 YAML 清单 + ConfigMap 适配 | 4 |
| **PreCheck 适配** | 适配 KEP-5-2 CheckPolicy，增加 API 废弃扫描 | 5 |
| **PostCheck 适配** | 适配版本验证 + 健康 + DNS + 应用检查 | 3 |
| **多跳升级编排** | 验证 ClusterVersionReconciler 逐跳触发逻辑 | 3 |
| **回滚验证** | 验证 VersionContext 回滚语义 + etcd 数据恢复 | 4 |
| **小计** | - | **38 人天** |

### 10.2 测试工作量

| 测试类型 | 测试内容 | 工作量（人天） |
|---------|---------|---------------|
| **单元测试** | 各组件版本比较、DAG 构建、依赖解析 | 5 |
| **集成测试** | 单跳升级（v1.34→v1.35, v1.35→v1.36） | 8 |
| **E2E 测试** | 完整升级流程（v1.34→v1.36，2 跳连续） | 8 |
| **回滚测试** | 各跳回滚场景 | 5 |
| **兼容性测试** | API 兼容性、业务应用兼容性 | 5 |
| **性能测试** | 升级过程性能影响 | 3 |
| **压力测试** | 大规模集群升级 | 3 |
| **小计** | - | **37 人天** |

### 10.3 文档工作量

| 文档类型 | 文档内容 | 工作量（人天） |
|---------|---------|---------------|
| **升级指南** | 用户升级操作指南 | 2 |
| **兼容性矩阵** | 版本兼容性矩阵文档 | 1 |
| **故障排查** | 升级故障排查指南 | 2 |
| **API 变更** | K8s 1.34→1.36 API 变更清单 | 2 |
| **Release Notes** | 各版本 Release Notes | 1 |
| **小计** | - | **8 人天** |

### 10.4 总工作量汇总

| 类别 | 工作量（人天） | 工作量（人周） |
|------|---------------|---------------|
| **开发** | 38 | 7.6 |
| **测试** | 37 | 7.4 |
| **文档** | 8 | 1.6 |
| **总计** | **83** | **16.6** |

**按人员配置估算**：
- 如果 2 人全职投入：约 8.5 周（2 个月）
- 如果 3 人全职投入：约 5.5 周（1.5 个月）
- 如果 4 人全职投入：约 4 周（1 个月）

> **注意**：工作量基于现有声明式升级框架已实现 DAG 调度、Command CR、ENV 命令、SSH 推送等核心能力，本次工作主要是版本适配和测试验证。若框架能力需新建，工作量需相应增加。

## 11. 风险与缓解措施

### 11.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **kubeadm 不支持逐版本升级** | 必须跳版本升级 | 低 | 提前验证 kubeadm 行为 |
| **etcd 数据格式不兼容** | etcd 升级失败 | 中 | 升级前 snapshot 备份，验证版本兼容性 |
| **API 废弃导致资源失效** | 升级后部分资源无法创建 | 高 | PreCheck 扫描废弃 API，提供迁移工具 |
| **containerd CRI 不兼容** | kubelet 无法启动 | 中 | 验证 CRI 接口兼容性 |
| **kubelet 倾斜策略违反** | kubelet 无法注册 | 中 | 确保先升级 master 再升级 worker |
| **BKEAgent 不可用** | 无法执行升级命令 | 中 | 升级前检查所有节点 BKEAgent Ready |

### 11.2 运维风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **升级时间过长** | 业务中断时间长 | 高 | 优化升级脚本，Worker 支持并行 |
| **回滚失败** | 无法恢复到稳定状态 | 中 | 充分测试回滚流程，准备手动恢复方案 |
| **数据丢失** | 升级后数据在回滚时丢失 | 高 | 升级前通知用户，避免在升级窗口期写入重要数据 |

### 11.3 业务风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **业务 Pod 重启** | 业务短暂中断 | 高 | Worker drain 时选择业务低峰期 |
| **DNS 短暂不可用** | 业务解析失败 | 中 | coredns 滚动更新，保持至少一个副本可用 |
| **网络中断** | kube-proxy 升级导致 | 中 | kube-proxy 滚动更新，保持至少一个副本可用 |

## 12. 与工行协作计划

### 12.1 协作事项

| 事项 | 责任方 | 时间 | 交付物 |
|------|--------|------|--------|
| **提供 K8s 版本变更清单** | openFuyao 社区 | 8 月底 | K8s 1.35/1.36 API 变更清单 |
| **提供兼容性矩阵** | openFuyao 社区 | 8 月底 | K8s/containerd/etcd 版本兼容性矩阵 |
| **验证工行环境 API 兼容性** | 工行 | 9 月初 | API 兼容性扫描报告 |
| **提供升级方案评审** | 双方 | 9 月中旬 | 升级方案评审纪要 |
| **联合测试升级流程** | 双方 | 9 月下旬 | 测试报告 |
| **确认回滚策略和 SLA** | 双方 | 9 月底 | 回滚策略文档 |
| **生产环境升级** | 双方 | 10 月 | 升级完成报告 |

### 12.2 里程碑计划

```
8 月底 ──── 9 月中旬 ──── 9 月底 ──── 10 月中旬 ──── 10 月底
   │            │            │              │              │
   ├─ 提供变更清单          │              │              │
   ├─ 提供兼容性矩阵        │              │              │
   │            │            │              │              │
   │         ├─ API 兼容性验证             │              │
   │         ├─ 升级方案评审               │              │
   │            │            │              │              │
   │            │         ├─ 联合测试       │              │
   │            │         ├─ 回滚策略确认   │              │
   │            │            │              │              │
   │            │            │           ├─ 测试环境升级   │
   │            │            │              │              │
   │            │            │              │           ├─ 生产环境升级
```

### 12.3 风险共担

| 风险 | openFuyao 社区责任 | 工行责任 |
|------|-------------------|---------|
| **升级失败** | 提供回滚方案和技术支持 | 配合执行回滚操作 |
| **数据丢失** | 提供备份恢复工具 | 升级前确认数据备份 |
| **业务中断** | 优化升级流程，减少中断时间 | 选择业务低峰期升级 |
| **兼容性问题** | 提供兼容性扫描工具 | 提前验证业务应用兼容性 |

---

## 附录

### A. 参考文档

1. [Kubernetes 升级指南](https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/kubeadm-upgrade/)
2. [etcd 升级指南](https://etcd.io/docs/v3.5/upgrades/)
3. [containerd 发布说明](https://github.com/containerd/containerd/releases)
4. [KEP-5 声明式升级框架](../kep/kep5/kep5.md)
5. [KEP-6 三层状态机设计](../kep/kep6/kep6-state-machine-v5.md)
6. [KEP-5-2 升级前预检设计](../kep/kep5/kep5-2-precheck-postcheck-design-v2.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **Hop** | 一次版本跳转，如 v1.34 → v1.35 |
| **ReleaseImage** | 版本清单 CRD，定义 install/upgrade 组件列表 |
| **ComponentVersion** | 组件版本定义 CRD，声明类型/依赖/策略 |
| **UpgradePath** | 升级路径 CRD，管控版本间升级合法性 |
| **VersionContext** | 版本上下文，通过 current/target 字符串比较判断是否需要升级 |
| **DAG** | 有向无环图，从 ReleaseImage bundle 动态构建，按拓扑批次执行 |
| **Command CR** | BKEAgent 命令 CRD，向节点发送 Kubeadm 升级命令 |
| **ENV 命令** | 环境命令，批量操作多节点（containerd reset/redeploy） |
| **倾斜策略** | kubelet 版本不能比 apiserver 新，最多旧 2 个版本 |
| **DeclarativeUpgradeStatus** | DAG 升级状态追踪，记录已完成组件列表 |
| **upgrade-ready annotation** | `cvo.openfuyao.cn/upgrade-ready`，触发单跳升级 |

### C. 代码库参考

| 代码路径 | 说明 |
|---------|------|
| `controllers/capbke/bkecluster_upgrade_dag.go` | DAG 执行入口 `executeUpgradeDAG` |
| `pkg/dagexec/scheduler.go` | DAG 调度器 `Scheduler.ExecuteDAG` |
| `pkg/topology/build.go` | DAG 构建 `BuildUpgradeDAG` |
| `pkg/upgrade/bundle.go` | Bundle DAG 构建 + 依赖解析 |
| `pkg/upgrade/catalog.go` | 升级组件目录（inline/manifest 映射） |
| `pkg/upgrade/context.go` | VersionContext（版本比较） |
| `pkg/phaseframe/phases/ensure_master_upgrade.go` | 控制面升级 |
| `pkg/phaseframe/phases/ensure_worker_upgrade.go` | Worker 升级 |
| `pkg/phaseframe/phases/ensure_etcd_upgrade.go` | etcd 升级 |
| `pkg/phaseframe/phases/ensure_containerd_upgrade.go` | containerd 升级 |
| `pkg/phaseframe/phases/ensure_agent_upgrade.go` | bkeagent 升级 |
| `pkg/command/upgrade.go` | Upgrade Command CR 创建 |
