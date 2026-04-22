# KEPU-2: 基于 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 的声明式集群版本管理
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-2 |
| **标题** | 声明式集群版本管理：YAML 配置驱动的安装、升级与扩缩容 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **依赖** | KEPU-1（整体架构重构） |
## 1. 摘要
本提案设计基于四个 CRD（ClusterVersion、ReleaseImage、ComponentVersion、NodeConfig）的声明式集群版本管理方案。**核心变化**：各 Phase 的安装、升级、卸载逻辑全部通过 YAML 配置声明，由通用 ActionEngine 解释执行，**不再为每个组件编写 Go 代码实现**。

**设计原则**：
- **配置即代码**：组件生命周期（安装/升级/卸载/健康检查）全部声明在 ComponentVersion YAML 中
- **通用引擎**：ActionEngine 是唯一的执行器，解释 YAML 中的 Action 定义并执行
- **零组件代码**：不编写组件特定的 Go Executor，所有行为由 YAML 驱动
- **模板化**：脚本和 manifest 支持模板变量（`{{.Version}}`、`{{.NodeIP}}` 等），运行时渲染
## 2. 动机
### 2.1 上一版方案的问题
上一版 KEPU-2 提案虽然定义了 ComponentVersion CRD，但实际执行仍依赖 Go 代码：
```
旧方案：ComponentVersion YAML → ComponentVersion Controller → 13 个 Go Executor（每个组件一个）
```
| 问题 | 说明 |
|------|------|
| **组件仍需编写 Go 代码** | 每个组件需要实现 ScriptExecutor/ManifestExecutor/ChartExecutor，本质上是换了个壳的 Phase |
| **新增组件需改代码** | 新增组件需编写新的 Executor，无法通过配置扩展 |
| **脚本与代码耦合** | shell 命令散落在 Go 代码中（如 `systemctl stop containerd`），修改需重新编译 |
| **配置与逻辑混合** | ActionSpec 定义了 Type（Script/Manifest/Chart/Controller），但 Controller 类型仍需 Go 实现 |
### 2.2 新方案的核心变化
```
新方案：ComponentVersion YAML → ComponentVersion Controller → 通用 ActionEngine（解释执行 YAML）
```

| 变化 | 旧方案 | 新方案 |
|------|--------|--------|
| 组件安装逻辑 | Go Executor 代码 | YAML 中的 Script/Manifest/Chart 声明 |
| 新增组件 | 编写 Go Executor | 编写 ComponentVersion YAML |
| 修改安装脚本 | 修改 Go 代码 → 编译 → 发布 | 修改 YAML → 应用 |
| 健康检查 | Go 代码实现 | YAML 中的 healthCheck 声明 |
| 卸载逻辑 | Go 代码实现 | YAML 中的 uninstallAction 声明 |
## 3. 目标
### 3.1 核心目标
1. 定义 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 四个 CRD
2. **全部组件行为通过 YAML 声明**：安装/升级/卸载/健康检查均为 YAML 配置
3. 实现通用 ActionEngine：解释执行 YAML 中的 Action 定义
4. 实现 ClusterVersion Controller：编排升级流程
5. 实现 ComponentVersion Controller：驱动组件生命周期
6. 实现 NodeConfig Controller：管理节点级组件
### 3.2 非目标
1. 不实现 OS 级别的升级
2. 不实现版本包的构建与发布
3. 不修改现有 BKECluster CRD 的 Spec 定义
## 4. 范围与约束
### 4.1 场景覆盖
| 场景 | 覆盖 | 说明 |
|------|------|------|
| 全新安装 | ✅ | ComponentVersion DAG + YAML Script/Manifest |
| 滚动升级 | ✅ | ClusterVersion 编排 + YAML upgradeAction |
| 单组件升级 | ✅ | 修改 ComponentVersion 版本 |
| 扩容 | ✅ | 新增 NodeConfig + YAML installAction |
| 缩容 | ✅ | NodeConfig phase=Deleting + YAML uninstallAction |
| 回滚 | ✅ | ClusterVersion History + YAML rollbackAction |
| 版本兼容性检查 | ✅ | ReleaseImage compatibility + YAML preCheck |
| 离线交付 | ✅ | ReleaseImage OCI 镜像 |
### 4.2 约束
1. **所有组件行为必须可 YAML 声明**：不允许在 ActionEngine 之外编写组件特定 Go 代码
2. **ReleaseImage 不可变**：创建后不可修改
3. **ActionEngine 是唯一执行路径**：不绕过引擎直接操作
4. **向后兼容**：Feature Gate 渐进切换
## 5. 提案
### 5.1 资源关联关系
```
BKECluster (1) ──→ ClusterVersion (1) ──→ ReleaseImage (1)
                                                │
                                    spec.componentVersions[]
                                                │
                                                ▼
                                    ComponentVersion (N)  ← YAML 声明全部行为
                                                │
                                    spec.nodeSelector 匹配
                                                │
                                                ▼
                                    NodeConfig (M)
```
### 5.2 CRD 详细设计
#### 5.2.1 ReleaseImage CRD
```go
type ReleaseImageSpec struct {
    Version           string               `json:"version"`
    ComponentVersions []ComponentVersionRef `json:"componentVersions"`
    Images            []ImageManifest       `json:"images,omitempty"`
    UpgradePaths      []UpgradePath         `json:"upgradePaths,omitempty"`
    Compatibility     CompatibilityMatrix   `json:"compatibility,omitempty"`
}

type ComponentVersionRef struct {
    Name    string         `json:"name"`
    Version string         `json:"version"`
    Ref     *ObjectReference `json:"ref,omitempty"`
}
```
#### 5.2.2 ClusterVersion CRD
```go
type ClusterVersionSpec struct {
    DesiredVersion  string           `json:"desiredVersion"`
    ReleaseRef      *ObjectReference `json:"releaseRef,omitempty"`
    ClusterRef      *ObjectReference `json:"clusterRef,omitempty"`
    UpgradeStrategy UpgradeStrategy  `json:"upgradeStrategy,omitempty"`
    Pause           bool             `json:"pause,omitempty"`
}

type ClusterVersionStatus struct {
    CurrentVersion    string              `json:"currentVersion,omitempty"`
    CurrentReleaseRef *ObjectReference    `json:"currentReleaseRef,omitempty"`
    Phase             ClusterVersionPhase `json:"phase,omitempty"`
    UpgradeSteps      []UpgradeStep       `json:"upgradeSteps,omitempty"`
    CurrentStepIndex  int                 `json:"currentStepIndex,omitempty"`
    History           []UpgradeHistory    `json:"history,omitempty"`
    Conditions        []metav1.Condition  `json:"conditions,omitempty"`
}
```
#### 5.2.3 ComponentVersion CRD（核心变化）
```go
type ComponentVersionSpec struct {
    ComponentName   ComponentName    `json:"componentName"`
    Version         string           `json:"version"`
    Scope           ComponentScope   `json:"scope,omitempty"`
    Dependencies    []ComponentName  `json:"dependencies,omitempty"`
    NodeSelector    *NodeSelector    `json:"nodeSelector,omitempty"`

    // 全部行为通过 YAML 声明
    InstallAction   *ActionSpec      `json:"installAction,omitempty"`
    UpgradeAction   *ActionSpec      `json:"upgradeAction,omitempty"`
    UninstallAction *ActionSpec      `json:"uninstallAction,omitempty"`
    RollbackAction  *ActionSpec      `json:"rollbackAction,omitempty"`
    HealthCheck     *HealthCheckSpec `json:"healthCheck,omitempty"`

    // 组件来源
    Source          *ComponentSource `json:"source,omitempty"`
}

type ActionSpec struct {
    // 执行步骤序列（按顺序执行）
    Steps []ActionStep `json:"steps,omitempty"`

    // 前置检查
    PreCheck  *ActionStep `json:"preCheck,omitempty"`
    // 后置检查
    PostCheck *ActionStep `json:"postCheck,omitempty"`

    // 超时
    Timeout *metav1.Duration `json:"timeout,omitempty"`

    // 执行策略
    Strategy ActionStrategy `json:"strategy,omitempty"`
}

type ActionStep struct {
    Name        string            `json:"name"`
    Type        ActionType        `json:"type"`

    // Script 类型：shell 脚本内容（支持模板变量）
    Script      string            `json:"script,omitempty"`

    // Manifest 类型：Kubernetes YAML 清单（支持模板变量）
    Manifest    string            `json:"manifest,omitempty"`

    // Chart 类型：Helm Chart 配置
    Chart       *ChartAction      `json:"chart,omitempty"`

    // Kubectl 类型：kubectl 操作
    Kubectl     *KubectlAction    `json:"kubectl,omitempty"`

    // 条件判断：仅当条件满足时执行
    Condition   string            `json:"condition,omitempty"`

    // 失败处理策略
    OnFailure   FailurePolicy     `json:"onFailure,omitempty"`

    // 重试次数
    Retries     int               `json:"retries,omitempty"`

    // 节点选择（覆盖 ComponentVersion 级别的 nodeSelector）
    NodeSelector *NodeSelector    `json:"nodeSelector,omitempty"`
}

type ActionType string

const (
    ActionScript     ActionType = "Script"
    ActionManifest   ActionType = "Manifest"
    ActionChart      ActionType = "Chart"
    ActionKubectl    ActionType = "Kubectl"
)

type ActionStrategy struct {
    // 节点级执行策略
    ExecutionMode ExecutionMode `json:"executionMode,omitempty"`
    // 滚动升级批次大小
    BatchSize     int           `json:"batchSize,omitempty"`
    // 批次间隔
    BatchInterval *metav1.Duration `json:"batchInterval,omitempty"`
}

type ExecutionMode string

const (
    // 所有节点并行执行
    ExecutionParallel ExecutionMode = "Parallel"
    // 逐节点串行执行
    ExecutionSerial   ExecutionMode = "Serial"
    // 按批次滚动执行
    ExecutionRolling  ExecutionMode = "Rolling"
)

type FailurePolicy string

const (
    FailFast    FailurePolicy = "FailFast"
    Continue    FailurePolicy = "Continue"
    Retry       FailurePolicy = "Retry"
)

type KubectlAction struct {
    Operation  KubectlOperation `json:"operation"`
    Resource   string           `json:"resource,omitempty"`
    Namespace  string           `json:"namespace,omitempty"`
    Manifest   string           `json:"manifest,omitempty"`
    FieldPatch string           `json:"fieldPatch,omitempty"`
}

type KubectlOperation string

const (
    KubectlApply   KubectlOperation = "Apply"
    KubectlDelete  KubectlOperation = "Delete"
    KubectlPatch   KubectlOperation = "Patch"
    KubectlWait    KubectlOperation = "Wait"
    KubectlDrain   KubectlOperation = "Drain"
)

type ChartAction struct {
    RepoURL    string            `json:"repoURL"`
    ChartName  string            `json:"chartName"`
    Version    string            `json:"version"`
    ReleaseName string          `json:"releaseName"`
    Namespace  string            `json:"namespace"`
    Values     string            `json:"values,omitempty"`
}

type HealthCheckSpec struct {
    Steps []HealthCheckStep `json:"steps,omitempty"`
}

type HealthCheckStep struct {
    Name           string            `json:"name"`
    Type           ActionType        `json:"type"`
    Script         string            `json:"script,omitempty"`
    Kubectl        *KubectlAction    `json:"kubectl,omitempty"`
    ExpectedOutput string            `json:"expectedOutput,omitempty"`
    Timeout        *metav1.Duration  `json:"timeout,omitempty"`
    Interval       *metav1.Duration  `json:"interval,omitempty"`
}
```
### 5.3 模板变量系统
ActionSpec 中的 Script、Manifest、Chart.Values 支持模板变量，运行时由 ActionEngine 渲染：
```go
// 模板变量来源
type TemplateContext struct {
    // 来自 ComponentVersion
    ComponentName string
    Version       string

    // 来自 NodeConfig
    NodeIP        string
    NodeHostname  string
    NodeRoles     []string
    NodeOS        NodeOSInfo

    // 来自 ClusterVersion
    ClusterName      string
    ClusterNamespace string
    CurrentVersion   string

    // 来自 BKECluster Spec
    EtcdVersion        string
    KubernetesVersion  string
    ContainerdVersion  string
    OpenFuyaoVersion   string
    ImageRepo          string
    HTTPRepo           string
    CertificatesDir    string
    ControlPlaneEndpoint string

    // 来自 NodeConfig.Spec.Components
    ContainerdConfig  *ContainerdComponentConfig
    KubeletConfig     *KubeletComponentConfig
    EtcdConfig        *EtcdComponentConfig
    BKEAgentConfig    *BKEAgentComponentConfig
}
```
**模板语法**：采用 Go 标准 `text/template`
```
{{.Version}}           → v1.7.2
{{.NodeIP}}            → 192.168.1.10
{{.ImageRepo}}         → repo.openfuyao.cn
{{.EtcdDataDir}}       → /var/lib/etcd
{{.ControlPlaneEndpoint}} → 192.168.1.100:6443
```
### 5.4 各 Phase 的 YAML 声明
#### 5.4.1 containerd ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.2
  namespace: cluster-system
