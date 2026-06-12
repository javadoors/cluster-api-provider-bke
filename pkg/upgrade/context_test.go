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

package upgrade

import "testing"

func TestVersionContext_NeedsUpgrade(t *testing.T) {
	vc := NewVersionContext()
	vc.SetCurrent(ComponentEtcd, "3.5.10")
	vc.SetTarget(ComponentEtcd, "3.5.12")

	if !vc.NeedsUpgrade(ComponentEtcd) {
		t.Fatal("expected etcd upgrade needed")
	}

	vc.SetCurrent(ComponentEtcd, "3.5.12")
	if vc.NeedsUpgrade(ComponentEtcd) {
		t.Fatal("expected no etcd upgrade when versions match")
	}
}

func TestVersionContext_HasTarget(t *testing.T) {
	vc := NewVersionContext()
	if vc.HasTarget(ComponentKubernetesMaster) {
		t.Fatal("expected no target before set")
	}
	vc.SetTarget(ComponentKubernetesMaster, "v1.29.0")
	if !vc.HasTarget(ComponentKubernetesMaster) {
		t.Fatal("expected target after set")
	}
}

func TestVersionContext_NilSafe(t *testing.T) {
	var vc *VersionContext
	vc.SetCurrent(ComponentEtcd, "1")
	if vc.GetCurrent(ComponentEtcd) != "" {
		t.Fatal("nil context should be no-op")
	}
	if vc.NeedsUpgrade(ComponentEtcd) {
		t.Fatal("nil context should not need upgrade")
	}
}
