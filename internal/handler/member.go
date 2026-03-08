package handler

import (
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
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
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"invitation": invitation})
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

	member, err := h.memberService.UpdateRole(c.Request.Context(), projectID, memberID, req.Role)
	if err != nil {
		switch err {
		case service.ErrMemberNotFound:
			util.FailNotFound(c)
		case service.ErrCannotRemoveLastAdmin:
			util.Fail(c, constant.CodeBizError, "不能修改最后一个管理员的角色")
		case service.ErrInvalidRole:
			util.Fail(c, constant.CodeInvalidParam, "无效的角色")
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

	if err := h.memberService.RemoveMember(c.Request.Context(), projectID, memberID); err != nil {
		switch err {
		case service.ErrMemberNotFound:
			util.FailNotFound(c)
		case service.ErrCannotRemoveLastAdmin:
			util.Fail(c, constant.CodeBizError, "不能移除最后一个管理员")
		default:
			util.FailServerError(c)
		}
		return
	}
	util.SuccessNoData(c)
}
