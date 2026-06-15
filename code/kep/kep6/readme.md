# Containerd 与 BKEAgent 二进制组件重构方案

## 一、现状分析

### 1.1 当前实现方式

| 组件 | 安装场景 | 升级场景 | 当前实现方式 |
|------|---------|---------|-------------|
| **Containerd** | 在 `EnsureNodesEnv` 中通过脚本安装 | `EnsureContainerdUpgrade` Phase (inline) | 硬编码在代码中，版本来自 `BKECluster.Spec.ContainerdVersion` |
| **BKEAgent** | `EnsureBKEAgent` Phase (SSH推送) | `EnsureAgentUpgrade` Phase (SSH推送) | 硬编码在代码中，版本来自 `BKECluster.Spec.OpenFuyaoVersion` |

### 1.2 当前问题

| 问题 | 影响 |
|------|------|
| **版本硬编码** | 版本信息散落在 `BKECluster.Spec` 各字段，无法通过 ReleaseImage 统一管理 |
| **安装/升级分离** | 安装和升级使用不同的 Phase，逻辑重复 |
| **无二进制组件支持** | `ComponentTypeBinary` 已定义但未实现 |
| **架构适配硬编码** | bkeagent 的架构适配 (`bkeagent_linux_{arch}`) 写在代码中 |
| **新增组件需改代码** | 新增一个二进制组件需要修改核心调度代码 |

## 二、目标架构

### 2.1 ReleaseImage 中的二进制组件定义

```yaml
# ReleaseImage OCI 定义示例
apiVersion: config.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: ri-v2.6.0
spec:
  version: "v2.6.0"
  digest: "sha256:abc123..."
  
  install:
    components:
      - name: containerd
        version: v1.7.18
      - name: bkeagent
        version: v2.6.0
      - name: kubernetes
        version: v1.29.0
      # ... 其他组件
        
  upgrade:
    components:
      - name: containerd
        version: v1.7.18
      - name: bkeagent
        version: v2.6.0
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0
      - name: etcd
        version: v3.5.12
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0
      # ... 其他组件
```

### 2.2 bke-manifests 中的二进制组件定义

```yaml
# bke-manifests/containerd/v1.7.18/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.18
spec:
  name: containerd
  type: binary
  version: v1.7.18
  
  # 二进制制品定义
  binary:
    artifacts:
      - name: containerd
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:abc123..."
        installPath: "/usr/local/bin"
        executable: containerd
        
      - name: containerd-shim-runc-v2
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{version}}/containerd-shim-runc-v2-{{version}}-linux-{{arch}}"
        checksum: "sha256:def456..."
        installPath: "/usr/local/bin"
        executable: containerd-shim-runc-v2
        
      - name: ctr
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{version}}/ctr-{{version}}-linux-{{arch}}"
        checksum: "sha256:ghi789..."
        installPath: "/usr/local/bin"
        executable: ctr
    
    # 安装脚本
    installScript: |
      #!/bin/bash
      set -e
      
      # 停止 containerd 服务
      systemctl stop containerd || true
      
      # 备份旧版本
      if [ -f /usr/local/bin/containerd ]; then
        cp /usr/local/bin/containerd /usr/local/bin/containerd.bak.$(date +%Y%m%d%H%M%S)
      fi
      
      # 解压并安装新二进制文件
      tar -xzf {{artifact.containerd.path}} -C {{binary.artifacts[0].installPath}}
      chmod +x {{binary.artifacts[0].installPath}}/containerd
      
      # 安装 systemd service 文件
      cat > /etc/systemd/system/containerd.service << 'EOF'
      [Unit]
      Description=containerd container runtime
      Documentation=https://containerd.io
      After=network.target
      
      [Service]
      ExecStartPre=/sbin/modprobe overlay
      ExecStart=/usr/local/bin/containerd
      Restart=always
      RestartSec=5
      Delegate=yes
      KillMode=process
      LimitNOFILE=1048576
      LimitNPROC=infinity
      LimitCORE=infinity
      
      [Install]
      WantedBy=multi-user.target
      EOF
      
      # 重新加载并启动服务
      systemctl daemon-reload
      systemctl enable containerd
      systemctl start containerd
      
      # 验证安装
      /usr/local/bin/containerd --version
    
    # 卸载脚本
    uninstallScript: |
      #!/bin/bash
      systemctl stop containerd || true
      systemctl disable containerd || true
      rm -f /usr/local/bin/containerd
      rm -f /usr/local/bin/ctr
      rm -f /etc/systemd/system/containerd.service
      systemctl daemon-reload
    
    # 架构支持
    supportedArchitectures:
      - amd64
      - arm64
    
    # 操作系统支持
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
      - name: kylin
        versions: ["V10"]
  
  # 兼容性约束
  compatibility:
    constraints:
      - component: kubernetes
        rule: ">=1.26.0"
      - component: runc
        rule: ">=1.1.0"
  
  # 依赖关系
  dependencies:
    - name: kubernetes
      phase: Install  # 安装时依赖 kubernetes 先安装
    
  # 升级策略
  upgradeStrategy:
    mode: Rolling      # Rolling / Parallel / Batch
    batchSize: 1       # 每批升级节点数
    timeout: "10m"
    failurePolicy: Continue  # FailFast / Continue / Rollback
```

```yaml
# bke-manifests/bkeagent/v2.6.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v2.6.0
spec:
  name: bkeagent
  type: binary
  version: v2.6.0
  
  binary:
    artifacts:
      - name: bkeagent
        url: "https://release-repo.openfuyao.cn/binaries/bkeagent/{{version}}/bkeagent-{{version}}-linux-{{arch}}"
        checksum: "sha256:xyz789..."
        installPath: "/usr/local/bin"
        executable: bkeagent
    
    # 配置文件模板
    configTemplates:
      - name: bkeagent.conf
        path: "/etc/openFuyao/bkeagent/bkeagent.conf"
        content: |
          cluster_name: {{clusterName}}
          api_server: {{apiServer}}
          kubeconfig_path: /etc/openFuyao/bkeagent/kubeconfig
          log_level: info
          log_path: /var/log/bkeagent/bkeagent.log
    
    # 安装脚本
    installScript: |
      #!/bin/bash
      set -e
      
      # 创建目录
      mkdir -p /etc/openFuyao/bkeagent
      mkdir -p /var/log/bkeagent
      
      # 停止旧服务
      systemctl stop bkeagent || true
      
      # 备份旧版本
      if [ -f /usr/local/bin/bkeagent ]; then
        cp /usr/local/bin/bkeagent /usr/local/bin/bkeagent.bak.$(date +%Y%m%d%H%M%S)
      fi
      
      # 安装新二进制文件
      install -m 0755 {{artifact.bkeagent.path}} /usr/local/bin/bkeagent
      
      # 安装 systemd service
      cat > /etc/systemd/system/bkeagent.service << 'EOF'
      [Unit]
      Description=BKE Agent
      After=network.target
      
      [Service]
      ExecStart=/usr/local/bin/bkeagent --config /etc/openFuyao/bkeagent/bkeagent.conf
      Restart=always
      RestartSec=5
      
      [Install]
      WantedBy=multi-user.target
      EOF
      
      systemctl daemon-reload
      systemctl enable bkeagent
      systemctl start bkeagent
      
      # 验证
      /usr/local/bin/bkeagent --version
    
    supportedArchitectures:
      - amd64
      - arm64
    
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
      - name: kylin
        versions: ["V10"]
  
  # 升级策略
  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: Continue
```

### 2.3 二进制组件安装器

