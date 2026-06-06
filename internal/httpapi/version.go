package httpapi

import (
	"net/http"

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
