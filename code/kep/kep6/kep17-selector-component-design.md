# KEP-17: Selector 组件类型设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-17 |
| **标题** | Selector 组件类型：基于 Condition 的互斥选择器设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **依赖** | KEP-5 声明式升级框架、KEP-6 声明式集群版本升级方案、KEP-13 二进制组件改造 |
| **来源** | 从 `kep6-detailed-design.md` 抽离 selector 组件相关内容 |

---

## 目录

1. [设计动机](#1-设计动机)
2. [设计目标与约束](#2-设计目标与约束)
3. [Selector 类型定义](#3-selector-类型定义)
   - 3.1 [SubComponent 与 Condition 字段](#31-subcomponent-与-condition-字段)
   - 3.2 [selector 与其他类型的 subComponents 语义对比](#32-selector-与其他类型的-subcomponents-语义对比)
   - 3.3 [ComponentVersion YAML 示例](#33-componentversion-yaml-示例)
4. [DAG 展开机制](#4-dag-展开机制)
   - 4.1 [Selector DAG 构建流程](#41-selector-dag-构建流程)
   - 4.2 [expandSelectorComponents 实现](#42-expandselectorcomponents-实现)
   - 4.3 [evaluateCondition 通用条件评估](#43-evaluatecondition-通用条件评估)
5. [Selector 依赖处理](#5-selector-依赖处理)
   - 5.1 [问题分析](#51-问题分析)
   - 5.2 [设计原则](#52-设计原则)
   - 5.3 [数据结构](#53-数据结构)
   - 5.4 [实现方案](#54-实现方案)
   - 5.5 [完整流程示例](#55-完整流程示例)
   - 5.6 [边界情况处理](#56-边界情况处理)
   - 5.7 [优势分析](#57-优势分析)
6. [容器运行时互斥选择](#6-容器运行时互斥选择)
7. [与现有代码的对应关系](#7-与现有代码的对应关系)
8. [工作量评估](#8-工作量评估)
9. [风险与缓解](#9-风险与缓解)

---

## 1. 设计动机

在集群管理中，存在"从多个候选组件中选择一个"的场景。典型用例是容器运行时——一个集群只能安装一种容器运行时（containerd 或 docker），选择由 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 决定。

当前代码中，容器运行时选择通过 `init.go:789-797` 的 `downloadContainerRuntime` switch-case 硬编码实现，新增运行时类型需修改控制器代码。引入 `selector` 类型后，选择逻辑声明式化在 ComponentVersion YAML 中，通过 Go Template condition 表达式在 DAG 构建期评估，无需修改代码。

## 2. 设计目标与约束

### 2.1 设计目标

1. **声明式互斥选择**：通过 `condition` 字段声明选择条件，DAG 构建期自动评估
2. **selector 自身不产生 DAG 节点**：selector 仅作为容器和选择器，展开后的子组件各自产生 DAG 节点
3. **依赖继承与展开**：selector 级别的依赖被子组件继承，外部组件对 selector 的依赖展开为对全部子组件的依赖
4. **向后兼容**：`subComponents` 字段在不同 `type` 下有不同语义，由 `type` 字段天然消歧

### 2.2 设计约束

| 约束 | 说明 |
|------|------|
| **selector 自身不产生 DAG 节点** | selector 仅作为容器，DAG 中只有展开后的子组件节点 |
| **不定义专属 Spec 结构体** | 无 `SelectorSpec`，复用现有的 `SubComponent`（含 `Condition` 字段）和 `UpgradeStrategySpec` |
| **condition 全部为 false 时报错** | selector 展开为空表示配置错误 |
| **condition 评估使用 TemplateRenderer** | 可访问 TemplateContext 中的所有变量和自定义函数 |

## 3. Selector 类型定义

### 3.1 SubComponent 与 Condition 字段

selector 类型复用现有的 `SubComponent` 结构体，新增 `Condition` 字段：

```go
// api/v1alpha1/componentversion_types.go

// SubComponent 定义子组件引用 ✅复用现有, 🆕新增 Condition 字段
type SubComponent struct {
    // 子组件名称
    Name string `json:"name"`
    
    // 子组件版本
    Version string `json:"version"`

    // 🆕生成条件 (Go Template 表达式)
    // 仅 type=selector 时使用: DAG 构建期评估, condition 为真的子组件纳入 DAG
    // type=yaml 等其他类型时忽略此字段 (全包含语义不变)
    // 示例: '{{.ContainerRuntimeCRI == "containerd"}}'
    Condition string `json:"condition,omitempty"`
}

// ComponentType 新增 selector 值
const (
    ComponentTypeSelector ComponentType = "selector" // 🆕互斥选择器: 从 subComponents 中按 condition 选择一个
)
```

### 3.2 selector 与其他类型的 subComponents 语义对比

**设计思路 — 互斥选择器，按 type 区分 subComponents 语义**：

`selector` 类型用于表达"从多个候选组件中选择一个"的场景。典型用例：容器运行时——一个集群只能安装一种容器运行时（containerd 或 docker），选择由 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 决定。

`subComponents` 字段在不同 `type` 下有不同语义，由 `type` 字段天然消歧：

| 维度 | type=yaml（组合） | type=selector（互斥选择） |
|------|------|------|
| subComponents 语义 | 全包含——所有子组件都安装 | 条件选一——评估 condition，为真的纳入 DAG |
| Condition 字段 | 忽略（不评估） | 评估后选一 |
| DAG 节点 | 父组件 + 所有子组件各自产生 DAG 节点 | 仅 condition 为真的子组件产生 DAG 节点 |
| selector 自身 | 不适用 | 不产生 DAG 节点（纯选择器，无自身安装逻辑） |
| 典型场景 | openfuyao-core 包含 kubernetes-master + kubernetes-worker | container-runtime 选 containerd 或 docker |

selector 类型不定义专属 Spec 结构体（无 `SelectorSpec`），仅复用现有的 `SubComponent`（含 `Condition` 字段）和 `UpgradeStrategySpec`。

### 3.3 ComponentVersion YAML 示例

**container-runtime ComponentVersion YAML（selector 类型）**：
```yaml
# bke-manifests/container-runtime/v1.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: container-runtime-v1.0.0
spec:
  name: container-runtime
  type: selector
  version: v1.0.0
  subComponents:
    # containerd 运行时 (CRI=containerd 时选择)
    - name: containerd
      version: v1.7.18
      condition: '{{.ContainerRuntimeCRI == "containerd"}}'

    # docker 运行时 (CRI=docker 时选择)
    - name: docker
      version: v26.0.0
      condition: '{{.ContainerRuntimeCRI == "docker"}}'

    # cri-dockerd (CRI=docker 时选择, K8s >=1.24 必需)
    - name: cri-dockerd
      version: v0.3.9
      condition: '{{.ContainerRuntimeCRI == "docker"}}'

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```
> **ReleaseImage 引用方式**：ReleaseImage 只引用 `container-runtime/v1.0.0`，DAG 构建期自动展开为 containerd 或 docker + cri-dockerd。无需在 ReleaseImage 中分别声明。

**docker ComponentVersion YAML（binary 类型）**：

> 完整的 Docker ComponentVersion YAML 定义见 **12.3.4.2 Docker ComponentVersion YAML 完整定义**。

简化示例：
```yaml
# bke-manifests/docker/v26.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: docker-v26.0.0
spec:
  name: docker
  type: binary
  version: v26.0.0

  binary:
    variables:
      cgroupDriver: "systemd"
      dataRoot: "/var/lib/docker"
      lowLevelRuntime: "runc"

    # Docker 通过包管理器安装 (非二进制下载), 无 artifacts
    installScript: |
      #!/bin/bash
      # yum/apt 安装 docker-ce

    configTemplates:
      - name: daemon.json
        path: "/etc/docker/daemon.json"
        content: |
          {
            "exec-opts": ["native.cgroupdriver={{.Variables.cgroupDriver}}"],
            "data-root": "{{.Variables.dataRoot}}"
          }

    healthCheck:
      enabled: true
      script: |
        systemctl is-active docker

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
```
> **Docker 与 containerd 的关键差异**：Docker 无 `hosts.toml`（镜像仓库配置在 `daemon.json` 的 `registry-mirrors` 中）；Docker 通过包管理器安装（无 `artifacts` 二进制下载）；Docker 需要 `cri-dockerd` 作为 CRI 适配层（K8s ≥1.24）。

**cri-dockerd ComponentVersion YAML（binary 类型）**：

> 完整的 cri-dockerd ComponentVersion YAML 定义见 **12.3.4.3 cri-dockerd ComponentVersion YAML 完整定义**。

简化示例：
```yaml
# bke-manifests/cri-dockerd/v0.3.9/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: cri-dockerd-v0.3.9
spec:
  name: cri-dockerd
  type: binary
  version: v0.3.9

  binary:
    variables:
      sandboxImage: "{{imageRegistry}}/pause:3.9"

    artifacts:
      - name: cri-dockerd
        url: "{{imageRegistry}}/cri-dockerd/{{version}}/cri-dockerd-{{version}}-{{arch}}"
        installPath: "/usr/bin"

    configTemplates:
      - name: cri-dockerd.service
        path: "/etc/systemd/system/cri-dockerd.service"
        content: |
          [Service]
          ExecStart=/usr/bin/cri-dockerd --pod-infra-container-image {{.Variables.sandboxImage}}

      - name: cri-dockerd.socket
        path: "/etc/systemd/system/cri-dockerd.socket"
        content: |
          [Socket]
          ListenStream=/var/run/cri-dockerd.sock

    installScript: |
      #!/bin/bash
      install -m 0755 {{artifact.cri-dockerd.path}} /usr/bin/cri-dockerd

    healthCheck:
      enabled: true
      script: |
        systemctl is-active cri-dockerd

  dependencies:
    - name: docker
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
```

## 4. DAG 展开机制

### 4.1 Selector DAG 构建流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                      Selector DAG 构建流程                                       │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌──────────────────────────────┐
    │  BuildDAGFromBundle          │
    │  遍历 ReleaseImage.components│
    └──────────────┬───────────────┘
                   │
                   │ 遇到 container-runtime/v1.0.0
                   ▼
    ┌──────────────────────────────┐
    │  加载 ComponentVersion       │
    │  cv.Spec.Type == "selector"  │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  读取 ContainerRuntimeCRI    │
    │  从 ExecutionContext         │
    │  .TemplateContext.Variables  │
    │  ["ContainerRuntimeCRI"]     │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  遍历 cv.Spec.SubComponents  │
    │  评估每个 sub.Condition       │
    └──────────────┬───────────────┘
                   │
       ┌───────────┼───────────┐
       │           │           │
       ▼           ▼           ▼
  ┌──────────┐ ┌─────────┐ ┌──────────┐
  │containerd│ │ docker  │ │cri-docker│
  │condition │ │condition│ │condition │
  │= true?   │ │= true?  │ │= true?   │
  └────┬─────┘ └────┬────┘ └─────┬────┘
       │           │             │
  CRI=containerd CRI=docker   CRI=docker
       │           │             │
       ▼           ▼             ▼
   纳入 DAG     纳入 DAG      纳入 DAG
   (binary)    (binary)      (binary)
      │           │             │
      │           └──────┬──────┘
      │                  │ 依赖关系
      │                  ▼
      │           docker → cri-dockerd
      │           (DAG 依赖边)
      │
      ▼
  selector 自身不产生 DAG 节点
  (纯选择器, 无安装逻辑)
```

### 4.2 expandSelectorComponents 实现

```go
// expandSelectorComponents 在 DAG 构建期展开 selector 类型的 ComponentVersion
// selector 自身不产生 DAG 节点；遍历 subComponents，评估 condition，为真的子组件创建 DAG 节点
// condition 通过 TemplateRenderer 通用评估，可访问 TemplateContext 中的所有变量
func (s *Scheduler) expandSelectorComponents(
    ctx context.Context,
    execCtx *ExecutionContext,
    cv *configv1alpha1.ComponentVersion,
) ([]topology.ComponentNode, error) {
    if cv.Spec.Type != configv1alpha1.ComponentTypeSelector {
        return nil, nil // 非 selector 类型, 不展开
    }

    var nodes []topology.ComponentNode
    for _, sub := range cv.Spec.SubComponents {
        if sub.Condition == "" {
            // 无 condition = 始终纳入 (兼容组合语义)
            nodes = append(nodes, topology.ComponentNode{
                Name:    sub.Name,
                Version: sub.Version,
            })
            continue
        }
        // 评估 condition: 使用 TemplateRenderer 通用评估 Go Template 表达式
        // condition 可访问 TemplateContext 中的所有变量和函数
        // 示例: '{{.ContainerRuntimeCRI == "containerd"}}'
        //       '{{.isOffline}}'
        //       '{{eq .Variables.logLevel "debug"}}'
        //       '{{and (eq .ContainerRuntimeCRI "containerd") (ge .KubernetesVersion "1.24")}}'
        matched, err := s.evaluateCondition(sub.Condition, execCtx.TemplateContext)
        if err != nil {
            return nil, fmt.Errorf("failed to evaluate condition for %s: %w", sub.Name, err)
        }
        if matched {
            nodes = append(nodes, topology.ComponentNode{
                Name:    sub.Name,
                Version: sub.Version,
            })
        }
    }
    return nodes, nil
}

// evaluateCondition 通用评估 selector condition
// 使用 TemplateRenderer 渲染 condition Go Template，渲染结果为 "true" 时返回 true
// 可访问 TemplateContext 中的所有变量（集群信息、节点信息、版本信息、自定义变量等）
// 可使用 TemplateRenderer 注册的所有自定义函数（eq/ne/gt/ge/lt/le/upper/lower/joinPath 等）
func (s *Scheduler) evaluateCondition(condition string, tmplCtx manifest.TemplateContext) (bool, error) {
    if s.templateRenderer == nil {
        return false, fmt.Errorf("templateRenderer is not initialized")
    }
    // 渲染 condition 为字符串
    result, err := s.templateRenderer.RenderScript(condition, tmplCtx)
    if err != nil {
        return false, fmt.Errorf("failed to render condition template: %w", err)
    }
    // 渲染结果为 "true" 时返回 true（trimSpace 去除首尾空白）
    return strings.TrimSpace(result) == "true", nil
}
```

### 4.3 evaluateCondition 通用条件评估

`evaluateCondition` 使用 TemplateRenderer 渲染 condition Go Template 表达式，渲染结果为 `"true"` 时返回 `true`。

**支持的 condition 表达式示例**：

| 表达式 | 说明 |
|--------|------|
| `{{.ContainerRuntimeCRI == "containerd"}}` | 等值匹配 |
| `{{.isOffline}}` | 布尔变量（非空/"false"时为 true） |
| `{{eq .Variables.logLevel "debug"}}` | 使用 eq 函数 |
| `{{and (eq .ContainerRuntimeCRI "containerd") (ge .KubernetesVersion "1.24")}}` | 复合条件 |

condition 可访问 TemplateContext 中的所有变量（集群信息、节点信息、版本信息、自定义变量等），可使用 TemplateRenderer 注册的所有自定义函数（eq/ne/gt/ge/lt/le/upper/lower/joinPath 等）。

## 5. Selector 依赖处理

当组件类型为 selector 时，依赖解析面临两个问题：

### 5.1 问题分析

**问题 1：Selector 的依赖需要传递给子组件**

```yaml
# container-runtime selector
spec:
  type: selector
  dependencies:
    - name: bkeagent  # selector 依赖 bkeagent
  subComponents:
    - name: containerd
      condition: '{{.ContainerRuntimeCRI == "containerd"}}'
    - name: docker
      condition: '{{.ContainerRuntimeCRI == "docker"}}'
```

展开后，`containerd` 或 `docker` 应该继承 `bkeagent` 依赖，但当前实现不会传递。

**问题 2：其他组件对 selector 的依赖需要展开**

```yaml
# kubernetes-master 依赖 container-runtime
spec:
  dependencies:
    - name: container-runtime  # 这是 selector
```

展开后，`kubernetes-master` 应该依赖实际的子组件（`containerd` 或 `docker`），但当前实现无法自动转换。

### 5.2 设计原则

**原则 1：依赖定义在具体组件中**

不在 selector 的 `subComponents` 中定义依赖，而是在具体组件的 `spec.dependencies` 中定义：

```yaml
# containerd/v1.7.18/component.yaml
spec:
  name: containerd
  type: binary
  dependencies:
    - name: bkeagent

# docker/v26.0.0/component.yaml
spec:
  name: docker
  type: binary
  dependencies:
    - name: bkeagent

# cri-dockerd/v0.3.9/component.yaml
spec:
  name: cri-dockerd
  type: binary
  dependencies:
    - name: docker      # cri-dockerd 依赖 docker
    - name: bkeagent
```

**优势**：
- ✅ 不需要扩展 `SubComponent` 结构体
- ✅ 依赖关系在具体组件中定义，更清晰
- ✅ 符合现有依赖解析机制
- ✅ 每个组件独立维护自己的依赖

**原则 2：对 selector 的依赖展开为对所有子组件的依赖（AND 语义）**

当 selector 展开为多个子组件时，将对 selector 的依赖转换为对所有子组件的依赖，由 DAG 拓扑排序自动处理执行顺序。

**示例**：
```
CRI=docker
container-runtime → [docker, cri-dockerd]
kubernetes-master 依赖 container-runtime → 转换为依赖 [docker, cri-dockerd]

DAG 执行顺序：
1. bkeagent
2. docker
3. cri-dockerd (等待 docker 完成)
4. kubernetes-master (等待 docker + cri-dockerd 完成)
```

**实际效果**：虽然 `kubernetes-master` 同时依赖 `docker` 和 `cri-dockerd`，但由于 `cri-dockerd` 已经依赖 `docker`，所以 `docker` 的依赖是冗余的，最终效果等同于只依赖 `cri-dockerd`。

### 5.3 数据结构

```go
// SelectorMapping 记录 selector 展开后的子组件映射
type SelectorMapping struct {
    SelectorName  string   // selector 名称
    ExpandedNames []string // 展开后的子组件名称列表
}
```

### 5.4 实现方案

**修改 expandSelectorComponents**：

```go
// expandSelectorComponents 在 DAG 构建期展开 selector 类型的 ComponentVersion
// selector 自身不产生 DAG 节点；遍历 subComponents，评估 condition，为真的子组件创建 DAG 节点
// 同时合并 selector 的依赖和子组件自身的依赖
func (s *Scheduler) expandSelectorComponents(
    ctx context.Context,
    execCtx *ExecutionContext,
    cv *configv1alpha1.ComponentVersion,
) ([]topology.ComponentNode, error) {
    if cv.Spec.Type != configv1alpha1.ComponentTypeSelector {
        return nil, nil // 非 selector 类型, 不展开
    }

    var nodes []topology.ComponentNode
    for _, sub := range cv.Spec.SubComponents {
        if sub.Condition == "" {
            // 无 condition = 始终纳入 (兼容组合语义)
            node, err := s.buildSubComponentNode(ctx, cv, sub)
            if err != nil {
                return nil, err
            }
            nodes = append(nodes, node)
            continue
        }
        
        // 评估 condition
        matched, err := s.evaluateCondition(sub.Condition, execCtx.TemplateContext)
        if err != nil {
            return nil, fmt.Errorf("failed to evaluate condition for %s: %w", sub.Name, err)
        }
        
        if matched {
            node, err := s.buildSubComponentNode(ctx, cv, sub)
            if err != nil {
                return nil, err
            }
            nodes = append(nodes, node)
        }
    }
    
    return nodes, nil
}

// buildSubComponentNode 构建子组件节点，合并 selector 和子组件的依赖
func (s *Scheduler) buildSubComponentNode(
    ctx context.Context,
    selectorCV *configv1alpha1.ComponentVersion,
    sub configv1alpha1.SubComponent,
) (topology.ComponentNode, error) {
    // 加载子组件的 ComponentVersion
    subCV, err := s.cvStore.GetComponentVersion(ctx, sub.Name, sub.Version)
    if err != nil {
        return topology.ComponentNode{}, fmt.Errorf(
            "failed to load sub-component %s: %w", sub.Name, err)
    }
    
    // 合并依赖：selector 的依赖 + 子组件自己的依赖
    mergedDeps := mergeDependencies(selectorCV.Spec.Dependencies, subCV.Spec.Dependencies)
    
    return topology.ComponentNode{
        Name:         sub.Name,
        Version:      sub.Version,
        Dependencies: mergedDeps,
    }, nil
}

// mergeDependencies 合并 selector 和子组件的依赖（去重）
func mergeDependencies(
    selectorDeps, subDeps []configv1alpha1.Dependency,
) []configv1alpha1.Dependency {
    depMap := make(map[string]configv1alpha1.Dependency)
    
    // 先添加 selector 的依赖
    for _, dep := range selectorDeps {
        depMap[dep.Name] = dep
    }
    
    // 再添加子组件的依赖（覆盖同名依赖）
    for _, dep := range subDeps {
        depMap[dep.Name] = dep
    }
    
    // 转换为切片
    result := make([]configv1alpha1.Dependency, 0, len(depMap))
    for _, dep := range depMap {
        result = append(result, dep)
    }
    return result
}
```

**修改依赖解析逻辑**：

```go
// expandSelectorDependencies 将对 selector 的依赖展开为对子组件的依赖
func expandSelectorDependencies(
    deps []string,
    selectorMappings []SelectorMapping,
) []string {
    var result []string
    seen := make(map[string]bool)
    
    for _, dep := range deps {
        // 查找是否为 selector
        var expanded []string
        for _, mapping := range selectorMappings {
            if mapping.SelectorName == dep {
                expanded = mapping.ExpandedNames
                break
            }
        }
        
        if len(expanded) > 0 {
            // 是 selector，展开为所有子组件
            for _, subName := range expanded {
                if !seen[subName] {
                    result = append(result, subName)
                    seen[subName] = true
                }
            }
        } else {
            // 不是 selector，保持原样
            if !seen[dep] {
                result = append(result, dep)
                seen[dep] = true
            }
        }
    }
    
    return result
}
```

**修改 DAG 构建流程**：

```go
func BuildUpgradeDAG(
    components []cvv1alpha1.ReleaseImageUpgradeComponent,
    resolve topology.DependencyResolver,
    selectorMappings []SelectorMapping,
) (*topology.UpgradeDAG, error) {
    dag := NewUpgradeDAG()
    
    // 阶段 1：添加所有组件节点（包括展开后的子组件）
    for _, comp := range components {
        node := &ComponentNode{
            Name:    comp.Name,
            Version: comp.Version,
        }
        if err := dag.AddNode(node); err != nil {
            return nil, err
        }
    }
    
    // 阶段 2：解析依赖并添加边
    for _, comp := range components {
        // 从 ComponentVersion 读取依赖
        deps, err := resolve(comp.Name, comp.Version)
        if err != nil {
            return nil, err
        }
        
        // 展开 selector 依赖
        expandedDeps := expandSelectorDependencies(deps, selectorMappings)
        
        // 添加依赖边
        for _, dep := range expandedDeps {
            if dep == comp.Name {
                continue // 跳过自依赖
            }
            if _, ok := dag.GetNode(dep); !ok {
                return nil, fmt.Errorf(
                    "component %q depends on %q which is not in the DAG", 
                    comp.Name, dep)
            }
            if err := dag.AddDependency(dep, comp.Name); err != nil {
                return nil, err
            }
        }
    }
    
    // 阶段 3：验证 DAG（检测循环依赖）
    if _, err := dag.TopologicalBatches(); err != nil {
        return nil, fmt.Errorf("invalid DAG: %w", err)
    }
    
    return dag, nil
}
```

### 5.5 完整流程示例

**输入**：
```yaml
# ReleaseImage
spec:
  upgrade:
    components:
      - name: container-runtime
        version: v1.0.0
      - name: kubernetes-master
        version: v1.29.0

# container-runtime selector (CRI=docker)
spec:
  type: selector
  subComponents:
    - name: docker
      version: v26.0.0
    - name: cri-dockerd
      version: v0.3.9

# docker
spec:
  dependencies:
    - name: bkeagent

# cri-dockerd
spec:
  dependencies:
    - name: docker

# kubernetes-master
spec:
  dependencies:
    - name: container-runtime
```

**执行流程**：

```
1. 展开 selector
   container-runtime → [docker, cri-dockerd]
   SelectorMapping: {
       SelectorName: "container-runtime", 
       ExpandedNames: ["docker", "cri-dockerd"]
   }

2. 构建组件列表
   [bkeagent, docker, cri-dockerd, kubernetes-master]

3. 解析依赖
   bkeagent: []
   docker: [bkeagent]
   cri-dockerd: [docker]
   kubernetes-master: [container-runtime] → 展开为 [docker, cri-dockerd]

4. 添加依赖边
   bkeagent → docker
   docker → cri-dockerd
   docker → kubernetes-master
   cri-dockerd → kubernetes-master

5. 拓扑排序
   Batch 0: [bkeagent]
   Batch 1: [docker]
   Batch 2: [cri-dockerd]
   Batch 3: [kubernetes-master]
```

### 5.6 边界情况处理

**循环依赖检测**：

```go
// 在 AddDependency 时检测循环
func (dag *UpgradeDAG) AddDependency(from, to string) error {
    if from == to {
        return fmt.Errorf("self-dependency detected: %s", from)
    }
    
    // 检测是否形成循环
    if dag.hasPath(to, from) {
        return fmt.Errorf("circular dependency detected: %s -> %s", from, to)
    }
    
    // 添加边
    dag.edges[from] = append(dag.edges[from], to)
    return nil
}
```

**依赖不存在的组件**：

```go
// 在添加边之前检查组件是否存在
for _, dep := range expandedDeps {
    if _, ok := dag.GetNode(dep); !ok {
        return nil, fmt.Errorf(
            "component %q depends on %q which is not in the DAG", 
            comp.Name, dep)
    }
}
```

**Selector 未展开（condition 全部为 false）**：

```go
// 如果 selector 展开为空，报错
if len(expandedNames) == 0 {
    return nil, fmt.Errorf(
        "selector %q expanded to zero components", selectorName)
}
```

### 5.7 优势分析

1. **实现简单**：不需要额外逻辑判断依赖哪个子组件
2. **正确性保证**：DAG 拓扑排序自动处理执行顺序
3. **冗余依赖无害**：即使依赖了多个组件，DAG 也会正确执行
4. **通用性强**：适用于任何 selector 展开场景

## 6. 容器运行时互斥选择

ReleaseImage 引用 `container-runtime/v1.0.0`（type=selector），DAG 构建期根据 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 自动展开为 containerd 或 docker + cri-dockerd。

**ReleaseImage 定义**：

```yaml
spec:
  install:
    components:
      - name: container-runtime   # ← selector 类型, DAG 展开为 containerd 或 docker
        version: v1.0.0
```

**Selector 展开规则**：
- `BKECluster.Spec.Cluster.ContainerRuntime.CRI == "containerd"` → 展开为 `containerd/v1.7.18`
- `BKECluster.Spec.Cluster.ContainerRuntime.CRI == "docker"` → 展开为 `docker/v26.0.0` + `cri-dockerd/v0.3.9`（依赖关系：docker → cri-dockerd）

**container-runtime ComponentVersion YAML（selector 类型）**：

```yaml
# bke-manifests/container-runtime/v1.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: container-runtime-v1.0.0
spec:
  name: container-runtime
  type: selector
  version: v1.0.0
  subComponents:
    - name: containerd
      version: v1.7.18
      condition: '{{.ContainerRuntimeCRI == "containerd"}}'
    - name: docker
      version: v26.0.0
      condition: '{{.ContainerRuntimeCRI == "docker"}}'
    - name: cri-dockerd
      version: v0.3.9
      condition: '{{.ContainerRuntimeCRI == "docker"}}'
   upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

## 7. 与现有代码的对应关系

| 现有代码 | KEP-6 selector 设计 |
|---------|-------------------|
| `init.go:789-797` `downloadContainerRuntime` switch CRI | DAG 构建器评估 subComponents.condition |
| `CRIContainerd = "containerd"` | `condition: '{{.ContainerRuntimeCRI == "containerd"}}'` |
| `CRIDocker = "docker"` + CRIDockerPlugin | docker + cri-dockerd 两个 ComponentVersion，condition 均匹配 docker |
| `BKECluster.Spec.Cluster.ContainerRuntime.CRI` | `ExecutionContext.TemplateContext.Variables["ContainerRuntimeCRI"]` |
| DockerPlugin: yum 安装 + daemon.json | docker ComponentVersion: installScript(yum) + configTemplates(daemon.json) |
| CRIDockerPlugin: 下载二进制 + service + socket | cri-dockerd ComponentVersion: artifacts + configTemplates(service+socket) |

## 8. 工作量评估

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **SubComponent.Condition 字段** | 类型定义 + deepcopy + CRD schema | 1 |
| **expandSelectorComponents** | DAG 构建期展开逻辑 + selector 自身不产生节点 | 2 |
| **evaluateCondition** | TemplateRenderer 通用条件评估 | 1 |
| **SelectorMapping** | 依赖展开数据结构 + expandSelectorDependencies | 2 |
| **mergeDependencies** | selector 级别依赖继承 + 去重 | 1 |
| **BuildUpgradeDAG 适配** | 集成 selectorMappings 参数 + 依赖展开 | 2 |
| **容器运行时 ComponentVersion** | container-runtime selector YAML + docker/cri-dockerd YAML | 2 |
| **集成测试** | condition 评估 + 依赖展开 + 循环检测 + 空 selector E2E | 3 |
| **小计** | - | **14 人天** |

## 9. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **condition 评估错误** | 选择错误的子组件 | 低 | 单元测试覆盖各类 condition 表达式 |
| **selector 展开为空** | DAG 构建失败 | 低 | 展开为空时报错，提示检查 condition |
| **循环依赖** | DAG 构建失败 | 低 | AddDependency 时 hasPath 检测 + TopologicalBatches 校验 |
| **依赖展开遗漏** | 执行顺序错误 | 低 | expandSelectorDependencies 单元测试 + AND 语义去重 |
| **混合模式兼容性** | selector + 独立条目冲突 | 中 | 校验逻辑覆盖混合模式 |

---

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](kep5/kep5.md)
2. [KEP-6 声明式集群版本升级方案](kep6-detailed-design.md)
3. [KEP-13 二进制组件改造](kep13-binary-component-migration-design.md)
4. [KEP-15 Composite 组件类型设计](kep15-composite-component-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **selector 类型** | ComponentVersion 的一种类型，从 subComponents 中按 condition 互斥选择子组件 |
| **condition** | Go Template 表达式，DAG 构建期评估，为真时子组件纳入 DAG |
| **SelectorMapping** | 记录 selector 展开后的子组件映射，用于依赖展开 |
| **AND 语义** | 对 selector 的依赖展开为对全部子组件的依赖 |
| **ContainerRuntimeCRI** | 集群配置的容器运行时类型，注入 TemplateContext.Variables 供 condition 评估 |

### C. 与 Composite 类型的对比

selector 与 composite（KEP-15）都是"聚合类型"，但语义不同：

| 维度 | selector | composite |
|------|----------|-----------|
| **选择方式** | 按 condition 互斥选择 | 全部包含 |
| **子组件数量** | 0-N 个（condition 为真的） | 全部 |
| **典型场景** | container-runtime 选 containerd 或 docker | kubernetes-core 包含 7 个 K8s 组件 |
| **依赖处理** | 对称（SelectorMapping + expandSelectorDependencies） | 对称（CompositeMapping + expandCompositeDependencies） |
