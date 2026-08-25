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

本提案设计 Kubernetes v1.34 → v1.36 的声明式升级方案。在 openFuyao 架构中，升级的基本单位是 **openFuyao 版本**（如 v2.6.0 → v2.7.0），而非 K8s 版本。每个 openFuyao 版本通过 ReleaseImage 声明其包含的组件版本（K8s、etcd、containerd 等）。当 openFuyao 版本升级导致 K8s 版本跨越多个小版本（如 v1.34 → v1.36）时，需要处理 kubeadm 版本约束、API 兼容性、组件配套升级等问题。

本方案基于已有的 DAG 驱动升级框架（KEP-5），通过 ReleaseImage 定义版本清单、UpgradePath 管控 openFuyao 版本间升级路径，由 ClusterVersionReconciler 逐跳触发、BKEClusterReconciler 构建 DAG 执行、BKEAgent 在节点上执行 kubeadm 升级命令。

## 2. 动机

### 2.1 版本模型

openFuyao 采用分层版本模型：

```
┌─────────────────────────────────────────────────────────────────────┐
│  openFuyao 版本（升级基本单位）                                      │
│  ClusterVersion.Spec.DesiredVersion = v2.7.0                       │
│  UpgradePath: v2.6.0 → v2.7.0（合法路径校验）                     │
│                                                                     │
│  ReleaseImage v2.7.0:                                               │
│  ├─ kubernetes-master: v1.36.0    ← K8s 版本是组件，非升级单位     │
│  ├─ kubernetes-worker: v1.36.0                                     │
│  ├─ etcd: v3.5.20                                                  │
│  ├─ containerd: v1.7.24                                            │
│  ├─ bkeagent: v2.7.0                                               │
│  ├─ kube-proxy: v1.36.0                                            │
│  ├─ coredns: v1.11.3                                               │
│  └─ ...                                                            │
└─────────────────────────────────────────────────────────────────────┘
```

**关键约束**：
- `ClusterVersion.Spec.DesiredVersion` 是 openFuyao 版本，不是 K8s 版本
- 一个 openFuyao 版本可能跨越多个 K8s 小版本（如 v2.6.0 含 K8s 1.34，v2.7.0 含 K8s 1.36）
- 也可能 openFuyao 版本升级但 K8s 版本不变（仅升级其他组件）
- UpgradePath 在 openFuyao 版本级别管控，不在 K8s 版本级别

### 2.2 升级场景

| 场景 | openFuyao 版本 | K8s 版本变化 | 说明 |
|------|---------------|-------------|------|
| **同 K8s 版本升级** | v2.6.0 → v2.6.1 | v1.34 → v1.34 | 仅 patch 升级，K8s 不变 |
| **K8s 跨 1 个小版本** | v2.6.0 → v2.7.0 | v1.34 → v1.35 | 常规升级 |
| **K8s 跨 2 个小版本** | v2.6.0 → v2.7.0 | v1.34 → v1.36 | 本方案目标场景 |
| **多跳逐步升级** | v2.6.0→v2.6.5→v2.7.0 | v1.34→v1.35→v1.36 | 通过中间版本逐跳 |

### 2.3 现有框架能力

openFuyao 声明式升级框架已具备以下能力：

