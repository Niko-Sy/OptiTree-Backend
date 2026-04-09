package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"
)

const (
	AssistantConversationTypeFaultTree      = "faultTree"
	AssistantConversationTypeKnowledgeGraph = "knowledgeGraph"
	assistantHistoryLimit                   = 20
)

var (
	ErrAssistantConversationNotFound = errors.New("会话不存在")
	ErrAssistantPermissionDenied     = errors.New("无会话访问权限")
	ErrAssistantInvalidType          = errors.New("会话类型非法")
	ErrAssistantProjectTypeMismatch  = errors.New("会话类型与项目类型不匹配")
	ErrAssistantInvalidCursor        = errors.New("历史游标非法")
	ErrAssistantMessageEmpty         = errors.New("消息不能为空")
)

type AssistantService struct {
	conversationRepo *repository.AIConversationRepository
	messageRepo      *repository.AIChatMessageRepository
	projectRepo      *repository.ProjectRepository
	memberRepo       *repository.MemberRepository
	provider         ai.AIProvider
	ftService        *FaultTreeService
	kgService        *KnowledgeGraphService
}

func NewAssistantService(
	conversationRepo *repository.AIConversationRepository,
	messageRepo *repository.AIChatMessageRepository,
	projectRepo *repository.ProjectRepository,
	memberRepo *repository.MemberRepository,
	provider ai.AIProvider,
	ftService *FaultTreeService,
	kgService *KnowledgeGraphService,
) *AssistantService {
	return &AssistantService{
		conversationRepo: conversationRepo,
		messageRepo:      messageRepo,
		projectRepo:      projectRepo,
		memberRepo:       memberRepo,
		provider:         provider,
		ftService:        ftService,
		kgService:        kgService,
	}
}

type CreateConversationInput struct {
	ProjectID string
	UserID    string
	Type      string
}

func (s *AssistantService) CreateConversation(ctx context.Context, input CreateConversationInput) (*model.AIConversation, error) {
	projectID := strings.TrimSpace(input.ProjectID)
	userID := strings.TrimSpace(input.UserID)
	conversationType := strings.TrimSpace(input.Type)
	if !isValidAssistantType(conversationType) {
		return nil, ErrAssistantInvalidType
	}

	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrProjectNotFound
	}
	if project.Type != toProjectType(conversationType) {
		return nil, ErrAssistantProjectTypeMismatch
	}
	if err := s.ensureProjectMembership(projectID, userID); err != nil {
		return nil, err
	}

	conversation := &model.AIConversation{
		ID:        util.NewConversationID(),
		ProjectID: projectID,
		UserID:    userID,
		Type:      conversationType,
	}
	if err := s.conversationRepo.Create(conversation); err != nil {
		return nil, err
	}
	return conversation, nil
}

type ListConversationsInput struct {
	UserID    string
	ProjectID string
	Type      string
	Page      int
	PageSize  int
}

func (s *AssistantService) ListConversations(ctx context.Context, input ListConversationsInput) ([]model.AIConversation, int64, error) {
	projectID := strings.TrimSpace(input.ProjectID)
	conversationType := strings.TrimSpace(input.Type)
	if conversationType != "" && !isValidAssistantType(conversationType) {
		return nil, 0, ErrAssistantInvalidType
	}
	if projectID != "" {
		if err := s.ensureProjectMembership(projectID, strings.TrimSpace(input.UserID)); err != nil {
			return nil, 0, err
		}
	}
	page := input.Page
	if page < 1 {
		page = util.DefaultPage
	}
	pageSize := input.PageSize
	if pageSize < 1 {
		pageSize = util.DefaultPageSize
	}
	if pageSize > util.MaxPageSize {
		pageSize = util.MaxPageSize
	}
	return s.conversationRepo.ListByUser(strings.TrimSpace(input.UserID), projectID, conversationType, page, pageSize)
}

type MessageHistoryInput struct {
	ConversationID string
	UserID         string
	Before         string
	Limit          int
}

