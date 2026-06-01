package build

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
)

// shellDriver 是默认 Driver:shell 到探测出的容器 CLI(bin),所有命令经 Commander 以 array 执行。
//
// AC-SEC-02:命令一律 []string(程序 + 参数),绝不拼 shell 字符串(参数里的 `;`、`$()`、反引号
// 全被当字面量,无注入面)。secret 构建变量经 --build-arg 注入但命令回显里只列 key(不打印值);
// 仓库口令经 stdin(--password-stdin)注入,绝不进 argv/日志。
type shellDriver struct {
	bin  string
	cmdr Commander
}

func (d *shellDriver) Binary() string { return d.bin }

// emitCmd 把「将执行的命令」作为一行 stdout 日志回显(便于审计/AI 诊断),但**只回显安全部分**:
// 调用方传入的 display 已剥离 secret 值(只留 key)。
func emitCmd(onLine func(stream, line string), bin string, display []string) {
	if onLine == nil {
		return
	}
	onLine("stdout", "$ "+bin+" "+strings.Join(display, " "))
}

func (d *shellDriver) Build(ctx context.Context, contextDir, dockerfile, localTag string, buildArgs, secretArgs []string, onLine func(stream, line string)) (int, error) {
	// `-f` 的相对路径由 docker 相对**当前工作目录**(而非 context)解析,故须把相对 Dockerfile
	// 显式拼到 contextDir 下,否则 docker 在进程 cwd 找不到克隆出的 Dockerfile。绝对路径原样用。
	dfPath := dockerfile
	if !filepath.IsAbs(dfPath) {
		dfPath = filepath.Join(contextDir, dockerfile)
	}
	// 真实参数:含 secret 的 --build-arg K=V(注入子进程,不进可见日志)。
	args := []string{"build", "-f", dfPath, "-t", localTag}
	// 回显参数:secret 的 --build-arg 只列 key(K=***),绝不打印值。
	display := []string{"build", "-f", dfPath, "-t", localTag}
	for _, kv := range buildArgs {
		args = append(args, "--build-arg", kv)
		display = append(display, "--build-arg", kv) // 非 secret:可见
	}
	for _, kv := range secretArgs {
		args = append(args, "--build-arg", kv)
		display = append(display, "--build-arg", maskKV(kv)) // secret:只列 key
	}
	args = append(args, contextDir)
	display = append(display, contextDir)

	emitCmd(onLine, d.bin, display)
	return d.cmdr.Stream(ctx, d.bin, args, "", onLine)
}

func (d *shellDriver) RunToolchain(ctx context.Context, image, hostDir, workdir string, env []string, cmd []string, res pipeline.Resource, onLine func(stream, line string)) (int, error) {
	// docker run --rm -v hostDir:workdir -w workdir [--cpus N] [--memory M] [-e K=V...] image cmd...
	args := []string{"run", "--rm", "-v", hostDir + ":" + workdir, "-w", workdir}
	display := []string{"run", "--rm", "-v", hostDir + ":" + workdir, "-w", workdir}
	// 资源规格(可选):cpu→--cpus、memory→--memory。值原样作 array 实参(不拼 shell;非法值由 docker 拒绝)。
	// 资源 flag 非 secret,回显与真实参数一致。
	if cpu := strings.TrimSpace(res.CPU); cpu != "" {
		args = append(args, "--cpus", cpu)
		display = append(display, "--cpus", cpu)
	}
	if mem := strings.TrimSpace(res.Memory); mem != "" {
		args = append(args, "--memory", mem)
		display = append(display, "--memory", mem)
	}
	// 旁挂服务(services):脚本容器加入服务网络,按服务名(network-alias)互访。运行时设置,非 secret。
	if net := strings.TrimSpace(res.Network); net != "" {
		args = append(args, "--network", net)
		display = append(display, "--network", net)
	}
	for _, kv := range env {
		args = append(args, "-e", kv)
		display = append(display, "-e", maskKV(kv)) // 环境变量可能含 secret:回显只列 key
	}
	args = append(args, image)
	args = append(args, cmd...)
	display = append(display, image)
	display = append(display, cmd...)

	emitCmd(onLine, d.bin, display)
	return d.cmdr.Stream(ctx, d.bin, args, "", onLine)
}

