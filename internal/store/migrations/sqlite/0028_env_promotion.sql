-- 0028 env_promotion:环境晋级流 dev→staging→prod(Epic 8 · Story 8-7 / FR-8-7)。
--
-- 三张表:
--   project_environments      : 每项目一条有序环境链(JSON 数组,含 name/gated 配置)。
--   environment_variables     : 逐环境作用域的 key=value 变量;secret 变量经 credential_id 引用保险库(无明文)。
--   run_promotions            : 晋级记录(哪个 run 晋级到哪个环境、由谁、复用的审批门 stageId)。
--
-- 安全:variables 表的 secret 行只存 credential_id(指向 credentials 表),**绝无明文密钥**;
-- 非 secret 行存明文 value(构建参数级,非密钥)。晋级记录不含敏感数据。
-- 删项目级联删环境配置/变量/晋级记录。

-- 每项目一条:有序环境链配置(JSON 数组,如 [{"name":"dev","gated":false},{"name":"prod","gated":true}])。
CREATE TABLE IF NOT EXISTS project_environments (
    project_id  TEXT PRIMARY KEY,
    chain_json  TEXT NOT NULL DEFAULT '[]',
    updated_at  TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

-- 逐环境作用域的变量:
--   is_secret=0 → value 为明文(非密钥构建变量);credential_id 空。
--   is_secret=1 → credential_id 引用 credentials 表(保险库),value 空(绝无明文密钥)。
CREATE TABLE IF NOT EXISTS environment_variables (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL,
    environment   TEXT NOT NULL,
    var_key       TEXT NOT NULL,
    value         TEXT NOT NULL DEFAULT '',
    is_secret     INTEGER NOT NULL DEFAULT 0,
    credential_id TEXT NOT NULL DEFAULT '',
    updated_at    TEXT NOT NULL,
    UNIQUE (project_id, environment, var_key),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_env_vars_proj_env
    ON environment_variables (project_id, environment);

-- 晋级记录:一次「把 source_run 晋级到 target_environment」的事实。
--   status        : pending(待批) | promoted(已晋级) | rejected(被拒/超时/取消)
--   approval_stage: 复用审批门内核(run_approvals)时的 stageId(gated 环境;非 gated 为空)
--   promoted_by   : 操作者(展示/审计;绝无敏感数据)
CREATE TABLE IF NOT EXISTS run_promotions (
    id                 TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL,
    source_run_id      TEXT NOT NULL,
    from_environment   TEXT NOT NULL DEFAULT '',
    target_environment TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending',
    approval_stage     TEXT NOT NULL DEFAULT '',
    promoted_by        TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    decided_at         TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    FOREIGN KEY (source_run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_promotions_proj ON run_promotions (project_id);
CREATE INDEX IF NOT EXISTS idx_run_promotions_run ON run_promotions (source_run_id);
