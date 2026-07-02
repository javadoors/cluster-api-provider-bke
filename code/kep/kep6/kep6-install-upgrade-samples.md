# KEP-6 安装与升级样例

**文档版本**: v1.2  
**状态**: Draft  
**依赖**: KEP-6 详细设计文档 (kep6-detailed-design.md)

---

## 目录

1. [安装样例](#1-安装样例)
   - 1.1 ComponentVersion YAML 样例
   - 1.2 ReleaseImage YAML 样例
   - 1.3 安装执行流程
2. [升级样例](#2-升级样例)
   - 2.1 版本变更对比
   - 2.2 ReleaseImage YAML 样例
   - 2.3 升级执行流程
3. [关键设计点说明](#3-关键设计点说明)

---

## 1. 安装样例

**场景**：用户新建 BKECluster，desiredVersion 指向 ReleaseImage v2.6.0，需安装 containerd（binary）、coredns（helm）、openfuyao-core（yaml）、kubernetes-master/worker（inline）四种类型组件。

### 1.1 ComponentVersion YAML 样例

```yaml
# bke-manifests/containerd/v1.7.18/component.yaml (简化示例，完整定义见 13.3.2)
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.18
spec:
  name: containerd
  type: binary
  version: v1.7.18
  binary:
    artifacts:
      - name: containerd
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:abc123def456..."
        installPath: "/"  # 解压到根目录
    installScript: |
      #!/bin/bash
      set -e
      systemctl stop containerd || true
      tar -xzf {{artifact.containerd.path}} -C /
      chmod +x /usr/bin/containerd
      systemctl daemon-reload
      systemctl enable containerd
      systemctl start containerd
      /usr/bin/containerd --version
    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
    defaultConfigPath: "/etc/containerd"
    defaultLogPath: "/var/log/containerd"
    defaultDataPath: "/var/lib/containerd"
  upgradeStrategy:
    mode: Rolling
    failurePolicy: FailFast
    timeout: "10m"
```

```yaml
# bke-manifests/coredns/v1.11.1/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.11.1
spec:
  name: coredns
  type: helm
  version: v1.11.1
  helm:
    chart:
      oci:
        repository: "registry.openfuyao.cn/charts/coredns"
        tag: "v1.11.1"
      checksum: "sha256:ghi789jkl012..."
    namespace: kube-system
    releaseName: coredns
    values:
      image:
        repository: "registry.openfuyao.cn/coredns/coredns"
        tag: "{{componentVersion}}"
      replicaCount: 2
      resources:
        limits:
          cpu: "100m"
          memory: "128Mi"
    strategy:
      mode: Upgrade
      wait: true
      waitTimeout: "5m"
      atomic: true
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: kube-system
            labelSelector: "k8s-app=kube-dns"
            minReady: 1
  upgradeStrategy:
    mode: Parallel
    failurePolicy: FailFast
    timeout: "10m"
```

```yaml
# bke-manifests/openfuyao-core/v26.03/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: openfuyao-core-v26.03
spec:
  name: openfuyao-core
  type: yaml
  version: v26.03
  yaml:
    manifests:
      - url: "https://release-repo.openfuyao.cn/manifests/openfuyao-core/v26.03/crds.yaml"
        checksum: "sha256:mno345pqr678..."
      - url: "https://release-repo.openfuyao.cn/manifests/openfuyao-core/v26.03/deployment.yaml"
        checksum: "sha256:stu901vwx234..."
    namespace: openfuyao-system
    applyStrategy: ServerSideApply
    prune: true
    pruneLabelSelector:
      app.kubernetes.io/managed-by: openfuyao-core
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: openfuyao-system
            labelSelector: "app.kubernetes.io/name=openfuyao-core"
            minReady: 1
  subComponents:
    - name: kubernetes-master
      version: v1.29.0
    - name: kubernetes-worker
      version: v1.29.0
  upgradeStrategy:
    mode: Parallel
    failurePolicy: FailFast
    timeout: "5m"
```

### 1.2 ReleaseImage YAML 样例

```yaml
# releaseimage-v2.6.0.yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: bke-v2.6.0
spec:
  version: v2.6.0
  install:
    components:
      - name: container-runtime
        version: v1.0.0
      - name: bkeagent
        version: v2.6.0
      - name: coredns
        version: v1.11.1
      - name: openfuyao-core
        version: v26.03
      - name: kubernetes-master
        version: v1.29.0
        inline:
          handler: EnsureMasterInit
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.29.0
        inline:
          handler: EnsureWorkerJoin
          version: v1.0.0
```

### 1.3 安装执行流程

```
用户创建 BKECluster (desiredVersion: v2.6.0)
  │
  ▼
BKEClusterReconciler.Reconcile()
  │
  ├─ 1. 解析 ReleaseImage v2.6.0
  │     releaseImage.GetInstallComponents()
  │     → [containerd, bkeagent, coredns, openfuyao-core, kubernetes-master, kubernetes-worker]
  │
  ├─ 2. 加载 ComponentVersion
  │     manifestStore.GetComponentManifests() 逐个加载组件定义
  │     → containerd: type=binary, artifacts=[...], installScript=...
  │     → coredns: type=helm, chart=oci://..., values={...}
  │     → openfuyao-core: type=yaml, manifests=[...], subComponents=[...]
  │
  ├─ 3. 构建安装 DAG
  │     BuildInstallDAG(releaseImage)
  │
  │     DAG 拓扑批次:
  │     Batch 0: [finalizer, paused, manage, delete, dryrun]  (CommonPhases, inline)
  │     Batch 1: [containerd, bkeagent]                       (binary, 并行)
  │     Batch 2: [kubernetes-master]                          (inline, 依赖 containerd)
  │     Batch 3: [kubernetes-worker]                          (inline, 依赖 kubernetes-master)
  │     Batch 4: [coredns, openfuyao-core]                   (helm/yaml, 依赖 kubernetes-master)
  │
  ├─ 4. Scheduler.ExecuteDAG(ctx, dag)
  │     │
  │     ├─ Batch 0: CommonPhases (inline executor)
  │     │   finalizer → paused → manage → delete → dryrun
  │     │
  │     ├─ Batch 1: Binary 组件 (并行)
  │     │   ├─ containerd: BinaryComponentExecutor
  │     │   │   ├─ VersionContext.HasCurrent("containerd") = false → Action = Install
  │     │   │   ├─ NodeProvider.GetNodes() → 3 个节点
  │     │   │   ├─ Rolling 逐节点:
  │     │   │   │   node1: 下载制品 → 渲染脚本 → SSH 执行安装 → ✅
  │     │   │   │   node2: 下载制品 → 渲染脚本 → SSH 执行安装 → ✅
  │     │   │   │   node3: 下载制品 → 渲染脚本 → SSH 执行安装 → ✅
  │     │   │   └─ bkeagent: BinaryComponentExecutor (同上)
  │     │
  │     ├─ Batch 2: kubernetes-master (inline executor)
  │     │   └─ InlineRunner.Execute(handler="EnsureMasterInit") → kubeadm init
  │     │
  │     ├─ Batch 3: kubernetes-worker (inline executor)
  │     │   └─ InlineRunner.Execute(handler="EnsureWorkerJoin") → kubeadm join
  │     │
  │     └─ Batch 4: Helm + YAML 组件 (并行)
  │         ├─ coredns: HelmComponentExecutor
  │         │   ├─ VersionContext.HasCurrent("coredns") = false → Action = Install
  │         │   ├─ 拉取 Chart (OCI Registry)
  │         │   ├─ 渲染 Values (模板变量)
  │         │   ├─ helm install --atomic --wait
  │         │   └─ HealthCheck: PodReady (kube-dns) → ✅
  │         │
  │         └─ openfuyao-core: YamlComponentExecutor
  │             ├─ VersionContext.HasCurrent("openfuyao-core") = false → 需要安装
  │             ├─ resolveManifests(): 从 URL 下载 crds.yaml + deployment.yaml
  │             ├─ parseYAMLDocuments(): 解析多文档 YAML
  │             ├─ ApplyWithStrategy(ServerSideApply): 应用到目标集群
  │             └─ PruneResources(): 无废弃资源 (首次安装)
  │
  ├─ 5. 健康检查
  │     PodReady + EndpointReady 检查所有组件
  │
  └─ 6. 更新 BKECluster.Status
        phase: Ready
        conditions: [{type: Ready, status: True}]
```

---

## 2. 升级样例

**场景**：集群从 v2.5.0 升级到 v2.6.0，containerd/bkeagent/coredns/openfuyao-core 版本变更，kubernetes-master/worker 版本不变。

### 2.1 版本变更对比

| 组件 | 当前版本 | 目标版本 | 类型 | 升级策略 | FailurePolicy |
|------|---------|---------|------|---------|---------------|
| containerd | v1.7.15 | v1.7.18 | binary | Rolling | FailFast |
| bkeagent | v2.5.0 | v2.6.0 | binary | Batch (batchSize=2) | Continue |
| coredns | v1.10.1 | v1.11.1 | helm | Parallel | FailFast |
| openfuyao-core | v26.01 | v26.03 | yaml | Parallel | FailFast |
| kubernetes-master | v1.29.0 | v1.29.0 | inline | — | 不升级 |
| kubernetes-worker | v1.29.0 | v1.29.0 | inline | — | 不升级 |

### 2.2 ReleaseImage YAML 样例

```yaml
# releaseimage-v2.6.0.yaml (升级场景)
apiVersion: config.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: bke-v2.6.0
spec:
  version: v2.6.0
  upgrade:
    components:
      - name: container-runtime
        version: v1.0.0
      - name: bkeagent
        version: v2.6.0
      - name: coredns
        version: v1.11.1
      - name: openfuyao-core
        version: v26.03
```

### 2.3 升级执行流程

```
用户修改 ClusterVersion desiredVersion: v2.5.0 → v2.6.0
  │
  ▼
ClusterVersionReconciler.Reconcile()
  │
  ├─ 1. 解析目标 ReleaseImage v2.6.0
  │     releaseImage.GetUpgradeComponents()
  │     → [containerd, bkeagent, coredns, openfuyao-core]
  │
  ├─ 2. 解析当前 ReleaseImage v2.5.0
  │     currentReleaseImage.GetUpgradeComponents()
  │     → [containerd:v1.7.15, bkeagent:v2.5.0, coredns:v1.10.1, openfuyao-core:v26.01]
  │
  ├─ 3. 构建 VersionContext (版本对比)
  │     vc.SetCurrent("containerd", "v1.7.15")
  │     vc.SetTarget("containerd", "v1.7.18")
  │     vc.SetCurrent("bkeagent", "v2.5.0")
  │     vc.SetTarget("bkeagent", "v2.6.0")
  │     ... (每个组件设置 current/target)
  │
  │     VersionContext 决策结果:
  │     containerd:       HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     bkeagent:         HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     coredns:          HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     openfuyao-core:   HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     kubernetes-master: HasCurrent=true, NeedsUpgrade=false → Skip
  │
  ├─ 4. 构建升级 DAG
  │     BuildUpgradeDAG(releaseImage)
  │
  │     DAG 拓扑批次:
  │     Batch 0: [provider]                                    (manifest, 前置)
  │     Batch 1: [containerd, bkeagent]                        (binary, 并行)
  │     Batch 2: [coredns, openfuyao-core]                     (helm/yaml, 并行)
  │
  ├─ 5. Scheduler.ExecuteDAG(ctx, dag, versionContext)
  │     │
  │     ├─ Batch 0: provider (manifest executor)
  │     │   └─ ManifestApplier.ApplyComponent() → 更新 provider 自身
  │     │
  │     ├─ Batch 1: Binary 组件升级 (并行)
  │     │   ├─ containerd: BinaryComponentExecutor
  │     │   │   ├─ VersionContext.NeedsUpgrade("containerd") = true
  │     │   │   ├─ VersionContext.HasCurrent("containerd") = true → Action=Upgrade
  │     │   │   ├─ NodeProvider.GetNodes() → 3 个节点
  │     │   │   ├─ Rolling 逐节点升级:
  │     │   │   │   node1: 停止 containerd → 备份 → 安装 v1.7.18 → 启动 → ✅
  │     │   │   │   node2: 停止 containerd → 备份 → 安装 v1.7.18 → 启动 → ✅
  │     │   │   │   node3: 停止 containerd → 备份 → 安装 v1.7.18 → 启动 → ✅
  │     │   │   └─ FailurePolicy=FailFast: 任一节点失败则整体终止
  │     │   │
  │     │   └─ bkeagent: BinaryComponentExecutor
  │     │       ├─ VersionContext.NeedsUpgrade("bkeagent") = true
  │     │       ├─ VersionContext.HasCurrent("bkeagent") = true → Action=Upgrade
  │     │       ├─ NodeProvider.GetNodes() → 3 个节点
  │     │       ├─ Batch 升级 (batchSize=2):
  │     │       │   Batch 1: node1 → ✅, node2 → ✅  (检查集群健康)
  │     │       │   Batch 2: node3 → ✅
  │     │       └─ FailurePolicy=Continue: node3 失败时记录警告，继续执行
  │     │
  │     └─ Batch 2: Helm + YAML 组件升级 (并行)
  │         ├─ coredns: HelmComponentExecutor
  │         │   ├─ VersionContext.NeedsUpgrade("coredns") = true
  │         │   ├─ VersionContext.HasCurrent("coredns") = true → Action=Upgrade
  │         │   ├─ 拉取新 Chart v1.11.1
  │         │   ├─ 渲染 Values
  │         │   ├─ helm upgrade --atomic --wait
  │         │   │   ├─ 成功 → Release 更新到 v1.11.1
  │         │   │   └─ 失败 → helm 自动回滚到 v1.10.1 (atomic)
  │         │   └─ HealthCheck: PodReady (kube-dns) → ✅
  │         │
  │         └─ openfuyao-core: YamlComponentExecutor
  │             ├─ VersionContext.NeedsUpgrade("openfuyao-core") = true
  │             ├─ resolveManifests(): 下载 v26.03 清单
  │             ├─ ApplyWithStrategy(ServerSideApply): 增量更新
  │             │   └─ SSA 仅更新变更字段，保留其他管理者字段
  │             └─ PruneResources():
  │                 └─ 删除标签匹配但不在 v26.03 清单中的废弃资源 → ✅
  │
  ├─ 6. 健康检查
  │     所有组件 PodReady + EndpointReady
  │
  └─ 7. 更新 BKECluster.Status
        phase: Ready
        conditions: [{type: Upgraded, status: True}]
        versions:
          containerd: v1.7.18
          bkeagent: v2.6.0
          coredns: v1.11.1
          openfuyao-core: v26.03
```

---

## 3. 关键设计点说明

**ComponentVersion YAML 存放路径约定**：

```
bke-manifests/
├── container-runtime/v1.0.0/component.yaml  ← type: selector (容器运行时互斥选择)
├── containerd/v1.7.18/component.yaml        ← type: binary (被 selector 引用)
├── docker/v26.0.0/component.yaml            ← type: binary (被 selector 引用)
├── cri-dockerd/v0.3.9/component.yaml        ← type: binary (被 selector 引用, 依赖 docker)
├── bkeagent/v2.6.0/component.yaml           ← type: binary
├── coredns/v1.11.1/component.yaml           ← type: helm
├── openfuyao-core/v26.03/component.yaml     ← type: yaml (含 subComponents)
└── kubernetes-master/v1.29.0/               ← type: inline (无需 YAML, 由 inline handler 定义)
```

**ReleaseImage install vs upgrade components 区别**：
- `spec.install.components`：新集群安装时使用，包含所有组件（含 CommonPhases）
- `spec.upgrade.components`：升级时使用，仅包含需要升级的组件，未列出的组件保持不变

**VersionContext 在升级流程中的决策时机**：

| 决策点 | VersionContext 方法 | 判定结果 | 后续动作 |
|--------|-------------------|---------|---------|
| DAG 构建时 | `NeedsUpgrade(name)` | false | 组件不加入 DAG，跳过执行 |
| Executor 执行时 | `NeedsUpgrade(name)` | false | 组件已在目标版本，返回 nil 跳过 |
| Executor 执行时 | `HasCurrent(name)` | true | Action = Upgrade |
| Executor 执行时 | `HasCurrent(name)` | false | Action = Install |

**FailurePolicy 在不同场景下的行为**：

| 场景 | FailurePolicy | 行为 |
|------|---------------|------|
| Rolling 模式单节点失败 | FailFast | 立即返回错误，终止整个组件升级 |
| Rolling 模式单节点失败 | Continue | 记录警告日志，继续升级下一个节点 |
| Rolling 模式单节点失败 | Rollback | 对该节点执行 UninstallScript，继续下一个节点 |
| Batch 模式单批失败 | FailFast | 终止后续批次，已升级批次保留 |
| Helm `--atomic` 失败 | — | Helm SDK 自动回滚到上一个 Release |

---

**文档版本**: v1.2  
**维护者**: openFuyao Team
