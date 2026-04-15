package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentcore "optitree-backend/internal/agent"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/model"

	"github.com/gin-gonic/gin"
)

type fakeAgentService struct {
	enabled      bool
	runStreamFn  func(ctx context.Context, session *agentcore.AgentSession, input agentcore.AgentRunInput, writeEvent func(map[string]interface{}) bool) (*agentcore.AgentRunOutput, error)
	persistedOut *agentcore.PersistedSessionStatus
	persistedErr error
}

func (f *fakeAgentService) Enabled() bool {
	return f.enabled
}

func (f *fakeAgentService) RunStream(
	ctx context.Context,
	session *agentcore.AgentSession,
	input agentcore.AgentRunInput,
	writeEvent func(map[string]interface{}) bool,
) (*agentcore.AgentRunOutput, error) {
	if f.runStreamFn == nil {
		return nil, nil
	}
	return f.runStreamFn(ctx, session, input, writeEvent)
}

func (f *fakeAgentService) GetPersistedSessionStatus(sessionID, userID string) (*agentcore.PersistedSessionStatus, error) {
	if f.persistedErr != nil {
		return nil, f.persistedErr
	}
	return f.persistedOut, nil
}

func TestAgentStream_SSESequence_StartedContentToolDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := agentcore.NewAgentSessionManager(time.Minute)
	svc := &fakeAgentService{
		enabled: true,
		runStreamFn: func(ctx context.Context, session *agentcore.AgentSession, input agentcore.AgentRunInput, writeEvent func(map[string]interface{}) bool) (*agentcore.AgentRunOutput, error) {
			if !writeEvent(map[string]interface{}{"type": "content", "content": "开始处理"}) {
				return nil, errors.New("stream closed")
			}
			if !writeEvent(map[string]interface{}{"type": "tool_call_start", "callId": "call_1", "tool": "update_node"}) {
				return nil, errors.New("stream closed")
			}
			if !writeEvent(map[string]interface{}{"type": "tool_call_result", "callId": "call_1", "tool": "update_node", "success": true}) {
				return nil, errors.New("stream closed")
			}

			return &agentcore.AgentRunOutput{
				ConversationID:     input.ConversationID,
				SessionID:          session.ID,
				UserMessageID:      "msg_user_1",
				AssistantMessageID: "msg_assistant_1",
				TokensUsed:         12,
				ModelUsed:          "test-model",
				ServerOps:          1,
			}, nil
		},
	}

	h := NewAgentHandler(svc, mgr)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, "user_1")
		c.Next()
	})
	r.POST("/assistant/conversations/:conversationId/agent/stream", h.AgentStream)

	req := httptest.NewRequest(http.MethodPost, "/assistant/conversations/conv_1/agent/stream", strings.NewReader(`{"message":"请修改节点","model":"qwen3"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	eventTypes, err := parseSSEEventTypes(w.Body.String())
	if err != nil {
		t.Fatalf("parse sse events failed: %v", err)
	}
	if err := assertSSESequence(eventTypes); err != nil {
		t.Fatalf("invalid SSE sequence: %v\nall events: %v\nraw: %s", err, eventTypes, w.Body.String())
	}
}

func TestAgentStream_ForwardsThinkingEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := agentcore.NewAgentSessionManager(time.Minute)
	svc := &fakeAgentService{
		enabled: true,
		runStreamFn: func(ctx context.Context, session *agentcore.AgentSession, input agentcore.AgentRunInput, writeEvent func(map[string]interface{}) bool) (*agentcore.AgentRunOutput, error) {
			if !writeEvent(map[string]interface{}{"type": "thinking", "phase": "context_loaded", "message": "已加载会话图上下文，开始规划执行步骤"}) {
				return nil, errors.New("stream closed")
			}
			if !writeEvent(map[string]interface{}{"type": "thinking", "phase": "round_start", "round": 1, "message": "第 1 轮：正在规划下一步"}) {
				return nil, errors.New("stream closed")
			}
			if !writeEvent(map[string]interface{}{"type": "content", "content": "开始处理"}) {
				return nil, errors.New("stream closed")
			}

			return &agentcore.AgentRunOutput{
				ConversationID: input.ConversationID,
				SessionID:      session.ID,
				UserMessageID:  "msg_user_1",
				TokensUsed:     8,
				ModelUsed:      "test-model",
			}, nil
		},
	}

	h := NewAgentHandler(svc, mgr)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, "user_1")
		c.Next()
	})
	r.POST("/assistant/conversations/:conversationId/agent/stream", h.AgentStream)

	req := httptest.NewRequest(http.MethodPost, "/assistant/conversations/conv_1/agent/stream", strings.NewReader(`{"message":"请分析当前进度"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	events, err := parseSSEPayloadObjects(w.Body.String())
	if err != nil {
		t.Fatalf("parse sse payloads failed: %v", err)
	}

	foundThinking := false
	foundRoundThinking := false
	for _, evt := range events {
		typeName, _ := evt["type"].(string)
		if typeName != "thinking" {
			continue
		}
		foundThinking = true
		phase, _ := evt["phase"].(string)
		message, _ := evt["message"].(string)
		if strings.TrimSpace(phase) == "round_start" && strings.TrimSpace(message) != "" {
			if round, ok := evt["round"].(float64); ok && int(round) == 1 {
				foundRoundThinking = true
			}
		}
	}

	if !foundThinking {
		t.Fatalf("expected at least one thinking event, raw=%s", w.Body.String())
	}
	if !foundRoundThinking {
		t.Fatalf("expected round_start thinking event with round, raw=%s", w.Body.String())
	}
}

func TestAgentConfirm_AcceptsContinueRounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := agentcore.NewAgentSessionManager(time.Minute)
	session := mgr.NewSession("s5", "c1", "p1", "user_1", "faultTree")
	if err := mgr.Create(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	session.SetPending("iter_limit_10_10", "iteration_limit_continue", nil)
	session.SetState(agentcore.StatePausedForConfirm)

	received := make(chan agentcore.ConfirmSignal, 1)
	go func() {
		received <- <-session.ConfirmChan()
	}()

	svc := &fakeAgentService{enabled: true}
	h := NewAgentHandler(svc, mgr)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, "user_1")
		c.Next()
	})
	r.POST("/agent/sessions/:sessionId/confirm", h.AgentConfirm)

	req := httptest.NewRequest(http.MethodPost, "/agent/sessions/s5/confirm", strings.NewReader(`{"callId":"iter_limit_10_10","approved":true,"continueRounds":4}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	select {
	case got := <-received:
		if got.ContinueRounds != 4 {
			t.Fatalf("expected continueRounds=4, got %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive confirm signal")
	}
}

func TestAgentConfirm_AllowsEmptyCallIDForUniquePending(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := agentcore.NewAgentSessionManager(time.Minute)
	session := mgr.NewSession("s_empty_call", "c1", "p1", "user_1", "faultTree")
	if err := mgr.Create(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	session.SetPending("call_expected", "update_node", []byte(`{"nodeId":"n1"}`))
	session.SetState(agentcore.StatePausedForConfirm)

	received := make(chan agentcore.ConfirmSignal, 1)
	go func() {
		received <- <-session.ConfirmChan()
	}()

	h := NewAgentHandler(&fakeAgentService{enabled: true}, mgr)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, "user_1")
		c.Next()
	})
	r.POST("/agent/sessions/:sessionId/confirm", h.AgentConfirm)

	req := httptest.NewRequest(http.MethodPost, "/agent/sessions/s_empty_call/confirm", strings.NewReader(`{"approved":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	select {
	case got := <-received:
		if got.CallID != "call_expected" || !got.Approved {
			t.Fatalf("expected pending call to be filled, got %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive confirm signal")
	}
}

func TestAgentStream_PassesSnapshotOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := agentcore.NewAgentSessionManager(time.Minute)
	svc := &fakeAgentService{
		enabled: true,
		runStreamFn: func(ctx context.Context, session *agentcore.AgentSession, input agentcore.AgentRunInput, writeEvent func(map[string]interface{}) bool) (*agentcore.AgentRunOutput, error) {
			if !input.ReadOnly {
				t.Fatalf("expected readOnly=true")
			}
			if input.ClientRevision == nil || *input.ClientRevision != 7 {
				t.Fatalf("unexpected clientRevision: %+v", input.ClientRevision)
			}
			if input.MaxToolRounds != 3 {
				t.Fatalf("unexpected maxToolRounds: %d", input.MaxToolRounds)
			}
			if len(input.GraphSnapshot) == 0 {
				t.Fatalf("expected graphSnapshot to be passed through")
			}
			if strings.Join(input.FocusNodeIDs, ",") != "gate_1,basic_2" {
				t.Fatalf("unexpected focusNodeIds: %+v", input.FocusNodeIDs)
			}
			if strings.Join(input.SelectedNodeIDs, ",") != "gate_3" {
				t.Fatalf("unexpected selectedNodeIds: %+v", input.SelectedNodeIDs)
			}
			return &agentcore.AgentRunOutput{ConversationID: input.ConversationID, SessionID: session.ID, UserMessageID: "msg_u"}, nil
		},
	}

	h := NewAgentHandler(svc, mgr)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, "user_1")
		c.Next()
	})
	r.POST("/assistant/conversations/:conversationId/agent/stream", h.AgentStream)

	body := `{"message":"分析当前图","model":"qwen3","graphSnapshot":{"nodes":[],"edges":[]},"clientRevision":7,"readOnly":true,"maxToolRounds":3,"focusNodeIds":["gate_1","basic_2"],"selectedNodeIds":["gate_3"]}`
	req := httptest.NewRequest(http.MethodPost, "/assistant/conversations/conv_1/agent/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestAgentStatus_ReturnsMemoryPendingFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := agentcore.NewAgentSessionManager(time.Minute)
	session := mgr.NewSession("s_status_mem", "c1", "p1", "user_1", "faultTree")
	if err := mgr.Create(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	session.SetPending("call_1", "update_node", []byte(`{"nodeId":"n1"}`))
	session.SetState(agentcore.StatePausedForConfirm)

	h := NewAgentHandler(&fakeAgentService{enabled: true}, mgr)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, "user_1")
		c.Next()
	})
	r.GET("/agent/sessions/:sessionId/status", h.AgentStatus)

	req := httptest.NewRequest(http.MethodGet, "/agent/sessions/s_status_mem/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	data := unwrapResponseData(t, w.Body.String())
	if source, _ := data["source"].(string); source != "memory" {
		t.Fatalf("expected source=memory, got %v", data["source"])
	}
	if canConfirm, _ := data["canConfirm"].(bool); !canConfirm {
		t.Fatalf("expected canConfirm=true, got %v", data["canConfirm"])
	}
	if canResume, _ := data["canResume"].(bool); !canResume {
		t.Fatalf("expected canResume=true, got %v", data["canResume"])
	}
}

