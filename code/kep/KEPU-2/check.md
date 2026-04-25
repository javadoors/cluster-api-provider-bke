# ActionEngine 组件状态判断标准规范

## 1. 核心设计原则

| 原则 | 说明 |
|------|------|
| **声明式定义** | 组件的安装/升级完成条件完全由 `ComponentVersion.spec.versions[].healthCheck` 声明 |
| **分层验证** | 包含三个层级：版本存在性验证 → 功能可用性验证 → 健康状态验证 |
| **幂等性** | 健康检查可重复执行，不影响组件运行状态 |
| **可观测性** | 所有检查结果详细记录到 Status，便于排错 |

## 2. 标准规范定义

### 2.1 健康检查标准结构

```yaml
healthCheck:
  # 第一层：版本存在性验证（必须）
  versionCheck:
    enabled: true
    steps:
      - name: check-binary-version
        type: Script
        script: |
          # 示例：检查 containerd 版本
          containerd --version 2>&1 | grep -q "{{.Version}}"
        expectedOutput: ""  # 空表示只要脚本退出码为0即成功
        timeout: 30s
        interval: 5s

  # 第二层：功能可用性验证（可选）
  functionalCheck:
    enabled: true
    steps:
      - name: check-service-running
        type: Script
        script: |
          systemctl is-active containerd
        expectedOutput: "active"
        timeout: 10s
        interval: 2s
      - name: check-api-responsiveness
        type: Kubectl
        kubectl:
          operation: Wait
          resource: pods
          namespace: kube-system
          fieldSelector: status.phase=Running
          timeout: 60s

  # 第三层：健康状态验证（可选）
  healthCheck:
    enabled: true
    steps:
      - name: check-metrics-available
        type: Script
        script: |
          curl -s http://127.0.0.1:10257/healthz | grep -q "ok"
        timeout: 10s
        interval: 5s
```

### 2.2 健康检查步骤标准类型

| 类型 | 适用场景 | 示例 |
|------|----------|------|
| **Script** | 节点级组件检查 | 检查二进制版本、服务状态、本地端口 |
| **Kubectl** | 集群级组件检查 | 等待 Pod 就绪、检查 API 资源状态 |
| **Manifest** | 资源声明验证 | 验证 Deployment/Service 存在且状态正常 |

### 2.3 健康检查结果标准判定

```go
type HealthCheckResult struct {
    // 整体结果
    Passed  bool              `json:"passed"`
    Message string            `json:"message"`
    
    // 逐层结果
    VersionCheck    *LayerResult `json:"versionCheck,omitempty"`
    FunctionalCheck *LayerResult `json:"functionalCheck,omitempty"`
    HealthCheck     *LayerResult `json:"healthCheck,omitempty"`
    
    // 执行信息
    StartedAt   metav1.Time `json:"startedAt"`
    CompletedAt metav1.Time `json:"completedAt"`
}

type LayerResult struct {
    Passed bool             `json:"passed"`
    Steps  []StepResult     `json:"steps"`
}

type StepResult struct {
    Name       string        `json:"name"`
    Passed     bool          `json:"passed"`
    Output     string        `json:"output,omitempty"`
    Error      string        `json:"error,omitempty"`
    Duration   metav1.Duration `json:"duration,omitempty"`
}
```

## 3. 标准流程定义

### 3.1 安装完成判断流程

