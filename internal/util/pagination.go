package util

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type PageQuery struct {
	Page      int    `form:"page"`
	PageSize  int    `form:"pageSize"`
	Keyword   string `form:"keyword"`
	SortBy    string `form:"sortBy"`
	SortOrder string `form:"sortOrder"`
}

// GetPagination 从 gin.Context 中解析分页参数
func GetPagination(c *gin.Context) (page, pageSize int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ = strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return
}

// Offset 计算 SQL OFFSET
func Offset(page, pageSize int) int {
	return (page - 1) * pageSize
}

// SafeSortBy 校验 sortBy 字段白名单
func SafeSortBy(sortBy string, allowed []string, defaultField string) string {
	for _, a := range allowed {
		if sortBy == a {
			return sortBy
		}
	}
	return defaultField
}

// SafeSortOrder 校验排序方向
func SafeSortOrder(order string) string {
	if order == "asc" {
		return "asc"
	}
	return "desc"
}
