package logger

import (
	"log"
	"os"
)

var (
	debugLogger *log.Logger
	infoLogger  *log.Logger
	warnLogger  *log.Logger
	errorLogger *log.Logger

	currentLevel = "info"
)

const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Init initializes the logger with the specified level
func Init(level string) {
	currentLevel = level
	debugLogger = log.New(os.Stdout, "[DEBUG] ", log.LstdFlags|log.Lshortfile)
	infoLogger = log.New(os.Stdout, "[INFO] ", log.LstdFlags)
	warnLogger = log.New(os.Stdout, "[WARN] ", log.LstdFlags)
	errorLogger = log.New(os.Stderr, "[ERROR] ", log.LstdFlags|log.Lshortfile)
}

func init() {
	// Default initialization
	Init(LevelInfo)
}

// Debug logs a debug message
func Debug(args ...interface{}) {
	if currentLevel == LevelDebug {
		debugLogger.Println(args...)
	}
}

// Debugf logs a formatted debug message
func Debugf(format string, args ...interface{}) {
	if currentLevel == LevelDebug {
		debugLogger.Printf(format, args...)
	}
}

// Info logs an info message
func Info(args ...interface{}) {
	infoLogger.Println(args...)
}

// Infof logs a formatted info message
func Infof(format string, args ...interface{}) {
	infoLogger.Printf(format, args...)
}

// Warn logs a warning message
func Warn(args ...interface{}) {
	warnLogger.Println(args...)
}

// Warnf logs a formatted warning message
func Warnf(format string, args ...interface{}) {
	warnLogger.Printf(format, args...)
}

// Error logs an error message
func Error(args ...interface{}) {
	errorLogger.Println(args...)
}

// Errorf logs a formatted error message
func Errorf(format string, args ...interface{}) {
	errorLogger.Printf(format, args...)
}
