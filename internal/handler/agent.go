package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	agentcore "optitree-backend/internal/agent"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

const agentStreamHeartbeatInterval = 10 * time.Second

type agentServiceRuntime interface {
	Enabled() bool
	RunStream(ctx context.Context, session *agentcore.AgentSession, input agentcore.AgentRunInput, writeEvent func(map[string]interface{}) bool) (*agentcore.AgentRunOutput, error)
	GetPersistedSessionStatus(sessionID, userID string) (*agentcore.PersistedSessionStatus, error)
}

type AgentHandler struct {
	agentService agentServiceRuntime
	sessionMgr   *agentcore.AgentSessionManager
}

func NewAgentHandler(agentService agentServiceRuntime, sessionMgr *agentcore.AgentSessionManager) *AgentHandler {
	return &AgentHandler{agentService: agentService, sessionMgr: sessionMgr}
}

type agentStreamRequest struct {
	Message        string          `json:"message" binding:"required,max=2000"`
	Model          string          `json:"model"`
	GraphSnapshot  json.RawMessage `json:"graphSnapshot"`
	ClientRevision *int            `json:"clientRevision"`
	ReadOnly       bool            `json:"readOnly"`
	MaxToolRounds  *int            `json:"maxToolRounds"`
}

func (h *AgentHandler) AgentStream(c *gin.Context) {
	var req agentStreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	if req.MaxToolRounds != nil && *req.MaxToolRounds <= 0 {
		util.Fail(c, constant.CodeInvalidParam, "maxToolRounds 必须大于 0")
		return
	}

	if !h.agentService.Enabled() {
		util.Fail(c, constant.CodeForbidden, "Agent 功能未启用")
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		util.Fail(c, constant.CodeServerError, "streaming not supported by the server")
		return
	}

	sessionID := util.NewAgentSessionID()
	session := h.sessionMgr.NewSession(sessionID, c.Param("conversationId"), "", middleware.GetUserID(c), "")
	if err := h.sessionMgr.Create(session); err != nil {
		util.Fail(c, constant.CodeServerError, err.Error())
		return
	}
	keepSession := false
	defer func() {
		if keepSession {
			return
		}
		h.sessionMgr.Remove(sessionID)
	}()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")

	streamCtx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	var mu sync.Mutex
	closed := false
	writeEvent := func(payload map[string]interface{}) bool {
		b, _ := json.Marshal(payload)
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", b); err != nil {
			closed = true
			cancel()
			return false
		}
		flusher.Flush()
		return true
	}

	if !writeEvent(map[string]interface{}{
		"type":           "started",
		"sessionId":      sessionID,
		"conversationId": c.Param("conversationId"),
	}) {
		return
	}

	heartbeatStop := make(chan struct{})
	var heartbeatWG sync.WaitGroup
	heartbeatWG.Add(1)
	go func() {
		defer heartbeatWG.Done()
		ticker := time.NewTicker(agentStreamHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !writeEvent(map[string]interface{}{"type": "heartbeat"}) {
					return
				}
			case <-streamCtx.Done():
				return
			case <-heartbeatStop:
				return
			}
		}
	}()
	defer func() {
		close(heartbeatStop)
		heartbeatWG.Wait()
	}()

	maxToolRounds := 0
	if req.MaxToolRounds != nil {
		maxToolRounds = *req.MaxToolRounds
	}

	out, err := h.agentService.RunStream(streamCtx, session, agentcore.AgentRunInput{
		ConversationID: c.Param("conversationId"),
		UserID:         middleware.GetUserID(c),
		Message:        req.Message,
		Model:          req.Model,
		GraphSnapshot:  req.GraphSnapshot,
		ClientRevision: req.ClientRevision,
		ReadOnly:       req.ReadOnly,
		MaxToolRounds:  maxToolRounds,
	}, writeEvent)
	if err != nil {
		snap := session.Snapshot()
		if errors.Is(err, context.Canceled) && (snap.State == agentcore.StatePausedForConfirm || snap.State == agentcore.StatePausedForPreview) {
			keepSession = true
		}
		if out != nil {
			_ = writeEvent(map[string]interface{}{
				"type":               "done",
				"conversationId":     out.ConversationID,
				"sessionId":          out.SessionID,
				"userMessageId":      out.UserMessageID,
				"assistantMessageId": out.AssistantMessageID,
				"tokensUsed":         out.TokensUsed,
				"modelId":            out.ModelUsed,
				"stats": map[string]interface{}{
					"serverOps": out.ServerOps,
					"clientOps": out.ClientOps,
					"hybridOps": out.HybridOps,
				},
				"errorCode":    mapAgentErrorCode(err),
				"errorMessage": err.Error(),
			})
			return
		}
		_ = writeEvent(map[string]interface{}{
			"type":    "error",
			"code":    mapAgentErrorCode(err),
			"message": err.Error(),
		})
		return
	}

	if out == nil {
		_ = writeEvent(map[string]interface{}{"type": "done", "sessionId": sessionID})
		return
	}

	snap := session.Snapshot()
	if snap.State == agentcore.StatePausedForConfirm || snap.State == agentcore.StatePausedForPreview {
		keepSession = true
	}
	_ = writeEvent(map[string]interface{}{
		"type":               "done",
		"conversationId":     out.ConversationID,
		"sessionId":          out.SessionID,
		"userMessageId":      out.UserMessageID,
		"assistantMessageId": out.AssistantMessageID,
		"tokensUsed":         out.TokensUsed,
		"modelId":            out.ModelUsed,
		"stats": map[string]interface{}{
			"serverOps": out.ServerOps,
			"clientOps": out.ClientOps,
			"hybridOps": out.HybridOps,
		},
	})
}