```go
// pkg/binaryinstaller/installer.go

// BinaryInstaller 负责二进制组件的安装、升级和卸载
type BinaryInstaller struct {
    client     client.Client
    sshClient  *bkessh.MultiCli
    cacheDir   string
    httpClient *http.Client
}

// InstallOptions 安装选项
type InstallOptions struct {
    Component   *ComponentVersion
    Node        *BKENode
    Cluster     *BKECluster
    Action      BinaryAction  // Install / Upgrade / Uninstall
    Timeout     time.Duration
    RetryCount  int
}

// BinaryAction 定义二进制操作类型
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
    
    // 1. 解析架构和操作系统
    arch := opts.Node.Spec.Architecture   // amd64 / arm64
    os := opts.Node.Spec.OperatingSystem // centos / ubuntu / kylin
    
    // 2. 下载二进制制品 (带缓存)
    artifacts, err := i.downloadArtifacts(ctx, binary, arch, os)
    if err != nil {
        return fmt.Errorf("failed to download artifacts: %w", err)
    }
    
    // 3. 准备安装脚本 (模板渲染)
    script, err := i.renderInstallScript(binary.InstallScript, artifacts, opts)
    if err != nil {
        return fmt.Errorf("failed to render install script: %w", err)
    }
    
    // 4. 通过 SSH 执行安装
    switch opts.Action {
    case BinaryActionInstall, BinaryActionUpgrade:
        return i.executeInstall(ctx, opts.Node, script, artifacts)
    case BinaryActionUninstall:
        return i.executeUninstall(ctx, opts.Node, binary.UninstallScript)
    }
    
    return nil
}

// downloadArtifacts 下载二进制制品 (支持架构适配)
func (i *BinaryInstaller) downloadArtifacts(ctx context.Context, binary BinarySpec, arch, os string) (map[string]*Artifact, error) {
    artifacts := make(map[string]*Artifact)
    
    for _, art := range binary.Artifacts {
        // 解析模板变量
        url := i.resolveTemplate(art.URL, map[string]string{
            "{{version}}": binary.Version,
            "{{arch}}":    arch,
            "{{os}}":      os,
        })
        
        // 检查缓存
        cacheKey := i.computeCacheKey(url, art.Checksum)
        if cached := i.cache.Get(cacheKey); cached != nil {
            artifacts[art.Name] = cached
            continue
        }
        
        // 下载并校验 checksum
        data, err := i.downloadAndVerify(ctx, url, art.Checksum)
        if err != nil {
            return nil, err
        }
        
        artifact := &Artifact{
            Name:     art.Name,
            URL:      url,
            Checksum: art.Checksum,
            Data:     data,
            Path:     i.saveToCache(cacheKey, data),
        }
        artifacts[art.Name] = artifact
    }
    
    return artifacts, nil
}

// executeInstall 通过 SSH 执行安装脚本
func (i *BinaryInstaller) executeInstall(ctx context.Context, node *BKENode, script string, artifacts map[string]*Artifact) error {
    // 1. 上传二进制文件到节点
    for name, art := range artifacts {
        remotePath := fmt.Sprintf("/tmp/bke-install/%s", name)
        if err := i.sshClient.Upload(node.IP, art.Data, remotePath); err != nil {
            return fmt.Errorf("failed to upload %s to %s: %w", name, node.IP, err)
        }
    }
    
    // 2. 执行安装脚本
    result, err := i.sshClient.Execute(node.IP, script)
    if err != nil {
        return fmt.Errorf("install script failed on %s: %w\nstdout: %s\nstderr: %s", 
            node.IP, err, result.Stdout, result.Stderr)
    }
    
    return nil
}
```

### 2.4 DAG 调度集成

```go
// pkg/dagexec/binary_component_executor.go

// BinaryComponentExecutor 负责在 DAG 中执行二进制组件
type BinaryComponentExecutor struct {
    installer *binaryinstaller.BinaryInstaller
    store     *manifest.Store
}

// ExecuteComponent 执行二进制组件 (适配 DAG 调度器接口)
func (e *BinaryComponentExecutor) ExecuteComponent(ctx context.Context, node *dagexec.ComponentNode, phaseCtx *phaseframe.PhaseContext) error {
    component := node.Component
    
    // 1. 从 ManifestStore 获取 ComponentVersion
    cv, err := e.store.GetComponentVersion(component.Name, component.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version: %w", err)
    }
    
    // 2. 确认是二进制类型
    if cv.Spec.Type != configv1alpha1.ComponentTypeBinary {
        return fmt.Errorf("component %s is not a binary component", component.Name)
    }
    
    // 3. 获取需要操作的节点
    nodes := e.getTargetNodes(phaseCtx, component)
    
    // 4. 根据升级策略执行
    strategy := cv.Spec.UpgradeStrategy
    switch strategy.Mode {
    case "Rolling":
        return e.executeRolling(ctx, nodes, cv, strategy)
    case "Parallel":
        return e.executeParallel(ctx, nodes, cv, strategy)
    case "Batch":
        return e.executeBatch(ctx, nodes, cv, strategy)
    }
    
    return nil
}

// executeRolling 滚动执行 (逐节点)
func (e *BinaryComponentExecutor) executeRolling(ctx context.Context, nodes []Node, cv *ComponentVersion, strategy UpgradeStrategySpec) error {
    for _, node := range nodes {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }
        
        opts := binaryinstaller.InstallOptions{
            Component:  cv,
            Node:       node,
            Action:     binaryinstaller.BinaryActionUpgrade,
            Timeout:    strategy.Timeout,
            RetryCount: 3,
        }
        
        if err := e.installer.Install(ctx, opts); err != nil {
            switch strategy.FailurePolicy {
            case "FailFast":
                return err
            case "Continue":
                log.Warn("node %s upgrade failed, continuing: %v", node.IP, err)
                continue
            case "Rollback":
                if rbErr := e.rollback(node, cv); rbErr != nil {
                    return fmt.Errorf("upgrade failed and rollback failed: %w; rollback: %v", err, rbErr)
                }
                continue
            }
        }
    }
    
    return nil
}
```

## 三、Phase 重构方案

### 3.1 移除旧 Phase

| 旧 Phase | 新实现方式 | 说明 |
|---------|-----------|------|
| `EnsureContainerdUpgrade` | 二进制组件 + DAG 调度 | 通过 ReleaseImage 声明，BinaryInstaller 执行 |
| `EnsureBKEAgent` (安装) | 二进制组件 + DAG 调度 | 通过 ReleaseImage install.components 声明 |
| `EnsureAgentUpgrade` (升级) | 二进制组件 + DAG 调度 | 通过 ReleaseImage upgrade.components 声明 |

### 3.2 新 DAG 定义

```go
// pkg/dagexec/install_dag.go (重构后)

func BuildInstallDAG(releaseImage *ReleaseImage) *DAG {
    dag := NewDAG()
    
    // CommonPhases
    dag.AddNode("finalizer", NewEnsureFinalizer(), nil, FailFast)
    dag.AddNode("paused", NewEnsurePaused(), []string{"finalizer"}, Continue)
    dag.AddNode("manage", NewEnsureClusterManage(), []string{"paused"}, Continue)
    dag.AddNode("delete", NewEnsureDeleteOrReset(), []string{"manage"}, Continue)
    dag.AddNode("dryrun", NewEnsureDryRun(), []string{"delete"}, Continue)
    
    // 从 ReleaseImage 动态构建 DeployPhases
    for _, comp := range releaseImage.Spec.Install.Components {
        cv := manifestStore.GetComponentVersion(comp.Name, comp.Version)
        
        switch cv.Spec.Type {
        case ComponentTypeBinary:
            // 二进制组件节点
            dag.AddNode(comp.Name, NewBinaryComponentNode(cv), getDependencies(cv), getFailurePolicy(cv))
        case ComponentTypeInline:
            // inline Phase 节点
            dag.AddNode(comp.Name, NewInlinePhaseNode(cv), getDependencies(cv), getFailurePolicy(cv))
        case ComponentTypeYAML, ComponentTypeHelm:
            // 清单组件节点
            dag.AddNode(comp.Name, NewManifestComponentNode(cv), getDependencies(cv), getFailurePolicy(cv))
        }
    }
    
    return dag
}

func BuildUpgradeDAG(releaseImage *ReleaseImage) *DAG {
    dag := NewDAG()
    
    // 从 ReleaseImage 动态构建 UpgradePhases
    for _, comp := range releaseImage.Spec.Upgrade.Components {
        cv := manifestStore.GetComponentVersion(comp.Name, comp.Version)
        
        switch cv.Spec.Type {
        case ComponentTypeBinary:
            dag.AddNode(comp.Name, NewBinaryComponentNode(cv), getDependencies(cv), getFailurePolicy(cv))
        case ComponentTypeInline:
            dag.AddNode(comp.Name, NewInlinePhaseNode(cv), getDependencies(cv), getFailurePolicy(cv))
        case ComponentTypeYAML, ComponentTypeHelm:
            dag.AddNode(comp.Name, NewManifestComponentNode(cv), getDependencies(cv), getFailurePolicy(cv))
        }
    }
    
    return dag
}
```

