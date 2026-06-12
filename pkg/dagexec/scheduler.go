/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package dagexec

import (
	"context"
	"fmt"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/manifest"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/topology"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	defaultComponentVersion   = "v1.0.0"
	defaultMaxParallelPerBatch = 8
)

// InlinePhaseRunner runs an inline upgrade handler registered in ComponentFactory.
type InlinePhaseRunner interface {
	Execute(phaseCtx *phaseframe.PhaseContext, oldCluster, newCluster *bkev1beta1.BKECluster, handler, version string) error
}

// Scheduler executes upgrade components according to a topological DAG.
type Scheduler struct {
	InlineRunner          InlinePhaseRunner
	ManifestStore         manifest.Store
	ManifestApplier       manifest.Applier
	MaxParallelPerBatch   int
}

// Config holds dependencies for DAG execution.
type Config struct {
	InlineRunner          InlinePhaseRunner
	ManifestStore         manifest.Store
	ManifestApplier       manifest.Applier
	// MaxParallelPerBatch limits concurrent components within one batch; 0 uses defaultMaxParallelPerBatch.
	MaxParallelPerBatch   int
}

// NewScheduler creates a scheduler with the given dependencies.
func NewScheduler(cfg Config) *Scheduler {
	maxParallel := cfg.MaxParallelPerBatch
	if maxParallel == 0 {
		maxParallel = defaultMaxParallelPerBatch
	}
	return &Scheduler{
		InlineRunner:        cfg.InlineRunner,
		ManifestStore:       cfg.ManifestStore,
		ManifestApplier:     cfg.ManifestApplier,
		MaxParallelPerBatch: maxParallel,
	}
}

type componentResult struct {
	name string
	node *topology.ComponentNode
	err  error
}

// ExecuteDAG runs all components in topological batches.
func (s *Scheduler) ExecuteDAG(
	ctx context.Context,
	phaseCtx *phaseframe.PhaseContext,
	oldCluster, newCluster *bkev1beta1.BKECluster,
	dag *topology.UpgradeDAG,
) error {
	if s == nil {
		return fmt.Errorf("dag scheduler is nil")
	}
	if dag == nil {
		return fmt.Errorf("upgrade DAG is nil")
	}
	if phaseCtx == nil {
		return fmt.Errorf("phase context is required")
	}

	if phaseCtx.VersionContext == nil {
		phaseCtx.BuildAndSetVersionContext()
	}
	batches, err := dag.TopologicalBatches()
	if err != nil {
		return err
	}

	tmpl := manifest.TemplateContext{
		ClusterName: phaseCtx.BKECluster.GetName(),
		Namespace:   phaseCtx.BKECluster.GetNamespace(),
	}
	if phaseCtx.BKECluster.Spec.ClusterConfig != nil {
		spec := phaseCtx.BKECluster.Spec.ClusterConfig.Cluster
		tmpl.KubernetesVersion = spec.KubernetesVersion
		tmpl.OpenFuyaoVersion = spec.OpenFuyaoVersion
	}

	var agg []error
	for batchIdx, batch := range batches {
		batchErrs, failFastStop := s.executeBatchParallel(
			ctx, phaseCtx, oldCluster, newCluster, batchIdx, batch, dag, tmpl,
		)
		if len(batchErrs) > 0 {
			agg = append(agg, batchErrs...)
		}
		if failFastStop {
			return kerrors.NewAggregate(agg)
		}
	}
	return kerrors.NewAggregate(agg)
}

