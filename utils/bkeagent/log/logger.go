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
	"fmt"

	factor "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/logger"

	"go.uber.org/zap"
)

const (
	// BkeagentLogPath 默认日志文件路径
	BkeagentLogPath = "/var/log/openFuyao/bkeagent.log"
)

var (
	bkeagentLogger *zap.SugaredLogger
)

func init() {
	// 使用工厂创建默认logger，保持原有配置
	config := factor.DefaultConfig()
	config.LogPath = BkeagentLogPath

	var err error
	bkeagentLogger, err = factor.NewLogger(config)
	if err != nil {
		fmt.Println("logger init failed")
		bkeagentLogger = zap.NewNop().Sugar()
		return
	}
}

func Debug(args ...interface{}) {
	bkeagentLogger.Debug(args...)
}

func Debugf(template string, args ...interface{}) {
	bkeagentLogger.Debugf(template, args...)
}

func Info(args ...interface{}) {
	bkeagentLogger.Info(args...)
}

func Infof(template string, args ...interface{}) {
	bkeagentLogger.Infof(template, args...)
}

func Warn(args ...interface{}) {
	bkeagentLogger.Warn(args...)
}

func Warnf(template string, args ...interface{}) {
	bkeagentLogger.Warnf(template, args...)
}

func Error(args ...interface{}) {
	bkeagentLogger.Error(args...)
}

func Errorf(template string, args ...interface{}) {
	bkeagentLogger.Errorf(template, args...)
}

func Panic(args ...interface{}) {
	bkeagentLogger.Panic(args...)
}

func Panicf(template string, args ...interface{}) {
	bkeagentLogger.Panicf(template, args...)
}

func Fatal(args ...interface{}) {
	bkeagentLogger.Fatal(args...)
}

func Fatalf(template string, args ...interface{}) {
	bkeagentLogger.Fatalf(template, args...)
}

func Desugar() *zap.Logger {
	return bkeagentLogger.Desugar()
}

func SetTestLogger(logger *zap.SugaredLogger) {
	bkeagentLogger = logger
}