### 3.3 版本上下文注入

```go
// pkg/versioncontext/context.go

// VersionContext 提供版本信息给 Phase 和 BinaryInstaller
type VersionContext struct {
    CurrentBundle *ReleaseBundle
    TargetBundle  *ReleaseBundle
}

// GetTarget 获取目标组件版本
func (vc *VersionContext) GetTarget(component string) string {
    for _, comp := range vc.TargetBundle.Spec.Upgrade.Components {
        if comp.Name == component {
            return comp.Version
        }
    }
    // 回退到 install.components
    for _, comp := range vc.TargetBundle.Spec.Install.Components {
        if comp.Name == component {
            return comp.Version
        }
    }
    return ""
}

// GetCurrent 获取当前组件版本
func (vc *VersionContext) GetCurrent(component string) string {
    for _, comp := range vc.CurrentBundle.Spec.Install.Components {
        if comp.Name == component {
            return comp.Version
        }
    }
    return ""
}

// NeedUpgrade 判断是否需要升级
func (vc *VersionContext) NeedUpgrade(component string) bool {
    current := vc.GetCurrent(component)
    target := vc.GetTarget(component)
    return current != target && semver.Compare(target, current) > 0
}
```

## 四、完整执行流程

### 4.1 安装流程

```
用户创建 BKECluster (desiredVersion: v2.6.0)
  │
  ▼
ClusterVersionReconciler 检测到新版本
  │
  ├── 解析 ReleaseImage v2.6.0
  │     └── install.components: [containerd/v1.7.18, bkeagent/v2.6.0, kubernetes/v1.29.0, ...]
  │
  ├── 从 bke-manifests 加载 ComponentVersion
  │     ├── containerd/v1.7.18/component.yaml (type: binary)
  │     ├── bkeagent/v2.6.0/component.yaml (type: binary)
  │     └── kubernetes/v1.29.0/component.yaml (type: composite)
  │
  ├── 构建安装 DAG
  │     ┌─────────────────────────────────────────┐
  │     │ finalizer → paused → manage → delete    │
  │     │                    → dryrun              │
  │     │                           → agent (binary)│
  │     │                           → env (binary) │
  │     │                           → apiobj       │
  │     │                           → certs        │
  │     │                           → lb           │
  │     │                           → master_init  │
  │     │                           → master_join  │
  │     │                           → worker_join  │
  │     │                           → addon        │
  │     │                           → postprocess  │
  │     │                           → agent_switch │
  │     └─────────────────────────────────────────┘
  │
  ├── DAG Scheduler 执行
  │     ├── BinaryComponentExecutor 执行 containerd 安装
  │     │     ├── 下载 containerd-1.7.18-linux-amd64.tar.gz
  │     │     ├── 渲染安装脚本
  │     │     ├── SSH 上传并执行
  │     │     └── 验证 containerd --version
  │     │
  │     └── BinaryComponentExecutor 执行 bkeagent 安装
  │           ├── 下载 bkeagent-2.6.0-linux-amd64
  │           ├── 渲染配置文件模板
  │           ├── SSH 上传并执行
  │           └── 验证 bkeagent --version
  │
  └── 安装完成 → ClusterStatus = Ready
```

### 4.2 升级流程

```
用户修改 ClusterVersion desiredVersion: v2.5.0 → v2.6.0
  │
  ▼
ClusterVersionReconciler 检测到版本变更
  │
  ├── 解析 ReleaseImage v2.6.0
  │     └── upgrade.components: [containerd/v1.7.18, bkeagent/v2.6.0, etcd/v3.5.12, ...]
  │
  ├── 解析当前 ReleaseImage v2.5.0
  │     └── install.components: [containerd/v1.7.15, bkeagent/v2.5.0, ...]
  │
  ├── 对比版本，确定需要升级的组件
  │     ├── containerd: v1.7.15 → v1.7.18 ✅ 需要升级
  │     ├── bkeagent: v2.5.0 → v2.6.0 ✅ 需要升级
  │     └── kubernetes: v1.28.0 → v1.29.0 ✅ 需要升级
  │
  ├── 构建升级 DAG
  │     ┌─────────────────────────────────────────┐
  │     │ provider → agent (binary)               │
  │     │           → containerd (binary)          │
  │     │           → etcd (inline)                │
  │     │           → worker (inline)              │
  │     │           → master (inline)              │
  │     │           → worker_del                   │
  │     │           → master_del                   │
  │     │           → component                    │
  │     │           → cluster                      │
  │     └─────────────────────────────────────────┘
  │
  ├── DAG Scheduler 执行 (按拓扑批次)
  │     │
  │     ├── Batch 1: provider
  │     │
  │     ├── Batch 2: agent (binary)
  │     │     └── BinaryComponentExecutor 滚动升级 bkeagent
  │     │           ├── 逐节点下载新二进制
  │     │           ├── 停止旧服务 → 备份 → 安装新二进制 → 启动
  │     │           └── Ping 验证新 Agent 就绪
  │     │
  │     ├── Batch 3: containerd (binary)
  │     │     └── BinaryComponentExecutor 滚动升级 containerd
  │     │           ├── 逐节点下载新二进制
  │     │           ├── systemctl stop → 备份 → 安装 → systemctl start
  │     │           └── containerd --version 验证
  │     │
  │     ├── Batch 4: etcd → worker → master (inline)
  │     │
  │     └── Batch 5: component → cluster
  │
  └── 升级完成 → ClusterStatus = Ready
```

## 五、迁移策略

### 5.1 分阶段迁移

| 阶段 | 时间 | 内容 | 风险 |
|------|------|------|------|
| **Phase 1** | 第1周 | 实现 BinaryInstaller 核心逻辑 | 低 (独立组件) |
| **Phase 2** | 第2周 | 创建 containerd/bkeagent 的 ComponentVersion YAML | 低 (声明式配置) |
| **Phase 3** | 第3周 | 重构 ReleaseImage 加载逻辑，支持 binary 类型 | 中 (需充分测试) |
| **Phase 4** | 第4周 | 将 containerd/bkeagent 从硬编码 Phase 迁移到 DAG | 高 (需要灰度发布) |
| **Phase 5** | 第5周 | 移除旧 Phase 代码 | 中 (需要回滚预案) |

### 5.2 Feature Gate 控制

```go
const (
    BinaryComponentSupport = "BinaryComponentSupport"  // 启用二进制组件支持
)

// 默认关闭
var defaultFeatureGates = map[string]bool{
    BinaryComponentSupport: false,
}
```

### 5.3 向后兼容

```go
// 兼容层：同时支持新旧两种方式
func (r *BKEClusterReconciler) executeContainerdUpgrade(ctx context.Context) error {
    if featuregate.Enabled(BinaryComponentSupport) {
        // 新方式：通过 DAG + BinaryInstaller 执行
        return r.executeBinaryComponent(ctx, "containerd")
    }
    
    // 旧方式：使用硬编码 Phase
    return r.executeLegacyContainerdUpgrade(ctx)
}
```

## 六、收益评估

| 指标 | 当前 | 重构后 | 提升 |
|------|------|--------|------|
| **版本管理** | 散落在 BKECluster.Spec 各字段 | 统一在 ReleaseImage 中 | 集中管理 |
| **新增二进制组件** | 修改核心代码 + 新增 Phase | 添加 ComponentVersion YAML | 零代码侵入 |
| **安装/升级一致性** | 不同的 Phase 实现 | 统一的 BinaryInstaller | 逻辑复用 |
| **架构适配** | 硬编码在代码中 | 模板变量 `{{arch}}` | 声明式配置 |
| **回滚能力** | 无 | BinaryInstaller 支持卸载脚本 | 可回滚 |
| **可观测性** | 日志分散 | 统一事件记录 | 完整追溯 |

# ReleaseImage 支持 ComponentTypeHelm 及二进制组件模板变量完整设计方案

## 一、ComponentTypeHelm 类型组件设计

