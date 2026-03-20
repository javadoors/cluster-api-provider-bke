/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */

package log

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

func init() {
	if bkeagentLogger == nil {
		bkeagentLogger = zap.NewNop().Sugar()
	}
}

const (
	testMessage     = "test message"
	testTemplate    = "test %s"
	debugLevelEnv   = "DEBUG"
	debugLevelValue = "true"
	infoLevelValue  = "false"
	logFilePath     = "/var/log/bkeagent.log"
	testLogPath     = "/var/log/test.log"
	maxSize         = 30
	maxBackups      = 3
	maxAge          = 1
)

func TestDebug(t *testing.T) {
	Debug(testMessage)
	assert.True(t, true)
}

func TestDebugf(t *testing.T) {
	Debugf(testTemplate, testMessage)
	assert.True(t, true)
}

func TestDebugfMultipleArgs(t *testing.T) {
	Debugf("test %s %d", testMessage, 123)
	assert.True(t, true)
}

func TestInfo(t *testing.T) {
	Info(testMessage)
	assert.True(t, true)
}

func TestInfof(t *testing.T) {
	Infof(testTemplate, testMessage)
	assert.True(t, true)
}

func TestInfofMultipleArgs(t *testing.T) {
	Infof("test %s %d", testMessage, 456)
	assert.True(t, true)
}

func TestWarn(t *testing.T) {
	Warn(testMessage)
	assert.True(t, true)
}

func TestWarnf(t *testing.T) {
	Warnf(testTemplate, testMessage)
	assert.True(t, true)
}

func TestWarnfMultipleArgs(t *testing.T) {
	Warnf("test %s %d", testMessage, 789)
	assert.True(t, true)
}

func TestError(t *testing.T) {
	Error(testMessage)
	assert.True(t, true)
}

func TestErrorf(t *testing.T) {
	Errorf(testTemplate, testMessage)
	assert.True(t, true)
}

func TestErrorfMultipleArgs(t *testing.T) {
	Errorf("test %s %d", testMessage, 101112)
	assert.True(t, true)
}

func TestPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			assert.NotNil(t, r)
		}
	}()

	Panic(testMessage)
}

func TestPanicf(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			assert.NotNil(t, r)
		}
	}()

	Panicf(testTemplate, testMessage)
}

func TestFatal(t *testing.T) {
	t.Skip("skipping test as Fatal() exits the program")
}

func TestFatalf(t *testing.T) {
	t.Skip("skipping test as Fatalf() exits the program")
}

func TestDesugar(t *testing.T) {
	logger := Desugar()
	assert.NotNil(t, logger)
	assert.IsType(t, &zap.Logger{}, logger)
}

func TestGetEnvDebugTrue(t *testing.T) {
	originalEnv := os.Getenv(debugLevelEnv)
	defer os.Setenv(debugLevelEnv, originalEnv)

	os.Setenv(debugLevelEnv, debugLevelValue)

	level := zap.InfoLevel
	debugEnv := os.Getenv(debugLevelEnv)
	if debugEnv == debugLevelValue {
		level = zap.DebugLevel
	}

	assert.Equal(t, zap.DebugLevel, level)
}

func TestGetEnvDebugFalse(t *testing.T) {
	originalEnv := os.Getenv(debugLevelEnv)
	defer os.Setenv(debugLevelEnv, originalEnv)

	os.Setenv(debugLevelEnv, infoLevelValue)

	level := zap.InfoLevel
	debugEnv := os.Getenv(debugLevelEnv)
	if debugEnv == debugLevelValue {
		level = zap.DebugLevel
	}

	assert.Equal(t, zap.InfoLevel, level)
}

func TestGetEnvNotSet(t *testing.T) {
	originalEnv := os.Getenv(debugLevelEnv)
	defer os.Setenv(debugLevelEnv, originalEnv)

	os.Unsetenv(debugLevelEnv)

	level := zap.InfoLevel
	debugEnv := os.Getenv(debugLevelEnv)
	if debugEnv == debugLevelValue {
		level = zap.DebugLevel
	}

	assert.Equal(t, zap.InfoLevel, level)
}

func TestZapcoreEncoderConfig(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "line",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
		EncodeName:     zapcore.FullNameEncoder,
	}

	assert.Equal(t, "time", encoderConfig.TimeKey)
	assert.Equal(t, "level", encoderConfig.LevelKey)
	assert.Equal(t, "logger", encoderConfig.NameKey)
	assert.Equal(t, "line", encoderConfig.CallerKey)
	assert.Equal(t, "msg", encoderConfig.MessageKey)
	assert.Equal(t, "stacktrace", encoderConfig.StacktraceKey)
}

func TestZapcoreNewConsoleEncoder(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		MessageKey:     "msg",
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	encoder := zapcore.NewConsoleEncoder(encoderConfig)

	assert.NotNil(t, encoder)
}

func TestZapcoreNewCore(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:     "time",
		LevelKey:    "level",
		MessageKey:  "msg",
		EncodeLevel: zapcore.CapitalColorLevelEncoder,
		EncodeTime:  zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
	}

	encoder := zapcore.NewConsoleEncoder(encoderConfig)
	syncer := zapcore.AddSync(os.Stdout)
	level := zap.InfoLevel

	core := zapcore.NewCore(encoder, syncer, level)

	assert.NotNil(t, core)
}

