/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */
package testutils

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLog util
func NewLog() *zap.SugaredLogger {
	// 1. 配置编码器（JSON格式，适合日志收集系统）
	encoderConfig1 := zap.NewProductionEncoderConfig()
	encoderConfig1.EncodeTime = zapcore.ISO8601TimeEncoder // 时间格式
	// 2. 创建核心（输出到文件+控制台）
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig1),
		zapcore.NewMultiWriteSyncer(
			zapcore.AddSync(os.Stdout),
		),
		zap.InfoLevel, // 日志级别
	)

	return zap.New(core).Sugar()
}
