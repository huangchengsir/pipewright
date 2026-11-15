package authlogin

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"testing"
	"time"
)

func TestEmployeePlatformIdentityIsolation(t *testing.T) {
	for _, provider := range Providers {
		t.Run(provider, func(t *testing.T) {
			ctx := context.Background()
			f := coreSetup(t)
			repo := tenancy.NewRepository(f.db.DB)
			if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
				t.Fatal(err)
			}
			accounts := tenancy.NewAccounts(f.db.DB)
			admin, err := accounts.Login(ctx, "admin", "fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			f.s.adapters[provider] = f.adapter
			f.adapter.identity.Provider = provider
			f.adapter.identity.Issuer = "https://identity.example/" + provider
			secret := "only-a-local-application-secret"
			app, err := f.s.SaveApp(ctx, admin.Token, provider, AppInput{ExpectedVersion: 1, ClientID: "dedicated-client", ClientSecret: &secret, GiteeMode: GiteeModeConfidentialCode, MicrosoftTenant: "common"})
			if err != nil {
				t.Fatal(err)
			}
			app, err = f.s.SetEnabled(ctx, admin.Token, provider, app.Provider.ConfigVersion, true)
			if err != nil {
				t.Fatal("enable before bootstrap binding", err)
			}
			makeEmployee := func(name string) tenancy.Session {
				initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name})
				if e != nil {
					t.Fatal(e)
				}
				signed, e := accounts.Login(ctx, name, initial.Password)
				if e != nil {
					t.Fatal(e)
				}
				signed, e = accounts.ChangePassword(ctx, signed.Token, initial.Password, name+"-permanent-password")
				if e != nil {
					t.Fatal(e)
				}
				return signed
			}
			first, second := makeEmployee("first"), makeEmployee("second")
			public, err := f.s.PublicProviders(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range public {
				if entry.Provider == provider && !entry.Available {
					t.Fatal("availability depended on bootstrap binding")
				}
			}
			bind := func(account tenancy.Session, subject string) {
				f.adapter.identity.Subject = subject
				start, e := f.s.Start(ctx, provider, "bind", account.Token, "/settings/account", app.Provider.ConfigVersion)
				if e != nil {
					t.Fatal(e)
				}
				calls := f.adapter.calls.Load()
				if _, e = f.s.Complete(ctx, provider, stateOf(t, start), strings.Repeat("0", 64), account.Token, "code", false); e == nil || f.adapter.calls.Load() != calls {
					t.Fatal("wrong browser reached provider", e)
				}
				completed, e := f.s.Complete(ctx, provider, stateOf(t, start), start.BrowserNonce, account.Token, "code", false)
				if e != nil || completed.BindingFlow != start.FlowID {
					t.Fatal(completed, e)
				}
				calls = f.adapter.calls.Load()
				if _, e = f.s.Complete(ctx, provider, stateOf(t, start), start.BrowserNonce, account.Token, "code", false); e == nil || f.adapter.calls.Load() != calls {
					t.Fatal("callback replay exchanged again", e)
				}
				other := second
				if account.User.ID == second.User.ID {
					other = first
				}
				if _, e = f.s.Pending(ctx, other.Token, start.FlowID); e == nil {
					t.Fatal("other employee read candidate")
				}
				if _, e = f.s.Confirm(ctx, other.Token, provider, start.FlowID, app.Provider.ConfigVersion); e == nil {
					t.Fatal("other employee confirmed candidate")
				}
				result, e := f.s.Confirm(ctx, account.Token, provider, start.FlowID, app.Provider.ConfigVersion)
				if e != nil {
					t.Fatal(e)
				}
				if result.Provider.ConfigVersion != app.Provider.ConfigVersion || !result.Provider.Enabled || result.Provider.ClientID != "" {
					t.Fatal("employee mutated or read global application", result.Provider)
				}
				if _, e = f.s.Confirm(ctx, account.Token, provider, start.FlowID, app.Provider.ConfigVersion); e == nil {
					t.Fatal("candidate replayed")
				}
			}
			bind(first, "first-subject")
			bind(second, "second-subject")
			login := func(subject string) (Completion, error) {
				f.adapter.identity.Subject = subject
				start, e := f.s.Start(ctx, provider, "login", "", "/", 0)
				if e != nil {
					return Completion{}, e
				}
				return f.s.Complete(ctx, provider, stateOf(t, start), start.BrowserNonce, "", "code", false)
			}
			firstLogin, err := login("first-subject")
			if err != nil || firstLogin.Session == nil || firstLogin.Session.UserID != first.User.ID || firstLogin.Session.AuthSource != provider {
				t.Fatal("first independent login", firstLogin, err)
			}
			secondLogin, err := login("second-subject")
			if err != nil || secondLogin.Session == nil || secondLogin.Session.UserID != second.User.ID {
				t.Fatal("second independent login", secondLogin, err)
			}
			if verified, e := accounts.Verify(ctx, firstLogin.Session.Token); e != nil || verified.AuthSource != provider {
				t.Fatal("session lost provider source", e)
			}
			own, err := f.s.Settings(ctx, first.Token)
			if err != nil || own.CanManage {
				t.Fatal("employee settings", err)
			}
			for _, entry := range own.Providers {
				if entry.ClientID != "" || entry.HasSecret || entry.CallbackURL != "" {
					t.Fatal("application metadata leaked")
				}
				if entry.Provider == provider && (entry.Binding == nil || entry.Binding.Subject != "first-subject") {
					t.Fatal("another employee binding exposed")
				}
			}
			if _, e := f.s.SetEnabled(ctx, first.Token, provider, app.Provider.ConfigVersion, false); !errors.Is(e, tenancy.ErrForbidden) {
				t.Fatal("employee changed global enable", e)
			}

			// Another employee's verified subject remains owned by that employee.
			f.adapter.identity.Subject = "second-subject"
			takeover, e := f.s.Start(ctx, provider, "bind", first.Token, "/settings/account", app.Provider.ConfigVersion)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.Complete(ctx, provider, stateOf(t, takeover), takeover.BrowserNonce, first.Token, "code", false); e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.Confirm(ctx, first.Token, provider, takeover.FlowID, app.Provider.ConfigVersion); !errors.Is(e, ErrIdentityDenied) {
				t.Fatal("subject takeover", e)
			}
			if _, e := f.s.Start(ctx, provider, "bind", firstLogin.Session.Token, "/", app.Provider.ConfigVersion); !errors.Is(e, ErrReauthRequired) {
				t.Fatal("external session fabricated password proof", e)
			}
			if _, e := accounts.Reauthenticate(ctx, firstLogin.Session.Token, "second-permanent-password"); e == nil {
				t.Fatal("another employee password accepted")
			}
			if _, e := accounts.Reauthenticate(ctx, firstLogin.Session.Token, "first-permanent-password"); e != nil {
				t.Fatal("own password reauth", e)
			}
			if _, e := f.s.Unbind(ctx, first.Token, provider, app.Provider.ConfigVersion); e != nil {
				t.Fatal(e)
			}
			if _, e := accounts.Verify(ctx, firstLogin.Session.Token); e == nil {
				t.Fatal("unbound source session survived")
			}
			if _, e := accounts.Verify(ctx, secondLogin.Session.Token); e != nil {
				t.Fatal("other employee session revoked", e)
			}
			if _, e := login("first-subject"); !errors.Is(e, ErrIdentityDenied) {
				t.Fatal("unbound identity authenticated", e)
			}

			// Revocation while the provider exchange is in flight must discard
			// the result, including when the same client merely rotates a secret.
			f.adapter.identity.Subject = "second-subject"
			racing, e := f.s.Start(ctx, provider, "login", "", "/", 0)
			if e != nil {
				t.Fatal(e)
			}
			f.adapter.entered = make(chan struct{}, 1)
			f.adapter.release = make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				_, err := f.s.Complete(ctx, provider, stateOf(t, racing), racing.BrowserNonce, "", "code", false)
				finished <- err
			}()
			select {
			case <-f.adapter.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("exchange never entered")
			}
			rotated := "replacement-local-secret"
			app, e = f.s.SaveApp(ctx, admin.Token, provider, AppInput{ExpectedVersion: app.Provider.ConfigVersion, ClientID: "dedicated-client", ClientSecret: &rotated, GiteeMode: GiteeModeConfidentialCode, MicrosoftTenant: "common"})
			close(f.adapter.release)
			if e != nil {
				t.Fatal(e)
			}
			select {
			case e = <-finished:
				if e == nil {
					t.Fatal("rotated app accepted old exchange")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("exchange never settled")
			}
			f.adapter.entered = nil
			f.adapter.release = nil
			if _, e = login("second-subject"); e != nil {
				t.Fatal("new app flow unusable", e)
			}
			transient := makeEmployee("transient")
			abandoned, e := f.s.Start(ctx, provider, "bind", transient.Token, "/settings/account", app.Provider.ConfigVersion)
			if e != nil {
				t.Fatal(e)
			}
			if e = accounts.Logout(ctx, transient.Token); e != nil {
				t.Fatal(e)
			}
			calls := f.adapter.calls.Load()
			if _, e = f.s.Complete(ctx, provider, stateOf(t, abandoned), abandoned.BrowserNonce, transient.Token, "code", false); e == nil || f.adapter.calls.Load() != calls {
				t.Fatal("logout binding reached provider", e)
			}
			if e := accounts.SetEnabled(ctx, admin.Actor, second.User.ID, false); e != nil {
				t.Fatal(e)
			}
			if _, e := login("second-subject"); !errors.Is(e, ErrIdentityDenied) {
				t.Fatal("disabled employee authenticated", e)
			}
			if e := accounts.SetEnabled(ctx, admin.Actor, second.User.ID, true); e != nil {
				t.Fatal(e)
			}

			waiting, e := f.s.Start(ctx, provider, "login", "", "/", 0)
			if e != nil {
				t.Fatal(e)
			}
			app, e = f.s.SetEnabled(ctx, admin.Token, provider, app.Provider.ConfigVersion, false)
			if e != nil {
				t.Fatal(e)
			}
			calls = f.adapter.calls.Load()
			if _, e = f.s.Complete(ctx, provider, stateOf(t, waiting), waiting.BrowserNonce, "", "code", false); e == nil || f.adapter.calls.Load() != calls {
				t.Fatal("global disable did not invalidate pending exchange", e)
			}
			app, e = f.s.SetEnabled(ctx, admin.Token, provider, app.Provider.ConfigVersion, true)
			if e != nil {
				t.Fatal(e)
			}
			if _, e := accounts.ResetPassword(ctx, admin.Actor, second.User.ID); e != nil {
				t.Fatal(e)
			}
			if _, e := login("second-subject"); !errors.Is(e, ErrIdentityDenied) {
				t.Fatal("SSO bypassed mandatory password change", e)
			}
			oldMode, e := f.s.Start(ctx, provider, "login", "", "/", 0)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: false, Explicit: true}, true); e != nil {
				t.Fatal(e)
			}
			if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
				t.Fatal(e)
			}
			calls = f.adapter.calls.Load()
			if _, e = f.s.Complete(ctx, provider, stateOf(t, oldMode), oldMode.BrowserNonce, "", "code", false); e == nil || f.adapter.calls.Load() != calls {
				t.Fatal("old mode flow reached provider", e)
			}

		})
	}
}
func TestEmployeeFiveProvidersRemainIndependent(t *testing.T) {
	ctx := context.Background()
	f := coreSetup(t)
	repo := tenancy.NewRepository(f.db.DB)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(f.db.DB)
	admin, e := accounts.Login(ctx, "admin", "fixture-password")
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "allproviders", DisplayName: "Employee"})
	if e != nil {
		t.Fatal(e)
	}
	employee, e := accounts.Login(ctx, "allproviders", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	employee, e = accounts.ChangePassword(ctx, employee.Token, initial.Password, "employee-permanent-password")
	if e != nil {
		t.Fatal(e)
	}
	for _, provider := range Providers {
		f.s.adapters[provider] = &coreAdapter{identity: Identity{Provider: provider, Issuer: "https://identity.example/" + provider, Subject: "same-subject-string", DisplayLabel: "Employee"}}
		secret := "synthetic-app-secret"
		app, e := f.s.SaveApp(ctx, admin.Token, provider, AppInput{ExpectedVersion: 1, ClientID: "test-client", ClientSecret: &secret, GiteeMode: GiteeModeConfidentialCode, MicrosoftTenant: "common"})
		if e != nil {
			t.Fatal(provider, e)
		}
		app, e = f.s.SetEnabled(ctx, admin.Token, provider, app.Provider.ConfigVersion, true)
		if e != nil {
			t.Fatal(provider, e)
		}
		start, e := f.s.Start(ctx, provider, "bind", employee.Token, "/settings/account", app.Provider.ConfigVersion)
		if e != nil {
			t.Fatal(provider, e)
		}
		if _, e = f.s.Complete(ctx, provider, stateOf(t, start), start.BrowserNonce, employee.Token, "code", false); e != nil {
			t.Fatal(provider, e)
		}
		if _, e = f.s.Confirm(ctx, employee.Token, provider, start.FlowID, app.Provider.ConfigVersion); e != nil {
			t.Fatal(provider, e)
		}
	}
	own, e := f.s.Settings(ctx, employee.Token)
	if e != nil {
		t.Fatal(e)
	}
	bound := 0
	for _, entry := range own.Providers {
		if entry.Binding != nil {
			bound++
			if entry.Binding.Subject != "same-subject-string" {
				t.Fatal("binding overwritten")
			}
		}
	}
	if bound != len(Providers) {
		t.Fatalf("bound %d providers, expected %d", bound, len(Providers))
	}
	for _, provider := range Providers {
		start, e := f.s.Start(ctx, provider, "login", "", "/", 0)
		if e != nil {
			t.Fatal(provider, e)
		}
		completed, e := f.s.Complete(ctx, provider, stateOf(t, start), start.BrowserNonce, "", "code", false)
		if e != nil || completed.Session == nil || completed.Session.UserID != employee.User.ID {
			t.Fatal(provider, "wrong employee", e)
		}
	}
}
