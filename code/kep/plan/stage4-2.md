# Phase 重构为 ComponentVersion/NodeConfig 
## Phase 重构为 ComponentVersion/NodeConfig 声明式配置方案
### 一、Phase 分类与重构策略
根据对各 Phase 的深入分析，首先按**操作性质**将 20 个 Phase 分为四大类：

| 分类 | Phase | 操作性质 | 重构策略 |
|------|-------|----------|----------|
| **控制类** | EnsureFinalizer, EnsurePaused, EnsureClusterManage, EnsureDeleteOrReset, EnsureDryRun | 集群生命周期控制，无组件安装 | 保留在 BKECluster Controller 中，不映射为 ComponentVersion |
| **节点级组件安装** | EnsureBKEAgent, EnsureNodesEnv, EnsureNodesPostProcess, EnsureContainerdUpgrade | 在节点上安装/升级软件 | 映射为 NodeConfig + ComponentVersion |
| **集群级组件安装** | EnsureClusterAPIObj, EnsureCerts, EnsureLoadBalance, EnsureMasterInit, EnsureMasterJoin, EnsureWorkerJoin, EnsureAddonDeploy, EnsureAgentSwitch | 创建/配置集群资源 | 映射为 ComponentVersion（集群级） |
| **升级/缩容** | EnsureProviderSelfUpgrade, EnsureAgentUpgrade, EnsureEtcdUpgrade, EnsureWorkerUpgrade, EnsureMasterUpgrade, EnsureComponentUpgrade, EnsureWorkerDelete, EnsureMasterDelete, EnsureCluster | 版本变更/节点增删 | 映射为 ComponentVersion（升级路径）+ NodeConfig（缩容） |
### 二、ComponentVersion CRD 定义
基于用户提供的 ComponentVersion 模板，结合 BKE 各 Phase 的实际需求，定义完整的 CRD：
```yaml
# api/nodecomponent/v1alpha1/componentversion_types.go
```

```go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ComponentName string

const (
    ComponentBKEAgent       ComponentName = "bkeAgent"
    ComponentNodesEnv       ComponentName = "nodesEnv"
    ComponentContainerd     ComponentName = "containerd"
    ComponentEtcd           ComponentName = "etcd"
    ComponentKubernetes     ComponentName = "kubernetes"
    ComponentOpenFuyao      ComponentName = "openFuyao"
    ComponentBKEProvider    ComponentName = "bkeProvider"
    ComponentCerts          ComponentName = "certs"
    ComponentLoadBalancer   ComponentName = "loadBalancer"
    ComponentClusterAPI     ComponentName = "clusterAPI"
    ComponentAddon          ComponentName = "addon"
    ComponentNodesPostProc  ComponentName = "nodesPostProcess"
    ComponentAgentSwitch    ComponentName = "agentSwitch"
)

type ComponentVersionSpec struct {
    ComponentName ComponentName `json:"componentName"`
    Version       string        `json:"version"`

    Source *ComponentSource `json:"source,omitempty"`

    InstallAction *ActionSpec `json:"installAction,omitempty"`
    UpgradeAction *ActionSpec `json:"upgradeAction,omitempty"`
    RollbackAction *ActionSpec `json:"rollbackAction,omitempty"`

    HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`

    Compatibility *CompatibilitySpec `json:"compatibility,omitempty"`

    UpgradePath *UpgradePathSpec `json:"upgradePath,omitempty"`

    Scope ComponentScope `json:"scope,omitempty"`
}

type ComponentScope string

const (
    ComponentScopeNode   ComponentScope = "Node"
    ComponentScopeCluster ComponentScope = "Cluster"
)

type ComponentSource struct {
    Type     SourceType `json:"type"`
    URL      string     `json:"url,omitempty"`
    Checksum string     `json:"checksum,omitempty"`

    Charts      []ChartSource `json:"charts,omitempty"`
    Images      []ImageSource `json:"images,omitempty"`
    Scripts     []ScriptSource `json:"scripts,omitempty"`
    Packages    []PackageSource `json:"packages,omitempty"`
}

type SourceType string

const (
    SourceTypeHTTP   SourceType = "HTTP"
    SourceTypeOCI    SourceType = "OCI"
    SourceTypeLocal  SourceType = "Local"
    SourceTypeInline SourceType = "Inline"
)

type ChartSource struct {
    Name      string `json:"name"`
    RepoURL   string `json:"repoURL,omitempty"`
    Version   string `json:"version,omitempty"`
    Namespace string `json:"namespace,omitempty"`
    Values    string `json:"values,omitempty"`
}

type ImageSource struct {
    Name       string   `json:"name"`
    Repository string   `json:"repository,omitempty"`
    Tag        string   `json:"tag,omitempty"`
    Platforms  []string `json:"platforms,omitempty"`
}

type ScriptSource struct {
    Name     string `json:"name"`
    Content  string `json:"content,omitempty"`
    URL      string `json:"url,omitempty"`
    Checksum string `json:"checksum,omitempty"`
}

type PackageSource struct {
    Name    string `json:"name"`
    Version string `json:"version,omitempty"`
    URL     string `json:"url,omitempty"`
}

type ActionSpec struct {
    Type     ActionType `json:"type"`
    Command  string     `json:"command,omitempty"`
    Script   string     `json:"script,omitempty"`
    Config   string     `json:"config,omitempty"`
    Timeout  *metav1.Duration `json:"timeout,omitempty"`

    NodeSelector *NodeSelector `json:"nodeSelector,omitempty"`

    PreCheck  *ActionSpec `json:"preCheck,omitempty"`
    PostCheck *ActionSpec `json:"postCheck,omitempty"`
}

type ActionType string

const (
    ActionTypeCommand    ActionType = "Command"
    ActionTypeScript     ActionType = "Script"
    ActionTypeManifest   ActionType = "Manifest"
    ActionTypeController ActionType = "Controller"
)

type NodeSelector struct {
    Roles  []string `json:"roles,omitempty"`
    Labels map[string]string `json:"labels,omitempty"`
}

