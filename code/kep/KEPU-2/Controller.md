# BKEClusterReconciler和ClusterVersion Controller的详细设计思路与代码实现
## 一、设计思路
### 1.1 整体架构演进
```
┌─────────────────────────────────────────────────────────────┐
│                    BKEClusterReconciler                     │
│  (保持现有职责：管理 BKECluster 生命周期)                   │
└────────────────────┬────────────────────────────────────────┘
                     │
        ┌────────────┴────────────┐
        │  Feature Gate 检查      │
        └────────────┬────────────┘
                     │
        ┌────────────┴────────────────────────────┐
        │                                         │
   ┌────▼─────┐                           ┌──────▼───────┐
   │ 旧路径   │                           │  新路径      │
   │PhaseFlow │                           │ClusterVersion│
   │ (保留)   │                           │  Controller  │
   └──────────┘                           └──────┬───────┘
                                                 │
                                    ┌────────────┴────────────┐
                                    │                         │
                            ┌───────▼────────┐       ┌────────▼────────┐
                            │ ReleaseImage   │       │ ComponentVersion│
                            │  Controller    │       │   Controller    │
                            │ (版本清单管理) │       │  (组件生命周期) │
                            └────────────────┘       └─────────────────┘
```
### 1.2 BKEClusterReconciler 改造要点
**核心原则**：保持现有职责不变，通过 Feature Gate 渐进切换到声明式架构

**主要变化**：
1. **新增 ClusterVersion 创建逻辑**：在集群初始化时创建对应的 ClusterVersion CR
2. **Feature Gate 分流**：根据 Feature Gate 决定使用 PhaseFlow 还是 ClusterVersion 编排
3. **Watch ClusterVersion**：监听 ClusterVersion 状态变化，更新 BKECluster Status
4. **保留现有 PhaseFlow**：确保向后兼容
### 1.3 ClusterVersion Controller 设计要点
**核心职责**：
1. **框架级逻辑**：处理 EnsureFinalizer、EnsurePaused、EnsureDeleteOrReset、EnsureDryRun
2. **版本编排**：管理集群版本升级流程
3. **DAG 调度**：按依赖关系调度 ComponentVersion 升级
4. **历史管理**：维护版本历史，支持回滚

**关键设计**：
- **Finalizer 管理**：在 Reconcile 开始时添加 Finalizer，删除时触发各组件 uninstallAction
- **Pause 控制**：暂停时停止所有 ComponentVersion 的调谐
- **Delete/Reset 编排**：删除时按逆序调用各组件的 uninstallAction
- **升级编排**：检测 desiredVersion 变化 → 解析 ReleaseImage → DAG 调度 → 逐组件升级
## 二、代码实现
### 2.1 BKEClusterReconciler 改造
```go
// d:\code\github\cluster-api-provider-bke\controllers\capbke\bkecluster_controller.go

package capbke

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/feature"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phases"
	bkepredicates "gopkg.openfuyao.cn/cluster-api-provider-bke/predicates"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	nodeutil "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

const (
	nodeWatchRequeueInterval = 10 * time.Minute
)

var log = capbkelog.Log

type BKEClusterReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	RestConfig  *rest.Config
	Tracker     *remote.ClusterCacheTracker
	controller  controller.Controller
	NodeFetcher *nodeutil.NodeFetcher
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

	// ===== 新增：Feature Gate 分流 =====
	if feature.DefaultFeatureGate.Enabled(feature.DeclarativeVersionOrchestration) {
		// 新路径：通过 ClusterVersion 编排
		return r.reconcileWithClusterVersion(ctx, bkeCluster, oldBkeCluster, bkeLogger)
	}

	// 旧路径：通过 PhaseFlow 编排
	return r.reconcileWithPhaseFlow(ctx, bkeCluster, oldBkeCluster, bkeLogger)
}

// reconcileWithClusterVersion 使用 ClusterVersion 编排集群生命周期
func (r *BKEClusterReconciler) reconcileWithClusterVersion(
	ctx context.Context,
	bkeCluster *bkev1beta1.BKECluster,
	oldBkeCluster *bkev1beta1.BKECluster,
	bkeLogger *bkev1beta1.BKELogger,
) (ctrl.Result, error) {
	// 1. 确保存在对应的 ClusterVersion CR
	clusterVersion, err := r.ensureClusterVersion(ctx, bkeCluster)
	if err != nil {
		bkeLogger.Error(constant.ReconcileErrorReason, "failed to ensure ClusterVersion: %v", err)
		return ctrl.Result{}, err
	}

	// 2. 同步 BKECluster Spec 到 ClusterVersion
	if err := r.syncBKEClusterSpecToClusterVersion(ctx, bkeCluster, clusterVersion); err != nil {
		bkeLogger.Error(constant.ReconcileErrorReason, "failed to sync spec to ClusterVersion: %v", err)
		return ctrl.Result{}, err
	}

	// 3. 根据 ClusterVersion 状态更新 BKECluster Status
	if err := r.syncClusterVersionStatusToBKECluster(ctx, bkeCluster, clusterVersion); err != nil {
		bkeLogger.Error(constant.ReconcileErrorReason, "failed to sync ClusterVersion status: %v", err)
		return ctrl.Result{}, err
	}

	// 4. 设置集群监控
	watchResult, err := r.setupClusterWatching(ctx, bkeCluster, bkeLogger)
	if err != nil {
		return watchResult, err
	}

	return statusmanage.BKEClusterStatusManager.GetCtrlResult(bkeCluster), nil
}

// ensureClusterVersion 确保存在对应的 ClusterVersion CR
func (r *BKEClusterReconciler) ensureClusterVersion(
	ctx context.Context,
	bkeCluster *bkev1beta1.BKECluster,
) (*cvov1beta1.ClusterVersion, error) {
	clusterVersion := &cvov1beta1.ClusterVersion{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      bkeCluster.Name,
		Namespace: bkeCluster.Namespace,
	}, clusterVersion)

	if err == nil {
		return clusterVersion, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	// 创建新的 ClusterVersion
	clusterVersion = &cvov1beta1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bkeCluster.Name,
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
		Spec: cvov1beta1.ClusterVersionSpec{
			DesiredVersion: bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion,
			ClusterRef: &corev1.ObjectReference{
				APIVersion: bkev1beta1.GroupVersion.String(),
				Kind:       "BKECluster",
				Name:       bkeCluster.Name,
				Namespace:  bkeCluster.Namespace,
			},
			Pause: bkeCluster.Spec.Pause,
		},
	}

	if err := r.Create(ctx, clusterVersion); err != nil {
		return nil, errors.Wrap(err, "failed to create ClusterVersion")
	}

	return clusterVersion, nil
}

// syncBKEClusterSpecToClusterVersion 同步 BKECluster Spec 到 ClusterVersion
func (r *BKEClusterReconciler) syncBKEClusterSpecToClusterVersion(
	ctx context.Context,
	bkeCluster *bkev1beta1.BKECluster,
	clusterVersion *cvov1beta1.ClusterVersion,
) error {
	if clusterVersion.Spec.DesiredVersion == bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion &&
		clusterVersion.Spec.Pause == bkeCluster.Spec.Pause {
		return nil
	}

	patchHelper, err := patch.NewHelper(clusterVersion, r.Client)
	if err != nil {
		return err
	}

	clusterVersion.Spec.DesiredVersion = bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
	clusterVersion.Spec.Pause = bkeCluster.Spec.Pause

	return patchHelper.Patch(ctx, clusterVersion)
}

// syncClusterVersionStatusToBKECluster 同步 ClusterVersion 状态到 BKECluster
func (r *BKEClusterReconciler) syncClusterVersionStatusToBKECluster(
	ctx context.Context,
	bkeCluster *bkev1beta1.BKECluster,
	clusterVersion *cvov1beta1.ClusterVersion,
) error {
	patchHelper, err := patch.NewHelper(bkeCluster, r.Client)
	if err != nil {
		return err
	}

	// 同步版本信息
	bkeCluster.Status.OpenFuyaoVersion = clusterVersion.Status.CurrentVersion

	// 同步阶段状态
	if clusterVersion.Status.Phase != "" {
		bkeCluster.Status.Phase = confv1beta1.BKEClusterPhase(clusterVersion.Status.Phase)
	}

	// 同步条件
	for _, cond := range clusterVersion.Status.Conditions {
		condition.ConditionMark(bkeCluster, confv1beta1.ClusterConditionType(cond.Type), confv1beta1.ConditionStatus(cond.Status), cond.Reason, cond.Message)
	}

	return patchHelper.Patch(ctx, bkeCluster)
}

// reconcileWithPhaseFlow 使用 PhaseFlow 编排集群生命周期（保留旧路径）
func (r *BKEClusterReconciler) reconcileWithPhaseFlow(
	ctx context.Context,
	bkeCluster *bkev1beta1.BKECluster,
	oldBkeCluster *bkev1beta1.BKECluster,
	bkeLogger *bkev1beta1.BKELogger,
) (ctrl.Result, error) {
	phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx).
		SetBKECluster(bkeCluster).
		SetClient(r.Client).
		SetLogger(bkeLogger).
		SetScheme(r.Scheme).
		SetRestConfig(r.RestConfig)

	if err := phaseCtx.RefreshCtxCluster(); err != nil {
		return ctrl.Result{}, err
	}

	flow := phases.NewPhaseFlow(phaseCtx, oldBkeCluster, bkeCluster)
	err := flow.CalculatePhase(oldBkeCluster, bkeCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	res, err := flow.Execute()
	if err != nil {
		bkeLogger.Warn(constant.ReconcileErrorReason, "Reconcile bkeCluster %q failed: %v", utils.ClientObjNS(bkeCluster), err)
	}

	return res, nil
}

// SetupWithManager 设置控制器
func (r *BKEClusterReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager,
	options controller.Options) error {

	r.NodeFetcher = nodeutil.NewNodeFetcher(mgr.GetClient())

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&bkev1beta1.BKECluster{},
			builder.WithPredicates(predicate.Or(
				bkepredicates.BKEClusterAnnotationsChange(),
				bkepredicates.BKEClusterSpecChange(),
			)),
		).
		WithOptions(options).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(clusterToBKEClusterMapFunc(ctx,
				bkev1beta1.GroupVersion.WithKind("BKECluster"),
				mgr.GetClient(), &bkev1beta1.BKECluster{})),
			builder.WithPredicates(bkepredicates.ClusterUnPause()),
		).
		Watches(
			&confv1beta1.BKENode{},
			handler.EnqueueRequestsFromMapFunc(r.bkeNodeToBKEClusterMapFunc()),
			builder.WithPredicates(bkepredicates.BKENodeChange()),
		)

	// ===== 新增：监听 ClusterVersion =====
	if feature.DefaultFeatureGate.Enabled(feature.DeclarativeVersionOrchestration) {
		builder.Watches(
			&cvov1beta1.ClusterVersion{},
			handler.EnqueueRequestsFromMapFunc(r.clusterVersionToBKEClusterMapFunc()),
		)
	}

	c, err := builder.Build(r)
	if err != nil {
		return errors.Errorf("failed setting up with a controller manager: %v", err)
	}
	r.controller = c
	return nil
}

// clusterVersionToBKEClusterMapFunc ClusterVersion 到 BKECluster 的映射
func (r *BKEClusterReconciler) clusterVersionToBKEClusterMapFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		clusterVersion, ok := obj.(*cvov1beta1.ClusterVersion)
		if !ok {
			return nil
		}

		// ClusterVersion 与 BKECluster 同名同命名空间
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Name:      clusterVersion.Name,
				Namespace: clusterVersion.Namespace,
			},
		}}
	}
}

// ... 其他辅助方法保持不变 ...
```
### 2.2 Feature Gate 定义
```go
// d:\code\github\cluster-api-provider-bke\pkg\feature\feature_gate.go

package feature

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// DeclarativeVersionOrchestration 启用声明式版本编排
	// 启用后，集群生命周期由 ClusterVersion/ComponentVersion 编排，而非 PhaseFlow
	DeclarativeVersionOrchestration featuregate.Feature = "DeclarativeVersionOrchestration"
)

func init() {
	runtime.Must(featuregate.DefaultMutableFeatureGate.Add(defaultFeatureGates))
}

var defaultFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	DeclarativeVersionOrchestration: {Default: false, PreRelease: featuregate.Alpha},
}
```
### 2.3 ClusterVersion Controller 实现
```go
// d:\code\github\cluster-api-provider-bke\controllers\cvo\clusterversion_controller.go

package cvo

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
	nodecomponentv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/nodecomponent/v1alpha1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/dag_scheduler"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/orchestrator"
)

const (
	clusterVersionFinalizer = "clusterversion.cvo.openfuyao.cn/finalizer"
)

// ClusterVersionReconciler reconciles a ClusterVersion object
type ClusterVersionReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Recorder      record.EventRecorder
	Orchestrator  *orchestrator.Orchestrator
	DAGScheduler  *dag_scheduler.DAGScheduler
}

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cv := &cvov1beta1.ClusterVersion{}
	if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(cv, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, cv); err != nil {
			logger.Error(err, "failed to patch ClusterVersion")
		}
	}()

	// ===== 1. 处理删除 =====
	if !cv.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, cv)
	}

	// ===== 2. 确保 Finalizer =====
	if !controllerutil.ContainsFinalizer(cv, clusterVersionFinalizer) {
		controllerutil.AddFinalizer(cv, clusterVersionFinalizer)
		conditions.MarkTrue(cv, cvov1beta1.ClusterVersionFinalizerAdded, "FinalizerAdded", "Finalizer added successfully")
		return ctrl.Result{}, nil
	}

	// ===== 3. 处理暂停 =====
	if cv.Spec.Pause {
		conditions.MarkTrue(cv, cvov1beta1.ClusterVersionPaused, "Paused", "ClusterVersion reconciliation is paused")
		cv.Status.Phase = cvov1beta1.ClusterVersionPhasePaused
		return ctrl.Result{}, nil
	}

	// ===== 4. 处理 DryRun =====
	if cv.Spec.DryRun {
		return r.reconcileDryRun(ctx, cv)
	}

	// ===== 5. 处理 Reset =====
	if cv.Spec.Reset {
		return r.reconcileReset(ctx, cv)
	}

	// ===== 6. 处理版本变更 =====
	return r.reconcileVersion(ctx, cv)
}

// reconcileDelete 处理删除逻辑
func (r *ClusterVersionReconciler) reconcileDelete(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. 获取所有 ComponentVersion，按依赖逆序排列
	componentVersions, err := r.getAllComponentVersions(ctx, cv)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 2. 按逆序执行 uninstallAction
	for i := len(componentVersions) - 1; i >= 0; i-- {
		cv := componentVersions[i]
		if cv.Spec.UninstallAction != nil && cv.Status.Phase != nodecomponentv1alpha1.CompPhaseUninstalled {
			cv.Status.Phase = nodecomponentv1alpha1.CompPhaseUninstalling
			if err := r.Status().Update(ctx, cv); err != nil {
				return ctrl.Result{}, err
			}

			// 等待 ComponentVersion Controller 执行 uninstallAction
			if err := r.waitForComponentPhase(ctx, cv, nodecomponentv1alpha1.CompPhaseUninstalled, 5*time.Minute); err != nil {
				logger.Error(err, "failed to uninstall component", "component", cv.Name)
				return ctrl.Result{}, err
			}
		}
	}

	// 3. 移除 Finalizer
	controllerutil.RemoveFinalizer(cv, clusterVersionFinalizer)
	return ctrl.Result{}, nil
}

// reconcileDryRun 处理 DryRun 逻辑
func (r *ClusterVersionReconciler) reconcileDryRun(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. 验证 ReleaseImage 是否存在
	if cv.Spec.ReleaseRef == nil {
		return ctrl.Result{}, errors.New("releaseRef is required for dry-run")
	}

	releaseImage := &cvov1beta1.ReleaseImage{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      cv.Spec.ReleaseRef.Name,
		Namespace: cv.Spec.ReleaseRef.Namespace,
	}, releaseImage); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to get ReleaseImage")
	}

	// 2. 验证所有 ComponentVersion 是否存在
	for _, compRef := range releaseImage.Spec.ComponentVersions {
		cv := &nodecomponentv1alpha1.ComponentVersion{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      compRef.Name,
			Namespace: cv.Namespace,
		}, cv); err != nil {
			logger.Error(err, "ComponentVersion not found", "name", compRef.Name)
			conditions.MarkFalse(cv, cvov1beta1.ClusterVersionValid, "ComponentNotFound", "ComponentVersion %s not found", compRef.Name)
			return ctrl.Result{}, err
		}
	}

	// 3. 验证 DAG 是否有循环依赖
	if err := r.DAGScheduler.ValidateDAG(releaseImage); err != nil {
		conditions.MarkFalse(cv, cvov1beta1.ClusterVersionValid, "InvalidDAG", "DAG validation failed: %v", err)
		return ctrl.Result{}, err
	}

	conditions.MarkTrue(cv, cvov1beta1.ClusterVersionValid, "Valid", "ClusterVersion is valid")
	cv.Status.Phase = cvov1beta1.ClusterVersionPhaseValidated
	return ctrl.Result{}, nil
}

// reconcileReset 处理 Reset 逻辑
func (r *ClusterVersionReconciler) reconcileReset(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
	// Reset 本质上是删除后重建，直接调用删除逻辑
	return r.reconcileDelete(ctx, cv)
}

// reconcileVersion 处理版本变更逻辑
func (r *ClusterVersionReconciler) reconcileVersion(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. 检查是否需要升级
	if cv.Status.CurrentVersion == cv.Spec.DesiredVersion && cv.Status.Phase == cvov1beta1.ClusterVersionPhaseReady {
		return ctrl.Result{}, nil
	}

	// 2. 解析 ReleaseImage
	releaseImage, err := r.resolveReleaseImage(ctx, cv)
	if err != nil {
		conditions.MarkFalse(cv, cvov1beta1.ClusterVersionReleaseResolved, "ResolveFailed", "Failed to resolve ReleaseImage: %v", err)
		return ctrl.Result{}, err
	}
	conditions.MarkTrue(cv, cvov1beta1.ClusterVersionReleaseResolved, "Resolved", "ReleaseImage resolved successfully")

	// 3. 构建 DAG
	dag, err := r.DAGScheduler.BuildDAG(releaseImage)
	if err != nil {
		conditions.MarkFalse(cv, cvov1beta1.ClusterVersionDAGBuilt, "BuildFailed", "Failed to build DAG: %v", err)
		return ctrl.Result{}, err
	}
	conditions.MarkTrue(cv, cvov1beta1.ClusterVersionDAGBuilt, "Built", "DAG built successfully")

	// 4. 执行升级编排
	cv.Status.Phase = cvov1beta1.ClusterVersionPhaseUpgrading
	cv.Status.UpgradeSteps = dag.GetSteps()
	cv.Status.CurrentStepIndex = 0

	for i, step := range dag.GetSteps() {
		cv.Status.CurrentStepIndex = i
		cv.Status.CurrentStepName = step.Name

		// 更新 ComponentVersion 的版本
		for _, compRef := range step.Components {
			comp := &nodecomponentv1alpha1.ComponentVersion{}
			if err := r.Get(ctx, types.NamespacedName{
				Name:      compRef.Name,
				Namespace: cv.Namespace,
			}, comp); err != nil {
				return ctrl.Result{}, errors.Wrapf(err, "failed to get ComponentVersion %s", compRef.Name)
			}

			compPatch, err := patch.NewHelper(comp, r.Client)
			if err != nil {
				return ctrl.Result{}, err
			}

			comp.Spec.Version = compRef.Version
			if err := compPatch.Patch(ctx, comp); err != nil {
				return ctrl.Result{}, errors.Wrapf(err, "failed to update ComponentVersion %s", compRef.Name)
			}

			// 等待 ComponentVersion 完成
			if err := r.waitForComponentPhase(ctx, comp, nodecomponentv1alpha1.CompPhaseReady, 10*time.Minute); err != nil {
				cv.Status.Phase = cvov1beta1.ClusterVersionPhaseFailed
				conditions.MarkFalse(cv, cvov1beta1.ClusterVersionUpgradeCompleted, "ComponentFailed", "Component %s upgrade failed: %v", compRef.Name, err)
				return ctrl.Result{}, err
			}
		}

		// 更新步骤状态
		cv.Status.UpgradeSteps[i].Status = cvov1beta1.UpgradeStepStatusCompleted
	}

	// 5. 升级完成
	cv.Status.CurrentVersion = cv.Spec.DesiredVersion
	cv.Status.CurrentReleaseRef = cv.Spec.ReleaseRef
	cv.Status.Phase = cvov1beta1.ClusterVersionPhaseReady
	cv.Status.History = append(cv.Status.History, cvov1beta1.UpgradeHistory{
		Version:     cv.Spec.DesiredVersion,
		StartedAt:   metav1.Now(),
		CompletedAt: metav1.Now(),
		Status:      cvov1beta1.UpgradeHistoryStatusCompleted,
	})
	conditions.MarkTrue(cv, cvov1beta1.ClusterVersionUpgradeCompleted, "Completed", "Cluster upgrade completed successfully")

	logger.Info("Cluster upgrade completed", "version", cv.Spec.DesiredVersion)
	return ctrl.Result{}, nil
}

// resolveReleaseImage 解析 ReleaseImage
func (r *ClusterVersionReconciler) resolveReleaseImage(ctx context.Context, cv *cvov1beta1.ClusterVersion) (*cvov1beta1.ReleaseImage, error) {
	if cv.Spec.ReleaseRef == nil {
		return nil, errors.New("releaseRef is nil")
	}

	releaseImage := &cvov1beta1.ReleaseImage{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      cv.Spec.ReleaseRef.Name,
		Namespace: cv.Spec.ReleaseRef.Namespace,
	}, releaseImage); err != nil {
		return nil, errors.Wrap(err, "failed to get ReleaseImage")
	}

	return releaseImage, nil
}

// getAllComponentVersions 获取所有 ComponentVersion
func (r *ClusterVersionReconciler) getAllComponentVersions(ctx context.Context, cv *cvov1beta1.ClusterVersion) ([]*nodecomponentv1alpha1.ComponentVersion, error) {
	releaseImage, err := r.resolveReleaseImage(ctx, cv)
	if err != nil {
		return nil, err
	}

	var components []*nodecomponentv1alpha1.ComponentVersion
	for _, compRef := range releaseImage.Spec.ComponentVersions {
		comp := &nodecomponentv1alpha1.ComponentVersion{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      compRef.Name,
			Namespace: cv.Namespace,
		}, comp); err != nil {
			return nil, errors.Wrapf(err, "failed to get ComponentVersion %s", compRef.Name)
		}
		components = append(components, comp)
	}

	// 按 DAG 顺序排序
	dag, err := r.DAGScheduler.BuildDAG(releaseImage)
	if err != nil {
		return nil, err
	}

	sortedComponents := make([]*nodecomponentv1alpha1.ComponentVersion, 0, len(components))
	for _, step := range dag.GetSteps() {
		for _, comp := range components {
			for _, compRef := range step.Components {
				if comp.Name == compRef.Name {
					sortedComponents = append(sortedComponents, comp)
					break
				}
			}
		}
	}

	return sortedComponents, nil
}

// waitForComponentPhase 等待 ComponentVersion 达到指定阶段
func (r *ClusterVersionReconciler) waitForComponentPhase(
	ctx context.Context,
	cv *nodecomponentv1alpha1.ComponentVersion,
	targetPhase nodecomponentv1alpha1.ComponentVersionPhase,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return errors.Errorf("timeout waiting for ComponentVersion %s to reach phase %s", cv.Name, targetPhase)
		case <-ticker.C:
			if err := r.Get(ctx, types.NamespacedName{
				Name:      cv.Name,
				Namespace: cv.Namespace,
			}, cv); err != nil {
				return err
			}
			if cv.Status.Phase == targetPhase {
				return nil
			}
			if cv.Status.Phase == nodecomponentv1alpha1.CompPhaseFailed {
				return errors.Errorf("ComponentVersion %s failed", cv.Name)
			}
		}
	}
}

// SetupWithManager 设置控制器
func (r *ClusterVersionReconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cvov1beta1.ClusterVersion{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(options).
		Watches(
			&cvov1beta1.ReleaseImage{},
			handler.EnqueueRequestsFromMapFunc(r.releaseImageToClusterVersionMapFunc()),
		).
		Watches(
			&nodecomponentv1alpha1.ComponentVersion{},
			handler.EnqueueRequestsFromMapFunc(r.componentVersionToClusterVersionMapFunc()),
		).
		Complete(r)
}

// releaseImageToClusterVersionMapFunc ReleaseImage 到 ClusterVersion 的映射
func (r *ClusterVersionReconciler) releaseImageToClusterVersionMapFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		releaseImage, ok := obj.(*cvov1beta1.ReleaseImage)
		if !ok {
			return nil
		}

		// 查找引用该 ReleaseImage 的所有 ClusterVersion
		cvList := &cvov1beta1.ClusterVersionList{}
		if err := r.List(ctx, cvList, client.MatchingFields{
			"spec.releaseRef.name": releaseImage.Name,
		}); err != nil {
			return nil
		}

		var requests []reconcile.Request
		for _, cv := range cvList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      cv.Name,
					Namespace: cv.Namespace,
				},
			})
		}
		return requests
	}
}

// componentVersionToClusterVersionMapFunc ComponentVersion 到 ClusterVersion 的映射
func (r *ClusterVersionReconciler) componentVersionToClusterVersionMapFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cv, ok := obj.(*nodecomponentv1alpha1.ComponentVersion)
		if !ok {
			return nil
		}

		// 查找该命名空间下的 ClusterVersion
		clusterVersionList := &cvov1beta1.ClusterVersionList{}
		if err := r.List(ctx, clusterVersionList, client.InNamespace(cv.Namespace)); err != nil {
			return nil
		}

		var requests []reconcile.Request
		for _, clusterVersion := range clusterVersionList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      clusterVersion.Name,
					Namespace: clusterVersion.Namespace,
				},
			})
		}
		return requests
	}
}
```
### 2.4 ClusterVersion CRD 定义
```go
// d:\code\github\cluster-api-provider-bke\api\cvo\v1beta1\clusterversion_types.go

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterVersionSpec defines the desired state of ClusterVersion
type ClusterVersionSpec struct {
	// DesiredVersion 是期望的集群版本
	DesiredVersion string `json:"desiredVersion"`

	// ReleaseRef 引用 ReleaseImage
	ReleaseRef *corev1.ObjectReference `json:"releaseRef,omitempty"`

	// ClusterRef 引用 BKECluster
	ClusterRef *corev1.ObjectReference `json:"clusterRef,omitempty"`

	// UpgradeStrategy 定义升级策略
	UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`

	// Pause 暂停调谐
	Pause bool `json:"pause,omitempty"`

	// DryRun 仅验证不执行
	DryRun bool `json:"dryRun,omitempty"`

	// Reset 重置集群
	Reset bool `json:"reset,omitempty"`
}

