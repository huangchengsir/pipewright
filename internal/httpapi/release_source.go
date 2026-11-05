package httpapi

import (
	"errors"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/releasesource"
	"net/http"
)

func releaseSourceError(w http.ResponseWriter, e error) {
	status, code := 503, "source_unavailable"
	if errors.Is(e, releasesource.ErrConflict) {
		status, code = 409, "source_settings_conflict"
	}
	if errors.Is(e, releasesource.ErrInvalid) {
		status, code = 400, "bad_request"
	}
	writeError(w, status, code, "升级任务暂不可用，请核对原任务状态")
}
func makeGetReleaseSourceSettingsHandler(s *releasesource.Settings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, e := s.Get(r.Context())
		if e != nil {
			releaseSourceError(w, e)
			return
		}
		writeJSON(w, 200, out)
	}
}
func makeSaveReleaseSourceSettingsHandler(s *releasesource.Settings, a audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in releasesource.SettingsInput
		if !decodeUpdate(w, r, &in) {
			return
		}
		out, e := s.Save(r.Context(), in)
		if e != nil {
			releaseSourceError(w, e)
			return
		}
		if a != nil {
			_ = a.Record(r.Context(), audit.Entry{Actor: auditActor, Action: "release_source_settings_update", TargetType: "release_source_settings", TargetID: "1", Detail: map[string]any{"source": out.Source, "revision": out.Revision}})
		}
		writeJSON(w, 200, out)
	}
}
