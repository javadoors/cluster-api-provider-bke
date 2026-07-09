# KEP-6 集群健康状态计算器设计文档

**文档版本**: v1.0  
**状态**: Draft  
**创建日期**: 2026-07-09  
**依赖**: KEP-6 声明式升级方案

---

## 目录

1. [概述](#1-概述)
2. [问题分析](#2-问题分析)
3. [设计方案](#3-设计方案)
4. [核心组件设计](#4-核心组件设计)
5. [实现细节](#5-实现细节)
6. [状态转换图](#6-状态转换图)
7. [实施步骤](#7-实施步骤)
8. [测试设计](#8-测试设计)
9. [DeclarativeUpgrade 字段分析](#9-declarativeupgrade-字段分析)

---

## 1. 概述

### 1.1 设计目标

本文档提供基于组件状态的集群健康状态计算方案，引入 `ClusterHealthCalculator` 和 `ComponentHealthChecker`，实现真正的健康状态评估，替代当前基于操作类型的简单状态设置。

### 1.2 设计范围

| 范围 | 说明 |
|------|------|
| 健康检查器 | ClusterHealthCalculator、ComponentHealthChecker 接口设计 |
| 组件检查 | 节点、Helm Release、YAML 资源健康检查 |
| 状态聚合 | 基于组件状态的健康状态聚合算法 |
| 集成 | 与 BKEClusterReconciler 集成 |

### 1.3 设计约束

| 约束 | 说明 |
|------|------|
| 向后兼容 | 保留现有 ClusterHealthState 枚举值 |
| 性能 | 健康检查不应阻塞 Reconcile 流程 |
| 可观测性 | 提供健康检查的详细日志和指标 |

---

## 2. 问题分析

### 2.1 当前实现的问题

#### 2.1.1 基于操作类型而非实际健康状态

**当前逻辑**（`bkecluster_controller.go:757-775`）：

```go
func (r *BKEClusterReconciler) setClusterHealthStatus(
    bkeCluster *bkev1beta1.BKECluster, 
    flags ClusterHealthStatusFlags,
) {
    // 首次部署设置为正在部署
    if flags.DeployFlag || flags.DeployFailedFlag {
        markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Deploying)
    }
    // 需要升级集群设置为正在升级
    if flags.UpgradeFlag || flags.UpgradeFailedFlag {
        markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Upgrading)
    }
    // 需要纳管集群设置为正在纳管
    if flags.ManageFlag || flags.ManageFailedFlag {
        markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Managing)
    }
    // 删除集群
    if phaseutil.IsDeleteOrReset(bkeCluster) {
        markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Deleting)
    }
}
```

**问题**：
- ❌ 基于操作类型（Deploy/Upgrade/Manage）而非实际健康状态
- ❌ 无法反映部分组件健康、部分组件不健康的情况
- ❌ 无法区分"正在升级"和"升级失败但部分成功"
- ❌ 没有考虑 DAG 调度中的组件级状态

#### 2.1.2 标志位计算过于简单

**当前逻辑**（`bkecluster_controller.go:620-641`）：

```go
func (r *BKEClusterReconciler) getNodeFlags(
    ctx context.Context, 
    bkeCluster *bkev1beta1.BKECluster,
) (bool, bool, bool) {
    // 是否是初次部署
    deployFlag := nodeCount == 0
    
    // 是否需要升级集群
    upgradeFlag := phaseutil.GetNeedUpgradeNodesWithBKENodes(bkeCluster, bkeNodes).Length() > 0
    
    // 是否需要纳管集群
    manageFlag := clusterutil.IsBocloudCluster(bkeCluster) && !clusterutil.FullyControlled(bkeCluster)
    
    return deployFlag, upgradeFlag, manageFlag
}
```

**问题**：
- ❌ `deployFlag` 仅基于节点数量，不考虑组件状态
- ❌ `upgradeFlag` 仅检查是否需要升级，不考虑升级进度
- ❌ 没有考虑 Helm/YAML 组件的健康状态

#### 2.1.3 健康状态设置时机不当

**当前逻辑**：
- 在 `initNodeStatus()` 中设置（`bkecluster_controller.go:496`）
- 在 Phase 执行前设置，无法反映执行后的实际状态

**问题**：
- ❌ 无法反映 DAG 执行后的实际健康状态
- ❌ 无法反映组件级健康检查结果

### 2.2 需要解决的问题

1. **如何检查节点健康状态？**
   - 检查 NodeState（Ready/Failed/Upgrading）
   - 检查 StateCode 标志位
   - 检查 bkeagent 连接状态

2. **如何检查 Helm Release 健康状态？**
   - 检查 Release.Status（Deployed/Failed/PendingInstall）
   - 检查 Pod 状态（Ready/Running）
   - 检查 Deployment 状态（Available/Progressing）

3. **如何检查 YAML 资源健康状态？**
   - 检查资源状态（Available/Progressing/Degraded）
   - 检查 Pod 状态
   - 检查 Endpoint 状态

4. **如何聚合组件健康状态？**
   - 全部健康 → Healthy
   - 部分失败 → Unhealthy
   - 部分进行中 → Upgrading/Deploying
   - 未知状态 → Unknown

---

## 3. 设计方案

### 3.1 架构设计

```
┌─────────────────────────────────────────────────────────────────┐
│                    BKEClusterReconciler                          │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             │ 调用
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                ClusterHealthCalculator                           │
│  - CalculateHealthState(ctx, bkeCluster)                        │
│  - checkNodeHealth(ctx, nodes)                                  │
│  - checkHelmHealth(ctx, releases)                               │
│  - checkYAMLHealth(ctx, resources)                              │
│  - aggregateResults(results)                                    │
└────────────────────────────┬────────────────────────────────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
              ▼              ▼              ▼
┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐
│ NodeHealthChecker│ │HelmHealthChecker │ │YAMLHealthChecker │
│  - CheckNode()   │ │ - CheckRelease() │ │ - CheckResource()│
└──────────────────┘ └──────────────────┘ └──────────────────┘
```

### 3.2 核心接口设计

```go
// ClusterHealthCalculator 集群健康状态计算器
type ClusterHealthCalculator interface {
    // CalculateHealthState 计算集群健康状态
    CalculateHealthState(
        ctx context.Context,
        bkeCluster *bkev1beta1.BKECluster,
    ) (confv1beta1.ClusterHealthState, error)
}

// ComponentHealthChecker 组件健康检查器
type ComponentHealthChecker interface {
    // CheckHealth 检查组件健康状态
    CheckHealth(ctx context.Context) (*HealthCheckResult, error)
}

// HealthCheckResult 健康检查结果
type HealthCheckResult struct {
    Component string
    Healthy   bool
    Status    string
    Message   string
    Error     error
}
```

### 3.3 健康状态计算流程

```
1. 检查是否正在删除
   └─ if IsDeleteOrReset(): return Deleting

2. 检查声明式升级状态
   └─ if DeclarativeUpgrade != nil:
        ├─ if FinishedAt == nil && LastError != "": return UpgradeFailed
        ├─ if FinishedAt == nil: return Upgrading
        └─ if FinishedAt != nil && LastError == "": return Healthy

3. 检查组件健康状态
   ├─ 检查所有节点状态
   ├─ 检查所有 Helm Release 状态
   └─ 检查所有 YAML 资源状态

4. 聚合健康状态
   ├─ if AllHealthy: return Healthy
   ├─ if AnyFailed: return Unhealthy
   ├─ if AnyUpgrading: return Upgrading
   ├─ if AnyDeploying: return Deploying
   └─ else: return Unknown
```

---

## 4. 核心组件设计

### 4.1 ClusterHealthCalculator

```go
// pkg/healthcheck/calculator.go

package healthcheck

import (
    "context"
    "fmt"

    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterHealthCalculator 集群健康状态计算器
type ClusterHealthCalculator struct {
    client      client.Client
    nodeChecker *NodeHealthChecker
    helmChecker *HelmHealthChecker
    yamlChecker *YAMLHealthChecker
}

// NewClusterHealthCalculator 创建健康状态计算器
func NewClusterHealthCalculator(client client.Client) *ClusterHealthCalculator {
    return &ClusterHealthCalculator{
        client:      client,
        nodeChecker: NewNodeHealthChecker(client),
        helmChecker: NewHelmHealthChecker(client),
        yamlChecker: NewYAMLHealthChecker(client),
    }
}

// CalculateHealthState 计算集群健康状态
func (c *ClusterHealthCalculator) CalculateHealthState(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) (bkev1beta1.ClusterHealthState, error) {
    
    // 1. 检查是否正在删除
    if phaseutil.IsDeleteOrReset(bkeCluster) {
        return bkev1beta1.Deleting, nil
    }
    
    // 2. 检查声明式升级状态
    if healthState, shouldReturn := c.checkDeclarativeUpgrade(bkeCluster); shouldReturn {
        return healthState, nil
    }
    
    // 3. 检查组件健康状态
    results, err := c.checkAllComponents(ctx, bkeCluster)
    if err != nil {
        return bkev1beta1.Unhealthy, err
    }
    
    // 4. 聚合健康状态
    return c.aggregateResults(results), nil
}

// checkDeclarativeUpgrade 检查声明式升级状态
func (c *ClusterHealthCalculator) checkDeclarativeUpgrade(
    bkeCluster *bkev1beta1.BKECluster,
) (bkev1beta1.ClusterHealthState, bool) {
    
    if bkeCluster.Status.DeclarativeUpgrade == nil {
        return "", false
    }
    
    upgrade := bkeCluster.Status.DeclarativeUpgrade
    
    // 升级进行中
    if upgrade.FinishedAt == nil {
        if upgrade.LastError != "" {
            return bkev1beta1.UpgradeFailed, true
        }
        return bkev1beta1.Upgrading, true
    }
    
    // 升级完成
    if upgrade.LastError == "" {
        return bkev1beta1.Healthy, true
    }
    
    return "", false
}

// checkAllComponents 检查所有组件健康状态
func (c *ClusterHealthCalculator) checkAllComponents(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) ([]*HealthCheckResult, error) {
    
    results := make([]*HealthCheckResult, 0)
    
    // 1. 检查节点状态
    nodeResults, err := c.nodeChecker.CheckAllNodes(ctx, bkeCluster)
    if err != nil {
        return nil, fmt.Errorf("check nodes: %w", err)
    }
    results = append(results, nodeResults...)
    
    // 2. 检查 Helm Release 状态
    helmResults, err := c.helmChecker.CheckAllReleases(ctx, bkeCluster)
    if err != nil {
        return nil, fmt.Errorf("check helm releases: %w", err)
    }
    results = append(results, helmResults...)
    
    // 3. 检查 YAML 资源状态
    yamlResults, err := c.yamlChecker.CheckAllResources(ctx, bkeCluster)
    if err != nil {
        return nil, fmt.Errorf("check yaml resources: %w", err)
    }
    results = append(results, yamlResults...)
    
    return results, nil
}

// aggregateResults 聚合健康检查结果
func (c *ClusterHealthCalculator) aggregateResults(
    results []*HealthCheckResult,
) bkev1beta1.ClusterHealthState {
    
    if len(results) == 0 {
        return bkev1beta1.Unknown
    }
    
    allHealthy := true
    anyFailed := false
    anyUpgrading := false
    anyDeploying := false
    
    for _, result := range results {
        if !result.Healthy {
            allHealthy = false
            
            if result.Error != nil {
                anyFailed = true
            }
            
            if result.Status == "Upgrading" {
                anyUpgrading = true
            }
            
            if result.Status == "Deploying" {
                anyDeploying = true
            }
        }
    }
    
    if allHealthy {
        return bkev1beta1.Healthy
    }
    
    if anyFailed {
        return bkev1beta1.Unhealthy
    }
    
    if anyUpgrading {
        return bkev1beta1.Upgrading
    }
    
    if anyDeploying {
        return bkev1beta1.Deploying
    }
    
    return bkev1beta1.Unknown
}
```

### 4.2 NodeHealthChecker

```go
// pkg/healthcheck/node_checker.go

package healthcheck

import (
    "context"
    "fmt"

    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// NodeHealthChecker 节点健康检查器
type NodeHealthChecker struct {
    client client.Client
}

// NewNodeHealthChecker 创建节点健康检查器
func NewNodeHealthChecker(client client.Client) *NodeHealthChecker {
    return &NodeHealthChecker{client: client}
}

// CheckAllNodes 检查所有节点健康状态
func (c *NodeHealthChecker) CheckAllNodes(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) ([]*HealthCheckResult, error) {
    
    // 获取所有节点
    bkeNodes, err := phaseutil.GetBKENodesWrapperForCluster(ctx, bkeCluster)
    if err != nil {
        return nil, fmt.Errorf("get nodes: %w", err)
    }
    
    results := make([]*HealthCheckResult, 0, len(bkeNodes))
    
    for _, node := range bkeNodes {
        result := c.checkNode(node)
        results = append(results, result)
    }
    
    return results, nil
}

// checkNode 检查单个节点健康状态
func (c *NodeHealthChecker) checkNode(node *bkev1beta1.BKENode) *HealthCheckResult {
    result := &HealthCheckResult{
        Component: fmt.Sprintf("node/%s", node.Spec.IP),
    }
    
    // 检查节点状态
    switch node.Status.State {
    case bkev1beta1.NodeReady:
        result.Healthy = true
        result.Status = "Ready"
        result.Message = "Node is ready"
        
    case bkev1beta1.NodeFailed:
        result.Healthy = false
        result.Status = "Failed"
        result.Message = node.Status.Message
        result.Error = fmt.Errorf("node failed: %s", node.Status.Message)
        
    case bkev1beta1.NodeUpgrading:
        result.Healthy = false
        result.Status = "Upgrading"
        result.Message = "Node is upgrading"
        
    case bkev1beta1.NodeBootStrapping:
        result.Healthy = false
        result.Status = "Deploying"
        result.Message = "Node is bootstrapping"
        
    case bkev1beta1.NodeNotReady:
        result.Healthy = false
        result.Status = "NotReady"
        result.Message = "Node is not ready"
        result.Error = fmt.Errorf("node not ready")
        
    default:
        result.Healthy = false
        result.Status = "Unknown"
        result.Message = fmt.Sprintf("Unknown state: %s", node.Status.State)
        result.Error = fmt.Errorf("unknown node state: %s", node.Status.State)
    }
    
    // 检查 StateCode 标志位
    if node.Status.StateCode&bkev1beta1.NodeFailedFlag != 0 {
        result.Healthy = false
        result.Status = "Failed"
        result.Message = "Node has failed flag"
        result.Error = fmt.Errorf("node has failed flag")
    }
    
    if node.Status.StateCode&bkev1beta1.NodeDeletingFlag != 0 {
        result.Healthy = false
        result.Status = "Deleting"
        result.Message = "Node is deleting"
    }
    
    return result
}
```

### 4.3 HelmHealthChecker

```go
// pkg/healthcheck/helm_checker.go

package healthcheck

import (
    "context"
    "fmt"

    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
    "helm.sh/helm/v3/pkg/action"
    "helm.sh/helm/v3/pkg/cli"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// HelmHealthChecker Helm Release 健康检查器
type HelmHealthChecker struct {
    client client.Client
}

// NewHelmHealthChecker 创建 Helm 健康检查器
func NewHelmHealthChecker(client client.Client) *HelmHealthChecker {
    return &HelmHealthChecker{client: client}
}

// CheckAllReleases 检查所有 Helm Release 健康状态
func (c *HelmHealthChecker) CheckAllReleases(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) ([]*HealthCheckResult, error) {
    
    results := make([]*HealthCheckResult, 0)
    
    // 检查所有 addon
    for _, addon := range bkeCluster.Spec.ClusterConfig.Cluster.Addons {
        if addon.Type != "chart" {
            continue
        }
        
        result, err := c.checkRelease(ctx, addon)
        if err != nil {
            return nil, fmt.Errorf("check release %s: %w", addon.Name, err)
        }
        
        results = append(results, result)
    }
    
    return results, nil
}

// checkRelease 检查单个 Helm Release 健康状态
func (c *HelmHealthChecker) checkRelease(
    ctx context.Context,
    addon bkev1beta1.Product,
) (*HealthCheckResult, error) {
    
    result := &HealthCheckResult{
        Component: fmt.Sprintf("helm/%s", addon.ReleaseName),
    }
    
    // 初始化 Helm action configuration
    settings := cli.New()
    actionConfig := new(action.Configuration)
    
    namespace := addon.Namespace
    if namespace == "" {
        namespace = "default"
    }
    
    if err := actionConfig.Init(
        settings.RESTClientGetter(),
        namespace,
        "secret",
        func(format string, v ...interface{}) {},
    ); err != nil {
        return nil, fmt.Errorf("init action config: %w", err)
    }
    
    // 获取 Release 状态
    statusClient := action.NewStatus(actionConfig)
    release, err := statusClient.Run(addon.ReleaseName)
    if err != nil {
        result.Healthy = false
        result.Status = "NotFound"
        result.Message = fmt.Sprintf("Release not found: %v", err)
        result.Error = err
        return result, nil
    }
    
    // 检查 Release 状态
    switch release.Info.Status {
    case "deployed":
        result.Healthy = true
        result.Status = "Deployed"
        result.Message = "Release is deployed"
        
    case "failed":
        result.Healthy = false
        result.Status = "Failed"
        result.Message = release.Info.Description
        result.Error = fmt.Errorf("release failed: %s", release.Info.Description)
        
    case "pending-install", "pending-upgrade", "pending-rollback":
        result.Healthy = false
        result.Status = "Pending"
        result.Message = fmt.Sprintf("Release is %s", release.Info.Status)
        
    case "uninstalling":
        result.Healthy = false
        result.Status = "Uninstalling"
        result.Message = "Release is uninstalling"
        
    case "superseded":
        result.Healthy = false
        result.Status = "Superseded"
        result.Message = "Release is superseded"
        
    case "uninstalled":
        result.Healthy = false
        result.Status = "Uninstalled"
        result.Message = "Release is uninstalled"
        
    default:
        result.Healthy = false
        result.Status = "Unknown"
        result.Message = fmt.Sprintf("Unknown status: %s", release.Info.Status)
        result.Error = fmt.Errorf("unknown release status: %s", release.Info.Status)
    }
    
    return result, nil
}
```

### 4.4 YAMLHealthChecker

```go
// pkg/healthcheck/yaml_checker.go

package healthcheck

import (
    "context"
    "fmt"

    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/apimachinery/pkg/types"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// YAMLHealthChecker YAML 资源健康检查器
type YAMLHealthChecker struct {
    client client.Client
}

// NewYAMLHealthChecker 创建 YAML 健康检查器
func NewYAMLHealthChecker(client client.Client) *YAMLHealthChecker {
    return &YAMLHealthChecker{client: client}
}

// CheckAllResources 检查所有 YAML 资源健康状态
func (c *YAMLHealthChecker) CheckAllResources(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) ([]*HealthCheckResult, error) {
    
    results := make([]*HealthCheckResult, 0)
    
    // 检查所有 addon
    for _, addon := range bkeCluster.Spec.ClusterConfig.Cluster.Addons {
        if addon.Type != "yaml" {
            continue
        }
        
        result, err := c.checkResource(ctx, addon)
        if err != nil {
            return nil, fmt.Errorf("check resource %s: %w", addon.Name, err)
        }
        
        results = append(results, result)
    }
    
    return results, nil
}

// checkResource 检查单个 YAML 资源健康状态
func (c *YAMLHealthChecker) checkResource(
    ctx context.Context,
    addon bkev1beta1.Product,
) (*HealthCheckResult, error) {
    
    result := &HealthCheckResult{
        Component: fmt.Sprintf("yaml/%s", addon.Name),
    }
    
    // 检查 Deployment 状态
    deployment := &appsv1.Deployment{}
    err := c.client.Get(ctx, types.NamespacedName{
        Name:      addon.Name,
        Namespace: addon.Namespace,
    }, deployment)
    
    if err != nil {
        if errors.IsNotFound(err) {
            result.Healthy = false
            result.Status = "NotFound"
            result.Message = "Deployment not found"
            result.Error = err
            return result, nil
        }
        return nil, fmt.Errorf("get deployment: %w", err)
    }
    
    // 检查 Deployment 状态
    if deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
        result.Healthy = true
        result.Status = "Available"
        result.Message = "Deployment is available"
    } else if deployment.Status.UnavailableReplicas > 0 {
        result.Healthy = false
        result.Status = "Progressing"
        result.Message = "Deployment is progressing"
    } else {
        result.Healthy = false
        result.Status = "Degraded"
        result.Message = "Deployment is degraded"
        result.Error = fmt.Errorf("deployment degraded")
    }
    
    return result, nil
}
```

---

## 5. 实现细节

### 5.1 与 BKEClusterReconciler 集成

```go
// controllers/capbke/bkecluster_controller.go

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ... 现有逻辑 ...
    
    // 计算健康状态
    healthCalculator := healthcheck.NewClusterHealthCalculator(r.Client)
    healthState, err := healthCalculator.CalculateHealthState(ctx, bkeCluster)
    if err != nil {
        bkeClusterLogger().Warnf("Failed to calculate health state: %v", err)
        healthState = bkev1beta1.Unknown
    }
    
    // 设置健康状态
    markBKEClusterHealthyStatus(bkeCluster, healthState)
    
    // ... 现有逻辑 ...
}
```

### 5.2 性能优化

```go
// 使用缓存减少 API 调用
type CachedHealthChecker struct {
    cache     map[string]*HealthCheckResult
    cacheTTL  time.Duration
    lastCheck time.Time
}

func (c *CachedHealthChecker) CheckHealth(ctx context.Context) (*HealthCheckResult, error) {
    // 检查缓存是否有效
    if time.Since(c.lastCheck) < c.cacheTTL {
        if result, ok := c.cache["key"]; ok {
            return result, nil
        }
    }
    
    // 执行健康检查
    result, err := c.doCheck(ctx)
    if err != nil {
        return nil, err
    }
    
    // 更新缓存
    c.cache["key"] = result
    c.lastCheck = time.Now()
    
    return result, nil
}
```

### 5.3 错误处理

```go
// 部分失败时返回部分结果
func (c *ClusterHealthCalculator) checkAllComponents(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) ([]*HealthCheckResult, error) {
    
    results := make([]*HealthCheckResult, 0)
    var errs []error
    
    // 检查节点状态
    nodeResults, err := c.nodeChecker.CheckAllNodes(ctx, bkeCluster)
    if err != nil {
        errs = append(errs, fmt.Errorf("check nodes: %w", err))
    } else {
        results = append(results, nodeResults...)
    }
    
    // 检查 Helm Release 状态
    helmResults, err := c.helmChecker.CheckAllReleases(ctx, bkeCluster)
    if err != nil {
        errs = append(errs, fmt.Errorf("check helm releases: %w", err))
    } else {
        results = append(results, helmResults...)
    }
    
    // 检查 YAML 资源状态
    yamlResults, err := c.yamlChecker.CheckAllResources(ctx, bkeCluster)
    if err != nil {
        errs = append(errs, fmt.Errorf("check yaml resources: %w", err))
    } else {
        results = append(results, yamlResults...)
    }
    
    // 如果有错误但有结果，返回部分结果
    if len(errs) > 0 && len(results) > 0 {
        return results, fmt.Errorf("partial errors: %v", errs)
    }
    
    // 如果所有检查都失败，返回错误
    if len(errs) > 0 {
        return nil, fmt.Errorf("all checks failed: %v", errs)
    }
    
    return results, nil
}
```

---

## 6. 状态转换图

```
┌─────────────────────────────────────────────────────────────────┐
│                ClusterHealthState 状态转换图                     │
└─────────────────────────────────────────────────────────────────┘

                         ┌──────────────┐
                         │   Unknown    │
                         └──────┬───────┘
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
          ┌─────────────────┐     ┌─────────────────┐
          │   Deploying     │     │   Upgrading     │
          └────────┬────────┘     └────────┬────────┘
                   │                       │
         ┌─────────┴─────────┐             │
         │                   │             │
         ▼                   ▼             ▼
┌─────────────────┐ ┌─────────────────┐ ┌──────────────┐
│     Healthy     │ │  UpgradeFailed  │ │   Unhealthy  │
└────────┬────────┘ └────────┬────────┘ └──────────────┘
         │                   │
         │         ┌─────────┴─────────┐
         │         │                   │
         │         ▼                   ▼
         │  ┌─────────────┐   ┌──────────────┐
         │  │    Retry    │   │   Deleting   │
         │  └──────┬──────┘   └──────────────┘
         │         │
         │         ▼
         │  ┌─────────────┐
         └─►│   Healthy   │
            └─────────────┘

状态转换规则：
  Unknown → Deploying (首次部署)
  Unknown → Upgrading (声明式升级)
  Deploying → Healthy (所有组件健康)
  Deploying → Unhealthy (组件失败)
  Upgrading → Healthy (升级完成)
  Upgrading → UpgradeFailed (升级失败)
  UpgradeFailed → Upgrading (重试)
  Healthy → Upgrading (新升级)
  Healthy → Deleting (删除)
```

---

## 7. 实施步骤

### 阶段 1：核心组件实现（1-2 周）

1. 创建 `pkg/healthcheck/calculator.go`
   - 实现 `ClusterHealthCalculator` 接口
   - 实现 `CalculateHealthState` 方法
   - 实现 `checkDeclarativeUpgrade` 方法
   - 实现 `aggregateResults` 方法

2. 创建 `pkg/healthcheck/node_checker.go`
   - 实现 `NodeHealthChecker` 接口
   - 实现 `CheckAllNodes` 方法
   - 实现 `checkNode` 方法

3. 创建 `pkg/healthcheck/helm_checker.go`
   - 实现 `HelmHealthChecker` 接口
   - 实现 `CheckAllReleases` 方法
   - 实现 `checkRelease` 方法

4. 创建 `pkg/healthcheck/yaml_checker.go`
   - 实现 `YAMLHealthChecker` 接口
   - 实现 `CheckAllResources` 方法
   - 实现 `checkResource` 方法

### 阶段 2：集成到控制器（1 周）

1. 在 `BKEClusterReconciler` 中注入 `ClusterHealthCalculator`
2. 替换 `setClusterHealthStatus` 逻辑
3. 在 DAG 执行后调用健康检查
4. 添加错误处理和日志

### 阶段 3：测试与优化（1 周）

1. 编写单元测试
2. 编写集成测试
3. 性能优化（缓存、并发）
4. 错误处理优化

### 阶段 4：文档与发布（1 周）

1. 编写用户文档
2. 编写开发文档
3. 代码审查
4. 发布

---

## 8. 测试设计

### 8.1 单元测试

```go
// pkg/healthcheck/calculator_test.go

func TestCalculateHealthState(t *testing.T) {
    tests := []struct {
        name     string
        cluster  *bkev1beta1.BKECluster
        expected bkev1beta1.ClusterHealthState
    }{
        {
            name: "deleting cluster",
            cluster: &bkev1beta1.BKECluster{
                Spec: bkev1beta1.BKEClusterSpec{
                    Reset: true,
                },
            },
            expected: bkev1beta1.Deleting,
        },
        {
            name: "upgrading cluster",
            cluster: &bkev1beta1.BKECluster{
                Status: bkev1beta1.BKEClusterStatus{
                    DeclarativeUpgrade: &bkev1beta1.DeclarativeUpgradeStatus{
                        FinishedAt: nil,
                        LastError:  "",
                    },
                },
            },
            expected: bkev1beta1.Upgrading,
        },
        {
            name: "upgrade failed",
            cluster: &bkev1beta1.BKECluster{
                Status: bkev1beta1.BKEClusterStatus{
                    DeclarativeUpgrade: &bkev1beta1.DeclarativeUpgradeStatus{
                        FinishedAt: nil,
                        LastError:  "upgrade failed",
                    },
                },
            },
            expected: bkev1beta1.UpgradeFailed,
        },
        {
            name: "healthy cluster",
            cluster: &bkev1beta1.BKECluster{
                Status: bkev1beta1.BKEClusterStatus{
                    DeclarativeUpgrade: &bkev1beta1.DeclarativeUpgradeStatus{
                        FinishedAt: &metav1.Time{Time: time.Now()},
                        LastError:  "",
                    },
                },
            },
            expected: bkev1beta1.Healthy,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            calculator := NewClusterHealthCalculator(fake.NewClientBuilder().Build())
            state, err := calculator.CalculateHealthState(context.Background(), tt.cluster)
            
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            
            if state != tt.expected {
                t.Errorf("expected %s, got %s", tt.expected, state)
            }
        })
    }
}
```

### 8.2 集成测试

```go
// controllers/capbke/bkecluster_controller_health_test.go

func TestBKEClusterReconciler_HealthCalculation(t *testing.T) {
    // 创建测试集群
    cluster := &bkev1beta1.BKECluster{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-cluster",
            Namespace: "default",
        },
        Spec: bkev1beta1.BKEClusterSpec{
            ClusterConfig: &bkev1beta1.BKEConfig{
                Cluster: bkev1beta1.Cluster{
                    Addons: []bkev1beta1.Product{
                        {
                            Name:        "coredns",
                            Type:        "chart",
                            ReleaseName: "coredns",
                            Namespace:   "kube-system",
                        },
                    },
                },
            },
        },
    }
    
    // 创建测试节点
    node := &bkev1beta1.BKENode{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "node1",
            Namespace: "default",
            Labels: map[string]string{
                clusterv1.ClusterNameLabel: "test-cluster",
            },
        },
        Spec: bkev1beta1.BKENodeSpec{
            IP: "10.0.0.1",
        },
        Status: bkev1beta1.BKENodeStatus{
            State: bkev1beta1.NodeReady,
        },
    }
    
    // 创建 fake client
    fakeClient := fake.NewClientBuilder().
        WithObjects(cluster, node).
        Build()
    
    // 创建 reconciler
    reconciler := &BKEClusterReconciler{
        Client: fakeClient,
    }
    
    // 执行 Reconcile
    result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
        NamespacedName: types.NamespacedName{
            Name:      "test-cluster",
            Namespace: "default",
        },
    })
    
    // 验证结果
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    // 验证健康状态
    updatedCluster := &bkev1beta1.BKECluster{}
    err = fakeClient.Get(context.Background(), types.NamespacedName{
        Name:      "test-cluster",
        Namespace: "default",
    }, updatedCluster)
    
    if err != nil {
        t.Fatalf("failed to get cluster: %v", err)
    }
    
    if updatedCluster.Status.ClusterHealthState != bkev1beta1.Healthy {
        t.Errorf("expected Healthy, got %s", updatedCluster.Status.ClusterHealthState)
    }
}
```

---

## 9. DeclarativeUpgrade 字段分析

### 9.1 字段定义

**位置**: `api/bkecommon/v1beta1/bkecluster_status.go:85-112`

```go
type DeclarativeUpgradeStatus struct {
    TargetVersion string                                  // 目标版本
    StartedAt     *metav1.Time                            // 升级开始时间
    FinishedAt    *metav1.Time                            // 升级完成时间
    LastError     string                                  // 最后错误信息
    LastFailure   *DeclarativeUpgradeFailureRecord        // 最后失败记录
    Completed     []DeclarativeUpgradeComponentRecord     // 已完成组件列表
}
```

### 9.2 核心作用

1. **持久化升级进度**：在控制器重启后恢复升级状态
2. **幂等性保证**：避免重复执行已完成的组件
3. **失败追踪**：记录失败组件和错误信息，便于调试
4. **状态报告**：提供升级进度的可观测性

### 9.3 当前实现情况

#### 9.3.1 类型定义 ✅ 完整

- ✅ `DeclarativeUpgradeStatus` 结构体定义
- ✅ `DeclarativeUpgradeComponentRecord` 组件完成记录
- ✅ `DeclarativeUpgradeFailureRecord` 失败记录
- ✅ DeepCopy 方法自动生成

#### 9.3.2 辅助方法 ✅ 完整

```go
// 重置升级进度（目标版本变更时）
func (s *DeclarativeUpgradeStatus) ResetForTarget(targetVersion string, now metav1.Time)

// 确保初始化（返回是否需要重置）
func (s *DeclarativeUpgradeStatus) EnsureInitialized(targetVersion string, now metav1.Time) bool

// 检查组件是否已完成
func (s *DeclarativeUpgradeStatus) IsCompleted(name, version string) bool

// 标记组件完成
func (s *DeclarativeUpgradeStatus) MarkCompleted(name, version string, now metav1.Time)

// 标记组件失败
func (s *DeclarativeUpgradeStatus) MarkFailure(name, version, errMsg string, now metav1.Time)

// 清除失败记录
func (s *DeclarativeUpgradeStatus) ClearFailure()
```

#### 9.3.3 DAG 调度器集成 ✅ 完整

**文件**: `pkg/dagexec/scheduler.go:276-324`

```go
// 跳过已完成组件
func (s *Scheduler) shouldSkipComponent(phaseCtx, node) bool {
    st := phaseCtx.BKECluster.Status.DeclarativeUpgrade
    return st.IsCompleted(node.Name, s.nodeVersionKey(node))
}

// 标记组件完成
func (s *Scheduler) markComponentCompleted(phaseCtx, node) error {
    bc.Status.DeclarativeUpgrade.MarkCompleted(node.Name, version, now)
    bc.Status.DeclarativeUpgrade.LastError = ""
    bc.Status.DeclarativeUpgrade.ClearFailure()
}

// 标记组件失败
func (s *Scheduler) markComponentFailed(phaseCtx, node, err) error {
    bc.Status.DeclarativeUpgrade.MarkFailure(node.Name, version, errMsg, now)
}
```

#### 9.3.4 控制器集成 ✅ 完整

**文件**: `controllers/capbke/bkecluster_upgrade_dag.go`

```go
// 初始化升级进度
func (r *BKEClusterReconciler) ensureDeclarativeUpgradeProgress(bkeCluster, targetVersion) error {
    bc.Status.DeclarativeUpgrade.EnsureInitialized(targetVersion, now)
}

// 从 API 同步状态（防止内存状态过期）
func (r *BKEClusterReconciler) syncDeclarativeUpgradeStatusFromAPI(ctx, bkeCluster) error {
    fresh, _ := mergecluster.GetCombinedBKECluster(ctx, ...)
    mergecluster.PreserveDeclarativeUpgradeFromFresh(fresh, bkeCluster)
}

// 完成升级
func (r *BKEClusterReconciler) completeDeclarativeUpgrade(ctx, bkeCluster) error {
    bc.Status.DeclarativeUpgrade.FinishedAt = &now
    bc.Status.DeclarativeUpgrade.LastError = ""
    bc.Status.DeclarativeUpgrade.ClearFailure()
    // 清除 upgrade-ready 注解
    delete(ann, featuregate.UpgradeReadyAnnotationKey)
}
```

#### 9.3.5 Feature Gate 控制 ✅ 完整

**文件**: `pkg/featuregate/features.go`

```go
// 全局开关
config.DeclarativeUpgrade = true/false

// 注解控制
cvo.openfuyao.cn/declarative-upgrade: "true"

// 升级就绪注解（由 ClusterVersion 控制器设置）
cvo.openfuyao.cn/upgrade-ready: "v2.6.0"
```

#### 9.3.6 状态合并逻辑 ✅ 完整

**文件**: `pkg/mergecluster/bkecluster.go:151-157`

```go
// 从新鲜状态保留 DeclarativeUpgrade
func PreserveDeclarativeUpgradeFromFresh(fresh, target *v1beta1.BKECluster) {
    if fresh.Status.DeclarativeUpgrade == nil {
        return
    }
    target.Status.DeclarativeUpgrade = fresh.Status.DeclarativeUpgrade.DeepCopy()
}
```

### 9.4 使用流程

```
1. ClusterVersion 控制器检测到 desiredVersion 变更
   └─ 设置 cvo.openfuyao.cn/upgrade-ready="v2.6.0"

2. BKECluster 控制器检测到 upgrade-ready 注解
   └─ shouldUseDeclarativeUpgrade() = true

3. 初始化升级进度
   └─ ensureDeclarativeUpgradeProgress()
   └─ DeclarativeUpgrade.EnsureInitialized("v2.6.0", now)
   └─ TargetVersion = "v2.6.0"
   └─ StartedAt = now
   └─ Completed = []

4. 构建并执行 DAG
   └─ for each component:
        ├─ shouldSkipComponent() 检查 IsCompleted()
        ├─ 如果已完成：跳过
        └─ 如果未完成：执行
             ├─ 成功：markComponentCompleted()
             └─ 失败：markComponentFailed()

5. 完成升级
   └─ completeDeclarativeUpgrade()
   └─ FinishedAt = now
   └─ LastError = ""
   └─ 清除 upgrade-ready 注解
```

### 9.5 关键设计决策

1. **版本归一化**：空版本默认为 "v1.0.0"
   ```go
   const defaultComponentVersion = "v1.0.0"
   ```

2. **失败重试计数**：连续失败同一组件时递增 Attempt
   ```go
   if s.LastFailure.Name == name && s.LastFailure.Version == version {
       attempt = s.LastFailure.Attempt + 1
   }
   ```

3. **状态同步**：DAG 执行后从 API 刷新状态，防止内存状态过期
   ```go
   syncDeclarativeUpgradeStatusFromAPI(ctx, bkeCluster)
   ```

4. **幂等性**：基于组件名+版本判断是否完成
   ```go
   IsCompleted(name, version string) bool
   ```

### 9.6 总结

**DeclarativeUpgrade 字段实现完整**，包括：
- ✅ 完整的类型定义和辅助方法
- ✅ DAG 调度器集成（跳过/标记）
- ✅ 控制器集成（初始化/同步/完成）
- ✅ Feature Gate 控制
- ✅ 状态合并逻辑
- ✅ 单元测试覆盖

**核心功能**：
1. 持久化升级进度（跨控制器重启）
2. 幂等性保证（避免重复执行）
3. 失败追踪（记录错误信息）
4. 状态报告（提供可观测性）

当前实现已经可以支持声明式升级的完整生命周期管理。

---

**文档版本**: v1.0  
**最后更新**: 2026-07-09  
**维护者**: BKE Team
