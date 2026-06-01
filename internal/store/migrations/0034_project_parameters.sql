-- 0034 project_parameters:项目级「类型化运行参数」定义(P0 · 对标 Jenkins parameters / 云效 variables type)。
-- 每项目至多一行(project_id 既是主键也是外键)。defs_json 存一份参数定义数组:
--   [{ key, label, type(string|choice|boolean|number), default, options[], required }]
-- 手动触发弹窗据此渲染类型化控件并校验;运行执行仍以 key=value(map[string]string)注入,
-- 故仅是「触发期的定义 + 校验层」,不改执行语义。无定义 → 触发回退自由 KV(向后兼容)。
-- 不存敏感信息(secret 仍走 vault 引用,绝不作明文参数);删项目级联删参数定义。
--   project_id : FK → projects.id;PK;ON DELETE CASCADE
--   defs_json  : 参数定义数组 JSON(空数组 = 未配置 = 自由 KV)

CREATE TABLE IF NOT EXISTS project_parameters (
    project_id TEXT PRIMARY KEY,
    defs_json  TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