### 1.1 Helm 组件定义

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
  
  # Helm 配置
  helm:
    # Chart 来源 (支持多种来源)
    chart:
      # 方式1: OCI Registry
      oci:
        repository: "registry.openfuyao.cn/charts/coredns"
        tag: "v1.11.1"
      
      # 方式2: HTTP URL
      # url: "https://charts.openfuyao.cn/coredns-v1.11.1.tgz"
      
      # 方式3: 本地路径 (用于离线环境)
      # localPath: "/opt/bke-manifests/charts/coredns-v1.11.1.tgz"
    
    # 版本校验
    checksum: "sha256:abc123..."
    
    # 命名空间
    namespace: kube-system
    
    # Release 名称
    releaseName: coredns
    
    # 值覆盖 (支持模板变量)
    values:
      image:
        repository: "registry.openfuyao.cn/coredns/coredns"
        tag: "{{componentVersion}}"
        pullPolicy: IfNotPresent
      replicaCount: "{{controlPlaneReplicas}}"
      resources:
        limits:
          cpu: "100m"
          memory: "128Mi"
        requests:
          cpu: "50m"
          memory: "64Mi"
      service:
        type: ClusterIP
        clusterIP: "{{corednsClusterIP}}"
    
    # 自定义 values 文件 (可选)
    valuesFiles:
      - "values-{{os}}.yaml"    # 按操作系统区分
      - "values-{{arch}}.yaml"  # 按架构区分
    
    # 安装/升级策略
    strategy:
      # 安装模式: Install / Upgrade / Rollback
      mode: Upgrade
      
      # 等待策略
      wait: true
      waitTimeout: "5m"
      
      # 原子操作 (失败自动回滚)
      atomic: true
      
      # 清理旧资源
      cleanupOnFail: false
    
    # 依赖检查
    preInstallHooks:
      - name: check-namespace
        type: Job
        manifest: |
          apiVersion: batch/v1
          kind: Job
          metadata:
            name: pre-install-check
            namespace: kube-system
          spec:
            template:
              spec:
                containers:
                  - name: check
                    image: registry.openfuyao.cn/busybox:1.36
                    command: ["sh", "-c", "kubectl get ns kube-system"]
                restartPolicy: Never
    
    # 健康检查
    healthCheck:
      enabled: true
      timeout: "3m"
      interval: "10s"
      # 检查方式
      checks:
        - type: PodReady
          namespace: kube-system
          labelSelector: "k8s-app=kube-dns"
          minReady: 1
        - type: EndpointReady
          namespace: kube-system
          name: kube-dns
          port: 53
    
    # 回滚配置
    rollback:
      enabled: true
      maxHistory: 3  # 保留历史版本数
    
    # 卸载配置
    uninstall:
      # 卸载前清理资源
      preUninstallHooks:
        - type: Job
          manifest: |
            apiVersion: batch/v1
            kind: Job
            metadata:
              name: pre-uninstall-cleanup
              namespace: kube-system
            spec:
              template:
                spec:
                  containers:
                    - name: cleanup
                      image: registry.openfuyao.cn/busybox:1.36
                      command: ["sh", "-c", "echo cleaning up"]
                  restartPolicy: Never
    
  # 兼容性约束
  compatibility:
    constraints:
      - component: kubernetes
        rule: ">=1.24.0"
  
  # 依赖关系
  dependencies:
    - name: kubernetes
      phase: Install
  
  # 升级策略
  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

### 1.2 Helm 组件安装器

```go
// pkg/helminstaller/installer.go

// HelmInstaller 负责 Helm 类型组件的安装、升级和卸载
type HelmInstaller struct {
    client     client.Client
    restConfig *rest.Config
    cacheDir   string
    httpClient *http.Client
    ociClient  *oci.Client
}

// InstallOptions Helm 安装选项
type InstallOptions struct {
    Component   *ComponentVersion
    Cluster     *BKECluster
    Action      HelmAction  // Install / Upgrade / Uninstall / Rollback
    Timeout     time.Duration
    DryRun      bool
}

// HelmAction 定义 Helm 操作类型
type HelmAction string

const (
    HelmActionInstall   HelmAction = "Install"
    HelmActionUpgrade   HelmAction = "Upgrade"
    HelmActionUninstall HelmAction = "Uninstall"
    HelmActionRollback  HelmAction = "Rollback"
)

// Install 执行 Helm 组件安装/升级
func (i *HelmInstaller) Install(ctx context.Context, opts InstallOptions) error {
    component := opts.Component
    helm := component.Spec.Helm
    
    // 1. 获取 Chart
    chart, err := i.getChart(ctx, helm.Chart)
    if err != nil {
        return fmt.Errorf("failed to get chart: %w", err)
    }
    
    // 2. 渲染 Values (模板变量替换)
    values, err := i.renderValues(ctx, helm.Values, opts)
    if err != nil {
        return fmt.Errorf("failed to render values: %w", err)
    }
    
    // 3. 加载自定义 values 文件
    for _, vf := range helm.ValuesFiles {
        customValues, err := i.loadValuesFile(ctx, vf, opts)
        if err != nil {
            log.Warn("failed to load values file %s: %v", vf, err)
            continue
        }
        values = mergeValues(values, customValues)
    }
    
    // 4. 执行 Helm 操作
    actionConfig, err := i.getActionConfig(ctx, helm.Namespace)
    if err != nil {
        return fmt.Errorf("failed to get action config: %w", err)
    }
    
    switch opts.Action {
    case HelmActionInstall:
        return i.install(ctx, actionConfig, chart, values, helm, opts)
    case HelmActionUpgrade:
        return i.upgrade(ctx, actionConfig, chart, values, helm, opts)
    case HelmActionUninstall:
        return i.uninstall(ctx, actionConfig, helm, opts)
    case HelmActionRollback:
        return i.rollback(ctx, actionConfig, helm, opts)
    }
    
    return nil
}

// getChart 获取 Chart (支持 OCI/HTTP/本地)
func (i *HelmInstaller) getChart(ctx context.Context, chartSpec ChartSpec) (*chart.Chart, error) {
    if chartSpec.OCI != nil {
        return i.getChartFromOCI(ctx, chartSpec.OCI)
    }
    if chartSpec.URL != "" {
        return i.getChartFromURL(ctx, chartSpec.URL)
    }
    if chartSpec.LocalPath != "" {
        return i.getChartFromLocal(ctx, chartSpec.LocalPath)
    }
    return nil, errors.New("no chart source specified")
}

// renderValues 渲染 Values 模板
func (i *HelmInstaller) renderValues(ctx context.Context, rawValues map[string]interface{}, opts InstallOptions) (map[string]interface{}, error) {
    // 构建模板变量映射
    vars := i.buildTemplateVariables(ctx, opts)
    
    // 递归渲染所有字符串值
    return renderMap(rawValues, vars)
}

// buildTemplateVariables 构建模板变量映射
func (i *HelmInstaller) buildTemplateVariables(ctx context.Context, opts InstallOptions) map[string]string {
    cluster := opts.Cluster
    vars := map[string]string{
        // 集群信息
        "{{clusterName}}":      cluster.Name,
        "{{clusterNamespace}}": cluster.Namespace,
        "{{apiServer}}":        cluster.Spec.ClusterConfig.Cluster.APIServer,
        
        // 版本信息
        "{{componentVersion}}": opts.Component.Spec.Version,
        "{{clusterVersion}}":   cluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
        
        // 节点信息 (取第一个控制面节点)
        "{{controlPlaneReplicas}}": strconv.Itoa(len(cluster.Spec.Nodes.Masters())),
        
        // 网络信息
        "{{serviceCIDR}}":     cluster.Spec.ClusterConfig.Network.ServiceCIDR,
        "{{podCIDR}}":         cluster.Spec.ClusterConfig.Network.PodCIDR,
        "{{dnsDomain}}":       cluster.Spec.ClusterConfig.Cluster.DNSDomain,
        "{{corednsClusterIP}}": i.computeCoreDNSClusterIP(cluster),
        
        // 镜像仓库
        "{{imageRegistry}}":   cluster.Spec.ClusterConfig.Registry.Endpoint,
        "{{imagePullSecret}}": cluster.Spec.ClusterConfig.Registry.PullSecret,
    }
    
    return vars
}

// install 执行 Helm Install
func (i *HelmInstaller) install(ctx context.Context, actionConfig *action.Configuration, 
    chart *chart.Chart, values map[string]interface{}, helm HelmSpec, opts InstallOptions) error {
    
    client := action.NewInstall(actionConfig)
    client.ReleaseName = helm.ReleaseName
    client.Namespace = helm.Namespace
    client.CreateNamespace = true
    client.Wait = helm.Strategy.Wait
    client.Timeout = helm.Strategy.WaitTimeout
    client.Atomic = helm.Strategy.Atomic
    client.DryRun = opts.DryRun
    
    release, err := client.Run(chart, values)
    if err != nil {
        if helm.Strategy.CleanupOnFail && release != nil {
            // 清理失败的安装
            uninstallClient := action.NewUninstall(actionConfig)
            uninstallClient.Run(release.Name)
        }
        return fmt.Errorf("helm install failed: %w", err)
    }
    
    // 执行健康检查
    if helm.HealthCheck.Enabled {
        if err := i.runHealthCheck(ctx, helm.HealthCheck, opts); err != nil {
            return fmt.Errorf("health check failed after install: %w", err)
        }
    }
    
    return nil
}

// upgrade 执行 Helm Upgrade
func (i *HelmInstaller) upgrade(ctx context.Context, actionConfig *action.Configuration,
    chart *chart.Chart, values map[string]interface{}, helm HelmSpec, opts InstallOptions) error {
    
    client := action.NewUpgrade(actionConfig)
    client.Namespace = helm.Namespace
    client.Wait = helm.Strategy.Wait
    client.Timeout = helm.Strategy.WaitTimeout
    client.Atomic = helm.Strategy.Atomic
    client.MaxHistory = helm.Rollback.MaxHistory
    client.DryRun = opts.DryRun
    
    release, err := client.Run(helm.ReleaseName, chart, values)
    if err != nil {
        if helm.Strategy.CleanupOnFail && release != nil {
            uninstallClient := action.NewUninstall(actionConfig)
            uninstallClient.Run(release.Name)
        }
        return fmt.Errorf("helm upgrade failed: %w", err)
    }
    
    // 执行健康检查
    if helm.HealthCheck.Enabled {
        if err := i.runHealthCheck(ctx, helm.HealthCheck, opts); err != nil {
            return fmt.Errorf("health check failed after upgrade: %w", err)
        }
    }
    
    return nil
}
```

