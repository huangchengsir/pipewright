-- 0030 pipeline_chains:流水线串联(成功后触发下游流水线)配置 + 运行串联溯源(FR-8-11)。
--
-- 两部分:
--   1) project_chain_targets:每项目「成功后要触发的下游目标」列表(0..N 行)。
--      上游项目成功落终态时,串联终态钩子据此为每个 enabled 目标创建一次「chain」触发的下游运行。
--   2) pipeline_runs 两列追加:记录某次运行由哪条上游运行串联触发(溯源)+ 串联深度(防无限环)。
--
-- 环路安全(必做,见 internal/chain):下游运行携带 chain_depth = 上游 depth + 1;
-- 钩子拒绝超过 maxChainDepth(默认 5)的串联;并在串联链路上检测「项目已在本链路径中」二次兜底。
--
-- project_chain_targets:
--   id                    : uuid v4;PK
--   project_id            : 上游项目;FK → projects.id;ON DELETE CASCADE(删上游项目级联删其串联配置)
--   downstream_project_id : 下游(被触发)项目;FK → projects.id;ON DELETE CASCADE(删下游项目级联清理)
--   branch                : 触发下游时使用的分支(空 = 下游项目默认分支,由 run.Create 解析)
--   enabled               : 0/1 是否启用该串联目标
--   created_at/updated_at : RFC3339
--   唯一 (project_id, downstream_project_id, branch):同一上游对同一下游同一分支不重复配置。
--
-- pipeline_runs 追加列(均有默认值,旧行不受影响):
--   chain_source_run_id : 触发本次运行的上游运行 id(NULL/'' = 非串联触发)
--   chain_depth         : 串联深度(根运行=0;每向下游串联一层 +1;防无限链)

CREATE TABLE IF NOT EXISTS project_chain_targets (
    id                    TEXT PRIMARY KEY,
    project_id            TEXT NOT NULL,
    downstream_project_id TEXT NOT NULL,
    branch                TEXT NOT NULL DEFAULT '',
    enabled               INTEGER NOT NULL DEFAULT 1,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    FOREIGN KEY (downstream_project_id) REFERENCES projects (id) ON DELETE CASCADE
);

-- 串联终态钩子按上游 project_id 扫描其启用目标;建索引避免全表扫。
CREATE INDEX IF NOT EXISTS idx_project_chain_targets_project ON project_chain_targets (project_id);
-- 同一上游对同一下游同一分支唯一(防重复配置导致一次成功多触发同一下游)。
CREATE UNIQUE INDEX IF NOT EXISTS uq_project_chain_targets
    ON project_chain_targets (project_id, downstream_project_id, branch);

-- pipeline_runs 串联溯源列(additive;旧行默认非串联、深度 0)。
ALTER TABLE pipeline_runs ADD COLUMN chain_source_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN chain_depth INTEGER NOT NULL DEFAULT 0;