spec:
  componentName: containerd
  version: v1.7.2
  scope: Node
  dependencies: [nodesEnv]
  nodeSelector:
    roles: [master, worker]

  installAction:
    steps:
      - name: install-containerd
        type: Script
        script: |
          #!/bin/bash
          set -e
          mkdir -p /etc/containerd
          tar -xzf /tmp/containerd-{{.Version}}-linux-amd64.tar.gz -C /usr/bin
          chmod +x /usr/bin/containerd /usr/bin/containerd-shim-runc-v2 /usr/bin/ctr /usr/bin/crictl
          cp /tmp/containerd.service /usr/lib/systemd/system/containerd.service
          systemctl daemon-reload
          systemctl enable containerd
          systemctl restart containerd
    postCheck:
      name: verify-containerd
      type: Script
      script: "systemctl is-active containerd"
      expectedOutput: "active"

  upgradeAction:
    preCheck:
      name: check-agent-ready
      type: Script
      script: "systemctl is-active bke-agent"
      expectedOutput: "active"
    steps:
      - name: stop-containerd
        type: Script
        script: |
          systemctl stop containerd
          systemctl disable containerd
      - name: remove-old-binaries
        type: Script
        script: |
          rm -f /usr/bin/containerd /usr/bin/containerd-stress
          rm -f /usr/bin/containerd-shim-shimless-v2 /usr/bin/containerd-shim-runc-v2
          rm -f /usr/bin/crictl /etc/crictl.yaml /usr/bin/ctr /usr/bin/nerdctl
          rm -f /usr/lib/systemd/system/containerd.service /usr/local/sbin/runc
          rm -rf /usr/local/beyondvm /etc/containerd/
      - name: install-new-containerd
        type: Script
        script: |
          #!/bin/bash
          set -e
          mkdir -p /etc/containerd
          tar -xzf /tmp/containerd-{{.Version}}-linux-amd64.tar.gz -C /usr/bin
          chmod +x /usr/bin/containerd /usr/bin/containerd-shim-runc-v2 /usr/bin/ctr /usr/bin/crictl
          cp /tmp/containerd.service /usr/lib/systemd/system/containerd.service
          systemctl daemon-reload
          systemctl enable containerd
          systemctl restart containerd
    postCheck:
      name: verify-containerd
      type: Script
      script: "systemctl is-active containerd"
      expectedOutput: "active"
    strategy:
      executionMode: Rolling
      batchSize: 1
      batchInterval: 30s

  uninstallAction:
    steps:
      - name: stop-containerd
        type: Script
        script: |
          systemctl stop containerd
          systemctl disable containerd
      - name: remove-binaries-and-config
        type: Script
        script: |
          rm -f /usr/bin/containerd /usr/bin/containerd-stress
          rm -f /usr/bin/containerd-shim-shimless-v2 /usr/bin/containerd-shim-runc-v2
          rm -f /usr/bin/crictl /etc/crictl.yaml /usr/bin/ctr /usr/bin/nerdctl
          rm -f /usr/lib/systemd/system/containerd.service /usr/local/sbin/runc
          rm -rf /usr/local/beyondvm /etc/containerd/

  healthCheck:
    steps:
      - name: containerd-active
        type: Script
        script: "systemctl is-active containerd"
        expectedOutput: "active"
        timeout: 30s
        interval: 5s

  source:
    type: HTTP
    url: "https://repo.openfuyao.cn/containerd/{{.Version}}/containerd-{{.Version}}-linux-amd64.tar.gz"
    checksum: "sha256:xxxxx"
    scripts:
      - name: containerd.service
        content: |
          [Unit]
          Description=containerd container runtime
          [Service]
          ExecStart=/usr/bin/containerd
          Restart=always
          [Install]
          WantedBy=multi-user.target
