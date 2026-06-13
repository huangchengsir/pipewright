-- 0040 pipeline_runs.spec_source*(MySQL):记录驱动本次运行的流水线配置来源(GitOps 配置来源可见性 · Slice 2)。
-- spec_source: "" | repo | stored —— repo=仓库根 .pipewright.yml 驱动;stored=库内(网页)配置驱动;""=未记录(老运行)。
-- spec_source_ref/file: repo 来源时取用的分支/ref 与文件名;spec_source_fallback: stored 来源回退原因。
ALTER TABLE pipeline_runs ADD COLUMN spec_source VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN spec_source_ref VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN spec_source_file VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN spec_source_fallback VARCHAR(64) NOT NULL DEFAULT '';
