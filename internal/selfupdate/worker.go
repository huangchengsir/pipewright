package selfupdate

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"os"
	"path/filepath"
)

func (s *Service) work(t Task) {
	ctx, cancel := context.WithTimeout(s.ctx, s.opts.TotalTimeout)
	defer cancel()
	if e := s.transition(&t, "checking", ""); e != nil {
		return
	}
	if t.SHA256 == "" {
		sum, e := s.checksum(ctx, t)
		if e != nil {
			s.transition(&t, "interrupted", "checksum_unavailable")
			return
		}
		t.SHA256 = sum
		if e = s.transition(&t, "checking", ""); e != nil {
			return
		}
	}
	if e := s.transition(&t, "downloading", ""); e != nil {
		return
	}
	if e := s.download(ctx, &t); e != nil {
		s.transition(&t, "interrupted", "download_interrupted")
		return
	}
	if e := s.transition(&t, "verifying", ""); e != nil {
		return
	}
	binary, digest, e := s.extract(ctx, &t)
	if e != nil {
		s.transition(&t, "failed", "verification_failed")
		return
	}
	t.BinarySHA256 = digest
	if e = s.transition(&t, "extracting", ""); e != nil {
		return
	}
	// The candidate is reverified on every explicit continue; an old ready card
	// never grants a fresh, unbound installation.
	if e = maintenance.Fence(ctx, s.db, t.ID); e != nil {
		if errors.Is(e, maintenance.ErrBlocked) || errors.Is(e, configstate.ErrMaintenance) {
			var blocked *maintenance.BlockedError
			if errors.As(e, &blocked) {
				t.Blockers = blocked.Blockers
			}
			s.transition(&t, "ready_blocked", "active_work")
		} else {
			s.transition(&t, "ready_blocked", "readiness_unavailable")
		}
		return
	}
	t.Blockers = nil
	base, e := fileDigest(s.opts.Executable)
	if e != nil || base != t.BaseSHA256 {
		_ = s.finish(context.Background(), &t, "failed", "installation_target_changed")
		return
	}
	if e = s.transition(&t, "installing", ""); e != nil {
		return
	} // preserve fence on uncertain persistence
	if e = ctx.Err(); e != nil {
		s.transition(&t, "unknown", "installation_not_confirmed")
		return
	}
	got, e := fileDigest(filepath.Join(s.opts.Directory, binary))
	if e != nil || got != t.BinarySHA256 {
		s.transition(&t, "unknown", "candidate_changed")
		return
	}
	if e = os.Chmod(filepath.Join(s.opts.Directory, binary), 0755); e != nil {
		s.transition(&t, "unknown", "installation_not_confirmed")
		return
	}
	if e = s.opts.Install(filepath.Join(s.opts.Directory, binary), s.opts.Executable); e != nil {
		s.transition(&t, "unknown", "installation_not_confirmed")
		return
	}
	if e = s.transition(&t, "restarting", ""); e != nil {
		return
	}
	if s.opts.RequestRestart == nil || s.opts.RequestRestart() != nil {
		s.transition(&t, "restart_required", "restart_required")
	}
}
