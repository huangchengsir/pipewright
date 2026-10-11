package opschat

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestEntryContextsPersistIndependentSessionsAndCascade(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	f := executor(a, b)
	s, v, db := fixture(t, f, nil)
	keys := []string{"containers", "overview", "servers", "terminal:" + a, "terminal:" + b}
	ids := []string{}
	for _, key := range keys {
		c, err := s.Create(context.Background(), CreateInput{Context: key})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	for i, key := range keys {
		page, err := s.ListContext(context.Background(), "", 100, key)
		if err != nil || page.ActiveSessionID != ids[i] {
			t.Fatal(key, page, err)
		}
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DB: db, Vault: v, Executor: f})
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	s = reopened
	for i, key := range keys {
		page, e := s.ListContext(context.Background(), "", 100, key)
		if e != nil || page.ActiveSessionID != ids[i] {
			t.Fatal("restart mapping", key, page, e)
		}
	}
	legacy, err := s.List(context.Background(), "", 100)
	if err != nil || legacy.ActiveSessionID != "" || len(legacy.Sessions) != 5 {
		t.Fatal("old sessions not preserved", legacy, err)
	}
	if err = s.ActivateContext(context.Background(), ids[0], keys[3]); err != nil {
		t.Fatal(err)
	}
	page, _ := s.ListContext(context.Background(), "", 100, keys[4])
	if page.ActiveSessionID != ids[4] {
		t.Fatal("terminal B changed")
	}
	if err = s.Delete(context.Background(), ids[0]); err != nil {
		t.Fatal(err)
	}
	var n int
	if db.QueryRow("SELECT COUNT(*) FROM ops_chat_contexts WHERE session_id=?", ids[0]).Scan(&n) != nil || n != 0 {
		t.Fatal("orphan mappings")
	}
	for _, key := range []string{"http://evil", "terminal:" + uuid.NewString(), "overview/path", "terminal:invalid"} {
		if _, err = s.Create(context.Background(), CreateInput{Context: key}); err != ErrInvalid {
			t.Fatal(key, err)
		}
	}
}
