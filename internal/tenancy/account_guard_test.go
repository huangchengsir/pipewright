package tenancy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/auth"
	"testing"
	"time"
)

func TestCollidingFormerBucketsDoNotShareAccountLockouts(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	first, err := accounts.Create(ctx, admin, CreateUserInput{Username: "guardfirst", DisplayName: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := accounts.Create(ctx, admin, CreateUserInput{Username: "guardsecond", DisplayName: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	left := "collision-0"
	bucket := sha256.Sum256([]byte("user:" + left))
	right := ""
	for i := 1; i < 10000; i++ {
		candidate := fmt.Sprintf("collision-%d", i)
		sum := sha256.Sum256([]byte("user:" + candidate))
		if sum[0]%64 == bucket[0]%64 {
			right = candidate
			break
		}
	}
	if right == "" {
		t.Fatal("fixture collision not found")
	}
	if _, err = r.db.Exec(`UPDATE access_users SET id=? WHERE id=?`, left, first.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`UPDATE access_users SET id=? WHERE id=?`, right, second.User.ID); err != nil {
		t.Fatal(err)
	}
	s, err := accounts.Login(ctx, "guardfirst", first.Password)
	if err != nil {
		t.Fatal(err)
	}
	s, err = accounts.ChangePassword(ctx, s.Token, first.Password, "first-good-password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := accounts.Login(ctx, "guardsecond", second.Password)
	if err != nil {
		t.Fatal(err)
	}
	other, err = accounts.ChangePassword(ctx, other.Token, second.Password, "second-good-password")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < auth.LockoutThreshold; i++ {
		if _, err = accounts.Login(ctx, "guardfirst", "incorrect-password"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatal(i, err)
		}
		if _, err = accounts.Login(ctx, "guardsecond", "second-good-password"); err != nil {
			t.Fatal("another account was locked", i, err)
		}
	}
	var locked *auth.ErrLockedOut
	if _, err = accounts.Login(ctx, "guardfirst", "first-good-password"); !errors.As(err, &locked) {
		t.Fatal("other successful account reset first failures", err)
	}
	if _, err = accounts.ChangePassword(ctx, s.Token, "first-good-password", "next-first-password"); !errors.As(err, &locked) {
		t.Fatal("password change bypassed same account lockout", err)
	}
	if _, err = accounts.ChangePassword(ctx, other.Token, "second-good-password", "next-second-password"); err != nil {
		t.Fatal("unrelated password change blocked", err)
	}
}
func TestAccountGuardCapacityDoesNotEvictActiveCounters(t *testing.T) {
	var guards accountGuards
	manager, release, err := guards.acquire("protected")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < auth.LockoutThreshold; i++ {
		manager.RecordFailure()
	}
	release()
	for i := 1; i < 4096; i++ {
		_, done, e := guards.acquire(fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
		done()
	}
	if _, _, err = guards.acquire("extra"); err == nil {
		t.Fatal("capacity not enforced")
	}
	manager, release, err = guards.acquire("protected")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if locked, _ := manager.Check(); !locked {
		t.Fatal("active lockout evicted")
	}
	guards.mu.Lock()
	guards.entries["1"].last = time.Now().Add(-2 * time.Hour)
	guards.mu.Unlock()
	_, done, err := guards.acquire("extra")
	if err != nil {
		t.Fatal(err)
	}
	done()
}

func TestUnknownUsernameBudgetCannotConsumeKnownAccountCapacity(t *testing.T) {
	accounts, _, _ := accountFixture(t)
	for i := 0; i < 4096; i++ {
		_, done, err := accounts.unknownLockouts.acquire(fmt.Sprintf("unknown:%d", i))
		if err != nil {
			t.Fatal(err)
		}
		done()
	}
	if _, _, err := accounts.unknownLockouts.acquire("unknown:extra"); err == nil {
		t.Fatal("unknown capacity not enforced")
	}
	// A real account uses a separate full-ID pool and remains available.
	if _, err := accounts.Login(context.Background(), "admin", "existing-admin-password"); err != nil {
		t.Fatal("unknown names exhausted known account pool", err)
	}
}
