package logger

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// ANSI colour codes for terminal output
// ─────────────────────────────────────────────────────────────────────────────
const (
	colReset  = "\033[0m"
	colGreen  = "\033[32m"
	colYellow = "\033[33m"
	colRed    = "\033[31m"
	colCyan   = "\033[36m"
	colWhite  = "\033[97m"
	colGray   = "\033[90m"
	colBold   = "\033[1m"
)

func statusColour(s int) string {
	switch {
	case s >= 500:
		return colRed
	case s >= 400:
		return colYellow
	case s >= 200:
		return colGreen
	default:
		return colWhite
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LogEntry — structured record written as one JSON line to the log file
// ─────────────────────────────────────────────────────────────────────────────

type LogEntry struct {
	Timestamp  string         `json:"timestamp"`
	Level      string         `json:"level"`
	Method     string         `json:"method,omitempty"`
	Path       string         `json:"path,omitempty"`
	Status     int            `json:"status,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Actor      string         `json:"actor,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
	Payload    any            `json:"payload,omitempty"`
	Response   any            `json:"response,omitempty"`
	Error      string         `json:"error,omitempty"`
	Message    string         `json:"message,omitempty"`
	Fields     map[string]any `json:"fields,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// LoggerService
// ─────────────────────────────────────────────────────────────────────────────

type LoggerService struct {
	Config        map[string]interface{}
	jsonFile      *os.File
	mu            sync.Mutex
	stopCh        chan struct{}
	wg            sync.WaitGroup
	currentJSON   string
	maxFileBytes  int64
	retentionDays int
	folderPath    string

	// stdoutLog writes plain lines to os.Stdout (always visible in terminal)
	stdoutLog *log.Logger
}

func NewLoggerService(config map[string]interface{}) *LoggerService {
	maxMB := intFromConfig(config, "max_file_mb", 50)
	retention := intFromConfig(config, "retention_days", 30)
	folder, _ := config["folder_path"].(string)
	if folder == "" {
		folder = "./logs"
	}
	return &LoggerService{
		Config:        config,
		stopCh:        make(chan struct{}),
		maxFileBytes:  int64(maxMB) * 1024 * 1024,
		retentionDays: retention,
		folderPath:    folder,
		stdoutLog:     log.New(os.Stdout, "", 0),
	}
}

func intFromConfig(cfg map[string]interface{}, key string, def int) int {
	if v, ok := cfg[key].(int); ok && v > 0 {
		return v
	}
	if v, ok := cfg[key].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

func (l *LoggerService) Name() string { return "Logger" }

func (l *LoggerService) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(l.folderPath, 0755); err != nil {
		return err
	}

	// Structured JSON log file (errors + full payload)
	jsonFile := l.nextFileName("structured", "jsonl")
	jf, err := os.OpenFile(jsonFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	l.jsonFile = jf
	l.currentJSON = jsonFile

	// Keep log.SetOutput pointing at stdout so existing log.Printf calls
	// in workers/db still print to the terminal.
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime)

	l.stdoutLog.Printf("%s[LoggerService]%s started → %s", colCyan, colReset, jsonFile)

	l.wg.Add(1)
	go l.backgroundWorker()
	return nil
}

func (l *LoggerService) Stop() error {
	close(l.stopCh)
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stdoutLog.Printf("%s[LoggerService]%s stopped", colCyan, colReset)
	if l.jsonFile != nil {
		return l.jsonFile.Close()
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Core logging methods
// ─────────────────────────────────────────────────────────────────────────────

// Log writes:
//   - a coloured human-readable line to stdout (always visible in terminal)
//   - a full JSON line to the structured log file (for retention/debugging)
func (l *LoggerService) Log(entry LogEntry) {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if entry.Level == "" {
		entry.Level = "INFO"
	}

	// ── Terminal output ──────────────────────────────────────────────────────
	l.terminalLine(entry)

	// ── JSON file ────────────────────────────────────────────────────────────
	l.writeJSON(entry)
}

func (l *LoggerService) terminalLine(e LogEntry) {
	ts := time.Now().Format("15:04:05")

	if e.Method != "" {
		// HTTP request line
		sc := statusColour(e.Status)
		line := fmt.Sprintf("%s%s%s  %s%-6s%s %s%-40s%s  %s%d%s  %s%dms%s  actor=%s%s%s",
			colGray, ts, colReset,
			colCyan, e.Method, colReset,
			colWhite, e.Path, colReset,
			sc+colBold, e.Status, colReset,
			colGray, e.DurationMs, colReset,
			colYellow, e.Actor, colReset,
		)

		// Payload (request body)
		if e.Payload != nil {
			pl := compactJSON(e.Payload)
			if pl != "" && pl != "null" {
				line += fmt.Sprintf("\n         %s↑ payload  %s%s%s", colGray, colWhite, pl, colReset)
			}
		}

		// Response (always shown for errors, truncated for success)
		if e.Response != nil {
			resp := compactJSON(e.Response)
			if resp != "" && resp != "null" {
				rc := colGray
				if e.Status >= 400 {
					rc = colRed
				}
				line += fmt.Sprintf("\n         %s↓ response %s%s%s", colGray, rc, resp, colReset)
			}
		}

		// Error summary on its own line (bright red)
		if e.Error != "" {
			line += fmt.Sprintf("\n         %s✖ error    %s%s%s", colGray, colRed+colBold, e.Error, colReset)
		}

		l.stdoutLog.Println(line)
		return
	}

	// Non-HTTP line
	lc := colWhite
	switch e.Level {
	case "ERROR":
		lc = colRed
	case "WARN":
		lc = colYellow
	case "AUDIT":
		lc = colCyan
	}
	msg := e.Message
	if e.Error != "" {
		msg += fmt.Sprintf("  %s%s%s", colRed, e.Error, colReset)
	}
	l.stdoutLog.Printf("%s%s%s  %s[%s]%s %s", colGray, ts, colReset, lc, e.Level, colReset, msg)
}

func (l *LoggerService) writeJSON(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.jsonFile == nil {
		return
	}
	b, _ := json.Marshal(entry)
	b = append(b, '\n')
	_, _ = l.jsonFile.Write(b)
}

// compactJSON marshals v to a compact JSON string, max 400 chars.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

// LogHTTP is the primary method called by the HTTP middleware.
func (l *LoggerService) LogHTTP(method, path string, status int, durationMs int64,
	actor string, payload, response any, errMsg string) {
	level := "INFO"
	if status >= 500 {
		level = "ERROR"
	} else if status >= 400 {
		level = "WARN"
	}
	l.Log(LogEntry{
		Level:      level,
		Method:     method,
		Path:       path,
		Status:     status,
		DurationMs: durationMs,
		Actor:      actor,
		Payload:    payload,
		Response:   response,
		Error:      errMsg,
	})
}

// LogAudit logs an audit event.
func (l *LoggerService) LogAudit(msg string) {
	l.Log(LogEntry{Level: "AUDIT", Message: msg})
}

// LogError logs an application error.
func (l *LoggerService) LogError(msg string, err error, fields map[string]any) {
	e := ""
	if err != nil {
		e = err.Error()
	}
	l.Log(LogEntry{Level: "ERROR", Message: msg, Error: e, Fields: fields})
}

// ─────────────────────────────────────────────────────────────────────────────
// Rotation & retention
// ─────────────────────────────────────────────────────────────────────────────

func (l *LoggerService) nextFileName(prefix, ext string) string {
	ts := time.Now().Format("20060102_150405")
	return filepath.Join(l.folderPath, fmt.Sprintf("%s_%s.%s", prefix, ts, ext))
}

func (l *LoggerService) rotateIfNeeded() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.jsonFile == nil || l.maxFileBytes <= 0 {
		return
	}
	info, err := l.jsonFile.Stat()
	if err != nil || info.Size() < l.maxFileBytes {
		return
	}
	l.jsonFile.Close()
	newPath := l.nextFileName("structured", "jsonl")
	jf, err := os.OpenFile(newPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	l.jsonFile = jf
	l.currentJSON = newPath
	l.stdoutLog.Printf("%s[LoggerService]%s rotated → %s", colCyan, colReset, newPath)
}

func (l *LoggerService) backgroundWorker() {
	defer l.wg.Done()
	rotateTicker := time.NewTicker(10 * time.Second)
	retentionTicker := time.NewTicker(24 * time.Hour)
	defer rotateTicker.Stop()
	defer retentionTicker.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-rotateTicker.C:
			l.rotateIfNeeded()
		case <-retentionTicker.C:
			l.zipAndCleanOldLogs()
		}
	}
}

func (l *LoggerService) zipAndCleanOldLogs() {
	if l.retentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -l.retentionDays)
	files, err := os.ReadDir(l.folderPath)
	if err != nil {
		return
	}
	zipName := filepath.Join(l.folderPath, fmt.Sprintf("logs_%s.zip", time.Now().Format("20060102")))
	zipFile, err := os.Create(zipName)
	if err != nil {
		return
	}
	defer zipFile.Close()
	zw := zip.NewWriter(zipFile)
	defer zw.Close()

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := filepath.Ext(f.Name())
		if ext != ".jsonl" {
			continue
		}
		fullPath := filepath.Join(l.folderPath, f.Name())
		if fullPath == l.currentJSON {
			continue
		}
		info, err := os.Stat(fullPath)
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		w, err := zw.Create(f.Name())
		if err != nil {
			continue
		}
		src, err := os.Open(fullPath)
		if err != nil {
			continue
		}
		_, _ = io.Copy(w, src)
		src.Close()
		os.Remove(fullPath)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Global singleton & package-level helpers
// ─────────────────────────────────────────────────────────────────────────────

var GlobalLogger *LoggerService

func SetGlobalLogger(l *LoggerService) { GlobalLogger = l }

// LogHTTP is a package-level HTTP log helper.
func LogHTTP(method, path string, status int, durationMs int64,
	actor string, payload, response any, errMsg string) {
	if GlobalLogger != nil {
		GlobalLogger.LogHTTP(method, path, status, durationMs, actor, payload, response, errMsg)
		return
	}
	// fallback: plain stdout
	fmt.Printf("[http] %s %s %d %dms actor=%s err=%s\n", method, path, status, durationMs, actor, errMsg)
}

// ParseBodyAsAny attempts to JSON-decode bytes into any.
// Falls back to the raw string if not valid JSON.
func ParseBodyAsAny(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(b), &v); err != nil {
		s := string(b)
		if len(s) > 400 {
			s = s[:400] + "…"
		}
		return s
	}
	return v
}
