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

package record

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkemetrics "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/metrics"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

func NodeBootstrapFailedCountRecord(obj client.Object) {
	if config.MetricsAddr == "0" {
		return
	}
	bkemetrics.MetricRegister.RecordCounterVec(
		bkemetrics.NodeBootstrapFailedCount,
		utils.ClientObjNS(obj),
	).Inc()

}

func NodeBootstrapSuccessCountRecord(obj client.Object) {
	if config.MetricsAddr == "0" {
		return
	}
	bkemetrics.MetricRegister.RecordCounterVec(
		bkemetrics.NodeBootstrapSuccessCount,
		utils.ClientObjNS(obj),
	).Inc()
}

func NodeBootstrapDurationRecord(obj client.Object, node confv1beta1.Node, startTime time.Time, info string) {
	if config.MetricsAddr == "0" {
		return
	}

	key := utils.ClientObjNS(obj)

	bkemetrics.MetricRegister.RecordGaugeVec(
		bkemetrics.NodeBootstrapDurationSeconds,
		key,
		phaseutil.NodeInfo(node),
		phaseutil.NodeRoleString(node),
		info,
		bkemetrics.FormatTime(startTime),
		bkemetrics.TimeNow(),
	).Set(time.Since(startTime).Seconds())

	if config.E2EMode {
		if err := bkemetrics.MetricRegister.E2EDataGather(key); err != nil {
			log.Errorf("e2e mode enabled, but failed to save metrics data: %v", err)
			return
		}
	}

}
