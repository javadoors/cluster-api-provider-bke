/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package job

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/k8s"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/shell"
)

// Task Global tasks
type Task struct {
	StopChan                chan struct{}        `json:"stopChan"`
	Phase                   v1beta1.CommandPhase `json:"phase"`
	ResourceVersion         string               `json:"resourceVersion"`
	Generation              int64                `json:"generation"`
	TTLSecondsAfterFinished int                  `json:"ttlSecondsAfterFinished"`
	HasAddTimer             bool                 `json:"hasAddTimer"`
	Once                    *sync.Once           `json:"once"`
}

type Job struct {
	BuiltIn builtin.BuiltIn
	K8s     k8s.K8s
	Shell   shell.Shell
	Task    map[string]*Task
}

func NewJob(client client.Client) (Job, error) {

	var j Job
	commandExec := &exec.CommandExecutor{}

	j.BuiltIn = builtin.New(commandExec, client)
	j.K8s = &k8s.Task{
		K8sClient: client,
		Exec:      commandExec,
	}
	j.Shell = &shell.Task{
		Exec: commandExec,
	}
	j.Task = map[string]*Task{}

	return j, nil
}

func (t *Task) SafeClose() {
	t.Once.Do(func() {
		close(t.StopChan)
	})
}
