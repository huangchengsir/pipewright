package releasesource

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"os"
	"strings"
)

type Settings struct {
	db      *sql.DB
	catalog *Catalog
}
type SettingsView struct {
	Source   ID           `json:"source"`
	Revision int64        `json:"revision"`
	Sources  []Descriptor `json:"sources"`
}
type SettingsInput struct {
	Source           ID    `json:"source"`
	ExpectedRevision int64 `json:"expectedRevision"`
}

func NewSettings(db *sql.DB, catalog *Catalog) *Settings { return &Settings{db: db, catalog: catalog} }
func (s *Settings) Get(ctx context.Context) (SettingsView, error) {
	if s == nil || s.db == nil || s.catalog == nil {
		return SettingsView{}, ErrUnavailable
	}
	out := SettingsView{Sources: s.catalog.Sources()}
	e := s.db.QueryRowContext(ctx, "SELECT source,revision FROM release_source_settings WHERE id=1").Scan(&out.Source, &out.Revision)
	if e != nil || !ValidID(out.Source) {
		return SettingsView{}, ErrUnavailable
	}
	return out, nil
}
func (s *Settings) Preferred(ctx context.Context) (ID, error) {
	out, e := s.Get(ctx)
	return out.Source, e
}
func (s *Settings) Save(ctx context.Context, in SettingsInput) (SettingsView, error) {
	if s == nil || s.db == nil || s.catalog == nil {
		return SettingsView{}, ErrUnavailable
	}
	if !ValidID(in.Source) || in.ExpectedRevision < 1 {
		return SettingsView{}, ErrInvalid
	}
	if _, e := s.catalog.Resolve(in.Source); e != nil {
		return SettingsView{}, e
	}
	out := SettingsView{Source: in.Source, Revision: in.ExpectedRevision + 1, Sources: s.catalog.Sources()}
	e := configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		r, e := q.ExecContext(ctx, "UPDATE release_source_settings SET source=?,revision=revision+1 WHERE id=1 AND revision=?", in.Source, in.ExpectedRevision)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
	if e != nil {
		if errors.Is(e, ErrConflict) {
			return SettingsView{}, ErrConflict
		}
		return SettingsView{}, ErrUnavailable
	}
	return out, nil
}

// InitializeFromEnvironment is a one-time installation preference. Once any
// choice is saved (including an explicit GitHub choice), the database wins.
func (s *Settings) InitializeFromEnvironment(ctx context.Context) error {
	selected := ID(strings.TrimSpace(os.Getenv("PIPEWRIGHT_UPDATE_SOURCE")))
	if selected == "" || selected == GitHub {
		return nil
	}
	if !ValidID(selected) {
		return ErrInvalid
	}
	if s == nil || s.db == nil || s.catalog == nil {
		return ErrUnavailable
	}
	if _, e := s.catalog.Resolve(selected); e != nil {
		return e
	}
	_, e := s.db.ExecContext(ctx, "UPDATE release_source_settings SET source=?,revision=2 WHERE id=1 AND revision=1 AND source='github'", selected)
	return e
}