```
┌─────────────────────────────────────────────────────────────────┐
│                    安装完成判断流程                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. 等待 InstallAction 所有步骤执行完成                          │
│     └── 如果任何步骤失败 → 标记 InstallFailed，中止               │
│                                                                 │
│  2. 执行 VersionCheck（第一层）                                  │
│     ├── 验证组件二进制/版本标识存在                              │
│     ├── 验证版本号匹配 spec.desiredVersion                       │
│     └── 失败 → 标记 InstallFailed                                │
│                                                                 │
│  3. 执行 FunctionalCheck（第二层，如启用）                       │
│     ├── 验证服务/进程运行状态                                    │
│     ├── 验证基本功能可用（如API响应）                            │
│     └── 失败 → 标记 Degraded（可选降级）                         │
│                                                                 │
│  4. 执行 HealthCheck（第三层，如启用）                           │
│     ├── 验证组件健康指标（如metrics、healthz）                   │
│     └── 失败 → 标记 Degraded（可选降级）                         │
│                                                                 │
│  5. 所有检查通过 → 更新状态                                      │
│     ├── status.phase = Healthy                                   │
│     ├── status.installedVersion = spec.desiredVersion            │
│     └── status.lastOperation = {Type: Install, Result: Success}  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 升级完成判断流程

```
┌─────────────────────────────────────────────────────────────────┐
│                    升级完成判断流程                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. 等待 UninstallOldAction（旧版本卸载）完成                     │
│     └── 失败 → 标记 UpgradeFailed                                │
│                                                                 │
│  2. 等待 UpgradeAction 所有步骤执行完成                          │
│     └── 失败 → 标记 UpgradeFailed                                │
│                                                                 │
│  3. 执行 VersionCheck（第一层）                                  │
│     ├── 验证组件版本已更新为 spec.desiredVersion                 │
│     ├── 验证旧版本标识已清除                                      │
│     └── 失败 → 标记 UpgradeFailed                                │
│                                                                 │
│  4. 执行 FunctionalCheck（第二层）                               │
│     ├── 验证服务/进程正常运行                                    │
│     ├── 验证功能未退化（如API兼容）                              │
│     └── 失败 → 标记 Degraded（可选降级）                         │
│                                                                 │
│  5. 执行 HealthCheck（第三层）                                   │
│     ├── 验证组件健康指标正常                                     │
│     ├── 验证性能指标在可接受范围                                 │
│     └── 失败 → 标记 Degraded（可选降级）                         │
│                                                                 │
│  6. 所有检查通过 → 更新状态                                      │
│     ├── status.phase = Healthy                                   │
│     ├── status.installedVersion = spec.desiredVersion            │
│     └── status.lastOperation = {Type: Upgrade, Result: Success}  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## 4. 标准状态机定义

### 4.1 组件生命周期状态机

```
                    ┌───────────┐
                    │  Pending  │
                    └─────┬─────┘
                          │
                          ▼
              ┌─────────────────────┐
              │    Installing       │
              │  (执行 InstallAction)│
              └──────────┬──────────┘
                         │
           ┌─────────────┼─────────────┐
           │             │             │
           ▼             ▼             ▼
    ┌───────────┐  ┌─────────┐  ┌───────────┐
    │Installed  │  │Degraded │  │InstallFailed│
    └─────┬─────┘  └────┬────┘  └───────────┘
          │             │
          │             │
          ▼             ▼
    ┌───────────┐  ┌─────────┐
    │  Healthy  │◄─┤Rolling  │
    └─────┬─────┘  │ Back    │
          │        └────┬────┘
          │             │
          ▼             │
    ┌───────────┐       │
    │ Upgrading │◄──────┘
    └─────┬─────┘
          │
    ┌─────┴─────┐
    │           │
    ▼           ▼
┌─────────┐ ┌──────────────┐
│ Healthy │ │UpgradeFailed │
└─────────┘ └──────────────┘
```

### 4.2 状态转换标准规则

| 当前状态 | 触发条件 | 目标状态 |
|---------|---------|---------|
| Pending | desiredVersion != installedVersion | Installing |
| Installing | InstallAction 完成 + 所有健康检查通过 | Healthy |
| Installing | InstallAction 失败 | InstallFailed |
| Installing | InstallAction 完成但部分健康检查失败 | Degraded |
| Healthy | desiredVersion 变化 | Upgrading |
| Upgrading | UpgradeAction 完成 + 所有健康检查通过 | Healthy |
| Upgrading | UpgradeAction 失败 | UpgradeFailed |
| Upgrading | UpgradeAction 完成但部分健康检查失败 | Degraded |
| Degraded | 健康检查全部通过 | Healthy |
| Healthy/Degraded | Periodic health check 失败 | Degraded |

## 5. 标准实现示例

### 5.1 Containerd 组件健康检查定义

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.2
spec:
  componentName: containerd
  scope: Node
  versions:
    - version: v1.7.2
      installAction:
        steps:
          - name: install-containerd
            type: Script
            script: |
              # 安装 containerd 二进制
              tar -xzf /tmp/containerd-{{.Version}}.tar.gz -C /usr/local/bin/
              systemctl daemon-reload
              systemctl enable containerd
              systemctl start containerd
      healthCheck:
        # 第一层：版本验证
        versionCheck:
          enabled: true
          steps:
            - name: check-containerd-version
              type: Script
              script: |
                containerd --version 2>&1 | grep -q "v1.7.2"
              timeout: 30s
              interval: 5s
        
        # 第二层：功能验证
        functionalCheck:
          enabled: true
          steps:
            - name: check-service-active
              type: Script
              script: |
                systemctl is-active containerd
              expectedOutput: "active"
              timeout: 10s
              interval: 2s
            - name: check-socket-accessible
              type: Script
              script: |
                ctr version 2>&1 >/dev/null
              timeout: 10s
              interval: 2s
        
        # 第三层：健康验证
        healthCheck:
          enabled: true
          steps:
            - name: check-metrics-available
              type: Script
              script: |
                curl -s http://127.0.0.1:10257/healthz | grep -q "ok"
              timeout: 10s
              interval: 5s