func (s *Scheduler) executeBatchParallel(
	ctx context.Context,
	phaseCtx *phaseframe.PhaseContext,
	oldCluster, newCluster *bkev1beta1.BKECluster,
	batchIdx int,
	batch []string,
	dag *topology.UpgradeDAG,
	tmpl manifest.TemplateContext,
) (batchErrs []error, failFastStop bool) {
	type workItem struct {
		name string
		node *topology.ComponentNode
	}

	var items []workItem
	for _, compName := range batch {
		node, ok := dag.GetNode(compName)
		if !ok {
			batchErrs = append(batchErrs, fmt.Errorf("batch %d: component %q not found", batchIdx, compName))
			continue
		}
		if s.shouldSkipComponent(phaseCtx, node) {
			continue
		}
		items = append(items, workItem{name: compName, node: node})
	}

	if len(items) == 0 {
		return batchErrs, false
	}

	results := make([]componentResult, len(items))
	g, batchCtx := errgroup.WithContext(ctx)
	parallelLimit := s.maxParallel(len(items))
	sem := make(chan struct{}, parallelLimit)
	var activeWorkers atomic.Int32

	s.logBatchParallel(phaseCtx, "batch start, index=%d, batch_size=%d, runnable=%d, parallel_limit=%d", batchIdx, len(batch), len(items), parallelLimit)

	for i, item := range items {
		i, item := i, item
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-batchCtx.Done():
				return batchCtx.Err()
			}

			active := activeWorkers.Add(1)
			s.logBatchParallel(phaseCtx, "component start, batch=%d, component=%s, active_workers=%d", batchIdx, item.name, active)
			err := s.executeComponent(batchCtx, phaseCtx, oldCluster, newCluster, item.node, tmpl)
			results[i] = componentResult{name: item.name, node: item.node, err: err}
			active = activeWorkers.Add(-1)
			s.logBatchParallel(phaseCtx, "component done, batch=%d, component=%s, active_workers=%d, has_error=%t", batchIdx, item.name, active, err != nil)

			if err == nil || manifest.IsSkipNotInstalled(err) {
				return nil
			}
			if item.node.FailurePolicy == topology.FailurePolicyFailFast {
				return err
			}
			return nil
		})
	}

	_ = g.Wait()
	s.logBatchParallel(phaseCtx, "batch done, index=%d, batch_size=%d, runnable=%d", batchIdx, len(batch), len(items))

	return s.persistBatchResults(phaseCtx, results, batchErrs)
}

func (s *Scheduler) logBatchParallel(phaseCtx *phaseframe.PhaseContext, format string, args ...interface{}) {
	if phaseCtx == nil || phaseCtx.Log == nil {
		return
	}
	phaseCtx.Log.Info(constant.ComponentUpgradingReason, format, args...)
}

func (s *Scheduler) persistBatchResults(
	phaseCtx *phaseframe.PhaseContext,
	results []componentResult,
	batchErrs []error,
) ([]error, bool) {
	var failFastStop bool
	for _, r := range results {
		if r.node == nil {
			continue
		}
		compName := r.name
		if r.err != nil {
			if manifest.IsSkipNotInstalled(r.err) {
				continue
			}
			if persistErr := s.markComponentFailed(phaseCtx, r.node, r.err); persistErr != nil {
				batchErrs = append(batchErrs, fmt.Errorf("%s: persist failure: %w", compName, persistErr))
				if r.node.FailurePolicy == topology.FailurePolicyFailFast {
					failFastStop = true
				}
			}
			batchErrs = append(batchErrs, fmt.Errorf("%s: %w", compName, r.err))
			if r.node.FailurePolicy == topology.FailurePolicyFailFast {
				failFastStop = true
			}
			continue
		}
		if err := s.markComponentCompleted(phaseCtx, r.node); err != nil {
			batchErrs = append(batchErrs, fmt.Errorf("%s: persist completion: %w", compName, err))
			if r.node.FailurePolicy == topology.FailurePolicyFailFast {
				failFastStop = true
			}
		}
	}
	return batchErrs, failFastStop
}

func (s *Scheduler) maxParallel(batchLen int) int {
	if batchLen <= 0 {
		return 1
	}
	limit := s.MaxParallelPerBatch
	if limit <= 0 {
		limit = defaultMaxParallelPerBatch
	}
	if limit > batchLen {
		return batchLen
	}
	return limit
}

func (s *Scheduler) nodeVersionKey(node *topology.ComponentNode) string {
	if node == nil {
		return defaultComponentVersion
	}
	if node.Inline != nil {
		if node.Inline.Version != "" {
			return node.Inline.Version
		}
		return defaultComponentVersion
	}
	if node.Version != "" {
		return node.Version
	}
	return defaultComponentVersion
}

func (s *Scheduler) shouldSkipComponent(phaseCtx *phaseframe.PhaseContext, node *topology.ComponentNode) bool {
	if phaseCtx == nil || phaseCtx.BKECluster == nil || node == nil {
		return false
	}
	st := phaseCtx.BKECluster.Status.DeclarativeUpgrade
	if st == nil {
		return false
	}
	return st.IsCompleted(node.Name, s.nodeVersionKey(node))
}