type MessageHistoryOutput struct {
	Conversation *model.AIConversation `json:"conversation"`
	Messages     []model.AIChatMessage `json:"messages"`
	NextCursor   string                `json:"nextCursor,omitempty"`
	HasMore      bool                  `json:"hasMore"`
}

func (s *AssistantService) GetMessageHistory(ctx context.Context, input MessageHistoryInput) (*MessageHistoryOutput, error) {
	conversation, err := s.getConversationForUser(strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.UserID))
	if err != nil {
		return nil, err
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var before *repository.MessageCursor
	if strings.TrimSpace(input.Before) != "" {
		cursor, parseErr := decodeMessageCursor(input.Before)
		if parseErr != nil {
			return nil, ErrAssistantInvalidCursor
		}
		before = cursor
	}

	messages, hasMore, err := s.messageRepo.ListByConversationBefore(conversation.ID, before, limit)
	if err != nil {
		return nil, err
	}

	nextCursor := ""
	if hasMore && len(messages) > 0 {
		last := messages[len(messages)-1]
		nextCursor = encodeMessageCursor(last.CreatedAt, last.ID)
	}

	return &MessageHistoryOutput{
		Conversation: conversation,
		Messages:     messages,
		NextCursor:   nextCursor,
		HasMore:      hasMore,
	}, nil
}

type SendMessageInput struct {
	ConversationID string
	UserID         string
	Message        string
	Model          string
}

type SendMessageOutput struct {
	ConversationID   string               `json:"conversationId"`
	UserMessage      *model.AIChatMessage `json:"userMessage"`
	AssistantMessage *model.AIChatMessage `json:"assistantMessage"`
	Suggestions      []string             `json:"suggestions"`
}

