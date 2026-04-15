package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/config"
	"optitree-backend/internal/graph_ops"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"gorm.io/datatypes"
)

var (
	ErrAgentDisabled               = errors.New("agent is disabled")
	ErrAgentConversationNotFound   = errors.New("agent conversation not found")
	ErrAgentPermissionDenied       = errors.New("agent permission denied")
	ErrAgentMessageEmpty           = errors.New("agent message empty")
	ErrAgentInvalidGraphSnapshot   = errors.New("agent invalid graph snapshot")
	ErrAgentClientRevisionConflict = errors.New("agent client revision conflict")
)

const (
	agentHistoryLimit         = 20
	iterationLimitPendingTool = "iteration_limit"
	toolCallResultSchemaV1    = 1
	agentThinkingEventType    = "thinking"
	toolStatusSuccess         = "success"
	toolStatusFailed          = "failed"
	toolStatusCancelled       = "cancelled"
	toolStatusDiscarded       = "discarded"
	toolStatusClientOnly      = "client_only"
	toolStatusPending         = "pending"
)

type faultTreeRuntimeSnapshot struct {
	nodes    []model.FaultTreeNode
	edges    []model.FaultTreeEdge
	revision int
}

type AgentRunInput struct {
	ConversationID  string
	UserID          string
	Message         string
	Model           string
	GraphSnapshot   json.RawMessage
	ClientRevision  *int
	ReadOnly        bool
	MaxToolRounds   int
	FocusNodeIDs    []string
	SelectedNodeIDs []string
}

type AgentRunOutput struct {
	ConversationID     string `json:"conversationId"`
	SessionID          string `json:"sessionId"`
	UserMessageID      string `json:"userMessageId"`
	AssistantMessageID string `json:"assistantMessageId,omitempty"`
	TokensUsed         int    `json:"tokensUsed"`
	ModelUsed          string `json:"modelUsed,omitempty"`
	ServerOps          int    `json:"serverOps"`
	ClientOps          int    `json:"clientOps"`
	HybridOps          int    `json:"hybridOps"`
}

type AgentService struct {
	provider          ai.AIProvider
	executor          *graph_ops.Executor
	hybridEngine      *graph_ops.HybridEngine
	sessionMgr        *AgentSessionManager
	safety            *SafetyController
	memberRepo        *repository.MemberRepository
	conversationRepo  *repository.AIConversationRepository
	chatMessageRepo   *repository.AIChatMessageRepository
	agentSessionRepo  *repository.AgentSessionRepository
	agentRuntimeRepo  *repository.AgentSessionRuntimeRepository
	agentToolCallRepo *repository.AgentToolCallRepository
	ftService         *service.FaultTreeService
	kgService         *service.KnowledgeGraphService
	cfg               config.AgentConfig
}

type PersistedSessionStatus struct {
	Session        *model.AgentSession      `json:"session"`
	RuntimeSummary *PersistedRuntimeSummary `json:"runtimeSummary,omitempty"`
	CanConfirm     bool                     `json:"canConfirm"`
	CanResume      bool                     `json:"canResume"`
}

type PersistedRuntimeSummary struct {
	WaitType              string     `json:"waitType"`
	WaitStatus            string     `json:"waitStatus"`
	PendingCallID         string     `json:"pendingCallId,omitempty"`
	PendingTool           string     `json:"pendingTool,omitempty"`
	PendingTier           string     `json:"pendingTier,omitempty"`
	PendingArgsSummary    string     `json:"pendingArgsSummary,omitempty"`
	PendingPreviewSummary string     `json:"pendingPreviewSummary,omitempty"`
	LastEventSeq          int64      `json:"lastEventSeq"`
	ExpiresAt             *time.Time `json:"expiresAt,omitempty"`
	UpdatedAt             time.Time  `json:"updatedAt,omitempty"`
}

func NewAgentService(
	provider ai.AIProvider,
	executor *graph_ops.Executor,
	hybridEngine *graph_ops.HybridEngine,
	sessionMgr *AgentSessionManager,
	safety *SafetyController,
	memberRepo *repository.MemberRepository,
	conversationRepo *repository.AIConversationRepository,
	chatMessageRepo *repository.AIChatMessageRepository,
	agentSessionRepo *repository.AgentSessionRepository,
	agentRuntimeRepo *repository.AgentSessionRuntimeRepository,
	agentToolCallRepo *repository.AgentToolCallRepository,
	ftService *service.FaultTreeService,
	kgService *service.KnowledgeGraphService,
	cfg config.AgentConfig,
) *AgentService {
	return &AgentService{
		provider:          provider,
		executor:          executor,
		hybridEngine:      hybridEngine,
		sessionMgr:        sessionMgr,
		safety:            safety,
		memberRepo:        memberRepo,
		conversationRepo:  conversationRepo,
		chatMessageRepo:   chatMessageRepo,
		agentSessionRepo:  agentSessionRepo,
		agentRuntimeRepo:  agentRuntimeRepo,
		agentToolCallRepo: agentToolCallRepo,
		ftService:         ftService,
		kgService:         kgService,
		cfg:               cfg,
	}
}

func (s *AgentService) Enabled() bool {
	return s.cfg.Enabled
}

func (s *AgentService) resolveAgentModel(inputModel string) string {
	model := strings.TrimSpace(inputModel)
	if model != "" {
		return model
	}
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.AgentModel)
}

func (s *AgentService) GetPersistedSessionStatus(sessionID, userID string) (*PersistedSessionStatus, error) {
	sessionID = strings.TrimSpace(sessionID)
	userID = strings.TrimSpace(userID)
	if sessionID == "" || userID == "" {
		return nil, ErrSessionNotFound
	}

	record, err := s.agentSessionRepo.FindByID(sessionID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, ErrSessionNotFound
	}
	if strings.TrimSpace(record.UserID) != userID {
		return nil, ErrAgentPermissionDenied
	}

	var runtime *model.AgentSessionRuntime
	if s.agentRuntimeRepo != nil {
		runtime, err = s.agentRuntimeRepo.FindBySessionID(sessionID)
		if err != nil {
			return nil, err
		}
	}

	status := &PersistedSessionStatus{Session: record}
	if runtime != nil {
		status.RuntimeSummary = buildPersistedRuntimeSummary(runtime)
		status.CanResume = canResumeFromRuntime(runtime)
	}

	return status, nil
}

func buildPersistedRuntimeSummary(runtime *model.AgentSessionRuntime) *PersistedRuntimeSummary {
	if runtime == nil {
		return nil
	}

	summary := &PersistedRuntimeSummary{
		WaitType:              strings.TrimSpace(runtime.WaitType),
		WaitStatus:            strings.TrimSpace(runtime.WaitStatus),
		PendingArgsSummary:    summarizePendingArgs(json.RawMessage(runtime.PendingArgs)),
		PendingPreviewSummary: summarizePendingArgs(json.RawMessage(runtime.PendingPreview)),
		LastEventSeq:          runtime.LastEventSeq,
		ExpiresAt:             runtime.ExpiresAt,
		UpdatedAt:             runtime.UpdatedAt,
	}
	if runtime.PendingCallID != nil {
		summary.PendingCallID = strings.TrimSpace(*runtime.PendingCallID)
	}
	if runtime.PendingTool != nil {
		summary.PendingTool = strings.TrimSpace(*runtime.PendingTool)
	}
	if runtime.PendingTier != nil {
		summary.PendingTier = strings.TrimSpace(*runtime.PendingTier)
	}
	return summary
}

func canResumeFromRuntime(runtime *model.AgentSessionRuntime) bool {
	if runtime == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(runtime.WaitStatus), "waiting") {
		return false
	}
	if runtime.ExpiresAt != nil && time.Now().UTC().After(runtime.ExpiresAt.UTC()) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(runtime.WaitType)) {
	case "confirm", "preview", "iteration":
		return true
	default:
		return false
	}
}