### 1.3 ReleaseImage 中 Helm 组件引用

```yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: ri-v2.6.0
spec:
  version: "v2.6.0"
  
  install:
    components:
      - name: coredns
        version: v1.11.1
      - name: kube-proxy
        version: v1.29.0
      # ... 其他组件
        
  upgrade:
    components:
      - name: coredns
        version: v1.11.1
      - name: kube-proxy
        version: v1.29.0
      # ... 其他组件
```

## 二、configTemplates 完整使用设计

### 2.1 configTemplates 定义增强

```yaml
# bke-manifests/bkeagent/v2.6.0/component.yaml
spec:
  name: bkeagent
  type: binary
  version: v2.6.0
  
  binary:
    # 配置文件模板
    configTemplates:
      # 模板1: 主配置文件
      - name: bkeagent.conf
        path: "/etc/openFuyao/bkeagent/bkeagent.conf"
        mode: "0644"
        owner: "root:root"
        # 模板内容 (支持 Go template 语法)
        content: |
          # BKE Agent Configuration
          # Generated by BKE Cluster Provider - DO NOT EDIT MANUALLY
          
          cluster_name: {{.clusterName}}
          cluster_namespace: {{.clusterNamespace}}
          
          # API Server 配置
          api_server: {{.apiServer}}
          api_server_ca: /etc/openFuyao/bkeagent/ca.crt
          api_server_cert: /etc/openFuyao/bkeagent/tls.crt
          api_server_key: /etc/openFuyao/bkeagent/tls.key
          
          # Kubeconfig
          kubeconfig_path: /etc/openFuyao/bkeagent/kubeconfig
          
          # 日志配置
          log_level: {{.logLevel | default "info"}}
          log_path: /var/log/bkeagent/bkeagent.log
          log_max_size: {{.logMaxSize | default "100"}}
          log_max_backups: {{.logMaxBackups | default "3"}}
          
          # 节点信息
          node_ip: {{.nodeIP}}
          node_hostname: {{.nodeHostname}}
          node_role: {{.nodeRole}}
          
          # 心跳配置
          heartbeat_interval: {{.heartbeatInterval | default "10"}}
          heartbeat_timeout: {{.heartbeatTimeout | default "30"}}
          
          # 命令执行配置
          command_timeout: {{.commandTimeout | default "600"}}
          command_max_concurrent: {{.commandMaxConcurrent | default "5"}}
          
          # 特性开关
          features:
            command_support: {{.featureCommandSupport | default "true"}}
            file_transfer: {{.featureFileTransfer | default "true"}}
            log_collection: {{.featureLogCollection | default "true"}}
      
      # 模板2: TLS 证书配置 (从 Secret 获取)
      - name: tls.crt
        path: "/etc/openFuyao/bkeagent/tls.crt"
        mode: "0644"
        owner: "root:root"
        # 从 Secret 获取内容
        secretRef:
          name: bkeagent-tls
          namespace: "{{.clusterNamespace}}"
          key: tls.crt
      
      - name: tls.key
        path: "/etc/openFuyao/bkeagent/tls.key"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{.clusterNamespace}}"
          key: tls.key
      
      - name: ca.crt
        path: "/etc/openFuyao/bkeagent/ca.crt"
        mode: "0644"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{.clusterNamespace}}"
          key: ca.crt
      
      # 模板3: Kubeconfig (动态生成)
      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/kubeconfig"
        mode: "0600"
        owner: "root:root"
        # 动态生成 kubeconfig
        kubeconfigTemplate:
          clusterName: "{{.clusterName}}"
          apiServer: "{{.apiServer}}"
          caCertPath: "/etc/openFuyao/bkeagent/ca.crt"
          clientCertPath: "/etc/openFuyao/bkeagent/tls.crt"
          clientKeyPath: "/etc/openFuyao/bkeagent/tls.key"
          namespace: "{{.clusterNamespace}}"
          serviceAccount: bkeagent
```

### 2.2 configTemplates 渲染引擎

