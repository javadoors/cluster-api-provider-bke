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

package clusterversion

const (
	// AnnotationUpgradeReady is set on BKECluster when an upgrade hop is approved.
	AnnotationUpgradeReady = "cvo.openfuyao.cn/upgrade-ready"
	// AnnotationClusterVersion links BKECluster to ClusterVersion during upgrade.
	AnnotationClusterVersion = "cvo.openfuyao.cn/cluster-version"
	// AnnotationUpgradePath records the validated upgrade path on BKECluster.
	AnnotationUpgradePath = "cvo.openfuyao.cn/upgrade-path"
)
