package jwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrTokenExpired = errors.New("token已过期")
	ErrTokenInvalid = errors.New("token无效")
)

type Claims struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
	JTI    string `json:"jti"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret            []byte
	accessExpire      time.Duration
	refreshExpire     time.Duration
	refreshExpireLong time.Duration
}

func NewManager(secret string, accessExpire, refreshExpire, refreshExpireLong time.Duration) *Manager {
	return &Manager{
		secret:            []byte(secret),
		accessExpire:      accessExpire,
		refreshExpire:     refreshExpire,
		refreshExpireLong: refreshExpireLong,
	}
}

// GenerateAccessToken 生成 Access Token，返回 token 字符串和 jti
func (m *Manager) GenerateAccessToken(userID, role string) (string, string, error) {
	jti := uuid.New().String()
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		JTI:    jti,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessExpire)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   userID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	return signed, jti, err
}

// GenerateRefreshToken 生成 Refresh Token，返回 token 字符串和过期时间
func (m *Manager) GenerateRefreshToken(userID string, remember bool) (string, time.Time, error) {
	expire := m.refreshExpire
	if remember {
		expire = m.refreshExpireLong
	}
	jti := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(expire)
	claims := Claims{
		UserID: userID,
		JTI:    jti,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   userID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	return signed, expiresAt, err
}

// ParseToken 解析并验证 token，返回 Claims
func (m *Manager) ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrTokenInvalid
		}
		return m.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, ErrTokenInvalid
}
