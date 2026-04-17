# 阶段六：版本包管理 + Asset 框架 — 详细设计
## 1. 当前问题分析
### 1.1 版本信息散布
当前版本信息以硬编码常量和分散的映射表形式存在：

| 位置 | 内容 | 问题 |
|------|------|------|
| [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go) | `DefaultKubernetesVersion = "v1.25.6"`, `DefaultEtcdVersion = "v3.5.21-of.1"`, `DefaultPauseImageTag = "3.9"` | 版本硬编码，新增版本需改代码 |
| [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go) | `GetDefaultEtcdK8sVersionImageMap()`, `GetDefaultPauseK8sVersionImageMap()` | 版本映射表硬编码，Deprecated 但仍在用 |
| [validation.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/validation/validation.go) | `MinSupportedK8sVersion = "v1.21.0"`, `MaxSupportedK8sVersion = "v1.28.0"` | 版本范围硬编码 |
| [exporter.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/imagehelper/exporter.go) | `ImageExporter` 根据 K8s 版本生成镜像列表 | 版本与镜像映射逻辑耦合 |
| [bkecluster_spec.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_spec.go) | `KubernetesVersion`, `EtcdVersion`, `ContainerdVersion`, `OpenFuyaoVersion` 分散字段 | 无版本间兼容性校验 |
### 1.2 资产生成无依赖管理
当前安装流程中各资产的生成顺序和依赖关系是隐式的：
```
当前 Phase 顺序（[list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go)）：
  EnsureFinalizer → EnsureCerts → EnsureClusterAPIObj → EnsureMasterInit → 
  EnsureBKEAgent → EnsureNodesEnv → EnsureLoadBalance → EnsureAgentSwitch
```
**问题：**
- Phase 之间依赖关系隐式定义在列表顺序中
- 无法追踪单个资产的生成状态
- 失败后无法增量重试，只能从头开始
- 无法并行生成无依赖关系的资产
### 1.3 缺少版本兼容性矩阵
当前版本兼容性校验仅做了简单的范围检查：
```go
// validation.go
MinSupportedK8sVersion, _ = semver.ParseTolerant("v1.21.0")
MaxSupportedK8sVersion, _ = semver.ParseTolerant("v1.28.0")
```
**缺少：**
- K8s 版本与 etcd 版本的兼容性
- K8s 版本与 containerd 版本的兼容性
- 升级路径验证（能否从 A 版本升级到 B 版本）
- 组件版本间的交叉依赖
## 2. VersionPackage CRD 设计
### 2.1 CRD 定义
```go
// api/versionpackage/v1beta1/types.go

package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type VersionPackage struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   VersionPackageSpec   `json:"spec,omitempty"`
    Status VersionPackageStatus `json:"status,omitempty"`
}

type VersionPackageSpec struct {
    Version string `json:"version"`

    ReleaseDate string `json:"releaseDate,omitempty"`

    Deprecated bool `json:"deprecated,omitempty"`

    Components []ComponentVersion `json:"components"`

    Images []ImageDefinition `json:"images,omitempty"`

    Compatibility CompatibilityMatrix `json:"compatibility"`

    UpgradePaths []UpgradePath `json:"upgradePaths,omitempty"`

    OSRequirements []OSRequirement `json:"osRequirements,omitempty"`

    Assets []AssetDefinition `json:"assets,omitempty"`
}

type ComponentVersion struct {
    Name    string `json:"name"`
    Version string `json:"version"`
    Image   string `json:"image,omitempty"`
    URL     string `json:"url,omitempty"`
    Checksum string `json:"checksum,omitempty"`
}

type ImageDefinition struct {
    Name       string `json:"name"`
    Repository string `json:"repository"`
    Tag        string `json:"tag"`
    OS         string `json:"os,omitempty"`
    Arch       string `json:"arch,omitempty"`
}

type CompatibilityMatrix struct {
    MinK8sVersion    string              `json:"minK8sVersion"`
    MaxK8sVersion    string              `json:"maxK8sVersion,omitempty"`
    EtcdVersions     []string            `json:"etcdVersions,omitempty"`
    ContainerdVersions []string          `json:"containerdVersions,omitempty"`
    IncompatibleWith []string            `json:"incompatibleWith,omitempty"`
    Dependencies     []ComponentDep      `json:"dependencies,omitempty"`
}

type ComponentDep struct {
    Component  string   `json:"component"`
    MinVersion string   `json:"minVersion"`
    MaxVersion string   `json:"maxVersion,omitempty"`
    Versions   []string `json:"versions,omitempty"`
}

type UpgradePath struct {
    FromVersion string `json:"fromVersion"`
    ToVersion   string `json:"toVersion"`
    Order       []string `json:"order"`
    PreCheck    *UpgradeCheck `json:"preCheck,omitempty"`
    PostCheck   *UpgradeCheck `json:"postCheck,omitempty"`
}

type UpgradeCheck struct {
    Description string `json:"description"`
    Command     string `json:"command,omitempty"`
    Timeout     string `json:"timeout,omitempty"`
}

type OSRequirement struct {
    Name     string   `json:"name"`
    Versions []string `json:"versions"`
    Arch     []string `json:"arch,omitempty"`
}

type AssetDefinition struct {
    Name         string   `json:"name"`
    Type         string   `json:"type"`
    Dependencies []string `json:"dependencies,omitempty"`
    Template     string   `json:"template,omitempty"`
    OutputPath   string   `json:"outputPath,omitempty"`
}

type VersionPackageStatus struct {
    Phase      string             `json:"phase,omitempty"`
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    LoadedAt   *metav1.Time       `json:"loadedAt,omitempty"`
}
```
### 2.2 VersionPackage 示例
```yaml
# config/versionpackage/v1.28.0.yaml
apiVersion: versionpackage.bke.bocloud.com/v1beta1
kind: VersionPackage
metadata:
  name: v1.28.0
  namespace: bke-system
spec:
  version: "1.28.0"
  releaseDate: "2025-06-01"
  deprecated: false
  components:
    - name: kubernetes
      version: "1.28.0"
    - name: etcd
      version: "3.5.21-of.1"
      image: "etcd:3.5.21-of.1"
    - name: pause
      version: "3.9"
      image: "pause:3.9"
    - name: kube-apiserver
      version: "1.28.0"
      image: "kube-apiserver:v1.28.0"
    - name: kube-controller-manager
      version: "1.28.0"
      image: "kube-controller-manager:v1.28.0"
    - name: kube-scheduler
      version: "1.28.0"
      image: "kube-scheduler:v1.28.0"
    - name: containerd
      version: "1.7.8"
      url: "containerd-1.7.8-linux-amd64.tar.gz"
    - name: kubelet
      version: "1.28.0"
      url: "kubelet-v1.28.0-amd64"
  images:
    - name: kube-apiserver
      repository: kubernetes
      tag: "v1.28.0"
    - name: kube-controller-manager
      repository: kubernetes
      tag: "v1.28.0"
    - name: kube-scheduler
      repository: kubernetes
      tag: "v1.28.0"
    - name: etcd
      repository: kubernetes
      tag: "3.5.21-of.1"
    - name: pause
      repository: kubernetes
      tag: "3.9"
  compatibility:
    minK8sVersion: "1.28.0"
    maxK8sVersion: "1.28.x"
    etcdVersions:
      - "3.5.21-of.1"
    containerdVersions:
      - "1.7.8"
      - "1.7.2"
    dependencies:
      - component: etcd
        minVersion: "3.5.6"
      - component: containerd
        minVersion: "1.6.0"
  upgradePaths:
    - fromVersion: "1.25.6"
      toVersion: "1.28.0"
      order:
        - etcd
        - containerd
        - kube-apiserver
        - kube-controller-manager
        - kube-scheduler
        - kubelet
      preCheck:
        description: "Verify cluster health before upgrade"
        timeout: "5m"
      postCheck:
        description: "Verify cluster health after upgrade"
        timeout: "5m"
    - fromVersion: "1.27.0"
      toVersion: "1.28.0"
      order:
        - kube-apiserver
        - kube-controller-manager
        - kube-scheduler
        - kubelet
  osRequirements:
    - name: centos
      versions: ["7", "8", "9"]
      arch: ["amd64", "arm64"]
    - name: ubuntu
      versions: ["20.04", "22.04", "24.04"]
      arch: ["amd64", "arm64"]
    - name: kylin
      versions: ["V10"]
      arch: ["amd64", "arm64"]
    - name: openeuler
      versions: ["20.03", "22.03"]
      arch: ["amd64", "arm64"]
  assets:
    - name: certificates
      type: certs
      dependencies: []
    - name: kubeconfig
      type: kubeconfig
      dependencies:
        - certificates
    - name: kubeadm-config
      type: config
      dependencies:
        - certificates
    - name: static-pods
      type: manifest
      dependencies:
        - certificates
        - kubeadm-config
    - name: kubelet-config
      type: config
      dependencies:
        - kubeadm-config
```
## 3. VersionPackageManager 设计
### 3.1 Manager 接口
```go
// pkg/versionpackage/manager.go

package versionpackage

import (
    "context"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"

    v1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/versionpackage/v1beta1"
)

type Manager interface {
    Load(ctx context.Context, version string) (*v1beta1.VersionPackage, error)
    List(ctx context.Context) ([]*v1beta1.VersionPackage, error)
    Validate(ctx context.Context, pkg *v1beta1.VersionPackage) error
    ValidateUpgrade(ctx context.Context, fromVersion, toVersion string) error
    ValidateCompatibility(ctx context.Context, pkg *v1beta1.VersionPackage, k8sVersion, etcdVersion, containerdVersion string) error
    GetComponentVersion(pkg *v1beta1.VersionPackage, componentName string) (*v1beta1.ComponentVersion, error)
    GetImageList(pkg *v1beta1.VersionPackage, repo string, phase string) (map[string]string, error)
    GetUpgradePath(pkg *v1beta1.VersionPackage, fromVersion string) (*v1beta1.UpgradePath, error)
    GetAssetDefinitions(pkg *v1beta1.VersionPackage) ([]v1beta1.AssetDefinition, error)
}

type versionPackageManager struct {
    client client.Client
    cache  map[string]*v1beta1.VersionPackage
}
```
### 3.2 Manager 实现
```go
// pkg/versionpackage/manager_impl.go

package versionpackage

import (
    "context"
    "fmt"
    "strings"

    "github.com/blang/semver"
    "github.com/pkg/errors"
    "sigs.k8s.io/controller-runtime/pkg/client"

    v1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/versionpackage/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/imagehelper"
    initdefaults "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
)

func NewManager(client client.Client) Manager {
    return &versionPackageManager{
        client: client,
        cache:  make(map[string]*v1beta1.VersionPackage),
    }
}

func (m *versionPackageManager) Load(ctx context.Context, version string) (*v1beta1.VersionPackage, error) {
    if pkg, ok := m.cache[version]; ok {
        return pkg, nil
    }

    pkg := &v1beta1.VersionPackage{}
    if err := m.client.Get(ctx, client.ObjectKey{
        Name:      version,
        Namespace: "bke-system",
    }, pkg); err != nil {
        return nil, errors.Wrapf(err, "failed to load VersionPackage %s", version)
    }

    m.cache[version] = pkg
    return pkg, nil
}

func (m *versionPackageManager) List(ctx context.Context) ([]*v1beta1.VersionPackage, error) {
    list := &v1beta1.VersionPackageList{}
    if err := m.client.List(ctx, list, client.InNamespace("bke-system")); err != nil {
        return nil, err
    }

    result := make([]*v1beta1.VersionPackage, 0, len(list.Items))
    for i := range list.Items {
        item := &list.Items[i]
        m.cache[item.Spec.Version] = item
        result = append(result, item)
    }
    return result, nil
}

func (m *versionPackageManager) Validate(ctx context.Context, pkg *v1beta1.VersionPackage) error {
    if pkg.Spec.Version == "" {
        return errors.New("version must be specified")
    }
    for _, comp := range pkg.Spec.Components {
        if comp.Name == "" || comp.Version == "" {
            return errors.Errorf("component name and version must be specified: %+v", comp)
        }
    }
    return nil
}

func (m *versionPackageManager) ValidateUpgrade(ctx context.Context, fromVersion, toVersion string) error {
    from, err := semver.ParseTolerant(fromVersion)
    if err != nil {
        return errors.Wrapf(err, "invalid from version %s", fromVersion)
    }
    to, err := semver.ParseTolerant(toVersion)
    if err != nil {
        return errors.Wrapf(err, "invalid to version %s", toVersion)
    }

    if to.LTE(from) {
        return errors.Errorf("target version %s must be greater than current version %s", toVersion, fromVersion)
    }

    targetPkg, err := m.Load(ctx, toVersion)
    if err != nil {
        return errors.Wrapf(err, "target version package %s not found", toVersion)
    }

    if targetPkg.Spec.Deprecated {
        return errors.Errorf("target version %s is deprecated", toVersion)
    }

    pathFound := false
    for _, path := range targetPkg.Spec.UpgradePaths {
        if path.FromVersion == fromVersion {
            pathFound = true
            break
        }
    }
    if !pathFound {
        return errors.Errorf("no upgrade path from %s to %s", fromVersion, toVersion)
    }

    return nil
}

func (m *versionPackageManager) ValidateCompatibility(ctx context.Context, pkg *v1beta1.VersionPackage, k8sVersion, etcdVersion, containerdVersion string) error {
    compat := &pkg.Spec.Compatibility

    if k8sVersion != "" {
        k8sV, err := semver.ParseTolerant(k8sVersion)
        if err != nil {
            return errors.Wrapf(err, "invalid kubernetes version %s", k8sVersion)
        }
        if min, err := semver.ParseTolerant(compat.MinK8sVersion); err == nil && k8sV.LT(min) {
            return errors.Errorf("kubernetes version %s is below minimum %s", k8sVersion, compat.MinK8sVersion)
        }
        if compat.MaxK8sVersion != "" {
            if max, err := semver.ParseTolerant(compat.MaxK8sVersion); err == nil && k8sV.GT(max) {
                return errors.Errorf("kubernetes version %s exceeds maximum %s", k8sVersion, compat.MaxK8sVersion)
            }
        }
    }

    if etcdVersion != "" && len(compat.EtcdVersions) > 0 {
        found := false
        for _, v := range compat.EtcdVersions {
            if v == etcdVersion {
                found = true
                break
            }
        }
        if !found {
            return errors.Errorf("etcd version %s is not compatible, supported: %v", etcdVersion, compat.EtcdVersions)
        }
    }

    if containerdVersion != "" && len(compat.ContainerdVersions) > 0 {
        found := false
        for _, v := range compat.ContainerdVersions {
            if v == containerdVersion {
                found = true
                break
            }
        }
        if !found {
            return errors.Errorf("containerd version %s is not compatible, supported: %v", containerdVersion, compat.ContainerdVersions)
        }
    }

    for _, dep := range compat.Dependencies {
        switch dep.Component {
        case "etcd":
            if etcdVersion != "" {
                if err := validateComponentDep("etcd", etcdVersion, dep); err != nil {
                    return err
                }
            }
        case "containerd":
            if containerdVersion != "" {
                if err := validateComponentDep("containerd", containerdVersion, dep); err != nil {
                    return err
                }
            }
        }
    }

    return nil
}

func validateComponentDep(name, version string, dep v1beta1.ComponentDep) error {
    v, err := semver.ParseTolerant(version)
    if err != nil {
        return errors.Wrapf(err, "invalid %s version %s", name, version)
    }
    if dep.MinVersion != "" {
        if min, err := semver.ParseTolerant(dep.MinVersion); err == nil && v.LT(min) {
            return errors.Errorf("%s version %s is below minimum %s", name, version, dep.MinVersion)
        }
    }
    if dep.MaxVersion != "" {
        if max, err := semver.ParseTolerant(dep.MaxVersion); err == nil && v.GT(max) {
            return errors.Errorf("%s version %s exceeds maximum %s", name, version, dep.MaxVersion)
        }
    }
    return nil
}

func (m *versionPackageManager) GetComponentVersion(pkg *v1beta1.VersionPackage, componentName string) (*v1beta1.ComponentVersion, error) {
    for i := range pkg.Spec.Components {
        if pkg.Spec.Components[i].Name == componentName {
            return &pkg.Spec.Components[i], nil
        }
    }
    return nil, errors.Errorf("component %s not found in version package %s", componentName, pkg.Spec.Version)
}

func (m *versionPackageManager) GetImageList(pkg *v1beta1.VersionPackage, repo string, phase string) (map[string]string, error) {
    imageMap := make(map[string]string)

    for _, img := range pkg.Spec.Images {
        fullImage := imagehelper.GetFullImageName(repo, img.Repository+"/"+img.Name, strings.TrimPrefix(img.Tag, "v"))
        imageMap[img.Name] = fullImage
    }

    switch phase {
    case "JoinWorker", "UpgradeWorker":
        if pauseImg, ok := imageMap["pause"]; ok {
            return map[string]string{"pause": pauseImg}, nil
        }
    }

    return imageMap, nil
}

func (m *versionPackageManager) GetUpgradePath(pkg *v1beta1.VersionPackage, fromVersion string) (*v1beta1.UpgradePath, error) {
    for i := range pkg.Spec.UpgradePaths {
        if pkg.Spec.UpgradePaths[i].FromVersion == fromVersion {
            return &pkg.Spec.UpgradePaths[i], nil
        }
    }
    return nil, errors.Errorf("no upgrade path from %s to %s", fromVersion, pkg.Spec.Version)
}

func (m *versionPackageManager) GetAssetDefinitions(pkg *v1beta1.VersionPackage) ([]v1beta1.AssetDefinition, error) {
    if len(pkg.Spec.Assets) == 0 {
        return m.defaultAssetDefinitions(), nil
    }
    return pkg.Spec.Assets, nil
}

func (m *versionPackageManager) defaultAssetDefinitions() []v1beta1.AssetDefinition {
    return []v1beta1.AssetDefinition{
        {Name: "certificates", Type: "certs", Dependencies: []string{}},
        {Name: "kubeconfig", Type: "kubeconfig", Dependencies: []string{"certificates"}},
        {Name: "kubeadm-config", Type: "config", Dependencies: []string{"certificates"}},
        {Name: "static-pods", Type: "manifest", Dependencies: []string{"certificates", "kubeadm-config"}},
        {Name: "kubelet-config", Type: "config", Dependencies: []string{"kubeadm-config"}},
    }
}
```
## 4. Asset 框架设计
### 4.1 Asset 接口
```go
// pkg/asset/asset.go

package asset

import (
    "context"
)

type AssetState string

const (
    AssetStatePending    AssetState = "Pending"
    AssetStateGenerating AssetState = "Generating"
    AssetStateGenerated  AssetState = "Generated"
    AssetStatePersisted  AssetState = "Persisted"
    AssetStateFailed     AssetState = "Failed"
    AssetStateSkipped    AssetState = "Skipped"
)

type Asset interface {
    Name() string
    Type() string
    Dependencies() []string
    Generate(ctx context.Context, deps map[string]*AssetResult) (*AssetResult, error)
    Persist(ctx context.Context, result *AssetResult) error
    Load(ctx context.Context) (*AssetResult, error)
    Exists(ctx context.Context) bool
}

type AssetResult struct {
    Name     string
    Type     string
    Data     map[string][]byte
    Checksum string
    State    AssetState
}
```
### 4.2 Asset Registry 和 DAG 调度器
```go
// pkg/asset/registry.go

package asset

import (
    "fmt"
    "sync"

    "github.com/pkg/errors"
)

type Registry struct {
    mu     sync.RWMutex
    assets map[string]Asset
}

func NewRegistry() *Registry {
    return &Registry{
        assets: make(map[string]Asset),
    }
}

func (r *Registry) Register(asset Asset) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    name := asset.Name()
    if _, exists := r.assets[name]; exists {
        return errors.Errorf("asset %q already registered", name)
    }
    r.assets[name] = asset
    return nil
}

func (r *Registry) Get(name string) (Asset, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    a, ok := r.assets[name]
    return a, ok
}

func (r *Registry) List() []Asset {
    r.mu.RLock()
    defer r.mu.RUnlock()
    result := make([]Asset, 0, len(r.assets))
    for _, a := range r.assets {
        result = append(result, a)
    }
    return result
}

func (r *Registry) BuildDAG() (*DAG, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()

    dag := &DAG{
        nodes: make(map[string]*dagNode),
    }

    for name, asset := range r.assets {
        dag.nodes[name] = &dagNode{
            asset:  asset,
            deps:   asset.Dependencies(),
            state:  NodeStatePending,
        }
    }

    for name, node := range dag.nodes {
        for _, dep := range node.deps {
            if _, exists := dag.nodes[dep]; !exists {
                return nil, errors.Errorf("asset %q depends on %q which is not registered", name, dep)
            }
        }
    }

    if cycle := dag.detectCycle(); cycle != nil {
        return nil, errors.Errorf("circular dependency detected: %v", cycle)
    }

    return dag, nil
}
```
### 4.3 DAG 调度器
```go
// pkg/asset/dag.go

package asset

import (
    "context"
    "fmt"
    "sync"

    "github.com/pkg/errors"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

type NodeState string

const (
    NodeStatePending    NodeState = "Pending"
    NodeStateProcessing NodeState = "Processing"
    NodeStateCompleted  NodeState = "Completed"
    NodeStateFailed     NodeState = "Failed"
    NodeStateSkipped    NodeState = "Skipped"
)

type dagNode struct {
    asset  Asset
    deps   []string
    state  NodeState
    result *AssetResult
    mu     sync.Mutex
}

type DAG struct {
    nodes map[string]*dagNode
}

type DAGExecutionResult struct {
    Results map[string]*AssetResult
    Failed  []string
    Skipped []string
}

func (d *DAG) detectCycle() []string {
    visited := make(map[string]bool)
    stack := make(map[string]bool)
    path := []string{}

    var dfs func(name string) []string
    dfs = func(name string) []string {
        visited[name] = true
        stack[name] = true
        path = append(path, name)

        node := d.nodes[name]
        for _, dep := range node.deps {
            if !visited[dep] {
                if cycle := dfs(dep); cycle != nil {
                    return cycle
                }
            } else if stack[dep] {
                cycleStart := -1
                for i, p := range path {
                    if p == dep {
                        cycleStart = i
                        break
                    }
                }
                return path[cycleStart:]
            }
        }

        path = path[:len(path)-1]
        stack[name] = false
        return nil
    }

    for name := range d.nodes {
        if !visited[name] {
            if cycle := dfs(name); cycle != nil {
                return cycle
            }
        }
    }
    return nil
}

func (d *DAG) topologicalOrder() []string {
    visited := make(map[string]bool)
    order := []string{}

    var dfs func(name string)
    dfs = func(name string) {
        if visited[name] {
            return
        }
        visited[name] = true
        node := d.nodes[name]
        for _, dep := range node.deps {
            dfs(dep)
        }
        order = append(order, name)
    }

    for name := range d.nodes {
        dfs(name)
    }
    return order
}

func (d *DAG) Execute(ctx context.Context, stateStore AssetStateStore) (*DAGExecutionResult, error) {
    order := d.topologicalOrder()
    result := &DAGExecutionResult{
        Results: make(map[string]*AssetResult),
        Failed:  []string{},
        Skipped: []string{},
    }

    depResults := make(map[string]*AssetResult)

    for _, name := range order {
        node := d.nodes[name]

        if stateStore != nil {
            if existing, err := stateStore.Load(ctx, name); err == nil && existing != nil {
                log.Infof("Asset %q already exists, skipping generation", name)
                node.state = NodeStateCompleted
                node.result = existing
                depResults[name] = existing
                result.Results[name] = existing
                continue
            }
        }

        skip := false
        for _, dep := range node.deps {
            if d.nodes[dep].state == NodeStateFailed {
                skip = true
                break
            }
        }
        if skip {
            node.state = NodeStateSkipped
            result.Skipped = append(result.Skipped, name)
            log.Warnf("Asset %q skipped due to failed dependency", name)
            continue
        }

        node.state = NodeStateProcessing
        log.Infof("Generating asset %q (type: %s, deps: %v)", name, node.asset.Type(), node.deps)

        assetResult, err := node.asset.Generate(ctx, depResults)
        if err != nil {
            node.state = NodeStateFailed
            result.Failed = append(result.Failed, name)
            log.Errorf("Failed to generate asset %q: %v", name, err)
            continue
        }

        if stateStore != nil {
            if err := stateStore.Persist(ctx, name, assetResult); err != nil {
                log.Warnf("Failed to persist asset %q state: %v", name, err)
            }
        }

        if err := node.asset.Persist(ctx, assetResult); err != nil {
            node.state = NodeStateFailed
            result.Failed = append(result.Failed, name)
            log.Errorf("Failed to persist asset %q: %v", name, err)
            continue
        }

        node.state = NodeStateCompleted
        node.result = assetResult
        depResults[name] = assetResult
        result.Results[name] = assetResult
        log.Infof("Asset %q generated and persisted successfully", name)
    }

    if len(result.Failed) > 0 {
        return result, errors.Errorf("failed to generate assets: %v", result.Failed)
    }

    return result, nil
}

func (d *DAG) GetReadyAssets() []string {
    var ready []string
    for name, node := range d.nodes {
        allDepsCompleted := true
        for _, dep := range node.deps {
            if d.nodes[dep].state != NodeStateCompleted {
                allDepsCompleted = false
                break
            }
        }
        if allDepsCompleted && node.state == NodeStatePending {
            ready = append(ready, name)
        }
    }
    return ready
}
```
### 4.4 Asset 状态持久化
```go
// pkg/asset/statestore.go

package asset

import (
    "context"
    "encoding/json"
    "fmt"

    corev1 "k8s.io/api/core/v1"
    apierrors "k8s.io/apimachinery/pkg/api/errors"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

type AssetStateStore interface {
    Load(ctx context.Context, assetName string) (*AssetResult, error)
    Persist(ctx context.Context, assetName string, result *AssetResult) error
    Delete(ctx context.Context, assetName string) error
    List(ctx context.Context) (map[string]*AssetResult, error)
}

type configMapStateStore struct {
    client     client.Client
    namespace  string
    clusterName string
}

func NewConfigMapStateStore(client client.Client, namespace, clusterName string) AssetStateStore {
    return &configMapStateStore{
        client:      client,
        namespace:   namespace,
        clusterName: clusterName,
    }
}

func (s *configMapStateStore) configMapName() string {
    return fmt.Sprintf("%s-asset-state", s.clusterName)
}

func (s *configMapStateStore) Load(ctx context.Context, assetName string) (*AssetResult, error) {
    cm := &corev1.ConfigMap{}
    if err := s.client.Get(ctx, client.ObjectKey{
        Name:      s.configMapName(),
        Namespace: s.namespace,
    }, cm); err != nil {
        if apierrors.IsNotFound(err) {
            return nil, nil
        }
        return nil, err
    }

    data, ok := cm.Data[assetName]
    if !ok {
        return nil, nil
    }

    result := &AssetResult{}
    if err := json.Unmarshal([]byte(data), result); err != nil {
        return nil, err
    }

    if result.State == AssetStatePersisted || result.State == AssetStateGenerated {
        return result, nil
    }
    return nil, nil
}

func (s *configMapStateStore) Persist(ctx context.Context, assetName string, result *AssetResult) error {
    cm := &corev1.ConfigMap{}
    err := s.client.Get(ctx, client.ObjectKey{
        Name:      s.configMapName(),
        Namespace: s.namespace,
    }, cm)
    if apierrors.IsNotFound(err) {
        cm = &corev1.ConfigMap{
            ObjectMeta: metav1.ObjectMeta{
                Name:      s.configMapName(),
                Namespace: s.namespace,
            },
            Data: make(map[string]string),
        }
    } else if err != nil {
        return err
    }

    if cm.Data == nil {
        cm.Data = make(map[string]string)
    }

    persistedResult := *result
    persistedResult.State = AssetStatePersisted
    persistedResult.Data = nil

    data, err := json.Marshal(&persistedResult)
    if err != nil {
        return err
    }
    cm.Data[assetName] = string(data)

    if cm.ResourceVersion == "" {
        return s.client.Create(ctx, cm)
    }
    return s.client.Update(ctx, cm)
}

func (s *configMapStateStore) Delete(ctx context.Context, assetName string) error {
    cm := &corev1.ConfigMap{}
    if err := s.client.Get(ctx, client.ObjectKey{
        Name:      s.configMapName(),
        Namespace: s.namespace,
    }, cm); err != nil {
        return err
    }

    delete(cm.Data, assetName)
    return s.client.Update(ctx, cm)
}

func (s *configMapStateStore) List(ctx context.Context) (map[string]*AssetResult, error) {
    cm := &corev1.ConfigMap{}
    if err := s.client.Get(ctx, client.ObjectKey{
        Name:      s.configMapName(),
        Namespace: s.namespace,
    }, cm); err != nil {
        if apierrors.IsNotFound(err) {
            return map[string]*AssetResult{}, nil
        }
        return nil, err
    }

    results := make(map[string]*AssetResult)
    for name, data := range cm.Data {
        result := &AssetResult{}
        if err := json.Unmarshal([]byte(data), result); err != nil {
            continue
        }
        results[name] = result
    }
    return results, nil
}
```
## 5. 内置 Asset 实现
### 5.1 Certificates Asset
```go
// pkg/asset/builtin/certificates/certificates.go

package certificates

import (
    "context"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/asset"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/certs"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

type CertificatesAsset struct {
    generator *certs.BKEKubernetesCertGenerator
}

func NewCertificatesAsset(generator *certs.BKEKubernetesCertGenerator) *CertificatesAsset {
    return &CertificatesAsset{generator: generator}
}

func (a *CertificatesAsset) Name() string         { return "certificates" }
func (a *CertificatesAsset) Type() string          { return "certs" }
func (a *CertificatesAsset) Dependencies() []string { return []string{} }

func (a *CertificatesAsset) Generate(ctx context.Context, deps map[string]*asset.AssetResult) (*asset.AssetResult, error) {
    log.Infof("Generating certificates asset")

    if err := a.generator.LookUpOrGenerate(); err != nil {
        return nil, err
    }

    need, err := a.generator.NeedGenerate()
    if err != nil {
        return nil, err
    }
    if need {
        return &asset.AssetResult{
            Name:  a.Name(),
            Type:  a.Type(),
            State: asset.AssetStateFailed,
        }, nil
    }

    return &asset.AssetResult{
        Name:  a.Name(),
        Type:  a.Type(),
        State: asset.AssetStateGenerated,
    }, nil
}

func (a *CertificatesAsset) Persist(ctx context.Context, result *asset.AssetResult) error {
    log.Infof("Certificates asset persisted (certs are stored in K8s Secrets)")
    return nil
}

func (a *CertificatesAsset) Load(ctx context.Context) (*asset.AssetResult, error) {
    need, err := a.generator.NeedGenerate()
    if err != nil {
        return nil, err
    }
    if need {
        return nil, nil
    }
    return &asset.AssetResult{
        Name:  a.Name(),
        Type:  a.Type(),
        State: asset.AssetStatePersisted,
    }, nil
}

func (a *CertificatesAsset) Exists(ctx context.Context) bool {
    need, err := a.generator.NeedGenerate()
    if err != nil || need {
        return false
    }
    return true
}
```
### 5.2 Kubeconfig Asset
```go
// pkg/asset/builtin/kubeconfig/kubeconfig.go

package kubeconfig

import (
    "context"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/asset"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/certs"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

type KubeconfigAsset struct {
    certGenerator *certs.BKEKubernetesCertGenerator
}

func NewKubeconfigAsset(certGenerator *certs.BKEKubernetesCertGenerator) *KubeconfigAsset {
    return &KubeconfigAsset{certGenerator: certGenerator}
}

func (a *KubeconfigAsset) Name() string         { return "kubeconfig" }
func (a *KubeconfigAsset) Type() string          { return "kubeconfig" }
func (a *KubeconfigAsset) Dependencies() []string { return []string{"certificates"} }

func (a *KubeconfigAsset) Generate(ctx context.Context, deps map[string]*asset.AssetResult) (*asset.AssetResult, error) {
    log.Infof("Generating kubeconfig asset (depends on certificates)")

    if _, ok := deps["certificates"]; !ok {
        return nil, nil
    }

    return &asset.AssetResult{
        Name:  a.Name(),
        Type:  a.Type(),
        State: asset.AssetStateGenerated,
    }, nil
}

func (a *KubeconfigAsset) Persist(ctx context.Context, result *asset.AssetResult) error {
    log.Infof("Kubeconfig asset persisted")
    return nil
}

func (a *KubeconfigAsset) Load(ctx context.Context) (*asset.AssetResult, error) {
    return &asset.AssetResult{
        Name:  a.Name(),
        Type:  a.Type(),
        State: asset.AssetStatePersisted,
    }, nil
}

func (a *KubeconfigAsset) Exists(ctx context.Context) bool {
    return true
}
```
### 5.3 KubeadmConfig Asset
```go
// pkg/asset/builtin/kubeadmconfig/kubeadmconfig.go

package kubeadmconfig

import (
    "context"

    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
    bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/asset"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/versionpackage"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

type KubeadmConfigAsset struct {
    bkeCluster  *bkev1beta1.BKECluster
    vpManager   versionpackage.Manager
}

func NewKubeadmConfigAsset(bkeCluster *bkev1beta1.BKECluster, vpManager versionpackage.Manager) *KubeadmConfigAsset {
    return &KubeadmConfigAsset{
        bkeCluster: bkeCluster,
        vpManager:  vpManager,
    }
}

func (a *KubeadmConfigAsset) Name() string         { return "kubeadm-config" }
func (a *KubeadmConfigAsset) Type() string          { return "config" }
func (a *KubeadmConfigAsset) Dependencies() []string { return []string{"certificates"} }

func (a *KubeadmConfigAsset) Generate(ctx context.Context, deps map[string]*asset.AssetResult) (*asset.AssetResult, error) {
    log.Infof("Generating kubeadm config asset")

    cfg := a.bkeCluster.Spec.ClusterConfig
    if cfg == nil {
        return nil, nil
    }

    k8sVersion := cfg.Cluster.KubernetesVersion
    if k8sVersion == "" {
        k8sVersion = bkeinit.DefaultKubernetesVersion
    }

    pkg, err := a.vpManager.Load(ctx, k8sVersion)
    if err != nil {
        log.Warnf("VersionPackage for %s not found, using defaults: %v", k8sVersion, err)
    }

    if pkg != nil {
        if err := a.vpManager.ValidateCompatibility(ctx, pkg, k8sVersion, cfg.Cluster.EtcdVersion, cfg.Cluster.ContainerdVersion); err != nil {
            return nil, err
        }
    }

    return &asset.AssetResult{
        Name:  a.Name(),
        Type:  a.Type(),
        State: asset.AssetStateGenerated,
    }, nil
}

func (a *KubeadmConfigAsset) Persist(ctx context.Context, result *asset.AssetResult) error {
    log.Infof("Kubeadm config asset persisted")
    return nil
}

func (a *KubeadmConfigAsset) Load(ctx context.Context) (*asset.AssetResult, error) {
    return &asset.AssetResult{
        Name:  a.Name(),
        Type:  a.Type(),
        State: asset.AssetStatePersisted,
    }, nil
}

func (a *KubeadmConfigAsset) Exists(ctx context.Context) bool {
    return true
}
```
## 6. 与状态机（阶段二）的集成
### 6.1 Provisioning 状态中使用 Asset 框架
```go
// pkg/statemachine/states/provisioning.go (重构后)

type ProvisioningState struct {
    BaseState
    vpManager   versionpackage.Manager
    assetReg    *asset.Registry
}

func (s *ProvisioningState) Execute(ctx context.Context) (ClusterLifecycleState, error) {
    bkeCluster := s.GetCluster()

    dag, err := s.assetReg.BuildDAG()
    if err != nil {
        return StateFailed, err
    }

    stateStore := asset.NewConfigMapStateStore(
        s.GetClient(),
        bkeCluster.Namespace,
        bkeCluster.Name,
    )

    result, err := dag.Execute(ctx, stateStore)
    if err != nil {
        return StateFailed, err
    }

    if len(result.Failed) > 0 {
        return StateFailed, nil
    }

    return StateRunning, nil
}
```
### 6.2 与 CVO（阶段四）的集成
```go
// 升级时使用 VersionPackage 的 UpgradePath
func (e *UpgradeExecutor) ExecuteUpgrade(ctx context.Context, fromVersion, toVersion string) error {
    targetPkg, err := e.vpManager.Load(ctx, toVersion)
    if err != nil {
        return err
    }

    if err := e.vpManager.ValidateUpgrade(ctx, fromVersion, toVersion); err != nil {
        return err
    }

    upgradePath, err := e.vpManager.GetUpgradePath(targetPkg, fromVersion)
    if err != nil {
        return err
    }

    for _, componentName := range upgradePath.Order {
        comp, err := e.vpManager.GetComponentVersion(targetPkg, componentName)
        if err != nil {
            return err
        }
        if err := e.upgradeComponent(ctx, componentName, comp); err != nil {
            return err
        }
    }

    return nil
}
```
## 7. 重构 defaults.go — 消除版本硬编码
### 7.1 重构前
```go
// defaults.go (当前)
const (
    DefaultKubernetesVersion = "v1.25.6"
    DefaultEtcdVersion       = "v3.5.21-of.1"
    DefaultEtcdImageTag      = "3.5.21-of.1"
    DefaultPauseImageTag     = "3.9"
)

func GetDefaultEtcdK8sVersionImageMap() map[string]string {
    return map[string]string{
        "v1.21.1":  "etcd:3.4.13-0",
        "v1.25.6":  "etcd:3.5.6-0",
    }
}
```
### 7.2 重构后
```go
// defaults.go (重构后)

func SetDefaultEtcdVersion(obj *v1beta1.Cluster) {
    if obj.EtcdVersion != "" {
        return
    }

    pkg, err := globalVPManager.Load(context.Background(), obj.KubernetesVersion)
    if err == nil {
        if comp, err := globalVPManager.GetComponentVersion(pkg, "etcd"); err == nil {
            obj.EtcdVersion = comp.Version
            return
        }
    }

    obj.EtcdVersion = DefaultEtcdVersion
}

func SetDefaultContainerdVersion(obj *v1beta1.Cluster) {
    if obj.ContainerdVersion != "" {
        return
    }

    pkg, err := globalVPManager.Load(context.Background(), obj.KubernetesVersion)
    if err == nil {
        if comp, err := globalVPManager.GetComponentVersion(pkg, "containerd"); err == nil {
            obj.ContainerdVersion = comp.Version
            return
        }
    }

    obj.ContainerdVersion = DefaultContainerdVersion
}
```
### 7.3 重构 ImageExporter
```go
// imagehelper/exporter.go (重构后)

type ImageExporter struct {
    Repo      string
    Version   string
    vpManager versionpackage.Manager
}

func NewImageExporterWithVP(repo, version string, vpManager versionpackage.Manager) *ImageExporter {
    return &ImageExporter{
        Repo:      repo,
        Version:   version,
        vpManager: vpManager,
    }
}

func (e *ImageExporter) ExportImageMap() (map[string]string, error) {
    if e.vpManager != nil {
        pkg, err := e.vpManager.Load(context.Background(), e.Version)
        if err == nil {
            return e.vpManager.GetImageList(pkg, e.Repo, "")
        }
    }

    return e.fallbackExportImageMap(), nil
}

func (e *ImageExporter) fallbackExportImageMap() (map[string]string, error) {
    return map[string]string{
        "kube-apiserver":          imagehelper.GetFullImageName(e.Repo, "kube-apiserver", e.Version),
        "kube-controller-manager": imagehelper.GetFullImageName(e.Repo, "kube-controller-manager", e.Version),
        "kube-scheduler":          imagehelper.GetFullImageName(e.Repo, "kube-scheduler", e.Version),
        "etcd":                    imagehelper.GetFullImageName(e.Repo, "etcd", initdefaults.DefaultEtcdImageTag),
        "pause":                   imagehelper.GetFullImageName(e.Repo, "pause", initdefaults.DefaultPauseImageTag),
    }, nil
}
```
## 8. 文件目录结构
```
api/versionpackage/v1beta1/
├── types.go                    # VersionPackage CRD 定义
├── zz_generated.deepcopy.go    # DeepCopy 生成
└── groupversion_info.go        # GroupVersion 信息

pkg/versionpackage/
├── manager.go                  # Manager 接口
├── manager_impl.go             # Manager 实现
├── manager_test.go             # Manager 测试
└── defaults.go                 # 版本默认值（从 VersionPackage 加载）

pkg/asset/
├── asset.go                    # Asset 接口定义
├── registry.go                 # Asset 注册表
├── dag.go                      # DAG 调度器
├── statestore.go               # 状态持久化（ConfigMap）
├── dag_test.go                 # DAG 测试
├── registry_test.go            # Registry 测试
└── builtin/
    ├── certificates/
    │   ├── certificates.go     # 证书 Asset
    │   └── certificates_test.go
    ├── kubeconfig/
    │   ├── kubeconfig.go       # Kubeconfig Asset
    │   └── kubeconfig_test.go
    ├── kubeadmconfig/
    │   ├── kubeadmconfig.go    # Kubeadm 配置 Asset
    │   └── kubeadmconfig_test.go
    ├── staticpods/
    │   ├── staticpods.go       # Static Pod 清单 Asset
    │   └── staticpods_test.go
    └── kubeletconfig/
        ├── kubeletconfig.go    # Kubelet 配置 Asset
        └── kubeletconfig_test.go

config/versionpackage/
├── v1.25.6.yaml                # K8s 1.25.6 版本包
├── v1.27.0.yaml                # K8s 1.27.0 版本包
└── v1.28.0.yaml                # K8s 1.28.0 版本包
```
## 9. 迁移策略
### 9.1 迁移步骤
| 步骤 | 内容 | 影响范围 | 风险 |
|------|------|----------|------|
| **Step 1** | 创建 VersionPackage CRD 和 API 类型 | 新增文件 | 低 |
| **Step 2** | 创建版本包 YAML 文件（从 defaults.go 迁移） | 新增文件 | 低 |
| **Step 3** | 实现 VersionPackageManager | 新增文件 | 低 |
| **Step 4** | 创建 Asset 接口和 Registry | 新增文件 | 低 |
| **Step 5** | 实现 DAG 调度器和状态持久化 | 新增文件 | 低 |
| **Step 6** | 实现内置 Asset（certificates, kubeconfig 等） | 新增文件 | 低 |
| **Step 7** | 重构 defaults.go，从 VersionPackage 加载默认值 | 1 文件 | 中 |
| **Step 8** | 重构 ImageExporter，使用 VersionPackage | 1 文件 | 中 |
| **Step 9** | 重构 validation.go，使用 VersionPackage 兼容性矩阵 | 1 文件 | 中 |
| **Step 10** | 在 Provisioning 状态中集成 Asset 框架 | 状态机文件 | 高 |
| **Step 11** | 在 CVO 升级中使用 VersionPackage 升级路径 | CVO 文件 | 高 |
| **Step 12** | 删除 Deprecated 版本映射函数 | defaults.go | 低 |
### 9.2 兼容性保障
1. **Fallback 机制**：VersionPackage 加载失败时回退到硬编码默认值
2. **渐进式迁移**：先添加 VersionPackage 作为可选功能，再逐步替换硬编码
3. **双写验证**：迁移期间同时使用新旧逻辑，对比结果
4. **Feature Gate**：通过 `BKE_USE_VERSIONPACKAGE=true` 控制新旧路径
### 9.3 新增版本支持流程（重构后）
重构后，添加新 K8s 版本（如 v1.29.0）只需：
1. 创建 `config/versionpackage/v1.29.0.yaml`
2. 定义组件版本、镜像列表、兼容性矩阵、升级路径
3. 应用到集群：`kubectl apply -f config/versionpackage/v1.29.0.yaml`

