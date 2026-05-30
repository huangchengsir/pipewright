package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/huangjiawei/devopstool/internal/vault"
)

// fakeRunCreator 记录被请求创建的运行(不触 run 包,避免领域互引)。
type fakeRunCreator struct {
	mu    sync.Mutex
	calls []RunRequest
	id    string
}

func (f *fakeRunCreator) CreateWebhookRun(_ context.Context, _ string, in RunRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	if f.id == "" {
		f.id = "run-1"
	}
	return f.id, nil
}

func (f *fakeRunCreator) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newReceiver 装配 Receiver + 已配 push 事件 + 分支映射 main→prod 的项目;返回 token、明文密钥。
func newReceiver(t *testing.T) (*Receiver, *fakeRunCreator, string, string) {
	t.Helper()
	db, _ := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v)
	projID := seedProject(t, db)

	ctx := context.Background()
	cfg, err := svc.Get(ctx, projID)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	token := cfg.WebhookToken

	reset, err := svc.ResetSecret(ctx, projID)
	if err != nil {
		t.Fatalf("reset secret: %v", err)
	}
	secret := reset.Secret

	if _, err := svc.Save(ctx, projID, SaveInput{
		Events: Events{Push: true},
		BranchMappings: []BranchMapping{
			{BranchPattern: "main", Environment: "prod", TargetServerIDs: []string{"srv-1", "srv-2"}},
			{BranchPattern: "release/*", Environment: "staging", TargetServerIDs: []string{"srv-3"}},
		},
		UnmatchedPolicy: PolicyIgnore,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	fake := &fakeRunCreator{}
	rc := NewReceiver(db, v, fake)
	return rc, fake, token, secret
}

func pushBody(branch, commit string) []byte {
	return []byte(`{"ref":"refs/heads/` + branch + `","after":"` + commit + `"}`)
}

func TestWebhookPasswordModeCreatesRun(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	res, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   secret,
		DeliveryID: "d-1",
		RawBody:    pushBody("main", "abc123"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !res.Accepted || res.RunID == "" {
		t.Fatalf("expected accepted run, got %+v", res)
	}
	if fake.count() != 1 {
		t.Fatalf("expected 1 run created, got %d", fake.count())
	}
	got := fake.calls[0]
	if got.Branch != "main" || got.Commit != "abc123" {
		t.Fatalf("unexpected run req: %+v", got)
	}
	if got.ResolvedEnvironment != "prod" {
		t.Fatalf("expected resolved env prod, got %q", got.ResolvedEnvironment)
	}
	if len(got.ResolvedTargetServerIDs) != 2 {
		t.Fatalf("expected 2 target servers, got %v", got.ResolvedTargetServerIDs)
	}
	if got.Actor != "gitee" {
		t.Fatalf("expected actor gitee, got %q", got.Actor)
	}
}

func TestWebhookSignatureModeCreatesRun(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	// 新鲜 timestamp(秒 epoch),落在防重放窗口内。
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	res, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   sig,
		Timestamp:  ts,
		DeliveryID: "d-sig",
		RawBody:    pushBody("main", "deadbeef"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("expected accepted, got %+v", res)
	}
	if fake.count() != 1 {
		t.Fatalf("expected 1 run, got %d", fake.count())
	}
}

// TestWebhookSignatureReplayRejected 验证防重放:旧 timestamp(超窗)即便签名正确也 401。
func TestWebhookSignatureReplayRejected(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	ts := "1700000000" // 2023,远超 5min 窗口
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	_, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   sig,
		Timestamp:  ts,
		DeliveryID: "d-replay",
		RawBody:    pushBody("main", "deadbeef"),
	})
	if err != ErrUnauthorized {
		t.Fatalf("旧 timestamp 应 401(防重放), got %v", err)
	}
	if fake.count() != 0 {
		t.Fatalf("重放不应创建运行, got %d", fake.count())
	}
}