```
#### 5.4.2 etcd ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
  namespace: cluster-system
spec:
  componentName: etcd
  version: v3.5.12
  scope: Node
  dependencies: [nodesEnv]
  nodeSelector:
    roles: [master]

  installAction:
    steps:
      - name: setup-etcd-environment
        type: Script
        script: |
          #!/bin/bash
          set -e
          mkdir -p -m 700 {{.EtcdDataDir}}
          id etcd || useradd -r -c "etcd user" -s /sbin/nologin etcd -d {{.EtcdDataDir}}
          chown -R etcd:etcd {{.EtcdDataDir}}
      - name: generate-etcd-manifest
        type: Manifest
        manifest: |
          apiVersion: v1
          kind: Pod
          metadata:
            name: etcd-{{.NodeHostname}}
            namespace: kube-system
          spec:
            containers:
            - name: etcd
              image: {{.ImageRepo}}/etcd:{{.Version}}
              command:
              - /bin/sh
              - -c
              - |
                etcd --advertise-client-urls={{.EtcdClientURLs}} \
                  --data-dir={{.EtcdDataDir}} \
                  --listen-client-urls=https://127.0.0.1:2379,https://{{.NodeIP}}:2379 \
                  --listen-peer-urls=https://{{.NodeIP}}:2380 \
                  --name={{.NodeHostname}} \
                  ...
        nodeSelector:
          roles: [master]
      - name: restart-kubelet
        type: Script
        script: |
          if [ -f /etc/systemd/system/kubelet.service ]; then
            systemctl restart kubelet
          fi
    postCheck:
      name: etcd-health
      type: Script
      script: |
        ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
          --cacert=/etc/kubernetes/pki/etcd/ca.crt \
          --cert=/etc/kubernetes/pki/etcd/server.crt \
          --key=/etc/kubernetes/pki/etcd/server.key \
          endpoint health
      expectedOutput: "is healthy"
      timeout: 300s
      interval: 10s

  upgradeAction:
    preCheck:
      name: check-etcd-members
      type: Script
      script: |
        ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
          --cacert=/etc/kubernetes/pki/etcd/ca.crt \
          --cert=/etc/kubernetes/pki/etcd/peer.crt \
          --key=/etc/kubernetes/pki/etcd/peer.key \
          member list
      expectedOutput: "started"
    steps:
      - name: backup-etcd
        type: Script
        condition: "{{.NeedBackup}} == true"
        script: |
          mkdir -p /var/lib/bke/workspace/etcd-backup
          ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
            --cacert=/etc/kubernetes/pki/etcd/ca.crt \
            --cert=/etc/kubernetes/pki/etcd/server.crt \
            --key=/etc/kubernetes/pki/etcd/server.key \
            snapshot save /var/lib/bke/workspace/etcd-backup/backup-$(date +%Y%m%d%H%M).db
      - name: backup-kubernetes-config
        type: Script
        script: |
          mkdir -p /var/lib/bke/workspace/backup/etc/kubernetes
          cp -rf /etc/kubernetes/* /var/lib/bke/workspace/backup/etc/kubernetes/
      - name: update-etcd-manifest
        type: Manifest
        manifest: |
          # 同 installAction 中的 manifest，镜像 tag 更新为 {{.Version}}
          ...
      - name: restart-kubelet
        type: Script
        script: |
          if [ -f /etc/systemd/system/kubelet.service ]; then
            systemctl restart kubelet
          fi
    postCheck:
      name: verify-etcd-version
      type: Script
      script: |
        ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
          --cacert=/etc/kubernetes/pki/etcd/ca.crt \
          --cert=/etc/kubernetes/pki/etcd/server.crt \
          --key=/etc/kubernetes/pki/etcd/server.key \
          endpoint health
      expectedOutput: "is healthy"
      timeout: 300s
      interval: 10s
    strategy:
      executionMode: Serial
      batchSize: 1

  uninstallAction:
    steps:
      - name: remove-etcd-manifest
        type: Script
        script: |
          rm -f /etc/kubernetes/manifests/etcd.yaml
      - name: clean-etcd-data
        type: Script
        script: |
          rm -rf {{.EtcdDataDir}}
          rm -rf /etc/kubernetes

  healthCheck:
    steps:
      - name: etcd-endpoint-health
        type: Script
        script: |
          ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
            --cacert=/etc/kubernetes/pki/etcd/ca.crt \
            --cert=/etc/kubernetes/pki/etcd/server.crt \
            --key=/etc/kubernetes/pki/etcd/server.key \
            endpoint health
        expectedOutput: "is healthy"
        timeout: 60s
        interval: 10s
```
#### 5.4.3 kubernetes ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-v1.29.0
  namespace: cluster-system
spec:
  componentName: kubernetes
  version: v1.29.0
  scope: Node
  dependencies: [containerd, etcd, loadBalancer]

  installAction:
    steps:
      - name: init-control-plane
        type: Script
        condition: "{{.NodeRole}} == master && {{.IsFirstMaster}} == true"
        script: |
          #!/bin/bash
          set -e
          kubeadm init --config /tmp/kubeadm-init.yaml --upload-certs
          mkdir -p $HOME/.kube
          cp -f /etc/kubernetes/admin.conf $HOME/.kube/config
      - name: join-control-plane
        type: Script
        condition: "{{.NodeRole}} == master && {{.IsFirstMaster}} == false"
        script: |
          #!/bin/bash
          set -e
          kubeadm join --config /tmp/kubeadm-join-control-plane.yaml
      - name: join-worker
        type: Script
        condition: "{{.NodeRole}} == worker"
        script: |
          #!/bin/bash
          set -e
          kubeadm join --config /tmp/kubeadm-join.yaml
    postCheck:
      name: verify-kubelet
      type: Script
      script: "systemctl is-active kubelet"
      expectedOutput: "active"
      timeout: 120s

  upgradeAction:
    preCheck:
      name: check-cluster-health
      type: Kubectl
      kubectl:
        operation: Wait
        resource: nodes
        condition: "Ready"
        timeout: 120s
    steps:
      - name: upgrade-master
        type: Script
        condition: "{{.NodeRole}} == master"
        script: |
          #!/bin/bash
          set -e
          kubeadm upgrade apply {{.Version}} -y
          systemctl restart kubelet
      - name: upgrade-worker
        type: Script
        condition: "{{.NodeRole}} == worker"
        script: |
          #!/bin/bash
          set -e
          kubeadm upgrade node
          systemctl restart kubelet
    postCheck:
      name: verify-node-ready
      type: Kubectl
      kubectl:
        operation: Wait
        resource: "node/{{.NodeHostname}}"
        condition: "Ready"
        timeout: 180s
    strategy:
      executionMode: Rolling
      batchSize: 1
      batchInterval: 60s

  uninstallAction:
    steps:
      - name: kubeadm-reset
        type: Script
        script: |
          kubeadm reset -f
          rm -rf /etc/kubernetes /var/lib/kubelet /var/lib/etcd
          systemctl restart kubelet

  healthCheck:
    steps:
      - name: kubelet-active
        type: Script
        script: "systemctl is-active kubelet"
        expectedOutput: "active"
      - name: node-ready
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "node/{{.NodeHostname}}"
          condition: "Ready"
          timeout: 60s
