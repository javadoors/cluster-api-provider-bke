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