// TestWebhookSignatureMissingTimestampRejected 验证签名模式缺失 timestamp → 401。
func TestWebhookSignatureMissingTimestampRejected(t *testing.T) {
	rc, _, token, secret := newReceiver(t)
	// 用空 timestamp 计算的签名(攻击者可构造),但缺失 timestamp 头本身应被拒。
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("\n" + secret))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	_, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   sig,
		Timestamp:  "",
		DeliveryID: "d-no-ts",
		RawBody:    pushBody("main", "deadbeef"),
	})
	if err != ErrUnauthorized {
		t.Fatalf("签名模式缺 timestamp 应 401, got %v", err)
	}
}

// TestWebhookTimestampMillisAccepted 验证毫秒 epoch 也被正确解析为新鲜。
func TestWebhookTimestampMillisAccepted(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10) // 13 位毫秒
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	res, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   sig,
		Timestamp:  ts,
		DeliveryID: "d-ms",
		RawBody:    pushBody("main", "deadbeef"),
	})
	if err != nil {
		t.Fatalf("Handle(ms ts): %v", err)
	}
	if !res.Accepted || fake.count() != 1 {
		t.Fatalf("毫秒 timestamp 应被接受, res=%+v count=%d", res, fake.count())
	}
}

func TestWebhookWrongSecretUnauthorized(t *testing.T) {
	rc, fake, token, _ := newReceiver(t)
	_, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   "whsec_wrong",
		DeliveryID: "d-bad",
		RawBody:    pushBody("main", "x"),
	})
	if err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if fake.count() != 0 {
		t.Fatalf("expected no run on bad secret, got %d", fake.count())
	}
}

func TestWebhookEventNotSubscribed(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	// Tag 未勾选(仅 push 开)。
	res, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventTag,
		TokenHdr:   secret,
		DeliveryID: "d-tag",
		RawBody:    []byte(`{"ref":"refs/tags/v1","after":"t1"}`),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Accepted || res.Ignored != IgnoredEventNotSubscribed {
		t.Fatalf("expected event_not_subscribed, got %+v", res)
	}
	if fake.count() != 0 {
		t.Fatalf("expected no run, got %d", fake.count())
	}
}

