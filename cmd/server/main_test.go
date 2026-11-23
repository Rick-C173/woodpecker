package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/config"
)

// maskDSN：数据源连接串脱敏（密码替换 ***），日志不泄露凭证。
func TestMaskDSN(t *testing.T) {
	assert.Equal(t,
		"postgres://woodpecker:***@127.0.0.1:5432/woodpecker?sslmode=disable",
		maskDSN("postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker?sslmode=disable"),
	)
	// 无密码：原样
	assert.Equal(t,
		"postgres://woodpecker@127.0.0.1:5432/woodpecker",
		maskDSN("postgres://woodpecker@127.0.0.1:5432/woodpecker"),
	)
	// 非法串：原样返回
	assert.Equal(t, "not-a-url", maskDSN("not-a-url"))
}

// logFileForSummary：未开启日志文件时显示关闭态。
func TestLogFileForSummary(t *testing.T) {
	assert.Equal(t, "logs/app.log", logFileForSummary(&config.Config{LogToFile: true, LogFilePath: "logs/app.log"}))
	assert.Equal(t, "（关闭）", logFileForSummary(&config.Config{LogToFile: false}))
}

// setupLogWriter：关闭=仅控制台；开启=目录自动创建+文件句柄；目录不可建=降级仅控制台。
func TestSetupLogWriter(t *testing.T) {
	// 关闭
	cfg := &config.Config{LogToFile: false}
	w := setupLogWriter(cfg)
	assert.NotNil(t, w)

	// 开启：目录自动创建，文件可写
	path := filepath.Join(t.TempDir(), "logs", "app.log")
	cfg = &config.Config{LogToFile: true, LogFilePath: path}
	closer = nil
	w = setupLogWriter(cfg)
	require.NotNil(t, w)
	require.NotNil(t, closer, "开启日志文件后应持有句柄")
	_, err := os.Stat(path)
	require.NoError(t, err, "日志目录应自动创建")
	_, _ = w.Write([]byte("hello"))
	_ = closer.Close()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
	closer = nil

	// 坏路径（父路径是普通文件，目录必创建失败）：降级仅控制台
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	cfg = &config.Config{LogToFile: true, LogFilePath: filepath.Join(blocker, "x.log")}
	closer = nil
	w = setupLogWriter(cfg)
	assert.NotNil(t, w, "打开失败应降级为控制台 writer")
	assert.Nil(t, closer)
	closer = nil
}
