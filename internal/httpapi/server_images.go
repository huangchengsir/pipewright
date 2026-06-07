package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 镜像管理(Portainer 式):列表 / 拉取 / 删除。经 SSH 跑 docker,目标机零侵入。
//
// AC-SEC-02:列表是静态只读命令;拉取/删除的镜像引用经严格白名单校验(首字符非 `-` 防 flag
// 注入、无 shell 元字符),命令 array 化不拼 shell。写操作过 CSRF + 审计。

const (
	imagesCmdTimeout = 20 * time.Second // 列表/删除:只读或快操作
	imagePullTimeout = 8 * time.Minute  // 拉取:大镜像 + 慢网络可能很久,给充裕超时
	imagesOutMax     = 2 << 20
)

// reImageRef 已在 container_create.go 定义(镜像引用白名单)。删除可用镜像 ID(sha256 短/长)
// 或 repo:tag —— 同一套 reImageRef 即可覆盖(十六进制 ID 也匹配 [A-Za-z0-9._/:@-])。

var cmdImagesList = []string{"docker", "images", "--format", "{{json .}}"}

// imageDTO 是单个镜像的展示 DTO(冻结契约)。
type imageDTO struct {
	ID           string `json:"id"`           // 镜像 ID(短)
	Repository   string `json:"repository"`   // 仓库名(<none> 表示悬空)
	Tag          string `json:"tag"`          // 标签
	Size         string `json:"size"`         // 人读大小(如 "142MB")
	CreatedSince string `json:"createdSince"` // 人读创建时长(如 "3 weeks ago")
}

// serverImagesDTO 是单台服务器镜像清单响应体(冻结契约)。
type serverImagesDTO struct {
	ServerID    string     `json:"serverId"`
	Reachable   bool       `json:"reachable"`
	Runtime     string     `json:"runtime"`
	Error       string     `json:"error"`
	Images      []imageDTO `json:"images"`
	CollectedAt string     `json:"collectedAt"`
}

type dockerImageLine struct {
	ID           string `json:"ID"`
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	Size         string `json:"Size"`
	CreatedSince string `json:"CreatedSince"`
}

func parseImages(out string) []imageDTO {
	list := []imageDTO{}
	if len(out) > imagesOutMax {
		out = out[:imagesOutMax]
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p dockerImageLine
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue
		}
		list = append(list, imageDTO{
			ID:           p.ID,
			Repository:   p.Repository,
			Tag:          p.Tag,
			Size:         p.Size,
			CreatedSince: p.CreatedSince,
		})
	}
	return list
}

func collectServerImages(ctx context.Context, svc target.Service, id string) (serverImagesDTO, error) {
	out := serverImagesDTO{ServerID: id, Images: []imageDTO{}, CollectedAt: time.Now().UTC().Format(time.RFC3339)}
	cctx, cancel := context.WithTimeout(ctx, imagesCmdTimeout)
	defer cancel()

	res, err := svc.Exec(cctx, id, cmdImagesList)
	if err != nil {
		out.Reachable = false
		out.Error = humanContainersError(err)
		if isLocateError(err) {
			return out, err
		}
		return out, nil
	}
	out.Reachable = true
	if res.ExitCode != 0 {
		out.Error = "未检测到容器运行时(docker 未安装或当前用户无权限)"
		return out, nil
	}
	out.Runtime = "docker"
	out.Images = parseImages(res.Stdout)
	return out, nil
}

// makeServerImagesHandler 返回 GET /api/servers/{id}/images(认证,只读)。
func makeServerImagesHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}
		out, locErr := collectServerImages(r.Context(), svc, id)
		if locErr != nil {
			writeServerError(w, locErr)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// imageActionRequest 是拉取/删除请求体。
type imageActionRequest struct {
	Image string `json:"image"`           // pull/rm 的镜像引用(repo:tag 或 ID)
	Force bool   `json:"force,omitempty"` // rm 是否 -f
}

type imageActionDTO struct {
	ServerID string `json:"serverId"`
	Action   string `json:"action"`
	Image    string `json:"image"`
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	Error    string `json:"error"`
}

var reImageActionRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

func validateImageActionRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("镜像不能为空")
	}
	if len(ref) > 256 || !reImageActionRef.MatchString(ref) {
		return errors.New("非法镜像引用")
	}
	return nil
}

// makeImagePullHandler 返回 POST /api/servers/{id}/images/pull(认证 + CSRF;写)。
func makeImagePullHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return imageWriteHandler(svc, aud, "pull", imagePullTimeout, func(ref string, _ bool) []string {
		return []string{"docker", "pull", ref}
	})
}

// makeImageRemoveHandler 返回 POST /api/servers/{id}/images/remove(认证 + CSRF;写)。
func makeImageRemoveHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return imageWriteHandler(svc, aud, "rm", imagesCmdTimeout, func(ref string, force bool) []string {
		if force {
			return []string{"docker", "rmi", "-f", ref}
		}
		return []string{"docker", "rmi", ref}
	})
}

// imageWriteHandler 抽出拉取/删除共用骨架:校验 → array 化命令 → Exec → 审计 → 200/ok。
func imageWriteHandler(svc target.Service, aud audit.Recorder, action string, timeout time.Duration, build func(ref string, force bool) []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req imageActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if err := validateImageActionRef(req.Image); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_image_ref", "镜像引用非法:"+err.Error())
			return
		}
		req.Image = strings.TrimSpace(req.Image)
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		out := imageActionDTO{ServerID: id, Action: action, Image: req.Image}
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor:      auditActor,
				Action:     audit.ActionImageOp,
				TargetType: audit.TargetServer,
				TargetID:   id,
				Detail:     map[string]any{"action": action, "image": req.Image, "ok": ok},
				IP:         clientIP(r),
			})
		}

		cctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		res, err := svc.Exec(cctx, id, build(req.Image, req.Force))
		if err != nil {
			if errors.Is(err, target.ErrNotFound) {
				writeServerError(w, err)
				return
			}
			if errors.Is(err, target.ErrCredentialNotFound) || errors.Is(err, target.ErrVaultUnconfigured) {
				auditOp(false)
				writeServerError(w, err)
				return
			}
			out.OK = false
			out.Error = humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = "docker " + action + " 以非零状态退出"
			}
			out.OK = false
			out.Error = truncateLog(msg, 1024)
			out.Output = truncateLog(strings.TrimSpace(res.Stdout), 2048)
		} else {
			out.OK = true
			out.Output = truncateLog(strings.TrimSpace(res.Stdout), 2048)
		}
		auditOp(out.OK)
		writeJSON(w, http.StatusOK, out)
	}
}
