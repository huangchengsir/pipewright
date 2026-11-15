package previewenv

import (
	"context"
	"testing"
)

func TestExecutionVersionPinsConfigAndProviderSecretGeneration(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	seedTenantPreviewResources(t, svc.store.db)
	ctx := context.Background()
	before, e := ExecutionVersion(ctx, svc.store.db, "p1")
	if e != nil {
		t.Fatal(e)
	}
	enablePreview(t, svc, "p1")
	inputs, e := PlanInputs(ctx, svc.store.db, "p1", 1)
	if e != nil || !inputs.Enabled || inputs.Version == before || inputs.CredentialID != "preview-fixture" {
		t.Fatal(inputs, e)
	}
	if _, e = svc.store.db.Exec(`UPDATE credentials SET ciphertext=? WHERE id='preview-fixture'`, []byte{1}); e != nil {
		t.Fatal(e)
	}
	rotated, e := ExecutionVersion(ctx, svc.store.db, "p1")
	if e != nil || rotated == inputs.Version {
		t.Fatal("secret generation not pinned", e)
	}
	if _, e = svc.SetConfig(ctx, Config{ProjectID: "p1", Enabled: false}); e != nil {
		t.Fatal(e)
	}
	disabled, e := ExecutionVersion(ctx, svc.store.db, "p1")
	if e != nil || disabled == rotated {
		t.Fatal("disable not pinned", e)
	}
}
