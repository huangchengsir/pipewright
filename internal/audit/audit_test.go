package audit

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

// testDB 打开含迁移的临时 SQLite(含 0009_audit append-only 表 + trigger)。
func testDB(t *testing.T) *sql.DB {
	return storetest.OpenDB(t)
}

// memSink 是内存远端 sink(测试用):线程安全地累积收到的记录。
type memSink struct {
	mu      sync.Mutex
	records []Record
	failNow bool
}

func (s *memSink) Send(_ context.Context, r Record) error {
	if s.failNow {
		return errFail
	}
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
	return nil
}

func (s *memSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

var errFail = &sinkErr{}

type sinkErr struct{}

func (*sinkErr) Error() string { return "sink down" }

// TestRecordAndList 基本写入 + 列表回读。
func TestRecordAndList(t *testing.T) {
	db := testDB(t)
	rec := New(db, mask.NewMasker(), nil)
	ctx := context.Background()

	if err := rec.Record(ctx, Entry{
		Actor: "admin", Action: ActionCredentialCreate, TargetType: TargetCredential,
		TargetID: "c1", Detail: map[string]any{"name": "ci"}, IP: "10.0.0.1",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	res, err := rec.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.Actor != "admin" || e.Action != ActionCredentialCreate || e.TargetID != "c1" || e.IP != "10.0.0.1" {
		t.Fatalf("entry fields wrong: %+v", e)
	}
	if e.Detail["name"] != "ci" {
		t.Fatalf("detail wrong: %v", e.Detail)
	}
}

// TestDetailMasked 是脱敏铁律回归:detail 内的已登记 secret 落库即 [MASKED],不含明文。
func TestDetailMasked(t *testing.T) {
	db := testDB(t)
	const secret = "ghp_plaintext_secret_value_123456"
	m := mask.NewMasker()
	m.RegisterSecret(secret)
	rec := New(db, m, nil)
	ctx := context.Background()

	if err := rec.Record(ctx, Entry{
		Actor: "admin", Action: ActionCredentialCreate, TargetType: TargetCredential,
		Detail: map[string]any{"leak": "value=" + secret},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// 直接读 detail_json 原始列,断言库里不含明文。
	var raw string
	if err := db.QueryRow(`SELECT detail_json FROM audit_log LIMIT 1`).Scan(&raw); err != nil {
		t.Fatalf("scan detail_json: %v", err)
	}
	if strings.Contains(raw, secret) {
		t.Fatalf("detail_json 落库含明文 secret: %q", raw)
	}
	if !strings.Contains(raw, mask.Placeholder) {
		t.Fatalf("detail_json 应含 %s: %q", mask.Placeholder, raw)
	}
}

// TestAppendOnlyUpdateAborted 是 append-only 硬化回归:UPDATE audit_log 被 SQLite
// trigger RAISE(ABORT) 拒绝。
func TestAppendOnlyUpdateAborted(t *testing.T) {
	db := testDB(t)
	rec := New(db, mask.NewMasker(), nil)
	ctx := context.Background()
	if err := rec.Record(ctx, Entry{Actor: "admin", Action: ActionProjectCreate, TargetType: TargetProject, TargetID: "p1"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	_, err := db.Exec(`UPDATE audit_log SET actor = 'attacker' WHERE target_id = 'p1'`)
	if err == nil {
		t.Fatal("UPDATE 应被 trigger ABORT,却成功了")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "append-only") {
		t.Fatalf("ABORT 错误信息应提示 append-only, got: %v", err)
	}
	// 数据未被改。
	var actor string
	if err := db.QueryRow(`SELECT actor FROM audit_log WHERE target_id = 'p1'`).Scan(&actor); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if actor != "admin" {
		t.Fatalf("actor 被篡改: %q", actor)
	}
}

// TestAppendOnlyDeleteAborted 是 append-only 硬化回归:DELETE audit_log 被 trigger 拒绝。
func TestAppendOnlyDeleteAborted(t *testing.T) {
	db := testDB(t)
	rec := New(db, mask.NewMasker(), nil)
	ctx := context.Background()
	if err := rec.Record(ctx, Entry{Actor: "admin", Action: ActionProjectDelete, TargetType: TargetProject, TargetID: "p2"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	_, err := db.Exec(`DELETE FROM audit_log WHERE target_id = 'p2'`)
	if err == nil {
		t.Fatal("DELETE 应被 trigger ABORT,却成功了")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "append-only") {
		t.Fatalf("ABORT 错误信息应提示 append-only, got: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM audit_log WHERE target_id = 'p2'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("记录被删,append-only 失效: count=%d", n)
	}
}

// TestRemoteSinkSurvivesLocalDeletion 是 AC-SEC-03 核心回归:
// 写 N 条 → 远端 sink 有 N 条 → 即便「丢弃」本地 DB(close),远端 sink 仍持完整 N 条。
//
// 注:本地表受 append-only trigger 保护无法 DELETE;此处以「丢弃本地 DB 连接 +
// 关闭」模拟『本地日志被删/丢失』,断言远端 sink 不依赖本地、仍完整(AC-SEC-03)。
func TestRemoteSinkSurvivesLocalDeletion(t *testing.T) {
	db := testDB(t)
	sink := &memSink{}
	rec := New(db, mask.NewMasker(), sink)
	ctx := context.Background()

	const n = 5
	for i := 0; i < n; i++ {
		if err := rec.Record(ctx, Entry{
			Actor: "admin", Action: ActionCredentialCreate, TargetType: TargetCredential,
			TargetID: "c" + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	// 本地有 n 条。
	var localCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM audit_log`).Scan(&localCount); err != nil {
		t.Fatalf("local count: %v", err)
	}
	if localCount != n {
		t.Fatalf("local count = %d, want %d", localCount, n)
	}

	// 模拟本地丢失:关闭并丢弃本地 DB。
	_ = db.Close()

	// 远端 sink 仍完整持 n 条(不依赖本地存储)。
	if got := sink.count(); got != n {
		t.Fatalf("远端 sink 在本地丢失后应仍有 %d 条, got %d", n, got)
	}
}

// TestSinkFailureDoesNotBlock 是降级铁律回归:远端 sink 失败不阻断主操作,
// 本地仍成功落库,Record 返回 nil。
func TestSinkFailureDoesNotBlock(t *testing.T) {
	db := testDB(t)
	sink := &memSink{failNow: true}
	var observed error
	rec := &recorder{db: db, masker: mask.NewMasker(), sink: sink, onSinkError: func(e error) { observed = e }}
	ctx := context.Background()

	err := rec.Record(ctx, Entry{Actor: "admin", Action: ActionProjectCreate, TargetType: TargetProject, TargetID: "p9"})
	if err != nil {
		t.Fatalf("sink 失败不应阻断主操作, got err: %v", err)
	}
	if observed == nil {
		t.Fatal("sink 失败应被 onSinkError 观测到(可观测降级)")
	}
	// 本地仍落库。
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM audit_log WHERE target_id = 'p9'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("sink 失败时本地仍应落库, count=%d", n)
	}
}

// TestListPaginationAndFilter 验证游标分页 + action/targetType 过滤。
func TestListPaginationAndFilter(t *testing.T) {
	db := testDB(t)
	rec := New(db, mask.NewMasker(), nil)
	ctx := context.Background()

	// 写 7 条 credential_create + 3 条 project_create。
	for i := 0; i < 7; i++ {
		_ = rec.Record(ctx, Entry{Actor: "admin", Action: ActionCredentialCreate, TargetType: TargetCredential, TargetID: "c"})
	}
	for i := 0; i < 3; i++ {
		_ = rec.Record(ctx, Entry{Actor: "admin", Action: ActionProjectCreate, TargetType: TargetProject, TargetID: "p"})
	}

	// 过滤 action=project_create → 3 条。
	res, err := rec.List(ctx, ListFilter{Action: ActionProjectCreate})
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("action 过滤应得 3 条, got %d", len(res.Entries))
	}
	for _, e := range res.Entries {
		if e.Action != ActionProjectCreate {
			t.Fatalf("过滤泄漏: %s", e.Action)
		}
	}

	// 过滤 targetType=credential → 7 条。
	res, err = rec.List(ctx, ListFilter{TargetType: TargetCredential})
	if err != nil {
		t.Fatalf("list filtered2: %v", err)
	}
	if len(res.Entries) != 7 {
		t.Fatalf("targetType 过滤应得 7 条, got %d", len(res.Entries))
	}

	// 分页:limit=4,共 10 条,第一页 4 条 + nextBefore;翻页直到取尽,合计 10 条无重叠。
	seen := map[string]bool{}
	before := ""
	pages := 0
	for {
		page, err := rec.List(ctx, ListFilter{Limit: 4, Before: before})
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		for _, e := range page.Entries {
			if seen[e.ID] {
				t.Fatalf("分页出现重复 id: %s", e.ID)
			}
			seen[e.ID] = true
		}
		pages++
		if page.NextBefore == "" {
			break
		}
		before = page.NextBefore
		if pages > 10 {
			t.Fatal("分页未终止")
		}
	}
	if len(seen) != 10 {
		t.Fatalf("分页合计应覆盖 10 条, got %d", len(seen))
	}
}
