package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword 使用 bcrypt 哈希密码
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword 验证密码
func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// SHA256 计算字符串的 SHA256 哈希
func SHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// HashDocIDs 将文档 ID 列表哈希为稳定的字符串（顺序无关）
func HashDocIDs(docIDs []string) string {
	sorted := make([]string, len(docIDs))
	copy(sorted, docIDs)
	sort.Strings(sorted)
	return SHA256(strings.Join(sorted, ","))
}

// HashToken 使用 SHA256 对 token 做哈希存库
func HashToken(token string) string {
	return SHA256(fmt.Sprintf("token:%s", token))
}
