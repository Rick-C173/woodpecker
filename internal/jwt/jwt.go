// Package jwt JWT 签发与解析（HS256）。
// claims：sub=用户ID、adm=是否管理员、ver=users.token_version（禁用/改密即失效，见 API 设计 §1）。
package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"woodpecker/internal/model"
)

var (
	ErrTokenInvalid = errors.New("token 无效")
	ErrTokenExpired = errors.New("token 已过期")
)

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) Issue(u *model.User) (string, error) {
	claims := jwt.MapClaims{
		"sub": fmt.Sprint(u.ID),
		"adm": u.IsAdmin,
		"ver": u.TokenVersion,
		"exp": time.Now().Add(i.ttl).Unix(),
		"iat": time.Now().Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
}

func (i *Issuer) Parse(tokenStr string) (*Claims, error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrTokenInvalid
		}
		return i.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	mc, ok := t.Claims.(jwt.MapClaims)
	if !ok || !t.Valid {
		return nil, ErrTokenInvalid
	}
	sub, _ := mc["sub"].(string)
	adm, _ := mc["adm"].(bool)
	verF, _ := mc["ver"].(float64)
	id, err := parseID(sub)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	return &Claims{UserID: id, IsAdmin: adm, TokenVersion: int(verF)}, nil
}

type Claims struct {
	UserID       int64
	IsAdmin      bool
	TokenVersion int
}

func parseID(s string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(s, "%d", &id)
	return id, err
}
