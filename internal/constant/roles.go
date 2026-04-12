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
)

// AI 任务状态
const (
	AITaskStatusPending    = "pending"
	AITaskStatusProcessing = "processing"
	AITaskStatusRetrying   = "retrying"
	AITaskStatusCompleted  = "completed"
	AITaskStatusFailed     = "failed"
	AITaskStatusDead       = "dead"
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

// 通知类型
const (
	NotificationTypeProjectInvite     = "project_invite"
	NotificationTypeMemberRoleChanged = "member_role_changed"
	NotificationTypeVersionCreated    = "version_created"
	NotificationTypeAITaskCompleted   = "ai_task_completed"
	NotificationTypeAITaskFailed      = "ai_task_failed"
	NotificationTypeSystemAnnounce    = "system_announce"
)

// 审计动作
const (
	AuditActionMemberInvite         = "member.invite"
	AuditActionMemberInviteAccepted = "member.invite.accept"
	AuditActionMemberInviteRejected = "member.invite.reject"
	AuditActionMemberInviteRevoke   = "member.invite.revoke"
	AuditActionMemberRoleUpdate     = "member.role.update"
	AuditActionMemberRemove         = "member.remove"
)

// 成员状态
const (
	MemberStatusActive   = "active"
	MemberStatusInactive = "inactive"
)

// Redis key 前缀
const (
	RedisKeyAccessToken                 = "token:"
	RedisKeyRefreshToken                = "rt:"
	RedisKeyBlacklist                   = "blacklist:"
	RedisKeyProjectDetail               = "projects:detail:"
	RedisKeyGraphFT                     = "graph:ft:"
	RedisKeyGraphKG                     = "graph:kg:"
	RedisKeyProjectList                 = "projects:list:"
	RedisKeyProjectListIx               = "projects:list:index:user:"
	RedisKeyVersionList                 = "versions:list:"
	RedisKeyVersionListIx               = "versions:list:index:project:"
	RedisKeyAITask                      = "ai:task:"
	RedisKeyAITaskDedupe                = "ai:task:callback:dedupe:"
	RedisKeyAITaskLock                  = "ai:task:project:lock:"
	RedisKeyAITaskLatest                = "ai:task:project:latest:"
	RedisKeyAITaskWorkerStreamEntries   = "ai:task:stream:entries:worker:"
	RedisKeyAITaskProducerStreamEntries = "ai:task:stream:entries:producer:"
	RedisKeyResetPassword               = "reset:"
	RedisKeyUserInfo                    = "user:info:"
)
