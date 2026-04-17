# 阶段七：清理 + 集成测试 详细设计
## 一、遗留代码清理
### 1.1 Deprecated 函数清理清单
基于代码扫描，以下 Deprecated 函数需要在重构完成后清理：

| 文件 | Deprecated 函数 | 替代函数 | 清理策略 |
|------|----------------|---------|----------|
| `common/cluster/initialize/defaults.go:113` | `GetDefaultEtcdK8sVersionImageMap()` | VersionPackage CRD 的 images 字段 | 迁移到 VersionPackage 后删除 |
| `common/cluster/initialize/defaults.go:122` | `GetDefaultPauseK8sVersionImageMap()` | VersionPackage CRD 的 images 字段 | 迁移到 VersionPackage 后删除 |
| `common/cluster/initialize/defaults.go:87-93` | `DefaultK8sV121EtcdImageTag` 等常量 | VersionPackage CRD | 迁移后删除 |
| `common/cluster/initialize/export.go:127` | `GetDefaultBKENodes()` | 新的节点管理接口 | 迁移后删除 |
| `utils/bkeagent/pkiutil/altname.go:35` | `GetAPIServerCertAltNamesFromBkeConfig()` | `GetAPIServerCertAltNamesWithNodes()` | 已有替代，直接删除 |
| `utils/bkeagent/pkiutil/altname.go:88` | `GetEtcdCertAltNamesFromBkeConfig()` | `GetEtcdCertAltNamesWithNodes()` | 已有替代，直接删除 |
| `utils/utils.go:84` | `ClusterName()` | 从 CRD 对象获取集群名 | 迁移后删除 |
| `pkg/phaseframe/phaseutil/util.go:304` | `GetNeedPostProcessNodes()` | `GetNeedPostProcessNodesWithBKENodes()` | 已有替代，直接删除 |
| `pkg/phaseframe/phaseutil/command.go:187` | `GetNotSkipFailedNode()` | `GetNotSkipFailedNodeWithBKENodes()` | 已有替代，直接删除 |
| `pkg/phaseframe/phaseutil/clusterapi.go:250` | `ClusterEndDeployedWithBKENodes()` | `ClusterEndDeployedWithContext()` | 已有替代，直接删除 |
| `pkg/phaseframe/phaseutil/clusterapi.go:291` | `IsNodeBootFlagSet()` | `IsNodeBootFlagSetWithBKENodes()` | 已有替代，直接删除 |
| `pkg/phaseframe/phases/ensure_cluster.go:286` | bkeconfig cm 创建逻辑 | 新的 Addon 框架 | 迁移后删除 |
| `pkg/job/builtin/kubeadm/kubelet/containerd.go:134` | 旧版 containerd 命令 | `newNerdctlCommand()` | 已有替代，直接删除 |
| `pkg/job/builtin/kubeadm/env/init.go:1133` | 额外依赖处理 | addon 项目处理 | 迁移后删除 |
| `pkg/command/command.go:451` | 旧版命令处理 | 新命令框架 | 迁移后删除 |
### 1.2 硬编码版本常量清理
**目标**：将所有硬编码的版本常量迁移到 VersionPackage CRD，由 CRD 控制器动态注入。
```go
// common/cluster/initialize/defaults.go
// 需要清理的硬编码常量：
const (
    DefaultKubernetesVersion = "v1.25.6"     // -> VersionPackage.spec.components[k8s].version
    DefaultEtcdVersion       = "v3.5.21-of.1" // -> VersionPackage.spec.components[etcd].version
    DefaultEtcdImageTag      = "3.5.21-of.1"  // -> VersionPackage.spec.images[etcd].tag
    DefaultPauseImageTag     = "3.9"          // -> VersionPackage.spec.images[pause].tag
)
```
**清理策略**：
1. **第一阶段 — 兼容层**：创建 `VersionProvider` 接口，默认实现从常量读取，新实现从 CRD 读取
```go
// pkg/versionpackage/provider.go
package versionpackage

type VersionProvider interface {
    GetDefaultKubernetesVersion() string
    GetDefaultEtcdVersion() string
    GetEtcdImageTag(k8sVersion string) (string, error)
    GetPauseImageTag(k8sVersion string) (string, error)
    GetImageRegistry() string
}

// StaticVersionProvider 从硬编码常量提供版本信息（兼容旧逻辑）
type StaticVersionProvider struct{}

func (p *StaticVersionProvider) GetDefaultKubernetesVersion() string {
    return initialize.DefaultKubernetesVersion
}

func (p *StaticVersionProvider) GetDefaultEtcdVersion() string {
    return initialize.DefaultEtcdVersion
}

func (p *StaticVersionProvider) GetEtcdImageTag(k8sVersion string) (string, error) {
    imageMap := initialize.GetDefaultEtcdK8sVersionImageMap()
    if tag, ok := imageMap[k8sVersion]; ok {
        return tag, nil
    }
    return initialize.DefaultEtcdImageTag, nil
}

func (p *StaticVersionProvider) GetPauseImageTag(k8sVersion string) (string, error) {
    imageMap := initialize.GetDefaultPauseK8sVersionImageMap()
    if tag, ok := imageMap[k8sVersion]; ok {
        return tag, nil
    }
    return initialize.DefaultPauseImageTag, nil
}

func (p *StaticVersionProvider) GetImageRegistry() string {
    return initialize.DefaultImageRepo + ":" + initialize.DefaultImageRepoPort
}

// CRDVersionProvider 从 VersionPackage CRD 提供版本信息
type CRDVersionProvider struct {
    Client client.Client
}

func (p *CRDVersionProvider) GetDefaultKubernetesVersion() string {
    vp, err := p.getDefaultVersionPackage()
    if err != nil {
        return initialize.DefaultKubernetesVersion
    }
    for _, comp := range vp.Spec.Components {
        if comp.Name == "kubernetes" {
            return comp.Version
        }
    }
    return initialize.DefaultKubernetesVersion
}

func (p *CRDVersionProvider) getDefaultVersionPackage() (*VersionPackage, error) {
    ctx := context.Background()
    var vpList VersionPackageList
    if err := p.Client.List(ctx, &vpList, client.Limit(1)); err != nil {
        return nil, err
    }
    if len(vpList.Items) == 0 {
        return nil, fmt.Errorf("no VersionPackage found")
    }
    return &vpList.Items[0], nil
}
```
2. **第二阶段 — 切换注入点**：将所有使用硬编码常量的地方改为注入 `VersionProvider`
3. **第三阶段 — 删除常量**：确认所有调用点迁移完成后，删除硬编码常量
### 1.3 硬编码版本验证清理
**目标**：将 `validation.go` 中的硬编码版本范围迁移到 CompatibilityMatrix CRD。
```go
// common/cluster/validation/validation.go
// 需要清理的硬编码变量：
var (
    MinSupportedK8sVersion, _       = semver.ParseTolerant("v1.21.0") // -> CompatibilityMatrix.spec.k8sVersionRange.minVersion
    DockerMinSupportedK8sVersion, _ = semver.ParseTolerant("v1.24.0") // -> CompatibilityMatrix.spec.k8sVersionRange.minVersion (docker)
    MaxSupportedK8sVersion, _       = semver.ParseTolerant("v1.28.0") // -> CompatibilityMatrix.spec.k8sVersionRange.maxVersion
)
```
**清理策略**：
1. 创建 `CompatibilityChecker` 接口，默认实现从变量读取，新实现从 CRD 读取
```go
// pkg/compatibility/checker.go
package compatibility

type CompatibilityChecker interface {
    ValidateK8sVersion(version string) error
    ValidateComponentVersion(component, version string) error
    ValidateOS(name, version string) error
    GetSupportedK8sRange() (min, max string)
}

// StaticCompatibilityChecker 从硬编码变量检查兼容性（兼容旧逻辑）
type StaticCompatibilityChecker struct{}

func (c *StaticCompatibilityChecker) ValidateK8sVersion(version string) error {
    return validation.ValidateK8sVersion(version)
}

func (c *StaticCompatibilityChecker) GetSupportedK8sRange() (min, max string) {
    return "v1.21.0", "v1.28.0"
}

// CRDCompatibilityChecker 从 CompatibilityMatrix CRD 检查兼容性
type CRDCompatibilityChecker struct {
    Client client.Client
}

func (c *CRDCompatibilityChecker) ValidateK8sVersion(version string) error {
    ctx := context.Background()
    var cmList CompatibilityMatrixList
    if err := c.Client.List(ctx, &cmList); err != nil {
        return err
    }
    if len(cmList.Items) == 0 {
        return validation.ValidateK8sVersion(version)
    }
    cm := &cmList.Items[0]
    v, err := semver.ParseTolerant(version)
    if err != nil {
        return err
    }
    minV, _ := semver.ParseTolerant(cm.Spec.K8sVersionRange.MinVersion)
    if v.LT(minV) {
        return fmt.Errorf("kubernetes version %s is below minimum %s", version, cm.Spec.K8sVersionRange.MinVersion)
    }
    if cm.Spec.K8sVersionRange.MaxVersion != "" {
        maxV, _ := semver.ParseTolerant(cm.Spec.K8sVersionRange.MaxVersion)
        if v.GT(maxV) {
            return fmt.Errorf("kubernetes version %s is above maximum %s", version, cm.Spec.K8sVersionRange.MaxVersion)
        }
    }
    return nil
}
```
2. 将 `ValidateK8sVersion` 调用点替换为 `CompatibilityChecker` 接口调用
3. 确认迁移完成后删除硬编码变量
### 1.4 镜像映射逻辑清理
**目标**：将 `imagehelper/exporter.go` 中的镜像映射逻辑迁移到 VersionPackage.images。
```go
// common/cluster/imagehelper/exporter.go
// 需要清理的逻辑：
func (e *ImageExporter) generateImageMap() {
    k8sComponentImageMapWithoutRepo := map[string]string{
        initialize.DefaultAPIServerImageName: GetImageNameWithTag(
            initialize.DefaultAPIServerImageName, e.Version),
        // ... 硬编码镜像名和版本映射
    }
}
```
**清理策略**：
1. 创建 `ImageResolver` 接口，从 VersionPackage.images 解析镜像
```go
// pkg/versionpackage/resolver.go
package versionpackage

type ImageResolver interface {
    ResolveImage(componentName string) (string, error)
    ResolveAllImages() (map[string]string, error)
    GetImageRegistry() string
}

// StaticImageResolver 从硬编码逻辑解析镜像（兼容旧逻辑）
type StaticImageResolver struct {
    K8sVersion  string
    EtcdVersion string
    ImageRepo   string
}

func (r *StaticImageResolver) ResolveImage(componentName string) (string, error) {
    exporter := imagehelper.NewImageExporter(r.K8sVersion, r.EtcdVersion, r.ImageRepo)
    imageMap := exporter.GetImageMap()
    if img, ok := imageMap[componentName]; ok {
        return img, nil
    }
    return "", fmt.Errorf("image for component %s not found", componentName)
}

// CRDImageResolver 从 VersionPackage CRD 解析镜像
type CRDImageResolver struct {
    VersionPackage *VersionPackage
    ImageRepo      string
}

func (r *CRDImageResolver) ResolveImage(componentName string) (string, error) {
    for _, img := range r.VersionPackage.Spec.Images {
        if img.ComponentName == componentName {
            repo := r.ImageRepo
            if img.Registry != "" {
                repo = img.Registry
            }
            if repo != "" {
                return fmt.Sprintf("%s/%s:%s", repo, img.Name, img.Tag), nil
            }
            return fmt.Sprintf("%s:%s", img.Name, img.Tag), nil
        }
    }
    return "", fmt.Errorf("image for component %s not found in VersionPackage", componentName)
}

func (r *CRDImageResolver) ResolveAllImages() (map[string]string, error) {
    result := make(map[string]string)
    for _, img := range r.VersionPackage.Spec.Images {
        repo := r.ImageRepo
        if img.Registry != "" {
            repo = img.Registry
        }
        if repo != "" {
            result[img.ComponentName] = fmt.Sprintf("%s/%s:%s", repo, img.Name, img.Tag)
        } else {
            result[img.ComponentName] = fmt.Sprintf("%s:%s", img.Name, img.Tag)
        }
    }
    return result, nil
}
```
2. 将所有 `ImageExporter` 使用点替换为 `ImageResolver` 接口
3. 确认迁移完成后清理 `imagehelper` 包中的硬编码逻辑
### 1.5 清理执行计划
```
清理阶段时间线
├── 第1周：兼容层实现
│   ├── 实现 VersionProvider 接口（Static + CRD）
│   ├── 实现 CompatibilityChecker 接口（Static + CRD）
│   ├── 实现 ImageResolver 接口（Static + CRD）
│   └── 编写兼容层单元测试
│
├── 第2周：调用点迁移
│   ├── defaults.go 调用点 → VersionProvider
│   ├── validation.go 调用点 → CompatibilityChecker
│   ├── exporter.go 调用点 → ImageResolver
│   ├── render.go 调用点 → ImageResolver
│   └── 运行全量单元测试验证兼容性
│
├── 第3周：Deprecated 函数删除
│   ├── 删除 GetDefaultEtcdK8sVersionImageMap
│   ├── 删除 GetDefaultPauseK8sVersionImageMap
│   ├── 删除 GetAPIServerCertAltNamesFromBkeConfig
│   ├── 删除 GetEtcdCertAltNamesFromBkeConfig
│   ├── 删除 ClusterName()
│   ├── 删除 GetNeedPostProcessNodes
│   ├── 删除 GetNotSkipFailedNode
│   ├── 删除旧版 ClusterEndDeployed 变体
│   └── 删除旧版 IsNodeBootFlagSet 变体
│
└── 第4周：常量清理 + 验证
    ├── 删除 Deprecated 版本常量
    ├── 删除硬编码版本验证变量
    ├── 删除 imagehelper 中的硬编码映射
    ├── 全量回归测试
    └── 更新文档
```
## 二、测试体系设计
### 2.1 测试分层架构
```
测试金字塔
                    ┌───────────┐
                    │  E2E测试  │  ← 少量，验证完整流程
                    │  (envtest)│
                 ┌──┴───────────┴──┐
                 │  集成测试       │  ← 中等，验证模块交互
                 │  (envtest+fake) │
              ┌──┴─────────────────┴──┐
              │  单元测试             │  ← 大量，验证独立逻辑
              │  (testify+fake client)│
              └───────────────────────┘
```
### 2.2 单元测试
#### 2.2.1 现有测试覆盖分析
当前项目已有大量单元测试（150+ 测试文件），主要分布在：