- **ReleaseImage CRD**：定义版本清单（`spec.install.components` + `spec.upgrade.components`）
- **ComponentVersion CRD**：声明组件类型（inline/yaml/helm）、依赖关系、升级策略
- **UpgradePath CRD**：管控 **openFuyao 版本间** 升级路径合法性
- **DAG 调度器**：从 ReleaseImage bundle 动态构建 DAG，按拓扑批次并行执行
- **VersionContext**：通过 `current != target` 字符串比较判断是否需要升级
- **BKEAgent 命令机制**：通过 `Command` CR 向节点发送 `Kubeadm Upgrade*` 内置命令
- **ClusterVersionReconciler**：通过 `cvo.openfuyao.cn/upgrade-ready` annotation 逐跳触发

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **升级单位** | openFuyao 版本（如 v2.6.0 → v2.7.0） |
| **K8s 版本变化** | v1.34 → v1.36（可能跨 2 个小版本） |
| **配套组件** | containerd、etcd、kubelet、apiserver、controller-manager、scheduler、kube-proxy、coredns、bkeagent |
| **回滚策略** | 仅支持集群级回滚（回退 openFuyao 版本） |
| **PreCheck/PostCheck** | 引用 KEP-5-2 的 CheckPolicy CRD 设计 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **openFuyao 版本为升级单位** | 用户修改 `DesiredVersion`（openFuyao 版本），系统自动编排组件升级 |
| **K8s 版本是组件** | K8s 版本通过 ReleaseImage 中的 `kubernetes-master`/`kubernetes-worker` 组件定义 |
| **kubeadm 版本约束** | kubeadm 可能不支持跨多个 K8s 小版本直接升级，需验证或提供中间版本 |
| **版本一致性** | 运行态必须与 ReleaseImage 保持一致，不支持部分回滚 |
| **先 master 后 worker** | kubelet 不能比 apiserver 新，必须先升级控制面 |
| **BKEAgent 就绪** | 节点 BKEAgent 必须 Ready 才能执行升级命令 |
| **幂等性** | 所有升级操作必须幂等，支持 Reconcile 重入 |

## 4. 版本兼容性矩阵

### 4.1 openFuyao 版本与组件版本对应关系

| openFuyao 版本 | K8s | containerd | etcd | bkeagent | kube-proxy | coredns | 说明 |
|---------------|-----|-----------|------|----------|------------|---------|------|
| **v2.6.0** | v1.34.x | v1.7.20 | v3.5.18 | v2.6.0 | v1.34.x | v1.11.1 | 当前版本 |
| **v2.6.5** | v1.35.x | v1.7.22 | v3.5.19 | v2.6.5 | v1.35.x | v1.11.2 | 中间版本（可选） |
| **v2.7.0** | v1.36.x | v1.7.24 | v3.5.20 | v2.7.0 | v1.36.x | v1.11.3 | 目标版本 |

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

| K8s 版本 | 废弃 API | 替代 API | 影响范围 |
|---------|---------|---------|---------|
| **v1.35** | `flowcontrol.apiserver.k8s.io/v1beta3` | `v1` | FlowSchema, PriorityLevelConfiguration |
| **v1.36** | `discovery.k8s.io/v1beta1` | `v1` | EndpointSlice |

## 5. 升级策略设计

### 5.1 升级路径方案

**核心问题**：openFuyao v2.6.0（K8s 1.34）升级到 v2.7.0（K8s 1.36）时，K8s 跨越 2 个小版本。kubeadm 是否支持直接从 1.34 升级到 1.36？

**方案 A：单跳直接升级（如果 kubeadm 支持）**

```
openFuyao v2.6.0 (K8s 1.34) → v2.7.0 (K8s 1.36)
```

- UpgradePath 定义 `v2.6.0 → v2.7.0` 合法路径
- ReleaseImage v2.7.0 包含 `kubernetes-master: v1.36.0`
- VersionContext: `kubernetes-master` current=v1.34.0, target=v1.36.0
- DAG 执行时 `EnsureMasterUpgrade` 发送 `Kubeadm UpgradeControlPlane` 命令
- **前提**：验证 kubeadm 支持从 1.34 直接升级到 1.36

**方案 B：多跳逐步升级（通过中间版本）**

```
openFuyao v2.6.0 (K8s 1.34) → v2.6.5 (K8s 1.35) → v2.7.0 (K8s 1.36)
```

- 发布中间版本 v2.6.5（含 K8s 1.35）
- UpgradePath 定义 `v2.6.0 → v2.6.5 → v2.7.0` 合法路径
- ClusterVersionReconciler 逐跳触发（2 跳）
- 每跳都是完整的 openFuyao 版本升级（含所有组件）

