package build

import (
	"context"

	"github.com/huangchengsir/pipewright/internal/buildcache"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

// buildcache.go 把「构建依赖缓存」(build cache · P0)接进 dag 阶段执行:script 类 job 执行前
// 据 job.Config 恢复缓存目录到工作区、执行后保存回缓存库。缓存配置键(对齐前端 jobConfigSchema):
//
//   - cachePaths:多行,每行一个相对工作区根的目录(node_modules / .m2/repository / .gradle 等)。
//     仅当配了 cachePaths 才启用缓存(空 → 跳过,零开销)。
//   - cacheKey  :可选 key 模板;留空用默认推导(分支 + 工作区内 lockfile 们的内容 hash)。
//     模板里的 {{参数}} 由 templateContext 渲染(与命令/产物路径一致)。
//
// 可靠性铁律:缓存问题绝不让构建失败 —— 恢复未命中/失败 = 冷构建;保存失败 = 只记日志。

// jobCacheConfig 是从 job.Config 解析出的缓存配置。enabled=false 时调用方跳过缓存。
type jobCacheConfig struct {
	enabled bool
	paths   []string // 相对工作区根的缓存路径(已 split 去空)
	key     string   // 已渲染的 key 模板(空 = 用默认推导)
}

// parseCacheConfig 从 job.Config 解析缓存配置(cachePaths 多行 + 可选 cacheKey 模板,渲染 {{参数}})。
// 没配 cachePaths → enabled=false(调用方跳过)。
func parseCacheConfig(jb pipeline.Job) jobCacheConfig {
	tplCtx := templateContext(jb.Config)
	paths := splitCommands(renderTemplate(cfgString(jb.Config, "cachePaths"), tplCtx))
	if len(paths) == 0 {
		return jobCacheConfig{enabled: false}
	}
	return jobCacheConfig{
		enabled: true,
		paths:   paths,
		key:     renderTemplate(cfgString(jb.Config, "cacheKey"), tplCtx),
	}
}

// restoreJobCache 恢复 job 的缓存到工作区(best-effort:未命中/失败=冷构建,只记日志)。
// 缓存库未注入或 job 未配 cachePaths → 无操作。branch 用于默认 key 推导。
func (b *Builder) restoreJobCache(ctx context.Context, rep dagrun.StageReporter, jb pipeline.Job, branch, workspace string) {
	if b.buildCache == nil {
		return
	}
	cc := parseCacheConfig(jb)
	if !cc.enabled {
		return
	}
	key := buildcache.DeriveKey(branch, cc.key, workspace)
	hit, err := b.buildCache.Restore(ctx, key, cc.paths, workspace)
	if err != nil {
		// 仅输入非法才到这(理论上不会);当冷构建处理。
		_ = rep.Log(ctx, streamStdout, "缓存恢复跳过(配置无效),本次冷构建")
		return
	}
	if hit {
		_ = rep.Log(ctx, streamStdout, "已恢复构建缓存(暖构建):"+joinPaths(cc.paths))
	} else {
		_ = rep.Log(ctx, streamStdout, "未命中构建缓存(冷构建),构建后将保存:"+joinPaths(cc.paths))
	}
}

// saveJobCache 把 job 的缓存路径保存回缓存库(best-effort:保存失败只记日志,不阻断构建)。
// 缓存库未注入或 job 未配 cachePaths → 无操作。
func (b *Builder) saveJobCache(ctx context.Context, rep dagrun.StageReporter, jb pipeline.Job, branch, workspace string) {
	if b.buildCache == nil {
		return
	}
	cc := parseCacheConfig(jb)
	if !cc.enabled {
		return
	}
	key := buildcache.DeriveKey(branch, cc.key, workspace)
	if err := b.buildCache.Save(ctx, key, cc.paths, workspace); err != nil {
		_ = rep.Log(ctx, streamStderr, "构建缓存保存失败(不影响本次构建结果):"+err.Error())
		return
	}
	_ = rep.Log(ctx, streamStdout, "已保存构建缓存:"+joinPaths(cc.paths))
}

// joinPaths 把缓存路径列表拼成一行日志(逗号分隔)。
func joinPaths(paths []string) string {
	out := ""
	for i, p := range paths {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// 编译期断言:*buildcache.Store 满足 buildCacheStore(注入契约不漂移)。
var _ buildCacheStore = (*buildcache.Store)(nil)
