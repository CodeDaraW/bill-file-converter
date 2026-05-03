package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func (l *processLogger) Close() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Close()
}

// auditWriter persists provider raw requests, raw responses and failure
// summaries as standalone JSON files under intermediateDir/audit. Keeping
// these large JSON blobs out of the line-based log keeps the log file small
// enough to comfortably open and syntax-highlight in editors.
type auditWriter struct {
	dir string
	mu  sync.Mutex
}

func newAuditWriter(dir string) (*auditWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &auditWriter{dir: dir}, nil
}

func (a *auditWriter) writeRaw(name string, content string) (string, error) {
	if a == nil || content == "" {
		return "", nil
	}
	path := filepath.Join(a.dir, name)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (a *auditWriter) writeJSON(name string, payload any) (string, error) {
	if a == nil {
		return "", nil
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(a.dir, name)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
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
	if err == nil || options.auditWriter == nil {
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
	path, writeErr := options.auditWriter.writeJSON("failure.json", artifact)
	if writeErr != nil {
		logErrorf(options, "failed to persist failure.json: %s", writeErr)
		return
	}
	logErrorf(options, "wrote failure summary to %s", path)
}
