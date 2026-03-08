package constant

// 错误码
const (
	CodeSuccess         = 0
	CodeBizError        = 40000
	CodeInvalidParam    = 40001
	CodeFileType        = 40003
	CodeFileTooLarge    = 40004
	CodeConflict        = 40009
	CodeUnauthorized    = 40100
	CodeTokenExpired    = 40101
	CodeForbidden       = 40300
	CodeNotFound        = 40400
	CodeVersionConflict = 40900
	CodeAIFailed        = 42200
	CodeRateLimited     = 42900
	CodeServerError     = 50000
	CodeAIUnavailable   = 50300
)

// 错误消息
const (
	MsgSuccess         = "ok"
	MsgBizError        = "业务错误"
	MsgInvalidParam    = "参数校验失败"
	MsgFileType        = "文件格式不支持"
	MsgFileTooLarge    = "文件过大"
	MsgConflict        = "资源状态冲突"
	MsgUnauthorized    = "未登录或token无效"
	MsgTokenExpired    = "token已过期"
	MsgForbidden       = "无权限"
	MsgNotFound        = "资源不存在"
	MsgVersionConflict = "数据版本冲突，请刷新后重试"
	MsgAIFailed        = "AI解析失败"
	MsgRateLimited     = "请求频率过高"
	MsgServerError     = "服务器内部错误"
	MsgAIUnavailable   = "AI服务暂不可用"
)