**推荐**：优先验证方案 A（单跳），若 kubeadm 不支持则使用方案 B（多跳）。

### 5.2 ReleaseImage 结构设计

```yaml
# ReleaseImage v2.7.0（目标版本，含 K8s 1.36）
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.7.0
spec:
  version: "v2.7.0"
  digest: "sha256:..."
  install:
    components:
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.24
      - name: etcd
        version: v3.5.20
      - name: kubernetes-master
        version: v1.36.0          # K8s 版本是组件版本，非顶层版本
      - name: kubernetes-worker
        version: v1.36.0
      - name: kube-proxy
        version: v1.36.0
      - name: coredns
        version: v1.11.3
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: v1.0.0
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.24
      - name: etcd
        version: v3.5.20
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0.0
      - name: kubernetes-master
        version: v1.36.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.36.0
        inline:
          handler: EnsureWorkerUpgrade
          version: v1.0.0
      - name: kube-proxy
        version: v1.36.0
      - name: coredns
        version: v1.11.3
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
6. VersionContext 决策：current != target 才执行，否则跳过
```

**VersionContext 版本比较**：

```go
// VersionContext 使用简单字符串比较
// Target 来自 ReleaseImage v2.7.0 bundle:
//   kubernetes-master = v1.36.0
//   etcd = v3.5.20
//   containerd = v1.7.24
//   ...
//
// Current 来自当前 ReleaseImage v2.6.0 bundle:
//   kubernetes-master = v1.34.0
//   etcd = v3.5.18
//   containerd = v1.7.20
//   ...
//
// NeedsUpgrade("kubernetes-master") => "v1.34.0" != "v1.36.0" => true
// NeedsUpgrade("bkeagent") => "v2.6.0" != "v2.7.0" => true
```

**典型 DAG 结构（openFuyao v2.6.0 → v2.7.0 单跳）**：

```
Batch 1: [pre-upgrade-resources]     ← 隐式前置依赖，最先执行
    └─ 创建升级所需的 CRD/RBAC/ConfigMap 资源

Batch 2: [bkeagent, containerd]      ← 无相互依赖，并行执行
    ├─ bkeagent: v2.6.0 → v2.7.0（SSH 推送二进制）
    └─ containerd: v1.7.20 → v1.7.24（ENV 命令）

Batch 3: [etcd]                      ← 依赖 pre-upgrade-resources
    └─ etcd: v3.5.18 → v3.5.20（Upgrade CR → Kubeadm UpgradeEtcd）

Batch 4: [kubernetes-master]         ← 依赖 etcd
    └─ master: v1.34.0 → v1.36.0（Upgrade CR → Kubeadm UpgradeControlPlane）

Batch 5: [kubernetes-worker]         ← 依赖 kubernetes-master（倾斜策略）
    └─ worker: v1.34.0 → v1.36.0（Upgrade CR → Kubeadm UpgradeWorker）

Batch 6: [kube-proxy, coredns]       ← 依赖 kubernetes-master
    ├─ kube-proxy: v1.34.0 → v1.36.0（YAML Apply）
    └─ coredns: v1.11.1 → v1.11.3（YAML Apply）
```

> **注意**：实际 DAG 结构由 ReleaseImage bundle 中的 `ComponentVersion.spec.dependencies` 决定。上图为典型配置。

### 5.4 多跳升级编排

当采用方案 B（多跳逐步升级）时，升级编排如下：

