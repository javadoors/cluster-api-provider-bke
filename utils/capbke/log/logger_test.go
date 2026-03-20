/*
   Copyright @ 2021 bocloud <fushaosong@beyondcent.com>.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.

   Original file: https://gitee.com/bocloud-open-source/carina/blob/v0.9.1/utils/log/logger.go
*/

package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func init() {
	if BkeLogger == nil {
		BkeLogger = zap.NewNop().Sugar()
	}
}

func TestDebug(t *testing.T) {
	Debug("test debug")
	assert.NotNil(t, BkeLogger)
}

func TestDebugf(t *testing.T) {
	Debugf("test %s", "debugf")
	assert.NotNil(t, BkeLogger)
}

func TestInfo(t *testing.T) {
	Info("test info")
	assert.NotNil(t, BkeLogger)
}

func TestInfof(t *testing.T) {
	Infof("test %s", "infof")
	assert.NotNil(t, BkeLogger)
}

func TestWarn(t *testing.T) {
	Warn("test warn")
	assert.NotNil(t, BkeLogger)
}

func TestWarnf(t *testing.T) {
	Warnf("test %s", "warnf")
	assert.NotNil(t, BkeLogger)
}

func TestError(t *testing.T) {
	Error("test error")
	assert.NotNil(t, BkeLogger)
}

func TestErrorf(t *testing.T) {
	Errorf("test %s", "errorf")
	assert.NotNil(t, BkeLogger)
}

func TestWith(t *testing.T) {
	logger := With("key", "value")
	assert.NotNil(t, logger)
}

func TestNamed(t *testing.T) {
	logger := Named("test")
	assert.NotNil(t, logger)
}

func TestBkeLogPath(t *testing.T) {
	assert.Equal(t, "/var/log/openFuyao/bke.log", BkeLogPath)
}


func TestPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			assert.NotNil(t, r)
		}
	}()
	Panic("test panic")
}

func TestPanicf(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			assert.NotNil(t, r)
		}
	}()
	Panicf("test %s", "panicf")
}

func TestBkeLogger(t *testing.T) {
	assert.NotNil(t, BkeLogger)
	logger := BkeLogger.With("test", "value")
	assert.NotNil(t, logger)
}
