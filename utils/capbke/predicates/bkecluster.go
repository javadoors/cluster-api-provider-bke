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

package predicates

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
	l "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

func BKEAgentReady() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newObj, ok := e.ObjectNew.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}
			if newObj != nil {
				angent := condition.HasConditionStatus(bkev1beta1.BKEAgentCondition, newObj, confv1beta1.ConditionTrue) && condition.HasConditionStatus(bkev1beta1.NodesEnvCondition, newObj, confv1beta1.ConditionTrue)
				requeue := condition.HasConditionStatus(bkev1beta1.TargetClusterBootCondition, newObj, confv1beta1.ConditionFalse)
				return angent || requeue
			}
			return false
		},
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func BKEClusterUnPause() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newObj, ok := e.ObjectNew.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}
			return !newObj.Spec.Pause
		},
		CreateFunc: func(e event.CreateEvent) bool {
			obj, ok := e.Object.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}
			return !obj.Spec.Pause
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func BKEClusterSpecChange() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newObj, ok := e.ObjectNew.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}
			oldObj, ok := e.ObjectOld.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}
			log := l.With("bkecluster", utils.ClientObjNS(newObj))

			if newObj != nil && oldObj != nil {
				if newObj.Generation != oldObj.Generation {
					patches, _ := mergecluster.GetCurrentBkeClusterPatches(oldObj.DeepCopy(), newObj.DeepCopy())
					for _, patch := range patches {
						kind := patch.Kind()
						path, _ := patch.Path()
						value, _ := patch.ValueInterface()
						log.Debugf("%s path: %s, value: %v", kind, path, value)
					}
					log.Debugf("cluster spec change, generation: %d, new: %d, old: %d", newObj.Generation, newObj.Generation, oldObj.Generation)

					if !newObj.ObjectMeta.DeletionTimestamp.IsZero() || !oldObj.ObjectMeta.DeletionTimestamp.IsZero() {
						log.Infof("bkecluster 正在删除")
						return true
					}

					if oldObj.Spec.Pause != newObj.Spec.Pause {
						log.Infof("集群暂停状态变更 %v -> %v", oldObj.Spec.Pause, newObj.Spec.Pause)
						return true
					}

					if config.EnableInternalUpdate {
						if _, ok := condition.HasCondition(bkev1beta1.InternalSpecChangeCondition, newObj); ok {
							log.Infof("内部修改spec内容，跳过入队")
							return false
						}
					}

					// 如果在deploying中，所有更新不入队
					if newObj.Status.ClusterHealthState == bkev1beta1.Deploying {
						log.Debugf("集群状态为deploying，跳过入队")
						return false
					}

					return true
				}
			}

			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			obj, ok := e.Object.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}
			return obj != nil
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

func BKEClusterAnnotationsChange() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newObj, ok := e.ObjectNew.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}
			oldObj, ok := e.ObjectOld.(*bkev1beta1.BKECluster)
			if !ok {
				return false
			}

			allowChangeAnnotations := []string{
				annotation.AppointmentDeletedNodesAnnotationKey,
				annotation.AppointmentAddNodesAnnotationKey,
				annotation.RetryAnnotationKey,
				annotation.ClusterTrackerHealthyCheckFailedAnnotationKey,
			}
			log := l.With("bkecluster", utils.ClientObjNS(newObj))
			for _, key := range allowChangeAnnotations {
				newV, newFound := annotation.HasAnnotation(newObj, key)
				oldV, oldFound := annotation.HasAnnotation(oldObj, key)
				if (newV != oldV) || (newFound && !oldFound) {
					log.Infof("cluster annotations change, key: %s, new: %v, old: %v", key, newV, oldV)
					return true
				}
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// BKENodeChange returns a predicate that allows BKENode create/update/delete events to trigger reconciliation
func BKENodeChange() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			obj, ok := e.Object.(*confv1beta1.BKENode)
			if !ok {
				return false
			}
			log := l.With("bkenode", utils.ClientObjNS(obj))
			log.Infof("BKENode 创建，触发调谐")
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newObj, ok := e.ObjectNew.(*confv1beta1.BKENode)
			if !ok {
				return false
			}
			oldObj, ok := e.ObjectOld.(*confv1beta1.BKENode)
			if !ok {
				return false
			}
			log := l.With("bkenode", utils.ClientObjNS(newObj))

			// If generation changed, spec has changed
			if newObj.Generation != oldObj.Generation {
				log.Infof("BKENode Spec 变更，触发调谐, generation: %d -> %d", oldObj.Generation, newObj.Generation)
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			obj, ok := e.Object.(*confv1beta1.BKENode)
			if !ok {
				return false
			}
			log := l.With("bkenode", utils.ClientObjNS(obj))
			log.Infof("BKENode 删除，触发调谐")
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}
