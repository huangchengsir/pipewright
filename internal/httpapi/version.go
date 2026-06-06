package httpapi

import (
	"log"
	"net/http"
	"time"

	"github.com/huangchengsir/pipewright/internal/version"
)

// makeCheckUpdateHandler 处理 GET /api/version/check:查询 GitHub 最新发布并与当前版本比对。
// 检查失败(网络/限流)不返 5xx —— 而是 200 + UpdateInfo.CheckError,让前端稳定渲染降级态。
func makeCheckUpdateHandler(checker *version.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info := checker.Check(r.Context())
		writeJSON(w, http.StatusOK, info)
	}
}

// updateResult 是 POST /api/version/update 的返回。
type updateResult struct {
	Mode    string `json:"mode"`              // binary | docker
	Status  string `json:"status"`            // restarting | manual | uptodate | error
	From    string `json:"from"`              // 当前版本
	To      string `json:"to"`                // 目标版本
	Message string `json:"message"`           // 给用户的说明
	Command string `json:"command,omitempty"` // docker 模式的升级命令
}

// makeSelfUpdateHandler 处理 POST /api/version/update:执行一键自动更新。
//
//   - binary 模式(裸机/install.sh):下载新版二进制 + 校验和核验 + 原子替换当前可执行文件,
//     回完响应后用新二进制 re-exec 自重启(同 PID 重新绑定端口,短暂不可用,前端轮询 /version 重连)。
//   - docker 模式:容器不能替换自身镜像,返回精确升级命令供用户执行。
//
// 串行化:同一时刻只允许一个更新在进行。
func makeSelfUpdateHandler(checker *version.Checker, inflight *updateGate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !inflight.tryAcquire() {
			writeJSON(w, http.StatusConflict, updateResult{Status: "error", Message: "已有更新正在进行"})
			return
		}
		// binary 成功路径会 re-exec(不返回),故仅在非成功路径释放。
		release := inflight.release

		info := checker.Check(r.Context())
		res := updateResult{From: info.Current, To: info.Latest}

		if info.CheckError != "" {
			release()
			res.Status, res.Message = "error", info.CheckError
			writeJSON(w, http.StatusBadGateway, res)
			return
		}
		if !info.UpdateAvailable {
			release()
			res.Status, res.Message = "uptodate", "已是最新版本"
			writeJSON(w, http.StatusOK, res)
			return
		}

		// Docker:不能自换镜像,给升级命令。
		if version.Mode() == version.ModeDocker {
			release()
			res.Mode, res.Status = "docker", "manual"
			res.Command = version.DockerUpgradeCommand()
			res.Message = "Docker 部署无法在容器内替换自身镜像。请在宿主执行升级命令(数据卷保留,新版自动迁移)。"
			writeJSON(w, http.StatusOK, res)
			return
		}

		res.Mode = "binary"
		if !version.CanSelfReplace() {
			release()
			res.Status = "manual"
			res.Message = "当前平台不支持替换运行中的程序,请手动下载新版替换。"
			writeJSON(w, http.StatusOK, res)
			return
		}

		// 下载 + 校验 + 替换二进制。失败则释放并报错(进程仍跑旧版,不影响服务)。
		if err := checker.ApplyBinaryUpdate(r.Context(), info.Latest); err != nil {
			release()
			res.Status, res.Message = "error", err.Error()
			writeJSON(w, http.StatusInternalServerError, res)
			return
		}

		// 替换成功:先把响应发回(并 flush),再延迟 re-exec,给前端时间进入"等待重启"态。
		res.Status = "restarting"
		res.Message = "新版本已就位,正在重启…"
		writeJSON(w, http.StatusOK, res)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		log.Printf("[update] %s → %s 已就位,即将 re-exec 重启", info.Current, info.Latest)
		go func() {
			time.Sleep(700 * time.Millisecond) // 让响应抵达客户端
			if err := version.Reexec(); err != nil {
				log.Printf("[update] re-exec 失败(新版已就位,请手动重启): %v", err)
				release()
			}
		}()
	}
}

// updateGate 串行化自更新:同一时刻仅一个进行中。
type updateGate struct {
	ch chan struct{}
}

func newUpdateGate() *updateGate {
	g := &updateGate{ch: make(chan struct{}, 1)}
	g.ch <- struct{}{}
	return g
}

func (g *updateGate) tryAcquire() bool {
	select {
	case <-g.ch:
		return true
	default:
		return false
	}
}

func (g *updateGate) release() {
	select {
	case g.ch <- struct{}{}:
	default:
	}
}
