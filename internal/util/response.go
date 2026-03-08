package util

import (
	"net/http"
	"time"

	"optitree-backend/internal/constant"

	"github.com/gin-gonic/gin"
)

type Response struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	RequestID string      `json:"requestId"`
	Timestamp string      `json:"timestamp"`
}

type ErrorResponse struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Errors    interface{} `json:"errors,omitempty"`
	RequestID string      `json:"requestId"`
	Timestamp string      `json:"timestamp"`
}

type PageData struct {
	List     interface{} `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
	HasMore  bool        `json:"hasMore"`
}

func getRequestID(c *gin.Context) string {
	if id, exists := c.Get("requestId"); exists {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return c.GetHeader("X-Request-Id")
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:      constant.CodeSuccess,
		Message:   constant.MsgSuccess,
		Data:      data,
		RequestID: getRequestID(c),
		Timestamp: now(),
	})
}

// SuccessNoData 成功无数据
func SuccessNoData(c *gin.Context) {
	c.JSON(http.StatusOK, Response{
		Code:      constant.CodeSuccess,
		Message:   constant.MsgSuccess,
		RequestID: getRequestID(c),
		Timestamp: now(),
	})
}

// PageSuccess 分页成功响应
func PageSuccess(c *gin.Context, list interface{}, total int64, page, pageSize int) {
	c.JSON(http.StatusOK, Response{
		Code:    constant.CodeSuccess,
		Message: constant.MsgSuccess,
		Data: PageData{
			List:     list,
			Total:    total,
			Page:     page,
			PageSize: pageSize,
			HasMore:  int64(page*pageSize) < total,
		},
		RequestID: getRequestID(c),
		Timestamp: now(),
	})
}

// Fail 失败响应（使用错误码）
func Fail(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, ErrorResponse{
		Code:      code,
		Message:   message,
		RequestID: getRequestID(c),
		Timestamp: now(),
	})
}

// FailWithErrors 带 errors 字段的失败响应
func FailWithErrors(c *gin.Context, code int, message string, errors interface{}) {
	c.JSON(http.StatusOK, ErrorResponse{
		Code:      code,
		Message:   message,
		Errors:    errors,
		RequestID: getRequestID(c),
		Timestamp: now(),
	})
}

// FailServerError 服务器内部错误
func FailServerError(c *gin.Context) {
	Fail(c, constant.CodeServerError, constant.MsgServerError)
}

// FailNotFound 资源不存在
func FailNotFound(c *gin.Context) {
	Fail(c, constant.CodeNotFound, constant.MsgNotFound)
}

// FailUnauthorized 未授权
func FailUnauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, ErrorResponse{
		Code:      constant.CodeUnauthorized,
		Message:   constant.MsgUnauthorized,
		RequestID: getRequestID(c),
		Timestamp: now(),
	})
	c.Abort()
}

// FailForbidden 无权限
func FailForbidden(c *gin.Context) {
	Fail(c, constant.CodeForbidden, constant.MsgForbidden)
	c.Abort()
}
