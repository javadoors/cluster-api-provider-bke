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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
)

func TestDeclarativeUpgradeEnabled(t *testing.T) {
	config.DeclarativeUpgrade = false
	bc := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			DeclarativeUpgradeAnnotationKey: "true",
		}},
	}
	if !DeclarativeUpgradeEnabled(bc) {
		t.Fatal("annotation should enable")
	}

	bc.Annotations[DeclarativeUpgradeAnnotationKey] = "false"
	if DeclarativeUpgradeEnabled(bc) {
		t.Fatal("false annotation should not enable without global flag")
	}

	config.DeclarativeUpgrade = true
	if !DeclarativeUpgradeEnabled(&bkev1beta1.BKECluster{}) {
		t.Fatal("global flag should enable")
	}
	config.DeclarativeUpgrade = false
}

func TestUpgradeReady(t *testing.T) {
	bc := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			UpgradeReadyAnnotationKey: "v2.6.0",
		}},
	}
	v, ok := UpgradeReady(bc)
	if !ok || v != "v2.6.0" {
		t.Fatalf("got %q ok=%v", v, ok)
	}
	if _, ok := UpgradeReady(&bkev1beta1.BKECluster{}); ok {
		t.Fatal("expected not ready")
	}
}
