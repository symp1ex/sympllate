package config

import "log"

type Logger interface {
	Warnf(format string, args ...any)
}

type stdLogger struct{}

func (stdLogger) Warnf(format string, args ...any) {
	log.Printf("[config] "+format, args...)
}

var cfgLogger Logger = stdLogger{}

func SetLogger(l Logger) {
	if l == nil {
		cfgLogger = stdLogger{}
		return
	}
	cfgLogger = l
}
