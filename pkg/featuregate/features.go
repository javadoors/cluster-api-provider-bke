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

package featuregate

import (
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
)

const (
	// DeclarativeUpgradeAnnotationKey enables declarative DAG upgrade on a BKECluster (legacy; gate uses upgrade-ready only).
	DeclarativeUpgradeAnnotationKey = "cvo.openfuyao.cn/declarative-upgrade"
	// UpgradeReadyAnnotationKey is set by ClusterVersionReconciler when an upgrade should run.
	UpgradeReadyAnnotationKey = annotation.CVOUpgradeReadyAnnotationKey
)

// DeclarativeUpgradeEnabled reports whether declarative upgrade DAG should run.
// Global flag --declarative-upgrade or per-cluster annotation overrides.
func DeclarativeUpgradeEnabled(obj client.Object) bool {
	if config.DeclarativeUpgrade {
		return true
	}
	if obj == nil {
		return false
	}
	v, ok := annotation.HasAnnotation(obj, DeclarativeUpgradeAnnotationKey)
	return ok && strings.EqualFold(strings.TrimSpace(v), "true")
}

// UpgradeReady reports whether ClusterVersionReconciler requested a declarative upgrade.
func UpgradeReady(obj client.Object) (string, bool) {
	if obj == nil {
		return "", false
	}
	v, ok := annotation.HasAnnotation(obj, UpgradeReadyAnnotationKey)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}
