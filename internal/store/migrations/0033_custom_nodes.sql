-- 0033 自定义节点(复用库 Tier 2 · 对标 Jenkins 自定义步骤 / 云效自建任务模板)。
--
-- custom_nodes:命名、可复用的「单节点」定义。用户在画布把某个节点(常为 templated/script
-- 自定义节点,也可是任意内建类型)配好后「存为自定义节点」,落一份 config 快照;之后从节点选择器
-- 挑选该自定义节点,即插入一个预填好 config 的 Job(免去重复填参)。与模板(整条流水线)互补:
-- 模板复用「一段 stages」,自定义节点复用「一个 job」。跨项目共享、与具体项目解耦。
--
-- 参数自由:config_json 是 Job.Config 的原样快照(自由 KV),不强加 schema(单管理员场景,
-- 留足自定义自由度)。绝不存任何明文 secret —— config 与流水线 spec 同形状,secret 走保险库引用。
--   id          : 自定义节点主键(uuid)
--   name        : 节点名(去空白后非空;库内唯一,便于挑选)
--   description : 说明(可空)
--   node_type   : 底层 Job 类型(如 templated/script/build_frontend;决定执行语义与图标)
--   summary     : 卡片副标题展示文本(可空,镜像 Job.Summary)
--   config_json : Job.Config 的 JSON 快照(自由 KV;不含明文 secret)
CREATE TABLE IF NOT EXISTS custom_nodes (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    node_type   TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- 名称唯一(挑选时人读、避免重名歧义)。
CREATE UNIQUE INDEX IF NOT EXISTS idx_custom_nodes_name ON custom_nodes (name);
