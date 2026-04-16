# 阶段五：OS Provider 抽象 — 详细设计
## 1. 当前问题分析
通过代码审查，OS 相关硬编码逻辑散布在以下位置：
### 1.1 OS 检测与平台判断
| 位置 | 模式 | 说明 |
|------|------|------|
| [machine.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/machine.go) | `h.Platform` | 通过 gopsutil 获取平台信息 |
| [const.go](file:///d:/code/github/cluster-api-provider-bke/utils/const.go) | `GetSupportPlatforms()` | 硬编码支持列表 `["centos", "kylin", "ubuntu"]` |
| [httprepo/helper.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/httprepo/helper.go) | `init()` switch | 包管理器选择 `apt` vs `yum` |
### 1.2 OS 特定初始化逻辑
| 位置 | 函数 | OS 判断 | 说明 |
|------|------|---------|------|
| [init.go:174](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L174) | `setupCentos7DetachMounts()` | `platform != "centos"` | CentOS 7 + containerd 特殊参数 |
| [init.go:272](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L272) | `setupUbuntuModules()` | `platform != "ubuntu"` | Ubuntu 模块写入 `/etc/modules` |
| [init.go:295](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L295) | `setupCentosKylinModules()` | `platform != "centos" && != "kylin"` | CentOS/Kylin 模块文件 |
| [init.go:312](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L312) | `setupKylinRcLocal()` | `platform != "kylin"` | Kylin rc.local 配置 |
| [init.go:1061](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L1061) | `installLxcfs()` | switch platform | 各平台 lxcfs 安装方式不同 |
| [init.go:752](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L752) | `initDNS()` | `platform == "centos"` | CentOS NetworkManager 特殊处理 |
| [init.go:378](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L378) | `initSelinux()` | `platform == "ubuntu"` | Ubuntu/OpenEuler 跳过 SELinux |
| [init.go:1030](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L1030) | `installNfsUtilIfNeeded()` | `platform == "ubuntu"` | Ubuntu 用 nfs-common，其他用 nfs-utils |
### 1.3 OS 特定检查逻辑
| 位置 | 函数 | OS 判断 | 说明 |
|------|------|---------|------|
| [check.go:161](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/check.go#L161) | `checkUbuntuSysModules()` | `platform != "ubuntu"` | Ubuntu 模块检查 |
| [check.go:184](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/check.go#L184) | `checkCentosKylinSysModules()` | `platform != "centos" && != "kylin"` | CentOS/Kylin 模块检查 |
| [check.go:201](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/check.go#L201) | `checkKylinRcLocal()` | `platform != "kylin"` | Kylin rc.local 检查 |
| [check.go:246](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/check.go#L246) | `checkSelinux()` | `platform == "ubuntu"` | Ubuntu 跳过 SELinux 检查 |
### 1.4 包管理与源管理
| 位置 | 函数 | OS 判断 | 说明 |
|------|------|---------|------|
| [source.go](file:///d:/code/github/cluster-api-provider-bke/common/source/source.go) | `GetRPMDownloadPath()` | switch Platform | 各 OS 下载路径不同 |
| [source.go](file:///d:/code/github/cluster-api-provider-bke/common/source/source.go) | `SetSource()` | `strings.Contains(baseurl, "Ubuntu")` | apt vs yum 源配置 |
| [source.go](file:///d:/code/github/cluster-api-provider-bke/common/source/source.go) | `ResetSource()` | switch Platform | 源恢复 |
| [httprepo/helper.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/httprepo/helper.go) | `init()` | switch Platform | 包管理器初始化 |
### 1.5 核心问题总结
1. **新增 OS 需修改多处代码**：添加 openEuler 支持需要修改 `const.go`、`init.go`、`check.go`、`source.go`、`helper.go` 等
2. **OS 判断逻辑分散**：同一 OS 的逻辑散布在不同文件，无法集中管理
3. **缺乏 OS 能力抽象**：无法声明式描述 OS 能力差异（如是否支持 SELinux、使用什么包管理器）
4. **测试困难**：OS 特定逻辑与业务逻辑耦合，难以单元测试
## 2. OSProvider 接口设计
### 2.1 核心接口定义
```go
// pkg/osprovider/provider.go

package osprovider

import (
    "context"
)

type OSName string

const (
    CentOS    OSName = "centos"
    Ubuntu    OSName = "ubuntu"
    Kylin     OSName = "kylin"
    OpenEuler OSName = "openeuler"
    HopeOS    OSName = "hopeos"
    EulerOS   OSName = "euleros"
)

type PackageType string

const (
    PackageTypeRPM PackageType = "rpm"
    PackageTypeDEB PackageType = "deb"
)

type OSInfo struct {
    Name       OSName
    Version    string
    MajorVer   string
    Kernel     string
    Arch       string
    PackageType PackageType
}

type OSProvider interface {
    Name() OSName
    Detect(ctx context.Context, info *OSInfo) bool
    PackageType() PackageType
    SupportedVersions() []string
    
    FirewallOps
    SELinuxOps
    KernelOps
    ModuleOps
    PackageOps
    ServiceOps
    DNSOps
    LxcfsOps
    SourceOps
}

type FirewallOps interface {
    DisableFirewall(ctx context.Context, exec CommandExecutor) error
    CheckFirewallDisabled(ctx context.Context, exec CommandExecutor) error
    FirewallType() string
}

type SELinuxOps interface {
    DisableSELinux(ctx context.Context, exec CommandExecutor) error
    CheckSELinuxDisabled(ctx context.Context, exec CommandExecutor) error
    SupportsSELinux() bool
}

type KernelOps interface {
    KernelParamFilePath() string
    ExtraKernelParams(ctx context.Context, info *OSInfo, cfg *KernelConfig) map[string]string
    SwapFilePath() string
}

type ModuleOps interface {
    ModuleFilePath() string
    SetupModules(ctx context.Context, exec CommandExecutor, modules []string) error
    CheckModules(ctx context.Context, exec CommandExecutor, modules []string) error
    PostModuleSetup(ctx context.Context, exec CommandExecutor) error
}

type PackageOps interface {
    InstallPackages(ctx context.Context, exec CommandExecutor, packages ...string) error
    RemovePackages(ctx context.Context, exec CommandExecutor, packages ...string) error
    SearchPackage(ctx context.Context, exec CommandExecutor, pkg string) error
    UpdateRepo(ctx context.Context, exec CommandExecutor) error
    PackageName(genericName string) string
}

type ServiceOps interface {
    ServiceName(genericName string) string
    DisableService(ctx context.Context, exec CommandExecutor, service string) error
}

type DNSOps interface {
    ConfigureDNS(ctx context.Context, exec CommandExecutor) error
    CheckDNS(ctx context.Context, exec CommandExecutor) error
    NeedsNetworkManagerFix() bool
}

type LxcfsOps interface {
    LxcfsPackages() []string
    PostLxcfsInstall(ctx context.Context, exec CommandExecutor) error
}

type SourceOps interface {
    SourceDir() string
    SourceTemplate() string
    BackupSource(ctx context.Context, exec CommandExecutor) error
    WriteSource(ctx context.Context, exec CommandExecutor, baseURL string) error
    ResetSource(ctx context.Context, exec CommandExecutor) error
    DownloadPath(baseURL string, info *OSInfo) string
}

type CommandExecutor interface {
    ExecuteCommand(cmd string, args ...string) error
    ExecuteCommandWithOutput(cmd string, args ...string) (string, error)
    ExecuteCommandWithCombinedOutput(cmd string, args ...string) (string, error)
}

type KernelConfig struct {
    CRI           string
    ProxyMode     string
    IPMode        string
}
```
### 2.2 接口设计说明
**设计原则：**
1. **组合优于继承**：使用小接口组合（`FirewallOps`、`SELinuxOps` 等），每个 OS Provider 可以选择性实现
2. **能力声明式**：通过 `SupportsSELinux()`、`NeedsNetworkManagerFix()` 等方法声明 OS 能力
3. **名称映射**：通过 `PackageName()`、`ServiceName()` 实现通用名到 OS 特定名的映射
4. **上下文传递**：所有操作接受 `context.Context`，支持超时和取消
## 3. Provider 注册与发现机制
### 3.1 Registry 设计
```go
// pkg/osprovider/registry.go

package osprovider

import (
    "fmt"
    "sync"
)

var (
    globalRegistry = &Registry{
        providers: make(map[OSName]OSProvider),
    }
)

type Registry struct {
    mu        sync.RWMutex
    providers map[OSName]OSProvider
}

func GlobalRegistry() *Registry {
    return globalRegistry
}

func (r *Registry) Register(provider OSProvider) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    name := provider.Name()
    if _, exists := r.providers[name]; exists {
        return fmt.Errorf("OS provider %q already registered", name)
    }
    r.providers[name] = provider
    return nil
}

func (r *Registry) MustRegister(provider OSProvider) {
    if err := r.Register(provider); err != nil {
        panic(err)
    }
}

func (r *Registry) Get(name OSName) (OSProvider, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    p, ok := r.providers[name]
    return p, ok
}

func (r *Registry) List() []OSName {
    r.mu.RLock()
    defer r.mu.RUnlock()
    names := make([]OSName, 0, len(r.providers))
    for name := range r.providers {
        names = append(names, name)
    }
    return names
}

func (r *Registry) Detect(ctx context.Context, info *OSInfo) (OSProvider, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()

    for _, provider := range r.providers {
        if provider.Detect(ctx, info) {
            return provider, nil
        }
    }
    return nil, fmt.Errorf("no OS provider found for %s %s", info.Name, info.Version)
}

func Register(provider OSProvider) error {
    return globalRegistry.Register(provider)
}

func MustRegister(provider OSProvider) {
    globalRegistry.MustRegister(provider)
}

func Get(name OSName) (OSProvider, bool) {
    return globalRegistry.Get(name)
}

func Detect(ctx context.Context, info *OSInfo) (OSProvider, error) {
    return globalRegistry.Detect(ctx, info)
}
```
### 3.2 OS 信息检测
```go
// pkg/osprovider/detect.go

package osprovider

import (
    "context"
    "runtime"
    "strings"

    "github.com/shirou/gopsutil/v3/host"
)

func DetectOSInfo(ctx context.Context) (*OSInfo, error) {
    h, err := host.Info()
    if err != nil {
        return nil, err
    }

    info := &OSInfo{
        Name:       OSName(strings.ToLower(h.Platform)),
        Version:    h.PlatformVersion,
        Kernel:     h.KernelVersion,
        Arch:       runtime.GOARCH,
    }

    if !strings.Contains(h.PlatformVersion, ".") {
        info.MajorVer = strings.ToUpper(h.PlatformVersion)
    } else {
        parts := strings.Split(h.PlatformVersion, ".")
        if len(parts) >= 1 {
            info.MajorVer = parts[0]
        }
    }

    info.PackageType = detectPackageType(info.Name)

    return info, nil
}

func detectPackageType(name OSName) PackageType {
    switch name {
    case Ubuntu:
        return PackageTypeDEB
    case CentOS, Kylin, OpenEuler, HopeOS, EulerOS:
        return PackageTypeRPM
    default:
        return PackageTypeRPM
    }
}
```
## 4. 内置 Provider 实现
### 4.1 BaseProvider — 公共逻辑
```go
// pkg/osprovider/base/base.go

package base

import (
    "context"
    "fmt"
    "strings"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider"
)

type BaseProvider struct {
    name          osprovider.OSName
    pkgType       osprovider.PackageType
    versions      []string
    supportsSEL   bool
    needsNMFix    bool
}

func (b *BaseProvider) Name() osprovider.OSName          { return b.name }
func (b *BaseProvider) PackageType() osprovider.PackageType { return b.pkgType }
func (b *BaseProvider) SupportedVersions() []string       { return b.versions }
func (b *BaseProvider) SupportsSELinux() bool             { return b.supportsSEL }
func (b *BaseProvider) NeedsNetworkManagerFix() bool      { return b.needsNMFix }

func (b *BaseProvider) KernelParamFilePath() string {
    return "/etc/sysctl.d/k8s.conf"
}

func (b *BaseProvider) SwapFilePath() string {
    return "/etc/sysctl.d/k8s-swap.conf"
}

func (b *BaseProvider) FirewallType() string {
    if b.pkgType == osprovider.PackageTypeDEB {
        return "ufw"
    }
    return "firewalld"
}

func (b *BaseProvider) DisableFirewall(ctx context.Context, exec osprovider.CommandExecutor) error {
    if b.pkgType == osprovider.PackageTypeDEB {
        if out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "ufw disable"); err != nil {
            return fmt.Errorf("disable ufw failed: %w, output: %s", err, out)
        }
        return nil
    }
    if out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "systemctl stop firewalld"); err != nil {
        return fmt.Errorf("stop firewalld failed: %w, output: %s", err, out)
    }
    if out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "systemctl disable firewalld"); err != nil {
        return fmt.Errorf("disable firewalld failed: %w, output: %s", err, out)
    }
    return nil
}

func (b *BaseProvider) CheckFirewallDisabled(ctx context.Context, exec osprovider.CommandExecutor) error {
    if b.pkgType == osprovider.PackageTypeDEB {
        out, err := exec.ExecuteCommandWithOutput("/bin/sh", "-c", "ufw status")
        if err == nil && strings.Contains(out, "inactive") {
            return nil
        }
        return fmt.Errorf("ufw is still active")
    }
    out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "systemctl status firewalld | grep dead")
    if err != nil && !strings.Contains(out, "not loaded") && !strings.Contains(out, "not be found") {
        return fmt.Errorf("firewalld not disabled")
    }
    return nil
}

func (b *BaseProvider) DisableSELinux(ctx context.Context, exec osprovider.CommandExecutor) error {
    if !b.supportsSEL {
        return nil
    }
    if out, err := exec.ExecuteCommandWithOutput("/bin/sh", "-c", "setenforce 0"); err != nil {
        return fmt.Errorf("setenforce 0 failed: %w, output: %s", err, out)
    }
    return nil
}

func (b *BaseProvider) CheckSELinuxDisabled(ctx context.Context, exec osprovider.CommandExecutor) error {
    if !b.supportsSEL {
        return nil
    }
    out, err := exec.ExecuteCommandWithOutput("/bin/sh", "-c", "getenforce")
    if err == nil && (out == "Disabled" || out == "Permissive") {
        return nil
    }
    return fmt.Errorf("SELinux not disabled: %s", out)
}

func (b *BaseProvider) ExtraKernelParams(ctx context.Context, info *osprovider.OSInfo, cfg *osprovider.KernelConfig) map[string]string {
    return map[string]string{}
}

func (b *BaseProvider) InstallPackages(ctx context.Context, exec osprovider.CommandExecutor, packages ...string) error {
    pkgMgr := "yum"
    if b.pkgType == osprovider.PackageTypeDEB {
        pkgMgr = "apt"
    }
    out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c",
        fmt.Sprintf("%s install -y %s", pkgMgr, strings.Join(packages, " ")))
    if err != nil {
        return fmt.Errorf("install packages %v failed: %w, output: %s", packages, err, out)
    }
    return nil
}

func (b *BaseProvider) RemovePackages(ctx context.Context, exec osprovider.CommandExecutor, packages ...string) error {
    pkgMgr := "yum"
    if b.pkgType == osprovider.PackageTypeDEB {
        pkgMgr = "apt"
    }
    out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c",
        fmt.Sprintf("%s remove -y %s", pkgMgr, strings.Join(packages, " ")))
    if err != nil {
        return fmt.Errorf("remove packages %v failed: %w, output: %s", packages, err, out)
    }
    if b.pkgType == osprovider.PackageTypeDEB {
        exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c",
            fmt.Sprintf("apt purge -y %s", strings.Join(packages, " ")))
    }
    return nil
}

func (b *BaseProvider) SearchPackage(ctx context.Context, exec osprovider.CommandExecutor, pkg string) error {
    pkgMgr := "yum"
    if b.pkgType == osprovider.PackageTypeDEB {
        pkgMgr = "apt"
    }
    out, err := exec.ExecuteCommandWithOutput("/bin/sh", "-c",
        fmt.Sprintf("%s search %s 2>/dev/null | grep -w %s", pkgMgr, pkg, pkg))
    if err != nil || !strings.Contains(out, pkg) {
        return fmt.Errorf("package %q not found", pkg)
    }
    return nil
}

func (b *BaseProvider) UpdateRepo(ctx context.Context, exec osprovider.CommandExecutor) error {
    if b.pkgType == osprovider.PackageTypeDEB {
        out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "apt update")
        if err != nil {
            return fmt.Errorf("apt update failed: %w, output: %s", err, out)
        }
        return nil
    }
    if out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "yum clean all"); err != nil {
        return fmt.Errorf("yum clean all failed: %w, output: %s", err, out)
    }
    if out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "yum makecache"); err != nil {
        return fmt.Errorf("yum makecache failed: %w, output: %s", err, out)
    }
    return nil
}

func (b *BaseProvider) PackageName(genericName string) string {
    return genericName
}

func (b *BaseProvider) ServiceName(genericName string) string {
    return genericName
}

func (b *BaseProvider) DisableService(ctx context.Context, exec osprovider.CommandExecutor, service string) error {
    out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c",
        fmt.Sprintf("systemctl disable %s", service))
    if err != nil {
        return fmt.Errorf("disable service %s failed: %w, output: %s", service, err, out)
    }
    return nil
}

func (b *BaseProvider) ConfigureDNS(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (b *BaseProvider) CheckDNS(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (b *BaseProvider) PostLxcfsInstall(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (b *BaseProvider) PostModuleSetup(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}
```
### 4.2 CentOS Provider
```go
// pkg/osprovider/centos/centos.go

package centos

import (
    "context"
    "fmt"
    "os"
    "strings"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider/base"
)

type CentOSProvider struct {
    base.BaseProvider
}

func New() *CentOSProvider {
    return &CentOSProvider{
        BaseProvider: base.BaseProvider{
            name:       osprovider.CentOS,
            pkgType:    osprovider.PackageTypeRPM,
            versions:   []string{"7", "8", "9"},
            supportsSEL: true,
            needsNMFix: true,
        },
    }
}

func (p *CentOSProvider) Detect(ctx context.Context, info *osprovider.OSInfo) bool {
    return info.Name == osprovider.CentOS
}

func (p *CentOSProvider) ModuleFilePath() string {
    return "/etc/sysconfig/modules/ip_vs.modules"
}

func (p *CentOSProvider) SetupModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    return nil
}

func (p *CentOSProvider) CheckModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    path := "/etc/sysconfig/modules/ip_vs.modules"
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return fmt.Errorf("%s not found", path)
    }
    return nil
}

func (p *CentOSProvider) ExtraKernelParams(ctx context.Context, info *osprovider.OSInfo, cfg *osprovider.KernelConfig) map[string]string {
    params := map[string]string{}
    if strings.HasPrefix(info.Version, "7") && cfg != nil && cfg.CRI == "containerd" {
        params["fs.may_detach_mounts"] = "1"
    }
    return params
}

func (p *CentOSProvider) ConfigureDNS(ctx context.Context, exec osprovider.CommandExecutor) error {
    if p.NeedsNetworkManagerFix() {
        out, err := exec.ExecuteCommandWithOutput("/bin/sh", "-c", "systemctl restart NetworkManager")
        if err != nil {
            return fmt.Errorf("restart NetworkManager failed: %w, output: %s", err, out)
        }
    }
    return nil
}

func (p *CentOSProvider) LxcfsPackages() []string {
    return []string{"lxcfs"}
}

func (p *CentOSProvider) PackageName(genericName string) string {
    switch genericName {
    case "nfs":
        return "nfs-utils"
    default:
        return genericName
    }
}

func (p *CentOSProvider) SourceDir() string {
    return "/etc/yum.repos.d"
}

func (p *CentOSProvider) SourceTemplate() string {
    return `[bke]
baseurl = {{.baseurl}}
enabled = 1
gpgcheck = 0
name = repo
`
}

func (p *CentOSProvider) BackupSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (p *CentOSProvider) WriteSource(ctx context.Context, exec osprovider.CommandExecutor, baseURL string) error {
    return nil
}

func (p *CentOSProvider) ResetSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (p *CentOSProvider) DownloadPath(baseURL string, info *osprovider.OSInfo) string {
    if !strings.HasSuffix(baseURL, "/") {
        baseURL += "/"
    }
    return baseURL + "CentOS/" + info.MajorVer + "/" + info.Arch
}

func init() {
    osprovider.MustRegister(New())
}
```
### 4.3 Ubuntu Provider
```go
// pkg/osprovider/ubuntu/ubuntu.go

package ubuntu

import (
    "context"
    "fmt"
    "os"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider/base"
)

type UbuntuProvider struct {
    base.BaseProvider
}

func New() *UbuntuProvider {
    return &UbuntuProvider{
        BaseProvider: base.BaseProvider{
            name:       osprovider.Ubuntu,
            pkgType:    osprovider.PackageTypeDEB,
            versions:   []string{"18.04", "20.04", "22.04", "24.04"},
            supportsSEL: false,
            needsNMFix: false,
        },
    }
}

func (p *UbuntuProvider) Detect(ctx context.Context, info *osprovider.OSInfo) bool {
    return info.Name == osprovider.Ubuntu
}

func (p *UbuntuProvider) ModuleFilePath() string {
    return "/etc/modules"
}

func (p *UbuntuProvider) SetupModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    for _, m := range modules {
        out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c",
            fmt.Sprintf("grep -w %s /etc/modules || echo %q >> /etc/modules", m, m))
        if err != nil {
            return fmt.Errorf("add module %s to /etc/modules failed: %w, output: %s", m, err, out)
        }
    }
    return nil
}

func (p *UbuntuProvider) CheckModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    if _, err := os.Stat("/etc/modules"); os.IsNotExist(err) {
        return fmt.Errorf("/etc/modules not found")
    }
    return nil
}

func (p *UbuntuProvider) LxcfsPackages() []string {
    return []string{"lxcfs"}
}

func (p *UbuntuProvider) PackageName(genericName string) string {
    switch genericName {
    case "nfs":
        return "nfs-common"
    default:
        return genericName
    }
}

func (p *UbuntuProvider) SourceDir() string {
    return "/etc/apt"
}

func (p *UbuntuProvider) SourceTemplate() string {
    return `deb [trusted=yes] {{.baseurl}} ./
`
}

func (p *UbuntuProvider) BackupSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return os.Rename("/etc/apt/sources.list", "/etc/apt/sources.list.bak")
}

func (p *UbuntuProvider) WriteSource(ctx context.Context, exec osprovider.CommandExecutor, baseURL string) error {
    return nil
}

func (p *UbuntuProvider) ResetSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (p *UbuntuProvider) DownloadPath(baseURL string, info *osprovider.OSInfo) string {
    if !strings.HasSuffix(baseURL, "/") {
        baseURL += "/"
    }
    return baseURL + "Ubuntu/" + info.MajorVer + "/" + info.Arch
}

func init() {
    osprovider.MustRegister(New())
}
```
### 4.4 Kylin Provider
```go
// pkg/osprovider/kylin/kylin.go

package kylin

import (
    "context"
    "fmt"
    "os"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider/base"
)

type KylinProvider struct {
    base.BaseProvider
}

func New() *KylinProvider {
    return &KylinProvider{
        BaseProvider: base.BaseProvider{
            name:       osprovider.Kylin,
            pkgType:    osprovider.PackageTypeRPM,
            versions:   []string{"V10"},
            supportsSEL: true,
            needsNMFix: false,
        },
    }
}

func (p *KylinProvider) Detect(ctx context.Context, info *osprovider.OSInfo) bool {
    return info.Name == osprovider.Kylin
}

func (p *KylinProvider) ModuleFilePath() string {
    return "/etc/sysconfig/modules/ip_vs.modules"
}

func (p *KylinProvider) SetupModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    return nil
}

func (p *KylinProvider) CheckModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    path := "/etc/sysconfig/modules/ip_vs.modules"
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return fmt.Errorf("%s not found", path)
    }
    return nil
}

func (p *KylinProvider) PostModuleSetup(ctx context.Context, exec osprovider.CommandExecutor) error {
    rcLocal := "/etc/rc.d/rc.local"
    source := "source /etc/sysconfig/modules/ip_vs.modules"
    if _, err := os.Stat(rcLocal); os.IsNotExist(err) {
        return os.WriteFile(rcLocal, []byte(source), 0755)
    }
    out, err := exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c",
        fmt.Sprintf("echo %q >> %s", source, rcLocal))
    if err != nil {
        return fmt.Errorf("add to rc.local failed: %w, output: %s", err, out)
    }
    exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "chmod +x /etc/rc.d/rc.local")
    return nil
}

func (p *KylinProvider) LxcfsPackages() []string {
    return []string{"lxcfs", "lxcfs-tools"}
}

func (p *KylinProvider) PackageName(genericName string) string {
    switch genericName {
    case "nfs":
        return "nfs-utils"
    default:
        return genericName
    }
}

func (p *KylinProvider) SourceDir() string {
    return "/etc/yum.repos.d"
}

func (p *KylinProvider) SourceTemplate() string {
    return `[bke]
baseurl = {{.baseurl}}
enabled = 1
gpgcheck = 0
name = repo
`
}

func (p *KylinProvider) BackupSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (p *KylinProvider) WriteSource(ctx context.Context, exec osprovider.CommandExecutor, baseURL string) error {
    return nil
}

func (p *KylinProvider) ResetSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (p *KylinProvider) DownloadPath(baseURL string, info *osprovider.OSInfo) string {
    if !strings.HasSuffix(baseURL, "/") {
        baseURL += "/"
    }
    return baseURL + "Kylin/" + info.MajorVer + "/" + info.Arch
}

func init() {
    osprovider.MustRegister(New())
}
```
### 4.5 OpenEuler Provider
```go
// pkg/osprovider/openeuler/openeuler.go

package openeuler

import (
    "context"
    "fmt"
    "os"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider/base"
)

type OpenEulerProvider struct {
    base.BaseProvider
}

func New() *OpenEulerProvider {
    return &OpenEulerProvider{
        BaseProvider: base.BaseProvider{
            name:       osprovider.OpenEuler,
            pkgType:    osprovider.PackageTypeRPM,
            versions:   []string{"20.03", "22.03"},
            supportsSEL: false,
            needsNMFix: false,
        },
    }
}

func (p *OpenEulerProvider) Detect(ctx context.Context, info *osprovider.OSInfo) bool {
    return info.Name == osprovider.OpenEuler
}

func (p *OpenEulerProvider) ModuleFilePath() string {
    return "/etc/sysconfig/modules/ip_vs.modules"
}

func (p *OpenEulerProvider) SetupModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    return nil
}

func (p *OpenEulerProvider) CheckModules(ctx context.Context, exec osprovider.CommandExecutor, modules []string) error {
    path := "/etc/sysconfig/modules/ip_vs.modules"
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return fmt.Errorf("%s not found", path)
    }
    return nil
}

func (p *OpenEulerProvider) LxcfsPackages() []string {
    return []string{"lxcfs"}
}

func (p *OpenEulerProvider) PackageName(genericName string) string {
    switch genericName {
    case "nfs":
        return "nfs-utils"
    default:
        return genericName
    }
}

func (p *OpenEulerProvider) SourceDir() string {
    return "/etc/yum.repos.d"
}

func (p *OpenEulerProvider) SourceTemplate() string {
    return `[bke]
baseurl = {{.baseurl}}
enabled = 1
gpgcheck = 0
name = repo
`
}

func (p *OpenEulerProvider) BackupSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (p *OpenEulerProvider) WriteSource(ctx context.Context, exec osprovider.CommandExecutor, baseURL string) error {
    return nil
}

func (p *OpenEulerProvider) ResetSource(ctx context.Context, exec osprovider.CommandExecutor) error {
    return nil
}

func (p *OpenEulerProvider) DownloadPath(baseURL string, info *osprovider.OSInfo) string {
    if !strings.HasSuffix(baseURL, "/") {
        baseURL += "/"
    }
    return baseURL + "OpenEuler/" + info.MajorVer + "/" + info.Arch
}

func init() {
    osprovider.MustRegister(New())
}
```
## 5. EnvPlugin 重构 — 消除 OS 硬编码
### 5.1 重构后的 EnvPlugin 结构
```go
// pkg/job/builtin/kubeadm/env/env.go (重构后)

type EnvPlugin struct {
    exec      exec.Executor
    k8sClient client.Client

    bkeConfig   *bkev1beta1.BKEConfig
    bkeConfigNS string
    currenNode  bkenode.Node
    nodes       bkenode.Nodes

    sudo   string
    scope  string
    backup string

    extraHosts   string
    clusterHosts []string
    hostPort     []string

    machine  *Machine
    osProvider osprovider.OSProvider
    osInfo    *osprovider.OSInfo
}
```
### 5.2 初始化时获取 Provider
```go
func New(exec exec.Executor, cfg *bkev1beta1.BKEConfig) plugin.Plugin {
    machine := NewMachine()
    
    osInfo := &osprovider.OSInfo{
        Name:     osprovider.OSName(machine.platform),
        Version:  machine.version,
        MajorVer: extractMajorVersion(machine.version),
        Kernel:   machine.kernel,
        Arch:     machine.hostArch,
    }
    
    var provider osprovider.OSProvider
    if p, err := osprovider.Get(osInfo.Name); err == nil {
        provider = p
    } else {
        provider, _ = osprovider.Detect(context.Background(), osInfo)
    }

    return &EnvPlugin{
        exec:       exec,
        bkeConfig:  cfg,
        machine:    machine,
        osProvider: provider,
        osInfo:     osInfo,
    }
}
```
### 5.3 重构 initKernelParam — 消除 OS 判断
**重构前**（[init.go:328-345](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L328)）：
```go
func (ep *EnvPlugin) initKernelParam() error {
    // ...
    ep.setupCentos7DetachMounts()    // OS 硬编码
    ep.setupIPVSConfig()
    // ...
    errs = append(errs, ep.setupUbuntuModules()...)      // OS 硬编码
    errs = append(errs, ep.setupCentosKylinModules()...)  // OS 硬编码
    errs = append(errs, ep.setupKylinRcLocal()...)        // OS 硬编码
    // ...
}
```
**重构后**：
```go
func (ep *EnvPlugin) initKernelParam() error {
    f, err := os.OpenFile(ep.osProvider.KernelParamFilePath(), os.O_CREATE|os.O_TRUNC|os.O_RDWR, RwRR)
    if err != nil {
        return errors.Wrap(err, "Open file failed when init kernel net bridge")
    }
    defer f.Close()

    ep.setupUlimit()

    if ep.bkeConfig != nil {
        cfg := &osprovider.KernelConfig{
            CRI:       ep.bkeConfig.Cluster.ContainerRuntime.CRI,
            ProxyMode: ep.bkeConfig.CustomExtra["proxyMode"],
            IPMode:    DefaultIpMode,
        }
        extraParams := ep.osProvider.ExtraKernelParams(ctx, ep.osInfo, cfg)
        for k, v := range extraParams {
            execKernelParam[k] = v
        }
        ep.setupIPVSConfig()
    }

    var errs []error
    errs = append(errs, ep.writeKernelParams(f)...)
    errs = append(errs, ep.loadSysModules()...)
    errs = append(errs, ep.osProvider.SetupModules(ctx, ep.exec, sysModule)...)
    errs = append(errs, ep.osProvider.PostModuleSetup(ctx, ep.exec)...)

    return kerrors.NewAggregate(errs)
}
```
### 5.4 重构 initSelinux — 消除 OS 判断
**重构前**（[init.go:378](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L378)）：
```go
func (ep *EnvPlugin) initSelinux() error {
    if ep.machine.platform == utils.UbuntuOS || ep.machine.platform == utils.OpenEulerOS {
        return nil  // OS 硬编码
    }
    // ...
}
```
**重构后**：
```go
func (ep *EnvPlugin) initSelinux() error {
    if !ep.osProvider.SupportsSELinux() {
        return nil
    }
    if out, err := ep.exec.ExecuteCommandWithOutput("/bin/sh", "-c", "sudo setenforce 0"); err != nil {
        log.Warnf("setenforce 0 failed, err: %s, output: %s", err, out)
    }
    if err := catAndReplace(InitSelinuxConfPath, "", "SELINUX=disabled", SelinuxRegex); err != nil {
        return errors.Wrap(err, "Disable selinux failed")
    }
    return nil
}
```
### 5.5 重构 initDNS — 消除 OS 判断
**重构前**（[init.go:752](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L752)）：
```go
func (ep *EnvPlugin) initDNS() error {
    if !utils.Exists(InitDNSConfPath) {
        if _, err := os.OpenFile(InitDNSConfPath, os.O_CREATE, RwxRwRw); err != nil {
            return errors.Wrapf(err, "create resolv.conf failed")
        }
    }
    if ep.machine.platform == "centos" {  // OS 硬编码
        log.Infof("Turn off the function that the Network Manager automatically overwrites the resolv.conf file in centos")
        return ep.initNetworkManager()
    }
    return nil
}
```
**重构后**：
```go
func (ep *EnvPlugin) initDNS() error {
    if !utils.Exists(InitDNSConfPath) {
        if _, err := os.OpenFile(InitDNSConfPath, os.O_CREATE, RwxRwRw); err != nil {
            return errors.Wrapf(err, "create resolv.conf failed")
        }
    }
    if ep.osProvider.NeedsNetworkManagerFix() {
        log.Infof("Configuring NetworkManager DNS settings")
        return ep.osProvider.ConfigureDNS(ctx, ep.exec)
    }
    return nil
}
```
### 5.6 重构 installLxcfs — 消除 OS 判断
**重构前**（[init.go:1061](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L1061)）：
```go
func (ep *EnvPlugin) installLxcfs() error {
    // ...
    switch ep.machine.platform {  // OS 硬编码
    case "centos":
        if strings.HasPrefix(ep.machine.version, "7") {
            httprepo.RepoInstall("fuse-libs", "lxcfs")
        }
        if strings.HasPrefix(ep.machine.version, "8") {
            httprepo.RepoInstall("lxcfs")
        }
    case "kylin":
        httprepo.RepoInstall("lxcfs", "lxcfs-tools")
    case "ubuntu":
        httprepo.RepoInstall("lxcfs")
    default:
        log.Warnf("not support platform: %s", ep.machine.platform)
    }
    // ...
}
```
**重构后**：
```go
func (ep *EnvPlugin) installLxcfs() error {
    if !utils.Exists("/var/lib/lxc/lxcfs") {
        if err := os.MkdirAll("/var/lib/lxc/lxcfs", RwxRxRx); err != nil {
            log.Errorf("failed create lxcfs dir, err: %v", err)
            return nil
        }
    }

    packages := ep.osProvider.LxcfsPackages()
    if len(packages) > 0 {
        if err := ep.osProvider.InstallPackages(ctx, ep.exec, packages...); err != nil {
            log.Warnf("failed install lxcfs packages %v, err: %v", packages, err)
        }
    }

    return ep.osProvider.PostLxcfsInstall(ctx, ep.exec)
}
```
### 5.7 重构 installNfsUtilIfNeeded — 消除 OS 判断
**重构前**（[init.go:1043](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go#L1043)）：
```go
func (ep *EnvPlugin) installNfsUtilIfNeeded() {
    // ...
    nfsUtil := "nfs-utils"
    if ep.machine.platform == "ubuntu" {  // OS 硬编码
        nfsUtil = "nfs-common"
    }
    if err := httprepo.RepoInstall(nfsUtil); err != nil {
        log.Warnf("failed install %s, err: %v", nfsUtil, err)
    }
}
```
**重构后**：
```go
func (ep *EnvPlugin) installNfsUtilIfNeeded() {
    if ep.bkeConfig == nil || ep.bkeConfig.CustomExtra == nil {
        return
    }
    v, ok := ep.bkeConfig.CustomExtra["pipelineServer"]
    if !ok || v != ep.currenNode.IP {
        return
    }

    nfsPkg := ep.osProvider.PackageName("nfs")
    if err := ep.osProvider.InstallPackages(ctx, ep.exec, nfsPkg); err != nil {
        log.Warnf("failed install %s for pipeline server node, err: %v", nfsPkg, err)
    }
}
```
### 5.8 重构 check 逻辑
**重构前**（[check.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/check.go)）：
```go
func (ep *EnvPlugin) checkUbuntuSysModules() []error {
    if ep.machine.platform != "ubuntu" { return nil }
    // ...
}

func (ep *EnvPlugin) checkCentosKylinSysModules() []error {
    if ep.machine.platform != "centos" && ep.machine.platform != "kylin" { return nil }
    // ...
}

func (ep *EnvPlugin) checkKylinRcLocal() []error {
    if ep.machine.platform != "kylin" { return nil }
    // ...
}
```
**重构后**：
```go
func (ep *EnvPlugin) checkKernelParam() error {
    var checkErrs []error
    checkErrs = append(checkErrs, ep.checkFileLimitConfig()...)

    if ep.bkeConfig != nil {
        cfg := &osprovider.KernelConfig{
            CRI: ep.bkeConfig.Cluster.ContainerRuntime.CRI,
        }
        extraParams := ep.osProvider.ExtraKernelParams(ctx, ep.osInfo, cfg)
        for k, v := range extraParams {
            execKernelParam[k] = v
        }
    }

    for k, v := range execKernelParam {
        pathEum := append([]string{procSysPath}, strings.Split(k, ".")...)
        path := filepath.Join(pathEum...)
        if _, err := catAndSearch(path, v, ""); err != nil {
            checkErrs = append(checkErrs, errors.Wrapf(err, "kernel param %s=%s failed", k, v))
        }
    }

    checkErrs = append(checkErrs, ep.osProvider.CheckModules(ctx, ep.exec, sysModule)...)
    return kerrors.NewAggregate(checkErrs)
}

func (ep *EnvPlugin) checkSelinux() error {
    return ep.osProvider.CheckSELinuxDisabled(ctx, ep.exec)
}
```
## 6. Source 管理重构
### 6.1 重构 source.go
**重构前**（[source.go](file:///d:/code/github/cluster-api-provider-bke/common/source/source.go)）：
```go
func GetRPMDownloadPath(url string) (string, error) {
    h, err := host.Info()
    // switch strings.ToLower(h.Platform) { ... }  // OS 硬编码
}

func SetSource(url string) error {
    // if strings.Contains(baseurl, "Ubuntu") { ... }  // OS 硬编码
}
```
**重构后**：
```go
// common/source/source.go (重构后)

package source

import (
    "context"
    "os"
    "strings"

    "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/common/warehouse"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider"
)

func GetDownloadPath(url string) (string, error) {
    osInfo, err := osprovider.DetectOSInfo(context.Background())
    if err != nil {
        return "", err
    }

    provider, err := osprovider.Get(osInfo.Name)
    if err != nil {
        return "", err
    }

    return provider.DownloadPath(url, osInfo), nil
}

func SetSource(url string, provider osprovider.OSProvider, osInfo *osprovider.OSInfo) error {
    baseURL := provider.DownloadPath(url, osInfo)

    if err := provider.BackupSource(context.Background(), nil); err != nil {
        return err
    }
    if err := provider.WriteSource(context.Background(), nil, baseURL); err != nil {
        return err
    }
    return nil
}

func ResetSource(provider osprovider.OSProvider) error {
    return provider.ResetSource(context.Background(), nil)
}
```
### 6.2 重构 httprepo/helper.go
**重构前**（[helper.go](file:///d:/code/github/cluster-api-provider-bke/utils/bkeagent/httprepo/helper.go)）：
```go
func init() {
    h, _, _, err := host.PlatformInformation()
    switch h {
    case "ubuntu", "debian":
        packageManager = "apt"
    case "centos", "kylin", "redhat", "fedora", "openeuler", "hopeos":
        packageManager = "yum"
    }
}
```
**重构后**：
```go
// utils/bkeagent/httprepo/helper.go (重构后)

package httprepo

import (
    "context"
    "fmt"
    "strings"

    "github.com/pkg/errors"
    
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

var (
    osProvider  osprovider.OSProvider
    executor    = &exec.CommandExecutor{}
)

func init() {
    osInfo, err := osprovider.DetectOSInfo(context.Background())
    if err != nil {
        log.Errorf("detect OS info failed, err: %v", err)
        return
    }
    p, err := osprovider.Get(osInfo.Name)
    if err != nil {
        log.Errorf("get OS provider for %s failed, err: %v", osInfo.Name, err)
        return
    }
    osProvider = p
}

func RepoUpdate() error {
    return osProvider.UpdateRepo(context.Background(), executor)
}

func RepoSearch(pkg string) error {
    return osProvider.SearchPackage(context.Background(), executor, pkg)
}

func RepoInstall(packages ...string) error {
    return osProvider.InstallPackages(context.Background(), executor, packages...)
}

func RepoRemove(packages ...string) error {
    return osProvider.RemovePackages(context.Background(), executor, packages...)
}
```
## 7. 文件目录结构
```
pkg/osprovider/
├── provider.go          # 核心接口定义
├── registry.go          # Provider 注册表
├── detect.go            # OS 信息检测
├── base/
│   └── base.go          # 公共逻辑实现
├── centos/
│   ├── centos.go        # CentOS Provider
│   └── centos_test.go   # CentOS 单元测试
├── ubuntu/
│   ├── ubuntu.go        # Ubuntu Provider
│   └── ubuntu_test.go   # Ubuntu 单元测试
├── kylin/
│   ├── kylin.go         # Kylin Provider
│   └── kylin_test.go    # Kylin 单元测试
└── openeuler/
    ├── openeuler.go     # OpenEuler Provider
    └── openeuler_test.go # OpenEuler 单元测试
```
## 8. 迁移策略
### 8.1 迁移步骤
| 步骤 | 内容 | 影响范围 | 风险 |
|------|------|----------|------|
| **Step 1** | 创建 `pkg/osprovider/` 包，定义接口和 Registry | 新增文件 | 低 |
| **Step 2** | 实现 BaseProvider 公共逻辑 | 新增文件 | 低 |
| **Step 3** | 实现 CentOS/Ubuntu/Kylin/OpenEuler Provider | 新增文件 | 低 |
| **Step 4** | 重构 `httprepo/helper.go`，使用 OSProvider | 1 文件 | 中 |
| **Step 5** | 重构 `common/source/source.go`，使用 OSProvider | 1 文件 | 中 |
| **Step 6** | 重构 `env/init.go`，替换 OS 判断为 Provider 调用 | 1 文件 | 高 |
| **Step 7** | 重构 `env/check.go`，替换 OS 判断为 Provider 调用 | 1 文件 | 高 |
| **Step 8** | 删除 `env/centos.go`，逻辑已迁移到 CentOS Provider | 1 文件 | 中 |
| **Step 9** | 更新 `utils/const.go`，`GetSupportPlatforms()` 改用 Registry | 1 文件 | 低 |
| **Step 10** | 补充单元测试和集成测试 | 测试文件 | 低 |
### 8.2 兼容性保障
1. **渐进式迁移**：每个 Step 完成后独立测试，确保功能不变
2. **Feature Gate**：通过环境变量 `BKE_USE_OSPROVIDER=true` 控制新旧路径
3. **双写验证**：迁移期间同时执行新旧逻辑，对比结果
4. **回滚机制**：每个 Step 可独立回滚到旧实现
### 8.3 新增 OS 支持的流程（重构后）
重构后，添加新 OS（如 Rocky Linux）只需：
1. 创建 `pkg/osprovider/rockylinux/rockylinux.go`
2. 实现 `OSProvider` 接口
3. 在 `init()` 中调用 `osprovider.MustRegister(New())`
4. 在 `main.go` 中添加空白导入 `_ "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/osprovider/rockylinux"`

**无需修改任何现有代码**，完全符合开闭原则。
## 9. 测试策略
### 9.1 Provider 单元测试
```go
// pkg/osprovider/centos/centos_test.go

func TestCentOSProvider_ExtraKernelParams(t *testing.T) {
    p := New()
    
    tests := []struct {
        name     string
        version  string
        cri      string
        expected map[string]string
    }{
        {
            name:    "CentOS 7 with containerd",
            version: "7.9",
            cri:     "containerd",
            expected: map[string]string{"fs.may_detach_mounts": "1"},
        },
        {
            name:     "CentOS 7 with docker",
            version:  "7.9",
            cri:      "docker",
            expected: map[string]string{},
        },
        {
            name:     "CentOS 8 with containerd",
            version:  "8.5",
            cri:      "containerd",
            expected: map[string]string{},
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            info := &osprovider.OSInfo{Version: tt.version}
            cfg := &osprovider.KernelConfig{CRI: tt.cri}
            result := p.ExtraKernelParams(context.Background(), info, cfg)
            assert.Equal(t, tt.expected, result)
        })
    }
}
```
### 9.2 Mock Executor 测试
```go
// pkg/osprovider/ubuntu/ubuntu_test.go

type mockExecutor struct {
    commands []string
    outputs  map[string]string
    errors   map[string]error
}

func (m *mockExecutor) ExecuteCommand(cmd string, args ...string) error {
    m.commands = append(m.commands, fmt.Sprintf("%s %s", cmd, strings.Join(args, " ")))
    return nil
}

func (m *mockExecutor) ExecuteCommandWithOutput(cmd string, args ...string) (string, error) {
    key := fmt.Sprintf("%s %s", cmd, strings.Join(args, " "))
    if err, ok := m.errors[key]; ok {
        return "", err
    }
    return m.outputs[key], nil
}

func (m *mockExecutor) ExecuteCommandWithCombinedOutput(cmd string, args ...string) (string, error) {
    return m.ExecuteCommandWithOutput(cmd, args...)
}

func TestUbuntuProvider_SetupModules(t *testing.T) {
    p := New()
    exec := &mockExecutor{}
    
    err := p.SetupModules(context.Background(), exec, []string{"ip_vs", "br_netfilter"})
    assert.NoError(t, err)
    assert.Contains(t, exec.commands, "grep -w ip_vs /etc/modules || echo ip_vs >> /etc/modules")
}
```
### 9.3 Registry 集成测试
```go
// pkg/osprovider/registry_test.go

func TestRegistry_Detect(t *testing.T) {
    centosP := centos.New()
    ubuntuP := ubuntu.New()
    
    reg := &Registry{providers: make(map[osprovider.OSName]osprovider.OSProvider)}
    reg.MustRegister(centosP)
    reg.MustRegister(ubuntuP)
    
    tests := []struct {
        name     string
        osInfo   *osprovider.OSInfo
        expected osprovider.OSName
    }{
        {name: "detect centos", osInfo: &osprovider.OSInfo{Name: osprovider.CentOS}, expected: osprovider.CentOS},
        {name: "detect ubuntu", osInfo: &osprovider.OSInfo{Name: osprovider.Ubuntu}, expected: osprovider.Ubuntu},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            p, err := reg.Detect(context.Background(), tt.osInfo)
            assert.NoError(t, err)
            assert.Equal(t, tt.expected, p.Name())
        })
    }
}
```
## 10. 重构前后对比
| 维度 | 重构前 | 重构后 |
|------|--------|--------|
| **新增 OS** | 修改 5+ 文件，散布 switch/if | 创建 1 个 Provider 文件 |
| **OS 判断** | `platform == "centos"` 硬编码 | `osProvider.SupportsSELinux()` 能力声明 |
| **包名映射** | 业务代码中 if/else | `osProvider.PackageName("nfs")` |
| **模块管理** | 各 OS 独立函数 | `osProvider.SetupModules()` 统一接口 |
| **源管理** | `strings.Contains(baseurl, "Ubuntu")` | `osProvider.SourceTemplate()` |
| **测试** | 需要 mock 整个 OS 环境 | Mock CommandExecutor 即可 |
| **扩展性** | 违反开闭原则 | 完全符合开闭原则 |
## 11. 与其他阶段的集成
### 11.1 与阶段二（状态机）的集成
OSProvider 在状态机中的使用位置：
```
Provisioning 状态
├── InitEnvState
│   ├── 使用 OSProvider.SetupModules()
│   ├── 使用 OSProvider.DisableFirewall()
│   ├── 使用 OSProvider.DisableSELinux()
│   └── 使用 OSProvider.ConfigureDNS()
├── InitRuntimeState
│   └── 使用 OSProvider.InstallPackages()
└── InitKubeletState
    └── 使用 OSProvider.PackageName()
```
### 11.2 与阶段三（Bootstrap Provider）的集成
Bootstrap Provider 在生成节点初始化配置时，需要 OS 信息：
```go
type BootstrapConfig struct {
    OSProvider   osprovider.OSProvider
    OSInfo       *osprovider.OSInfo
    Packages     []string
    KernelParams map[string]string
    Modules      []string
}
```
### 11.3 与阶段四（CVO 升级）的集成
升级过程中 OS Provider 用于：
```go
func (e *UpgradeExecutor) UpgradeNode(ctx context.Context) error {
    provider := e.osProvider
    
    // 1. 更新包仓库
    if err := provider.UpdateRepo(ctx, e.exec); err != nil {
        return err
    }
    
    // 2. 安装新版本包
    packages := provider.LxcfsPackages()
    if err := provider.InstallPackages(ctx, e.exec, packages...); err != nil {
        return err
    }
    
    return nil
}
```
这就是阶段五 OS Provider 抽象的完整详细设计。核心思路是：
1. **定义 OSProvider 接口**：通过组合小接口（`FirewallOps`、`SELinuxOps`、`ModuleOps` 等）实现能力抽象
2. **BaseProvider 复用公共逻辑**：各 OS 共享的 yum/apt 通用操作放在 BaseProvider
3. **注册机制**：通过 `init()` + `MustRegister()` 实现自动注册，新增 OS 零侵入
4. **渐进式迁移**：10 个 Step 逐步替换硬编码，每步可独立验证和回滚
5. **能力声明式**：用 `SupportsSELinux()`、`NeedsNetworkManagerFix()` 替代 `platform == "xxx"` 判断
        
