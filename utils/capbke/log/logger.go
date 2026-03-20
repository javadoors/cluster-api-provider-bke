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
	"os"

	"github.com/onsi/ginkgo/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	factor "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/logger"
)

const (
	// BkeLogPath 默认日志文件路径
	BkeLogPath = "/var/log/openFuyao/bke.log"
)

var (
	BkeLogger *zap.SugaredLogger
	Encoder   zapcore.Encoder
)

func init() {
	// test模式：特殊处理，输出到GinkgoWriter
	if os.Getenv("test") == "true" {
		encoder := zapcore.NewConsoleEncoder(factor.DefaultEncoderConfig())
		syncer := zapcore.AddSync(ginkgo.GinkgoWriter)
		level := zap.InfoLevel
		if os.Getenv("DEBUG") != "" {
			level = zap.DebugLevel
		}
		core := zapcore.NewCore(encoder, syncer, level)
		BkeLogger = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
		Encoder = encoder
		return
	}

	// container模式和默认模式都使用工厂创建logger
	config := factor.DefaultConfig()
	config.LogPath = BkeLogPath
	var err error
	BkeLogger, err = factor.NewLogger(config)
	if err != nil {
		fmt.Println("logger init failed")
		BkeLogger = zap.NewNop().Sugar()
		return
	}
	Encoder = zapcore.NewConsoleEncoder(factor.DefaultEncoderConfig())
}

func Debug(args ...interface{}) {
	BkeLogger.Debug(args...)
}

func Debugf(template string, args ...interface{}) {
	BkeLogger.Debugf(template, args...)
}

func Info(args ...interface{}) {
	BkeLogger.Info(args...)
}

func Infof(template string, args ...interface{}) {
	BkeLogger.Infof(template, args...)
}

func Warn(args ...interface{}) {
	BkeLogger.Warn(args...)
}

func Warnf(template string, args ...interface{}) {
	BkeLogger.Warnf(template, args...)
}

func Error(args ...interface{}) {
	BkeLogger.Error(args...)
}

func Errorf(template string, args ...interface{}) {
	BkeLogger.Errorf(template, args...)
}

func Panic(args ...interface{}) {
	BkeLogger.Panic(args...)
}

func Panicf(template string, args ...interface{}) {
	BkeLogger.Panicf(template, args...)
}

func Fatal(args ...interface{}) {
	BkeLogger.Fatal(args...)
}

func Fatalf(template string, args ...interface{}) {
	BkeLogger.Fatalf(template, args...)
}

func With(key string, value interface{}) *zap.SugaredLogger {
	return BkeLogger.With(key, value)
}

func Named(name string) *zap.SugaredLogger {
	return BkeLogger.Named(name)
}