```
用户操作：修改 ClusterVersion.Spec.DesiredVersion = v2.7.0（openFuyao 版本）

ClusterVersionReconciler 编排：
┌─────────────────────────────────────────────────────────────────────┐
│  Hop 1: openFuyao v2.6.0 → v2.6.5                                  │
│  （K8s v1.34.0 → v1.35.0, containerd v1.7.20→v1.7.22, ...）        │
│                                                                     │
│  1. ClusterVersionReconciler 校验 UpgradePath:                     │
│     v2.6.0 → v2.6.5 路径合法、未被阻断                              │
│  2. 设置 annotation: cvo.openfuyao.cn/upgrade-ready = v2.6.5      │
│  3. BKEClusterReconciler 检测到 annotation:                       │
│     └─ executeUpgradeDAG()                                          │
│        ├─ 解析 ReleaseImage v2.6.5（Status.Phase 必须为 Valid）   │
│        ├─ 构建 VersionContext                                       │
│        │   ├─ kubernetes-master: current=v1.34.0, target=v1.35.0  │
│        │   ├─ etcd: current=v3.5.18, target=v3.5.19               │
│        │   └─ ...                                                   │
│        ├─ 构建 DAG + 执行 DAG                                      │
│        └─ 成功后清除 annotation + CompleteUpgradeHop               │
│  4. ClusterVersionReconciler 更新:                                 │
│     └─ Status.CurrentVersion = v2.6.5                              │
└─────────────────────────────────────────────────────────────────────┘
                                    ↓
┌─────────────────────────────────────────────────────────────────────┐
│  Hop 2: openFuyao v2.6.5 → v2.7.0                                  │
│  （K8s v1.35.0 → v1.36.0, containerd v1.7.22→v1.7.24, ...）       │
│                                                                     │
│  1. ClusterVersionReconciler 校验 UpgradePath:                     │
│     v2.6.5 → v2.7.0 路径合法                                       │
│  2. 设置 annotation: cvo.openfuyao.cn/upgrade-ready = v2.7.0       │
│  3. BKEClusterReconciler 检测到 annotation:                       │
│     └─ executeUpgradeDAG()                                          │
│        ├─ 解析 ReleaseImage v2.7.0                                 │
│        ├─ 构建 VersionContext                                       │
│        │   ├─ kubernetes-master: current=v1.35.0, target=v1.36.0  │
│        │   ├─ etcd: current=v3.5.19, target=v3.5.20               │
│        │   └─ ...                                                   │
│        ├─ 构建 DAG + 执行 DAG                                      │
│        └─ 成功后清除 annotation + CompleteUpgradeHop               │
│  4. ClusterVersionReconciler 更新:                                 │
│     └─ Status.CurrentVersion = v2.7.0                               │
└─────────────────────────────────────────────────────────────────────┘
```

**关键设计**：
- 升级单位是 openFuyao 版本，K8s 版本是其中的一个组件
- 每跳由 `ClusterVersionReconciler` 通过 `cvo.openfuyao.cn/upgrade-ready` annotation 触发
- VersionContext 的 Target/Current 是各组件的版本（如 `kubernetes-master=v1.36.0`），而非 openFuyao 版本
- BKEClusterReconciler 执行完一跳后清除 annotation，控制权交回 ClusterVersionReconciler
- 用户可在任意跳之间暂停（设置 `BKECluster.Spec.Pause = true`）
- 如果某一跳失败，`StatusManager` 提供 10 次重试，超过后设置终端 `*Failed` 状态

### 5.5 单跳直接升级方案（方案 A 详细设计）

如果 kubeadm 支持从 v1.34 直接升级到 v1.36，则无需中间版本：

```
用户操作：修改 ClusterVersion.Spec.DesiredVersion = v2.7.0

ClusterVersionReconciler:
1. 校验 UpgradePath: v2.6.0 → v2.7.0 合法
2. 设置 annotation: cvo.openfuyao.cn/upgrade-ready = v2.7.0

BKEClusterReconciler.executeUpgradeDAG():
1. 解析 ReleaseImage v2.7.0
2. 构建 VersionContext:
   ├─ kubernetes-master: current=v1.34.0, target=v1.36.0（跨 2 个小版本）
   ├─ etcd: current=v3.5.18, target=v3.5.20
   ├─ containerd: current=v1.7.20, target=v1.7.24
   └─ ...
3. 所有组件 current != target，全部触发升级
4. DAG 执行（同 5.3 节 DAG 结构）

ClusterVersionReconciler:
3. 更新 Status.CurrentVersion = v2.7.0
```