```go
// pkg/binaryinstaller/config_renderer.go

// ConfigRenderer 负责配置文件模板的渲染
type ConfigRenderer struct {
    client     client.Client
    templateFuncs template.FuncMap
}

// RenderConfig 渲染配置文件模板
func (r *ConfigRenderer) RenderConfig(ctx context.Context, template ConfigTemplate, opts InstallOptions) ([]byte, error) {
    // 1. 根据模板类型渲染
    switch {
    case template.Content != "":
        return r.renderContentTemplate(ctx, template, opts)
    case template.SecretRef != nil:
        return r.renderSecretTemplate(ctx, template, opts)
    case template.KubeconfigTemplate != nil:
        return r.renderKubeconfigTemplate(ctx, template, opts)
    }
    
    return nil, errors.New("no template content specified")
}

// renderContentTemplate 渲染内容模板 (Go template)
func (r *ConfigRenderer) renderContentTemplate(ctx context.Context, template ConfigTemplate, opts InstallOptions) ([]byte, error) {
    // 构建模板数据
    data := r.buildTemplateData(ctx, opts)
    
    // 解析模板
    tmpl, err := template.New(template.Name).Funcs(r.templateFuncs).Parse(template.Content)
    if err != nil {
        return nil, fmt.Errorf("failed to parse template: %w", err)
    }
    
    // 渲染
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return nil, fmt.Errorf("failed to render template: %w", err)
    }
    
    return buf.Bytes(), nil
}

// renderSecretTemplate 从 Secret 获取内容
func (r *ConfigRenderer) renderSecretTemplate(ctx context.Context, template ConfigTemplate, opts InstallOptions) ([]byte, error) {
    secretRef := template.SecretRef
    
    // 渲染 namespace 模板变量
    namespace, err := r.renderString(secretRef.Namespace, opts)
    if err != nil {
        return nil, fmt.Errorf("failed to render namespace: %w", err)
    }
    
    // 获取 Secret
    secret := &corev1.Secret{}
    if err := r.client.Get(ctx, types.NamespacedName{
        Name:      secretRef.Name,
        Namespace: namespace,
    }, secret); err != nil {
        return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretRef.Name, err)
    }
    
    // 获取指定 key 的内容
    data, ok := secret.Data[secretRef.Key]
    if !ok {
        return nil, fmt.Errorf("key %s not found in secret %s/%s", secretRef.Key, namespace, secretRef.Name)
    }
    
    return data, nil
}

// renderKubeconfigTemplate 动态生成 kubeconfig
func (r *ConfigRenderer) renderKubeconfigTemplate(ctx context.Context, template ConfigTemplate, opts InstallOptions) ([]byte, error) {
    kc := template.KubeconfigTemplate
    
    kubeconfig := clientcmdapi.Config{
        Kind:       "Config",
        APIVersion: "v1",
        Clusters: map[string]*clientcmdapi.Cluster{
            kc.ClusterName: {
                Server:                   kc.APIServer,
                CertificateAuthority:     kc.CACertPath,
            },
        },
        AuthInfos: map[string]*clientcmdapi.AuthInfo{
            kc.ClusterName: {
                ClientCertificate: kc.ClientCertPath,
                ClientKey:         kc.ClientKeyPath,
            },
        },
        Contexts: map[string]*clientcmdapi.Context{
            kc.ClusterName: {
                Cluster:   kc.ClusterName,
                AuthInfo:  kc.ClusterName,
                Namespace: kc.Namespace,
            },
        },
        CurrentContext: kc.ClusterName,
    }
    
    return clientcmd.Write(kubeconfig)
}

// buildTemplateData 构建模板数据
func (r *ConfigRenderer) buildTemplateData(ctx context.Context, opts InstallOptions) map[string]interface{} {
    cluster := opts.Cluster
    node := opts.Node
    
    return map[string]interface{}{
        // 集群信息
        "clusterName":      cluster.Name,
        "clusterNamespace": cluster.Namespace,
        "apiServer":        cluster.Spec.ClusterConfig.Cluster.APIServer,
        
        // 节点信息
        "nodeIP":       node.IP,
        "nodeHostname": node.Hostname,
        "nodeRole":     node.Role,  // master / worker
        "nodeArch":     node.Spec.Architecture,
        "nodeOS":       node.Spec.OperatingSystem,
        
        // 版本信息
        "componentVersion": opts.Component.Spec.Version,
        "clusterVersion":   cluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
        
        // 默认值 (可通过 ComponentVersion 覆盖)
        "logLevel":              "info",
        "logMaxSize":            "100",
        "logMaxBackups":         "3",
        "heartbeatInterval":     "10",
        "heartbeatTimeout":      "30",
        "commandTimeout":        "600",
        "commandMaxConcurrent":  "5",
        "featureCommandSupport": "true",
        "featureFileTransfer":   "true",
        "featureLogCollection":  "true",
    }
}
```

### 2.3 configTemplates 安装流程

```go
// pkg/binaryinstaller/installer.go (补充)

// installConfigs 安装配置文件
func (i *BinaryInstaller) installConfigs(ctx context.Context, node *BKENode, component *ComponentVersion, opts InstallOptions) error {
    renderer := &ConfigRenderer{client: i.client}
    
    for _, template := range component.Spec.Binary.ConfigTemplates {
        // 1. 渲染模板内容
        content, err := renderer.RenderConfig(ctx, template, opts)
        if err != nil {
            return fmt.Errorf("failed to render config %s: %w", template.Name, err)
        }
        
        // 2. 上传到节点
        remotePath := template.Path
        if err := i.sshClient.Upload(node.IP, content, remotePath); err != nil {
            return fmt.Errorf("failed to upload config %s to %s: %w", template.Name, node.IP, err)
        }
        
        // 3. 设置权限
        if template.Mode != "" {
            if err := i.sshClient.Execute(node.IP, fmt.Sprintf("chmod %s %s", template.Mode, remotePath)); err != nil {
                log.Warn("failed to set mode for %s: %v", remotePath, err)
            }
        }
        if template.Owner != "" {
            if err := i.sshClient.Execute(node.IP, fmt.Sprintf("chown %s %s", template.Owner, remotePath)); err != nil {
                log.Warn("failed to set owner for %s: %v", remotePath, err)
            }
        }
    }
    
    return nil
}
```

## 三、installScript 模板变量完整设计

### 3.1 模板变量分类

```yaml
# installScript 支持的所有模板变量
variables:
  # 1. 集群信息变量
  cluster:
    - "{{clusterName}}"           # 集群名称
    - "{{clusterNamespace}}"      # 集群命名空间
    - "{{apiServer}}"             # API Server 地址
    - "{{apiServerPort}}"         # API Server 端口
    - "{{serviceCIDR}}"           # Service CIDR
    - "{{podCIDR}}"               # Pod CIDR
    - "{{dnsDomain}}"             # DNS 域名
    - "{{clusterDNS}}"            # Cluster DNS IP
  
  # 2. 节点信息变量
  node:
    - "{{nodeIP}}"                # 节点 IP
    - "{{nodeHostname}}"          # 节点主机名
    - "{{nodeRole}}"              # 节点角色 (master/worker/etcd)
    - "{{nodeArch}}"              # 节点架构 (amd64/arm64)
    - "{{nodeOS}}"                # 操作系统 (centos/ubuntu/kylin)
    - "{{nodeOSVersion}}"         # 操作系统版本 (7/8/20.04/V10)
    - "{{nodeKernelVersion}}"     # 内核版本
    - "{{nodeCPUs}}"              # CPU 核心数
    - "{{nodeMemoryMB}}"          # 内存大小 (MB)
    - "{{nodeDiskGB}}"            # 磁盘大小 (GB)
  
  # 3. 版本信息变量
  version:
    - "{{componentVersion}}"      # 当前组件版本
    - "{{componentPreviousVersion}}"  # 上一组件版本 (升级时)
    - "{{clusterVersion}}"        # 集群 Kubernetes 版本
    - "{{etcdVersion}}"           # Etcd 版本
    - "{{containerdVersion}}"     # Containerd 版本
    - "{{bkeagentVersion}}"       # BKEAgent 版本
  
  # 4. 二进制制品变量
  artifact:
    - "{{artifact.<name>.path}}"      # 制品本地路径
    - "{{artifact.<name>.url}}"       # 制品原始 URL
    - "{{artifact.<name>.checksum}}"  # 制品校验和
    - "{{artifact.<name>.filename}}"  # 制品文件名
  
  # 5. 镜像仓库变量
  registry:
    - "{{imageRegistry}}"         # 镜像仓库地址
    - "{{imagePullSecret}}"       # 镜像拉取 Secret
    - "{{imageNamespace}}"        # 镜像命名空间
  
  # 6. 安装路径变量
  path:
    - "{{installPath}}"           # 默认安装路径
    - "{{configPath}}"            # 配置路径
    - "{{logPath}}"               # 日志路径
    - "{{dataPath}}"              # 数据路径
    - "{{binPath}}"               # 二进制路径
  
  # 7. 操作类型变量
  action:
    - "{{action}}"                # 操作类型 (install/upgrade/uninstall)
    - "{{isUpgrade}}"             # 是否升级 (true/false)
    - "{{isInstall}}"             # 是否安装 (true/false)
  
  # 8. 自定义变量 (通过 ComponentVersion 定义)
  custom:
    - "{{.<key>}}"                # 自定义变量，通过 component.spec.binary.variables 定义
```

### 3.2 模板变量在 ComponentVersion 中的定义