func TestAgentResume_ReturnsDBRuntimeWhenMemoryMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	expiresAt := time.Now().UTC().Add(time.Minute)

	svc := &fakeAgentService{
		enabled: true,
		persistedOut: &agentcore.PersistedSessionStatus{
			Session: &model.AgentSession{ID: "s_resume_db", UserID: "user_1", State: agentcore.StatePausedForConfirm},
			RuntimeSummary: &agentcore.PersistedRuntimeSummary{
				WaitType:              "confirm",
				WaitStatus:            "waiting",
				PendingCallID:         "call_db_1",
				PendingTool:           "update_node",
				PendingTier:           "server",
				PendingArgsSummary:    "json_object(keys=nodeId)",
				PendingPreviewSummary: "json_object(keys=ops)",
				LastEventSeq:          3,
				ExpiresAt:             &expiresAt,
			},
			CanConfirm: false,
			CanResume:  true,
		},
	}

	h := NewAgentHandler(svc, agentcore.NewAgentSessionManager(time.Minute))
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, "user_1")
		c.Next()
	})
	r.POST("/agent/sessions/:sessionId/resume", h.AgentResume)

	req := httptest.NewRequest(http.MethodPost, "/agent/sessions/s_resume_db/resume", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	data := unwrapResponseData(t, w.Body.String())
	if source, _ := data["source"].(string); source != "db" {
		t.Fatalf("expected source=db, got %v", data["source"])
	}
	if canConfirm, _ := data["canConfirm"].(bool); canConfirm {
		t.Fatalf("expected canConfirm=false for db fallback, got %v", data["canConfirm"])
	}
	if canResume, _ := data["canResume"].(bool); canResume {
		t.Fatalf("expected canResume=false for db-only fallback, got %v", data["canResume"])
	}
	if recoverable, _ := data["recoverable"].(bool); recoverable {
		t.Fatalf("expected recoverable=false for db-only fallback, got %v", data["recoverable"])
	}
	runtimeSummary, ok := data["runtimeSummary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected runtimeSummary object, got %T", data["runtimeSummary"])
	}
	if waitStatus, _ := runtimeSummary["waitStatus"].(string); waitStatus != "waiting" {
		t.Fatalf("expected runtimeSummary.waitStatus=waiting, got %v", runtimeSummary["waitStatus"])
	}
}

