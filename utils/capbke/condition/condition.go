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

package condition

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

// ConditionMark add or update condition to BKECluster
func ConditionMark(bkeCluster *bkev1beta1.BKECluster, conditionType confv1beta1.ClusterConditionType, conditionStatus confv1beta1.ConditionStatus, reason, message string) {
	AddonConditionMark(bkeCluster, conditionType, conditionStatus, reason, message, "")
}

func AddonConditionMark(bkeCluster *bkev1beta1.BKECluster, conditionType confv1beta1.ClusterConditionType, conditionStatus confv1beta1.ConditionStatus, reason, message, addonName string) {
	if bkeCluster.Status.Conditions == nil {
		bkeCluster.Status.Conditions = []confv1beta1.ClusterCondition{}
	}
	now := metav1.Now()

	if addonName != "" && conditionType == bkev1beta1.ClusterAddonCondition {
		if condition, ok := HasAddonCondition(bkeCluster, addonName); ok {
			condition.Status = conditionStatus
			condition.Reason = reason
			condition.Message = message
			condition.LastTransitionTime = &now
			for i, c := range bkeCluster.Status.Conditions {
				if c.Type == conditionType {
					bkeCluster.Status.Conditions[i] = *condition
				}
			}
			return
		}
	}

	// update condition
	if condition, ok := HasCondition(conditionType, bkeCluster); ok {
		condition.LastTransitionTime = &now
		condition.Reason = reason
		condition.Message = message
		condition.Status = conditionStatus
		condition.AddonName = addonName
		for i, c := range bkeCluster.Status.Conditions {
			if c.Type == conditionType {
				bkeCluster.Status.Conditions[i] = *condition
			}
		}
		return
	}

	// new condition
	condition := confv1beta1.ClusterCondition{
		Type:               conditionType,
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		AddonName:          addonName,
		LastTransitionTime: &now,
	}
	bkeCluster.Status.Conditions = append(bkeCluster.Status.Conditions, condition)
}

func HasCondition(conditionType confv1beta1.ClusterConditionType, bkeCluster *bkev1beta1.BKECluster) (*confv1beta1.ClusterCondition, bool) {
	if bkeCluster.Status.Conditions == nil {
		return nil, false
	}

	for _, condition := range bkeCluster.Status.Conditions {
		if condition.Type == conditionType {
			return &condition, true
		}
	}

	return nil, false
}

func HasAddonCondition(bkeCluster *bkev1beta1.BKECluster, addonName string) (*confv1beta1.ClusterCondition, bool) {
	if bkeCluster.Status.Conditions == nil {
		return nil, false
	}
	for _, condition := range bkeCluster.Status.Conditions {
		if condition.Type == bkev1beta1.ClusterAddonCondition && condition.AddonName == addonName {
			return &condition, true
		}
	}
	return nil, false
}

func HasConditionStatus(conditionType confv1beta1.ClusterConditionType, bkeCluster *bkev1beta1.BKECluster, conditionStatus confv1beta1.ConditionStatus) bool {
	condition, ok := HasCondition(conditionType, bkeCluster)
	if !ok {
		return false
	}
	return condition.Status == conditionStatus
}

func RemoveCondition(conditionType confv1beta1.ClusterConditionType, bkeCluster *bkev1beta1.BKECluster) {
	conditions := bkeCluster.Status.Conditions
	if conditions == nil {
		return
	}

	indexToRemove := -1

	for i, condition := range conditions {
		if condition.Type == conditionType {
			indexToRemove = i
			break
		}
	}

	if indexToRemove != -1 {
		conditions = append(conditions[:indexToRemove], conditions[indexToRemove+1:]...)
		bkeCluster.Status.Conditions = conditions
	}
}