type HealthCheckSpec struct {
    Type           ActionType   `json:"type"`
    Command        string       `json:"command,omitempty"`
    ExpectedOutput string       `json:"expectedOutput,omitempty"`
    Timeout        *metav1.Duration `json:"timeout,omitempty"`
    Interval       *metav1.Duration `json:"interval,omitempty"`
}

type CompatibilitySpec struct {
    OS     []OSCompatibility     `json:"os,omitempty"`
    Dependencies []DependencySpec `json:"dependencies,omitempty"`
    KubernetesVersions []string  `json:"kubernetesVersions,omitempty"`
}

type OSCompatibility struct {
    Type     string   `json:"type"`
    Distros  []string `json:"distros,omitempty"`
    Versions []string `json:"versions,omitempty"`
    Arch     []string `json:"arch,omitempty"`
}

type DependencySpec struct {
    Component    ComponentName `json:"component"`
    VersionRange string        `json:"versionRange"`
}

type UpgradePathSpec struct {
    FromVersions    []string `json:"fromVersions,omitempty"`
    PreUpgradeCheck string   `json:"preUpgradeCheck,omitempty"`
}

type ComponentVersionStatus struct {
    Phase      ComponentPhase `json:"phase,omitempty"`
    InstalledVersion string  `json:"installedVersion,omitempty"`
    Message    string        `json:"message,omitempty"`

    NodeStatuses map[string]NodeComponentStatus `json:"nodeStatuses,omitempty"`

    LastOperation *LastOperation `json:"lastOperation,omitempty"`

    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ComponentPhase string

const (
    ComponentPhasePending    ComponentPhase = "Pending"
    ComponentPhaseInstalling ComponentPhase = "Installing"
    ComponentPhaseUpgrading  ComponentPhase = "Upgrading"
    ComponentPhaseReady      ComponentPhase = "Ready"
    ComponentPhaseFailed     ComponentPhase = "Failed"
    ComponentPhaseRollingBack ComponentPhase = "RollingBack"
)

type NodeComponentStatus struct {
    NodeName         string        `json:"nodeName"`
    InstalledVersion string        `json:"installedVersion,omitempty"`
    Phase            ComponentPhase `json:"phase,omitempty"`
    Message          string        `json:"message,omitempty"`
    LastUpdated      *metav1.Time  `json:"lastUpdated,omitempty"`
}

type LastOperation struct {
    Type      string       `json:"type"`
    Component ComponentName `json:"component,omitempty"`
    StartTime *metav1.Time `json:"startTime,omitempty"`
    EndTime   *metav1.Time `json:"endTime,omitempty"`
    Result    string       `json:"result,omitempty"`
    Message   string       `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="COMPONENT",type="string",JSONPath=".spec.componentName"
// +kubebuilder:printcolumn:name="VERSION",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="INSTALLED",type="string",JSONPath=".status.installedVersion"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type ComponentVersion struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   ComponentVersionSpec   `json:"spec,omitempty"`
    Status ComponentVersionStatus `json:"status,omitempty"`
}
```
### 三、NodeConfig CRD 定义
```go
// api/nodecomponent/v1alpha1/nodeconfig_types.go

type NodeConfigSpec struct {
    Connection NodeConnection `json:"connection,omitempty"`
    OS         NodeOSInfo    `json:"os,omitempty"`

    Components NodeComponents `json:"components,omitempty"`

    Roles []NodeRole `json:"roles,omitempty"`
}

type NodeConnection struct {
    Host         string `json:"host,omitempty"`
    Port         int    `json:"port,omitempty"`
    SSHKeySecret *SecretReference `json:"sshKeySecret,omitempty"`
    AgentPort    int    `json:"agentPort,omitempty"`
}

type SecretReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace,omitempty"`
}

type NodeOSInfo struct {
    Type    string `json:"type,omitempty"`
    Distro  string `json:"distro,omitempty"`
    Version string `json:"version,omitempty"`
    Arch    string `json:"arch,omitempty"`
}

type NodeRole string

const (
    NodeRoleMaster NodeRole = "master"
    NodeRoleWorker NodeRole = "worker"
    NodeRoleEtcd   NodeRole = "etcd"
)

type NodeComponents struct {
    Containerd   *ContainerdComponentConfig   `json:"containerd,omitempty"`
    Kubelet      *KubeletComponentConfig      `json:"kubelet,omitempty"`
    Etcd         *EtcdComponentConfig         `json:"etcd,omitempty"`
    BKEAgent     *BKEAgentComponentConfig     `json:"bkeAgent,omitempty"`
    NodesEnv     *NodesEnvComponentConfig     `json:"nodesEnv,omitempty"`
    PostProcess  *PostProcessComponentConfig  `json:"postProcess,omitempty"`
}

type ContainerdComponentConfig struct {
    Version string `json:"version,omitempty"`
    Config  string `json:"config,omitempty"`
    DataDir string `json:"dataDir,omitempty"`
    Registry *RegistryConfig `json:"registry,omitempty"`
    SystemdCgroup bool `json:"systemdCgroup,omitempty"`
}

type KubeletComponentConfig struct {
    Version      string            `json:"version,omitempty"`
    ExtraArgs    map[string]string `json:"extraArgs,omitempty"`
    FeatureGates map[string]bool   `json:"featureGates,omitempty"`
    Config       string            `json:"config,omitempty"`
}

type EtcdComponentConfig struct {
    Version    string   `json:"version,omitempty"`
    DataDir    string   `json:"dataDir,omitempty"`
    ClientURLs []string `json:"clientURLs,omitempty"`
    PeerURLs   []string `json:"peerURLs,omitempty"`
}

type BKEAgentComponentConfig struct {
    Version     string `json:"version,omitempty"`
    Config      string `json:"config,omitempty"`
    Kubeconfig  string `json:"kubeconfig,omitempty"`
}

type NodesEnvComponentConfig struct {
    ExtraScripts []string `json:"extraScripts,omitempty"`
    HTTPRepo     string   `json:"httpRepo,omitempty"`
    ImageRepo    string   `json:"imageRepo,omitempty"`
}

type PostProcessComponentConfig struct {
    Scripts []string `json:"scripts,omitempty"`
}

type RegistryConfig struct {
    Mirrors map[string]string `json:"mirrors,omitempty"`
}

type NodeConfigStatus struct {
    Phase NodeConfigPhase `json:"phase,omitempty"`

    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`

    OSInfo *NodeOSDetailInfo `json:"osInfo,omitempty"`

    LastOperation *LastOperation `json:"lastOperation,omitempty"`

    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type NodeConfigPhase string

const (
    NodeConfigPhasePending    NodeConfigPhase = "Pending"
    NodeConfigPhaseInstalling NodeConfigPhase = "Installing"
    NodeConfigPhaseUpgrading  NodeConfigPhase = "Upgrading"
    NodeConfigPhaseReady      NodeConfigPhase = "Ready"
    NodeConfigPhaseFailed     NodeConfigPhase = "Failed"
    NodeConfigPhaseDeleting   NodeConfigPhase = "Deleting"
)

type NodeComponentDetailStatus struct {
    InstalledVersion string        `json:"installedVersion,omitempty"`
    Status           ComponentPhase `json:"status,omitempty"`
    LastUpdated      *metav1.Time  `json:"lastUpdated,omitempty"`
    Message          string        `json:"message,omitempty"`
}

type NodeOSDetailInfo struct {
    KernelVersion string `json:"kernelVersion,omitempty"`
    OSImage       string `json:"osImage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="HOST",type="string",JSONPath=".spec.connection.host"
// +kubebuilder:printcolumn:name="ROLE",type="string",JSONPath=".spec.roles"`
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"`
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type NodeConfig struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   NodeConfigSpec   `json:"spec,omitempty"`
    Status NodeConfigStatus `json:"status,omitempty"`
}
```
### 四、各 Phase 到 ComponentVersion/NodeConfig 的映射
#### 4.1 DeployPhases 映射
##### EnsureBKEAgent → NodeConfig.components.bkeAgent + ComponentVersion(bkeAgent)
**当前逻辑**：通过 SSH 推送 Agent 二进制到节点，配置 kubeconfig，启动 Agent 服务，然后 ping Agent 确认就绪。

**声明式映射**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v1.0.0
  namespace: cluster-system
  ownerReferences:
    - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
      kind: BKECluster
      name: my-cluster
spec:
  componentName: bkeAgent
  version: v1.0.0
  scope: Node
  source:
    type: HTTP
    url: https://repo.openfuyao.cn/bke-agent/v1.0.0/bke-agent-linux-amd64.tar.gz
    checksum: "sha256:xxxxx"
    scripts:
      - name: install-bke-agent.sh
        content: |
          #!/bin/bash
          set -e
          tar -xzf /tmp/bke-agent.tar.gz -C /usr/local/bin
          mkdir -p /etc/bke-agent
          cat > /etc/bke-agent/config.yaml <<EOF
          {{ .Config }}
          EOF
          cat > /etc/systemd/system/bke-agent.service <<EOF
          [Unit]
          Description=BKE Agent
          After=network.target
          [Service]
          ExecStart=/usr/local/bin/bke-agent --config /etc/bke-agent/config.yaml
          Restart=always
          [Install]
          WantedBy=multi-user.target
          EOF
          systemctl daemon-reload
          systemctl enable bke-agent
          systemctl start bke-agent
  installAction:
    type: Script
    script: install-bke-agent.sh
    timeout: 300s
    nodeSelector:
      roles: ["master", "worker", "etcd"]
  healthCheck:
    type: Command
    command: "systemctl is-active bke-agent"
    expectedOutput: "active"
    timeout: 10s
  compatibility:
    os:
      - type: linux
        distros: [ubuntu, centos, kylin, uos]
        versions: ["20.04", "22.04", "7", "8"]
        arch: [amd64, arm64]
  upgradePath:
    fromVersions: ["v0.9.0"]
```

```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: NodeConfig
metadata:
  name: node-192.168.1.10
  namespace: cluster-system
spec:
  connection:
    host: 192.168.1.10
    port: 22
    sshKeySecret:
      name: node-ssh-key
      namespace: cluster-system
  os:
    type: linux
    distro: ubuntu
    version: "22.04"
    arch: amd64
  roles: ["master"]
  components:
    bkeAgent:
      version: v1.0.0
      kubeconfig: |
        apiVersion: v1
        kind: Config
        ...
```
##### EnsureNodesEnv → NodeConfig.components.nodesEnv + ComponentVersion(nodesEnv)
**当前逻辑**：在节点上执行一系列环境初始化脚本（安装 lxcfs、nfs-utils、etcdctl、helm、calicoctl、更新 runc 等），通过 `command.ENV` 创建命令资源下发。

**声明式映射**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: nodesenv-v1.0.0
spec:
  componentName: nodesEnv
  version: v1.0.0
  scope: Node
  source:
    type: Inline
    scripts:
      - name: install-lxcfs.sh
        url: https://scripts.openfuyao.cn/install-lxcfs.sh
      - name: install-nfsutils.sh
        url: https://scripts.openfuyao.cn/install-nfsutils.sh
      - name: install-etcdctl.sh
        url: https://scripts.openfuyao.cn/install-etcdctl.sh
      - name: install-helm.sh
        url: https://scripts.openfuyao.cn/install-helm.sh
      - name: install-calicoctl.sh
        url: https://scripts.openfuyao.cn/install-calicoctl.sh
      - name: update-runc.sh
        url: https://scripts.openfuyao.cn/update-runc.sh
      - name: file-downloader.sh
        url: https://scripts.openfuyao.cn/file-downloader.sh
      - name: package-downloader.sh
        url: https://scripts.openfuyao.cn/package-downloader.sh
  installAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      for script in install-lxcfs install-nfsutils install-etcdctl install-helm install-calicoctl update-runc; do
        /tmp/scripts/${script}.sh
      done
    timeout: 600s
    nodeSelector:
      roles: ["master", "worker", "etcd"]
  healthCheck:
    type: Command
    command: "which helm && which etcdctl && which calicoctl"
    timeout: 10s
  compatibility:
    os:
      - type: linux
        distros: [ubuntu, centos, kylin, uos]
        arch: [amd64, arm64]
    dependencies:
      - component: bkeAgent
        versionRange: ">=v1.0.0"
```
```yaml
# NodeConfig 中引用
spec:
  components:
    nodesEnv:
      extraScripts:
        - install-lxcfs.sh
        - install-nfsutils.sh
        - install-etcdctl.sh
        - install-helm.sh
        - install-calicoctl.sh
        - update-runc.sh
      httpRepo: https://repo.openfuyao.cn
      imageRepo: registry.openfuyao.cn
```
##### EnsureClusterAPIObj → ComponentVersion(clusterAPI)
**当前逻辑**：创建 Cluster API 的 Cluster、Machine 等对象，等待 ControlPlane 初始化完成。这是集群级操作，不涉及节点。

**声明式映射**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: clusterapi-v1.0.0
spec:
  componentName: clusterAPI
  version: v1.0.0
  scope: Cluster
  source:
    type: Inline
  installAction:
    type: Controller
    config: |
      # 由 Controller 负责创建 Cluster API 对象
      # Cluster, Machine, KubeadmControlPlane 等
    timeout: 300s
  healthCheck:
    type: Command
    command: "check-control-plane-initialized"
    timeout: 300s
  compatibility:
    dependencies:
      - component: bkeAgent
        versionRange: ">=v1.0.0"
      - component: nodesEnv
        versionRange: ">=v1.0.0"
```
##### EnsureCerts → ComponentVersion(certs)
**当前逻辑**：通过 `certs.BKEKubernetesCertGenerator` 生成/查找证书，存储在 Secret 中。

**声明式映射**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: certs-v1.0.0
spec:
  componentName: certs
  version: v1.0.0
  scope: Cluster
  source:
    type: Inline
  installAction:
    type: Controller
    config: |
      # 由 Controller 负责证书生成
      # 包括: ca, etcd-ca, front-proxy-ca, sa
      # 证书存储在 Secret 中
    timeout: 120s
  healthCheck:
    type: Command
    command: "check-certs-validity"
    timeout: 30s
  compatibility:
    dependencies:
      - component: clusterAPI
        versionRange: ">=v1.0.0"
```
##### EnsureLoadBalance → ComponentVersion(loadBalancer)
**当前逻辑**：在 HAProxy 节点上配置负载均衡器（如果 ControlPlaneEndpoint.Host 不是 master 节点 IP）。

**声明式映射**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: loadbalancer-v1.0.0
spec:
  componentName: loadBalancer
  version: v1.0.0
  scope: Node
  source:
    type: Inline
    scripts:
      - name: configure-haproxy.sh
        content: |
          #!/bin/bash
          set -e
          # 配置 HAProxy
          cat > /etc/haproxy/haproxy.cfg <<EOF
          {{ .Config }}
          EOF
          systemctl restart haproxy
  installAction:
    type: Script
    script: configure-haproxy.sh
    timeout: 60s
    nodeSelector:
      labels:
        role: loadbalancer
  healthCheck:
    type: Command
    command: "systemctl is-active haproxy"
    expectedOutput: "active"
    timeout: 10s
  compatibility:
    dependencies:
      - component: certs
        versionRange: ">=v1.0.0"
```
##### EnsureMasterInit → ComponentVersion(kubernetes) 的 Init 步骤
**当前逻辑**：在第一个 master 节点上执行 `kubeadm init`，等待控制平面初始化完成。

**声明式映射**：合并到 Kubernetes ComponentVersion 的安装步骤中（见下文 KubernetesUpgrader 设计）。
##### EnsureMasterJoin → ComponentVersion(kubernetes) 的 Join 步骤
**当前逻辑**：在后续 master 节点上执行 `kubeadm join`，加入控制平面。
##### EnsureWorkerJoin → ComponentVersion(kubernetes) 的 WorkerJoin 步骤
**当前逻辑**：在 worker 节点上执行 `kubeadm join`，加入集群。
##### EnsureAddonDeploy → ComponentVersion(addon)
**当前逻辑**：部署各种 Addon（通过 Helm chart 或 YAML manifest），包括 etcd-backup、ingress、监控等。

**声明式映射**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: addon-v1.0.0
spec:
  componentName: addon
  version: v1.0.0
  scope: Cluster
  source:
    type: OCI
    charts:
      - name: etcd-backup
        repoURL: https://charts.openfuyao.cn
        version: 1.0.0
        namespace: kube-system
      - name: ingress-nginx
        repoURL: https://charts.openfuyao.cn
        version: 4.8.0
        namespace: ingress-nginx
    images:
      - name: etcd-backup
        repository: registry.openfuyao.cn/etcd-backup
        tag: v1.0.0
      - name: ingress-nginx-controller
        repository: registry.openfuyao.cn/ingress-nginx/controller
        tag: v1.8.2
  installAction:
    type: Manifest
    config: |
      # 由 Controller 负责 Helm 部署
    timeout: 600s
  healthCheck:
    type: Command
    command: "check-addons-ready"
    timeout: 120s
  compatibility:
    dependencies:
      - component: kubernetes
        versionRange: ">=v1.27.0"
```
##### EnsureNodesPostProcess → NodeConfig.components.postProcess + ComponentVersion(nodesPostProcess)
**当前逻辑**：在节点上执行后处理脚本（如标签、污点设置等）。
##### EnsureAgentSwitch → ComponentVersion(agentSwitch)
**当前逻辑**：切换 Agent 监听的集群（从引导集群切换到目标集群）。

**声明式映射**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: agentswitch-v1.0.0
spec:
  componentName: agentSwitch
  version: v1.0.0
  scope: Cluster
  source:
    type: Inline
  installAction:
    type: Controller
    config: |
      # 由 Controller 负责切换 Agent 监听目标
      # 更新 Agent 的 kubeconfig 指向目标集群
    timeout: 120s
  healthCheck:
    type: Command
    command: "check-agent-listening-target"
    timeout: 30s
  compatibility:
    dependencies:
      - component: addon
        versionRange: ">=v1.0.0"
```
#### 4.2 PostDeployPhases 映射
##### EnsureProviderSelfUpgrade → ComponentVersion(bkeProvider)
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeprovider-v1.1.0
spec:
  componentName: bkeProvider
  version: v1.1.0
  scope: Cluster
  source:
    type: OCI
    images:
      - name: cluster-api-provider-bke
        repository: registry.openfuyao.cn/cluster-api-provider-bke
        tag: v1.1.0
  installAction:
    type: Controller
    config: |
      # 由 Controller 负责 Provider Deployment 滚动更新
    timeout: 300s
  upgradeAction:
    type: Controller
    config: |
      # 更新 Deployment 镜像 tag，等待滚动更新完成
    timeout: 300s
  healthCheck:
    type: Command
    command: "check-deployment-ready bke-controller-manager cluster-system"
    timeout: 60s
  upgradePath:
    fromVersions: ["v1.0.0"]
```
##### EnsureAgentUpgrade → ComponentVersion(bkeAgent) 的升级路径
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v1.1.0
spec:
  componentName: bkeAgent
  version: v1.1.0
  scope: Node
  source:
    type: OCI
    images:
      - name: bkeagent-deployer
        repository: registry.openfuyao.cn/bkeagent-deployer
        tag: v1.1.0
  upgradeAction:
    type: Controller
    config: |
      # 更新 DaemonSet 镜像 tag，等待滚动更新完成
    timeout: 300s
    nodeSelector:
      roles: ["master", "worker", "etcd"]
  healthCheck:
    type: Command
    command: "check-daemonset-ready bkeagent-deployer cluster-system"
    timeout: 300s
  compatibility:
    dependencies:
      - component: bkeProvider
        versionRange: ">=v1.1.0"
  upgradePath:
    fromVersions: ["v1.0.0"]
    preUpgradeCheck: |
      #!/bin/bash
      # 检查所有节点 Agent 状态
```
##### EnsureContainerdUpgrade → ComponentVersion(containerd) 的升级路径
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.2
spec:
  componentName: containerd
  version: v1.7.2
  scope: Node
  source:
    type: HTTP
    url: https://github.com/containerd/containerd/releases/download/v1.7.2/containerd-1.7.2-linux-amd64.tar.gz
    checksum: "sha256:xxxxx"
  upgradeAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      systemctl stop containerd
      cp /usr/local/bin/containerd /usr/local/bin/containerd.bak
      tar -xzf /tmp/containerd.tar.gz -C /usr/local/bin
      systemctl start containerd
      if ! systemctl is-active containerd; then
        mv /usr/local/bin/containerd.bak /usr/local/bin/containerd
        systemctl start containerd
        exit 1
      fi
    timeout: 120s
    nodeSelector:
      roles: ["master", "worker", "etcd"]
  rollbackAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      systemctl stop containerd
      mv /usr/local/bin/containerd.bak /usr/local/bin/containerd
      systemctl start containerd
  healthCheck:
    type: Command
    command: "containerd --version"
    expectedOutput: "containerd github.com/containerd/containerd v1.7.2"
    timeout: 10s
  compatibility:
    os:
      - type: linux
        distros: [ubuntu, centos, debian, kylin, uos]
        versions: ["20.04", "22.04", "7", "8", "9"]
        arch: [amd64, arm64]
    dependencies:
      - component: bkeAgent
        versionRange: ">=v1.0.0"
      - component: kubernetes
        versionRange: ">=v1.27.0"
  upgradePath:
    fromVersions: ["v1.7.0", "v1.7.1"]
```
##### EnsureEtcdUpgrade → ComponentVersion(etcd) 的升级路径
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
spec:
  componentName: etcd
  version: v3.5.12
  scope: Node
  source:
    type: HTTP
    url: https://github.com/etcd-io/etcd/releases/download/v3.5.12/etcd-v3.5.12-linux-amd64.tar.gz
    checksum: "sha256:xxxxx"
  upgradeAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      # 逐节点升级 etcd
      systemctl stop etcd
      cp /usr/local/bin/etcd /usr/local/bin/etcd.bak
      tar -xzf /tmp/etcd.tar.gz -C /usr/local/bin
      systemctl start etcd
      # 等待 etcd 健康
      until etcdctl endpoint health; do sleep 2; done
    timeout: 300s
    nodeSelector:
      roles: ["master", "etcd"]
  rollbackAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      systemctl stop etcd
      mv /usr/local/bin/etcd.bak /usr/local/bin/etcd
      systemctl start etcd
  healthCheck:
    type: Command
    command: "etcdctl endpoint health"
    expectedOutput: "is healthy"
    timeout: 30s
  compatibility:
    dependencies:
      - component: bkeAgent
        versionRange: ">=v1.0.0"
  upgradePath:
    fromVersions: ["v3.5.9", "v3.5.10", "v3.5.11"]
    preUpgradeCheck: |
      #!/bin/bash
      etcdctl endpoint health --cluster
```
##### EnsureMasterUpgrade + EnsureWorkerUpgrade → ComponentVersion(kubernetes) 的升级步骤
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-v1.29.0
spec:
  componentName: kubernetes
  version: v1.29.0
  scope: Node
  source:
    type: HTTP
    packages:
      - name: kubeadm
        version: v1.29.0
        url: https://packages.openfuyao.cn/kubeadm-v1.29.0-linux-amd64
      - name: kubelet
        version: v1.29.0
        url: https://packages.openfuyao.cn/kubelet-v1.29.0-linux-amd64
      - name: kubectl
        version: v1.29.0
        url: https://packages.openfuyao.cn/kubectl-v1.29.0-linux-amd64
  installAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      # Master Init: kubeadm init
      # Master Join: kubeadm join --control-plane
      # Worker Join: kubeadm join
    timeout: 600s
    nodeSelector:
      roles: ["master", "worker"]
  upgradeAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      # 按步骤升级:
      # 1. PreCheck
      # 2. EtcdBackup (master only)
      # 3. MasterRollout (逐节点)
      # 4. WorkerRollout (逐节点)
      # 5. PostCheck
    timeout: 1800s
    nodeSelector:
      roles: ["master", "worker"]
    preCheck:
      type: Script
      script: |
        #!/bin/bash
        kubectl get nodes --no-headers | grep -v Ready
    postCheck:
      type: Script
      script: |
        #!/bin/bash
        kubectl get nodes --no-headers | grep Ready
  rollbackAction:
    type: Script
    script: |
      #!/bin/bash
      set -e
      # 回滚到备份版本
  healthCheck:
    type: Command
    command: "kubelet --version"
    expectedOutput: "Kubernetes v1.29.0"
    timeout: 10s
  compatibility:
    os:
      - type: linux
        distros: [ubuntu, centos, kylin, uos]
        arch: [amd64, arm64]
    kubernetesVersions: [">=v1.27.0"]
    dependencies:
      - component: containerd
        versionRange: ">=v1.6.0"
      - component: etcd
        versionRange: ">=v3.5.9"
  upgradePath:
    fromVersions: ["v1.27.0", "v1.27.5", "v1.28.0", "v1.28.5"]
    preUpgradeCheck: |
      #!/bin/bash
      kubectl get nodes -o wide
      etcdctl endpoint health --cluster
```
##### EnsureComponentUpgrade → ComponentVersion(openFuyao) 的升级路径
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: openfuyao-v1.1.0
spec:
  componentName: openFuyao
  version: v1.1.0
  scope: Cluster
  source:
    type: OCI
    images:
      - name: openfuyao-operator
        repository: registry.openfuyao.cn/openfuyao/operator
        tag: v1.1.0
      - name: openfuyao-apiserver
        repository: registry.openfuyao.cn/openfuyao/apiserver
        tag: v1.1.0
  upgradeAction:
    type: Manifest
    config: |
      # 由 Controller 负责更新各 Deployment 的镜像 tag
    timeout: 600s
  healthCheck:
    type: Command
    command: "check-deployments-ready openfuyao-system"
    timeout: 120s
  compatibility:
    dependencies:
      - component: bkeAgent
        versionRange: ">=v1.0.0"
      - component: kubernetes
        versionRange: ">=v1.27.0"
  upgradePath:
    fromVersions: ["v1.0.0"]
```
##### EnsureWorkerDelete / EnsureMasterDelete → NodeConfig 状态变更
**当前逻辑**：drain 节点 → 删除 Machine → 等待节点移除

**声明式映射**：不映射为 ComponentVersion，而是通过 NodeConfig 的 `status.phase = Deleting` 触发缩容流程：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: NodeConfig
metadata:
  name: node-192.168.1.20
spec:
  roles: ["worker"]
status:
  phase: Deleting  # 触发缩容流程
  lastOperation:
    type: Delete
    component: kubelet
    startTime: "2024-01-15T11:00:00Z"
```
##### EnsureCluster → ComponentVersion 全局健康检查
**声明式映射**：不映射为独立 ComponentVersion，而是作为 ClusterVersion 的全局健康检查步骤。
### 五、组件依赖 DAG（完整版）
```
                    ┌─────────────────┐
                    │  BKEAgent       │ ← 最先安装
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───────┐      │     ┌────────▼───────┐
     │   NodesEnv     │      │     │  ClusterAPI    │
     └────────┬───────┘      │     └────────┬───────┘
              │              │              │
              │              │     ┌────────▼───────┐
              │              │     │    Certs       │
              │              │     └────────┬───────┘
              │              │              │
              │     ┌────────▼──────────────▼───────┐
              │     │       LoadBalancer            │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼──────────────────────┐
              │     │    Kubernetes (Init/Join)     │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼───────┐
              │     │     Addon      │
              │     └────────┬───────┘
              │              │
              │     ┌────────▼──────────┐
              │     │  NodesPostProcess │
              │     └────────┬──────────┘
              │              │
              └──────┬───────┘──────────────┐
                     │                      │
           ┌─────────▼──────────┐  ┌────────▼───────┐
           │   AgentSwitch      │  │  BKEProvider   │ ← 升级阶段
           └────────────────────┘  └────────┬───────┘
                                            │
                                  ┌─────────▼──────────┐
                                  │   BKEAgent(upgrade)│
                                  └─────────┬──────────┘
                                            │
                         ┌──────────────────┼─────────────────┐
                         │                  │                 │
                ┌────────▼───────┐  ┌───────▼──────┐  ┌───────▼────────┐
                │  Containerd    │  │    Etcd      │  │   OpenFuyao    │
                │  (upgrade)     │  │  (upgrade)   │  │  (upgrade)     │
                └────────┬───────┘  └───────┬──────┘  └───────┬────────┘
                         │                  │                 │
                         └──────────┬───────┘─────────────────┘
                                    │
                          ┌─────────▼──────────┐
                          │  Kubernetes        │
                          │  (Master+Worker    │
                          │   upgrade)         │
                          └────────────────────┘
```
### 六、ClusterOrchestrator 调谐器设计
```go
// pkg/orchestrator/cluster_orchestrator.go

type ClusterOrchestrator struct {
    client    client.Client
    scheme    *runtime.Scheme
    scheduler *DAGScheduler
    executors map[ComponentName]ComponentExecutor
}

type ComponentExecutor interface {
    Name() ComponentName
    Scope() ComponentScope
    Install(ctx context.Context, cv *v1alpha1.ComponentVersion, nc *v1alpha1.NodeConfig) (ctrl.Result, error)
    Upgrade(ctx context.Context, cv *v1alpha1.ComponentVersion, nc *v1alpha1.NodeConfig) (ctrl.Result, error)
    Rollback(ctx context.Context, cv *v1alpha1.ComponentVersion, nc *v1alpha1.NodeConfig) error
    HealthCheck(ctx context.Context, cv *v1alpha1.ComponentVersion, nc *v1alpha1.NodeConfig) (bool, error)
}

func (o *ClusterOrchestrator) Reconcile(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // 1. 根据 BKECluster Spec 生成/更新 ComponentVersion 和 NodeConfig 列表
    if err := o.syncDesiredState(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }

    // 2. 获取所有 ComponentVersion
    components, err := o.listComponentVersions(ctx, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 3. DAG 调度：计算就绪、阻塞、运行中的组件
    completed := o.getCompletedComponents(components)
    scheduleResult := o.scheduler.Schedule(components, completed)

    // 4. 执行就绪组件的安装/升级
    for _, comp := range scheduleResult.Ready {
        executor := o.executors[comp.Spec.ComponentName]
        result, err := o.executeComponent(ctx, executor, comp)
        if err != nil {
            return result, err
        }
    }

    // 5. 同步状态回 BKECluster
    if err := o.syncStatusToBKECluster(ctx, bkeCluster, components); err != nil {
        return ctrl.Result{}, err
    }

    // 6. 判断是否全部完成
    if len(scheduleResult.Ready) == 0 && len(scheduleResult.Blocked) == 0 && len(scheduleResult.Running) == 0 {
        return ctrl.Result{}, nil
    }

    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (o *ClusterOrchestrator) syncDesiredState(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) error {
    cluster := bkeCluster.Spec.ClusterConfig.Cluster

    // 生成 ComponentVersion 列表
    desiredComponents := []v1alpha1.ComponentVersion{
        o.buildBKEAgentComponentVersion(cluster),
        o.buildNodesEnvComponentVersion(cluster),
        o.buildClusterAPIComponentVersion(cluster),
        o.buildCertsComponentVersion(cluster),
        o.buildLoadBalancerComponentVersion(cluster),
        o.buildKubernetesComponentVersion(cluster),
        o.buildAddonComponentVersion(cluster),
        o.buildNodesPostProcessComponentVersion(cluster),
        o.buildAgentSwitchComponentVersion(cluster),
    }

    for _, desired := range desiredComponents {
        existing := &v1alpha1.ComponentVersion{}
        err := o.client.Get(ctx, client.ObjectKey{Name: desired.Name, Namespace: desired.Namespace}, existing)
        if apierrors.IsNotFound(err) {
            if err := controllerutil.SetControllerReference(bkeCluster, &desired, o.scheme); err != nil {
                return err
            }
            if err := o.client.Create(ctx, &desired); err != nil {
                return err
            }
        } else if err == nil {
            existing.Spec = desired.Spec
            if err := o.client.Update(ctx, existing); err != nil {
                return err
            }
        }
    }

    // 生成 NodeConfig 列表
    nodes, _ := o.getNodesFromBKECluster(bkeCluster)
    for _, node := range nodes {
        desiredNC := o.buildNodeConfig(bkeCluster, node)
        existingNC := &v1alpha1.NodeConfig{}
        err := o.client.Get(ctx, client.ObjectKey{Name: desiredNC.Name, Namespace: desiredNC.Namespace}, existingNC)
        if apierrors.IsNotFound(err) {
            if err := controllerutil.SetControllerReference(bkeCluster, &desiredNC, o.scheme); err != nil {
                return err
            }
            if err := o.client.Create(ctx, &desiredNC); err != nil {
                return err
            }
        } else if err == nil {
            existingNC.Spec = desiredNC.Spec
            if err := o.client.Update(ctx, existingNC); err != nil {
                return err
            }
        }
    }

    return nil
}

func (o *ClusterOrchestrator) executeComponent(
    ctx context.Context,
    executor ComponentExecutor,
    comp *v1alpha1.ComponentVersion,
) (ctrl.Result, error) {
    switch comp.Status.Phase {
    case "", v1alpha1.ComponentPhasePending:
        // 安装
        var nodeConfigs []*v1alpha1.NodeConfig
        if executor.Scope() == ComponentScopeNode {
            var err error
            nodeConfigs, err = o.getNodeConfigsForComponent(ctx, comp)
            if err != nil {
                return ctrl.Result{}, err
            }
        }
        return executor.Install(ctx, comp, nodeConfigs)

    case v1alpha1.ComponentPhaseInstalling, v1alpha1.ComponentPhaseUpgrading:
        // 继续执行或检查状态
        healthy, err := executor.HealthCheck(ctx, comp, nil)
        if err != nil {
            return ctrl.Result{}, err
        }
        if healthy {
            comp.Status.Phase = v1alpha1.ComponentPhaseReady
            comp.Status.InstalledVersion = comp.Spec.Version
            return ctrl.Result{}, o.client.Status().Update(ctx, comp)
        }
        return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

    case v1alpha1.ComponentPhaseReady:
        // 检查是否需要升级
        if comp.Spec.Version != comp.Status.InstalledVersion {
            comp.Status.Phase = v1alpha1.ComponentPhaseUpgrading
            return ctrl.Result{Requeue: true}, o.client.Status().Update(ctx, comp)
        }
        return ctrl.Result{}, nil

    case v1alpha1.ComponentPhaseFailed:
        // 等待人工干预或自动重试
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    return ctrl.Result{}, nil
}
```
### 七、Kubernetes ComponentVersion 的步骤状态机
Kubernetes 组件（合并了 MasterInit、MasterJoin、WorkerJoin、MasterUpgrade、WorkerUpgrade）是最复杂的组件，需要内部步骤状态机：
```go
// pkg/orchestrator/executor/kubernetes_executor.go

type KubernetesExecutor struct {
    client client.Client
}

type K8sStep string

const (
    K8sStepMasterInit      K8sStep = "MasterInit"
    K8sStepMasterJoin      K8sStep = "MasterJoin"
    K8sStepWorkerJoin      K8sStep = "WorkerJoin"
    K8sStepUpgradePreCheck K8sStep = "UpgradePreCheck"
    K8sStepUpgradeBackup   K8sStep = "UpgradeBackup"
    K8sStepUpgradeMaster   K8sStep = "UpgradeMaster"
    K8sStepUpgradeWorker   K8sStep = "UpgradeWorker"
    K8sStepUpgradePostCheck K8sStep = "UpgradePostCheck"
)

func (e *KubernetesExecutor) Install(
    ctx context.Context,
    cv *v1alpha1.ComponentVersion,
    nodeConfigs []*v1alpha1.NodeConfig,
) (ctrl.Result, error) {
    currentStep := e.getCurrentStep(cv)

    switch currentStep {
    case "", K8sStepMasterInit:
        return e.executeMasterInit(ctx, cv, nodeConfigs)
    case K8sStepMasterJoin:
        return e.executeMasterJoin(ctx, cv, nodeConfigs)
    case K8sStepWorkerJoin:
        return e.executeWorkerJoin(ctx, cv, nodeConfigs)
    }
    return ctrl.Result{}, nil
}

func (e *KubernetesExecutor) Upgrade(
    ctx context.Context,
    cv *v1alpha1.ComponentVersion,
    nodeConfigs []*v1alpha1.NodeConfig,
) (ctrl.Result, error) {
    currentStep := e.getCurrentStep(cv)

    switch currentStep {
    case K8sStepUpgradePreCheck:
        return e.executeUpgradePreCheck(ctx, cv, nodeConfigs)
    case K8sStepUpgradeBackup:
        return e.executeUpgradeBackup(ctx, cv, nodeConfigs)
    case K8sStepUpgradeMaster:
        return e.executeUpgradeMaster(ctx, cv, nodeConfigs)
    case K8sStepUpgradeWorker:
        return e.executeUpgradeWorker(ctx, cv, nodeConfigs)
    case K8sStepUpgradePostCheck:
        return e.executeUpgradePostCheck(ctx, cv, nodeConfigs)
    }
    return ctrl.Result{}, nil
}
```
### 八、控制类 Phase 的处理
控制类 Phase（EnsureFinalizer、EnsurePaused、EnsureClusterManage、EnsureDeleteOrReset、EnsureDryRun）**不映射为 ComponentVersion**，它们是集群生命周期管理操作，保留在 BKECluster Controller 中作为前置/后置逻辑：
```go
// 重构后的 BKECluster Controller Reconcile 流程

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bkeCluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, bkeCluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // === 控制类逻辑（保留在 Controller 中） ===
    // 1. EnsureFinalizer
    if !controllerutil.ContainsFinalizer(bkeCluster, bkev1beta1.ClusterFinalizer) {
        controllerutil.AddFinalizer(bkeCluster, bkev1beta1.ClusterFinalizer)
    }

    // 2. EnsurePaused
    if bkeCluster.Spec.Pause {
        return ctrl.Result{}, nil
    }

    // 3. EnsureDryRun
    if bkeCluster.Spec.DryRun {
        return r.handleDryRun(ctx, bkeCluster)
    }

    // 4. EnsureDeleteOrReset
    if bkeCluster.Spec.Reset || !bkeCluster.DeletionTimestamp.IsZero() {
        return r.handleDeleteOrReset(ctx, bkeCluster)
    }

    // 5. EnsureClusterManage
    if err := r.handleClusterManage(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }

    // === 声明式组件编排（由 ClusterOrchestrator 接管） ===
    return r.orchestrator.Reconcile(ctx, bkeCluster)
}
```
### 九、资源关系总览
```
BKECluster (Owner)
    │
    ├── ClusterVersion (1:1) ─── 全局版本状态
    │
    ├── ComponentVersion[] (1:N) ─── 各组件版本定义
    │   ├── bkeAgent-v1.0.0
    │   ├── nodesEnv-v1.0.0
    │   ├── clusterAPI-v1.0.0
    │   ├── certs-v1.0.0
    │   ├── loadBalancer-v1.0.0
    │   ├── kubernetes-v1.29.0
    │   ├── containerd-v1.7.2
    │   ├── etcd-v3.5.12
    │   ├── addon-v1.0.0
    │   ├── openFuyao-v1.1.0
    │   ├── bkeProvider-v1.1.0
    │   ├── nodesPostProcess-v1.0.0
    │   └── agentSwitch-v1.0.0
    │
    └── NodeConfig[] (1:N) ─── 节点级配置
        ├── node-192.168.1.10 (master)
        ├── node-192.168.1.11 (master)
        ├── node-192.168.1.20 (worker)
        └── node-192.168.1.30 (etcd)
```
### 十、迁移策略
| 阶段 | 目标 | Feature Gate | 验证方式 |
|------|------|-------------|----------|
| **Phase 1** | 定义 CRD + Controller 骨架，不修改现有 PhaseFlow | `DeclarativeOrchestration=false` | CRD 可创建，Controller 可启动 |
| **Phase 2** | 实现 ClusterOrchestrator + DAGScheduler + 各 ComponentExecutor，内部复用现有 Phase 的核心函数 | `DeclarativeOrchestration=true`（可选开启） | 对比新旧路径执行结果 |
| **Phase 3** | 移除 PhaseFlow 中的 DeployPhases 和 PostDeployPhases，完全由声明式编排接管 | `DeclarativeOrchestration=true`（默认开启） | 全量 E2E 测试 |
| **Phase 4** | 清理旧 Phase 代码，统一版本状态到 ClusterVersion | 不可逆 | 代码量减少，架构简化 |
### 十一、关键设计决策总结
1. **控制类 Phase 不映射为 ComponentVersion**：Finalizer、Paused、DryRun、DeleteOrReset、ClusterManage 是集群生命周期控制逻辑，不属于组件安装/升级范畴，保留在 Controller 中。
2. **Kubernetes 组件合并**：MasterInit、MasterJoin、WorkerJoin、MasterUpgrade、WorkerUpgrade 合并为单个 `kubernetes` ComponentVersion，内部通过步骤状态机管理，因为它们操作的是同一组二进制（kubeadm/kubelet/kubectl），只是在不同节点角色上执行不同步骤。
3. **NodeConfig 与 ComponentVersion 分离**：NodeConfig 描述节点的连接信息和期望配置，ComponentVersion 描述组件的安装脚本和版本兼容性。两者通过 `componentName` 关联——NodeConfig 声明"这个节点需要什么版本的组件"，ComponentVersion 声明"这个组件如何安装/升级"。
4. **Scope 区分**：`ComponentScopeNode`（节点级，如 containerd、etcd）需要结合 NodeConfig 执行；`ComponentScopeCluster`（集群级，如 certs、addon）不需要 NodeConfig。
5. **Action 类型**：`Controller` 类型表示由 Controller 代码直接执行（如证书生成、Cluster API 对象创建），`Script` 类型表示通过脚本在节点上执行，`Manifest` 类型表示通过 Helm/YAML 部署。
        
