package handler

import (
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type UserHandler struct {
	userService *service.UserService
}

func NewUserHandler(userService *service.UserService) *UserHandler {
	return &UserHandler{userService: userService}
}

func (h *UserHandler) GetMe(c *gin.Context) {
	userID := middleware.GetUserID(c)
	user, err := h.userService.GetMe(c.Request.Context(), userID)
	if err != nil {
		util.FailNotFound(c)
		return
	}
	util.Success(c, gin.H{"user": user})
}

type updateProfileRequest struct {
	DisplayName string `json:"displayName" binding:"omitempty,max=50"`
	Email       string `json:"email" binding:"omitempty,email"`
}

func (h *UserHandler) UpdateProfile(c *gin.Context) {
	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	userID := middleware.GetUserID(c)
	user, err := h.userService.UpdateProfile(c.Request.Context(), userID, service.UpdateProfileInput{
		DisplayName: req.DisplayName,
		Email:       req.Email,
	})
	if err != nil {
		switch err {
		case service.ErrEmailTaken:
			util.Fail(c, constant.CodeConflict, "邮箱已被使用")
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"user": user})
}

func (h *UserHandler) UploadAvatar(c *gin.Context) {
	fh, err := c.FormFile("avatar")
	if err != nil {
		util.Fail(c, constant.CodeInvalidParam, "请上传头像文件")
		return
	}
	userID := middleware.GetUserID(c)
	f, err := fh.Open()
	if err != nil {
		util.FailServerError(c)
		return
	}
	defer f.Close()
	avatarURL, err := h.userService.UploadAvatar(c.Request.Context(), userID, f, fh)
	if err != nil {
		switch err {
		case service.ErrFileTypeForbidden:
			util.Fail(c, constant.CodeFileType, constant.MsgFileType)
		case service.ErrFileTooLarge:
			util.Fail(c, constant.CodeFileTooLarge, constant.MsgFileTooLarge)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"avatarUrl": avatarURL})
}

func (h *UserHandler) GetLoginLogs(c *gin.Context) {
	page, pageSize := util.GetPagination(c)
	userID := middleware.GetUserID(c)
	logs, total, err := h.userService.GetLoginLogs(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.PageSuccess(c, logs, total, page, pageSize)
}
