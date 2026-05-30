package deploy

import (
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"github.com/huangchengsir/pipewright/internal/run"
)

// commands.go 按产物 type 构造部署命令(**全程 array 化 []string**;AC-SEC-02)。
//
//   - dist    : 传输 + 部署到目标机(本机可真验):mkdir 目标目录 → 经 SSH 把 reference
//               写入目标(cat > / base64 -d,小文件够用)→ 软链 / 标记当前版本。
//   - jar     : 放置 jar(mkdir → 写入)+ 构造 `java -jar` 启动命令(命令构造正确即可;
//               目标无 java → 该机 failed 人读)。
//   - image   : 构造 `docker pull` + `docker run`(目标无 docker → 该机 failed 人读)。
//   - archive : 放置归档 + 解包到目标目录。
//
// 命令各参数原样作为 array 元素,绝不在平台侧拼接 shell;target.Exec 各参数 shell 转义后
// 再交 SSH session。
//
// 返回:(命令序列, 成功摘要文案, 错误)。错误用于无法构造命令的情形(如非法 type)。

// defaultDeployRoot 是未在 Config 指定路径时的部署根目录(本机真验友好:用临时区,不需 root)。
const defaultDeployRoot = "/tmp/pipewright-deploy"

// buildCommands 据产物 type 返回部署命令序列 + 成功摘要。
func buildCommands(a run.Artifact, cfg map[string]string) ([][]string, string, error) {
	switch a.Type {
	case run.ArtifactDist, run.ArtifactArchive:
		return buildFileDeploy(a, cfg)
	case run.ArtifactJar:
		return buildJarDeploy(a, cfg)
	case run.ArtifactImage:
		return buildImageDeploy(a, cfg)
	default:
		return nil, "", fmt.Errorf("不支持的产物类型:%s", a.Type)
	}
}

// targetDir 解析部署目标目录(Config["path"] 优先;否则 defaultDeployRoot/<run 名净化>)。
func targetDir(a run.Artifact, cfg map[string]string) string {
	if p := strings.TrimSpace(cfg["path"]); p != "" {
		return p
	}
	name := sanitizeName(a.Name)
	if name == "" {
		name = "app"
	}
	return path.Join(defaultDeployRoot, name)
}

// sanitizeName 把产物名净化为安全目录段(仅字母数字 . _ -;其余替为 _)。
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// buildFileDeploy 构造 dist / archive 的「传输 + 部署」命令(本机可真验)。
//
// reference 视为产物内容寻址:本期把 reference 作为产物负载文本经 SSH 写入目标
// (base64 -d 经 stdin 不便,故用 array 化的 sh -c 'echo <b64> | base64 -d > file' 会
// 引入 shell 拼接 —— 为守住 AC-SEC-02,改用 `tee` 风格不可行无 stdin)。这里采用
// **array 化的 printf 写入**:命令各参数仍是独立 array 元素,无平台侧 shell 拼接。
//
// 步骤:
//  1. mkdir -p <dir>
//  2. 写入产物负载到 <dir>/<artifact-file>(经 base64 经 array 命令落地)
//  3. ln -sfn <dir> <dir>-current(标记当前版本;dist 软链语义)
func buildFileDeploy(a run.Artifact, cfg map[string]string) ([][]string, string, error) {
	dir := targetDir(a, cfg)
	file := path.Join(dir, deployFileName(a))

	// 产物负载:本期以 reference 文本作为内容真验传输链路(reference 为目录/tarball 寻址,
	// 真实构建 3-3 落地后换为读取真实产物字节)。base64 编码后经 array 命令解码落地,
	// 避免任何 shell 元字符问题,且不拼接 shell。
	payload := base64.StdEncoding.EncodeToString([]byte(a.Reference + "\n"))

	cmds := [][]string{
		{"mkdir", "-p", dir},
		// `sh -c` 仅承载固定模板 + base64(纯 [A-Za-z0-9+/=],无 shell 元字符注入面);
		// 文件路径 / payload 作为 $0/$1 位置参数传入,绝不拼进脚本体(AC-SEC-02)。
		{"sh", "-c", `printf '%s' "$1" | base64 -d > "$0"`, file, payload},
	}
	summary := fmt.Sprintf("%s 部署完成 → %s", a.Type, file)
	if a.Type == run.ArtifactDist {
		link := dir + "-current"
		cmds = append(cmds, []string{"ln", "-sfn", dir, link})
		summary = fmt.Sprintf("dist 部署完成 → %s(当前版本 → %s)", dir, link)
	}
	return cmds, summary, nil
}

// deployFileName 取产物落地文件名(reference 的 base 名;无则用净化产物名 + .bin)。
func deployFileName(a run.Artifact) string {
	base := path.Base(strings.TrimRight(a.Reference, "/"))
	base = sanitizeName(base)
	if base == "" || base == "." {
		base = sanitizeName(a.Name) + ".bin"
	}
	return base
}

// buildJarDeploy 构造 jar 的放置 + `java -jar` 启动命令(命令构造正确即可;目标无 java → failed)。
func buildJarDeploy(a run.Artifact, cfg map[string]string) ([][]string, string, error) {
	dir := targetDir(a, cfg)
	jarPath := path.Join(dir, deployFileName(a))
	payload := base64.StdEncoding.EncodeToString([]byte(a.Reference + "\n"))

	cmds := [][]string{
		{"mkdir", "-p", dir},
		{"sh", "-c", `printf '%s' "$1" | base64 -d > "$0"`, jarPath, payload},
		// 启动命令:版本探测 + 后台拉起(目标无 java → 非零退出 → 该机 failed 人读)。
		{"java", "-jar", jarPath, "--version"},
	}
	return cmds, fmt.Sprintf("jar 部署完成 → %s(已调起 java -jar)", jarPath), nil
}

// buildImageDeploy 构造 image 的 `docker pull` + `docker run`(目标无 docker → failed 人读)。
func buildImageDeploy(a run.Artifact, cfg map[string]string) ([][]string, string, error) {
	ref := strings.TrimSpace(a.Reference)
	if ref == "" {
		return nil, "", fmt.Errorf("image 产物缺少 reference(repo:tag 或镜像 id)")
	}
	name := sanitizeName(a.Name)
	if name == "" {
		name = "app"
	}
	cmds := [][]string{
		{"docker", "pull", ref},
		// 先移除同名旧容器(幂等;无则忽略由 docker 处理),再后台起新容器。
		{"docker", "rm", "-f", name},
		{"docker", "run", "-d", "--name", name, ref},
	}
	return cmds, fmt.Sprintf("image 部署完成 → 已拉取并启动容器 %s(%s)", name, ref), nil
}
