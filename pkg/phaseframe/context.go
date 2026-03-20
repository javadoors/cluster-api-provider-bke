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

package phaseframe

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

type PhaseContext struct {
	BKECluster *bkev1beta1.BKECluster
	Cluster    *clusterv1.Cluster
	client.Client
	context.Context
	Log        *bkev1beta1.BKELogger
	Scheme     *runtime.Scheme
	RestConfig *rest.Config
	cancelFunc context.CancelFunc

	mux         sync.Mutex
	nodeFetcher *nodeutil.NodeFetcher
}

func NewReconcilePhaseCtx(ctx context.Context) *PhaseContext {
	phaseCancelCtx, phaseCancel := context.WithCancel(ctx)
	return &PhaseContext{
		Context:    phaseCancelCtx,
		cancelFunc: phaseCancel,
		mux:        sync.Mutex{},
	}
}

func (pc *PhaseContext) SetBKECluster(bkeCluster *bkev1beta1.BKECluster) *PhaseContext {
	pc.BKECluster = bkeCluster
	return pc
}

func (pc *PhaseContext) SetCluster(cluster *clusterv1.Cluster) *PhaseContext {
	pc.Cluster = cluster
	return pc
}

func (pc *PhaseContext) SetClient(client client.Client) *PhaseContext {
	pc.Client = client
	return pc
}

func (pc *PhaseContext) SetLogger(log *bkev1beta1.BKELogger) *PhaseContext {
	pc.Log = log
	return pc
}

func (pc *PhaseContext) SetScheme(scheme *runtime.Scheme) *PhaseContext {
	pc.Scheme = scheme
	return pc
}

func (pc *PhaseContext) SetRestConfig(restConfig *rest.Config) *PhaseContext {
	pc.RestConfig = restConfig
	return pc
}

func (pc *PhaseContext) Untie() (context.Context, client.Client, *bkev1beta1.BKECluster, *runtime.Scheme, *bkev1beta1.BKELogger) {
	return pc.Context, pc.Client, pc.BKECluster, pc.Scheme, pc.Log
}

func (pc *PhaseContext) GetNewestBKECluster(customCtx ...context.Context) (*bkev1beta1.BKECluster, error) {
	var getCtx context.Context
	ctx, c, bkeCluster, _, _ := pc.Untie()
	if customCtx != nil && len(customCtx) != 0 {
		getCtx = customCtx[0]
	} else {
		getCtx = ctx
	}
	newBKECluster, err := mergecluster.GetCombinedBKECluster(getCtx, c, bkeCluster.Namespace, bkeCluster.Name)
	if err != nil {
		return nil, err
	}
	return newBKECluster, nil
}

func (pc *PhaseContext) RefreshCtxBKECluster(customCtx ...context.Context) error {
	pc.mux.Lock()
	defer pc.mux.Unlock()

	var ctx context.Context
	if customCtx != nil && len(customCtx) != 0 {
		ctx = customCtx[0]
	} else {
		ctx = pc.Context
	}
	newBKECluster, err := pc.GetNewestBKECluster(ctx)
	if err != nil {
		return err
	}
	pc.BKECluster = newBKECluster
	return nil
}

func (pc *PhaseContext) RefreshCtxCluster(customCtx ...context.Context) error {
	var refreshCtx context.Context
	if customCtx != nil && len(customCtx) != 0 {
		refreshCtx = customCtx[0]
	} else {
		refreshCtx = pc.Context
	}
	err := pc.RefreshCtxBKECluster(refreshCtx)
	if err != nil {
		return err
	}
	cluster, err := util.GetOwnerCluster(refreshCtx, pc.Client, pc.BKECluster.ObjectMeta)
	if err != nil {
		return errors.Wrapf(err, "failed to get owner cluster")
	}
	if cluster == nil {
		return errors.New("owner cluster is nil")
	}
	pc.Cluster = cluster
	return nil
}

func (pc *PhaseContext) Cancel() {
	pc.cancelFunc()
}

