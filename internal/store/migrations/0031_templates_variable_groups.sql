-- 0031 流水线模板 + 变量组(FR-8-13 复用基座 · 对标 Jenkins Shared Library / 云效模板与变量组)。
--
-- pipeline_templates:命名、可复用的流水线定义(与项目流水线同一套 stages 模型)。
-- 一个模板存一份 spec(stages_json,与 pipeline_configs.spec_json 同形状),项目可「应用模板」
-- 把模板的 stages 拷进自己的流水线(经 pipeline.Service.Save 同一套规范化/校验/渲染落库)。
-- 模板与项目解耦(不挂 project_id 外键):跨项目共享。绝不存任何明文 secret(spec 不含 secret)。
--   id          : 模板主键(uuid)
--   name        : 模板名(去空白后非空;库内唯一,便于挑选)
--   description : 模板说明(可空)
--   stages_json : 流水线 spec 的阶段集合 JSON(与 pipeline spec 同形状)
CREATE TABLE IF NOT EXISTS pipeline_templates (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    stages_json TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- 名称唯一(挑选模板时人读、避免重名歧义)。
CREATE UNIQUE INDEX IF NOT EXISTS idx_pipeline_templates_name ON pipeline_templates (name);

-- variable_groups:命名、可复用的变量集合(key=value + secret 引用),镜像每流水线变量模型。
-- 一个变量组存一份 vars_json(与 pipeline_settings 的 BuildVar 同语义):非 secret 存明文 value;
-- secret 只存 vault 凭据引用(credentialId),明文绝不入库。供多流水线引用/复用同一套变量。
--   id        : 变量组主键(uuid)
--   name      : 变量组名(去空白后非空;库内唯一)
--   description: 说明(可空)
--   vars_json : 变量列表 JSON(BuildVar 持久化形状:非 secret 明文 value / secret 仅 credentialId 引用)
CREATE TABLE IF NOT EXISTS variable_groups (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    vars_json   TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_variable_groups_name ON variable_groups (name);