| 包 | 测试文件数 | 覆盖范围 |
|----|-----------|----------|
| `pkg/phaseframe/phases/` | 25+ | 各 Phase 执行逻辑 |
| `pkg/phaseframe/phaseutil/` | 12 | 工具函数 |
| `pkg/certs/` | 3 | 证书生成和获取 |
| `pkg/remote/` | 8 | SSH/SFTP 远程操作 |
| `pkg/kube/` | 9 | K8s 客户端封装 |
| `pkg/job/` | 20+ | Job 执行逻辑 |
| `common/cluster/` | 5 | 集群初始化和验证 |
| `utils/` | 20+ | 工具函数 |

**测试框架**：`testify/assert` + `fake client` + `gomonkey`
#### 2.2.2 新增单元测试清单
重构后需要新增的单元测试：

| 模块 | 测试文件 | 测试内容 |
|------|---------|----------|
| `pkg/versionpackage/` | `provider_test.go` | VersionProvider 接口的 Static 和 CRD 实现 |
| `pkg/versionpackage/` | `resolver_test.go` | ImageResolver 接口的 Static 和 CRD 实现 |
| `pkg/versionpackage/` | `manager_test.go` | VersionPackage 加载、验证、下载 |
| `pkg/compatibility/` | `checker_test.go` | CompatibilityChecker 接口的 Static 和 CRD 实现 |
| `pkg/compatibility/` | `matrix_test.go` | 兼容性矩阵解析和验证 |
| `pkg/cluster/` | `interface_test.go` | Cluster 接口的各实现 |
| `pkg/upgrade/` | `path_test.go` | ClusterUpgradePath 解析和验证 |
| `pkg/upgrade/` | `dag_test.go` | DAG 构建和拓扑排序 |
| `pkg/upgrade/` | `scheduler_test.go` | DAG 调度器并行执行 |
| `pkg/upgrade/` | `executor_test.go` | 升级执行器逻辑 |
| `pkg/asset/` | `definition_test.go` | AssetDefinition 解析和验证 |
| `pkg/asset/` | `dag_test.go` | Asset DAG 依赖解析 |
| `pkg/asset/` | `generator_test.go` | Asset 生成器逻辑 |
| `pkg/installer/` | `installer_test.go` | 组件安装器各模式 |
#### 2.2.3 单元测试模板
```go
// pkg/versionpackage/provider_test.go
package versionpackage

import (
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestStaticVersionProvider_GetDefaultKubernetesVersion(t *testing.T) {
    provider := &StaticVersionProvider{}
    version := provider.GetDefaultKubernetesVersion()
    assert.Equal(t, "v1.25.6", version)
}

func TestStaticVersionProvider_GetEtcdImageTag(t *testing.T) {
    tests := []struct {
        name       string
        k8sVersion string
        expected   string
    }{
        {"v1.25.6", "v1.25.6", "3.5.6-0"},
        {"v1.21.1", "v1.21.1", "3.4.13-0"},
        {"unknown", "v1.99.0", "3.5.21-of.1"},
    }
    provider := &StaticVersionProvider{}
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tag, err := provider.GetEtcdImageTag(tt.k8sVersion)
            assert.NoError(t, err)
            assert.Equal(t, tt.expected, tag)
        })
    }
}

func TestCRDVersionProvider_GetDefaultKubernetesVersion(t *testing.T) {
    scheme := runtime.NewScheme()
    _ = AddToScheme(scheme)
    fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

    provider := &CRDVersionProvider{Client: fakeClient}
    version := provider.GetDefaultKubernetesVersion()
    assert.Equal(t, "v1.25.6", version)
}

func TestCRDVersionProvider_WithVersionPackage(t *testing.T) {
    scheme := runtime.NewScheme()
    _ = AddToScheme(scheme)
    _ = corev1.AddToScheme(scheme)

    vp := &VersionPackage{
        ObjectMeta: metav1.ObjectMeta{Name: "v1.28.0"},
        Spec: VersionPackageSpec{
            Version: "v1.28.0",
            Components: []ComponentVersion{
                {Name: "kubernetes", Version: "v1.28.0"},
                {Name: "etcd", Version: "v3.5.9"},
            },
        },
    }
    fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vp).Build()

    provider := &CRDVersionProvider{Client: fakeClient}
    version := provider.GetDefaultKubernetesVersion()
    assert.Equal(t, "v1.28.0", version)
}
```
### 2.3 集成测试
#### 2.3.1 集成测试框架
使用 `envtest` 搭建集成测试环境，验证控制器和 CRD 的交互：
```go
// test/integration/suite_test.go
package integration

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "testing"

    "github.com/onsi/ginkgo/v2"
    "github.com/onsi/gomega"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes/scheme"
    "k8s.io/client-go/rest"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/envtest"
    logf "sigs.k8s.io/controller-runtime/pkg/log"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"

    versionpackagev1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/versionpackage/v1alpha1"
    compatibilityv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/compatibility/v1alpha1"
    upgradev1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/upgrade/v1alpha1"
    assetv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/asset/v1alpha1"
)

var (
    cfg       *rest.Config
    k8sClient client.Client
    testEnv   *envtest.Environment
    ctx       context.Context
    cancel    context.CancelFunc
)

func TestIntegration(t *testing.T) {
    gomega.RegisterFailHandler(ginkgo.Fail)
    ginkgo.RunSpecs(t, "Integration Suite")
}

var _ = ginkgo.BeforeSuite(func() {
    logf.SetLogger(zap.New(zap.WriteTo(ginkgo.GinkgoWriter), zap.UseDevMode(true)))

    ctx, cancel = context.WithCancel(context.Background())

    testEnv = &envtest.Environment{
        CRDDirectoryPaths: []string{
            filepath.Join("..", "..", "config", "crd", "bases"),
        },
        ErrorIfCRDPathMissing: true,
    }

    var err error
    cfg, err = testEnv.Start()
    gomega.Expect(err).NotTo(gomega.HaveOccurred())
    gomega.Expect(cfg).NotTo(gomega.BeNil())

    err = versionpackagev1alpha1.AddToScheme(scheme.Scheme)
    gomega.Expect(err).NotTo(gomega.HaveOccurred())
    err = compatibilityv1alpha1.AddToScheme(scheme.Scheme)
    gomega.Expect(err).NotTo(gomega.HaveOccurred())
    err = upgradev1alpha1.AddToScheme(scheme.Scheme)
    gomega.Expect(err).NotTo(gomega.HaveOccurred())
    err = assetv1alpha1.AddToScheme(scheme.Scheme)
    gomega.Expect(err).NotTo(gomega.HaveOccurred())

    k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
    gomega.Expect(err).NotTo(gomega.HaveOccurred())
    gomega.Expect(k8sClient).NotTo(gomega.BeNil())

    mgr, err := ctrl.NewManager(cfg, ctrl.Options{
        Scheme: scheme.Scheme,
    })
    gomega.Expect(err).NotTo(gomega.HaveOccurred())

    go func() {
        defer ginkgo.GinkgoRecover()
        err = mgr.Start(ctx)
        gomega.Expect(err).NotTo(gomega.HaveOccurred())
    }()
})

var _ = ginkgo.AfterSuite(func() {
    cancel()
    err := testEnv.Stop()
    gomega.Expect(err).NotTo(gomega.HaveOccurred())
})
```
#### 2.3.2 集成测试用例
**VersionPackage 集成测试**：
```go
// test/integration/versionpackage_test.go
package integration

import (
    "context"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/types"

    versionpackagev1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/versionpackage/v1alpha1"
)

var _ = Describe("VersionPackage Controller", func() {
    const timeout = time.Second * 30
    const interval = time.Second * 1

    Context("When creating a VersionPackage", func() {
        It("Should create successfully and update status", func() {
            ctx := context.Background()
            vp := &versionpackagev1alpha1.VersionPackage{
                ObjectMeta: metav1.ObjectMeta{
                    Name: "v1.28.0",
                },
                Spec: versionpackagev1alpha1.VersionPackageSpec{
                    Version:     "v1.28.0",
                    Description: "Test version package",
                    Components: []versionpackagev1alpha1.ComponentVersion{
                        {Name: "kubernetes", Version: "v1.28.0", Type: "core"},
                        {Name: "etcd", Version: "v3.5.9", Type: "core"},
                        {Name: "containerd", Version: "1.7.11", Type: "runtime"},
                    },
                    Images: []versionpackagev1alpha1.ImageSpec{
                        {ComponentName: "etcd", Name: "etcd", Tag: "3.5.9-0"},
                        {ComponentName: "kubernetes", Name: "kube-apiserver", Tag: "v1.28.0"},
                        {ComponentName: "kubernetes", Name: "kube-controller-manager", Tag: "v1.28.0"},
                        {ComponentName: "kubernetes", Name: "kube-scheduler", Tag: "v1.28.0"},
                        {ComponentName: "pause", Name: "pause", Tag: "3.9"},
                    },
                },
            }

            Expect(k8sClient.Create(ctx, vp)).Should(Succeed())

            Eventually(func() bool {
                fetched := &versionpackagev1alpha1.VersionPackage{}
                err := k8sClient.Get(ctx, types.NamespacedName{Name: "v1.28.0"}, fetched)
                return err == nil && fetched.Status.ObservedGeneration > 0
            }, timeout, interval).Should(BeTrue())
        })

        It("Should reject invalid version format", func() {
            ctx := context.Background()
            vp := &versionpackagev1alpha1.VersionPackage{
                ObjectMeta: metav1.ObjectMeta{
                    Name: "invalid-version",
                },
                Spec: versionpackagev1alpha1.VersionPackageSpec{
                    Version: "not-a-version",
                },
            }
            Expect(k8sClient.Create(ctx, vp)).ShouldNot(Succeed())
        })
    })

    Context("When using VersionPackage for image resolution", func() {
        It("Should resolve all component images correctly", func() {
            ctx := context.Background()
            resolver := versionpackage.NewCRDImageResolver(k8sClient, "deploy.bocloud.k8s:40443")

            images, err := resolver.ResolveAllImages(ctx, "v1.28.0")
            Expect(err).NotTo(HaveOccurred())
            Expect(images).To(HaveKey("etcd"))
            Expect(images["etcd"]).To(ContainSubstring("3.5.9-0"))
            Expect(images).To(HaveKey("kube-apiserver"))
            Expect(images["kube-apiserver"]).To(ContainSubstring("v1.28.0"))
        })
    })
})
```
**CompatibilityMatrix 集成测试**：
```go
// test/integration/compatibility_test.go
package integration

var _ = Describe("CompatibilityMatrix Controller", func() {
    Context("When validating K8s version compatibility", func() {
        It("Should accept versions within range", func() {
            ctx := context.Background()
            cm := createTestCompatibilityMatrix("v1.28.0", "v1.21.0", "v1.30.0")
            Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

            checker := compatibility.NewCRDCompatibilityChecker(k8sClient)
            err := checker.ValidateK8sVersion("v1.25.6")
            Expect(err).NotTo(HaveOccurred())
        })

        It("Should reject versions below minimum", func() {
            ctx := context.Background()
            checker := compatibility.NewCRDCompatibilityChecker(k8sClient)
            err := checker.ValidateK8sVersion("v1.20.0")
            Expect(err).To(HaveOccurred())
            Expect(err.Error()).To(ContainSubstring("below minimum"))
        })

        It("Should allow updating compatibility after VersionPackage release", func() {
            ctx := context.Background()
            cm := createTestCompatibilityMatrix("v1.28.0", "v1.21.0", "v1.28.0")
            Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

            updated := cm.DeepCopy()
            updated.Spec.K8sVersionRange.MaxVersion = "v1.30.0"
            Expect(k8sClient.Update(ctx, updated)).Should(Succeed())

            checker := compatibility.NewCRDCompatibilityChecker(k8sClient)
            err := checker.ValidateK8sVersion("v1.29.0")
            Expect(err).NotTo(HaveOccurred())
        })
    })
})
```
**ClusterUpgradePath DAG 集成测试**：
```go
// test/integration/upgrade_test.go
package integration

var _ = Describe("ClusterUpgradePath Controller", func() {
    Context("When executing DAG-based upgrade", func() {
        It("Should execute independent tasks in parallel", func() {
            ctx := context.Background()
            path := createTestUpgradePath("v1.27.0", "v1.28.0")
            Expect(k8sClient.Create(ctx, path)).Should(Succeed())

            scheduler := upgrade.NewDAGScheduler()
            dag, err := scheduler.BuildDAG(path.Spec.Tasks)
            Expect(err).NotTo(HaveOccurred())

            layers := scheduler.TopologicalSort(dag)
            Expect(len(layers)).To(BeNumerically(">=", 2))

            firstLayer := layers[0]
            for _, task := range firstLayer {
                Expect(task.DependsOn).To(BeEmpty())
            }
        })

        It("Should respect task dependencies", func() {
            scheduler := upgrade.NewDAGScheduler()
            tasks := []upgrade.UpgradeTask{
                {Name: "pre-check", Layer: "management", DependsOn: []string{}},
                {Name: "etcd-upgrade", Layer: "management", DependsOn: []string{"pre-check"}},
                {Name: "k8s-upgrade", Layer: "management", DependsOn: []string{"etcd-upgrade"}},
                {Name: "containerd-upgrade", Layer: "management", DependsOn: []string{"pre-check"}},
            }

            dag, err := scheduler.BuildDAG(tasks)
            Expect(err).NotTo(HaveOccurred())

            layers := scheduler.TopologicalSort(dag)
            Expect(layers[0]).To(HaveLen(1))
            Expect(layers[0][0].Name).To(Equal("pre-check"))
            Expect(layers[1]).To(HaveLen(2))

            layer1Names := make(map[string]bool)
            for _, t := range layers[1] {
                layer1Names[t.Name] = true
            }
            Expect(layer1Names).To(HaveKey("etcd-upgrade"))
            Expect(layer1Names).To(HaveKey("containerd-upgrade"))
        })

        It("Should detect circular dependencies", func() {
            scheduler := upgrade.NewDAGScheduler()
            tasks := []upgrade.UpgradeTask{
                {Name: "a", Layer: "management", DependsOn: []string{"b"}},
                {Name: "b", Layer: "management", DependsOn: []string{"a"}},
            }

            _, err := scheduler.BuildDAG(tasks)
            Expect(err).To(HaveOccurred())
            Expect(err.Error()).To(ContainSubstring("circular"))
        })
    })
})
```
**AssetDefinition 集成测试**：
```go
// test/integration/asset_test.go
package integration

var _ = Describe("AssetDefinition Controller", func() {
    Context("When managing asset dependencies", func() {
        It("Should resolve asset generation order", func() {
            ctx := context.Background()
            assets := []asset.AssetDefinition{
                createAssetDef("kubeconfig", []string{"certs"}),
                createAssetDef("certs", []string{}),
                createAssetDef("manifest", []string{"kubeconfig"}),
            }
            for _, a := range assets {
                Expect(k8sClient.Create(ctx, &a)).Should(Succeed())
            }

            generator := asset.NewDAGGenerator(k8sClient)
            order, err := generator.ResolveGenerationOrder(ctx, "v1.28.0")
            Expect(err).NotTo(HaveOccurred())
            Expect(order[0].Name).To(Equal("certs"))
            Expect(order[1].Name).To(Equal("kubeconfig"))
            Expect(order[2].Name).To(Equal("manifest"))
        })

        It("Should regenerate assets on upgrade", func() {
            ctx := context.Background()
            generator := asset.NewDAGGenerator(k8sClient)

            changedAssets, err := generator.DetectChangedAssets(ctx, "v1.27.0", "v1.28.0")
            Expect(err).NotTo(HaveOccurred())

            for _, a := range changedAssets {
                Expect(a.Name).ToNot(Equal("certs"))
            }
        })
    })
})
```
### 2.4 E2E 测试
#### 2.4.1 E2E 测试场景
| 场景 | 描述 | 验证点 |
|------|------|--------|
| 集群创建 | 从 VersionPackage 创建完整集群 | 所有组件版本正确、镜像拉取成功、Phase 按序执行 |
| 版本升级 | 从 v1.27.0 升级到 v1.28.0 | DAG 并行执行、兼容性检查通过、回滚机制可用 |
| 兼容性更新 | 发布后更新 CompatibilityMatrix | 新版本范围生效、不影响已创建集群 |
| 证书轮换 | Asset 框架驱动证书更新 | DAG 依赖正确、新证书生效 |
| 版本包切换 | Static → CRD VersionProvider | 行为一致、无功能退化 |
#### 2.4.2 E2E 测试框架
```go
// test/e2e/e2e_test.go
package e2e

import (
    "context"
    "fmt"
    "os"
    "testing"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/types"
    "sigs.k8s.io/controller-runtime/pkg/client"

    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
    versionpackagev1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/versionpackage/v1alpha1"
    compatibilityv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/compatibility/v1alpha1"
    upgradev1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/upgrade/v1alpha1"
)

type E2ETestSuite struct {
    Client    client.Client
    Namespace string
}

func (s *E2ETestSuite) Setup() {
    s.Namespace = fmt.Sprintf("e2e-%d", time.Now().Unix())
    ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: s.Namespace}}
    Expect(s.Client.Create(context.Background(), ns)).Should(Succeed())
}

func (s *E2ETestSuite) Teardown() {
    ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: s.Namespace}}
    _ = s.Client.Delete(context.Background(), ns)
}

func TestE2E(t *testing.T) {
    RegisterFailHandler(Fail)
    RunSpecs(t, "E2E Suite")
}

var _ = Describe("Cluster Lifecycle E2E", func() {
    var suite *E2ETestSuite

    BeforeEach(func() {
        suite = &E2ETestSuite{Client: k8sClient}
        suite.Setup()
    })

    AfterEach(func() {
        suite.Teardown()
    })

    Describe("Cluster Provisioning with VersionPackage", func() {
        It("Should provision cluster using VersionPackage CRD", func() {
            ctx := context.Background()

            vp := createVersionPackage("v1.28.0")
            Expect(suite.Client.Create(ctx, vp)).Should(Succeed())

            cm := createCompatibilityMatrix("v1.28.0")
            Expect(suite.Client.Create(ctx, cm)).Should(Succeed())

            bkeCluster := createBKECluster(suite.Namespace, "test-cluster", "v1.28.0")
            Expect(suite.Client.Create(ctx, bkeCluster)).Should(Succeed())

            Eventually(func() string {
                fetched := &bkev1beta1.BKECluster{}
                _ = suite.Client.Get(ctx, types.NamespacedName{
                    Name:      bkeCluster.Name,
                    Namespace: suite.Namespace,
                }, fetched)
                return string(fetched.Status.Phase)
            }, 5*time.Minute, 5*time.Second).Should(Equal("Provisioned"))
        })
    })

    Describe("Cluster Upgrade with DAG", func() {
        It("Should upgrade cluster following DAG tasks", func() {
            ctx := context.Background()

            vp := createVersionPackage("v1.28.0")
            Expect(suite.Client.Create(ctx, vp)).Should(Succeed())

            upgradePath := createUpgradePath("v1.27.0", "v1.28.0")
            Expect(suite.Client.Create(ctx, upgradePath)).Should(Succeed())

            bkeCluster := createProvisionedCluster(suite.Namespace, "upgrade-cluster", "v1.27.0")
            Expect(suite.Client.Create(ctx, bkeCluster)).Should(Succeed())

            updated := bkeCluster.DeepCopy()
            updated.Spec.ClusterConfig.Cluster.KubernetesVersion = "v1.28.0"
            Expect(suite.Client.Update(ctx, updated)).Should(Succeed())

            Eventually(func() string {
                fetched := &bkev1beta1.BKECluster{}
                _ = suite.Client.Get(ctx, types.NamespacedName{
                    Name:      bkeCluster.Name,
                    Namespace: suite.Namespace,
                }, fetched)
                return fetched.Status.KubernetesVersion
            }, 5*time.Minute, 5*time.Second).Should(Equal("v1.28.0"))
        })
    })
})
```
### 2.5 测试工具和辅助函数
#### 2.5.1 测试数据工厂
```go
// test/testutil/factory.go
package testutil

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    versionpackagev1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/versionpackage/v1alpha1"
    compatibilityv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/compatibility/v1alpha1"
    upgradev1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/upgrade/v1alpha1"
    assetv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/asset/v1alpha1"
)

func NewVersionPackage(version string) *versionpackagev1alpha1.VersionPackage {
    return &versionpackagev1alpha1.VersionPackage{
        ObjectMeta: metav1.ObjectMeta{Name: version},
        Spec: versionpackagev1alpha1.VersionPackageSpec{
            Version:     version,
            ReleaseDate: metav1.Now(),
            Components: []versionpackagev1alpha1.ComponentVersion{
                {Name: "kubernetes", Version: version, Type: "core"},
                {Name: "etcd", Version: "v3.5.9", Type: "core"},
                {Name: "containerd", Version: "1.7.11", Type: "runtime"},
            },
            Images: []versionpackagev1alpha1.ImageSpec{
                {ComponentName: "etcd", Name: "etcd", Tag: "3.5.9-0"},
                {ComponentName: "kube-apiserver", Name: "kube-apiserver", Tag: version},
                {ComponentName: "kube-controller-manager", Name: "kube-controller-manager", Tag: version},
                {ComponentName: "kube-scheduler", Name: "kube-scheduler", Tag: version},
                {ComponentName: "pause", Name: "pause", Tag: "3.9"},
            },
        },
    }
}

func NewCompatibilityMatrix(targetVersion, minK8s, maxK8s string) *compatibilityv1alpha1.CompatibilityMatrix {
    return &compatibilityv1alpha1.CompatibilityMatrix{
        ObjectMeta: metav1.ObjectMeta{Name: targetVersion + "-compat"},
        Spec: compatibilityv1alpha1.CompatibilityMatrixSpec{
            TargetVersion: targetVersion,
            K8sVersionRange: compatibilityv1alpha1.VersionRange{
                MinVersion: minK8s,
                MaxVersion: maxK8s,
            },
            ComponentCompat: []compatibilityv1alpha1.ComponentCompatibility{
                {
                    Component:    "containerd",
                    MinVersion:   "1.6.0",
                    MaxVersion:   "1.7.x",
                },
            },
            OSCompatibility: []compatibilityv1alpha1.OSCompatibility{
                {Name: "ubuntu", Versions: []string{"20.04", "22.04"}},
                {Name: "centos", Versions: []string{"7", "8", "9"}},
            },
        },
    }
}

func NewUpgradePath(fromVersion, toVersion string) *upgradev1alpha1.ClusterUpgradePath {
    return &upgradev1alpha1.ClusterUpgradePath{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("%s-to-%s", fromVersion, toVersion),
        },
        Spec: upgradev1alpha1.ClusterUpgradePathSpec{
            FromVersion: fromVersion,
            ToVersion:   toVersion,
            Type:        "solution",
            Tasks: []upgradev1alpha1.UpgradeTask{
                {Name: "pre-check", Layer: "management", DependsOn: []string{}},
                {Name: "etcd-upgrade", Layer: "management", DependsOn: []string{"pre-check"}},
                {Name: "k8s-control-plane", Layer: "management", DependsOn: []string{"etcd-upgrade"}},
                {Name: "containerd-upgrade", Layer: "management", DependsOn: []string{"pre-check"}},
                {Name: "k8s-worker", Layer: "workload", DependsOn: []string{"k8s-control-plane"}},
                {Name: "addon-upgrade", Layer: "workload", DependsOn: []string{"k8s-worker", "containerd-upgrade"}},
            },
        },
    }
}
```
#### 2.5.2 Mock 和 Fake 实现
```go
// test/testutil/mock_interfaces.go
package testutil

type MockVersionProvider struct {
    K8sVersion  string
    EtcdVersion string
    Err         error
}

func (m *MockVersionProvider) GetDefaultKubernetesVersion() string {
    return m.K8sVersion
}

func (m *MockVersionProvider) GetDefaultEtcdVersion() string {
    return m.EtcdVersion
}

func (m *MockVersionProvider) GetEtcdImageTag(k8sVersion string) (string, error) {
    if m.Err != nil {
        return "", m.Err
    }
    return "3.5.9-0", nil
}

func (m *MockVersionProvider) GetPauseImageTag(k8sVersion string) (string, error) {
    if m.Err != nil {
        return "", m.Err
    }
    return "3.9", nil
}

func (m *MockVersionProvider) GetImageRegistry() string {
    return "deploy.bocloud.k8s:40443"
}

type MockCompatibilityChecker struct {
    MinK8s string
    MaxK8s string
    Err    error
}

func (m *MockCompatibilityChecker) ValidateK8sVersion(version string) error {
    return m.Err
}

func (m *MockCompatibilityChecker) ValidateComponentVersion(component, version string) error {
    return m.Err
}

func (m *MockCompatibilityChecker) ValidateOS(name, version string) error {
    return m.Err
}

func (m *MockCompatibilityChecker) GetSupportedK8sRange() (min, max string) {
    return m.MinK8s, m.MaxK8s
}

type MockImageResolver struct {
    Images map[string]string
    Err    error
}

func (m *MockImageResolver) ResolveImage(componentName string) (string, error) {
    if m.Err != nil {
        return "", m.Err
    }
    if img, ok := m.Images[componentName]; ok {
        return img, nil
    }
    return "", fmt.Errorf("image not found: %s", componentName)
}

func (m *MockImageResolver) ResolveAllImages() (map[string]string, error) {
    if m.Err != nil {
        return nil, m.Err
    }
    return m.Images, nil
}

func (m *MockImageResolver) GetImageRegistry() string {
    return "deploy.bocloud.k8s:40443"
}
```
## 三、迁移验证策略
### 3.1 行为一致性验证
确保从硬编码迁移到 CRD 驱动后，系统行为完全一致：
```
验证流程
├── 1. 双轨运行期
│   ├── Static 和 CRD Provider 同时存在
│   ├── 对比两者输出是否一致
│   └── 记录差异并修复
│
├── 2. 灰度切换
│   ├── 通过 Feature Gate 控制切换
│   ├── 先在测试环境切换
│   └── 逐步在生产环境切换
│
└── 3. 完全迁移
    ├── 删除 Static Provider
    ├── 删除硬编码常量
    └── 全量回归测试
```
### 3.2 Feature Gate 设计
```go
// pkg/features/features.go
package features

const (
    CRDVersionProvider     = "CRDVersionProvider"
    CRDCompatibilityCheck  = "CRDCompatibilityCheck"
    CRDImageResolver       = "CRDImageResolver"
    DAGUpgradeScheduler    = "DAGUpgradeScheduler"
    AssetDAGFramework      = "AssetDAGFramework"
)

var defaultKubernetesFeatureGates = map[string]bool{
    CRDVersionProvider:     false,
    CRDCompatibilityCheck:  false,
    CRDImageResolver:       false,
    DAGUpgradeScheduler:    false,
    AssetDAGFramework:      false,
}
```
### 3.3 回归测试检查清单
| 检查项 | 验证方法 | 通过标准 |
|--------|---------|----------|
| 默认版本填充 | 创建 BKECluster 不指定版本 | 版本值与硬编码一致 |
| 版本验证 | 创建 BKECluster 指定非法版本 | 返回与旧逻辑相同的错误 |
| 镜像解析 | 获取各组件镜像 | 镜像名和 tag 与旧逻辑一致 |
| Phase 执行 | 创建集群全流程 | 所有 Phase 正常执行 |
| 升级流程 | 执行版本升级 | DAG 执行顺序正确 |
| 证书生成 | 生成集群证书 | 证书内容与旧逻辑一致 |
| Addon 部署 | 部署集群 Addon | Addon 正常运行 |
| 节点管理 | 添加/删除节点 | 节点状态正确 |
## 四、目录结构
```
pkg/
├── versionpackage/          # 版本包管理
│   ├── provider.go          # VersionProvider 接口
│   ├── provider_test.go
n│   ├── resolver.go          # ImageResolver 接口
│   ├── resolver_test.go
│   ├── manager.go           # VersionPackage 管理器
│   └── manager_test.go
├── compatibility/           # 兼容性检查
│   ├── checker.go           # CompatibilityChecker 接口
│   ├── checker_test.go
│   ├── matrix.go            # 兼容性矩阵解析
│   └── matrix_test.go
├── upgrade/                 # 升级管理
│   ├── path.go              # ClusterUpgradePath CRD
│   ├── dag.go               # DAG 构建和拓扑排序
│   ├── dag_test.go
│   ├── scheduler.go         # DAG 调度器
│   ├── scheduler_test.go
│   ├── executor.go          # 升级执行器
│   └── executor_test.go
├── asset/                   # 资产框架
│   ├── definition.go        # AssetDefinition CRD
│   ├── dag.go               # Asset DAG 依赖
│   ├── dag_test.go
│   ├── generator.go         # 资产生成器
│   └── generator_test.go
├── features/                # Feature Gate
│   └── features.go
└── installer/               # 组件安装器
    ├── installer.go
    └── installer_test.go

test/
├── integration/             # 集成测试
│   ├── suite_test.go
│   ├── versionpackage_test.go
│   ├── compatibility_test.go
│   ├── upgrade_test.go
│   └── asset_test.go
├── e2e/                     # E2E 测试
│   ├── e2e_test.go
│   └── e2e_suite_test.go
└── testutil/                # 测试工具
    ├── factory.go
    └── mock_interfaces.go
```
## 五、工作量评估
| 任务 | 详细内容 | 工作量 |
|------|---------|--------|
| 兼容层实现 | VersionProvider/CompatibilityChecker/ImageResolver 接口和双实现 | 3人天 |
| 调用点迁移 | 将硬编码调用点替换为接口调用 | 5人天 |
| Deprecated 函数删除 | 清理所有 Deprecated 标记的函数 | 2人天 |
| 硬编码常量清理 | 删除版本常量和验证变量 | 1人天 |
| 单元测试 | 新增模块的单元测试 | 5人天 |
| 集成测试 | envtest 集成测试框架和用例 | 5人天 |
| E2E 测试 | 完整流程 E2E 测试 | 3人天 |
| Feature Gate | 实现灰度切换机制 | 2人天 |
| 回归验证 | 双轨运行和一致性验证 | 3人天 |
| **总计** | | **29人天** |
## 六、风险与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 迁移过程中引入行为差异 | 集群创建/升级失败 | 双轨运行期对比验证，Feature Gate 灰度切换 |
| envtest 环境搭建复杂 | 集成测试延迟 | 先用 fake client 完成单元测试，envtest 逐步引入 |
| Deprecated 函数有隐式调用方 | 删除后编译失败 | 全局搜索确认所有调用点，分批删除 |
| CRD 不可用时系统不可用 | 集群无法创建 | Static Provider 作为 fallback，CRD 不可用时自动降级 |
| DAG 循环依赖检测遗漏 | 升级死锁 | DAG 构建时强制检测循环依赖，单元测试覆盖 |
              