**方案 A 的前提验证**：

| 验证项 | 说明 | 验证方法 |
|--------|------|---------|
| **kubeadm 支持** | kubeadm `UpgradeControlPlane` 是否支持从 1.34 到 1.36 | 测试环境验证 |
| **etcd 数据兼容** | etcd v3.5.18 → v3.5.20 数据格式是否兼容 | etcd 升级文档 |
| **API 累积变更** | v1.35 和 v1.36 的 API 废弃是否同时影响 | PreCheck 扫描 |

## 6. 组件升级方案

### 6.1 bkeagent 升级

**执行方式**：SSH 直接推送二进制（非 Command CR）

**原因**：BKEAgent 无法自升级（chicken-and-egg 问题），必须由 Controller 直接推送

**执行流程**（`EnsureAgentUpgrade` → `upgradeBKEAgentViaSSH`）：

```
1. 获取所有节点（BKENodes）
2. 解析目标版本（VersionContext.GetTarget("bkeagent")）
   └─ 例如: v2.6.0 → v2.7.0
3. SSH 发现每个节点的 CPU 架构（amd64/arm64）
4. 从 HTTP 制品仓库下载对应架构的 bkeagent 二进制
5. SSH 推送二进制到每个节点
6. Ping 验证升级后的 Agent 响应
7. Master 节点失败：立即返回错误（阻塞）
   Worker 节点失败：记录失败数量，不阻塞
```

### 6.2 containerd 升级

**执行方式**：ENV 命令（`NewConatinerdReset` + `NewConatinerdRedeploy`）

**执行流程**（`EnsureContainerdUpgrade` → `rolloutContainerd`）：

```
1. 同步目标版本到 BKECluster.Spec（SyncUpgradeTargetsToClusterSpec）
2. 解析目标版本（VersionContext.GetTarget("containerd")）
   └─ 例如: v1.7.20 → v1.7.24
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
      ├─ EtcdVersion: v3.5.20（从 VersionContext 获取）
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
   └─ 跳过 kubelet 版本已匹配目标版本的节点
     （读取 Node.Status.NodeInfo.KubeletVersion）
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

> **注意**：如果 K8s 跨多个小版本（如 v1.34→v1.36），kubeadm 的 `UpgradeControlPlane` 命令是否支持直接跨版本升级需要验证。若不支持，需采用 5.1 方案 B（多跳）。

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
1. 从 ManifestStore 加载 ComponentVersion
2. 检查是否需要升级（manifestNeedsUpgrade: 比较当前 vs 目标镜像 tag）
3. YamlInstaller.Apply:
   a. 加载完整资源清单（DaemonSet + ConfigMap + RBAC）
   b. 按文件名排序应用（01-crd → 02-rbac → 03-configmap → 04-daemonset）
   c. 使用 SSA Apply 更新资源（ServerSideApply 策略）
   d. Prune 不再需要的旧版本资源（按 label selector）
4. 健康检查:
   a. PodReady: 验证 kube-proxy Pod Ready
   b. EndpointReady: 验证 Service Endpoint 就绪

coredns (type: yaml):
1. 从 ManifestStore 加载 ComponentVersion
2. 同上流程，应用 Deployment + ConfigMap + Service + RBAC
3. 健康检查: 验证 coredns Pod Ready + DNS 解析正常
```

### 6.7 升级组件清单汇总