```yaml
# bke-manifests/containerd/v1.7.18/component.yaml
spec:
  name: containerd
  type: binary
  version: v1.7.18
  
  binary:
    # 自定义变量 (可覆盖默认值)
    variables:
      logLevel: "info"
      maxConcurrentDownloads: 10
      snapshotter: "overlayfs"
      cniPluginDir: "/opt/cni/bin"
      cniConfigDir: "/etc/cni/net.d"
    
    artifacts:
      - name: containerd
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{componentVersion}}/containerd-{{componentVersion}}-linux-{{nodeArch}}.tar.gz"
        checksum: "sha256:abc123..."
        installPath: "/usr/local/bin"
    
    installScript: |
      #!/bin/bash
      set -e
      
      # ============================================
      # Containerd 安装脚本
      # ============================================
      # 集群: {{clusterName}}
      # 节点: {{nodeIP}} ({{nodeRole}})
      # 架构: {{nodeArch}}
      # 系统: {{nodeOS}} {{nodeOSVersion}}
      # 版本: {{componentVersion}}
      # 操作: {{action}}
      # ============================================
      
      # 1. 环境检查
      echo "[1/7] Checking environment..."
      
      # 检查操作系统
      if [ "{{nodeOS}}" != "centos" ] && [ "{{nodeOS}}" != "ubuntu" ] && [ "{{nodeOS}}" != "kylin" ]; then
        echo "ERROR: Unsupported OS: {{nodeOS}}"
        exit 1
      fi
      
      # 检查架构
      if [ "{{nodeArch}}" != "amd64" ] && [ "{{nodeArch}}" != "arm64" ]; then
        echo "ERROR: Unsupported architecture: {{nodeArch}}"
        exit 1
      fi
      
      # 检查磁盘空间 (至少 1GB)
      available_space=$(df -BG /usr/local | tail -1 | awk '{print $4}' | sed 's/G//')
      if [ "$available_space" -lt 1 ]; then
        echo "ERROR: Insufficient disk space: ${available_space}GB < 1GB"
        exit 1
      fi
      
      # 2. 停止旧服务
      echo "[2/7] Stopping containerd service..."
      systemctl stop containerd || true
      
      # 3. 备份旧版本
      echo "[3/7] Backing up old version..."
      {{if .isUpgrade}}
      if [ -f /usr/local/bin/containerd ]; then
        backup_time=$(date +%Y%m%d%H%M%S)
        cp /usr/local/bin/containerd /usr/local/bin/containerd.bak.${backup_time}
        cp /usr/local/bin/ctr /usr/local/bin/ctr.bak.${backup_time}
        echo "Backup created: containerd.bak.${backup_time}"
      fi
      {{end}}
      
      # 4. 解压并安装二进制文件
      echo "[4/7] Installing containerd {{componentVersion}}..."
      
      # 解压 tar.gz
      tar -xzf "{{artifact.containerd.path}}" -C /usr/local/bin --strip-components=1
      
      # 设置权限
      chmod +x /usr/local/bin/containerd
      chmod +x /usr/local/bin/containerd-shim-runc-v2
      chmod +x /usr/local/bin/ctr
      
      # 5. 创建配置文件
      echo "[5/7] Creating configuration..."
      
      mkdir -p {{configPath}}
      mkdir -p {{dataPath}}
      mkdir -p {{logPath}}
      
      # 生成 containerd 配置
      cat > {{configPath}}/config.toml << EOF
      version = 2
      
      [plugins]
        [plugins."io.containerd.grpc.v1.cri"]
          sandbox_image = "{{imageRegistry}}/pause:3.9"
          
          [plugins."io.containerd.grpc.v1.cri".containerd]
            snapshotter = "{{snapshotter}}"
            default_runtime_name = "runc"
            
            [plugins."io.containerd.grpc.v1.cri".containerd.runtimes]
              [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
                runtime_type = "io.containerd.runc.v2"
                [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
                  SystemdCgroup = true
                  
          [plugins."io.containerd.grpc.v1.cri".cni]
            bin_dir = "{{cniPluginDir}}"
            conf_dir = "{{cniConfigDir}}"
            
        [plugins."io.containerd.grpc.v1.cri".registry]
          config_path = "/etc/containerd/certs.d"
      EOF
      
      # 6. 安装 systemd service
      echo "[6/7] Installing systemd service..."
      
      cat > /etc/systemd/system/containerd.service << EOF
      [Unit]
      Description=containerd container runtime
      Documentation=https://containerd.io
      After=network.target
      
      [Service]
      ExecStartPre=/sbin/modprobe overlay
      ExecStart=/usr/local/bin/containerd --config {{configPath}}/config.toml --log-level {{logLevel}}
      Restart=always
      RestartSec=5
      Delegate=yes
      KillMode=process
      LimitNOFILE=1048576
      LimitNPROC=infinity
      LimitCORE=infinity
      Environment="PATH=/usr/local/bin:{{binPath}}:/sbin:/bin"
      
      [Install]
      WantedBy=multi-user.target
      EOF
      
      # 7. 启动并验证
      echo "[7/7] Starting and verifying containerd..."
      
      systemctl daemon-reload
      systemctl enable containerd
      systemctl start containerd
      
      # 等待服务就绪
      for i in $(seq 1 30); do
        if systemctl is-active --quiet containerd; then
          echo "containerd started successfully"
          break
        fi
        if [ $i -eq 30 ]; then
          echo "ERROR: containerd failed to start"
          journalctl -u containerd --no-pager -n 50
          exit 1
        fi
        sleep 1
      done
      
      # 验证版本
      installed_version=$(/usr/local/bin/containerd --version | awk '{print $3}')
      if [ "$installed_version" != "{{componentVersion}}" ]; then
        echo "ERROR: Version mismatch: expected {{componentVersion}}, got $installed_version"
        exit 1
      fi
      
      echo "Containerd {{componentVersion}} installed successfully on {{nodeIP}}"
```

### 3.3 模板渲染引擎

```go
// pkg/binaryinstaller/template_renderer.go

// TemplateRenderer 负责安装脚本的模板渲染
type TemplateRenderer struct {
    funcMap template.FuncMap
}

// NewTemplateRenderer 创建模板渲染器
func NewTemplateRenderer() *TemplateRenderer {
    return &TemplateRenderer{
        funcMap: template.FuncMap{
            // 字符串函数
            "upper": strings.ToUpper,
            "lower": strings.ToLower,
            "trim":  strings.TrimSpace,
            
            // 条件函数
            "eq": func(a, b interface{}) bool { return reflect.DeepEqual(a, b) },
            "ne": func(a, b interface{}) bool { return !reflect.DeepEqual(a, b) },
            
            // 默认值函数
            "default": func(def, val interface{}) interface{} {
                if val == nil || val == "" {
                    return def
                }
                return val
            },
            
            // 路径函数
            "joinPath": filepath.Join,
            
            // 时间函数
            "now": time.Now,
            "date": func(format string) string {
                return time.Now().Format(format)
            },
        },
    }
}

// RenderScript 渲染安装脚本
func (r *TemplateRenderer) RenderScript(script string, data ScriptData) (string, error) {
    tmpl, err := template.New("installScript").Funcs(r.funcMap).Parse(script)
    if err != nil {
        return "", fmt.Errorf("failed to parse script template: %w", err)
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("failed to render script template: %w", err)
    }
    
    return buf.String(), nil
}

// ScriptData 脚本模板数据
type ScriptData struct {
    // 集群信息
    ClusterName      string
    ClusterNamespace string
    APIServer        string
    APIServerPort    string
    ServiceCIDR      string
    PodCIDR          string
    DNSDomain        string
    ClusterDNS       string
    
    // 节点信息
    NodeIP          string
    NodeHostname    string
    NodeRole        string
    NodeArch        string
    NodeOS          string
    NodeOSVersion   string
    NodeKernelVersion string
    NodeCPUs        int
    NodeMemoryMB    int
    NodeDiskGB      int
    
    // 版本信息
    ComponentVersion       string
    ComponentPreviousVersion string
    ClusterVersion         string
    EtcdVersion            string
    ContainerdVersion      string
    BKEAgentVersion        string
    
    // 二进制制品
    Artifacts map[string]*ArtifactData
    
    // 镜像仓库
    ImageRegistry    string
    ImagePullSecret  string
    ImageNamespace   string
    
    // 安装路径
    InstallPath string
    ConfigPath  string
    LogPath     string
    DataPath    string
    BinPath     string
    
    // 操作类型
    Action     string
    IsUpgrade  bool
    IsInstall  bool
    
    // 自定义变量
    Variables map[string]string
}

// ArtifactData 制品数据
type ArtifactData struct {
    Name     string
    Path     string
    URL      string
    Checksum string
    Filename string
}

// BuildScriptData 构建脚本模板数据
func BuildScriptData(ctx context.Context, opts InstallOptions) ScriptData {
    cluster := opts.Cluster
    node := opts.Node
    component := opts.Component
    binary := component.Spec.Binary
    
    return ScriptData{
        // 集群信息
        ClusterName:      cluster.Name,
        ClusterNamespace: cluster.Namespace,
        APIServer:        cluster.Spec.ClusterConfig.Cluster.APIServer,
        APIServerPort:    cluster.Spec.ClusterConfig.Cluster.APIServerPort,
        ServiceCIDR:      cluster.Spec.ClusterConfig.Network.ServiceCIDR,
        PodCIDR:          cluster.Spec.ClusterConfig.Network.PodCIDR,
        DNSDomain:        cluster.Spec.ClusterConfig.Cluster.DNSDomain,
        ClusterDNS:       i.computeClusterDNS(cluster),
        
        // 节点信息
        NodeIP:          node.IP,
        NodeHostname:    node.Hostname,
        NodeRole:        node.Role,
        NodeArch:        node.Spec.Architecture,
        NodeOS:          node.Spec.OperatingSystem,
        NodeOSVersion:   node.Spec.OperatingSystemVersion,
        NodeKernelVersion: node.Status.KernelVersion,
        NodeCPUs:        node.Status.Capacity.CPU,
        NodeMemoryMB:    node.Status.Capacity.MemoryMB,
        NodeDiskGB:      node.Status.Capacity.DiskGB,
        
        // 版本信息
        ComponentVersion:       component.Spec.Version,
        ComponentPreviousVersion: i.getPreviousVersion(ctx, component, node),
        ClusterVersion:         cluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
        
        // 二进制制品
        Artifacts: i.buildArtifactData(ctx, binary, opts),
        
        // 镜像仓库
        ImageRegistry:   cluster.Spec.ClusterConfig.Registry.Endpoint,
        ImagePullSecret: cluster.Spec.ClusterConfig.Registry.PullSecret,
        
        // 安装路径
        InstallPath: binary.DefaultInstallPath,
        ConfigPath:  binary.DefaultConfigPath,
        LogPath:     binary.DefaultLogPath,
        DataPath:    binary.DefaultDataPath,
        BinPath:     binary.DefaultBinPath,
        
        // 操作类型
        Action:    string(opts.Action),
        IsUpgrade: opts.Action == BinaryActionUpgrade,
        IsInstall: opts.Action == BinaryActionInstall,
        
        // 自定义变量
        Variables: binary.Variables,
    }
}
```