```
#### 5.4.4 bkeAgent ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v1.0.0
  namespace: cluster-system
spec:
  componentName: bkeAgent
  version: v1.0.0
  scope: Node
  dependencies: []

  installAction:
    steps:
      - name: push-agent-binary
        type: Script
        script: |
          #!/bin/bash
          set -e
          mkdir -p /usr/local/bin /etc/bke-agent
          cp /tmp/bke-agent /usr/local/bin/bke-agent
          chmod +x /usr/local/bin/bke-agent
          cp /tmp/bke-agent-kubeconfig /etc/bke-agent/kubeconfig
          cp /tmp/bke-agent.service /usr/lib/systemd/system/bke-agent.service
          systemctl daemon-reload
          systemctl enable bke-agent
          systemctl restart bke-agent
    postCheck:
      name: verify-agent
      type: Script
      script: |
        for i in $(seq 1 30); do
          curl -sk https://127.0.0.1:{{.AgentPort}}/healthz && exit 0
          sleep 2
        done
        exit 1
      timeout: 90s

  upgradeAction:
    steps:
      - name: update-agent-binary
        type: Script
        script: |
          #!/bin/bash
          set -e
          systemctl stop bke-agent
          cp /tmp/bke-agent /usr/local/bin/bke-agent
          chmod +x /usr/local/bin/bke-agent
          systemctl start bke-agent
    postCheck:
      name: verify-agent
      type: Script
      script: "curl -sk https://127.0.0.1:{{.AgentPort}}/healthz"
      expectedOutput: "ok"
      timeout: 60s
    strategy:
      executionMode: Rolling
      batchSize: 1

  uninstallAction:
    steps:
      - name: remove-agent
        type: Script
        script: |
          systemctl stop bke-agent
          systemctl disable bke-agent
          rm -f /usr/local/bin/bke-agent
          rm -f /usr/lib/systemd/system/bke-agent.service
          rm -rf /etc/bke-agent
```
#### 5.4.5 addon ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: addon-v1.2.0
  namespace: cluster-system
spec:
  componentName: addon
  version: v1.2.0
  scope: Cluster
  dependencies: [kubernetes]

  installAction:
    steps:
      - name: install-calico
        type: Chart
        chart:
          repoURL: "https://repo.openfuyao.cn/charts"
          chartName: calico
          version: "{{.CalicoChartVersion}}"
          releaseName: calico
          namespace: kube-system
          values: |
            imageRepository: {{.ImageRepo}}
            ...
      - name: install-coredns
        type: Chart
        chart:
          repoURL: "https://repo.openfuyao.cn/charts"
          chartName: coredns
          version: "{{.CoreDNSChartVersion}}"
          releaseName: coredns
          namespace: kube-system
          values: |
            image: {{.ImageRepo}}/coredns:{{.CoreDNSVersion}}
            ...
      - name: install-kube-proxy
        type: Manifest
        manifest: |
          apiVersion: apps/v1
          kind: DaemonSet
          metadata:
            name: kube-proxy
            namespace: kube-system
          spec:
            template:
              spec:
                containers:
                - name: kube-proxy
                  image: {{.ImageRepo}}/kube-proxy:{{.KubernetesVersion}}
                  ...

  upgradeAction:
    steps:
      - name: upgrade-calico
        type: Chart
        chart:
          repoURL: "https://repo.openfuyao.cn/charts"
          chartName: calico
          version: "{{.CalicoChartVersion}}"
          releaseName: calico
          namespace: kube-system
          values: |
            imageRepository: {{.ImageRepo}}
            ...
      - name: upgrade-coredns
        type: Chart
        chart:
          repoURL: "https://repo.openfuyao.cn/charts"
          chartName: coredns
          version: "{{.CoreDNSChartVersion}}"
          releaseName: coredns
          namespace: kube-system

  uninstallAction:
    steps:
      - name: uninstall-calico
        type: Chart
        chart:
          releaseName: calico
          namespace: kube-system
      - name: uninstall-coredns
        type: Chart
        chart:
          releaseName: coredns
          namespace: kube-system
```
#### 5.4.6 certs ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: certs-v1.0.0
  namespace: cluster-system
spec:
  componentName: certs
  version: v1.0.0
  scope: Cluster
  dependencies: [clusterAPI]

  installAction:
    steps:
      - name: generate-ca-certs
        type: Script
        script: |
          #!/bin/bash
          set -e
          mkdir -p {{.CertificatesDir}}
          openssl genrsa -out {{.CertificatesDir}}/ca.key 2048
          openssl req -x509 -new -nodes -key {{.CertificatesDir}}/ca.key \
            -subj "/CN=kubernetes" -days 3650 \
            -out {{.CertificatesDir}}/ca.crt
          openssl genrsa -out {{.CertificatesDir}}/etcd/ca.key 2048
          openssl req -x509 -new -nodes -key {{.CertificatesDir}}/etcd/ca.key \
            -subj "/CN=etcd-ca" -days 3650 \
            -out {{.CertificatesDir}}/etcd/ca.crt
          openssl genrsa -out {{.CertificatesDir}}/front-proxy-ca.key 2048
          openssl req -x509 -new -nodes -key {{.CertificatesDir}}/front-proxy-ca.key \
            -subj "/CN=front-proxy-ca" -days 3650 \
            -out {{.CertificatesDir}}/front-proxy-ca.crt
          openssl genrsa -out {{.CertificatesDir}}/sa.key 2048
          openssl rsa -in {{.CertificatesDir}}/sa.key -pubout -out {{.CertificatesDir}}/sa.pub
      - name: upload-certs-as-secret
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: v1
            kind: Secret
            metadata:
              name: {{.ClusterName}}-certs
              namespace: {{.ClusterNamespace}}
            type: Opaque
            data:
              ca.crt: <base64>
              ca.key: <base64>
              ...

  upgradeAction:
    steps:
      - name: renew-certs
        type: Script
        script: |
          kubeadm certs renew all
```
#### 5.4.7 loadBalancer ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: loadbalancer-v1.0.0
  namespace: cluster-system
spec:
  componentName: loadBalancer
  version: v1.0.0
  scope: Node
  dependencies: [certs]
  nodeSelector:
    roles: [master]

  installAction:
    steps:
      - name: deploy-keepalived-haproxy
        type: Manifest
        manifest: |
          apiVersion: v1
          kind: Pod
          metadata:
            name: keepalived-haproxy
            namespace: kube-system
          spec:
            containers:
            - name: haproxy
              image: {{.ImageRepo}}/haproxy:2.8
              volumeMounts:
              - name: haproxy-config
                mountPath: /usr/local/etc/haproxy
            - name: keepalived
              image: {{.ImageRepo}}/keepalived:2.2
              securityContext:
                privileged: true
            volumes:
            - name: haproxy-config
              configMap:
                name: haproxy-config
      - name: create-haproxy-config
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: v1
            kind: ConfigMap
            metadata:
              name: haproxy-config
              namespace: kube-system
            data:
              haproxy.cfg: |
                global
                  log 127.0.0.1 local0
                frontend k8s-api
                  bind *:{{.ControlPlaneEndpointPort}}
                  default_backend k8s-api
                backend k8s-api
                  balance roundrobin
                  {{range .MasterNodes}}
                  server {{.Hostname}} {{.IP}}:6443 check
                  {{end}}

  upgradeAction:
    steps:
      - name: update-haproxy-config
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            # 同 installAction 中的 ConfigMap
```
#### 5.4.8 openFuyao ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: openfuyao-v2.6.0
  namespace: cluster-system
spec:
  componentName: openFuyao
  version: v2.6.0
  scope: Cluster
  dependencies: [kubernetes]

  installAction:
    steps:
      - name: deploy-openfuyao-controller
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: openfuyao-controller-manager
              namespace: openfuyao-system
            spec:
              template:
                spec:
                  containers:
                  - name: manager
                    image: {{.ImageRepo}}/openfuyao-controller:{{.Version}}
      - name: deploy-openfuyao-apiservice
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: apiregistration.k8s.io/v1
            kind: APIService
            ...

  upgradeAction:
    steps:
      - name: patch-controller-image
        type: Kubectl
        kubectl:
          operation: Patch
          resource: "deployment/openfuyao-controller-manager"
          namespace: openfuyao-system
          fieldPatch: |
            {"spec":{"template":{"spec":{"containers":[{"name":"manager","image":"{{.ImageRepo}}/openfuyao-controller:{{.Version}}"}]}}}}
    postCheck:
      name: verify-deployment-ready
      type: Kubectl
      kubectl:
        operation: Wait
        resource: "deployment/openfuyao-controller-manager"
        namespace: openfuyao-system
        condition: "Available"
        timeout: 180s

  rollbackAction:
    steps:
      - name: rollback-controller-image
        type: Kubectl
        kubectl:
          operation: Patch
          resource: "deployment/openfuyao-controller-manager"
          namespace: openfuyao-system
          fieldPatch: |
            {"spec":{"template":{"spec":{"containers":[{"name":"manager","image":"{{.ImageRepo}}/openfuyao-controller:{{.PreviousVersion}}"}]}}}}
```
#### 5.4.9 bkeProvider ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeprovider-v1.1.0
  namespace: cluster-system