**无需修改任何代码**，完全声明式管理。
## 10. 重构前后对比
| 维度 | 重构前 | 重构后 |
|------|--------|--------|
| **版本定义** | `defaults.go` 硬编码常量 | `VersionPackage` CRD 声明式 |
| **版本兼容性** | `MinSupportedK8sVersion` 范围检查 | `CompatibilityMatrix` 完整矩阵 |
| **升级路径** | Phase 列表隐式定义 | `UpgradePath` 声明式定义 |
| **镜像映射** | `GetDefaultEtcdK8sVersionImageMap()` 硬编码 | `VersionPackage.Spec.Images` 声明式 |
| **资产生成** | Phase 顺序隐式依赖 | DAG 拓扑排序显式依赖 |
| **资产状态** | 无持久化，失败需重头开始 | ConfigMap 持久化，支持断点续传 |
| **并行生成** | Phase 串行执行 | DAG 调度支持并行 |
| **新增版本** | 修改代码 + 重新编译 | 创建 YAML + kubectl apply |
## 11. 与其他阶段的集成关系
```
阶段一 (错误处理 + CRD 扩展)
  └─ ReconcileError 用于 Asset 生成和版本校验的错误分类

阶段二 (状态机引擎)
  └─ Provisioning 状态使用 Asset DAG 生成资产
  └─ Upgrading 状态使用 VersionPackage 升级路径

阶段三 (Provider 分离)
  └─ Bootstrap Provider 使用 Asset 框架生成 Ignition/CloudInit 配置
  └─ ControlPlane Provider 使用 VersionPackage 获取组件版本

阶段四 (CVO 升级)
  └─ CVO 控制器使用 VersionPackage.ValidateUpgrade()
  └─ 升级执行器使用 UpgradePath.Order 编排升级顺序

阶段五 (OS Provider)
  └─ VersionPackage.OSRequirements 定义 OS 兼容性
  └─ Asset 生成时使用 OSProvider 获取平台特定配置
```
这就是阶段六版本包管理 + Asset 框架的完整详细设计。核心思路：
1. **VersionPackage CRD**：声明式定义版本信息，包括组件版本、镜像列表、兼容性矩阵、升级路径和资产定义
2. **VersionPackageManager**：提供版本加载、校验、兼容性检查、镜像列表生成等核心能力
3. **Asset 框架**：通过 DAG 依赖图管理资产生成顺序，支持断点续传和增量重试
4. **状态持久化**：资产生成状态保存到 ConfigMap，失败后可从断点恢复
5. **消除硬编码**：版本信息从代码常量迁移到 CRD，新增版本零代码修改

