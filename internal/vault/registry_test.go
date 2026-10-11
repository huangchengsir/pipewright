package vault

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestRegistryCanonicalCredentialsAndLegacyEdits(t *testing.T) {
	db := testDB(t)
	v := New(db, testKey())
	for _, input := range []CreateInput{
		{Name: "explicit", Type: TypeRegistry, Username: "alice", Secret: " p:a:ss "},
		{Name: "legacy", Type: TypeRegistry, Secret: "alice: p:a:ss "},
	} {
		c, err := v.Create(input)
		if err != nil {
			t.Fatal(err)
		}
		value, err := v.Reveal(c.ID)
		if err != nil || value != "alice: p:a:ss " || c.Username != "alice" || strings.Contains(c.MaskedValue, "p:a") {
			t.Fatal(c, value, err)
		}
		user := "bob"
		empty := ""
		c, err = v.Update(c.ID, UpdateInput{Username: &user, Secret: &empty})
		value, _ = v.Reveal(c.ID)
		if err != nil || value != "bob: p:a:ss " || c.Username != "bob" {
			t.Fatal(c, value, err)
		}
		password := "bob:new:password "
		c, err = v.Update(c.ID, UpdateInput{Username: &user, Secret: &password})
		value, _ = v.Reveal(c.ID)
		if err != nil || value != "bob:bob:new:password " {
			t.Fatal(value, err)
		}
		// Metadata-only edits cannot double-prefix the canonical value.
		name := "renamed"
		c, err = v.Update(c.ID, UpdateInput{Name: &name})
		value, _ = v.Reveal(c.ID)
		if err != nil || value != "bob:bob:new:password " {
			t.Fatal(value, err)
		}
		var raw []byte
		if err = db.QueryRow("SELECT ciphertext FROM credentials WHERE id=?", c.ID).Scan(&raw); err != nil || strings.Contains(string(raw), password) {
			t.Fatal("plaintext storage", err)
		}
	}
	token, err := v.Create(CreateInput{Name: "token", Type: TypeRegistry, Secret: "legacy-token-value"})
	if err != nil {
		t.Fatal(err)
	}
	token, err = v.Update(token.ID, UpdateInput{})
	value, _ := v.Reveal(token.ID)
	if err != nil || value != "legacy-token-value" || token.Username != "" {
		t.Fatal(token, value, err)
	}
	for _, username := range []string{"bad:user", "bad\nuser", "\tbad"} {
		if _, err = v.Create(CreateInput{Name: "bad", Type: TypeRegistry, Username: username, Secret: "password"}); err != ErrInvalidUsername {
			t.Fatal(err)
		}
	}
	// Simulate an old metadata-less record and an unsafe old persisted mask.
	old, _ := v.Create(CreateInput{Name: "old", Type: TypeRegistry, Secret: "legacy:colon:password"})
	db.Exec("UPDATE credentials SET username='', masked_value='colon:password' WHERE id=?", old.ID)
	list, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == old.ID && (c.Username != "legacy" || c.MaskedValue != "legacy "+maskDots) {
			t.Fatal(c)
		}
	}
}

func TestRegistryExplicitEmptyUsernamePreservesColonToken(t *testing.T) {
	v := New(testDB(t), testKey())
	c, err := v.Create(CreateInput{Name: "registry", Type: TypeRegistry, Username: "alice", Secret: "p:a:ss"})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	c, err = v.Update(c.ID, UpdateInput{Username: &empty})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := v.Reveal(c.ID)
	if c.Username != "" || value != ":p:a:ss" || strings.Contains(c.MaskedValue, "p:a") {
		t.Fatal("explicit token-only edit lost its meaning", c, value)
	}
	name := "renamed"
	c, err = v.Update(c.ID, UpdateInput{Name: &name})
	value, _ = v.Reveal(c.ID)
	if err != nil || c.Username != "" || value != ":p:a:ss" {
		t.Fatal("metadata edit reparsed the token", c, value, err)
	}
	user := "bob"
	c, err = v.Update(c.ID, UpdateInput{Username: &user})
	value, _ = v.Reveal(c.ID)
	if err != nil || c.Username != "bob" || value != "bob:p:a:ss" {
		t.Fatal("restored username changed the password", c, value, err)
	}
}

func TestRegistryCreateExplicitEmptyUsernameIsNotLegacyComposite(t *testing.T) {
	v := New(testDB(t), testKey())
	c, err := v.Create(CreateInput{Name: "token", Type: TypeRegistry, UsernameProvided: true, Secret: "private:token"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := v.Reveal(c.ID)
	if err != nil || c.Username != "" || value != ":private:token" || strings.Contains(c.MaskedValue, "private") {
		t.Fatal("explicit token exposed or changed its prefix", c, value, err)
	}
	list, err := v.List()
	if err != nil || len(list) != 1 || list[0].Username != "" || strings.Contains(list[0].MaskedValue, "private") {
		t.Fatal(list, err)
	}
	legacy, err := v.Create(CreateInput{Name: "legacy", Type: TypeRegistry, Secret: "bob:password"})
	if err != nil || legacy.Username != "bob" {
		t.Fatal("omitted username lost backwards compatibility", legacy, err)
	}
}

func TestRegistryConcurrentRenameAndRotationDoNotLoseUpdates(t *testing.T) {
	v := New(testDB(t), testKey())
	for i := 0; i < 48; i++ {
		c, err := v.Create(CreateInput{Name: "old", Type: TypeRegistry, Username: "alice", Secret: "old-password"})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var renameErr, rotateErr error
		name, user, password := "renamed", "alice", "new:password"
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, renameErr = v.Update(c.ID, UpdateInput{Name: &name})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, rotateErr = v.Update(c.ID, UpdateInput{Username: &user, Secret: &password})
		}()
		close(start)
		wg.Wait()
		for _, err := range []error{renameErr, rotateErr} {
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		}
		view, err := v.(*service).getView(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		value, err := v.Reveal(c.ID)
		if err != nil || (renameErr == nil && view.Name != name) || (rotateErr == nil && value != "alice:new:password") {
			t.Fatal("a successful update was overwritten", view, value, renameErr, rotateErr, err)
		}
	}
}

func TestRegistryLegacyMetadataDoesNotChangeLoginOnRename(t *testing.T) {
	db := testDB(t)
	v := New(db, testKey())
	for _, secret := range []string{"bob:password", "token-only"} {
		c, err := v.Create(CreateInput{Name: "old", Type: TypeRegistry, Secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE credentials SET username='alice' WHERE id=?", c.ID); err != nil {
			t.Fatal(err)
		}
		view, err := v.(*service).getView(c.ID)
		if err != nil || view.Username == "alice" {
			t.Fatal("legacy view returned an unused account", view, err)
		}
		name := "renamed"
		c, err = v.Update(c.ID, UpdateInput{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
		value, err := v.Reveal(c.ID)
		if err != nil || value != secret || c.Username == "alice" {
			t.Fatal("metadata-only edit changed legacy authentication", c, value, err)
		}
		c, err = v.Update(c.ID, UpdateInput{Name: &name, Username: &view.Username})
		value, revealErr := v.Reveal(c.ID)
		want := secret
		if view.Username == "" {
			want = ":" + secret
		}
		if err != nil || revealErr != nil || value != want || c.Username != view.Username {
			t.Fatal("UI metadata edit changed legacy authentication", c, value, err, revealErr)
		}
	}
}
