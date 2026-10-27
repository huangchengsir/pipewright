package mask

import (
	"context"
	"testing"
)

func TestOperationRegistrarMasksExactShortSecretsAndRemainsScoped(t *testing.T) {
	left, right := NewMasker(), NewMasker()
	first := WithSecretRegistrar(context.Background(), left.RegisterExactSecret)
	second := WithSecretRegistrar(context.Background(), right.RegisterExactSecret)
	child, cancel := context.WithCancel(first)
	defer cancel()
	RegisterContextSecret(child, "xy")
	RegisterContextSecret(second, "second-credential")
	if left.Scrub("xy") != Placeholder || right.Scrub("xy") != "xy" {
		t.Fatal("known short credential was exposed or registered to another run")
	}
	if left.Scrub("second-credential") != "second-credential" || right.Scrub("second-credential") != Placeholder {
		t.Fatal("operation secret registration crossed context ownership")
	}
	RegisterContextSecret(context.Background(), "unbound-credential")
}
