//go:build windows

package logrus

import (
	"fmt"
	"sync/atomic"

	logrus "github.com/sirupsen/logrus"

	"github.com/winfsp/go-winfsp"
	"github.com/winfsp/go-winfsp/log"
)

type Logrus struct {
	Logger  *logrus.Logger
	Enable  log.Topics
	counter uint64
}

func (l *Logrus) Enabled(topics log.Topics) bool {
	return (l.Enable & topics) != 0
}

func logHandleDebugStruct(l *logrus.Entry, fields log.M, msg string) {
	smallFields := make(log.M)
	for name, field := range fields {
		if ds, ok := field.(winfsp.DebugStruct); ok {
			if m := ds.Fields(); m != nil {
				bigFields := make(log.M)
				for fieldName, value := range m {
					bigFields[name+"."+fieldName] = value
				}
				l.WithFields(bigFields).Info(msg)
				continue
			}
		}
		smallFields[name] = field
	}
	l.WithFields(smallFields).Info(msg)
}

func (l *Logrus) Call(name string, args log.M) string {
	if !l.Enabled(log.TopicCall) {
		return ""
	}
	cookie := fmt.Sprintf("%x", atomic.AddUint64(&l.counter, 1))
	logHandleDebugStruct(l.Logger.WithFields(logrus.Fields{
		"name":   name,
		"cookie": cookie,
	}), args, "call")
	return cookie
}

func (l *Logrus) Log(topics log.Topics, msg string) {
	if !l.Enabled(topics) {
		return
	}
	// TODO: assign different level for topics.
	l.Logger.Info(msg)
}

func (l *Logrus) Logf(topics log.Topics, msg string, args ...any) {
	if !l.Enabled(topics) {
		return
	}
	// TODO: assign different level for topics.
	l.Logger.Infof(msg, args...)
}

func (l *Logrus) Return(name, cookie string, rets log.M) {
	if !l.Enabled(log.TopicCall) {
		return
	}
	logHandleDebugStruct(l.Logger.WithFields(logrus.Fields{
		"name":   name,
		"cookie": cookie,
	}), rets, "return")
}

var _ log.Log = (*Logrus)(nil)

func Default() *Logrus {
	return &Logrus{
		Logger: logrus.New(),
		Enable: log.AllTopics,
	}
}
