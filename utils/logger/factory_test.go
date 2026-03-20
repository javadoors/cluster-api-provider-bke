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
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxSize != 30 {
		t.Errorf("expected MaxSize 30, got %d", cfg.MaxSize)
	}
	if cfg.MaxBackups != 3 {
		t.Errorf("expected MaxBackups 3, got %d", cfg.MaxBackups)
	}
	if cfg.MaxAge != 1 {
		t.Errorf("expected MaxAge 1, got %d", cfg.MaxAge)
	}
	if cfg.Level != "info" {
		t.Errorf("expected Level info, got %s", cfg.Level)
	}
	if !cfg.EnableConsole {
		t.Error("expected EnableConsole true")
	}
	if !cfg.EnableCaller {
		t.Error("expected EnableCaller true")
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := Config{}
	applyDefaults(&cfg)
	if cfg.MaxSize != 30 {
		t.Errorf("expected MaxSize 30, got %d", cfg.MaxSize)
	}
	if cfg.Level != "info" {
		t.Errorf("expected Level info, got %s", cfg.Level)
	}
}

func TestApplyDefaults_PartialConfig(t *testing.T) {
	cfg := Config{MaxSize: 50, Level: "debug"}
	applyDefaults(&cfg)
	if cfg.MaxSize != 50 {
		t.Errorf("expected MaxSize 50, got %d", cfg.MaxSize)
	}
	if cfg.Level != "debug" {
		t.Errorf("expected Level debug, got %s", cfg.Level)
	}
	if cfg.MaxBackups != 3 {
		t.Errorf("expected MaxBackups 3, got %d", cfg.MaxBackups)
	}
}

func TestSetupLumberjack(t *testing.T) {
	cfg := Config{
		LogPath:    "/tmp/test.log",
		MaxSize:    50,
		MaxBackups: 5,
		MaxAge:     7,
		Compress:   true,
	}
	hook := setupLumberjack(cfg)
	if hook.Filename != cfg.LogPath {
		t.Errorf("expected Filename %s, got %s", cfg.LogPath, hook.Filename)
	}
	if hook.MaxSize != cfg.MaxSize {
		t.Errorf("expected MaxSize %d, got %d", cfg.MaxSize, hook.MaxSize)
	}
}

func TestSetupEncoder(t *testing.T) {
	cfg := Config{JSONFormat: false}
	encoder := setupEncoder(cfg)
	if encoder == nil {
		t.Error("expected non-nil encoder")
	}

	cfg.JSONFormat = true
	encoder = setupEncoder(cfg)
	if encoder == nil {
		t.Error("expected non-nil JSON encoder")
	}
}

func TestDefaultEncoderConfig(t *testing.T) {
	cfg := DefaultEncoderConfig()
	if cfg.TimeKey != "time" {
		t.Errorf("expected TimeKey 'time', got %s", cfg.TimeKey)
	}
	if cfg.LevelKey != "level" {
		t.Errorf("expected LevelKey 'level', got %s", cfg.LevelKey)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		expected zapcore.Level
	}{
		{"info level", "info", zapcore.InfoLevel},
		{"debug level", "debug", zapcore.DebugLevel},
		{"warn level", "warn", zapcore.WarnLevel},
		{"error level", "error", zapcore.ErrorLevel},
		{"invalid level", "invalid", zapcore.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Level: tt.level}
			level := parseLogLevel(cfg)
			if level != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, level)
			}
		})
	}
}

func TestParseLogLevel_WithDebugEnv(t *testing.T) {
	os.Setenv("DEBUG", "true")
	defer os.Unsetenv("DEBUG")
	
	cfg := Config{Level: "info"}
	level := parseLogLevel(cfg)
	if level != zapcore.DebugLevel {
		t.Errorf("expected DebugLevel with DEBUG=true, got %v", level)
	}
}

func TestNewLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	
	cfg := Config{
		LogPath:       logPath,
		Level:         "info",
		EnableConsole: false,
	}
	
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger == nil {
		t.Error("expected non-nil logger")
	}
}

func TestNewLogger_WithName(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	
	cfg := Config{
		LogPath:       logPath,
		Name:          "test-logger",
		EnableConsole: false,
	}
	
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger == nil {
		t.Error("expected non-nil logger")
	}
}