func (s *AssistantService) SendMessage(ctx context.Context, input SendMessageInput) (*SendMessageOutput, error) {
	conversation, err := s.getConversationForUser(strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.UserID))
	if err != nil {
		return nil, err
	}

	messageText := strings.TrimSpace(input.Message)
	if messageText == "" {
		return nil, ErrAssistantMessageEmpty
	}

	historyMessages, err := s.messageRepo.ListRecentByConversation(conversation.ID, assistantHistoryLimit)
	if err != nil {
		return nil, err
	}

	var contextData interface{}
	if conversation.MessageCount == 0 || len(historyMessages) == 0 {
		contextData, err = s.loadConversationContext(ctx, conversation)
		if err != nil {
			return nil, err
		}
	}

	userMessage := &model.AIChatMessage{
		ID:             util.NewMessageID(),
		ConversationID: conversation.ID,
		Role:           "user",
		Content:        messageText,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.messageRepo.Create(userMessage); err != nil {
		return nil, err
	}
	if err := s.conversationRepo.RecordMessageActivity(conversation.ID, userMessage.CreatedAt, buildConversationTitle(messageText)); err != nil {
		return nil, err
	}

	aiResp, err := s.provider.Chat(ctx, ai.ChatRequest{
		ContextData: contextData,
		GraphType:   conversation.Type,
		Message:     messageText,
		Model:       strings.TrimSpace(input.Model),
		History:     toChatHistory(historyMessages),
	})
	if err != nil {
		return nil, err
	}
	if aiResp == nil {
		return nil, errors.New("AI 返回为空")
	}

	assistantText := strings.TrimSpace(aiResp.Reply)
	if assistantText == "" {
		assistantText = "(empty response)"
	}
	assistantMessage := &model.AIChatMessage{
		ID:             util.NewMessageID(),
		ConversationID: conversation.ID,
		Role:           "assistant",
		Content:        assistantText,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.messageRepo.Create(assistantMessage); err != nil {
		return nil, err
	}
	if err := s.conversationRepo.RecordMessageActivity(conversation.ID, assistantMessage.CreatedAt, ""); err != nil {
		return nil, err
	}
	if aiResp.Suggestions == nil {
		aiResp.Suggestions = []string{}
	}

	return &SendMessageOutput{
		ConversationID:   conversation.ID,
		UserMessage:      userMessage,
		AssistantMessage: assistantMessage,
		Suggestions:      aiResp.Suggestions,
	}, nil
}

type SendMessageStreamOutput struct {
	ConversationID     string `json:"conversationId"`
	UserMessageID      string `json:"userMessageId"`
	AssistantMessageID string `json:"assistantMessageId,omitempty"`
	TokensUsed         int    `json:"tokensUsed"`
	ModelUsed          string `json:"modelUsed,omitempty"`
	IsPartial          bool   `json:"isPartial"`
}

func (s *AssistantService) SendMessageStream(ctx context.Context, input SendMessageInput, onChunk func(string)) (*SendMessageStreamOutput, error) {
	conversation, err := s.getConversationForUser(strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.UserID))
	if err != nil {
		return nil, err
	}

	messageText := strings.TrimSpace(input.Message)
	if messageText == "" {
		return nil, ErrAssistantMessageEmpty
	}
	if onChunk == nil {
		onChunk = func(string) {}
	}

	historyMessages, err := s.messageRepo.ListRecentByConversation(conversation.ID, assistantHistoryLimit)
	if err != nil {
		return nil, err
	}

	var contextData interface{}
	if conversation.MessageCount == 0 || len(historyMessages) == 0 {
		contextData, err = s.loadConversationContext(ctx, conversation)
		if err != nil {
			return nil, err
		}
	}

	userMessage := &model.AIChatMessage{
		ID:             util.NewMessageID(),
		ConversationID: conversation.ID,
		Role:           "user",
		Content:        messageText,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.messageRepo.Create(userMessage); err != nil {
		return nil, err
	}
	if err := s.conversationRepo.RecordMessageActivity(conversation.ID, userMessage.CreatedAt, buildConversationTitle(messageText)); err != nil {
		return nil, err
	}

	var builder strings.Builder
	tokensUsed, modelUsed, streamErr := s.provider.ChatStream(ctx, ai.ChatRequest{
		ContextData: contextData,
		GraphType:   conversation.Type,
		Message:     messageText,
		Model:       strings.TrimSpace(input.Model),
		History:     toChatHistory(historyMessages),
	}, func(chunk string) {
		if chunk == "" {
			return
		}
		builder.WriteString(chunk)
		onChunk(chunk)
	})

	assistantText := strings.TrimSpace(builder.String())
	if streamErr != nil {
		if assistantText == "" {
			return &SendMessageStreamOutput{
				ConversationID: conversation.ID,
				UserMessageID:  userMessage.ID,
				TokensUsed:     tokensUsed,
				ModelUsed:      modelUsed,
				IsPartial:      false,
			}, streamErr
		}
		partialMessage := &model.AIChatMessage{
			ID:             util.NewMessageID(),
			ConversationID: conversation.ID,
			Role:           "assistant",
			Content:        assistantText,
			CreatedAt:      time.Now().UTC(),
			IsPartial:      true,
		}
		if strings.TrimSpace(modelUsed) != "" {
			v := strings.TrimSpace(modelUsed)
			partialMessage.Model = &v
		}
		if tokensUsed > 0 {
			v := tokensUsed
			partialMessage.TokensUsed = &v
		}
		if err := s.messageRepo.Create(partialMessage); err == nil {
			_ = s.conversationRepo.RecordMessageActivity(conversation.ID, partialMessage.CreatedAt, "")
		}
		return &SendMessageStreamOutput{
			ConversationID:     conversation.ID,
			UserMessageID:      userMessage.ID,
			AssistantMessageID: partialMessage.ID,
			TokensUsed:         tokensUsed,
			ModelUsed:          modelUsed,
			IsPartial:          true,
		}, streamErr
	}

	if assistantText == "" {
		assistantText = "(empty response)"
	}
	assistantMessage := &model.AIChatMessage{
		ID:             util.NewMessageID(),
		ConversationID: conversation.ID,
		Role:           "assistant",
		Content:        assistantText,
		CreatedAt:      time.Now().UTC(),
	}
	if strings.TrimSpace(modelUsed) != "" {
		v := strings.TrimSpace(modelUsed)
		assistantMessage.Model = &v
	}
	if tokensUsed > 0 {
		v := tokensUsed
		assistantMessage.TokensUsed = &v
	}
	if err := s.messageRepo.Create(assistantMessage); err != nil {
		return nil, err
	}
	if err := s.conversationRepo.RecordMessageActivity(conversation.ID, assistantMessage.CreatedAt, ""); err != nil {
		return nil, err
	}

	return &SendMessageStreamOutput{
		ConversationID:     conversation.ID,
		UserMessageID:      userMessage.ID,
		AssistantMessageID: assistantMessage.ID,
		TokensUsed:         tokensUsed,
		ModelUsed:          modelUsed,
		IsPartial:          false,
	}, nil
}

func (s *AssistantService) DeleteConversation(ctx context.Context, conversationID, userID string) error {
	conversation, err := s.getConversationForUser(strings.TrimSpace(conversationID), strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	return s.conversationRepo.DeleteByID(conversation.ID)
}

func (s *AssistantService) getConversationForUser(conversationID, userID string) (*model.AIConversation, error) {
	conversation, err := s.conversationRepo.FindByID(conversationID)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, ErrAssistantConversationNotFound
	}
	if strings.TrimSpace(conversation.UserID) != strings.TrimSpace(userID) {
		return nil, ErrAssistantPermissionDenied
	}
	if err := s.ensureProjectMembership(conversation.ProjectID, userID); err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			return nil, ErrAssistantConversationNotFound
		}
		return nil, err
	}
	return conversation, nil
}