| 组件 | 执行模式 | 执行方式 | 版本比较 | 滚动策略 | 失败处理 |
|------|---------|---------|---------|---------|---------|
| **pre-upgrade-resources** | inline | PhaseHandler | current != target | 一次性 | 阻塞（FailFast） |
| **bkeagent** | inline | SSH 推送二进制 | v2.6.0 != v2.7.0 | 全节点 | Master 阻塞，Worker 继续 |
| **containerd** | inline | ENV 命令 | v1.7.20 != v1.7.24 | 批量节点 | 阻塞 |
| **etcd** | inline | Upgrade CR → Kubeadm | v3.5.18 != v3.5.20 | 逐节点阻塞 | 阻塞 |
| **kubernetes-master** | inline | Upgrade CR → Kubeadm | v1.34.0 != v1.36.0 | 逐节点阻塞 | 阻塞 |
| **kubernetes-worker** | inline | Upgrade CR → Kubeadm | v1.34.0 != v1.36.0 | 逐节点非阻塞 | 继续（记录失败） |
| **kube-proxy** | manifest | YamlInstaller SSA Apply | v1.34.0 != v1.36.0 | DaemonSet 滚动 | 阻塞 |
| **coredns** | manifest | YamlInstaller SSA Apply | v1.11.1 != v1.11.3 | Deployment 滚动 | 阻塞 |

## 7. 升级执行流程

### 7.1 触发机制

```
1. 用户修改 ClusterVersion.Spec.DesiredVersion = v2.7.0（openFuyao 版本）
2. ClusterVersionReconciler 检测到 openFuyao 版本变更
3. 校验 UpgradePath（v2.6.0 → v2.7.0 路径合法、未被阻断）
4. 设置 annotation: cvo.openfuyao.cn/upgrade-ready = v2.7.0
5. BKEClusterReconciler 检测到 annotation
```

### 7.2 DAG 执行流程（单跳）

```
BKEClusterReconciler.executeUpgradeDAG():

1. 解析 hop 目标版本（从 annotation 读取 openFuyao 版本: v2.7.0）
2. 解析 ReleaseImage CR:
   ├─ 通过 clusterversion.ResolveReleaseImageForVersion 获取
   └─ 验证 Status.Phase == Valid（否则 requeue 30s）
3. 解析 OCI bundle（releaseStore.ResolveRelease）
4. 解析当前版本 bundle（用于构建 VersionContext.Current）
5. 构建 VersionContext:
   ├─ Target: 从目标 bundle 的 ReleaseImage.Spec.Upgrade.Components
   │   ├─ kubernetes-master: v1.36.0
   │   ├─ etcd: v3.5.20
   │   ├─ containerd: v1.7.24
   │   └─ ...
   └─ Current: 从当前 bundle 或 BKECluster.Status
       ├─ kubernetes-master: v1.34.0
       ├─ etcd: v3.5.18
       ├─ containerd: v1.7.20
       └─ ...
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

### 7.3 PreCheck / PostCheck

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
- 回滚时回退 openFuyao 版本（如 v2.7.0 回退到 v2.6.0）

### 8.2 回滚触发

| 场景 | 触发方式 | 说明 |
|------|---------|------|
| **升级过程中失败** | 自动（FailurePolicy） | StatusManager 10 次重试后设置终端 Failed 状态 |
| **升级完成后发现问题** | 手动（修改 DesiredVersion 回退） | 回退到指定 openFuyao 版本 |

### 8.3 回滚执行

回滚通过修改 `ClusterVersion.Spec.DesiredVersion` 回退触发，复用升级 DAG：

```
单跳回滚（openFuyao v2.7.0 → v2.6.0）:
1. 用户修改 DesiredVersion = v2.6.0
2. ClusterVersionReconciler 设置 upgrade-ready = v2.6.0
3. BKEClusterReconciler 执行 executeUpgradeDAG:
   ├─ 加载 ReleaseImage v2.6.0
   ├─ VersionContext:
   │   ├─ kubernetes-master: current=v1.36.0, target=v1.34.0
   │   ├─ etcd: current=v3.5.20, target=v3.5.18
   │   └─ ...
   ├─ DAG 执行（各组件 current != target，触发"升级"）
   │   └─ 实际是降级操作（各执行器通过版本比较判断方向）
   └─ etcd 数据从升级前备份恢复
4. 更新 CurrentVersion = v2.6.0

