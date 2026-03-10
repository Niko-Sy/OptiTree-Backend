package constant

// 项目角色
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// 角色权重（数值越大权限越高）
var RoleWeight = map[string]int{
	RoleViewer: 0,
	RoleEditor: 1,
	RoleAdmin:  2,
}

// 系统角色
const (
	SystemRoleUser  = "user"
	SystemRoleAdmin = "admin"
)

// 项目类型
const (
	ProjectTypeFT = "ft" // 故障树
	ProjectTypeKG = "kg" // 知识图谱
)

// 项目 AI 生成状态
const (
	ProjectGenerationPending   = "pending_generating"
	ProjectGenerationRunning   = "generating"
	ProjectGenerationCompleted = "completed"
	ProjectGenerationFailed    = "failed"
)

// 版本快照最大保留数
const MaxVersionCount = 30

// 文档状态
const (
	DocStatusPending = "pending"
	DocStatusParsing = "parsing"
	DocStatusParsed  = "parsed"
	DocStatusFailed  = "failed"
)

// AI 任务类型
const (
	AITaskTypeGenerateFaultTree      = "generateFaultTree"
	AITaskTypeGenerateKnowledgeGraph = "generateKnowledgeGraph"
	AITaskTypeParseDocument          = "parseDocument"
	AITaskTypeValidateGraph          = "validateGraph"
)

// AI 任务状态
const (
	AITaskStatusPending    = "pending"
	AITaskStatusParsing    = "parsing"
	AITaskStatusGenerating = "generating"
	AITaskStatusCompleted  = "completed"
	AITaskStatusFailed     = "failed"
	AITaskStatusCancelled  = "cancelled"
)

// 用户状态
const (
	UserStatusActive   = "active"
	UserStatusInactive = "inactive"
	UserStatusBanned   = "banned"
)

// 邀请状态
const (
	InviteStatusPending  = "pending"
	InviteStatusAccepted = "accepted"
	InviteStatusRejected = "rejected"
	InviteStatusExpired  = "expired"
)

// 成员状态
const (
	MemberStatusActive   = "active"
	MemberStatusInactive = "inactive"
)

// Redis key 前缀
const (
	RedisKeyAccessToken   = "token:"
	RedisKeyRefreshToken  = "rt:"
	RedisKeyBlacklist     = "blacklist:"
	RedisKeyGraphFT       = "graph:ft:"
	RedisKeyGraphKG       = "graph:kg:"
	RedisKeyAITask        = "ai:task:"
	RedisKeyResetPassword = "reset:"
	RedisKeyUserInfo      = "user:info:"
)
