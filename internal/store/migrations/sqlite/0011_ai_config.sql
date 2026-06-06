-- 0011 ai_config:可配置 AI 提供商单例配置(FR-24 · NFR-10 · Story 7.1)。
-- 全平台一份活跃 AI 配置(本期单一活跃 provider,不并存);id 强制为 1 的单例表。
-- 记录 provider(claude|openai|ollama)+ baseUrl + model + apiKey(加密)+ budget 声明 + enabled 开关。
--
-- AC-SEC:apiKey 经 vault secretbox(master key)加密后仅以**密文 BLOB**(api_key_ciphertext)入库;
-- DB dump 绝无明文 key。ollama 无需 key 时该列为空。对外(REST)只暴露掩码;明文绝不常驻/回显。
-- budget 本期仅存声明(monthlyTokenLimit),不强制执行/计量(Epic7 用量统计后做)。
--   id                  : 单例主键,CHECK(id=1) 锁死只此一行
--   provider            : 提供商枚举(claude|openai|ollama;空=未配置)
--   base_url            : 提供商 API 基址(provider 非空时非空;前端可预填默认,后端兜底)
--   model               : 选用模型名(自由文本)
--   api_key_ciphertext  : vault secretbox 密文 BLOB(空=无 key;ollama 常态空)
--   budget_json         : 预算声明 JSON,形如 {"monthlyTokenLimit":number|null}
--   enabled             : 启用开关(未配置/未启用 → 下游优雅禁用,核心 CI/CD 不依赖)
--   created_at          : 创建时间(RFC3339)
--   updated_at          : 更新时间(RFC3339)

CREATE TABLE IF NOT EXISTS ai_config (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    provider            TEXT NOT NULL DEFAULT '',
    base_url            TEXT NOT NULL DEFAULT '',
    model               TEXT NOT NULL DEFAULT '',
    api_key_ciphertext  BLOB,
    budget_json         TEXT NOT NULL DEFAULT '{"monthlyTokenLimit":null}',
    enabled             INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