### 3.4 installScript 条件渲染示例

```yaml
# 条件渲染示例
binary:
  installScript: |
    #!/bin/bash
    set -e
    
    # 根据操作系统选择包管理器
    {{if eq .nodeOS "centos"}}
    echo "Using yum package manager..."
    yum install -y {{.packageName}}
    {{else if eq .nodeOS "ubuntu"}}
    echo "Using apt package manager..."
    apt-get update && apt-get install -y {{.packageName}}
    {{else if eq .nodeOS "kylin"}}
    echo "Using kylin package manager..."
    kylin-install -y {{.packageName}}
    {{end}}
    
    # 升级时执行额外逻辑
    {{if .isUpgrade}}
    echo "Performing upgrade from {{.componentPreviousVersion}} to {{.componentVersion}}..."
    
    # 备份数据
    cp -r {{.dataPath}} {{.dataPath}}.bak.$(date +%Y%m%d%H%M%S)
    
    # 执行数据迁移
    {{if ge (semver .componentVersion) (semver "1.7.0")}}
    echo "Running data migration for version >= 1.7.0..."
    /usr/local/bin/containerd migrate-data
    {{end}}
    {{end}}
    
    # 根据架构选择不同配置
    {{if eq .nodeArch "arm64"}}
    echo "Applying ARM64 specific configuration..."
    cat >> {{.configPath}}/config.toml << EOF
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
      NoPivotRoot = false
    EOF
    {{end}}
    
    # 使用自定义变量
    echo "Setting log level to {{.Variables.logLevel | default "info"}}..."
    echo "Setting max concurrent downloads to {{.Variables.maxConcurrentDownloads | default "10"}}..."
    
    # 使用制品路径
    echo "Installing from {{.artifact.containerd.path}}..."
    tar -xzf "{{.artifact.containerd.path}}" -C {{.installPath}}
```

## 四、完整执行流程 (含 Helm + Binary)

```
用户创建 BKECluster (desiredVersion: v2.6.0)
  │
  ▼
ClusterVersionReconciler 解析 ReleaseImage v2.6.0
  │
  ├── install.components:
  │     ├── containerd/v1.7.18 (type: binary)
  │     ├── bkeagent/v2.6.0 (type: binary)
  │     ├── coredns/v1.11.1 (type: helm)
  │     ├── kube-proxy/v1.29.0 (type: helm)
  │     └── kubernetes/v1.29.0 (type: composite)
  │
  ├── 从 bke-manifests 加载 ComponentVersion
  │     ├── containerd/v1.7.18/component.yaml
  │     │     ├── binary.artifacts: [containerd, ctr, shim]
  │     │     ├── binary.configTemplates: [config.toml, service file]
  │     │     └── binary.installScript: (带完整模板变量)
  │     │
  │     ├── bkeagent/v2.6.0/component.yaml
  │     │     ├── binary.artifacts: [bkeagent]
  │     │     ├── binary.configTemplates: [bkeagent.conf, tls.crt, tls.key, kubeconfig]
  │     │     └── binary.installScript: (带完整模板变量)
  │     │
  │     └── coredns/v1.11.1/component.yaml
  │           ├── helm.chart.oci: registry/charts/coredns
  │           ├── helm.values: (带模板变量)
  │           └── helm.healthCheck: PodReady + EndpointReady
  │
  ├── 构建安装 DAG
  │     ┌─────────────────────────────────────────────────┐
  │     │ finalizer → ... → dryrun                        │
  │     │                    → agent (binary)             │
  │     │                    → env (binary)               │
  │     │                    → containerd (binary)        │
  │     │                    → apiobj → certs → lb        │
  │     │                    → master_init → master_join  │
  │     │                    → worker_join                │
  │     │                    → coredns (helm)             │
  │     │                    → kube-proxy (helm)          │
  │     │                    → addon → postprocess        │
  │     │                    → agent_switch               │
  │     └─────────────────────────────────────────────────┘
  │
  ├── DAG Scheduler 执行
  │     │
  │     ├── BinaryComponentExecutor 执行 containerd
  │     │     ├── TemplateRenderer 渲染 installScript
  │     │     │     ├── 替换 {{nodeArch}} → amd64
  │     │     │     ├── 替换 {{nodeOS}} → centos
  │     │     │     ├── 替换 {{componentVersion}} → v1.7.18
  │     │     │     ├── 替换 {{artifact.containerd.path}} → /tmp/...
  │     │     │     └── 渲染条件块 {{if eq .nodeOS "centos"}}...{{end}}
  │     │     │
  │     │     ├── ConfigRenderer 渲染 configTemplates
  │     │     │     ├── bkeagent.conf → 渲染 Go template
  │     │     │     ├── tls.crt → 从 Secret 获取
  │     │     │     └── kubeconfig → 动态生成
  │     │     │
  │     │     └── SSH 上传制品 + 配置 + 执行脚本
  │     │
  │     └── HelmComponentExecutor 执行 coredns
  │           ├── 从 OCI Registry 拉取 Chart
  │           ├── 渲染 Values (替换 {{clusterName}} 等变量)
  │           ├── helm install coredns --namespace kube-system
  │           └── 健康检查: PodReady + EndpointReady
  │
  └── 安装完成 → ClusterStatus = Ready
```

## 五、设计收益

| 维度 | 当前 | 重构后 | 提升 |
|------|------|--------|------|
| **组件类型支持** | inline + manifest | inline + manifest + binary + helm | 完整覆盖 |
| **配置管理** | 硬编码在脚本中 | configTemplates 声明式 | 可维护性↑ |
| **模板变量** | 仅 {{arch}} | 8类50+变量 | 灵活性↑ |
| **条件渲染** | 无 | Go template 完整支持 | 表达能力↑ |
| **Helm 支持** | 无 | OCI/HTTP/本地 Chart | 生态兼容↑ |
| **新增组件** | 修改代码 | 添加 YAML | 零代码侵入 |