func (s *AgentService) RunStream(
	ctx context.Context,
	session *AgentSession,
	input AgentRunInput,
	writeEvent func(map[string]interface{}) bool,
) (*AgentRunOutput, error) {
	if !s.cfg.Enabled {
		return nil, ErrAgentDisabled
	}
	if session == nil {
		return nil, ErrSessionNotFound
	}
	if writeEvent == nil {
		writeEvent = func(map[string]interface{}) bool { return true }
	}

	conversation, err := s.conversationRepo.FindByID(strings.TrimSpace(input.ConversationID))
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, ErrAgentConversationNotFound
	}
	if strings.TrimSpace(conversation.UserID) != strings.TrimSpace(input.UserID) {
		return nil, ErrAgentPermissionDenied
	}
	if err := graph_ops.ValidatePermissionBoundary(input.UserID, conversation.ProjectID, s.memberRepo); err != nil {
		return nil, ErrAgentPermissionDenied
	}

	message := strings.TrimSpace(input.Message)
	if message == "" {
		return nil, ErrAgentMessageEmpty
	}

	session.ProjectID = conversation.ProjectID
	session.GraphType = conversation.Type
	session.UserID = input.UserID
	session.ConversationID = conversation.ID
	session.SetState(StateRunning)

	var runErr error
	completed := false

	modelSession := &model.AgentSession{
		ID:             session.ID,
		ConversationID: conversation.ID,
		ProjectID:      conversation.ProjectID,
		UserID:         input.UserID,
		GraphType:      conversation.Type,
		State:          StateRunning,
		StartedAt:      time.Now().UTC(),
	}
	if err := s.agentSessionRepo.Create(nil, modelSession); err != nil {
		return nil, err
	}
	defer func() {
		snap := session.Snapshot()
		finalState := snap.State
		if finalState != StatePausedForConfirm && finalState != StatePausedForPreview {
			switch {
			case runErr == nil && completed:
				finalState = StateDone
			case errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
				finalState = "timeout"
			case errors.Is(runErr, context.Canceled), errors.Is(runErr, ErrSessionClosed), errors.Is(ctx.Err(), context.Canceled):
				finalState = StateCancelled
			case runErr != nil:
				finalState = "failed"
			default:
				finalState = "failed"
			}
			session.SetState(finalState)
			snap = session.Snapshot()
		}

		modelSession.State = snap.State
		modelSession.ToolCallCount = snap.ToolCallCount
		modelSession.ServerOps = snap.ServerOps
		modelSession.ClientOps = snap.ClientOps
		modelSession.HybridOps = snap.HybridOps
		modelSession.TokensUsed = snap.TokensUsed

		if isTerminalSessionState(snap.State) {
			now := time.Now().UTC()
			if runErr != nil {
				errText := strings.TrimSpace(runErr.Error())
				modelSession.ErrorMessage = &errText
			} else {
				modelSession.ErrorMessage = nil
			}
			modelSession.EndedAt = &now
		} else {
			modelSession.ErrorMessage = nil
			modelSession.EndedAt = nil
		}

		_ = s.agentSessionRepo.Update(nil, modelSession)

		if s.agentRuntimeRepo != nil && finalState != StatePausedForConfirm && finalState != StatePausedForPreview {
			_ = s.agentRuntimeRepo.ClearPending(session.ID, "cleared")
		}
		if s.safety != nil {
			s.safety.ClearSession(session.ID)
		}
	}()

	historyMessages, err := s.chatMessageRepo.ListRecentByConversation(conversation.ID, agentHistoryLimit)
	if err != nil {
		return nil, err
	}

	contextData, serverRevision, err := s.loadConversationContext(ctx, conversation)
	if err != nil {
		return nil, err
	}
	emitThinkingEvent(writeEvent, "context_loaded", 0, "已加载会话图上下文，开始规划执行步骤")

	currentContext := contextData
	contextSource := "db"
	var runtimeSnapshot *faultTreeRuntimeSnapshot

	hasClientRevision := input.ClientRevision != nil
	if hasClientRevision && *input.ClientRevision != serverRevision {
		_ = writeEvent(map[string]interface{}{
			"type":           "context_revision_mismatch",
			"clientRevision": *input.ClientRevision,
			"serverRevision": serverRevision,
		})
		if !input.ReadOnly {
			return nil, ErrAgentClientRevisionConflict
		}
	}

	if len(strings.TrimSpace(string(input.GraphSnapshot))) > 0 {
		contextSource = "client_snapshot"
		emitThinkingEvent(writeEvent, "context_override", 0, "检测到前端快照，当前轮次将优先使用前端图上下文")
		switch conversation.Type {
		case service.AssistantConversationTypeFaultTree:
			snapshot, err := parseFaultTreeGraphSnapshot(input.GraphSnapshot)
			if err != nil {
				return nil, err
			}
			currentContext = map[string]interface{}{"nodes": snapshot.Nodes, "edges": snapshot.Edges}
			revision := serverRevision
			if hasClientRevision {
				revision = *input.ClientRevision
			}
			runtimeSnapshot = &faultTreeRuntimeSnapshot{
				nodes:    append([]model.FaultTreeNode(nil), snapshot.Nodes...),
				edges:    append([]model.FaultTreeEdge(nil), snapshot.Edges...),
				revision: revision,
			}
		case service.AssistantConversationTypeKnowledgeGraph:
			var snapshot struct {
				Nodes []model.KnowledgeGraphNode `json:"rfNodes"`
				Edges []model.KnowledgeGraphEdge `json:"rfEdges"`
			}
			if err := json.Unmarshal(input.GraphSnapshot, &snapshot); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrAgentInvalidGraphSnapshot, err)
			}
			currentContext = map[string]interface{}{"rfNodes": snapshot.Nodes, "rfEdges": snapshot.Edges}
		default:
			return nil, fmt.Errorf("unsupported conversation type: %s", conversation.Type)
		}
	}

	if !input.ReadOnly && contextSource == "client_snapshot" && !hasClientRevision {
		return nil, fmt.Errorf("%w: clientRevision is required when graphSnapshot is used in write mode", ErrAgentClientRevisionConflict)
	}

	userMessage := &model.AIChatMessage{
		ID:             util.NewMessageID(),
		ConversationID: conversation.ID,
		Role:           "user",
		Content:        message,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.chatMessageRepo.Create(userMessage); err != nil {
		return nil, err
	}
	if err := s.conversationRepo.RecordMessageActivity(conversation.ID, userMessage.CreatedAt, buildConversationTitle(message)); err != nil {
		return nil, err
	}

	toolDefs := graph_ops.FilterToolsForMode(conversation.Type, input.ReadOnly, s.cfg.IncludeHybridTools)
	toolSchemas := graph_ops.ToOAITools(toolDefs)
	toolGuide := graph_ops.BuildToolPromptGuide(toolDefs)
	history := toChatHistory(historyMessages)
	workingHistory := append([]ai.ChatHistoryMessage(nil), history...)
	modelForRun := s.resolveAgentModel(input.Model)

	currentMessage := message
	if input.ReadOnly {
		currentMessage = "系统约束：当前会话为只读模式，仅允许分析与只读工具，禁止执行结构写操作。\n" + currentMessage
	}
	if contextSource == "client_snapshot" {
		if hasClientRevision {
			currentMessage = fmt.Sprintf("系统上下文：本轮使用前端 graphSnapshot（clientRevision=%d）。\n%s", *input.ClientRevision, currentMessage)
		} else {
			currentMessage = "系统上下文：本轮使用前端 graphSnapshot。\n" + currentMessage
		}
	}
	if focusHint := buildFocusNodeHint(input.FocusNodeIDs, input.SelectedNodeIDs); focusHint != "" {
		currentMessage = focusHint + "\n" + currentMessage
	}
	roundLimit := s.cfg.MaxRounds
	if input.MaxToolRounds > 0 {
		roundLimit = input.MaxToolRounds
	}
	finalReply := ""
	finalReasoning := ""
	modelUsed := ""
	allToolSummaries := make([]string, 0)

	for round := 0; ; round++ {
		emitThinkingEvent(writeEvent, "round_start", round+1, fmt.Sprintf("第 %d 轮：正在规划下一步", round+1))

		if roundLimit > 0 && round >= roundLimit {
			emitThinkingEvent(writeEvent, "round_limit_reached", round+1, fmt.Sprintf("已达到当前最大轮次 %d，等待继续确认", roundLimit))
			continueRounds, approved, err := s.requestIterationContinuation(ctx, session, round, roundLimit, writeEvent)
			if err != nil {
				runErr = err
				break
			}
			if !approved {
				emitThinkingEvent(writeEvent, "round_stopped", round+1, "用户未继续迭代，准备结束")
				allToolSummaries = append(allToolSummaries, fmt.Sprintf("达到最大迭代轮次 %d，已停止继续迭代", roundLimit))
				break
			}
			emitThinkingEvent(writeEvent, "round_resumed", round+1, fmt.Sprintf("已确认继续，新增 %d 轮", continueRounds))
			roundLimit += continueRounds
		}

		if s.safety != nil {
			s.safety.BeginRound(session.ID, round)
			if err := s.safety.CheckRoundWithLimit(round, roundLimit); err != nil {
				runErr = err
				break
			}
		}

		var (
			roundReply     string
			roundReasoning string
			roundToolCalls []ai.ToolCall
			roundTokens    int
			roundModelUsed string
			roundErr       error
		)

		temperature := toolPlanningTemperature(round, len(toolSchemas) > 0)

		if round == 0 {
			var textBuilder strings.Builder
			roundReply, roundReasoning, roundToolCalls, roundTokens, roundModelUsed, roundErr = s.provider.ChatStreamWithTools(ctx, ai.AgentChatRequest{
				ChatRequest: ai.ChatRequest{
					ContextData: currentContext,
					GraphType:   conversation.Type,
					Message:     currentMessage,
					Model:       modelForRun,
					History:     workingHistory,
				},
				Tools:                toolSchemas,
				ToolChoice:           "auto",
				ToolGuide:            toolGuide,
				PromptVersion:        strings.TrimSpace(s.cfg.PromptVersion),
				ReadOnly:             input.ReadOnly,
				FullContextThreshold: s.cfg.FullContextNodeThreshold,
				Temperature:          &temperature,
				EnableFallbackParser: s.cfg.EnableFallbackParser,
			}, func(chunk string) {
				if chunk == "" {
					return
				}
				textBuilder.WriteString(chunk)
			})
			if strings.TrimSpace(roundReply) == "" {
				roundReply = strings.TrimSpace(textBuilder.String())
			}
		} else {
			resp, err := s.provider.ChatWithTools(ctx, ai.AgentChatRequest{
				ChatRequest: ai.ChatRequest{
					ContextData: currentContext,
					GraphType:   conversation.Type,
					Message:     currentMessage,
					Model:       modelForRun,
					History:     workingHistory,
				},
				Tools:                toolSchemas,
				ToolChoice:           "auto",
				ToolGuide:            toolGuide,
				PromptVersion:        strings.TrimSpace(s.cfg.PromptVersion),
				ReadOnly:             input.ReadOnly,
				FullContextThreshold: s.cfg.FullContextNodeThreshold,
				Temperature:          &temperature,
				EnableFallbackParser: s.cfg.EnableFallbackParser,
			})
			if err != nil {
				roundErr = err
			} else if resp != nil {
				roundReply = strings.TrimSpace(resp.Reply)
				roundReasoning = strings.TrimSpace(resp.ReasoningContent)
				roundToolCalls = resp.ToolCalls
				roundTokens = resp.TokensUsed
				roundModelUsed = strings.TrimSpace(resp.ModelUsed)
				if roundReply != "" {
					_ = writeEvent(map[string]interface{}{"type": "content", "content": roundReply})
				}
			}
		}

		if shouldEmitFirstRoundContent(round, roundErr, roundToolCalls, roundReply) {
			_ = writeEvent(map[string]interface{}{"type": "content", "content": roundReply})
		}

		if roundTokens > 0 {
			session.AddTokens(roundTokens)
		}
		if roundModelUsed != "" {
			modelUsed = roundModelUsed
		}
		if strings.TrimSpace(roundReply) != "" {
			finalReply = strings.TrimSpace(roundReply)
		}
		if strings.TrimSpace(roundReasoning) != "" {
			finalReasoning = strings.TrimSpace(roundReasoning)
		}

		if roundErr != nil {
			emitThinkingEvent(writeEvent, "model_error", round+1, fmt.Sprintf("模型调用失败：%s", roundErr.Error()))
			if strings.TrimSpace(finalReply) == "" && len(allToolSummaries) == 0 {
				runErr = roundErr
				return &AgentRunOutput{
					ConversationID: conversation.ID,
					SessionID:      session.ID,
					UserMessageID:  userMessage.ID,
					ModelUsed:      modelUsed,
				}, runErr
			}
			runErr = roundErr
			break
		}

		if strings.TrimSpace(roundReasoning) != "" {
			emitThinkingEvent(writeEvent, "reasoning", round+1, summarizeThinking(roundReasoning, 1200))
		}
		if len(roundToolCalls) > 0 {
			emitThinkingEvent(writeEvent, "tool_plan", round+1, fmt.Sprintf("计划调用 %d 个工具：%s", len(roundToolCalls), summarizeToolNames(roundToolCalls, 6)))
		} else {
			emitThinkingEvent(writeEvent, "tool_plan", round+1, "当前无需调用工具，准备输出最终答复")
		}

		emitThinkingEvent(writeEvent, "tool_execution", round+1, "开始执行本轮工具调用")

		roundSummaries, roundToolResults, graphMutated, stopErr := s.executeRoundToolCalls(
			ctx,
			session,
			input.UserID,
			conversation.ProjectID,
			conversation.Type,
			input.ReadOnly,
			runtimeSnapshot,
			roundToolCalls,
			writeEvent,
		)
		if stopErr != nil {
			emitThinkingEvent(writeEvent, "round_stopped", round+1, fmt.Sprintf("触发安全策略停止：%s", stopErr.Error()))
			runErr = stopErr
			break
		}
		if len(roundSummaries) > 0 {
			emitThinkingEvent(writeEvent, "round_summary", round+1, fmt.Sprintf("本轮完成：%s", summarizeThinking(strings.Join(roundSummaries, "；"), 1200)))
		} else {
			emitThinkingEvent(writeEvent, "round_summary", round+1, "本轮未产生可执行工具结果")
		}
		allToolSummaries = append(allToolSummaries, roundSummaries...)
		currentMessage = buildToolContinuationPrompt(message, round+1, roundSummaries)

		if len(roundToolCalls) == 0 {
			break
		}

		roundHistoryToPersist := make([]ai.ChatHistoryMessage, 0, 1+len(roundToolResults))
		if assistantHistory, ok := buildAssistantToolHistoryMessage(roundReply, roundReasoning, roundToolCalls); ok {
			workingHistory = append(workingHistory, assistantHistory)
			roundHistoryToPersist = append(roundHistoryToPersist, assistantHistory)
		}
		if len(roundToolResults) > 0 {
			workingHistory = append(workingHistory, roundToolResults...)
			roundHistoryToPersist = append(roundHistoryToPersist, roundToolResults...)
		}
		if err := s.persistRoundHistoryMessages(conversation.ID, roundHistoryToPersist); err != nil {
			runErr = err
			break
		}

		if graphMutated {
			emitThinkingEvent(writeEvent, "context_refresh", round+1, "检测到图结构变化，正在刷新上下文")
			if runtimeSnapshot != nil && conversation.Type == service.AssistantConversationTypeFaultTree {
				currentContext = map[string]interface{}{
					"nodes": runtimeSnapshot.nodes,
					"edges": runtimeSnapshot.edges,
				}
			} else {
				refreshed, _, loadErr := s.loadConversationContext(ctx, conversation)
				if loadErr == nil {
					currentContext = refreshed
				}
			}
		}
	}
	emitThinkingEvent(writeEvent, "finalizing", 0, "正在整理最终答复并落库")

	if errors.Is(runErr, context.Canceled) {
		snap := session.Snapshot()
		if snap.State == StatePausedForConfirm || snap.State == StatePausedForPreview {
			return nil, runErr
		}
	}

	if strings.TrimSpace(finalReply) == "" && len(allToolSummaries) > 0 {
		finalReply = "工具执行已完成：" + summarizePlainText(strings.Join(allToolSummaries, "；"), s.maxToolSummaryChars())
	}
	if strings.TrimSpace(finalReply) == "" {
		finalReply = "操作已完成"
	}

	if runErr == nil {
		session.SetState(StateDone)
		completed = true
	}
	snap := session.Snapshot()

	assistantMessage := &model.AIChatMessage{
		ID:             util.NewMessageID(),
		ConversationID: conversation.ID,
		Role:           "assistant",
		Content:        strings.TrimSpace(finalReply),
		CreatedAt:      time.Now().UTC(),
	}
	if strings.TrimSpace(finalReasoning) != "" {
		v := strings.TrimSpace(finalReasoning)
		assistantMessage.ReasoningContent = &v
	}
	if strings.TrimSpace(modelUsed) != "" {
		v := strings.TrimSpace(modelUsed)
		assistantMessage.Model = &v
	}
	if snap.TokensUsed > 0 {
		v := snap.TokensUsed
		assistantMessage.TokensUsed = &v
	}
	if err := s.chatMessageRepo.Create(assistantMessage); err != nil {
		runErr = err
		return nil, err
	}
	if err := s.conversationRepo.RecordMessageActivity(conversation.ID, assistantMessage.CreatedAt, ""); err != nil {
		runErr = err
		return nil, err
	}

	return &AgentRunOutput{
		ConversationID:     conversation.ID,
		SessionID:          session.ID,
		UserMessageID:      userMessage.ID,
		AssistantMessageID: assistantMessage.ID,
		TokensUsed:         snap.TokensUsed,
		ModelUsed:          modelUsed,
		ServerOps:          snap.ServerOps,
		ClientOps:          snap.ClientOps,
		HybridOps:          snap.HybridOps,
	}, runErr
}

