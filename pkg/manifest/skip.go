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

import (
	"errors"
	"fmt"
)

// ErrSkipNotInstalled indicates the target cluster does not have the component installed.
// The scheduler must not record this component in BKECluster.status.declarativeUpgrade.completed.
var ErrSkipNotInstalled = errors.New("manifest upgrade skipped: component not installed")

// SkipNotInstalledError carries the component name for logging and errors.Is checks.
type SkipNotInstalledError struct {
	Component string
}

func (e *SkipNotInstalledError) Error() string {
	if e == nil || e.Component == "" {
		return ErrSkipNotInstalled.Error()
	}
	return fmt.Sprintf("%s: %s", ErrSkipNotInstalled.Error(), e.Component)
}

func (e *SkipNotInstalledError) Is(target error) bool {
	return target == ErrSkipNotInstalled
}

// NewSkipNotInstalledError builds a skip error for componentName.
func NewSkipNotInstalledError(componentName string) error {
	return &SkipNotInstalledError{Component: componentName}
}

// IsSkipNotInstalled reports whether err is a not-installed skip (no BC completed record).
func IsSkipNotInstalled(err error) bool {
	return errors.Is(err, ErrSkipNotInstalled)
}
