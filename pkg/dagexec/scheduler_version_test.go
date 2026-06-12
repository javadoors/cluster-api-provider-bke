/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
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
	"testing"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/manifest"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/topology"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/upgrade"
)

func TestManifestNeedsUpgrade(t *testing.T) {
	t.Parallel()

	vc := upgrade.NewVersionContext()
	vc.SetTarget(upgrade.ComponentCoreDNS, "v1.0.0")
	vc.SetCurrent(upgrade.ComponentCoreDNS, "v1.0.0")

	pc := phaseframe.NewReconcilePhaseCtx(context.Background())
	pc.SetVersionContext(vc)

	if manifestNeedsUpgrade(pc, upgrade.ComponentCoreDNS) {
		t.Fatal("expected false when current equals target")
	}
	if !manifestNeedsUpgrade(pc, upgrade.ComponentKubeProxy) {
		t.Fatal("expected true when component has no target in VersionContext")
	}
	if !manifestNeedsUpgrade(nil, upgrade.ComponentCoreDNS) {
		t.Fatal("expected true when phase context is nil")
	}

	vc.SetCurrent(upgrade.ComponentCoreDNS, "v0.9.0")
	if !manifestNeedsUpgrade(pc, upgrade.ComponentCoreDNS) {
		t.Fatal("expected true when current differs from target")
	}
}

type countingManifestApplier struct {
	calls atomic.Int32
}

func (a *countingManifestApplier) ApplyComponent(context.Context, *manifest.ComponentPackage) error {
	a.calls.Add(1)
	return nil
}

func TestExecuteManifest_SkipsApplyWhenVersionMatched(t *testing.T) {
	phaseCtx := phaseframe.NewReconcilePhaseCtx(context.Background())
	vc := upgrade.NewVersionContext()
	vc.SetTarget(upgrade.ComponentCoreDNS, "v1.0.0")
	vc.SetCurrent(upgrade.ComponentCoreDNS, "v1.0.0")
	phaseCtx.SetVersionContext(vc)

	applier := &countingManifestApplier{}
	sched := NewScheduler(Config{
		ManifestStore:   skipManifestStore{},
		ManifestApplier: applier,
	})

	node := &topology.ComponentNode{Name: upgrade.ComponentCoreDNS, Version: "v1.0.0"}
	if err := sched.executeManifest(context.Background(), phaseCtx, node, manifest.TemplateContext{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applier.calls.Load() != 0 {
		t.Fatalf("expected manifest applier not called, got %d calls", applier.calls.Load())
	}
}

func TestExecuteManifest_AppliesWhenVersionDiffers(t *testing.T) {
	phaseCtx := phaseframe.NewReconcilePhaseCtx(context.Background())
	vc := upgrade.NewVersionContext()
	vc.SetTarget(upgrade.ComponentCoreDNS, "v1.0.0")
	vc.SetCurrent(upgrade.ComponentCoreDNS, "v0.9.0")
	phaseCtx.SetVersionContext(vc)

	applier := &countingManifestApplier{}
	sched := NewScheduler(Config{
		ManifestStore:   skipManifestStore{},
		ManifestApplier: applier,
	})

	node := &topology.ComponentNode{Name: upgrade.ComponentCoreDNS, Version: "v1.0.0"}
	if err := sched.executeManifest(context.Background(), phaseCtx, node, manifest.TemplateContext{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applier.calls.Load() != 1 {
		t.Fatalf("expected manifest applier called once, got %d", applier.calls.Load())
	}
}

type failManifestApplier struct{}

func (failManifestApplier) ApplyComponent(context.Context, *manifest.ComponentPackage) error {
	return fmt.Errorf("apply should not run")
}

func TestExecuteManifest_VersionMatched_DoesNotReachApplier(t *testing.T) {
	phaseCtx := phaseframe.NewReconcilePhaseCtx(context.Background())
	vc := upgrade.NewVersionContext()
	vc.SetTarget(upgrade.ComponentProvider, "v1.0.0")
	vc.SetCurrent(upgrade.ComponentProvider, "v1.0.0")
	phaseCtx.SetVersionContext(vc)

	sched := NewScheduler(Config{
		ManifestStore:   skipManifestStore{},
		ManifestApplier: failManifestApplier{},
	})

	node := &topology.ComponentNode{Name: upgrade.ComponentProvider, Version: "v1.0.0"}
	if err := sched.executeManifest(context.Background(), phaseCtx, node, manifest.TemplateContext{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
