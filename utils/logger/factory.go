/*
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config logger configuration
type Config struct {
	// LogPath 日志文件路径，如：/var/log/openFuyao/bkeagent.log 
	LogPath string
	// MaxSize 单个文件最大大小，单位MB，默认30
	MaxSize int
	// MaxBackups 最多保留的备份文件个数，默认3
	MaxBackups int
	// MaxAge 保留日志文件的最大天数，默认1
	MaxAge int
	// Compress 是否压缩旧日志文件，默认false
	Compress bool
	// Level 日志级别：debug/info/warn/error/fatal，默认info
	Level string
	// EnableConsole 是否同时输出到控制台，默认true
	EnableConsole bool
	// EnableCaller 是否显示调用者信息（文件名和行号），默认true
	EnableCaller bool
	// CallerSkip 调用栈跳过层数，默认1
	CallerSkip int
	// JSONFormat 是否使用JSON格式输出，默认false（使用文本格式）
	JSONFormat bool
	// Name logger名称，用于区分不同的logger实例
	Name string
}

// DefaultConfig returns default logger configuration
// Matches the existing logger configuration in bkeagent/log and capbke/log
func DefaultConfig() Config {
	return Config{
		MaxSize:       30,
		MaxBackups:    3,
		MaxAge:        1,
		Compress:      false,
		Level:         "info",
		EnableConsole: true,
		EnableCaller:  true,
		CallerSkip:    1,
		JSONFormat:    false,
	}
}

// NewLogger creates a new logger instance with the given configuration
func NewLogger(config Config) (*zap.SugaredLogger, error) {
	applyDefaults(&config)

	if err := createLogDirectory(config.LogPath); err != nil {
		return nil, err
	}

	hook := setupLumberjack(config)
	syncer := setupWriters(config, hook)
	encoder := setupEncoder(config)
	level := parseLogLevel(config)

	core := zapcore.NewCore(encoder, syncer, level)
	return createLogger(config, core), nil
}

// applyDefaults sets default values for zero-value fields
func applyDefaults(config *Config) {
	defaults := DefaultConfig()
	if config.MaxSize == 0 {
		config.MaxSize = defaults.MaxSize
	}
	if config.MaxBackups == 0 {
		config.MaxBackups = defaults.MaxBackups
	}
	if config.MaxAge == 0 {
		config.MaxAge = defaults.MaxAge
	}
	if config.Level == "" {
		config.Level = defaults.Level
	}
	if config.CallerSkip == 0 {
		config.CallerSkip = defaults.CallerSkip
	}
}

// createLogDirectory creates the log directory if it does not exist
func createLogDirectory(logPath string) error {
	logDir := filepath.Dir(logPath)
	return os.MkdirAll(logDir, 0755)
}

// setupLumberjack creates a lumberjack logger for log rotation
func setupLumberjack(config Config) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   config.LogPath,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}
}

// setupWriters creates a WriteSyncer with console and file outputs
func setupWriters(config Config, hook *lumberjack.Logger) zapcore.WriteSyncer {
	var writers []zapcore.WriteSyncer
	if config.EnableConsole {
		writers = append(writers, zapcore.AddSync(os.Stdout))
	}
	writers = append(writers, zapcore.AddSync(hook))
	return zapcore.NewMultiWriteSyncer(writers...)
}

// DefaultEncoderConfig returns the default encoder configuration
func DefaultEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
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
}

// setupEncoder creates the appropriate encoder based on config
func setupEncoder(config Config) zapcore.Encoder {
	encoderConfig := DefaultEncoderConfig()
	if config.JSONFormat {
		return zapcore.NewJSONEncoder(encoderConfig)
	}
	return zapcore.NewConsoleEncoder(encoderConfig)
}

// parseLogLevel parses the log level from config, with DEBUG env override
func parseLogLevel(config Config) zapcore.Level {
	level := zap.InfoLevel
	if err := level.UnmarshalText([]byte(config.Level)); err != nil {
		return zap.InfoLevel
	}

	// Enable DEBUG level if DEBUG env var is set
	if os.Getenv("DEBUG") == "true" || os.Getenv("DEBUG") != "" {
		return zap.DebugLevel
	}

	return level
}

// createLogger creates the final zap.SugaredLogger with all options
func createLogger(config Config, core zapcore.Core) *zap.SugaredLogger {
	opts := []zap.Option{zap.AddCallerSkip(config.CallerSkip)}
	if config.EnableCaller {
		opts = append(opts, zap.AddCaller())
	}

	log := zap.New(core, opts...)
	sugaredLogger := log.Sugar()

	if config.Name != "" {
		sugaredLogger = sugaredLogger.Named(config.Name)
	}

	return sugaredLogger
}