func unwrapResponseData(t *testing.T, raw string) map[string]interface{} {
	t.Helper()

	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("unmarshal response failed: %v, raw=%s", err, raw)
	}
	data, ok := envelope["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("response data field missing or invalid, raw=%s", raw)
	}
	return data
}

func parseSSEEventTypes(raw string) ([]string, error) {
	payloads, err := parseSSEPayloadObjects(raw)
	if err != nil {
		return nil, err
	}

	types := make([]string, 0, len(payloads))
	for _, obj := range payloads {
		t, _ := obj["type"].(string)
		if strings.TrimSpace(t) != "" {
			types = append(types, strings.TrimSpace(t))
		}
	}
	return types, nil
}

func parseSSEPayloadObjects(raw string) ([]map[string]interface{}, error) {
	lines := strings.Split(raw, "\n")
	payloads := make([]map[string]interface{}, 0, 8)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "" {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			return nil, err
		}
		payloads = append(payloads, obj)
	}
	return payloads, nil
}

func assertSSESequence(allTypes []string) error {
	filtered := make([]string, 0, len(allTypes))
	for _, t := range allTypes {
		t = strings.TrimSpace(t)
		if t == "" || t == "heartbeat" {
			continue
		}
		filtered = append(filtered, t)
	}

	if len(filtered) == 0 {
		return errors.New("no SSE events")
	}
	if filtered[0] != "started" {
		return errors.New("first event must be started")
	}
	if filtered[len(filtered)-1] != "done" {
		return errors.New("last event must be done")
	}

	contentIdx := -1
	toolIdx := -1
	for i, t := range filtered {
		if contentIdx < 0 && t == "content" {
			contentIdx = i
		}
		if toolIdx < 0 && strings.HasPrefix(t, "tool_call_") {
			toolIdx = i
		}
	}
	if contentIdx < 0 {
		return errors.New("missing content event")
	}
	if toolIdx < 0 {
		return errors.New("missing tool_call_* event")
	}
	if contentIdx <= 0 {
		return errors.New("content must appear after started")
	}
	if toolIdx <= contentIdx {
		return errors.New("tool_call_* must appear after content")
	}
	if toolIdx >= len(filtered)-1 {
		return errors.New("tool_call_* must appear before done")
	}

	return nil
}
