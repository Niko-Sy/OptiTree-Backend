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

	body := `{"message":"分析当前图","model":"qwen3","graphSnapshot":{"nodes":[],"edges":[]},"clientRevision":7,"readOnly":true,"maxToolRounds":3}`
	req := httptest.NewRequest(http.MethodPost, "/assistant/conversations/conv_1/agent/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
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
