// Copyright (c) 2018 Intel Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logging

import (
	"fmt"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Debugf defines our printf for debug level.
func Debugf(format string, a ...interface{}) {
	zap.S().Debugf(format, a...)
}

// Verbosef defines our printf for Verbose level.
func Verbosef(format string, a ...interface{}) {
	zap.S().Warnf(format, a...)
}

// Errorf defines our printf for error level.
func Errorf(format string, a ...interface{}) error {
	zap.S().Errorf(format, a...)
	return fmt.Errorf(format, a...)
}

// Panicf defines our printf for panic level.
func Panicf(format string, a ...interface{}) {
	zap.S().Panicf(format, a...)
	zap.S().Panicf("========= Stack trace output ========")
	zap.S().Panicf("%+v", errors.New("Whereabouts Panic"))
	zap.S().Panicf("========= Stack trace output end ========")
}

func ConfigureLogger(logFile string) {
	w := zapcore.AddSync(&lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    100, // mb
		MaxBackups: 5,
		MaxAge:     30,
		Compress:   true,
	})

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		w,
		zap.DebugLevel,
	)

	logger := zap.New(core)
	defer logger.Sync()
	zap.ReplaceGlobals(logger)
	zap.S().Debugf("Started zap logger")
	return
}
