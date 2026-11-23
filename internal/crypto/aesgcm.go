// AES-256-GCM 加解密（PRD FR-12）：模型 API Key 加密落库，密钥来自环境变量。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// ErrCiphertextInvalid 密文格式非法（过短或认证失败）。
var ErrCiphertextInvalid = errors.New("密文非法或已被篡改")

// AESGCM AES-256-GCM 加解密器。任意长度密钥经 SHA-256 归一为 32 字节。
type AESGCM struct {
	gcm cipher.AEAD
}

// NewAESGCM 用密钥字符串构造；密钥经 SHA-256 归一为 32 字节（与配置约定一致）。
func NewAESGCM(key string) (*AESGCM, error) {
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("初始化 AES: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM: %w", err)
	}
	return &AESGCM{gcm: gcm}, nil
}

// Encrypt 加密明文，输出 nonce||ciphertext。
func (a *AESGCM) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("生成 nonce: %w", err)
	}
	return a.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 解密 nonce||ciphertext；密文非法或被篡改返回 ErrCiphertextInvalid。
func (a *AESGCM) Decrypt(ciphertext []byte) ([]byte, error) {
	ns := a.gcm.NonceSize()
	if len(ciphertext) < ns+a.gcm.Overhead() {
		return nil, ErrCiphertextInvalid
	}
	plain, err := a.gcm.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
	if err != nil {
		return nil, ErrCiphertextInvalid
	}
	return plain, nil
}
