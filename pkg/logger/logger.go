package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Config 日志配置
type Config struct {
	Level      string // debug / info / warn / error
	Output     string // stdout / file / both
	FilePath   string // 日志文件路径
	MaxSize    int    // 最大文件大小（MB）
	MaxBackups int    // 保留的旧日志文件数
	MaxAge     int    // 保留天数
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Level:      "info",
		Output:     "stdout",
		FilePath:   "logs/woodpecker.log",
		MaxSize:    10, // 10 MB
		MaxBackups: 3,
		MaxAge:     7, // 7 days
	}
}

// Logger 结构化日志接口
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
	With(fields ...Field) Logger
}

// Field 日志字段
type Field struct {
	Key   string
	Value interface{}
}

// String 创建字符串字段
func String(key, value string) Field {
	return Field{Key: key, Value: value}
}

// Int 创建整数字段
func Int(key string, value int) Field {
	return Field{Key: key, Value: value}
}

// ErrorField 创建错误字段
func ErrorField(err error) Field {
	return Field{Key: "error", Value: err}
}

// Duration 创建时间间隔字段
func Duration(key string, d time.Duration) Field {
	return Field{Key: key, Value: d}
}

// simpleLogger 简单实现（可替换为 zap/slog 等）
type simpleLogger struct {
	level  string
	writer io.Writer
	fields []Field
}

// New 创建日志器
func New(cfg Config) Logger {
	var writers []io.Writer

	if cfg.Output == "stdout" || cfg.Output == "both" {
		writers = append(writers, os.Stdout)
	}

	if cfg.Output == "file" || cfg.Output == "both" {
		// 确保日志目录存在
		if err := os.MkdirAll(filepath.Dir(cfg.FilePath), 0755); err != nil {
			log.Printf("创建日志目录失败: %v", err)
		} else {
			f, err := os.OpenFile(cfg.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				log.Printf("打开日志文件失败: %v", err)
			} else {
				writers = append(writers, f)
			}
		}
	}

	var writer io.Writer
	if len(writers) == 0 {
		writer = os.Stdout
	} else if len(writers) == 1 {
		writer = writers[0]
	} else {
		writer = io.MultiWriter(writers...)
	}

	return &simpleLogger{
		level:  cfg.Level,
		writer: writer,
	}
}

func (l *simpleLogger) Debug(msg string, fields ...Field) {
	if l.shouldLog("debug") {
		l.write("DEBUG", msg, fields)
	}
}

func (l *simpleLogger) Info(msg string, fields ...Field) {
	if l.shouldLog("info") {
		l.write("INFO", msg, fields)
	}
}

func (l *simpleLogger) Warn(msg string, fields ...Field) {
	if l.shouldLog("warn") {
		l.write("WARN", msg, fields)
	}
}

func (l *simpleLogger) Error(msg string, fields ...Field) {
	if l.shouldLog("error") {
		l.write("ERROR", msg, fields)
	}
}

func (l *simpleLogger) Fatal(msg string, fields ...Field) {
	l.write("FATAL", msg, fields)
	os.Exit(1)
}

func (l *simpleLogger) With(fields ...Field) Logger {
	return &simpleLogger{
		level:  l.level,
		writer: l.writer,
		fields: append(l.fields[:], fields...),
	}
}

func (l *simpleLogger) shouldLog(level string) bool {
	levels := map[string]int{
		"debug": 0,
		"info":  1,
		"warn":  2,
		"error": 3,
	}
	return levels[level] >= levels[l.level]
}

func (l *simpleLogger) write(level, msg string, fields []Field) {
	allFields := append(l.fields, fields...)

	// 简单格式: [时间] [级别] 消息 key=value ...
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := timestamp + " [" + level + "] " + msg

	for _, f := range allFields {
		logLine += " " + f.Key + "=" + formatValue(f.Value)
	}

	logLine += "\n"
	io.WriteString(l.writer, logLine)
}

func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return fmt.Sprintf("%d", val)
	case time.Duration:
		return val.String()
	case error:
		return val.Error()
	case float64:
		return fmt.Sprintf("%.2f", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
