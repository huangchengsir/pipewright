package build

import (
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

func TestRegistryRevealActualVaultCompatibility(t *testing.T) {
	db := storetest.OpenDB(t)
	key := [32]byte{1, 2, 3}
	v := vault.New(db, &key)
	b := &Builder{vault: v}
	for _, input := range []vault.CreateInput{
		{Name: "explicit", Type: vault.TypeRegistry, Username: "alice", Secret: " pass:with:colons "},
		{Name: "legacy", Type: vault.TypeRegistry, Secret: "alice: pass:with:colons "},
		{Name: "token", Type: vault.TypeRegistry, Secret: "token-only"},
		{Name: "explicit-token", Type: vault.TypeRegistry, UsernameProvided: true, Secret: "private:token"},
	} {
		c, err := v.Create(input)
		if err != nil {
			t.Fatal(err)
		}
		user, password := b.revealRegistryCred(c.ID)
		if input.Name == "token" {
			if user != "" || password != "token-only" {
				t.Fatal(user, password)
			}
			continue
		}
		if input.Name == "explicit-token" {
			if user != "" || password != "private:token" {
				t.Fatal(user, password)
			}
			continue
		}
		if user != "alice" || password != " pass:with:colons " {
			t.Fatal(user, password)
		}
		empty := ""
		if _, err := v.Update(c.ID, vault.UpdateInput{Username: &empty}); err != nil {
			t.Fatal(err)
		}
		user, password = b.revealRegistryCred(c.ID)
		if user != "" || password != " pass:with:colons " {
			t.Fatal("token-only update changed the consumer contract", user, password)
		}
	}
}
