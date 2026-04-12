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
)

type faultTreeRuntimeSnapshot struct {
	nodes    []model.FaultTreeNode
	edges    []model.FaultTreeEdge
	revision int
}

type AgentRunInput struct {
	ConversationID string
	UserID         string
	Message        string
	Model          string
	GraphSnapshot  json.RawMessage
	ClientRevision *int
	ReadOnly       bool
	MaxToolRounds  int
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
	agentToolCallRepo *repository.AgentToolCallRepository
	ftService         *service.FaultTreeService
	kgService         *service.KnowledgeGraphService
	cfg               config.AgentConfig
}

type PersistedSessionStatus struct {
	Session *model.AgentSession `json:"session"`
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
		agentToolCallRepo: agentToolCallRepo,
		ftService:         ftService,
		kgService:         kgService,
		cfg:               cfg,
	}
}

func (s *AgentService) Enabled() bool {
	return s.cfg.Enabled
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

	return &PersistedSessionStatus{Session: record}, nil
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
		now := time.Now().UTC()
		modelSession.State = snap.State
		modelSession.ToolCallCount = snap.ToolCallCount
		modelSession.ServerOps = snap.ServerOps
		modelSession.ClientOps = snap.ClientOps
		modelSession.HybridOps = snap.HybridOps
		modelSession.TokensUsed = snap.TokensUsed
		modelSession.EndedAt = &now
		_ = s.agentSessionRepo.Update(nil, modelSession)
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

	toolDefs := graph_ops.GetToolsForGraphType(conversation.Type)
	toolSchemas := graph_ops.ToOAITools(toolDefs)
	history := toChatHistory(historyMessages)
	workingHistory := append([]ai.ChatHistoryMessage(nil), history...)

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
	roundLimit := s.cfg.MaxRounds
	if input.MaxToolRounds > 0 {
		roundLimit = input.MaxToolRounds
	}
	finalReply := ""
	finalReasoning := ""
	modelUsed := ""
	allToolSummaries := make([]string, 0)
	var runErr error

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

		if round == 0 {
			var textBuilder strings.Builder
			roundReply, roundReasoning, roundToolCalls, roundTokens, roundModelUsed, roundErr = s.provider.ChatStreamWithTools(ctx, ai.AgentChatRequest{
				ChatRequest: ai.ChatRequest{
					ContextData: currentContext,
					GraphType:   conversation.Type,
					Message:     currentMessage,
					Model:       strings.TrimSpace(input.Model),
					History:     workingHistory,
				},
				Tools:      toolSchemas,
				ToolChoice: "auto",
			}, func(chunk string) {
				if chunk == "" {
					return
				}
				textBuilder.WriteString(chunk)
				_ = writeEvent(map[string]interface{}{"type": "content", "content": chunk})
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
					Model:       strings.TrimSpace(input.Model),
					History:     workingHistory,
				},
				Tools:      toolSchemas,
				ToolChoice: "auto",
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
				session.SetState(StateDone)
				return &AgentRunOutput{
					ConversationID: conversation.ID,
					SessionID:      session.ID,
					UserMessageID:  userMessage.ID,
					ModelUsed:      modelUsed,
				}, roundErr
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

	if strings.TrimSpace(finalReply) == "" && len(allToolSummaries) > 0 {
		finalReply = strings.Join(allToolSummaries, "；")
	}
	if strings.TrimSpace(finalReply) == "" {
		finalReply = "操作已完成"
	}

	session.SetState(StateDone)
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
		return nil, err
	}
	if err := s.conversationRepo.RecordMessageActivity(conversation.ID, assistantMessage.CreatedAt, ""); err != nil {
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
	_ = writeEvent(map[string]interface{}{
		"type":                    "iteration_limit_reached",
		"callId":                  callID,
		"round":                   round,
		"maxRounds":               roundLimit,
		"suggestedContinueRounds": 1,
	})

	signal, err := s.waitForConfirmation(ctx, session, s.cfg.ConfirmTimeout)
	session.ClearPending()
	session.SetState(StateRunning)
	if err != nil {
		return 0, false, err
	}

	if !signal.Approved {
		_ = writeEvent(map[string]interface{}{
			"type":      "iteration_stopped",
			"callId":    callID,
			"round":     round,
			"maxRounds": roundLimit,
		})
		return 0, false, nil
	}

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
	summaries = make([]string, 0, len(toolCalls))
	toolResults = make([]ai.ChatHistoryMessage, 0, len(toolCalls))
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

		if s.safety != nil {
			if err := s.safety.CheckToolCall(session, call, estimateNodeMutation(call.Name)); err != nil {
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

		toolRecord := &model.AgentToolCall{
			ID:        util.NewAgentToolCallID(),
			SessionID: session.ID,
			CallID:    call.ID,
			ToolName:  call.Name,
			Tier:      string(def.Tier),
			Arguments: datatypes.JSON(call.Arguments),
			Status:    "running",
		}
		_ = s.agentToolCallRepo.Create(nil, toolRecord)

		outcome, resultErr := s.dispatchToolCall(ctx, session, projectID, graphType, readOnly, runtimeSnapshot, call, def, writeEvent)
		if resultErr != nil {
			errMsg := resultErr.Error()
			_ = s.agentToolCallRepo.UpdateStatus(toolRecord.ID, toolStatusFailed, nil, nil, &errMsg)
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusFailed, false, errMsg, errMsg, nil))
			summary := fmt.Sprintf("%s: %s", call.Name, errMsg)
			summaries = append(summaries, summary)
			toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, toolStatusFailed, summary, nil, errMsg))
			continue
		}
		if outcome == nil {
			outcome = &toolDispatchOutcome{Summary: "", Status: toolStatusSuccess}
		}

		finalStatus := normalizeToolFinalStatus(outcome.Status)
		resultSummary := strings.TrimSpace(outcome.Summary)
		resultPatch := outcome.Patch
		if resultSummary == "" {
			resultSummary = fmt.Sprintf("%s: %s", call.Name, finalStatus)
		}

		if hasGraphPatchChanges(resultPatch) {
			graphMutated = true
		}

		var patchRaw []byte
		if resultPatch != nil {
			patchRaw, _ = json.Marshal(resultPatch)
		}
		resCopy := resultSummary
		_ = s.agentToolCallRepo.UpdateStatus(toolRecord.ID, finalStatus, &resCopy, patchRaw, nil)
		summaries = append(summaries, resultSummary)
		toolResults = append(toolResults, buildToolResultHistoryMessage(call.ID, call.Name, finalStatus, resultSummary, resultPatch, ""))
	}
	return summaries, toolResults, graphMutated, nil
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
	Summary string
	Patch   *graph_ops.GraphPatch
	Status  string
}

