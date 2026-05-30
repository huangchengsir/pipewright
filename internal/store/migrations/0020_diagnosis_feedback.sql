-- 0020 diagnosis feedback:诊断反馈闭环(FR-26 / Story 7.5)。
-- 每条 AI 诊断 👍/👎(👎 可附正确根因);反馈持久化、关联运行与诊断快照,
-- 汇入设置页统计(准确率/反馈数/趋势),并为知识库种子(本期仅持久化 + 统计)。
--
--   id                 : uuid v4;PK
--   run_id             : FK → pipeline_runs.id;ON DELETE CASCADE(删运行级联删反馈)。
--                        **UNIQUE**:同一 run 仅一条反馈,改判 = upsert 覆盖最新。
--   verdict            : up | down(应用层枚举校验;👍/👎)。
--   correct_root_cause : 👎 附的正确根因文本(知识库种子)。**脱敏尽力 + 长度上限**(写入前
--                        过 mask.Masker;绝无明文 secret)。up / 未填 = 空串。
--   diagnosis_snapshot : 反馈时刻诊断快照 JSON(关联该诊断,供错误案例沉淀)。空串 = 无快照。
--                        **绝不含明文 secret**(诊断本身已脱敏后落 pipeline_runs.diagnosis_json)。
--   created_at         : 首次反馈时刻 RFC3339。
--   updated_at         : 最近改判时刻 RFC3339(upsert 覆盖时刷新)。

CREATE TABLE IF NOT EXISTS diagnosis_feedback (
    id                 TEXT PRIMARY KEY,
    run_id             TEXT NOT NULL UNIQUE,
    verdict            TEXT NOT NULL,
    correct_root_cause TEXT NOT NULL DEFAULT '',
    diagnosis_snapshot TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_diagnosis_feedback_created_at ON diagnosis_feedback (created_at);
