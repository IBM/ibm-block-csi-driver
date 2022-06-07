/**
 * Copyright 2019 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// This package is used for logging.
// It is implemented as decorator for logrus which formats messages in specific manner
// while adding additional data to each message like goroutine id.
// E.g. 2019-08-20 17:57:01.821 info	[1] [vol] (main.go:83) - my logg message
//
// We can add additional info to goid which is specified in the log by mapping it to some string value
// using goid_info acage. E.g. to volume id
//
// To change log level add argument -loglevel <level> (e.g. -log-level debug). Default is info.

package logger

import (
	"bytes"
	"flag"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/goid_info"
	"github.com/ibm/ibm-block-csi-driver/node/util"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"github.com/sirupsen/logrus"
)

const (
	callerField             = "caller"
	goIDField               = "goid"
	additionalGoIDInfoField = "addId"
	unknownValue            = "unknown"
	noAdditionalIDValue     = "-"
	startCallerDepth        = 1
)

type LogFormat struct {
	TimestampFormat string
}

// singleton logrus instance
var instance *logrus.Logger

// Format the entry which contains log message info
func (f *LogFormat) Format(entry *logrus.Entry) ([]byte, error) {
	goid := entry.Data[goIDField]
	if goid == nil {
		goid = unknownValue
	}
	additionalGoIDInfo := entry.Data[additionalGoIDInfoField]
	if additionalGoIDInfo == nil || len(additionalGoIDInfo.(string)) == 0 {
		additionalGoIDInfo = noAdditionalIDValue
	}
	caller := entry.Data[callerField] // file and line this log is caled from
	var b *bytes.Buffer
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}
	b.WriteString(entry.Time.Format(f.TimestampFormat) + " ")
	b.WriteString(strings.ToUpper(entry.Level.String()) + "\t")
	b.WriteString(fmt.Sprintf("%v", "["+goid.(string)) + "] ")
	b.WriteString(fmt.Sprintf("%v", "["+additionalGoIDInfo.(string)) + "] ")
	b.WriteString("(" + caller.(string) + ") - ")
	b.WriteString(entry.Message)
	b.WriteString("\n")
	return b.Bytes(), nil
}

// Initialize logrus instance if it was not initialized yet
// It panics if -loglevel is specified but as illegal value
func getInstance(callerDepth int) *logrus.Logger {
	if instance == nil {
		formatter := LogFormat{}
		instance = logrus.New()
		instance.SetReportCaller(true)
		// in logrus timestamp format is specified using example
		formatter.TimestampFormat = "2006-01-02 15:04:05,123"
		instance.SetFormatter(&formatter)
		// set log level
		logLevel := flag.String("loglevel", "trace", "The level of logs (error, warning info, debug, trace etc...).")
		level, err := logrus.ParseLevel(*logLevel)
		if err != nil {
			logEntry(callerDepth + 1).Panic(err)
		}
		instance.SetLevel(level)
	}
	return instance
}

// Create log entry with additional data
// 1) goroutine id
// 2) caller: file and line log was called from
func logEntry(callerDepth int) *logrus.Entry {
	goid := util.GetGoID()
	additionalId, _ := goid_info.GetAdditionalIDInfo()
	_, file, no, ok := runtime.Caller(callerDepth + 1)
	caller := unknownValue
	if ok {
		caller = filepath.Base(file) + ":" + strconv.Itoa(no)
	}
	logEntry := getInstance(callerDepth + 1).WithFields(logrus.Fields{goIDField: strconv.FormatUint(goid, 10),
		additionalGoIDInfoField: additionalId,
		callerField:             caller})
	return logEntry
}

func Tracef(format string, args ...interface{}) {
	logEntry(startCallerDepth).Tracef(format, args...)
}

func Debug(args ...interface{}) {
	logEntry(startCallerDepth).Debug(args...)
}

func Debugf(format string, args ...interface{}) {
	logEntry(startCallerDepth).Debugf(format, args...)
}

func DebugfCustomDepth(callerDepth int, format string, args ...interface{}) {
	logEntry(callerDepth+1).Debugf(format, args...)
}

func Info(args ...interface{}) {
	logEntry(startCallerDepth).Info(args...)
}

func Infof(format string, args ...interface{}) {
	logEntry(startCallerDepth).Infof(format, args...)
}

func Warning(args ...interface{}) {
	logEntry(startCallerDepth).Warn(args...)
}

func Warningf(format string, args ...interface{}) {
	logEntry(startCallerDepth).Warnf(format, args...)
}

func Error(args ...interface{}) {
	logEntry(startCallerDepth).Error(args...)
}

func Errorln(args ...interface{}) {
	logEntry(startCallerDepth).Errorln(args...)
}

func Errorf(format string, args ...interface{}) {
	logEntry(startCallerDepth).Errorf(format, args...)
}

func Panicln(args ...interface{}) {
	logEntry(startCallerDepth).Panicln(args...)
}

func GetLevel() string {
	return getInstance(startCallerDepth).GetLevel().String()
}

func getStringFromCall(request interface{}, methodName string) string {
	var returnString string
	method := reflect.ValueOf(request).MethodByName(methodName)
	if method.IsValid() {
		returnValue := method.Call([]reflect.Value{})[0]
		returnString, _ = returnValue.Interface().(string)
	}
	return returnString
}

func setLoggerVolumeId(request interface{}) {
	volumeId := getStringFromCall(request, "GetVolumeId")
	goid_info.SetAdditionalIDInfo(volumeId)
}

func getFuncName() string {
	var funcName string
	programCounter, _, _, ok := runtime.Caller(2)
	details := runtime.FuncForPC(programCounter)
	if ok && details != nil {
		funcPath := details.Name()
		lastDot := strings.LastIndexByte(funcPath, '.')
		funcName = funcPath[lastDot+1:]
	}
	return funcName
}

func DebugfLogEnter(callerDepth int, format string, args ...interface{}) {
	DebugfCustomDepth(callerDepth+1, format, args...)
}

func DebugfLogExit(callerDepth int, format string, args ...interface{}) {
	DebugfCustomDepth(callerDepth+1, format, args...)
}

func logEnter(callerDepth int, funcName string, request interface{}) {
	callerDepth++
	var message = ">>>> %v"
	if getStringFromCall(request, "String") != "" {
		message += ": called with args %+v"
		var request = protosanitizer.StripSecrets(request)
		DebugfLogEnter(callerDepth, message, funcName, request)
	} else {
		DebugfLogEnter(callerDepth, message, funcName)
	}
}

func Enter(request interface{}) string {
	setLoggerVolumeId(request)

	funcName := getFuncName()
	if funcName != "" {
		logEnter(startCallerDepth, funcName, request)
	}
	return funcName
}

func Exit(funcName string) {
	if funcName != "" {
		DebugfLogExit(startCallerDepth, "<<<< %v", funcName)
	}
	goid_info.DeleteAdditionalIDInfo()
}