// UpgradeStrategy 定义升级策略
type UpgradeStrategy struct {
	// Type 升级类型：Rolling/InPlace
	Type UpgradeStrategyType `json:"type,omitempty"`

	// RollingParams 滚动升级参数
	RollingParams *RollingParams `json:"rollingParams,omitempty"`

	// MaxUnavailable 最大不可用节点数
	MaxUnavailable int `json:"maxUnavailable,omitempty"`

	// Timeout 升级超时时间
	Timeout *metav1.Duration `json:"timeout,omitempty"`
}

type UpgradeStrategyType string

const (
	UpgradeStrategyRolling UpgradeStrategyType = "Rolling"
	UpgradeStrategyInPlace UpgradeStrategyType = "InPlace"
)

type RollingParams struct {
	// BatchSize 每批次节点数
	BatchSize int `json:"batchSize,omitempty"`

	// BatchInterval 批次间隔
	BatchInterval *metav1.Duration `json:"batchInterval,omitempty"`

	// MaxSurge 最大激增节点数
	MaxSurge int `json:"maxSurge,omitempty"`
}

// ClusterVersionStatus defines the observed state of ClusterVersion
type ClusterVersionStatus struct {
	// CurrentVersion 当前版本
	CurrentVersion string `json:"currentVersion,omitempty"`

	// CurrentReleaseRef 当前 ReleaseImage 引用
	CurrentReleaseRef *corev1.ObjectReference `json:"currentReleaseRef,omitempty"`

	// Phase 当前阶段
	Phase ClusterVersionPhase `json:"phase,omitempty"`

	// UpgradeSteps 升级步骤
	UpgradeSteps []UpgradeStep `json:"upgradeSteps,omitempty"`

	// CurrentStepIndex 当前步骤索引
	CurrentStepIndex int `json:"currentStepIndex,omitempty"`

	// CurrentStepName 当前步骤名称
	CurrentStepName string `json:"currentStepName,omitempty"`

	// History 升级历史
	History []UpgradeHistory `json:"history,omitempty"`

	// Conditions 条件
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ClusterVersionPhase string

const (
	ClusterVersionPhasePending    ClusterVersionPhase = "Pending"
	ClusterVersionPhaseValidated  ClusterVersionPhase = "Validated"
	ClusterVersionPhasePaused     ClusterVersionPhase = "Paused"
	ClusterVersionPhaseUpgrading  ClusterVersionPhase = "Upgrading"
	ClusterVersionPhaseReady      ClusterVersionPhase = "Ready"
	ClusterVersionPhaseFailed     ClusterVersionPhase = "Failed"
	ClusterVersionPhaseDeleting   ClusterVersionPhase = "Deleting"
)

type UpgradeStep struct {
	// Name 步骤名称
	Name string `json:"name"`

	// Components 该步骤涉及的组件
	Components []ComponentRef `json:"components,omitempty"`

	// Status 步骤状态
	Status UpgradeStepStatus `json:"status,omitempty"`

	// Message 步骤消息
	Message string `json:"message,omitempty"`

	// StartedAt 开始时间
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt 完成时间
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

type UpgradeStepStatus string

const (
	UpgradeStepStatusPending   UpgradeStepStatus = "Pending"
	UpgradeStepStatusRunning   UpgradeStepStatus = "Running"
	UpgradeStepStatusCompleted UpgradeStepStatus = "Completed"
	UpgradeStepStatusFailed    UpgradeStepStatus = "Failed"
	UpgradeStepStatusSkipped   UpgradeStepStatus = "Skipped"
)

type ComponentRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type UpgradeHistory struct {
	Version     string                  `json:"version"`
	StartedAt   metav1.Time             `json:"startedAt"`
	CompletedAt metav1.Time             `json:"completedAt,omitempty"`
	Status      UpgradeHistoryStatus    `json:"status"`
	Message     string                  `json:"message,omitempty"`
}

type UpgradeHistoryStatus string

const (
	UpgradeHistoryStatusCompleted UpgradeHistoryStatus = "Completed"
	UpgradeHistoryStatusFailed    UpgradeHistoryStatus = "Failed"
	UpgradeHistoryStatusPartial   UpgradeHistoryStatus = "Partial"
)

// ClusterVersion Condition Types
const (
	ClusterVersionFinalizerAdded      clusterv1.ConditionType = "FinalizerAdded"
	ClusterVersionPaused              clusterv1.ConditionType = "Paused"
	ClusterVersionValid               clusterv1.ConditionType = "Valid"
	ClusterVersionReleaseResolved     clusterv1.ConditionType = "ReleaseResolved"
	ClusterVersionDAGBuilt            clusterv1.ConditionType = "DAGBuilt"
	ClusterVersionUpgradeCompleted    clusterv1.ConditionType = "UpgradeCompleted"
	ClusterVersionComponentsHealthy   clusterv1.ConditionType = "ComponentsHealthy"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cv
// +kubebuilder:printcolumn:name="DESIRED VERSION",type="string",JSONPath=".spec.desiredVersion"
// +kubebuilder:printcolumn:name="CURRENT VERSION",type="string",JSONPath=".status.currentVersion"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

type ClusterVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterVersionSpec   `json:"spec,omitempty"`
	Status ClusterVersionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type ClusterVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterVersion `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterVersion{}, &ClusterVersionList{})
}
```
### 2.5 DAG Scheduler 实现
```go
// d:\code\github\cluster-api-provider-bke\pkg\cvo\dag_scheduler\dag_scheduler.go

package dag_scheduler

import (
	"fmt"
	"sort"

	"github.com/pkg/errors"

	cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
	nodecomponentv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/nodecomponent/v1alpha1"
)

type DAGScheduler struct{}

type DAG struct {
	steps []*DAGStep
}

type DAGStep struct {
	Name       string
	Components []cvov1beta1.ComponentRef
	DependsOn  []string
}

func NewDAGScheduler() *DAGScheduler {
	return &DAGScheduler{}
}

// BuildDAG 根据组件依赖关系构建 DAG
func (s *DAGScheduler) BuildDAG(releaseImage *cvov1beta1.ReleaseImage) (*DAG, error) {
	// 1. 构建组件依赖图
	componentDeps := make(map[string][]string)
	componentVersions := make(map[string]cvov1beta1.ComponentVersionRef)

	for _, compRef := range releaseImage.Spec.ComponentVersions {
		componentVersions[compRef.Name] = compRef
		componentDeps[compRef.Name] = compRef.Dependencies
	}

	// 2. 拓扑排序
	sorted, err := s.topologicalSort(componentDeps)
	if err != nil {
		return nil, errors.Wrap(err, "failed to sort components by dependencies")
	}

	// 3. 构建 DAG Steps
	dag := &DAG{}
	for _, compName := range sorted {
		compRef := componentVersions[compName]
		step := &DAGStep{
			Name: compName,
			Components: []cvov1beta1.ComponentRef{
				{
					Name:    compRef.Name,
					Version: compRef.Version,
				},
			},
			DependsOn: compRef.Dependencies,
		}
		dag.steps = append(dag.steps, step)
	}

	return dag, nil
}

// topologicalSort 拓扑排序
func (s *DAGScheduler) topologicalSort(deps map[string][]string) ([]string, error) {
	visited := make(map[string]bool)
	visiting := make(map[string]bool)
	var result []string

	var visit func(string) error
	visit = func(node string) error {
		if visited[node] {
			return nil
		}
		if visiting[node] {
			return errors.Errorf("circular dependency detected at node %s", node)
		}

		visiting[node] = true
		for _, dep := range deps[node] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[node] = false
		visited[node] = true
		result = append(result, node)
		return nil
	}

	for node := range deps {
		if err := visit(node); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// ValidateDAG 验证 DAG 是否有效
func (s *DAGScheduler) ValidateDAG(releaseImage *cvov1beta1.ReleaseImage) error {
	_, err := s.BuildDAG(releaseImage)
	return err
}

// GetSteps 获取 DAG 步骤
func (d *DAG) GetSteps() []cvov1beta1.UpgradeStep {
	var steps []cvov1beta1.UpgradeStep
	for _, step := range d.steps {
		steps = append(steps, cvov1beta1.UpgradeStep{
			Name:       step.Name,
			Components: step.Components,
			Status:     cvov1beta1.UpgradeStepStatusPending,
		})
	}
	return steps
}
```
## 三、设计总结
### 3.1 BKEClusterReconciler 改造要点
| 改造点 | 说明 |
|--------|------|
| **Feature Gate 分流** | 通过 `DeclarativeVersionOrchestration` Feature Gate 决定使用 PhaseFlow 还是 ClusterVersion 编排 |
| **创建 ClusterVersion** | 在集群初始化时自动创建对应的 ClusterVersion CR，OwnerReference 指向 BKECluster |
| **同步 Spec** | 将 BKECluster.Spec 中的版本信息同步到 ClusterVersion.Spec |
| **同步 Status** | 将 ClusterVersion.Status 同步回 BKECluster.Status |
| **Watch ClusterVersion** | 监听 ClusterVersion 状态变化，触发 BKECluster 调谐 |
| **保留旧路径** | PhaseFlow 路径完全保留，确保向后兼容 |
### 3.2 ClusterVersion Controller 核心职责
| 职责 | 说明 |
|------|------|
| **Finalizer 管理** | 在 Reconcile 开始时添加 Finalizer，删除时按逆序执行各组件 uninstallAction |
| **Pause 控制** | 暂停时停止所有 ComponentVersion 的调谐 |
| **DryRun 验证** | 验证 ReleaseImage 和 ComponentVersion 存在性，验证 DAG 无循环依赖 |
| **Reset 重置** | 本质是删除后重建，调用删除逻辑 |
| **版本编排** | 检测 desiredVersion 变化 → 解析 ReleaseImage → 构建 DAG → 逐组件升级 |
| **DAG 调度** | 根据组件依赖关系构建 DAG，按拓扑序执行升级 |
| **历史管理** | 维护版本历史，支持回滚 |
### 3.3 关键设计决策
1. **渐进式迁移**：通过 Feature Gate 实现新旧路径并存，确保平滑过渡
2. **OwnerReference 关联**：ClusterVersion 的 OwnerReference 指向 BKECluster，确保生命周期一致
3. **状态同步机制**：BKECluster 与 ClusterVersion 双向同步，保持状态一致性
4. **DAG 拓扑排序**：确保组件按依赖关系正确升级，避免循环依赖
5. **逆序删除**：删除时按 DAG 逆序执行 uninstallAction，确保依赖关系正确

这个设计确保了：
- **向后兼容**：Feature Gate 关闭时，现有 PhaseFlow 完全不受影响
- **渐进迁移**：可以逐步将 Phase 迁移到 ComponentVersion YAML 声明
- **架构清晰**：BKEClusterReconciler 专注于 BKECluster 生命周期管理，ClusterVersion Controller 专注于版本编排
- **可扩展性**：新增组件只需添加 ComponentVersion YAML，无需修改代码

# ReleaseImage Controller 的设计思路与代码实现
## 一、ReleaseImage Controller 设计思路
### 1.1 核心定位
ReleaseImage 是**发布版本清单**，是整个声明式版本管理的"数据源头"。借鉴 OpenShift CVO 的 Release Payload 概念，但使用 CRD 替代容器镜像载体。
```
┌──────────────────────────────────────────────────────────────┐
│                     ReleaseImage Controller                  │
│                                                              │
│  核心职责：                                                  │
│  1. 不可变性校验 — 创建后 Spec 不可修改                      │
│  2. 组件引用验证 — 确保所有 ComponentVersion 引用有效        │
│  3. 升级路径校验 — 验证 upgradePaths 的合法性                │
│  4. 兼容性校验 — 验证兼容性矩阵的完整性                      │
│  5. 镜像清单验证 — 确保所有镜像在仓库中存在                  │
│  6. DAG 依赖验证 — 确保组件依赖无循环                        │
│  7. 自动创建 ComponentVersion — 确保 ComponentVersion CR 存在│
│  8. 状态上报 — 维护 ReleaseImageStatus                       │
└──────────────────────────────────────────────────────────────┘
```
### 1.2 ReleaseImage 在架构中的角色
```
BKECluster ──→ ClusterVersion ──→ ReleaseImage ──→ ComponentVersion
                  (编排)            (清单)            (执行)
```
| 角色 | 说明 |
|------|------|
| **数据源头** | 定义某个版本包含哪些组件及版本号 |
| **不可变快照** | 创建后不可修改，确保版本可追溯 |
| **升级路径定义** | 定义哪些版本可以升级到当前版本 |
| **兼容性约束** | 定义最低/最高兼容的 K8s/openFuyao 版本 |
| **组件引用解析** | ClusterVersion Controller 通过 ReleaseImage 找到所有 ComponentVersion |
### 1.3 关键设计决策
**1. 不可变性**：ReleaseImage 创建后 Spec 不可修改。这是借鉴 OpenShift 的核心设计——版本清单一旦发布就不应变化，确保升级的可追溯性和一致性。实现方式：
- 通过 ValidatingWebhook 拦截 Spec 修改请求
- Controller 端也做防御性检查，如果检测到 Spec 变更则标记为 Invalid

**2. 组件引用验证**：ReleaseImage 引用的所有 ComponentVersion 必须存在且可用。Controller 在创建/更新时验证引用完整性，将结果记录到 Status。

**3. 自动创建 ComponentVersion**：当 ReleaseImage 引用的 ComponentVersion 不存在时，Controller 可以从内嵌的 ComponentVersion 模板自动创建 CR，确保引用链完整。

**4. 离线支持**：ReleaseImage 的 `images` 字段列出所有需要的容器镜像，Controller 验证这些镜像在目标仓库中存在（可选，通过 Feature Gate 控制）。
## 二、代码实现
### 2.1 ReleaseImage CRD 完整定义
```go
// d:\code\github\cluster-api-provider-bke\api\cvo\v1beta1\releaseimage_types.go

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ReleaseImageSpec struct {
	Version string `json:"version"`

	DisplayName string `json:"displayName,omitempty"`

	Description string `json:"description,omitempty"`

	ReleaseTime *metav1.Time `json:"releaseTime,omitempty"`

	Components []ReleaseComponent `json:"components"`

	Images []ImageManifest `json:"images,omitempty"`

	Compatibility *ReleaseCompatibility `json:"compatibility,omitempty"`

	UpgradePaths []UpgradePath `json:"upgradePaths,omitempty"`
}

type ReleaseComponent struct {
	ComponentName ComponentName `json:"componentName"`

	Version string `json:"version"`

	ComponentVersionRef *ComponentVersionReference `json:"componentVersionRef,omitempty"`

	Mandatory bool `json:"mandatory,omitempty"`

	Dependencies []ComponentName `json:"dependencies,omitempty"`
}

type ComponentVersionReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type ImageManifest struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Digest  string `json:"digest,omitempty"`
}

type ReleaseCompatibility struct {
	MinKubernetesVersion string `json:"minKubernetesVersion,omitempty"`
	MaxKubernetesVersion string `json:"maxKubernetesVersion,omitempty"`
	MinOpenFuyaoVersion  string `json:"minOpenFuyaoVersion,omitempty"`
	OSRequirements       []OSRequirement `json:"osRequirements,omitempty"`
}

type OSRequirement struct {
	OSType    string `json:"osType,omitempty"`
	MinVersion string `json:"minVersion,omitempty"`
}

type UpgradePath struct {
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	Blocked     bool   `json:"blocked,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type ReleaseImageStatus struct {
	Phase ReleaseImagePhase `json:"phase,omitempty"`

	ValidatedComponents []ValidatedComponent `json:"validatedComponents,omitempty"`

	ValidationErrors []string `json:"validationErrors,omitempty"`

	ReferencedBy []ReleaseImageReference `json:"referencedBy,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ReleaseImagePhase string

const (
	ReleaseImageProcessing ReleaseImagePhase = "Processing"
	ReleaseImageValid      ReleaseImagePhase = "Valid"
	ReleaseImageInvalid    ReleaseImagePhase = "Invalid"
)

type ValidatedComponent struct {
	ComponentName ComponentName `json:"componentName"`
	Version       string        `json:"version"`
	Available     bool          `json:"available"`
	Message       string        `json:"message,omitempty"`
}

type ReleaseImageReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ri
// +kubebuilder:printcolumn:name="VERSION",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="COMPONENTS",type="integer",JSONPath=".spec.components"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

type ReleaseImage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReleaseImageSpec   `json:"spec,omitempty"`
	Status ReleaseImageStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type ReleaseImageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReleaseImage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReleaseImage{}, &ReleaseImageList{})
}
```
### 2.2 ReleaseImage Controller 实现
```go
// d:\code\github\cluster-api-provider-bke\controllers\cvo\releaseimage_controller.go

package cvo

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
	nodecomponentv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/nodecomponent/v1alpha1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/dag_scheduler"
)

const (
	releaseImageFinalizer = "releaseimage.cvo.openfuyao.cn/finalizer"
)

type ReleaseImageReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
	DAGScheduler *dag_scheduler.DAGScheduler
}

func (r *ReleaseImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ri := &cvov1beta1.ReleaseImage{}
	if err := r.Get(ctx, req.NamespacedName, ri); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(ri, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, ri); err != nil {
			logger.Error(err, "failed to patch ReleaseImage")
		}
	}()

	// ===== 1. 处理删除 =====
	if !ri.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, ri)
	}

	// ===== 2. 不可变性校验 =====
	if result, err := r.ensureImmutability(ctx, ri); err != nil || result.Requeue {
		return result, err
	}

	// ===== 3. 验证组件引用 =====
	validationResult, err := r.validateComponentReferences(ctx, ri)
	if err != nil {
		ri.Status.Phase = cvov1beta1.ReleaseImageInvalid
		conditions.MarkFalse(ri, cvov1beta1.ReleaseImageComponentsValid, "ValidationError",
			"Component validation failed: %v", err)
		return ctrl.Result{}, err
	}
	ri.Status.ValidatedComponents = validationResult.ValidatedComponents
	ri.Status.ValidationErrors = validationResult.Errors

	if len(validationResult.Errors) > 0 {
		ri.Status.Phase = cvov1beta1.ReleaseImageInvalid
		conditions.MarkFalse(ri, cvov1beta1.ReleaseImageComponentsValid, "InvalidComponents",
			"Found %d validation errors", len(validationResult.Errors))
		return ctrl.Result{}, nil
	}

	// ===== 4. 验证 DAG 依赖 =====
	if err := r.validateDAGDependencies(ctx, ri); err != nil {
		ri.Status.Phase = cvov1beta1.ReleaseImageInvalid
		conditions.MarkFalse(ri, cvov1beta1.ReleaseImageDAGValid, "InvalidDAG",
			"DAG validation failed: %v", err)
		return ctrl.Result{}, err
	}
	conditions.MarkTrue(ri, cvov1beta1.ReleaseImageDAGValid, "ValidDAG",
		"DAG validation passed")

	// ===== 5. 验证升级路径 =====
	if err := r.validateUpgradePaths(ctx, ri); err != nil {
		ri.Status.Phase = cvov1beta1.ReleaseImageInvalid
		conditions.MarkFalse(ri, cvov1beta1.ReleaseImageUpgradePathsValid, "InvalidUpgradePaths",
			"Upgrade path validation failed: %v", err)
		return ctrl.Result{}, err
	}
	conditions.MarkTrue(ri, cvov1beta1.ReleaseImageUpgradePathsValid, "ValidUpgradePaths",
		"Upgrade path validation passed")

	// ===== 6. 确保引用的 ComponentVersion 存在 =====
	if err := r.ensureComponentVersionsExist(ctx, ri); err != nil {
		ri.Status.Phase = cvov1beta1.ReleaseImageInvalid
		conditions.MarkFalse(ri, cvov1beta1.ReleaseImageComponentsAvailable, "ComponentVersionsMissing",
			"Failed to ensure ComponentVersions exist: %v", err)
		return ctrl.Result{}, err
	}
	conditions.MarkTrue(ri, cvov1beta1.ReleaseImageComponentsAvailable, "ComponentsAvailable",
		"All referenced ComponentVersions are available")

	// ===== 7. 更新引用关系 =====
	if err := r.updateReferencedBy(ctx, ri); err != nil {
		logger.Error(err, "failed to update referencedBy")
	}

	// ===== 8. 标记为 Valid =====
	ri.Status.Phase = cvov1beta1.ReleaseImageValid
	conditions.MarkTrue(ri, cvov1beta1.ReleaseImageComponentsValid, "ComponentsValid",
		"All %d components validated successfully", len(ri.Spec.Components))

	return ctrl.Result{}, nil
}

// ensureImmutability 确保 ReleaseImage Spec 不可变
func (r *ReleaseImageReconciler) ensureImmutability(ctx context.Context, ri *cvov1beta1.ReleaseImage) (ctrl.Result, error) {
	if ri.Status.Phase == "" || ri.Status.Phase == cvov1beta1.ReleaseImageProcessing {
		return ctrl.Result{}, nil
	}

	// 检查 Spec 是否被修改（通过 Annotation 记录原始 Spec 的 hash）
	originalHash := ri.Annotations["cvo.openfuyao.cn/spec-hash"]
	if originalHash == "" {
		// 首次创建，记录 Spec hash
		if ri.Annotations == nil {
			ri.Annotations = make(map[string]string)
		}
		specHash := computeSpecHash(ri.Spec)
		ri.Annotations["cvo.openfuyao.cn/spec-hash"] = specHash
		return ctrl.Result{Requeue: true}, nil
	}

	currentHash := computeSpecHash(ri.Spec)
	if currentHash != originalHash {
		// Spec 被修改，标记为 Invalid
		ri.Status.Phase = cvov1beta1.ReleaseImageInvalid
		ri.Status.ValidationErrors = append(ri.Status.ValidationErrors,
			"ReleaseImage spec is immutable after creation, but spec has been modified")
		conditions.MarkFalse(ri, cvov1beta1.ReleaseImageImmutable, "SpecModified",
			"ReleaseImage spec was modified after creation")
		r.Recorder.Eventf(ri, "Warning", "SpecModified",
			"ReleaseImage spec is immutable, but spec has been modified")
		return ctrl.Result{}, errors.New("releaseImage spec is immutable")
	}

	conditions.MarkTrue(ri, cvov1beta1.ReleaseImageImmutable, "Immutable",
		"ReleaseImage spec has not been modified")
	return ctrl.Result{}, nil
}

// validateComponentReferences 验证所有组件引用
func (r *ReleaseImageReconciler) validateComponentReferences(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
) (*ValidationResult, error) {
	result := &ValidationResult{}

	for _, comp := range ri.Spec.Components {
		validated := cvov1beta1.ValidatedComponent{
			ComponentName: comp.ComponentName,
			Version:       comp.Version,
		}

		// 查找 ComponentVersion
		cv, err := r.findComponentVersion(ctx, ri, comp)
		if err != nil {
			validated.Available = false
			validated.Message = fmt.Sprintf("ComponentVersion not found: %v", err)
			result.Errors = append(result.Errors,
				fmt.Sprintf("component %s version %s: %v", comp.ComponentName, comp.Version, err))
		} else {
			validated.Available = true
			validated.Message = "ComponentVersion found and available"

			// 验证 ComponentVersion 的版本是否匹配
			if cv.Spec.Version != comp.Version {
				validated.Available = false
				validated.Message = fmt.Sprintf(
					"ComponentVersion version mismatch: expected %s, got %s",
					comp.Version, cv.Spec.Version)
				result.Errors = append(result.Errors,
					fmt.Sprintf("component %s: version mismatch (expected %s, got %s)",
						comp.ComponentName, comp.Version, cv.Spec.Version))
			}

			// 验证 ComponentVersion 的 componentName 是否匹配
			if cv.Spec.ComponentName != comp.ComponentName {
				validated.Available = false
				validated.Message = fmt.Sprintf(
					"ComponentVersion componentName mismatch: expected %s, got %s",
					comp.ComponentName, cv.Spec.ComponentName)
				result.Errors = append(result.Errors,
					fmt.Sprintf("component: componentName mismatch (expected %s, got %s)",
						comp.ComponentName, cv.Spec.ComponentName))
			}
		}

		result.ValidatedComponents = append(result.ValidatedComponents, validated)
	}

	return result, nil
}

// findComponentVersion 查找 ComponentVersion
func (r *ReleaseImageReconciler) findComponentVersion(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
	comp cvov1beta1.ReleaseComponent,
) (*nodecomponentv1alpha1.ComponentVersion, error) {
	// 优先使用显式引用
	if comp.ComponentVersionRef != nil {
		cv := &nodecomponentv1alpha1.ComponentVersion{}
		ns := comp.ComponentVersionRef.Namespace
		if ns == "" {
			ns = ri.Namespace
		}
		err := r.Get(ctx, types.NamespacedName{
			Name:      comp.ComponentVersionRef.Name,
			Namespace: ns,
		}, cv)
		return cv, err
	}

	// 按命名约定查找：{componentName}-{version}
	cv := &nodecomponentv1alpha1.ComponentVersion{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-%s", comp.ComponentName, comp.Version),
		Namespace: ri.Namespace,
	}, cv)
	return cv, err
}

// validateDAGDependencies 验证 DAG 依赖无循环
func (r *ReleaseImageReconciler) validateDAGDependencies(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
) error {
	// 构建组件依赖图
	deps := make(map[string][]string)
	for _, comp := range ri.Spec.Components {
		deps[string(comp.ComponentName)] = comp.Dependencies
	}

	// 拓扑排序检测循环依赖
	visited := make(map[string]bool)
	visiting := make(map[string]bool)

	var visit func(string) error
	visit = func(node string) error {
		if visited[node] {
			return nil
		}
		if visiting[node] {
			return errors.Errorf("circular dependency detected at component %s", node)
		}

		visiting[node] = true
		for _, dep := range deps[node] {
			if _, exists := deps[dep]; !exists {
				return errors.Errorf("component %s depends on %s, but %s is not in the release", node, dep, dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[node] = false
		visited[node] = true
		return nil
	}

	for node := range deps {
		if err := visit(node); err != nil {
			return err
		}
	}

	return nil
}

// validateUpgradePaths 验证升级路径
func (r *ReleaseImageReconciler) validateUpgradePaths(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
) error {
	for _, path := range ri.Spec.UpgradePaths {
		if path.ToVersion != ri.Spec.Version && !path.Blocked {
			return errors.Errorf(
				"upgrade path from %s to %s does not match ReleaseImage version %s",
				path.FromVersion, path.ToVersion, ri.Spec.Version)
		}

		// 验证源版本对应的 ReleaseImage 是否存在（可选）
		if path.FromVersion != "" && !path.Blocked {
			sourceRI := &cvov1beta1.ReleaseImage{}
			err := r.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("release-%s", path.FromVersion),
				Namespace: ri.Namespace,
			}, sourceRI)
			if err != nil && !apierrors.IsNotFound(err) {
				return errors.Wrapf(err, "failed to check source ReleaseImage for upgrade path %s→%s",
					path.FromVersion, path.ToVersion)
			}
		}
	}
	return nil
}

// ensureComponentVersionsExist 确保引用的 ComponentVersion 存在
func (r *ReleaseImageReconciler) ensureComponentVersionsExist(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
) error {
	for _, comp := range ri.Spec.Components {
		_, err := r.findComponentVersion(ctx, ri, comp)
		if apierrors.IsNotFound(err) {
			// ComponentVersion 不存在，尝试创建
			if err := r.createComponentVersion(ctx, ri, comp); err != nil {
				return errors.Wrapf(err, "failed to create ComponentVersion for %s", comp.ComponentName)
			}
		} else if err != nil {
			return errors.Wrapf(err, "failed to check ComponentVersion for %s", comp.ComponentName)
		}
	}
	return nil
}

// createComponentVersion 创建 ComponentVersion CR
func (r *ReleaseImageReconciler) createComponentVersion(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
	comp cvov1beta1.ReleaseComponent,
) error {
	cvName := fmt.Sprintf("%s-%s", comp.ComponentName, comp.Version)
	if comp.ComponentVersionRef != nil {
		cvName = comp.ComponentVersionRef.Name
	}

	cv := &nodecomponentv1alpha1.ComponentVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cvName,
			Namespace: ri.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: cvov1beta1.GroupVersion.String(),
					Kind:       "ReleaseImage",
					Name:       ri.Name,
					UID:        ri.UID,
				},
			},
		},
		Spec: nodecomponentv1alpha1.ComponentVersionSpec{
			ComponentName: comp.ComponentName,
			Version:       comp.Version,
		},
	}

	if err := r.Create(ctx, cv); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}

	r.Recorder.Eventf(ri, "Normal", "ComponentVersionCreated",
		"Created ComponentVersion %s for component %s version %s",
		cvName, comp.ComponentName, comp.Version)
	return nil
}

// updateReferencedBy 更新引用关系
func (r *ReleaseImageReconciler) updateReferencedBy(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
) error {
	// 查找引用该 ReleaseImage 的所有 ClusterVersion
	cvList := &cvov1beta1.ClusterVersionList{}
	if err := r.List(ctx, cvList, client.InNamespace(ri.Namespace)); err != nil {
		return err
	}

	var refs []cvov1beta1.ReleaseImageReference
	for _, cv := range cvList.Items {
		if cv.Spec.ReleaseRef != nil && cv.Spec.ReleaseRef.Name == ri.Name {
			refs = append(refs, cvov1beta1.ReleaseImageReference{
				Name:      cv.Name,
				Namespace: cv.Namespace,
			})
		}
	}

	ri.Status.ReferencedBy = refs
	return nil
}

// reconcileDelete 处理删除
func (r *ReleaseImageReconciler) reconcileDelete(
	ctx context.Context,
	ri *cvov1beta1.ReleaseImage,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 检查是否仍被 ClusterVersion 引用
	if len(ri.Status.ReferencedBy) > 0 {
		logger.Info("ReleaseImage is still referenced by ClusterVersion, cannot delete",
			"referencedBy", ri.Status.ReferencedBy)
		r.Recorder.Eventf(ri, "Warning", "ReferencedByClusterVersion",
			"Cannot delete ReleaseImage: still referenced by %d ClusterVersion(s)",
			len(ri.Status.ReferencedBy))
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// 移除 Finalizer
	return ctrl.Result{}, nil
}

// SetupWithManager 设置控制器
func (r *ReleaseImageReconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cvov1beta1.ReleaseImage{}, builder.WithPredicates(
			predicate.Or(
				predicate.GenerationChangedPredicate{},
				predicate.AnnotationChangedPredicate{},
			),
		)).
		WithOptions(options).
		Watches(
			&cvov1beta1.ClusterVersion{},
			handler.EnqueueRequestsFromMapFunc(r.clusterVersionToReleaseImageMapFunc()),
		).
		Watches(
			&nodecomponentv1alpha1.ComponentVersion{},
			handler.EnqueueRequestsFromMapFunc(r.componentVersionToReleaseImageMapFunc()),
		).
		Complete(r)
}

// clusterVersionToReleaseImageMapFunc ClusterVersion 变更触发 ReleaseImage 调谐
func (r *ReleaseImageReconciler) clusterVersionToReleaseImageMapFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cv, ok := obj.(*cvov1beta1.ClusterVersion)
		if !ok {
			return nil
		}

		if cv.Spec.ReleaseRef == nil {
			return nil
		}

		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Name:      cv.Spec.ReleaseRef.Name,
				Namespace: cv.Namespace,
			},
		}}
	}
}

// componentVersionToReleaseImageMapFunc ComponentVersion 变更触发 ReleaseImage 调谐
func (r *ReleaseImageReconciler) componentVersionToReleaseImageMapFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cv, ok := obj.(*nodecomponentv1alpha1.ComponentVersion)
		if !ok {
			return nil
		}

		// 查找引用该 ComponentVersion 的所有 ReleaseImage
		riList := &cvov1beta1.ReleaseImageList{}
		if err := r.List(ctx, riList, client.InNamespace(cv.Namespace)); err != nil {
			return nil
		}

		var requests []reconcile.Request
		for _, ri := range riList.Items {
			for _, comp := range ri.Spec.Components {
				if comp.ComponentName == cv.Spec.ComponentName {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      ri.Name,
							Namespace: ri.Namespace,
						},
					})
					break
				}
			}
		}
		return requests
	}
}

// ValidationResult 验证结果
type ValidationResult struct {
	ValidatedComponents []cvov1beta1.ValidatedComponent
	Errors              []string
}

// computeSpecHash 计算 Spec 的 hash（用于不可变性校验）
func computeSpecHash(spec cvov1beta1.ReleaseImageSpec) string {
	h := fnv.New32a()
	h.Write([]byte(spec.Version))
	for _, comp := range spec.Components {
		h.Write([]byte(string(comp.ComponentName)))
		h.Write([]byte(comp.Version))
	}
	return fmt.Sprintf("%x", h.Sum32())
}
```
### 2.3 ValidatingWebhook 实现（不可变性校验）
```go
// d:\code\github\cluster-api-provider-bke\api\cvo\v1beta1\releaseimage_webhook.go

package v1beta1

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (r *ReleaseImage) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-cvo-openfuyao-cn-v1beta1-releaseimage,mutating=false,failurePolicy=fail,sideEffects=None,groups=cvo.openfuyao.cn,resources=releaseimages,verbs=create;update,versions=v1beta1,name=vreleaseimage.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &ReleaseImage{}

func (r *ReleaseImage) ValidateCreate() (admission.Warnings, error) {
	var allErrs field.ErrorList

	if r.Spec.Version == "" {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "version"),
			r.Spec.Version,
			"version is required",
		))
	}

	if len(r.Spec.Components) == 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "components"),
			r.Spec.Components,
			"at least one component is required",
		))
	}

	// 验证组件名称唯一性
	componentNames := make(map[ComponentName]bool)
	for i, comp := range r.Spec.Components {
		if comp.ComponentName == "" {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "components").Index(i).Child("componentName"),
				comp.ComponentName,
				"componentName is required",
			))
		}
		if componentNames[comp.ComponentName] {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "components").Index(i).Child("componentName"),
				comp.ComponentName,
				fmt.Sprintf("duplicate componentName: %s", comp.ComponentName),
			))
		}
		componentNames[comp.ComponentName] = true

		if comp.Version == "" {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "components").Index(i).Child("version"),
				comp.Version,
				"version is required",
			))
		}
	}

	// 验证升级路径的 toVersion 必须匹配当前版本
	for i, path := range r.Spec.UpgradePaths {
		if path.ToVersion != r.Spec.Version && !path.Blocked {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "upgradePaths").Index(i).Child("toVersion"),
				path.ToVersion,
				fmt.Sprintf("toVersion must match ReleaseImage version %s", r.Spec.Version),
			))
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(r.GroupVersionKind().GroupKind(), r.Name, allErrs)
	}
	return nil, nil
}

func (r *ReleaseImage) ValidateUpdate(old runtime.Object) error {
	oldRI, ok := old.(*ReleaseImage)
	if !ok {
		return apierrors.NewBadRequest("expected old object to be ReleaseImage")
	}

	// 不可变性校验：Spec 创建后不可修改
	if oldRI.Status.Phase != "" && oldRI.Status.Phase != ReleaseImageProcessing {
		if !releaseImageSpecEqual(r.Spec, oldRI.Spec) {
			return apierrors.NewInvalid(
				r.GroupVersionKind().GroupKind(),
				r.Name,
				field.ErrorList{
					field.Invalid(
						field.NewPath("spec"),
						r.Spec,
						"ReleaseImage spec is immutable after creation",
					),
				},
			)
		}
	}

	return nil
}

func (r *ReleaseImage) ValidateDelete() (admission.Warnings, error) {
	return nil, nil
}

func releaseImageSpecEqual(a, b ReleaseImageSpec) bool {
	if a.Version != b.Version {
		return false
	}
	if a.DisplayName != b.DisplayName {
		return false
	}
	if len(a.Components) != len(b.Components) {
		return false
	}
	for i := range a.Components {
		if a.Components[i].ComponentName != b.Components[i].ComponentName {
			return false
		}
		if a.Components[i].Version != b.Components[i].Version {
			return false
		}
	}
	if len(a.UpgradePaths) != len(b.UpgradePaths) {
		return false
	}
	return true
}
```
### 2.4 ReleaseImage YAML 示例
```yaml
# d:\code\github\cluster-api-provider-bke\config\releases\release-v2.6.0.yaml

apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: release-v2.6.0
  namespace: cluster-system
  annotations:
    cvo.openfuyao.cn/spec-hash: "a1b2c3d4"
spec:
  version: v2.6.0
  displayName: "openFuyao v2.6.0"
  description: "openFuyao 2026 Q1 Release"
  releaseTime: "2026-03-01T00:00:00Z"

  components:
    - componentName: bkeAgent
      version: v1.0.0
      dependencies: []
      mandatory: true
    - componentName: nodesEnv
      version: v1.0.0
      dependencies: [bkeAgent]
      mandatory: true
    - componentName: clusterAPI
      version: v1.0.0
      dependencies: [bkeAgent]
      mandatory: true
    - componentName: certs
      version: v1.0.0
      dependencies: [clusterAPI]
      mandatory: true
    - componentName: loadBalancer
      version: v1.0.0
      dependencies: [certs]
      mandatory: true
    - componentName: containerd
      version: v1.7.2
      dependencies: [nodesEnv]
      mandatory: true
    - componentName: etcd
      version: v3.5.12
      dependencies: [nodesEnv]
      mandatory: true
    - componentName: kubernetes
      version: v1.29.0
      dependencies: [containerd, etcd, loadBalancer]
      mandatory: true
    - componentName: addon
      version: v1.2.0
      dependencies: [kubernetes]
      mandatory: true
    - componentName: nodesPostProcess
      version: v1.0.0
      dependencies: [addon]
      mandatory: false
    - componentName: agentSwitch
      version: v1.0.0
      dependencies: [nodesPostProcess]
      mandatory: true
    - componentName: bkeProvider
      version: v1.1.0
      dependencies: []
      mandatory: true
    - componentName: openFuyao
      version: v2.6.0
      dependencies: [kubernetes]
      mandatory: true
    - componentName: clusterManage
      version: v1.0.0
      dependencies: []
      mandatory: false
    - componentName: nodeDelete
      version: v1.0.0
      dependencies: []
      mandatory: false
    - componentName: clusterHealth
      version: v1.0.0
      dependencies: [kubernetes, addon, openFuyao]
      mandatory: false

  images:
    - name: etcd
      image: repo.openfuyao.cn/etcd:v3.5.12
      digest: "sha256:abc123"
    - name: kube-apiserver
      image: repo.openfuyao.cn/kube-apiserver:v1.29.0
      digest: "sha256:def456"
    - name: kube-controller-manager
      image: repo.openfuyao.cn/kube-controller-manager:v1.29.0
      digest: "sha256:ghi789"
    - name: kube-scheduler
      image: repo.openfuyao.cn/kube-scheduler:v1.29.0
      digest: "sha256:jkl012"
    - name: kube-proxy
      image: repo.openfuyao.cn/kube-proxy:v1.29.0
      digest: "sha256:mno345"
    - name: coredns
      image: repo.openfuyao.cn/coredns:1.9.3
      digest: "sha256:pqr678"
    - name: calico-node
      image: repo.openfuyao.cn/calico-node:v3.26.0
      digest: "sha256:stu901"
    - name: openfuyao-controller
      image: repo.openfuyao.cn/openfuyao-controller:v2.6.0
      digest: "sha256:vwx234"
    - name: bke-controller
      image: repo.openfuyao.cn/cluster-api-provider-bke:v1.1.0
      digest: "sha256:yzA567"

  compatibility:
    minKubernetesVersion: "v1.27.0"
    maxKubernetesVersion: "v1.30.0"
    minOpenFuyaoVersion: "v2.4.0"
    osRequirements:
      - osType: "kylin"
        minVersion: "V10"
      - osType: "centos"
        minVersion: "7.9"

  upgradePaths:
    - fromVersion: v2.4.0
      toVersion: v2.6.0
    - fromVersion: v2.5.0
      toVersion: v2.6.0
    - fromVersion: v2.3.0
      toVersion: v2.6.0
      blocked: true
      reason: "v2.3.0 must upgrade to v2.4.0 first, direct upgrade to v2.6.0 is not supported"
```
## 三、设计总结
### 3.1 ReleaseImage Controller 核心职责
| 职责 | 说明 | 实现方式 |
|------|------|---------|
| **不可变性校验** | 创建后 Spec 不可修改 | Spec Hash + ValidatingWebhook 双重保障 |
| **组件引用验证** | 确保所有 ComponentVersion 引用有效 | 逐组件查找 ComponentVersion CR |
| **DAG 依赖验证** | 确保组件依赖无循环 | 拓扑排序 + 循环检测 |
| **升级路径校验** | 验证 upgradePaths 的合法性 | toVersion 必须匹配当前版本 |
| **兼容性校验** | 验证兼容性矩阵完整性 | 检查 minKubernetesVersion 等字段 |
| **自动创建 ComponentVersion** | 确保引用链完整 | OwnerReference 关联 |
| **引用关系维护** | 记录哪些 ClusterVersion 引用了自己 | Status.referencedBy |
| **删除保护** | 被 ClusterVersion 引用时不可删除 | 检查 referencedBy 列表 |
### 3.2 ReleaseImage 与其他 Controller 的交互
```
┌─────────────────────────────────────────────────────────────┐
│                    Controller 交互关系                      │
│                                                             │
│  BKEClusterReconciler                                       │
│    └── 创建 ClusterVersion                                  │
│          │                                                  │
│          ▼                                                  │
│  ClusterVersion Controller                                  │
│    ├── 解析 ReleaseImage → 获取组件列表                     │
│    ├── 按 DAG 顺序更新 ComponentVersion.spec.version        │
│    └── Watch ReleaseImage 状态变化                          │
│          │                                                  │
│          ▼                                                  │
│  ReleaseImage Controller                                    │
│    ├── 验证组件引用完整性                                   │
│    ├── 验证 DAG 依赖无循环                                  │
│    ├── 自动创建缺失的 ComponentVersion                      │
│    ├── Watch ComponentVersion 状态变化                      │
│    └── Watch ClusterVersion 引用变化                        │
│          │                                                  │
│          ▼                                                  │
│  ComponentVersion Controller                                │
│    └── 执行 ActionEngine 驱动组件生命周期                   │
└─────────────────────────────────────────────────────────────┘
```
### 3.3 关键设计决策
1. **不可变性的双重保障**：
   - **ValidatingWebhook**：在 API 层拦截 Spec 修改请求，返回 403
   - **Controller 端**：通过 Spec Hash 检测变更，标记为 Invalid 并记录事件
   - 这确保即使 Webhook 未就绪，Controller 也能检测到非法修改
2. **组件引用解析策略**：
   - **显式引用**：`componentVersionRef.name` 直接指定 ComponentVersion 名称
   - **隐式引用**：按命名约定 `{componentName}-{version}` 查找
   - 优先使用显式引用，隐式引用作为降级方案
3. **自动创建 ComponentVersion**：
   - ReleaseImage 作为 ComponentVersion 的 Owner
   - 创建的 ComponentVersion 仅包含基础字段（componentName、version）
   - 完整的 ActionSpec 需要通过独立的 ComponentVersion YAML 补充
4. **删除保护**：
   - 被 ClusterVersion 引用的 ReleaseImage 不可删除
   - Controller 定期检查引用关系，更新 Status.referencedBy
   - 删除时先解除所有 ClusterVersion 引用
5. **Watch 链路**：
   - ClusterVersion 变更 → 触发 ReleaseImage 调谐（更新 referencedBy）
   - ComponentVersion 变更 → 触发 ReleaseImage 调谐（重新验证引用）
   - ReleaseImage 变更 → 触发 ClusterVersion 调谐（更新组件列表）

# ComponentVersion Controller 的完整设计思路与代码实现
## ComponentVersion Controller 详细设计
### 一、设计思路
ComponentVersion Controller 是组件生命周期的核心执行控制器，也是最复杂的控制器。它的核心职责是将声明式的 ComponentVersion CR 转化为实际的集群操作。
#### 1.1 核心设计原则
| 原则 | 说明 |
|------|------|
| **声明式驱动** | 控制器不维护任何内存状态，所有状态来源于 CR 的 Spec/Status |
| **幂等执行** | 同一操作可重复执行而不产生副作用，通过 status.phase 判断当前阶段 |
| **渐进推进** | 每次 Reconcile 只推进一个阶段，避免长时间阻塞 |
| **失败安全** | 升级失败时自动回滚，卸载失败时记录错误但不阻塞 |
| **节点级粒度** | Scope=Node 时跟踪每个节点的组件状态，支持逐节点升级 |
#### 1.2 状态机设计
```
                    ┌──────────┐
                    │ Pending  │ ← 初始状态 / 依赖未就绪
                    └────┬─────┘
                         │ 依赖就绪 + 需要安装
                         ▼
              ┌─────────────────────┐
              │  UninstallingOld    │ ← 升级时先卸载旧版本
              └────────┬────────────┘
                       │ 旧版本卸载完成
                       ▼
                ┌─────────────┐
                │ Installing  │ ← 执行 installAction
                └──────┬──────┘
                       │ 安装成功
                       ▼
              ┌──────────────────┐
              │  PostChecking    │ ← 执行 postCheck
              └────────┬─────────┘
                       │ postCheck 通过
                       ▼
                ┌──────────┐
                │ Healthy  │ ← 正常运行，周期性健康检查
                └────┬─────┘
                     │ 版本变更（desiredVersion != installedVersion）
                     ▼
              ┌──────────────┐
              │  Upgrading   │ ← 执行 upgradeAction
              └──────┬───────┘
                     │
            ┌────────┴────────┐
            │                 │
     升级成功 ▼          升级失败 ▼
      ┌──────────┐    ┌───────────────┐
      │ Healthy  │    │ UpgradeFailed │
      └──────────┘    └───────┬───────┘
                              │ 有 rollbackAction
                              ▼
                      ┌──────────────┐
                      │ RollingBack  │ ← 执行 rollbackAction
                      └──────┬───────┘
                             │
                    ┌────────┴────────┐
                    │                 │
             回滚成功 ▼          回滚失败 ▼
              ┌──────────┐    ┌──────────┐
              │ Healthy  │    │ Degraded │
              └──────────┘    └──────────┘

    任何阶段 + CR 被删除（Finalizer 触发）：
              ┌──────────────┐
              │ Uninstalling │ ← 执行 uninstallAction
              └──────┬───────┘
                     │ 卸载完成
                     ▼
              ┌────────────┐
              │ Uninstalled│ → 移除 Finalizer
              └────────────┘
```
#### 1.3 关键设计决策
| 决策 | 选择 | 原因 |
|------|------|------|
| desiredVersion 来源 | 从 ReleaseImage 间接获取（ClusterVersion 设置 ComponentVersion 的目标版本） | 版本变更由 ClusterVersion 编排，ComponentVersion 只负责执行 |
| 旧版本卸载时机 | 升级前先卸载旧版本 | 确保旧版本资源完全清理，避免与新版本冲突 |
| 旧版本查找路径 | ClusterVersion.status.currentReleaseRef → 旧 ReleaseImage → spec.components → 旧 ComponentVersion | 通过不可变的 ReleaseImage 追溯历史版本 |
| 节点级状态跟踪 | status.nodeStatuses map[string]NodeComponentStatus | Scope=Node 时需要逐节点跟踪 |
| 健康检查周期 | Reconcile 间隔 30s + 条件触发 | 平衡实时性与性能 |
| Finalizer 策略 | 添加 Finalizer，删除时执行 uninstallAction | 确保组件被正确清理 |
### 二、代码实现
#### 2.1 控制器结构体定义
```go
// controllers/cvo/componentversion_controller.go

type ComponentVersionReconciler struct {
    client.Client
    Scheme       *runtime.Scheme
    ActionEngine *actionengine.ActionEngine
    Recorder     record.EventRecorder

    HealthCheckInterval time.Duration
    RequeueInterval     time.Duration
}

const (
    componentVersionFinalizer = "cvo.openfuyao.cn/componentversion-protection"

    DefaultHealthCheckInterval = 30 * time.Second
    DefaultRequeueInterval     = 5 * time.Second
)
```
#### 2.2 Reconcile 主入口
```go
func (r *ComponentVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    cv := &cvo.ComponentVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if !cv.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, cv)
    }

    if !controllerutil.ContainsFinalizer(cv, componentVersionFinalizer) {
        controllerutil.AddFinalizer(cv, componentVersionFinalizer)
        if err := r.Update(ctx, cv); err != nil {
            return ctrl.Result{}, err
        }
    }

    desiredVersion := r.resolveDesiredVersion(ctx, cv)
    if desiredVersion == "" {
        logger.Info("desired version not resolved, waiting")
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    cv.Status.DesiredVersion = desiredVersion

    switch cv.Status.Phase {
    case "", cvo.ComponentPending:
        return r.handlePending(ctx, cv, desiredVersion)
    case cvo.ComponentInstalling:
        return r.handleInstalling(ctx, cv)
    case cvo.ComponentUninstalling:
        return r.handleUninstalling(ctx, cv)
    case cvo.ComponentHealthy, cvo.ComponentInstalled:
        return r.handleHealthy(ctx, cv, desiredVersion)
    case cvo.ComponentUpgrading:
        return r.handleUpgrading(ctx, cv)
    case cvo.ComponentUpgradeFailed:
        return r.handleUpgradeFailed(ctx, cv)
    case cvo.ComponentRollingBack:
        return r.handleRollingBack(ctx, cv)
    case cvo.ComponentDegraded:
        return r.handleDegraded(ctx, cv, desiredVersion)
    default:
        logger.Info("unknown phase, resetting to Pending", "phase", cv.Status.Phase)
        cv.Status.Phase = cvo.ComponentPending
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{Requeue: true}, nil
    }
}
```
#### 2.3 版本变更检测：resolveDesiredVersion
```go
func (r *ComponentVersionReconciler) resolveDesiredVersion(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) string {
    clusterName, ok := cv.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return ""
    }

    clusterVersions := &cvo.ClusterVersionList{}
    if err := r.List(ctx, clusterVersions,
        client.InNamespace(cv.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return ""
    }

    if len(clusterVersions.Items) == 0 {
        return ""
    }

    clusterVer := clusterVersions.Items[0]
    releaseRef := clusterVer.Spec.ReleaseRef

    release := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      releaseRef.Name,
        Namespace: cv.Namespace,
    }, release); err != nil {
        return ""
    }

    for _, comp := range release.Spec.Components {
        if comp.ComponentName == cv.Spec.ComponentName {
            return comp.Version
        }
    }
    return ""
}
```
#### 2.4 依赖检查：checkDependencies
```go
func (r *ComponentVersionReconciler) checkDependencies(
    ctx context.Context,
    cv *cvo.ComponentVersion,
    phase cvo.DependencyPhase,
) (bool, string) {
    for _, dep := range cv.Spec.Dependencies {
        if dep.Phase != "" && dep.Phase != phase && dep.Phase != cvo.DependencyAll {
            continue
        }

        depCV := &cvo.ComponentVersion{}
        depName := r.getComponentVersionName(dep.ComponentName, cv.Namespace)
        if err := r.Get(ctx, types.NamespacedName{
            Name:      depName,
            Namespace: cv.Namespace,
        }, depCV); err != nil {
            return false, fmt.Sprintf("dependency %s not found: %v", dep.ComponentName, err)
        }

        if depCV.Status.Phase != cvo.ComponentHealthy && depCV.Status.Phase != cvo.ComponentInstalled {
            return false, fmt.Sprintf("dependency %s not ready (phase=%s)", dep.ComponentName, depCV.Status.Phase)
        }

        if dep.VersionConstraint != "" {
            ok, err := versionSatisfies(depCV.Status.InstalledVersion, dep.VersionConstraint)
            if err != nil || !ok {
                return false, fmt.Sprintf("dependency %s version %s does not satisfy constraint %s",
                    dep.ComponentName, depCV.Status.InstalledVersion, dep.VersionConstraint)
            }
        }
    }
    return true, ""
}

func (r *ComponentVersionReconciler) getComponentVersionName(
    componentName cvo.ComponentName,
    namespace string,
) string {
    return fmt.Sprintf("%s-%s", namespace, componentName)
}

func versionSatisfies(version string, constraint string) (bool, error) {
    if strings.HasPrefix(constraint, ">=") {
        return semverCompare(version, strings.TrimPrefix(constraint, ">=")) >= 0, nil
    }
    if strings.HasPrefix(constraint, "<=") {
        return semverCompare(version, strings.TrimPrefix(constraint, "<=")) <= 0, nil
    }
    return version == constraint, nil
}
```
#### 2.5 旧版本卸载：findOldComponentVersion + uninstallOldVersion
```go
func (r *ComponentVersionReconciler) findOldComponentVersion(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (*cvo.ComponentVersion, string, error) {
    clusterName, ok := cv.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return nil, "", nil
    }

    clusterVersions := &cvo.ClusterVersionList{}
    if err := r.List(ctx, clusterVersions,
        client.InNamespace(cv.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return nil, "", err
    }

    if len(clusterVersions.Items) == 0 {
        return nil, "", nil
    }

    clusterVer := clusterVersions.Items[0]
    if clusterVer.Status.CurrentReleaseRef == nil {
        return nil, "", nil
    }

    oldRelease := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      clusterVer.Status.CurrentReleaseRef.Name,
        Namespace: cv.Namespace,
    }, oldRelease); err != nil {
        if apierrors.IsNotFound(err) {
            return nil, "", nil
        }
        return nil, "", err
    }

    for _, comp := range oldRelease.Spec.Components {
        if comp.ComponentName == cv.Spec.ComponentName {
            var oldCVName string
            if comp.ComponentVersionRef != nil {
                oldCVName = comp.ComponentVersionRef.Name
            } else {
                oldCVName = r.getComponentVersionName(comp.ComponentName, cv.Namespace)
            }

            oldCV := &cvo.ComponentVersion{}
            if err := r.Get(ctx, types.NamespacedName{
                Name:      oldCVName,
                Namespace: cv.Namespace,
            }, oldCV); err != nil {
                if apierrors.IsNotFound(err) {
                    return nil, comp.Version, nil
                }
                return nil, "", err
            }
            return oldCV, comp.Version, nil
        }
    }
    return nil, "", nil
}

func (r *ComponentVersionReconciler) uninstallOldVersion(
    ctx context.Context,
    cv *cvo.ComponentVersion,
    nodeConfigs []*cvo.NodeConfig,
) error {
    oldCV, oldVersion, err := r.findOldComponentVersion(ctx, cv)
    if err != nil {
        return fmt.Errorf("find old component version: %w", err)
    }

    if oldCV == nil {
        ctrl.LoggerFrom(ctx).Info("no old component version found, skip uninstall")
        return nil
    }

    oldEntry := r.findVersionEntry(oldCV, oldVersion)
    if oldEntry == nil {
        ctrl.LoggerFrom(ctx).Info("old version entry not found in ComponentVersion",
            "componentName", cv.Spec.ComponentName, "oldVersion", oldVersion)
        return nil
    }

    if oldEntry.UninstallAction == nil {
        ctrl.LoggerFrom(ctx).Info("old version has no uninstallAction, skip uninstall",
            "componentName", cv.Spec.ComponentName, "oldVersion", oldVersion)
        return nil
    }

    ctrl.LoggerFrom(ctx).Info("uninstalling old version",
        "componentName", cv.Spec.ComponentName,
        "oldVersion", oldVersion,
        "newVersion", cv.Status.DesiredVersion)

    templateCtx := r.buildTemplateContext(ctx, cv, nil)
    if err := r.ActionEngine.ExecuteAction(ctx, oldEntry.UninstallAction, oldCV, nodeConfigs, templateCtx); err != nil {
        r.Recorder.Eventf(cv, corev1.EventTypeWarning, "UninstallOldFailed",
            "Failed to uninstall old version %s: %v", oldVersion, err)
        return fmt.Errorf("uninstall old version %s: %w", oldVersion, err)
    }

    r.Recorder.Eventf(cv, corev1.EventTypeNormal, "UninstallOldSucceeded",
        "Successfully uninstalled old version %s", oldVersion)
    return nil
}

func (r *ComponentVersionReconciler) findVersionEntry(
    cv *cvo.ComponentVersion,
    version string,
) *cvo.ComponentVersionEntry {
    for i := range cv.Spec.Versions {
        if cv.Spec.Versions[i].Version == version {
            return &cv.Spec.Versions[i]
        }
    }
    return nil
}
```
#### 2.6 Pending 处理：handlePending
```go
func (r *ComponentVersionReconciler) handlePending(
    ctx context.Context,
    cv *cvo.ComponentVersion,
    desiredVersion string,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    ready, msg := r.checkDependencies(ctx, cv, cvo.DependencyInstall)
    if !ready {
        logger.Info("dependencies not ready, waiting", "message", msg)
        cv.Status.Phase = cvo.ComponentPending
        cv.Status.Message = fmt.Sprintf("waiting for dependencies: %s", msg)
        conditions.Set(cv, &cvo.ComponentVersionCondition{
            Type:    cvo.ComponentDependenciesReady,
            Status:  corev1.ConditionFalse,
            Reason:  "DependenciesNotReady",
            Message: msg,
        })
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    nodeConfigs, err := r.getNodeConfigs(ctx, cv)
    if err != nil {
        return ctrl.Result{}, fmt.Errorf("get node configs: %w", err)
    }

    if cv.Status.InstalledVersion != "" && cv.Status.InstalledVersion != desiredVersion {
        logger.Info("upgrading: uninstalling old version first",
            "installedVersion", cv.Status.InstalledVersion,
            "desiredVersion", desiredVersion)

        cv.Status.Phase = cvo.ComponentUninstalling
        cv.Status.LastOperation = &cvo.LastOperation{
            Type:      cvo.OperationUpgrade,
            Version:   desiredVersion,
            StartedAt: &metav1.Time{Time: time.Now()},
        }
        _ = r.Status().Update(ctx, cv)

        if err := r.uninstallOldVersion(ctx, cv, nodeConfigs); err != nil {
            cv.Status.Phase = cvo.ComponentDegraded
            cv.Status.Message = fmt.Sprintf("uninstall old version failed: %v", err)
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, err
        }

        cv.Status.Phase = cvo.ComponentInstalling
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{Requeue: true}, nil
    }

    logger.Info("installing component", "version", desiredVersion)
    cv.Status.Phase = cvo.ComponentInstalling
    cv.Status.LastOperation = &cvo.LastOperation{
        Type:      cvo.OperationInstall,
        Version:   desiredVersion,
        StartedAt: &metav1.Time{Time: time.Now()},
    }
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{Requeue: true}, nil
}
```
#### 2.7 安装处理：handleInstalling
```go
func (r *ComponentVersionReconciler) handleInstalling(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)
    desiredVersion := cv.Status.DesiredVersion

    entry := r.findVersionEntry(cv, desiredVersion)
    if entry == nil {
        cv.Status.Phase = cvo.ComponentDegraded
        cv.Status.Message = fmt.Sprintf("version entry %s not found in ComponentVersion", desiredVersion)
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{}, fmt.Errorf("version entry %s not found", desiredVersion)
    }

    if entry.PreCheck != nil {
        nodeConfigs, _ := r.getNodeConfigs(ctx, cv)
        templateCtx := r.buildTemplateContext(ctx, cv, nil)
        if err := r.ActionEngine.ExecuteAction(ctx, entry.PreCheck, cv, nodeConfigs, templateCtx); err != nil {
            cv.Status.Phase = cvo.ComponentDegraded
            cv.Status.Message = fmt.Sprintf("preCheck failed: %v", err)
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, err
        }
    }

    if entry.InstallAction == nil {
        cv.Status.Phase = cvo.ComponentDegraded
        cv.Status.Message = fmt.Sprintf("installAction not defined for version %s", desiredVersion)
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{}, fmt.Errorf("installAction not defined for version %s", desiredVersion)
    }

    nodeConfigs, err := r.getNodeConfigs(ctx, cv)
    if err != nil {
        return ctrl.Result{}, fmt.Errorf("get node configs: %w", err)
    }

    templateCtx := r.buildTemplateContext(ctx, cv, nil)
    if err := r.ActionEngine.ExecuteAction(ctx, entry.InstallAction, cv, nodeConfigs, templateCtx); err != nil {
        logger.Error(err, "install action failed",
            "componentName", cv.Spec.ComponentName, "version", desiredVersion)
        cv.Status.Phase = cvo.ComponentDegraded
        cv.Status.Message = fmt.Sprintf("install failed: %v", err)
        cv.Status.LastOperation.Result = cvo.OperationFailed
        cv.Status.LastOperation.Message = err.Error()
        cv.Status.LastOperation.CompletedAt = &metav1.Time{Time: time.Now()}
        r.Recorder.Eventf(cv, corev1.EventTypeWarning, "InstallFailed",
            "Failed to install %s %s: %v", cv.Spec.ComponentName, desiredVersion, err)
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{}, err
    }

    if entry.PostCheck != nil {
        templateCtx := r.buildTemplateContext(ctx, cv, nil)
        if err := r.ActionEngine.ExecuteActionWithRetry(ctx, entry.PostCheck, cv, nodeConfigs, templateCtx,
            entry.PostCheck.RetryPolicy); err != nil {
            cv.Status.Phase = cvo.ComponentDegraded
            cv.Status.Message = fmt.Sprintf("postCheck failed: %v", err)
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, err
        }
    }

    cv.Status.InstalledVersion = desiredVersion
    cv.Status.Phase = cvo.ComponentHealthy
    cv.Status.Message = ""
    cv.Status.LastOperation.Result = cvo.OperationSucceeded
    cv.Status.LastOperation.CompletedAt = &metav1.Time{Time: time.Now()}
    r.updateNodeStatuses(cv, nodeConfigs, desiredVersion, cvo.ComponentHealthy)
    conditions.Set(cv, &cvo.ComponentVersionCondition{
        Type:   cvo.ComponentAvailable,
        Status: corev1.ConditionTrue,
        Reason: "InstallSucceeded",
    })
    r.Recorder.Eventf(cv, corev1.EventTypeNormal, "InstallSucceeded",
        "Successfully installed %s %s", cv.Spec.ComponentName, desiredVersion)
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{RequeueAfter: r.HealthCheckInterval}, nil
}
```
#### 2.8 升级处理：handleUpgrading
```go
func (r *ComponentVersionReconciler) handleUpgrading(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)
    desiredVersion := cv.Status.DesiredVersion
    installedVersion := cv.Status.InstalledVersion

    upgradeAction := r.findUpgradeAction(cv, installedVersion, desiredVersion)
    if upgradeAction == nil {
        logger.Info("no matching upgradeAction found, falling back to uninstall+install",
            "fromVersion", installedVersion, "toVersion", desiredVersion)
        return r.handleFallbackUpgrade(ctx, cv)
    }

    if upgradeAction.PreCheck != nil {
        nodeConfigs, _ := r.getNodeConfigs(ctx, cv)
        templateCtx := r.buildTemplateContext(ctx, cv, nil)
        if err := r.ActionEngine.ExecuteAction(ctx, upgradeAction.PreCheck, cv, nodeConfigs, templateCtx); err != nil {
            cv.Status.Phase = cvo.ComponentUpgradeFailed
            cv.Status.Message = fmt.Sprintf("upgrade preCheck failed: %v", err)
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, err
        }
    }

    nodeConfigs, err := r.getNodeConfigs(ctx, cv)
    if err != nil {
        return ctrl.Result{}, fmt.Errorf("get node configs: %w", err)
    }

    templateCtx := r.buildTemplateContext(ctx, cv, nil)
    templateCtx.PreviousVersion = installedVersion
    if err := r.ActionEngine.ExecuteAction(ctx, upgradeAction, cv, nodeConfigs, templateCtx); err != nil {
        logger.Error(err, "upgrade action failed",
            "componentName", cv.Spec.ComponentName,
            "fromVersion", installedVersion,
            "toVersion", desiredVersion)

        cv.Status.Phase = cvo.ComponentUpgradeFailed
        cv.Status.Message = fmt.Sprintf("upgrade failed: %v", err)
        cv.Status.LastOperation.Result = cvo.OperationFailed
        cv.Status.LastOperation.Message = err.Error()
        r.Recorder.Eventf(cv, corev1.EventTypeWarning, "UpgradeFailed",
            "Failed to upgrade %s from %s to %s: %v",
            cv.Spec.ComponentName, installedVersion, desiredVersion, err)
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{Requeue: true}, nil
    }

    if upgradeAction.PostCheck != nil {
        templateCtx := r.buildTemplateContext(ctx, cv, nil)
        templateCtx.PreviousVersion = installedVersion
        if err := r.ActionEngine.ExecuteActionWithRetry(ctx, upgradeAction.PostCheck, cv, nodeConfigs, templateCtx,
            upgradeAction.PostCheck.RetryPolicy); err != nil {
            cv.Status.Phase = cvo.ComponentUpgradeFailed
            cv.Status.Message = fmt.Sprintf("upgrade postCheck failed: %v", err)
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{Requeue: true}, nil
        }
    }

    cv.Status.InstalledVersion = desiredVersion
    cv.Status.Phase = cvo.ComponentHealthy
    cv.Status.Message = ""
    cv.Status.LastOperation.Result = cvo.OperationSucceeded
    cv.Status.LastOperation.CompletedAt = &metav1.Time{Time: time.Now()}
    r.updateNodeStatuses(cv, nodeConfigs, desiredVersion, cvo.ComponentHealthy)
    conditions.Set(cv, &cvo.ComponentVersionCondition{
        Type:   cvo.ComponentAvailable,
        Status: corev1.ConditionTrue,
        Reason: "UpgradeSucceeded",
    })
    r.Recorder.Eventf(cv, corev1.EventTypeNormal, "UpgradeSucceeded",
        "Successfully upgraded %s from %s to %s",
        cv.Spec.ComponentName, installedVersion, desiredVersion)
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{RequeueAfter: r.HealthCheckInterval}, nil
}

func (r *ComponentVersionReconciler) findUpgradeAction(
    cv *cvo.ComponentVersion,
    fromVersion string,
    toVersion string,
) *cvo.ActionSpec {
    entry := r.findVersionEntry(cv, toVersion)
    if entry == nil {
        return nil
    }

    for _, upgrade := range entry.UpgradeFrom {
        if upgrade.FromVersion == fromVersion {
            return upgrade.Action
        }
    }
    return nil
}

func (r *ComponentVersionReconciler) handleFallbackUpgrade(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (ctrl.Result, error) {
    nodeConfigs, err := r.getNodeConfigs(ctx, cv)
    if err != nil {
        return ctrl.Result{}, err
    }

    if err := r.uninstallOldVersion(ctx, cv, nodeConfigs); err != nil {
        cv.Status.Phase = cvo.ComponentDegraded
        cv.Status.Message = fmt.Sprintf("fallback uninstall failed: %v", err)
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{}, err
    }

    cv.Status.Phase = cvo.ComponentInstalling
    cv.Status.InstalledVersion = ""
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{Requeue: true}, nil
}
```
#### 2.9 升级失败与回滚处理
```go
func (r *ComponentVersionReconciler) handleUpgradeFailed(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    entry := r.findVersionEntry(cv, cv.Status.DesiredVersion)
    if entry == nil || entry.RollbackAction == nil {
        logger.Info("no rollbackAction defined, staying in UpgradeFailed",
            "componentName", cv.Spec.ComponentName)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    clusterName := cv.Labels["cluster.x-k8s.io/cluster-name"]
    autoRollback := r.shouldAutoRollback(ctx, clusterName, cv.Namespace)
    if !autoRollback {
        logger.Info("autoRollback not enabled, staying in UpgradeFailed",
            "componentName", cv.Spec.ComponentName)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    logger.Info("starting rollback",
        "componentName", cv.Spec.ComponentName,
        "fromVersion", cv.Status.DesiredVersion,
        "toVersion", cv.Status.InstalledVersion)

    cv.Status.Phase = cvo.ComponentRollingBack
    cv.Status.LastOperation = &cvo.LastOperation{
        Type:      cvo.OperationRollback,
        Version:   cv.Status.InstalledVersion,
        StartedAt: &metav1.Time{Time: time.Now()},
    }
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{Requeue: true}, nil
}

func (r *ComponentVersionReconciler) handleRollingBack(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)
    failedVersion := cv.Status.DesiredVersion
    rollbackVersion := cv.Status.InstalledVersion

    entry := r.findVersionEntry(cv, failedVersion)
    if entry == nil || entry.RollbackAction == nil {
        cv.Status.Phase = cvo.ComponentDegraded
        cv.Status.Message = "rollbackAction not found"
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{}, nil
    }

    nodeConfigs, err := r.getNodeConfigs(ctx, cv)
    if err != nil {
        return ctrl.Result{}, err
    }

    templateCtx := r.buildTemplateContext(ctx, cv, nil)
    templateCtx.PreviousVersion = failedVersion
    if err := r.ActionEngine.ExecuteAction(ctx, entry.RollbackAction, cv, nodeConfigs, templateCtx); err != nil {
        logger.Error(err, "rollback action failed",
            "componentName", cv.Spec.ComponentName,
            "rollbackVersion", rollbackVersion)
        cv.Status.Phase = cvo.ComponentDegraded
        cv.Status.Message = fmt.Sprintf("rollback failed: %v", err)
        cv.Status.LastOperation.Result = cvo.OperationFailed
        cv.Status.LastOperation.Message = err.Error()
        r.Recorder.Eventf(cv, corev1.EventTypeWarning, "RollbackFailed",
            "Failed to rollback %s to %s: %v", cv.Spec.ComponentName, rollbackVersion, err)
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{}, err
    }

    cv.Status.DesiredVersion = rollbackVersion
    cv.Status.Phase = cvo.ComponentHealthy
    cv.Status.Message = fmt.Sprintf("rolled back from %s to %s", failedVersion, rollbackVersion)
    cv.Status.LastOperation.Result = cvo.OperationSucceeded
    cv.Status.LastOperation.CompletedAt = &metav1.Time{Time: time.Now()}
    r.Recorder.Eventf(cv, corev1.EventTypeNormal, "RollbackSucceeded",
        "Successfully rolled back %s from %s to %s",
        cv.Spec.ComponentName, failedVersion, rollbackVersion)
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{RequeueAfter: r.HealthCheckInterval}, nil
}

func (r *ComponentVersionReconciler) shouldAutoRollback(
    ctx context.Context,
    clusterName string,
    namespace string,
) bool {
    clusterVersions := &cvo.ClusterVersionList{}
    if err := r.List(ctx, clusterVersions,
        client.InNamespace(namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return false
    }

    if len(clusterVersions.Items) == 0 {
        return false
    }

    return clusterVersions.Items[0].Spec.UpgradeStrategy.AutoRollback
}
```
#### 2.10 健康检查处理：handleHealthy
```go
func (r *ComponentVersionReconciler) handleHealthy(
    ctx context.Context,
    cv *cvo.ComponentVersion,
    desiredVersion string,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    if cv.Status.InstalledVersion != desiredVersion {
        logger.Info("version drift detected, starting upgrade",
            "installedVersion", cv.Status.InstalledVersion,
            "desiredVersion", desiredVersion)

        ready, msg := r.checkDependencies(ctx, cv, cvo.DependencyUpgrade)
        if !ready {
            logger.Info("upgrade dependencies not ready", "message", msg)
            return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
        }

        nodeConfigs, _ := r.getNodeConfigs(ctx, cv)

        if err := r.uninstallOldVersion(ctx, cv, nodeConfigs); err != nil {
            cv.Status.Phase = cvo.ComponentDegraded
            cv.Status.Message = fmt.Sprintf("uninstall old version failed: %v", err)
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, err
        }

        cv.Status.Phase = cvo.ComponentUpgrading
        cv.Status.LastOperation = &cvo.LastOperation{
            Type:      cvo.OperationUpgrade,
            Version:   desiredVersion,
            StartedAt: &metav1.Time{Time: time.Now()},
        }
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{Requeue: true}, nil
    }

    if cv.Spec.HealthCheck != nil {
        healthy, err := r.executeHealthCheck(ctx, cv)
        if err != nil {
            logger.Error(err, "health check failed",
                "componentName", cv.Spec.ComponentName)
        }

        if !healthy {
            conditions.Set(cv, &cvo.ComponentVersionCondition{
                Type:    cvo.ComponentAvailable,
                Status:  corev1.ConditionFalse,
                Reason:  "HealthCheckFailed",
                Message: fmt.Sprintf("health check failed: %v", err),
            })
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{RequeueAfter: r.HealthCheckInterval}, nil
        }

        conditions.Set(cv, &cvo.ComponentVersionCondition{
            Type:   cvo.ComponentAvailable,
            Status: corev1.ConditionTrue,
            Reason: "HealthCheckSucceeded",
        })
        _ = r.Status().Update(ctx, cv)
    }

    return ctrl.Result{RequeueAfter: r.HealthCheckInterval}, nil
}

func (r *ComponentVersionReconciler) executeHealthCheck(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (bool, error) {
    if cv.Spec.HealthCheck == nil {
        return true, nil
    }

    nodeConfigs, _ := r.getNodeConfigs(ctx, cv)
    templateCtx := r.buildTemplateContext(ctx, cv, nil)

    for _, step := range cv.Spec.HealthCheck.Steps {
        if err := r.ActionEngine.ExecuteHealthCheckStep(ctx, &step, cv, nodeConfigs, templateCtx); err != nil {
            return false, fmt.Errorf("health check step %s failed: %w", step.Name, err)
        }
    }
    return true, nil
}
```
#### 2.11 删除处理（Finalizer）：handleDeletion
```go
func (r *ComponentVersionReconciler) handleDeletion(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    if !controllerutil.ContainsFinalizer(cv, componentVersionFinalizer) {
        return ctrl.Result{}, nil
    }

    if cv.Status.Phase != cvo.ComponentUninstalling {
        logger.Info("component being deleted, executing uninstallAction",
            "componentName", cv.Spec.ComponentName,
            "installedVersion", cv.Status.InstalledVersion)

        entry := r.findVersionEntry(cv, cv.Status.InstalledVersion)
        if entry != nil && entry.UninstallAction != nil {
            cv.Status.Phase = cvo.ComponentUninstalling
            cv.Status.LastOperation = &cvo.LastOperation{
                Type:      cvo.OperationUninstall,
                Version:   cv.Status.InstalledVersion,
                StartedAt: &metav1.Time{Time: time.Now()},
            }
            _ = r.Status().Update(ctx, cv)

            nodeConfigs, _ := r.getNodeConfigs(ctx, cv)
            templateCtx := r.buildTemplateContext(ctx, cv, nil)
            if err := r.ActionEngine.ExecuteAction(ctx, entry.UninstallAction, cv, nodeConfigs, templateCtx); err != nil {
                logger.Error(err, "uninstall action failed on deletion",
                    "componentName", cv.Spec.ComponentName)
                cv.Status.Phase = cvo.ComponentDegraded
                cv.Status.Message = fmt.Sprintf("uninstall on deletion failed: %v", err)
                _ = r.Status().Update(ctx, cv)
                return ctrl.Result{}, err
            }

            r.Recorder.Eventf(cv, corev1.EventTypeNormal, "UninstallSucceeded",
                "Successfully uninstalled %s %s on deletion",
                cv.Spec.ComponentName, cv.Status.InstalledVersion)
        }

        controllerutil.RemoveFinalizer(cv, componentVersionFinalizer)
        if err := r.Update(ctx, cv); err != nil {
            return ctrl.Result{}, err
        }
        logger.Info("component uninstalled and finalizer removed",
            "componentName", cv.Spec.ComponentName)
    }

    return ctrl.Result{}, nil
}
```
#### 2.12 节点级状态跟踪
```go
func (r *ComponentVersionReconciler) getNodeConfigs(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) ([]*cvo.NodeConfig, error) {
    clusterName, ok := cv.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return nil, nil
    }

    nodeConfigList := &cvo.NodeConfigList{}
    if err := r.List(ctx, nodeConfigList,
        client.InNamespace(cv.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return nil, err
    }

    var result []*cvo.NodeConfig
    for i := range nodeConfigList.Items {
        nc := &nodeConfigList.Items[i]
        if r.nodeMatchesComponent(nc, cv) {
            result = append(result, nc)
        }
    }
    return result, nil
}

func (r *ComponentVersionReconciler) nodeMatchesComponent(
    nc *cvo.NodeConfig,
    cv *cvo.ComponentVersion,
) bool {
    for _, comp := range nc.Spec.Components {
        if comp.ComponentName == cv.Spec.ComponentName {
            return true
        }
    }
    return false
}

func (r *ComponentVersionReconciler) updateNodeStatuses(
    cv *cvo.ComponentVersion,
    nodeConfigs []*cvo.NodeConfig,
    version string,
    phase cvo.ComponentPhase,
) {
    if cv.Spec.Scope != cvo.ScopeNode {
        return
    }

    if cv.Status.NodeStatuses == nil {
        cv.Status.NodeStatuses = make(map[string]cvo.NodeComponentStatus)
    }

    for _, nc := range nodeConfigs {
        cv.Status.NodeStatuses[nc.Spec.NodeName] = cvo.NodeComponentStatus{
            Phase:     phase,
            Version:   version,
            UpdatedAt: &metav1.Time{Time: time.Now()},
        }
    }
}

func (r *ComponentVersionReconciler) updateSingleNodeStatus(
    cv *cvo.ComponentVersion,
    nodeName string,
    version string,
    phase cvo.ComponentPhase,
    message string,
) {
    if cv.Spec.Scope != cvo.ScopeNode {
        return
    }

    if cv.Status.NodeStatuses == nil {
        cv.Status.NodeStatuses = make(map[string]cvo.NodeComponentStatus)
    }

    cv.Status.NodeStatuses[nodeName] = cvo.NodeComponentStatus{
        Phase:     phase,
        Version:   version,
        Message:   message,
        UpdatedAt: &metav1.Time{Time: time.Now()},
    }
}
```
#### 2.13 模板上下文构建
```go
func (r *ComponentVersionReconciler) buildTemplateContext(
    ctx context.Context,
    cv *cvo.ComponentVersion,
    nodeConfig *cvo.NodeConfig,
) *actionengine.TemplateContext {
    templateCtx := &actionengine.TemplateContext{
        ComponentName: string(cv.Spec.ComponentName),
        Version:       cv.Status.DesiredVersion,
    }

    clusterName, ok := cv.Labels["cluster.x-k8s.io/cluster-name"]
    if ok {
        templateCtx.ClusterName = clusterName
        templateCtx.ClusterNamespace = cv.Namespace

        bkeCluster := &bkev1beta1.BKECluster{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      clusterName,
            Namespace: cv.Namespace,
        }, bkeCluster); err == nil {
            cluster := bkeCluster.Spec.ClusterConfig.Cluster
            templateCtx.KubernetesVersion = cluster.KubernetesVersion
            templateCtx.EtcdVersion = cluster.EtcdVersion
            templateCtx.ContainerdVersion = cluster.ContainerdVersion
            templateCtx.OpenFuyaoVersion = cluster.OpenFuyaoVersion
            templateCtx.ImageRepo = cluster.ImageRepo.URL
            templateCtx.HTTPRepo = cluster.HTTPRepo.URL
            templateCtx.ControlPlaneEndpoint = bkeCluster.Spec.ControlPlaneEndpoint.String()
        }
    }

    if nodeConfig != nil {
        templateCtx.NodeIP = nodeConfig.Spec.NodeIP
        templateCtx.NodeHostname = nodeConfig.Spec.NodeName
        templateCtx.NodeRoles = make([]string, len(nodeConfig.Spec.Roles))
        for i, role := range nodeConfig.Spec.Roles {
            templateCtx.NodeRoles[i] = string(role)
        }
        templateCtx.IsFirstMaster = r.isFirstMasterNode(ctx, nodeConfig)
    }

    return templateCtx
}

func (r *ComponentVersionReconciler) isFirstMasterNode(
    ctx context.Context,
    nc *cvo.NodeConfig,
) bool {
    for _, role := range nc.Spec.Roles {
        if role == cvo.NodeRoleMaster {
            masterNodes := &cvo.NodeConfigList{}
            if err := r.List(ctx, masterNodes,
                client.InNamespace(nc.Namespace),
                client.MatchingLabels{
                    "cluster.x-k8s.io/cluster-name": nc.Labels["cluster.x-k8s.io/cluster-name"],
                    "node-role":                      "master",
                },
            ); err == nil && len(masterNodes.Items) > 0 {
                return masterNodes.Items[0].Name == nc.Name
            }
        }
    }
    return false
}
```
#### 2.14 Degraded 处理
```go
func (r *ComponentVersionReconciler) handleDegraded(
    ctx context.Context,
    cv *cvo.ComponentVersion,
    desiredVersion string,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    if cv.Status.InstalledVersion == desiredVersion {
        logger.Info("component in degraded state but version matches, re-running health check",
            "componentName", cv.Spec.ComponentName)

        if cv.Spec.HealthCheck != nil {
            healthy, _ := r.executeHealthCheck(ctx, cv)
            if healthy {
                cv.Status.Phase = cvo.ComponentHealthy
                cv.Status.Message = ""
                _ = r.Status().Update(ctx, cv)
                return ctrl.Result{RequeueAfter: r.HealthCheckInterval}, nil
            }
        }
    }

    logger.Info("component in degraded state, attempting recovery",
        "componentName", cv.Spec.ComponentName,
        "installedVersion", cv.Status.InstalledVersion,
        "desiredVersion", desiredVersion)

    cv.Status.Phase = cvo.ComponentPending
    cv.Status.Message = "recovering from degraded state"
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{Requeue: true}, nil
}
```
#### 2.15 SetupWithManager
```go
func (r *ComponentVersionReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&cvo.ComponentVersion{}).
        Watches(
            &cvo.ClusterVersion{},
            handler.EnqueueRequestsFromMapFunc(r.clusterVersionToComponentVersions),
        ).
        Watches(
            &cvo.NodeConfig{},
            handler.EnqueueRequestsFromMapFunc(r.nodeConfigToComponentVersions),
        ).
        WithEventFilter(predicate.GenerationChangedPredicate{}).
        Complete(r)
}

func (r *ComponentVersionReconciler) clusterVersionToComponentVersions(
    ctx context.Context,
    obj client.Object,
) []reconcile.Request {
    clusterVersion, ok := obj.(*cvo.ClusterVersion)
    if !ok {
        return nil
    }

    release := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      clusterVersion.Spec.ReleaseRef.Name,
        Namespace: clusterVersion.Namespace,
    }, release); err != nil {
        return nil
    }

    var requests []reconcile.Request
    for _, comp := range release.Spec.Components {
        name := r.getComponentVersionName(comp.ComponentName, clusterVersion.Namespace)
        requests = append(requests, reconcile.Request{
            NamespacedName: types.NamespacedName{
                Name:      name,
                Namespace: clusterVersion.Namespace,
            },
        })
    }
    return requests
}

func (r *ComponentVersionReconciler) nodeConfigToComponentVersions(
    ctx context.Context,
    obj client.Object,
) []reconcile.Request {
    nodeConfig, ok := obj.(*cvo.NodeConfig)
    if !ok {
        return nil
    }

    var requests []reconcile.Request
    for _, comp := range nodeConfig.Spec.Components {
        name := r.getComponentVersionName(comp.ComponentName, nodeConfig.Namespace)
        requests = append(requests, reconcile.Request{
            NamespacedName: types.NamespacedName{
                Name:      name,
                Namespace: nodeConfig.Namespace,
            },
        })
    }
    return requests
}
```
### 三、核心流程时序图
#### 3.1 安装流程
```
ComponentVersion CR 创建
    │
    ▼
Reconcile: phase=Pending
    │
    ├── checkDependencies() ──→ 依赖未就绪 → requeue 5s
    │
    ├── checkDependencies() ──→ 依赖就绪
    │
    ├── findOldComponentVersion() ──→ 有旧版本
    │   └── uninstallOldVersion() ──→ 执行旧 uninstallAction
    │       ├── 成功 → phase=Installing
    │       └── 失败 → phase=Degraded
    │
    ├── findOldComponentVersion() ──→ 无旧版本
    │   └── phase=Installing
    │
    ▼
Reconcile: phase=Installing
    │
    ├── findVersionEntry(desiredVersion)
    ├── executeAction(preCheck) ──→ 失败 → phase=Degraded
    ├── executeAction(installAction) ──→ 失败 → phase=Degraded
    ├── executeActionWithRetry(postCheck) ──→ 失败 → phase=Degraded
    │
    └── 成功:
        ├── status.installedVersion = desiredVersion
        ├── status.phase = Healthy
        └── updateNodeStatuses() → requeue 30s (健康检查)
```
#### 3.2 升级流程
```
ClusterVersion 更新 desiredVersion
    │
    ▼
ComponentVersion Reconcile 触发
    │
    ├── phase=Healthy, installedVersion != desiredVersion
    │   ├── checkDependencies(upgrade)
    │   ├── uninstallOldVersion() ──→ 通过 ClusterVersion.currentReleaseRef 找旧版本
    │   └── phase=Upgrading
    │
    ▼
Reconcile: phase=Upgrading
    │
    ├── findUpgradeAction(fromVersion, toVersion)
    │   ├── 找到 → 执行 upgradeAction
    │   └── 未找到 → handleFallbackUpgrade() → uninstall + install
    │
    ├── executeAction(preCheck) ──→ 失败 → phase=UpgradeFailed
    ├── executeAction(upgradeAction) ──→ 失败 → phase=UpgradeFailed
    ├── executeActionWithRetry(postCheck) ──→ 失败 → phase=UpgradeFailed
    │
    └── 成功:
        ├── status.installedVersion = desiredVersion
        ├── status.phase = Healthy
        └── updateNodeStatuses()
```
#### 3.3 回滚流程
```
Reconcile: phase=UpgradeFailed
    │
    ├── findVersionEntry(failedVersion).rollbackAction
    │   ├── 无 → 停留在 UpgradeFailed
    │   └── 有 → 检查 shouldAutoRollback()
    │       ├── false → 停留在 UpgradeFailed
    │       └── true → phase=RollingBack
    │
    ▼
Reconcile: phase=RollingBack
    │
    ├── executeAction(rollbackAction) ──→ 失败 → phase=Degraded
    │
    └── 成功:
        ├── status.desiredVersion = installedVersion (回退到旧版本)
        ├── status.phase = Healthy
        └── requeue 30s
```
### 四、关键设计要点总结
| 要点 | 设计 | 代码位置 |
|------|------|---------|
| **版本变更检测** | resolveDesiredVersion() 从 ReleaseImage 获取目标版本，对比 status.installedVersion | §2.3 |
| **依赖检查** | checkDependencies() 检查依赖组件 phase + 版本约束 | §2.4 |
| **旧版本卸载** | findOldComponentVersion() 通过 ClusterVersion.currentReleaseRef → 旧 ReleaseImage → 旧 ComponentVersion → uninstallAction | §2.5 |
| **安装/升级/回滚** | 状态机驱动：Pending→Installing→Healthy→Upgrading→Healthy/UpgradeFailed→RollingBack | §2.6-2.9 |
| **健康检查** | handleHealthy() 周期性执行 healthCheck，更新 conditions | §2.10 |
| **Finalizer** | handleDeletion() 删除时执行 uninstallAction 后移除 Finalizer | §2.11 |
| **节点级状态** | updateNodeStatuses() / updateSingleNodeStatus() 跟踪每个节点的组件状态 | §2.12 |
| **Fallback 升级** | findUpgradeAction() 未找到匹配的 upgradeAction 时，回退到 uninstall+install | §2.8 |
| **事件通知** | Recorder.Eventf() 记录安装/升级/回滚/卸载的成功/失败事件 | 全局 |
| **Watch 联动** | Watch ClusterVersion + NodeConfig 变更，触发关联 ComponentVersion Reconcile | §2.15 |

# NodeConfig Controller 的详细设计
## NodeConfig Controller 详细设计
### 一、设计思路
NodeConfig Controller 是节点级组件生命周期管理控制器，承担五大核心职责：监听节点增删、触发组件安装/卸载、更新节点组件状态、触发 cluster-api 扩缩容。它是 ComponentVersion Controller 的"节点侧代理"，负责将节点维度的变更转化为 ComponentVersion 的操作请求。
#### 1.1 核心设计原则
| 原则 | 说明 |
|------|------|
| **节点即状态** | NodeConfig 是节点在管理面的投影，其 Spec 变更代表节点期望状态变化 |
| **委托执行** | NodeConfig Controller 不直接执行组件操作，而是通过更新 ComponentVersion 的 nodeStatuses 委托给 ComponentVersion Controller 执行 |
| **扩缩容桥接** | NodeConfig 增删触发 cluster-api 的 Machine 增删，实现声明式扩缩容 |
| **状态聚合** | 聚合节点上所有组件的状态，形成节点级健康视图 |
| **有序卸载** | 删除节点时按依赖逆序卸载组件，确保集群稳定性 |
#### 1.2 NodeConfig Controller 与 ComponentVersion Controller 的职责边界
```
┌─────────────────────────────────────────────────────────┐
│  NodeConfig Controller                                  │
│  ┌──────────────────────────────────────────────────┐   │
│  │ 职责：                                           │   │
│  │ 1. 监听 NodeConfig CR 增删改                     │   │
│  │ 2. 新增节点→触发ComponentVersion安装该节点组件   │   │
│  │ 3. 删除节点→触发ComponentVersion 卸载该节点组件  │   │
│  │ 4. 聚合节点组件状态 → 更新 NodeConfig.status     │   │
│  │ 5. 新增/删除节点 → 触发 cluster-api Machine 增删 │   │
│  └──────────────────────────────────────────────────┘   │
│                         │ 委托                          │
│                         ▼                               │
│  ┌──────────────────────────────────────────────────┐   │
│  │ ComponentVersion Controller                      │   │
│  │ 职责：                                           │   │
│  │ 1. 实际执行installAction/upgradeAction/uninstall │   │
│  │ 2. 管理组件版本状态                              │   │
│  │ 3. 健康检查                                      │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```
#### 1.3 状态机设计
```
                    ┌──────────┐
                    │ Pending  │ ← 初始状态/等待组件安装
                    └────┬─────┘
                         │ 所有组件安装成功
                         ▼
                ┌──────────────┐
                │   Ready      │ ← 正常运行
                └──┬───┬───┬───┘
                   │   │   │
       组件版本变更│   │   │ 节点被标记删除
                   │   │   │
                   ▼   │   ▼
          ┌──────────┐ │ ┌──────────┐
          │ Upgrading│ │ │ Deleting │ ← 触发组件卸载+Machine 删除
          └────┬─────┘ │ └────┬─────┘
               │       │      │
     ┌─────────┴──┐    │      │ 所有组件卸载完成
     │            │    │      │
成功 ▼      失败  ▼    │      ▼
  ┌───────┐ ┌────────┐ │ ┌──────────┐
  │ Ready │ │NotReady│ │ │ Deleted  │ → 移除 Finalizer
  └───────┘ └────────┘ │ └──────────┘
                       │
        部分组件不健康 ▼
                ┌──────────┐
                │ NotReady │
                └────┬─────┘
                     │ 组件恢复
                     ▼
                ┌──────────┐
                │   Ready  │
                └──────────┘
```
#### 1.4 关键设计决策
| 决策 | 选择 | 原因 |
|------|------|------|
| 组件安装触发方式 | NodeConfig 更新 ComponentVersion 的 nodeStatuses，ComponentVersion Controller 检测到新节点后执行安装 | 保持 ComponentVersion Controller 作为唯一执行器，NodeConfig Controller 只做状态触发 |
| 节点删除流程 | NodeConfig phase=Deleting → 按依赖逆序通知 ComponentVersion 卸载 → 删除 Machine → 移除 Finalizer | 确保组件被正确卸载后再删除基础设施 |
| 扩容触发 | 新增 NodeConfig CR → 创建 Machine → 等待 Machine Ready → 触发组件安装 | 与 cluster-api 标准流程对齐 |
| 缩容触发 | NodeConfig phase=Deleting → 卸载组件 → 删除 Machine → 删除 NodeConfig | 先卸载再删除，避免资源泄漏 |
| 组件状态聚合 | 遍历关联 ComponentVersion 的 nodeStatuses[本节点] | NodeConfig 不维护独立组件状态，从 ComponentVersion 聚合 |
### 二、代码实现
#### 2.1 控制器结构体定义
```go
// controllers/cvo/nodeconfig_controller.go

type NodeConfigReconciler struct {
    client.Client
    Scheme   *runtime.Scheme
    Recorder record.EventRecorder

    RequeueInterval time.Duration
}

const (
    nodeConfigFinalizer = "cvo.openfuyao.cn/nodeconfig-protection"

    DefaultNodeConfigRequeueInterval = 5 * time.Second
)
```
#### 2.2 Reconcile 主入口
```go
func (r *NodeConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    nc := &cvo.NodeConfig{}
    if err := r.Get(ctx, req.NamespacedName, nc); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if !nc.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, nc)
    }

    if !controllerutil.ContainsFinalizer(nc, nodeConfigFinalizer) {
        controllerutil.AddFinalizer(nc, nodeConfigFinalizer)
        if err := r.Update(ctx, nc); err != nil {
            return ctrl.Result{}, err
        }
    }

    switch nc.Status.Phase {
    case "", cvo.NodeConfigPending:
        return r.handlePending(ctx, nc)
    case cvo.NodeConfigInstalling:
        return r.handleInstalling(ctx, nc)
    case cvo.NodeConfigReady:
        return r.handleReady(ctx, nc)
    case cvo.NodeConfigUpgrading:
        return r.handleUpgrading(ctx, nc)
    case cvo.NodeConfigNotReady:
        return r.handleNotReady(ctx, nc)
    case cvo.NodeConfigDeleting:
        return r.handleDeleting(ctx, nc)
    default:
        logger.Info("unknown phase, resetting to Pending", "phase", nc.Status.Phase)
        nc.Status.Phase = cvo.NodeConfigPending
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{Requeue: true}, nil
    }
}
```
#### 2.3 监听节点增删：handlePending
新增 NodeConfig CR 时触发，负责初始化节点组件状态并启动安装流程。
```go
func (r *NodeConfigReconciler) handlePending(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    if len(nc.Spec.Components) == 0 {
        logger.Info("no components defined, generating from ReleaseImage")
        if err := r.populateComponentsFromRelease(ctx, nc); err != nil {
            logger.Error(err, "failed to populate components from ReleaseImage")
            return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
        }
    }

    if nc.Status.ComponentStatus == nil {
        nc.Status.ComponentStatus = make(map[string]cvo.NodeComponentDetailStatus)
    }

    for _, comp := range nc.Spec.Components {
        if _, exists := nc.Status.ComponentStatus[string(comp.ComponentName)]; !exists {
            nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
                Phase: cvo.ComponentPending,
            }
        }
    }

    nc.Status.Phase = cvo.NodeConfigInstalling
    nc.Status.LastOperation = &cvo.LastOperation{
        Type:      cvo.OperationInstall,
        StartedAt: &metav1.Time{Time: time.Now()},
    }
    _ = r.Status().Update(ctx, nc)

    r.triggerComponentInstallForNode(ctx, nc)

    return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
}

func (r *NodeConfigReconciler) populateComponentsFromRelease(
    ctx context.Context,
    nc *cvo.NodeConfig,
) error {
    clusterName, ok := nc.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return fmt.Errorf("cluster label not found on NodeConfig")
    }

    clusterVersions := &cvo.ClusterVersionList{}
    if err := r.List(ctx, clusterVersions,
        client.InNamespace(nc.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return err
    }

    if len(clusterVersions.Items) == 0 {
        return fmt.Errorf("ClusterVersion not found for cluster %s", clusterName)
    }

    releaseRef := clusterVersions.Items[0].Spec.ReleaseRef
    release := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      releaseRef.Name,
        Namespace: nc.Namespace,
    }, release); err != nil {
        return err
    }

    var components []cvo.NodeComponent
    for _, comp := range release.Spec.Components {
        if r.componentMatchesNodeRole(comp, nc.Spec.Roles) {
            components = append(components, cvo.NodeComponent{
                ComponentName: comp.ComponentName,
                Version:       comp.Version,
                ComponentVersionRef: &cvo.ComponentVersionReference{
                    Name:      comp.ComponentVersionRef.Name,
                    Namespace: nc.Namespace,
                },
            })
        }
    }

    nc.Spec.Components = components
    return r.Update(ctx, nc)
}

func (r *NodeConfigReconciler) componentMatchesNodeRole(
    comp cvo.ReleaseComponent,
    roles []cvo.NodeRole,
) bool {
    if comp.Scope == cvo.ScopeCluster {
        return false
    }

    if len(comp.NodeSelector.Roles) == 0 {
        return true
    }

    for _, role := range roles {
        for _, selectorRole := range comp.NodeSelector.Roles {
            if role == selectorRole {
                return true
            }
        }
    }
    return false
}
```
#### 2.4 触发组件安装：triggerComponentInstallForNode
```go
func (r *NodeConfigReconciler) triggerComponentInstallForNode(
    ctx context.Context,
    nc *cvo.NodeConfig,
) {
    logger := ctrl.LoggerFrom(ctx)

    for _, comp := range nc.Spec.Components {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            logger.Error(err, "failed to get ComponentVersion",
                "componentName", comp.ComponentName, "cvName", cvName)
            continue
        }

        if cv.Status.NodeStatuses == nil {
            cv.Status.NodeStatuses = make(map[string]cvo.NodeComponentStatus)
        }

        if _, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]; exists {
            continue
        }

        cv.Status.NodeStatuses[nc.Spec.NodeName] = cvo.NodeComponentStatus{
            Phase:   cvo.ComponentPending,
            Version: comp.Version,
            Message: "triggered by NodeConfig creation",
        }

        if err := r.Status().Update(ctx, cv); err != nil {
            logger.Error(err, "failed to update ComponentVersion nodeStatuses",
                "componentName", comp.ComponentName, "nodeName", nc.Spec.NodeName)
            continue
        }

        r.Recorder.Eventf(cv, corev1.EventTypeNormal, "NodeAdded",
            "Node %s added, triggering install for component %s",
            nc.Spec.NodeName, comp.ComponentName)
    }
}

func (r *NodeConfigReconciler) resolveComponentVersionName(
    ctx context.Context,
    comp cvo.NodeComponent,
) string {
    if comp.ComponentVersionRef != nil {
        return comp.ComponentVersionRef.Name
    }
    return string(comp.ComponentName)
}
```
#### 2.5 安装中状态处理：handleInstalling
```go
func (r *NodeConfigReconciler) handleInstalling(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    allReady := true
    anyFailed := false

    for _, comp := range nc.Spec.Components {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            logger.Error(err, "failed to get ComponentVersion",
                "componentName", comp.ComponentName)
            allReady = false
            continue
        }

        nodeStatus, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]
        if !exists {
            allReady = false
            continue
        }

        nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
            Phase:   nodeStatus.Phase,
            Version: nodeStatus.Version,
            Message: nodeStatus.Message,
        }

        switch nodeStatus.Phase {
        case cvo.ComponentHealthy, cvo.ComponentInstalled:
            installedAt := metav1.Now()
            nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
                Phase:       nodeStatus.Phase,
                Version:     nodeStatus.Version,
                InstalledAt: &installedAt,
            }
        case cvo.ComponentDegraded, cvo.ComponentUpgradeFailed:
            anyFailed = true
            allReady = false
        default:
            allReady = false
        }
    }

    if allReady {
        nc.Status.Phase = cvo.NodeConfigReady
        nc.Status.LastOperation.Result = cvo.OperationSucceeded
        nc.Status.LastOperation.CompletedAt = &metav1.Time{Time: time.Now()}
        conditions.Set(nc, &cvo.NodeConfigCondition{
            Type:   cvo.NodeConfigAvailable,
            Status: corev1.ConditionTrue,
            Reason: "AllComponentsReady",
        })
        r.Recorder.Eventf(nc, corev1.EventTypeNormal, "InstallSucceeded",
            "All components installed on node %s", nc.Spec.NodeName)
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    if anyFailed {
        nc.Status.Phase = cvo.NodeConfigNotReady
        nc.Status.LastOperation.Result = cvo.OperationFailed
        nc.Status.LastOperation.Message = "one or more components failed to install"
        conditions.Set(nc, &cvo.NodeConfigCondition{
            Type:    cvo.NodeConfigAvailable,
            Status:  corev1.ConditionFalse,
            Reason:  "ComponentInstallFailed",
            Message: "one or more components failed to install",
        })
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    _ = r.Status().Update(ctx, nc)
    return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
}
```
#### 2.6 Ready 状态处理：handleReady（周期性状态聚合 + 版本变更检测）
```go
func (r *NodeConfigReconciler) handleReady(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    allHealthy := true
    needsUpgrade := false
    var upgradeComponents []string

    for _, comp := range nc.Spec.Components {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            logger.Error(err, "failed to get ComponentVersion",
                "componentName", comp.ComponentName)
            allHealthy = false
            continue
        }

        nodeStatus, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]
        if !exists {
            allHealthy = false
            continue
        }

        nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
            Phase:   nodeStatus.Phase,
            Version: nodeStatus.Version,
            Message: nodeStatus.Message,
        }

        if nodeStatus.Phase != cvo.ComponentHealthy && nodeStatus.Phase != cvo.ComponentInstalled {
            allHealthy = false
        }

        if comp.Version != nodeStatus.Version {
            needsUpgrade = true
            upgradeComponents = append(upgradeComponents, string(comp.ComponentName))
        }
    }

    if !allHealthy {
        nc.Status.Phase = cvo.NodeConfigNotReady
        conditions.Set(nc, &cvo.NodeConfigCondition{
            Type:    cvo.NodeConfigAvailable,
            Status:  corev1.ConditionFalse,
            Reason:  "ComponentNotHealthy",
            Message: "one or more components are not healthy",
        })
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    if needsUpgrade {
        logger.Info("component version drift detected, triggering upgrade",
            "nodeName", nc.Spec.NodeName,
            "components", strings.Join(upgradeComponents, ","))

        nc.Status.Phase = cvo.NodeConfigUpgrading
        nc.Status.LastOperation = &cvo.LastOperation{
            Type:      cvo.OperationUpgrade,
            StartedAt: &metav1.Time{Time: time.Now()},
            Message:   fmt.Sprintf("upgrading components: %s", strings.Join(upgradeComponents, ",")),
        }
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{Requeue: true}, nil
    }

    _ = r.Status().Update(ctx, nc)
    return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}
```
#### 2.7 升级状态处理：handleUpgrading
```go
func (r *NodeConfigReconciler) handleUpgrading(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    allReady := true
    anyFailed := false

    for _, comp := range nc.Spec.Components {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            allReady = false
            continue
        }

        nodeStatus, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]
        if !exists {
            allReady = false
            continue
        }

        nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
            Phase:   nodeStatus.Phase,
            Version: nodeStatus.Version,
            Message: nodeStatus.Message,
        }

        switch nodeStatus.Phase {
        case cvo.ComponentHealthy, cvo.ComponentInstalled:
            if comp.Version != nodeStatus.Version {
                allReady = false
            }
        case cvo.ComponentUpgrading, cvo.ComponentInstalling, cvo.ComponentPending:
            allReady = false
        case cvo.ComponentDegraded, cvo.ComponentUpgradeFailed:
            anyFailed = true
            allReady = false
        }
    }

    if allReady {
        nc.Status.Phase = cvo.NodeConfigReady
        nc.Status.LastOperation.Result = cvo.OperationSucceeded
        nc.Status.LastOperation.CompletedAt = &metav1.Time{Time: time.Now()}
        conditions.Set(nc, &cvo.NodeConfigCondition{
            Type:   cvo.NodeConfigAvailable,
            Status: corev1.ConditionTrue,
            Reason: "UpgradeSucceeded",
        })
        r.Recorder.Eventf(nc, corev1.EventTypeNormal, "UpgradeSucceeded",
            "All components upgraded on node %s", nc.Spec.NodeName)
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    if anyFailed {
        nc.Status.Phase = cvo.NodeConfigNotReady
        nc.Status.LastOperation.Result = cvo.OperationFailed
        nc.Status.LastOperation.Message = "one or more components failed to upgrade"
        conditions.Set(nc, &cvo.NodeConfigCondition{
            Type:    cvo.NodeConfigAvailable,
            Status:  corev1.ConditionFalse,
            Reason:  "ComponentUpgradeFailed",
            Message: "one or more components failed to upgrade",
        })
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    _ = r.Status().Update(ctx, nc)
    return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
}
```
#### 2.8 NotReady 状态处理：handleNotReady
```go
func (r *NodeConfigReconciler) handleNotReady(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    allHealthy := true

    for _, comp := range nc.Spec.Components {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            allHealthy = false
            continue
        }

        nodeStatus, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]
        if !exists {
            allHealthy = false
            continue
        }

        nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
            Phase:   nodeStatus.Phase,
            Version: nodeStatus.Version,
            Message: nodeStatus.Message,
        }

        if nodeStatus.Phase != cvo.ComponentHealthy && nodeStatus.Phase != cvo.ComponentInstalled {
            allHealthy = false
        }
    }

    if allHealthy {
        logger.Info("all components recovered, transitioning to Ready",
            "nodeName", nc.Spec.NodeName)
        nc.Status.Phase = cvo.NodeConfigReady
        nc.Status.Message = ""
        conditions.Set(nc, &cvo.NodeConfigCondition{
            Type:   cvo.NodeConfigAvailable,
            Status: corev1.ConditionTrue,
            Reason: "ComponentsRecovered",
        })
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    _ = r.Status().Update(ctx, nc)
    return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
}
```
#### 2.9 触发组件卸载 + 删除处理：handleDeletion / handleDeleting
```go
func (r *NodeConfigReconciler) handleDeletion(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    if !controllerutil.ContainsFinalizer(nc, nodeConfigFinalizer) {
        return ctrl.Result{}, nil
    }

    if nc.Status.Phase != cvo.NodeConfigDeleting {
        logger.Info("node being deleted, starting component uninstall",
            "nodeName", nc.Spec.NodeName)

        nc.Status.Phase = cvo.NodeConfigDeleting
        nc.Status.LastOperation = &cvo.LastOperation{
            Type:      cvo.OperationUninstall,
            StartedAt: &metav1.Time{Time: time.Now()},
        }
        _ = r.Status().Update(ctx, nc)

        r.triggerComponentUninstallForNode(ctx, nc)

        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    return r.handleDeleting(ctx, nc)
}

func (r *NodeConfigReconciler) triggerComponentUninstallForNode(
    ctx context.Context,
    nc *cvo.NodeConfig,
) {
    logger := ctrl.LoggerFrom(ctx)

    sortedComponents := r.sortComponentsByReverseDependency(ctx, nc)

    for _, comp := range sortedComponents {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            logger.Error(err, "failed to get ComponentVersion for uninstall",
                "componentName", comp.ComponentName)
            continue
        }

        if cv.Status.NodeStatuses == nil {
            continue
        }

        if _, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]; !exists {
            continue
        }

        cv.Status.NodeStatuses[nc.Spec.NodeName] = cvo.NodeComponentStatus{
            Phase:   cvo.ComponentUninstalling,
            Version: cv.Status.NodeStatuses[nc.Spec.NodeName].Version,
            Message: "triggered by NodeConfig deletion",
        }

        if err := r.Status().Update(ctx, cv); err != nil {
            logger.Error(err, "failed to update ComponentVersion for node uninstall",
                "componentName", comp.ComponentName, "nodeName", nc.Spec.NodeName)
            continue
        }

        r.Recorder.Eventf(cv, corev1.EventTypeNormal, "NodeRemoving",
            "Node %s being removed, triggering uninstall for component %s",
            nc.Spec.NodeName, comp.ComponentName)
    }
}

func (r *NodeConfigReconciler) sortComponentsByReverseDependency(
    ctx context.Context,
    nc *cvo.NodeConfig,
) []cvo.NodeComponent {
    depGraph := cvo.UpgradeDependencyGraph

    componentDeps := make(map[string][]string)
    for _, comp := range nc.Spec.Components {
        name := string(comp.ComponentName)
        if deps, ok := depGraph[cvo.ComponentName(name)]; ok {
            depNames := make([]string, 0, len(deps))
            for _, d := range deps {
                depNames = append(depNames, string(d))
            }
            componentDeps[name] = depNames
        } else {
            componentDeps[name] = nil
        }
    }

    sorted, err := topoSortReverse(componentDeps)
    if err != nil {
        ctrl.LoggerFrom(ctx).Error(err, "topological sort failed, using original order")
        return nc.Spec.Components
    }

    compMap := make(map[string]cvo.NodeComponent)
    for _, comp := range nc.Spec.Components {
        compMap[string(comp.ComponentName)] = comp
    }

    var result []cvo.NodeComponent
    for _, name := range sorted {
        if comp, ok := compMap[name]; ok {
            result = append(result, comp)
        }
    }
    return result
}

func topoSortReverse(deps map[string][]string) ([]string, error) {
    inDegree := make(map[string]int)
    for node := range deps {
        if _, ok := inDegree[node]; !ok {
            inDegree[node] = 0
        }
        for _, dep := range deps[node] {
            inDegree[dep]++
        }
    }

    var queue []string
    for node, degree := range inDegree {
        if degree == 0 {
            queue = append(queue, node)
        }
    }

    var sorted []string
    for len(queue) > 0 {
        node := queue[0]
        queue = queue[1:]
        sorted = append(sorted, node)
        for _, dep := range deps[node] {
            inDegree[dep]--
            if inDegree[dep] == 0 {
                queue = append(queue, dep)
            }
        }
    }

    if len(sorted) != len(deps) {
        return nil, fmt.Errorf("cycle detected in dependency graph")
    }

    var reversed []string
    for i := len(sorted) - 1; i >= 0; i-- {
        reversed = append(reversed, sorted[i])
    }
    return reversed, nil
}

func (r *NodeConfigReconciler) handleDeleting(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    allUninstalled := true

    for _, comp := range nc.Spec.Components {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            if apierrors.IsNotFound(err) {
                continue
            }
            allUninstalled = false
            continue
        }

        nodeStatus, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]
        if !exists {
            continue
        }

        nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
            Phase:   nodeStatus.Phase,
            Version: nodeStatus.Version,
            Message: nodeStatus.Message,
        }

        if nodeStatus.Phase != cvo.ComponentUninstalled && nodeStatus.Phase != cvo.ComponentPending {
            allUninstalled = false
        }
    }

    if !allUninstalled {
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    if err := r.triggerMachineDeletion(ctx, nc); err != nil {
        logger.Error(err, "failed to delete Machine",
            "nodeName", nc.Spec.NodeName)
        return ctrl.Result{}, err
    }

    controllerutil.RemoveFinalizer(nc, nodeConfigFinalizer)
    if err := r.Update(ctx, nc); err != nil {
        return ctrl.Result{}, err
    }

    logger.Info("node deleted and finalizer removed",
        "nodeName", nc.Spec.NodeName)
    return ctrl.Result{}, nil
}
```
#### 2.10 触发 cluster-api 扩缩容
```go
func (r *NodeConfigReconciler) triggerMachineCreation(
    ctx context.Context,
    nc *cvo.NodeConfig,
) error {
    logger := ctrl.LoggerFrom(ctx)

    clusterName, ok := nc.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return fmt.Errorf("cluster label not found on NodeConfig")
    }

    existingMachine, err := r.findMachineForNode(ctx, nc)
    if err != nil {
        return err
    }
    if existingMachine != nil {
        logger.Info("Machine already exists for node",
            "nodeName", nc.Spec.NodeName,
            "machineName", existingMachine.Name)
        return nil
    }

    isMaster := r.isMasterNode(nc)

    if isMaster {
        return r.createControlPlaneMachine(ctx, nc, clusterName)
    }
    return r.createWorkerMachine(ctx, nc, clusterName)
}

func (r *NodeConfigReconciler) createControlPlaneMachine(
    ctx context.Context,
    nc *cvo.NodeConfig,
    clusterName string,
) error {
    machineName := fmt.Sprintf("%s-%s", clusterName, nc.Spec.NodeName)

    kcp := &capiv1.KubeadmControlPlane{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      clusterName,
        Namespace: nc.Namespace,
    }, kcp); err != nil {
        return fmt.Errorf("get KubeadmControlPlane: %w", err)
    }

    desiredReplicas := int32(1)
    if kcp.Spec.Replicas != nil {
        desiredReplicas = *kcp.Spec.Replicas + 1
    }
    kcp.Spec.Replicas = &desiredReplicas

    if err := r.Update(ctx, kcp); err != nil {
        return fmt.Errorf("update KubeadmControlPlane replicas: %w", err)
    }

    r.Recorder.Eventf(nc, corev1.EventTypeNormal, "MachineScalingUp",
        "Increased KubeadmControlPlane replicas to %d for node %s",
        desiredReplicas, nc.Spec.NodeName)
    return nil
}

func (r *NodeConfigReconciler) createWorkerMachine(
    ctx context.Context,
    nc *cvo.NodeConfig,
    clusterName string,
) error {
    machineName := fmt.Sprintf("%s-%s", clusterName, nc.Spec.NodeName)

    mdList := &capiv1.MachineDeploymentList{}
    if err := r.List(ctx, mdList,
        client.InNamespace(nc.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return fmt.Errorf("list MachineDeployments: %w", err)
    }

    if len(mdList.Items) == 0 {
        return fmt.Errorf("no MachineDeployment found for cluster %s", clusterName)
    }

    md := mdList.Items[0]
    desiredReplicas := int32(1)
    if md.Spec.Replicas != nil {
        desiredReplicas = *md.Spec.Replicas + 1
    }
    md.Spec.Replicas = &desiredReplicas

    if err := r.Update(ctx, md); err != nil {
        return fmt.Errorf("update MachineDeployment replicas: %w", err)
    }

    r.Recorder.Eventf(nc, corev1.EventTypeNormal, "MachineScalingUp",
        "Increased MachineDeployment replicas to %d for node %s",
        desiredReplicas, nc.Spec.NodeName)
    return nil
}

func (r *NodeConfigReconciler) triggerMachineDeletion(
    ctx context.Context,
    nc *cvo.NodeConfig,
) error {
    logger := ctrl.LoggerFrom(ctx)
    isMaster := r.isMasterNode(nc)

    clusterName, ok := nc.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return fmt.Errorf("cluster label not found on NodeConfig")
    }

    if isMaster {
        kcp := &capiv1.KubeadmControlPlane{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      clusterName,
            Namespace: nc.Namespace,
        }, kcp); err != nil {
            return fmt.Errorf("get KubeadmControlPlane: %w", err)
        }

        if kcp.Spec.Replicas != nil && *kcp.Spec.Replicas > 1 {
            desiredReplicas := *kcp.Spec.Replicas - 1
            kcp.Spec.Replicas = &desiredReplicas
            if err := r.Update(ctx, kcp); err != nil {
                return fmt.Errorf("decrease KubeadmControlPlane replicas: %w", err)
            }
            r.Recorder.Eventf(nc, corev1.EventTypeNormal, "MachineScalingDown",
                "Decreased KubeadmControlPlane replicas to %d for node %s removal",
                desiredReplicas, nc.Spec.NodeName)
        }
    } else {
        mdList := &capiv1.MachineDeploymentList{}
        if err := r.List(ctx, mdList,
            client.InNamespace(nc.Namespace),
            client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
        ); err != nil {
            return fmt.Errorf("list MachineDeployments: %w", err)
        }

        if len(mdList.Items) > 0 {
            md := mdList.Items[0]
            if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
                desiredReplicas := *md.Spec.Replicas - 1
                md.Spec.Replicas = &desiredReplicas
                if err := r.Update(ctx, md); err != nil {
                    return fmt.Errorf("decrease MachineDeployment replicas: %w", err)
                }
                r.Recorder.Eventf(nc, corev1.EventTypeNormal, "MachineScalingDown",
                    "Decreased MachineDeployment replicas to %d for node %s removal",
                    desiredReplicas, nc.Spec.NodeName)
            }
        }
    }

    machine, err := r.findMachineForNode(ctx, nc)
    if err != nil {
        logger.Error(err, "failed to find Machine for node",
            "nodeName", nc.Spec.NodeName)
        return nil
    }
    if machine != nil {
        if err := r.Delete(ctx, machine); err != nil && !apierrors.IsNotFound(err) {
            return fmt.Errorf("delete Machine: %w", err)
        }
    }

    return nil
}

func (r *NodeConfigReconciler) findMachineForNode(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (*capiv1.Machine, error) {
    machineList := &capiv1.MachineList{}
    if err := r.List(ctx, machineList,
        client.InNamespace(nc.Namespace),
        client.MatchingLabels{
            "cluster.x-k8s.io/cluster-name": nc.Labels["cluster.x-k8s.io/cluster-name"],
        },
    ); err != nil {
        return nil, err
    }

    for i := range machineList.Items {
        m := &machineList.Items[i]
        if m.Spec.InfrastructureRef.Name == nc.Spec.NodeName ||
            strings.HasPrefix(m.Name, nc.Spec.NodeName) {
            return m, nil
        }
    }
    return nil, nil
}

func (r *NodeConfigReconciler) isMasterNode(nc *cvo.NodeConfig) bool {
    for _, role := range nc.Spec.Roles {
        if role == cvo.NodeRoleMaster {
            return true
        }
    }
    return false
}
```
#### 2.11 Watch 联动：SetupWithManager
```go
func (r *NodeConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&cvo.NodeConfig{}).
        Watches(
            &cvo.ComponentVersion{},
            handler.EnqueueRequestsFromMapFunc(r.componentVersionToNodeConfigs),
        ).
        Watches(
            &capiv1.Machine{},
            handler.EnqueueRequestsFromMapFunc(r.machineToNodeConfig),
        ).
        WithEventFilter(predicate.GenerationChangedPredicate{}).
        Complete(r)
}

func (r *NodeConfigReconciler) componentVersionToNodeConfigs(
    ctx context.Context,
    obj client.Object,
) []reconcile.Request {
    cv, ok := obj.(*cvo.ComponentVersion)
    if !ok {
        return nil
    }

    clusterName, ok := cv.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return nil
    }

    ncList := &cvo.NodeConfigList{}
    if err := r.List(ctx, ncList,
        client.InNamespace(cv.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return nil
    }

    var requests []reconcile.Request
    for _, nc := range ncList.Items {
        if _, exists := cv.Status.NodeStatuses[nc.Spec.NodeName]; exists {
            requests = append(requests, reconcile.Request{
                NamespacedName: types.NamespacedName{
                    Name:      nc.Name,
                    Namespace: nc.Namespace,
                },
            })
        }
    }
    return requests
}

func (r *NodeConfigReconciler) machineToNodeConfig(
    ctx context.Context,
    obj client.Object,
) []reconcile.Request {
    machine, ok := obj.(*capiv1.Machine)
    if !ok {
        return nil
    }

    clusterName, ok := machine.Labels["cluster.x-k8s.io/cluster-name"]
    if !ok {
        return nil
    }

    ncList := &cvo.NodeConfigList{}
    if err := r.List(ctx, ncList,
        client.InNamespace(machine.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterName},
    ); err != nil {
        return nil
    }

    for _, nc := range ncList.Items {
        if nc.Spec.NodeName == machine.Spec.InfrastructureRef.Name ||
            strings.HasPrefix(machine.Name, nc.Spec.NodeName) {
            return []reconcile.Request{
                {
                    NamespacedName: types.NamespacedName{
                        Name:      nc.Name,
                        Namespace: nc.Namespace,
                    },
                },
            }
        }
    }
    return nil
}
```
### 三、核心流程时序图
#### 3.1 扩容流程（新增节点）
```
用户创建 NodeConfig CR
    │
    ▼
NodeConfig Controller: Reconcile
    │
    ├── phase=Pending
    │   ├── populateComponentsFromRelease() → 从 ReleaseImage 填充组件列表
    │   ├── triggerMachineCreation() → 增加 MachineDeployment/ControlPlane replicas
    │   ├── triggerComponentInstallForNode() → 更新 ComponentVersion.nodeStatuses[新节点]
    │   └── phase=Installing
    │
    ▼
ComponentVersion Controller 检测到 nodeStatuses 新增节点
    │
    ├── 对新节点执行 installAction（通过 ActionEngine）
    ├── 更新 nodeStatuses[新节点].phase = Healthy
    │
    ▼
NodeConfig Controller: Watch ComponentVersion 变更
    │
    ├── handleInstalling() → 聚合所有组件状态
    │   ├── 所有组件 Healthy → phase=Ready
    │   └── 有组件未就绪 → requeue
    │
    └── phase=Ready → 扩容完成
```
#### 3.2 缩容流程（删除节点）
```
用户删除 NodeConfig CR（或标记 phase=Deleting）
    │
    ▼
NodeConfig Controller: handleDeletion
    │
    ├── phase=Deleting
    ├── sortComponentsByReverseDependency() → 按依赖逆序排列
    ├── triggerComponentUninstallForNode()
    │   └── 逐组件更新 ComponentVersion.nodeStatuses[节点].phase = Uninstalling
    │
    ▼
ComponentVersion Controller 检测到节点状态变更
    │
    ├── 对该节点执行 uninstallAction
    ├── 更新 nodeStatuses[节点].phase = Uninstalled
    │
    ▼
NodeConfig Controller: handleDeleting
    │
    ├── 检查所有组件 nodeStatuses[节点].phase == Uninstalled
    │   ├── 未全部完成 → requeue
    │   └── 全部完成 → 继续
    │
    ├── triggerMachineDeletion()
    │   ├── Master: 减少 KubeadmControlPlane replicas
    │   ├── Worker: 减少 MachineDeployment replicas
    │   └── 删除 Machine 对象
    │
    ├── 移除 Finalizer
    └── NodeConfig CR 被垃圾回收
```
#### 3.3 版本变更触发升级
```
ClusterVersion 更新 desiredVersion
    │
    ▼
ClusterVersion Controller 更新 ComponentVersion.spec.version
    │
    ▼
ComponentVersion Controller 执行升级
    │
    ▼
NodeConfig Controller: Watch ComponentVersion 变更
    │
    ├── handleReady() 检测到版本漂移
    │   └── phase=Upgrading
    │
    ├── handleUpgrading() 聚合组件升级状态
    │   ├── 所有组件升级完成 → phase=Ready
    │   ├── 有组件升级失败 → phase=NotReady
    │   └── 有组件升级中 → requeue
    │
    └── phase=Ready → 升级完成
```
### 四、关键设计要点总结
| 要点 | 设计 | 代码位置 |
|------|------|---------|
| **监听节点增删** | Watch NodeConfig CR 增删事件，新增时触发安装，删除时触发卸载 | §2.2, §2.3, §2.9 |
| **触发组件安装** | triggerComponentInstallForNode() 更新 ComponentVersion.nodeStatuses[新节点]=Pending，委托 ComponentVersion Controller 执行 | §2.4 |
| **触发组件卸载** | triggerComponentUninstallForNode() 按依赖逆序更新 nodeStatuses[节点]=Uninstalling，委托 ComponentVersion Controller 执行 | §2.9 |
| **更新节点组件状态** | 从 ComponentVersion.nodeStatuses[本节点] 聚合到 NodeConfig.status.componentStatus | §2.5, §2.6, §2.7, §2.8 |
| **触发 cluster-api 扩缩容** | triggerMachineCreation() 增加 replicas；triggerMachineDeletion() 减少 replicas + 删除 Machine | §2.10 |
| **依赖逆序卸载** | sortComponentsByReverseDependency() 使用拓扑排序逆序，确保被依赖组件最后卸载 | §2.9 |
| **Finalizer 保护** | 添加 Finalizer，删除时先卸载组件再删除 Machine，最后移除 Finalizer | §2.9 |
| **Watch 联动** | Watch ComponentVersion + Machine 变更，触发关联 NodeConfig Reconcile | §2.11 |
| **组件自动填充** | populateComponentsFromRelease() 根据 ReleaseImage + 节点角色自动填充组件列表 | §2.3 |
| **状态聚合** | NodeConfig 不独立维护组件状态，从 ComponentVersion.nodeStatuses 聚合，确保单一数据源 | §2.5-2.8 |
