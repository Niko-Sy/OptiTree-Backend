package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewID 生成带前缀的唯一 ID：{prefix}_{timestamp}_{random6}
func NewID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b))
}

// NewUserID 生成用户 ID
func NewUserID() string {
	return NewID("user")
}

// NewProjectID 生成项目 ID
func NewProjectID() string {
	return NewID("proj")
}

// NewDocumentID 生成文档 ID
func NewDocumentID() string {
	return NewID("doc")
}

// NewVersionID 生成版本 ID
func NewVersionID() string {
	return NewID("ver")
}

// NewMemberID 生成成员 ID
func NewMemberID() string {
	return NewID("member")
}

// NewAITaskID 生成 AI 任务 ID
func NewAITaskID() string {
	return NewID("task")
}

// NewInviteID 生成邀请 ID
func NewInviteID() string {
	return NewID("invite")
}

// NewConversationID 生成会话 ID
func NewConversationID() string {
	return NewID("conv")
}

// NewMessageID 生成消息 ID
func NewMessageID() string {
	return NewID("msg")
}

// RandomToken 生成 N 字节的随机十六进制 token
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
