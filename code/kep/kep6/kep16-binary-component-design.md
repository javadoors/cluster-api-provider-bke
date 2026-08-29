# KEP-16: 二进制组件声明式安装与升级方案

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-16 |
| **标题** | 二进制组件声明式安装与升级：BinaryInstaller 完整设计方案 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-26 |
| **依赖** | KEP-5 声明式升级框架、KEP-13 二进制组件改造 |
| **来源** | 从 `声明式集群版本升级方案-支持二进制与Helm组件.md` 抽离二进制组件相关内容 |

---

## 目录

1. [设计目标与约束](#1-设计目标与约束)
2. [BinarySpec 类型定义](#2-binaryspec-类型定义)
3. [BinaryInstaller 详细设计](#3-binaryinstaller-详细设计)
4. [ConfigRenderer 详细设计](#4-configrenderer-详细设计)
5. [ConfigTemplateSpec forEach 动态多文件生成](#5-configtemplatespec-foreach-动态多文件生成)
6. [模板变量系统](#6-模板变量系统)
7. [BinaryComponentExecutor](#7-binarycomponentexecutor)
8. [组件迁移设计](#8-组件迁移设计)
9. [工作量评估](#9-工作量评估)
10. [风险与缓解](#10-风险与缓解)

---

## 1. 设计目标与约束

### 1.1 设计目标

本设计文档提供二进制组件的完整实现方案，包括：

- **BinaryInstaller**: 二进制组件的下载、渲染、安装
- **TemplateRenderer**: BinaryInstaller 内置的脚本与配置模板渲染引擎 (Go template)
- **ConfigRenderer**: BinaryInstaller 内置的配置文件模板渲染引擎 (支持 Content/Secret/Kubeconfig 三种模式)
- **DAG 集成**: BinaryComponentExecutor 执行器注册与调度流程

### 1.2 设计范围

| 范围 | 说明 |
| ------ | ------ |
| CRD 扩展 | ComponentVersion 新增 `binary` 类型的完整字段定义 |
| 核心安装器 | BinaryInstaller 的完整实现 |
| 渲染引擎 | BinaryInstaller 内置 TemplateRenderer (脚本渲染) + ConfigRenderer (配置渲染) 的完整实现 |
| DAG 集成 | BinaryComponentExecutor |
| 迁移策略 | Feature Gate、向后兼容、灰度发布 |

### 1.3 设计约束

| 约束 | 说明 |
| ------ | ------ |
| 向后兼容 | 必须支持从现有硬编码 Phase 平滑迁移 |
| 离线环境 | 二进制制品支持本地缓存 |
| 架构支持 | 必须支持 amd64 和 arm64 架构 |
| 操作系统支持 | 必须支持 CentOS 7/8、Ubuntu 20.04/22.04 |
| 接口复用 | 复用现有 `NeedExecute()` 接口 |
| 安全性 | 制品必须支持 checksum 校验 |

### 1.4 术语表

| 术语 | 定义 |
| ------ | ------ |
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装的安装器 |
| **configTemplates** | 配置文件模板系统，支持 Go template/Secret/kubeconfig |
| **installScript** | 安装脚本模板，支持 8 类 50+ 变量和条件渲染 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径等信息 |
| **ComponentVersion** | 组件版本 CRD，定义组件的类型、配置、依赖等 |

---

## 2. BinarySpec 类型定义

```go
// api/v1alpha1/componentversion_types.go

// BinarySpec 定义二进制组件规格 🆕新增
type BinarySpec struct {
    // 自定义变量 (可覆盖默认值)
    Variables map[string]string `json:"variables,omitempty"`
    
    // 二进制制品列表
    Artifacts []ArtifactSpec `json:"artifacts"`
    
    // 配置文件模板列表
    ConfigTemplates []ConfigTemplateSpec `json:"configTemplates,omitempty"`
    
    // 安装脚本 (支持 Go template 语法)
    InstallScript string `json:"installScript"`
    
    // 卸载脚本 (支持 Go template 语法)
    UninstallScript string `json:"uninstallScript,omitempty"`
    
    // 支持的架构列表
    SupportedArchitectures []string `json:"supportedArchitectures"`
    
    // 支持的操作系统列表
    SupportedOS []OSSpec `json:"supportedOS"`
    
    // 默认配置路径 (组件级共享)
    DefaultConfigPath string `json:"defaultConfigPath,omitempty"`
    
    // 默认日志路径 (组件级共享)
    DefaultLogPath string `json:"defaultLogPath,omitempty"`
    
    // 默认数据路径 (组件级共享)
    DefaultDataPath string `json:"defaultDataPath,omitempty"`
    
    // 健康检查配置 (安装/升级后通过 SSH 执行脚本验证服务可用性)
    HealthCheck *BinaryHealthCheckSpec `json:"healthCheck,omitempty"`
}

// BinaryHealthCheckSpec 定义二进制组件健康检查规格
// 与 Helm 的 HealthCheckSpec 不同, Binary 组件运行在远程节点上,
// 健康检查通过 SSH 执行脚本完成, 退出码 0=健康, 非零=不健康
type BinaryHealthCheckSpec struct {
    // 是否启用健康检查
    Enabled bool `json:"enabled"`
    
    // 等待超时时间 (默认 2m)
    Timeout string `json:"timeout,omitempty"`
    
    // 重试间隔 (默认 5s)
    Interval string `json:"interval,omitempty"`
    
    // 健康检查脚本 (Go template, 通过 SSH 在远程节点执行)
    // 支持 installScript 的所有模板变量
    // 退出码 0 = 健康, 非零 = 不健康
    Script string `json:"script"`
}

// ArtifactSpec 定义二进制制品规格
type ArtifactSpec struct {
    // 制品名称
    Name string `json:"name"`
    
    // 制品 URL (支持模板变量)
    URL string `json:"url"`
    
    // 制品校验和 (格式: sha256:xxx)
    Checksum string `json:"checksum"`
    
    // 安装路径 (per-artifact, 不同 artifact 可安装到不同路径)
    // 对于归档文件: 解压到此路径 (如 "/" 表示解压到根目录)
    // 对于单文件: 复制到此路径
    InstallPath string `json:"installPath"`
}

// ConfigTemplateSpec 定义配置文件模板规格
type ConfigTemplateSpec struct {
    // 模板名称
    Name string `json:"name"`
    
    // 静态目标路径 (与 PathTemplate 互斥)
    Path string `json:"path,omitempty"`
    
    // 动态路径模板 (Go template, 与 ForEach 配合使用)
    PathTemplate string `json:"pathTemplate,omitempty"`
    
    // 迭代源路径 (点分隔, 从 TemplateContext 中解析)
    ForEach string `json:"forEach,omitempty"`
    
    // 文件权限 (如 "0644")
    Mode string `json:"mode,omitempty"`
    
    // 文件所有者 (如 "root:root")
    Owner string `json:"owner,omitempty"`
    
    // 模板内容 (Go template 语法)
    Content string `json:"content,omitempty"`
    
    // Secret 引用
    SecretRef *SecretRefSpec `json:"secretRef,omitempty"`
    
    // Kubeconfig 模板
    KubeconfigTemplate *KubeconfigTemplateSpec `json:"kubeconfigTemplate,omitempty"`
    
    // 生成条件 (Go Template 表达式)
    // 空 = 始终生成；渲染结果为 "false" 或空时跳过
    Condition string `json:"condition,omitempty"`
}

// SecretRefSpec 定义 Secret 引用规格
type SecretRefSpec struct {
    Name string `json:"name"`
    Namespace string `json:"namespace"`
    Key string `json:"key"`
}

// KubeconfigTemplateSpec 定义 Kubeconfig 模板规格
type KubeconfigTemplateSpec struct {
    ClusterName string `json:"clusterName"`
    APIServer string `json:"apiServer"`
    CACertPath string `json:"caCertPath"`
    ClientCertPath string `json:"clientCertPath"`
    ClientKeyPath string `json:"clientKeyPath"`
    Namespace string `json:"namespace,omitempty"`
    ServiceAccount string `json:"serviceAccount,omitempty"`
}

// OSSpec 定义操作系统规格
type OSSpec struct {
    Name string `json:"name"`
    Versions []string `json:"versions"`
}
```

**字段约束**：

| 条件 | 说明 |
| ------ | ------ |
| `ForEach != ""` | `PathTemplate` 必填，`Path` 忽略，按迭代源展开为多个文件 |
| `ForEach == ""` | `Path` 必填，`PathTemplate` 忽略（原有行为，单文件） |

---

## 3. BinaryInstaller 详细设计

### 3.1 核心组件架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            BinaryInstaller                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                    ArtifactDownloader                           │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │
│  │  │  HTTP Client │  │ Cache Manager│  │ Checksum Verifier   │    │    │
│  │  │  (下载制品)   │  │ (本地缓存)   │  │ (校验和验证)         │    │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                     TemplateRenderer                            │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │
│  │  │  Go Template │  │  FuncMap      │  │ Variable Resolver   │    │    │
│  │  │  (模板解析)   │  │ (自定义函数)  │  │ (变量解析)          │    │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                      ConfigRenderer                             │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │
│  │  │ Content Mode │  │ Secret Mode  │  │ Kubeconfig Mode     │    │    │
│  │  │ (模板渲染)    │  │ (Secret获取) │  │ (动态生成)          │    │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                       SSH Executor                              │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │
│  │  │ File Upload  │  │ Script Exec  │  │ Result Collector    │    │    │
│  │  │ (文件上传)    │  │ (脚本执行)   │  │ (结果收集)          │    │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                     HealthChecker (SSH)                         │    │
│  └─────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────┘
```

### 3.2 BinaryInstaller 执行流程

**设计思路**：BinaryInstaller 的执行流程分为 7 个主要步骤：解析架构、下载制品、校验 Checksum、渲染脚本、渲染配置、SSH 执行、收集结果。

**关键设计点**：

- **缓存机制**：制品下载后保存到本地缓存，避免重复下载
- **Checksum 校验**：下载后立即校验，确保制品完整性
- **模板渲染**：支持 installScript 和 configTemplates 两种模板
- **SSH 执行**：复用现有 bkessh.MultiCli，上传制品和配置后执行脚本

### 3.3 核心接口定义

```go
// pkg/binaryinstaller/installer.go

// SSHExecutor SSH 执行抽象接口
type SSHExecutor interface {
    Execute(ctx context.Context, nodeIP, script string) (*SSHResult, error)
    Upload(ctx context.Context, nodeIP string, data []byte, remotePath string) error
    DiscoverArch(ctx context.Context, nodeIP string) (string, error)
}

// BinaryInstaller 二进制组件安装器
type BinaryInstaller struct {
    client         client.Client
    sshExecutor    SSHExecutor
    cacheDir       string
    httpClient     *http.Client
    cache          *ArtifactCache
    renderer       *TemplateRenderer
    configRenderer *ConfigRenderer
    logger         *bkev1beta1.BKELogger
}

// InstallOptions 安装选项
type InstallOptions struct {
    Component   *ComponentVersion
    TemplateCtx manifest.TemplateContext
    Action      BinaryAction
    Timeout     time.Duration
    RetryCount  int
}

// BinaryAction 二进制操作类型
type BinaryAction string

const (
    BinaryActionInstall   BinaryAction = "Install"
    BinaryActionUpgrade   BinaryAction = "Upgrade"
    BinaryActionUninstall BinaryAction = "Uninstall"
)

// Install 执行二进制组件安装/升级
func (i *BinaryInstaller) Install(ctx context.Context, opts InstallOptions) error {
    component := opts.Component
    binary := component.Spec.Binary
    tmplCtx := opts.TemplateCtx
    
    // 1. 通过 SSH 发现节点架构
    arch, err := i.sshExecutor.DiscoverArch(ctx, tmplCtx.NodeIP)
    if err != nil {
        return fmt.Errorf("failed to discover arch: %w", err)
    }
    tmplCtx.NodeArch = arch
    
    // 2. 下载二进制制品 (带缓存)
    artifacts, err := i.downloadArtifacts(ctx, binary, arch)
    if err != nil {
        return fmt.Errorf("failed to download artifacts: %w", err)
    }
    
    // 3. 填充 TemplateContext 的制品信息
    tmplCtx.Artifacts = make(map[string]*ArtifactInfo)
    for name, art := range artifacts {
        tmplCtx.Artifacts[name] = &ArtifactInfo{
            Name:        art.Name,
            Path:        art.Path,
            URL:         art.URL,
            Checksum:    art.Checksum,
            Filename:    art.Filename,
            InstallPath: art.InstallPath,
        }
    }
    
    // 4. 填充自定义变量
    tmplCtx.Variables = binary.Variables
    
    // 5. 填充组件级路径变量
    tmplCtx.ConfigPath = binary.DefaultConfigPath
    tmplCtx.LogPath = binary.DefaultLogPath
    tmplCtx.DataPath = binary.DefaultDataPath
    
    // 6. 填充操作类型
    tmplCtx.Action = string(opts.Action)
    tmplCtx.IsUpgrade = opts.Action == BinaryActionUpgrade
    
    // 7. 渲染安装脚本
    script, err := i.renderer.RenderScript(binary.InstallScript, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render install script: %w", err)
    }
    
    // 8. 渲染配置文件模板
    configs, err := i.renderConfigTemplates(binary.ConfigTemplates, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render config templates: %w", err)
    }
    
    // 9. SSH 执行安装
    switch opts.Action {
    case BinaryActionInstall, BinaryActionUpgrade:
        if err := i.executeInstall(ctx, tmplCtx.NodeIP, script, artifacts, configs); err != nil {
            return err
        }
        // 10. 健康检查
        if binary.HealthCheck != nil && binary.HealthCheck.Enabled {
            if err := i.executeHealthCheck(ctx, tmplCtx.NodeIP, binary.HealthCheck, tmplCtx); err != nil {
                return fmt.Errorf("health check failed: %w", err)
            }
        }
        return nil
    case BinaryActionUninstall:
        return i.executeUninstall(ctx, tmplCtx.NodeIP, binary.UninstallScript, tmplCtx)
    }
    
    return nil
}
```

**设计思路 — Install 与 Upgrade 共用 InstallScript**：

`BinaryAction` 有三个值（Install/Upgrade/Uninstall），但 `BinarySpec` 只有 `InstallScript` 和 `UninstallScript` 两个脚本，没有 `UpgradeScript`。这是有意的：

1. **Install 和 Upgrade 本质是同一操作**——"让目标版本成为运行版本"，区别仅在于是否有旧版本存在。
2. **通过 `{{if .isUpgrade}}` 条件渲染区分差异**，以 containerd 为例：备份步骤仅在升级时执行，其余步骤完全相同。
3. **不设 UpgradeScript 的原因**：避免用户编写维护两份高度重复的脚本。

**`isUpgrade` 的来源链路**：

```txt
VersionContext.HasCurrent("containerd")
  → true: BinaryActionUpgrade
  → false: BinaryActionInstall
    → InstallOptions.Action 传递给 BinaryInstaller.Install()
      → tmplCtx.IsUpgrade = (Action == Upgrade)
        → 模板渲染: {{if .isUpgrade}}...{{end}}
```

### 3.4 Binary Uninstall 流程

**设计思路** — Uninstall 与 Install/Upgrade 的区别：

- **Install/Upgrade**：下载制品 → 渲染脚本 → SSH 执行 → 健康检查
- **Uninstall**：渲染卸载脚本 → SSH 执行 → 验证服务已停止

卸载不需要下载制品，因为目标节点上已有二进制文件。

```go
// executeUninstall 执行二进制组件卸载
func (i *BinaryInstaller) executeUninstall(
    ctx context.Context,
    nodeIP string,
    uninstallScript string,
    tmplCtx manifest.TemplateContext,
) error {
    // 1. 渲染卸载脚本
    script, err := i.renderer.RenderScript(uninstallScript, tmplCtx)
    if err != nil {
        return fmt.Errorf("render uninstall script: %w", err)
    }

    // 2. 通过 SSH 执行卸载脚本
    result, err := i.sshExecutor.Execute(ctx, nodeIP, script)
    if err != nil {
        return fmt.Errorf("uninstall failed on %s: %w\nstdout: %s\nstderr: %s",
            nodeIP, err, result.Stdout, result.Stderr)
    }

    // 3. 验证服务已停止
    verifyCmd := fmt.Sprintf("systemctl is-active %s || true", tmplCtx.ServiceName)
    verifyResult, _ := i.sshExecutor.Execute(ctx, nodeIP, verifyCmd)
    if verifyResult.Stdout == "active" {
        return fmt.Errorf("service %s still active after uninstall on %s", 
            tmplCtx.ServiceName, nodeIP)
    }

    return nil
}
```

---

## 4. ConfigRenderer 详细设计

### 4.1 三种渲染模式

| 模式 | 说明 | 场景 |
|------|------|------|
| **Content** | Go template 渲染 | 简单的配置文件，如 containerd 的 config.toml |
| **Secret** | 从 K8s Secret 获取内容 | 证书、密钥等敏感数据 |
| **Kubeconfig** | 动态生成 kubeconfig | bkeagent 的 kubeconfig |

### 4.2 ConfigRenderer 渲染流程

```
1. 遍历 ConfigTemplates 列表
2. 对每个 ConfigTemplateSpec:
   ├─ 评估 Condition (可选)
   │   → false 或空: 跳过此模板
   │   → true 或非空: 继续渲染
   ├─ 检查 ForEach
   │   ├─ 空: 单文件渲染
   │   │   ├─ Content Mode: 渲染 Go template
   │   │   ├─ Secret Mode: 从 K8s API 获取 Secret
   │   │   └─ Kubeconfig Mode: 动态生成
   │   └─ 非空: 多文件渲染 (详见第 5 章)
   └─ 返回渲染后的配置文件列表
```

### 4.3 核心接口定义

```go
// pkg/binaryinstaller/config_renderer.go

// ConfigRenderer 配置文件渲染器
type ConfigRenderer struct {
    client   client.Client
    renderer *TemplateRenderer
}

// RenderConfigTemplates 渲染所有配置文件模板
func (r *ConfigRenderer) RenderConfigTemplates(
    templates []ConfigTemplateSpec,
    tmplCtx manifest.TemplateContext,
) (map[string][]byte, error) {
    results := make(map[string][]byte)
    
    for _, tmpl := range templates {
        // 评估 Condition
        if tmpl.Condition != "" {
            result, err := r.renderer.RenderScript(tmpl.Condition, tmplCtx)
            if err != nil {
                return nil, fmt.Errorf("failed to render condition: %w", err)
            }
            if result == "false" || result == "" {
                continue
            }
        }
        
        // 检查 ForEach
        if tmpl.ForEach != "" {
            // 多文件渲染 (详见第 5 章)
            files, err := r.renderForEach(tmpl, tmplCtx)
            if err != nil {
                return nil, err
            }
            for path, content := range files {
                results[path] = content
            }
        } else {
            // 单文件渲染
            content, err := r.renderSingle(tmpl, tmplCtx)
            if err != nil {
                return nil, err
            }
            results[tmpl.Path] = content
        }
    }
    
    return results, nil
}

// renderSingle 渲染单个配置文件
func (r *ConfigRenderer) renderSingle(
    tmpl ConfigTemplateSpec,
    tmplCtx manifest.TemplateContext,
) ([]byte, error) {
    // 按模式分发
    if tmpl.Content != "" {
        // Content Mode: Go template 渲染
        rendered, err := r.renderer.RenderScript(tmpl.Content, tmplCtx)
        return []byte(rendered), err
    }
    
    if tmpl.SecretRef != nil {
        // Secret Mode: 从 K8s Secret 获取
        secret := &corev1.Secret{}
        err := r.client.Get(ctx, client.ObjectKey{
            Namespace: tmpl.SecretRef.Namespace,
            Name:      tmpl.SecretRef.Name,
        }, secret)
        if err != nil {
            return nil, fmt.Errorf("failed to get secret %s/%s: %w", 
                tmpl.SecretRef.Namespace, tmpl.SecretRef.Name, err)
        }
        return secret.Data[tmpl.SecretRef.Key], nil
    }
    
    if tmpl.KubeconfigTemplate != nil {
        // Kubeconfig Mode: 动态生成 kubeconfig
        return r.generateKubeconfig(tmpl.KubeconfigTemplate, tmplCtx)
    }
    
    return nil, fmt.Errorf("no content, secret, or kubeconfig template specified")
}
```

---

## 5. ConfigTemplateSpec forEach 动态多文件生成

### 5.1 ForEach 语义

`forEach` 用于从 TemplateContext 中解析迭代源，为每个元素生成一个配置文件。

| 迭代源类型 | 迭代变量 | 示例 |
|-----------|---------|------|
| `map[string]interface{}` | `.Key`, `.Value` | 镜像仓库 hosts.toml: 每个仓库一个文件 |
| `[]interface{}` | `.Index`, `.Value` | 多个 etcd 节点配置: 每个节点一个文件 |

### 5.2 ForEachContext 迭代上下文

```go
// ForEachContext 迭代上下文
type ForEachContext struct {
    Key   string      // map 的 key 或 array 的 index
    Value interface{} // map 或 array 的值
    Index int         // 当前索引 (0-based)
}
```

### 5.3 渲染引擎核心代码

```go
// renderForEach 渲染 ForEach 配置模板
func (r *ConfigRenderer) renderForEach(
    tmpl ConfigTemplateSpec,
    tmplCtx manifest.TemplateContext,
) (map[string][]byte, error) {
    results := make(map[string][]byte)
    
    // 1. 解析迭代源 (点分隔路径从 TemplateContext 中获取)
    source, err := resolveForEachSource(tmpl.ForEach, tmplCtx)
    if err != nil {
        return nil, fmt.Errorf("failed to resolve forEach source: %w", err)
    }
    
    // 2. 遍历迭代源
    for key, value := range source {
        // 创建迭代上下文
        forEachCtx := ForEachContext{
            Key:   key,
            Value: value
        }
        
        // 3. 渲染路径模板
        path, err := r.renderer.RenderScript(tmpl.PathTemplate, tmplCtx, forEachCtx)
        if err != nil {
            return nil, fmt.Errorf("failed to render path template: %w", err)
        }
        
        // 4. 渲染内容模板
        content, err := r.renderer.RenderScript(tmpl.Content, tmplCtx, forEachCtx)
        if err != nil {
            return nil, fmt.Errorf("failed to render content: %w", err)
        }
        
        results[path] = []byte(content)
    }
    
    return results, nil
}
```

### 5.4 典型用例：containerd hosts.toml

```yaml
configTemplates:
  - name: hosts.toml
    pathTemplate: "{{cd \"containerd\" \"registryConfigPath\"}}/{{.Key}}/hosts.toml"
    forEach: "Config.Cluster.ContainerRuntime.Registry"
    content: |
      server = "https://{{.Key}}"
      
      [host."{{.Value.Override}}"]
      capabilities = ["pull", "resolve"]
    condition: "{{.isOffline}}"
```

---

## 6. 模板变量系统

### 6.1 变量分类

| 类别 | 变量示例 | 说明 |
|---------|---------|------|
| **节点信息** | `{{nodeIP}}`, `{{nodeName}}`, `{{arch}}` | 节点 IP、主机名、CPU 架构 |
| **集群配置** | `{{clusterName}}`, `{{namespace}}`, `{{kubernetesVersion}}` | 集群名称、命名空间、K8s 版本 |
| **组件版本** | `{{version}}`, `{{componentVersion}}` | 组件版本号 |
| **制品信息** | `{{artifact.<name>.path}}`, `{{artifact.<name>.installPath}}` | 制品下载路径、安装路径 |
| **配置路径** | `{{configPath}}`, `{{logPath}}`, `{{dataPath}}` | 组件级默认路径 |
| **自定义变量** | `{{.Variables.<key>}}` | ComponentVersion 中定义的变量 |
| **操作类型** | `{{isUpgrade}}` | 是否为升级操作 |
| **条件渲染** | `{{if .isUpgrade}}...{{end}}` | 按操作类型条件渲染 |
| **部署模式** | `{{.isOffline}}` | 是否离线模式 |

### 6.2 isUpgrade 来源链路

```txt
VersionContext.HasCurrent("containerd")
  → true: BinaryActionUpgrade
  → false: BinaryActionInstall
    → InstallOptions.Action 传递给 BinaryInstaller.Install()
      → tmplCtx.IsUpgrade = (Action == Upgrade)
        → 模板渲染: {{if .isUpgrade}}...{{end}}
```

### 6.3 isOffline 来源链路

```txt
BKECluster.Spec (imageRepo + insecureRegistries)
  → BinaryComponentExecutor 检查 repo ∈ insecureRegistries
    → true: tmplCtx.Variables["isOffline"] = "true"
    → false: tmplCtx.Variables["isOffline"] = "false"
          → ConfigTemplateSpec.condition: "{{.isOffline}}"
            → 离线时生成 hosts.toml 重定向文件，在线时跳过
```

---

## 7. BinaryComponentExecutor

### 7.1 执行器架构

```go
// pkg/dagexec/executor/binary.go

// BinaryComponentExecutor 二进制组件执行器
type BinaryComponentExecutor struct {
    installer              *binaryinstaller.BinaryInstaller
    cvStore                ComponentVersionStore
    nodeFilter             NodeFilter
    statusUpdater          NodeStatusUpdater
    componentStatusUpdater ComponentStatusUpdater
}

func (e *BinaryComponentExecutor) GetComponentType() ComponentType {
    return ComponentTypeBinary
}
```

### 7.2 节点级执行策略

| 策略 | 并发度 | 适用场景 | FailurePolicy 交互 |
| ------ | -------- | --------- | ------------------- |
| Rolling | 1 | 高风险组件 (containerd) | 逐节点判定，单节点失败可 Rollback 后继续 |
| Parallel | N (全部) | 低风险组件 (配置文件更新) | 全节点同时操作，FailFast 时全部中断 |
| Batch | BatchSize | 中风险组件 (bkeagent) | 逐批判定，每批完成后可检查集群健康状态 |

### 7.3 ExecuteComponent 实现

```go
// ExecuteComponent 执行二进制组件
func (e *BinaryComponentExecutor) ExecuteComponent(
    ctx context.Context,
    node *ComponentNode,
    execCtx *ExecutionContext,
) error {
    component := node.Component
    
    // 1. 获取 ComponentVersion
    cv, err := e.cvStore.GetComponentVersion(ctx, component.Name, component.Version)
    if err != nil {
        return err
    }
    
    // 2. 确认是二进制类型
    if cv.Spec.Type != configv1alpha1.ComponentTypeBinary {
        return fmt.Errorf("component %s is not binary type", component.Name)
    }
    
    // 3. 组件级幂等判断
    vc := execCtx.VersionContext
    if vc != nil && !vc.NeedsUpgrade(component.Name) {
        return nil
    }
    
    // 4. 获取全部节点
    allNodes, err := execCtx.NodeProvider.GetNodes(ctx, execCtx.Cluster)
    if err != nil {
        return err
    }
    
    // 5. 节点级过滤 (NodeFilter)
    targetNodes, err := e.nodeFilter.Filter(ctx, allNodes, cv, execCtx)
    if err != nil {
        return err
    }
    if len(targetNodes) == 0 {
        return nil
    }
    
    // 6. 根据升级策略执行
    strategy := cv.Spec.UpgradeStrategy
    switch strategy.Mode {
    case "Rolling":
        return e.executeRolling(ctx, targetNodes, cv, strategy, execCtx)
    case "Parallel":
        return e.executeParallel(ctx, targetNodes, cv, strategy, execCtx)
    case "Batch":
        return e.executeBatch(ctx, targetNodes, cv, strategy, execCtx)
    }
    
    return nil
}
```

### 7.4 Executor 与 BinaryInstaller 的协作边界

| 层级 | 职责 |
|------|------|
| **Executor** | 获取节点列表 → 选择 Rolling/Parallel/Batch 策略 → 为每个节点构建 InstallOptions → 调用 BinaryInstaller.Install() → 处理 FailurePolicy |
| **BinaryInstaller** | SSH 发现 arch → 下载制品 → 填充 TemplateContext → 渲染脚本 → SSH 执行 → 健康检查 |

边界清晰：Executor 不关心"怎么在单节点上安装"，Installer 不关心"在哪些节点上安装、什么顺序"。

---

## 8. 组件迁移设计

### 8.1 containerd 重构

从现有 `EnsureContainerdUpgrade` Phase 迁移为 `binary` 类型 ComponentVersion。

**当前 Phase 逻辑**：通过 ENV 命令批量重置 + 重新部署 containerd。

**迁移后**：
- ComponentVersion YAML 定义 containerd 的制品、配置模板、安装脚本
- BinaryInstaller 通过 SSH 在每个节点上执行
- 支持 `forEach` 动态生成 hosts.toml 离线重定向文件

### 8.2 bkeagent 重构

从现有 `EnsureBKEAgent`/`EnsureAgentUpgrade` Phase 迁移为 `binary` 类型 ComponentVersion。

**当前 Phase 逻辑**：SSH 直接推送 bkeagent 二进制。

**迁移后**：
- ComponentVersion YAML 定义 bkeagent 的制品、配置模板（bkeagent.conf + kubeconfig）
- BinaryInstaller 通过 SSH 在每个节点上执行
- ConfigRenderer 的 Kubeconfig Mode 动态生成 kubeconfig

### 8.3 Feature Gate

```go
var (
    BinaryComponentEnabled = featuregate.NewFeature()
    ContainerdBinaryMigration = featuregate.NewFeature()
    BKEAgentBinaryMigration = featuregate.NewFeature()
)
```

### 8.4 迁移原则

1. **渐进式迁移**：通过 Feature Gate 控制，新旧路径可并存
2. **向后兼容**：迁移期间 ReleaseImage 可同时包含 inline 和 binary 类型组件
3. **版本对齐**：迁移在 openFuyao 版本升级时进行，不在运行中切换

---

## 9. 工作量评估

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **BinarySpec 类型定义** | 类型定义 + deepcopy + CRD schema | 3 |
| **BinaryInstaller** | 核心安装器（下载/渲染/SSH执行/健康检查） | 8 |
| **ConfigRenderer** | 三种渲染模式 + forEach 机制 | 5 |
| **模板变量系统** | 8 类 50+ 变量 + 自定义函数 | 3 |
| **BinaryComponentExecutor** | 执行器 + 三种策略 + NodeFilter | 5 |
| **组件迁移** | containerd/bkeagent ComponentVersion YAML | 4 |
| **集成测试** | 各组件安装/升级/卸载 E2E | 5 |
| **小计** | - | **33 人天** |

---

## 10. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **制品下载失败** | 安装阻塞 | 中 | 本地缓存 + 重试机制 |
| **模板渲染错误** | 配置文件错误 | 中 | dry-run 验证 + 单元测试 |
| **SSH 执行超时** | 安装阻塞 | 低 | 超时控制 + 重试 |
| **Inline 迁移兼容性** | 新旧路径冲突 | 中 | Feature Gate 控制 + 渐进迁移 |
| **离线环境制品缺失** | 安装失败 | 中 | 制品预推送到本地仓库 + checksum 校验 |

---

## 附录

### A. 参考文档

1. [声明式集群版本升级方案-支持二进制与Helm组件](声明式集群版本升级方案-支持二进制与Helm组件.md)
2. [KEP-5 声明式升级框架](kep5/kep5.md)
3. [KEP-13 二进制组件改造](kep13-binary-component-migration-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装的安装器 |
| **BinaryComponentExecutor** | DAG 执行器，将 binary 组件纳入 DAG 统一调度 |
| **installScript** | 安装脚本模板，支持 Go template 语法和 50+ 模板变量 |
| **configTemplates** | 配置文件模板系统，支持 Content/Secret/Kubeconfig 三种渲染模式 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径 |
| **NodeFilterSpec** | 节点过滤策略，控制 binary 组件在哪些节点上执行 |
| **isUpgrade** | 模板变量，区分安装和升级场景，用于条件渲染 |
