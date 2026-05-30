-- 0012 run diagnosis:为失败运行补「失败日志捕获」+「AI 诊断持久化」(FR-22 / Story 7.2)。
-- additive only:仅为既有 pipeline_runs 增列,不改既有列/索引/语义(3-1/3-2 done)。
--
-- 当前系统无真实构建日志(3-3 卡 Docker、3-6 实时日志 backlog),故本期由桩 runner 在
-- 失败路径合成真实感错误日志写入 failure_log,供 ai.Service.Diagnose 消费。3-3/3-6 落地后换真实日志。
--
--   failure_log    : 桩/真实失败日志原文(脱敏前;**绝不**经此列出网,出网前必过 mask.Masker)。
--                    空串 = 无失败日志(成功 run / 未捕获)。
--   diagnosis_json : AI 诊断结果 JSON(对齐冻结 diagnosis 子 DTO;evidence 取自脱敏后日志)。
--                    空串 = 未诊断(run-detail diagnosis 字段为 null);非空 = 已诊断(填值)。
--                    **绝不含明文 secret**(写入前日志已脱敏)。

ALTER TABLE pipeline_runs ADD COLUMN failure_log TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN diagnosis_json TEXT NOT NULL DEFAULT '';
