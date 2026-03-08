package handler

import (
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	authService *service.AuthService
}

func NewAuthHandler(authService *service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

type registerRequest struct {
	Username string `json:"username" binding:"required,min=3,max=20"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	Agree    bool   `json:"agree" binding:"required"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	if !req.Agree {
		util.Fail(c, constant.CodeInvalidParam, "请阅读并同意用户协议")
		return
	}

	user, err := h.authService.Register(c.Request.Context(), service.RegisterInput{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		switch err {
		case service.ErrUsernameTaken:
			util.Fail(c, constant.CodeConflict, "用户名已被占用")
		case service.ErrEmailTaken:
			util.Fail(c, constant.CodeConflict, "邮箱已被注册")
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"user": user})
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Remember bool   `json:"remember"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	result, err := h.authService.Login(
		c.Request.Context(),
		req.Username, req.Password, req.Remember,
		c.ClientIP(), c.GetHeader("User-Agent"),
	)
	if err != nil {
		switch err {
		case service.ErrWrongPassword, service.ErrUserNotFound:
			util.Fail(c, constant.CodeUnauthorized, "用户名或密码错误")
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{
		"user":         result.User,
		"accessToken":  result.AccessToken,
		"refreshToken": result.RefreshToken,
		"tokenType":    result.TokenType,
		"expiresIn":    result.ExpiresIn,
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	result, err := h.authService.RefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		util.Fail(c, constant.CodeUnauthorized, constant.MsgUnauthorized)
		return
	}
	util.Success(c, gin.H{
		"accessToken":  result.AccessToken,
		"refreshToken": result.RefreshToken,
		"tokenType":    result.TokenType,
		"expiresIn":    result.ExpiresIn,
	})
}

type logoutRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	_ = c.ShouldBindJSON(&req)

	jti, _ := c.Get(middleware.ContextKeyJTI)
	jtiStr, _ := jti.(string)

	_ = h.authService.Logout(c.Request.Context(), jtiStr, req.RefreshToken)
	util.SuccessNoData(c)
}

type changePasswordRequest struct {
	OldPassword string `json:"oldPassword" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required,min=8"`
}

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	userID := middleware.GetUserID(c)
	if err := h.authService.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword); err != nil {
		switch err {
		case service.ErrWrongPassword:
			util.Fail(c, constant.CodeUnauthorized, "原密码错误")
		case service.ErrUserNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.SuccessNoData(c)
}

type forgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req forgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	// P0: 生成 token，输出到日志而不实际发邮件
	token, err := h.authService.ForgotPassword(c.Request.Context(), req.Email)
	if err != nil {
		util.FailServerError(c)
		return
	}
	// 不泄露用户是否存在
	_ = token
	util.Success(c, gin.H{"message": "如果该邮箱已注册，您将收到重置邮件"})
}

type resetPasswordRequest struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required,min=8"`
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	if err := h.authService.ResetPassword(c.Request.Context(), req.Token, req.NewPassword); err != nil {
		util.Fail(c, constant.CodeUnauthorized, "重置链接已失效或不存在")
		return
	}
	util.SuccessNoData(c)
}