func (s *AgentService) upsertRuntimePending(session *AgentSession, callID, toolName, tier string, args json.RawMessage, preview interface{}, waitType string, timeout time.Duration) error {
	if s.agentRuntimeRepo == nil || session == nil {
		return nil
	}

	sessionID := strings.TrimSpace(session.ID)
	if sessionID == "" {
		return nil
	}

	argsJSON := []byte("{}")
	if len(args) > 0 && json.Valid(args) {
		argsJSON = append([]byte(nil), args...)
	}

	previewJSON := []byte("{}")
	if preview != nil {
		raw, err := json.Marshal(preview)
		if err == nil && json.Valid(raw) {
			previewJSON = raw
		}
	}

	var expiresAt *time.Time
	if timeout > 0 {
		t := time.Now().UTC().Add(timeout)
		expiresAt = &t
	}

	callID = strings.TrimSpace(callID)
	toolName = strings.TrimSpace(toolName)
	tier = strings.TrimSpace(tier)
	runtime := &model.AgentSessionRuntime{
		SessionID:      sessionID,
		PendingArgs:    datatypes.JSON(argsJSON),
		PendingPreview: datatypes.JSON(previewJSON),
		WaitType:       strings.TrimSpace(waitType),
		WaitStatus:     "waiting",
		ExpiresAt:      expiresAt,
	}
	if callID != "" {
		runtime.PendingCallID = &callID
	}
	if toolName != "" {
		runtime.PendingTool = &toolName
	}
	if tier != "" {
		runtime.PendingTier = &tier
	}
	return s.agentRuntimeRepo.UpsertPending(runtime)
}

func (s *AgentService) clearRuntimePending(sessionID, waitStatus string) error {
	if s.agentRuntimeRepo == nil {
		return nil
	}
	return s.agentRuntimeRepo.ClearPending(strings.TrimSpace(sessionID), strings.TrimSpace(waitStatus))
}

func (s *AgentService) markRuntimeWaitStatus(sessionID, waitStatus string) error {
	if s.agentRuntimeRepo == nil {
		return nil
	}
	return s.agentRuntimeRepo.MarkWaitStatus(strings.TrimSpace(sessionID), strings.TrimSpace(waitStatus))
}

