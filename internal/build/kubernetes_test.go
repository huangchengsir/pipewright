package build

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"math/big"
	"strings"
	"testing"
	"time"
)

type kubernetesSourceExecution struct {
	in       runretry.Inputs
	snapshot runretry.Snapshot
}

func (e kubernetesSourceExecution) Snapshot() runretry.Snapshot { return e.snapshot }
func (e kubernetesSourceExecution) Node(string, string) (runretry.NodeRef, bool) {
	return runretry.NodeRef{}, false
}
func (e kubernetesSourceExecution) ControlNode(string, string) (runretry.NodeRef, bool) {
	return runretry.NodeRef{}, false
}
func (e kubernetesSourceExecution) Decision(runretry.NodeRef) runretry.Decision {
	return runretry.Decision{}
}
func (e kubernetesSourceExecution) Inputs(context.Context, runretry.NodeRef) (runretry.Inputs, error) {
	return e.in, nil
}
func (e kubernetesSourceExecution) Begin(context.Context, runretry.NodeRef) error { return nil }
func (e kubernetesSourceExecution) Finish(context.Context, runretry.NodeRef, runretry.Outcome) error {
	return nil
}
func (e kubernetesSourceExecution) BindCommit(context.Context, string) error { return nil }
func (e kubernetesSourceExecution) Control(context.Context, runretry.NodeRef, runretry.Outcome) error {
	return nil
}
func TestKubernetesSourceRequiresActualArtifactIdentity(t *testing.T) {
	metadata := map[string]any{"pushed": true, "sourceStageId": "build", "sourceJobId": "push", "declarationIndex": 0, "logicalNodeKey": "producer", "digestRepository": "registry.invalid/app/backend", "digest": "sha256:" + strings.Repeat("b", 64)}
	image := runretry.ImageRef{ArtifactID: "actual-id", ProducerAttemptID: "original", ProducerNodeKey: "producer", Metadata: metadata}
	ex := kubernetesSourceExecution{in: runretry.Inputs{Images: map[string]runretry.ImageRef{"producer": image}}, snapshot: runretry.Snapshot{CommitSHA: strings.Repeat("a", 40)}}
	artifact := run.Artifact{ID: "actual-id", RunID: "original", Type: run.ArtifactImage, Metadata: metadata}
	b := &Builder{artifactLister: func(context.Context, string) ([]run.Artifact, error) { return []run.Artifact{artifact}, nil }}
	index := 0
	selector := pipeline.KubernetesArtifactSource{StageID: "build", JobID: "push", DeclarationIndex: &index}
	source, ref, e := b.exactKubernetesImage(context.Background(), ex, runretry.NodeRef{}, selector)
	if e != nil || source.ArtifactID != "actual-id" || !strings.Contains(ref, "@sha256:") {
		t.Fatal(source, ref, e)
	}
	artifact.ID = "invented-id"
	if _, _, e = b.exactKubernetesImage(context.Background(), ex, runretry.NodeRef{}, selector); e == nil {
		t.Fatal("nonexistent checkpoint artifact accepted")
	}
	artifact.ID = "actual-id"
	artifact.RunID = "other-run"
	if _, _, e = b.exactKubernetesImage(context.Background(), ex, runretry.NodeRef{}, selector); e == nil {
		t.Fatal("wrong source run accepted")
	}
	artifact.RunID = "original"
	metadata["pushed"] = false
	if _, _, e = b.exactKubernetesImage(context.Background(), ex, runretry.NodeRef{}, selector); e == nil {
		t.Fatal("local image accepted")
	}
}
func TestKubernetesWithoutProtectedWiringFailsClosed(t *testing.T) {
	b := &Builder{}
	_, e := b.PrepareKubernetesStage(context.Background(), &run.Run{}, pipeline.Stage{Jobs: []pipeline.Job{{Type: pipeline.KubernetesJobType}}})
	if !errors.Is(e, runretry.ErrUnavailable) {
		t.Fatal(e)
	}
}

// Use real authorization and operation storage through the production builder
// option; no runtime or cluster is opened while preparing an approval.
type preparedKubernetesExecution struct {
	kubernetesSourceExecution
	node runretry.NodeRef
}

