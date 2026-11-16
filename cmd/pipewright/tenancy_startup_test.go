package main

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessAssemblyPinsBothModesAndBlocksIncompleteActivation(t *testing.T) {
	db := storetest.OpenDB(t)
	ctx := context.Background()
	legacy := auth.NewService(db, nil)
	if e := legacy.Bootstrap("admin", "assembly-admin-password"); e != nil {
		t.Fatal(e)
	}
	off, e := assembleAccess(ctx, db, legacy, config.MultiTenantMode{}, tenancyRuntimeCoverageReady)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = assembleAccess(ctx, db, legacy, config.MultiTenantMode{Enabled: true, Explicit: true}, false); !errors.Is(e, tenancy.ErrNotReady) {
		t.Fatal("incomplete activation", e)
	}
	session, e := off.authenticator.Login("admin", "assembly-admin-password")
	if e != nil || session.MultiTenant {
		t.Fatal(e)
	}
	on, e := assembleAccess(ctx, db, legacy, config.MultiTenantMode{Enabled: true, Explicit: true}, tenancyRuntimeCoverageReady)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = off.authenticator.Verify(session.Token); e == nil {
		t.Fatal("old off process survived transition")
	}
	if _, e = off.authenticator.Login("admin", "assembly-admin-password"); e == nil {
		t.Fatal("old off process admitted login")
	}
	called := false
	w := httptest.NewRecorder()
	off.Guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/providers", nil))
	if w.Code != 503 || called {
		t.Fatal("public callback process boundary", w.Code, called)
	}
	multi, e := on.authenticator.Login("admin", "assembly-admin-password")
	if e != nil || !multi.MultiTenant {
		t.Fatal(e)
	}
	if _, e = assembleAccess(ctx, db, legacy, config.MultiTenantMode{}, tenancyRuntimeCoverageReady); !errors.Is(e, tenancy.ErrExplicitModeRequired) {
		t.Fatal("implicit downgrade", e)
	}
	if _, e = assembleAccess(ctx, db, legacy, config.MultiTenantMode{Explicit: true}, tenancyRuntimeCoverageReady); e != nil {
		t.Fatal(e)
	}
	if _, e = on.authenticator.Verify(multi.Token); e == nil {
		t.Fatal("old on process survived transition")
	}
}
