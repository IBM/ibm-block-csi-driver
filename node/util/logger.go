package util

import (
	"bytes"
	"fmt"
	"github.com/sirupsen/logrus"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

var instance *logrus.Logger

type LogFormat struct {
	TimestampFormat string
}

func (f *LogFormat) Format(entry *logrus.Entry) ([]byte, error) {
	var b *bytes.Buffer

	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}

	gid := entry.Data["gid"]
	caller := entry.Data["caller"]

	b.WriteString(entry.Time.Format(f.TimestampFormat) + "\t")
	b.WriteString(strings.ToUpper(entry.Level.String()) + "\t")
	b.WriteString(fmt.Sprintf("%12v", "[" + gid.(string)) + "]\t")
	b.WriteString("(" + caller.(string) + ")\t")
	b.WriteString(entry.Message)
	b.WriteString("\n")
	
	return b.Bytes(), nil
}

func getInstance() *logrus.Logger {
	if instance == nil {
		formatter := LogFormat{}
		instance = logrus.New()
		instance.SetReportCaller(true)
		formatter.TimestampFormat = "2006-01-02 15:04:05.1234567"
		instance.SetFormatter(&formatter)
	}
	return instance
}

func log(level logrus.Level, isAddNewLine bool, args ...interface{}) {
	gid := GetGoID()
	_, file, no, ok := runtime.Caller(2)
	caller := "Unknown"
	if ok {
		caller = filepath.Base(file) + ":" + strconv.Itoa(no)
	}
	logEntry := getInstance().WithFields(logrus.Fields{"gid": strconv.FormatUint(gid, 10), "caller": caller})
	if (isAddNewLine) {
		logEntry.Logln(level, args...)
	} else {
		logEntry.Log(level, args...)
	}
}

func Info(args ...interface{}) {
	log(logrus.InfoLevel, false, args...)
}

func Infoln(args ...interface{}) {
	log(logrus.InfoLevel, true, args...)
}

func Warn(args ...interface{}) {
	log(logrus.WarnLevel, false, args...)
}

func Warnln(args ...interface{}) {
	log(logrus.WarnLevel, true, args...)
}

func Error(args ...interface{}) {
	log(logrus.ErrorLevel, false, args...)
}

func Errorln(args ...interface{}) {
	log(logrus.ErrorLevel, true, args...)
}

func Fatal(err error) {
	log(logrus.ErrorLevel, false, err)
	panic(err)
}

func Fatalln(err error) {
	log(logrus.ErrorLevel, true, err)
	panic(err)
}