spec:
  componentName: bkeProvider
  version: v1.1.0
  scope: Cluster
  dependencies: []

  installAction:
    steps:
      - name: deploy-provider
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: bke-controller-manager
              namespace: cluster-system
            spec:
              template:
                spec:
                  containers:
                  - name: manager
                    image: {{.ImageRepo}}/cluster-api-provider-bke:{{.Version}}

  upgradeAction:
    steps:
      - name: patch-provider-image
        type: Kubectl
        kubectl:
          operation: Patch
          resource: "deployment/bke-controller-manager"
          namespace: cluster-system
          fieldPatch: |
            {"spec":{"template":{"spec":{"containers":[{"name":"manager","image":"{{.ImageRepo}}/cluster-api-provider-bke:{{.Version}}"}]}}}}
    postCheck:
      name: verify-provider-ready
      type: Kubectl
      kubectl:
        operation: Wait
        resource: "deployment/bke-controller-manager"
        namespace: cluster-system
        condition: "Available"
        timeout: 300s
```
#### 5.4.10 clusterAPI ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: clusterapi-v1.0.0
  namespace: cluster-system
spec:
  componentName: clusterAPI
  version: v1.0.0
  scope: Cluster
  dependencies: [bkeAgent]

  installAction:
    steps:
      - name: create-cluster-object
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: Cluster
            metadata:
              name: {{.ClusterName}}
              namespace: {{.ClusterNamespace}}
            spec:
              infrastructureRef:
                apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
                kind: BKECluster
                name: {{.ClusterName}}
              controlPlaneRef:
                apiVersion: controlplane.cluster.x-k8s.io/v1beta1
                kind: KubeadmControlPlane
                name: {{.ClusterName}}
      - name: create-machine-deployment
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: MachineDeployment
            ...

  upgradeAction:
    steps:
      - name: update-machine-replicas
        type: Kubectl
        kubectl:
          operation: Patch
          resource: "machinedeployment"
          namespace: "{{.ClusterNamespace}}"
          fieldPatch: |
            {"spec":{"replicas":{{.WorkerReplicas}}}}
```
#### 5.4.11 nodesEnv ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: nodesenv-v1.0.0
  namespace: cluster-system
spec:
  componentName: nodesEnv
  version: v1.0.0
  scope: Node
  dependencies: [bkeAgent]
  nodeSelector:
    roles: [master, worker]

  installAction:
    steps:
      - name: install-lxcfs
        type: Script
        script: "{{.HTTPRepo}}/scripts/install-lxcfs.sh"
      - name: install-nfsutils
        type: Script
        script: "{{.HTTPRepo}}/scripts/install-nfsutils.sh"
      - name: install-etcdctl
        type: Script
        script: "{{.HTTPRepo}}/scripts/install-etcdctl.sh"
      - name: install-helm
        type: Script
        script: "{{.HTTPRepo}}/scripts/install-helm.sh"
      - name: install-calicoctl
        type: Script
        script: "{{.HTTPRepo}}/scripts/install-calicoctl.sh"
      - name: update-runc
        type: Script
        script: "{{.HTTPRepo}}/scripts/update-runc.sh"
    strategy:
      executionMode: Parallel

  upgradeAction:
    steps:
      - name: update-tools
        type: Script
        script: "{{.HTTPRepo}}/scripts/update-tools.sh"
```
#### 5.4.12 nodesPostProcess ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: nodespostprocess-v1.0.0
  namespace: cluster-system
spec:
  componentName: nodesPostProcess
  version: v1.0.0
  scope: Node
  dependencies: [addon]
  nodeSelector:
    roles: [master, worker]

  installAction:
    steps:
      - name: run-post-process-scripts
        type: Script
        script: |
          #!/bin/bash
          for script in /tmp/post-process/*.sh; do
            [ -f "$script" ] && bash "$script"
          done
    strategy:
      executionMode: Parallel

  upgradeAction:
    steps:
      - name: rerun-post-process-scripts
        type: Script
        script: |
          #!/bin/bash
          for script in /tmp/post-process/*.sh; do
            [ -f "$script" ] && bash "$script"
          done
```
#### 5.4.13 agentSwitch ComponentVersion YAML
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: agentswitch-v1.0.0
  namespace: cluster-system
spec:
  componentName: agentSwitch
  version: v1.0.0
  scope: Cluster
  dependencies: [nodesPostProcess]

  installAction:
    steps:
      - name: switch-agent-kubeconfig
        type: Kubectl
        kubectl:
          operation: Patch
          resource: "bkecluster/{{.ClusterName}}"
          namespace: "{{.ClusterNamespace}}"
          fieldPatch: |
            {"metadata":{"annotations":{"bke-agent-listener":"current"}}}
      - name: update-agent-config
        type: Script
        script: |
          #!/bin/bash
          set -e
          # 更新 Agent kubeconfig 指向目标集群
          cp /tmp/target-kubeconfig /etc/bke-agent/kubeconfig
          systemctl restart bke-agent
```
### 5.5 通用 ActionEngine 设计
```go
// pkg/actionengine/engine.go

type ActionEngine struct {
    client     client.Client
    templateCtx *TemplateContext
    executor   *StepExecutor
}

func (e *ActionEngine) ExecuteAction(
    ctx context.Context,
    action *ActionSpec,
    nodeConfigs []*v1alpha1.NodeConfig,
) error {
    if action.PreCheck != nil {
        if err := e.executeStep(ctx, action.PreCheck, nil); err != nil {
            return fmt.Errorf("preCheck failed: %w", err)
        }
    }

    for _, step := range action.Steps {
        if step.Condition != "" && !e.evaluateCondition(step.Condition) {
            continue
        }

        switch action.Strategy.ExecutionMode {
        case ExecutionParallel:
            e.executeParallel(ctx, step, nodeConfigs)
        case ExecutionSerial:
            e.executeSerial(ctx, step, nodeConfigs)
        case ExecutionRolling:
            e.executeRolling(ctx, step, nodeConfigs, action.Strategy)
        }
    }

    if action.PostCheck != nil {
        if err := e.executeStepWithRetry(ctx, action.PostCheck, nil); err != nil {
            return fmt.Errorf("postCheck failed: %w", err)
        }
    }
    return nil
}