func (s *AssistantService) ensureProjectMembership(projectID, userID string) error {
	projectID = strings.TrimSpace(projectID)
	userID = strings.TrimSpace(userID)
	if projectID == "" {
		return ErrProjectNotFound
	}
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return err
	}
	if project == nil {
		return ErrProjectNotFound
	}
	member, err := s.memberRepo.FindByProjectAndUser(projectID, userID)
	if err != nil {
		return err
	}
	if member == nil {
		return ErrAssistantPermissionDenied
	}
	return nil
}

func (s *AssistantService) loadConversationContext(ctx context.Context, conversation *model.AIConversation) (interface{}, error) {
	if conversation == nil {
		return nil, ErrAssistantConversationNotFound
	}
	switch conversation.Type {
	case AssistantConversationTypeFaultTree:
		graph, _, err := s.ftService.GetGraph(ctx, conversation.ProjectID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"nodes": graph.Nodes,
			"edges": graph.Edges,
		}, nil
	case AssistantConversationTypeKnowledgeGraph:
		graph, _, err := s.kgService.GetGraph(ctx, conversation.ProjectID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"rfNodes": graph.Nodes,
			"rfEdges": graph.Edges,
		}, nil
	default:
		return nil, ErrAssistantInvalidType
	}
}

func isValidAssistantType(t string) bool {
	v := strings.TrimSpace(t)
	return v == AssistantConversationTypeFaultTree || v == AssistantConversationTypeKnowledgeGraph
}

func toProjectType(conversationType string) string {
	if strings.TrimSpace(conversationType) == AssistantConversationTypeKnowledgeGraph {
		return constant.ProjectTypeKG
	}
	return constant.ProjectTypeFT
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

func toChatHistory(messages []model.AIChatMessage) []ai.ChatHistoryMessage {
	if len(messages) == 0 {
		return []ai.ChatHistoryMessage{}
	}

	history := make([]ai.ChatHistoryMessage, 0, len(messages))
	for _, m := range messages {
		role := strings.TrimSpace(strings.ToLower(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		history = append(history, ai.ChatHistoryMessage{Role: role, Content: content})
	}
	return history
}

func encodeMessageCursor(createdAt time.Time, id string) string {
	payload := fmt.Sprintf("%d|%s", createdAt.UTC().UnixNano(), strings.TrimSpace(id))
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeMessageCursor(cursor string) (*repository.MessageCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return nil, ErrAssistantInvalidCursor
	}
	ns, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return nil, err
	}
	return &repository.MessageCursor{
		CreatedAt: time.Unix(0, ns).UTC(),
		ID:        strings.TrimSpace(parts[1]),
	}, nil
}
