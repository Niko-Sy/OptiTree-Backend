package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/repository"

	"github.com/redis/go-redis/v9"
)

func buildProjectListCacheKey(params repository.ProjectListParams) string {
	keywordHash := hashProjectKeyword(params.Keyword)
	typePart := strings.TrimSpace(params.Type)
	if typePart == "" {
		typePart = "all"
	}
	sortBy := strings.TrimSpace(params.SortBy)
	if sortBy == "" {
		sortBy = "created_at"
	}
	sortOrder := strings.TrimSpace(params.SortOrder)
	if sortOrder == "" {
		sortOrder = "desc"
	}

	return fmt.Sprintf("%su:%s:t:%s:q:%s:s:%s:%s:p:%d:ps:%d",
		constant.RedisKeyProjectList,
		params.UserID,
		typePart,
		keywordHash,
		sortBy,
		sortOrder,
		params.Page,
		params.PageSize,
	)
}

func projectListIndexKey(userID string) string {
	return constant.RedisKeyProjectListIx + strings.TrimSpace(userID)
}

func hashProjectKeyword(keyword string) string {
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		return "none"
	}
	sum := sha1.Sum([]byte(kw))
	return hex.EncodeToString(sum[:8])
}

func addProjectListCacheIndex(ctx context.Context, rdb *redis.Client, userID, cacheKey string, indexTTLSeconds int64) {
	if rdb == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(cacheKey) == "" {
		return
	}
	ix := projectListIndexKey(userID)
	_ = rdb.SAdd(ctx, ix, cacheKey).Err()
	if indexTTLSeconds > 0 {
		_ = rdb.Expire(ctx, ix, time.Duration(indexTTLSeconds)*time.Second).Err()
	}
}

func invalidateProjectListCacheByUserIDs(ctx context.Context, rdb *redis.Client, userIDs []string) {
	if rdb == nil {
		return
	}
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		uid := strings.TrimSpace(userID)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}

		ix := projectListIndexKey(uid)
		keys, err := rdb.SMembers(ctx, ix).Result()
		if err == nil && len(keys) > 0 {
			_ = rdb.Del(ctx, keys...).Err()
		}
		_ = rdb.Del(ctx, ix).Err()
	}
}