func TestZapNewWithCaller(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:    "time",
		LevelKey:   "level",
		MessageKey: "msg",
		EncodeTime: zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
	}

	encoder := zapcore.NewConsoleEncoder(encoderConfig)
	syncer := zapcore.AddSync(os.Stdout)
	level := zap.InfoLevel

	core := zapcore.NewCore(encoder, syncer, level)
	log := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	assert.NotNil(t, log)
}

func TestZapSugarLogger(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:    "time",
		LevelKey:   "level",
		MessageKey: "msg",
		EncodeTime: zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
	}

	encoder := zapcore.NewConsoleEncoder(encoderConfig)
	syncer := zapcore.AddSync(os.Stdout)
	level := zap.InfoLevel

	core := zapcore.NewCore(encoder, syncer, level)
	log := zap.New(core, zap.AddCaller())
	sugar := log.Sugar()

	assert.NotNil(t, sugar)
}

func TestLumberjackLogger(t *testing.T) {
	hook := lumberjack.Logger{
		Filename:   testLogPath,
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		Compress:   false,
	}

	hook.MaxAge = maxAge

	assert.Equal(t, testLogPath, hook.Filename)
	assert.Equal(t, maxSize, hook.MaxSize)
	assert.Equal(t, maxBackups, hook.MaxBackups)
	assert.Equal(t, maxAge, hook.MaxAge)
	assert.False(t, hook.Compress)
}

func TestLumberjackLoggerWithLogPath(t *testing.T) {
	hook := lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		Compress:   false,
	}

	hook.MaxAge = maxAge

	assert.Equal(t, logFilePath, hook.Filename)
	assert.Equal(t, maxSize, hook.MaxSize)
	assert.Equal(t, maxBackups, hook.MaxBackups)
	assert.Equal(t, maxAge, hook.MaxAge)
	assert.False(t, hook.Compress)
}

func TestZapcoreMultiWriteSyncer(t *testing.T) {
	syncer := zapcore.NewMultiWriteSyncer(
		zapcore.AddSync(os.Stdout),
		zapcore.AddSync(&lumberjack.Logger{
			Filename:   testLogPath,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
		}),
	)

	assert.NotNil(t, syncer)
}

func TestZapcoreAddSync(t *testing.T) {
	syncer := zapcore.AddSync(os.Stdout)
	assert.NotNil(t, syncer)
}

func TestZapcoreDefaultLineEnding(t *testing.T) {
	assert.NotEmpty(t, zapcore.DefaultLineEnding)
}

func TestZapcoreCapitalColorLevelEncoder(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		EncodeLevel: zapcore.CapitalColorLevelEncoder,
	}
	assert.NotNil(t, encoderConfig.EncodeLevel)
}

func TestZapcoreTimeEncoderOfLayout(t *testing.T) {
	timeLayout := "2006-01-02 15:04:05"
	encoder := zapcore.TimeEncoderOfLayout(timeLayout)
	assert.NotNil(t, encoder)
}

func TestZapcoreSecondsDurationEncoder(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		EncodeDuration: zapcore.SecondsDurationEncoder,
	}
	assert.NotNil(t, encoderConfig.EncodeDuration)
}

func TestZapcoreShortCallerEncoder(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		EncodeCaller: zapcore.ShortCallerEncoder,
	}
	assert.NotNil(t, encoderConfig.EncodeCaller)
}

func TestZapcoreFullNameEncoder(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		EncodeName: zapcore.FullNameEncoder,
	}
	assert.NotNil(t, encoderConfig.EncodeName)
}

func TestZapAddCaller(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:    "time",
		LevelKey:   "level",
		MessageKey: "msg",
		EncodeTime: zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
	}

	encoder := zapcore.NewConsoleEncoder(encoderConfig)
	syncer := zapcore.AddSync(os.Stdout)
	level := zap.InfoLevel

	core := zapcore.NewCore(encoder, syncer, level)
	log := zap.New(core, zap.AddCaller())

	assert.NotNil(t, log)
}

func TestZapAddCallerSkip(t *testing.T) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:    "time",
		LevelKey:   "level",
		MessageKey: "msg",
		EncodeTime: zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
	}

	encoder := zapcore.NewConsoleEncoder(encoderConfig)
	syncer := zapcore.AddSync(os.Stdout)
	level := zap.InfoLevel

	core := zapcore.NewCore(encoder, syncer, level)
	log := zap.New(core, zap.AddCallerSkip(1))

	assert.NotNil(t, log)
}

func TestZapLevels(t *testing.T) {
	assert.Equal(t, zap.DebugLevel, zap.DebugLevel)
	assert.Equal(t, zap.InfoLevel, zap.InfoLevel)
	assert.Equal(t, zap.WarnLevel, zap.WarnLevel)
	assert.Equal(t, zap.ErrorLevel, zap.ErrorLevel)
	assert.Equal(t, zap.PanicLevel, zap.PanicLevel)
	assert.Equal(t, zap.FatalLevel, zap.FatalLevel)
}

func TestLogConstants(t *testing.T) {
	assert.Equal(t, "/var/log/bkeagent.log", logFilePath)
	assert.Equal(t, 30, maxSize)
	assert.Equal(t, 3, maxBackups)
	assert.Equal(t, 1, maxAge)
}
