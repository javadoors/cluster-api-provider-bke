# BKECluster控制器升级场景优化与重构方案
## 一、现状分析
### 1.1 当前升级判断逻辑
根据代码分析，当前升级判断存在以下问题：

当前实现（bkecluster_controller.go:530）：
```go
upgradeFlag := phaseutil.GetNeedUpgradeNodesWithBKENodes(bkeCluster, bkeNodes).Length() > 0
```
核心逻辑（util.go:775）：
```go
func GetNeedUpgradeNodesWithBKENodes(bkeCluster *bkev1beta1.BKECluster, bkeNodes bkev1beta1.BKENodes) bkenode.Nodes {
    if bkeCluster.Status.OpenFuyaoVersion == "" {
        return nil
    }
    
    return compareVersionAndGetNodesWithBKENodes(
        bkeCluster,
        bkeCluster.Status.OpenFuyaoVersion,
        bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion,
        bkeNodes,
    )
}
```
### 1.2 存在的问题
| 问题类别 | 具体问题 | 影响 | 
| - | - | - |
| 判断维度单一 | 仅检查OpenFuyaoVersion变化 | 无法识别K8s版本升级、组件升级、配置升级等 |
| 缺少前置检查 | 无升级兼容性验证 | 可能导致升级失败或集群不可用 |
| 缺少策略控制 | 无升级速率、并发数控制 | 可能影响集群稳定性 |
| 状态跟踪粗糙 | 仅通过UpgradeFlag标记 | 无法准确跟踪升级进度和阶段 |
| 缺少回滚机制 | 升级失败后无法回滚 | 需要手动修复或重建集群 |
| 缺少版本兼容矩阵 | 无版本兼容性检查 | 可能升级到不兼容的版本 |
## 二、优化设计方案
### 2.1 升级类型分类
```plainText
升级类型分类
├─ 版本升级
│   ├─ Kubernetes版本升级（v1.26 -> v1.27）
│   ├─ OpenFuyao版本升级（v1.0 -> v2.0）
│   └─ Containerd版本升级（1.6 -> 1.7）
│
├─ 组件升级
│   ├─ CNI插件升级
│   ├─ CSI插件升级
│   └─ 其他Addon升级
│
├─ 配置升级
│   ├─ 节点配置变更（CPU/Memory/Disk）
│   ├─ 网络配置变更（CIDR/MTU）
│   └─ 存储配置变更
│
└─ 安全升级
    ├─ 证书轮换
    ├─ 密钥更新
    └─ 安全补丁
```
### 2.2 升级状态机设计
```plainText
┌─────────────┐
│   Idle      │ ──检测到升级需求──>┌──────────────┐
└─────────────┘                    │ PreCheck     │
                                   └──────────────┘
                                          │
                    ┌─────────────────────┴──────────────────┐
                    │ 检查通过                               │ 检查失败
                    ▼                                        ▼
            ┌──────────────┐                          ┌─────────────┐
            │  Preparing   │                          │   Failed    │
            └──────────────┘                          └─────────────┘
                    │
                    ▼
            ┌──────────────┐
            │  Upgrading   │ ──升级Master──> ┌──────────────┐
            └──────────────┘                 │ MasterUpgrade│
                                             └──────────────┘
                    │                               │
                    │                               ▼
                    │                        ┌──────────────┐
                    │                        │ WorkerUpgrade│
                    │                        └──────────────┘
                    │                               │
                    └───────────────────────────────┤
                                                    ▼
                                            ┌──────────────┐
                                            │ PostCheck    │
                                            └──────────────┘
                                                    │
                            ┌───────────────────────┴─────────────────┐
                            │ 检查通过                                │ 检查失败
                            ▼                                         ▼
                    ┌──────────────┐                          ┌─────────────┐
                    │   Success    │                          │   Rollback  │
                    └──────────────┘                          └─────────────┘
```
### 2.3 核心数据结构设计
UpgradeInfo CRD
```go
type UpgradeInfo struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   UpgradeInfoSpec   `json:"spec,omitempty"`
    Status UpgradeInfoStatus `json:"status,omitempty"`
}

type UpgradeInfoSpec struct {
    ClusterRef *corev1.ObjectReference `json:"clusterRef"`
    
    UpgradeType UpgradeType `json:"upgradeType"`
    
    FromVersion string `json:"fromVersion"`
    ToVersion   string `json:"toVersion"`
    
    Strategy UpgradeStrategy `json:"strategy"`
    
    Components []ComponentUpgrade `json:"components"`
}

type UpgradeType string

const (
    UpgradeTypeKubernetes UpgradeType = "Kubernetes"
    UpgradeTypeOpenFuyao  UpgradeType = "OpenFuyao"
    UpgradeTypeContainerd UpgradeType = "Containerd"
    UpgradeTypeComponent  UpgradeType = "Component"
    UpgradeTypeConfig     UpgradeType = "Config"
    UpgradeTypeSecurity   UpgradeType = "Security"
)

type UpgradeStrategy struct {
    Type UpgradeStrategyType `json:"type"`
    
    MaxUnavailable int `json:"maxUnavailable"`
    MaxSurge       int `json:"maxSurge"`
    
    BatchSize      int           `json:"batchSize"`
    BatchInterval  time.Duration `json:"batchInterval"`
    
    PreCheckTimeout    time.Duration `json:"preCheckTimeout"`
    UpgradeTimeout     time.Duration `json:"upgradeTimeout"`
    PostCheckTimeout   time.Duration `json:"postCheckTimeout"`
    
    RollbackOnFailure  bool `json:"rollbackOnFailure"`
    PauseOnFailure     bool `json:"pauseOnFailure"`
}

type UpgradeStrategyType string

const (
    UpgradeStrategyRollingUpdate UpgradeStrategyType = "RollingUpdate"
    UpgradeStrategyInPlace       UpgradeStrategyType = "InPlace"
    UpgradeStrategyBlueGreen     UpgradeStrategyType = "BlueGreen"
)

type ComponentUpgrade struct {
    Name       string `json:"name"`
    Type       string `json:"type"`
    FromVersion string `json:"fromVersion"`
    ToVersion   string `json:"toVersion"`
    
    Nodes []string `json:"nodes"`
    
    Status ComponentUpgradeStatus `json:"status"`
}

type UpgradeInfoStatus struct {
    Phase UpgradePhase `json:"phase"`
    
    StartTime      *metav1.Time `json:"startTime"`
    CompletionTime *metav1.Time `json:"completionTime"`
    
    TotalNodes     int `json:"totalNodes"`
    UpgradedNodes  int `json:"upgradedNodes"`
    FailedNodes    int `json:"failedNodes"`
    
    CurrentBatch   int `json:"currentBatch"`
    TotalBatches   int `json:"totalBatches"`
    
    Components []ComponentUpgradeStatus `json:"components"`
    
    Conditions []metav1.Condition `json:"conditions"`
}

type UpgradePhase string

const (
    UpgradePhaseIdle       UpgradePhase = "Idle"
    UpgradePhasePreCheck   UpgradePhase = "PreCheck"
    UpgradePhasePreparing  UpgradePhase = "Preparing"
    UpgradePhaseUpgrading  UpgradePhase = "Upgrading"
    UpgradePhasePostCheck  UpgradePhase = "PostCheck"
    UpgradePhaseSuccess    UpgradePhase = "Success"
    UpgradePhaseFailed     UpgradePhase = "Failed"
    UpgradePhaseRollback   UpgradePhase = "Rollback"
    UpgradePhasePaused     UpgradePhase = "Paused"
)
```
## 三、优化实现方案
### 3.1 升级检测器
文件位置：controllers/capbke/upgrade_detector.go
```go
package capbke

import (
    "context"
    "fmt"
    
    "k8s.io/apimachinery/pkg/types"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

type UpgradeDetector struct {
    client.Client
    NodeFetcher *nodeutil.NodeFetcher
}

type UpgradePlan struct {
    NeedUpgrade      bool                  `json:"needUpgrade"`
    UpgradeType      UpgradeType           `json:"upgradeType"`
    FromVersion      string                `json:"fromVersion"`
    ToVersion        string                `json:"toVersion"`
    AffectedNodes    []string              `json:"affectedNodes"`
    Components       []ComponentUpgrade    `json:"components"`
    BreakingChanges  []string              `json:"breakingChanges"`
    Warnings         []string              `json:"warnings"`
}

func (d *UpgradeDetector) DetectUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) (*UpgradePlan, error) {
    plan := &UpgradePlan{}
    
    if err := d.detectKubernetesUpgrade(bkeCluster, plan); err != nil {
        return nil, err
    }
    
    if err := d.detectOpenFuyaoUpgrade(bkeCluster, plan); err != nil {
        return nil, err
    }
    
    if err := d.detectContainerdUpgrade(bkeCluster, plan); err != nil {
        return nil, err
    }
    
    if err := d.detectComponentUpgrade(bkeCluster, plan); err != nil {
        return nil, err
    }
    
    if err := d.detectConfigUpgrade(bkeCluster, plan); err != nil {
        return nil, err
    }
    
    if plan.NeedUpgrade {
        if err := d.analyzeBreakingChanges(plan); err != nil {
            return nil, err
        }
    }
    
    return plan, nil
}

func (d *UpgradeDetector) detectKubernetesUpgrade(bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan) error {
    currentVersion := bkeCluster.Status.KubernetesVersion
    targetVersion := bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
    
    if currentVersion == "" || currentVersion == targetVersion {
        return nil
    }
    
    if !isValidKubernetesUpgrade(currentVersion, targetVersion) {
        return fmt.Errorf("invalid Kubernetes upgrade path: %s -> %s", currentVersion, targetVersion)
    }
    
    bkeNodes, err := d.NodeFetcher.GetBKENodesWrapperForCluster(context.Background(), bkeCluster)
    if err != nil {
        return err
    }
    
    affectedNodes := []string{}
    for _, node := range bkeNodes {
        if node.Status.KubernetesVersion != targetVersion {
            affectedNodes = append(affectedNodes, node.Spec.IP)
        }
    }
    
    plan.NeedUpgrade = true
    plan.UpgradeType = UpgradeTypeKubernetes
    plan.FromVersion = currentVersion
    plan.ToVersion = targetVersion
    plan.AffectedNodes = affectedNodes
    
    return nil
}

func (d *UpgradeDetector) detectOpenFuyaoUpgrade(bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan) error {
    currentVersion := bkeCluster.Status.OpenFuyaoVersion
    targetVersion := bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
    
    if currentVersion == "" || currentVersion == targetVersion {
        return nil
    }
    
    bkeNodes, err := d.NodeFetcher.GetBKENodesWrapperForCluster(context.Background(), bkeCluster)
    if err != nil {
        return err
    }
    
    affectedNodes := []string{}
    for _, node := range bkeNodes {
        if node.Status.OpenFuyaoVersion != targetVersion {
            affectedNodes = append(affectedNodes, node.Spec.IP)
        }
    }
    
    if !plan.NeedUpgrade {
        plan.NeedUpgrade = true
        plan.UpgradeType = UpgradeTypeOpenFuyao
        plan.FromVersion = currentVersion
        plan.ToVersion = targetVersion
    }
    
    plan.Components = append(plan.Components, ComponentUpgrade{
        Name:        "OpenFuyao",
        Type:        "Core",
        FromVersion: currentVersion,
        ToVersion:   targetVersion,
        Nodes:       affectedNodes,
    })
    
    return nil
}

func (d *UpgradeDetector) detectContainerdUpgrade(bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan) error {
    currentVersion := bkeCluster.Status.ContainerdVersion
    targetVersion := bkeCluster.Spec.ClusterConfig.Cluster.ContainerdVersion
    
    if currentVersion == "" || currentVersion == targetVersion {
        return nil
    }
    
    bkeNodes, err := d.NodeFetcher.GetBKENodesWrapperForCluster(context.Background(), bkeCluster)
    if err != nil {
        return err
    }
    
    affectedNodes := []string{}
    for _, node := range bkeNodes {
        if node.Status.ContainerdVersion != targetVersion {
            affectedNodes = append(affectedNodes, node.Spec.IP)
        }
    }
    
    if !plan.NeedUpgrade {
        plan.NeedUpgrade = true
        plan.UpgradeType = UpgradeTypeContainerd
        plan.FromVersion = currentVersion
        plan.ToVersion = targetVersion
    }
    
    plan.Components = append(plan.Components, ComponentUpgrade{
        Name:        "Containerd",
        Type:        "Runtime",
        FromVersion: currentVersion,
        ToVersion:   targetVersion,
        Nodes:       affectedNodes,
    })
    
    return nil
}

func (d *UpgradeDetector) detectComponentUpgrade(bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan) error {
    return nil
}

func (d *UpgradeDetector) detectConfigUpgrade(bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan) error {
    return nil
}

func (d *UpgradeDetector) analyzeBreakingChanges(plan *UpgradePlan) error {
    return nil
}
```
### 3.2 升级前置检查器
文件位置：controllers/capbke/upgrade_checker.go
```go
package capbke

import (
    "context"
    "fmt"
    
    corev1 "k8s.io/api/core/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

type UpgradeChecker struct {
    client.Client
}

type PreCheckResult struct {
    Passed   bool          `json:"passed"`
    Warnings []string      `json:"warnings"`
    Errors   []string      `json:"errors"`
    Items    []CheckItem   `json:"items"`
}

type CheckItem struct {
    Name    string `json:"name"`
    Passed  bool   `json:"passed"`
    Message string `json:"message"`
}

func (c *UpgradeChecker) RunPreCheck(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan) (*PreCheckResult, error) {
    result := &PreCheckResult{
        Passed: true,
    }
    
    if err := c.checkClusterHealth(ctx, bkeCluster, result); err != nil {
        return nil, err
    }
    
    if err := c.checkNodesReady(ctx, bkeCluster, result); err != nil {
        return nil, err
    }
    
    if err := c.checkVersionCompatibility(ctx, bkeCluster, plan, result); err != nil {
        return nil, err
    }
    
    if err := c.checkResourceAvailability(ctx, bkeCluster, plan, result); err != nil {
        return nil, err
    }
    
    if err := c.checkBackupExists(ctx, bkeCluster, result); err != nil {
        return nil, err
    }
    
    if err := c.checkCustomChecks(ctx, bkeCluster, plan, result); err != nil {
        return nil, err
    }
    
    result.Passed = len(result.Errors) == 0
    return result, nil
}

func (c *UpgradeChecker) checkClusterHealth(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, result *PreCheckResult) error {
    if bkeCluster.Status.ClusterHealthState != bkev1beta1.Healthy {
        result.Errors = append(result.Errors, "集群状态不健康，无法执行升级")
        result.Items = append(result.Items, CheckItem{
            Name:    "ClusterHealth",
            Passed:  false,
            Message: fmt.Sprintf("当前集群状态: %s", bkeCluster.Status.ClusterHealthState),
        })
        return nil
    }
    
    result.Items = append(result.Items, CheckItem{
        Name:    "ClusterHealth",
        Passed:  true,
        Message: "集群状态健康",
    })
    
    return nil
}

func (c *UpgradeChecker) checkNodesReady(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, result *PreCheckResult) error {
    nodes := &corev1.NodeList{}
    if err := c.List(ctx, nodes); err != nil {
        return err
    }
    
    notReadyNodes := []string{}
    for _, node := range nodes.Items {
        for _, condition := range node.Status.Conditions {
            if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
                notReadyNodes = append(notReadyNodes, node.Name)
            }
        }
    }
    
    if len(notReadyNodes) > 0 {
        result.Errors = append(result.Errors, fmt.Sprintf("存在未就绪节点: %v", notReadyNodes))
        result.Items = append(result.Items, CheckItem{
            Name:    "NodesReady",
            Passed:  false,
            Message: fmt.Sprintf("未就绪节点: %d", len(notReadyNodes)),
        })
        return nil
    }
    
    result.Items = append(result.Items, CheckItem{
        Name:    "NodesReady",
        Passed:  true,
        Message: "所有节点就绪",
    })
    
    return nil
}

func (c *UpgradeChecker) checkVersionCompatibility(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan, result *PreCheckResult) error {
    compatible, err := checkVersionCompatibility(plan.FromVersion, plan.ToVersion, plan.UpgradeType)
    if err != nil {
        return err
    }
    
    if !compatible {
        result.Errors = append(result.Errors, fmt.Sprintf("版本不兼容: %s -> %s", plan.FromVersion, plan.ToVersion))
        result.Items = append(result.Items, CheckItem{
            Name:    "VersionCompatibility",
            Passed:  false,
            Message: "版本升级路径不兼容",
        })
        return nil
    }
    
    result.Items = append(result.Items, CheckItem{
        Name:    "VersionCompatibility",
        Passed:  true,
        Message: "版本兼容性检查通过",
    })
    
    return nil
}

func (c *UpgradeChecker) checkResourceAvailability(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan, result *PreCheckResult) error {
    return nil
}

func (c *UpgradeChecker) checkBackupExists(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, result *PreCheckResult) error {
    return nil
}

func (c *UpgradeChecker) checkCustomChecks(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan, result *PreCheckResult) error {
    return nil
}
```
### 3.3 升级协调器
文件位置：controllers/capbke/upgrade_coordinator.go
```go
package capbke

import (
    "context"
    "fmt"
    "time"
    
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

type UpgradeCoordinator struct {
    client.Client
    Detector *UpgradeDetector
    Checker  *UpgradeChecker
}

func (c *UpgradeCoordinator) CoordinateUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) error {
    plan, err := c.Detector.DetectUpgrade(ctx, bkeCluster)
    if err != nil {
        return fmt.Errorf("failed to detect upgrade: %w", err)
    }
    
    if !plan.NeedUpgrade {
        return nil
    }
    
    upgradeInfo, err := c.createUpgradeInfo(ctx, bkeCluster, plan)
    if err != nil {
        return fmt.Errorf("failed to create upgrade info: %w", err)
    }
    
    checkResult, err := c.Checker.RunPreCheck(ctx, bkeCluster, plan)
    if err != nil {
        return fmt.Errorf("failed to run pre-check: %w", err)
    }
    
    if !checkResult.Passed {
        upgradeInfo.Status.Phase = UpgradePhaseFailed
        upgradeInfo.Status.Conditions = append(upgradeInfo.Status.Conditions, metav1.Condition{
            Type:    "PreCheckFailed",
            Status:  metav1.ConditionTrue,
            Reason:  "PreCheckFailed",
            Message: fmt.Sprintf("Pre-check failed: %v", checkResult.Errors),
        })
        return c.Status().Update(ctx, upgradeInfo)
    }
    
    upgradeInfo.Status.Phase = UpgradePhaseUpgrading
    upgradeInfo.Status.StartTime = &metav1.Time{Time: time.Now()}
    if err := c.Status().Update(ctx, upgradeInfo); err != nil {
        return err
    }
    
    if err := c.executeUpgrade(ctx, bkeCluster, upgradeInfo, plan); err != nil {
        return c.handleUpgradeFailure(ctx, bkeCluster, upgradeInfo, err)
    }
    
    upgradeInfo.Status.Phase = UpgradePhaseSuccess
    upgradeInfo.Status.CompletionTime = &metav1.Time{Time: time.Now()}
    return c.Status().Update(ctx, upgradeInfo)
}

func (c *UpgradeCoordinator) createUpgradeInfo(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan) (*UpgradeInfo, error) {
    upgradeInfo := &UpgradeInfo{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("%s-upgrade", bkeCluster.Name),
            Namespace: bkeCluster.Namespace,
            OwnerReferences: []metav1.OwnerReference{
                {
                    APIVersion: bkev1beta1.GroupVersion.String(),
                    Kind:       "BKECluster",
                    Name:       bkeCluster.Name,
                    UID:        bkeCluster.UID,
                },
            },
        },
        Spec: UpgradeInfoSpec{
            ClusterRef: &corev1.ObjectReference{
                APIVersion: bkev1beta1.GroupVersion.String(),
                Kind:       "BKECluster",
                Name:       bkeCluster.Name,
                Namespace:  bkeCluster.Namespace,
            },
            UpgradeType: plan.UpgradeType,
            FromVersion: plan.FromVersion,
            ToVersion:   plan.ToVersion,
            Strategy: UpgradeStrategy{
                Type:               UpgradeStrategyRollingUpdate,
                MaxUnavailable:     1,
                BatchSize:          1,
                BatchInterval:      5 * time.Minute,
                PreCheckTimeout:    10 * time.Minute,
                UpgradeTimeout:     30 * time.Minute,
                PostCheckTimeout:   10 * time.Minute,
                RollbackOnFailure:  true,
                PauseOnFailure:     true,
            },
            Components: plan.Components,
        },
        Status: UpgradeInfoStatus{
            Phase: UpgradePhasePreCheck,
        },
    }
    
    if err := c.Create(ctx, upgradeInfo); err != nil {
        return nil, err
    }
    
    return upgradeInfo, nil
}

func (c *UpgradeCoordinator) executeUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, upgradeInfo *UpgradeInfo, plan *UpgradePlan) error {
    masterNodes := c.filterMasterNodes(plan.AffectedNodes)
    workerNodes := c.filterWorkerNodes(plan.AffectedNodes)
    
    upgradeInfo.Status.TotalNodes = len(plan.AffectedNodes)
    upgradeInfo.Status.TotalBatches = c.calculateBatches(len(workerNodes), upgradeInfo.Spec.Strategy.BatchSize)
    
    if err := c.upgradeMasterNodes(ctx, bkeCluster, upgradeInfo, masterNodes); err != nil {
        return fmt.Errorf("master upgrade failed: %w", err)
    }
    
    if err := c.upgradeWorkerNodes(ctx, bkeCluster, upgradeInfo, workerNodes); err != nil {
        return fmt.Errorf("worker upgrade failed: %w", err)
    }
    
    return nil
}

func (c *UpgradeCoordinator) upgradeMasterNodes(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, upgradeInfo *UpgradeInfo, nodes []string) error {
    for _, nodeIP := range nodes {
        if err := c.upgradeSingleNode(ctx, bkeCluster, nodeIP, upgradeInfo.Spec); err != nil {
            return fmt.Errorf("failed to upgrade master node %s: %w", nodeIP, err)
        }
        
        upgradeInfo.Status.UpgradedNodes++
        if err := c.Status().Update(ctx, upgradeInfo); err != nil {
            return err
        }
        
        if err := c.waitForNodeReady(ctx, bkeCluster, nodeIP, upgradeInfo.Spec.Strategy.UpgradeTimeout); err != nil {
            return fmt.Errorf("master node %s not ready after upgrade: %w", nodeIP, err)
        }
    }
    
    return nil
}

func (c *UpgradeCoordinator) upgradeWorkerNodes(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, upgradeInfo *UpgradeInfo, nodes []string) error {
    batchSize := upgradeInfo.Spec.Strategy.BatchSize
    batchInterval := upgradeInfo.Spec.Strategy.BatchInterval
    
    for i := 0; i < len(nodes); i += batchSize {
        end := i + batchSize
        if end > len(nodes) {
            end = len(nodes)
        }
        
        batch := nodes[i:end]
        upgradeInfo.Status.CurrentBatch = (i / batchSize) + 1
        
        for _, nodeIP := range batch {
            if err := c.upgradeSingleNode(ctx, bkeCluster, nodeIP, upgradeInfo.Spec); err != nil {
                return fmt.Errorf("failed to upgrade worker node %s: %w", nodeIP, err)
            }
            
            upgradeInfo.Status.UpgradedNodes++
        }
        
        if err := c.Status().Update(ctx, upgradeInfo); err != nil {
            return err
        }
        
        for _, nodeIP := range batch {
            if err := c.waitForNodeReady(ctx, bkeCluster, nodeIP, upgradeInfo.Spec.Strategy.UpgradeTimeout); err != nil {
                return fmt.Errorf("worker node %s not ready after upgrade: %w", nodeIP, err)
            }
        }
        
        if i+batchSize < len(nodes) {
            time.Sleep(batchInterval)
        }
    }
    
    return nil
}

func (c *UpgradeCoordinator) upgradeSingleNode(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, nodeIP string, spec UpgradeInfoSpec) error {
    return nil
}

func (c *UpgradeCoordinator) waitForNodeReady(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, nodeIP string, timeout time.Duration) error {
    return nil
}

func (c *UpgradeCoordinator) handleUpgradeFailure(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, upgradeInfo *UpgradeInfo, err error) error {
    upgradeInfo.Status.Phase = UpgradePhaseFailed
    upgradeInfo.Status.Conditions = append(upgradeInfo.Status.Conditions, metav1.Condition{
        Type:    "UpgradeFailed",
        Status:  metav1.ConditionTrue,
        Reason:  "UpgradeError",
        Message: err.Error(),
    })
    
    if upgradeInfo.Spec.Strategy.RollbackOnFailure {
        upgradeInfo.Status.Phase = UpgradePhaseRollback
        if rollbackErr := c.rollbackUpgrade(ctx, bkeCluster, upgradeInfo); rollbackErr != nil {
            return fmt.Errorf("upgrade failed: %w, rollback also failed: %v", err, rollbackErr)
        }
    }
    
    return c.Status().Update(ctx, upgradeInfo)
}

func (c *UpgradeCoordinator) rollbackUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, upgradeInfo *UpgradeInfo) error {
    return nil
}

func (c *UpgradeCoordinator) filterMasterNodes(nodes []string) []string {
    return nodes
}

func (c *UpgradeCoordinator) filterWorkerNodes(nodes []string) []string {
    return nodes
}

func (c *UpgradeCoordinator) calculateBatches(totalNodes, batchSize int) int {
    return (totalNodes + batchSize - 1) / batchSize
}
```
### 3.4 控制器集成
修改文件：controllers/capbke/bkecluster_controller.go
```go
type BKEClusterReconciler struct {
    client.Client
    Scheme      *runtime.Scheme
    Recorder    record.EventRecorder
    RestConfig  *rest.Config
    Tracker     *remote.ClusterCacheTracker
    controller  controller.Controller
    NodeFetcher *nodeutil.NodeFetcher
    
    UpgradeDetector    *UpgradeDetector
    UpgradeChecker     *UpgradeChecker
    UpgradeCoordinator *UpgradeCoordinator
}

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bkeCluster, err := r.getAndValidateCluster(ctx, req)
    if err != nil {
        return r.handleClusterError(err)
    }
    
    r.registerMetrics(bkeCluster)
    
    oldBkeCluster, err := r.getOldBKECluster(bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    bkeLogger := r.initializeLogger(bkeCluster)
    
    if err = r.handleClusterStatus(ctx, bkeCluster, bkeLogger); err != nil {
        return ctrl.Result{}, err
    }
    
    upgradePlan, err := r.detectAndValidateUpgrade(ctx, bkeCluster, bkeLogger)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    if upgradePlan != nil && upgradePlan.NeedUpgrade {
        if err := r.handleUpgrade(ctx, bkeCluster, upgradePlan, bkeLogger); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    phaseResult, err := r.executePhaseFlow(ctx, bkeCluster, oldBkeCluster, bkeLogger)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    watchResult, err := r.setupClusterWatching(ctx, bkeCluster, bkeLogger)
    if err != nil {
        return watchResult, err
    }
    
    result, err := r.getFinalResult(phaseResult, bkeCluster)
    return result, err
}

func (r *BKEClusterReconciler) detectAndValidateUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, logger *bkev1beta1.BKELogger) (*UpgradePlan, error) {
    plan, err := r.UpgradeDetector.DetectUpgrade(ctx, bkeCluster)
    if err != nil {
        logger.Error(constant.InternalErrorReason, "failed to detect upgrade: %v", err)
        return nil, err
    }
    
    if !plan.NeedUpgrade {
        return nil, nil
    }
    
    logger.Info("检测到升级需求: %s %s -> %s", plan.UpgradeType, plan.FromVersion, plan.ToVersion)
    
    if len(plan.BreakingChanges) > 0 {
        logger.Warn("检测到破坏性变更: %v", plan.BreakingChanges)
    }
    
    if len(plan.Warnings) > 0 {
        logger.Warn("升级警告: %v", plan.Warnings)
    }
    
    return plan, nil
}

func (r *BKEClusterReconciler) handleUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, plan *UpgradePlan, logger *bkev1beta1.BKELogger) error {
    logger.Info("开始执行升级: %s", plan.UpgradeType)
    
    if err := r.UpgradeCoordinator.CoordinateUpgrade(ctx, bkeCluster); err != nil {
        logger.Error(constant.InternalErrorReason, "upgrade failed: %v", err)
        return err
    }
    
    logger.Info("升级完成: %s -> %s", plan.FromVersion, plan.ToVersion)
    return nil
}
```
## 四、配置示例
### 4.1 升级策略配置
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: production-cluster
spec:
  clusterConfig:
    cluster:
      kubernetesVersion: "v1.27.0"
      openFuyaoVersion: "v2.0.0"
      containerdVersion: "1.7.0"
  
  upgradeConfig:
    strategy:
      type: RollingUpdate
      maxUnavailable: 1
      maxSurge: 1
      batchSize: 2
      batchInterval: 5m
      preCheckTimeout: 10m
      upgradeTimeout: 30m
      postCheckTimeout: 10m
      rollbackOnFailure: true
      pauseOnFailure: true
    
    preCheck:
      enabled: true
      checks:
        - name: ClusterHealth
          enabled: true
        - name: NodesReady
          enabled: true
        - name: VersionCompatibility
          enabled: true
        - name: ResourceAvailability
          enabled: true
        - name: BackupExists
          enabled: true
    
    postCheck:
      enabled: true
      checks:
        - name: ClusterHealth
          enabled: true
        - name: NodesReady
          enabled: true
        - name: ComponentsReady
          enabled: true
```
## 五、总结
优化要点
- 多维度升级检测：支持K8s、OpenFuyao、Containerd、组件、配置等多种升级类型
- 完善的前置检查：在升级前验证集群健康、节点就绪、版本兼容等
- 灵活的升级策略：支持滚动升级、批处理、超时控制、失败策略
- 精细的状态跟踪：通过UpgradeInfo CRD跟踪升级进度和状态
- 安全的回滚机制：升级失败时自动回滚到之前版本
- 版本兼容矩阵：验证升级路径的兼容性

### 实施建议
分阶段实施：
- 第一阶段：实现升级检测和前置检查
- 第二阶段：实现升级协调和状态跟踪
- 第三阶段：实现回滚和高级策略

充分测试：
- 单元测试：测试各个组件的逻辑
- 集成测试：测试完整的升级流程
- E2E测试：测试各种升级场景

监控告警：
- 监控升级进度和状态
- 设置升级超时和失败告警
- 记录升级历史和审计日志