func (s *AgentService) requestIterationContinuation(
	ctx context.Context,
	session *AgentSession,
	round int,
	roundLimit int,
	writeEvent func(map[string]interface{}) bool,
) (continueRounds int, approved bool, err error) {
	callID := fmt.Sprintf("iter_limit_%d_%d", roundLimit, round)
	session.SetPending(callID, iterationLimitPendingTool, nil)
	session.SetState(StatePausedForConfirm)
	_ = s.upsertRuntimePending(session, callID, iterationLimitPendingTool, string(graph_ops.TierServer), nil, nil, "iteration", s.cfg.ConfirmTimeout)
	_ = writeEvent(map[string]interface{}{
		"type":                    "iteration_limit_reached",
		"callId":                  callID,
		"round":                   round,
		"maxRounds":               roundLimit,
		"suggestedContinueRounds": 1,
	})

	signal, err := s.waitForConfirmation(ctx, session, s.cfg.ConfirmTimeout)
	if err != nil {
		if errors.Is(err, context.Canceled) && session.State() == StatePausedForConfirm {
			_ = s.markRuntimeWaitStatus(session.ID, "waiting")
			return 0, false, err
		}
		waitStatus := "rejected"
		if errors.Is(err, ErrSessionConfirmTimeout) {
			waitStatus = "timeout"
		}
		_ = s.clearRuntimePending(session.ID, waitStatus)
		session.ClearPending()
		session.SetState(StateRunning)
		return 0, false, err
	}

	session.ClearPending()
	session.SetState(StateRunning)

	if !signal.Approved {
		_ = s.clearRuntimePending(session.ID, "rejected")
		_ = writeEvent(map[string]interface{}{
			"type":      "iteration_stopped",
			"callId":    callID,
			"round":     round,
			"maxRounds": roundLimit,
		})
		return 0, false, nil
	}
	_ = s.clearRuntimePending(session.ID, "approved")

	continueRounds = signal.ContinueRounds
	if continueRounds <= 0 {
		continueRounds = 1
	}
	_ = writeEvent(map[string]interface{}{
		"type":           "iteration_resumed",
		"callId":         callID,
		"round":          round,
		"continueRounds": continueRounds,
		"newMaxRounds":   roundLimit + continueRounds,
	})
	return continueRounds, true, nil
}

func (s *AgentService) executeRoundToolCalls(
	ctx context.Context,
	session *AgentSession,
	userID, projectID, graphType string,
	readOnly bool,
	runtimeSnapshot *faultTreeRuntimeSnapshot,
	toolCalls []ai.ToolCall,
	writeEvent func(map[string]interface{}) bool,
) (summaries []string, toolResults []ai.ChatHistoryMessage, graphMutated bool, stopErr error) {
	summaries = make([]string, 0, len(toolCalls)+4)
	toolResults = make([]ai.ChatHistoryMessage, 0, len(toolCalls)+4)
	toolCalls = mergeRoundMutationCalls(toolCalls)

	roundReadContextUsed := false
	hadRecentReadContext := false
	if session != nil {
		hadRecentReadContext = session.HasRecentReadContext()
		defer session.SetRecentReadContext(roundReadContextUsed)
	}

	for _, call := range toolCalls {
		if strings.TrimSpace(call.ID) == "" {
			call.ID = util.NewAgentToolCallID()
		}

		if err := graph_ops.ValidatePermissionBoundary(userID, projectID, s.memberRepo); err != nil {
			errMsg := ErrAgentPermissionDenied.Error()
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": errMsg})
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusFailed, false, errMsg, errMsg, nil))
			summary := fmt.Sprintf("%s: %s", call.Name, errMsg)
			summaries = append(summaries, summary)
			toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, toolStatusFailed, summary, nil, errMsg))
			continue
		}

		def, ok := graph_ops.GetTool(call.Name)
		if !ok {
			errMsg := "unknown tool"
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": errMsg})
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusFailed, false, errMsg, errMsg, nil))
			summary := fmt.Sprintf("%s: %s", call.Name, errMsg)
			summaries = append(summaries, summary)
			toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, toolStatusFailed, summary, nil, errMsg))
			continue
		}
		if !graph_ops.ToolSupportsGraphType(def, graphType) {
			errMsg := fmt.Sprintf("tool %s does not support graph type %s", call.Name, graphType)
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": errMsg})
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusFailed, false, errMsg, errMsg, nil))
			summary := fmt.Sprintf("%s: %s", call.Name, errMsg)
			summaries = append(summaries, summary)
			toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, toolStatusFailed, summary, nil, errMsg))
			continue
		}

		if err := graph_ops.ValidateParameters(call.Name, call.Arguments); err != nil {
			errMsg := appendToolParameterHint(call.Name, err.Error())
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": errMsg})
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusFailed, false, errMsg, errMsg, nil))
			summary := fmt.Sprintf("%s: %s", call.Name, errMsg)
			summaries = append(summaries, summary)
			toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, toolStatusFailed, summary, nil, errMsg))
			continue
		}

		if def.Tier == graph_ops.TierServer && def.MutatesGraph && !readOnly && !roundReadContextUsed && !hadRecentReadContext {
			autoReadCall := buildAutoReadCallForMutation(call)
			autoDef, ok := graph_ops.GetTool(autoReadCall.Name)
			if ok {
				if s.safety != nil {
					if err := s.safety.CheckToolCall(session, autoReadCall, 0); err != nil {
						errMsg := err.Error()
						_ = writeEvent(buildToolCallResultEvent(autoReadCall.ID, autoReadCall.Name, toolStatusFailed, false, errMsg, errMsg, nil))
						summaries = append(summaries, fmt.Sprintf("%s: %s", autoReadCall.Name, errMsg))
						toolResults = append(toolResults, buildToolResultHistoryMessage(autoReadCall.ID, autoReadCall.Name, toolStatusFailed, errMsg, nil, errMsg))
						if isHardStopSafetyError(err) {
							return summaries, toolResults, graphMutated, err
						}
					}
				}
				session.IncToolCallCount()
				outcome, status, err := s.runToolCallWithAudit(ctx, session, projectID, graphType, readOnly, runtimeSnapshot, autoReadCall, autoDef, writeEvent)
				if err != nil {
					return summaries, toolResults, graphMutated, err
				}
				roundReadContextUsed = true
				autoSummary := strings.TrimSpace(outcome.Summary)
				if autoSummary == "" {
					autoSummary = "已自动读取图上下文"
				}
				autoSummary = s.compactToolObservationText(autoReadCall.Name, status, "auto_read_context: "+autoSummary, outcome.Patch, "")
				summaries = append(summaries, autoSummary)
				toolResults = append(toolResults, buildToolResultHistoryMessage(autoReadCall.ID, autoReadCall.Name, status, autoSummary, outcome.Patch, ""))
			}
		}

		if s.safety != nil {
			if err := s.safety.CheckToolCall(session, call, 0); err != nil {
				errMsg := err.Error()
				_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": errMsg})
				_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusFailed, false, errMsg, errMsg, nil))
				summary := fmt.Sprintf("%s: %s", call.Name, errMsg)
				summaries = append(summaries, summary)
				toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, toolStatusFailed, summary, nil, errMsg))
				if isHardStopSafetyError(err) {
					return summaries, toolResults, graphMutated, err
				}
				continue
			}
		}
		session.IncToolCallCount()

		outcome, finalStatus, err := s.runToolCallWithAudit(ctx, session, projectID, graphType, readOnly, runtimeSnapshot, call, def, writeEvent)
		if err != nil {
			return summaries, toolResults, graphMutated, err
		}

		resultSummary := strings.TrimSpace(outcome.Summary)
		if resultSummary == "" {
			resultSummary = fmt.Sprintf("%s: %s", call.Name, finalStatus)
		}
		if hasGraphPatchChanges(outcome.Patch) {
			graphMutated = true
		}
		if def.Tier == graph_ops.TierServer && graph_ops.ToolIsReadContext(def.Name) {
			roundReadContextUsed = true
		}

		observationSummary := s.compactToolObservationText(call.Name, finalStatus, resultSummary, outcome.Patch, "")
		summaries = append(summaries, observationSummary)
		toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, finalStatus, resultSummary, outcome.Patch, ""))

		if def.Tier == graph_ops.TierServer && def.MutatesGraph && finalStatus == toolStatusSuccess {
			if s.safety != nil {
				changedUnits := outcome.ChangedNodes
				if changedUnits <= 0 && outcome.ChangedEdges > 0 {
					changedUnits = 1
				}
				if changedUnits <= 0 {
					changedUnits = patchNodeMutationCount(outcome.Patch)
				}
				if err := s.safety.RecordMutation(session.ID, changedUnits); err != nil {
					return summaries, toolResults, graphMutated, err
				}
			}

			followups := []ai.ToolCall{{ID: util.NewAgentToolCallID(), Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)}}
			if shouldRunGateSemanticsFollowup(call) {
				followups = append(followups, ai.ToolCall{ID: util.NewAgentToolCallID(), Name: "check_gate_semantics", Arguments: gateSemanticsArgsFromCall(call)})
			}

			for _, followCall := range followups {
				followDef, ok := graph_ops.GetTool(followCall.Name)
				if !ok || !graph_ops.ToolSupportsGraphType(followDef, graphType) {
					continue
				}
				if err := graph_ops.ValidateParameters(followCall.Name, followCall.Arguments); err != nil {
					continue
				}

				if s.safety != nil {
					if err := s.safety.CheckToolCall(session, followCall, 0); err != nil {
						errMsg := err.Error()
						_ = writeEvent(buildToolCallResultEvent(followCall.ID, followCall.Name, toolStatusFailed, false, errMsg, errMsg, nil))
						summaries = append(summaries, fmt.Sprintf("%s: %s", followCall.Name, errMsg))
						toolResults = append(toolResults, buildToolResultHistoryMessage(followCall.ID, followCall.Name, toolStatusFailed, errMsg, nil, errMsg))
						if isHardStopSafetyError(err) {
							return summaries, toolResults, graphMutated, err
						}
						continue
					}
				}

				session.IncToolCallCount()
				followOutcome, followStatus, err := s.runToolCallWithAudit(ctx, session, projectID, graphType, readOnly, runtimeSnapshot, followCall, followDef, writeEvent)
				if err != nil {
					return summaries, toolResults, graphMutated, err
				}
				if graph_ops.ToolIsReadContext(followCall.Name) {
					roundReadContextUsed = true
				}
				followSummary := strings.TrimSpace(followOutcome.Summary)
				if followSummary == "" {
					followSummary = fmt.Sprintf("%s: %s", followCall.Name, followStatus)
				}
				followSummary = s.compactToolObservationText(followCall.Name, followStatus, followCall.Name+"(auto): "+followSummary, followOutcome.Patch, "")
				summaries = append(summaries, followSummary)
				toolResults = append(toolResults, buildToolResultHistoryMessage(followCall.ID, followCall.Name, followStatus, followSummary, followOutcome.Patch, ""))
			}
		}
	}

	return summaries, toolResults, graphMutated, nil
}