func (e *ActionEngine) executeStep(
    ctx context.Context,
    step *ActionStep,
    nodeConfig *v1alpha1.NodeConfig,
) error {
    rendered, err := e.renderTemplate(step)
    if err != nil {
        return err
    }

    switch step.Type {
    case ActionScript:
        return e.executor.ExecuteScript(ctx, rendered.Script, nodeConfig)
    case ActionManifest:
        return e.executor.ApplyManifest(ctx, rendered.Manifest)
    case ActionChart:
        return e.executor.UpgradeChart(ctx, rendered.Chart)
    case ActionKubectl:
        return e.executor.ExecuteKubectl(ctx, rendered.Kubectl)
    }
    return nil
}
```
### 5.6 ComponentVersion Controller 设计
```go
func (r *ComponentVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &nc.ComponentVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if cv.Status.InstalledVersion == cv.Spec.Version && cv.Status.Phase == CompPhaseReady {
        return ctrl.Result{}, nil
    }

    engine := r.actionEngine.ForComponent(cv)

    switch cv.Status.Phase {
    case "", CompPhasePending:
        // 查找旧版本 → 先卸载
        oldCV := r.findOldComponentVersion(ctx, cv)
        if oldCV != nil && oldCV.Spec.UninstallAction != nil {
            cv.Status.Phase = CompPhaseUninstalling
            _ = r.Status().Update(ctx, cv)
            if err := engine.ExecuteAction(ctx, oldCV.Spec.UninstallAction, nodeConfigs); err != nil {
                cv.Status.Phase = CompPhaseFailed
                _ = r.Status().Update(ctx, cv)
                return ctrl.Result{}, err
            }
        }
        // 执行安装
        cv.Status.Phase = CompPhaseInstalling
        _ = r.Status().Update(ctx, cv)
        if err := engine.ExecuteAction(ctx, cv.Spec.InstallAction, nodeConfigs); err != nil {
            cv.Status.Phase = CompPhaseFailed
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, err
        }
        cv.Status.InstalledVersion = cv.Spec.Version
        cv.Status.Phase = CompPhaseReady
        return ctrl.Result{}, r.Status().Update(ctx, cv)

    case CompPhaseReady:
        // 版本变更 → 升级
        if cv.Status.InstalledVersion != cv.Spec.Version {
            cv.Status.Phase = CompPhaseUpgrading
            _ = r.Status().Update(ctx, cv)
            if err := engine.ExecuteAction(ctx, cv.Spec.UpgradeAction, nodeConfigs); err != nil {
                if cv.Spec.RollbackAction != nil {
                    cv.Status.Phase = CompPhaseRollingBack
                    _ = r.Status().Update(ctx, cv)
                    _ = engine.ExecuteAction(ctx, cv.Spec.RollbackAction, nodeConfigs)
                }
                cv.Status.Phase = CompPhaseFailed
                _ = r.Status().Update(ctx, cv)
                return ctrl.Result{}, err
            }
            cv.Status.InstalledVersion = cv.Spec.Version
            cv.Status.Phase = CompPhaseReady
            return ctrl.Result{}, r.Status().Update(ctx, cv)
        }
    }
    return ctrl.Result{}, nil
}
```
### 5.7 升级时卸载旧组件的流程
```
用户修改 ClusterVersion.spec.desiredVersion = "v2.6.0"
    │
    ├── ClusterVersion Controller
    │   ├── 查找新 ReleaseImage → 解析新 ComponentVersion 列表
    │   └── 按升级 DAG 逐步更新 ComponentVersion CR 的 spec.version
    │
    ├── ComponentVersion Controller（逐组件触发）
    │   ├── 检测 spec.version != status.installedVersion
    │   ├── 查找旧版本：
    │   │   └── ClusterVersion.status.currentReleaseRef
    │   │       → 旧 ReleaseImage
    │   │         → spec.componentVersions[name=当前组件]
    │   │           → 旧 ComponentVersion CR → spec.uninstallAction
    │   ├── 执行旧版本 uninstallAction（YAML 声明）
    │   ├── 执行新版本 upgradeAction（YAML 声明）
    │   └── 健康检查（YAML 声明）
    │
    └── 全部组件完成 → ClusterVersion 更新 currentVersion