oldStr: 	return remoteResult, nil
}
```
以上是阶段七（清理 + 集成测试）的完整详细设计，主要包含以下核心内容：
### 设计要点总结
**一、遗留代码清理**
1. **Deprecated 函数清理**：识别了 15+ 个 Deprecated 函数，分为两类处理策略：
   - **已有替代的**（如 `GetAPIServerCertAltNamesFromBkeConfig` → `GetAPIServerCertAltNamesWithNodes`）：直接删除
   - **需要迁移到 CRD 的**（如 `GetDefaultEtcdK8sVersionImageMap` → VersionPackage.images）：先迁移再删除
2. **硬编码版本常量清理**：通过 `VersionProvider` 接口实现兼容层，支持 Static（旧逻辑）和 CRD（新逻辑）双实现，确保迁移过程中行为一致
3. **硬编码版本验证清理**：通过 `CompatibilityChecker` 接口将 `MinSupportedK8sVersion`/`MaxSupportedK8sVersion` 迁移到 CompatibilityMatrix CRD
4. **镜像映射逻辑清理**：通过 `ImageResolver` 接口将 `imagehelper/exporter.go` 的硬编码映射迁移到 VersionPackage.images
5. **四阶段清理计划**：兼容层实现 → 调用点迁移 → Deprecated 删除 → 常量清理

**二、测试体系设计**
1. **测试金字塔**：单元测试（大量）→ 集成测试（中等）→ E2E 测试（少量）
2. **单元测试**：14 个新增测试文件，覆盖 VersionPackage/Compatibility/Upgrade/Asset/Installer 模块
3. **集成测试**：基于 `envtest` 框架，验证 CRD 控制器交互、DAG 调度、兼容性检查等
4. **E2E 测试**：验证完整集群生命周期（创建、升级、兼容性更新、证书轮换）
5. **测试工具**：数据工厂（`factory.go`）和 Mock 实现（`mock_interfaces.go`）

**三、迁移验证策略**
1. **双轨运行**：Static 和 CRD Provider 同时存在，对比输出一致性
2. **Feature Gate**：5 个特性门控，支持灰度切换
3. **回归测试检查清单**：8 项关键检查点，确保迁移无功能退化

**四、工作量**：总计 29 人天

请 Review 并 Accept 上述 diff 以将阶段七设计追加到 [refactor.md](file:///d:/code/github/cluster-api-provider-bke/code/refactor.md)。
        