# 阶段六重构
现在我已经充分了解了代码库的现状和之前的设计。让我来回答用户的三个问题：
1. 阶段六的设计说明（各CRD字段的关键设计）
2. Asset只在Provisioning阶段使用吗？
3. UpgradePath.Order重构为支持DAG的并行设计
## 阶段六设计说明
### 一、VersionPackage CRD 字段关键设计
#### 1.1 核心设计思路
当前代码中版本信息散落在多处：
- [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go#L44-L48) 中硬编码了 `DefaultKubernetesVersion`、`DefaultEtcdVersion`、`DefaultEtcdImageTag`、`DefaultPauseImageTag`
- [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go#L97-L114) 中 `GetDefaultEtcdK8sVersionImageMap()` 和 `GetDefaultPauseK8sVersionImageMap()` 硬编码了版本映射
- [validation.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/validation/validation.go#L35-L37) 中硬编码了 `MinSupportedK8sVersion`、`MaxSupportedK8sVersion`
- [exporter.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/imagehelper/exporter.go#L130-L145) 中硬编码了组件镜像映射关系
- [bkecluster_spec.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_spec.go#L142-L152) 中 `KubernetesVersion`、`EtcdVersion`、`ContainerdVersion`、`OpenFuyaoVersion` 散落在 Cluster 结构体中

**VersionPackage 的核心目标是：将这些散落的版本信息统一收归到一个声明式 CRD 中管理。**
#### 1.2 VersionPackage CRD 字段设计
```go
// api/versionpackage/v1beta1/types.go

type VersionPackageSpec struct {
    // ===== 核心标识 =====
    // Version: 语义化版本号，如 "v2.0.0"，对应整个解决方案版本
    // 设计要点：与 SolutionVersion 对齐，一个 VersionPackage 代表一个完整可部署的版本
    Version string `json:"version"`
    
    // ReleaseDate: 发布日期，用于版本排序和生命周期管理
    ReleaseDate *metav1.Time `json:"releaseDate,omitempty"`
    
    // Deprecated: 标记版本是否已废弃，废弃版本不允许新集群部署
    // 但允许已部署的集群继续运行和升级
    Deprecated bool `json:"deprecated,omitempty"`
    
    // ===== 组件版本 =====
    // Components: 声明该版本包含的所有组件及其版本
    // 设计要点：替代 defaults.go 中的硬编码常量和映射表
    // 每个组件有独立的 Type 标识，用于 Asset 框架的依赖解析
    Components []ComponentVersion `json:"components"`
    
    // ===== 镜像定义 =====
    // Images: 声明该版本需要的所有容器镜像
    // 设计要点：替代 exporter.go 中的硬编码镜像映射
    // 通过 Component 字段关联到具体组件，支持按组件过滤镜像
    Images []ImageDefinition `json:"images,omitempty"`
    
    // ===== 兼容性矩阵 =====
    // Compatibility: 声明版本间的兼容性约束
    // 设计要点：替代 validation.go 中的硬编码版本范围
    // 支持向前兼容声明和组件间兼容性校验
    Compatibility CompatibilityMatrix `json:"compatibility"`
    
    // ===== 升级路径 =====
    // UpgradePaths: 声明从哪些版本可以升级到当前版本
    // 设计要点：重构为 DAG 结构，支持并行升级（详见第三节）
    UpgradePaths []UpgradePath `json:"upgradePaths,omitempty"`
    
    // ===== OS 要求 =====
    // OSRequirements: 声明该版本对操作系统的要求
    // 设计要点：与阶段五的 OSProvider 对接
    OSRequirements []OSRequirement `json:"osRequirements,omitempty"`
    
    // ===== Asset 定义 =====
    // Assets: 声明该版本需要生成的所有 Asset
    // 设计要点：与 Asset 框架对接，定义 Asset 的生成配置
    Assets []AssetDefinition `json:"assets,omitempty"`
}
```
#### 1.3 关键子结构设计
**ComponentVersion** — 替代散落的版本常量
```go
type ComponentVersion struct {
    // Name: 组件名称，如 "kubernetes", "etcd", "containerd", "pause"
    // 设计要点：与 Asset 框架中的 Asset.Name 对应，建立组件-Asset 关联
    Name string `json:"name"`
    
    // Version: 组件版本号
    Version string `json:"version"`
    
    // Type: 组件类型，用于分类和过滤
    // "core" = 核心组件(k8s,etcd), "runtime" = 运行时(containerd), "addon" = 附加组件
    Type ComponentType `json:"type,omitempty"`
    
    // Critical: 是否关键组件，关键组件升级失败触发回滚
    Critical bool `json:"critical,omitempty"`
}
```
**ImageDefinition** — 替代 exporter.go 中的硬编码映射
```go
type ImageDefinition struct {
    // Name: 镜像名称，如 "kube-apiserver", "etcd", "pause"
    Name string `json:"name"`
    
    // Tag: 镜像标签，如 "v1.25.6", "3.5.6-0"
    // 设计要点：不再依赖 K8s 版本推算，而是显式声明
    Tag string `json:"tag"`
    
    // Component: 关联的组件名称，用于按组件过滤镜像
    // 如 etcd 镜像关联到 "etcd" 组件
    Component string `json:"component,omitempty"`
    
    // Archs: 支持的架构列表
    Archs []string `json:"archs,omitempty"`
    
    // Digest: 镜像摘要，用于离线环境验证
    Digest string `json:"digest,omitempty"`
}
```
**CompatibilityMatrix** — 替代 validation.go 中的硬编码范围
```go
type CompatibilityMatrix struct {
    // K8sVersionRange: 支持的 K8s 版本范围
    // 设计要点：替代 MinSupportedK8sVersion / MaxSupportedK8sVersion
    K8sVersionRange *VersionRange `json:"k8sVersionRange,omitempty"`
    
    // ComponentCompat: 组件间兼容性约束
    // 设计要点：替代 GetDefaultEtcdK8sVersionImageMap() 中的隐式兼容关系
    // 显式声明如 "etcd 3.5.6 兼容 k8s [1.23, 1.26)"
    ComponentCompat []ComponentCompatibility `json:"componentCompat,omitempty"`
    
    // MinNodeOSVersion: 最低节点 OS 版本要求
    MinNodeOSVersion map[string]string `json:"minNodeOSVersion,omitempty"`
}

type VersionRange struct {
    Min string `json:"min"` // 如 "v1.21.0"
    Max string `json:"max,omitempty"` // 如 "v1.28.0"，空表示无上限
}

type ComponentCompatibility struct {
    Component   string       `json:"component"`   // 如 "etcd"
    Version     string       `json:"version"`     // 如 "3.5.6-0"
    Compatible  []VersionRange `json:"compatible"` // 如 [{Min: "v1.23.0", Max: "v1.26.0"}]
}
```
#### 1.4 VersionPackageStatus 设计
```go
type VersionPackageStatus struct {
    // Phase: 版本包的当前阶段
    // "Available" = 可用, "Deprecated" = 已废弃, "EOL" = 终止支持
    Phase VersionPackagePhase `json:"phase,omitempty"`
    
    // ActiveClusters: 当前使用此版本部署的集群数量
    ActiveClusters int `json:"activeClusters,omitempty"`
    
    // Conditions: 标准条件列表
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    
    // ObservedGeneration: 观察到的代次
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
```
### 二、Asset 是否只在 Provisioning 阶段使用？
**答案：不是。Asset 贯穿集群的整个生命周期，在 Provisioning、Upgrading、Scaling 三个阶段都有使用。**
#### 2.1 当前代码中 Asset 的隐式存在
从 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) 可以看到，当前 Phase 列表分为三类：

| 阶段 | Phase 列表 | 隐式 Asset |
|------|-----------|-----------|
| **Provisioning** | EnsureCerts → EnsureClusterAPIObj → EnsureMasterInit → EnsureMasterJoin → EnsureWorkerJoin → EnsureAddonDeploy | 证书、kubeconfig、etcd 配置、静态 Pod manifest、Addon chart |
| **Upgrading** | EnsureAgentUpgrade → EnsureContainerdUpgrade → EnsureEtcdUpgrade → EnsureMasterUpgrade → EnsureWorkerUpgrade → EnsureComponentUpgrade | 升级后的证书轮换、新版本 manifest、新组件配置 |
| **Scaling** | EnsureMasterJoin / EnsureMasterDelete / EnsureWorkerJoin / EnsureWorkerDelete | 新节点的证书、kubeconfig、kubelet 配置 |

从 [certs/generator.go](file:///d:/code/github/cluster-api-provider-bke/pkg/certs/generator.go) 可以看到，证书生成在 Provisioning 和 Upgrade（证书轮换）时都会触发。
#### 2.2 Asset 在各阶段的使用场景
```
┌──────────────────────────────────────────────────────────────┐
│                    Asset 生命周期覆盖                        │
├──────────────┬───────────────────────────────────────────────┤
│   阶段       │   使用的 Asset                                │
├──────────────┼───────────────────────────────────────────────┤
│ Provisioning │ CA证书 → 组件证书 → kubeconfig → etcd配置     │
│              │ → static-pod-manifest → kubelet-config        │
│              │ → addon-chart → network-config                │
├──────────────┼───────────────────────────────────────────────┤
│ Upgrading    │ 新版本manifest → 证书轮换 → etcd迁移配置      │
│              │ → 组件升级配置 → 新版本kubelet-config         │
│              │ → 新版本addon-chart                           │
├──────────────┼───────────────────────────────────────────────┤
│ Scaling      │ 新节点证书 → 新节点kubeconfig                 │
│              │ → 新节点kubelet-config → LB配置更新           │
├──────────────┼───────────────────────────────────────────────┤
│ Deleting     │ 清理Asset持久化状态 → 撤销证书                │
└──────────────┴───────────────────────────────────────────────┘
```
#### 2.3 Asset 接口设计（支持全生命周期）
```go
// pkg/asset/asset.go

type AssetPhase string

const (
    AssetPhaseProvision AssetPhase = "provision"
    AssetPhaseUpgrade   AssetPhase = "upgrade"
    AssetPhaseScale     AssetPhase = "scale"
    AssetPhaseDelete    AssetPhase = "delete"
)

type Asset interface {
    Name() string
    Type() string
    
    // Dependencies: 声明依赖的其他 Asset
    // DAG 调度器据此构建依赖图
    Dependencies() []string
    
    // ApplicablePhases: 声明该 Asset 在哪些生命周期阶段生效
    // 如 CA 证书只在 provision 和 upgrade(轮换) 阶段生成
    ApplicablePhases() []AssetPhase
    
    // Generate: 生成 Asset 数据
    // deps 是已完成的依赖 Asset 的结果
    Generate(ctx context.Context, deps map[string]*AssetResult) (*AssetResult, error)
    
    // Persist: 持久化 Asset 结果到 ConfigMap/Secret
    Persist(ctx context.Context, result *AssetResult) error
    
    // Load: 从持久化存储加载 Asset 结果
    // 用于断点续传和 Upgrade 阶段复用已有 Asset
    Load(ctx context.Context) (*AssetResult, error)
    
    // Exists: 检查 Asset 是否已存在
    Exists(ctx context.Context) bool
    
    // Cleanup: 清理 Asset（Delete 阶段使用）
    Cleanup(ctx context.Context) error
}
```
#### 2.4 Asset 与状态机的集成
```go
// 状态机中 Asset 的使用方式
func (sm *ClusterStateMachine) handleProvisioning(ctx context.Context) (State, error) {
    // 只执行 ApplicablePhases 包含 "provision" 的 Asset
    assets := sm.assetRegistry.GetByPhase(AssetPhaseProvision)
    return sm.assetScheduler.Execute(ctx, assets)
}

func (sm *ClusterStateMachine) handleUpgrading(ctx context.Context) (State, error) {
    // 执行 ApplicablePhases 包含 "upgrade" 的 Asset
    // 已存在的 Asset 通过 Load 复用，不需要重新生成
    assets := sm.assetRegistry.GetByPhase(AssetPhaseUpgrade)
    return sm.assetScheduler.Execute(ctx, assets)
}

func (sm *ClusterStateMachine) handleScaling(ctx context.Context) (State, error) {
    // 执行 ApplicablePhases 包含 "scale" 的 Asset
    // 如新节点的证书和 kubeconfig
    assets := sm.assetRegistry.GetByPhase(AssetPhaseScale)
    return sm.assetScheduler.Execute(ctx, assets)
}
```
### 三、UpgradePath 重构为 DAG 并行设计
#### 3.1 当前问题
refactor.md 中的 `UpgradePath.UpgradeOrder` 是顺序执行的：
```go
// 当前设计：顺序执行
type UpgradePathSpec struct {
    UpgradeOrder []LayerUpgrade `json:"upgradeOrder"`  // 只能按数组顺序执行
}

// 执行器也是顺序遍历
for _, layerUpgrade := range upgradePath.Spec.UpgradeOrder {
    e.executeLayerUpgrade(ctx, layerUpgrade, ...)
}
```
**问题：**
1. **无法并行**：bootstrap → management → workload 必须顺序执行，但同一层内的多个组件可以并行
2. **无法表达复杂依赖**：如 etcd 升级依赖证书轮换完成，但 kube-apiserver 升级同时依赖 etcd 和证书
3. **无法条件分支**：某些升级步骤只在特定条件下需要执行
#### 3.2 DAG 并行设计
**核心思想：将 UpgradePath 从线性列表重构为 DAG（有向无环图），支持节点级并行和层级并行。**
```go
// api/versionpackage/v1beta1/upgrade_dag.go

// UpgradeDAG 定义升级的有向无环图
type UpgradeDAG struct {
    // Nodes: DAG 中的所有节点
    Nodes []UpgradeNode `json:"nodes"`
    
    // Edges: DAG 中的所有边（依赖关系）
    Edges []UpgradeEdge `json:"edges"`
    
    // EntryPoints: DAG 入口节点（无前驱的节点）
    // 调度器从这些节点开始执行
    EntryPoints []string `json:"entryPoints"`
    
    // ExitPoints: DAG 出口节点（无后继的节点）
    // 所有出口节点完成才算升级完成
    ExitPoints []string `json:"exitPoints"`
}

// UpgradeNode 定义 DAG 中的一个升级节点
type UpgradeNode struct {
    // ID: 节点唯一标识，如 "etcd-upgrade", "cert-rotation"
    ID string `json:"id"`
    
    // Name: 可读名称
    Name string `json:"name"`
    
    // Type: 节点类型
    // "component" = 组件升级, "check" = 健康检查, "backup" = 备份, "config" = 配置更新
    Type UpgradeNodeType `json:"type"`
    
    // Component: 关联的组件名称（与 VersionPackage.Components.Name 对应）
    Component string `json:"component,omitempty"`
    
    // Layer: 所属集群层级
    // "bootstrap" | "management" | "workload"
    Layer string `json:"layer,omitempty"`
    
    // Action: 节点执行的动作
    Action UpgradeAction `json:"action"`
    
    // Timeout: 节点执行超时时间
    Timeout metav1.Duration `json:"timeout,omitempty"`
    
    // RetryPolicy: 重试策略
    RetryPolicy *RetryPolicy `json:"retryPolicy,omitempty"`
    
    // RollbackAction: 回滚动作
    RollbackAction *UpgradeAction `json:"rollbackAction,omitempty"`
    
    // Condition: 执行条件（CEL 表达式）
    // 如 "cluster.etcdVersion != targetVersion" 表示只在 etcd 版本不匹配时执行
    Condition string `json:"condition,omitempty"`
    
    // Critical: 是否关键节点
    // 关键节点失败触发整条路径回滚
    Critical bool `json:"critical,omitempty"`
    
    // ParallelGroup: 并行组标识
    // 同一并行组内的节点可以同时执行
    // 不同并行组之间按依赖关系排序
    ParallelGroup string `json:"parallelGroup,omitempty"`
}

// UpgradeEdge 定义 DAG 中的边
type UpgradeEdge struct {
    // From: 边的起始节点 ID
    From string `json:"from"`
    
    // To: 边的目标节点 ID
    To string `json:"to"`
    
    // Type: 边的类型
    // "hard" = 硬依赖（必须等待 From 完成）, "soft" = 软依赖（From 失败不阻塞 To）
    Type EdgeType `json:"type"`
    
    // Condition: 边的条件（CEL 表达式）
    // 如 "from.status == 'succeeded'" 表示只在 From 成功时才激活此依赖
    Condition string `json:"condition,omitempty"`
}

type UpgradeNodeType string

const (
    UpgradeNodeComponent UpgradeNodeType = "component"
    UpgradeNodeCheck     UpgradeNodeType = "check"
    UpgradeNodeBackup    UpgradeNodeType = "backup"
    UpgradeNodeConfig    UpgradeNodeType = "config"
)

type EdgeType string

const (
    EdgeHard EdgeType = "hard"
    EdgeSoft EdgeType = "soft"
)

// UpgradeAction 定义升级动作
type UpgradeAction struct {
    // Type: 动作类型
    // "image-rollout" = 滚动更新镜像, "config-apply" = 应用配置, "script" = 执行脚本
    Type ActionType `json:"type"`
    
    // Image: 新镜像（image-rollout 类型使用）
    Image string `json:"image,omitempty"`
    
    // Config: 配置内容（config-apply 类型使用）
    Config *runtime.RawExtension `json:"config,omitempty"`
    
    // Script: 脚本路径（script 类型使用）
    Script string `json:"script,omitempty"`
    
    // Strategy: 执行策略
    Strategy UpgradeStrategy `json:"strategy,omitempty"`
}

type UpgradeStrategy struct {
    // MaxUnavailable: 最大不可用数（滚动更新时使用）
    MaxUnavailable intstr.IntOrString `json:"maxUnavailable,omitempty"`
    
    // MaxSurge: 最大激增数
    MaxSurge intstr.IntOrString `json:"maxSurge,omitempty"`
    
    // PauseBetweenNodes: 节点间暂停时间
    PauseBetweenNodes metav1.Duration `json:"pauseBetweenNodes,omitempty"`
}

type RetryPolicy struct {
    MaxRetries int              `json:"maxRetries"`
    Delay      metav1.Duration  `json:"delay"`
}
```
#### 3.3 UpgradePath 重构
```go
// 重构后的 UpgradePath
type UpgradePath struct {
    // FromVersion: 源版本
    FromVersion string `json:"fromVersion"`
    
    // ToVersion: 目标版本
    ToVersion string `json:"toVersion"`
    
    // Type: 升级类型
    // "solution" = 整体升级, "component" = 单组件升级, "patch" = 补丁升级
    Type UpgradePathType `json:"type"`
    
    // DAG: 升级执行的有向无环图
    // 替代原来的 UpgradeOrder []LayerUpgrade
    DAG UpgradeDAG `json:"dag"`
    
    // PreCheckDAG: 升级前检查的 DAG
    // 独立于主 DAG，在主 DAG 执行前完成
    PreCheckDAG *UpgradeDAG `json:"preCheckDag,omitempty"`
    
    // PostCheckDAG: 升级后验证的 DAG
    // 独立于主 DAG，在主 DAG 执行后完成
    PostCheckDAG *UpgradeDAG `json:"postCheckDag,omitempty"`
    
    // RollbackDAG: 回滚 DAG
    // 主 DAG 中关键节点失败时执行
    RollbackDAG *UpgradeDAG `json:"rollbackDag,omitempty"`
}
```
#### 3.4 DAG 调度器实现
```go
// pkg/upgrade/dag_scheduler.go

type DAGScheduler struct {
    nodes    map[string]*UpgradeNode
    edges    []UpgradeEdge
    inDegree map[string]int
    nodeStatus map[string]NodeExecutionStatus
    mu       sync.Mutex
}

type NodeExecutionStatus string

const (
    NodePending   NodeExecutionStatus = "pending"
    NodeRunning   NodeExecutionStatus = "running"
    NodeSucceeded NodeExecutionStatus = "succeeded"
    NodeFailed    NodeExecutionStatus = "failed"
    NodeSkipped   NodeExecutionStatus = "skipped"
    NodeRollback  NodeExecutionStatus = "rollback"
)

func NewDAGScheduler(dag *UpgradeDAG) (*DAGScheduler, error) {
    s := &DAGScheduler{
        nodes:      make(map[string]*UpgradeNode),
        inDegree:   make(map[string]int),
        nodeStatus: make(map[string]NodeExecutionStatus),
    }
    
    for i := range dag.Nodes {
        node := &dag.Nodes[i]
        s.nodes[node.ID] = node
        s.inDegree[node.ID] = 0
        s.nodeStatus[node.ID] = NodePending
    }
    
    for _, edge := range dag.Edges {
        s.edges = append(s.edges, edge)
        s.inDegree[edge.To]++
    }
    
    if err := s.validateDAG(); err != nil {
        return nil, err
    }
    
    return s, nil
}

// Execute 并行执行 DAG
func (s *DAGScheduler) Execute(ctx context.Context, executor NodeExecutor) error {
    // 初始化：找到所有入度为 0 的节点作为初始可执行集合
    ready := s.getReadyNodes()
    
    errCh := make(chan error, len(s.nodes))
    var wg sync.WaitGroup
    
    for {
        if len(ready) == 0 {
            break
        }
        
        // 并行执行所有就绪节点
        for _, nodeID := range ready {
            wg.Add(1)
            go func(id string) {
                defer wg.Done()
                
                node := s.nodes[id]
                
                // 检查执行条件
                if node.Condition != "" {
                    if !evaluateCondition(node.Condition, s.nodeStatus) {
                        s.updateStatus(id, NodeSkipped)
                        return
                    }
                }
                
                s.updateStatus(id, NodeRunning)
                
                // 执行节点
                err := executor.Execute(ctx, node)
                if err != nil {
                    if node.Critical {
                        // 关键节点失败，触发回滚
                        s.updateStatus(id, NodeFailed)
                        errCh <- fmt.Errorf("critical node %s failed: %w", id, err)
                        return
                    }
                    // 非关键节点失败，检查边类型
                    s.updateStatus(id, NodeFailed)
                    if !s.hasSoftEdgeOnly(id) {
                        errCh <- fmt.Errorf("node %s failed: %w", id, err)
                        return
                    }
                    return
                }
                
                s.updateStatus(id, NodeSucceeded)
            }(nodeID)
        }
        
        wg.Wait()
        
        // 检查是否有失败
        select {
        case err := <-errCh:
            return s.handleFailure(ctx, executor, err)
        default:
        }
        
        // 计算下一轮就绪节点
        ready = s.getReadyNodes()
    }
    
    return nil
}

// getReadyNodes 获取当前可执行的节点（所有硬依赖已完成）
func (s *DAGScheduler) getReadyNodes() []string {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    var ready []string
    for id, degree := range s.inDegree {
        if degree == 0 && s.nodeStatus[id] == NodePending {
            // 检查所有入边对应的源节点是否已完成
            allDepsMet := true
            for _, edge := range s.edges {
                if edge.To == id {
                    if s.nodeStatus[edge.From] != NodeSucceeded {
                        if edge.Type == EdgeHard {
                            allDepsMet = false
                            break
                        }
                        // soft edge: 源节点失败也继续
                    }
                }
            }
            if allDepsMet {
                ready = append(ready, id)
            }
        }
    }
    
    // 标记就绪节点的入度为 -1（避免重复调度）
    for _, id := range ready {
        s.inDegree[id] = -1
        // 减少后继节点的入度
        for _, edge := range s.edges {
            if edge.From == id {
                s.inDegree[edge.To]--
            }
        }
    }
    
    return ready
}

// handleFailure 处理失败：执行回滚 DAG
func (s *DAGScheduler) handleFailure(ctx context.Context, executor NodeExecutor, failErr error) error {
    // 找到已成功的关键节点，逆序回滚
    var completedCritical []string
    for id, status := range s.nodeStatus {
        if status == NodeSucceeded {
            if node, ok := s.nodes[id]; ok && node.RollbackAction != nil {
                completedCritical = append(completedCritical, id)
            }
        }
    }
    
    // 逆序执行回滚
    for i := len(completedCritical) - 1; i >= 0; i-- {
        id := completedCritical[i]
        node := s.nodes[id]
        if err := executor.Rollback(ctx, node); err != nil {
            return fmt.Errorf("rollback failed for %s: %w (original error: %v)", id, err, failErr)
        }
    }
    
    return failErr
}

// validateDAG 验证 DAG 无环
func (s *DAGScheduler) validateDAG() error {
    visited := make(map[string]bool)
    visiting := make(map[string]bool)
    
    var dfs func(id string) error
    dfs = func(id string) error {
        if visiting[id] {
            return fmt.Errorf("cycle detected at node %s", id)
        }
        if visited[id] {
            return nil
        }
        visiting[id] = true
        for _, edge := range s.edges {
            if edge.From == id {
                if err := dfs(edge.To); err != nil {
                    return err
                }
            }
        }
        visiting[id] = false
        visited[id] = true
        return nil
    }
    
    for id := range s.nodes {
        if err := dfs(id); err != nil {
            return err
        }
    }
    return nil
}
```
#### 3.5 DAG 示例：K8s 从 v1.25.6 升级到 v1.27.0
```yaml
apiVersion: versionpackage.bke.bocloud.com/v1beta1
kind: VersionPackage
metadata:
  name: v2.1.0
spec:
  version: v2.1.0
  components:
    - name: kubernetes
      version: v1.27.0
      type: core
      critical: true
    - name: etcd
      version: v3.5.8-0
      type: core
      critical: true
    - name: containerd
      version: "1.6.24"
      type: runtime
  images:
    - name: kube-apiserver
      tag: v1.27.0
      component: kubernetes
    - name: etcd
      tag: 3.5.8-0
      component: etcd
  upgradePaths:
    - fromVersion: v2.0.0
      toVersion: v2.1.0
      type: solution
      dag:
        nodes:
          # ===== Pre-check 阶段 =====
          - id: health-check
            name: 集群健康检查
            type: check
            action:
              type: script
              script: /upgrade/pre-check.sh
            timeout: 300s
            critical: true
          
          # ===== 备份阶段 =====
          - id: etcd-backup
            name: ETCD 数据备份
            type: backup
            layer: management
            action:
              type: script
              script: /upgrade/etcd-backup.sh
            timeout: 600s
            critical: true
          
          # ===== 证书阶段（可与备份并行） =====
          - id: cert-rotation
            name: 证书轮换
            type: config
            action:
              type: config-apply
            timeout: 120s
            parallelGroup: phase1
          
          # ===== 核心组件升级阶段 =====
          - id: etcd-upgrade
            name: ETCD 升级
            type: component
            component: etcd
            layer: management
            action:
              type: image-rollout
              image: etcd:3.5.8-0
              strategy:
                maxUnavailable: 1
            timeout: 900s
            critical: true
            rollbackAction:
              type: image-rollout
              image: etcd:3.5.6-0
          
          - id: containerd-upgrade
            name: Containerd 升级
            type: component
            component: containerd
            action:
              type: script
              script: /upgrade/containerd-upgrade.sh
              strategy:
                maxUnavailable: 1
                pauseBetweenNodes: 30s
            timeout: 1800s
            parallelGroup: component-upgrade  # 与 etcd 无依赖，可并行
          
          # ===== 控制面升级（依赖 etcd + 证书） =====
          - id: api-server-upgrade
            name: API Server 升级
            type: component
            component: kubernetes
            layer: management
            action:
              type: image-rollout
              image: kube-apiserver:v1.27.0
            timeout: 600s
            critical: true
            parallelGroup: control-plane  # 三个组件可并行
          
          - id: controller-manager-upgrade
            name: Controller Manager 升级
            type: component
            component: kubernetes
            layer: management
            action:
              type: image-rollout
              image: kube-controller-manager:v1.27.0
            timeout: 600s
            parallelGroup: control-plane
          
          - id: scheduler-upgrade
            name: Scheduler 升级
            type: component
            component: kubernetes
            layer: management
            action:
              type: image-rollout
              image: kube-scheduler:v1.27.0
            timeout: 600s
            parallelGroup: control-plane
          
          # ===== Worker 升级 =====
          - id: worker-upgrade
            name: Worker 节点升级
            type: component
            component: kubernetes
            layer: workload
            action:
              type: image-rollout
              image: kubelet:v1.27.0
              strategy:
                maxUnavailable: "10%"
                pauseBetweenNodes: 60s
            timeout: 3600s
          
          # ===== 验证阶段 =====
          - id: post-check
            name: 升级后验证
            type: check
            action:
              type: script
              script: /upgrade/post-check.sh
            timeout: 300s
            critical: true
        
        edges:
          # 健康检查 → 备份
          - from: health-check
            to: etcd-backup
            type: hard
          
          # 健康检查 → 证书轮换（并行路径）
          - from: health-check
            to: cert-rotation
            type: hard
          
          # 备份 + 证书 → etcd 升级
          - from: etcd-backup
            to: etcd-upgrade
            type: hard
          - from: cert-rotation
            to: etcd-upgrade
            type: hard
          
          # 健康检查 → containerd 升级（与 etcd 路径并行）
          - from: health-check
            to: containerd-upgrade
            type: hard
          
          # etcd 升级 → 控制面升级
          - from: etcd-upgrade
            to: api-server-upgrade
            type: hard
          - from: etcd-upgrade
            to: controller-manager-upgrade
            type: hard
          - from: etcd-upgrade
            to: scheduler-upgrade
            type: hard
          
          # 控制面 → Worker
          - from: api-server-upgrade
            to: worker-upgrade
            type: hard
          - from: controller-manager-upgrade
            to: worker-upgrade
            type: soft  # soft: 一个失败不阻塞
          - from: scheduler-upgrade
            to: worker-upgrade
            type: soft
          
          # Worker → 验证
          - from: worker-upgrade
            to: post-check
            type: hard
          - from: containerd-upgrade
            to: post-check
            type: soft
        
        entryPoints: ["health-check"]
        exitPoints: ["post-check"]
```
**对应的 DAG 执行图：**

```
                    health-check
                    /          \
                   v            v
            etcd-backup    cert-rotation
                   \          /
                    v        v
                  etcd-upgrade     containerd-upgrade
                 /    |     \            |
                v     v      v           |
        api-server  ctrl-mgr  scheduler  |
                \     |       /          |
                 v    v      v           |
                   worker-upgrade        |
                        \               /
                         v             v
                        post-check
```
**并行执行时间线：**
```
T0: health-check
T1: etcd-backup ∥ cert-rotation          ← 并行
T2: etcd-upgrade ∥ containerd-upgrade    ← 并行
T3: api-server ∥ ctrl-mgr ∥ scheduler    ← 并行（控制面三组件）
T4: worker-upgrade
T5: post-check
```
对比原来的顺序执行 `bootstrap → management → workload`，DAG 设计将 T2-T3 的 etcd 升级与 containerd 升级并行化，T3 的三个控制面组件并行化，**预计可减少 30-40% 的升级时间**。
#### 3.6 NodeExecutor 接口
```go
// pkg/upgrade/executor.go

type NodeExecutor interface {
    Execute(ctx context.Context, node *UpgradeNode) error
    Rollback(ctx context.Context, node *UpgradeNode) error
}

type DefaultNodeExecutor struct {
    clusterManager cluster.ClusterManager
    versionPkgMgr  *versionpackage.Manager
}

func (e *DefaultNodeExecutor) Execute(ctx context.Context, node *UpgradeNode) error {
    switch node.Action.Type {
    case "image-rollout":
        return e.rolloutImage(ctx, node)
    case "config-apply":
        return e.applyConfig(ctx, node)
    case "script":
        return e.executeScript(ctx, node)
    default:
        return fmt.Errorf("unknown action type: %s", node.Action.Type)
    }
}
```
#### 3.7 与 Asset 框架的集成
DAG 调度器与 Asset 框架共享同一套 DAG 执行引擎：
```go
// pkg/dag/engine.go — 通用 DAG 执行引擎

type DAGEngine struct {
    nodes      map[string]*DAGNode
    edges      []DAGEdge
    executor   NodeExecutor
    stateStore StateStore
}

// DAGNode 通用 DAG 节点
type DAGNode struct {
    ID           string
    Dependencies []string
    ExecuteFunc  func(ctx context.Context, deps map[string]*NodeResult) (*NodeResult, error)
}

// Execute 通用 DAG 执行
// Asset 框架和 Upgrade DAG 都使用此引擎
func (e *DAGEngine) Execute(ctx context.Context) error {
    // 拓扑排序 + 并行调度
    // ...
}
```
这样 Asset 框架和 Upgrade DAG 不需要各自实现调度逻辑，共享同一个 DAG 执行引擎，只是注册不同的节点执行函数。
### 总结
| 问题 | 设计要点 |
|------|---------|
| **CRD 字段设计** | VersionPackage 统一收归散落在 defaults.go、validation.go、exporter.go 中的版本信息，通过 Components/Images/Compatibility/UpgradePaths 四大板块声明式管理 |
| **Asset 生命周期** | Asset 不仅用于 Provisioning，还覆盖 Upgrading（证书轮换、新版本 manifest）、Scaling（新节点证书/kubeconfig）、Deleting（清理持久化状态），通过 `ApplicablePhases()` 声明生效阶段 |
| **UpgradePath DAG 并行** | 将 `UpgradeOrder []LayerUpgrade` 重构为 `UpgradeDAG`，支持：节点级并行（ParallelGroup）、硬/软依赖边（EdgeType）、条件执行（Condition CEL 表达式）、关键节点回滚（Critical + RollbackAction）、DAG 无环验证 |

# UpgradePath 与 VersionPackage 分离的重构方案。
## UpgradePath 与 VersionPackage 分离重构设计
### 一、问题分析
当前设计中 UpgradePath 嵌入在 VersionPackage 内部：
```
VersionPackage
  └── UpgradePaths []UpgradePath    ← 升级路径绑定在版本包中
```
**核心矛盾：**
1. **发布耦合**：VersionPackage 描述"这个版本包含什么"，UpgradePath 描述"怎么升级到这个版本"，两者变化频率不同。一个版本包发布后，其组件版本、镜像列表基本不变；但升级路径可能需要频繁调整（如发现新的兼容性问题、增加中间步骤、调整并行策略）。
2. **方向性不对称**：VersionPackage 是"目标态"，UpgradePath 是"路径"。从 v2.0.0 升级到 v2.1.0 的路径，既不属于 v2.0.0 也不属于 v2.1.0，它是两个版本之间的关系。
3. **多对多关系**：一个目标版本可能有多条来源路径（v1.0→v2.0, v1.5→v2.0），一个源版本也可能有多条目标路径（v1.5→v2.0, v1.5→v2.1）。将 UpgradePath 嵌入 VersionPackage 无法自然表达这种关系。
4. **无法独立演进**：版本包发布后如果发现升级路径有问题，必须修改 VersionPackage CRD 并重新发布整个版本包，这违反了"版本包不可变"原则。
### 二、重构方案：独立 CRD + 引用关联
#### 2.1 整体架构
```
┌─────────────────────┐          ┌──────────────────────────┐
│   VersionPackage    │          │   ClusterUpgradePath     │
│ 不可变，发布时确定) │          │(可独立更新，描述升级路径)│
│                     │          │                          │
│  version: v2.1.0    │◄─────────│  toVersion: v2.1.0       │
│  components: [...]  │          │  fromVersion: v2.0.0     │
│  images: [...]      │          │  dag: {nodes, edges}     │
│  compatibility: ... │          │  preCheckDag: ...        │
│                     │          │  rollbackDag: ...        │
└─────────────────────┘          └──────────────────────────┘
        ▲                                  ▲
        │                                  │
        │  引用                            │  引用
        │                                  │
┌───────┴───────────┐          ┌───────────┴──────────────┐
│  ClusterVersion   │───────── │  CVO Controller          │
│(集群当前版本声明) │  触发升级│  (协调升级执行)          │
└───────────────────┘          └──────────────────────────┘
```
**关键设计原则：**
- **VersionPackage 不可变**：发布后不修改，只读使用
- **ClusterUpgradePath 可独立更新**：可以随时新增、修改升级路径，不影响版本包
- **ClusterVersion 触发升级**：声明目标版本，CVO 查找匹配的 UpgradePath 执行
#### 2.2 VersionPackage CRD（移除 UpgradePaths）
```go
// api/versionpackage/v1beta1/types.go

type VersionPackage struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   VersionPackageSpec   `json:"spec,omitempty"`
    Status VersionPackageStatus `json:"status,omitempty"`
}

type VersionPackageSpec struct {
    // Version: 语义化版本号
    Version string `json:"version"`
    
    // ReleaseDate: 发布日期
    ReleaseDate *metav1.Time `json:"releaseDate,omitempty"`
    
    // Deprecated: 是否已废弃
    Deprecated bool `json:"deprecated,omitempty"`
    
    // Components: 组件版本列表
    Components []ComponentVersion `json:"components"`
    
    // Images: 镜像定义列表
    Images []ImageDefinition `json:"images,omitempty"`
    
    // Compatibility: 兼容性矩阵
    Compatibility CompatibilityMatrix `json:"compatibility"`
    
    // OSRequirements: OS 要求
    OSRequirements []OSRequirement `json:"osRequirements,omitempty"`
    
    // Assets: Asset 定义
    Assets []AssetDefinition `json:"assets,omitempty"`
    
    // 注意：UpgradePaths 已移除
    // 升级路径由独立的 ClusterUpgradePath CRD 管理
}

type ComponentVersion struct {
    Name     string         `json:"name"`
    Version  string         `json:"version"`
    Type     ComponentType  `json:"type,omitempty"`
    Critical bool           `json:"critical,omitempty"`
}

type ComponentType string

const (
    ComponentCore     ComponentType = "core"
    ComponentRuntime  ComponentType = "runtime"
    ComponentAddon    ComponentType = "addon"
    ComponentInfra    ComponentType = "infra"
)

type ImageDefinition struct {
    Name      string   `json:"name"`
    Tag       string   `json:"tag"`
    Component string   `json:"component,omitempty"`
    Archs     []string `json:"archs,omitempty"`
    Digest    string   `json:"digest,omitempty"`
}

type CompatibilityMatrix struct {
    K8sVersionRange *VersionRange             `json:"k8sVersionRange,omitempty"`
    ComponentCompat []ComponentCompatibility  `json:"componentCompat,omitempty"`
    MinNodeOSVersion map[string]string        `json:"minNodeOSVersion,omitempty"`
}

type VersionRange struct {
    Min string `json:"min"`
    Max string `json:"max,omitempty"`
}

type ComponentCompatibility struct {
    Component  string        `json:"component"`
    Version    string        `json:"version"`
    Compatible []VersionRange `json:"compatible"`
}

type OSRequirement struct {
    Name      string   `json:"name"`
    MinVersion string  `json:"minVersion"`
    Archs     []string `json:"archs,omitempty"`
}

type AssetDefinition struct {
    Name         string   `json:"name"`
    Type         string   `json:"type"`
    Dependencies []string `json:"dependencies,omitempty"`
    Phases       []string `json:"phases,omitempty"`
    Config       runtime.RawExtension `json:"config,omitempty"`
}

type VersionPackagePhase string

const (
    VPPPhaseAvailable   VersionPackagePhase = "Available"
    VPPPhaseDeprecated  VersionPackagePhase = "Deprecated"
    VPPPhaseEOL         VersionPackagePhase = "EOL"
)

type VersionPackageStatus struct {
    Phase             VersionPackagePhase `json:"phase,omitempty"`
    ActiveClusters    int                 `json:"activeClusters,omitempty"`
    Conditions        []metav1.Condition  `json:"conditions,omitempty"`
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
}
```
#### 2.3 ClusterUpgradePath CRD（独立升级路径）
```go
// api/upgrade/v1beta1/types.go

// ClusterUpgradePath 定义从一个版本到另一个版本的升级路径
// 这是一个独立的 CRD，与 VersionPackage 解耦
// 可以在 VersionPackage 发布后独立创建和更新
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="From",type=string,JSONPath=`.spec.fromVersion`
// +kubebuilder:printcolumn:name="To",type=string,JSONPath=`.spec.toVersion`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ClusterUpgradePath struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   ClusterUpgradePathSpec   `json:"spec,omitempty"`
    Status ClusterUpgradePathStatus `json:"status,omitempty"`
}

type ClusterUpgradePathSpec struct {
    // ===== 版本引用（与 VersionPackage 解耦，通过版本号引用） =====
    
    // FromVersion: 源版本号，对应某个 VersionPackage.Spec.Version
    FromVersion string `json:"fromVersion"`
    
    // ToVersion: 目标版本号，对应某个 VersionPackage.Spec.Version
    ToVersion string `json:"toVersion"`
    
    // Type: 升级路径类型
    // "solution" = 整体方案升级, "component" = 单组件升级,
    // "patch" = 补丁升级, "emergency" = 紧急修复
    Type UpgradePathType `json:"type"`
    
    // ===== 升级 DAG =====
    
    // DAG: 主升级流程的有向无环图
    DAG UpgradeDAG `json:"dag"`
    
    // PreCheckDAG: 升级前检查 DAG
    // 在主 DAG 之前执行，全部成功才开始主升级
    PreCheckDAG *UpgradeDAG `json:"preCheckDag,omitempty"`
    
    // PostCheckDAG: 升级后验证 DAG
    // 在主 DAG 之后执行，验证升级结果
    PostCheckDAG *UpgradeDAG `json:"postCheckDag,omitempty"`
    
    // RollbackDAG: 回滚 DAG
    // 主 DAG 中关键节点失败时执行
    RollbackDAG *UpgradeDAG `json:"rollbackDag,omitempty"`
    
    // ===== 约束条件 =====
    
    // Prerequisites: 升级前置条件
    // 如要求先完成中间版本升级
    Prerequisites []Prerequisite `json:"prerequisites,omitempty"`
    
    // MaxDuration: 升级最大持续时间
    MaxDuration metav1.Duration `json:"maxDuration,omitempty"`
    
    // PauseAllowed: 是否允许暂停升级
    PauseAllowed bool `json:"pauseAllowed,omitempty"`
    
    // Deprecated: 此升级路径是否已废弃
    // 废弃后不再推荐使用，但已启动的升级可继续
    Deprecated bool `json:"deprecated,omitempty"`
}

// UpgradePathType 升级路径类型
type UpgradePathType string

const (
    UpgradePathSolution  UpgradePathType = "solution"
    UpgradePathComponent UpgradePathType = "component"
    UpgradePathPatch     UpgradePathType = "patch"
    UpgradePathEmergency UpgradePathType = "emergency"
)

// Prerequisite 升级前置条件
type Prerequisite struct {
    // Type: 前置条件类型
    // "intermediate-version" = 需要先升级到中间版本
    // "resource" = 资源要求（磁盘空间、内存等）
    // "custom" = 自定义检查
    Type string `json:"type"`
    
    // Value: 前置条件的值
    // 如 intermediate-version: "v2.0.0"
    // 如 resource: "disk-space:10GB"
    Value string `json:"value"`
    
    // Message: 人类可读的说明
    Message string `json:"message,omitempty"`
}

// ===== UpgradeDAG 定义 =====

type UpgradeDAG struct {
    // Nodes: DAG 中的所有节点
    Nodes []UpgradeNode `json:"nodes"`
    
    // Edges: DAG 中的所有边（依赖关系）
    Edges []UpgradeEdge `json:"edges"`
    
    // EntryPoints: 入口节点 ID 列表
    EntryPoints []string `json:"entryPoints"`
    
    // ExitPoints: 出口节点 ID 列表
    ExitPoints []string `json:"exitPoints"`
}

type UpgradeNode struct {
    // ID: 节点唯一标识
    ID string `json:"id"`
    
    // Name: 可读名称
    Name string `json:"name"`
    
    // Type: 节点类型
    Type UpgradeNodeType `json:"type"`
    
    // Component: 关联的组件名称（引用 VersionPackage.Components.Name）
    Component string `json:"component,omitempty"`
    
    // Layer: 所属集群层级
    Layer ClusterLayer `json:"layer,omitempty"`
    
    // Action: 执行动作
    Action UpgradeAction `json:"action"`
    
    // Timeout: 超时时间
    Timeout metav1.Duration `json:"timeout,omitempty"`
    
    // RetryPolicy: 重试策略
    RetryPolicy *RetryPolicy `json:"retryPolicy,omitempty"`
    
    // RollbackAction: 回滚动作
    RollbackAction *UpgradeAction `json:"rollbackAction,omitempty"`
    
    // Condition: 执行条件（CEL 表达式）
    Condition string `json:"condition,omitempty"`
    
    // Critical: 关键节点标记
    Critical bool `json:"critical,omitempty"`
    
    // ParallelGroup: 并行组标识
    ParallelGroup string `json:"parallelGroup,omitempty"`
}

type UpgradeNodeType string

const (
    UpgradeNodeComponent UpgradeNodeType = "component"
    UpgradeNodeCheck     UpgradeNodeType = "check"
    UpgradeNodeBackup    UpgradeNodeType = "backup"
    UpgradeNodeConfig    UpgradeNodeType = "config"
)

type ClusterLayer string

const (
    LayerBootstrap  ClusterLayer = "bootstrap"
    LayerManagement ClusterLayer = "management"
    LayerWorkload   ClusterLayer = "workload"
)

type UpgradeAction struct {
    // Type: 动作类型
    Type ActionType `json:"type"`
    
    // Image: 新镜像（image-rollout 类型）
    Image string `json:"image,omitempty"`
    
    // Config: 配置内容（config-apply 类型）
    Config *runtime.RawExtension `json:"config,omitempty"`
    
    // Script: 脚本路径（script 类型）
    Script string `json:"script,omitempty"`
    
    // Strategy: 执行策略
    Strategy *UpgradeStrategy `json:"strategy,omitempty"`
}

type ActionType string

const (
    ActionImageRollout ActionType = "image-rollout"
    ActionConfigApply  ActionType = "config-apply"
    ActionScript       ActionType = "script"
)

type UpgradeStrategy struct {
    MaxUnavailable    intstr.IntOrString `json:"maxUnavailable,omitempty"`
    MaxSurge          intstr.IntOrString `json:"maxSurge,omitempty"`
    PauseBetweenNodes metav1.Duration    `json:"pauseBetweenNodes,omitempty"`
}

type RetryPolicy struct {
    MaxRetries int             `json:"maxRetries"`
    Delay      metav1.Duration `json:"delay"`
}

type UpgradeEdge struct {
    From      string   `json:"from"`
    To        string   `json:"to"`
    Type      EdgeType `json:"type"`
    Condition string   `json:"condition,omitempty"`
}

type EdgeType string

const (
    EdgeHard EdgeType = "hard"
    EdgeSoft EdgeType = "soft"
)

// ===== ClusterUpgradePathStatus =====

type ClusterUpgradePathPhase string

const (
    UpgradePathActive      ClusterUpgradePathPhase = "Active"
    UpgradePathValidating  ClusterUpgradePathPhase = "Validating"
    UpgradePathDeprecated  ClusterUpgradePathPhase = "Deprecated"
    UpgradePathInvalid     ClusterUpgradePathPhase = "Invalid"
)

type ClusterUpgradePathStatus struct {
    // Phase: 升级路径当前阶段
    Phase ClusterUpgradePathPhase `json:"phase,omitempty"`
    
    // ValidationErrors: DAG 验证错误
    ValidationErrors []string `json:"validationErrors,omitempty"`
    
    // ReferencedVersions: 验证后确认引用的 VersionPackage 是否存在
    FromVersionExists bool `json:"fromVersionExists,omitempty"`
    ToVersionExists   bool `json:"toVersionExists,omitempty"`
    
    // UsageCount: 使用此路径进行升级的次数
    UsageCount int `json:"usageCount,omitempty"`
    
    // LastUsed: 最后使用时间
    LastUsed *metav1.Time `json:"lastUsed,omitempty"`
    
    Conditions        []metav1.Condition `json:"conditions,omitempty"`
    ObservedGeneration int64             `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterUpgradePathList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []ClusterUpgradePath `json:"items"`
}
```
#### 2.4 ClusterVersion CRD（升级触发器，阶段四的扩展）
```go
// api/clusterversion/v1beta1/types.go

// ClusterVersion 声明集群的目标版本
// 当 Spec.DesiredVersion 与 Status.CurrentVersion 不同时，
// CVO 控制器查找匹配的 ClusterUpgradePath 并执行升级
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ClusterVersion struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   ClusterVersionSpec   `json:"spec,omitempty"`
    Status ClusterVersionStatus `json:"status,omitempty"`
}

type ClusterVersionSpec struct {
    // DesiredVersion: 期望达到的版本
    // 对应 VersionPackage.Spec.Version
    DesiredVersion string `json:"desiredVersion"`
    
    // UpgradePathRef: 显式指定升级路径
    // 如果为空，CVO 自动查找 FromVersion=CurrentVersion, ToVersion=DesiredVersion 的路径
    // +optional
    UpgradePathRef *UpgradePathReference `json:"upgradePathRef,omitempty"`
    
    // AllowIntermediateUpgrade: 是否允许自动经过中间版本升级
    // 如从 v1.0 升级到 v2.0 需要先经过 v1.5
    AllowIntermediateUpgrade bool `json:"allowIntermediateUpgrade,omitempty"`
    
    // DryRun: 只检查不执行
    DryRun bool `json:"dryRun,omitempty"`
    
    // Pause: 暂停升级
    Pause bool `json:"pause,omitempty"`
}

type UpgradePathReference struct {
    // Name: ClusterUpgradePath 资源名称
    Name string `json:"name"`
    
    // Namespace: ClusterUpgradePath 所在命名空间
    Namespace string `json:"namespace,omitempty"`
}

type ClusterVersionStatus struct {
    // CurrentVersion: 当前实际版本
    CurrentVersion string `json:"currentVersion,omitempty"`
    
    // DesiredVersion: 期望版本（镜像自 Spec）
    DesiredVersion string `json:"desiredVersion,omitempty"`
    
    // UpgradePhase: 升级阶段
    UpgradePhase UpgradePhase `json:"upgradePhase,omitempty"`
    
    // ActiveUpgradePath: 当前正在使用的升级路径
    ActiveUpgradePath *UpgradePathReference `json:"activeUpgradePath,omitempty"`
    
    // UpgradeHistory: 升级历史
    UpgradeHistory []UpgradeRecord `json:"upgradeHistory,omitempty"`
    
    // DAGExecutionStatus: DAG 执行状态（断点续传用）
    DAGExecutionStatus *DAGExecutionStatus `json:"dagExecutionStatus,omitempty"`
    
    // NextUpgradePath: 自动规划的下一步升级路径（中间版本场景）
    NextUpgradePath *UpgradePathReference `json:"nextUpgradePath,omitempty"`
    
    Conditions        []metav1.Condition `json:"conditions,omitempty"`
    ObservedGeneration int64             `json:"observedGeneration,omitempty"`
}

type UpgradePhase string

const (
    UpgradePhaseIdle        UpgradePhase = "Idle"
    UpgradePhasePreChecking UpgradePhase = "PreChecking"
    UpgradePhaseUpgrading   UpgradePhase = "Upgrading"
    UpgradePhasePostChecking UpgradePhase = "PostChecking"
    UpgradePhasePaused      UpgradePhase = "Paused"
    UpgradePhaseRollingBack UpgradePhase = "RollingBack"
    UpgradePhaseCompleted   UpgradePhase = "Completed"
    UpgradePhaseFailed      UpgradePhase = "Failed"
)

type UpgradeRecord struct {
    FromVersion   string      `json:"fromVersion"`
    ToVersion     string      `json:"toVersion"`
    UpgradePath   string      `json:"upgradePath"`
    StartTime     metav1.Time `json:"startTime"`
    EndTime       *metav1.Time `json:"endTime,omitempty"`
    Result        string      `json:"result"` // "succeeded", "failed", "rolled-back"
    Message       string      `json:"message,omitempty"`
}

type DAGExecutionStatus struct {
    // NodeStatuses: 每个节点的执行状态
    NodeStatuses map[string]NodeExecStatus `json:"nodeStatuses,omitempty"`
    
    // CurrentWave: 当前执行波次（并行调度用）
    CurrentWave int `json:"currentWave,omitempty"`
    
    // Checkpoint: 断点信息，用于断点续传
    Checkpoint string `json:"checkpoint,omitempty"`
}

type NodeExecStatus struct {
    Status    NodeExecutionStatus `json:"status"`
    StartTime *metav1.Time        `json:"startTime,omitempty"`
    EndTime   *metav1.Time        `json:"endTime,omitempty"`
    Message   string              `json:"message,omitempty"`
}

type NodeExecutionStatus string

const (
    NodePending   NodeExecutionStatus = "Pending"
    NodeRunning   NodeExecutionStatus = "Running"
    NodeSucceeded NodeExecutionStatus = "Succeeded"
    NodeFailed    NodeExecutionStatus = "Failed"
    NodeSkipped   NodeExecutionStatus = "Skipped"
    NodeRollback  NodeExecutionStatus = "Rollback"
)
```
### 三、CVO 控制器协调逻辑
```go
// controllers/clusterversion/controller.go

type ClusterVersionReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    
    VersionPackageManager *versionpackage.Manager
    DAGSchedulerFactory   *DAGSchedulerFactory
}

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &clusterversion.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 已是目标版本，无需升级
    if cv.Status.CurrentVersion == cv.Spec.DesiredVersion {
        return r.ensureUpgradePhase(cv, clusterversion.UpgradePhaseIdle)
    }
    
    // 查找升级路径
    upgradePath, err := r.resolveUpgradePath(ctx, cv)
    if err != nil {
        return r.markFailed(ctx, cv, fmt.Sprintf("resolve upgrade path: %v", err))
    }
    
    // 执行升级 DAG
    return r.executeUpgrade(ctx, cv, upgradePath)
}

// resolveUpgradePath 解析升级路径
// 优先使用显式指定的路径，否则自动查找
func (r *ClusterVersionReconciler) resolveUpgradePath(ctx context.Context, 
    cv *clusterversion.ClusterVersion) (*upgrade.ClusterUpgradePath, error) {
    
    // 1. 显式指定了升级路径
    if cv.Spec.UpgradePathRef != nil {
        path := &upgrade.ClusterUpgradePath{}
        err := r.Get(ctx, types.NamespacedName{
            Name:      cv.Spec.UpgradePathRef.Name,
            Namespace: cv.Spec.UpgradePathRef.Namespace,
        }, path)
        if err != nil {
            return nil, errors.Wrap(err, "failed to get specified upgrade path")
        }
        return path, nil
    }
    
    // 2. 自动查找：FromVersion=CurrentVersion, ToVersion=DesiredVersion
    paths := &upgrade.ClusterUpgradePathList{}
    if err := r.List(ctx, paths, client.MatchingFields{
        "spec.fromVersion": cv.Status.CurrentVersion,
        "spec.toVersion":   cv.Spec.DesiredVersion,
    }); err != nil {
        return nil, errors.Wrap(err, "failed to list upgrade paths")
    }
    
    if len(paths.Items) == 0 {
        // 3. 检查是否允许中间版本升级
        if cv.Spec.AllowIntermediateUpgrade {
            return r.findIntermediatePath(ctx, cv)
        }
        return nil, errors.Errorf(
            "no upgrade path from %s to %s", 
            cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
    }
    
    // 优先选择非 deprecated 的路径
    for i := range paths.Items {
        if !paths.Items[i].Spec.Deprecated {
            return &paths.Items[i], nil
        }
    }
    
    return &paths.Items[0], nil
}

// findIntermediatePath 查找经过中间版本的升级路径
func (r *ClusterVersionReconciler) findIntermediatePath(ctx context.Context, 
    cv *clusterversion.ClusterVersion) (*upgrade.ClusterUpgradePath, error) {
    
    // BFS 查找从 CurrentVersion 到 DesiredVersion 的路径
    // 如 v1.0 → v1.5 → v2.0
    visited := map[string]bool{cv.Status.CurrentVersion: true}
    queue := []string{cv.Status.CurrentVersion}
    parent := map[string]string{}
    
    for len(queue) > 0 {
        current := queue[0]
        queue = queue[1:]
        
        paths := &upgrade.ClusterUpgradePathList{}
        if err := r.List(ctx, paths, client.MatchingFields{
            "spec.fromVersion": current,
        }); err != nil {
            continue
        }
        
        for _, path := range paths.Items {
            if path.Spec.Deprecated {
                continue
            }
            target := path.Spec.ToVersion
            if visited[target] {
                continue
            }
            parent[target] = current
            visited[target] = true
            
            if target == cv.Spec.DesiredVersion {
                // 回溯路径
                return r.buildIntermediatePath(ctx, parent, cv)
            }
            queue = append(queue, target)
        }
    }
    
    return nil, errors.Errorf(
        "no intermediate upgrade path from %s to %s",
        cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
}
```
### 四、独立演进场景
#### 场景 1：版本包发布后新增升级路径
```yaml
# 版本包 v2.1.0 已发布（不可变）
apiVersion: versionpackage.bke.bocloud.com/v1beta1
kind: VersionPackage
metadata:
  name: v2.1.0
spec:
  version: v2.1.0
  components:
    - name: kubernetes
      version: v1.27.0
      type: core
    - name: etcd
      version: v3.5.8-0
      type: core
  # 无 UpgradePaths 字段
---
# 后续独立发布升级路径：v1.0 → v2.1.0
# 不需要修改 VersionPackage
apiVersion: upgrade.bke.bocloud.com/v1beta1
kind: ClusterUpgradePath
metadata:
  name: v1.0-to-v2.1.0
  labels:
    fromVersion: v1.0.0
    toVersion: v2.1.0
spec:
  fromVersion: v1.0.0
  toVersion: v2.1.0
  type: solution
  prerequisites:
    - type: intermediate-version
      value: v2.0.0
      message: "必须先升级到 v2.0.0"
  dag:
    nodes: [...]
    edges: [...]
```
#### 场景 2：修复升级路径中的问题
```yaml
# 发现 v2.0→v2.1 的升级路径中 etcd 升级需要先做备份
# 只需修改 ClusterUpgradePath，无需重新发布 VersionPackage

# 旧路径标记为 deprecated
apiVersion: upgrade.bke.bocloud.com/v1beta1
kind: ClusterUpgradePath
metadata:
  name: v2.0-to-v2.1.0
spec:
  fromVersion: v2.0.0
  toVersion: v2.1.0
  type: solution
  deprecated: true    # ← 标记废弃
  dag:
    nodes: [...]      # 旧的 DAG
---
# 新路径发布
apiVersion: upgrade.bke.bocloud.com/v1beta1
kind: ClusterUpgradePath
metadata:
  name: v2.0-to-v2.1.0-v2    # ← 新版本
spec:
  fromVersion: v2.0.0
  toVersion: v2.1.0
  type: solution
  dag:
    nodes:
      - id: health-check
        name: 健康检查
        type: check
        action: { type: script, script: /upgrade/pre-check.sh }
        timeout: 300s
        critical: true
      - id: etcd-backup        # ← 新增备份步骤
        name: ETCD备份
        type: backup
        action: { type: script, script: /upgrade/etcd-backup.sh }
        timeout: 600s
        critical: true
      - id: etcd-upgrade
        name: ETCD升级
        type: component
        component: etcd
        action: { type: image-rollout, image: etcd:3.5.8-0 }
        timeout: 900s
        critical: true
    edges:
      - { from: health-check, to: etcd-backup, type: hard }
      - { from: etcd-backup, to: etcd-upgrade, type: hard }  # ← 新增依赖
```
#### 场景 3：紧急补丁升级路径
```yaml
# CVE 修复，不需要新的 VersionPackage，只需发布紧急升级路径
apiVersion: upgrade.bke.bocloud.com/v1beta1
kind: ClusterUpgradePath
metadata:
  name: cve-2025-xxxx-patch
  labels:
    fromVersion: v2.1.0
    toVersion: v2.1.1
    priority: critical
spec:
  fromVersion: v2.1.0
  toVersion: v2.1.1
  type: emergency        # ← 紧急类型
  maxDuration: 1800s     # 30分钟内完成
  dag:
    nodes:
      - id: cve-patch
        name: CVE补丁应用
        type: component
        component: kubernetes
        action:
          type: image-rollout
          image: kube-apiserver:v1.27.1-fix
          strategy:
            maxUnavailable: 1
        timeout: 600s
        critical: true
    edges: []
    entryPoints: ["cve-patch"]
    exitPoints: ["cve-patch"]
```
### 五、ClusterUpgradePath 控制器（验证 + 状态管理）
```go
// controllers/upgrade/clusterupgradepath_controller.go

type ClusterUpgradePathReconciler struct {
    client.Client
    Scheme *runtime.Scheme
}

func (r *ClusterUpgradePathReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cup := &upgrade.ClusterUpgradePath{}
    if err := r.Get(ctx, req.NamespacedName, cup); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 1. 验证 DAG 无环
    if err := validateDAG(&cup.Spec.DAG); err != nil {
        cup.Status.Phase = upgrade.UpgradePathInvalid
        cup.Status.ValidationErrors = []string{err.Error()}
        return r.updateStatus(ctx, cup)
    }
    
    // 2. 验证入口/出口节点
    if err := validateEntryExitPoints(&cup.Spec.DAG); err != nil {
        cup.Status.Phase = upgrade.UpgradePathInvalid
        cup.Status.ValidationErrors = []string{err.Error()}
        return r.updateStatus(ctx, cup)
    }
    
    // 3. 验证引用的 VersionPackage 存在
    fromExists := r.versionPackageExists(ctx, cup.Spec.FromVersion)
    toExists := r.versionPackageExists(ctx, cup.Spec.ToVersion)
    cup.Status.FromVersionExists = fromExists
    cup.Status.ToVersionExists = toExists
    
    // VersionPackage 不存在不阻塞路径创建
    // 可能在路径创建后版本包才发布
    if cup.Spec.Deprecated {
        cup.Status.Phase = upgrade.UpgradePathDeprecated
    } else {
        cup.Status.Phase = upgrade.UpgradePathActive
    }
    
    cup.Status.ValidationErrors = nil
    return r.updateStatus(ctx, cup)
}

func validateDAG(dag *upgrade.UpgradeDAG) error {
    nodeSet := make(map[string]bool)
    for _, node := range dag.Nodes {
        if nodeSet[node.ID] {
            return fmt.Errorf("duplicate node ID: %s", node.ID)
        }
        nodeSet[node.ID] = true
    }
    
    // DFS 检测环
    visited := make(map[string]bool)
    visiting := make(map[string]bool)
    
    var dfs func(id string) error
    dfs = func(id string) error {
        if visiting[id] {
            return fmt.Errorf("cycle detected at node %s", id)
        }
        if visited[id] {
            return nil
        }
        visiting[id] = true
        for _, edge := range dag.Edges {
            if edge.From == id {
                if err := dfs(edge.To); err != nil {
                    return err
                }
            }
        }
        visiting[id] = false
        visited[id] = true
        return nil
    }
    
    for _, entry := range dag.EntryPoints {
        if err := dfs(entry); err != nil {
            return err
        }
    }
    return nil
}

func (r *ClusterUpgradePathReconciler) versionPackageExists(ctx context.Context, version string) bool {
    vpList := &versionpackage.VersionPackageList{}
    if err := r.List(ctx, vpList, client.MatchingFields{
        "spec.version": version,
    }); err != nil {
        return false
    }
    return len(vpList.Items) > 0
}
```
### 六、与现有 Phase 的集成
```go
// pkg/upgrade/phase_adapter.go
// 将现有 Phase 适配为 DAG 节点执行器

type PhaseNodeExecutor struct {
    phaseContext *phaseframe.PhaseContext
}

func (e *PhaseNodeExecutor) Execute(ctx context.Context, node *upgrade.UpgradeNode) error {
    // 根据节点 ID 映射到现有 Phase
    phaseFactory := e.resolvePhaseFactory(node.ID)
    if phaseFactory == nil {
        return fmt.Errorf("no phase factory for node %s", node.ID)
    }
    
    phase := phaseFactory(e.phaseContext)
    _, err := phase.Execute()
    return err
}

func (e *PhaseNodeExecutor) resolvePhaseFactory(nodeID string) func(*phaseframe.PhaseContext) phaseframe.Phase {
    mapping := map[string]func(*phaseframe.PhaseContext) phaseframe.Phase{
        "etcd-upgrade":       NewEnsureEtcdUpgrade,
        "master-upgrade":     NewEnsureMasterUpgrade,
        "worker-upgrade":     NewEnsureWorkerUpgrade,
        "containerd-upgrade": NewEnsureContainerdUpgrade,
        "agent-upgrade":      NewEnsureAgentUpgrade,
        "component-upgrade":  NewEnsureComponentUpgrade,
    }
    return mapping[nodeID]
}
```
### 七、对比总结
| 维度 | 原设计（UpgradePath 嵌入 VersionPackage） | 重构后（独立 ClusterUpgradePath CRD） |
|------|----------------------------------------|--------------------------------------|
| **发布耦合** | 升级路径与版本包一起发布，修改需重新发布版本包 | 升级路径独立发布，版本包发布后可随时新增/修改路径 |
| **不可变性** | VersionPackage 包含可变的升级路径，违反不可变原则 | VersionPackage 纯描述"是什么"，真正不可变 |
| **多对多关系** | 一个 VersionPackage 只能声明"到自己的路径"，无法表达"从自己出发的路径" | ClusterUpgradePath 天然表达双向关系 |
| **紧急修复** | CVE 补丁需要发布新 VersionPackage | 只需新增 emergency 类型的 ClusterUpgradePath |
| **路径废弃** | 废弃某条路径需要修改 VersionPackage | 只需设置 `deprecated: true`，不影响版本包 |
| **中间版本** | 需要在 VersionPackage 中声明 Requires | 由 CVO 通过 BFS 自动规划，Prerequisites 声明约束 |
| **验证时机** | 版本包加载时验证 | ClusterUpgradePath 控制器独立验证 DAG 合法性 |
| **回滚支持** | 嵌入在 VersionPackage 中 | 独立的 RollbackDAG，可针对不同路径定制 |

**核心收益：版本发布与升级路径的发布独立演进，VersionPackage 发布后不可变，ClusterUpgradePath 可随时新增、修改、废弃，两者通过版本号松耦合关联。**

# VersionPackageSpec 中包含 Asset 属性是否合理
## VersionPackageSpec 中包含 Asset 属性是否合理？——深度分析
### 一、先看 Asset 到底是什么
从代码中梳理出当前系统中的"Asset"（即集群部署/升级过程中需要生成的制品）：

| Asset | 生成位置 | 输入依赖 | 版本相关性 |
|-------|---------|---------|-----------|
| **CA 证书** | [certs/generator.go](file:///d:/code/github/cluster-api-provider-bke/pkg/certs/generator.go#L95) `setupGlobalCA()` | 无（自签根） | ❌ 与版本无关 |
| **组件证书** | [certs/generator.go](file:///d:/code/github/cluster-api-provider-bke/pkg/certs/generator.go#L99) `prepareBkeCerts()` | CA 证书 + 节点信息 + Endpoint | ❌ 与版本无关 |
| **Kubeconfig** | [certs/generator.go](file:///d:/code/github/cluster-api-provider-bke/pkg/certs/generator.go#L106) | CA 证书 + Endpoint | ❌ 与版本无关 |
| **Static Pod Manifest** | [mfutil/manifest.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/manifest.go#L56) `GenerateManifestYaml()` | **K8s 版本** + 镜像仓库 + 证书目录 + 节点信息 | ✅ **强版本相关** |
| **HA 配置** | [mfutil/manifest.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/manifest.go#L73) `GenerateHAManifestYaml()` | VIP + 节点列表 | ❌ 与版本无关 |
| **Addon Chart** | [ensure_addon_deploy.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go) | Chart 仓库 + 版本 | ✅ **版本相关** |
| **Kubelet 配置** | [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go#L136) `SetDefaultKubelet()` | K8s 版本 + CRI 配置 | ✅ **版本相关** |
| **Etcd 配置** | [render.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/render.go#L271) `renderEtcdYaml()` | Etcd 版本 + 节点信息 | ✅ **强版本相关** |

**关键发现：Asset 分为两类——版本无关的（证书、kubeconfig、HA配置）和版本相关的（manifest、addon chart、kubelet/etcd 配置）。**
### 二、VersionPackageSpec.Assets 的核心问题
#### 问题 1：职责混淆——"是什么"与"怎么生成"
VersionPackage 的核心职责是声明**"这个版本包含什么"**（What），而不是**"怎么生成这些制品"**（How）。
```
VersionPackage 回答的问题：
  ✅ kubernetes 版本是什么？ → v1.27.0
  ✅ etcd 镜像是什么？      → etcd:3.5.8-0
  ✅ 兼容的 K8s 范围？      → [v1.23, v1.28)
  
Asset 回答的问题：
  ✅ 证书怎么生成？         → 需要 CA + 节点列表 + Endpoint
  ✅ Static Pod 怎么渲染？  → 需要模板 + 版本 + 镜像仓库 + 证书目录
  ✅ Kubeconfig 怎么构建？  → 需要 CA + admin 证书 + Endpoint
```
Asset 的生成逻辑依赖**运行时上下文**（节点列表、Endpoint、镜像仓库地址、证书目录），这些信息在 VersionPackage 发布时根本不存在。
#### 问题 2：不可变性冲突
上一轮重构已确立 VersionPackage **发布后不可变**的原则。但 Asset 定义中包含生成配置（如模板参数、依赖声明），这些配置可能需要调整：
- 证书生成算法需要更新（如从 SHA-256 迁移到 SHA-3）
- Static Pod 模板需要调整（如新增启动参数）
- Addon 部署方式需要变更（如从 Helm 改为 OLM）

如果 Asset 定义在 VersionPackage 中，修改这些生成逻辑就需要重新发布 VersionPackage，违反不可变原则。
#### 问题 3：版本无关的 Asset 不应属于 VersionPackage
证书、kubeconfig、HA 配置这些 Asset 的生成逻辑**跨版本通用**，把它们放进某个特定版本的 VersionPackage 没有道理——它们不属于"v2.1.0 这个版本"，而是属于"集群生命周期管理"这个能力。
#### 问题 4：Asset 依赖图是运行时动态构建的
从 [ensure_certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_certs.go#L42) 可以看到，证书生成需要节点信息：
```go
func NewEnsureCerts(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    certsGenerator := certs.NewKubernetesCertGenerator(ctx.Context, ctx.Client, ctx.BKECluster)
    nodes, err := ctx.GetNodes()  // ← 运行时获取节点
    certsGenerator.SetNodes(nodes)
    ...
}
```
从 [manifest.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/mfutil/manifest.go#L56) 可以看到，Static Pod 生成依赖运行时上下文：
```go
func GenerateManifestYaml(components Components, boot *BootScope) error {
    cfg := bkeinit.BkeConfig(*boot.BkeConfig)
    log.Infof("generate %q version cluster manifests", cfg.Cluster.KubernetesVersion)
    // boot 包含运行时上下文：ClusterName, HostName, HostIP, BkeConfig...
}
```
Asset 的依赖图在 VersionPackage 发布时无法确定，因为节点列表、Endpoint 等输入是运行时才有的。
### 三、正确的分层：将 Asset 从 VersionPackage 中剥离
```
┌──────────────────────────────────────────────────────────────────┐
│                    三层分离架构                                  │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Layer 1: VersionPackage（不可变，描述"是什么"）                 │
│  ┌─────────────────────────────────────────┐                     │
│  │ version: v2.1.0                         │                     │
│  │ components:                             │                     │
│  │   - name: kubernetes, version: v1.27.0  │  ← 只声明版本       │
│  │   - name: etcd, version: v3.5.8-0       │                     │
│  │ images:                                 │                     │
│  │   - name: kube-apiserver, tag: v1.27.0  │  ← 只声明镜像       │
│  │ compatibility: ...                      │  ← 只声明约束       │
│  └─────────────────────────────────────────┘                     │
│       │                                                          │
│       │ 版本号引用                                               │
│       ▼                                                          │
│  Layer 2: AssetDefinition（可独立更新，描述"怎么生成"）          │
│  ┌─────────────────────────────────────────┐                     │
│  │ name: static-pod-manifest               │                     │
│  │ type: template-render                   │  ← 生成方式         │
│  │ dependencies: [ca-cert, node-info]      │  ← 依赖声明         │
│  │ applicablePhases: [provision, upgrade]  │  ← 生命周期         │
│  │ versionConstraints:                     │                     │
│  │   kubernetes: ">=1.25.0"                │  ← 版本约束         │
│  │ config:                                 │                     │
│  │   template: /templates/static-pod.yaml  │  ← 模板路径         │
│  │   imageSource: VersionPackage.Images    │  ← 从 VP 获取镜像   │
│  └─────────────────────────────────────────┘                     │
│       │                                                          │
│       │ 运行时绑定                                               │
│       ▼                                                          │
│  Layer 3: AssetRuntime（运行时，执行生成）                       │
│  ┌─────────────────────────────────────────┐                     │
│  │ 输入: VersionPackage+ClusterSpec+节点   │                     │
│  │ 输出: Secret/ConfigMap (证书/manifest)  │                     │
│  │ 状态: AssetResult + Checksum            │                     │
│  └─────────────────────────────────────────┘                     │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```
### 四、重构后的 CRD 设计
#### 4.1 VersionPackage（移除 Assets 字段）
```go
type VersionPackageSpec struct {
    Version       string               `json:"version"`
    ReleaseDate   *metav1.Time         `json:"releaseDate,omitempty"`
    Deprecated    bool                 `json:"deprecated,omitempty"`
    Components    []ComponentVersion   `json:"components"`
    Images        []ImageDefinition    `json:"images,omitempty"`
    Compatibility CompatibilityMatrix  `json:"compatibility"`
    OSRequirements []OSRequirement     `json:"osRequirements,omitempty"`
    
    // Assets 字段已移除
    // Asset 定义由独立的 AssetDefinition CRD 管理
}
```
**VersionPackage 只保留"是什么"：组件版本、镜像列表、兼容性约束。**
#### 4.2 AssetDefinition（独立 CRD）
```go
// api/asset/v1beta1/types.go

// AssetDefinition 定义一种 Asset 的生成方式
// 与 VersionPackage 解耦，可独立更新
// 通过 versionConstraints 声明适用的版本范围
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Phases",type=string,JSONPath=`.spec.applicablePhases`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type AssetDefinition struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   AssetDefinitionSpec   `json:"spec,omitempty"`
    Status AssetDefinitionStatus `json:"status,omitempty"`
}

type AssetDefinitionSpec struct {
    // Name: Asset 名称，全局唯一
    Name string `json:"name"`
    
    // Type: Asset 类型
    // "certificate" = 证书, "kubeconfig" = Kubeconfig,
    // "template-render" = 模板渲染(static pod, etcd config),
    // "chart-deploy" = Helm Chart 部署,
    // "config-generate" = 配置生成(kubelet config)
    Type AssetType `json:"type"`
    
    // Dependencies: 依赖的其他 Asset 名称
    // DAG 调度器据此构建依赖图
    Dependencies []string `json:"dependencies,omitempty"`
    
    // ApplicablePhases: 生效的生命周期阶段
    ApplicablePhases []AssetPhase `json:"applicablePhases"`
    
    // VersionConstraints: 版本约束
    // 声明此 Asset 定义适用的组件版本范围
    // 运行时根据 VersionPackage 中的组件版本匹配
    VersionConstraints []VersionConstraint `json:"versionConstraints,omitempty"`
    
    // Generator: Asset 生成器配置
    Generator AssetGenerator `json:"generator"`
    
    // PersistConfig: 持久化配置
    PersistConfig *AssetPersistConfig `json:"persistConfig,omitempty"`
}

type AssetType string

const (
    AssetTypeCertificate     AssetType = "certificate"
    AssetTypeKubeconfig      AssetType = "kubeconfig"
    AssetTypeTemplateRender  AssetType = "template-render"
    AssetTypeChartDeploy     AssetType = "chart-deploy"
    AssetTypeConfigGenerate  AssetType = "config-generate"
)

type AssetPhase string

const (
    AssetPhaseProvision AssetPhase = "provision"
    AssetPhaseUpgrade   AssetPhase = "upgrade"
    AssetPhaseScale     AssetPhase = "scale"
    AssetPhaseDelete    AssetPhase = "delete"
)

// VersionConstraint 声明此 Asset 定义适用的版本范围
// 运行时与 VersionPackage.Components 匹配
type VersionConstraint struct {
    // Component: 组件名称
    Component string `json:"component"`
    
    // Range: 版本范围（semver 表达式）
    // 如 ">=1.25.0 <1.28.0" 表示适用于 K8s 1.25~1.27
    Range string `json:"range"`
}

type AssetGenerator struct {
    // Certificate: 证书生成配置（type=certificate 时使用）
    Certificate *CertificateGenerator `json:"certificate,omitempty"`
    
    // Kubeconfig: kubeconfig 生成配置（type=kubeconfig 时使用）
    Kubeconfig *KubeconfigGenerator `json:"kubeconfig,omitempty"`
    
    // TemplateRender: 模板渲染配置（type=template-render 时使用）
    TemplateRender *TemplateRenderGenerator `json:"templateRender,omitempty"`
    
    // ChartDeploy: Chart 部署配置（type=chart-deploy 时使用）
    ChartDeploy *ChartDeployGenerator `json:"chartDeploy,omitempty"`
    
    // ConfigGenerate: 配置生成配置（type=config-generate 时使用）
    ConfigGenerate *ConfigGenerateGenerator `json:"configGenerate,omitempty"`
}

type CertificateGenerator struct {
    // CertType: 证书类型
    // "ca" = 根证书, "server" = 服务端证书, "client" = 客户端证书
    CertType string `json:"certType"`
    
    // CARef: 签发 CA 的 Asset 名称（非 CA 证书时必填）
    CARef string `json:"caRef,omitempty"`
    
    // SansSource: SAN 来源
    // "cluster-endpoint" = 从 ClusterSpec.ControlPlaneEndpoint 获取
    // "node-ips" = 从节点列表获取
    // "static" = 从 StaticSANs 获取
    SansSource string `json:"sansSource,omitempty"`
    
    // StaticSANs: 静态 SAN 列表
    StaticSANs []string `json:"staticSANs,omitempty"`
    
    // Validity: 证书有效期
    Validity metav1.Duration `json:"validity,omitempty"`
}

type KubeconfigGenerator struct {
    // CARef: CA 证书 Asset 名称
    CARef string `json:"caRef"`
    
    // EndpointSource: API Endpoint 来源
    // "cluster-spec" = 从 ClusterSpec.ControlPlaneEndpoint 获取
    EndpointSource string `json:"endpointSource"`
}

type TemplateRenderGenerator struct {
    // TemplateRef: 模板引用
    // 可以是嵌入的模板路径或 ConfigMap 引用
    TemplateRef string `json:"templateRef"`
    
    // ImageSources: 镜像来源声明
    // 声明模板中需要的镜像从 VersionPackage 的哪些组件获取
    // 如 "kubernetes" → 从 VersionPackage.Images 中筛选 component=kubernetes 的镜像
    ImageSources []string `json:"imageSources,omitempty"`
    
    // ConfigSources: 配置来源声明
    // 声明模板中需要的配置从 ClusterSpec 的哪些字段获取
    ConfigSources []string `json:"configSources,omitempty"`
}

type ChartDeployGenerator struct {
    // ChartRef: Chart 引用
    ChartRef string `json:"chartRef"`
    
    // VersionSource: Chart 版本来源
    // "component" = 从 VersionPackage.Components 中获取
    VersionSource string `json:"versionSource,omitempty"`
    
    // Component: 关联的组件名称
    Component string `json:"component,omitempty"`
    
    // ValuesTemplate: Values 模板
    ValuesTemplate string `json:"valuesTemplate,omitempty"`
}

type ConfigGenerateGenerator struct {
    // ConfigType: 配置类型
    // "kubelet" = kubelet 配置, "etcd" = etcd 配置,
    // "containerd" = containerd 配置
    ConfigType string `json:"configType"`
    
    // VersionSource: 版本来源组件
    VersionSource string `json:"versionSource"`
}

type AssetPersistConfig struct {
    // StoreType: 存储类型
    // "secret" = K8s Secret, "configmap" = K8s ConfigMap
    StoreType string `json:"storeType"`
    
    // NamePattern: 存储名称模式
    // 支持 {cluster-name}, {asset-name} 占位符
    NamePattern string `json:"namePattern"`
    
    // Namespace: 存储命名空间，空则与集群同命名空间
    Namespace string `json:"namespace,omitempty"`
}

type AssetDefinitionStatus struct {
    Phase             AssetDefinitionPhase `json:"phase,omitempty"`
    ValidationErrors  []string             `json:"validationErrors,omitempty"`
    Conditions        []metav1.Condition   `json:"conditions,omitempty"`
    ObservedGeneration int64               `json:"observedGeneration,omitempty"`
}

type AssetDefinitionPhase string

const (
    AssetDefActive    AssetDefinitionPhase = "Active"
    AssetDefInvalid   AssetDefinitionPhase = "Invalid"
    AssetDefDeprecated AssetDefinitionPhase = "Deprecated"
)
```
#### 4.3 运行时 Asset 注册表
```go
// pkg/asset/registry.go

type AssetRegistry struct {
    definitions map[string]*AssetDefinition
    client      client.Client
}

// ResolveForCluster 根据集群上下文解析适用的 Asset
func (r *AssetRegistry) ResolveForCluster(
    ctx context.Context,
    phase AssetPhase,
    vp *VersionPackage,
    cluster *bkev1beta1.BKECluster,
) ([]Asset, error) {
    
    var assets []Asset
    
    for _, def := range r.definitions {
        // 1. 检查生命周期阶段匹配
        if !containsPhase(def.Spec.ApplicablePhases, phase) {
            continue
        }
        
        // 2. 检查版本约束匹配
        if !r.matchVersionConstraints(def.Spec.VersionConstraints, vp) {
            continue
        }
        
        // 3. 构建 Asset 实例
        asset := r.buildAsset(def, vp, cluster)
        assets = append(assets, asset)
    }
    
    // 4. 构建依赖图
    return r.buildDependencyGraph(assets)
}

// matchVersionConstraints 检查版本约束是否匹配
func (r *AssetRegistry) matchVersionConstraints(
    constraints []VersionConstraint,
    vp *VersionPackage,
) bool {
    if len(constraints) == 0 {
        return true // 无约束则始终匹配
    }
    
    for _, constraint := range constraints {
        component := findComponent(vp.Spec.Components, constraint.Component)
        if component == nil {
            return false
        }
        
        rangeSemver, _ := semver.ParseRange(constraint.Range)
        version, _ := semver.ParseTolerant(component.Version)
        if !rangeSemver(version) {
            return false
        }
    }
    return true
}
```
### 五、对比分析
| 维度 | Assets 在 VersionPackage 中 | Assets 独立为 AssetDefinition |
|------|---------------------------|------------------------------|
| **职责清晰度** | ❌ 混合了"是什么"和"怎么生成" | ✅ VersionPackage 只管版本，AssetDefinition 只管生成 |
| **不可变性** | ❌ 修改生成逻辑需重新发布 VersionPackage | ✅ VersionPackage 真正不可变 |
| **版本无关 Asset** | ❌ 证书/kubeconfig 被绑定到特定版本 | ✅ 证书/kubeconfig 作为通用 Asset 跨版本复用 |
| **版本相关 Asset** | ⚠️ 可以工作，但耦合度高 | ✅ 通过 VersionConstraints 灵活匹配版本范围 |
| **运行时依赖** | ❌ 发布时无法确定节点列表等输入 | ✅ 运行时通过 Registry 动态解析 |
| **生成逻辑更新** | ❌ 需重新发布 VersionPackage | ✅ 只更新 AssetDefinition |
| **模板更新** | ❌ 修改 static pod 模板需重新发布 | ✅ 只更新 AssetDefinition 的 TemplateRef |
| **新增 Asset 类型** | ❌ 需修改 VersionPackage CRD | ✅ 新增 AssetDefinition 实例即可 |
| **复杂度** | ✅ 一个 CRD 搞定 | ⚠️ 多一个 CRD，但职责更清晰 |
### 六、结论
**VersionPackageSpec 中包含 Asset 属性是不合理的**，应将 Asset 剥离为独立的 AssetDefinition CRD。核心原因：
1. **职责分离**：VersionPackage 描述"这个版本包含什么"（组件版本、镜像、兼容性），AssetDefinition 描述"怎么生成制品"（模板、依赖、生成器配置）。两者是不同的关注点。
2. **不可变性**：VersionPackage 发布后不可变，但 Asset 的生成逻辑（模板、算法、配置）可能需要独立更新。将两者绑定会互相制约。
3. **生命周期差异**：证书/kubeconfig 等版本无关的 Asset 不应属于任何特定版本；Static Pod Manifest 等版本相关的 Asset 通过 VersionConstraints 动态匹配，而非静态嵌入。
4. **运行时上下文**：Asset 生成依赖节点列表、Endpoint、镜像仓库地址等运行时信息，这些在 VersionPackage 发布时不存在。

**推荐方案**：VersionPackage 只保留 `Components` + `Images` + `Compatibility`，Asset 定义独立为 `AssetDefinition` CRD，通过 `VersionConstraints` 与 VersionPackage 松耦合关联，运行时由 `AssetRegistry` 动态解析匹配。

# 组件与镜像的复杂关系
## VersionPackageSpec 中 `images` 属性是否合理？
### 一、从代码看组件与镜像的真实关系
#### 1.1 当前代码中的映射关系
从 [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go#L68-L77) 和 [exporter.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/imagehelper/exporter.go#L124-L143) 中梳理出的完整映射：
```
组件 (Component)          镜像名 (Image Name)         版本来源
───────────────────────────────────────────────────────────────
kubernetes v1.25.6   →   kube-apiserver:v1.25.6      = K8s版本
kubernetes v1.25.6   →   kube-controller-manager:v1.25.6  = K8s版本
kubernetes v1.25.6   →   kube-scheduler:v1.25.6      = K8s版本
kubernetes v1.25.6   →   kubelet:v1.25.6             = K8s版本

etcd v3.5.21-of.1    →   etcd:3.5.21-of.1           ≠ 组件版本！
                     →   etcd:3.5.6-0               (旧映射，K8s版本相关)

pause                →   pause:3.9                   ≠ 任何组件版本！
                     →   pause:3.8                   (旧映射，K8s版本相关)
                     →   pause:3.6                   (旧映射，K8s版本相关)

coredns              →   coredns/coredns:v1.8.0     ≠ 任何组件版本！

bkeagent-deployer    →   bkeagent-deployer:v1.2.3    来自 addon status

calico               →   calico 相关镜像              来自 chart values
```
**关键发现：组件版本 ≠ 镜像 Tag，这是核心矛盾。**
#### 1.2 版本与 Tag 的不一致性
| 组件 | 组件版本 | 镜像 Tag | 关系 |
|------|---------|---------|------|
| kubernetes | v1.25.6 | kube-apiserver:**v1.25.6** | ✅ 相等（去掉 v 前缀后） |
| etcd | v3.5.21-of.1 | etcd:**3.5.21-of.1** | ⚠️ 去掉 v 前缀后相等 |
| etcd (旧) | — | etcd:**3.5.6-0** | ❌ 与 K8s 版本隐式映射 |
| pause | — | pause:**3.9** | ❌ 独立版本，与任何组件版本无关 |
| coredns | — | coredns/coredns:**v1.8.0** | ❌ 独立版本 |
#### 1.3 一个组件对应多个镜像
Kubernetes 一个组件版本对应 **4 个镜像**：
```go
// exporter.go generateImageMap()
k8sComponentImageMapWithoutRepo := map[string]string{
    "kube-apiserver":          "kube-apiserver:v1.25.6",
    "kube-controller-manager": "kube-controller-manager:v1.25.6",
    "kube-scheduler":          "kube-scheduler:v1.25.6",
    "etcd":                    "etcd:3.5.6-0",
}
```
#### 1.4 镜像仓库是运行时上下文
从 [config.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/config.go#L151-L158)：
```go
func (bc *BkeConfig) ImageFuyaoRepo() string {
    // 镜像仓库地址来自 ClusterSpec，不是版本包
    address := validation.GetImageRepoAddress(bc.Cluster.ImageRepo)
    return fmt.Sprintf("%s/%s/", address, bc.Cluster.ImageRepo.Prefix)
}
```
镜像仓库地址（`deploy.bocloud.k8s:40443/kubernetes/`）是**运行时从 ClusterSpec 获取**的，不属于版本包的范畴。
### 二、`images` 放在 VersionPackage 中的问题
#### 问题 1：组件版本 ≠ 镜像 Tag，无法从 components 推导
假设 VersionPackage 只有 components：
```yaml
components:
  - name: kubernetes
    version: v1.25.6
  - name: etcd
    version: v3.5.21-of.1
```
**无法推导出：**
- `pause:3.9` — pause 不是一个"组件"，它没有出现在 components 列表中
- `etcd:3.5.6-0` — 旧版本的 etcd 镜像 tag 与组件版本不一致
- `coredns/coredns:v1.8.0` — coredns 镜像名带仓库前缀，tag 与任何组件无关

**如果尝试推导：**
```go
// 假设的推导逻辑
func deriveImage(component ComponentVersion) string {
    switch component.Name {
    case "kubernetes":
        // 一个组件 → 4 个镜像？哪个？
        return "kube-apiserver:" + strings.TrimPrefix(component.Version, "v")
    case "etcd":
        // v3.5.21-of.1 → 3.5.21-of.1？还是 3.5.6-0？
        return "etcd:" + strings.TrimPrefix(component.Version, "v")
    case "pause":
        // pause 根本不在 components 里！
    }
}
```
这种推导逻辑本质上是把 [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go#L96-L114) 中的 `GetDefaultEtcdK8sVersionImageMap()` 和 `GetDefaultPauseK8sVersionImageMap()` **重新硬编码了一遍**，只是从 Go 代码搬到了推导逻辑中。
#### 问题 2：一个组件对应多个镜像的 1:N 关系
Kubernetes 组件对应 4 个镜像，Addon（如 calico）可能对应 5+ 个镜像。components 的结构是：
```yaml
components:
  - name: kubernetes
    version: v1.25.6
```
如果要从 components 推导 images，需要在 ComponentVersion 中嵌入镜像列表：
```yaml
components:
  - name: kubernetes
    version: v1.25.6
    images:                          # ← 这不就是把 images 字段搬了个位置？
      - name: kube-apiserver
        tag: v1.25.6
      - name: kube-controller-manager
        tag: v1.25.6
      - name: kube-scheduler
        tag: v1.25.6
      - name: kubelet
        tag: v1.25.6
```
**这和单独的 images 列表没有本质区别，只是换了个嵌套位置。**
#### 问题 3：镜像仓库地址不属于 VersionPackage
完整镜像 = `仓库地址/镜像名:Tag`
- `镜像名:Tag` → 版本相关，属于 VersionPackage
- `仓库地址` → 运行时上下文，属于 ClusterSpec

当前代码中，镜像仓库地址由 [config.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/config.go#L151) 的 `ImageFuyaoRepo()` 从 ClusterSpec.ImageRepo 动态计算。VersionPackage 中的 images 只能声明 `name:tag`，不能包含仓库地址。
#### 问题 4：Addon 镜像的生命周期不同
从 [ensure_addon_deploy.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go) 和 [Product](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_spec.go#L280) 结构可以看到，Addon 是通过 Chart 部署的，其镜像定义在 Chart 的 values.yaml 中，不由 VersionPackage 控制。
```
Addon 镜像来源：
  calico    → calico chart values.yaml → calico-node, calico-kube-controllers, ...
  coredns   → coredns chart values.yaml → coredns/coredns:v1.8.0
  bkeagent  → bkeagent chart values.yaml → bkeagent-deployer:v1.2.3
```
这些镜像不应该出现在 VersionPackage 的 images 列表中，因为它们由 Addon 自己管理。
### 三、images 是否应该保留？——分类讨论
将系统中的镜像按特征分类：

| 镜像类别 | 示例 | 版本来源 | 1:N 关系 | 是否应入 VersionPackage |
|---------|------|---------|---------|----------------------|
| **K8s 核心镜像** | kube-apiserver, kube-controller-manager, kube-scheduler, kubelet | = K8s 组件版本 | 1:4 | ✅ 可以从组件推导 |
| **K8s 依赖镜像** | etcd, pause | ≠ 任何组件版本，独立版本 | 1:1 | ❌ 无法从组件推导 |
| **基础设施镜像** | coredns, haproxy, keepalived | 独立版本 | 1:1 | ❌ 无法从组件推导 |
| **Addon 镜像** | calico-*, bkeagent-*, csi-* | 由 Chart 管理 | 1:N | ❌ 不属于版本包 |
| **工具镜像** | etcdctl, kubeadm, crictl | 跟随组件版本 | 1:1 | ⚠️ 可选 |

**结论：K8s 核心镜像可以从组件推导，但 etcd/pause/coredns 等依赖镜像无法从 components 推导。**
### 四、推荐方案：保留 images，但重新定义语义
#### 4.1 images 的正确定位
images 不是 components 的冗余副本，而是**补充 components 无法覆盖的镜像信息**：
```
components: 描述"有什么组件，什么版本"    → 供给侧视角
images:     描述"需要拉取什么镜像"         → 消费侧视角
```
两者的关系：
```
components                          images
┌─────────────────────┐            ┌─────────────────────────────┐
│ kubernetes v1.25.6  │────推导───→│ kube-apiserver:v1.25.6      │
│                     │            │ kube-controller-manager:... │
│                     │            │ kube-scheduler:...          │
│                     │            │ kubelet:v1.25.6             │
├─────────────────────┤            ├─────────────────────────────┤
│ etcd v3.5.21-of.1   │────推导───→│ etcd:3.5.21-of.1            │
│                     │            │                             │
│ (无法推导 pause)    │            │ pause:3.9        ← 独立声明 │
│ (无法推导 coredns)  │            │ coredns:v1.8.0   ← 独立声明 │
└─────────────────────┘            └─────────────────────────────┘
```
#### 4.2 重构后的 ComponentVersion 和 ImageDefinition
```go
type ComponentVersion struct {
    Name     string        `json:"name"`
    Version  string        `json:"version"`
    Type     ComponentType `json:"type,omitempty"`
    Critical bool          `json:"critical,omitempty"`
    
    // Images: 该组件对应的镜像列表
    // 对于 kubernetes 组件，这里列出 4 个核心镜像
    // 对于 etcd 组件，这里列出 etcd 镜像
    // 如果为空，则使用默认推导规则：imageName = componentName, tag = version
    Images []ComponentImage `json:"images,omitempty"`
}

type ComponentImage struct {
    // Name: 镜像名称，如 "kube-apiserver", "etcd"
    Name string `json:"name"`
    
    // Tag: 镜像标签，如 "v1.25.6", "3.5.21-of.1"
    // 如果为空，默认使用父级 ComponentVersion.Version
    Tag string `json:"tag,omitempty"`
}

type ImageDefinition struct {
    // Name: 镜像名称
    Name string `json:"name"`
    
    // Tag: 镜像标签
    Tag string `json:"tag"`
    
    // Component: 关联的组件名称（可选）
    // 有此字段的镜像可以通过 components 查询
    // 无此字段的镜像是独立镜像（如 pause, coredns）
    Component string `json:"component,omitempty"`
    
    // Category: 镜像分类
    // "core" = 核心组件镜像（可通过 components 推导）
    // "dependency" = 依赖镜像（无法从 components 推导，如 pause, coredns）
    // "tool" = 工具镜像（如 etcdctl, kubeadm）
    Category ImageCategory `json:"category,omitempty"`
    
    // Archs: 支持的架构
    Archs []string `json:"archs,omitempty"`
    
    // Digest: 镜像摘要
    Digest string `json:"digest,omitempty"`
}

type ImageCategory string

const (
    ImageCategoryCore       ImageCategory = "core"
    ImageCategoryDependency ImageCategory = "dependency"
    ImageCategoryTool       ImageCategory = "tool"
)
```
#### 4.3 VersionPackageSpec 最终设计
```go
type VersionPackageSpec struct {
    Version       string              `json:"version"`
    ReleaseDate   *metav1.Time        `json:"releaseDate,omitempty"`
    Deprecated    bool                `json:"deprecated,omitempty"`
    
    // Components: 组件版本列表
    // 每个组件可声明其对应的镜像（ComponentImage）
    // K8s 核心镜像通过此字段推导
    Components    []ComponentVersion  `json:"components"`
    
    // Images: 独立镜像列表
    // 只包含无法从 Components 推导的镜像：
    //   - dependency 类：pause, coredns, haproxy, keepalived
    //   - tool 类：etcdctl, kubeadm
    // 不包含 core 类镜像（已在 Components.Images 中声明）
    Images        []ImageDefinition   `json:"images,omitempty"`
    
    // Compatibility: 兼容性矩阵
    Compatibility CompatibilityMatrix `json:"compatibility"`
    
    // OSRequirements: OS 要求
    OSRequirements []OSRequirement    `json:"osRequirements,omitempty"`
}
```
#### 4.4 YAML 示例
```yaml
apiVersion: versionpackage.bke.bocloud.com/v1beta1
kind: VersionPackage
metadata:
  name: v2.1.0
spec:
  version: v2.1.0
  components:
    - name: kubernetes
      version: v1.27.0
      type: core
      critical: true
      images:
        - name: kube-apiserver
        - name: kube-controller-manager
        - name: kube-scheduler
        - name: kubelet
        # tag 为空，默认使用 kubernetes 的 version: v1.27.0
    
    - name: etcd
      version: v3.5.8-0
      type: core
      critical: true
      images:
        - name: etcd
          tag: "3.5.8-0"    # 显式声明，因为 tag ≠ version（无 v 前缀）
    
    - name: containerd
      version: "1.6.24"
      type: runtime
      # 无 images：containerd 不是容器镜像

  # 独立镜像：无法从 components 推导
  images:
    - name: pause
      tag: "3.9"
      category: dependency
    
    - name: coredns/coredns
      tag: v1.9.3
      category: dependency
    
    - name: haproxy
      tag: "2.8.0"
      category: dependency
    
    - name: keepalived
      tag: "2.2.8"
      category: dependency
```
#### 4.5 镜像查询 API
```go
// pkg/versionpackage/image_resolver.go

type ImageResolver struct {
    vp *VersionPackage
}

// ResolveAll 解析版本包中的所有镜像（components 镜像 + 独立镜像）
func (r *ImageResolver) ResolveAll() []ResolvedImage {
    var images []ResolvedImage
    
    // 1. 从 Components 推导镜像
    for _, comp := range r.vp.Spec.Components {
        for _, img := range comp.Images {
            tag := img.Tag
            if tag == "" {
                tag = comp.Version
            }
            images = append(images, ResolvedImage{
                Name:      img.Name,
                Tag:       tag,
                Component: comp.Name,
                Category:  ImageCategoryCore,
            })
        }
    }
    
    // 2. 加入独立镜像
    for _, img := range r.vp.Spec.Images {
        images = append(images, ResolvedImage{
            Name:      img.Name,
            Tag:       img.Tag,
            Component: img.Component,
            Category:  img.Category,
        })
    }
    
    return images
}

// ResolveByComponent 按组件查询镜像
// 替代当前 exporter.go 的 generateImageMap()
func (r *ImageResolver) ResolveByComponent(componentName string) []ResolvedImage {
    var images []ResolvedImage
    for _, comp := range r.vp.Spec.Components {
        if comp.Name == componentName {
            for _, img := range comp.Images {
                tag := img.Tag
                if tag == "" {
                    tag = comp.Version
                }
                images = append(images, ResolvedImage{
                    Name:      img.Name,
                    Tag:       tag,
                    Component: comp.Name,
                })
            }
        }
    }
    return images
}

// ResolveByPhase 按部署阶段查询镜像
// 替代当前 exporter.go 的 ExportImageMapWithBootStrapPhase()
func (r *ImageResolver) ResolveByPhase(phase string) []ResolvedImage {
    switch phase {
    case "InitControlPlane", "JoinControlPlane", "UpgradeControlPlane":
        return r.ResolveAll()
    case "JoinWorker", "UpgradeWorker":
        return r.ResolveByComponent("") | filter(pause)
    default:
        return nil
    }
}

type ResolvedImage struct {
    Name      string        `json:"name"`
    Tag       string        `json:"tag"`
    Component string        `json:"component,omitempty"`
    Category  ImageCategory `json:"category,omitempty"`
}

// FullReference 返回完整镜像引用（需运行时注入仓库地址）
func (i *ResolvedImage) FullReference(repo string) string {
    return fmt.Sprintf("%s/%s:%s", repo, i.Name, i.Tag)
}
```
### 五、对比总结
| 方案 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| **A: 删除 images，全部从 components 推导** | components 中嵌入 ComponentImage | 统一入口，无冗余 | pause/coredns 等独立镜像无处安放；需虚构"pause 组件" |
| **B: 保留 images，与 components 完全独立** | components 管版本，images 管镜像 | 职责清晰 | K8s 核心镜像在两处重复声明（components 中有版本，images 中又有） |
| **C: components 含 ComponentImage + images 只放独立镜像** ✅ | 核心镜像从组件推导，独立镜像单独声明 | 无冗余；覆盖所有场景；语义清晰 | components 结构稍复杂 |

**方案 C 是最优解：**
1. **K8s 核心镜像**（kube-apiserver 等）通过 `ComponentVersion.Images` 声明，tag 默认等于组件版本，避免冗余
2. **独立镜像**（pause, coredns 等）通过 `VersionPackageSpec.Images` 声明，`category: dependency` 标识
3. **镜像仓库地址**不在 VersionPackage 中，由运行时 `ImageResolver.FullReference(repo)` 注入
4. **Addon 镜像**不在 VersionPackage 中，由 Addon Chart 自行管理

**核心原则：images 不是 components 的冗余副本，而是 components 无法覆盖的补充。核心镜像归 components，独立镜像归 images。**

    
# CompatibilityMatrix Phase 6 的设计
## Phase 6 设计回顾与总结
根据之前的讨论，Phase 6 涉及四个核心 CRD 的设计，经过多轮分析，我们得出了以下关键结论：
### 1. VersionPackage CRD — 版本包管理
**核心定位**：声明式定义一个版本发布所包含的组件和镜像，是版本发布的"快照"。

**关键设计决策**：

| 属性 | 是否包含 | 理由 |
|------|---------|------|
| `components` | ✅ 包含 | 核心属性，定义版本包中的组件列表及其版本 |
| `images` | ✅ 包含 | 虽然组件列表可推导镜像，但组件→镜像映射关系复杂（如 etcd 镜像 tag 与 K8s 版本非线性对应），显式声明可避免运行时计算错误，且便于镜像预拉取和离线部署 |
| `asset` | ❌ 不包含 | Asset 是运行时产物（证书、manifest、配置），不属于版本包的静态声明范畴，应通过 AssetDefinition CRD 独立管理 |
| `compatibility` | ❌ 不包含 | 兼容性信息需要在版本包发布后持续更新（如发现新的不兼容场景），应通过 CompatibilityMatrix CRD 独立演进 |

**最终结构**：
```go
type VersionPackageSpec struct {
    Version     string              `json:"version"`
    ReleaseDate metav1.Time         `json:"releaseDate"`
    Description string              `json:"description"`
    Components  []ComponentVersion  `json:"components"`
    Images      []ImageSpec         `json:"images"`
}
```
### 2. ClusterUpgradePath CRD — 升级路径管理
**核心定位**：独立于 VersionPackage 的升级路径定义，支持 DAG 并行执行。

**关键设计决策**：

| 设计点 | 决策 | 理由 |
|--------|------|------|
| 与 VersionPackage 分离 | ✅ 独立 CRD | 版本发布与升级路径发布独立演进，版本包发布后可逐步补充升级路径 |
| 执行模型 | DAG 并行 | 替代原有 `UpgradeOrder []LayerUpgrade` 的顺序执行，支持无依赖步骤并行 |
| 引用方式 | 通过 version label 关联 | `spec.fromVersion` / `spec.toVersion` 关联 VersionPackage |

**DAG 并行设计**：
```go
type ClusterUpgradePathSpec struct {
    FromVersion string           `json:"fromVersion"`
    ToVersion   string           `json:"toVersion"`
    Type        UpgradePathType  `json:"type"`
    Tasks       []UpgradeTask    `json:"tasks"`
    PreCheck    PreCheck         `json:"preCheck"`
    Rollback    RollbackConfig   `json:"rollback"`
}

type UpgradeTask struct {
    Name         string   `json:"name"`
    Layer        string   `json:"layer"`
    Component    string   `json:"component"`
    DependsOn    []string `json:"dependsOn,omitempty"`
    Action       string   `json:"action"`
    Timeout      *metav1.Duration `json:"timeout,omitempty"`
}
```
DAG 调度器根据 `DependsOn` 构建依赖图，拓扑排序后并行执行无依赖的 task。
### 3. AssetDefinition CRD — 资产框架
**核心定位**：定义集群运行时资产的生成逻辑和依赖关系。

**关键设计决策**：

| 设计点 | 决策 | 理由 |
|--------|------|------|
| Asset 不限于 Provisioning | ✅ | Asset 在 Provisioning 和 Upgrade 阶段都会使用（如证书轮换、manifest 更新） |
| DAG 依赖管理 | ✅ | 资产间存在依赖（如 kubeconfig 依赖证书，manifest 依赖 kubeconfig） |
| 独立于 VersionPackage | ✅ | 资产生成逻辑是运行时行为，不属于版本包的静态声明 |

**Asset 使用场景**：
- **Provisioning**：首次生成所有资产（证书、kubeconfig、manifest、配置文件）
- **Upgrade**：部分资产需要重新生成（如 K8s 版本升级后 API Server manifest 变更）
- **Maintenance**：证书轮换、配置更新
### 4. CompatibilityMatrix CRD — 兼容性矩阵
**核心定位**：独立于 VersionPackage 的兼容性信息管理，支持版本发布后持续更新。

**关键设计决策**：

| 设计点 | 决策 | 理由 |
|--------|------|------|
| 与 VersionPackage 分离 | ✅ 独立 CRD | 兼容性信息需要在版本包发布后持续补充（如发现新的不兼容场景、新增 OS 支持） |
| 可独立演进 | ✅ | 修改兼容性信息不需要重新发布 VersionPackage |
| 多维度兼容性 | ✅ | 包括 K8s 版本范围、组件兼容、OS 兼容、Addon 兼容 |

**最终结构**：
```go
type CompatibilityMatrixSpec struct {
    TargetVersion       string                    `json:"targetVersion"`
    K8sVersionRange     VersionRange              `json:"k8sVersionRange"`
    ComponentCompat     []ComponentCompatibility  `json:"componentCompat"`
    OSCompatibility     []OSCompatibility         `json:"osCompatibility"`
    AddonCompatibility  []AddonCompatibility      `json:"addonCompatibility,omitempty"`
}

type VersionRange struct {
    MinVersion string `json:"minVersion"`
    MaxVersion string `json:"maxVersion,omitempty"`
}
```
### 5. 四个 CRD 的关系图
```
┌────────────────────┐        ┌────────────────────────┐
│  VersionPackage    │        │  CompatibilityMatrix   │
│  (版本快照)        │◄───────│  (兼容性约束)          │
│  - components      │ 引用   │  - targetVersion       │
│  - images          │        │  - k8sVersionRange     │
└────────────────────┘        │  - componentCompat     │
         │                    │  - osCompatibility     │
         │ 引用               └────────────────────────┘
         ▼
┌────────────────────────┐    ┌────────────────────────┐
│  ClusterUpgradePath    │    │  AssetDefinition       │
│  (升级路径)            │    │  (资产生成逻辑)        │
│  - fromVersion         │    │  - assetType           │
│  - toVersion           │    │  - dependencies (DAG)  │
│  - tasks (DAG)         │    │  - generator           │
│  - dependsOn           │    │  - params              │
└────────────────────────┘    └────────────────────────┘
```

**关联方式**：
- `CompatibilityMatrix.targetVersion` → 引用 `VersionPackage.version`
- `ClusterUpgradePath.fromVersion/toVersion` → 引用 `VersionPackage.version`
- `AssetDefinition` 通过 Phase 框架在 Provisioning/Upgrade 阶段被调用
### 6. 对现有代码的影响
| 现有代码 | 变更 |
|---------|------|
| [defaults.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/defaults.go) 中的硬编码版本常量 | 迁移到 VersionPackage CRD |
| [validation.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/validation/validation.go) 中的硬编码版本范围 | 迁移到 CompatibilityMatrix CRD |
| [exporter.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/imagehelper/exporter.go) 中的镜像映射逻辑 | 迁移到 VersionPackage.images |
| [refactor.md](file:///d:/code/github/cluster-api-provider-bke/code/refactor.md) 中的 `UpgradeOrder []LayerUpgrade` | 重构为 DAG-based `Tasks []UpgradeTask` |

以上是 Phase 6 四个核心 CRD 的完整设计总结。每个 CRD 都遵循了**关注点分离**原则，确保各自可以独立演进。如果你需要进一步深入某个 CRD 的实现细节，或者想讨论其他阶段的设计，请告诉我。
