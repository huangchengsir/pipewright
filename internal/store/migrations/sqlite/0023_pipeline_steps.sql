-- 0022 pipeline_settings.steps_json:自定义脚本步骤列表(Epic 8 地基 · Story 8-1)。
-- 在既有 pipeline_settings(0010)上加一列,承载一条「有序步骤列表」(本期非完整 DAG,DAG 留 8-3)。
-- 每步是流水线里可跑「任意命令」的通用原语(对标 Jenkins sh / 云效自定义命令任务),
-- 在隔离构建容器内执行(绝不在中控机宿主跑)。
--
-- AC-SEC:与 build_json/environments_json 同律 —— secret 类型的步骤 env**只存 credentialId 引用**,
-- 引用 credentials 表的密文凭据;本列 JSON 中**绝无任何明文 secret**(非 secret 项才存明文 value)。
-- 响应掩码由服务端经保险库即时计算,不入库;命令回显/出网/落库前一律脱敏。
--   steps_json : 步骤定义 JSON 数组(冻结 DTO),元素形如
--                {"id","name","type":"script","image":"node:20","commands":["...","..."],
--                 "env":[{"id","key","secret",("value"|"credentialId")}],"workDir":"."}
--
-- 老行(0010 建、本迁移前写入)无该列值,SQLite 以 DEFAULT '[]' 兜底(无步骤=行为不变,向后兼容)。

ALTER TABLE pipeline_settings ADD COLUMN steps_json TEXT NOT NULL DEFAULT '[]';
