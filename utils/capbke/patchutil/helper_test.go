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

package patchutil

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
)

func TestDiff(t *testing.T) {
	oldobj := kube.ProductStatus{
		Name: "test",
		StartTime: &metav1.Time{
			Time: time.Now(),
		},
		UpdateTime: &metav1.Time{
			Time: time.Now().Add(time.Minute),
		},
		CompletionTime: &metav1.Time{
			// 未来一个小时
			Time: time.Now().Add(time.Hour),
		},
		Health: true,
		Component: []kube.ComponentStatus{
			{
				Name:     "test",
				Resource: "",
				Health:   true,
				Message:  "ok",
			},
		},
		Reason: "no reason",
	}

	newobj := kube.ProductStatus{
		Name: "test",
		StartTime: &metav1.Time{
			Time: time.Now(),
		},
		UpdateTime: &metav1.Time{
			Time: time.Now().Add(time.Minute),
		},
		CompletionTime: &metav1.Time{
			// 未来一个小时
			Time: time.Now().Add(time.Hour),
		},
		Health: true,
		Component: []kube.ComponentStatus{
			{
				Name:     "test",
				Resource: "",
				Health:   true,
				Message:  "ok",
			},
			{
				Name:     "test2",
				Resource: "",
				Health:   true,
				Message:  "ok",
			},
		},
		Reason: "no reason",
	}

	diff, err := Diff(oldobj, newobj)
	if err != nil {
		t.Error(err)
		return
	}

	for _, d := range diff {
		switch d.Kind() {
		case "add":
			valueInterface, err := d.ValueInterface()
			if err != nil {
				return
			}
			//valueInterface （map[string]interface） 转为 ProductStatus
			value, err := json.Marshal(valueInterface)
			if err != nil {
				return
			}
			var component kube.ComponentStatus
			err = json.Unmarshal(value, &component)
			if err != nil {
				return
			}
			t.Logf("component: %v, health: %v, msg: %v", component.Name, component.Health, component.Message)
		}
	}
}
