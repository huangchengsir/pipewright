-- 0036 trigger_path_filters:路径过滤触发(monorepo · P0 引擎能力)。
-- 给触发配置加一组 glob(如 backend/**):仅当本次 push 改动的文件匹配任一 glob 才触发,
-- 不匹配则忽略(IgnoredReason=path_no_match);拿不到改动文件列表时放行不拦(诚实降级)。
-- 以 JSON 数组存于 pipeline_triggers 新列(非敏感);空数组 [] = 不启用路径过滤(放行一切,向后兼容)。
ALTER TABLE pipeline_triggers ADD COLUMN path_filters_json TEXT NOT NULL DEFAULT '[]';