多跳回滚（openFuyao v2.7.0 → v2.6.0，如果 v2.6.0→v2.7.0 走了多跳）:
  Hop 2 回滚: v2.7.0 → v2.6.5
  Hop 1 回滚: v2.6.5 → v2.6.0
```

### 8.4 etcd 数据回滚

- **升级前备份**：每个 etcd 节点升级前通过 `etcdctl snapshot save` 备份
- **回滚时恢复**：如果 etcd 数据格式不兼容，从 snapshot 恢复
- **数据丢失风险**：升级后产生的数据在回滚时会丢失

## 9. 可观测性

### 9.1 状态追踪

```bash
# 查询 openFuyao 版本升级状态
kubectl get clusterversion my-cluster -o jsonpath='{.status}'
# 输出:
# {
#   "currentVersion": "v2.6.0",
#   "phase": "Upgrading",
#   "upgradeHistory": [...]
# }

# 查询 DAG 执行进度
kubectl get bkecluster my-cluster -o jsonpath='{.status.declarativeUpgrade}'
# 输出:
# {
#   "targetVersion": "v2.7.0",
#   "completed": ["pre-upgrade-resources", "bkeagent", "containerd"],
#   "lastError": ""
# }

# 查询组件状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.clusterComponentStatuses}'
# 输出:
# {
#   "etcd": {"phase": "Pending", "version": "v3.5.18"},
#   "containerd": {"phase": "Installed", "version": "v1.7.24"}
# }

# 查询 K8s 版本
kubectl get bkecluster my-cluster -o jsonpath='{.status.kubernetesVersion}'
# 输出: v1.34.0（升级中）/ v1.36.0（升级后）
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
| **ReleaseImage 定义** | 定义 v2.7.0（+可选 v2.6.5）的 ReleaseImage + ComponentVersion | 3 |
| **UpgradePath 配置** | 配置 openFuyao 版本间升级路径 + 兼容性规则 | 1 |
| **bkeagent 升级适配** | 适配 v2.7.0 版本的 bkeagent SSH 推送 | 2 |
| **containerd 升级适配** | 适配 v1.7.24 的 ENV 命令（reset + redeploy） | 3 |
| **etcd 升级适配** | 适配 v3.5.20 的 Upgrade CR + 健康检查 | 3 |
| **控制面升级适配** | 适配 K8s v1.36.0 的 Upgrade CR + kubeadm 验证 | 4 |
| **kubelet 升级适配** | 适配 K8s v1.36.0 的 Upgrade CR + drain/uncordon | 3 |
| **kube-proxy/coredns 清单** | 编写各版本的完整 YAML 清单 + ConfigMap 适配 | 4 |
| **PreCheck 适配** | 适配 KEP-5-2 CheckPolicy，增加 API 废弃扫描（v1.35+v1.36） | 5 |
| **PostCheck 适配** | 适配版本验证 + 健康 + DNS + 应用检查 | 3 |
| **kubeadm 跨版本验证** | 验证 kubeadm 是否支持 K8s 1.34→1.36 直接升级 | 3 |
| **多跳编排验证** | 验证 ClusterVersionReconciler 逐跳触发逻辑（如需多跳） | 3 |
| **回滚验证** | 验证 VersionContext 回滚语义 + etcd 数据恢复 | 4 |
| **小计** | - | **41 人天** |

### 10.2 测试工作量

| 测试类型 | 测试内容 | 工作量（人天） |
|---------|---------|---------------|
| **单元测试** | 各组件版本比较、DAG 构建、依赖解析 | 5 |
| **集成测试** | 单跳升级（v2.6.0→v2.7.0） | 8 |
| **E2E 测试** | 完整升级流程（含 K8s 1.34→1.36） | 8 |
| **多跳测试** | 多跳升级（v2.6.0→v2.6.5→v2.7.0，如需多跳） | 5 |
| **回滚测试** | 各场景回滚 | 5 |
| **兼容性测试** | API 兼容性、业务应用兼容性 | 5 |
| **性能测试** | 升级过程性能影响 | 3 |
| **压力测试** | 大规模集群升级 | 3 |
| **小计** | - | **42 人天** |