func (s *AgentService) runToolCallWithAudit(
	ctx context.Context,
	session *AgentSession,
	projectID, graphType string,
	readOnly bool,
	runtimeSnapshot *faultTreeRuntimeSnapshot,
	call ai.ToolCall,
	def *graph_ops.ToolDefinition,
	writeEvent func(map[string]interface{}) bool,
) (*toolDispatchOutcome, string, error) {
	if def == nil {
		return nil, toolStatusFailed, graph_ops.ErrUnknownTool
	}

	args := call.Arguments
	if len(args) == 0 || !json.Valid(args) {
		args = json.RawMessage(`{}`)
	}

	toolRecord := &model.AgentToolCall{
		ID:        util.NewAgentToolCallID(),
		SessionID: session.ID,
		CallID:    call.ID,
		ToolName:  call.Name,
		Tier:      string(def.Tier),
		Arguments: datatypes.JSON(args),
		Status:    "running",
	}
	if err := s.agentToolCallRepo.Create(nil, toolRecord); err != nil {
		return nil, toolStatusFailed, fmt.Errorf("create agent tool call audit failed: %w", err)
	}

	outcome, dispatchErr := s.dispatchToolCall(ctx, session, projectID, graphType, readOnly, runtimeSnapshot, call, def, writeEvent)
	if dispatchErr != nil {
		errMsg := dispatchErr.Error()
		if errors.Is(dispatchErr, context.Canceled) {
			snap := session.Snapshot()
			if snap.State == StatePausedForConfirm || snap.State == StatePausedForPreview {
				waitingSummary := "waiting_user_confirmation"
				if err := s.agentToolCallRepo.UpdateStatus(toolRecord.ID, toolStatusPending, &waitingSummary, nil, nil); err != nil {
					return nil, toolStatusFailed, fmt.Errorf("update waiting tool call status error: %v (origin=%w)", err, dispatchErr)
				}
				return &toolDispatchOutcome{Summary: waitingSummary, Status: toolStatusPending}, toolStatusPending, dispatchErr
			}
		}
		if err := s.agentToolCallRepo.UpdateStatus(toolRecord.ID, toolStatusFailed, nil, nil, &errMsg); err != nil {
			return nil, toolStatusFailed, fmt.Errorf("update failed tool call status error: %v (origin=%w)", err, dispatchErr)
		}
		_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusFailed, false, errMsg, errMsg, nil))
		return &toolDispatchOutcome{Summary: errMsg, Status: toolStatusFailed}, toolStatusFailed, nil
	}

	if outcome == nil {
		outcome = &toolDispatchOutcome{Summary: "", Status: toolStatusSuccess}
	}
	finalStatus := normalizeToolFinalStatus(outcome.Status)

	var patchRaw []byte
	if outcome.Patch != nil {
		patchRaw, _ = json.Marshal(outcome.Patch)
	}
	resultSummary := strings.TrimSpace(outcome.Summary)
	if resultSummary == "" {
		resultSummary = fmt.Sprintf("%s: %s", call.Name, finalStatus)
	}
	if err := s.agentToolCallRepo.UpdateStatus(toolRecord.ID, finalStatus, &resultSummary, patchRaw, nil); err != nil {
		return nil, toolStatusFailed, fmt.Errorf("update tool call status failed: %w", err)
	}
	return outcome, finalStatus, nil
}

func mergeRoundMutationCalls(toolCalls []ai.ToolCall) []ai.ToolCall {
	if len(toolCalls) <= 1 {
		return toolCalls
	}

	mutations := make([]ai.ToolCall, 0, len(toolCalls))
	firstMutationIndex := -1
	for i, call := range toolCalls {
		if !isMergeableMutationTool(call.Name) {
			continue
		}
		if firstMutationIndex < 0 {
			firstMutationIndex = i
		}
		mutations = append(mutations, call)
	}
	if len(mutations) <= 1 {
		return toolCalls
	}

	merged, ok := mergeMutationSegment(mutations)
	if !ok {
		return toolCalls
	}

	out := make([]ai.ToolCall, 0, len(toolCalls)-len(mutations)+1)
	inserted := false
	for i, call := range toolCalls {
		if isMergeableMutationTool(call.Name) {
			if !inserted && i == firstMutationIndex {
				out = append(out, merged)
				inserted = true
			}
			continue
		}
		out = append(out, call)
	}
	return out
}

func isMergeableMutationTool(toolName string) bool {
	def, ok := graph_ops.GetTool(toolName)
	if !ok {
		return false
	}
	if def.Tier != graph_ops.TierServer || !def.MutatesGraph {
		return false
	}
	return !strings.EqualFold(def.Name, "batch_operations")
}

func mergeMutationSegment(segment []ai.ToolCall) (ai.ToolCall, bool) {
	if len(segment) <= 1 {
		return ai.ToolCall{}, false
	}

	batchOps := make([]map[string]interface{}, 0, len(segment))
	for _, call := range segment {
		argsObj := map[string]interface{}{}
		if len(call.Arguments) > 0 {
			if !json.Valid(call.Arguments) {
				return ai.ToolCall{}, false
			}
			if err := json.Unmarshal(call.Arguments, &argsObj); err != nil {
				return ai.ToolCall{}, false
			}
		}

		batchOps = append(batchOps, map[string]interface{}{
			"tool": strings.TrimSpace(call.Name),
			"args": argsObj,
		})
	}

	batchArgs, err := json.Marshal(map[string]interface{}{"operations": batchOps})
	if err != nil {
		return ai.ToolCall{}, false
	}

	mergedID := strings.TrimSpace(segment[0].ID)
	if mergedID == "" {
		mergedID = util.NewAgentToolCallID()
	}

	return ai.ToolCall{
		ID:        mergedID,
		Name:      "batch_operations",
		Arguments: json.RawMessage(batchArgs),
	}, true
}

func toolPlanningTemperature(round int, hasTools bool) float64 {
	if round == 0 && hasTools {
		return 0.1
	}
	return 0.3
}

func shouldEmitFirstRoundContent(round int, roundErr error, toolCalls []ai.ToolCall, reply string) bool {
	return round == 0 && roundErr == nil && len(toolCalls) == 0 && strings.TrimSpace(reply) != ""
}

func isTerminalSessionState(state string) bool {
	switch strings.TrimSpace(state) {
	case StateDone, StateCancelled, "failed", "timeout":
		return true
	default:
		return false
	}
}

func shouldRunGateSemanticsFollowup(call ai.ToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "update_gate", "add_gate":
		return true
	case "batch_operations":
		var payload struct {
			Operations []struct {
				Tool string `json:"tool"`
			} `json:"operations"`
		}
		if err := json.Unmarshal(call.Arguments, &payload); err != nil {
			return false
		}
		for _, op := range payload.Operations {
			switch strings.ToLower(strings.TrimSpace(op.Tool)) {
			case "update_gate", "add_gate", "add_node", "delete_node", "move_node":
				return true
			}
		}
	}
	return false
}