```
## 6. 迁移策略
| 阶段 | Feature Gate | 行为 |
|------|-------------|------|
| Phase 1 | `DeclarativeOrchestration=false` | CRD + YAML 可创建，ActionEngine 可启动，不影响 PhaseFlow |
| Phase 2 | `DeclarativeOrchestration=true`（可选） | ActionEngine 执行 YAML，对比验证 |
| Phase 3 | `DeclarativeOrchestration=true`（默认） | 全量切换 |
| Phase 4 | 不可逆 | 移除旧 Phase 代码 |
## 7. 目录结构
```
cluster-api-provider-bke/
├── api/
│   ├── cvo/v1beta1/
│   │   ├── clusterversion_types.go
│   │   ├── releaseimage_types.go
│   │   └── zz_generated.deepcopy.go
│   └── nodecomponent/v1alpha1/
│       ├── componentversion_types.go
│       ├── nodeconfig_types.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   ├── cvo/
│   │   ├── clusterversion_controller.go
│   │   └── releaseimage_controller.go
│   └── nodecomponent/
│       ├── componentversion_controller.go
│       └── nodeconfig_controller.go
├── pkg/
│   ├── actionengine/                    # 通用执行引擎
│   │   ├── engine.go                    # 主引擎
│   │   ├── template.go                  # 模板渲染
│   │   ├── condition.go                 # 条件评估
│   │   └── executor/
│   │       ├── script_executor.go       # Script 执行（通过 Agent Command）
│   │       ├── manifest_executor.go     # Manifest 执行（kubectl apply）
│   │       ├── chart_executor.go        # Chart 执行（helm upgrade）
│   │       └── kubectl_executor.go      # Kubectl 操作
│   ├── cvo/
│   │   ├── orchestrator.go
│   │   ├── validator.go
│   │   ├── rollback.go
│   │   └── dag_scheduler.go
│   └── phaseframe/                      # 保留，逐步废弃
├── config/
│   └── components/                      # 组件 YAML 声明
│       ├── containerd-v1.7.2.yaml
│       ├── etcd-v3.5.12.yaml
│       ├── kubernetes-v1.29.0.yaml
│       ├── bkeagent-v1.0.0.yaml
│       ├── addon-v1.2.0.yaml
│       ├── certs-v1.0.0.yaml
│       ├── loadbalancer-v1.0.0.yaml
│       ├── clusterapi-v1.0.0.yaml
│       ├── nodesenv-v1.0.0.yaml
│       ├── nodespostprocess-v1.0.0.yaml
│       ├── agentswitch-v1.0.0.yaml
│       ├── openfuyao-v2.6.0.yaml
│       └── bkeprovider-v1.1.0.yaml
```
## 8. 工作量评估
| 步骤 | 内容 | 工作量 |
|------|------|--------|
| 第一步 | CRD 定义（4 个）+ ActionEngine（4 种 Executor）+ 模板渲染 | 12 人天 |
| 第二步 | ComponentVersion Controller + NodeConfig Controller + ClusterVersion Controller | 10 人天 |
| 第三步 | 13 个组件 YAML 声明 + DAGScheduler + 安装 E2E | 10 人天 |
| 第四步 | 升级全链路 + 扩缩容 + 回滚 | 10 人天 |
| 测试 | 单元测试 + 集成测试 + E2E + 新旧路径对比 | 8 人天 |
| **总计** | | **50 人天** |
## 9. 风险评估
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| YAML 脚本调试困难 | 中 | 提供 dry-run 模式 + 模板渲染预览 + 日志输出 |
| 模板变量缺失/错误 | 中 | 模板校验 + 缺失变量报错 + 默认值机制 |
| 复杂条件逻辑难以 YAML 表达 | 低 | 支持基本条件表达式 + 必要时拆分为多个步骤 |
| 旧版本 ComponentVersion 找不到 | 中 | ReleaseImage 保留历史版本；降级为直接执行 uninstallAction |
| DAG 调度死锁 | 高 | 超时 + 循环依赖检测 + 手动跳过 |
## 10. 验收标准
1. **YAML 声明验收**：13 个组件全部通过 YAML 声明安装/升级/卸载，无组件特定 Go 代码
2. **安装验收**：从零创建集群，ActionEngine 解释 YAML 完成安装
3. **升级验收**：修改 ClusterVersion 版本，触发旧版本卸载 + 新版本安装
4. **单组件升级验收**：修改 ComponentVersion 版本，仅升级该组件
5. **扩缩容验收**：添加/移除节点，NodeConfig 自动创建/删除
6. **回滚验收**：升级失败后自动执行 rollbackAction
7. **模板验收**：模板变量正确渲染，条件表达式正确评估
8. **兼容性验收**：Feature Gate 关闭时旧 PhaseFlow 正常运行

# ComponentVersion YAML 完整性核查
## ComponentVersion YAML 完整性核查
### 一、Phase → ComponentVersion 映射核查
#### 安装阶段（DeployPhases，11 个 Phase）
| # | Phase | 提案中 ComponentVersion | 覆盖状态 |
|---|-------|------------------------|---------|
| 1 | EnsureBKEAgent | bkeAgent ✅ | 已列出 |
| 2 | EnsureNodesEnv | nodesEnv ✅ | 已列出 |
| 3 | EnsureClusterAPIObj | clusterAPI ✅ | 已列出 |
| 4 | EnsureCerts | certs ✅ | 已列出 |
| 5 | EnsureLoadBalance | loadBalancer ✅ | 已列出 |
| 6 | EnsureMasterInit | kubernetes ✅ | 已列出（condition 分支） |
| 7 | EnsureMasterJoin | kubernetes ✅ | 已列出（condition 分支） |
| 8 | EnsureWorkerJoin | kubernetes ✅ | 已列出（condition 分支） |
| 9 | EnsureAddonDeploy | addon ✅ | 已列出 |
| 10 | EnsureNodesPostProcess | nodesPostProcess ✅ | 已列出 |
| 11 | EnsureAgentSwitch | agentSwitch ✅ | 已列出 |
#### 升级阶段（PostDeployPhases，10 个 Phase）

| # | Phase | 提案中 ComponentVersion | 覆盖状态 |
|---|-------|------------------------|---------|
| 1 | EnsureProviderSelfUpgrade | bkeProvider ✅ | 已列出 |
| 2 | EnsureAgentUpgrade | bkeAgent ✅ | 已列出（upgradeAction） |
| 3 | EnsureContainerdUpgrade | containerd ✅ | 已列出 |
| 4 | EnsureEtcdUpgrade | etcd ✅ | 已列出 |
| 5 | EnsureWorkerUpgrade | kubernetes ✅ | 已列出（condition 分支） |
| 6 | EnsureMasterUpgrade | kubernetes ✅ | 已列出（condition 分支） |
| 7 | EnsureWorkerDelete | ❌ **未列出** | **缺少** |
| 8 | EnsureMasterDelete | ❌ **未列出** | **缺少** |
| 9 | EnsureComponentUpgrade | openFuyao ✅ | 已列出 |
| 10 | EnsureCluster | ❌ **未列出** | **缺少** |
#### 控制阶段（CommonPhases，5 个 Phase）— 不映射为 ComponentVersion
| # | Phase | 归属 | 说明 |
|---|-------|------|------|
| 1 | EnsureFinalizer | ClusterVersion Controller | 框架级逻辑，非组件 |
| 2 | EnsurePaused | ClusterVersion Controller | 框架级逻辑，非组件 |
| 3 | EnsureClusterManage | ❌ **未列出** | 纳管现有集群，独立场景 |
| 4 | EnsureDeleteOrReset | ClusterVersion Controller | 框架级逻辑，非组件 |
| 5 | EnsureDryRun | ClusterVersion Controller | 框架级逻辑，非组件 |
### 二、缺少的 ComponentVersion YAML
经过核查，**缺少 3 个组件的 YAML**：
1. **clusterManage（纳管现有集群）**— EnsureClusterManage 的逻辑包含：收集集群基础信息 → 推送 Agent → 收集 Agent 信息 → 伪引导 → 兼容性补丁，这是一个独立于安装/升级的完整场景
2. **nodeDelete（节点删除/缩容）**— EnsureWorkerDelete + EnsureMasterDelete 的逻辑包含：drain 节点 → 删除 Machine → 等待节点移除，缩容场景必需
3. **clusterHealth（集群健康检查）**— EnsureCluster 的逻辑包含：检查所有 Node Ready → 检查组件健康 → 更新集群状态，升级后验证必需
### 三、补充的 ComponentVersion YAML
#### 3.1 clusterManage ComponentVersion YAML（纳管现有集群）
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: clustermanage-v1.0.0
  namespace: cluster-system
spec:
  componentName: clusterManage
  version: v1.0.0
  scope: Cluster
  dependencies: []

  installAction:
    preCheck:
      name: check-not-bke-cluster
      type: Script
      script: |
        #!/bin/bash
        # 仅对非 BKE 创建的集群执行纳管
        kubectl get bkecluster {{.ClusterName}} -o jsonpath='{.metadata.labels.bke\.openfuyao\.cn/managed}' 2>/dev/null
      expectedOutput: ""
    steps:
      - name: collect-base-info
        type: Script
        script: |
          #!/bin/bash
          set -e
          mkdir -p /var/lib/bke/workspace/collect
          kubectl version -o json > /var/lib/bke/workspace/collect/k8s-version.json
          kubectl get nodes -o json > /var/lib/bke/workspace/collect/nodes.json
          kubectl cluster-info dump > /var/lib/bke/workspace/collect/cluster-info.txt 2>/dev/null || true
          # 收集运行时信息
          for node in $(kubectl get nodes -o jsonpath='{.items[*].status.addresses[?(@.type=="InternalIP")].address}'); do
            echo "$node: $(ssh -o StrictHostKeyChecking=no root@$node 'containerd --version; runc --version; cat /etc/os-release' 2>/dev/null)" >> /var/lib/bke/workspace/collect/runtime-info.txt || true
          done
      - name: push-agent-via-daemonset
        type: Kubectl
        kubectl:
          operation: Apply
          manifest: |
            apiVersion: apps/v1
            kind: DaemonSet
            metadata:
              name: bkeagent-launcher
              namespace: kube-system
            spec:
              selector:
                matchLabels:
                  app: bkeagent-launcher
              template:
                spec:
                  hostPID: true
                  hostNetwork: true
                  containers:
                  - name: launcher
                    image: {{.ImageRepo}}/bke-agent-launcher:{{.Version}}
                    securityContext:
                      privileged: true
                    volumeMounts:
                    - name: rootfs
                      mountPath: /host
                  volumes:
                  - name: rootfs
                    hostPath:
                      path: /
      - name: collect-agent-info
        type: Script
        script: |
          #!/bin/bash
          set -e
          timeout=120
          elapsed=0
          while [ $elapsed -lt $timeout ]; do
            if kubectl get pods -n kube-system -l app=bkeagent-launcher -o jsonpath='{.items[*].status.phase}' | grep -q "Running"; then
              break
            fi
            sleep 2
            elapsed=$((elapsed + 2))
          done
          # 通过 Agent 收集更多信息
          for node in $(kubectl get nodes -o jsonpath='{.items[*].status.addresses[?(@.type=="InternalIP")].address}'); do
            curl -sk https://$node:{{.AgentPort}}/api/v1/collect -o /var/lib/bke/workspace/collect/agent-$node.json 2>/dev/null || true
          done
      - name: fake-bootstrap
        type: Script
        condition: "{{.ClusterType}} == bocloud"
        script: |
          #!/bin/bash
          set -e
          # 伪引导：将现有集群转为 BKE 管理
          for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
            ssh -o StrictHostKeyChecking=no root@$node "mkdir -p /etc/bke && echo 'managed-by-bke' > /etc/bke/managed" 2>/dev/null || true
          done
          # 标记集群为 fully controlled
          kubectl label bkecluster {{.ClusterName}} bke.openfuyao.cn/fully-controlled=true --overwrite
      - name: compatibility-patch
        type: Script
        condition: "{{.ClusterType}} == bocloud"
        script: |
          #!/bin/bash
          set -e
          # ansible -> bke 兼容性修改
          for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
            ssh -o StrictHostKeyChecking=no root@$node "
              # 修复 kubelet 配置
              sed -i 's/--container-runtime=remote/--container-runtime=remote/g' /etc/systemd/system/kubelet.service 2>/dev/null || true
              systemctl daemon-reload
            " 2>/dev/null || true
          done
    postCheck:
      name: verify-managed
      type: Script
      script: |
        kubectl get bkecluster {{.ClusterName}} -o jsonpath='{.metadata.labels.bke\.openfuyao\.cn/fully-controlled}' 2>/dev/null
      expectedOutput: "true"
      timeout: 180s

  upgradeAction:
    steps:
      - name: re-collect-info
        type: Script
        script: |
          #!/bin/bash
          set -e
          kubectl version -o json > /var/lib/bke/workspace/collect/k8s-version.json
          kubectl get nodes -o json > /var/lib/bke/workspace/collect/nodes.json

  healthCheck:
    steps:
      - name: check-fully-controlled
        type: Script
        script: |
          kubectl get bkecluster {{.ClusterName}} -o jsonpath='{.metadata.labels.bke\.openfuyao\.cn/fully-controlled}' 2>/dev/null
        expectedOutput: "true"
```
#### 3.2 nodeDelete ComponentVersion YAML（节点删除/缩容）
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: nodedelete-v1.0.0
  namespace: cluster-system
