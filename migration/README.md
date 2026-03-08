# OptiTree 数据库迁移说明

## 迁移工具

本项目使用 [golang-migrate/migrate](https://github.com/golang-migrate/migrate) 管理数据库迁移，文件命名遵循 `{version}_{name}.{up|down}.sql` 格式。

## 迁移文件清单

| 版本 | 文件名 | 内容 |
|------|--------|------|
| 000001 | `create_users` | 用户主表 |
| 000002 | `create_projects` | 项目主表（ft/kg 共用） |
| 000003 | `create_documents` | 文档上传记录表 |
| 000004 | `create_fault_tree_tables` | 故障树节点表 + 边表 |
| 000005 | `create_knowledge_graph_tables` | 知识图谱节点表 + 边表 |
| 000006 | `create_version_snapshots` | 版本快照表 |
| 000007 | `create_project_members` | 项目协作成员表 |
| 000008 | `create_invitations` | 邀请表 |
| 000009 | `create_ai_tables` | AI 任务 / 对话会话 / 消息表 |
| 000010 | `create_auth_support_tables` | Refresh Token / 登录记录 / 社交绑定表 |
| 000011 | `create_notification_audit_tables` | 通知表 + 审计日志表 |
| 000012 | `create_indexes` | 全局非唯一查询索引 |

## 实体关系总览

```
users
 ├── projects (created_by)
 │    ├── fault_tree_nodes    (project_id, CASCADE)
 │    ├── fault_tree_edges    (project_id, CASCADE)
 │    ├── knowledge_graph_nodes (project_id, CASCADE)
 │    ├── knowledge_graph_edges (project_id, CASCADE)
 │    ├── version_snapshots   (project_id, CASCADE)
 │    ├── project_members     (project_id, CASCADE)
 │    ├── invitations         (project_id, CASCADE)
 │    ├── documents           (project_id, SET NULL)
 │    ├── ai_tasks            (project_id, CASCADE)
 │    ├── ai_conversations    (project_id, CASCADE)
 │    │    └── ai_chat_messages (conversation_id, CASCADE)
 │    ├── notifications       (project_id, 无 FK)
 │    └── audit_logs          (project_id, 无 FK)
 ├── refresh_tokens    (user_id, CASCADE)
 ├── login_logs        (user_id, CASCADE)
 └── user_social_bindings (user_id, CASCADE)
```

## 关键设计决策

### 图数据存储策略
- **实时编辑数据**：`fault_tree_nodes`/`fault_tree_edges` 和 `knowledge_graph_nodes`/`knowledge_graph_edges` 以行记录存储，每次保存为「整体批量替换」
- **历史版本数据**：`version_snapshots.snapshot_json` 存储全量 JSONB 快照，ft 用 `{nodes, edges}`，kg 用 `{rfNodes, rfEdges}`
- **复合主键**：图节点/边表使用 `(id, project_id)` 复合主键，因为前端节点 ID（如 `root`、`gate1`）仅在项目内唯一

### 乐观锁设计
- `projects.graph_revision` 作为整张图的版本号
- 保存图时客户端需传入当前 `revision`，后端 CAS 更新，失败返回 `40900` 版本冲突

### 循环依赖规避
- `projects.latest_version_id` → `version_snapshots` **不加 FOREIGN KEY**，由应用层维护，避免与 `version_snapshots.project_id → projects` 的反向 FK 形成循环

### 审计要求
- `audit_logs.operator_name` 以快照方式冗余存储操作人名称，防止用户删除后历史日志丢失可读性

## 安装 migrate CLI

```bash
# macOS
brew install golang-migrate

# Linux (amd64)
curl -L https://github.com/golang-migrate/migrate/releases/download/v4.17.0/migrate.linux-amd64.tar.gz | tar xvz

# Windows (scoop)
scoop install migrate
```

## 执行迁移命令

```bash
# 配置数据库 URL（示例）
export DB_URL="postgres://optitree:password@localhost:5432/optitree?sslmode=disable"

# 正向迁移（应用所有未执行的迁移）
migrate -path ./migration -database "${DB_URL}" up

# 回滚最近一步
migrate -path ./migration -database "${DB_URL}" down 1

# 回滚所有
migrate -path ./migration -database "${DB_URL}" down

# 查看当前版本
migrate -path ./migration -database "${DB_URL}" version

# 跳转到指定版本
migrate -path ./migration -database "${DB_URL}" goto 6
```

## Go 代码中集成迁移

```go
import (
    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/postgres"
    _ "github.com/golang-migrate/migrate/v4/source/file"
)

func RunMigrations(dsn string) error {
    m, err := migrate.New("file://migration", dsn)
    if err != nil {
        return err
    }
    if err := m.Up(); err != nil && err != migrate.ErrNoChange {
        return err
    }
    return nil
}
```
