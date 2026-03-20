/******************************************************************
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package phases

import (
	"context"

	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsurePausedName confv1beta1.BKEClusterPhase = "EnsurePaused"
)

type EnsurePaused struct {
	phaseframe.BasePhase
}

func NewEnsurePaused(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsurePausedName)
	return &EnsurePaused{
		BasePhase: base,
	}
}

// PauseOperationParams 包含暂停操作所需的参数
type PauseOperationParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Log        *bkev1beta1.BKELogger
}

func (e *EnsurePaused) ExecutePreHook() error {
	return e.BasePhase.DefaultPreHook()
}

func (e *EnsurePaused) Execute() (ctrl.Result, error) {
	if err := e.reconcilePause(); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (e *EnsurePaused) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	v, ok := annotation.HasAnnotation(e.Ctx.BKECluster, annotation.BKEClusterPauseAnnotationKey)
	flag := ok && v == "true"
	if new.Spec.Pause == flag {
		return false
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsurePaused) reconcilePause() error {
	_, c, bkeCluster, _, log := e.Ctx.Untie()
	// new context, avoid context cancel,when BKECluster is deleted
	ctx := context.Background()

	// 同步BKECluster暂停状态
	params := PauseOperationParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Log:        log,
	}
	if err := e.syncBKEClusterPauseStatus(params); err != nil {
		return err
	}

	// 暂停或恢复集群中的命令
	if err := e.pauseOrResumeCommands(params); err != nil {
		return err
	}

	// 暂停或恢复集群API对象
	return e.pauseOrResumeClusterAPIObjs(params)
}

// syncBKEClusterPauseStatus 同步BKECluster的暂停状态
func (e *EnsurePaused) syncBKEClusterPauseStatus(params PauseOperationParams) error {
	var patchF func(currentCombinedBKECluster *bkev1beta1.BKECluster)
	if params.BKECluster.Spec.Pause {
		patchF = func(currentCombinedBKECluster *bkev1beta1.BKECluster) {
			annotation.SetAnnotation(currentCombinedBKECluster, annotation.BKEClusterPauseAnnotationKey, "true")
		}
	} else {
		patchF = func(currentCombinedBKECluster *bkev1beta1.BKECluster) {
			annotation.RemoveAnnotation(currentCombinedBKECluster, annotation.BKEClusterPauseAnnotationKey)
		}
	}
	if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster, patchF); err != nil {
		return err
	}
	return nil
}

// pauseOrResumeCommands 暂停或恢复集群中的命令
func (e *EnsurePaused) pauseOrResumeCommands(params PauseOperationParams) error {
	commandLi := &agentv1beta1.CommandList{}
	filters := phaseutil.GetListFiltersByBKECluster(params.BKECluster)
	if err := params.Client.List(params.Ctx, commandLi, filters...); err != nil {
		params.Log.Error(constant.ReconcileErrorReason, "Failed to list command: %v", err)
		return errors.Errorf("failed to list command: %v", err)
	}

	// pause || resume all command in cluster
	for _, cmd := range commandLi.Items {
		if cmd.Spec.Suspend != params.BKECluster.Spec.Pause {
			cmd.Spec.Suspend = params.BKECluster.Spec.Pause
			if err := params.Client.Update(params.Ctx, &cmd); err != nil {
				params.Log.Warn(constant.ReconcileErrorReason, "Failed to Suspend command %q, err: %v", cmd.Name, err)
				continue
			}
		}
	}
	return nil
}

// pauseOrResumeClusterAPIObjs 暂停或恢复集群API对象
func (e *EnsurePaused) pauseOrResumeClusterAPIObjs(params PauseOperationParams) error {
	kcp, _ := phaseutil.GetClusterAPIKubeadmControlPlane(params.Ctx, params.Client, e.Ctx.Cluster)
	md, _ := phaseutil.GetClusterAPIMachineDeployment(params.Ctx, params.Client, e.Ctx.Cluster)

	if params.BKECluster.Spec.Pause {
		params.Log.Info(constant.BKEClusterPausedReason, "Cluster deploy %q is paused", params.BKECluster.Name)
		if kcp != nil {
			if err := phaseutil.PauseClusterAPIObj(params.Ctx, params.Client, kcp); err != nil {
				return err
			}
		}
		if md != nil {
			if err := phaseutil.PauseClusterAPIObj(params.Ctx, params.Client, md); err != nil {
				return err
			}
		}
	} else {
		params.Log.Info(constant.BKEClusterPausedReason, "Cluster deploy %q is resumed", params.BKECluster.Name)
		// do nothing if cluster in scale phase, upgrade phase
		if params.BKECluster.Status.Phase == bkev1beta1.Scale || params.BKECluster.Status.Phase == bkev1beta1.UpgradeControlPlane || params.BKECluster.Status.Phase == bkev1beta1.UpgradeWorker {
			return nil
		}
		if kcp != nil {
			if err := phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, kcp); err != nil {
				return err
			}
		}
		if md != nil {
			if err := phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, md); err != nil {
				return err
			}
		}
	}
	return nil
}
