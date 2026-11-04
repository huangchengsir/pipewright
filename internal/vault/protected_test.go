package vault

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestProtectedReferenceRejectedBeforeAllGenericAdapters(t *testing.T) {
	st := storetest.Open(t)
	key := [32]byte{1}
	v := New(st.DB, &key)
	id := "kcred_do-not-export"
	for name, call := range map[string]func() error{
		"get": func() error { _, e := v.Get(id); return e }, "reveal": func() error { _, e := v.Reveal(id); return e }, "git": func() error { _, e := v.GetGitAuth(id); return e }, "exists": func() error { _, e := v.Exists(id); return e }, "existsTx": func() error { _, e := ExistsTx(context.Background(), st.DB, id); return e }, "acquire": func() error { _, _, e := AcquireGitAuth(context.Background(), nil, id); return e }, "update": func() error { _, e := v.Update(id, UpdateInput{}); return e }, "delete": func() error { return v.Delete(id) },
	} {
		t.Run(name, func(t *testing.T) {
			if e := call(); !errors.Is(e, ErrProtectedCredential) {
				t.Fatal(e)
			}
		})
	}
}
