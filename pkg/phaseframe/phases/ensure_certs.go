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
	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/certs"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

const (
	EnsureCertsName confv1beta1.BKEClusterPhase = "EnsureCerts"
)

type EnsureCerts struct {
	phaseframe.BasePhase

	certsGenerator *certs.BKEKubernetesCertGenerator
}

func NewEnsureCerts(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureCertsName)
	certsGenerator := certs.NewKubernetesCertGenerator(ctx.Context, ctx.Client, ctx.BKECluster)

	// Set nodes from PhaseContext for cert generation
	nodes, err := ctx.GetNodes()
	if err != nil {
		log.Warnf("EnsureCerts phase: failed to get nodes from context: %v", err)
	} else {
		certsGenerator.SetNodes(nodes)
	}

	return &EnsureCerts{
		BasePhase:      base,
		certsGenerator: certsGenerator,
	}
}

func (e *EnsureCerts) Execute() (ctrl.Result, error) {
	if err := e.certsGenerator.LookUpOrGenerate(); err != nil {
		return ctrl.Result{}, errors.Errorf("failed to generate certs, err: %v", err)
	}

	need, err := e.certsGenerator.NeedGenerate()
	if err != nil {
		return ctrl.Result{}, err
	}
	if need {
		return ctrl.Result{}, errors.Errorf("certs need generate again, err: %v", err)
	}

	return ctrl.Result{}, nil
}

func (e *EnsureCerts) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}
	need, err := e.certsGenerator.NeedGenerate()
	if err != nil || !need {
		return false
	}
	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}