func (d *shellDriver) Tag(ctx context.Context, localTag, remoteTag string, onLine func(stream, line string)) (int, error) {
	args := []string{"tag", localTag, remoteTag}
	emitCmd(onLine, d.bin, args)
	return d.cmdr.Stream(ctx, d.bin, args, "", onLine)
}

func (d *shellDriver) Login(ctx context.Context, registryURL, username, password string, onLine func(stream, line string)) (int, error) {
	// 口令经 stdin(--password-stdin),**绝不**进 argv/日志。回显里也不含 -p/口令。
	args := []string{"login", registryURL, "-u", username, "--password-stdin"}
	emitCmd(onLine, d.bin, args)
	return d.cmdr.Stream(ctx, d.bin, args, password, onLine)
}

func (d *shellDriver) Push(ctx context.Context, remoteTag string, onLine func(stream, line string)) (int, error) {
	args := []string{"push", remoteTag}
	emitCmd(onLine, d.bin, args)
	return d.cmdr.Stream(ctx, d.bin, args, "", onLine)
}

// InspectImage 取镜像 digest 与字节大小,经 `inspect --format '{{json ...}}'`(参数化,不拼 shell)。
// 解析失败时 digest 退化为镜像 ref、size=0(产物仍可落库,只是 metadata 不全),不报致命错。
func (d *shellDriver) InspectImage(ctx context.Context, ref string) (string, int64, error) {
	// 取 RepoDigests(推送后才有)+ Id + Size 的 JSON。
	args := []string{"inspect", "--format", "{{json .}}", ref}
	stdout, _, code, _ := d.cmdr.Run(ctx, d.bin, args, "")
	if code != 0 || strings.TrimSpace(stdout) == "" {
		return "", 0, nil // 不致命:产物仍 emit,metadata 退化
	}
	digest, size := parseInspect(stdout)
	return digest, size, nil
}

// inspectShape 是 docker/nerdctl/podman inspect 输出里本包关心的子集。
type inspectShape struct {
	ID          string   `json:"Id"`
	RepoDigests []string `json:"RepoDigests"`
	Size        int64    `json:"Size"`
}

// parseInspect 从 inspect JSON 解析 digest(优先 RepoDigests 的 sha256,否则 Id)与字节大小。
func parseInspect(out string) (string, int64) {
	out = strings.TrimSpace(out)
	// inspect 默认返回数组;--format '{{json .}}' 返回单对象。两者都尝试。
	var single inspectShape
	if err := json.Unmarshal([]byte(out), &single); err == nil && (single.ID != "" || len(single.RepoDigests) > 0) {
		return digestFrom(single), single.Size
	}
	var arr []inspectShape
	if err := json.Unmarshal([]byte(out), &arr); err == nil && len(arr) > 0 {
		return digestFrom(arr[0]), arr[0].Size
	}
	return "", 0
}

// digestFrom 取 RepoDigests 里第一个 @sha256: 后缀,否则退化为 Id。
func digestFrom(s inspectShape) string {
	for _, rd := range s.RepoDigests {
		if i := strings.Index(rd, "@"); i >= 0 {
			return rd[i+1:]
		}
	}
	return s.ID
}

// maskKV 把 "K=V" 回显为 "K=***"(只暴露 key,值打码);无 '=' 时原样(仅 key)。
func maskKV(kv string) string {
	if i := strings.Index(kv, "="); i >= 0 {
		return kv[:i] + "=***"
	}
	return kv
}

// itoa 是 strconv.Itoa 的薄封装(避免在多处 import)。
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
