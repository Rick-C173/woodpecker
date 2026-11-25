// Package mail 邮件发送（FR-16 邀请制注册）：SMTP 配置管理 + 发送抽象。
//
// 配置持久化在 settings 表（key=email_smtp，JSON），SMTP 密码经 AESGCM 加密落库。
// Sender 抽象便于测试替身；SMTP 未配置时返回 ErrNotConfigured，调用方降级
// （邀请仍创建成功，send_status=未发送，链接文本可手动转发）。
package mail

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured SMTP 未配置（管理端尚未保存发件设置）。
var ErrNotConfigured = errors.New("SMTP 未配置")

// Config SMTP 发件配置（settings 表 email_smtp 键，JSON 序列化存储）。
type Config struct {
	Host        string `json:"host"`         // SMTP 服务器
	Port        int    `json:"port"`         // 465=隐式 TLS；587/25=STARTTLS
	Username    string `json:"username"`     // 认证账号（可空=匿名）
	PasswordEnc string `json:"password_enc"` // AESGCM 加密后的密码（base64）
	From        string `json:"from"`         // 发件人（如 "Woodpecker <noreply@corp.com>"）
}

// Valid 配置是否完整可发送。
func (c *Config) Valid() bool {
	return c != nil && c.Host != "" && c.Port > 0 && c.From != ""
}

// Secret 解密并返回 SMTP 认证密码（未配置返回空串）。
func (c *Config) Secret(decrypt func(ciphertext []byte) ([]byte, error)) (string, error) {
	if c.PasswordEnc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(c.PasswordEnc)
	if err != nil {
		return "", fmt.Errorf("SMTP 密码密文格式错误: %w", err)
	}
	plain, err := decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("SMTP 密码解密失败: %w", err)
	}
	return string(plain), nil
}

// Sender 邮件发送抽象（测试替身可替换）。
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
	Configured() bool
}

// Store SMTP 配置存取（repo.SettingRepo 满足；Set 带更新人，审计语义见 settings）。
type Store interface {
	GetString(ctx context.Context, key, def string) (string, error)
	Set(ctx context.Context, key string, value []byte, updatedBy int64) error
}

const settingKey = "email_smtp"

// SMTPSettingStore 基于 settings 表的配置存取（密码加密委托 cipher）。
type SMTPSettingStore struct {
	Store   Store
	Encrypt func(plaintext []byte) ([]byte, error)  // crypto.AESGCM.Encrypt
	Decrypt func(ciphertext []byte) ([]byte, error) // crypto.AESGCM.Decrypt
}

// Load 读当前配置；未配置返回 ErrNotConfigured。
func (s *SMTPSettingStore) Load(ctx context.Context) (*Config, error) {
	raw, err := s.Store.GetString(ctx, settingKey, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, ErrNotConfigured
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("SMTP 配置损坏: %w", err)
	}
	return &c, nil
}

// Save 写配置（密码明文传入，加密落库；密码为空表示不改）。actorID 为更新人。
func (s *SMTPSettingStore) Save(ctx context.Context, actorID int64, c *Config, newPassword string) error {
	if newPassword != "" {
		enc, err := s.Encrypt([]byte(newPassword))
		if err != nil {
			return fmt.Errorf("SMTP 密码加密失败: %w", err)
		}
		c.PasswordEnc = base64.StdEncoding.EncodeToString(enc)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.Store.Set(ctx, settingKey, raw, actorID)
}

// SMTPSender 生产实现。
type SMTPSender struct {
	Store   *SMTPSettingStore
	Timeout int // 秒；<=0 取 15
}

func NewSMTPSender(store *SMTPSettingStore) *SMTPSender {
	return &SMTPSender{Store: store, Timeout: 15}
}

// Configured SMTP 是否已配置。
func (s *SMTPSender) Configured() bool {
	c, err := s.Store.Load(context.Background())
	return err == nil && c.Valid()
}

// Send 发送一封文本邮件（连接复用 STARTTLS/隐式 TLS 按端口判定）。
func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
	c, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	if !c.Valid() {
		return ErrNotConfigured
	}
	password, err := c.Secret(s.Store.Decrypt)
	if err != nil {
		return err
	}

	addr := c.Host + ":" + strconv.Itoa(c.Port)
	from := parseFromAddr(c.From)
	msg := buildMessage(c.From, to, subject, body)

	auth := smtp.PlainAuth("", c.Username, password, c.Host)
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 15
	}
	dialer := &net.Dialer{Timeout: time.Duration(timeout) * time.Second}
	hostOnly, _, _ := strings.Cut(addr, ":")
	tlsCfg := &tls.Config{ServerName: hostOnly}

	if c.Port == 465 {
		// 隐式 TLS：先建立 TLS 连接再协商 SMTP
		conn, derr := dialer.DialContext(ctx, "tcp", addr)
		if derr != nil {
			return fmt.Errorf("SMTP 连接失败(%s): %w", addr, derr)
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if herr := tlsConn.HandshakeContext(ctx); herr != nil {
			_ = conn.Close()
			return fmt.Errorf("TLS 握手失败(%s): %w", addr, herr)
		}
		cl, cerr := smtp.NewClient(tlsConn, hostOnly)
		if cerr != nil {
			_ = tlsConn.Close()
			return fmt.Errorf("SMTP 会话失败: %w", cerr)
		}
		defer func() { _ = cl.Quit() }()
		if c.Username != "" {
			if aerr := cl.Auth(auth); aerr != nil {
				return fmt.Errorf("SMTP 认证失败: %w", aerr)
			}
		}
		return deliver(cl, from, to, msg)
	}

	// 明文 + STARTTLS（服务器支持时升级）
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("SMTP 连接失败(%s): %w", addr, err)
	}
	cl, cerr := smtp.NewClient(conn, hostOnly)
	if cerr != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP 会话失败: %w", cerr)
	}
	defer func() { _ = cl.Quit() }()
	if ok, _ := cl.Extension("STARTTLS"); ok {
		if terr := cl.StartTLS(tlsCfg); terr != nil {
			return fmt.Errorf("STARTTLS 失败: %w", terr)
		}
	}
	if c.Username != "" {
		if aerr := cl.Auth(auth); aerr != nil {
			return fmt.Errorf("SMTP 认证失败: %w", aerr)
		}
	}
	return deliver(cl, from, to, msg)
}

// deliver 从 MailFrom 到正文交付（认证已在调用方完成）。
func deliver(cl *smtp.Client, from, to, msg string) error {
	if err := cl.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM 失败: %w", err)
	}
	if err := cl.Rcpt(to); err != nil {
		return fmt.Errorf("收件人被拒收(%s): %w", to, err)
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("DATA 失败: %w", err)
	}
	if _, err = w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("写入正文失败: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("提交正文失败: %w", err)
	}
	return nil
}

// buildMessage 构造 RFC 5322 文本邮件（UTF-8 subject 按 RFC 2047 B 编码）。
func buildMessage(from, to, subject, body string) string {
	return strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?=",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte(body)),
	}, "\r\n")
}

// parseFromAddr 从 "Display Name <addr@host>" 提取纯地址（MAIL FROM 需要）。
func parseFromAddr(from string) string {
	if u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(from, "<"), ">")); err == nil && u.Opaque != "" {
		return u.Opaque
	}
	if i := strings.LastIndex(from, "<"); i >= 0 && strings.HasSuffix(from, ">") {
		return strings.TrimSuffix(from[i+1:], ">")
	}
	return strings.TrimSpace(from)
}

// HashToken 邀请 token 的落库形式：sha256 hex（URL 明文仅出现在邮件/创建响应）。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
