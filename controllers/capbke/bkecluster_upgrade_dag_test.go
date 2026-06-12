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

package capbke

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	cvv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/featuregate"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/topology"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/upgrade"
)

func TestShouldUseDeclarativeUpgrade(t *testing.T) {
	r := &BKEClusterReconciler{}

	bc := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				featuregate.UpgradeReadyAnnotationKey: "v2.5.0",
			},
		},
	}
	if !r.shouldUseDeclarativeUpgrade(bc) {
		t.Fatal("expected declarative path when upgrade-ready is set")
	}

	if r.shouldUseDeclarativeUpgrade(&bkev1beta1.BKECluster{}) {
		t.Fatal("expected no declarative path without upgrade-ready")
	}
}

func TestIsReleaseImageNotReady(t *testing.T) {
	if !isReleaseImageNotReady(&releaseImagePendingError{msg: "pending"}) {
		t.Fatal("expected pending detection")
	}
	if isReleaseImageNotReady(fmt.Errorf("other")) {
		t.Fatal("unexpected pending detection")
	}
}

func TestReleaseRefFromCRUsesVersionCacheKeyWhenSpecDigestEmpty(t *testing.T) {
	ri := &cvv1alpha1.ReleaseImage{
		Spec: cvv1alpha1.ReleaseImageSpec{
			Version: "openfuyao-v26.06",
		},
		Status: cvv1alpha1.ReleaseImageStatus{
			Digest: "sha256:d286a2c213244ef9b4581b2464c60a6198966c7a72764f1c9a1736f391394b8a",
		},
	}

	ref := releaseRefFromCR(ri)
	assert.Equal(t, "openfuyao-v26.06", ref.Version)
	assert.Empty(t, ref.Digest)
	assert.Equal(t, "openfuyao-v26.06", ref.CacheKey())
}

func TestReleaseRefFromCRUsesSpecDigestCacheKeyWhenSet(t *testing.T) {
	ri := &cvv1alpha1.ReleaseImage{
		Spec: cvv1alpha1.ReleaseImageSpec{
			Version: "v26.06",
			Digest:  "sha256:abc",
		},
	}

	ref := releaseRefFromCR(ri)
	assert.Equal(t, "sha256-abc", ref.CacheKey())
}

func TestDeclarativeUpgradePhaseName(t *testing.T) {
	node := &topology.ComponentNode{
		Name:   upgrade.ComponentEtcd,
		Inline: &topology.InlineRef{Handler: upgrade.InlineHandlerEtcdUpgrade},
	}
	if got := declarativeUpgradePhaseName(node); string(got) != upgrade.InlineHandlerEtcdUpgrade {
		t.Fatalf("got %s", got)
	}
}
