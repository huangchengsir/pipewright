package deployattempt

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func seedLedger(t *testing.T, st *store.Store) *Store {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','c','git_token','',X'00','m','old','old')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','p','https://example.invalid/p','main','c','old','old')`,
		`INSERT INTO project_config_versions(project_id,revision) VALUES('p',7)`,
		`INSERT INTO pipeline_runs(id,project_id,status,resolved_environment,created_at) VALUES('r','p','success','prod','old')`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return New(st.DB)
}

func targetInput() TargetInput {
	return TargetInput{ArtifactID: "a", ArtifactName: "app", ServerID: "s", ServerName: "server", ContainerName: "app", ConnectionFingerprint: strings.Repeat("f", 64), ExpectedImage: ExpectedImage{Reference: "reg.invalid/app@sha256:" + strings.Repeat("a", 64), Repository: "reg.invalid/app", Digest: "sha256:" + strings.Repeat("a", 64), DigestKind: DigestRepositoryUnknown}}
}

func TestReceiptHistoryAndRuntimeDigestTypes(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		s := seedLedger(t, st)
		ctx := context.Background()
		a, err := s.Begin(ctx, BeginInput{RunID: "r", StageID: "deploy-job", Initiator: "stage", ResolvedFullSHA: strings.Repeat("d", 40)})
		if err != nil {
			t.Fatal(err)
		}
		if a.Source.ProjectID != "p" || a.Source.Environment != "prod" || a.Source.ConfigRevision != 7 {
			t.Fatal(a.Source)
		}
		target, err := s.StartTarget(ctx, a.ID, targetInput())
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range []Receipt{{Kind: ActivationStarted}, {Kind: Activated, ActualImage: ActualImage{ContainerID: strings.Repeat("1", 64)}}, {Kind: Succeeded, ActualImage: ActualImage{ContainerID: strings.Repeat("1", 64)}, HealthGate: "not_run"}, {Kind: IdentityObserved, ActualImage: ActualImage{ContainerID: strings.Repeat("1", 64), ConfigID: "sha256:" + strings.Repeat("b", 64), RepoDigests: []string{"reg.invalid/app@sha256:" + strings.Repeat("a", 64)}, Platform: Platform{OS: "linux", Architecture: "amd64"}}}, {Kind: RolledBack, RollbackContainerID: strings.Repeat("2", 64)}} {
			if _, err = s.AppendReceipt(ctx, target.ID, r); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Get(ctx, a.ID)
		if err != nil || len(got.Targets) != 1 {
			t.Fatal(got, err)
		}
		tr := got.Targets[0]
		if tr.Outcome != RolledBack || len(tr.Receipts) != 5 || tr.Receipts[1].ActualImage.ContainerID != strings.Repeat("1", 64) || tr.Receipts[4].RollbackContainerID != strings.Repeat("2", 64) {
			t.Fatal(tr)
		}
		if tr.ExpectedImage.DigestKind != DigestRepositoryUnknown || tr.Receipts[3].ActualImage.ConfigID == tr.ExpectedImage.Digest || tr.Receipts[3].ActualImage.ManifestMappingVerified {
			t.Fatal("guessed digest relationship", tr)
		}
		for i, r := range tr.Receipts {
			if r.Sequence != int64(i+1) || r.ID == "" || r.TargetID != target.ID || r.ActualImage.RepoDigests == nil {
				t.Fatal(r)
			}
		}
		if _, err = s.AppendReceipt(ctx, target.ID, Receipt{Kind: Succeeded, ActualImage: ActualImage{ContainerID: strings.Repeat("1", 64)}}); !errors.Is(err, ErrConflict) {
			t.Fatal("terminal overwritten", err)
		}
		other, err := s.Begin(ctx, BeginInput{RunID: "r", Initiator: "retry"})
		if err != nil || other.ID == a.ID {
			t.Fatal(other, err)
		}
		second, err := s.StartTarget(ctx, other.ID, targetInput())
		if err != nil {
			t.Fatal(err)
		}
		if second.ID == target.ID || second.WorkloadKey != target.WorkloadKey {
			t.Fatal(second)
		}
		history, err := s.ListByWorkload(ctx, target.WorkloadKey, 5)
		if err != nil || len(history) != 2 {
			t.Fatal(history, err)
		}
		all, err := s.ListByRun(ctx, "r")
		if err != nil || len(all) != 2 {
			t.Fatal(all, err)
		}
	})
}

func TestRecoveryNoReplayAndSourceRetention(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		s := seedLedger(t, st)
		ctx := context.Background()
		a, err := s.Begin(ctx, BeginInput{RunID: "r"})
		if err != nil {
			t.Fatal(err)
		}
		tr, err := s.StartTarget(ctx, a.ID, targetInput())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.AppendReceipt(ctx, tr.ID, Receipt{Kind: ActivationStarted}); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.Exec(`DELETE FROM pipeline_runs WHERE id='r'`); err != nil {
			t.Fatal(err)
		}
		if err = s.RecoverInterrupted(ctx); err != nil {
			t.Fatal(err)
		}
		if err = s.RecoverInterrupted(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTarget(ctx, tr.ID)
		if err != nil || got.Outcome != OutcomeUnknown || len(got.Receipts) != 2 || got.Source.Environment != "prod" {
			t.Fatal(got, err)
		}
		if _, err = s.AppendReceipt(ctx, tr.ID, Receipt{Kind: Activated, ActualImage: ActualImage{ContainerID: strings.Repeat("1", 64)}}); !errors.Is(err, ErrConflict) {
			t.Fatal("recovered execution resumed", err)
		}
	})
}

func TestEmptyEnvironmentAndJSONArrays(t *testing.T) {
	st := storetest.Open(t)
	s := seedLedger(t, st)
	ctx := context.Background()
	if _, err := st.DB.Exec(`UPDATE pipeline_runs SET resolved_environment='' WHERE id='r'`); err != nil {
		t.Fatal(err)
	}
	a, err := s.Begin(ctx, BeginInput{RunID: "r"})
	if err != nil || a.Source.Environment != "" {
		t.Fatal(a, err)
	}
	b, _ := json.Marshal(a)
	if !strings.Contains(string(b), `"targets":[]`) {
		t.Fatal(string(b))
	}
	tr, err := s.StartTarget(ctx, a.ID, targetInput())
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(tr)
	if !strings.Contains(string(b), `"receipts":[]`) || !strings.Contains(string(b), `"identityTrust":"host_identity_unverified"`) {
		t.Fatal(string(b))
	}
	if WorkloadKey("p", "", "s", "app") == WorkloadKey("p", "prod", "s", "app") || WorkloadKey("p", "prod", "s", "app") == WorkloadKey("p", "prod", "s", "db") {
		t.Fatal("workload roles/environments collapsed")
	}
}

func TestInvalidIdentityAndMissingSourcesRejected(t *testing.T) {
	st := storetest.Open(t)
	s := seedLedger(t, st)
	ctx := context.Background()
	if _, err := s.Begin(ctx, BeginInput{RunID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Begin(ctx, BeginInput{RunID: "r", ResolvedFullSHA: "deadbee"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	a, _ := s.Begin(ctx, BeginInput{RunID: "r"})
	bad := targetInput()
	bad.ExpectedImage.DigestKind = DigestConfig
	bad.ExpectedImage.Digest = "tag"
	if _, err := s.StartTarget(ctx, a.ID, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	tr, _ := s.StartTarget(ctx, a.ID, targetInput())
	if _, err := s.AppendReceipt(ctx, tr.ID, Receipt{Kind: Activated, ActualImage: ActualImage{ContainerID: "short"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.AppendReceipt(ctx, tr.ID, Receipt{Kind: Activated, ActualImage: ActualImage{ManifestMappingVerified: true}}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
