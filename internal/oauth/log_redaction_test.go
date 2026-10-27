package oauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type redactionProjects struct {
	project.Service
	value *project.Project
}

func (p redactionProjects) Get(context.Context, string) (*project.Project, error) {
	return p.value, nil
}

type redactionSettings struct {
	pipeline.SettingsService
	value *pipeline.Settings
}

func (p redactionSettings) Get(context.Context, string) (*pipeline.Settings, error) {
	return p.value, nil
}

type redactionClone struct{}

func (redactionClone) Clone(_ context.Context, _ string, _ string, token string, _ string, _ string, _ string) (*build.CloneResolved, error) {
	if token != "fixture-access-2" {
		return nil, errors.New("clone did not receive the first rotated generation")
	}
	return &build.CloneResolved{CommitFull: strings.Repeat("a", 40), CommitShort: strings.Repeat("a", 7)}, nil
}

type redactionDriver struct {
	build.Driver
	expected            string
	afterSecondRotation bool
}

func (d redactionDriver) Binary() string { return "synthetic-driver" }
func (d redactionDriver) Build(_ context.Context, _ string, _ string, _ string, _ []string, secret []string, onLine func(string, string)) (int, error) {
	if len(secret) != 1 || secret[0] != "TOKEN="+d.expected {
		return -1, errors.New("secret dispatch received an unexpected generation")
	}
	line := "echo fixture-access-1 fixture-access-2"
	if d.afterSecondRotation {
		line += " fixture-access-3"
	}
	onLine("stdout", line)
	return 0, nil
}
func (redactionDriver) InspectImage(context.Context, string) (string, int64, error) {
	return "sha256:" + strings.Repeat("a", 64), 1, nil
}

type redactionSink struct {
	run.StepSink
	afterClone func(context.Context) error
}

func (s redactionSink) StepDone(ctx context.Context, ordinal int, status string) error {
	if ordinal == 0 && s.afterClone != nil {
		if err := s.afterClone(ctx); err != nil {
			return err
		}
	}
	return s.StepSink.StepDone(ctx, ordinal, status)
}

type redactionRunner struct {
	builder    *build.Builder
	afterClone func(context.Context) error
}

func (r redactionRunner) Run(ctx context.Context, value *run.Run, sink run.StepSink) error {
	return r.builder.Run(ctx, value, redactionSink{StepSink: sink, afterClone: r.afterClone})
}

// This uses the real OAuth protocol fixture, Builder secret resolution,
// WorkerPool, SQL StepSink, and the same event bus consumed by SSE. The container
// driver is synthetic: no Docker, production provider, or external model call.
func TestManagedOAuthRotatedBuildSecretsAreMaskedInSQLAndSSE(t *testing.T) {
	for _, second := range []bool{false, true} {
		name := "clone_refresh"
		if second {
			name = "rotation_after_clone_release"
		}
		t.Run(name, func(t *testing.T) {
			f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
			// The pre-run catalog still contains generation 1; only the first
			// scoped clone may exchange it at the declared refresh threshold.
			f.now.Add(61000)
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := f.db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('redaction-project','redaction-project','https://fixture.test/repo.git','main',?,?,?)`, f.id, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			settings := &pipeline.Settings{Build: pipeline.BuildConfig{Model: pipeline.BuildModelDockerfile, ArtifactType: pipeline.ArtifactImage, Vars: []pipeline.BuildVar{{Key: "TOKEN", Secret: true, CredentialID: f.id}}}}
			expected := "fixture-access-2"
			if second {
				expected = "fixture-access-3"
			}
			builder, err := build.NewBuilder(redactionProjects{value: &project.Project{ID: "redaction-project", Name: "redaction-project", RepoURL: "https://fixture.test/repo.git", CredentialID: f.id}}, redactionSettings{value: settings}, f.managed, build.WithCloner(redactionClone{}), build.WithDriver(redactionDriver{expected: expected, afterSecondRotation: second}), build.WithImageGC(false))
			if err != nil {
				t.Fatal(err)
			}
			var afterClone func(context.Context) error
			if second {
				afterClone = func(context.Context) error {
					var uses int
					if err := f.db.QueryRow("SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=?", f.id).Scan(&uses); err != nil {
						return err
					}
					if uses != 0 {
						return errors.New("clone generation had not actually released")
					}
					f.mode.Store("third")
					f.now.Add(61000)
					// Another operation rotates after the clone releases. Its background
					// context has no run registrar: the subsequent BuildVar Reveal must
					// register generation 3 itself, before dispatching the environment.
					_, release, err := vault.AcquireGitAuth(context.Background(), f.managed, f.id)
					if err != nil {
						return err
					}
					release()
					return nil
				}
			}
			svc := run.New(f.db)
			pool := run.NewWorkerPool(svc, run.WithConcurrency(1), run.WithRunner(redactionRunner{builder: builder, afterClone: afterClone}), run.WithLogMaskerFunc(func(ctx context.Context, _ string) *mask.Masker {
				m := mask.NewMasker()
				if err := vault.RegisterMaskSecrets(ctx, f.managed, m); err != nil {
					t.Error(err)
				}
				return m
			}))
			value, err := svc.Create(ctx(), "redaction-project", run.Trigger{Type: run.TriggerManual, Branch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			events, cancel := pool.Subscribe(value.ID)
			defer cancel()
			pool.Start()
			defer pool.Stop(context.Background())
			deadline := time.NewTimer(8 * time.Second)
			defer deadline.Stop()
			sawMasked := false
		wait:
			for {
				select {
				case event := <-events:
					if event.Kind == run.EventLog {
						if strings.Contains(event.Log.Text, "fixture-access-") {
							t.Fatal("SSE event contained a rotated credential")
						}
						if strings.Count(event.Log.Text, mask.Placeholder) >= 2 {
							sawMasked = true
						}
					}
					if event.Kind == run.EventStatus && (event.Status == run.StatusSuccess || event.Status == run.StatusFailed) {
						if event.Status != run.StatusSuccess {
							t.Fatal("synthetic build failed")
						}
						break wait
					}
				case <-deadline.C:
					t.Fatal("local secret-redaction run did not complete")
				}
			}
			if !sawMasked {
				t.Fatal("no redacted runtime log reached the SSE event bus")
			}
			rows, err := svc.GetLogs(ctx(), value.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range rows {
				if strings.Contains(line.Text, "fixture-access-") {
					t.Fatal("persisted runtime log contained a rotated credential")
				}
			}
			var leaked int
			if err := f.db.QueryRow("SELECT COUNT(*) FROM run_logs WHERE run_id=? AND `text` LIKE ?", value.ID, "%fixture-access-%").Scan(&leaked); err != nil || leaked != 0 {
				t.Fatal("raw SQL log boundary leaked a token", err)
			}
			want := int32(1)
			if second {
				want = 2
			}
			if f.refreshes.Load() != want {
				t.Fatal("unexpected number of real protocol refresh requests")
			}
		})
	}
}