func gateSemanticsArgsFromCall(call ai.ToolCall) json.RawMessage {
	if !strings.EqualFold(strings.TrimSpace(call.Name), "update_gate") {
		return json.RawMessage(`{}`)
	}
	var payload struct {
		NodeID string `json:"nodeId"`
	}
	if err := json.Unmarshal(call.Arguments, &payload); err != nil {
		return json.RawMessage(`{}`)
	}
	payload.NodeID = strings.TrimSpace(payload.NodeID)
	if payload.NodeID == "" {
		return json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(map[string]string{"nodeId": payload.NodeID})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}

func buildAutoReadCallForMutation(call ai.ToolCall) ai.ToolCall {
	args := map[string]interface{}{}
	if len(call.Arguments) > 0 && json.Valid(call.Arguments) {
		_ = json.Unmarshal(call.Arguments, &args)
	}

	for _, key := range []string{"nodeId", "parentId", "newParentId", "rootNodeId"} {
		if nodeID := stringFromMap(args, key); nodeID != "" {
			raw, _ := json.Marshal(map[string]string{"nodeId": nodeID})
			return ai.ToolCall{ID: util.NewAgentToolCallID(), Name: "get_node_detail", Arguments: json.RawMessage(raw)}
		}
	}

	if strings.EqualFold(strings.TrimSpace(call.Name), "batch_operations") {
		if nodeID := firstNodeIDFromBatchArgs(args); nodeID != "" {
			raw, _ := json.Marshal(map[string]string{"nodeId": nodeID})
			return ai.ToolCall{ID: util.NewAgentToolCallID(), Name: "get_node_detail", Arguments: json.RawMessage(raw)}
		}
	}

	return ai.ToolCall{
		ID:        util.NewAgentToolCallID(),
		Name:      "get_graph_snapshot",
		Arguments: json.RawMessage(`{"maxNodes":120,"maxEdges":200}`),
	}
}

func firstNodeIDFromBatchArgs(args map[string]interface{}) string {
	ops, ok := args["operations"].([]interface{})
	if !ok {
		return ""
	}
	for _, item := range ops {
		op, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		opArgs, ok := op["args"].(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range []string{"nodeId", "parentId", "newParentId", "rootNodeId"} {
			if nodeID := stringFromMap(opArgs, key); nodeID != "" {
				return nodeID
			}
		}
	}
	return ""
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	if v, ok := values[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func buildFocusNodeHint(focusNodeIDs, selectedNodeIDs []string) string {
	ids := uniqueStrings(append(append([]string{}, focusNodeIDs...), selectedNodeIDs...))
	if len(ids) == 0 {
		return ""
	}
	ids = limitStrings(ids, 20)
	return fmt.Sprintf("系统上下文：本轮前端关注节点为 [%s]。涉及编辑或判断时，优先调用 get_node_detail 或 get_subtree 读取这些节点附近结构。", strings.Join(ids, ","))
}

func isHardStopSafetyError(err error) bool {
	return errors.Is(err, ErrAgentLoopDetected) ||
		errors.Is(err, ErrAgentMaxToolCalls) ||
		errors.Is(err, ErrAgentRateLimited) ||
		errors.Is(err, ErrAgentNodeLimitExceeded)
}

func appendToolParameterHint(toolName, errMsg string) string {
	hint := buildToolParameterHint(toolName)
	if hint == "" {
		return strings.TrimSpace(errMsg)
	}
	base := strings.TrimSpace(errMsg)
	if base == "" {
		return hint
	}
	return base + "；" + hint
}

func buildToolParameterHint(toolName string) string {
	def, ok := graph_ops.GetTool(toolName)
	if !ok || len(def.Parameters) == 0 {
		return ""
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		return ""
	}

	rawRequired, ok := schema["required"].([]interface{})
	if !ok || len(rawRequired) == 0 {
		return ""
	}

	required := make([]string, 0, len(rawRequired))
	for _, item := range rawRequired {
		field := strings.TrimSpace(fmt.Sprintf("%v", item))
		if field == "" {
			continue
		}
		required = append(required, field)
	}
	if len(required) == 0 {
		return ""
	}

	hint := fmt.Sprintf("参数要求 required=[%s]", strings.Join(required, ","))
	if strings.EqualFold(strings.TrimSpace(toolName), "add_gate") {
		hint += "；若来源于 get_node_detail，可将 childNodeIds 映射为 childIds"
	}
	return hint
}

type toolDispatchOutcome struct {
	Summary      string
	Patch        *graph_ops.GraphPatch
	Status       string
	ChangedNodes int
	ChangedEdges int
}

type ToolObservationSummary struct {
	Tool            string         `json:"tool"`
	Status          string         `json:"status"`
	NodeCount       int            `json:"nodeCount,omitempty"`
	EdgeCount       int            `json:"edgeCount,omitempty"`
	IssueCount      int            `json:"issueCount,omitempty"`
	IssueCodes      []string       `json:"issueCodes,omitempty"`
	AffectedNodeIDs []string       `json:"affectedNodeIds,omitempty"`
	ChangedCounts   map[string]int `json:"changedCounts,omitempty"`
	Error           string         `json:"error,omitempty"`
	Summary         string         `json:"summary,omitempty"`
}

func normalizeToolFinalStatus(status string) string {
	switch strings.TrimSpace(status) {
	case toolStatusFailed, toolStatusCancelled, toolStatusDiscarded, toolStatusClientOnly, toolStatusSuccess, toolStatusPending:
		return strings.TrimSpace(status)
	default:
		return toolStatusSuccess
	}
}

func hasGraphPatchChanges(patch *graph_ops.GraphPatch) bool {
	if patch == nil {
		return false
	}
	return len(patch.UpsertNodes) > 0 || len(patch.DeleteNodes) > 0 || len(patch.UpsertEdges) > 0 || len(patch.DeleteEdges) > 0
}

func patchNodeMutationCount(patch *graph_ops.GraphPatch) int {
	if patch == nil {
		return 0
	}
	count := len(patch.UpsertNodes) + len(patch.DeleteNodes)
	if count == 0 && (len(patch.UpsertEdges) > 0 || len(patch.DeleteEdges) > 0) {
		return 1
	}
	return count
}

func buildToolCallResultEvent(callID, toolName, status string, success bool, summary, errMsg string, patch *graph_ops.GraphPatch) map[string]interface{} {
	event := map[string]interface{}{
		"type":          "tool_call_result",
		"schemaVersion": toolCallResultSchemaV1,
		"callId":        strings.TrimSpace(callID),
		"tool":          strings.TrimSpace(toolName),
		"status":        normalizeToolFinalStatus(status),
		"success":       success,
		"summary":       strings.TrimSpace(summary),
	}
	if v := strings.TrimSpace(errMsg); v != "" {
		event["error"] = v
	}
	if patch != nil {
		event["patch"] = *patch
	}
	return event
}

func emitThinkingEvent(writeEvent func(map[string]interface{}) bool, phase string, round int, message string) {
	if writeEvent == nil {
		return
	}
	p := strings.TrimSpace(phase)
	m := strings.TrimSpace(message)
	if p == "" || m == "" {
		return
	}
	event := map[string]interface{}{
		"type":    agentThinkingEventType,
		"phase":   p,
		"message": m,
	}
	if round > 0 {
		event["round"] = round
	}
	_ = writeEvent(event)
}

func summarizeToolNames(calls []ai.ToolCall, maxCount int) string {
	if len(calls) == 0 {
		return ""
	}
	if maxCount <= 0 {
		maxCount = 1
	}

	names := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) <= maxCount {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxCount], ", ") + fmt.Sprintf(" 等 %d 个", len(names))
}

func summarizeThinking(text string, maxLen int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if maxLen <= 0 {
		maxLen = 1200
	}
	runes := []rune(trimmed)
	if len(runes) <= maxLen {
		return trimmed
	}
	return string(runes[:maxLen]) + "..."
}

func buildAssistantToolHistoryMessage(reply, reasoning string, toolCalls []ai.ToolCall) (ai.ChatHistoryMessage, bool) {
	msg := ai.ChatHistoryMessage{
		Role:             "assistant",
		Content:          strings.TrimSpace(reply),
		ReasoningContent: strings.TrimSpace(reasoning),
		ToolCalls:        cloneToolCallsForHistory(toolCalls),
	}

	if msg.Content == "" && msg.ReasoningContent == "" && len(msg.ToolCalls) == 0 {
		return ai.ChatHistoryMessage{}, false
	}
	return msg, true
}

func cloneToolCallsForHistory(calls []ai.ToolCall) []ai.ToolCall {
	if len(calls) == 0 {
		return nil
	}

	out := make([]ai.ToolCall, 0, len(calls))
	for _, call := range calls {
		args := append(json.RawMessage(nil), call.Arguments...)
		out = append(out, ai.ToolCall{
			ID:        strings.TrimSpace(call.ID),
			Name:      strings.TrimSpace(call.Name),
			Arguments: args,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildToolResultHistoryMessage(callID, toolName, status, summary string, patch *graph_ops.GraphPatch, errMsg string) ai.ChatHistoryMessage {
	return ai.ChatHistoryMessage{
		Role:       "tool",
		ToolCallID: strings.TrimSpace(callID),
		Content:    buildToolResultContent(toolName, status, summary, patch, errMsg),
	}
}

func buildToolResultContent(toolName, status, summary string, patch *graph_ops.GraphPatch, errMsg string) string {
	observation := buildToolObservationSummary(toolName, status, summary, patch, errMsg)
	raw, err := json.Marshal(observation)
	if err != nil {
		fallback, _ := json.Marshal(map[string]string{
			"tool":    strings.TrimSpace(toolName),
			"status":  normalizeToolFinalStatus(status),
			"summary": summarizePlainText(summary, defaultToolSummaryLimit()),
			"error":   strings.TrimSpace(errMsg),
		})
		return string(fallback)
	}
	return string(raw)
}

func (s *AgentService) compactToolObservationText(toolName, status, summary string, patch *graph_ops.GraphPatch, errMsg string) string {
	raw, err := json.Marshal(buildToolObservationSummary(toolName, status, summary, patch, errMsg))
	if err != nil {
		return summarizePlainText(summary, s.maxToolSummaryChars())
	}
	return summarizePlainText(string(raw), s.maxToolSummaryChars())
}

func (s *AgentService) maxToolSummaryChars() int {
	if s != nil && s.cfg.MaxToolSummaryChars > 0 {
		return s.cfg.MaxToolSummaryChars
	}
	return defaultToolSummaryLimit()
}

func defaultToolSummaryLimit() int { return 1200 }

func buildToolObservationSummary(toolName, status, summary string, patch *graph_ops.GraphPatch, errMsg string) ToolObservationSummary {
	observation := ToolObservationSummary{
		Tool:   strings.TrimSpace(toolName),
		Status: normalizeToolFinalStatus(status),
		Error:  strings.TrimSpace(errMsg),
	}

	if patch != nil {
		observation.ChangedCounts = map[string]int{
			"upsertNodes": len(patch.UpsertNodes),
			"deleteNodes": len(patch.DeleteNodes),
			"upsertEdges": len(patch.UpsertEdges),
			"deleteEdges": len(patch.DeleteEdges),
		}
		affected := make([]string, 0, len(patch.UpsertNodes)+len(patch.DeleteNodes))
		for _, item := range patch.UpsertNodes {
			if id, ok := item["id"].(string); ok && strings.TrimSpace(id) != "" {
				affected = append(affected, strings.TrimSpace(id))
			}
		}
		affected = append(affected, patch.DeleteNodes...)
		observation.AffectedNodeIDs = limitStrings(uniqueStrings(affected), 12)
	}

	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return observation
	}
	if !json.Valid([]byte(trimmed)) {
		observation.Summary = summarizePlainText(trimmed, 280)
		return observation
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		observation.Summary = summarizePlainText(trimmed, 280)
		return observation
	}
	observation.NodeCount = intFromAny(payload["nodeCount"])
	if observation.NodeCount == 0 {
		observation.NodeCount = lenFromAny(payload["nodes"])
	}
	observation.EdgeCount = intFromAny(payload["edgeCount"])
	if observation.EdgeCount == 0 {
		observation.EdgeCount = lenFromAny(payload["edges"])
	}
	observation.IssueCount = intFromAny(payload["issueCount"])
	if observation.IssueCount == 0 {
		observation.IssueCount = lenFromAny(payload["issues"])
	}
	observation.IssueCodes = issueCodesFromPayload(payload["issues"])
	if observation.Summary == "" {
		if v, ok := payload["summary"].(string); ok {
			observation.Summary = summarizePlainText(v, 280)
		}
	}
	if len(observation.AffectedNodeIDs) == 0 {
		observation.AffectedNodeIDs = affectedNodeIDsFromPayload(payload)
	}
	return observation
}

func intFromAny(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	default:
		return 0
	}
}

func lenFromAny(v interface{}) int {
	switch t := v.(type) {
	case []interface{}:
		return len(t)
	case []map[string]interface{}:
		return len(t)
	default:
		return 0
	}
}

func issueCodesFromPayload(v interface{}) []string {
	items, ok := v.([]interface{})
	if !ok {
		return nil
	}
	codes := make([]string, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if code, ok := obj["code"].(string); ok && strings.TrimSpace(code) != "" {
			codes = append(codes, strings.TrimSpace(code))
		}
	}
	return limitStrings(uniqueStrings(codes), 12)
}

func affectedNodeIDsFromPayload(payload map[string]interface{}) []string {
	out := make([]string, 0, 8)
	for _, key := range []string{"nodeId", "rootNodeId"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	for _, key := range []string{"parentNodeIds", "childNodeIds", "topEventIds"} {
		out = append(out, stringSliceFromAny(payload[key])...)
	}
	return limitStrings(uniqueStrings(out), 12)
}

func stringSliceFromAny(v interface{}) []string {
	items, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func summarizePlainText(text string, maxLen int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if maxLen <= 0 {
		maxLen = defaultToolSummaryLimit()
	}
	runes := []rune(trimmed)
	if len(runes) <= maxLen {
		return trimmed
	}
	return string(runes[:maxLen]) + "..."
}

func summarizeToolSummaryForHistory(summary string) string {
	v := strings.TrimSpace(summary)
	if v == "" {
		return ""
	}

	if !json.Valid([]byte(v)) {
		if len([]rune(v)) <= 280 {
			return v
		}
		runes := []rune(v)
		return string(runes[:280]) + "..."
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(v), &payload); err != nil {
		return v
	}

	compact := map[string]interface{}{}
	for _, key := range []string{"tool", "status", "summary", "error", "nodeCount", "edgeCount", "issueCount", "returnedNodeCount", "returnedEdgeCount", "truncated"} {
		if val, ok := payload[key]; ok {
			compact[key] = val
		}
	}
	if ids, ok := payload["parentNodeIds"]; ok {
		compact["parentNodeIds"] = ids
	}
	if ids, ok := payload["childNodeIds"]; ok {
		compact["childNodeIds"] = ids
	}

	raw, err := json.Marshal(compact)
	if err != nil {
		return v
	}
	return string(raw)
}

func (s *AgentService) dispatchToolCall(
	ctx context.Context,
	session *AgentSession,
	projectID, graphType string,
	readOnly bool,
	runtimeSnapshot *faultTreeRuntimeSnapshot,
	call ai.ToolCall,
	def *graph_ops.ToolDefinition,
	writeEvent func(map[string]interface{}) bool,
) (*toolDispatchOutcome, error) {
	if def == nil {
		return nil, graph_ops.ErrUnknownTool
	}

	switch def.Tier {
	case graph_ops.TierClient:
		session.IncTierOps("client")
		_ = writeEvent(map[string]interface{}{"type": "client_tool", "callId": call.ID, "tool": call.Name, "args": json.RawMessage(call.Arguments)})
		_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusClientOnly, true, "client_tool_dispatched", "", nil))
		return &toolDispatchOutcome{Summary: "client_tool_dispatched", Status: toolStatusClientOnly}, nil
	case graph_ops.TierServer:
		if readOnly && graph_ops.ToolMutatesGraph(def.Name) {
			summary := fmt.Sprintf("%s skipped: read-only mode", call.Name)
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusCancelled, false, summary, "", nil))
			return &toolDispatchOutcome{Summary: summary, Status: toolStatusCancelled}, nil
		}

		needConfirm := def.RequireConfirm
		if strings.EqualFold(def.Name, "batch_operations") && def.ConfirmThreshold > 0 {
			var payload struct {
				Operations []map[string]interface{} `json:"operations"`
			}
			_ = json.Unmarshal(call.Arguments, &payload)
			if len(payload.Operations) > def.ConfirmThreshold {
				needConfirm = true
			}
		}
		if needConfirm {
			session.SetPending(call.ID, call.Name, call.Arguments)
			session.SetState(StatePausedForConfirm)
			_ = s.upsertRuntimePending(session, call.ID, call.Name, string(def.Tier), call.Arguments, nil, "confirm", s.cfg.ConfirmTimeout)
			_ = writeEvent(map[string]interface{}{
				"type":   "confirm_required",
				"callId": call.ID,
				"tool":   call.Name,
				"args":   json.RawMessage(call.Arguments),
			})
			signal, err := s.waitForConfirmation(ctx, session, s.cfg.ConfirmTimeout)
			if err != nil {
				if errors.Is(err, context.Canceled) && session.State() == StatePausedForConfirm {
					_ = s.markRuntimeWaitStatus(session.ID, "waiting")
					return nil, err
				}
				waitStatus := "rejected"
				if errors.Is(err, ErrSessionConfirmTimeout) {
					waitStatus = "timeout"
				}
				_ = s.clearRuntimePending(session.ID, waitStatus)
				session.ClearPending()
				session.SetState(StateRunning)
				return nil, err
			}
			session.ClearPending()
			session.SetState(StateRunning)
			if !signal.Approved {
				_ = s.clearRuntimePending(session.ID, "rejected")
				_ = writeEvent(map[string]interface{}{"type": "tool_call_cancelled", "callId": call.ID, "tool": call.Name})
				_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusCancelled, false, "user_cancelled", "", nil))
				return &toolDispatchOutcome{Summary: "user_cancelled", Status: toolStatusCancelled}, nil
			}
			_ = s.clearRuntimePending(session.ID, "approved")
		}

		_ = writeEvent(map[string]interface{}{"type": "tool_call_start", "callId": call.ID, "tool": call.Name})
		var (
			res *graph_ops.ExecuteResult
			err error
		)
		if runtimeSnapshot != nil && graphType == service.AssistantConversationTypeFaultTree {
			var nextNodes []model.FaultTreeNode
			var nextEdges []model.FaultTreeEdge
			var nextRevision int
			res, nextNodes, nextEdges, nextRevision, err = s.executor.ExecuteFaultTreeSnapshot(
				ctx,
				projectID,
				call.Name,
				call.Arguments,
				runtimeSnapshot.nodes,
				runtimeSnapshot.edges,
				runtimeSnapshot.revision,
			)
			if err == nil {
				runtimeSnapshot.nodes = nextNodes
				runtimeSnapshot.edges = nextEdges
				runtimeSnapshot.revision = nextRevision
			}
		} else {
			res, err = s.executor.Execute(ctx, projectID, graphType, call.Name, call.Arguments)
		}
		if err != nil {
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": err.Error()})
			return nil, err
		}
		session.IncTierOps("server")
		patchCopy := res.Patch
		_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusSuccess, true, res.Summary, "", &patchCopy))
		return &toolDispatchOutcome{Summary: res.Summary, Patch: &patchCopy, Status: toolStatusSuccess, ChangedNodes: res.ChangedNodes, ChangedEdges: res.ChangedEdges}, nil
	case graph_ops.TierHybrid:
		if readOnly {
			summary := fmt.Sprintf("%s skipped: read-only mode", call.Name)
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusCancelled, false, summary, "", nil))
			return &toolDispatchOutcome{Summary: summary, Status: toolStatusCancelled}, nil
		}
		preview, err := s.hybridEngine.GeneratePreview(ctx, projectID, graphType, call.Name, call.Arguments)
		if err != nil {
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": err.Error()})
			return nil, err
		}

		session.SetPending(call.ID, call.Name, call.Arguments)
		session.SetState(StatePausedForPreview)
		_ = s.upsertRuntimePending(session, call.ID, call.Name, string(def.Tier), call.Arguments, preview, "preview", s.cfg.PreviewTimeout)
		_ = writeEvent(map[string]interface{}{"type": "preview_ready", "callId": call.ID, "tool": call.Name, "preview": preview})

		signal, err := s.waitForConfirmation(ctx, session, s.cfg.PreviewTimeout)
		if err != nil {
			if errors.Is(err, context.Canceled) && session.State() == StatePausedForPreview {
				_ = s.markRuntimeWaitStatus(session.ID, "waiting")
				return nil, err
			}
			waitStatus := "rejected"
			if errors.Is(err, ErrSessionConfirmTimeout) {
				waitStatus = "timeout"
			}
			_ = s.clearRuntimePending(session.ID, waitStatus)
			session.ClearPending()
			session.SetState(StateRunning)
			return nil, err
		}
		session.ClearPending()
		session.SetState(StateRunning)
		if !signal.Approved {
			_ = s.clearRuntimePending(session.ID, "rejected")
			_ = writeEvent(map[string]interface{}{"type": "preview_discarded", "callId": call.ID, "tool": call.Name})
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusDiscarded, false, "preview_discarded", "", nil))
			return &toolDispatchOutcome{Summary: "preview_discarded", Status: toolStatusDiscarded}, nil
		}
		_ = s.clearRuntimePending(session.ID, "approved")

		res, err := s.hybridEngine.Commit(ctx, projectID, graphType, preview, signal.ApprovedOps)
		if err != nil {
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": err.Error()})
			return nil, err
		}
		if res == nil {
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusClientOnly, true, "preview_only", "", nil))
			return &toolDispatchOutcome{Summary: "preview_only", Status: toolStatusClientOnly}, nil
		}
		if !hasGraphPatchChanges(&res.Patch) {
			summary := strings.TrimSpace(res.Summary)
			if summary == "" {
				summary = "preview_only"
			}
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusClientOnly, true, summary, "", nil))
			return &toolDispatchOutcome{Summary: summary, Status: toolStatusClientOnly}, nil
		}
		session.IncTierOps("hybrid")
		patchCopy := res.Patch
		_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusSuccess, true, res.Summary, "", &patchCopy))
		return &toolDispatchOutcome{Summary: res.Summary, Patch: &patchCopy, Status: toolStatusSuccess, ChangedNodes: res.ChangedNodes, ChangedEdges: res.ChangedEdges}, nil
	default:
		return nil, graph_ops.ErrUnknownTool
	}
}

