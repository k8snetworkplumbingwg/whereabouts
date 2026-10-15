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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// Level type.
type Level uint32

// PanicLevel ErrorLevel etc are our logging level constants.
const (
	PanicLevel Level = iota
	ErrorLevel
	VerboseLevel
	DebugLevel
	MaxLevel
	UnknownLevel
)

var loggingStderr bool
var loggingFp *os.File
var loggingLevel Level

// mu guards all logging state (loggingStderr, loggingFp, loggingLevel).
// A full Mutex (not RWMutex) is used intentionally: SetLogFile closes
// the previous loggingFp under the lock, so concurrent Printf callers
// must not hold a reader lock that would let them write to a closed file.
var mu sync.Mutex

const defaultTimestampFormat = time.RFC3339

func (l Level) String() string {
	switch l {
	case PanicLevel:
		return "panic"
	case VerboseLevel:
		return "verbose"
	case ErrorLevel:
		return "error"
	case DebugLevel:
		return "debug"
	}
	return "unknown"
}

// Printf provides basic Printf functionality for logs.
func Printf(level Level, format string, a ...interface{}) {
	mu.Lock()
	defer mu.Unlock()

	if level > loggingLevel {
		return
	}

	t := time.Now()
	line := fmt.Sprintf("%s [%s] %s\n", t.Format(defaultTimestampFormat), level, fmt.Sprintf(format, a...))

	if loggingStderr {
		fmt.Fprint(os.Stderr, line)
	}

	if loggingFp != nil {
		fmt.Fprint(loggingFp, line)
	}
}

// Debugf defines our printf for debug level.
func Debugf(format string, a ...interface{}) {
	Printf(DebugLevel, format, a...)
}

// Verbosef defines our printf for Verbose level.
func Verbosef(format string, a ...interface{}) {
	Printf(VerboseLevel, format, a...)
}

// Errorf defines our printf for error level.
// It supports %w for proper error wrapping in the returned error.
// The log output substitutes %w with %v so fmt.Sprintf renders the message.
func Errorf(format string, a ...interface{}) error {
	Printf(ErrorLevel, strings.ReplaceAll(format, "%w", "%v"), a...)
	return fmt.Errorf(format, a...)
}

// Panicf defines our printf for panic level.
func Panicf(format string, a ...interface{}) {
	Printf(PanicLevel, format, a...)
	Printf(PanicLevel, "========= Stack trace output ========")
	Printf(PanicLevel, "%+v", errors.Errorf(format, a...))
	Printf(PanicLevel, "========= Stack trace output end ========")
	panic(fmt.Sprintf(format, a...))
}

// GetLoggingLevel returns loggingLevel.
func GetLoggingLevel() Level {
	return loggingLevel
}

func getLoggingLevel(levelStr string) Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return DebugLevel
	case "verbose":
		return VerboseLevel
	case "error":
		return ErrorLevel
	case "panic":
		return PanicLevel
	}
	fmt.Fprintf(os.Stderr, "Whereabouts logging: cannot set logging level to %s\n", levelStr)
	return UnknownLevel
}

// SetLogLevel sets loggingLevel.
func SetLogLevel(levelStr string) {
	level := getLoggingLevel(levelStr)
	if level < MaxLevel {
		mu.Lock()
		loggingLevel = level
		mu.Unlock()
	}
}

// SetLogStderr enables logging to stderr.
func SetLogStderr(enable bool) {
	mu.Lock()
	loggingStderr = enable
	mu.Unlock()
}

// SetLogFile defines which log file we'll log to.
func SetLogFile(filename string) {
	if filename == "" {
		return
	}

	fp, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		_, _ = os.Stderr.WriteString("Whereabouts logging: cannot open " + filename + "\n")
		return
	}

	mu.Lock()
	if loggingFp != nil {
		if closeErr := loggingFp.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Whereabouts logging: error closing previous log file: %v\n", closeErr)
		}
	}
	loggingFp = fp
	mu.Unlock()
}

func init() {
	loggingStderr = true
	loggingFp = nil
	loggingLevel = DebugLevel
}