func (pc *PhaseContext) WatchBKEClusterStatus() {

	refreshTicker := time.NewTicker(2 * time.Second)
	defer refreshTicker.Stop()
	pausedTicker := time.NewTicker(10 * time.Second)
	defer pausedTicker.Stop()

	if pc.BKECluster == nil {
		pc.Log.Error("", "BKECluster is nil, cannot watch status")
		return
	}

	pc.mux.Lock()
	defer pc.mux.Unlock()
	bkeCluster := pc.BKECluster.DeepCopy()
	select {
	case <-refreshTicker.C:
		cluster, err := pc.GetNewestBKECluster()
		if err != nil {
			return
		}
		bkeCluster = cluster

	case <-pausedTicker.C:
		v, ok := annotation.HasAnnotation(bkeCluster, annotation.BKEClusterPauseAnnotationKey)
		flag := ok && v == "true"
		// 外部设置了暂停但是，还在运行phase，给个日志提示下吧
		if bkeCluster.Spec.Pause && !flag {
			// get running phase
			for _, phase := range bkeCluster.Status.PhaseStatus {
				if phase.Status == bkev1beta1.PhaseRunning {
					pc.Log.Info(constant.PhaseRunningReason, "BKECluster is paused, but phase %q is running, "+
						"waiting for phase to complete", bkeCluster.Status.Phase)
				}
			}
		}

	case <-pc.Done():
		return
	default:
		if bkeCluster.DeletionTimestamp != nil && bkeCluster.Status.ClusterStatus != bkev1beta1.ClusterDeleting {
			// mark bkeCluster as deleting
			bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterDeleting
			if err := mergecluster.SyncStatusUntilComplete(pc.Client, bkeCluster); err != nil {
				pc.Log.Warn(constant.ReconcileErrorReason, "failed to update bkeCluster Status: %v", err)
			}

			pc.Log.Info(constant.ClusterDeletingReason, "BKECluster is deleted, canceling phase context")
			pc.Cancel()
			return
		}

	}
}

// NodeFetcher 返回懒加载的 NodeFetcher 实例
func (pc *PhaseContext) NodeFetcher() *nodeutil.NodeFetcher {
	if pc.nodeFetcher == nil {
		pc.nodeFetcher = nodeutil.NewNodeFetcher(pc.Client)
	}
	return pc.nodeFetcher
}

// GetNodes 获取当前 BKECluster 关联的节点列表
func (pc *PhaseContext) GetNodes() (bkenode.Nodes, error) {
	return pc.NodeFetcher().GetNodesForBKECluster(pc.Context, pc.BKECluster)
}

// GetBKENodes 获取当前 BKECluster 关联的 BKENode 包装列表
func (pc *PhaseContext) GetBKENodes() (bkev1beta1.BKENodes, error) {
	return pc.NodeFetcher().GetBKENodesWrapperForCluster(pc.Context, pc.BKECluster)
}

// HasNodes 检查当前 BKECluster 是否有关联的节点
func (pc *PhaseContext) HasNodes() (bool, error) {
	count, err := pc.NodeFetcher().GetNodeCountForCluster(pc.Context, pc.BKECluster)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetNodeStateFlag 检查节点是否设置了指定标志
func (pc *PhaseContext) GetNodeStateFlag(ip string, flag int) (bool, error) {
	return pc.NodeFetcher().GetNodeStateFlagForCluster(pc.Context, pc.BKECluster, ip, flag)
}

// SetNodeStateWithMessage 设置节点状态和消息
func (pc *PhaseContext) SetNodeStateWithMessage(ip string, state confv1beta1.NodeState, message string) error {
	return pc.NodeFetcher().SetNodeStateWithMessageForCluster(pc.Context, pc.BKECluster, ip, state, message)
}

// SetNodeStateMessage 只设置节点消息（不改变状态）
func (pc *PhaseContext) SetNodeStateMessage(ip string, message string) error {
	bkeNode, err := pc.NodeFetcher().GetNodeByIP(pc.Context, pc.BKECluster.Namespace, pc.BKECluster.Name, ip)
	if err != nil {
		return err
	}
	bkeNode.Status.Message = message
	return pc.NodeFetcher().UpdateNodeStatus(pc.Context, bkeNode)
}
