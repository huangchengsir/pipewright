-- 0013 run logs:为运行补「逐行日志持久化」(FR-16 实时侧 / Story 3.6)。
-- additive only:新增独立 run_logs 表,不改既有 pipeline_runs / run_steps 列或语义(3-1 done)。
--
-- 当前系统无真实构建日志(3-3 卡 Docker),故本期由桩 runner 在每步逐行 emit 真实感日志
-- (含一个假 secret 验脱敏链路);3-3 落地后由真实构建日志经同一 StepSink.Log 接口接入。
--
--   seq          : 该 run 内单调递增序号(从 1 起),供前端去重/排序/断线续传。
--   ts           : 行产生时刻(RFC3339;UTC)。
--   stream       : stdout | stderr。
--   step_ordinal : 关联步骤序号(-1 = 运行级,无具体步骤)。
--   text         : 单行**已脱敏**日志文本(出网/落库前必过 mask.Masker;绝无明文 secret)。

CREATE TABLE IF NOT EXISTS run_logs (
    id           TEXT PRIMARY KEY,
    run_id       TEXT NOT NULL,
    seq          INTEGER NOT NULL,
    ts           TEXT NOT NULL,
    stream       TEXT NOT NULL,
    step_ordinal INTEGER NOT NULL,
    text         TEXT NOT NULL
);

-- (run_id, seq) 复合索引:按 run 拉取 + sinceSeq 分页 + 升序回放的主查询路径。
CREATE INDEX IF NOT EXISTS idx_run_logs_run_seq ON run_logs (run_id, seq);
