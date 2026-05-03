package core

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type processLogger struct {
	path   string
	taskID string
	file   *os.File
	mu     sync.Mutex
}

type LogLevel string

const (
	LogVerbose LogLevel = "verbose"
	LogInfo    LogLevel = "info"
	LogWarning LogLevel = "warning"
	LogError   LogLevel = "error"
)

func normalizeLogLevel(level LogLevel) LogLevel {
	switch level {
	case LogVerbose, LogInfo, LogWarning, LogError:
		return level
	default:
		return LogInfo
	}
}

func newProcessLogger(path string, taskID string) (*processLogger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &processLogger{path: path, taskID: taskID, file: file}, nil
}

func (l *processLogger) Write(level LogLevel, message string) string {
	if l == nil || l.file == nil {
		return ""
	}
	level = normalizeLogLevel(level)
	line := fmt.Sprintf("%s [%s] [%s] %s", time.Now().Format(time.RFC3339), strings.ToUpper(string(level)), l.taskID, message)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.file.WriteString(line + "\n")
	return line
}

func (l *processLogger) WriteBlock(level LogLevel, label string, content string) {
	if l == nil || l.file == nil || content == "" {
		return
	}
	level = normalizeLogLevel(level)
	timestamp := time.Now().Format(time.RFC3339)
	header := fmt.Sprintf("%s [%s] [%s] %s BEGIN", timestamp, strings.ToUpper(string(level)), l.taskID, label)
	footer := fmt.Sprintf("%s [%s] [%s] %s END", timestamp, strings.ToUpper(string(level)), l.taskID, label)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.file.WriteString(header + "\n")
	_, _ = l.file.WriteString(content + "\n")
	_, _ = l.file.WriteString(footer + "\n")
}

func (l *processLogger) Close() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Close()
}

type failureArtifact struct {
	Time       string     `json:"time"`
	TaskID     string     `json:"task_id"`
	Source     SourceInfo `json:"source"`
	AdapterKey string     `json:"adapter_key,omitempty"`
	Adapter    string     `json:"adapter,omitempty"`
	Error      string     `json:"error"`
}

func logFailure(options Options, input Input, adapterName string, err error) {
	if err == nil {
		return
	}
	artifact := failureArtifact{
		Time:       time.Now().Format(time.RFC3339),
		TaskID:     options.taskID,
		Source:     sourceInfo(input),
		AdapterKey: options.AdapterKey,
		Error:      err.Error(),
	}
	if adapterName != "" {
		artifact.Adapter = adapterName
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return
	}
	logBlock(options, LogError, "failure", string(data))
}
