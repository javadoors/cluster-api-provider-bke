/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package manifest

import "fmt"

// Apply strategies for YAML/Manifest components. Empty means ServerSideApply.
const (
	ApplyStrategyServerSideApply = "ServerSideApply"
	ApplyStrategyReplace         = "Replace"
	ApplyStrategyCreateOnly      = "CreateOnly"
)

// NormalizeApplyStrategy returns the effective strategy.
// Empty input defaults to ServerSideApply; unknown values return an error.
func NormalizeApplyStrategy(strategy string) (string, error) {
	switch strategy {
	case "", ApplyStrategyServerSideApply:
		return ApplyStrategyServerSideApply, nil
	case ApplyStrategyReplace, ApplyStrategyCreateOnly:
		return strategy, nil
	default:
		return "", fmt.Errorf("unsupported applyStrategy %q", strategy)
	}
}