func (s *Scheduler) markComponentCompleted(phaseCtx *phaseframe.PhaseContext, node *topology.ComponentNode) error {
	if phaseCtx == nil || phaseCtx.BKECluster == nil || phaseCtx.Client == nil || node == nil {
		return nil
	}
	return mergecluster.SyncStatusUntilComplete(phaseCtx.Client, phaseCtx.BKECluster, func(bc *bkev1beta1.BKECluster) {
		if bc.Status.DeclarativeUpgrade == nil {
			return
		}
		bc.Status.DeclarativeUpgrade.MarkCompleted(node.Name, s.nodeVersionKey(node), metav1.Now())
		// Clear last error on successful component execution.
		bc.Status.DeclarativeUpgrade.LastError = ""
		bc.Status.DeclarativeUpgrade.ClearFailure()
	})
}

func (s *Scheduler) markComponentFailed(phaseCtx *phaseframe.PhaseContext, node *topology.ComponentNode, err error) error {
	if phaseCtx == nil {
		return nil
	}
	if phaseCtx.BKECluster == nil {
		return nil
	}
	if phaseCtx.Client == nil {
		return nil
	}
	if node == nil {
		return nil
	}
	if err == nil {
		return nil
	}
	return mergecluster.SyncStatusUntilComplete(phaseCtx.Client, phaseCtx.BKECluster, func(bc *bkev1beta1.BKECluster) {
		if bc.Status.DeclarativeUpgrade == nil {
			return
		}
		bc.Status.DeclarativeUpgrade.MarkFailure(node.Name, s.nodeVersionKey(node), err.Error(), metav1.Now())
	})
}

func (s *Scheduler) executeComponent(
	ctx context.Context,
	phaseCtx *phaseframe.PhaseContext,
	oldCluster, newCluster *bkev1beta1.BKECluster,
	node *topology.ComponentNode,
	tmpl manifest.TemplateContext,
) error {
	if node.Inline != nil {
		return s.executeInline(phaseCtx, oldCluster, newCluster, node)
	}
	return s.executeManifest(ctx, phaseCtx, node, tmpl)
}

func (s *Scheduler) executeInline(
	phaseCtx *phaseframe.PhaseContext,
	oldCluster, newCluster *bkev1beta1.BKECluster,
	node *topology.ComponentNode,
) error {
	if s.InlineRunner == nil {
		return fmt.Errorf("inline phase runner is nil")
	}
	handler := node.Inline.Handler
	version := node.Inline.Version
	if handler == "" {
		return fmt.Errorf("inline component %q missing handler", node.Name)
	}
	if version == "" {
		version = defaultComponentVersion
	}
	return s.InlineRunner.Execute(phaseCtx, oldCluster, newCluster, handler, version)
}

// manifestNeedsUpgrade mirrors inline NeedExecuteWithVersionContext: when VersionContext
// has a target for the component, skip manifest apply if current already matches target.
func manifestNeedsUpgrade(phaseCtx *phaseframe.PhaseContext, componentName string) bool {
	if phaseCtx == nil || phaseCtx.VersionContext == nil {
		return true
	}
	vc := phaseCtx.VersionContext
	if !vc.HasTarget(componentName) {
		return true
	}
	return vc.NeedsUpgrade(componentName)
}

func (s *Scheduler) executeManifest(
	ctx context.Context,
	phaseCtx *phaseframe.PhaseContext,
	node *topology.ComponentNode,
	tmpl manifest.TemplateContext,
) error {
	if node == nil {
		return fmt.Errorf("component node is nil")
	}
	if !manifestNeedsUpgrade(phaseCtx, node.Name) {
		return nil
	}
	version := node.Version
	if version == "" {
		version = defaultComponentVersion
	}
	if s.ManifestStore == nil {
		return fmt.Errorf("manifest store is not configured")
	}
	pkg, err := s.ManifestStore.GetComponentManifests(ctx, node.Name, version, tmpl)
	if err != nil {
		return err
	}
	if len(pkg.Manifests) == 0 {
		return fmt.Errorf("component %q version %q has no manifests to apply", node.Name, version)
	}
	if s.ManifestApplier == nil {
		return fmt.Errorf("manifest applier is not configured")
	}
	return s.ManifestApplier.ApplyComponent(ctx, pkg)
}

// RequeueAwareError reports whether the reconcile should requeue.
func RequeueAwareError(err error) (ctrl.Result, bool) {
	if err == nil {
		return ctrl.Result{}, false
	}
	return ctrl.Result{}, true
}
