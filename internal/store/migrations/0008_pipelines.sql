-- 0008 pipeline_configs:项目流水线编排声明式配置(UX-DR5 / 部分 FR-9 / Story 2.2)。
-- 每项目一行(project_id 既是主键也是外键),记录声明式 spec 的结构化 JSON 与
-- 服务端渲染的只读 YAML 文本(供"查看源码"/导出)、发布状态。本期 status 恒 'draft'
-- (active/发布 = 后续);spec 不含任何明文 secret(凭据仍走保险库引用,2-4 落引用字段)。
--   project_id : FK → projects.id;PK;ON DELETE CASCADE(删项目级联删流水线配置)
--   spec_json  : 结构化 spec JSON,形如 {"stages":[{id,name,kind,jobs:[{id,name,type,summary,config}]}]}
--   spec_yaml  : 按 spec 渲染的只读 YAML 文本(展示/导出用;由服务端从 spec_json 派生)
--   status     : draft(本期恒此值;active/发布为后续 story)

CREATE TABLE IF NOT EXISTS pipeline_configs (
    project_id TEXT PRIMARY KEY,
    spec_json  TEXT NOT NULL DEFAULT '{}',
    spec_yaml  TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'draft',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