func (s *AgentService) persistRoundHistoryMessages(conversationID string, history []ai.ChatHistoryMessage) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" || len(history) == 0 {
		return nil
	}

	base := time.Now().UTC()
	for i, item := range history {
		msg, ok, err := toPersistedAIChatMessage(conversationID, item, base.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := s.chatMessageRepo.Create(msg); err != nil {
			return err
		}
	}
	return nil
}

func toPersistedAIChatMessage(conversationID string, history ai.ChatHistoryMessage, createdAt time.Time) (*model.AIChatMessage, bool, error) {
	role := strings.ToLower(strings.TrimSpace(history.Role))
	if role == "" {
		return nil, false, nil
	}

	msg := &model.AIChatMessage{
		ID:             util.NewMessageID(),
		ConversationID: strings.TrimSpace(conversationID),
		Role:           role,
		Content:        strings.TrimSpace(history.Content),
		CreatedAt:      createdAt.UTC(),
	}

	switch role {
	case "assistant":
		reasoning := strings.TrimSpace(history.ReasoningContent)
		if reasoning != "" {
			msg.ReasoningContent = &reasoning
		}
		toolCallsJSON, err := marshalToolCallsForStorage(history.ToolCalls)
		if err != nil {
			return nil, false, err
		}
		if len(toolCallsJSON) > 0 {
			msg.ToolCalls = datatypes.JSON(toolCallsJSON)
		}
		if msg.Content == "" && msg.ReasoningContent == nil && len(msg.ToolCalls) == 0 {
			return nil, false, nil
		}
	case "tool":
		callID := strings.TrimSpace(history.ToolCallID)
		if callID == "" || msg.Content == "" {
			return nil, false, nil
		}
		msg.ToolCallID = &callID
	default:
		return nil, false, nil
	}

	return msg, true, nil
}