type agentConfirmRequest struct {
	CallID         string   `json:"callId" binding:"required"`
	Approved       bool     `json:"approved"`
	ApprovedOps    []string `json:"approvedOps"`
	ContinueRounds int      `json:"continueRounds"`
}

func (h *AgentHandler) AgentConfirm(c *gin.Context) {
	var req agentConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	sessionID := c.Param("sessionId")
	session, ok := h.sessionMgr.Get(sessionID)
	if !ok || session == nil {
		util.Fail(c, constant.CodeAgentSessionNotFound, constant.MsgAgentSessionNotFound)
		return
	}
	if session.UserID != middleware.GetUserID(c) {
		util.FailForbidden(c)
		return
	}

	err := h.sessionMgr.Confirm(sessionID, agentcore.ConfirmSignal{
		CallID:         req.CallID,
		Approved:       req.Approved,
		ApprovedOps:    req.ApprovedOps,
		ContinueRounds: req.ContinueRounds,
	})
	if err != nil {
		h.handleSessionError(c, err)
		return
	}
	util.SuccessNoData(c)
}

func (h *AgentHandler) AgentCancel(c *gin.Context) {
	sessionID := c.Param("sessionId")
	session, ok := h.sessionMgr.Get(sessionID)
	if !ok || session == nil {
		util.Fail(c, constant.CodeAgentSessionNotFound, constant.MsgAgentSessionNotFound)
		return
	}
	if session.UserID != middleware.GetUserID(c) {
		util.FailForbidden(c)
		return
	}

	if err := h.sessionMgr.Cancel(sessionID); err != nil {
		h.handleSessionError(c, err)
		return
	}
	util.SuccessNoData(c)
}

