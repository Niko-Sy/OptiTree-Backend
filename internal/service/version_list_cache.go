package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"optitree-backend/internal/constant"

	"github.com/redis/go-redis/v9"
)

func buildVersionListCacheKey(projectID string, page, pageSize int) string {
	return fmt.Sprintf("%sp:%s:page:%d:size:%d", constant.RedisKeyVersionList, strings.TrimSpace(projectID), page, pageSize)
}

func versionListIndexKey(projectID string) string {
	return constant.RedisKeyVersionListIx + strings.TrimSpace(projectID)
}

func addVersionListCacheIndex(ctx context.Context, rdb *redis.Client, projectID, cacheKey string, ttl time.Duration) {
	if rdb == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(cacheKey) == "" {
		return
	}
	ix := versionListIndexKey(projectID)
	_ = rdb.SAdd(ctx, ix, cacheKey).Err()
	if ttl > 0 {
		_ = rdb.Expire(ctx, ix, ttl).Err()
	}
}

func invalidateVersionListCacheByProject(ctx context.Context, rdb *redis.Client, projectID string) {
	if rdb == nil || strings.TrimSpace(projectID) == "" {
		return
	}
	ix := versionListIndexKey(projectID)
	keys, err := rdb.SMembers(ctx, ix).Result()
	if err == nil && len(keys) > 0 {
		_ = rdb.Del(ctx, keys...).Err()
	}
	_ = rdb.Del(ctx, ix).Err()
}