func (e preparedKubernetesExecution) Node(stage, job string) (runretry.NodeRef, bool) {
	return e.node, stage == e.node.ExecutionStageID && job == e.node.ExecutionJobID
}
func (e preparedKubernetesExecution) Decision(runretry.NodeRef) runretry.Decision {
	return runretry.Decision{Action: "execute"}
}
func TestKubernetesBuilderPreparesOwnedApprovalFromStoredPush(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenForDialect(t, store.SQLite)
	key := [32]byte{19}
	v := vault.New(db.DB, &key)
	legacy, err := v.Create(vault.CreateInput{Name: "fixture git", Type: vault.TypeGitToken, Secret: "fixture-only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES('p1','fixture','https://git.invalid/repo',?,'now','now')`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated test CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	domain := kubernetes.New(db.DB, v)
	cred, err := domain.SaveCredential(ctx, "", 0, kubernetes.CredentialInput{Name: "fixture", Mode: "server_token", Context: "fixture", Server: "https://cluster.invalid", CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Token: "fixture-only-token", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := domain.SaveConnection(ctx, "", 0, kubernetes.ConnectionInput{Name: "fixture", CredentialID: cred.ID, Enabled: true, Executors: []kubernetes.Executor{{Kind: "local"}}, Profile: kubernetes.ToolProfile{Protocol: kubernetes.ProfileProtocol, Image: "registry.invalid/tool@sha256:" + strings.Repeat("a", 64), Architecture: "amd64", KubectlPath: "/bin/kubectl", UID: 1000, Network: "bridge"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = domain.SaveGrant(ctx, conn.ID, "", 0, kubernetes.GrantInput{ProjectID: "p1", Environment: "test", Namespace: "app", Deployment: "backend", Containers: []string{"app"}, Executor: kubernetes.Executor{Kind: "local"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	stage := pipeline.Stage{ID: "deploy", Kind: "deploy", Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "kube", Type: pipeline.KubernetesJobType, Config: map[string]any{"connectionId": conn.ID, "credentialId": cred.ID, "context": "fixture", "namespace": "app", "deployment": "backend", "container": "app", "executor": `{"kind":"local"}`, "artifactSource": `{"stageId":"build","jobId":"push","declarationIndex":0}`}}}}
	snap := runretry.Snapshot{ProjectID: "p1", Trigger: runretry.Trigger{Environment: "test"}, CommitSHA: strings.Repeat("c", 40), Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "build", Kind: "build", Jobs: []pipeline.Job{{ID: "push", Type: "push_image"}}}, stage}}}
	if err = FreezeKubernetesPolicy(ctx, domain, &snap); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"pushed": true, "sourceStageId": "build", "sourceJobId": "push", "declarationIndex": 0, "logicalNodeKey": "push-node", "digestRepository": "registry.invalid/app", "digest": "sha256:" + strings.Repeat("b", 64)}
	artifact := run.Artifact{ID: "push-artifact", RunID: "root", Type: run.ArtifactImage, Metadata: metadata}
	ex := preparedKubernetesExecution{kubernetesSourceExecution: kubernetesSourceExecution{snapshot: snap, in: runretry.Inputs{Images: map[string]runretry.ImageRef{"push": {ArtifactID: artifact.ID, ProducerAttemptID: "root", ProducerNodeKey: "push-node", Metadata: metadata}}}}, node: runretry.NodeRef{Key: "kube-node", ExecutionStageID: "deploy", ExecutionJobID: "kube"}}
	ctx = runretry.WithExecution(ctx, ex)
	ctx = run.WithSourceContext(ctx, run.SourceContext{RefName: "main", CommitSHA: snap.CommitSHA, MetadataReady: true})
	b := &Builder{artifactLister: func(context.Context, string) ([]run.Artifact, error) { return []run.Artifact{artifact}, nil }}
	WithKubernetes(domain, kubernetes.NewController(domain, nil, "process_fixture"), "process_fixture")(b)
	prepared, err := b.PrepareKubernetesStage(ctx, &run.Run{ID: "root"}, snap.Spec.Stages[1])
	if err != nil {
		t.Fatal(err)
	}
	if source, present := run.SourceFromContext(prepared.Context); !present || source.CommitSHA != snap.CommitSHA {
		t.Fatal("Kubernetes preparation lost the Git source context")
	}
	if !prepared.RequireGate || prepared.Approve == nil || prepared.Context == nil {
		t.Fatal("approval boundary not prepared")
	}
	ops, err := domain.ListOperations(ctx, "root")
	if err != nil || len(ops) != 1 {
		t.Fatalf("operation not persisted: %v count=%d", err, len(ops))
	}
	if ops[0].OwnerEpoch != "process_fixture" || ops[0].ApprovedHash != "" || ops[0].Intent.Source.ArtifactID != artifact.ID || ops[0].Intent.Source.Commit != snap.CommitSHA {
		t.Fatal("intent was not bound to the current owner and stored push")
	}
	if err = prepared.Approve(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := domain.GetOperation(ctx, ops[0].ID)
	if err != nil || op.ApprovedHash != op.IntentHash {
		t.Fatal("approval was not bound to the persisted intent", err)
	}
	if err = prepared.Close(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = domain.GetOperation(ctx, ops[0].ID)
	if err != nil || !op.OwnerFenced || op.Business != kubernetes.Failed {
		t.Fatal("unexecuted approved intent not finalized", err)
	}
}