func TestWebhookNoBranchMatchIgnored(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	// UnmatchedPolicy=ignore;dev 不匹配任何映射。
	res, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   secret,
		DeliveryID: "d-dev",
		RawBody:    pushBody("dev", "c1"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Accepted || res.Ignored != IgnoredUnmatchedIgnored {
		t.Fatalf("expected unmatched_ignored, got %+v", res)
	}
	if fake.count() != 0 {
		t.Fatalf("expected no run, got %d", fake.count())
	}
}

func TestWebhookWildcardBranchMatch(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	res, err := rc.Handle(context.Background(), Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   secret,
		DeliveryID: "d-rel",
		RawBody:    pushBody("release/1.2", "r12"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("expected accepted for release/1.2, got %+v", res)
	}
	if fake.calls[0].ResolvedEnvironment != "staging" {
		t.Fatalf("expected staging env, got %q", fake.calls[0].ResolvedEnvironment)
	}
}

func TestWebhookDuplicateDeliveryIgnored(t *testing.T) {
	rc, fake, token, secret := newReceiver(t)
	d := Delivery{
		Token:      token,
		Event:      eventPush,
		TokenHdr:   secret,
		DeliveryID: "dup-1",
		RawBody:    pushBody("main", "samecommit"),
	}
	first, err := rc.Handle(context.Background(), d)
	if err != nil || !first.Accepted {
		t.Fatalf("first delivery should be accepted: %+v err=%v", first, err)
	}
	second, err := rc.Handle(context.Background(), d)
	if err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	if second.Accepted || second.Ignored != IgnoredDuplicate {
		t.Fatalf("expected duplicate ignored, got %+v", second)
	}
	if fake.count() != 1 {
		t.Fatalf("expected exactly 1 run despite duplicate, got %d", fake.count())
	}
}

// TestDerivedKeyNoColonCollision 验证派生去重键对含 ':' 的分支名不碰撞
// (长度前缀+hash 消歧:不同分段不能产生相同键)。
func TestDerivedKeyNoColonCollision(t *testing.T) {
	// 旧实现 "derived:event:branch:commit" 下,这两组会拼成相同串。
	a := derivedKey("push", "a:b", "c")
	b := derivedKey("push", "a", "b:c")
	if a == b {
		t.Fatalf("含冒号分支名派生键碰撞: %q == %q", a, b)
	}
	// 空 payload(空 branch/commit)与非空也应区分。
	if derivedKey("push", "", "") == derivedKey("push", "x", "") {
		t.Fatalf("空 payload 与非空派生键碰撞")
	}
}

// TestWebhookUnmatchedRecordIdempotent 验证未匹配 record 路径走同一 dedup claim:
// 同 delivery 重复投递不无界灌库(第二次判 duplicate),只入一行。
func TestWebhookUnmatchedRecordIdempotent(t *testing.T) {
	db, _ := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v)
	projID := seedProject(t, db)
	ctx := context.Background()
	cfg, err := svc.Get(ctx, projID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	reset, _ := svc.ResetSecret(ctx, projID)
	// Push 订阅,但无任何分支映射 + policy=record(分支必不匹配 → unmatched_recorded)。
	if _, err := svc.Save(ctx, projID, SaveInput{
		Events:          Events{Push: true},
		BranchMappings:  []BranchMapping{},
		UnmatchedPolicy: PolicyRecord,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rc := NewReceiver(db, v, &fakeRunCreator{})
	d := Delivery{
		Token:      cfg.WebhookToken,
		Event:      eventPush,
		TokenHdr:   reset.Secret,
		DeliveryID: "rec-1",
		RawBody:    pushBody("feature/x", "c1"),
	}
	r1, err := rc.Handle(ctx, d)
	if err != nil || r1.Ignored != IgnoredUnmatchedRecorded {
		t.Fatalf("first: %+v err=%v, want unmatched_recorded", r1, err)
	}
	r2, err := rc.Handle(ctx, d)
	if err != nil || r2.Ignored != IgnoredDuplicate {
		t.Fatalf("second(same delivery): %+v err=%v, want duplicate", r2, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM webhook_deliveries WHERE project_id = ?`, projID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("同 delivery 应只入 1 行(防无界灌库), got %d", n)
	}
}

func TestWebhookTokenNotFound(t *testing.T) {
	rc, _, _, secret := newReceiver(t)
	_, err := rc.Handle(context.Background(), Delivery{
		Token:    "nonexistent",
		Event:    eventPush,
		TokenHdr: secret,
		RawBody:  pushBody("main", "x"),
	})
	if err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

func TestWebhookVaultUnconfiguredRejects(t *testing.T) {
	db, _ := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v)
	projID := seedProject(t, db)
	cfg, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// 用未配置 vault 的 Receiver(master key 缺失)→ 验签不可用,明确拒绝。
	rc := NewReceiver(db, vault.New(db, nil), &fakeRunCreator{})
	_, err = rc.Handle(context.Background(), Delivery{
		Token:    cfg.WebhookToken,
		Event:    eventPush,
		TokenHdr: "anything",
		RawBody:  pushBody("main", "x"),
	})
	if err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized when vault unconfigured, got %v", err)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"main", "main", true},
		{"main", "master", false},
		{"release/*", "release/1.2", true},
		{"release/*", "release/", true},
		{"release/*", "dev", false},
		{"feature/*-hotfix", "feature/x-hotfix", true},
		{"feature/*-hotfix", "feature/x", false},
		{"*", "anything", true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q)=%v want %v", c.pattern, c.s, got, c.want)
		}
	}
}
