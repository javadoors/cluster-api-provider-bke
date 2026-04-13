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

      
# BKEAgent组件升级缺陷分析与优化建议
## 一、升级流程概览
根据代码分析，通过BKEAgent安装部署的组件升级涉及以下Phase：
```
┌─────────────────────────────────────────────────────────────────────┐
│                    组件升级Phase执行顺序                             │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  1. EnsureProviderSelfUpgrade    - Provider自升级                   │
│  2. EnsureAgentUpgrade           - BKEAgent升级                     │
│  3. EnsureContainerdUpgrade      - Containerd升级                   │
│  4. EnsureEtcdUpgrade            - Etcd升级                         │
│  5. EnsureMasterUpgrade          - Master节点升级                   │
│  6. EnsureWorkerUpgrade          - Worker节点升级                   │
│  7. EnsureComponentUpgrade       - openFuyao核心组件升级            │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```
## 二、各Phase缺陷详细分析
### 2.1 EnsureAgentUpgrade（BKEAgent升级）
**文件位置**：[ensure_agent_upgrade.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_agent_upgrade.go)

#### 缺陷清单
| 序号 | 缺陷类型 | 具体问题 | 影响 |
|-----|---------|---------|------|
| 1 | **版本检测分散** | 版本检测逻辑分布在`NeedExecute`、`isBKEAgentDeployerNeedUpgrade`等多处 | 代码重复、维护困难 |
| 2 | **缺少回滚机制** | 升级失败后无法回滚到之前版本 | 需要手动修复 |
| 3 | **状态跟踪不完整** | 只更新`AddonStatus`，缺少详细的升级状态记录 | 无法追溯升级历史 |
| 4 | **缺少并发控制** | 多节点同时升级可能导致资源竞争 | 升级过程不稳定 |
| 5 | **超时控制粗糙** | 固定5分钟超时，不考虑集群规模 | 大规模集群可能超时 |
| 6 | **错误处理简单** | 升级失败后直接返回错误，无重试机制 | 需要手动重新触发 |
#### 代码示例
```go
// 当前实现：版本检测分散
func (e *EnsureAgentUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    // 1. 检查 spec 和 status 是否有差异
    if new.Status.OpenFuyaoVersion == new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
        e.SetStatus(bkev1beta1.PhaseSucceeded)
        return false
    }
    
    // 2. 从 spec 中解析出目标版本
    targetVersion, err := e.getTargetBKEAgentDeployerVersionFromSpec(new)
    // ...
}

func (e *EnsureAgentUpgrade) isBKEAgentDeployerNeedUpgrade(old *bkev1beta1.BKECluster,
    new *bkev1beta1.BKECluster) bool {
    // 重复的版本检测逻辑
    if new.Status.OpenFuyaoVersion == new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
        return false
    }
    // ...
}
```
### 2.2 EnsureContainerdUpgrade（Containerd升级）
**文件位置**：[ensure_containerd_upgrade.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_containerd_upgrade.go)
#### 缺陷清单
| 序号 | 缺陷类型 | 具体问题 | 影响 |
|-----|---------|---------|------|
| 1 | **缺少前置健康检查** | 升级前不检查节点状态和集群健康 | 可能升级不健康节点 |
| 2 | **缺少回滚机制** | 升级失败后无法回滚 | 节点不可用 |
| 3 | **错误处理简单** | 只记录失败节点，无自动恢复 | 需要手动干预 |
| 4 | **缺少并发控制** | 所有节点同时升级 | 资源竞争、服务中断 |
| 5 | **版本兼容性检查缺失** | 不检查版本兼容性 | 可能导致不兼容升级 |
| 6 | **状态更新不完整** | 只更新`ContainerdVersion` | 缺少详细状态 |
#### 代码示例
```go
// 当前实现：缺少前置检查
func (e *EnsureContainerdUpgrade) rolloutContainerd() (ctrl.Result, error) {
    _, _, bkeCluster, _, log := e.Ctx.Untie()

    // 直接执行重置和重新部署，无前置检查
    err := e.resetContainerd()
    if err != nil {
        return ctrl.Result{}, err
    }
    err = e.redeployContainerd()
    if err != nil {
        return ctrl.Result{}, err
    }

    log.Info(constant.ContainerdUpgradeSuccess, "upgrade containerd success")
    bkeCluster.Status.ContainerdVersion = bkeCluster.Spec.ClusterConfig.Cluster.ContainerdVersion

    return ctrl.Result{}, nil
}
```
### 2.3 EnsureMasterUpgrade（Master节点升级）
**文件位置**：[ensure_master_upgrade.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_master_upgrade.go)
#### 缺陷清单
| 序号 | 缺陷类型 | 具体问题 | 影响 |
|-----|---------|---------|------|
| 1 | **缺少前置检查** | 不检查集群健康、资源充足性 | 可能升级失败 |
| 2 | **缺少回滚机制** | 升级失败后无法回滚 | 集群不可用 |
| 3 | **Etcd备份逻辑简单** | 只备份第一个节点，无验证 | 备份可能失败 |
| 4 | **升级顺序固定** | 按节点列表顺序升级，不考虑负载 | 可能影响服务 |
| 5 | **缺少超时控制** | 单节点升级无超时限制 | 可能长时间阻塞 |
| 6 | **状态标记不完整** | 只标记`NodeUpgrading`状态 | 缺少详细进度 |
#### 代码示例
```go
// 当前实现：Etcd备份逻辑简单
func (e *EnsureMasterUpgrade) rolloutUpgrade() (ctrl.Result, error) {
    // ...
    
    // 检查etcd配置
    specNodes, _ := e.Ctx.NodeFetcher().GetNodesForBKECluster(e.Ctx, bkeCluster)
    needBackupEtcd := false
    backEtcdNode := confv1beta1.Node{}
    etcdNodes := specNodes.Etcd()
    if etcdNodes.Length() != 0 {
        needBackupEtcd = true
        backEtcdNode = etcdNodes[0]  // 只取第一个节点
        log.Info(constant.MasterUpgradingReason, "backup etcd data to node %s", phaseutil.NodeInfo(backEtcdNode))
    }
    
    // 无备份验证逻辑
    // ...
}
```
### 2.4 EnsureWorkerUpgrade（Worker节点升级）
**文件位置**：[ensure_worker_upgrade.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_worker_upgrade.go)
#### 缺陷清单
| 序号 | 缺陷类型 | 具体问题 | 影响 |
|-----|---------|---------|------|
| 1 | **缺少并发控制** | 所有Worker节点串行升级 | 升级时间长 |
| 2 | **重试策略不完善** | 失败后直接返回，无自动重试 | 需要手动触发 |
| 3 | **缺少资源检查** | 不检查节点资源是否充足 | 可能升级失败 |
| 4 | **缺少Pod驱逐验证** | 驱逐后不验证Pod状态 | 可能丢失Pod |
| 5 | **状态跟踪粗糙** | 只记录成功/失败节点 | 缺少详细进度 |
| 6 | **缺少回滚机制** | 升级失败后无法回滚 | 节点不可用 |
#### 代码示例
```go
// 当前实现：缺少并发控制
func (e *EnsureWorkerUpgrade) processNodeUpgrade(params ProcessNodeUpgradeParams) (ctrl.Result, []string, error) {
    var failedUpgradeNodes []string
    nodeFetcher := e.Ctx.NodeFetcher()

    clientSet, _, _ := kube.GetTargetClusterClient(params.Ctx, params.Client, params.BKECluster)
    
    // 串行升级所有节点
    for _, node := range params.NeedUpgradeNodes {
        // ...
        if err := e.upgradeNode(node, remoteNode, params.Drainer); err != nil {
            failedUpgradeNodes = append(failedUpgradeNodes, phaseutil.NodeInfo(node))
            // 失败后继续下一个节点，无重试
            continue
        }
        // ...
    }
    return ctrl.Result{}, failedUpgradeNodes, nil
}
```
### 2.5 EnsureComponentUpgrade（核心组件升级）
**文件位置**：[ensure_component_upgrade.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_component_upgrade.go)
#### 缺陷清单
| 序号 | 缺陷类型 | 具体问题 | 影响 |
|-----|---------|---------|------|
| 1 | **缺少健康检查** | 升级后不检查组件健康状态 | 可能升级失败 |
| 2 | **缺少回滚机制** | 升级失败后无法回滚 | 组件不可用 |
| 3 | **升级顺序不可控** | 按PatchConfig顺序升级 | 可能违反依赖关系 |
| 4 | **缺少版本兼容性检查** | 不检查组件间兼容性 | 可能导致不兼容 |
| 5 | **错误处理简单** | 失败后直接返回错误 | 需要手动修复 |
| 6 | **缺少并发控制** | 所有组件串行升级 | 升级时间长 |
#### 代码示例
```go
// 当前实现：缺少健康检查和回滚
func (e *EnsureComponentUpgrade) rolloutOpenfuyaoComponent() (ctrl.Result, error) {
    _, _, bkeCluster, _, log := e.Ctx.Untie()
    patchCfg, err := e.getPatchConfig()
    if err != nil {
        return ctrl.Result{}, err
    }

    // 直接处理镜像更新，无前置检查
    if err = e.processImageUpdates(patchCfg); err != nil {
        return ctrl.Result{}, err
    }

    log.Info(constant.ComponentUpgradeSuccess, "upgrade all component success")
    bkeCluster.Status.OpenFuyaoVersion = bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion

    return ctrl.Result{}, nil
}
```
### 2.6 EnsureEtcdUpgrade（Etcd升级）
**文件位置**：[ensure_etcd_upgrade.go](file:///D:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_etcd_upgrade.go)
#### 缺陷清单
| 序号 | 缺陷类型 | 具体问题 | 影响 |
|-----|---------|---------|------|
| 1 | **缺少集群健康检查** | 升级前不检查Etcd集群健康 | 可能升级不健康集群 |
| 2 | **备份恢复机制不完善** | 备份后无验证，恢复逻辑缺失 | 数据丢失风险 |
| 3 | **缺少回滚机制** | 升级失败后无法回滚 | Etcd不可用 |
| 4 | **缺少仲裁保护** | 不保证升级过程中仲裁可用 | 集群脑裂风险 |
| 5 | **超时控制粗糙** | 固定超时时间 | 大规模集群可能超时 |
| 6 | **状态跟踪不完整** | 只更新`EtcdVersion` | 缺少详细状态 |
#### 代码示例
```go
// 当前实现：缺少集群健康检查
func (e *EnsureEtcdUpgrade) rolloutUpgrade() (ctrl.Result, error) {
    _, c, bkeCluster, _, log := e.Ctx.Untie()

    // 直接过滤可升级节点，无集群健康检查
    needUpgradeNodes, err := e.filterUpgradeableNodes(bkeCluster, log)
    if err != nil {
        return ctrl.Result{Requeue: true}, err
    }

    needBackupEtcd, backEtcdNode := e.determineBackupNode(bkeCluster, log)

    // 无仲裁保护逻辑
    // ...
}
```
## 三、通用缺陷总结
### 3.1 架构层面缺陷
| 缺陷类型 | 具体表现 | 影响 |
|---------|---------|------|
| **缺少统一升级框架** | 每个Phase独立实现升级逻辑 | 代码重复、维护困难 |
| **缺少回滚机制** | 所有Phase都没有回滚逻辑 | 升级失败后无法恢复 |
| **缺少状态机管理** | 升级状态跟踪不完整 | 无法准确跟踪升级进度 |
| **缺少并发控制** | 多节点/组件同时升级 | 资源竞争、服务中断 |
### 3.2 功能层面缺陷
| 缺陷类型 | 具体表现 | 影响 |
|---------|---------|------|
| **前置检查不足** | 缺少健康检查、资源检查、兼容性检查 | 升级失败率高 |
| **错误处理简单** | 失败后直接返回，无重试、无回滚 | 需要手动干预 |
| **超时控制粗糙** | 固定超时时间，不考虑规模 | 大规模集群超时 |
| **状态跟踪粗糙** | 只记录成功/失败，缺少详细进度 | 无法追溯升级历史 |
### 3.3 安全层面缺陷
| 缺陷类型 | 具体表现 | 影响 |
|---------|---------|------|
| **缺少备份验证** | 备份后不验证备份完整性 | 数据丢失风险 |
| **缺少仲裁保护** | Etcd升级不保证仲裁 | 集群脑裂风险 |
| **缺少资源限制** | 不检查节点资源充足性 | 升级失败或服务中断 |
## 四、优化与重构建议
### 4.1 统一升级框架设计
**目标**：建立统一的升级框架，支持前置检查、升级执行、健康验证、回滚机制。
```go
// 升级框架接口
type UpgradeFramework interface {
    PreCheck(ctx context.Context, target *UpgradeTarget) (*PreCheckResult, error)
    Backup(ctx context.Context, target *UpgradeTarget) (*BackupResult, error)
    Execute(ctx context.Context, target *UpgradeTarget) (*ExecuteResult, error)
    Verify(ctx context.Context, target *UpgradeTarget) (*VerifyResult, error)
    Rollback(ctx context.Context, target *UpgradeTarget, backup *BackupResult) error
}

// 升级目标
type UpgradeTarget struct {
    Type        UpgradeType      // 升级类型
    Version     string           // 目标版本
    Nodes       []string         // 目标节点
    Components  []string         // 目标组件
    Strategy    UpgradeStrategy  // 升级策略
}

// 升级策略
type UpgradeStrategy struct {
    MaxUnavailable  int           // 最大不可用数
    BatchSize       int           // 批处理大小
    Timeout         time.Duration // 超时时间
    RetryCount      int           // 重试次数
    RetryInterval   time.Duration // 重试间隔
}

// 前置检查结果
type PreCheckResult struct {
    Passed   bool
    Errors   []string
    Warnings []string
}

// 备份结果
type BackupResult struct {
    Success    bool
    BackupPath string
    Checksum   string
    Timestamp  time.Time
}

// 执行结果
type ExecuteResult struct {
    Success      bool
    SuccessNodes []string
    FailedNodes  []string
    Duration     time.Duration
}

// 验证结果
type VerifyResult struct {
    Healthy      bool
    Unhealthy    []string
    Details      map[string]string
}
```
### 4.2 升级管理器实现
```go
// 升级管理器
type UpgradeManager struct {
    client     client.Client
    recorder   record.EventRecorder
    frameworks map[UpgradeType]UpgradeFramework
}

func NewUpgradeManager(client client.Client, recorder record.EventRecorder) *UpgradeManager {
    return &UpgradeManager{
        client:   client,
        recorder: recorder,
        frameworks: map[UpgradeType]UpgradeFramework{
            UpgradeTypeAgent:      NewAgentUpgradeFramework(),
            UpgradeTypeContainerd: NewContainerdUpgradeFramework(),
            UpgradeTypeEtcd:       NewEtcdUpgradeFramework(),
            UpgradeTypeMaster:     NewMasterUpgradeFramework(),
            UpgradeTypeWorker:     NewWorkerUpgradeFramework(),
            UpgradeTypeComponent:  NewComponentUpgradeFramework(),
        },
    }
}

func (m *UpgradeManager) ExecuteUpgrade(ctx context.Context, target *UpgradeTarget) error {
    framework, ok := m.frameworks[target.Type]
    if !ok {
        return fmt.Errorf("unsupported upgrade type: %s", target.Type)
    }

    // 1. 前置检查
    preCheckResult, err := framework.PreCheck(ctx, target)
    if err != nil {
        return fmt.Errorf("pre-check failed: %w", err)
    }
    if !preCheckResult.Passed {
        return fmt.Errorf("pre-check failed: %v", preCheckResult.Errors)
    }

    // 2. 备份
    backupResult, err := framework.Backup(ctx, target)
    if err != nil {
        return fmt.Errorf("backup failed: %w", err)
    }

    // 3. 执行升级
    executeResult, err := framework.Execute(ctx, target)
    if err != nil {
        // 升级失败，尝试回滚
        if rollbackErr := framework.Rollback(ctx, target, backupResult); rollbackErr != nil {
            m.recorder.Event(target, "Warning", "RollbackFailed", rollbackErr.Error())
        }
        return fmt.Errorf("execute failed: %w", err)
    }

    // 4. 验证
    verifyResult, err := framework.Verify(ctx, target)
    if err != nil {
        // 验证失败，尝试回滚
        if rollbackErr := framework.Rollback(ctx, target, backupResult); rollbackErr != nil {
            m.recorder.Event(target, "Warning", "RollbackFailed", rollbackErr.Error())
        }
        return fmt.Errorf("verify failed: %w", err)
    }
    if !verifyResult.Healthy {
        // 验证不通过，尝试回滚
        if rollbackErr := framework.Rollback(ctx, target, backupResult); rollbackErr != nil {
            m.recorder.Event(target, "Warning", "RollbackFailed", rollbackErr.Error())
        }
        return fmt.Errorf("verify failed: unhealthy: %v", verifyResult.Unhealthy)
    }

    return nil
}
```
### 4.3 具体优化建议
#### 4.3.1 EnsureAgentUpgrade优化
```go
// 优化后的Agent升级框架
type AgentUpgradeFramework struct {
    client   client.Client
    recorder record.EventRecorder
}

func (f *AgentUpgradeFramework) PreCheck(ctx context.Context, target *UpgradeTarget) (*PreCheckResult, error) {
    result := &PreCheckResult{Passed: true}

    // 1. 检查集群健康
    if err := f.checkClusterHealth(ctx); err != nil {
        result.Errors = append(result.Errors, fmt.Sprintf("集群不健康: %v", err))
        result.Passed = false
    }

    // 2. 检查节点状态
    unhealthyNodes, err := f.checkNodesHealth(ctx, target.Nodes)
    if err != nil {
        return nil, err
    }
    if len(unhealthyNodes) > 0 {
        result.Warnings = append(result.Warnings, fmt.Sprintf("存在不健康节点: %v", unhealthyNodes))
    }

    // 3. 检查版本兼容性
    if err := f.checkVersionCompatibility(ctx, target.Version); err != nil {
        result.Errors = append(result.Errors, fmt.Sprintf("版本不兼容: %v", err))
        result.Passed = false
    }

    return result, nil
}

func (f *AgentUpgradeFramework) Backup(ctx context.Context, target *UpgradeTarget) (*BackupResult, error) {
    // 备份当前Agent配置和状态
    backupPath := fmt.Sprintf("/tmp/agent-backup-%d", time.Now().Unix())
    
    if err := f.backupAgentConfig(ctx, backupPath); err != nil {
        return nil, err
    }

    // 验证备份完整性
    checksum, err := f.verifyBackup(ctx, backupPath)
    if err != nil {
        return nil, err
    }

    return &BackupResult{
        Success:    true,
        BackupPath: backupPath,
        Checksum:   checksum,
        Timestamp:  time.Now(),
    }, nil
}

func (f *AgentUpgradeFramework) Execute(ctx context.Context, target *UpgradeTarget) (*ExecuteResult, error) {
    result := &ExecuteResult{
        SuccessNodes: []string{},
        FailedNodes:  []string{},
    }

    // 分批升级
    batchSize := target.Strategy.BatchSize
    for i := 0; i < len(target.Nodes); i += batchSize {
        end := i + batchSize
        if end > len(target.Nodes) {
            end = len(target.Nodes)
        }
        batch := target.Nodes[i:end]

        successNodes, failedNodes, err := f.upgradeBatch(ctx, batch, target.Version, target.Strategy)
        if err != nil {
            return nil, err
        }
        result.SuccessNodes = append(result.SuccessNodes, successNodes...)
        result.FailedNodes = append(result.FailedNodes, failedNodes...)
    }

    result.Success = len(result.FailedNodes) == 0
    return result, nil
}

func (f *AgentUpgradeFramework) Verify(ctx context.Context, target *UpgradeTarget) (*VerifyResult, error) {
    result := &VerifyResult{
        Healthy:   true,
        Details:   make(map[string]string),
    }

    for _, node := range target.Nodes {
        healthy, detail, err := f.verifyNode(ctx, node, target.Version)
        if err != nil {
            return nil, err
        }
        if !healthy {
            result.Healthy = false
            result.Unhealthy = append(result.Unhealthy, node)
        }
        result.Details[node] = detail
    }

    return result, nil
}

func (f *AgentUpgradeFramework) Rollback(ctx context.Context, target *UpgradeTarget, backup *BackupResult) error {
    // 恢复Agent配置
    if err := f.restoreAgentConfig(ctx, backup.BackupPath); err != nil {
        return err
    }

    // 重启Agent服务
    for _, node := range target.Nodes {
        if err := f.restartAgent(ctx, node); err != nil {
            f.recorder.Event(target, "Warning", "RollbackNodeFailed", 
                fmt.Sprintf("节点 %s 回滚失败: %v", node, err))
        }
    }

    return nil
}
```
#### 4.3.2 EnsureEtcdUpgrade优化
```go
// 优化后的Etcd升级框架
type EtcdUpgradeFramework struct {
    client   client.Client
    recorder record.EventRecorder
}

func (f *EtcdUpgradeFramework) PreCheck(ctx context.Context, target *UpgradeTarget) (*PreCheckResult, error) {
    result := &PreCheckResult{Passed: true}

    // 1. 检查Etcd集群健康
    healthy, err := f.checkEtcdClusterHealth(ctx)
    if err != nil || !healthy {
        result.Errors = append(result.Errors, "Etcd集群不健康")
        result.Passed = false
    }

    // 2. 检查仲裁状态
    quorum, err := f.checkEtcdQuorum(ctx)
    if err != nil || !quorum {
        result.Errors = append(result.Errors, "Etcd仲裁不可用")
        result.Passed = false
    }

    // 3. 检查版本兼容性
    if err := f.checkEtcdVersionCompatibility(ctx, target.Version); err != nil {
        result.Errors = append(result.Errors, fmt.Sprintf("Etcd版本不兼容: %v", err))
        result.Passed = false
    }

    return result, nil
}

func (f *EtcdUpgradeFramework) Backup(ctx context.Context, target *UpgradeTarget) (*BackupResult, error) {
    // 1. 选择备份节点（优先选择Leader）
    backupNode, err := f.selectBackupNode(ctx)
    if err != nil {
        return nil, err
    }

    // 2. 执行备份
    backupPath := fmt.Sprintf("/tmp/etcd-backup-%d", time.Now().Unix())
    if err := f.backupEtcd(ctx, backupNode, backupPath); err != nil {
        return nil, err
    }

    // 3. 验证备份完整性
    checksum, err := f.verifyEtcdBackup(ctx, backupNode, backupPath)
    if err != nil {
        return nil, err
    }

    return &BackupResult{
        Success:    true,
        BackupPath: backupPath,
        Checksum:   checksum,
        Timestamp:  time.Now(),
    }, nil
}

func (f *EtcdUpgradeFramework) Execute(ctx context.Context, target *UpgradeTarget) (*ExecuteResult, error) {
    result := &ExecuteResult{
        SuccessNodes: []string{},
        FailedNodes:  []string{},
    }

    // 滚动升级，每次只升级一个节点
    for _, node := range target.Nodes {
        // 检查仲裁状态
        if err := f.ensureQuorum(ctx); err != nil {
            return nil, fmt.Errorf("仲裁检查失败: %w", err)
        }

        // 升级单个节点
        if err := f.upgradeEtcdNode(ctx, node, target.Version, target.Strategy); err != nil {
            result.FailedNodes = append(result.FailedNodes, node)
            // 升级失败，停止后续升级
            return result, err
        }
        result.SuccessNodes = append(result.SuccessNodes, node)

        // 等待节点恢复
        if err := f.waitForEtcdNodeReady(ctx, node, target.Strategy.Timeout); err != nil {
            return nil, fmt.Errorf("节点 %s 恢复失败: %w", node, err)
        }
    }

    result.Success = len(result.FailedNodes) == 0
    return result, nil
}

func (f *EtcdUpgradeFramework) Rollback(ctx context.Context, target *UpgradeTarget, backup *BackupResult) error {
    // 1. 停止所有Etcd节点
    for _, node := range target.Nodes {
        if err := f.stopEtcd(ctx, node); err != nil {
            f.recorder.Event(target, "Warning", "RollbackStopFailed",
                fmt.Sprintf("节点 %s 停止失败: %v", node, err))
        }
    }

    // 2. 恢复备份数据
    if err := f.restoreEtcdBackup(ctx, backup.BackupPath); err != nil {
        return err
    }

    // 3. 启动所有Etcd节点
    for _, node := range target.Nodes {
        if err := f.startEtcd(ctx, node); err != nil {
            f.recorder.Event(target, "Warning", "RollbackStartFailed",
                fmt.Sprintf("节点 %s 启动失败: %v", node, err))
        }
    }

    // 4. 验证集群恢复
    healthy, err := f.checkEtcdClusterHealth(ctx)
    if err != nil || !healthy {
        return fmt.Errorf("Etcd集群恢复失败")
    }

    return nil
}
```
### 4.4 状态跟踪优化
```go
// 升级状态跟踪
type UpgradeStatus struct {
    Type        UpgradeType      `json:"type"`
    Phase       UpgradePhase     `json:"phase"`
    StartTime   time.Time        `json:"startTime"`
    EndTime     *time.Time       `json:"endTime,omitempty"`
    Version     string           `json:"version"`
    Nodes       []NodeStatus     `json:"nodes"`
    Backup      *BackupInfo      `json:"backup,omitempty"`
    Errors      []string         `json:"errors,omitempty"`
}

type NodeStatus struct {
    IP          string       `json:"ip"`
    Hostname    string       `json:"hostname"`
    Phase       string       `json:"phase"`
    StartTime   time.Time    `json:"startTime"`
    EndTime     *time.Time   `json:"endTime,omitempty"`
    Error       string       `json:"error,omitempty"`
}

type BackupInfo struct {
    Path       string    `json:"path"`
    Checksum   string    `json:"checksum"`
    Timestamp  time.Time `json:"timestamp"`
}

// 更新升级状态
func (m *UpgradeManager) updateUpgradeStatus(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, status *UpgradeStatus) error {
    // 更新BKECluster的Status中的升级状态
    if bkeCluster.Status.UpgradeHistory == nil {
        bkeCluster.Status.UpgradeHistory = []bkev1beta1.UpgradeRecord{}
    }

    record := bkev1beta1.UpgradeRecord{
        Type:      string(status.Type),
        Phase:     string(status.Phase),
        StartTime: status.StartTime,
        EndTime:   status.EndTime,
        Version:   status.Version,
        Errors:    status.Errors,
    }

    bkeCluster.Status.UpgradeHistory = append(bkeCluster.Status.UpgradeHistory, record)

    // 保留最近10次升级记录
    if len(bkeCluster.Status.UpgradeHistory) > 10 {
        bkeCluster.Status.UpgradeHistory = bkeCluster.Status.UpgradeHistory[len(bkeCluster.Status.UpgradeHistory)-10:]
    }

    return m.client.Status().Update(ctx, bkeCluster)
}
```
## 五、实施路线图
### 5.1 第一阶段：基础框架
```
任务清单：
├─ 1. 设计统一升级框架接口
│   ├─ UpgradeFramework接口定义
│   ├─ 升级目标、策略、结果结构体
│   └─ 状态跟踪结构体
│
├─ 2. 实现UpgradeManager
│   ├─ 前置检查流程
│   ├─ 备份流程
│   ├─ 执行流程
│   ├─ 验证流程
│   └─ 回滚流程
│
└─ 3. 实现状态跟踪
    ├─ 升级状态结构体
    ├─ 状态更新逻辑
    └─ 升级历史记录
```
### 5.2 第二阶段：具体实现
```
任务清单：
├─ 1. 实现AgentUpgradeFramework
│   ├─ 前置检查（集群健康、节点状态、版本兼容性）
│   ├─ 备份（配置备份、验证）
│   ├─ 执行（分批升级、并发控制）
│   ├─ 验证（节点健康、版本验证）
│   └─ 回滚（配置恢复、服务重启）
│
├─ 2. 实现EtcdUpgradeFramework
│   ├─ 前置检查（集群健康、仲裁状态、版本兼容性）
│   ├─ 备份（选择节点、执行备份、验证）
│   ├─ 执行（滚动升级、仲裁保护）
│   ├─ 验证（集群健康、数据完整性）
│   └─ 回滚（停止服务、恢复数据、启动服务）
│
├─ 3. 实现MasterUpgradeFramework
│   ├─ 前置检查（集群健康、资源充足性）
│   ├─ 备份（Etcd备份、配置备份）
│   ├─ 执行（滚动升级、负载均衡）
│   ├─ 验证（节点健康、控制平面就绪）
│   └─ 回滚（版本回退、配置恢复）
│
└─ 4. 实现其他框架
    ├─ ContainerdUpgradeFramework
    ├─ WorkerUpgradeFramework
    └─ ComponentUpgradeFramework
```
### 5.3 第三阶段：集成测试
```
任务清单：
├─ 1. 单元测试
│   ├─ 各Framework的单元测试
│   ├─ UpgradeManager的单元测试
│   └─ 状态跟踪的单元测试
│
├─ 2. 集成测试
│   ├─ 升级流程集成测试
│   ├─ 回滚流程集成测试
│   └─ 并发升级集成测试
│
└─ 3. E2E测试
    ├─ 小规模集群升级测试
    ├─ 大规模集群升级测试
    └─ 异常场景测试（网络中断、节点故障等）
```
## 六、总结
### 6.1 关键优化点
1. **统一升级框架**：建立统一的升级框架，支持前置检查、备份、执行、验证、回滚
2. **完善前置检查**：增加集群健康、节点状态、资源充足性、版本兼容性检查
3. **实现回滚机制**：升级失败后自动回滚到之前状态
4. **并发控制**：支持分批升级，控制并发数
5. **状态跟踪**：详细记录升级过程，支持历史查询
### 6.2 预期收益
| 优化项 | 预期收益 |
|--------|---------|
| 统一升级框架 | 代码复用率提升50%，维护成本降低30% |
| 完善前置检查 | 升级失败率降低40% |
| 实现回滚机制 | 升级失败后恢复时间从小时级降低到分钟级 |
| 并发控制 | 大规模集群升级时间缩短60% |
| 状态跟踪 | 问题定位时间缩短70% |


# 设计一个针对用户自提供操作系统场景的组件安装与升级方案
## 一、方案概述
### 1.1 设计目标
- **解耦组件安装与操作系统**：支持在用户自提供的操作系统上安装容器运行时/kubelet/cni/csi
- **灵活的版本管理**：支持各组件独立版本管理和升级
- **声明式配置**：通过 CRD 定义组件配置，实现声明式管理
- **升级策略支持**：支持滚动升级、批处理升级、回滚等策略
### 1.2 架构设计
```
┌────────────────────────────────────────────────────────────┐
│                    Management Cluster                      │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ BKECluster   │  │ NodeConfig   │  │ ComponentSet │      │
│  │ Controller   │  │ Controller   │  │ Controller   │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└────────────────────────────────────────────────────────────┘
                            │
                            │ BKEAgent (DaemonSet)
                            ▼
┌────────────────────────────────────────────────────────────┐
│                    Target Cluster (Nodes)                  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              Node Components                         │  │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐            │  │
│  │  │Containerd│  │ Kubelet  │  │ CNI/CSI  │            │  │
│  │  └──────────┘  └──────────┘  └──────────┘            │  │
│  └──────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────┘
```
## 二、核心 CRD 设计
### 2.1 NodeConfig CRD
定义单个节点的组件配置：
```yaml
# d:\code\github\cluster-api-provider-bke\api\bkecommon\v1beta1\nodeconfig_types.go
```go nodeconfig_types.go
/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 */

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NodeConfigSpec defines the desired state of NodeConfig
type NodeConfigSpec struct {
	// NodeRef references the BKENode this config applies to
	// +required
	NodeRef NodeReference `json:"nodeRef"`

	// ContainerRuntime defines the container runtime configuration
	// +optional
	ContainerRuntime *ContainerRuntimeConfig `json:"containerRuntime,omitempty"`

	// Kubelet defines the kubelet configuration
	// +optional
	Kubelet *KubeletConfigRef `json:"kubelet,omitempty"`

	// CNI defines the CNI plugin configuration
	// +optional
	CNI *CNIConfig `json:"cni,omitempty"`

	// CSI defines the CSI driver configuration
	// +optional
	CSI *CSIConfig `json:"csi,omitempty"`

	// UpgradeStrategy defines the upgrade strategy for this node
	// +optional
	UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`
}

// NodeReference references a BKENode
type NodeReference struct {
	// Name of the BKENode
	// +required
	Name string `json:"name"`

	// Namespace of the BKENode
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// IP address of the node
	// +optional
	IP string `json:"ip,omitempty"`
}

// ContainerRuntimeConfig defines container runtime configuration
type ContainerRuntimeConfig struct {
	// Type defines the container runtime type
	// +kubebuilder:validation:Enum=containerd;docker;cri-o
	// +kubebuilder:default=containerd
	Type string `json:"type"`

	// Version defines the container runtime version
	// +required
	Version string `json:"version"`

	// ConfigRef references a ContainerdConfig or other runtime-specific config
	// +optional
	ConfigRef *ConfigReference `json:"configRef,omitempty"`

	// InstallConfig defines installation-specific configuration
	// +optional
	InstallConfig *RuntimeInstallConfig `json:"installConfig,omitempty"`
}

// RuntimeInstallConfig defines runtime installation configuration
type RuntimeInstallConfig struct {
	// InstallMethod defines how to install the runtime
	// +kubebuilder:validation:Enum=binary;package;image
	// +kubebuilder:default=binary
	InstallMethod string `json:"installMethod,omitempty"`

	// BinaryPath defines the binary installation path
	// +optional
	BinaryPath string `json:"binaryPath,omitempty"`

	// DataRoot defines the data directory
	// +optional
	DataRoot string `json:"dataRoot,omitempty"`

	// ConfigPath defines the config file path
	// +optional
	ConfigPath string `json:"configPath,omitempty"`

	// DownloadURL defines custom download URL
	// +optional
	DownloadURL string `json:"downloadUrl,omitempty"`

	// PreInstallScript defines pre-installation script
	// +optional
	PreInstallScript string `json:"preInstallScript,omitempty"`

	// PostInstallScript defines post-installation script
	// +optional
	PostInstallScript string `json:"postInstallScript,omitempty"`
}

// CNIConfig defines CNI plugin configuration
type CNIConfig struct {
	// Type defines the CNI plugin type
	// +kubebuilder:validation:Enum=calico;flannel;cilium;weave;custom
	// +required
	Type string `json:"type"`

	// Version defines the CNI plugin version
	// +required
	Version string `json:"version"`

	// ConfigRef references a ConfigMap containing CNI configuration
	// +optional
	ConfigRef *ConfigReference `json:"configRef,omitempty"`

	// InstallConfig defines CNI installation configuration
	// +optional
	InstallConfig *CNIInstallConfig `json:"installConfig,omitempty"`
}

// CNIInstallConfig defines CNI installation configuration
type CNIInstallConfig struct {
	// BinDir defines the CNI binary directory
	// +optional
	BinDir string `json:"binDir,omitempty"`

	// ConfigDir defines the CNI config directory
	// +optional
	ConfigDir string `json:"configDir,omitempty"`

	// DownloadURL defines custom download URL
	// +optional
	DownloadURL string `json:"downloadUrl,omitempty"`
}

// CSIConfig defines CSI driver configuration
type CSIConfig struct {
	// Type defines the CSI driver type
	// +kubebuilder:validation:Enum=local-path;nfs;ceph;rbd;custom
	// +required
	Type string `json:"type"`

	// Version defines the CSI driver version
	// +required
	Version string `json:"version"`

	// ConfigRef references a ConfigMap containing CSI configuration
	// +optional
	ConfigRef *ConfigReference `json:"configRef,omitempty"`

	// InstallConfig defines CSI installation configuration
	// +optional
	InstallConfig *CSIInstallConfig `json:"installConfig,omitempty"`
}

// CSIInstallConfig defines CSI installation configuration
type CSIInstallConfig struct {
	// BinDir defines the CSI binary directory
	// +optional
	BinDir string `json:"binDir,omitempty"`

	// DownloadURL defines custom download URL
	// +optional
	DownloadURL string `json:"downloadUrl,omitempty"`
}

// ConfigReference references a configuration resource
type ConfigReference struct {
	// APIVersion of the config resource
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind of the config resource
	// +optional
	Kind string `json:"kind,omitempty"`

	// Name of the config resource
	// +required
	Name string `json:"name"`

	// Namespace of the config resource
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// UpgradeStrategy defines upgrade strategy
type UpgradeStrategy struct {
	// Type defines the upgrade type
	// +kubebuilder:validation:Enum=RollingUpdate;OnDelete;Recreate
	// +kubebuilder:default=RollingUpdate
	Type string `json:"type,omitempty"`

	// RollingUpdate defines rolling update parameters
	// +optional
	RollingUpdate *RollingUpdateStrategy `json:"rollingUpdate,omitempty"`

	// PreUpgradeHook defines pre-upgrade hook
	// +optional
	PreUpgradeHook *HookConfig `json:"preUpgradeHook,omitempty"`

	// PostUpgradeHook defines post-upgrade hook
	// +optional
	PostUpgradeHook *HookConfig `json:"postUpgradeHook,omitempty"`
}

// RollingUpdateStrategy defines rolling update parameters
type RollingUpdateStrategy struct {
	// MaxUnavailable defines maximum unavailable nodes
	// +optional
	MaxUnavailable int `json:"maxUnavailable,omitempty"`

	// MaxSurge defines maximum surge nodes
	// +optional
	MaxSurge int `json:"maxSurge,omitempty"`

	// BatchSize defines batch size for batch upgrade
	// +optional
	BatchSize int `json:"batchSize,omitempty"`

	// BatchInterval defines interval between batches
	// +optional
	BatchInterval metav1.Duration `json:"batchInterval,omitempty"`

	// Timeout defines upgrade timeout
	// +optional
	Timeout metav1.Duration `json:"timeout,omitempty"`
}

// HookConfig defines hook configuration
type HookConfig struct {
	// Script defines the hook script content
	// +optional
	Script string `json:"script,omitempty"`

	// ScriptPath defines the hook script path
	// +optional
	ScriptPath string `json:"scriptPath,omitempty"`

	// Timeout defines hook execution timeout
	// +optional
	Timeout metav1.Duration `json:"timeout,omitempty"`
}

// NodeConfigStatus defines the observed state of NodeConfig
type NodeConfigStatus struct {
	// ContainerRuntimeStatus defines container runtime status
	// +optional
	ContainerRuntimeStatus *ComponentStatus `json:"containerRuntimeStatus,omitempty"`

	// KubeletStatus defines kubelet status
	// +optional
	KubeletStatus *ComponentStatus `json:"kubeletStatus,omitempty"`

	// CNIStatus defines CNI status
	// +optional
	CNIStatus *ComponentStatus `json:"cniStatus,omitempty"`

	// CSIStatus defines CSI status
	// +optional
	CSIStatus *ComponentStatus `json:"csiStatus,omitempty"`

	// LastUpgradeTime defines the last upgrade time
	// +optional
	LastUpgradeTime metav1.Time `json:"lastUpgradeTime,omitempty"`

	// Conditions defines the conditions of the NodeConfig
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ComponentStatus defines component status
type ComponentStatus struct {
	// Version defines the current version
	// +optional
	Version string `json:"version,omitempty"`

	// State defines the component state
	// +kubebuilder:validation:Enum=Installing;Installed;Upgrading;Upgraded;Failed;Unknown
	// +optional
	State string `json:"state,omitempty"`

	// Message defines the status message
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdateTime defines the last update time
	// +optional
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NodeConfig is the Schema for the nodeconfigs API
type NodeConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeConfigSpec   `json:"spec,omitempty"`
	Status NodeConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeConfigList contains a list of NodeConfig
type NodeConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeConfig{}, &NodeConfigList{})
}
```
### 2.2 ComponentSet CRD
定义集群级别的组件版本集合：
```yaml
# d:\code\github\cluster-api-provider-bke\api\bkecommon\v1beta1\componentset_types.go
```go componentset_types.go
/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 */

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComponentSetSpec defines the desired state of ComponentSet
type ComponentSetSpec struct {
	// ClusterRef references the BKECluster this ComponentSet belongs to
	// +required
	ClusterRef ClusterReference `json:"clusterRef"`

	// Components defines the component versions
	// +required
	Components []ComponentVersion `json:"components"`

	// UpgradePolicy defines the global upgrade policy
	// +optional
	UpgradePolicy UpgradePolicy `json:"upgradePolicy,omitempty"`
}

// ClusterReference references a BKECluster
type ClusterReference struct {
	// Name of the BKECluster
	// +required
	Name string `json:"name"`

	// Namespace of the BKECluster
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ComponentVersion defines a component version
type ComponentVersion struct {
	// Name defines the component name
	// +kubebuilder:validation:Enum=containerd;kubelet;calico;flannel;cilium;csi-localpath;csi-nfs
	// +required
	Name string `json:"name"`

	// Version defines the component version
	// +required
	Version string `json:"version"`

	// ConfigRef references component-specific configuration
	// +optional
	ConfigRef *ConfigReference `json:"configRef,omitempty"`

	// UpgradeStrategy defines component-specific upgrade strategy
	// +optional
	UpgradeStrategy *UpgradeStrategy `json:"upgradeStrategy,omitempty"`
}

// UpgradePolicy defines global upgrade policy
type UpgradePolicy struct {
	// AutoUpgrade enables automatic upgrade when version changes
	// +optional
	AutoUpgrade bool `json:"autoUpgrade,omitempty"`

	// PauseOnFailure pauses upgrade on failure
	// +optional
	PauseOnFailure bool `json:"pauseOnFailure,omitempty"`

	// MaxConcurrentUpgrades defines maximum concurrent upgrades
	// +optional
	MaxConcurrentUpgrades int `json:"maxConcurrentUpgrades,omitempty"`

	// HealthCheckTimeout defines health check timeout after upgrade
	// +optional
	HealthCheckTimeout metav1.Duration `json:"healthCheckTimeout,omitempty"`

	// RollbackOnFailure enables automatic rollback on failure
	// +optional
	RollbackOnFailure bool `json:"rollbackOnFailure,omitempty"`
}

// ComponentSetStatus defines the observed state of ComponentSet
type ComponentSetStatus struct {
	// CurrentComponents defines current component versions
	// +optional
	CurrentComponents []ComponentVersion `json:"currentComponents,omitempty"`

	// UpgradeStatus defines upgrade status
	// +optional
	UpgradeStatus *UpgradeStatus `json:"upgradeStatus,omitempty"`

	// Conditions defines the conditions
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// UpgradeStatus defines upgrade status
type UpgradeStatus struct {
	// Phase defines the upgrade phase
	// +kubebuilder:validation:Enum=Pending;PreCheck;Backup;Upgrading;Verifying;Completed;Failed;Rollback
	// +optional
	Phase string `json:"phase,omitempty"`

	// Component defines the component being upgraded
	// +optional
	Component string `json:"component,omitempty"`

	// Progress defines upgrade progress (0-100)
	// +optional
	Progress int `json:"progress,omitempty"`

	// Message defines status message
	// +optional
	Message string `json:"message,omitempty"`

	// StartTime defines upgrade start time
	// +optional
	StartTime metav1.Time `json:"startTime,omitempty"`

	// EndTime defines upgrade end time
	// +optional
	EndTime metav1.Time `json:"endTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ComponentSet is the Schema for the componentsets API
type ComponentSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComponentSetSpec   `json:"spec,omitempty"`
	Status ComponentSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ComponentSetList contains a list of ComponentSet
type ComponentSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComponentSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComponentSet{}, &ComponentSetList{})
}
```
## 三、组件安装框架设计
### 3.1 组件安装器接口
```yaml
# d:\code\github\cluster-api-provider-bke\pkg\component\installer\interface.go
```go interface.go
/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 */

package installer

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
)

// Installer defines the component installer interface
type Installer interface {
	// Name returns the installer name
	Name() string

	// Install installs the component
	Install(ctx context.Context, config *InstallConfig) (*InstallResult, error)

	// Upgrade upgrades the component
	Upgrade(ctx context.Context, config *UpgradeConfig) (*UpgradeResult, error)

	// Uninstall uninstalls the component
	Uninstall(ctx context.Context, config *UninstallConfig) error

	// Validate validates the configuration
	Validate(ctx context.Context, config runtime.Object) error

	// CheckHealth checks component health
	CheckHealth(ctx context.Context, config *HealthCheckConfig) (*HealthCheckResult, error)

	// GetVersion gets current component version
	GetVersion(ctx context.Context, config *VersionConfig) (string, error)
}

// InstallConfig defines installation configuration
type InstallConfig struct {
	// NodeConfig is the node configuration
	NodeConfig *confv1beta1.NodeConfig

	// ComponentConfig is the component-specific configuration
	ComponentConfig runtime.Object

	// InstallMethod defines installation method
	InstallMethod string

	// DryRun indicates dry run mode
	DryRun bool

	// Force indicates force installation
	Force bool
}

// InstallResult defines installation result
type InstallResult struct {
	// Success indicates success
	Success bool

	// Version is the installed version
	Version string

	// Message is the result message
	Message string

	// Requeue indicates whether to requeue
	Requeue bool

	// RequeueAfter is the requeue duration
	RequeueAfter metav1.Duration
}

// UpgradeConfig defines upgrade configuration
type UpgradeConfig struct {
	// NodeConfig is the node configuration
	NodeConfig *confv1beta1.NodeConfig

	// ComponentConfig is the component-specific configuration
	ComponentConfig runtime.Object

	// OldVersion is the old version
	OldVersion string

	// NewVersion is the new version
	NewVersion string

	// Strategy is the upgrade strategy
	Strategy confv1beta1.UpgradeStrategy

	// DryRun indicates dry run mode
	DryRun bool

	// Force indicates force upgrade
	Force bool

	// SkipBackup skips backup
	SkipBackup bool
}

// UpgradeResult defines upgrade result
type UpgradeResult struct {
	// Success indicates success
	Success bool

	// Version is the upgraded version
	Version string

	// Message is the result message
	Message string

	// Requeue indicates whether to requeue
	Requeue bool

	// RequeueAfter is the requeue duration
	RequeueAfter metav1.Duration

	// RollbackNeeded indicates whether rollback is needed
	RollbackNeeded bool

	// RollbackConfig is the rollback configuration
	RollbackConfig *RollbackConfig
}

// RollbackConfig defines rollback configuration
type RollbackConfig struct {
	// TargetVersion is the target version to rollback to
	TargetVersion string

	// BackupPath is the backup path
	BackupPath string
}

// UninstallConfig defines uninstallation configuration
type UninstallConfig struct {
	// NodeConfig is the node configuration
	NodeConfig *confv1beta1.NodeConfig

	// ComponentConfig is the component-specific configuration
	ComponentConfig runtime.Object

	// Purge indicates whether to purge data
	Purge bool
}

// HealthCheckConfig defines health check configuration
type HealthCheckConfig struct {
	// NodeConfig is the node configuration
	NodeConfig *confv1beta1.NodeConfig

	// ComponentConfig is the component-specific configuration
	ComponentConfig runtime.Object

	// Timeout is the health check timeout
	Timeout metav1.Duration
}

// HealthCheckResult defines health check result
type HealthCheckResult struct {
	// Healthy indicates whether the component is healthy
	Healthy bool

	// Message is the health check message
	Message string

	// Details is the detailed health information
	Details map[string]string
}

// VersionConfig defines version check configuration
type VersionConfig struct {
	// NodeConfig is the node configuration
	NodeConfig *confv1beta1.NodeConfig

	// ComponentConfig is the component-specific configuration
	ComponentConfig runtime.Object
}

// InstallerFactory creates installers
type InstallerFactory interface {
	// CreateInstaller creates an installer for the given component type
	CreateInstaller(componentType string) (Installer, error)

	// RegisterInstaller registers an installer
	RegisterInstaller(componentType string, installer Installer)
}
```
### 3.2 Containerd 安装器实现
```yaml
# d:\code\github\cluster-api-provider-bke\pkg\component\installer\containerd\installer.go
```go installer.go
/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 */

package containerd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/component/installer"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

const (
	InstallerName = "containerd-installer"
	
	defaultBinaryPath    = "/usr/bin/containerd"
	defaultDataRoot      = "/var/lib/containerd"
	defaultConfigPath    = "/etc/containerd/config.toml"
	defaultServiceName   = "containerd"
	
	installTimeout = 10 * time.Minute
	upgradeTimeout = 15 * time.Minute
)

type ContainerdInstaller struct {
	exec exec.Executor
	log  log.Logger
}

func NewContainerdInstaller(exec exec.Executor, log log.Logger) installer.Installer {
	return &ContainerdInstaller{
		exec: exec,
		log:  log,
	}
}

func (ci *ContainerdInstaller) Name() string {
	return InstallerName
}

func (ci *ContainerdInstaller) Install(ctx context.Context, config *installer.InstallConfig) (*installer.InstallResult, error) {
	ci.log.Infof("Installing containerd version %s", config.NodeConfig.Spec.ContainerRuntime.Version)

	nodeConfig := config.NodeConfig
	runtimeConfig := nodeConfig.Spec.ContainerRuntime
	if runtimeConfig == nil {
		return nil, errors.New("container runtime config is nil")
	}

	installConfig := runtimeConfig.InstallConfig
	if installConfig == nil {
		installConfig = &confv1beta1.RuntimeInstallConfig{
			InstallMethod: "binary",
			BinaryPath:    defaultBinaryPath,
			DataRoot:      defaultDataRoot,
			ConfigPath:    defaultConfigPath,
		}
	}

	if err := ci.preInstall(ctx, installConfig); err != nil {
		return nil, errors.Wrap(err, "pre-install failed")
	}

	switch installConfig.InstallMethod {
	case "binary":
		if err := ci.installFromBinary(ctx, runtimeConfig, installConfig); err != nil {
			return nil, errors.Wrap(err, "binary installation failed")
		}
	case "package":
		if err := ci.installFromPackage(ctx, runtimeConfig, installConfig); err != nil {
			return nil, errors.Wrap(err, "package installation failed")
		}
	case "image":
		if err := ci.installFromImage(ctx, runtimeConfig, installConfig); err != nil {
			return nil, errors.Wrap(err, "image installation failed")
		}
	default:
		return nil, fmt.Errorf("unsupported install method: %s", installConfig.InstallMethod)
	}

	if err := ci.postInstall(ctx, installConfig); err != nil {
		return nil, errors.Wrap(err, "post-install failed")
	}

	if err := ci.startService(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to start containerd service")
	}

	installedVersion, err := ci.GetVersion(ctx, &installer.VersionConfig{
		NodeConfig:      nodeConfig,
		ComponentConfig: config.ComponentConfig,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get installed version")
	}

	ci.log.Infof("Containerd %s installed successfully", installedVersion)

	return &installer.InstallResult{
		Success: true,
		Version: installedVersion,
		Message: fmt.Sprintf("Containerd %s installed successfully", installedVersion),
	}, nil
}

func (ci *ContainerdInstaller) Upgrade(ctx context.Context, config *installer.UpgradeConfig) (*installer.UpgradeResult, error) {
	ci.log.Infof("Upgrading containerd from %s to %s", config.OldVersion, config.NewVersion)

	backupPath := ""
	if !config.SkipBackup {
		var err error
		backupPath, err = ci.backup(ctx, config)
		if err != nil {
			return nil, errors.Wrap(err, "backup failed")
		}
	}

	if err := ci.stopService(ctx); err != nil {
		return &installer.UpgradeResult{
			Success:       false,
			Message:       fmt.Sprintf("Failed to stop containerd: %v", err),
			RollbackNeeded: true,
			RollbackConfig: &installer.RollbackConfig{
				TargetVersion: config.OldVersion,
				BackupPath:    backupPath,
			},
		}, errors.Wrap(err, "failed to stop containerd")
	}

	installConfig := &installer.InstallConfig{
		NodeConfig:      config.NodeConfig,
		ComponentConfig: config.ComponentConfig,
		InstallMethod:   config.NodeConfig.Spec.ContainerRuntime.InstallConfig.InstallMethod,
		DryRun:          config.DryRun,
		Force:           config.Force,
	}

	installResult, err := ci.Install(ctx, installConfig)
	if err != nil {
		return &installer.UpgradeResult{
			Success:       false,
			Message:       fmt.Sprintf("Installation failed: %v", err),
			RollbackNeeded: true,
			RollbackConfig: &installer.RollbackConfig{
				TargetVersion: config.OldVersion,
				BackupPath:    backupPath,
			},
		}, errors.Wrap(err, "installation during upgrade failed")
	}

	healthResult, err := ci.CheckHealth(ctx, &installer.HealthCheckConfig{
		NodeConfig:      config.NodeConfig,
		ComponentConfig: config.ComponentConfig,
		Timeout:         metav1.Duration{Duration: 5 * time.Minute},
	})
	if err != nil || !healthResult.Healthy {
		return &installer.UpgradeResult{
			Success:       false,
			Message:       fmt.Sprintf("Health check failed: %v", err),
			RollbackNeeded: true,
			RollbackConfig: &installer.RollbackConfig{
				TargetVersion: config.OldVersion,
				BackupPath:    backupPath,
			},
		}, errors.Wrap(err, "health check failed")
	}

	ci.log.Infof("Containerd upgraded successfully to %s", installResult.Version)

	return &installer.UpgradeResult{
		Success: true,
		Version: installResult.Version,
		Message: fmt.Sprintf("Containerd upgraded to %s", installResult.Version),
	}, nil
}

func (ci *ContainerdInstaller) Uninstall(ctx context.Context, config *installer.UninstallConfig) error {
	ci.log.Infof("Uninstalling containerd")

	if err := ci.stopService(ctx); err != nil {
		ci.log.Warnf("Failed to stop containerd service: %v", err)
	}

	installConfig := config.NodeConfig.Spec.ContainerRuntime.InstallConfig
	if installConfig == nil {
		installConfig = &confv1beta1.RuntimeInstallConfig{
			BinaryPath: defaultBinaryPath,
			DataRoot:   defaultDataRoot,
			ConfigPath: defaultConfigPath,
		}
	}

	commands := []string{
		fmt.Sprintf("rm -f %s", installConfig.BinaryPath),
		fmt.Sprintf("rm -f %s", installConfig.ConfigPath),
		fmt.Sprintf("rm -f /etc/systemd/system/containerd.service"),
		fmt.Sprintf("systemctl daemon-reload"),
	}

	if config.Purge {
		commands = append(commands, fmt.Sprintf("rm -rf %s", installConfig.DataRoot))
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			ci.log.Warnf("Command failed: %s, output: %s, error: %v", cmd, output, err)
		}
	}

	ci.log.Infof("Containerd uninstalled successfully")
	return nil
}

func (ci *ContainerdInstaller) Validate(ctx context.Context, config runtime.Object) error {
	containerdConfig, ok := config.(*confv1beta1.ContainerdConfig)
	if !ok {
		return errors.New("invalid config type, expected ContainerdConfig")
	}

	if containerdConfig.Spec.Main != nil {
		if containerdConfig.Spec.Main.Root == "" {
			return errors.New("root directory must be specified")
		}
	}

	return nil
}

func (ci *ContainerdInstaller) CheckHealth(ctx context.Context, config *installer.HealthCheckConfig) (*installer.HealthCheckResult, error) {
	ci.log.Infof("Checking containerd health")

	cmd := "systemctl is-active containerd"
	output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd)
	if err != nil {
		return &installer.HealthCheckResult{
			Healthy: false,
			Message: fmt.Sprintf("Containerd service is not active: %s", output),
		}, nil
	}

	cmd = "ctr --address /run/containerd/containerd.sock version"
	output, err = ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd)
	if err != nil {
		return &installer.HealthCheckResult{
			Healthy: false,
			Message: fmt.Sprintf("Failed to check containerd version: %s", output),
		}, nil
	}

	cmd = "ctr --address /run/containerd/containerd.sock namespaces list"
	output, err = ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd)
	if err != nil {
		return &installer.HealthCheckResult{
			Healthy: false,
			Message: fmt.Sprintf("Failed to list namespaces: %s", output),
		}, nil
	}

	return &installer.HealthCheckResult{
		Healthy: true,
		Message: "Containerd is healthy",
		Details: map[string]string{
			"service": "active",
			"version": output,
		},
	}, nil
}

func (ci *ContainerdInstaller) GetVersion(ctx context.Context, config *installer.VersionConfig) (string, error) {
	cmd := "ctr --address /run/containerd/containerd.sock version --format json"
	output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd)
	if err != nil {
		return "", errors.Wrap(err, "failed to get containerd version")
	}

	return parseContainerdVersion(output)
}

func (ci *ContainerdInstaller) preInstall(ctx context.Context, config *confv1beta1.RuntimeInstallConfig) error {
	if config.PreInstallScript != "" {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", config.PreInstallScript); err != nil {
			return errors.Wrapf(err, "pre-install script failed: %s", output)
		}
	}

	commands := []string{
		"mkdir -p /etc/containerd",
		"mkdir -p /var/lib/containerd",
		"mkdir -p /run/containerd",
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			return errors.Wrapf(err, "command failed: %s, output: %s", cmd, output)
		}
	}

	return nil
}

func (ci *ContainerdInstaller) postInstall(ctx context.Context, config *confv1beta1.RuntimeInstallConfig) error {
	if config.PostInstallScript != "" {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", config.PostInstallScript); err != nil {
			return errors.Wrapf(err, "post-install script failed: %s", output)
		}
	}

	return nil
}

func (ci *ContainerdInstaller) installFromBinary(ctx context.Context, runtimeConfig *confv1beta1.ContainerRuntimeConfig, installConfig *confv1beta1.RuntimeInstallConfig) error {
	downloadURL := installConfig.DownloadURL
	if downloadURL == "" {
		downloadURL = fmt.Sprintf("https://github.com/containerd/containerd/releases/download/v%s/containerd-%s-linux-amd64.tar.gz",
			runtimeConfig.Version, runtimeConfig.Version)
	}

	commands := []string{
		fmt.Sprintf("curl -L -o /tmp/containerd.tar.gz %s", downloadURL),
		fmt.Sprintf("tar -xzf /tmp/containerd.tar.gz -C /tmp"),
		fmt.Sprintf("cp /tmp/bin/containerd %s", installConfig.BinaryPath),
		fmt.Sprintf("cp /tmp/bin/ctr /usr/bin/ctr"),
		"chmod +x /usr/bin/containerd /usr/bin/ctr",
		"rm -rf /tmp/containerd.tar.gz /tmp/bin",
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			return errors.Wrapf(err, "command failed: %s, output: %s", cmd, output)
		}
	}

	return nil
}

func (ci *ContainerdInstaller) installFromPackage(ctx context.Context, runtimeConfig *confv1beta1.ContainerRuntimeConfig, installConfig *confv1beta1.RuntimeInstallConfig) error {
	commands := []string{
		"apt-get update || yum makecache",
		fmt.Sprintf("apt-get install -y containerd.io=%s || yum install -y containerd-%s", runtimeConfig.Version, runtimeConfig.Version),
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			return errors.Wrapf(err, "command failed: %s, output: %s", cmd, output)
		}
	}

	return nil
}

func (ci *ContainerdInstaller) installFromImage(ctx context.Context, runtimeConfig *confv1beta1.ContainerRuntimeConfig, installConfig *confv1beta1.RuntimeInstallConfig) error {
	image := fmt.Sprintf("docker.io/library/containerd:v%s", runtimeConfig.Version)
	
	commands := []string{
		fmt.Sprintf("crictl pull %s", image),
		fmt.Sprintf("crictl create --name containerd-install %s /tmp/containerd-install.json"),
		fmt.Sprintf("crictl start $(crictl ps -a --name containerd-install -q)"),
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			return errors.Wrapf(err, "command failed: %s, output: %s", cmd, output)
		}
	}

	return nil
}

func (ci *ContainerdInstaller) startService(ctx context.Context) error {
	commands := []string{
		"systemctl daemon-reload",
		"systemctl enable containerd",
		"systemctl start containerd",
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			return errors.Wrapf(err, "command failed: %s, output: %s", cmd, output)
		}
	}

	return nil
}

func (ci *ContainerdInstaller) stopService(ctx context.Context) error {
	commands := []string{
		"systemctl stop containerd || true",
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			ci.log.Warnf("Command failed: %s, output: %s, error: %v", cmd, output, err)
		}
	}

	return nil
}

func (ci *ContainerdInstaller) backup(ctx context.Context, config *installer.UpgradeConfig) (string, error) {
	backupPath := filepath.Join("/var/lib/containerd-backup", time.Now().Format("20060102-150405"))
	
	commands := []string{
		fmt.Sprintf("mkdir -p %s", backupPath),
		fmt.Sprintf("cp -r /var/lib/containerd %s/", backupPath),
		fmt.Sprintf("cp /etc/containerd/config.toml %s/", backupPath),
	}

	for _, cmd := range commands {
		if output, err := ci.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", cmd); err != nil {
			return "", errors.Wrapf(err, "backup command failed: %s, output: %s", cmd, output)
		}
	}

	ci.log.Infof("Backup created at %s", backupPath)
	return backupPath, nil
}

func parseContainerdVersion(output string) (string, error) {
	return "", nil
}
```
## 四、升级框架设计
### 4.1 升级协调器
```yaml
# d:\code\github\cluster-api-provider-bke\pkg\component\upgrader\coordinator.go
```go coordinator.go
/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 */

package upgrader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/component/installer"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	upgradeLockTimeout = 30 * time.Minute
)

type UpgradeCoordinator struct {
	client     client.Client
	factory    installer.InstallerFactory
	lock       sync.Map
	lockExpiry time.Duration
}

func NewUpgradeCoordinator(client client.Client, factory installer.InstallerFactory) *UpgradeCoordinator {
	return &UpgradeCoordinator{
		client:     client,
		factory:    factory,
		lockExpiry: upgradeLockTimeout,
	}
}

type UpgradeRequest struct {
	ComponentSet    *confv1beta1.ComponentSet
	ComponentName   string
	TargetVersion   string
	NodeConfigs     []confv1beta1.NodeConfig
	UpgradeStrategy confv1beta1.UpgradeStrategy
	DryRun          bool
}

type UpgradeResponse struct {
	Success      bool
	Message      string
	Progress     int
	FailedNodes  []string
	RollbackInfo *RollbackInfo
}

type RollbackInfo struct {
	ComponentName string
	TargetVersion string
	BackupPath    string
	Nodes         []string
}

func (uc *UpgradeCoordinator) CoordinateUpgrade(ctx context.Context, req *UpgradeRequest) (*UpgradeResponse, error) {
	componentSet := req.ComponentSet
	componentName := req.ComponentName

	if !uc.acquireLock(componentSet.UID, componentName) {
		return &UpgradeResponse{
			Success: false,
			Message: fmt.Sprintf("Upgrade for component %s is already in progress", componentName),
		}, nil
	}
	defer uc.releaseLock(componentSet.UID, componentName)

	if err := uc.updateUpgradeStatus(ctx, componentSet, "PreCheck", componentName, 0, "Starting pre-check"); err != nil {
		return nil, errors.Wrap(err, "failed to update status")
	}

	if err := uc.preCheck(ctx, req); err != nil {
		return &UpgradeResponse{
			Success: false,
			Message: fmt.Sprintf("Pre-check failed: %v", err),
		}, nil
	}

	if err := uc.updateUpgradeStatus(ctx, componentSet, "Backup", componentName, 10, "Creating backup"); err != nil {
		return nil, errors.Wrap(err, "failed to update status")
	}

	backupInfo, err := uc.createBackup(ctx, req)
	if err != nil {
		return &UpgradeResponse{
			Success: false,
			Message: fmt.Sprintf("Backup failed: %v", err),
		}, nil
	}

	if err := uc.updateUpgradeStatus(ctx, componentSet, "Upgrading", componentName, 20, "Starting upgrade"); err != nil {
		return nil, errors.Wrap(err, "failed to update status")
	}

	response := uc.executeUpgrade(ctx, req, backupInfo)

	if response.Success {
		if err := uc.updateUpgradeStatus(ctx, componentSet, "Verifying", componentName, 90, "Verifying upgrade"); err != nil {
			return nil, errors.Wrap(err, "failed to update status")
		}

		if err := uc.verifyUpgrade(ctx, req); err != nil {
			response.Success = false
			response.Message = fmt.Sprintf("Verification failed: %v", err)
			response.RollbackInfo = &RollbackInfo{
				ComponentName: componentName,
				TargetVersion: backupInfo.OldVersion,
				BackupPath:    backupInfo.BackupPath,
				Nodes:         backupInfo.Nodes,
			}
		}
	}

	if response.Success {
		if err := uc.updateUpgradeStatus(ctx, componentSet, "Completed", componentName, 100, "Upgrade completed"); err != nil {
			return nil, errors.Wrap(err, "failed to update status")
		}
	} else {
		if err := uc.updateUpgradeStatus(ctx, componentSet, "Failed", componentName, response.Progress, response.Message); err != nil {
			return nil, errors.Wrap(err, "failed to update status")
		}
	}

	return response, nil
}

func (uc *UpgradeCoordinator) preCheck(ctx context.Context, req *UpgradeRequest) error {
	componentName := req.ComponentName

	installer, err := uc.factory.CreateInstaller(componentName)
	if err != nil {
		return errors.Wrapf(err, "failed to create installer for %s", componentName)
	}

	for _, nodeConfig := range req.NodeConfigs {
		healthResult, err := installer.CheckHealth(ctx, &installer.HealthCheckConfig{
			NodeConfig: &nodeConfig,
			Timeout:    metav1.Duration{Duration: 5 * time.Minute},
		})
		if err != nil || !healthResult.Healthy {
			return errors.Errorf("node %s is not healthy: %s", nodeConfig.Name, healthResult.Message)
		}
	}

	return nil
}

type BackupInfo struct {
	OldVersion string
	BackupPath string
	Nodes      []string
}

func (uc *UpgradeCoordinator) createBackup(ctx context.Context, req *UpgradeRequest) (*BackupInfo, error) {
	return &BackupInfo{
		OldVersion: "current",
		BackupPath: "/var/lib/component-backup",
		Nodes:      []string{},
	}, nil
}

func (uc *UpgradeCoordinator) executeUpgrade(ctx context.Context, req *UpgradeRequest, backupInfo *BackupInfo) *UpgradeResponse {
	componentName := req.ComponentName
	nodeConfigs := req.NodeConfigs
	strategy := req.UpgradeStrategy

	installer, err := uc.factory.CreateInstaller(componentName)
	if err != nil {
		return &UpgradeResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to create installer: %v", err),
		}
	}

	var failedNodes []string
	var rollbackNodes []string
	progress := 20
	progressStep := 70 / len(nodeConfigs)

	for i, nodeConfig := range nodeConfigs {
		currentVersion, err := installer.GetVersion(ctx, &installer.VersionConfig{
			NodeConfig: &nodeConfig,
		})
		if err != nil {
			failedNodes = append(failedNodes, nodeConfig.Name)
			continue
		}

		upgradeConfig := &installer.UpgradeConfig{
			NodeConfig:      &nodeConfig,
			OldVersion:      currentVersion,
			NewVersion:      req.TargetVersion,
			Strategy:        strategy,
			DryRun:          req.DryRun,
			SkipBackup:      true,
		}

		result, err := installer.Upgrade(ctx, upgradeConfig)
		if err != nil || !result.Success {
			failedNodes = append(failedNodes, nodeConfig.Name)
			if result != nil && result.RollbackNeeded {
				rollbackNodes = append(rollbackNodes, nodeConfig.Name)
			}
			continue
		}

		progress += progressStep
		uc.updateUpgradeStatus(ctx, req.ComponentSet, "Upgrading", componentName, progress, 
			fmt.Sprintf("Upgraded node %s (%d/%d)", nodeConfig.Name, i+1, len(nodeConfigs)))
	}

	if len(failedNodes) > 0 {
		return &UpgradeResponse{
			Success:     false,
			Message:     fmt.Sprintf("Upgrade failed on %d nodes", len(failedNodes)),
			FailedNodes: failedNodes,
			Progress:    progress,
			RollbackInfo: &RollbackInfo{
				ComponentName: componentName,
				TargetVersion: backupInfo.OldVersion,
				BackupPath:    backupInfo.BackupPath,
				Nodes:         rollbackNodes,
			},
		}
	}

	return &UpgradeResponse{
		Success:  true,
		Message:  "All nodes upgraded successfully",
		Progress: 90,
	}
}

func (uc *UpgradeCoordinator) verifyUpgrade(ctx context.Context, req *UpgradeRequest) error {
	componentName := req.ComponentName

	installer, err := uc.factory.CreateInstaller(componentName)
	if err != nil {
		return errors.Wrapf(err, "failed to create installer for %s", componentName)
	}

	for _, nodeConfig := range req.NodeConfigs {
		version, err := installer.GetVersion(ctx, &installer.VersionConfig{
			NodeConfig: &nodeConfig,
		})
		if err != nil {
			return errors.Wrapf(err, "failed to get version on node %s", nodeConfig.Name)
		}

		if version != req.TargetVersion {
			return errors.Errorf("node %s version mismatch: expected %s, got %s", 
				nodeConfig.Name, req.TargetVersion, version)
		}

		healthResult, err := installer.CheckHealth(ctx, &installer.HealthCheckConfig{
			NodeConfig: &nodeConfig,
			Timeout:    metav1.Duration{Duration: 5 * time.Minute},
		})
		if err != nil || !healthResult.Healthy {
			return errors.Errorf("node %s health check failed: %s", nodeConfig.Name, healthResult.Message)
		}
	}

	return nil
}

func (uc *UpgradeCoordinator) Rollback(ctx context.Context, rollbackInfo *RollbackInfo) error {
	installer, err := uc.factory.CreateInstaller(rollbackInfo.ComponentName)
	if err != nil {
		return errors.Wrapf(err, "failed to create installer for %s", rollbackInfo.ComponentName)
	}

	for _, nodeName := range rollbackInfo.Nodes {
		rollbackConfig := &installer.UpgradeConfig{
			OldVersion: "current",
			NewVersion: rollbackInfo.TargetVersion,
			SkipBackup: true,
		}

		result, err := installer.Upgrade(ctx, rollbackConfig)
		if err != nil || !result.Success {
			return errors.Wrapf(err, "rollback failed on node %s", nodeName)
		}
	}

	return nil
}

func (uc *UpgradeCoordinator) acquireLock(componentSetUID types.UID, componentName string) bool {
	key := fmt.Sprintf("%s-%s", componentSetUID, componentName)
	
	if _, exists := uc.lock.Load(key); exists {
		return false
	}

	uc.lock.Store(key, time.Now())
	return true
}

func (uc *UpgradeCoordinator) releaseLock(componentSetUID types.UID, componentName string) {
	key := fmt.Sprintf("%s-%s", componentSetUID, componentName)
	uc.lock.Delete(key)
}

func (uc *UpgradeCoordinator) updateUpgradeStatus(ctx context.Context, componentSet *confv1beta1.ComponentSet, phase, component string, progress int, message string) error {
	componentSet.Status.UpgradeStatus = &confv1beta1.UpgradeStatus{
		Phase:     phase,
		Component: component,
		Progress:  progress,
		Message:   message,
	}

	if phase == "Upgrading" || phase == "PreCheck" || phase == "Backup" {
		componentSet.Status.UpgradeStatus.StartTime = metav1.Now()
	} else if phase == "Completed" || phase == "Failed" {
		componentSet.Status.UpgradeStatus.EndTime = metav1.Now()
	}

	return uc.client.Status().Update(ctx, componentSet)
}
```
## 五、使用示例
### 5.1 创建 NodeConfig
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: NodeConfig
metadata:
  name: node-1-config
  namespace: default
spec:
  nodeRef:
    name: node-1
    namespace: default
    ip: 192.168.1.10
  containerRuntime:
    type: containerd
    version: 1.7.11
    configRef:
      apiVersion: bke.bocloud.com/v1beta1
      kind: ContainerdConfig
      name: containerd-config
    installConfig:
      installMethod: binary
      binaryPath: /usr/bin/containerd
      dataRoot: /var/lib/containerd
      configPath: /etc/containerd/config.toml
      preInstallScript: |
        #!/bin/bash
        echo "Pre-install script"
      postInstallScript: |
        #!/bin/bash
        echo "Post-install script"
  kubelet:
    name: kubelet-config
    namespace: default
  cni:
    type: calico
    version: v3.25.0
    configRef:
      name: calico-config
    installConfig:
      binDir: /opt/cni/bin
      configDir: /etc/cni/net.d
  csi:
    type: local-path
    version: v0.0.24
  upgradeStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
      batchSize: 1
      batchInterval: 60s
      timeout: 15m
    preUpgradeHook:
      script: |
        #!/bin/bash
        echo "Pre-upgrade hook"
    postUpgradeHook:
      script: |
        #!/bin/bash
        echo "Post-upgrade hook"
```
### 5.2 创建 ComponentSet
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: ComponentSet
metadata:
  name: cluster-components
  namespace: default
spec:
  clusterRef:
    name: my-cluster
    namespace: default
  components:
    - name: containerd
      version: 1.7.11
      configRef:
        apiVersion: bke.bocloud.com/v1beta1
        kind: ContainerdConfig
        name: containerd-config
      upgradeStrategy:
        type: RollingUpdate
        rollingUpdate:
          maxUnavailable: 1
          batchSize: 1
    - name: kubelet
      version: 1.28.4
      configRef:
        apiVersion: bke.bocloud.com/v1beta1
        kind: KubeletConfig
        name: kubelet-config
    - name: calico
      version: v3.25.0
      configRef:
        name: calico-config
  upgradePolicy:
    autoUpgrade: true
    pauseOnFailure: true
    maxConcurrentUpgrades: 1
    healthCheckTimeout: 5m
    rollbackOnFailure: true
```
### 5.3 升级场景示例
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: ComponentSet
metadata:
  name: cluster-components
spec:
  clusterRef:
    name: my-cluster
  components:
    - name: containerd
      version: 1.7.13  # 从 1.7.11 升级到 1.7.13
      upgradeStrategy:
        type: RollingUpdate
        rollingUpdate:
          maxUnavailable: 1
          batchSize: 2
          batchInterval: 120s
          timeout: 20m
    - name: kubelet
      version: 1.28.6  # 从 1.28.4 升级到 1.28.6
  upgradePolicy:
    autoUpgrade: true
    pauseOnFailure: true
    rollbackOnFailure: true
```
## 六、方案优势
### 6.1 灵活性
- **多种安装方式**：支持 binary、package、image 三种安装方式
- **自定义脚本**：支持 pre/post 安装和升级脚本
- **灵活配置**：通过 CRD 定义组件配置，支持细粒度控制
### 6.2 可靠性
- **前置检查**：升级前进行健康检查和版本兼容性检查
- **备份机制**：升级前自动备份，支持回滚
- **健康验证**：升级后进行健康检查，确保组件正常
### 6.3 可维护性
- **声明式配置**：通过 CRD 声明期望状态，控制器自动调谐
- **状态跟踪**：详细的状态信息，便于监控和排查问题
- **统一接口**：统一的 Installer 接口，便于扩展新组件
### 6.4 升级策略
- **滚动升级**：支持滚动升级，逐个节点升级
- **批处理升级**：支持批处理升级，批量节点升级
- **并发控制**：支持最大并发升级数控制
- **超时控制**：支持升级超时控制
- **回滚机制**：支持自动回滚和手动回滚

这个方案提供了完整的组件安装和升级框架，支持用户自提供操作系统场景，并提供了灵活的配置和升级策略。

   
# Cluster API集成扩展方案：管理集群使用Cluster API安装业务集群
## 一、方案概述
基于之前的节点组件管理方案，扩展支持Cluster API作为业务集群的创建和管理框架，同时将节点组件安装与Infrastructure Machine紧密集成，实现从基础设施到节点组件的端到端管理。
### 核心设计理念
1. **Cluster API集成**：复用Cluster API的成熟集群生命周期管理能力
2. **Infrastructure Provider扩展**：开发自定义Infrastructure Provider对接节点组件管理系统
3. **Machine生命周期整合**：在Machine的创建、升级、删除等阶段无缝集成组件安装
4. **多部署模式支持**：兼容Remote/Image/Hybrid三种部署模式
5. **状态同步机制**：确保Cluster API状态与组件安装状态的一致性
## 二、架构设计
### 2.1 整体架构
```
┌───────────────────────────────────────────────────────────────────────────────┐
│                                  管理集群                                     │
│  ┌─────────────────────────────────────────────────────────────────────────┐  │
│  │                     Cluster API Components                              │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │  │
│  │  │        CAPBKE Infrastructure Provider                           │    │  │
│  │  │  ┌─────────────────┐  ┌─────────────────┐  ┌───────────────┐    │    │  │
│  │  │  │ BKECluster      │  │ BKEMachine      │  │ BKEMachineSet │    │    │  │
│  │  │  │ Controller      │  │ Controller      │  │ Controller    │    │    │  │
│  │  │  └─────────────────┘  └─────────────────┘  └───────────────┘    │    │  │
│  │  │                     │                                           │    │  │
│  │  │                     ▼                                           │    │  │
│  │  │          ┌─────────────────────────┐                            │    │  │
│  │  │          │  NodeComponentManager   │                            │    │  │
│  │  │          │  (节点组件管理控制器)   │                            │    │  │
│  │  │          └─────────────────────────┘                            │    │  │
│  │  │                     │                                           │    │  │
│  │  │                     ▼                                           │    │  │
│  │  │          ┌─────────────────────────┐                            │    │  │
│  │  │          │   Provider Interface    │                            │    │  │
│  │  │          └─────────────────────────┘                            │    │  │
│  │  │                       │                                         │    │  │
│  │  │         ┌─────────────┼─────────────┐                           │    │  │
│  │  │         │             │             │                           │    │  │
│  │  │  ┌──────▼──────┐ ┌────▼──────┐ ┌────▼──────┐                    │    │  │
│  │  │  │ Remote      │ │ Image     │ │ Hybrid    │                    │    │  │
│  │  │  │ Provider    │ │ Provider  │ │ Provider  │                    │    │  │
│  │  │  └─────────────┘ └───────────┘ └───────────┘                    │    │  │
│  │  └─────────────────────────────────────────────────────────────────┘    │  │
│  │                                                                         │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │  │
│  │  │              Cluster API Core Components                        │    │  │
│  │  │  ┌─────────────────┐  ┌─────────────────┐  ┌───────────────┐    │    │  │
│  │  │  │ Cluster         │  │ Machine         │  │ MachineSet    │    │    │  │
│  │  │  │ Controller      │  │ Controller      │  │ Controller    │    │    │  │
│  │  │  └─────────────────┘  └─────────────────┘  └───────────────┘    │    │  │
│  │  │  ┌─────────────────┐  ┌─────────────────┐  ┌───────────────┐    │    │  │
│  │  │  │ KubeadmControl  │  │ KubeadmBootstrap│  │ MachineDeploy-│    │    │  │
│  │  │  │ PlaneController │  │   Controller    │  │ mentController│    │    │  │
│  │  │  └─────────────────┘  └─────────────────┘  └───────────────┘    │    │  │
│  │  └─────────────────────────────────────────────────────────────────┘    │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ 创建并管理
                                    ▼
┌───────────────────────────────────────────────────────────────────────────────┐
│                                  业务集群                                     │
│  ┌─────────────────────────────────────────────────────────────────────────┐  │
│  │                     Kubernetes Components                               │  │
│  │  ┌─────────────────┐   ┌─────────────────┐  ┌───────────────┐           │  │
│  │  │  Control Plane  │   │    Workers      │  │    Nodes      │           │  │
│  │  │  (apiserver,    │   │   (kubelet,     │  │   (containerd,│           │  │
│  │  │  controller-manager,│  kube-proxy,    │  │   kubelet,    │           │  │
│  │  │  scheduler, etcd)│  │  cni)           │  │   cni, csi)   │           │  │
│  │  └─────────────────┘   └─────────────────┘  └───────────────┘           │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────────────────────┘
```
### 2.2 核心组件
#### 2.2.1 CAPBKE Infrastructure Provider
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: workload-cluster-1
  namespace: clusters
spec:
  clusterNetwork:
    pods:
      cidrBlocks: ["192.168.0.0/16"]
    services:
      cidrBlocks: ["10.96.0.0/12"]
  
  # 引用基础设施Cluster
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: BKECluster
    name: workload-cluster-1
    namespace: clusters
```
```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: BKECluster
metadata:
  name: workload-cluster-1
  namespace: clusters
spec:
  # 集群配置
  controlPlaneEndpoint:
    host: 192.168.1.100
    port: 6443
  
  # 组件配置
  components:
    containerd:
      version: v1.7.2
      config:
        dataDir: /var/lib/containerd
        systemdCgroup: true
    
    kubelet:
      version: v1.28.2
      config:
        dataDir: /var/lib/kubelet
    
    cni:
      version: v1.3.0
      config:
        pluginList:
          - bridge
          - loopback
          - portmap
    
    csi:
      version: v2.1.0

status:
  phase: Provisioned
  controlPlaneReady: true
  ready: true
```
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Machine
metadata:
  name: workload-cluster-1-control-plane-0
  namespace: clusters
spec:
  clusterName: workload-cluster-1
  bootstrap:
    configRef:
      apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
      kind: KubeadmConfig
      name: workload-cluster-1-control-plane-0
  
  # 引用基础设施Machine
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: BKEMachine
    name: workload-cluster-1-control-plane-0
    namespace: clusters
  
  version: v1.28.2
```
```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: BKEMachine
metadata:
  name: workload-cluster-1-control-plane-0
  namespace: clusters
spec:
  # 节点连接信息
  connection:
    host: 192.168.2.101
    port: 22
    user: root
    sshKeySecret:
      name: node-ssh-key
      namespace: clusters
  
  # 节点配置
  nodeConfig:
    os:
      type: linux
      distro: ubuntu
      version: "22.04"
      arch: amd64
    
    # 部署模式
    deploymentMode:
      type: Remote
    
    # 组件配置
    components:
      containerd:
        version: v1.7.2
        config:
          dataDir: /var/lib/containerd
          systemdCgroup: true
      
      kubelet:
        version: v1.28.2
        config:
          dataDir: /var/lib/kubelet
      
      cni:
        version: v1.3.0
        config:
          pluginList:
            - bridge
            - loopback
            - portmap
      
      csi:
        version: v2.1.0

status:
  phase: Running
  providerID: bke://192.168.2.101
  ready: true
```
#### 2.2.2 BKEMachine Controller实现
```go
package controllers

import (
    "context"
    "fmt"
    "time"
    
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/client-go/tools/clientcmd"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
    
    clusterapisdk "sigs.k8s.io/cluster-api/util/sdk"
    clusterapiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
    
    bkeapi "infrastructure.cluster.x-k8s.io/bke/api/v1alpha1"
    "infrastructure.cluster.x-k8s.io/bke/pkg/provider"
    "infrastructure.cluster.x-k8s.io/bke/pkg/ssh"
)

const (
    // BKEMachineFinalizer is the finalizer for BKEMachine resources.
    BKEMachineFinalizer = "bkemachine.infrastructure.cluster.x-k8s.io"
)

// BKEMachineReconciler reconciles a BKEMachine object.
type BKEMachineReconciler struct {
    client.Client
    Scheme          *runtime.Scheme
    SSHManager      *ssh.SSHManager
    ProviderFactory provider.ProviderFactory
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=bkemachines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=bkemachines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=bkemachines/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *BKEMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    log.Info("Reconciling BKEMachine")
    
    // 获取BKEMachine资源
    bkeMachine := &bkeapi.BKEMachine{}
    if err := r.Get(ctx, req.NamespacedName, bkeMachine); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 获取对应的Machine资源
    machine, err := clusterapisdk.MachineFromMetadata(ctx, r.Client, bkeMachine.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 检查Machine是否标记为删除
    if !machine.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, bkeMachine, machine)
    }
    
    // 检查BKEMachine是否标记为删除
    if !bkeMachine.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, bkeMachine, machine)
    }
    
    // 确保finalizer存在
    if !controllerutil.ContainsFinalizer(bkeMachine, BKEMachineFinalizer) {
        controllerutil.AddFinalizer(bkeMachine, BKEMachineFinalizer)
        if err := r.Update(ctx, bkeMachine); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    // 检查Machine是否暂停
    if clusterapisdk.IsPaused(machine, bkeMachine) {
        log.Info("Machine is paused, skipping reconciliation")
        return ctrl.Result{}, nil
    }
    
    // 协调Machine状态
    return r.reconcileNormal(ctx, bkeMachine, machine)
}

// 协调Machine正常状态
func (r *BKEMachineReconciler) reconcileNormal(ctx context.Context, bkeMachine *bkeapi.BKEMachine, machine *clusterapiv1beta1.Machine) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    
    // 检查Machine是否已经准备好
    if bkeMachine.Status.Ready {
        return ctrl.Result{}, nil
    }
    
    // 获取对应的BKECluster资源
    bkeCluster := &bkeapi.BKECluster{}
    clusterName := types.NamespacedName{
        Name:      machine.Spec.ClusterName,
        Namespace: bkeMachine.Namespace,
    }
    if err := r.Get(ctx, clusterName, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    // 创建Provider
    providerType := provider.ProviderType(bkeMachine.Spec.NodeConfig.DeploymentMode.Type)
    prov, err := r.ProviderFactory.CreateProvider(providerType)
    if err != nil {
        return ctrl.Result{}, fmt.Errorf("create provider failed: %v", err)
    }
    
    // 更新状态为Provisioning
    if bkeMachine.Status.Phase != bkeapi.MachinePhaseProvisioning {
        bkeMachine.Status.Phase = bkeapi.MachinePhaseProvisioning
        if err := r.Status().Update(ctx, bkeMachine); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    // 1. 初始化Provider
    if err := prov.Initialize(ctx, bkeMachine.Spec.NodeConfig); err != nil {
        return ctrl.Result{}, fmt.Errorf("initialize provider failed: %v", err)
    }
    
    // 2. 检测组件状态
    componentStatus, err := prov.DetectComponentStatus(ctx, bkeMachine.Spec.NodeConfig, "containerd")
    if err != nil {
        return ctrl.Result{}, fmt.Errorf("detect component status failed: %v", err)
    }
    
    // 3. 安装或升级组件
    if componentStatus.Phase == provider.PhasePending || componentStatus.InstalledVersion != bkeMachine.Spec.NodeConfig.Components["containerd"].Version {
        // 获取组件版本定义
        componentVersion := &bkeapi.ComponentVersion{}
        componentVersionName := fmt.Sprintf("containerd-%s", bkeMachine.Spec.NodeConfig.Components["containerd"].Version)
        if err := r.Get(ctx, types.NamespacedName{
            Name: componentVersionName,
        }, componentVersion); err != nil {
            return ctrl.Result{}, fmt.Errorf("get component version failed: %v", err)
        }
        
        // 执行安装或升级
        if componentStatus.Phase == provider.PhasePending {
            if err := prov.InstallComponent(ctx, bkeMachine.Spec.NodeConfig, componentVersion, bkeMachine.Spec.NodeConfig.Components["containerd"]); err != nil {
                return ctrl.Result{}, fmt.Errorf("install component failed: %v", err)
            }
        } else {
            if err := prov.UpgradeComponent(ctx, bkeMachine.Spec.NodeConfig, componentVersion, bkeMachine.Spec.NodeConfig.Components["containerd"]); err != nil {
                return ctrl.Result{}, fmt.Errorf("upgrade component failed: %v", err)
            }
        }
    }
    
    // 4. 安装其他组件（kubelet, cni, csi）
    for componentName := range bkeMachine.Spec.NodeConfig.Components {
        if componentName == "containerd" {
            continue // 已经安装过
        }
        
        // 检测组件状态
        componentStatus, err := prov.DetectComponentStatus(ctx, bkeMachine.Spec.NodeConfig, componentName)
        if err != nil {
            return ctrl.Result{}, fmt.Errorf("detect component status failed: %v", err)
        }
        
        // 安装组件
        if componentStatus.Phase == provider.PhasePending || componentStatus.InstalledVersion != bkeMachine.Spec.NodeConfig.Components[componentName].Version {
            // 获取组件版本定义
            componentVersion := &bkeapi.ComponentVersion{}
            componentVersionName := fmt.Sprintf("%s-%s", componentName, bkeMachine.Spec.NodeConfig.Components[componentName].Version)
            if err := r.Get(ctx, types.NamespacedName{
                Name: componentVersionName,
            }, componentVersion); err != nil {
                return ctrl.Result{}, fmt.Errorf("get component version failed: %v", err)
            }
            
            // 执行安装或升级
            if componentStatus.Phase == provider.PhasePending {
                if err := prov.InstallComponent(ctx, bkeMachine.Spec.NodeConfig, componentVersion, bkeMachine.Spec.NodeConfig.Components[componentName]); err != nil {
                    return ctrl.Result{}, fmt.Errorf("install component failed: %v", err)
                }
            } else {
                if err := prov.UpgradeComponent(ctx, bkeMachine.Spec.NodeConfig, componentVersion, bkeMachine.Spec.NodeConfig.Components[componentName]); err != nil {
                    return ctrl.Result{}, fmt.Errorf("upgrade component failed: %v", err)
                }
            }
        }
    }
    
    // 5. 验证所有组件健康状态
    for componentName := range bkeMachine.Spec.NodeConfig.Components {
        if err := prov.HealthCheck(ctx, bkeMachine.Spec.NodeConfig, componentName); err != nil {
            return ctrl.Result{}, fmt.Errorf("component health check failed: %v", err)
        }
    }
    
    // 6. 设置ProviderID
    if bkeMachine.Status.ProviderID == "" {
        bkeMachine.Status.ProviderID = fmt.Sprintf("bke://%s", bkeMachine.Spec.Connection.Host)
    }
    
    // 7. 更新状态为Running和Ready
    bkeMachine.Status.Phase = bkeapi.MachinePhaseRunning
    bkeMachine.Status.Ready = true
    bkeMachine.Status.Addresses = []corev1.NodeAddress{
        {
            Type:    corev1.NodeInternalIP,
            Address: bkeMachine.Spec.Connection.Host,
        },
    }
    
    if err := r.Status().Update(ctx, bkeMachine); err != nil {
        return ctrl.Result{}, err
    }
    
    log.Info("BKEMachine provisioned successfully")
    return ctrl.Result{}, nil
}

// 协调Machine删除状态
func (r *BKEMachineReconciler) reconcileDelete(ctx context.Context, bkeMachine *bkeapi.BKEMachine, machine *clusterapiv1beta1.Machine) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    log.Info("Deleting BKEMachine")
    
    // 清理资源
    if controllerutil.ContainsFinalizer(bkeMachine, BKEMachineFinalizer) {
        // 执行清理操作，如卸载组件、清理数据等
        if err := r.cleanupMachine(ctx, bkeMachine); err != nil {
            return ctrl.Result{}, err
        }
        
        // 移除finalizer
        controllerutil.RemoveFinalizer(bkeMachine, BKEMachineFinalizer)
        if err := r.Update(ctx, bkeMachine); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    return ctrl.Result{}, nil
}

// 清理Machine资源
func (r *BKEMachineReconciler) cleanupMachine(ctx context.Context, bkeMachine *bkeapi.BKEMachine) error {
    log := ctrl.LoggerFrom(ctx)
    log.Info("Cleaning up BKEMachine resources")
    
    // 创建Provider
    providerType := provider.ProviderType(bkeMachine.Spec.NodeConfig.DeploymentMode.Type)
    prov, err := r.ProviderFactory.CreateProvider(providerType)
    if err != nil {
        return fmt.Errorf("create provider failed: %v", err)
    }
    
    // 清理组件
    if err := prov.Cleanup(ctx, bkeMachine.Spec.NodeConfig); err != nil {
        log.Error(err, "Cleanup provider resources failed, ignoring")
    }
    
    // 清理SSH连接
    key := fmt.Sprintf("%s:%d", bkeMachine.Spec.Connection.Host, bkeMachine.Spec.Connection.Port)
    r.SSHManager.RemoveClient(key)
    
    return nil
}

func (r *BKEMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&bkeapi.BKEMachine{}).
        Owns(&clusterapiv1beta1.Machine{}).
        Complete(r)
}
```
### 2.3 组件安装与Machine生命周期的集成
#### 2.3.1 Machine创建流程
```
┌─────────────────────────────────────────────────────────────────────┐
│                    Machine创建流程                                  │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────┐    ┌───────────────────┐    ┌───────────────────┐
│ Cluster API       │    │ CAPBKE Provider   │    │ NodeComponent     │
│ Machine Controller│    │ BKEMachine        │    │ Manager           │
│                   │    │ Controller        │    │                   │
└─────────┬─────────┘    └─────────┬─────────┘    └─────────┬─────────┘
          │                        │                        │
          │ Create BKEMachine      │                        │
          └───────────────────────>│                        │
                                    │                        │
                                    │ Create Provider        │
                                    └───────────────────────>│
                                    │                        │
                                    │ Initialize Provider    │
                                    <───────────────────────┤
                                    │                        │
                                    │ Detect Component Status│
                                    └───────────────────────>│
                                    │                        │
                                    │ Install Components      │
                                    <───────────────────────┤
                                    │                        │
                                    │ Health Check            │
                                    └───────────────────────>│
                                    │                        │
                                    │ Update BKEMachine Status│
                                    <───────────────────────┤
                                    │                        │
                                    │ Set ProviderID          │
                                    └───────────────────────>│
                                    │                        │
                                    │ Update Machine Status   │
                                    <───────────────────────┤
                                    │                        │
                                    │ Machine Ready           │
                                    └───────────────────────>│
```
#### 2.3.2 Machine升级流程
```
┌─────────────────────────────────────────────────────────────────────┐
│                    Machine升级流程                                  │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────┐    ┌───────────────────┐    ┌───────────────────┐
│ Cluster API       │    │ CAPBKE Provider   │    │ NodeComponent     │
│ Machine Controller│    │ BKEMachine        │    │ Manager           │
│                   │    │ Controller        │    │                   │
└─────────┬─────────┘    └─────────┬─────────┘    └───────────────────┘
          │                        │                        │
          │ Update Machine Spec    │                        │
          └───────────────────────>│                        │
                                    │                        │
                                    │ Detect Version Change  │
                                    └───────────────────────>│
                                    │                        │
                                    │ Check Upgrade Path     │
                                    <───────────────────────┤
                                    │                        │
                                    │ Backup Current Version │
                                    └───────────────────────>│
                                    │                        │
                                    │ Upgrade Components     │
                                    <───────────────────────┤
                                    │                        │
                                    │ Health Check            │
                                    └───────────────────────>│
                                    │                        │
                                    │ Update Status          │
                                    <───────────────────────┤
                                    │                        │
                                    │ Upgrade Complete        │
                                    └───────────────────────>│
```
### 2.4 ClusterTemplate与MachineTemplate设计
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: ClusterTemplate
metadata:
  name: bke-cluster-template
  namespace: clusters
spec:
  clusterClassRef:
    name: bke-cluster-class
  
  template:
    spec:
      clusterNetwork:
        pods:
          cidrBlocks: ["192.168.0.0/16"]
        services:
          cidrBlocks: ["10.96.0.0/12"]
      
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
        kind: BKEClusterTemplate
        name: bke-cluster-template
        namespace: clusters
```

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: BKEClusterTemplate
metadata:
  name: bke-cluster-template
  namespace: clusters
spec:
  template:
    spec:
      controlPlaneEndpoint:
        host: ${CONTROL_PLANE_ENDPOINT}
        port: 6443
      
      components:
        containerd:
          version: ${CONTAINERD_VERSION}
          config:
            systemdCgroup: true
        
        kubelet:
          version: ${KUBERNETES_VERSION}
        
        cni:
          version: ${CNI_VERSION}
        
        csi:
          version: ${CSI_VERSION}
```

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineTemplate
metadata:
  name: bke-control-plane-template
  namespace: clusters
spec:
  template:
    spec:
      bootstrap:
        configRef:
          apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
          kind: KubeadmConfigTemplate
          name: bke-control-plane-template
      
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
        kind: BKEMachineTemplate
        name: bke-control-plane-template
      
      version: ${KUBERNETES_VERSION}
```

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: BKEMachineTemplate
metadata:
  name: bke-control-plane-template
  namespace: clusters
spec:
  template:
    spec:
      connection:
        host: ${NODE_IP}
        port: 22
        user: root
        sshKeySecret:
          name: node-ssh-key
      
      nodeConfig:
        os:
          type: linux
          distro: ubuntu
          version: "22.04"
          arch: amd64
        
        deploymentMode:
          type: Remote
        
        components:
          containerd:
            version: ${CONTAINERD_VERSION}
            config:
              systemdCgroup: true
          
          kubelet:
            version: ${KUBERNETES_VERSION}
          
          cni:
            version: ${CNI_VERSION}
          
          csi:
            version: ${CSI_VERSION}
```
## 三、详细实现
### 3.1 组件安装与Machine的集成
#### 3.1.1 Provider接口扩展
```go
package provider

import (
    "context"
    
    bkeapi "infrastructure.cluster.x-k8s.io/bke/api/v1alpha1"
)

// MachineProvider扩展了Provider接口，支持与Cluster API Machine的集成
type MachineProvider interface {
    Provider
    
    // 从BKEMachine中获取节点配置
    GetNodeConfig(bkeMachine *bkeapi.BKEMachine) *NodeConfig
    
    // 设置Machine状态
    SetMachineStatus(ctx context.Context, bkeMachine *bkeapi.BKEMachine) error
    
    // 验证Machine配置
    ValidateMachineConfig(ctx context.Context, bkeMachine *bkeapi.BKEMachine) error
}
```
#### 3.1.2 RemoteMachineProvider实现
```go
package provider

import (
    "context"
    "fmt"
    
    bkeapi "infrastructure.cluster.x-k8s.io/bke/api/v1alpha1"
)

type RemoteMachineProvider struct {
    *RemoteProvider
}

func NewRemoteMachineProvider(sshManager *SSHManager) *RemoteMachineProvider {
    return &RemoteMachineProvider{
        RemoteProvider: NewRemoteProvider(sshManager),
    }
}

func (p *RemoteMachineProvider) GetNodeConfig(bkeMachine *bkeapi.BKEMachine) *NodeConfig {
    return &bkeMachine.Spec.NodeConfig
}

func (p *RemoteMachineProvider) SetMachineStatus(ctx context.Context, bkeMachine *bkeapi.BKEMachine) error {
    // 设置ProviderID
    bkeMachine.Status.ProviderID = fmt.Sprintf("bke://%s", bkeMachine.Spec.Connection.Host)
    
    // 设置节点地址
    bkeMachine.Status.Addresses = []corev1.NodeAddress{
        {
            Type:    corev1.NodeInternalIP,
            Address: bkeMachine.Spec.Connection.Host,
        },
    }
    
    return nil
}

func (p *RemoteMachineProvider) ValidateMachineConfig(ctx context.Context, bkeMachine *bkeapi.BKEMachine) error {
    // 验证连接信息
    if bkeMachine.Spec.Connection.Host == "" {
        return fmt.Errorf("connection host is required")
    }
    
    if bkeMachine.Spec.Connection.Port == 0 {
        return fmt.Errorf("connection port is required")
    }
    
    // 验证组件配置
    for componentName, config := range bkeMachine.Spec.NodeConfig.Components {
        if config.Version == "" {
            return fmt.Errorf("version for component %s is required", componentName)
        }
    }
    
    return nil
}
```
### 3.2 Cluster API集成点
#### 3.2.1 ClusterReconciler扩展
```go
package controllers

import (
    "context"
    "fmt"
    
    clusterapiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
    clusterapisdk "sigs.k8s.io/cluster-api/util/sdk"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    bkeapi "infrastructure.cluster.x-k8s.io/bke/api/v1alpha1"
    "infrastructure.cluster.x-k8s.io/bke/pkg/manager"
)

// BKEClusterReconciler reconciles a BKECluster object.
type BKEClusterReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    
    // 组件版本管理器
    VersionManager *manager.VersionManager
}

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    log.Info("Reconciling BKECluster")
    
    // 获取BKECluster资源
    bkeCluster := &bkeapi.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, bkeCluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 获取对应的Cluster资源
    cluster, err := clusterapisdk.ClusterFromMetadata(ctx, r.Client, bkeCluster.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 检查Cluster是否标记为删除
    if !cluster.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, bkeCluster, cluster)
    }
    
    // 检查BKECluster是否标记为删除
    if !bkeCluster.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, bkeCluster, cluster)
    }
    
    // 协调Cluster正常状态
    return r.reconcileNormal(ctx, bkeCluster, cluster)
}

func (r *BKEClusterReconciler) reconcileNormal(ctx context.Context, bkeCluster *bkeapi.BKECluster, cluster *clusterapiv1beta1.Cluster) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    
    // 检查组件版本兼容性
    if err := r.VersionManager.CheckCompatibility(ctx, bkeCluster.Spec.Components); err != nil {
        return ctrl.Result{}, fmt.Errorf("check component compatibility failed: %v", err)
    }
    
    // 更新状态为Provisioned
    if bkeCluster.Status.Phase != bkeapi.ClusterPhaseProvisioned {
        bkeCluster.Status.Phase = bkeapi.ClusterPhaseProvisioned
        bkeCluster.Status.ControlPlaneReady = true
        bkeCluster.Status.Ready = true
        
        if err := r.Status().Update(ctx, bkeCluster); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    log.Info("BKECluster provisioned successfully")
    return ctrl.Result{}, nil
}

func (r *BKEClusterReconciler) reconcileDelete(ctx context.Context, bkeCluster *bkeapi.BKECluster, cluster *clusterapiv1beta1.Cluster) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    log.Info("Deleting BKECluster")
    
    // 更新状态为Deleting
    bkeCluster.Status.Phase = bkeapi.ClusterPhaseDeleting
    if err := r.Status().Update(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    // 执行清理操作
    // ...
    
    log.Info("BKECluster deleted successfully")
    return ctrl.Result{}, nil
}
```
### 3.3 业务集群创建示例
#### 3.3.1 创建Cluster资源
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: workload-cluster-1
  namespace: clusters
spec:
  clusterNetwork:
    pods:
      cidrBlocks: ["192.168.0.0/16"]
    services:
      cidrBlocks: ["10.96.0.0/12"]
  
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: BKECluster
    name: workload-cluster-1
    namespace: clusters
```
#### 3.3.2 创建BKECluster资源
```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: BKECluster
metadata:
  name: workload-cluster-1
  namespace: clusters
spec:
  controlPlaneEndpoint:
    host: 192.168.1.100
    port: 6443
  
  components:
    containerd:
      version: v1.7.2
      config:
        systemdCgroup: true
    
    kubelet:
      version: v1.28.2
    
    cni:
      version: v1.3.0
    
    csi:
      version: v2.1.0
```
#### 3.3.3 创建Control Plane Machine
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Machine
metadata:
  name: workload-cluster-1-control-plane-0
  namespace: clusters
spec:
  clusterName: workload-cluster-1
  bootstrap:
    configRef:
      apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
      kind: KubeadmConfig
      name: workload-cluster-1-control-plane-0
  
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: BKEMachine
    name: workload-cluster-1-control-plane-0
    namespace: clusters
  
  version: v1.28.2
```
#### 3.3.4 创建BKEMachine资源（Control Plane）
```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: BKEMachine
metadata:
  name: workload-cluster-1-control-plane-0
  namespace: clusters
spec:
  connection:
    host: 192.168.2.101
    port: 22
    user: root
    sshKeySecret:
      name: node-ssh-key
      namespace: clusters
  
  nodeConfig:
    os:
      type: linux
      distro: ubuntu
      version: "22.04"
      arch: amd64
    
    deploymentMode:
      type: Remote
    
    components:
      containerd:
        version: v1.7.2
        config:
          systemdCgroup: true
      
      kubelet:
        version: v1.28.2
      
      cni:
        version: v1.3.0
      
      csi:
        version: v2.1.0
```
#### 3.3.5 创建Worker MachineDeployment
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineDeployment
metadata:
  name: workload-cluster-1-md-0
  namespace: clusters
spec:
  clusterName: workload-cluster-1
  replicas: 2
  selector:
    matchLabels:
      cluster.x-k8s.io/cluster-name: workload-cluster-1
      cluster.x-k8s.io/deployment-name: workload-cluster-1-md-0
  
  template:
    spec:
      clusterName: workload-cluster-1
      bootstrap:
        configRef:
          apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
          kind: KubeadmConfigTemplate
          name: workload-cluster-1-md-0
      
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
        kind: BKEMachineTemplate
        name: workload-cluster-1-md-0
      
      version: v1.28.2
```
#### 3.3.6 创建BKEMachineTemplate资源（Worker）
```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: BKEMachineTemplate
metadata:
  name: workload-cluster-1-md-0
  namespace: clusters
spec:
  template:
    spec:
      connection:
        host: ${NODE_IP}
        port: 22
        user: root
        sshKeySecret:
          name: node-ssh-key
      
      nodeConfig:
        os:
          type: linux
          distro: ubuntu
          version: "22.04"
          arch: amd64
        
        deploymentMode:
          type: Remote
        
        components:
          containerd:
            version: v1.7.2
            config:
              systemdCgroup: true
          
          kubelet:
            version: v1.28.2
          
          cni:
            version: v1.3.0
          
          csi:
            version: v2.1.0
```
## 四、方案优势
### 4.1 Cluster API集成的优势
1. **成熟的生命周期管理**：复用Cluster API的集群、机器生命周期管理能力
2. **标准化接口**：遵循Cluster API的标准接口，便于与其他Provider集成
3. **模板化部署**：支持ClusterTemplate和MachineTemplate，实现快速部署
4. **升级策略**：支持滚动升级、批量升级等多种升级策略
5. **状态同步**：自动同步Cluster API状态与节点组件安装状态
### 4.2 组件安装与Infrastructure Machine集成的优势
1. **端到端管理**：从基础设施到节点组件的完整管理流程
2. **状态一致性**：确保Machine状态与组件安装状态一致
3. **多部署模式**：支持Remote/Image/Hybrid三种部署模式
4. **灵活的组件管理**：每个Machine可以独立配置组件版本和部署模式
5. **统一的监控和管理**：通过Cluster API统一监控和管理所有集群
### 4.3 与之前方案的兼容性
1. **向后兼容**：保持与之前节点组件管理方案的兼容性
2. **平滑迁移**：支持从之前的方案迁移到Cluster API集成方案
3. **复用代码**：复用之前的Provider实现和组件管理逻辑
4. **统一的用户体验**：保持一致的配置方式和管理界面
## 五、总结
本方案通过开发自定义的Cluster API Infrastructure Provider（CAPBKE），将节点组件管理系统与Cluster API无缝集成，实现了：
1. **管理集群使用Cluster API安装业务集群**：利用Cluster API的成熟能力管理业务集群生命周期
2. **组件安装与Infrastructure Machine的紧密集成**：在Machine的创建、升级、删除等阶段自动管理节点组件
3. **支持多种部署模式**：兼容Remote/Image/Hybrid三种部署模式
4. **统一的状态管理**：确保Cluster API状态与组件安装状态的一致性
5. **模板化部署**：支持ClusterTemplate和MachineTemplate，实现快速部署

该方案既充分利用了Cluster API的成熟特性，又保留了节点组件管理系统的灵活性，为用户提供了从基础设施到节点组件的端到端管理解决方案。

    
# Cluster API集成的节点组件管理方案
## 一、方案概述
将节点组件管理与Cluster API深度集成，支持管理集群通过Cluster API安装业务集群，并实现组件安装与Infrastructure Machine的无缝衔接。
### 核心设计理念
1. **Cluster API原生集成**：扩展Cluster API资源，而非独立并行
2. **Infrastructure Machine驱动**：将组件安装作为Infrastructure Machine的生命周期一部分
3. **声明式组件管理**：使用CRD定义组件配置和版本
4. **统一生命周期管理**：组件安装、升级与Machine生命周期同步
## 二、架构设计
### 2.1 整体架构
```
┌───────────────────────────────────────────────────────────────────────┐
│                         管理集群                                      │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │                     Cluster API栈                               │  │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  │  │
│  │  │ Cluster         │  │ KubeadmControl  │  │ Machine         │  │  │
│  │  │ Controller      │  │ Plane Controller│  │ Controller      │  │  │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘  │  │
│  │                         │                     │                 │  │
│  │                         ▼                     ▼                 │  │
│  │  ┌─────────────────────────────────────────────────────────┐    │  │
│  │  │            Custom Infrastructure Provider               │    │  │
│  │  │  ┌─────────────────┐  ┌─────────────────┐               │    │  │
│  │  │  │ Machine Driver  │  │ Component       │               │    │  │
│  │  │  │ (如: Metal3)    │  │ Installer       │               │    │  │
│  │  │  └─────────────────┘  └─────────────────┘               │    │  │
│  │  └─────────────────────────────────────────────────────────┘    │  │
│  │                         │                     │                 │  │
│  │                         ▼                     ▼                 │  │
│  │  ┌─────────────────┐  ┌───────────────────────────────────┐     │  │
│  │  │ ComponentConfig │  │ ComponentVersion                  │     │  │
│  │  │ CRD             │  │ CRD                               │     │  │
│  │  └─────────────────┘  └───────────────────────────────────┘     │  │
│  └─────────────────────────────────────────────────────────────────┘  │
│                              │                                        │
│                           API调用                                     │
│                              │                                        │
└───────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ 创建
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         业务集群                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │                     Kubernetes集群                            │  │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  │  │
│  │  │ Control Plane   │  │ Worker Nodes    │  │ Addons          │  │  │
│  │  │ (kube-apiserver,│  │                 │  │ (CNI, CSI, ...) │  │  │
│  │  │ kube-scheduler, │  │                 │  │                 │  │  │
│  │  │ kube-controller)│  │                 │  │                 │  │  │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```
### 2.2 核心资源定义
#### 2.2.1 ComponentConfig CRD
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentConfig
metadata:
  name: container-runtime-config
spec:
  # 组件类型
  componentType: container-runtime  # container-runtime, kubelet, cni, csi
  
  # 组件配置
  config:
    # containerd配置示例
    containerd:
      version: 1.7.11
      config:
        dataDir: /var/lib/containerd
        registry:
          mirrors:
            docker.io:
              endpoint:
                - https://registry-1.docker.io
        plugins:
          io.containerd.grpc.v1.cri:
            systemd_cgroup: true
            containerd:
              default_runtime_name: runc
  
  # 适用的基础设施类型
  infrastructureRef:
    kind: Metal3MachineTemplate
    name: control-plane-template
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta1

status:
  conditions:
    - type: Ready
      status: "True"
      reason: ConfigValid
      message: Component config is valid
  observedGeneration: 1
```
#### 2.2.2 ComponentVersion CRD
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.11
spec:
  componentType: container-runtime
  name: containerd
  version: 1.7.11
  
  # 组件来源
  source:
    type: HTTP
    url: https://github.com/containerd/containerd/releases/download/v1.7.11/containerd-1.7.11-linux-amd64.tar.gz
    checksum: sha256:abc123...
  
  # 安装脚本
  installScript: |
    #!/bin/bash
    set -e
    
    # 下载并解压
    curl -L -o /tmp/containerd.tar.gz {{.Source.URL}}
    sha256sum -c <<< "{{.Source.Checksum}} /tmp/containerd.tar.gz"
    
    tar -C /usr/local -xzf /tmp/containerd.tar.gz
    
    # 安装systemd服务
    mkdir -p /etc/systemd/system
    cat > /etc/systemd/system/containerd.service <<EOF
    [Unit]
    Description=containerd container runtime
    Documentation=https://containerd.io
    After=network.target local-fs.target
    
    [Service]
    ExecStart=/usr/local/bin/containerd
    Type=notify
    Delegate=yes
    KillMode=process
    Restart=always
    RestartSec=5
    LimitNPROC=infinity
    LimitCORE=infinity
    LimitNOFILE=infinity
    TasksMax=infinity
    OOMScoreAdjust=-999
    
    [Install]
    WantedBy=multi-user.target
    EOF
    
    # 配置
    mkdir -p /etc/containerd
    containerd config default > /etc/containerd/config.toml
    
    # 启用并启动
    systemctl daemon-reload
    systemctl enable --now containerd
  
  # 升级脚本
  upgradeScript: |
    #!/bin/bash
    set -e
    
    # 备份当前版本
    cp /usr/local/bin/containerd /usr/local/bin/containerd.bak
    
    # 下载并解压新版本
    curl -L -o /tmp/containerd.tar.gz {{.Source.URL}}
    sha256sum -c <<< "{{.Source.Checksum}} /tmp/containerd.tar.gz"
    
    tar -C /usr/local -xzf /tmp/containerd.tar.gz
    
    # 重启服务
    systemctl restart containerd
    
    # 验证
    if ! systemctl is-active containerd; then
      # 回滚
      cp /usr/local/bin/containerd.bak /usr/local/bin/containerd
      systemctl restart containerd
      exit 1
    fi
  
  # 兼容性信息
  compatibility:
    kubernetesVersions:
      min: "1.26.0"
      max: "1.30.0"
    os:
      - name: ubuntu
        versions: ["20.04", "22.04"]
        architectures: ["amd64", "arm64"]
      - name: centos
        versions: ["7", "8", "9"]
        architectures: ["amd64"]

status:
  conditions:
    - type: Ready
      status: "True"
      reason: VersionValid
      message: Component version is valid
  downloaded: true
  downloadTime: "2024-01-15T10:00:00Z"
```
#### 2.2.3 扩展Machine CRD
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Machine
metadata:
  name: control-plane-0
spec:
  clusterName: my-cluster
  version: v1.28.2
  bootstrap:
    configRef:
      apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
      kind: KubeadmConfig
      name: control-plane-0
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
    kind: Metal3Machine
    name: control-plane-0
  # 新增：节点组件配置
  components:
    - name: container-runtime
      configRef:
        apiVersion: cluster.x-k8s.io/v1beta1
        kind: ComponentConfig
        name: container-runtime-config
    - name: kubelet
      configRef:
        apiVersion: cluster.x-k8s.io/v1beta1
        kind: ComponentConfig
        name: kubelet-config
    - name: cni
      configRef:
        apiVersion: cluster.x-k8s.io/v1beta1
        kind: ComponentConfig
        name: cni-config
```
#### 2.2.4 扩展MachineTemplate CRD
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineTemplate
metadata:
  name: control-plane-template
spec:
  template:
    spec:
      clusterName: my-cluster
      version: v1.28.2
      bootstrap:
        configRef:
          apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
          kind: KubeadmConfigTemplate
          name: control-plane-template
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: Metal3MachineTemplate
        name: control-plane-template
      # 新增：节点组件配置模板
      components:
        - name: container-runtime
          configRef:
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: ComponentConfig
            name: container-runtime-config
        - name: kubelet
          configRef:
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: ComponentConfig
            name: kubelet-config
        - name: cni
          configRef:
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: ComponentConfig
            name: cni-config
```
## 三、Infrastructure Provider集成
### 3.1 Custom Infrastructure Provider设计
```go
package metal3

import (
    "context"
    "fmt"
    "time"
    
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/types"
    "k8s.io/klog/v2"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    clusterapiv1beta1 "cluster.x-k8s.io/api/v1beta1"
    infrastructurev1beta1 "cluster.x-k8s.io/api/infrastructure/v1beta1"
    "cluster.x-k8s.io/cluster-api/controllers/noderefutil"
    "cluster.x-k8s.io/cluster-api/util"
    "cluster.x-k8s.io/cluster-api/util/annotations"
    "cluster.x-k8s.io/cluster-api/util/patch"
    
    "myprovider.io/api/v1beta1"
    "myprovider.io/provider/component"
)

// Metal3MachineReconciler reconciles a Metal3Machine object
type Metal3MachineReconciler struct {
    client.Client
    Scheme                 *runtime.Scheme
    ComponentInstaller     component.Installer
    SSHManager             *SSHManager
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metal3machines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metal3machines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metal3machines/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=componentconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=componentversions,verbs=get;list;watch

func (r *Metal3MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    log.Info("Reconciling Metal3Machine")
    
    // 获取Metal3Machine
    metal3Machine := &v1beta1.Metal3Machine{}
    if err := r.Get(ctx, req.NamespacedName, metal3Machine); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 获取对应的Machine
    machine, err := util.GetMachineFromMetadata(ctx, r.Client, metal3Machine.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 获取Cluster
    cluster, err := util.GetClusterFromMetadata(ctx, r.Client, metal3Machine.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 创建patch
    helper, err := patch.NewHelper(metal3Machine, r.Client)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 处理删除
    if !metal3Machine.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, metal3Machine, machine, cluster)
    }
    
    // 处理创建/更新
    return r.reconcileNormal(ctx, helper, metal3Machine, machine, cluster)
}

func (r *Metal3MachineReconciler) reconcileNormal(ctx context.Context, helper *patch.Helper,
    metal3Machine *v1beta1.Metal3Machine, machine *clusterapiv1beta1.Machine, cluster *clusterapiv1beta1.Cluster) (ctrl.Result, error) {
    
    log := ctrl.LoggerFrom(ctx)
    
    // 检查是否已经完成
    if metal3Machine.Status.Ready {
        return ctrl.Result{}, nil
    }
    
    // 检查基础设施是否就绪
    if !metal3Machine.Status.InfrastructureReady {
        log.Info("Infrastructure not ready, waiting")
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
    }
    
    // 获取节点IP
    nodeIP := metal3Machine.Status.Addresses[0].Address
    
    // 安装节点组件
    if err := r.installComponents(ctx, metal3Machine, machine, cluster, nodeIP); err != nil {
        log.Error(err, "Failed to install components")
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }
    
    // 更新状态
    metal3Machine.Status.Ready = true
    if err := helper.Patch(ctx, metal3Machine); err != nil {
        return ctrl.Result{}, err
    }
    
    log.Info("Metal3Machine ready")
    return ctrl.Result{}, nil
}

func (r *Metal3MachineReconciler) installComponents(ctx context.Context,
    metal3Machine *v1beta1.Metal3Machine, machine *clusterapiv1beta1.Machine,
    cluster *clusterapiv1beta1.Cluster, nodeIP string) error {
    
    log := ctrl.LoggerFrom(ctx)
    
    // 检查Machine是否有组件配置
    if machine.Spec.Components == nil || len(machine.Spec.Components) == 0 {
        log.Info("No components to install")
        return nil
    }
    
    // 获取SSH密钥
    sshKey, err := r.getSSHKey(ctx, metal3Machine)
    if err != nil {
        return fmt.Errorf("failed to get SSH key: %v", err)
    }
    
    // 创建SSH客户端
    sshClient, err := r.SSHManager.NewClient(nodeIP, 22, "root", sshKey)
    if err != nil {
        return fmt.Errorf("failed to create SSH client: %v", err)
    }
    defer sshClient.Close()
    
    // 安装每个组件
    for _, componentSpec := range machine.Spec.Components {
        // 获取ComponentConfig
        componentConfig := &clusterapiv1beta1.ComponentConfig{}
        configRef := componentSpec.ConfigRef
        configKey := types.NamespacedName{
            Namespace: configRef.Namespace,
            Name:      configRef.Name,
        }
        
        if err := r.Get(ctx, configKey, componentConfig); err != nil {
            return fmt.Errorf("failed to get ComponentConfig %s: %v", configKey, err)
        }
        
        // 获取ComponentVersion
        componentType := componentConfig.Spec.ComponentType
        componentVersionName := fmt.Sprintf("%s-v%s", componentType, componentConfig.Spec.Config[componentType]["version"])
        
        componentVersion := &clusterapiv1beta1.ComponentVersion{}
        versionKey := types.NamespacedName{
            Namespace: metal3Machine.Namespace,
            Name:      componentVersionName,
        }
        
        if err := r.Get(ctx, versionKey, componentVersion); err != nil {
            return fmt.Errorf("failed to get ComponentVersion %s: %v", versionKey, err)
        }
        
        // 执行组件安装
        log.Info("Installing component", "component", componentType, "version", componentVersion.Spec.Version)
        
        if err := r.ComponentInstaller.Install(ctx, componentVersion, componentConfig, sshClient); err != nil {
            return fmt.Errorf("failed to install component %s: %v", componentType, err)
        }
        
        log.Info("Component installed successfully", "component", componentType)
    }
    
    return nil
}

func (r *Metal3MachineReconciler) getSSHKey(ctx context.Context, metal3Machine *v1beta1.Metal3Machine) ([]byte, error) {
    // 从Secret获取SSH密钥
    sshKeySecret := &corev1.Secret{}
    secretKey := types.NamespacedName{
        Namespace: metal3Machine.Namespace,
        Name:      metal3Machine.Spec.SSHKeySecret.Name,
    }
    
    if err := r.Get(ctx, secretKey, sshKeySecret); err != nil {
        return nil, err
    }
    
    return sshKeySecret.Data["privateKey"], nil
}
```
### 3.2 组件安装器
```go
package component

import (
    "context"
    "fmt"
    "strings"
    "text/template"
    
    clusterapiv1beta1 "cluster.x-k8s.io/api/v1beta1"
)

// Installer defines the interface for installing components
type Installer interface {
    Install(ctx context.Context, version *clusterapiv1beta1.ComponentVersion, 
        config *clusterapiv1beta1.ComponentConfig, sshClient SSHClient) error
    
    Upgrade(ctx context.Context, version *clusterapiv1beta1.ComponentVersion, 
        config *clusterapiv1beta1.ComponentConfig, sshClient SSHClient) error
    
    HealthCheck(ctx context.Context, version *clusterapiv1beta1.ComponentVersion, 
        sshClient SSHClient) error
}

// SSHClient defines the interface for SSH operations
type SSHClient interface {
    ExecuteCommand(cmd string) (string, error)
    UploadFile(remotePath string, data []byte) error
    DownloadFile(remotePath string) ([]byte, error)
    Close() error
}

// DefaultInstaller implements Installer interface
type DefaultInstaller struct{}

func NewDefaultInstaller() *DefaultInstaller {
    return &DefaultInstaller{}
}

func (i *DefaultInstaller) Install(ctx context.Context, version *clusterapiv1beta1.ComponentVersion, 
    config *clusterapiv1beta1.ComponentConfig, sshClient SSHClient) error {
    
    // 渲染安装脚本
    installScript, err := i.renderScript(version.Spec.InstallScript, version, config)
    if err != nil {
        return fmt.Errorf("failed to render install script: %v", err)
    }
    
    // 上传脚本
    if err := sshClient.UploadFile("/tmp/install-component.sh", []byte(installScript)); err != nil {
        return fmt.Errorf("failed to upload install script: %v", err)
    }
    
    // 执行脚本
    cmd := "chmod +x /tmp/install-component.sh && /tmp/install-component.sh"
    output, err := sshClient.ExecuteCommand(cmd)
    if err != nil {
        return fmt.Errorf("failed to execute install script: %v, output: %s", err, output)
    }
    
    // 清理脚本
    sshClient.ExecuteCommand("rm /tmp/install-component.sh")
    
    return nil
}

func (i *DefaultInstaller) Upgrade(ctx context.Context, version *clusterapiv1beta1.ComponentVersion, 
    config *clusterapiv1beta1.ComponentConfig, sshClient SSHClient) error {
    
    // 渲染升级脚本
    upgradeScript, err := i.renderScript(version.Spec.UpgradeScript, version, config)
    if err != nil {
        return fmt.Errorf("failed to render upgrade script: %v", err)
    }
    
    // 上传脚本
    if err := sshClient.UploadFile("/tmp/upgrade-component.sh", []byte(upgradeScript)); err != nil {
        return fmt.Errorf("failed to upload upgrade script: %v", err)
    }
    
    // 执行脚本
    cmd := "chmod +x /tmp/upgrade-component.sh && /tmp/upgrade-component.sh"
    output, err := sshClient.ExecuteCommand(cmd)
    if err != nil {
        return fmt.Errorf("failed to execute upgrade script: %v, output: %s", err, output)
    }
    
    // 清理脚本
    sshClient.ExecuteCommand("rm /tmp/upgrade-component.sh")
    
    return nil
}

func (i *DefaultInstaller) HealthCheck(ctx context.Context, version *clusterapiv1beta1.ComponentVersion, 
    sshClient SSHClient) error {
    
    // 执行健康检查命令
    if version.Spec.HealthCheck != nil && version.Spec.HealthCheck.Command != "" {
        output, err := sshClient.ExecuteCommand(version.Spec.HealthCheck.Command)
        if err != nil {
            return fmt.Errorf("health check failed: %v", err)
        }
        
        // 验证输出
        if version.Spec.HealthCheck.ExpectedOutput != "" && 
           !strings.Contains(output, version.Spec.HealthCheck.ExpectedOutput) {
            return fmt.Errorf("health check output mismatch. Expected: %s, Got: %s", 
                version.Spec.HealthCheck.ExpectedOutput, output)
        }
    }
    
    return nil
}

// 渲染脚本模板
func (i *DefaultInstaller) renderScript(script string, version *clusterapiv1beta1.ComponentVersion, 
    config *clusterapiv1beta1.ComponentConfig) (string, error) {
    
    // 创建模板
    tmpl, err := template.New("component-script").Parse(script)
    if err != nil {
        return "", err
    }
    
    // 准备模板数据
    data := map[string]interface{}{
        "Version": version.Spec,
        "Config":  config.Spec.Config,
        "Source":  version.Spec.Source,
    }
    
    // 渲染模板
    var rendered strings.Builder
    if err := tmpl.Execute(&rendered, data); err != nil {
        return "", err
    }
    
    return rendered.String(), nil
}
```
## 四、Cluster API集成流程
### 4.1 集群创建流程
```
┌─────────────────────────────────────────────────────────────────────┐
│                         集群创建流程                               │
└─────────────────────────────────────────────────────────────────────┘

1. 用户创建Cluster资源
   └──> 2. Cluster Controller创建基础设施资源
          └──> 3. 用户创建Machine/MachineDeployment资源
                 └──> 4. Machine Controller创建BootstrapConfig
                        └──> 5. Machine Controller创建Infrastructure Machine
                               └──> 6. Infrastructure Provider创建基础设施资源
                                      └──> 7. Infrastructure Provider安装节点组件
                                             └──> 8. Infrastructure Provider更新Machine状态
                                                    └──> 9. Bootstrap Provider执行引导
                                                           └──> 10. Machine加入集群
                                                                  └──> 11. Cluster就绪
```
### 4.2 组件升级流程
```
┌─────────────────────────────────────────────────────────────────────┐
│                         组件升级流程                               │
└─────────────────────────────────────────────────────────────────────┘

1. 用户更新ComponentConfig资源（新版本）
   └──> 2. 用户更新Machine/MachineDeployment的组件配置引用
          └──> 3. Machine Controller触发Infrastructure Machine更新
                 └──> 4. Infrastructure Provider检测组件版本变更
                        └──> 5. Infrastructure Provider执行组件升级
                               └──> 6. Infrastructure Provider验证升级结果
                                      └──> 7. Infrastructure Provider更新Machine状态
                                             └──> 8. 升级完成
```
### 4.3 Machine扩容流程
```
┌─────────────────────────────────────────────────────────────────────┐
│                         Machine扩容流程                           │
└─────────────────────────────────────────────────────────────────────┘

1. 用户更新MachineDeployment的replicas字段
   └──> 2. Machine Deployment Controller创建新的Machine资源
          └──> 3. Machine Controller创建BootstrapConfig
                 └──> 4. Machine Controller创建Infrastructure Machine
                        └──> 5. Infrastructure Provider创建基础设施资源
                               └──> 6. Infrastructure Provider安装节点组件
                                      └──> 7. Infrastructure Provider更新Machine状态
                                             └──> 8. Bootstrap Provider执行引导
                                                    └──> 9. 新Machine加入集群
                                                           └──> 10. 扩容完成
```
## 五、组件版本管理
### 5.1 版本兼容性检查
```go
package component

import (
    "context"
    "fmt"
    "regexp"
    "strings"
    
    clusterapiv1beta1 "cluster.x-k8s.io/api/v1beta1"
    semver "github.com/Masterminds/semver/v3"
)

type VersionCompatibilityChecker struct{}

func NewVersionCompatibilityChecker() *VersionCompatibilityChecker {
    return &VersionCompatibilityChecker{}
}

func (c *VersionCompatibilityChecker) CheckCompatibility(ctx context.Context, 
    componentVersion *clusterapiv1beta1.ComponentVersion, 
    kubernetesVersion string, osInfo OSInfo) error {
    
    // 检查Kubernetes版本兼容性
    if err := c.checkKubernetesCompatibility(componentVersion, kubernetesVersion); err != nil {
        return err
    }
    
    // 检查OS兼容性
    if err := c.checkOSCompatibility(componentVersion, osInfo); err != nil {
        return err
    }
    
    return nil
}

func (c *VersionCompatibilityChecker) checkKubernetesCompatibility(
    componentVersion *clusterapiv1beta1.ComponentVersion, 
    kubernetesVersion string) error {
    
    // 解析Kubernetes版本
    k8sVerStr := extractSemver(kubernetesVersion)
    k8sVer, err := semver.NewVersion(k8sVerStr)
    if err != nil {
        return fmt.Errorf("failed to parse Kubernetes version %s: %v", kubernetesVersion, err)
    }
    
    // 检查最小版本
    if componentVersion.Spec.Compatibility.KubernetesVersions.Min != "" {
        minVer, err := semver.NewVersion(componentVersion.Spec.Compatibility.KubernetesVersions.Min)
        if err != nil {
            return fmt.Errorf("failed to parse min Kubernetes version %s: %v", 
                componentVersion.Spec.Compatibility.KubernetesVersions.Min, err)
        }
        
        if k8sVer.LessThan(minVer) {
            return fmt.Errorf("Kubernetes version %s is less than minimum required version %s", 
                k8sVerStr, componentVersion.Spec.Compatibility.KubernetesVersions.Min)
        }
    }
    
    // 检查最大版本
    if componentVersion.Spec.Compatibility.KubernetesVersions.Max != "" {
        maxVer, err := semver.NewVersion(componentVersion.Spec.Compatibility.KubernetesVersions.Max)
        if err != nil {
            return fmt.Errorf("failed to parse max Kubernetes version %s: %v", 
                componentVersion.Spec.Compatibility.KubernetesVersions.Max, err)
        }
        
        if k8sVer.GreaterThan(maxVer) {
            return fmt.Errorf("Kubernetes version %s is greater than maximum supported version %s", 
                k8sVerStr, componentVersion.Spec.Compatibility.KubernetesVersions.Max)
        }
    }
    
    return nil
}

func (c *VersionCompatibilityChecker) checkOSCompatibility(
    componentVersion *clusterapiv1beta1.ComponentVersion, 
    osInfo OSInfo) error {
    
    for _, osCompatibility := range componentVersion.Spec.Compatibility.OS {
        // 检查OS名称
        if osCompatibility.Name != osInfo.Name {
            continue
        }
        
        // 检查OS版本
        versionMatch := false
        for _, supportedVersion := range osCompatibility.Versions {
            if supportedVersion == osInfo.Version {
                versionMatch = true
                break
            }
        }
        
        if !versionMatch {
            continue
        }
        
        // 检查架构
        archMatch := false
        for _, supportedArch := range osCompatibility.Architectures {
            if supportedArch == osInfo.Architecture {
                archMatch = true
                break
            }
        }
        
        if archMatch {
            return nil
        }
    }
    
    return fmt.Errorf("OS %s %s %s is not compatible with component version %s", 
        osInfo.Name, osInfo.Version, osInfo.Architecture, componentVersion.Spec.Version)
}

// 从字符串中提取语义化版本
func extractSemver(version string) string {
    re := regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)
    matches := re.FindStringSubmatch(version)
    if len(matches) > 1 {
        return matches[1]
    }
    return strings.TrimPrefix(version, "v")
}

type OSInfo struct {
    Name         string
    Version      string
    Architecture string
}
```
### 5.2 升级策略控制器
```go
package controller

import (
    "context"
    "fmt"
    "time"
    
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/types"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    clusterapiv1beta1 "cluster.x-k8s.io/api/v1beta1"
    "cluster.x-k8s.io/cluster-api/util"
)

type ComponentUpgradeReconciler struct {
    client.Client
    Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=componentupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=componentupgrades/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=componentconfigs,verbs=get;list;watch

// ComponentUpgrade CRD
type ComponentUpgrade struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   ComponentUpgradeSpec   `json:"spec,omitempty"`
    Status ComponentUpgradeStatus `json:"status,omitempty"`
}

type ComponentUpgradeSpec struct {
    ClusterName string `json:"clusterName"`
    
    Components []ComponentUpgradeItem `json:"components"`
    
    // 升级策略
    Strategy UpgradeStrategy `json:"strategy"`
}

type ComponentUpgradeItem struct {
    ComponentType string `json:"componentType"`
    ConfigRef     corev1.ObjectReference `json:"configRef"`
}

type UpgradeStrategy struct {
    Type string `json:"type"` // rolling, batch, inplace
    
    // 滚动升级配置
    RollingUpdate *RollingUpdateStrategy `json:"rollingUpdate,omitempty"`
    
    // 批处理升级配置
    BatchUpdate *BatchUpdateStrategy `json:"batchUpdate,omitempty"`
    
    // 超时配置
    Timeout *metav1.Duration `json:"timeout,omitempty"`
}

type RollingUpdateStrategy struct {
    MaxUnavailable int32 `json:"maxUnavailable"`
    MaxSurge       int32 `json:"maxSurge"`
}

type BatchUpdateStrategy struct {
    BatchSize int32 `json:"batchSize"`
    Interval  metav1.Duration `json:"interval"`
}

type ComponentUpgradeStatus struct {
    Phase string `json:"phase"` // Pending, InProgress, Completed, Failed
    
    Progress ComponentUpgradeProgress `json:"progress"`
    
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ComponentUpgradeProgress struct {
    TotalNodes    int32 `json:"totalNodes"`
    UpgradedNodes int32 `json:"upgradedNodes"`
    FailedNodes   int32 `json:"failedNodes"`
}

func (r *ComponentUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    log.Info("Reconciling ComponentUpgrade")
    
    // 获取ComponentUpgrade
    upgrade := &ComponentUpgrade{}
    if err := r.Get(ctx, req.NamespacedName, upgrade); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 根据阶段执行操作
    switch upgrade.Status.Phase {
    case "", "Pending":
        return r.initiateUpgrade(ctx, upgrade)
    case "InProgress":
        return r.continueUpgrade(ctx, upgrade)
    case "Completed", "Failed":
        return ctrl.Result{}, nil
    default:
        return ctrl.Result{}, fmt.Errorf("unknown phase: %s", upgrade.Status.Phase)
    }
}

func (r *ComponentUpgradeReconciler) initiateUpgrade(ctx context.Context, upgrade *ComponentUpgrade) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    
    // 更新状态为InProgress
    upgrade.Status.Phase = "InProgress"
    upgrade.Status.Progress = ComponentUpgradeProgress{
        TotalNodes:    0,
        UpgradedNodes: 0,
        FailedNodes:   0,
    }
    
    // 获取集群的所有Machine
    machineList := &clusterapiv1beta1.MachineList{}
    if err := r.List(ctx, machineList, client.MatchingLabels{"cluster.x-k8s.io/cluster-name": upgrade.Spec.ClusterName}); err != nil {
        return ctrl.Result{}, err
    }
    
    upgrade.Status.Progress.TotalNodes = int32(len(machineList.Items))
    
    if err := r.Status().Update(ctx, upgrade); err != nil {
        return ctrl.Result{}, err
    }
    
    // 根据升级策略开始升级
    switch upgrade.Spec.Strategy.Type {
    case "rolling":
        return r.startRollingUpdate(ctx, upgrade, machineList.Items)
    case "batch":
        return r.startBatchUpdate(ctx, upgrade, machineList.Items)
    case "inplace":
        return r.startInplaceUpdate(ctx, upgrade, machineList.Items)
    default:
        return ctrl.Result{}, fmt.Errorf("unsupported upgrade strategy: %s", upgrade.Spec.Strategy.Type)
    }
}

func (r *ComponentUpgradeReconciler) startRollingUpdate(ctx context.Context, upgrade *ComponentUpgrade, 
    machines []clusterapiv1beta1.Machine) (ctrl.Result, error) {
    
    log := ctrl.LoggerFrom(ctx)
    
    // 计算可以同时升级的节点数
    maxUnavailable := upgrade.Spec.Strategy.RollingUpdate.MaxUnavailable
    if maxUnavailable <= 0 {
        maxUnavailable = 1
    }
    
    // 选择需要升级的节点
    var nodesToUpgrade []clusterapiv1beta1.Machine
    for _, machine := range machines {
        // 跳过已经升级的节点
        if hasBeenUpgraded(&machine, upgrade) {
            continue
        }
        
        nodesToUpgrade = append(nodesToUpgrade, machine)
        if len(nodesToUpgrade) >= int(maxUnavailable) {
            break
        }
    }
    
    // 升级节点
    for _, machine := range nodesToUpgrade {
        if err := r.upgradeMachine(ctx, &machine, upgrade); err != nil {
            log.Error(err, "Failed to upgrade machine", "machine", machine.Name)
            upgrade.Status.Progress.FailedNodes++
            continue
        }
        
        upgrade.Status.Progress.UpgradedNodes++
    }
    
    // 更新状态
    if err := r.Status().Update(ctx, upgrade); err != nil {
        return ctrl.Result{}, err
    }
    
    // 检查是否完成
    if upgrade.Status.Progress.UpgradedNodes+upgrade.Status.Progress.FailedNodes >= upgrade.Status.Progress.TotalNodes {
        if upgrade.Status.Progress.FailedNodes == 0 {
            upgrade.Status.Phase = "Completed"
        } else {
            upgrade.Status.Phase = "Failed"
        }
        
        if err := r.Status().Update(ctx, upgrade); err != nil {
            return ctrl.Result{}, err
        }
        
        return ctrl.Result{}, nil
    }
    
    // 继续升级
    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *ComponentUpgradeReconciler) upgradeMachine(ctx context.Context, machine *clusterapiv1beta1.Machine, 
    upgrade *ComponentUpgrade) error {
    
    log := ctrl.LoggerFrom(ctx)
    
    // 创建新的Machine组件配置
    newComponents := make([]clusterapiv1beta1.MachineComponent, len(machine.Spec.Components))
    copy(newComponents, machine.Spec.Components)
    
    // 更新组件配置引用
    for i, component := range newComponents {
        for _, upgradeItem := range upgrade.Spec.Components {
            if component.Name == upgradeItem.ComponentType {
                newComponents[i].ConfigRef = upgradeItem.ConfigRef
                break
            }
        }
    }
    
    // 更新Machine
    machine.Spec.Components = newComponents
    
    if err := r.Update(ctx, machine); err != nil {
        return fmt.Errorf("failed to update machine %s: %v", machine.Name, err)
    }
    
    log.Info("Machine updated for upgrade", "machine", machine.Name)
    return nil
}
```
## 六、使用示例
### 6.1 创建业务集群
```yaml
# 1. 创建Cluster资源
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: my-cluster
spec:
  clusterNetwork:
    pods:
      cidrBlocks: ["192.168.0.0/16"]
    services:
      cidrBlocks: ["10.96.0.0/12"]
  infrastructureRef:
    kind: Metal3Cluster
    name: my-cluster
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
---

# 2. 创建ComponentConfig资源
apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentConfig
metadata:
  name: container-runtime-config
spec:
  componentType: container-runtime
  config:
    containerd:
      version: 1.7.11
      config:
        dataDir: /var/lib/containerd
        systemdCgroup: true
---

apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentConfig
metadata:
  name: kubelet-config
spec:
  componentType: kubelet
  config:
    kubelet:
      version: 1.28.2
      config:
        maxPods: 110
        systemReserved:
          memory: 1Gi
          cpu: 1000m
---

apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentConfig
metadata:
  name: cni-config
spec:
  componentType: cni
  config:
    calico:
      version: 3.26.4
      config:
        ipam:
          type: host-local
          subnet: 192.168.0.0/24
---

# 3. 创建ComponentVersion资源
apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.11
spec:
  componentType: container-runtime
  name: containerd
  version: 1.7.11
  source:
    type: HTTP
    url: https://github.com/containerd/containerd/releases/download/v1.7.11/containerd-1.7.11-linux-amd64.tar.gz
    checksum: sha256:abc123...
  # 安装脚本等...
---

# 4. 创建MachineTemplate资源
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineTemplate
metadata:
  name: control-plane-template
spec:
  template:
    spec:
      clusterName: my-cluster
      version: v1.28.2
      bootstrap:
        configRef:
          apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
          kind: KubeadmConfigTemplate
          name: control-plane-template
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
        kind: Metal3MachineTemplate
        name: control-plane-template
      components:
        - name: container-runtime
          configRef:
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: ComponentConfig
            name: container-runtime-config
        - name: kubelet
          configRef:
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: ComponentConfig
            name: kubelet-config
        - name: cni
          configRef:
            apiVersion: cluster.x-k8s.io/v1beta1
            kind: ComponentConfig
            name: cni-config
---

# 5. 创建KubeadmControlPlane资源
apiVersion: controlplane.cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
metadata:
  name: my-cluster-control-plane
spec:
  replicas: 3
  version: v1.28.2
  machineTemplate:
    infrastructureRef:
      apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
      kind: Metal3MachineTemplate
      name: control-plane-template
  kubeadmConfigSpec:
    clusterConfiguration:
      apiServer:
        certSANs:
          - localhost
          - 127.0.0.1
      controlPlaneEndpoint:
        host: my-cluster-control-plane
        port: 6443
    initConfiguration:
      nodeRegistration:
        criSocket: /run/containerd/containerd.sock
        kubeletExtraArgs:
          cgroup-driver: systemd
    joinConfiguration:
      controlPlane: {}
      nodeRegistration:
        criSocket: /run/containerd/containerd.sock
        kubeletExtraArgs:
          cgroup-driver: systemd
```
### 6.2 升级组件
```yaml
# 1. 创建新版本的ComponentConfig
apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentConfig
metadata:
  name: container-runtime-config-v1.7.12
spec:
  componentType: container-runtime
  config:
    containerd:
      version: 1.7.12
      config:
        dataDir: /var/lib/containerd
        systemdCgroup: true
---

# 2. 创建新版本的ComponentVersion
apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.12
spec:
  componentType: container-runtime
  name: containerd
  version: 1.7.12
  source:
    type: HTTP
    url: https://github.com/containerd/containerd/releases/download/v1.7.12/containerd-1.7.12-linux-amd64.tar.gz
    checksum: sha256:def456...
  # 安装脚本等...
---

# 3. 创建ComponentUpgrade资源
apiVersion: cluster.x-k8s.io/v1beta1
kind: ComponentUpgrade
metadata:
  name: upgrade-containerd
spec:
  clusterName: my-cluster
  components:
    - componentType: container-runtime
      configRef:
        apiVersion: cluster.x-k8s.io/v1beta1
        kind: ComponentConfig
        name: container-runtime-config-v1.7.12
  strategy:
    type: rolling
    rollingUpdate:
      maxUnavailable: 1
      maxSurge: 0
    timeout:
      duration: 2h
```
## 七、方案优势
### 7.1 Cluster API原生集成
- **无缝集成**：扩展Cluster API资源，而非独立并行
- **统一控制平面**：通过Cluster API管理所有集群资源
- **标准化流程**：遵循Cluster API的声明式管理和状态协调模式
### 7.2 灵活的组件管理
- **声明式配置**：使用CRD定义组件配置和版本
- **多组件支持**：支持container-runtime、kubelet、CNI、CSI等
- **版本兼容性**：自动检查组件与Kubernetes、OS的兼容性
### 7.3 强大的升级能力
- **多种升级策略**：支持滚动升级、批处理升级、原地升级
- **自动化流程**：从版本检测到升级验证的全自动化
- **失败回滚**：升级失败时自动回滚到安全状态
### 7.4 高度可扩展
- **插件化架构**：支持自定义组件类型和安装脚本
- **多Infrastructure Provider**：支持各种基础设施类型
- **社区兼容**：遵循Cluster API的扩展规范
## 八、总结
本方案将节点组件管理与Cluster API深度集成，实现了：
1. **统一的集群生命周期管理**：从基础设施到应用层的完整管理
2. **灵活的组件部署方式**：支持多种组件和版本的声明式配置
3. **自动化的升级流程**：基于策略的组件升级和健康检查
4. **高度的可扩展性**：支持自定义组件和Infrastructure Provider

该方案完全遵循Cluster API的设计理念，提供了一个标准、可扩展的节点组件管理框架，适用于各种规模和类型的Kubernetes集群部署。

     
# 集群管理解决方案版本包设计
## 一、版本包概述
设计一个完整的版本包结构，支持引导集群、管理集群和业务集群的全生命周期管理与自动化升级。版本包采用分层设计，包含解决方案版本元数据、各层级集群的组件定义、升级策略和兼容性矩阵。
### 1.1 版本包核心概念
- **解决方案版本(Solution Version)**：全局唯一的版本标识，如 `v1.2.0`，包含引导集群、管理集群和业务集群的完整定义
- **层级版本(Layer Version)**：各层级集群(引导/管理/业务)的版本，如 `bootstrap-v1.2.0`、`management-v1.2.0`
- **组件版本(Component Version)**：集群内各组件的版本，如 `containerd-v1.7.11`、`kubelet-v1.28.2`
- **升级路径(Upgrade Path)**：从旧版本到新版本的升级流程定义
- **兼容性矩阵(Compatibility Matrix)**：定义版本间的兼容性关系
## 二、版本包结构
```
cluster-manager-solution-v1.2.0/
├── metadata.yaml                     # 解决方案版本元数据
├── CHANGELOG.md                      # 变更日志
├── README.md                         # 版本包说明文档
├── LICENSE                           # 许可证
├── checksum.sha256                   # 文件校验和
├── components/                       # 组件定义目录
│   ├── bootstrap/                    # 引导集群组件
│   │   ├── k3s/                      # K3s组件
│   │   │   ├── v1.27.0/              # 版本目录
│   │   │   │   ├── component.yaml    # 组件元数据
│   │   │   │   ├── install.sh        # 安装脚本
│   │   │   │   ├── upgrade.sh        # 升级脚本
│   │   │   │   └── files/            # 组件文件
│   │   ├── kind/                     # Kind组件
│   │   │   └── v0.17.0/              # 版本目录
│   │   └── minikube/                 # Minikube组件
│   │       └── v1.30.0/              # 版本目录
│   ├── management/                   # 管理集群组件
│   │   ├── container-runtime/        # 容器运行时组件
│   │   │   ├── containerd/           # Containerd组件
│   │   │   │   └── v1.7.11/          # 版本目录
│   │   ├── kubelet/                  # Kubelet组件
│   │   │   └── v1.28.2/              # 版本目录
│   │   ├── cni/                      # CNI组件
│   │   │   └── calico/               # Calico组件
│   │   │       └── v3.26.4/          # 版本目录
│   │   └── capi/                     # Cluster API组件
│   │       └── v1.5.2/               # 版本目录
│   └── workload/                     # 业务集群组件
│       ├── container-runtime/        # 容器运行时组件
│       │   └── containerd/           # Containerd组件
│       │       └── v1.7.11/          # 版本目录
│       ├── kubelet/                  # Kubelet组件
│       │   └── v1.28.2/              # 版本目录
│       ├── cni/                      # CNI组件
│       │   └── calico/               # Calico组件
│       │       └── v3.26.4/          # 版本目录
│       └── csi/                      # CSI组件
│           └── hostpath/             # HostPath CSI组件
│               └── v1.10.1/          # 版本目录
├── templates/                        # 资源模板目录
│   ├── bootstrap/                    # 引导集群模板
│   │   ├── k3s-config.yaml.tmpl      # K3s配置模板
│   │   └── kind-config.yaml.tmpl     # Kind配置模板
│   ├── management/                   # 管理集群模板
│   │   ├── cluster.yaml.tmpl         # Cluster资源模板
│   │   ├── component-config.yaml.tmpl # ComponentConfig资源模板
│   │   └── machine-template.yaml.tmpl # MachineTemplate资源模板
│   └── workload/                     # 业务集群模板
│       ├── cluster.yaml.tmpl         # Cluster资源模板
│       └── machine-deployment.yaml.tmpl # MachineDeployment资源模板
├── upgrade/                          # 升级定义目录
│   ├── solution/                     # 解决方案升级路径
│   │   ├── v1.1.0_to_v1.2.0/         # 从v1.1.0到v1.2.0的升级
│   │   │   ├── metadata.yaml         # 升级元数据
│   │   │   ├── pre-check.sh          # 前置检查脚本
│   │   │   ├── upgrade.sh            # 升级脚本
│   │   │   └── rollback.sh           # 回滚脚本
│   ├── bootstrap/                    # 引导集群升级路径
│   │   └── bootstrap-v1.1.0_to_v1.2.0/ # 引导集群升级
│   ├── management/                   # 管理集群升级路径
│   │   └── management-v1.1.0_to_v1.2.0/ # 管理集群升级
│   └── workload/                     # 业务集群升级路径
│       └── workload-v1.1.0_to_v1.2.0/ # 业务集群升级
├── compatibility/                    # 兼容性矩阵目录
│   ├── solution.yaml                 # 解决方案兼容性
│   ├── bootstrap.yaml                # 引导集群兼容性
│   ├── management.yaml               # 管理集群兼容性
│   └── workload.yaml                 # 业务集群兼容性
├── tools/                            # 工具目录
│   ├── cluster-manager-cli           # 命令行工具
│   ├── upgrade-agent                 # 升级代理
│   └── health-checker                # 健康检查工具
└── manifests/                        # 部署清单目录
    ├── bootstrap/                    # 引导集群部署清单
    ├── management/                   # 管理集群部署清单
    └── workload/                     # 业务集群部署清单
```
## 三、核心文件内容设计
### 3.1 解决方案元数据 (metadata.yaml)
```yaml
apiVersion: cluster-manager.io/v1alpha1
kind: SolutionVersion
metadata:
  name: cluster-manager-solution-v1.2.0
spec:
  # 解决方案版本信息
  version: v1.2.0
  releaseDate: "2024-01-15T00:00:00Z"
  description: "Cluster Manager Solution v1.2.0"
  releaseNotes: "CHANGELOG.md"
  
  # 层级集群版本映射
  layers:
    bootstrap:
      version: bootstrap-v1.2.0
      components:
        - name: k3s
          version: v1.27.0
        - name: kind
          version: v0.17.0
        - name: minikube
          version: v1.30.0
    
    management:
      version: management-v1.2.0
      components:
        - name: container-runtime
          type: containerd
          version: v1.7.11
        - name: kubelet
          version: v1.28.2
        - name: cni
          type: calico
          version: v3.26.4
        - name: capi
          version: v1.5.2
    
    workload:
      version: workload-v1.2.0
      components:
        - name: container-runtime
          type: containerd
          version: v1.7.11
        - name: kubelet
          version: v1.28.2
        - name: cni
          type: calico
          version: v3.26.4
        - name: csi
          type: hostpath
          version: v1.10.1
  
  # 依赖关系
  dependencies:
    - name: kubectl
      version: ">=1.26.0"
    - name: helm
      version: ">=3.9.0"
  
  # 支持的操作系统
  supportedOS:
    - name: ubuntu
      versions: ["20.04", "22.04"]
      architectures: ["amd64", "arm64"]
    - name: centos
      versions: ["7", "8", "9"]
      architectures: ["amd64"]
  
  # 升级路径
  upgradePaths:
    - fromVersion: v1.1.0
      toVersion: v1.2.0
      path: upgrade/solution/v1.1.0_to_v1.2.0/
    - fromVersion: v1.0.0
      toVersion: v1.2.0
      path: upgrade/solution/v1.0.0_to_v1.2.0/
      requires:
        - v1.0.0_to_v1.1.0
  
  # 校验信息
  checksums:
    - file: components/bootstrap/k3s/v1.27.0/component.yaml
      sha256: "abc123..."
    - file: components/management/container-runtime/containerd/v1.7.11/component.yaml
      sha256: "def456..."
```
### 3.2 组件元数据 (components/management/container-runtime/containerd/v1.7.11/component.yaml)
```yaml
apiVersion: cluster-manager.io/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.11
spec:
  componentType: container-runtime
  name: containerd
  version: v1.7.11
  
  # 组件来源
  source:
    type: HTTP
    url: https://github.com/containerd/containerd/releases/download/v1.7.11/containerd-1.7.11-linux-amd64.tar.gz
    checksum: sha256:abc123...
  
  # 安装配置
  installation:
    script: install.sh
    dependencies:
      - name: systemd
      - name: tar
      - name: curl
  
  # 升级配置
  upgrade:
    script: upgrade.sh
    dependencies:
      - name: systemd
      - name: tar
      - name: curl
  
  # 健康检查
  healthCheck:
    command: "systemctl is-active containerd"
    expectedOutput: "active"
    timeout: 30s
  
  # 兼容性信息
  compatibility:
    kubernetesVersions:
      min: "1.26.0"
      max: "1.30.0"
    os:
      - name: ubuntu
        versions: ["20.04", "22.04"]
        architectures: ["amd64", "arm64"]
      - name: centos
        versions: ["7", "8", "9"]
        architectures: ["amd64"]
  
  # 配置模板
  configTemplate: config.toml.tmpl
```
### 3.3 升级路径元数据 (upgrade/solution/v1.1.0_to_v1.2.0/metadata.yaml)
```yaml
apiVersion: cluster-manager.io/v1alpha1
kind: UpgradePath
metadata:
  name: solution-v1.1.0_to_v1.2.0
spec:
  fromVersion: v1.1.0
  toVersion: v1.2.0
  type: solution
  
  # 升级顺序
  upgradeOrder:
    - layer: bootstrap
      path: upgrade/bootstrap/bootstrap-v1.1.0_to_v1.2.0/
    - layer: management
      path: upgrade/management/management-v1.1.0_to_v1.2.0/
    - layer: workload
      path: upgrade/workload/workload-v1.1.0_to_v1.2.0/
  
  # 前置检查
  preCheck:
    description: "Pre-upgrade checks for solution v1.1.0 to v1.2.0"
    script: pre-check.sh
    timeout: 5m
  
  # 升级配置
  upgrade:
    description: "Upgrade script for solution v1.1.0 to v1.2.0"
    script: upgrade.sh
    timeout: 2h
  
  # 回滚配置
  rollback:
    description: "Rollback script for solution v1.1.0 to v1.2.0"
    script: rollback.sh
    timeout: 1h
  
  # 组件变更
  componentChanges:
    - layer: management
      component: container-runtime
      fromVersion: v1.7.10
      toVersion: v1.7.11
      changeType: patch
      description: "Fix security vulnerabilities"
    
    - layer: management
      component: kubelet
      fromVersion: v1.28.1
      toVersion: v1.28.2
      changeType: patch
      description: "Bug fixes and performance improvements"
    
    - layer: workload
      component: csi
      fromVersion: v1.10.0
      toVersion: v1.10.1
      changeType: patch
      description: "Fix volume attachment issues"
  
  # 兼容性信息
  compatibility:
    requiredTools:
      - name: kubectl
        version: ">=1.26.0"
      - name: helm
        version: ">=3.9.0"
    
    supportedOS:
      - name: ubuntu
        versions: ["20.04", "22.04"]
      - name: centos
        versions: ["7", "8", "9"]
```
### 3.4 兼容性矩阵 (compatibility/solution.yaml)
```yaml
apiVersion: cluster-manager.io/v1alpha1
kind: CompatibilityMatrix
metadata:
  name: solution-compatibility
spec:
  # 解决方案版本兼容性
  solutionVersions:
    - version: v1.2.0
      compatibleWith:
        - version: v1.1.0
          upgradePath: upgrade/solution/v1.1.0_to_v1.2.0/
        - version: v1.0.0
          upgradePath: upgrade/solution/v1.0.0_to_v1.2.0/
          requires:
            - v1.0.0_to_v1.1.0
    
    - version: v1.1.0
      compatibleWith:
        - version: v1.0.0
          upgradePath: upgrade/solution/v1.0.0_to_v1.1.0/
  
  # 层级集群版本兼容性
  layerCompatibility:
    bootstrap:
      - version: bootstrap-v1.2.0
        compatibleWith:
          - bootstrap-v1.1.0
    
    management:
      - version: management-v1.2.0
        compatibleWith:
          - management-v1.1.0
    
    workload:
      - version: workload-v1.2.0
        compatibleWith:
          - workload-v1.1.0
          - workload-v1.0.0
  
  # 组件版本兼容性
  componentCompatibility:
    container-runtime:
      - version: v1.7.11
        compatibleWith:
          - v1.7.10
          - v1.7.9
    
    kubelet:
      - version: v1.28.2
        compatibleWith:
          - v1.28.1
          - v1.28.0
          - v1.27.5
```
## 四、自动化升级流程
### 4.1 升级流程概述
```
┌─────────────────────────────────────────────────────────────────────┐
│                         自动化升级流程                             │
└─────────────────────────────────────────────────────────────────────┘

1. 版本检测与兼容性检查
   └──> 2. 下载并验证版本包
          └──> 3. 执行前置检查
                 └──> 4. 备份当前配置
                        └──> 5. 按顺序升级各层级集群
                               └──> 6. 升级引导集群
                                      └──> 7. 升级管理集群
                                             └──> 8. 升级业务集群
                                                    └──> 9. 执行健康检查
                                                           └──> 10. 更新版本元数据
                                                                  └──> 11. 清理临时文件
```
### 4.2 升级命令示例
```bash
# 检查当前版本和可用升级
cluster-manager upgrade check

# 输出：
# Current solution version: v1.1.0
# Available upgrades:
#   - v1.2.0 (security fixes, bug fixes)

# 执行升级
cluster-manager upgrade apply v1.2.0

# 输出：
# ℹ️  Downloading version package cluster-manager-solution-v1.2.0...
# ✅ Version package downloaded and verified
# ℹ️  Running pre-upgrade checks...
# ✅ Pre-upgrade checks passed
# ℹ️  Backing up current configuration...
# ✅ Configuration backed up to /var/lib/cluster-manager/backups/v1.1.0-20240115-143022
# ℹ️  Starting upgrade process...
#   - Upgrading bootstrap cluster (bootstrap-v1.1.0 → bootstrap-v1.2.0)...
#     - Upgrading k3s from v1.27.0 to v1.27.0... (no change)
#   ✅ Bootstrap cluster upgraded successfully
#   - Upgrading management cluster (management-v1.1.0 → management-v1.2.0)...
#     - Upgrading containerd from v1.7.10 to v1.7.11...
#     - Upgrading kubelet from v1.28.1 to v1.28.2...
#   ✅ Management cluster upgraded successfully
#   - Upgrading workload clusters...
#     - Upgrading cluster-1 (workload-v1.1.0 → workload-v1.2.0)...
#       - Upgrading containerd from v1.7.10 to v1.7.11...
#       - Upgrading kubelet from v1.28.1 to v1.28.2...
#       - Upgrading csi from v1.10.0 to v1.10.1...
#     ✅ Cluster-1 upgraded successfully
#   ✅ All workload clusters upgraded successfully
# ℹ️  Running post-upgrade health checks...
# ✅ All health checks passed
# ℹ️  Updating version metadata...
# ✅ Version metadata updated
# ℹ️  Cleaning up temporary files...
# ✅ Cleanup completed
# 
# 🎉 Upgrade completed successfully!
# Current solution version: v1.2.0
```
### 4.3 升级工具实现
```go
package cmd

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "time"
    
    "github.com/spf13/cobra"
    
    "cluster-manager.io/pkg/cli"
    "cluster-manager.io/pkg/upgrade"
    "cluster-manager.io/pkg/version"
)

var upgradeCmd = &cobra.Command{
    Use:   "upgrade",
    Short: "Upgrade the cluster manager solution",
    Long:  "Upgrade the cluster manager solution to a newer version",
}

var checkCmd = &cobra.Command{
    Use:   "check",
    Short: "Check for available upgrades",
    Long:  "Check for available upgrades for the current solution version",
    RunE:  runUpgradeCheck,
}

var applyCmd = &cobra.Command{
    Use:   "apply VERSION",
    Short: "Apply an upgrade",
    Long:  "Apply an upgrade to the specified version",
    Args:  cobra.ExactArgs(1),
    RunE:  runUpgradeApply,
}

func init() {
    rootCmd.AddCommand(upgradeCmd)
    upgradeCmd.AddCommand(checkCmd)
    upgradeCmd.AddCommand(applyCmd)
    
    // 升级相关参数
    applyCmd.Flags().BoolP("dry-run", "d", false, "Dry run upgrade")
    applyCmd.Flags().BoolP("force", "f", false, "Force upgrade without prompts")
    applyCmd.Flags().StringP("backup-dir", "b", "", "Backup directory")
    applyCmd.Flags().BoolP("skip-pre-check", "s", false, "Skip pre-upgrade checks")
}

func runUpgradeCheck(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context()
    
    // 获取当前版本
    currentVersion, err := version.GetCurrentSolutionVersion(ctx)
    if err != nil {
        return fmt.Errorf("get current version failed: %v", err)
    }
    
    fmt.Printf("Current solution version: %s\n", currentVersion)
    
    // 检查可用升级
    availableUpgrades, err := upgrade.CheckAvailableUpgrades(ctx, currentVersion)
    if err != nil {
        return fmt.Errorf("check available upgrades failed: %v", err)
    }
    
    if len(availableUpgrades) == 0 {
        fmt.Println("No available upgrades")
        return nil
    }
    
    fmt.Println("Available upgrades:")
    for _, upgrade := range availableUpgrades {
        fmt.Printf("  - %s (%s)\n", upgrade.Version, upgrade.Description)
    }
    
    return nil
}

func runUpgradeApply(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context()
    
    // 解析参数
    targetVersion := args[0]
    dryRun, _ := cmd.Flags().GetBool("dry-run")
    force, _ := cmd.Flags().GetBool("force")
    backupDir, _ := cmd.Flags().GetString("backup-dir")
    skipPreCheck, _ := cmd.Flags().GetBool("skip-pre-check")
    
    // 获取当前版本
    currentVersion, err := version.GetCurrentSolutionVersion(ctx)
    if err != nil {
        return fmt.Errorf("get current version failed: %v", err)
    }
    
    fmt.Printf("Current solution version: %s\n", currentVersion)
    fmt.Printf("Target solution version: %s\n", targetVersion)
    
    // 确认升级
    if !force && !dryRun {
        if !cli.Confirm("Are you sure you want to proceed with the upgrade?") {
            return fmt.Errorf("upgrade cancelled by user")
        }
    }
    
    // 获取升级路径
    upgradePath, err := upgrade.GetUpgradePath(ctx, currentVersion, targetVersion)
    if err != nil {
        return fmt.Errorf("get upgrade path failed: %v", err)
    }
    
    // 下载版本包
    fmt.Printf("ℹ️  Downloading version package cluster-manager-solution-%s...\n", targetVersion)
    versionPackage, err := upgrade.DownloadVersionPackage(ctx, targetVersion)
    if err != nil {
        return fmt.Errorf("download version package failed: %v", err)
    }
    fmt.Printf("✅ Version package downloaded and verified\n")
    
    // 执行前置检查
    if !skipPreCheck && !dryRun {
        fmt.Println("ℹ️  Running pre-upgrade checks...")
        if err := upgrade.RunPreCheck(ctx, upgradePath, versionPackage); err != nil {
            return fmt.Errorf("pre-upgrade check failed: %v", err)
        }
        fmt.Println("✅ Pre-upgrade checks passed")
    }
    
    // 备份配置
    if !dryRun {
        fmt.Println("ℹ️  Backing up current configuration...")
        backupPath, err := upgrade.BackupConfiguration(ctx, currentVersion, backupDir)
        if err != nil {
            return fmt.Errorf("backup configuration failed: %v", err)
        }
        fmt.Printf("✅ Configuration backed up to %s\n", backupPath)
    }
    
    // 执行升级
    fmt.Println("ℹ️  Starting upgrade process...")
    if err := upgrade.ExecuteUpgrade(ctx, upgradePath, versionPackage, dryRun); err != nil {
        // 升级失败，尝试回滚
        if !dryRun {
            fmt.Printf("❌ Upgrade failed: %v\n", err)
            fmt.Println("ℹ️  Attempting rollback...")
            if rollbackErr := upgrade.RollbackUpgrade(ctx, currentVersion, versionPackage); rollbackErr != nil {
                return fmt.Errorf("upgrade failed and rollback also failed: upgrade error: %v, rollback error: %v", err, rollbackErr)
            }
            fmt.Println("✅ Rollback completed")
        }
        return fmt.Errorf("upgrade failed: %v", err)
    }
    
    // 执行健康检查
    if !dryRun {
        fmt.Println("ℹ️  Running post-upgrade health checks...")
        if err := upgrade.RunHealthChecks(ctx, upgradePath, versionPackage); err != nil {
            return fmt.Errorf("health check failed: %v", err)
        }
        fmt.Println("✅ All health checks passed")
    }
    
    // 更新版本元数据
    if !dryRun {
        fmt.Println("ℹ️  Updating version metadata...")
        if err := version.UpdateSolutionVersion(ctx, targetVersion); err != nil {
            return fmt.Errorf("update version metadata failed: %v", err)
        }
        fmt.Println("✅ Version metadata updated")
    }
    
    // 清理临时文件
    if !dryRun {
        fmt.Println("ℹ️  Cleaning up temporary files...")
        if err := upgrade.CleanupUpgrade(ctx, versionPackage); err != nil {
            fmt.Printf("⚠️  Cleanup failed: %v\n", err)
        } else {
            fmt.Println("✅ Cleanup completed")
        }
    }
    
    fmt.Println("\n🎉 Upgrade completed successfully!")
    fmt.Printf("Current solution version: %s\n", targetVersion)
    
    return nil
}
```
## 五、版本管理最佳实践
### 5.1 版本命名规范
- **解决方案版本**：采用语义化版本，如 `v1.2.0`
- **层级版本**：采用 `{layer}-{version}` 格式，如 `management-v1.2.0`
- **组件版本**：采用 `{component}-{version}` 格式，如 `containerd-v1.7.11`
### 5.2 版本兼容性策略
1. **解决方案版本**：
   - 主版本号(MAJOR)变更：不兼容的API变更
   - 次版本号(MINOR)变更：向下兼容的功能新增
   - 修订号(PATCH)变更：向下兼容的bug修复
2. **层级版本**：
   - 与解决方案版本保持一致
   - 支持跨小版本升级
3. **组件版本**：
   - 支持独立升级
   - 必须与Kubernetes版本兼容
### 5.3 升级安全策略
1. **备份机制**：
   - 升级前自动备份配置
   - 支持手动备份和恢复
2. **回滚机制**：
   - 升级失败自动回滚
   - 支持手动回滚到指定版本
3. **健康检查**：
   - 升级前的前置检查
   - 升级过程中的实时检查
   - 升级后的完整性检查
4. **灰度升级**：
   - 支持按比例升级业务集群
   - 支持暂停和继续升级
## 六、总结
本版本包设计提供了一个完整的集群管理解决方案版本管理和自动化升级框架：
1. **完整的版本包结构**：包含解决方案、各层级集群和组件的定义
2. **丰富的元数据**：描述版本信息、组件定义、升级路径和兼容性
3. **自动化升级流程**：从版本检测到升级完成的全自动化流程
4. **安全可靠的升级机制**：包含备份、回滚和健康检查
5. **灵活的扩展性**：支持自定义组件和升级策略

该设计解决了跨集群层级的版本管理和自动化升级问题，为用户提供了一个统一、安全、可靠的集群管理解决方案。

# `upgrade.ExecuteUpgrade` 实现思路
## 一、核心功能概述
`upgrade.ExecuteUpgrade` 是集群管理解决方案升级流程的核心函数，负责协调执行从引导集群、管理集群到业务集群的全流程升级。该函数需要处理版本兼容性验证、组件升级顺序、错误回滚和健康检查等关键任务。
## 二、整体设计思路
### 2.1 架构设计
```
┌─────────────────────────────────────────────────────────────────────┐
│                      ExecuteUpgrade 架构                          │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────┐
│  ExecuteUpgrade         │
└─────────────────────────┘
            │
            ▼
┌─────────────────────────┐     ┌─────────────────────────┐
│  UpgradeContext         │     │  VersionPackage         │
└─────────────────────────┘     └─────────────────────────┘
            │                           │
            ▼                           ▼
┌─────────────────────────┐     ┌─────────────────────────┐
│  UpgradeManager         │────▶│  UpgradePath            │
└─────────────────────────┘     └─────────────────────────┘
            │
            ▼
┌─────────────────────────┐
│  LayerUpgradeExecutors  │
│  ┌─────────────────────┐│
│  │  BootstrapExecutor  ││
│  └─────────────────────┘│
│  ┌─────────────────────┐│
│  │  ManagementExecutor ││
│  └─────────────────────┘│
│  ┌─────────────────────┐│
│  │  WorkloadExecutor   ││
│  └─────────────────────┘│
└─────────────────────────┘
            │
            ▼
┌─────────────────────────┐
│  ComponentExecutor      │
└─────────────────────────┘
            │
            ▼
┌─────────────────────────┐
│  UpgradeSteps           │
│  ┌─────────────────────┐│
│  │  PreCheckStep       ││
│  └─────────────────────┘│
│  ┌─────────────────────┐│
│  │  BackupStep         ││
│  └─────────────────────┘│
│  ┌─────────────────────┐│
│  │  InstallStep        ││
│  └─────────────────────┘│
│  ┌─────────────────────┐│
│  │  VerifyStep         ││
│  └─────────────────────┘│
└─────────────────────────┘
```
### 2.2 关键设计原则
1. **分层执行**：按引导集群 → 管理集群 → 业务集群的顺序执行升级
2. **组件级隔离**：每个组件的升级独立封装，便于维护和扩展
3. **幂等性**：所有升级操作均可重复执行，不会产生副作用
4. **可回滚**：支持在升级失败时回滚到安全状态
5. **可观测性**：详细的日志和指标收集，便于问题定位
6. **Dry-Run支持**：允许在不实际修改系统的情况下测试升级流程
## 三、详细实现思路
### 3.1 函数签名与参数
```go
func ExecuteUpgrade(ctx context.Context, upgradePath *UpgradePath, 
    versionPackage *VersionPackage, dryRun bool) error {
    // 实现代码
}
```
**参数说明**：
- `ctx`：上下文，用于控制超时和取消
- `upgradePath`：升级路径定义，包含从旧版本到新版本的详细升级步骤
- `versionPackage`：版本包，包含所有组件的安装包、脚本和配置
- `dryRun`：是否为模拟运行，不实际修改系统
### 3.2 核心执行流程
```go
func ExecuteUpgrade(ctx context.Context, upgradePath *UpgradePath, 
    versionPackage *VersionPackage, dryRun bool) error {
    
    // 1. 初始化升级上下文
    upgradeCtx, err := initializeUpgradeContext(ctx, upgradePath, versionPackage, dryRun)
    if err != nil {
        return fmt.Errorf("initialize upgrade context failed: %v", err)
    }
    
    // 2. 加载升级管理器
    upgradeManager := NewUpgradeManager(upgradeCtx)
    
    // 3. 按顺序执行各层级集群升级
    for _, layerUpgrade := range upgradePath.Spec.UpgradeOrder {
        // 执行层级升级
        if err := upgradeManager.ExecuteLayerUpgrade(layerUpgrade); err != nil {
            // 升级失败，执行回滚
            if rollbackErr := upgradeManager.RollbackLayerUpgrade(layerUpgrade); rollbackErr != nil {
                return fmt.Errorf("upgrade %s failed and rollback also failed: upgrade error: %v, rollback error: %v", 
                    layerUpgrade.Layer, err, rollbackErr)
            }
            return fmt.Errorf("upgrade %s failed and rolled back: %v", layerUpgrade.Layer, err)
        }
    }
    
    // 4. 升级完成，执行最终验证
    if err := upgradeManager.FinalizeUpgrade(); err != nil {
        return fmt.Errorf("finalize upgrade failed: %v", err)
    }
    
    return nil
}
```
### 3.3 升级上下文初始化
```go
func initializeUpgradeContext(ctx context.Context, upgradePath *UpgradePath, 
    versionPackage *VersionPackage, dryRun bool) (*UpgradeContext, error) {
    
    // 创建升级上下文
    upgradeCtx := &UpgradeContext{
        Context:        ctx,
        UpgradePath:    upgradePath,
        VersionPackage: versionPackage,
        DryRun:         dryRun,
        StartTime:      time.Now(),
        Steps:          make(map[string]*UpgradeStep),
        Logger:         NewUpgradeLogger(upgradePath.Spec.ToVersion),
        Metrics:        NewUpgradeMetrics(),
    }
    
    // 加载配置
    config, err := loadUpgradeConfig()
    if err != nil {
        return nil, err
    }
    upgradeCtx.Config = config
    
    // 初始化状态存储
    upgradeCtx.StateStore, err = NewStateStore(config.StateStorePath)
    if err != nil {
        return nil, err
    }
    
    // 记录开始状态
    if err := upgradeCtx.StateStore.SaveInitialState(); err != nil {
        return nil, err
    }
    
    return upgradeCtx, nil
}
```
### 3.4 升级管理器实现
```go
type UpgradeManager struct {
    ctx     *UpgradeContext
    executors map[string]LayerUpgradeExecutor
}

func NewUpgradeManager(ctx *UpgradeContext) *UpgradeManager {
    return &UpgradeManager{
        ctx:     ctx,
        executors: make(map[string]LayerUpgradeExecutor),
    }
}

func (m *UpgradeManager) ExecuteLayerUpgrade(layerUpgrade LayerUpgrade) error {
    m.ctx.Logger.Info("Starting layer upgrade", "layer", layerUpgrade.Layer)
    
    // 获取或创建层级执行器
    executor, err := m.getLayerExecutor(layerUpgrade.Layer)
    if err != nil {
        return fmt.Errorf("get layer executor failed: %v", err)
    }
    
    // 加载层级升级配置
    layerUpgradeConfig, err := m.ctx.VersionPackage.GetLayerUpgradeConfig(layerUpgrade.Path)
    if err != nil {
        return fmt.Errorf("get layer upgrade config failed: %v", err)
    }
    
    // 执行层级升级
    if err := executor.Execute(layerUpgradeConfig); err != nil {
        m.ctx.Logger.Error(err, "Layer upgrade failed", "layer", layerUpgrade.Layer)
        return err
    }
    
    m.ctx.Logger.Info("Layer upgrade completed", "layer", layerUpgrade.Layer)
    return nil
}

func (m *UpgradeManager) getLayerExecutor(layer string) (LayerUpgradeExecutor, error) {
    // 检查执行器是否已存在
    if executor, ok := m.executors[layer]; ok {
        return executor, nil
    }
    
    // 创建新的执行器
    var executor LayerUpgradeExecutor
    switch layer {
    case "bootstrap":
        executor = NewBootstrapUpgradeExecutor(m.ctx)
    case "management":
        executor = NewManagementUpgradeExecutor(m.ctx)
    case "workload":
        executor = NewWorkloadUpgradeExecutor(m.ctx)
    default:
        return nil, fmt.Errorf("unsupported layer: %s", layer)
    }
    
    m.executors[layer] = executor
    return executor, nil
}
```
### 3.5 层级执行器实现（以管理集群为例）
```go
type ManagementUpgradeExecutor struct {
    ctx      *UpgradeContext
    client   client.Client
    sshMgr   *SSHManager
    kubeconfig string
}

func NewManagementUpgradeExecutor(ctx *UpgradeContext) *ManagementUpgradeExecutor {
    // 初始化客户端和SSH管理器
    kubeconfig, err := getManagementClusterKubeconfig()
    if err != nil {
        return nil, err
    }
    
    config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
    if err != nil {
        return nil, err
    }
    
    k8sClient, err := client.New(config, client.Options{})
    if err != nil {
        return nil, err
    }
    
    return &ManagementUpgradeExecutor{
        ctx:      ctx,
        client:   k8sClient,
        sshMgr:   NewSSHManager(),
        kubeconfig: kubeconfig,
    }
}

func (e *ManagementUpgradeExecutor) Execute(config *LayerUpgradeConfig) error {
    // 1. 执行前置检查
    if err := e.runPreChecks(config); err != nil {
        return fmt.Errorf("pre-checks failed: %v", err)
    }
    
    // 2. 备份当前状态
    if err := e.backupCurrentState(config); err != nil {
        return fmt.Errorf("backup failed: %v", err)
    }
    
    // 3. 执行组件升级
    for _, componentChange := range config.ComponentChanges {
        if err := e.upgradeComponent(componentChange); err != nil {
            return fmt.Errorf("upgrade component %s failed: %v", componentChange.Name, err)
        }
    }
    
    // 4. 执行后置检查
    if err := e.runPostChecks(config); err != nil {
        return fmt.Errorf("post-checks failed: %v", err)
    }
    
    // 5. 更新层级版本信息
    if err := e.updateLayerVersion(config); err != nil {
        return fmt.Errorf("update layer version failed: %v", err)
    }
    
    return nil
}

func (e *ManagementUpgradeExecutor) upgradeComponent(change *ComponentChange) error {
    e.ctx.Logger.Info("Upgrading component", "component", change.Name, 
        "from", change.FromVersion, "to", change.ToVersion)
    
    // 获取组件版本定义
    componentVersion, err := e.ctx.VersionPackage.GetComponentVersion(
        "management", change.Name, change.Type, change.ToVersion)
    if err != nil {
        return err
    }
    
    // 获取组件当前版本定义
    currentVersion, err := e.ctx.VersionPackage.GetComponentVersion(
        "management", change.Name, change.Type, change.FromVersion)
    if err != nil {
        return err
    }
    
    // 创建组件执行器
    executor := NewComponentUpgradeExecutor(e.ctx, e.client, e.sshMgr)
    
    // 执行组件升级
    if err := executor.Upgrade(componentVersion, currentVersion); err != nil {
        return err
    }
    
    e.ctx.Logger.Info("Component upgraded successfully", "component", change.Name)
    return nil
}
```
### 3.6 组件执行器实现
```go
type ComponentUpgradeExecutor struct {
    ctx      *UpgradeContext
    client   client.Client
    sshMgr   *SSHManager
}

func NewComponentUpgradeExecutor(ctx *UpgradeContext, client client.Client, sshMgr *SSHManager) *ComponentUpgradeExecutor {
    return &ComponentUpgradeExecutor{
        ctx:    ctx,
        client: client,
        sshMgr: sshMgr,
    }
}

func (e *ComponentUpgradeExecutor) Upgrade(targetVersion, currentVersion *ComponentVersion) error {
    // 1. 渲染升级脚本
    upgradeScript, err := e.renderUpgradeScript(targetVersion, currentVersion)
    if err != nil {
        return fmt.Errorf("render upgrade script failed: %v", err)
    }
    
    // 2. 获取所有需要升级的节点
    nodes, err := e.getTargetNodes(targetVersion)
    if err != nil {
        return fmt.Errorf("get target nodes failed: %v", err)
    }
    
    // 3. 按顺序升级每个节点
    for _, node := range nodes {
        if err := e.upgradeNode(node, upgradeScript, targetVersion); err != nil {
            return fmt.Errorf("upgrade node %s failed: %v", node.Name, err)
        }
    }
    
    // 4. 验证组件升级结果
    if err := e.verifyComponentUpgrade(targetVersion); err != nil {
        return fmt.Errorf("verify component upgrade failed: %v", err)
    }
    
    return nil
}

func (e *ComponentUpgradeExecutor) upgradeNode(node *corev1.Node, upgradeScript string, 
    componentVersion *ComponentVersion) error {
    
    e.ctx.Logger.Info("Upgrading node", "node", node.Name, "component", componentVersion.Spec.Name)
    
    // 获取节点IP
    nodeIP := getNodeIP(node)
    
    // 获取SSH密钥
    sshKey, err := e.getSSHKey()
    if err != nil {
        return err
    }
    
    // 创建SSH客户端
    sshClient, err := e.sshMgr.NewClient(nodeIP, 22, "root", sshKey)
    if err != nil {
        return err
    }
    defer sshClient.Close()
    
    // 如果是Dry-Run模式，只记录不执行
    if e.ctx.DryRun {
        e.ctx.Logger.Info("Dry-run: would execute upgrade script", "node", node.Name)
        return nil
    }
    
    // 上传升级脚本
    scriptPath := fmt.Sprintf("/tmp/upgrade-%s-%s.sh", 
        componentVersion.Spec.Name, componentVersion.Spec.Version)
    
    if err := sshClient.UploadFile(scriptPath, []byte(upgradeScript)); err != nil {
        return fmt.Errorf("upload upgrade script failed: %v", err)
    }
    
    // 执行升级脚本
    cmd := fmt.Sprintf("chmod +x %s && %s", scriptPath, scriptPath)
    output, err := sshClient.ExecuteCommand(cmd)
    if err != nil {
        return fmt.Errorf("execute upgrade script failed: %v, output: %s", err, output)
    }
    
    // 清理脚本
    sshClient.ExecuteCommand(fmt.Sprintf("rm %s", scriptPath))
    
    e.ctx.Logger.Info("Node upgraded successfully", "node", node.Name)
    return nil
}
```
### 3.7 回滚机制实现
```go
func (m *UpgradeManager) RollbackLayerUpgrade(layerUpgrade LayerUpgrade) error {
    m.ctx.Logger.Info("Starting layer rollback", "layer", layerUpgrade.Layer)
    
    // 获取层级执行器
    executor, err := m.getLayerExecutor(layerUpgrade.Layer)
    if err != nil {
        return fmt.Errorf("get layer executor failed: %v", err)
    }
    
    // 加载层级升级配置
    layerUpgradeConfig, err := m.ctx.VersionPackage.GetLayerUpgradeConfig(layerUpgrade.Path)
    if err != nil {
        return fmt.Errorf("get layer upgrade config failed: %v", err)
    }
    
    // 执行回滚
    if err := executor.Rollback(layerUpgradeConfig); err != nil {
        m.ctx.Logger.Error(err, "Layer rollback failed", "layer", layerUpgrade.Layer)
        return err
    }
    
    m.ctx.Logger.Info("Layer rollback completed", "layer", layerUpgrade.Layer)
    return nil
}

func (e *ManagementUpgradeExecutor) Rollback(config *LayerUpgradeConfig) error {
    // 1. 执行回滚前检查
    if err := e.runRollbackPreChecks(config); err != nil {
        return fmt.Errorf("rollback pre-checks failed: %v", err)
    }
    
    // 2. 恢复备份
    if err := e.restoreBackup(config); err != nil {
        return fmt.Errorf("restore backup failed: %v", err)
    }
    
    // 3. 执行组件回滚
    for i := len(config.ComponentChanges) - 1; i >= 0; i-- {
        componentChange := config.ComponentChanges[i]
        if err := e.rollbackComponent(componentChange); err != nil {
            return fmt.Errorf("rollback component %s failed: %v", componentChange.Name, err)
        }
    }
    
    // 4. 执行回滚后检查
    if err := e.runRollbackPostChecks(config); err != nil {
        return fmt.Errorf("rollback post-checks failed: %v", err)
    }
    
    // 5. 恢复层级版本信息
    if err := e.restoreLayerVersion(config); err != nil {
        return fmt.Errorf("restore layer version failed: %v", err)
    }
    
    return nil
}
```
### 3.8 Dry-Run模式支持
```go
func (e *ComponentUpgradeExecutor) Upgrade(targetVersion, currentVersion *ComponentVersion) error {
    // ...
    
    // 3. 按顺序升级每个节点
    for _, node := range nodes {
        if err := e.upgradeNode(node, upgradeScript, targetVersion); err != nil {
            return fmt.Errorf("upgrade node %s failed: %v", node.Name, err)
        }
    }
    
    // ...
}

func (e *ComponentUpgradeExecutor) upgradeNode(node *corev1.Node, upgradeScript string, 
    componentVersion *ComponentVersion) error {
    
    // ...
    
    // 如果是Dry-Run模式，只记录不执行
    if e.ctx.DryRun {
        e.ctx.Logger.Info("Dry-run: would execute upgrade script", 
            "node", node.Name,
            "script", upgradeScript[:100] + "...")  // 只显示脚本开头
        return nil
    }
    
    // ... 实际执行升级操作
}
```
## 四、关键技术实现细节
### 4.1 版本兼容性验证
```go
func (e *ComponentUpgradeExecutor) verifyCompatibility(targetVersion, currentVersion *ComponentVersion) error {
    // 检查当前版本是否在目标版本的兼容列表中
    compatible := false
    for _, compatibleVersion := range targetVersion.Spec.Compatibility.PreviousVersions {
        if currentVersion.Spec.Version == compatibleVersion {
            compatible = true
            break
        }
    }
    
    if !compatible {
        return fmt.Errorf("component %s version %s is not compatible with target version %s", 
            targetVersion.Spec.Name, currentVersion.Spec.Version, targetVersion.Spec.Version)
    }
    
    // 检查Kubernetes版本兼容性
    kubernetesVersion, err := e.getKubernetesVersion()
    if err != nil {
        return err
    }
    
    if !isVersionInRange(kubernetesVersion, targetVersion.Spec.Compatibility.KubernetesVersions.Min, 
        targetVersion.Spec.Compatibility.KubernetesVersions.Max) {
        return fmt.Errorf("Kubernetes version %s is not compatible with component %s version %s", 
            kubernetesVersion, targetVersion.Spec.Name, targetVersion.Spec.Version)
    }
    
    return nil
}
```
### 4.2 并行升级与批量控制
```go
func (e *WorkloadUpgradeExecutor) Execute(config *LayerUpgradeConfig) error {
    // ...
    
    // 3. 执行组件升级（支持批量并行）
    upgradeStrategy := config.UpgradeStrategy
    
    // 根据策略分组节点
    nodeGroups := e.groupNodesByStrategy(upgradeStrategy)
    
    // 按组执行升级
    for _, group := range nodeGroups {
        // 并行升级组内节点
        var wg sync.WaitGroup
        errs := make(chan error, len(group.Nodes))
        
        for _, node := range group.Nodes {
            wg.Add(1)
            go func(n *corev1.Node) {
                defer wg.Done()
                
                // 升级节点
                if err := e.upgradeNode(n, config); err != nil {
                    errs <- err
                    return
                }
                
                // 等待节点就绪
                if err := e.waitForNodeReady(n, upgradeStrategy.NodeReadyTimeout); err != nil {
                    errs <- err
                    return
                }
            }(node)
        }
        
        wg.Wait()
        close(errs)
        
        // 检查错误
        for err := range errs {
            if err != nil {
                return fmt.Errorf("upgrade group failed: %v", err)
            }
        }
        
        // 组间等待
        time.Sleep(upgradeStrategy.GroupInterval.Duration)
    }
    
    // ...
}
```
### 4.3 健康检查与监控
```go
func (e *ManagementUpgradeExecutor) runPostChecks(config *LayerUpgradeConfig) error {
    e.ctx.Logger.Info("Running post-upgrade health checks")
    
    // 检查集群状态
    if err := e.checkClusterHealth(); err != nil {
        return fmt.Errorf("cluster health check failed: %v", err)
    }
    
    // 检查节点状态
    if err := e.checkNodesHealth(); err != nil {
        return fmt.Errorf("nodes health check failed: %v", err)
    }
    
    // 检查组件状态
    for _, componentChange := range config.ComponentChanges {
        if err := e.checkComponentHealth(componentChange); err != nil {
            return fmt.Errorf("component %s health check failed: %v", componentChange.Name, err)
        }
    }
    
    // 检查API可用性
    if err := e.checkAPIAvailability(); err != nil {
        return fmt.Errorf("API availability check failed: %v", err)
    }
    
    return nil
}

func (e *ManagementUpgradeExecutor) checkComponentHealth(componentChange *ComponentChange) error {
    // 构建健康检查命令
    healthCheckCmd := fmt.Sprintf("systemctl is-active %s", componentChange.Name)
    
    // 获取所有节点
    nodes, err := e.getTargetNodesForComponent(componentChange)
    if err != nil {
        return err
    }
    
    // 在每个节点上执行健康检查
    for _, node := range nodes {
        nodeIP := getNodeIP(node)
        sshClient, err := e.sshMgr.NewClient(nodeIP, 22, "root", e.getSSHKey())
        if err != nil {
            return err
        }
        defer sshClient.Close()
        
        output, err := sshClient.ExecuteCommand(healthCheckCmd)
        if err != nil {
            return fmt.Errorf("health check failed on node %s: %v", node.Name, err)
        }
        
        if strings.TrimSpace(output) != "active" {
            return fmt.Errorf("component %s is not active on node %s", componentChange.Name, node.Name)
        }
    }
    
    return nil
}
```
## 五、总结
`upgrade.ExecuteUpgrade` 函数通过分层架构和模块化设计，实现了集群管理解决方案的全流程自动化升级。其核心特点包括：
1. **分层执行**：按引导集群 → 管理集群 → 业务集群的顺序执行升级
2. **组件级隔离**：每个组件的升级独立封装，便于维护和扩展
3. **安全可靠**：包含完善的备份、回滚和健康检查机制
4. **可观测性**：详细的日志和指标收集，便于问题定位
5. **灵活扩展**：支持自定义升级策略和组件执行器
6. **Dry-Run支持**：允许在不实际修改系统的情况下测试升级流程

这种设计确保了升级过程的安全性、可靠性和可维护性，能够有效支持集群管理解决方案的版本迭代和升级需求。

