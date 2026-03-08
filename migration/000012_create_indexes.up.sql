-- ============================================================
-- Migration: 000012_create_indexes
-- Description: 全局索引策略
--   - 主键索引已由各表 PRIMARY KEY 自动创建，此处不重复
--   - 唯一索引亦已在建表时通过 UNIQUE 约束创建，此处不重复
--   - 本文件仅创建「非唯一」查询加速索引
-- ============================================================


-- ─── users 表 ────────────────────────────────────────────────
-- 按 status 筛选活跃用户（运营查询）
CREATE INDEX IF NOT EXISTS idx_users_status
    ON users (status);

-- 按 created_at 排序（管理后台）
CREATE INDEX IF NOT EXISTS idx_users_created_at
    ON users (created_at DESC);


-- ─── projects 表 ─────────────────────────────────────────────
-- 用户仪表盘：按创建者 + 最近更新时间查询
CREATE INDEX IF NOT EXISTS idx_projects_created_by_updated_at
    ON projects (created_by, updated_at DESC);

-- 按类型筛选
CREATE INDEX IF NOT EXISTS idx_projects_type
    ON projects (type);

-- 全文搜索：项目名称（支持 keyword 搜索）
CREATE INDEX IF NOT EXISTS idx_projects_name
    ON projects USING gin (to_tsvector('simple', name));


-- ─── documents 表 ────────────────────────────────────────────
-- 按上传者查询
CREATE INDEX IF NOT EXISTS idx_documents_uploaded_by
    ON documents (uploaded_by);

-- 按项目查询
CREATE INDEX IF NOT EXISTS idx_documents_project_id
    ON documents (project_id);

-- 按解析状态查询（轮询待处理队列）
CREATE INDEX IF NOT EXISTS idx_documents_status
    ON documents (status);


-- ─── fault_tree_nodes 表 ─────────────────────────────────────
-- 读取指定项目所有节点（最核心查询）
CREATE INDEX IF NOT EXISTS idx_ftn_project_id
    ON fault_tree_nodes (project_id);


-- ─── fault_tree_edges 表 ─────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_fte_project_id
    ON fault_tree_edges (project_id);


-- ─── knowledge_graph_nodes 表 ────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_kgn_project_id
    ON knowledge_graph_nodes (project_id);

-- 按实体类型筛选
CREATE INDEX IF NOT EXISTS idx_kgn_entity_type
    ON knowledge_graph_nodes (project_id, entity_type);


-- ─── knowledge_graph_edges 表 ────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_kge_project_id
    ON knowledge_graph_edges (project_id);


-- ─── version_snapshots 表 ────────────────────────────────────
-- 版本列表：按项目 + 创建时间倒序（最常用查询，前端只取最近30条）
CREATE INDEX IF NOT EXISTS idx_vs_project_created_at
    ON version_snapshots (project_id, created_at DESC);

-- 按创建者查询（团队动态时间线）
CREATE INDEX IF NOT EXISTS idx_vs_created_by
    ON version_snapshots (created_by, created_at DESC);


-- ─── project_members 表 ──────────────────────────────────────
-- 按用户查询其加入的所有项目
CREATE INDEX IF NOT EXISTS idx_pm_user_id
    ON project_members (user_id);

-- 按项目查询成员列表
CREATE INDEX IF NOT EXISTS idx_pm_project_id
    ON project_members (project_id);


-- ─── invitations 表 ──────────────────────────────────────────
-- 按邮箱查询待处理邀请（注册流程中自动关联）
CREATE INDEX IF NOT EXISTS idx_inv_email_status
    ON invitations (email, status);

-- 按项目查询邀请列表
CREATE INDEX IF NOT EXISTS idx_inv_project_status
    ON invitations (project_id, status);


-- ─── ai_tasks 表 ─────────────────────────────────────────────
-- 按创建者 + 状态查询（用户任务列表）
CREATE INDEX IF NOT EXISTS idx_ait_created_by_status
    ON ai_tasks (created_by, status);

-- 按项目查询关联任务
CREATE INDEX IF NOT EXISTS idx_ait_project_id
    ON ai_tasks (project_id);

-- 按状态查询待处理任务（后台 Worker 轮询）
CREATE INDEX IF NOT EXISTS idx_ait_status_created_at
    ON ai_tasks (status, created_at);


-- ─── ai_conversations 表 ─────────────────────────────────────
-- 按项目 + 用户查询对话
CREATE INDEX IF NOT EXISTS idx_aconv_project_user
    ON ai_conversations (project_id, user_id);


-- ─── ai_chat_messages 表 ─────────────────────────────────────
-- 按会话查询消息（时序）
CREATE INDEX IF NOT EXISTS idx_acm_conversation_created_at
    ON ai_chat_messages (conversation_id, created_at);


-- ─── refresh_tokens 表 ───────────────────────────────────────
-- 按用户查询其所有 Token（多设备管理/强制下线）
CREATE INDEX IF NOT EXISTS idx_rt_user_id
    ON refresh_tokens (user_id);

-- 清理过期/已撤销 Token
CREATE INDEX IF NOT EXISTS idx_rt_expires_revoked
    ON refresh_tokens (expires_at, is_revoked);


-- ─── login_logs 表 ───────────────────────────────────────────
-- 按用户查询登录记录（时序）
CREATE INDEX IF NOT EXISTS idx_ll_user_created_at
    ON login_logs (user_id, created_at DESC);


-- ─── notifications 表 ────────────────────────────────────────
-- 用户未读消息查询（最常用）
CREATE INDEX IF NOT EXISTS idx_notif_user_read
    ON notifications (user_id, is_read, created_at DESC);


-- ─── audit_logs 表 ───────────────────────────────────────────
-- 按用户查询操作记录
CREATE INDEX IF NOT EXISTS idx_al_user_id
    ON audit_logs (user_id, created_at DESC);

-- 按项目查询操作记录
CREATE INDEX IF NOT EXISTS idx_al_project_id
    ON audit_logs (project_id, created_at DESC);

-- 按操作类型筛选
CREATE INDEX IF NOT EXISTS idx_al_action
    ON audit_logs (action);