```

### 5.2 Kubernetes 组件健康检查定义

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: kubernetes-v1.29.0
spec:
  componentName: kubernetes
  scope: Node
  versions:
    - version: v1.29.0
      healthCheck:
        versionCheck:
          enabled: true
          steps:
            - name: check-kubelet-version
              type: Script
              script: |
                kubelet --version 2>&1 | grep -q "v1.29.0"
              timeout: 30s
              interval: 5s
        
        functionalCheck:
          enabled: true
          steps:
            - name: check-kubelet-service
              type: Script
              script: |
                systemctl is-active kubelet
              expectedOutput: "active"
              timeout: 10s
              interval: 2s
            - name: check-node-ready
              type: Kubectl
              kubectl:
                operation: Wait
                resource: nodes
                fieldSelector: metadata.name={{.NodeHostname}},status.conditions[?(@.type=="Ready")].status=True
                timeout: 120s
        
        healthCheck:
          enabled: true
          steps:
            - name: check-kubelet-healthz
              type: Script
              script: |
                curl -s http://127.0.0.1:10248/healthz | grep -q "ok"
              timeout: 10s
              interval: 5s
```

## 6. ActionEngine 标准接口

```go
// ActionEngine 接口定义
type ActionEngine interface {
    // ExecuteInstall 执行安装并验证完成
    ExecuteInstall(ctx context.Context, cv *ComponentVersion, binding *ComponentVersionBinding) (*HealthCheckResult, error)
    
    // ExecuteUpgrade 执行升级并验证完成
    ExecuteUpgrade(ctx context.Context, cv *ComponentVersion, binding *ComponentVersionBinding, oldVersion string) (*HealthCheckResult, error)
    
    // ExecuteHealthCheck 执行健康检查
    ExecuteHealthCheck(ctx context.Context, cv *ComponentVersion, binding *ComponentVersionBinding) (*HealthCheckResult, error)
    
    // IsInstalled 判断组件是否已安装（仅版本检查）
    IsInstalled(ctx context.Context, cv *ComponentVersion, binding *ComponentVersionBinding) (bool, error)
    
    // IsHealthy 判断组件是否健康（完整检查）
    IsHealthy(ctx context.Context, cv *ComponentVersion, binding *ComponentVersionBinding) (bool, error)
}

// HealthChecker 健康检查执行器
type HealthChecker interface {
    // CheckVersion 执行版本检查
    CheckVersion(ctx context.Context, check *HealthCheckSpec, ctx *TemplateContext) (*LayerResult, error)
    
    // CheckFunctional 执行功能检查
    CheckFunctional(ctx context.Context, check *HealthCheckSpec, ctx *TemplateContext) (*LayerResult, error)
    
    // CheckHealth 执行健康检查
    CheckHealth(ctx context.Context, check *HealthCheckSpec, ctx *TemplateContext) (*LayerResult, error)
}
```

## 7. 标准重试与超时策略

| 层级 | 重试策略 | 超时策略 | 失败处理 |
|------|---------|---------|---------|
| **VersionCheck** | 3次，间隔5s | 单次步骤最长30s | 标记为 Failed，中止流程 |
| **FunctionalCheck** | 5次，间隔10s | 单次步骤最长60s | 标记为 Degraded，继续流程 |
| **HealthCheck** | 3次，间隔15s | 单次步骤最长30s | 标记为 Degraded，继续流程 |## 8. 总结

本标准规范的核心要点：
1. **三层验证模型**：版本验证 → 功能验证 → 健康验证，逐级递进
2. **声明式定义**：所有检查逻辑在 ComponentVersion 中定义，无需代码
3. **标准化状态机**：清晰的状态转换规则，可预测的行为
4. **可观测性**：详细记录检查结果，便于排错和审计
5. **容错机制**：区分致命错误和降级错误，灵活处理不同场景

此规范可直接集成到 `KEPU-2` 的设计中，作为 ActionEngine 组件状态判断的标准实现方案。
        
