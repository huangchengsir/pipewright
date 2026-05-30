-- 0014 run artifacts:为成功运行补「构建产物契约」持久化(FR-6 / Story 3.4)。
-- additive only:新增独立 run_artifacts 表,不改既有 pipeline_runs / run_steps / run_logs 列或语义。
--
-- 产物契约是 Epic 3 一等交付物:type 枚举 + reference 寻址语义定死,Epic 4 部署据此消费,
-- 无需了解构建内部。3-3 真实构建落地后经同一 EmitArtifact 接口接入,**契约形状不变**。
--
-- 当前系统无真实构建(3-3 卡 Docker),故本期由桩 runner 在成功路径合成 1~2 个桩产物
-- (metadata 标 "stub":true 诚实标注);3-3 落地后由真实产物经同一 StepSink.EmitArtifact 接入。
--
--   type          : image | jar | dist | archive(枚举冻结;后续新增类型只加枚举不改形状)。
--   name          : 产物逻辑名(如服务名 shop-api)。
--   reference     : 类型寻址引用(image: repo:tag 或本地 image id;jar: 路径/URL;
--                   dist: 目录/tarball 寻址;archive: 路径)。Epic 4 按 (type, reference) 消费。
--   size_bytes    : 产物字节数(未知 = 0)。
--   metadata_json : 自由 KV JSON(digest/path/stub 等);空串视为 {}。
--   created_at    : 产物登记时刻(RFC3339;UTC)。

CREATE TABLE IF NOT EXISTS run_artifacts (
    id            TEXT PRIMARY KEY,
    run_id        TEXT NOT NULL,
    type          TEXT NOT NULL,
    name          TEXT NOT NULL,
    reference     TEXT NOT NULL,
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

-- (run_id) 索引:按 run 拉取产物列表(run-detail slot + GET /api/runs/{id}/artifacts 的主查询路径)。
CREATE INDEX IF NOT EXISTS idx_run_artifacts_run ON run_artifacts (run_id);