spec:
  componentName: nodeDelete
  version: v1.0.0
  scope: Node
  dependencies: []

  installAction:
    steps: []  # 缩容场景无安装操作

  upgradeAction:
    steps: []  # 缩容场景无升级操作

  uninstallAction:
    preCheck:
      name: check-node-exists
      type: Kubectl
      kubectl:
        operation: Wait
        resource: "node/{{.NodeHostname}}"
        condition: "Ready"
        timeout: 30s
    steps:
      - name: drain-node
        type: Kubectl
        condition: "{{.NodeRole}} == worker"
        kubectl:
          operation: Drain
          resource: "node/{{.NodeHostname}}"
          timeout: 120s
      - name: drain-master-node
        type: Kubectl
        condition: "{{.NodeRole}} == master"
        kubectl:
          operation: Drain
          resource: "node/{{.NodeHostname}}"
          timeout: 300s
      - name: delete-machine
        type: Kubectl
        kubectl:
          operation: Delete
          resource: "machine"
          namespace: "{{.ClusterNamespace}}"
          fieldPatch: |
            {"metadata":{"labels":{"cluster.x-k8s.io/cluster-name":"{{.ClusterName}}","node-role":"{{.NodeRole}}"}}}
      - name: remove-etcd-member
        type: Script
        condition: "{{.NodeRole}} == master"
        script: |
          #!/bin/bash
          set -e
          ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
            --cacert=/etc/kubernetes/pki/etcd/ca.crt \
            --cert=/etc/kubernetes/pki/etcd/peer.crt \
            --key=/etc/kubernetes/pki/etcd/peer.key \
            member remove $(ETCDCTL_API=3 etcdctl member list | grep {{.NodeHostname}} | awk -F, '{print $1}' | tr -d ':')
        onFailure: Continue
      - name: clean-node-remnants
        type: Script
        script: |
          #!/bin/bash
          set -e
          # 清理节点上的残留配置
          ssh -o StrictHostKeyChecking=no root@{{.NodeIP}} "
            systemctl stop bke-agent 2>/dev/null || true
            systemctl disable bke-agent 2>/dev/null || true
            rm -rf /etc/bke /usr/local/bin/bke-agent 2>/dev/null || true
          " || true
    postCheck:
      name: verify-node-removed
      type: Kubectl
      kubectl:
        operation: Wait
        resource: "machine"
        namespace: "{{.ClusterNamespace}}"
        condition: "Deleted"
        timeout: 240s
    strategy:
      executionMode: Serial
      batchSize: 1

  healthCheck:
    steps:
      - name: verify-machines-match
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "machinedeployment"
          namespace: "{{.ClusterNamespace}}"
          condition: "Available"
          timeout: 60s
```
#### 3.3 clusterHealth ComponentVersion YAML（集群健康检查）
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: clusterhealth-v1.0.0
  namespace: cluster-system
spec:
  componentName: clusterHealth
  version: v1.0.0
  scope: Cluster
  dependencies: [kubernetes, addon, openFuyao]

  installAction:
    steps: []  # 健康检查无安装操作

  upgradeAction:
    steps: []  # 健康检查无升级操作

  uninstallAction:
    steps: []  # 健康检查无卸载操作

  healthCheck:
    steps:
      - name: check-all-nodes-ready
        type: Kubectl
        kubectl:
          operation: Wait
          resource: nodes
          condition: "Ready"
          timeout: 300s
      - name: check-all-pods-running
        type: Script
        script: |
          #!/bin/bash
          not_ready=$(kubectl get pods -A --field-selector=status.phase!=Running --no-headers 2>/dev/null | grep -v Completed | wc -l)
          if [ "$not_ready" -gt 0 ]; then
            echo "ERROR: $not_ready pods are not running"
            exit 1
          fi
          echo "All pods are running"
        expectedOutput: "All pods are running"
        timeout: 180s
        interval: 10s
      - name: check-etcd-cluster-health
        type: Script
        script: |
          ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
            --cacert=/etc/kubernetes/pki/etcd/ca.crt \
            --cert=/etc/kubernetes/pki/etcd/peer.crt \
            --key=/etc/kubernetes/pki/etcd/peer.key \
            endpoint health --cluster
        expectedOutput: "is healthy"
        timeout: 60s
        interval: 10s
      - name: check-component-status
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "deployment"
          namespace: "openfuyao-system"
          condition: "Available"
          timeout: 180s
      - name: check-coredns
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "deployment/coredns"
          namespace: "kube-system"
          condition: "Available"
          timeout: 60s
      - name: check-kube-proxy
        type: Kubectl
        kubectl:
          operation: Wait
          resource: "daemonset/kube-proxy"
          namespace: "kube-system"
          condition: "Available"
          timeout: 60s
```
### 四、修正后的完整 ComponentVersion 列表（16 个）
| # | ComponentName | Scope | 覆盖的 Phase | 场景 |
|---|--------------|-------|-------------|------|
| 1 | bkeAgent | Node | EnsureBKEAgent, EnsureAgentUpgrade | 安装+升级 |
| 2 | nodesEnv | Node | EnsureNodesEnv | 安装 |
| 3 | clusterAPI | Cluster | EnsureClusterAPIObj | 安装 |
| 4 | certs | Cluster | EnsureCerts | 安装 |
| 5 | loadBalancer | Node | EnsureLoadBalance | 安装 |
| 6 | kubernetes | Node | EnsureMasterInit, EnsureMasterJoin, EnsureWorkerJoin, EnsureMasterUpgrade, EnsureWorkerUpgrade | 安装+升级 |
| 7 | containerd | Node | EnsureContainerdUpgrade | 升级 |
| 8 | etcd | Node | EnsureEtcdUpgrade | 升级 |
| 9 | addon | Cluster | EnsureAddonDeploy | 安装 |
| 10 | nodesPostProcess | Node | EnsureNodesPostProcess | 安装 |
| 11 | agentSwitch | Cluster | EnsureAgentSwitch | 安装 |
| 12 | bkeProvider | Cluster | EnsureProviderSelfUpgrade | 升级 |
| 13 | openFuyao | Cluster | EnsureComponentUpgrade | 升级 |
| 14 | **clusterManage** | Cluster | EnsureClusterManage | **纳管** |
| 15 | **nodeDelete** | Node | EnsureWorkerDelete, EnsureMasterDelete | **缩容** |
| 16 | **clusterHealth** | Cluster | EnsureCluster | **健康检查** |
### 五、不映射为 ComponentVersion 的 Phase（5 个）
| Phase | 归属 | 原因 |
|-------|------|------|
| EnsureFinalizer | ClusterVersion Controller | 框架级 Finalizer 管理，非组件行为 |
| EnsurePaused | ClusterVersion Controller | 框架级暂停控制，非组件行为 |
| EnsureDeleteOrReset | ClusterVersion Controller | 框架级删除/重置，触发各组件 uninstallAction |
| EnsureDryRun | ClusterVersion Controller | 框架级预检模式，不执行实际操作 |
| EnsureClusterManage | → **clusterManage** ComponentVersion | 已补充 |

> **总结**：原提案列出了 13 个 ComponentVersion YAML，缺少 **clusterManage**（纳管现有集群）、**nodeDelete**（节点删除/缩容）、**clusterHealth**（集群健康检查）3 个组件。补充后共 **16 个** ComponentVersion，完整覆盖所有 26 个 Phase。
