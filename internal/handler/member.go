package handler

import (
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/model"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type MemberHandler struct {
	memberService *service.MemberService
}

func NewMemberHandler(memberService *service.MemberService) *MemberHandler {
	return &MemberHandler{memberService: memberService}
}

func (h *MemberHandler) ListMembers(c *gin.Context) {
	projectID := c.Param("projectId")
	members, err := h.memberService.ListMembers(c.Request.Context(), projectID)
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.Success(c, gin.H{"members": members})
}

type inviteMemberRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required,oneof=viewer editor admin"`
}

type invitationResponse struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	InvitedBy string    `json:"invitedBy"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toInvitationResponse(inv model.Invitation) invitationResponse {
	return invitationResponse{
		ID:        inv.ID,
		ProjectID: inv.ProjectID,
		Email:     inv.Email,
		Role:      inv.Role,
		Status:    inv.Status,
		InvitedBy: inv.InvitedBy,
		ExpiresAt: inv.ExpiresAt,
		CreatedAt: inv.CreatedAt,
		UpdatedAt: inv.UpdatedAt,
	}
}

func (h *MemberHandler) InviteMember(c *gin.Context) {
	var req inviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	projectID := c.Param("projectId")
	userID := middleware.GetUserID(c)

	invitation, err := h.memberService.InviteMember(c.Request.Context(), service.InviteMemberInput{
		ProjectID: projectID,
		Email:     req.Email,
		Role:      req.Role,
		InvitedBy: userID,
	})
	if err != nil {
		switch err {
		case service.ErrAlreadyMember:
			util.Fail(c, constant.CodeConflict, "该用户已是项目成员")
		case service.ErrInvalidRole:
			util.Fail(c, constant.CodeInvalidParam, "无效的角色")
		case service.ErrInviteEmailRequired:
			util.Fail(c, constant.CodeInvalidParam, "邀请邮箱不能为空")
		case service.ErrMemberProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"invitation": toInvitationResponse(*invitation)})
}

func (h *MemberHandler) ListInvitations(c *gin.Context) {
	projectID := c.Param("projectId")
	status := c.DefaultQuery("status", "all")
	page, pageSize := util.GetPagination(c)

	invitations, total, err := h.memberService.ListInvitations(c.Request.Context(), projectID, status, page, pageSize)
	if err != nil {
		switch err {
		case service.ErrMemberProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}

	list := make([]invitationResponse, len(invitations))
	for i, inv := range invitations {
		list[i] = toInvitationResponse(inv)
	}
	util.PageSuccess(c, list, total, page, pageSize)
}

func (h *MemberHandler) ListMyInvitations(c *gin.Context) {
	userID := middleware.GetUserID(c)
	status := c.DefaultQuery("status", "all")
	page, pageSize := util.GetPagination(c)

	invitations, total, err := h.memberService.ListMyInvitations(c.Request.Context(), userID, status, page, pageSize)
	if err != nil {
		switch err {
		case service.ErrUserNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}

	list := make([]invitationResponse, len(invitations))
	for i, inv := range invitations {
		list[i] = toInvitationResponse(inv)
	}
	util.PageSuccess(c, list, total, page, pageSize)
}

func (h *MemberHandler) QueryInviteCandidate(c *gin.Context) {
	projectID := c.Param("projectId")
	email := c.Query("email")
	if email == "" {
		util.Fail(c, constant.CodeInvalidParam, "缺少 email 参数")
		return
	}

	res, err := h.memberService.GetInviteCandidate(c.Request.Context(), projectID, email)
	if err != nil {
		switch err {
		case service.ErrInviteEmailRequired:
			util.Fail(c, constant.CodeInvalidParam, "邀请邮箱不能为空")
		case service.ErrMemberProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"candidate": res})
}

func (h *MemberHandler) AcceptInvitation(c *gin.Context) {
	projectID := c.Param("projectId")
	invitationID := c.Param("invitationId")
	userID := middleware.GetUserID(c)

	member, err := h.memberService.AcceptInvitation(c.Request.Context(), projectID, invitationID, userID)
	if err != nil {
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
		return
	}

	util.Success(c, gin.H{"member": member})
}

func (h *MemberHandler) RejectInvitation(c *gin.Context) {
	projectID := c.Param("projectId")
	invitationID := c.Param("invitationId")
	userID := middleware.GetUserID(c)

	err := h.memberService.RejectInvitation(c.Request.Context(), projectID, invitationID, userID)
	if err != nil {
		switch err {
		case service.ErrMemberProjectNotFound, service.ErrInvitationNotFound:
			util.FailNotFound(c)
		case service.ErrInvitationExpired:
			util.Fail(c, constant.CodeBizError, "邀请已过期")
		case service.ErrInvitationEmailMismatch:
			util.Fail(c, constant.CodeForbidden, "当前登录邮箱与邀请邮箱不匹配")
		case service.ErrInvitationProcessed:
			util.Fail(c, constant.CodeConflict, "邀请已处理")
		default:
			util.FailServerError(c)
		}
		return
	}

	util.SuccessNoData(c)
}

func (h *MemberHandler) RevokeInvitation(c *gin.Context) {
	projectID := c.Param("projectId")
	invitationID := c.Param("invitationId")
	operatorID := middleware.GetUserID(c)

	err := h.memberService.RevokeInvitation(c.Request.Context(), projectID, invitationID, operatorID)
	if err != nil {
		switch err {
		case service.ErrMemberProjectNotFound, service.ErrInvitationNotFound:
			util.FailNotFound(c)
		case service.ErrInvitationExpired:
			util.Fail(c, constant.CodeBizError, "邀请已过期，无需撤销")
		case service.ErrInvitationProcessed:
			util.Fail(c, constant.CodeConflict, "邀请已处理，无法撤销")
		default:
			util.FailServerError(c)
		}
		return
	}

	util.SuccessNoData(c)
}

type updateMemberRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=viewer editor admin"`
}

func (h *MemberHandler) UpdateRole(c *gin.Context) {
	var req updateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	projectID := c.Param("projectId")
	memberID := c.Param("memberId")
	operatorID := middleware.GetUserID(c)

	member, err := h.memberService.UpdateRole(c.Request.Context(), projectID, memberID, req.Role, operatorID)
	if err != nil {
		switch err {
		case service.ErrMemberNotFound:
			util.FailNotFound(c)
		case service.ErrCannotRemoveLastAdmin:
			util.Fail(c, constant.CodeBizError, "不能修改最后一个管理员的角色")
		case service.ErrInvalidRole:
			util.Fail(c, constant.CodeInvalidParam, "无效的角色")
		case service.ErrMemberProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"member": member})
}

func (h *MemberHandler) RemoveMember(c *gin.Context) {
	projectID := c.Param("projectId")
	memberID := c.Param("memberId")
	operatorID := middleware.GetUserID(c)

	if err := h.memberService.RemoveMember(c.Request.Context(), projectID, memberID, operatorID); err != nil {
		switch err {
		case service.ErrMemberNotFound:
			util.FailNotFound(c)
		case service.ErrCannotRemoveLastAdmin:
			util.Fail(c, constant.CodeBizError, "不能移除最后一个管理员")
		case service.ErrMemberProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.SuccessNoData(c)
}
