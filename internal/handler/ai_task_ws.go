package handler

import (
	"net/http"
	"strings"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"
	jwtpkg "optitree-backend/pkg/jwt"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var taskWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type AITaskWSHandler struct {
	hub        *service.TaskProgressHub
	jwtManager *jwtpkg.Manager
	rdb        *redis.Client
	memberRepo *repository.MemberRepository
}

func NewAITaskWSHandler(
	hub *service.TaskProgressHub,
	jwtManager *jwtpkg.Manager,
	rdb *redis.Client,
	memberRepo *repository.MemberRepository,
) *AITaskWSHandler {
	return &AITaskWSHandler{
		hub:        hub,
		jwtManager: jwtManager,
		rdb:        rdb,
		memberRepo: memberRepo,
	}
}

// StreamTaskProgress handles GET /api/v1/ws/tasks/:projectId?token=...
func (h *AITaskWSHandler) StreamTaskProgress(c *gin.Context) {
	projectID := strings.TrimSpace(c.Param("projectId"))
	if projectID == "" {
		util.Fail(c, constant.CodeInvalidParam, "projectId 不能为空")
		return
	}

	tokenStr := strings.TrimSpace(c.Query("token"))
	if tokenStr == "" {
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}
	}
	if tokenStr == "" {
		util.FailUnauthorized(c)
		return
	}

	claims, err := h.jwtManager.ParseToken(tokenStr)
	if err != nil {
		if err == jwtpkg.ErrTokenExpired {
			util.Fail(c, constant.CodeTokenExpired, constant.MsgTokenExpired)
			return
		}
		util.FailUnauthorized(c)
		return
	}

	blacklistKey := constant.RedisKeyBlacklist + claims.JTI
	exists, err := h.rdb.Exists(c.Request.Context(), blacklistKey).Result()
	if err == nil && exists > 0 {
		util.FailUnauthorized(c)
		return
	}

	member, err := h.memberRepo.FindByProjectAndUser(projectID, claims.UserID)
	if err != nil {
		util.FailServerError(c)
		return
	}
	if member == nil {
		util.FailForbidden(c)
		return
	}

	conn, err := taskWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	events, unsubscribe := h.hub.Subscribe(projectID)
	defer unsubscribe()

	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	welcome := service.NewTaskProgressEvent("connected", projectID)
	welcome.StageLabel = "订阅成功"
	if err := conn.WriteJSON(welcome); err != nil {
		return
	}

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-done:
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(evt); err != nil {
				return
			}
		case <-pingTicker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(10*time.Second)); err != nil {
				return
			}
		}
	}
}
