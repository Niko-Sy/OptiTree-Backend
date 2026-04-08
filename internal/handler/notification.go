package handler

import (
	"strconv"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type NotificationHandler struct {
	notificationService *service.NotificationService
	memberService       *service.MemberService
}

func NewNotificationHandler(notificationService *service.NotificationService, memberService *service.MemberService) *NotificationHandler {
	return &NotificationHandler{notificationService: notificationService, memberService: memberService}
}

type notificationActionRequest struct {
	Action string `json:"action" binding:"required,oneof=accept reject"`
}

func (h *NotificationHandler) List(c *gin.Context) {
	userID := middleware.GetUserID(c)
	page, pageSize := util.GetPagination(c)

	var isRead *bool
	if v := c.Query("isRead"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			util.Fail(c, constant.CodeInvalidParam, "isRead 参数必须是 true/false")
			return
		}
		isRead = &parsed
	}

	list, total, err := h.notificationService.List(c.Request.Context(), service.ListNotificationsParams{
		UserID:   userID,
		IsRead:   isRead,
		Type:     c.Query("type"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		util.FailServerError(c)
		return
	}

	util.PageSuccess(c, list, total, page, pageSize)
}

func (h *NotificationHandler) UnreadCount(c *gin.Context) {
	userID := middleware.GetUserID(c)
	count, err := h.notificationService.CountUnread(c.Request.Context(), userID)
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.Success(c, gin.H{"unreadCount": count})
}

func (h *NotificationHandler) MarkRead(c *gin.Context) {
	userID := middleware.GetUserID(c)
	notificationID := c.Param("notificationId")
	if err := h.notificationService.MarkRead(c.Request.Context(), userID, notificationID); err != nil {
		switch err {
		case service.ErrNotificationNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.SuccessNoData(c)
}

func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if err := h.notificationService.MarkAllRead(c.Request.Context(), userID); err != nil {
		util.FailServerError(c)
		return
	}
	util.SuccessNoData(c)
}

func (h *NotificationHandler) Action(c *gin.Context) {
	userID := middleware.GetUserID(c)
	notificationID := c.Param("notificationId")

	var req notificationActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	notification, err := h.notificationService.GetByID(c.Request.Context(), userID, notificationID)
	if err != nil {
		switch err {
		case service.ErrNotificationNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}

	if notification.Type != constant.NotificationTypeProjectInvite {
		util.Fail(c, constant.CodeInvalidParam, "该通知不支持操作")
		return
	}
	if notification.ProjectID == nil || notification.ResourceID == nil {
		util.Fail(c, constant.CodeInvalidParam, "通知缺少邀请信息")
		return
	}

	switch req.Action {
	case "accept":
		member, actionErr := h.memberService.AcceptInvitation(c.Request.Context(), *notification.ProjectID, *notification.ResourceID, userID)
		if actionErr != nil {
			h.handleInviteActionError(c, actionErr)
			return
		}
		_ = h.notificationService.MarkRead(c.Request.Context(), userID, notificationID)
		util.Success(c, gin.H{"member": member})
	case "reject":
		actionErr := h.memberService.RejectInvitation(c.Request.Context(), *notification.ProjectID, *notification.ResourceID, userID)
		if actionErr != nil {
			h.handleInviteActionError(c, actionErr)
			return
		}
		_ = h.notificationService.MarkRead(c.Request.Context(), userID, notificationID)
		util.SuccessNoData(c)
	default:
		util.Fail(c, constant.CodeInvalidParam, "无效的通知操作")
	}
}

func (h *NotificationHandler) handleInviteActionError(c *gin.Context, err error) {
	switch err {
	case service.ErrMemberProjectNotFound, service.ErrInvitationNotFound:
		util.FailNotFound(c)
	case service.ErrInvitationExpired:
		util.Fail(c, constant.CodeBizError, "邀请已过期")
	case service.ErrInvitationEmailMismatch:
		util.Fail(c, constant.CodeForbidden, "当前登录邮箱与邀请邮箱不匹配")
	case service.ErrInvitationProcessed:
		util.Fail(c, constant.CodeConflict, "邀请已处理")
	case service.ErrAlreadyMember:
		util.Fail(c, constant.CodeConflict, "你已经是该项目成员")
	default:
		util.FailServerError(c)
	}
}