func (h *AgentHandler) AgentStatus(c *gin.Context) {
	sessionID := c.Param("sessionId")
	session, ok := h.sessionMgr.Get(sessionID)
	if ok && session != nil {
		if session.UserID != middleware.GetUserID(c) {
			util.FailForbidden(c)
			return
		}
		snap := session.Snapshot()
		canConfirm := snap.State == agentcore.StatePausedForConfirm || snap.State == agentcore.StatePausedForPreview
		util.Success(c, gin.H{
			"session":     snap,
			"source":      "memory",
			"canConfirm":  canConfirm,
			"canResume":   canConfirm,
			"pendingTool": snap.PendingTool,
			"expiresAt":   snap.ExpiresAt,
		})
		return
	}

	persisted, err := h.agentService.GetPersistedSessionStatus(sessionID, middleware.GetUserID(c))
	if err != nil {
		switch {
		case errors.Is(err, agentcore.ErrSessionNotFound):
			util.Fail(c, constant.CodeAgentSessionNotFound, constant.MsgAgentSessionNotFound)
		case errors.Is(err, agentcore.ErrAgentPermissionDenied):
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{
		"session":        persisted.Session,
		"runtimeSummary": persisted.RuntimeSummary,
		"source":         "db",
		"canConfirm":     persisted.CanConfirm,
		"canResume":      persisted.CanResume,
	})
}

func (h *AgentHandler) AgentResume(c *gin.Context) {
	sessionID := c.Param("sessionId")
	if session, ok := h.sessionMgr.Get(sessionID); ok && session != nil {
		if session.UserID != middleware.GetUserID(c) {
			util.FailForbidden(c)
			return
		}
		snap := session.Snapshot()
		canConfirm := snap.State == agentcore.StatePausedForConfirm || snap.State == agentcore.StatePausedForPreview
		util.Success(c, gin.H{
			"session":    snap,
			"source":     "memory",
			"canConfirm": canConfirm,
			"canResume":  canConfirm,
		})
		return
	}

	persisted, err := h.agentService.GetPersistedSessionStatus(sessionID, middleware.GetUserID(c))
	if err != nil {
		switch {
		case errors.Is(err, agentcore.ErrSessionNotFound):
			util.Fail(c, constant.CodeAgentSessionNotFound, constant.MsgAgentSessionNotFound)
		case errors.Is(err, agentcore.ErrAgentPermissionDenied):
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}

	util.Success(c, gin.H{
		"session":        persisted.Session,
		"runtimeSummary": persisted.RuntimeSummary,
		"source":         "db",
		"canConfirm":     persisted.CanConfirm,
		"canResume":      persisted.CanResume,
	})
}

func (h *AgentHandler) handleSessionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agentcore.ErrSessionNotFound):
		util.Fail(c, constant.CodeAgentSessionNotFound, constant.MsgAgentSessionNotFound)
	case errors.Is(err, agentcore.ErrSessionConfirmTimeout):
		util.Fail(c, constant.CodeAgentSessionTimeout, constant.MsgAgentSessionTimeout)
	case errors.Is(err, agentcore.ErrSessionClosed):
		util.Fail(c, constant.CodeConflict, constant.MsgConflict)
	case errors.Is(err, agentcore.ErrSessionNotWaiting):
		util.Fail(c, constant.CodeConflict, "当前会话不处于等待确认状态")
	default:
		util.FailServerError(c)
	}
}

func mapAgentErrorCode(err error) int {
	switch {
	case errors.Is(err, agentcore.ErrAgentDisabled):
		return constant.CodeForbidden
	case errors.Is(err, agentcore.ErrAgentConversationNotFound):
		return constant.CodeNotFound
	case errors.Is(err, agentcore.ErrAgentPermissionDenied):
		return constant.CodeForbidden
	case errors.Is(err, agentcore.ErrAgentMessageEmpty):
		return constant.CodeInvalidParam
	case errors.Is(err, agentcore.ErrAgentInvalidGraphSnapshot):
		return constant.CodeInvalidParam
	case errors.Is(err, agentcore.ErrAgentClientRevisionConflict):
		return constant.CodeVersionConflict
	case errors.Is(err, agentcore.ErrSessionConfirmTimeout):
		return constant.CodeAgentSessionTimeout
	case errors.Is(err, agentcore.ErrSessionNotFound):
		return constant.CodeAgentSessionNotFound
	case errors.Is(err, agentcore.ErrAgentMaxRoundsExceeded):
		return constant.CodeAgentSafetyBlocked
	case errors.Is(err, agentcore.ErrAgentMaxToolCalls), errors.Is(err, agentcore.ErrAgentRateLimited):
		return constant.CodeAgentMaxToolCalls
	case errors.Is(err, agentcore.ErrAgentLoopDetected), errors.Is(err, agentcore.ErrAgentNodeLimitExceeded), errors.Is(err, agentcore.ErrAgentSessionInactive):
		return constant.CodeAgentSafetyBlocked
	default:
		return constant.CodeServerError
	}
}
