// Package log provides a simple structured logger for Water Writer.
// It writes timestamped log entries to a file in the user's data directory
// and can optionally forward entries to a TUI callback for in-app display.
package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Level represents the severity of a log message.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger writes structured log entries to a file.
// It is thread-safe (protected by a mutex).
type Logger struct {
	mu      sync.Mutex
	file    *os.File
	verbose bool // if true, also log to stderr
}

// New creates a Logger that writes to the given file path.
// The file is truncated on each open (fresh log per session).
func New(logPath string) (*Logger, error) {
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return &Logger{file: f}, nil
}

// SetVerbose toggles stderr logging for debugging.
func (l *Logger) SetVerbose(v bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.verbose = v
}

// Close closes the underlying log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// Debug writes a debug-level message.
func (l *Logger) Debug(format string, args ...interface{}) {
	l.write(LevelDebug, format, args...)
}

// Info writes an info-level message and forwards to the TUI callback.
func (l *Logger) Info(format string, args ...interface{}) {
	l.write(LevelInfo, format, args...)
}

// Warn writes a warning-level message and forwards to the TUI callback.
func (l *Logger) Warn(format string, args ...interface{}) {
	l.write(LevelWarn, format, args...)
}

// Error writes an error-level message and forwards to the TUI callback.
func (l *Logger) Error(format string, args ...interface{}) {
	l.write(LevelError, format, args...)
}

func (l *Logger) write(level Level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	now := time.Now().Format("2006-01-02 15:04:05.000")
	line := fmt.Sprintf("[%s] %s: %s\n", now, level, msg)

	l.mu.Lock()
	if l.file != nil {
		l.file.WriteString(line)
	}
	verbose := l.verbose
	l.mu.Unlock()

	// Print to stderr when verbose mode is on (for CLI debugging).
	if verbose {
		os.Stderr.WriteString(line)
	}
}

// DefaultLogPath returns the default path for the log file.
func DefaultLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./waterwriter.log"
	}
	return filepath.Join(home, ".waterwriter", "waterwriter.log")
}
