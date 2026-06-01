package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

// stage_env.go 实现「步骤输出 → 下游变量」(P1 · 对标云效 $FLOW_ENV / GitHub $GITHUB_ENV)。
//
// 同一阶段内多个 script job 共享克隆工作区。约定:job 容器内向 `$PIPEWRIGHT_ENV` 指向的文件
// 追加 `KEY=VALUE` 行,执行后由执行器捕获并注入**后续同阶段 job** 的环境变量,实现 job 间
// 传值(如 build job 算出版本号 → deploy/通知 job 引用)。
//
// 文件位于工作区根(挂载进容器),宿主可读;每个 job 执行后即捕获并清空,故各 job 的写入隔离、
// 捕获结果在内存累积(后写覆盖同名键,与环境变量语义一致)。绝不改单机执行语义、无新依赖。

// pipewrightEnvFileName 是阶段内「步骤输出环境」文件名(工作区根相对)。
const pipewrightEnvFileName = ".pipewright_env"

// pipewrightEnvVar 返回注入容器的 `PIPEWRIGHT_ENV=<容器内路径>` 变量;job 内向该路径写 KEY=VALUE。
func pipewrightEnvVar() pipeline.BuildVar {
	return pipeline.BuildVar{
		Key:   "PIPEWRIGHT_ENV",
		Value: joinContainerPath(scriptWorkspaceMount, pipewrightEnvFileName),
	}
}

// captureStageEnv 读取并清空工作区根的步骤输出文件,解析 KEY=VALUE 行为 BuildVar(明文,非 secret)。
// 文件不存在 / 空 → 返回 nil(无输出)。读失败 / 行非法宽松跳过(绝不阻断构建)。
func captureStageEnv(ctx context.Context, rep dagrun.StageReporter, workspace string) []pipeline.BuildVar {
	path := filepath.Join(workspace, pipewrightEnvFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // 不存在 = 本 job 无输出(常态)
	}
	_ = os.Remove(path) // 即捕即清,隔离各 job 的写入
	var out []pipeline.BuildVar
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		i := strings.IndexByte(t, '=')
		if i <= 0 {
			continue // 无 = 或 = 在首位 → 非法,跳过
		}
		key := strings.TrimSpace(t[:i])
		if !isEnvKey(key) {
			continue
		}
		out = append(out, pipeline.BuildVar{Key: key, Value: t[i+1:]})
	}
	if len(out) > 0 && rep != nil {
		keys := make([]string, 0, len(out))
		for _, v := range out {
			keys = append(keys, v.Key) // 仅记键名,绝不回显值(可能含敏感数据)
		}
		_ = rep.Log(ctx, streamStdout, "· 捕获步骤输出变量供下游 job 使用:"+strings.Join(keys, ", "))
	}
	return out
}

// isEnvKey 校验环境变量名合法(字母/下划线开头,仅字母数字下划线)。
func isEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