### 10.3 文档工作量

| 文档类型 | 文档内容 | 工作量（人天） |
|---------|---------|---------------|
| **升级指南** | 用户升级操作指南 | 2 |
| **兼容性矩阵** | openFuyao/K8s/component 版本兼容性矩阵 | 1 |
| **故障排查** | 升级故障排查指南 | 2 |
| **API 变更** | K8s 1.34→1.36 API 变更清单 | 2 |
| **Release Notes** | 各版本 Release Notes | 1 |
| **小计** | - | **8 人天** |

### 10.4 总工作量汇总

| 类别 | 工作量（人天） | 工作量（人周） |
|------|---------------|---------------|
| **开发** | 41 | 8.2 |
| **测试** | 42 | 8.4 |
| **文档** | 8 | 1.6 |
| **总计** | **91** | **18.2** |

**按人员配置估算**：
- 如果 2 人全职投入：约 9 周（2 个月）
- 如果 3 人全职投入：约 6 周（1.5 个月）
- 如果 4 人全职投入：约 4.5 周（1 个月）

> **注意**：工作量基于现有声明式升级框架已实现 DAG 调度、Command CR、ENV 命令、SSH 推送等核心能力，本次工作主要是版本适配、kubeadm 跨版本验证和测试验证。

## 11. 风险与缓解措施

### 11.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **kubeadm 不支持跨版本升级** | 无法单跳从 K8s 1.34 到 1.36 | 中 | 提前验证；若不支持，发布中间版本 v2.6.5（K8s 1.35）走多跳 |
| **etcd 数据格式不兼容** | etcd 升级失败 | 中 | 升级前 snapshot 备份，验证版本兼容性 |
| **API 废弃累积** | v1.35 和 v1.36 的 API 废弃同时影响 | 高 | PreCheck 扫描所有废弃 API |
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
| **提供兼容性矩阵** | openFuyao 社区 | 8 月底 | openFuyao/K8s/containerd/etcd 版本兼容性矩阵 |
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
| **openFuyao 版本** | openFuyao 平台的发布版本（如 v2.7.0），是升级的基本单位 |
| **Hop** | 一次 openFuyao 版本跳转（如 v2.6.0 → v2.7.0） |
| **ReleaseImage** | 版本清单 CRD，定义 install/upgrade 组件列表 |
| **ComponentVersion** | 组件版本定义 CRD，声明类型/依赖/策略 |
| **UpgradePath** | 升级路径 CRD，管控 openFuyao 版本间升级合法性 |
| **VersionContext** | 版本上下文，通过 current/target 字符串比较判断是否需要升级 |
| **K8s 版本** | ReleaseImage 中 `kubernetes-master`/`kubernetes-worker` 组件的版本，非顶层版本 |
| **DAG** | 有向无环图，从 ReleaseImage bundle 动态构建，按拓扑批次执行 |
| **Command CR** | BKEAgent 命令 CRD，向节点发送 Kubeadm 升级命令 |
| **ENV 命令** | 环境命令，批量操作多节点（containerd reset/redeploy） |
| **倾斜策略** | kubelet 版本不能比 apiserver 新，最多旧 2 个版本 |
| **DeclarativeUpgradeStatus** | DAG 升级状态追踪，记录已完成组件列表 |
| **upgrade-ready annotation** | `cvo.openfuyao.cn/upgrade-ready`，触发单跳升级（值为 openFuyao 版本） |

### C. 代码库参考

| 代码路径 | 说明 |
|---------|------|
| `controllers/capbke/bkecluster_upgrade_dag.go` | DAG 执行入口 `executeUpgradeDAG` |
| `controllers/clusterversion/` | ClusterVersionReconciler（逐跳触发） |
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
