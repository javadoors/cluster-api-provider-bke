# KEP-15: Composite 组件类型设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-15 |
| **标题** | Composite 组件类型：ReleaseImage 中 K8s 核心组件的组合管理 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-26 |
| **依赖** | KEP-5 声明式升级框架、KEP-9 Static Pod 类型设计、KEP-13 二进制组件改造、KEP-14 迭代式升级 |

---

## 目录

1. [设计动机](#1-设计动机)
2. [设计目标与约束](#2-设计目标与约束)
3. [CompositeSpec 类型定义](#3-compositespec-类型定义)
4. [ReleaseImage 结构设计](#4-releaseimage-结构设计)
5. [ComponentVersion 中声明偏差约束](#5-componentversion-中声明偏差约束)
6. [DAG 展开机制](#6-dag-展开机制)
7. [kubernetesVersion 统一声明](#7-kubernetesversion-统一声明)
8. [deferredSubComponents 延迟升级](#8-deferredsubcomponents-延迟升级)
9. [版本校验机制](#9-版本校验机制)
10. [控制器适配](#10-控制器适配)
11. [优势对比](#11-优势对比)
12. [迁移策略](#12-迁移策略)
13. [工作量评估](#13-工作量评估)
14. [风险与缓解](#14-风险与缓解)

---

## 1. 设计动机

### 1.1 当前问题

ReleaseImage 中 K8s 核心组件以 7 个独立条目声明，存在以下问题：

| 问题 | 说明 | 影响 |
|------|------|------|
| **条目多，易遗漏** | 7 个 K8s 核心组件各自独立声明，新增/删除时容易遗漏 | ReleaseImage 配置不完整 |
| **版本易不一致** | 需逐个声明 apiserver/cm/scheduler/kubelet/kubectl/kube-proxy 的版本 | 可能出现 apiserver=v1.36 但 kubelet=v1.35 的错误 |
| **延迟升级硬编码** | kubelet 延迟升级在控制器代码中硬编码 `deferredComponents=["kubelet"]` | 不可配置，新增延迟组件需改代码 |
| **偏差约束分散** | 每个子组件各自声明 versionSkew，缺少集中管理 | 维护成本高 |
| **依赖关系分散** | etcd→apiserver→cm/scheduler 的依赖关系分散在各组件的 dependencies 中 | 不直观 |

### 1.2 解决思路

引入 `composite` 类型组件，将 K8s 核心组件组合为一个 `kubernetes-core` 组件：

- **1 个条目管理 7 个子组件**：ReleaseImage 中只需声明 `kubernetes-core`
- **kubernetesVersion 统一声明**：K8s 组件版本自动从 `kubernetesVersion` 解析
- **deferredSubComponents 声明式延迟**：在 ReleaseImage 中声明哪些子组件延迟升级
- **DAG 自动展开**：composite 组件在 DAG 构建时自动展开为子组件节点

## 2. 设计目标与约束

### 2.1 设计目标

1. **简化 ReleaseImage**：K8s 核心组件从 7 个条目减为 1 个 composite 条目
2. **统一版本管理**：通过 `kubernetesVersion` 字段统一设置所有 K8s 组件版本
3. **声明式延迟升级**：通过 `deferredSubComponents` 在 ReleaseImage 中声明延迟组件
4. **DAG 自动展开**：composite 组件在 DAG 构建时自动展开为独立子组件节点
5. **向后兼容**：支持同时使用 composite 类型和独立条目声明的混合模式

### 2.2 设计约束

| 约束 | 说明 |
|------|------|
| **composite 自身不产生 DAG 节点** | composite 仅作为容器，DAG 中只有展开后的子组件节点 |
| **子组件名称与 K8s 官方一致** | `kube-apiserver`、`kube-controller-manager` 等（KEP-14 规范 4） |
| **相邻 ReleaseImage K8s 版本差 ≤ 1** | composite 不改变此约束，仍需校验（KEP-14 规范 9.8a） |
| **etcd 独立版本** | etcd 不使用 kubernetesVersion，需单独指定版本 |
| **混合模式兼容** | ReleaseImage 可同时包含 composite 类型和独立条目声明的组件 |

## 3. CompositeSpec 类型定义

### 3.1 Go 类型定义

```go
// api/v1alpha1/componentversion_types.go

// CompositeSpec 定义 Composite 组件规格 🆕新增
//
// 设计思路 — 与 selector 类型的区别:
// selector 类型: 从 subComponents 中按 condition 互斥选择一个
// composite 类型: 包含所有 subComponents，全部纳入 DAG
//
// 设计思路 — composite 自身不产生 DAG 节点:
// composite 仅作为容器和元数据载体（如 deferredSubComponents），
// DAG 构建时自动展开为子组件节点，composite 自身不执行任何操作
type CompositeSpec struct {
    // 子组件列表
    // 每个子组件可以是任意类型（staticpod/binary/yaml/helm）
    // DAG 构建时自动展开为独立节点
    SubComponents []CompositeSubComponent `json:"subComponents"`
    
    // 延迟升级的子组件名称列表
    // 编排器读取此字段，在 executeControlPlaneHop 中跳过这些子组件
    // 默认: ["kubelet"]
    // 示例: ["kubelet"] 或 ["kubelet", "kubectl"]
    DeferredSubComponents []string `json:"deferredSubComponents,omitempty"`
}

// CompositeSubComponent 定义 Composite 子组件引用
type CompositeSubComponent struct {
    // 子组件名称（必须与 K8s 官方名称一致）
    // 如: kube-apiserver, kubelet, etcd
    Name string `json:"name"`
    
    // 子组件版本
    // K8s 核心组件（apiserver/cm/scheduler/kubelet/kubectl/kube-proxy）:
    //   留空时自动从 ReleaseImage.Spec.KubernetesVersion 解析
    // 非 K8s 组件（如 etcd）:
    //   必须显式指定版本
    Version string `json:"version,omitempty"`
}

// ComponentVersionSpec 扩展
type ComponentVersionSpec struct {
    // ... 现有字段 ...
    
    // Composite 类型配置 (type=composite 时必填) 🆕新增
    Composite *CompositeSpec `json:"composite,omitempty"`
}

const (
    // ... 现有类型 ...
    ComponentTypeComposite ComponentType = "composite" // 🆕新增
)
```

### 3.2 ReleaseImage 中 composite 组件的解析

```go
// ReleaseImageSpec 扩展

type ReleaseImageSpec struct {
    Version string `json:"version"`
    
    // 🆕新增：统一 K8s 版本声明
    // composite 组件中未指定 version 的 K8s 核心子组件自动使用此版本
    KubernetesVersion string `json:"kubernetesVersion,omitempty"`
    
    Install *ReleaseImageInstallSpec `json:"install,omitempty"`
    Upgrade *ReleaseImageUpgradeSpec `json:"upgrade,omitempty"`
}

// K8s 核心组件名称集合（用于 kubernetesVersion 自动解析）
var K8sCoreComponentSet = map[string]bool{
    "kube-apiserver": true, "kube-controller-manager": true,
    "kube-scheduler": true, "kubelet": true,
    "kubectl": true, "kube-proxy": true,
    // 注意: etcd 不在此集合中，需独立指定版本
}

// GetComponentVersion 扩展：支持 composite 组件的子组件版本解析
func (s *ReleaseImageSpec) GetComponentVersion(name string) string {
    // K8s 核心组件优先从 kubernetesVersion 读取
    if K8sCoreComponentSet[name] && s.KubernetesVersion != "" {
        return s.KubernetesVersion
    }
    
    // 从 components 列表查找（包括 composite 的 subComponents）
    if s.Install != nil {
        for _, c := range s.Install.Components {
            if c.Name == name {
                return c.Version
            }
            // 展开 composite 子组件
            if c.Type == "composite" && c.Composite != nil {
                for _, sub := range c.Composite.SubComponents {
                    if sub.Name == name {
                        if sub.Version != "" {
                            return sub.Version
                        }
                        // K8s 核心组件从 kubernetesVersion 解析
                        if K8sCoreComponentSet[name] && s.KubernetesVersion != "" {
                            return s.KubernetesVersion
                        }
                    }
                }
            }
        }
    }
    // 同样检查 upgrade 列表
    if s.Upgrade != nil {
        // ... 同上逻辑 ...
    }
    
    return ""
}
```

## 4. ReleaseImage 结构设计

### 4.1 完整 YAML 示例

```yaml
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.7.0
spec:
  version: "v2.7.0"
  
  # 🆕新增：统一 K8s 版本声明
  # composite 中未指定 version 的 K8s 核心子组件自动使用此版本
  kubernetesVersion: "v1.36.0"

  install:
    components:
      # ── K8s 核心组件（composite 类型，1 个条目管理 7 个子组件） ──
      - name: kubernetes-core
        version: v1.36.0
        type: composite
        subComponents:
          # etcd: 独立版本号，不用 kubernetesVersion
          - name: etcd
            version: v3.5.20
          # K8s 核心组件: version 从 kubernetesVersion 自动解析为 v1.36.0
          - name: kube-apiserver
          - name: kube-controller-manager
          - name: kube-scheduler
          - name: kubelet
          - name: kubectl
          - name: kube-proxy
      # ── 其它组件（独立声明） ──
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.24
      - name: coredns
        version: v1.11.3

  upgrade:
    components:
      # ── K8s 核心组件（composite 类型） ──
      - name: kubernetes-core
        version: v1.36.0
        type: composite
        # 延迟升级的子组件列表
        deferredSubComponents:
          - kubelet
        subComponents:
          - name: etcd
            version: v3.5.20
          - name: kube-apiserver
          - name: kube-controller-manager
          - name: kube-scheduler
          - name: kube-proxy
          - name: kubectl
          - name: kubelet             # 可被 executeControlPlaneHop 跳过
      # ── 其它组件 ──
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.24
      - name: coredns
        version: v1.11.3
```

### 4.2 混合模式示例（向后兼容）

```yaml
# 混合模式：composite + 独立条目并存
upgrade:
  components:
    # composite 管理 K8s 核心组件
    - name: kubernetes-core
      version: v1.36.0
      type: composite
      deferredSubComponents:
        - kubelet
      subComponents:
        - name: etcd
          version: v3.5.20
        - name: kube-apiserver
        - name: kube-controller-manager
        - name: kube-scheduler
        - name: kube-proxy
        - name: kubectl
        - name: kubelet
    # 独立条目管理其它组件
    - name: bkeagent
      version: v2.7.0
    - name: containerd
      version: v1.7.24
```

## 5. ComponentVersion 中声明偏差约束

### 5.1 versionSkew 字段

偏差约束在子组件的 ComponentVersion 中声明（非 composite 层面）：

```yaml
# bke-manifests/kube-apiserver/v1.36.0/component.yaml
spec:
  name: kube-apiserver
  type: staticpod
  version: v1.36.0
  # apiserver 是参照基准，无需 versionSkew

# bke-manifests/kubelet/v1.36.0/component.yaml
spec:
  name: kubelet
  type: binary
  version: v1.36.0
  versionSkew:
    referenceComponent: kube-apiserver
    maxSkewBehind: 3
    direction: behind

# bke-manifests/kube-controller-manager/v1.36.0/component.yaml
spec:
  name: kube-controller-manager
  type: staticpod
  version: v1.36.0
  versionSkew:
    referenceComponent: kube-apiserver
    maxSkewBehind: 1
    direction: behind

# bke-manifests/etcd/v3.5.20/component.yaml
spec:
  name: etcd
  type: staticpod
  version: v3.5.20
  # etcd 不在 K8s Version Skew Policy 中，无 versionSkew
  compatibility:
    - component: kube-apiserver
      rule: ">=v1.34"
```

### 5.2 子组件依赖关系

```yaml
# etcd 依赖 kubelet（Static Pod 需 Kubelet 拉起）
spec:
  dependencies:
    - name: kubelet
      phase: Install

# kube-apiserver 依赖 etcd
spec:
  dependencies:
    - name: etcd
      phase: Install

# kube-controller-manager 依赖 kube-apiserver
spec:
  dependencies:
    - name: kube-apiserver
      phase: Install

# kubelet 依赖 containerd + kube-apiserver
spec:
  dependencies:
    - name: containerd
      phase: Install
    - name: kube-apiserver
      phase: Install
```

## 6. DAG 展开机制

### 6.1 展开流程

composite 组件在 DAG 构建时自动展开为子组件节点，自身不产生 DAG 节点：

```
ReleaseImage 中:
  kubernetes-core (composite, type=composite)
    └─ subComponents: [etcd, apiserver, cm, scheduler, kube-proxy, kubectl, kubelet]

DAG 构建时展开:
  Step 1: 识别 composite 组件
  Step 2: 遍历 subComponents，为每个子组件创建 DAG 节点
  Step 3: 从子组件的 ComponentVersion.spec.dependencies 解析依赖关系
  Step 4: composite 自身不产生 DAG 节点（与 selector 类型类似）

展开后的 DAG:
  etcd → kube-apiserver → kube-controller-manager ┐
                              kube-scheduler       ├→ kubelet
                              kube-proxy          │
                              kubectl             ┘
```

### 6.2 展开实现

> **注意**：本节展示的是基本展开逻辑。完整的依赖处理实现（含 `CompositeMapping` 返回、依赖继承合并、依赖展开）见 **6.4 节**，其中 `expandCompositeComponents` 和 `BuildDAGFromBundle` 有增强版本。

```go
// pkg/upgrade/bundle.go 扩展

// expandCompositeComponents 展开 composite 组件为独立子组件
// 基本版本：仅返回展开后的组件列表
// 增强版本（见 6.4.5.1）：同时返回 CompositeMapping 用于依赖展开
func expandCompositeComponents(
    components []ReleaseImageComponent,
    kubernetesVersion string,
) []ReleaseImageComponent {
    var expanded []ReleaseImageComponent
    
    for _, comp := range components {
        if comp.Type == "composite" && comp.Composite != nil {
            // composite 组件：展开为子组件
            for _, sub := range comp.Composite.SubComponents {
                version := sub.Version
                if version == "" && K8sCoreComponentSet[sub.Name] && kubernetesVersion != "" {
                    version = kubernetesVersion // 从 kubernetesVersion 自动解析
                }
                
                expanded = append(expanded, ReleaseImageComponent{
                    Name:    sub.Name,
                    Version: version,
                    // 子组件的类型从 ComponentVersion 解析
                })
            }
        } else {
            // 非 composite 组件：保持不变
            expanded = append(expanded, comp)
        }
    }
    
    return expanded
}

// BuildDAGFromBundle 扩展：先展开 composite 再构建 DAG
func BuildDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
) (*topology.UpgradeDAG, error) {
    // 1. 展开 composite 组件
    kubernetesVersion := bundle.Release.Spec.KubernetesVersion
    upgradeComponents := expandCompositeComponents(
        bundle.Release.Spec.Upgrade.Components,
        kubernetesVersion,
    )
    
    // 2. 转换为 ComponentNode（复用现有逻辑）
    var components []topology.ReleaseImageUpgradeComponent
    for _, c := range upgradeComponents {
        comp := topology.ReleaseImageUpgradeComponent{
            Name:    c.Name,
            Version: c.Version,
        }
        // 从 ComponentVersion 获取 inline handler
        if cv, err := bundle.GetComponentVersion(c.Name, c.Version); err == nil {
            if cv.Spec.Inline != nil {
                comp.Inline = &topology.ReleaseImageUpgradeInline{
                    Handler: cv.Spec.Inline.Handler,
                    Version: cv.Spec.Inline.Version,
                }
            }
        }
        components = append(components, comp)
    }
    
    // 3. 构建 DAG（基本版本）
    // 增强版本（见 6.4.5.4）：传入 compositeMappings 展开对 composite 的依赖
    return topology.BuildUpgradeDAG(components, resolve)
}
```

### 6.3 展开后的 DAG 结构

```
展开后的 DAG（K8s 核心组件按依赖排序）:

安装 DAG:
  Batch 1: [etcd]                    ← type: staticpod
  Batch 2: [kube-apiserver]          ← type: staticpod
  Batch 3: [kube-controller-manager,  ← 并行
            kube-scheduler]
  Batch 4: [kube-proxy]              ← type: yaml
  Batch 5: [kubelet, kubectl]        ← type: binary, 并行

升级 DAG（延迟 kubelet）:
  Batch 1: [etcd]
  Batch 2: [kube-apiserver]
  Batch 3: [cm, scheduler]
  Batch 4: [kube-proxy]
  Batch 5: [kubectl]
  ─── kubelet 延迟 ───
  Batch 6 (延迟): [kubelet]
```

### 6.4 外部组件对 composite 的依赖处理

composite 组件展开为子组件后，composite 自身不产生 DAG 节点。这引发两个依赖解析问题：composite 自身的依赖如何传递给子组件，以及其它组件对 composite 名称的依赖如何展开。本节设计统一的依赖处理机制，与 KEP-6 selector 依赖处理（§12.3.3）保持一致的架构模式。

#### 6.4.1 问题分析

**问题 1：composite 自身的依赖需要传递给子组件**

composite 组件的 ComponentVersion 可能在 `spec.dependencies` 中声明依赖：

```yaml
# kubernetes-core/v1.36.0/component.yaml
spec:
  name: kubernetes-core
  type: composite
  version: v1.36.0
  composite:
    subComponents:
      - name: etcd
        version: v3.5.20
      - name: kube-apiserver
      - name: kubelet
      # ...
  dependencies:          # ← composite 级别的依赖
    - name: bkeagent       # 所有子组件都应继承此依赖
      phase: Install
```

展开后，`etcd`、`kube-apiserver`、`kubelet` 等子组件应继承 `bkeagent` 依赖。

**问题 2：其它组件对 composite 的依赖需要展开**

其它组件可能在 `spec.dependencies` 中声明对 composite 名称的依赖：

```yaml
# coredns/v1.11.3/component.yaml
spec:
  name: coredns
  type: yaml
  version: v1.11.3
  dependencies:
    - name: kubernetes-core   # ← 依赖 composite 名称
      phase: Install
```

展开后，`kubernetes-core` 不存在于 DAG 中，需要将此依赖展开为对 composite 所有子组件的依赖。

#### 6.4.2 依赖展开规则

DAG 构建期的依赖解析涉及三类场景，处理规则如下：

| 场景 | 示例 | 处理规则 | 展开后效果 |
|------|------|---------|-----------|
| **子组件依赖外部组件** | `kubelet → containerd` | 无需展开，containerd 是独立 DAG 节点 | 保持原样 |
| **子组件依赖同 composite 的其它子组件** | `kube-apiserver → etcd` | 无需展开，两者展开后均为独立 DAG 节点 | 保持原样 |
| **外部组件依赖 composite 名称** | `coredns → kubernetes-core` | 展开为对 composite 所有子组件的依赖（AND 语义） | `coredns → [etcd, kube-apiserver, kubelet, ...]` |

**AND 语义说明**：对 composite 的依赖等价于依赖其全部子组件。DAG 拓扑排序自动处理执行顺序——只有所有被依赖的子组件都完成后，依赖方才会被调度。冗余依赖（如 `coredns → etcd` 和 `coredns → kube-apiserver`，而 `kube-apiserver` 已依赖 `etcd`）不影响正确性，拓扑排序会自动消除冗余路径。

#### 6.4.3 设计原则

**原则 1：依赖定义在具体子组件中**

composite 的 `subComponents` 不定义依赖，而是由各子组件的 ComponentVersion `spec.dependencies` 各自声明：

```yaml
# etcd/v3.5.20/component.yaml
spec:
  name: etcd
  type: staticpod
  dependencies:
    - name: kubelet          # etcd 依赖 kubelet (Static Pod 需 Kubelet 拉起)
      phase: Install

# kube-apiserver/v1.36.0/component.yaml
spec:
  name: kube-apiserver
  type: staticpod
  dependencies:
    - name: etcd            # apiserver 依赖 etcd
      phase: Install

# kubelet/v1.36.0/component.yaml
spec:
  name: kubelet
  type: binary
  dependencies:
    - name: containerd      # kubelet 依赖 containerd (外部组件)
      phase: Install
    - name: kube-apiserver  # kubelet 依赖 apiserver
      phase: Install
```

**优势**：

- ✅ 不需要扩展 `CompositeSubComponent` 结构体
- ✅ 依赖关系在具体组件中定义，更清晰
- ✅ 符合现有依赖解析机制
- ✅ 每个子组件独立维护自己的依赖

**原则 2：composite 级别的依赖被子组件继承**

composite 的 ComponentVersion `spec.dependencies` 中声明的依赖被所有子组件继承（与子组件自身的依赖合并去重）：

```txt
composite (kubernetes-core) dependencies: [bkeagent]
sub-component (kube-apiserver) dependencies: [etcd]
→ 合并后: kube-apiserver dependencies: [etcd, bkeagent]

sub-component (etcd) dependencies: [kubelet]
→ 合并后: etcd dependencies: [kubelet, bkeagent]
```

**原则 3：对 composite 的依赖展开为对所有子组件的依赖（AND 语义）**

与 KEP-6 selector 的依赖展开规则一致，对 composite 名称的依赖展开为对全部子组件的依赖。

#### 6.4.4 数据结构

```go
// pkg/upgrade/bundle.go

// CompositeMapping 记录 composite 展开后的子组件映射
// 与 selector 的 SelectorMapping 结构对称（KEP-6 §12.3.3.2）
type CompositeMapping struct {
    // CompositeName composite 组件名称（如 "kubernetes-core"）
    CompositeName string `json:"compositeName"`

    // ExpandedNames 展开后的子组件名称列表
    // 如 ["etcd", "kube-apiserver", "kube-controller-manager", ...]
    ExpandedNames []string `json:"expandedNames"`
}
```

#### 6.4.5 实现方案

##### 6.4.5.1 expandCompositeComponents 增强：返回映射

```go
// expandCompositeComponents 展开 composite 组件为独立子组件
// 增强：同时返回 CompositeMapping 列表，供依赖展开使用
func expandCompositeComponents(
    components []ReleaseImageComponent,
    kubernetesVersion string,
) ([]ReleaseImageComponent, []CompositeMapping) {
    var expanded []ReleaseImageComponent
    var mappings []CompositeMapping

    for _, comp := range components {
        if comp.Type == "composite" && comp.Composite != nil {
            // composite 组件：展开为子组件
            var subNames []string
            for _, sub := range comp.Composite.SubComponents {
                version := sub.Version
                if version == "" && K8sCoreComponentSet[sub.Name] && kubernetesVersion != "" {
                    version = kubernetesVersion // 从 kubernetesVersion 自动解析
                }

                expanded = append(expanded, ReleaseImageComponent{
                    Name:    sub.Name,
                    Version: version,
                })
                subNames = append(subNames, sub.Name)
            }

            // 记录映射：composite 名称 → 展开后的子组件名称列表
            mappings = append(mappings, CompositeMapping{
                CompositeName: comp.Name,
                ExpandedNames: subNames,
            })
        } else {
            // 非 composite 组件：保持不变
            expanded = append(expanded, comp)
        }
    }

    return expanded, mappings
}
```

##### 6.4.5.2 buildCompositeSubComponentNode：合并依赖

```go
// buildCompositeSubComponentNode 构建子组件节点，合并 composite 和子组件的依赖
//
// 合并规则: composite 级别的 dependencies + 子组件自身的 dependencies（去重）
// 与 selector 的 buildSubComponentNode 对称（KEP-6 §12.3.3.2）
func buildCompositeSubComponentNode(
    cvStore ComponentVersionStore,
    compositeCV *configv1alpha1.ComponentVersion,
    sub CompositeSubComponent,
    kubernetesVersion string,
) (topology.ComponentNode, error) {
    // 解析子组件版本
    version := sub.Version
    if version == "" && K8sCoreComponentSet[sub.Name] && kubernetesVersion != "" {
        version = kubernetesVersion
    }

    // 加载子组件的 ComponentVersion
    subCV, err := cvStore.GetComponentVersion(context.Background(), sub.Name, version)
    if err != nil {
        return topology.ComponentNode{}, fmt.Errorf(
            "failed to load sub-component %s: %w", sub.Name, err)
    }

    // 合并依赖：composite 的依赖 + 子组件自己的依赖
    mergedDeps := mergeDependencies(compositeCV.Spec.Dependencies, subCV.Spec.Dependencies)

    return topology.ComponentNode{
        Name:         sub.Name,
        Version:      version,
        Dependencies: mergedDeps,
    }, nil
}

// mergeDependencies 合并两组依赖（去重）
// composite 级别依赖先写入，子组件自身依赖覆盖同名项
func mergeDependencies(
    compositeDeps, subDeps []configv1alpha1.Dependency,
) []configv1alpha1.Dependency {
    depMap := make(map[string]configv1alpha1.Dependency)

    // 先添加 composite 级别的依赖
    for _, dep := range compositeDeps {
        depMap[dep.Name] = dep
    }

    // 再添加子组件自身的依赖（覆盖同名依赖）
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

##### 6.4.5.3 expandCompositeDependencies：展开对 composite 的依赖

```go
// expandCompositeDependencies 将对 composite 名称的依赖展开为对子组件的依赖
//
// 遍历依赖列表，将对 composite 名称的依赖替换为其全部子组件名称（AND 语义）。
// 非 composite 的依赖保持原样。结果去重。
//
// 与 selector 的 expandSelectorDependencies 对称（KEP-6 §12.3.3.2）
func expandCompositeDependencies(
    deps []string,
    compositeMappings []CompositeMapping,
) []string {
    var result []string
    seen := make(map[string]bool)

    for _, dep := range deps {
        // 查找是否为 composite
        var expanded []string
        for _, mapping := range compositeMappings {
            if mapping.CompositeName == dep {
                expanded = mapping.ExpandedNames
                break
            }
        }

        if len(expanded) > 0 {
            // 是 composite，展开为所有子组件
            for _, subName := range expanded {
                if !seen[subName] {
                    result = append(result, subName)
                    seen[subName] = true
                }
            }
        } else {
            // 不是 composite，保持原样
            if !seen[dep] {
                result = append(result, dep)
                seen[dep] = true
            }
        }
    }

    return result
}
```

##### 6.4.5.4 BuildDAGFromBundle 增强：集成依赖展开

```go
// BuildDAGFromBundle 增强：先展开 composite，再用映射展开依赖，最后构建 DAG
func BuildDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
    cvStore ComponentVersionStore,
) (*topology.UpgradeDAG, error) {
    kubernetesVersion := bundle.Release.Spec.KubernetesVersion

    // 1. 展开 composite 组件，同时获取映射
    upgradeComponents, compositeMappings := expandCompositeComponents(
        bundle.Release.Spec.Upgrade.Components,
        kubernetesVersion,
    )

    // 2. 转换为 ComponentNode
    var components []topology.ReleaseImageUpgradeComponent
    for _, c := range upgradeComponents {
        comp := topology.ReleaseImageUpgradeComponent{
            Name:    c.Name,
            Version: c.Version,
        }
        // 从 ComponentVersion 获取 inline handler
        if cv, err := cvStore.GetComponentVersion(context.Background(), c.Name, c.Version); err == nil {
            if cv.Spec.Inline != nil {
                comp.Inline = &topology.ReleaseImageUpgradeInline{
                    Handler: cv.Spec.Inline.Handler,
                    Version: cv.Spec.Inline.Version,
                }
            }
        }
        components = append(components, comp)
    }

    // 3. 构建 DAG，传入 compositeMappings 用于依赖展开
    return topology.BuildUpgradeDAG(components, resolve, compositeMappings)
}
```

```go
// pkg/topology/dag.go 扩展

// BuildUpgradeDAG 增强：接受 compositeMappings，构建时展开依赖
func BuildUpgradeDAG(
    components []ReleaseImageUpgradeComponent,
    resolve DependencyResolver,
    compositeMappings []CompositeMapping,
) (*UpgradeDAG, error) {
    dag := NewUpgradeDAG()

    // 阶段 1：添加所有组件节点（已展开后的独立子组件）
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

        // 提取依赖名称列表
        depNames := make([]string, 0, len(deps))
        for _, d := range deps {
            depNames = append(depNames, d.Name)
        }

        // 展开 composite 依赖：将 "kubernetes-core" 展开为 [etcd, kube-apiserver, ...]
        expandedDeps := expandCompositeDependencies(depNames, compositeMappings)

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

#### 6.4.6 完整流程示例

**输入**：

```yaml
# ReleaseImage
spec:
  upgrade:
    components:
      - name: kubernetes-core        # composite
        version: v1.36.0
        type: composite
        subComponents:
          - name: etcd
            version: v3.5.20
          - name: kube-apiserver
          - name: kube-controller-manager
          - name: kube-scheduler
          - name: kubelet
          - name: kubectl
          - name: kube-proxy
      - name: containerd
        version: v1.7.24
      - name: coredns
        version: v1.11.3
      - name: bkeagent
        version: v2.7.0

# kubernetes-core/v1.36.0/component.yaml
spec:
  name: kubernetes-core
  type: composite
  dependencies:
    - name: bkeagent          # composite 级别依赖，所有子组件继承
      phase: Install

# kube-apiserver/v1.36.0/component.yaml
spec:
  dependencies:
    - name: etcd

# kubelet/v1.36.0/component.yaml
spec:
  dependencies:
    - name: containerd
    - name: kube-apiserver

# coredns/v1.11.3/component.yaml
spec:
  dependencies:
    - name: kubernetes-core   # 依赖 composite 名称
```

**执行流程**：

```txt
1. 展开 composite
   kubernetes-core → [etcd, kube-apiserver, kube-controller-manager,
                      kube-scheduler, kubelet, kubectl, kube-proxy]
   CompositeMapping: {
       CompositeName: "kubernetes-core",
       ExpandedNames: ["etcd", "kube-apiserver", "kube-controller-manager",
                       "kube-scheduler", "kubelet", "kubectl", "kube-proxy"]
   }

2. 构建组件列表 (composite 自身不在列表中)
   [bkeagent, containerd, etcd, kube-apiserver, kube-controller-manager,
    kube-scheduler, kubelet, kubectl, kube-proxy, coredns]

3. 合并子组件依赖 (composite 级别依赖 bkeagent 被各子组件继承)
   etcd:                  [kubelet, bkeagent]       ← 继承 bkeagent
   kube-apiserver:        [etcd, bkeagent]          ← 继承 bkeagent
   kubelet:               [containerd, kube-apiserver, bkeagent]  ← 继承 bkeagent
   kube-controller-manager:[kube-apiserver, bkeagent]
   kube-scheduler:        [kube-apiserver, bkeagent]
   kubectl:               [bkeagent]
   kube-proxy:            [bkeagent]

4. 展开对 composite 的依赖
   coredns: [kubernetes-core] → 展开为 [etcd, kube-apiserver, kube-controller-manager,
                                        kube-scheduler, kubelet, kubectl, kube-proxy]

5. 添加依赖边
   bkeagent → etcd, kube-apiserver, kubelet, cm, scheduler, kubectl, kube-proxy
   containerd → kubelet
   kubelet → etcd
   etcd → kube-apiserver
   kube-apiserver → kubelet, cm, scheduler
   kube-apiserver → coredns (经展开)
   etcd → coredns (经展开)
   kubelet → coredns (经展开)
   ... (其它子组件 → coredns)

6. 拓扑排序
   Batch 0: [bkeagent]
   Batch 1: [containerd]
   Batch 2: [kubelet]              ← 依赖 containerd + bkeagent
   Batch 3: [etcd]                 ← 依赖 kubelet + bkeagent
   Batch 4: [kube-apiserver]        ← 依赖 etcd + bkeagent
   Batch 5: [kube-controller-manager, kube-scheduler, kubectl, kube-proxy]
   Batch 6: [coredns]              ← 依赖全部 K8s 核心组件
```

#### 6.4.7 边界情况处理

**循环依赖检测**：

```go
// AddDependency 时检测循环（复用现有拓扑排序校验）
func (dag *UpgradeDAG) AddDependency(from, to string) error {
    if from == to {
        return fmt.Errorf("self-dependency detected: %s", from)
    }
    if dag.hasPath(to, from) {
        return fmt.Errorf("circular dependency detected: %s -> %s", from, to)
    }
    dag.edges[from] = append(dag.edges[from], to)
    return nil
}
```

**依赖指向不存在的 composite**：

```go
// 展开后检查：依赖指向的 composite 名称不存在于 mappings 中
for _, dep := range expandedDeps {
    if _, ok := dag.GetNode(dep); !ok {
        return nil, fmt.Errorf(
            "component %q depends on %q which is not in the DAG",
            comp.Name, dep)
    }
}
```

**composite 的 subComponents 为空**：

```go
// 展开时检测：composite 无子组件
if comp.Type == "composite" && comp.Composite != nil {
    if len(comp.Composite.SubComponents) == 0 {
        return nil, fmt.Errorf(
            "composite %q has no subComponents", comp.Name)
    }
    // ...
}
```

**对 composite 的依赖与对子组件的依赖共存**：

```yaml
# coredns 同时依赖 composite 名称和具体子组件
spec:
  dependencies:
    - name: kubernetes-core      # composite 名称
    - name: kube-apiserver      # 具体子组件
```

展开后 `kubernetes-core` → `[etcd, kube-apiserver, ...]`，与显式的 `kube-apiserver` 去重，结果仍为 `[etcd, kube-apiserver, ...]`。`expandCompositeDependencies` 的 `seen` map 保证去重。

**deferredSubComponents 与依赖展开的交互**：

`deferredSubComponents` 延迟升级的是子组件的执行时机（由 `executeControlPlaneHop` 跳过），不影响 DAG 结构。即使 `kubelet` 被延迟，`coredns → kubernetes-core` 仍展开为包含 `kubelet` 的依赖——DAG 中 `kubelet` 节点存在，但延迟阶段才执行。`coredns` 仍需等待 `kubelet` 完成才能执行（延迟阶段结束后）。

#### 6.4.8 与 selector 依赖处理的统一

composite 与 selector 的依赖处理机制完全对称，区别仅在于展开条件：

| 维度 | selector (KEP-6 §12.3.3) | composite (本节) |
|------|--------------------------|------------------|
| **展开条件** | `subComponents[].condition` 评估为真 | 全部子组件（无 condition） |
| **映射类型** | `SelectorMapping` | `CompositeMapping` |
| **依赖继承** | selector 级别依赖 → 被选中子组件继承 | composite 级别依赖 → 全部子组件继承 |
| **依赖展开** | 对 selector 名称的依赖 → 被选中子组件 | 对 composite 名称的依赖 → 全部子组件 |
| **展开语义** | AND（依赖全部被选中的） | AND（依赖全部） |
| **合并函数** | `mergeDependencies` | `mergeDependencies`（复用） |
| **展开函数** | `expandSelectorDependencies` | `expandCompositeDependencies` |

**后续可统一**：若后续需要统一，可定义通用的 `AggregateMapping` 和 `expandAggregateDependencies`，由 `SelectorMapping` 和 `CompositeMapping` 转换。当前保持分离以避免过度抽象。

## 7. kubernetesVersion 统一声明

### 7.1 自动解析规则

| 子组件 | 是否自动解析 | 原因 |
|--------|------------|------|
| kube-apiserver | ✅ | K8s 核心组件，使用 kubernetesVersion |
| kube-controller-manager | ✅ | K8s 核心组件 |
| kube-scheduler | ✅ | K8s 核心组件 |
| kubelet | ✅ | K8s 核心组件 |
| kubectl | ✅ | K8s 核心组件 |
| kube-proxy | ✅ | K8s 核心组件 |
| etcd | ❌ | 非 K8s 核心组件，独立版本号 |

### 7.2 解析优先级

```go
// 子组件版本解析优先级:
// 1. subComponent.Version 非空 → 使用显式指定的版本
// 2. K8s 核心组件 + kubernetesVersion 非空 → 使用 kubernetesVersion
// 3. 从 components 列表独立查找
// 4. 返回空（组件不存在）
```

### 7.3 版本一致性校验

```go
// ValidateCompositeVersionConsistency 校验 composite 内 K8s 核心组件版本一致性
func ValidateCompositeVersionConsistency(
    composite *CompositeSpec,
    kubernetesVersion string,
) error {
    for _, sub := range composite.SubComponents {
        if K8sCoreComponentSet[sub.Name] {
            // K8s 核心组件
            if sub.Version != "" && sub.Version != kubernetesVersion {
                return fmt.Errorf(
                    "K8s component %s version %q conflicts with kubernetesVersion %q",
                    sub.Name, sub.Version, kubernetesVersion,
                )
            }
        }
    }
    return nil
}
```

## 8. deferredSubComponents 延迟升级

### 8.1 声明式延迟

```yaml
# ReleaseImage 中声明式指定延迟升级的子组件
upgrade:
  components:
    - name: kubernetes-core
      type: composite
      deferredSubComponents:
        - kubelet          # kubelet 延迟到偏差极限后补充升级
```

### 8.2 控制器读取

```go
// executeControlPlaneHop 从 composite 组件读取 deferredSubComponents

func (r *ClusterVersionReconciler) executeControlPlaneHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopTarget string,
    deferredComponents []string, // 从 composite.deferredSubComponents 传入
) (*HopResult, error) {
    // ...
    
    // 延迟升级：将 deferredComponents 的 Target 设为 Current（跳过）
    deferredSet := make(map[string]bool)
    for _, name := range deferredComponents {
        current := vc.GetCurrent(name)
        if current != "" {
            vc.SetTarget(name, current)
            deferredSet[name] = true
        }
    }
    
    // ...
}

// orchestrateFullUpgrade 从 ReleaseImage 解析 deferredSubComponents
func (r *ClusterVersionReconciler) resolveDeferredComponents(
    bundle *releasemanifest.Bundle,
) []string {
    for _, comp := range bundle.Release.Spec.Upgrade.Components {
        if comp.Type == "composite" && comp.Composite != nil {
            if len(comp.Composite.DeferredSubComponents) > 0 {
                return comp.Composite.DeferredSubComponents
            }
        }
    }
    return []string{"kubelet"} // 默认
}
```

## 9. 版本校验机制

### 9.1 发布时校验

```go
// pkg/release/validation.go

// ValidateReleaseImageWithComposite 校验包含 composite 的 ReleaseImage
func ValidateReleaseImageWithComposite(ri *ReleaseImage) error {
    // 1. 校验 composite 内 K8s 组件版本一致性
    if ri.Spec.KubernetesVersion != "" {
        for _, comp := range ri.Spec.Install.Components {
            if comp.Type == "composite" && comp.Composite != nil {
                if err := ValidateCompositeVersionConsistency(
                    comp.Composite, ri.Spec.KubernetesVersion,
                ); err != nil {
                    return err
                }
            }
        }
    }
    
    // 2. 校验 K8s 组件名称与官方一致
    for _, comp := range ri.Spec.Install.Components {
        if comp.Type == "composite" && comp.Composite != nil {
            for _, sub := range comp.Composite.SubComponents {
                if correct, isMistake := commonMistakes[sub.Name]; isMistake {
                    return fmt.Errorf(
                        "component name %q is not official, use %q instead",
                        sub.Name, correct,
                    )
                }
            }
        }
    }
    
    // 3. 校验相邻 ReleaseImage K8s 版本差 ≤ 1
    // （复用 KEP-14 的 ValidateAdjacentReleaseImages）
    
    return nil
}
```

### 9.2 升级时校验

```go
// resolveUpgradePath 扩展：校验 composite 展开后的相邻版本差
func (r *ClusterVersionReconciler) resolveUpgradePath(
    ctx context.Context,
    currentVersion string,
    desiredVersion string,
) ([]string, error) {
    // ... 查找路径 ...
    
    // 校验相邻 hop 的 K8s 组件版本差 ≤ 1
    for i := 0; i < len(path)-1; i++ {
        currentRI, _ := r.resolveReleaseImage(ctx, path[i])
        nextRI, _ := r.resolveReleaseImage(ctx, path[i+1])
        
        // 从 composite 或 kubernetesVersion 解析 K8s 版本
        currentK8sVer := currentRI.Spec.GetComponentVersion("kube-apiserver")
        nextK8sVer := nextRI.Spec.GetComponentVersion("kube-apiserver")
        
        skew := computeMinorVersionSkew(nextK8sVer, currentK8sVer)
        if skew > 1 {
            return nil, fmt.Errorf(
                "adjacent ReleaseImage K8s version skew %d exceeds max 1 (%s → %s)",
                skew, currentK8sVer, nextK8sVer,
            )
        }
    }
    
    return path, nil
}
```

## 10. 控制器适配

### 10.1 DAG 构建适配

```go
// BuildDAGFromBundle 已在 6.2 节定义：先展开 composite 再构建 DAG
// 控制器无需感知 composite，DAG 构建后全部是独立子组件节点
```

### 10.2 偏差门控适配

```go
// evaluateSkewGate 适配：从展开后的 VersionContext 读取版本
// composite 已展开为独立组件，VersionContext 中有各组件版本
// 偏差检查逻辑无需修改
```

### 10.3 状态追踪适配

```go
// updateComponentStatuses 适配：各子组件独立更新 Status
// composite 不产生 Status 条目（已展开为子组件）
```

## 11. 优势对比

| 维度 | 7 个独立条目（旧方案） | composite 组合（新方案） |
|------|----------------------|----------------------|
| **ReleaseImage 简洁性** | 7 个独立条目，容易遗漏 | 1 个 composite 条目 + subComponents |
| **版本一致性** | 需逐个声明版本，可能不一致 | kubernetesVersion 统一声明，自动解析 |
| **偏差约束管理** | 每个子组件各自声明 versionSkew | 子组件声明 + composite 层面校验一致性 |
| **延迟升级声明** | 编排器硬编码 `deferredComponents` | composite 的 `deferredSubComponents` 声明式 |
| **依赖关系** | 各组件独立声明 dependencies | 子组件声明 + composite 自动展开 |
| **DAG 构建** | 直接构建 | 先展开 composite 再构建（对控制器透明） |
| **向后兼容** | N/A | 支持混合模式（composite + 独立条目并存） |
| **校验机制** | 逐个校验 | composite 层面集中校验 + 子组件校验 |

## 12. 迁移策略

| 阶段 | 目标 | 说明 | Feature Gate |
|------|------|------|-------------|
| **阶段 1** | 支持 composite 类型 | 实现 CompositeSpec 类型 + DAG 展开 + kubernetesVersion 解析 | `CompositeComponentEnabled` |
| **阶段 2** | 迁移 K8s 核心组件 | ReleaseImage 中 K8s 核心组件从 7 个独立条目迁移为 1 个 composite | 灰度启用 |
| **阶段 3** | 启用 deferredSubComponents | 从硬编码改为声明式延迟升级 | 正式启用 |
| **阶段 4** | 移除独立条目 | ReleaseImage 中不再支持 K8s 核心组件独立声明（必须使用 composite） | 移除 Feature Gate |

```
迁移路径:

阶段 1: 现状（7 个独立条目）
  ReleaseImage:
    install:
      components:
        - name: kube-apiserver    ← 独立条目
        - name: kubelet           ← 独立条目
        - ...

阶段 2: 支持 composite 类型
  ReleaseImage:
    install:
      components:
        - name: kubernetes-core   ← composite 条目 🆕
          type: composite
          subComponents:
            - name: kube-apiserver
            - name: kubelet
            - ...
  Feature Gate: CompositeComponentEnabled ON

阶段 3: 启用 deferredSubComponents
  ReleaseImage:
    upgrade:
      components:
        - name: kubernetes-core
          type: composite
          deferredSubComponents:   ← 声明式延迟 🆕
            - kubelet
          subComponents:
            - ...

阶段 4: 移除独立条目（仅 composite）
  ReleaseImage 中 K8s 核心组件必须使用 composite 声明
  Feature Gate: 移除
```

## 13. 工作量评估

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **CompositeSpec 类型定义** | 类型定义 + deepcopy + CRD schema | 2 |
| **DAG 展开机制** | expandCompositeComponents + BuildDAGFromBundle 适配 | 3 |
| **依赖处理机制** | CompositeMapping + expandCompositeDependencies + mergeDependencies + buildCompositeSubComponentNode | 2 |
| **kubernetesVersion 解析** | GetComponentVersion 扩展 + 版本解析逻辑 | 2 |
| **deferredSubComponents** | 从 ReleaseImage 解析 + 传入 executeControlPlaneHop | 1 |
| **版本校验** | ValidateCompositeVersionConsistency + 名称校验 | 2 |
| **控制器适配** | resolveDeferredComponents + 适配测试 | 2 |
| **集成测试** | composite 展开 + 依赖展开 + 混合模式 + 延迟升级 E2E | 5 |
| **小计** | - | **19 人天** |

## 14. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **DAG 展开遗漏子组件** | 组件未升级 | 低 | 单元测试覆盖展开逻辑 |
| **依赖展开遗漏** | 依赖不完整导致执行顺序错误 | 低 | expandCompositeDependencies 单元测试 + AND 语义去重 |
| **循环依赖** | DAG 构建失败 | 低 | AddDependency 时 hasPath 检测 + TopologicalBatches 校验 |
| **kubernetesVersion 解析错误** | 版本不一致 | 低 | ValidateCompositeVersionConsistency 校验 |
| **混合模式兼容性** | composite + 独立条目冲突 | 中 | 校验逻辑覆盖混合模式 |
| **deferredSubComponents 读取失败** | kubelet 未延迟 | 低 | 默认值 ["kubelet"] 兜底 |
| **向后兼容性** | 旧 ReleaseImage 无 composite 字段 | 低 | omitempty + 独立条目仍支持 |

---

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](kep5/kep5.md)
2. [KEP-9 Static Pod 类型设计](kep9-staticpod-upgrade-framework.md)
3. [KEP-13 二进制组件改造](kep13-binary-component-migration-design.md)
4. [KEP-14 K8s 核心组件迭代式升级](kep14-iterative-upgrade-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **composite 类型** | ComponentVersion 的一种类型，将多个子组件组合为一个条目管理 |
| **kubernetes-core** | composite 类型组件名称，包含 7 个 K8s 核心子组件 |
| **kubernetesVersion** | ReleaseImage 中的统一 K8s 版本声明字段 |
| **deferredSubComponents** | composite 中声明延迟升级的子组件列表 |
| **DAG 展开** | composite 组件在 DAG 构建时自动展开为独立子组件节点 |
| **混合模式** | ReleaseImage 同时包含 composite 类型和独立条目声明的组件 |