func marshalToolCallsForStorage(calls []ai.ToolCall) ([]byte, error) {
	if len(calls) == 0 {
		return nil, nil
	}

	normalized := make([]ai.ToolCall, 0, len(calls))
	for i, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("history_call_%d", i+1)
		}
		args := bytes.TrimSpace(call.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = []byte("{}")
		}

		normalized = append(normalized, ai.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: append(json.RawMessage(nil), args...),
		})
	}
	if len(normalized) == 0 {
		return nil, nil
	}

	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *AgentService) waitForConfirmation(ctx context.Context, session *AgentSession, timeout time.Duration) (ConfirmSignal, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	pending := session.Snapshot().PendingCallID
	for {
		select {
		case <-ctx.Done():
			return ConfirmSignal{}, ctx.Err()
		case <-session.CancelChan():
			return ConfirmSignal{}, ErrSessionClosed
		case <-timer.C:
			return ConfirmSignal{}, ErrSessionConfirmTimeout
		case signal := <-session.ConfirmChan():
			signalCallID := strings.TrimSpace(signal.CallID)
			if pending == "" {
				if signalCallID == "" {
					return signal, nil
				}
				continue
			}
			if signalCallID == pending {
				return signal, nil
			}
		}
	}
}

func (s *AgentService) loadConversationContext(ctx context.Context, conversation *model.AIConversation) (interface{}, int, error) {
	if conversation == nil {
		return nil, 0, ErrAgentConversationNotFound
	}
	switch conversation.Type {
	case service.AssistantConversationTypeFaultTree:
		graph, revision, err := s.ftService.GetGraph(ctx, conversation.ProjectID)
		if err != nil {
			return nil, 0, err
		}
		return map[string]interface{}{"nodes": graph.Nodes, "edges": graph.Edges}, revision, nil
	case service.AssistantConversationTypeKnowledgeGraph:
		graph, revision, err := s.kgService.GetGraph(ctx, conversation.ProjectID)
		if err != nil {
			return nil, 0, err
		}
		return map[string]interface{}{"rfNodes": graph.Nodes, "rfEdges": graph.Edges}, revision, nil
	default:
		return nil, 0, fmt.Errorf("unsupported conversation type: %s", conversation.Type)
	}
}

func parseFaultTreeGraphSnapshot(raw json.RawMessage) (*service.FaultTreeGraph, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("%w: snapshot payload is empty", ErrAgentInvalidGraphSnapshot)
	}

	var payload service.FaultTreeGraph
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentInvalidGraphSnapshot, err)
	}
	if payload.Nodes == nil {
		payload.Nodes = []model.FaultTreeNode{}
	}
	if payload.Edges == nil {
		payload.Edges = []model.FaultTreeEdge{}
	}
	return &payload, nil
}

func toChatHistory(messages []model.AIChatMessage) []ai.ChatHistoryMessage {
	history := make([]ai.ChatHistoryMessage, 0, len(messages))
	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		switch role {
		case "user":
			content := strings.TrimSpace(m.Content)
			if content == "" {
				continue
			}
			history = append(history, ai.ChatHistoryMessage{Role: role, Content: content})
		case "assistant":
			content := strings.TrimSpace(m.Content)
			reasoning := ""
			if m.ReasoningContent != nil {
				reasoning = strings.TrimSpace(*m.ReasoningContent)
			}
			toolCalls := parseToolCallsFromStorage(m.ToolCalls)
			if content == "" && reasoning == "" && len(toolCalls) == 0 {
				continue
			}
			history = append(history, ai.ChatHistoryMessage{
				Role:             role,
				Content:          content,
				ReasoningContent: reasoning,
				ToolCalls:        toolCalls,
			})
		case "tool":
			content := strings.TrimSpace(m.Content)
			callID := ""
			if m.ToolCallID != nil {
				callID = strings.TrimSpace(*m.ToolCallID)
			}
			if content == "" || callID == "" {
				continue
			}
			history = append(history, ai.ChatHistoryMessage{Role: role, Content: content, ToolCallID: callID})
		}
	}
	return history
}

func parseToolCallsFromStorage(raw datatypes.JSON) []ai.ToolCall {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}

	var calls []ai.ToolCall
	if err := json.Unmarshal(trimmed, &calls); err != nil {
		return nil
	}

	out := make([]ai.ToolCall, 0, len(calls))
	for i, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("history_call_%d", i+1)
		}
		args := bytes.TrimSpace(call.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = []byte("{}")
		}
		out = append(out, ai.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: append(json.RawMessage(nil), args...),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildConversationTitle(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= 30 {
		return trimmed
	}
	return string(runes[:30])
}

func estimateNodeMutation(toolName string) int {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "delete_node", "batch_operations", "add_node", "add_gate", "suggest_node_merge":
		return 3
	default:
		return 1
	}
}

func buildToolContinuationPrompt(originalMessage string, round int, summaries []string) string {
	originalMessage = strings.TrimSpace(originalMessage)
	if originalMessage == "" {
		originalMessage = "请继续完成用户任务"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("原始用户目标：%s\n", originalMessage))
	b.WriteString(fmt.Sprintf("当前是第 %d 轮工具执行后的结果：\n", round))
	if len(summaries) == 0 {
		b.WriteString("- 本轮无可用工具执行摘要。\n")
	} else {
		for i, item := range summaries {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, item))
		}
	}
	b.WriteString("请基于以上结果决定下一步：\n")
	b.WriteString("1) 若仍需结构化图编辑，请继续调用合适工具；\n")
	b.WriteString("2) 若任务已完成或无可执行工具，请直接给出最终自然语言答复。")
	return b.String()
}
