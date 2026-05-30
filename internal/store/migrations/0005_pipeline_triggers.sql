-- 0005 pipeline_triggers:项目触发设置与分支映射(FR-1 / FR-2 / Story 2.3)。
-- 每项目一行(project_id 既是主键也是外键),记录 webhook 接收端点 token、
-- 经 vault secretbox 加密的签名密钥密文、触发事件开关、分支→环境/目标服务器映射、
-- 未匹配策略。webhook 签名密钥仅以密文 BLOB 入库,DB dump 绝无明文(AC1 / AC-SEC)。
--   project_id                : FK → projects.id;PK;ON DELETE CASCADE(删项目级联删触发设置)
--   webhook_token             : webhook 接收端点路径标识(/api/webhooks/<token>),UNIQUE;非密钥,可对外
--   webhook_secret_ciphertext : NaCl secretbox 密文(nonce||box),前置 24B 随机 nonce;仅存密文
--   events_json               : 触发事件开关 JSON,如 {"push":true,"tag":false,"pullRequest":false}
--   branch_mappings_json      : 分支映射 JSON 数组,元素 {id,branchPattern,environment,targetServerIds}
--   unmatched_policy          : record | ignore(未匹配任何映射的分支处理策略;应用层校验枚举)

CREATE TABLE IF NOT EXISTS pipeline_triggers (
    project_id                TEXT PRIMARY KEY,
    webhook_token             TEXT NOT NULL UNIQUE,
    webhook_secret_ciphertext BLOB NOT NULL,
    events_json               TEXT NOT NULL DEFAULT '{}',
    branch_mappings_json      TEXT NOT NULL DEFAULT '[]',
    unmatched_policy          TEXT NOT NULL DEFAULT 'record',
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
