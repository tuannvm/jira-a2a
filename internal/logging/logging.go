package logging

import (
	"go.uber.org/zap"
)

// Logger is the global logger instance for the application
var Logger *zap.SugaredLogger

func init() {
	logger, _ := zap.NewProduction()
	Logger = logger.Sugar()
}

// Top-level helpers for package alias usage
func Infof(format string, args ...interface{})  { Logger.Infof(format, args...) }
func Warnf(format string, args ...interface{})  { Logger.Warnf(format, args...) }
func Errorf(format string, args ...interface{}) { Logger.Errorf(format, args...) }
func Debugf(format string, args ...interface{}) { Logger.Debugf(format, args...) }
func Fatalf(format string, args ...interface{}) { Logger.Fatalf(format, args...) }
