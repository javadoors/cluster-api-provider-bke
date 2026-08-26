# KEP-14: K8s 核心组件迭代式升级方案设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-14 |
| **标题** | K8s 核心组件迭代式升级：版本偏差约束下的控制面与 kubelet 分离升级 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-26 |
| **依赖** | KEP-5 声明式升级框架、KEP-9 Static Pod 类型设计、KEP-13 二进制组件改造 |

---

## 目录

1. [概述](#1-概述)
2. [版本偏差约束](#2-版本偏差约束)
3. [设计方案](#3-设计方案)
4. [偏差门控实现](#4-偏差门控实现)
5. [ClusterVersionReconciler 集成](#5-clusterversionreconciler-集成)
6. [升级顺序总结](#6-升级顺序总结)
7. [优势与适用场景](#7-优势与适用场景)
8. [工作量评估](#8-工作量评估)
9. [风险与缓解](#9-风险与缓解)

---

## 1. 概述

### 1.1 设计背景

K8s 核心组件（kube-apiserver、kube-controller-manager、kube-scheduler、kubelet、kube-proxy）在跨多个小版本升级时（如 v1.34→v1.36），需要满足严格的版本偏差约束。当前设计中 `kubernetes-master` 作为 inline/composite 节点，kubeadm 在一个节点上一次性升级所有控制面组件 + kubelet，带来以下问题：

| 问题 | 说明 | 影响 |
|------|------|------|
| 跨 hop 偏差风险 | Hop 1 升级 apiserver 到 1.35，kubelet 还在 1.34；Hop 2 apiserver 到 1.36 时 kubelet 仍在 1.34，达到 2 版本偏差极限 | 可能导致 kubelet 无法正常工作 |
| 组件间无法独立编排 | apiserver 和 kubelet 被绑定在同一个 DAG 节点中 | 无法分别控制升级节奏 |
| kubelet 升级阻塞控制面 | 大规模集群 kubelet 逐节点 drain 耗时 | 控制面升级被 kubelet 阻塞 |

### 1.2 设计目标

- 将控制面升级（apiserver/cm/scheduler/kube-proxy）和 kubelet 升级拆分为独立的 DAG 节点
- 通过版本偏差约束动态控制执行顺序
- 支持 kubelet 延迟升级策略（控制面先升级，kubelet 批量补充升级）
- 偏差门控确保每个阶段都满足 K8s 版本偏差约束

## 2. 版本偏差约束

### 2.1 K8s 官方偏差规则

| 约束 | 规则 | 影响 |
|------|------|------|
| **kubelet vs apiserver** | kubelet 最多比 apiserver 旧 2 个小版本，不能比 apiserver 新 | kubelet 升级必须滞后于或同步于 apiserver |
| **kube-proxy vs apiserver** | kube-proxy 版本必须与 apiserver 一致 | kube-proxy 必须随 apiserver 同步升级 |
| **controller-manager vs apiserver** | 应与 apiserver 版本一致 | cm 必须紧随 apiserver |
| **scheduler vs apiserver** | 应与 apiserver 版本一致 | scheduler 必须紧随 apiserver |
| **etcd vs apiserver** | 每个 K8s 版本有推荐的 etcd 版本 | etcd 需要与 K8s 版本配套 |

### 2.2 偏差约束声明

```go
// pkg/upgrade/skew_constraints.go

// SkewConstraint 版本偏差约束定义
type SkewConstraint struct {
    // 被约束的组件名称
    Component string
    // 参照组件名称
    ReferenceComponent string
    // 最大版本偏差（被约束组件最多比参照组件旧几个小版本）
    // 0 表示必须版本一致
    MaxSkewBehind int
    // 是否要求版本完全一致（MaxSkewBehind=0 的语法糖）
    MustMatch bool
}

// K8sSkewConstraints K8s 标准版本偏差约束
var K8sSkewConstraints = []SkewConstraint{
    {
        Component:         "kubelet",
        ReferenceComponent: "kube-apiserver",
        MaxSkewBehind:     2,
    },
    {
        Component:         "kube-proxy",
        ReferenceComponent: "kube-apiserver",
        MustMatch:         true,
    },
    {
        Component:         "kube-controller-manager",
        ReferenceComponent: "kube-apiserver",
        MustMatch:         true,
    },
    {
        Component:         "kube-scheduler",
        ReferenceComponent: "kube-apiserver",
        MustMatch:         true,
    },
}
```

### 2.3 偏差计算

```go
// computeMinorVersionSkew 计算两个版本的小版本偏差
// 返回值: 正数表示 reference 比 component 新 N 个小版本
//         负数表示 component 比 reference 新（违反约束）
//         0 表示版本一致
func computeMinorVersionSkew(reference, component string) int {
    refMinor := parseMinorVersion(reference)   // "1.36" → 36
    compMinor := parseMinorVersion(component)   // "1.34" → 34
    return refMinor - compMinor
}

// parseMinorVersion 从版本号中提取小版本号
// "v1.36.0" → 36, "v1.34.2" → 34
func parseMinorVersion(version string) int {
    // 去除 v 前缀
    v := strings.TrimPrefix(version, "v")
    parts := strings.Split(v, ".")
    if len(parts) < 2 {
        return 0
    }
    minor, _ := strconv.Atoi(parts[1])
    return minor
}
```

## 3. 设计方案

### 3.1 核心思路

**将控制面升级和 kubelet 升级拆分为独立的 DAG 节点，通过版本偏差约束动态控制执行顺序。**

```
传统方式（kubeadm 黑盒）:
  Hop 1: kubeadm upgrade → apiserver + cm + scheduler + kubelet 全部升级
  Hop 2: kubeadm upgrade → apiserver + cm + scheduler + kubelet 全部升级

本方案（分离升级 + 偏差门控）:
  Hop 1: 控制面升级 → apiserver + cm + scheduler + kube-proxy（不含 kubelet）
  偏差门 1: kubelet(1.34) vs apiserver(1.35) → 1 偏差 → ✅ 通过
  Hop 2: 控制面升级 → apiserver + cm + scheduler + kube-proxy（不含 kubelet）
  偏差门 2: kubelet(1.34) vs apiserver(1.36) → 2 偏差 → ⚠️ 极限，必须升级 kubelet
  Kubelet 补充升级: 1.34→1.35→1.36（逐节点 drain → replace → uncordon）
  最终验证: 0 偏差 → ✅ 升级完成
```

### 3.2 单 hop 内的升级顺序

每个 hop 内严格执行以下顺序（通过 DAG dependencies 保证）：

```
单 hop 升级 DAG (K8s v1.34 → v1.35):

Batch 1: [etcd]                        ← 先升级 etcd（数据存储）
    └─ StaticPodInstaller

Batch 2: [kube-apiserver]             ← 升级 apiserver（控制面入口）
    └─ StaticPodInstaller

Batch 3: [kube-controller-manager,    ← 跟随 apiserver（并行）
          kube-scheduler]
    ├─ StaticPodInstaller
    └─ StaticPodInstaller

Batch 4: [kube-proxy]                 ← 匹配 apiserver 版本
    └─ YamlInstaller Apply

Batch 5: [kubectl]                    ← 命令行工具
    └─ BinaryInstaller

─── kubelet 不在此 hop 中升级（延迟到偏差门后） ───
```

### 3.3 多 hop 间的偏差门控

在 hop 之间增加**偏差验证门**，决定是否可以继续下一个 hop：

```
Hop 1: K8s v1.34 → v1.35
  ├─ apiserver: v1.34 → v1.35
  ├─ cm/scheduler: v1.34 → v1.35
  ├─ kube-proxy: v1.34 → v1.35
  └─ kubelet: 保持 v1.34（1 版本偏差，安全）

  偏差门 1: kubelet(v1.34) vs apiserver(v1.35) → 1 偏差 → ✅ 通过

Hop 2: K8s v1.35 → v1.36
  ├─ apiserver: v1.35 → v1.36
  ├─ cm/scheduler: v1.35 → v1.36
  ├─ kube-proxy: v1.35 → v1.36
  └─ kubelet: 保持 v1.34（2 版本偏差，极限，仍安全）

  偏差门 2: kubelet(v1.34) vs apiserver(v1.36) → 2 偏差 → ⚠️ 极限
  强制动作: 触发 kubelet 补充升级 v1.34 → v1.35 → v1.36
```

### 3.4 完整多 hop 升级流程

```
┌─────────────────────────────────────────────────────────────────────────┐
│  ClusterVersionReconciler 编排                                          │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Hop 1: 控制面升级 (K8s v1.34 → v1.35)                          │   │
│  │                                                                 │   │
│  │  Batch 1: [etcd] → v3.5.19 (StaticPod)                         │   │
│  │  Batch 2: [kube-apiserver] → v1.35.0 (StaticPod)               │   │
│  │  Batch 3: [kube-controller-manager, kube-scheduler] → v1.35.0   │   │
│  │  Batch 4: [kube-proxy] → v1.35.0 (YAML)                        │   │
│  │  Batch 5: [kubectl] → v1.35.0 (Binary)                         │   │
│  │  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─   │   │
│  │  kubelet: 保持 v1.34.0（延迟升级）                               │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  偏差门 1: Skew Gate                                            │   │
│  │                                                                 │   │
│  │  检查: kubelet(v1.34) vs apiserver(v1.35) → 1 版本偏差 → ✅     │   │
│  │  检查: kube-proxy(v1.35) == apiserver(v1.35) → ✅               │   │
│  │  检查: cm(v1.35) == apiserver(v1.35) → ✅                       │   │
│  │  检查: scheduler(v1.35) == apiserver(v1.35) → ✅                 │   │
│  │  决策: 允许继续 Hop 2                                            │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Hop 2: 控制面升级 (K8s v1.35 → v1.36)                          │   │
│  │                                                                 │   │
│  │  Batch 1: [etcd] → v3.5.20 (StaticPod)                         │   │
│  │  Batch 2: [kube-apiserver] → v1.36.0 (StaticPod)                │   │
│  │  Batch 3: [kube-controller-manager, kube-scheduler] → v1.36.0   │   │
│  │  Batch 4: [kube-proxy] → v1.36.0 (YAML)                        │   │
│  │  Batch 5: [kubectl] → v1.36.0 (Binary)                         │   │
│  │  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─   │   │
│  │  kubelet: 保持 v1.34.0（仍延迟升级）                             │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  偏差门 2: Skew Gate                                            │   │
│  │                                                                 │   │
│  │  检查: kubelet(v1.34) vs apiserver(v1.36) → 2 版本偏差 → ⚠️ 极限 │   │
│  │  决策: 必须先升级 kubelet，禁止继续下一个 hop（如有）              │   │
│  │  强制动作: 触发 kubelet 补充升级                                  │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Kubelet 补充升级 (v1.34 → v1.35 → v1.36)                      │   │
│  │                                                                 │   │
│  │  Sub-hop A: kubelet v1.34 → v1.35                              │   │
│  │    └─ BinaryInstaller: 逐节点 drain → replace → uncordon       │   │
│  │    偏差检查: kubelet(v1.35) vs apiserver(v1.36) → 1 偏差 → ✅   │   │
│  │                                                                 │   │
│  │  Sub-hop B: kubelet v1.35 → v1.36                              │   │
│  │    └─ BinaryInstaller: 逐节点 drain → replace → uncordon       │   │
│  │    偏差检查: kubelet(v1.36) vs apiserver(v1.36) → 0 偏差 → ✅   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  最终偏差验证                                                    │   │
│  │  检查: kubelet(v1.36) vs apiserver(v1.36) → 0 版本偏差 → ✅     │   │
│  │  检查: kube-proxy(v1.36) == apiserver(v1.36) → ✅               │   │
│  │  检查: etcd(v3.5.20) compatible with K8s v1.36 → ✅             │   │
│  │  升级完成                                                        │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
```

### 3.5 kubelet 延迟升级策略

对于大规模集群，kubelet 逐节点 drain 升级耗时较长。采用**延迟升级策略**：

| 策略 | 说明 | 适用场景 |
|------|------|---------|
| **同步升级** | 每个 hop 内 kubelet 与 apiserver 同步升级 | 小集群（<10 节点），偏差始终为 0 |
| **延迟升级** | 控制面先升级，kubelet 延迟到偏差达到极限时批量升级 | 大集群（>10 节点），减少 drain 次数 |
| **最终升级** | 所有 hop 完成后，kubelet 逐版本补充升级到目标版本 | 与延迟升级配合 |

**延迟升级的版本路径**：

```
2-hop 升级 (v1.34 → v1.36):

同步升级:           延迟升级:
  Hop 1:               Hop 1:
    apiserver 1.35       apiserver 1.35
    kubelet 1.35         kubelet 保持 1.34（偏差 1）
  Hop 2:               Hop 2:
    apiserver 1.36       apiserver 1.36
    kubelet 1.36         kubelet 保持 1.34（偏差 2，极限）
                        补充:
                          kubelet 1.34→1.35
                          kubelet 1.35→1.36

同步升级 drain 次数: 2 轮 × N 节点
延迟升级 drain 次数: 2 轮 × N 节点（但集中在最后，减少中间窗口）
```

## 4. 偏差门控实现

### 4.1 VersionSkewChecker

```go
// pkg/upgrade/skew_checker.go

// VersionSkewChecker 版本偏差检查器
type VersionSkewChecker struct {
    client client.Client
}

// SkewViolation 偏差违反详情
type SkewViolation struct {
    Component     string // 被约束的组件
    Reference     string // 参照组件
    ComponentVer  string // 被约束组件当前版本
    ReferenceVer  string // 参照组件当前版本
    Skew          int    // 偏差值（正=component比reference旧，负=component比reference新）
    Reason        string // 违反原因
}

// CheckSkew 检查版本偏差是否满足约束
// 返回: 通过=true, 违反=false + 违反详情列表
func (c *VersionSkewChecker) CheckSkew(
    vc *VersionContext,
    constraints []SkewConstraint,
) (bool, []SkewViolation) {
    var violations []SkewViolation
    
    for _, constraint := range constraints {
        componentVersion := vc.GetCurrent(constraint.Component)
        referenceVersion := vc.GetCurrent(constraint.ReferenceComponent)
        
        if componentVersion == "" || referenceVersion == "" {
            continue // 版本未知，跳过
        }
        
        skew := computeMinorVersionSkew(referenceVersion, componentVersion)
        
        if skew < 0 {
            // 组件比参照新，违反约束
            violations = append(violations, SkewViolation{
                Component:     constraint.Component,
                Reference:     constraint.ReferenceComponent,
                ComponentVer:  componentVersion,
                ReferenceVer:   referenceVersion,
                Skew:          skew,
                Reason:        "component is newer than reference (not allowed)",
            })
        } else if constraint.MustMatch && skew > 0 {
            // 要求版本一致但不一致
            violations = append(violations, SkewViolation{
                Component:     constraint.Component,
                Reference:     constraint.ReferenceComponent,
                ComponentVer:  componentVersion,
                ReferenceVer:   referenceVersion,
                Skew:          skew,
                Reason:        fmt.Sprintf("version mismatch: %s vs %s (must match)",
                    componentVersion, referenceVersion),
            })
        } else if !constraint.MustMatch && skew > constraint.MaxSkewBehind {
            // 偏差超过限制
            violations = append(violations, SkewViolation{
                Component:     constraint.Component,
                Reference:     constraint.ReferenceComponent,
                ComponentVer:  componentVersion,
                ReferenceVer:  referenceVersion,
                Skew:          skew,
                Reason:        fmt.Sprintf("skew %d exceeds max %d", skew, constraint.MaxSkewBehind),
            })
        }
    }
    
    return len(violations) == 0, violations
}
```

### 4.2 前瞻性偏差检查

```go
// CheckSkewBeforeHop 检查执行下一个 hop 后是否仍满足偏差约束
// 模拟下一个 hop 完成后的版本状态，检查偏差是否超限
func (c *VersionSkewChecker) CheckSkewBeforeHop(
    vc *VersionContext,
    nextHopTargetVersions map[string]string,
) (bool, []SkewViolation) {
    // 模拟下一个 hop 完成后的版本状态
    simulatedVC := vc.Clone()
    for name, version := range nextHopTargetVersions {
        simulatedVC.SetCurrent(name, version)
    }
    
    // 检查模拟状态下的偏差约束
    return c.CheckSkew(simulatedVC, K8sSkewConstraints)
}

// NeedsKubeletCatchup 判断是否需要 kubelet 补充升级
func (c *VersionSkewChecker) NeedsKubeletCatchup(
    vc *VersionContext,
    nextHopTargetVersions map[string]string,
) (bool, int) {
    // 模拟下一个 hop 后的偏差
    simulatedVC := vc.Clone()
    for name, version := range nextHopTargetVersions {
        simulatedVC.SetCurrent(name, version)
    }
    
    kubeletVersion := simulatedVC.GetCurrent("kubelet")
    apiserverVersion := simulatedVC.GetCurrent("kube-apiserver")
    
    if kubeletVersion == "" || apiserverVersion == "" {
        return false, 0
    }
    
    skew := computeMinorVersionSkew(apiserverVersion, kubeletVersion)
    
    // 偏差即将达到极限，需要补充升级
    if skew >= 2 {
        return true, skew
    }
    
    return false, skew
}
```

## 5. ClusterVersionReconciler 集成

### 5.1 多 hop 编排

```go
// controllers/clusterversion/clusterversion_controller.go

func (r *ClusterVersionReconciler) orchestrateMultiHopUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // ["v2.6.0", "v2.6.5", "v2.7.0"]
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    
    for i, hopTarget := range hopPath {
        log.Info("starting hop", "hop", i+1, "target", hopTarget)
        
        // 1. 执行当前 hop（控制面升级，kubelet 延迟）
        if err := r.executeControlPlaneHop(ctx, bkeCluster, hopTarget); err != nil {
            return fmt.Errorf("hop %d (%s) control plane upgrade failed: %w", i+1, hopTarget, err)
        }
        
        // 2. 更新 VersionContext（控制面组件已升级，kubelet 未升级）
        vc := r.getCurrentVersionContext(bkeCluster)
        
        // 3. 偏差门控
        if i < len(hopPath)-1 {
            // 还有下一个 hop
            nextHopVersions := r.resolveHopTargetVersions(hopPath[i+1])
            
            // 前瞻性检查：下一个 hop 后偏差是否超限
            needsCatchup, skew := skewChecker.NeedsKubeletCatchup(vc, nextHopVersions)
            
            if needsCatchup {
                log.Info("skew limit reached, must upgrade kubelet before next hop",
                    "currentSkew", skew, "maxSkew", 2)
                
                // 触发 kubelet 补充升级
                if err := r.upgradeKubeletCatchup(ctx, bkeCluster, vc, hopPath[:i+1]); err != nil {
                    return fmt.Errorf("kubelet catchup upgrade failed: %w", err)
                }
            } else {
                // 偏差在安全范围内，可以继续
                ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
                if !ok {
                    return fmt.Errorf("skew violations after hop %d: %v", i+1, violations)
                }
                log.Info("skew gate passed, continuing to next hop", "hop", i+1)
            }
        } else {
            // 最后一个 hop 完成，执行 kubelet 最终升级
            log.Info("final hop completed, upgrading kubelet to target version")
            if err := r.upgradeKubeletCatchup(ctx, bkeCluster, vc, hopPath); err != nil {
                return fmt.Errorf("final kubelet upgrade failed: %w", err)
            }
        }
        
        // 4. 最终偏差验证
        ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
        if !ok {
            return fmt.Errorf("skew violations after hop %d: %v", i+1, violations)
        }
        
        log.Info("hop completed, skew constraints satisfied",
            "hop", i+1, "version", hopTarget)
    }
    
    return nil
}
```

### 5.2 控制面 hop 执行

```go
// executeControlPlaneHop 执行单个 hop 的控制面升级（不含 kubelet）
func (r *ClusterVersionReconciler) executeControlPlaneHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopTarget string,
) error {
    // 1. 解析目标版本 ReleaseImage
    bundle, err := r.resolveReleaseBundle(ctx, hopTarget)
    if err != nil {
        return err
    }
    
    // 2. 构建 VersionContext（仅控制面组件的 Target）
    vc := upgrade.BuildVersionContextForUpgrade(bundle, currentBundle, bkeCluster)
    
    // 3. 从升级列表中排除 kubelet（延迟升级）
    //    通过设置 kubelet 的 Target = Current 实现（VersionContext 判定 current == target → Skip）
    kubeletCurrent := vc.GetCurrent("kubelet")
    if kubeletCurrent != "" {
        vc.SetTarget("kubelet", kubeletCurrent) // 保持不变，跳过
    }
    
    // 4. 构建 DAG（控制面组件 + kube-proxy + kubectl，不含 kubelet）
    dag, err := upgrade.BuildDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return err
    }
    
    // 5. 执行 DAG
    sched := r.buildScheduler(bundle, vc)
    execCtx := r.buildExecutionContext(ctx, bkeCluster, vc)
    
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return fmt.Errorf("execute control plane DAG: %w", err)
    }
    
    // 6. 更新控制面组件版本状态
    bkeCluster.Status.KubernetesVersion = vc.GetTarget("kube-apiserver")
    bkeCluster.Status.EtcdVersion = vc.GetTarget("etcd")
    // 注意: 不更新 kubelet 版本（延迟升级）
    
    return nil
}
```

### 5.3 kubelet 补充升级

```go
// upgradeKubeletCatchup kubelet 补充升级（从当前版本逐版本升级到目标版本）
func (r *ClusterVersionReconciler) upgradeKubeletCatchup(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    vc *upgrade.VersionContext,
    completedHops []string,
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    
    currentKubelet := vc.GetCurrent("kubelet")
    targetKubelet := vc.GetTarget("kubelet")
    
    log.Info("starting kubelet catchup upgrade",
        "from", currentKubelet, "to", targetKubelet)
    
    // 1. 计算需要经过的中间版本
    intermediateVersions := computeIntermediateVersions(currentKubelet, targetKubelet)
    // 例如: v1.34.0 → v1.36.0 → ["v1.35.0", "v1.36.0"]
    
    for _, version := range intermediateVersions {
        log.Info("upgrading kubelet to intermediate version",
            "from", currentKubelet, "to", version)
        
        // 2. 通过 BinaryInstaller 执行 kubelet 升级
        //    逐节点 drain → replace binary → restart → health check → uncordon
        if err := r.executeKubeletUpgrade(ctx, bkeCluster, version); err != nil {
            return fmt.Errorf("kubelet upgrade to %s failed: %w", version, err)
        }
        
        // 3. 更新 VersionContext
        currentKubelet = version
        vc.SetCurrent("kubelet", version)
        
        // 4. 每次中间版本升级后验证偏差
        ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
        if !ok {
            return fmt.Errorf("skew violation during kubelet catchup at %s: %v",
                version, violations)
        }
        
        log.Info("kubelet upgraded to intermediate version, skew check passed",
            "version", version)
    }
    
    // 5. 更新 kubelet 版本状态
    bkeCluster.Status.KubeletVersion = targetKubelet
    
    log.Info("kubelet catchup upgrade completed", "final", targetKubelet)
    return nil
}

// computeIntermediateVersions 计算从 currentVersion 到 targetVersion 的中间版本列表
// 例如: ("v1.34.0", "v1.36.0") → ["v1.35.0", "v1.36.0"]
func computeIntermediateVersions(currentVersion, targetVersion string) []string {
    currentMinor := parseMinorVersion(currentVersion)
    targetMinor := parseMinorVersion(targetVersion)
    
    var versions []string
    for minor := currentMinor + 1; minor <= targetMinor; minor++ {
        versions = append(versions, fmt.Sprintf("v1.%d.0", minor))
    }
    
    return versions
}

// executeKubeletUpgrade 执行 kubelet 到指定版本的升级
func (r *ClusterVersionReconciler) executeKubeletUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) error {
    // 1. 加载 kubelet ComponentVersion
    cv, err := r.CVStore.GetComponentVersion(ctx, "kubelet", targetVersion)
    if err != nil {
        return fmt.Errorf("get kubelet component version %s: %w", targetVersion, err)
    }
    
    // 2. 构建 BinaryInstaller 选项
    opts := binaryinstaller.InstallOptions{
        Component:  cv,
        Action:     binaryinstaller.BinaryActionUpgrade,
        TemplateCtx: r.buildTemplateContext(bkeCluster),
    }
    
    // 3. 获取所有节点
    bkeNodes, err := r.NodeFetcher().GetBKENodesWrapperForCluster(ctx, bkeCluster)
    if err != nil {
        return err
    }
    
    allNodes := bkeNodes.ToNodes()
    
    // 4. 创建 drainer
    drainer := phaseutil.NewDrainer(true, true, true, 20*time.Second)
    
    var failedNodes []string
    
    // 5. 逐节点升级（Master 阻塞式，Worker 非阻塞式）
    for _, node := range allNodes {
        // 5a. drain 节点
        if err := drainer.Drain(ctx, node.Hostname); err != nil {
            log.Error(err, "drain failed", "node", node.IP)
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("drain master node %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5b. 设置目标节点 IP
        opts.TemplateCtx.NodeIP = node.IP
        
        // 5c. 执行 BinaryInstaller 安装（升级）
        if err := r.BinaryInstaller.Install(ctx, opts); err != nil {
            log.Error(err, "kubelet upgrade failed", "node", node.IP)
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("kubelet upgrade on master %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5d. 等待节点健康检查
        if err := waitForNodeHealthCheck(ctx, r.Client, bkeCluster, node, targetVersion); err != nil {
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("health check on master %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5e. uncordon 节点
        _ = uncordonNode(ctx, r.Client, bkeCluster, node.Hostname)
        
        log.Info("kubelet upgraded", "node", node.IP, "version", targetVersion)
    }
    
    if len(failedNodes) > 0 {
        log.Info("kubelet upgrade completed with failures", "failedNodes", failedNodes)
        // Worker 节点失败不阻塞，Reconcile 重试时跳过已升级节点
    }
    
    return nil
}
```

## 6. 升级顺序总结

| 阶段 | 组件 | 升级方向 | 偏差状态 | 说明 |
|------|------|---------|---------|------|
| Hop 1 - 控制面 | etcd | v3.5.18→v3.5.19 | - | 先升级数据存储 |
| Hop 1 - 控制面 | apiserver | v1.34→v1.35 | - | 控制面入口 |
| Hop 1 - 控制面 | cm/scheduler | v1.34→v1.35 | cm/scheduler == apiserver | 紧随 apiserver |
| Hop 1 - 控制面 | kube-proxy | v1.34→v1.35 | proxy == apiserver | 匹配 apiserver |
| Hop 1 - 控制面 | kubelet | **保持 v1.34** | kubelet vs apiserver = 1 偏差 | **延迟升级** |
| **偏差门 1** | - | - | 1 偏差，✅ 通过 | 可继续 Hop 2 |
| Hop 2 - 控制面 | apiserver | v1.35→v1.36 | - | 第二跳 |
| Hop 2 - 控制面 | cm/scheduler | v1.35→v1.36 | cm/scheduler == apiserver | 紧随 apiserver |
| Hop 2 - 控制面 | kube-proxy | v1.35→v1.36 | proxy == apiserver | 匹配 apiserver |
| Hop 2 - 控制面 | kubelet | **仍保持 v1.34** | kubelet vs apiserver = 2 偏差 | **达到极限** |
| **偏差门 2** | - | - | 2 偏差，⚠️ 极限 | **必须升级 kubelet** |
| Kubelet 补充 | kubelet | v1.34→v1.35 | 1 偏差 → 0 偏差 | 逐节点 drain 升级 |
| Kubelet 补充 | kubelet | v1.35→v1.36 | 1 偏差 → 0 偏差 | 逐节点 drain 升级 |
| **最终验证** | - | - | 0 偏差，✅ 通过 | 升级完成 |

## 7. 优势与适用场景

### 7.1 优势

| 优势 | 说明 |
|------|------|
| **控制面快速升级** | apiserver/cm/scheduler 不等 kubelet，快速完成多 hop |
| **kubelet 延迟升级** | 大规模集群中 kubelet 逐节点 drain 耗时，延迟到偏差极限时批量升级 |
| **偏差安全** | 偏差门控确保每个阶段都满足 K8s 版本偏差约束 |
| **可观测** | 每个组件独立追踪版本，偏差状态清晰可见 |
| **灵活** | 小集群可选择每 hop 都升级 kubelet（0 偏差），大集群选择延迟升级 |
| **幂等** | kubelet 补充升级跳过已升级节点，支持 Reconcile 重入 |

### 7.2 适用场景

| 场景 | 推荐策略 | 说明 |
|------|---------|------|
| **小集群（<10 节点）** | 同步升级 | 每个 hop 内 kubelet 与 apiserver 同步升级，偏差始终为 0 |
| **中等集群（10-50 节点）** | 延迟 1 hop | 第 1 hop 延迟 kubelet，第 2 hop 前补充升级 |
| **大集群（>50 节点）** | 延迟到极限 | kubelet 延迟到偏差达到 2（极限），然后批量补充升级 |
| **跨 3+ hop 升级** | 必须延迟 | 无法一次升级 kubelet，必须逐版本补充 |

### 7.3 策略配置

```go
// KubeletUpgradeStrategy kubelet 升级策略
type KubeletUpgradeStrategy string

const (
    // KubeletStrategySync 每个 hop 内同步升级 kubelet
    KubeletStrategySync KubeletUpgradeStrategy = "Sync"
    
    // KubeletStrategyDeferred 延迟到偏差极限时升级
    KubeletStrategyDeferred KubeletUpgradeStrategy = "Deferred"
)

// UpgradeOrchestrationConfig 升级编排配置
type UpgradeOrchestrationConfig struct {
    // kubelet 升级策略
    KubeletStrategy KubeletUpgradeStrategy
    
    // 最大允许偏差（默认 2，与 K8s 官方一致）
    MaxKubeletSkew int
    
    // kubelet 补充升级的批次大小（每批 drain 多少节点）
    KubeletBatchSize int
}
```

## 8. 工作量评估

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **VersionSkewChecker** | 偏差约束定义 + 偏差计算 + 检查逻辑 | 3 |
| **前瞻性偏差检查** | CheckSkewBeforeHop + NeedsKubeletCatchup | 2 |
| **ClusterVersionReconciler 集成** | 多 hop 编排 + 偏差门控 + kubelet 补充升级触发 | 4 |
| **executeControlPlaneHop** | 控制面独立升级（排除 kubelet） | 3 |
| **upgradeKubeletCatchup** | kubelet 逐版本补充升级 + drain/uncordon | 4 |
| **computeIntermediateVersions** | 中间版本计算 + 版本路径规划 | 1 |
| **偏差门控日志 + 事件** | 偏差状态事件 + Prometheus 指标 | 2 |
| **策略配置** | UpgradeOrchestrationConfig + Feature Gate | 1 |
| **集成测试** | 2-hop 升级 + 延迟 kubelet + 偏差门控 + 补充升级 E2E | 5 |
| **小计** | - | **25 人天** |

## 9. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **偏差计算错误** | kubelet 无法注册到 apiserver | 低 | 单元测试覆盖版本解析 + 偏差计算 |
| **kubelet 补充升级超时** | 节点长时间 NotReady | 中 | 滚动升级 + 批次大小控制 + 超时回滚 |
| **控制面升级期间 kubelet 版本过低** | 部分 API 不兼容 | 中 | 偏差门控确保不超过 2 版本偏差 |
| **中间版本不存在** | kubelet 补充升级找不到制品 | 低 | 中间版本必须是已发布版本 |
| **节点 drain 失败** | Pod 无法驱逐 | 中 | 强制 drain + 超时 + Continue 策略 |
| **偏差门误判** | 允许继续或阻塞升级 | 低 | 前瞻性检查 + 实际状态双重验证 |

---

## 附录

### A. 参考文档

1. [Kubernetes Version Skew Policy](https://kubernetes.io/releases/version-skew-policy/)
2. [KEP-5 声明式升级框架](kep5/kep5.md)
3. [KEP-9 Static Pod 类型设计](kep9-staticpod-upgrade-framework.md)
4. [KEP-13 二进制组件改造](kep13-binary-component-migration-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **版本偏差** | 两个组件之间的小版本号差值，如 apiserver v1.36 vs kubelet v1.34 = 2 偏差 |
| **偏差门控** | 在 hop 之间检查版本偏差是否满足约束，决定是否继续或触发补充升级 |
| **延迟升级** | kubelet 不随控制面同步升级，延迟到偏差达到极限时批量升级 |
| **补充升级** | kubelet 从当前版本逐版本升级到目标版本的过程 |
| **中间版本** | 从当前版本到目标版本之间的 K8s 小版本，如 v1.34→v1.36 的中间版本是 v1.35 |
| **SkewConstraint** | 版本偏差约束定义，声明组件间的最大允许偏差 |