func normalizeToolFinalStatus(status string) string {
	switch strings.TrimSpace(status) {
	case toolStatusFailed, toolStatusCancelled, toolStatusDiscarded, toolStatusClientOnly, toolStatusSuccess:
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
	payload := map[string]interface{}{
		"tool":   strings.TrimSpace(toolName),
		"status": normalizeToolFinalStatus(status),
	}

	if v := strings.TrimSpace(summary); v != "" {
		payload["summary"] = v
	}
	if v := strings.TrimSpace(errMsg); v != "" {
		payload["error"] = v
	}
	if patch != nil {
		payload["patch"] = patch
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		fallback, _ := json.Marshal(map[string]string{
			"tool":    strings.TrimSpace(toolName),
			"status":  normalizeToolFinalStatus(status),
			"summary": strings.TrimSpace(summary),
			"error":   strings.TrimSpace(errMsg),
		})
		return string(fallback)
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
			_ = writeEvent(map[string]interface{}{
				"type":   "confirm_required",
				"callId": call.ID,
				"tool":   call.Name,
				"args":   json.RawMessage(call.Arguments),
			})
			signal, err := s.waitForConfirmation(ctx, session, s.cfg.ConfirmTimeout)
			session.ClearPending()
			session.SetState(StateRunning)
			if err != nil {
				return nil, err
			}
			if !signal.Approved {
				_ = writeEvent(map[string]interface{}{"type": "tool_call_cancelled", "callId": call.ID, "tool": call.Name})
				_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusCancelled, false, "user_cancelled", "", nil))
				return &toolDispatchOutcome{Summary: "user_cancelled", Status: toolStatusCancelled}, nil
			}
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
		return &toolDispatchOutcome{Summary: res.Summary, Patch: &patchCopy, Status: toolStatusSuccess}, nil
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
		_ = writeEvent(map[string]interface{}{"type": "preview_ready", "callId": call.ID, "tool": call.Name, "preview": preview})

		signal, err := s.waitForConfirmation(ctx, session, s.cfg.PreviewTimeout)
		session.ClearPending()
		session.SetState(StateRunning)
		if err != nil {
			return nil, err
		}
		if !signal.Approved {
			_ = writeEvent(map[string]interface{}{"type": "preview_discarded", "callId": call.ID, "tool": call.Name})
			_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusDiscarded, false, "preview_discarded", "", nil))
			return &toolDispatchOutcome{Summary: "preview_discarded", Status: toolStatusDiscarded}, nil
		}

		res, err := s.hybridEngine.Commit(ctx, projectID, graphType, preview, signal.ApprovedOps)
		if err != nil {
			_ = writeEvent(map[string]interface{}{"type": "tool_call_error", "callId": call.ID, "tool": call.Name, "error": err.Error()})
			return nil, err
		}
		session.IncTierOps("hybrid")
		patchCopy := res.Patch
		_ = writeEvent(buildToolCallResultEvent(call.ID, call.Name, toolStatusSuccess, true, res.Summary, "", &patchCopy))
		return &toolDispatchOutcome{Summary: res.Summary, Patch: &patchCopy, Status: toolStatusSuccess}, nil
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
