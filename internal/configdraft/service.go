package configdraft

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
)

func New(db *sql.DB, opts *Options) *Service {
	o := Options{}
	if opts != nil {
		o = *opts
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxCPU <= 0 {
		o.MaxCPU = 16
	}
	if o.MaxMemoryBytes <= 0 {
		o.MaxMemoryBytes = 32 << 30
	}
	key := append([]byte(nil), o.CursorKey...)
	if len(key) < 32 {
		key = make([]byte, 32)
		if _, e := rand.Read(key); e != nil {
			panic("configdraft: random source unavailable")
		}
	}
	return &Service{db: db, opts: o, cursorKey: key}
}

type guard struct {
	scrub  func(string) string
	epoch  int64
	authz  *int64
	policy *string
}

func (s *Service) prepare(ctx context.Context) (guard, error) {
	if s.opts.PrepareSecrets == nil {
		return guard{}, ErrSecret
	}
	a, e := configstate.ReadEpoch(ctx, s.db, configstate.EpochSecrets)
	if e != nil {
		return guard{}, ErrSecret
	}
	f, e := s.opts.PrepareSecrets(ctx)
	if e != nil || f == nil {
		return guard{}, ErrSecret
	}
	b, e := configstate.ReadEpoch(ctx, s.db, configstate.EpochSecrets)
	if e != nil || a != b {
		return guard{}, ErrCatalog
	}
	authz := int64(-1)
	policy := ""
	return guard{scrub: f, epoch: b, authz: &authz, policy: &policy}, nil
}
func (g guard) safe(v string) string {
	if g.scrub == nil {
		return ""
	}
	v = g.scrub(v)
	if !utf8.ValidString(v) {
		return "[REDACTED]"
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, v)
}
func (g guard) clean(v string) bool { return g.scrub != nil && g.safe(v) == v }
func (s *Service) fresh(ctx context.Context, tokenID string, g guard) error {
	t, e := auth.LoadMCPToken(ctx, s.db, s.db, tokenID, false)
	if e != nil {
		return ErrForbidden
	}
	if g.authz != nil && *g.authz >= 0 && t.AuthzRevision != *g.authz {
		return ErrForbidden
	}
	x, e := configstate.ReadEpoch(ctx, s.db, configstate.EpochSecrets)
	if e != nil || x != g.epoch {
		return ErrCatalog
	}
	if g.policy != nil && *g.policy != "" {
		p, e := s.epochs(ctx, s.db)
		if e != nil || p != *g.policy {
			return ErrPolicy
		}
	}
	return nil
}
func (s *Service) epochs(ctx context.Context, q configstate.DBTX) (string, error) {
	p, e := configstate.ReadEpoch(ctx, q, configstate.EpochPolicy)
	if e != nil {
		return "", e
	}
	x, e := configstate.ReadEpoch(ctx, q, configstate.EpochExecutor)
	if e != nil {
		return "", e
	}
	// These values contain control metadata only, never hidden configuration.
	c, e := configstate.ReadEpoch(ctx, q, configstate.EpochSecrets)
	if e != nil {
		return "", e
	}
	return "p1." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d:%d:%s:%s:%t:%t", p, x, c, s.opts.Runtime.RunnerMode, s.opts.Runtime.ExecutorKind, s.opts.Runtime.Available, s.opts.Runtime.PACOverride))), nil
}
func revision(v int64) string {
	return "r1." + base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(v, 10)))
}
func parseRevision(v string) (int64, error) {
	if !strings.HasPrefix(v, "r1.") {
		return 0, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(v, "r1."))
	if e != nil {
		return 0, ErrInvalid
	}
	n, e := strconv.ParseInt(string(b), 10, 64)
	if e != nil || n < 1 {
		return 0, ErrInvalid
	}
	return n, nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	x := sha256.Sum256(b)
	return hex.EncodeToString(x[:])
}
func validRequestID(v string) bool {
	if len(v) < 8 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func validText(v string, max int) bool {
	return utf8.ValidString(v) && strings.TrimSpace(v) != "" && len([]rune(v)) <= max && !strings.ContainsFunc(v, unicode.IsControl)
}
func sectionFor(op string) string {
	switch op {
	case "project.set_name", "project.set_default_branch":
		return "project"
	case "stage.rename", "job.rename":
		return "pipeline"
	case "step.set_limits":
		return "step_limits"
	case "build.set":
		return "build"
	}
	return ""
}
func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
func allowed(t *auth.MCPToken, pid, cap, section, op string) bool {
	if !strings.HasPrefix(cap, "config.") {
		cap = "config." + cap
	}
	if cap == "config.read" {
		op = ""
	}
	return t != nil && t.AllowsConfig(pid, cap, section, op)
}
func authorize(t *auth.MCPToken, pid, cap string, ops []Operation) error {
	for _, o := range ops {
		sec := sectionFor(o.Type)
		if sec == "" || !allowed(t, pid, "read", sec, o.Type) || !allowed(t, pid, "propose", sec, o.Type) || (cap == "apply" && !allowed(t, pid, "apply", sec, o.Type)) {
			return ErrForbidden
		}
	}
	return nil
}
func (s *Service) locks(ctx context.Context, q configstate.DBTX, tokenID string, g guard) (*auth.MCPToken, error) {
	if e := configstate.LockEpochs(ctx, q, s.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); e != nil {
		return nil, e
	}
	x, e := configstate.ReadEpoch(ctx, q, configstate.EpochSecrets)
	if e != nil || x != g.epoch {
		return nil, ErrCatalog
	}
	t, e := auth.LoadMCPToken(ctx, q, s.db, tokenID, true)
	if e != nil {
		return nil, ErrForbidden
	}
	if g.authz != nil && *g.authz < 0 {
		*g.authz = t.AuthzRevision
	}
	if g.policy != nil && *g.policy == "" {
		p, e := s.epochs(ctx, q)
		if e != nil {
			return nil, e
		}
		*g.policy = p
	}
	return t, nil
}
func (s *Service) audit(ctx context.Context, q configstate.DBTX, actor, action, pid, draft string, fields []string) error {
	e := audit.Entry{Actor: actor, Action: action, TargetType: "project", TargetID: pid, Detail: map[string]any{"draftId": draft, "fields": fields}}
	if s.opts.AppendAudit != nil {
		return s.opts.AppendAudit(ctx, q, e)
	}
	b, _ := json.Marshal(e.Detail)
	stamp := s.opts.Now().UTC().Format(time.RFC3339Nano)
	_, err := q.ExecContext(ctx, `INSERT INTO audit_log (id,timestamp,actor,action,target_type,target_id,detail_json,ip,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, uuid.NewString(), stamp, actor, action, "project", pid, string(b), "", stamp)
	return err
}

type document struct {
	projectID, name, branch, credential             string
	pac                                             bool
	rev                                             int64
	policy, source, applicability, runner, executor string
	raw                                             map[string]any
	spec                                            pipeline.Spec
	settings                                        pipeline.Settings
	validationEvents                                pipeline.ValidationEvents
	validationMappings                              []pipeline.ValidationBranchMapping
	stored                                          bool
}

func (s *Service) load(ctx context.Context, q configstate.DBTX, pid string) (*document, error) {
	d := &document{projectID: pid, runner: s.opts.Runtime.RunnerMode, executor: s.opts.Runtime.ExecutorKind, source: "stored", applicability: "supported"}
	var n int64
	var pac int
	e := q.QueryRowContext(ctx, `SELECT `+s.byteLength("name")+`+`+s.byteLength("default_branch")+`+`+s.byteLength("credential_id")+` FROM projects WHERE id=?`, pid).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrForbidden
	}
	if e != nil {
		return nil, e
	}
	if n > 16384 {
		return nil, ErrLarge
	}
	e = q.QueryRowContext(ctx, `SELECT name,default_branch,credential_id,pac_enabled FROM projects WHERE id=?`, pid).Scan(&d.name, &d.branch, &d.credential, &pac)
	if e != nil {
		return nil, e
	}
	d.pac = pac != 0
	d.rev, e = configstate.ReadRevision(ctx, q, pid)
	if e != nil {
		return nil, ErrStale
	}
	d.policy, e = s.epochs(ctx, q)
	if e != nil {
		return nil, e
	}
	if d.pac || s.opts.Runtime.PACOverride {
		d.source = "repo_preferred"
		d.applicability = "read_only"
	}
	if !s.opts.Runtime.Available || d.runner != "dag" || d.executor != "local_docker" {
		d.applicability = "read_only"
	}
	var remote string
	e = q.QueryRowContext(ctx, `SELECT runner_server_id FROM project_runners WHERE project_id=?`, pid).Scan(&remote)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if remote != "" {
		d.executor = "remote"
		d.applicability = "read_only"
	}
	var raw string
	e = q.QueryRowContext(ctx, `SELECT `+s.byteLength("spec_json")+` FROM pipeline_configs WHERE project_id=?`, pid).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		d.applicability = "missing"
		return d, nil
	}
	if e != nil {
		return nil, e
	}
	if n > 1<<20 {
		return nil, ErrLarge
	}
	if e = q.QueryRowContext(ctx, `SELECT spec_json FROM pipeline_configs WHERE project_id=?`, pid).Scan(&raw); e != nil {
		return nil, e
	}
	if !utf8.ValidString(raw) {
		return nil, ErrLarge
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if e = dec.Decode(&d.raw); e != nil {
		return nil, ErrUnsupported
	}
	if e = json.Unmarshal([]byte(raw), &d.spec); e != nil {
		return nil, ErrUnsupported
	}
	if e = boundedSpec(d.spec); e != nil {
		return nil, e
	}
	d.stored = true
	var build, envs, steps string
	e = q.QueryRowContext(ctx, `SELECT `+s.byteLength("build_json")+`+`+s.byteLength("environments_json")+`+`+s.byteLength("steps_json")+` FROM pipeline_settings WHERE project_id=?`, pid).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		d.applicability = "missing"
		return d, nil
	}
	if e != nil {
		return nil, e
	}
	if n > 1<<20 {
		return nil, ErrLarge
	}
	if e = q.QueryRowContext(ctx, `SELECT build_json,environments_json,steps_json FROM pipeline_settings WHERE project_id=?`, pid).Scan(&build, &envs, &steps); e != nil {
		return nil, e
	}
	if json.Unmarshal([]byte(build), &d.settings.Build) != nil || json.Unmarshal([]byte(envs), &d.settings.Environments) != nil || json.Unmarshal([]byte(steps), &d.settings.Steps) != nil {
		return nil, ErrUnsupported
	}
	if !utf8.ValidString(build) || !utf8.ValidString(envs) || !utf8.ValidString(steps) {
		return nil, ErrUnsupported
	}
	e = q.QueryRowContext(ctx, `SELECT `+s.byteLength("events_json")+`+`+s.byteLength("branch_mappings_json")+` FROM pipeline_triggers WHERE project_id=?`, pid).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		return d, nil
	}
	if e != nil {
		return nil, e
	}
	if n > 1<<20 {
		return nil, ErrLarge
	}
	var events, mappings string
	if e = q.QueryRowContext(ctx, `SELECT events_json,branch_mappings_json FROM pipeline_triggers WHERE project_id=?`, pid).Scan(&events, &mappings); e != nil {
		return nil, e
	}
	if !utf8.ValidString(events) || !utf8.ValidString(mappings) || json.Unmarshal([]byte(events), &d.validationEvents) != nil {
		return nil, ErrUnsupported
	}
	var rows []struct {
		BranchPattern   string   `json:"branchPattern"`
		Environment     string   `json:"environment"`
		TargetServerIDs []string `json:"targetServerIds"`
	}
	if json.Unmarshal([]byte(mappings), &rows) != nil || len(rows) > 256 {
		return nil, ErrUnsupported
	}
	for _, r := range rows {
		d.validationMappings = append(d.validationMappings, pipeline.ValidationBranchMapping{BranchPattern: r.BranchPattern, Environment: r.Environment, TargetServerIDs: r.TargetServerIDs})
	}
	return d, nil
}
func boundedSpec(p pipeline.Spec) error {
	if len(p.Stages) > 128 {
		return ErrLarge
	}
	jobs, edges := 0, 0
	expandedStages, expandedJobs := 0, 0
	seen := map[string]bool{}
	for _, st := range p.Stages {
		cells := 1
		for _, values := range st.Matrix {
			if len(values) == 0 || cells > 128/len(values) {
				return ErrLarge
			}
			cells *= len(values)
		}
		expandedStages += cells
		expandedJobs += cells * len(st.Jobs)
		if expandedStages > 128 || expandedJobs > 256 {
			return ErrLarge
		}
		if !validText(st.ID, 128) || seen[st.ID] {
			return ErrUnsupported
		}
		seen[st.ID] = true
		edges += len(st.Needs)
		jobs += len(st.Jobs)
		for _, j := range st.Jobs {
			if !validText(j.ID, 128) || seen[j.ID] {
				return ErrUnsupported
			}
			seen[j.ID] = true
			edges += len(j.Needs)
		}
	}
	if jobs > 256 || edges > 1024 {
		return ErrLarge
	}
	return nil
}

// Column names come only from fixed source-code literals, never request input.
func (s *Service) byteLength(column string) string {
	if store.DialectOf(s.db) == store.MySQL {
		return "COALESCE(OCTET_LENGTH(" + column + "),0)"
	}
	return "COALESCE(LENGTH(CAST(" + column + " AS BLOB)),0)"
}

func (s *Service) Capabilities(ctx context.Context, tokenID string) (map[string]any, error) {
	t, e := auth.LoadMCPToken(ctx, s.db, s.db, tokenID, false)
	if e != nil {
		return nil, ErrForbidden
	}
	return map[string]any{"configGrants": t.ConfigGrants, "authzRevision": t.AuthzRevision, "sections": Sections, "operations": OperationTypes, "maxOperations": 32, "maxDiffItems": 32, "maxPatchBytes": 16384, "applyEnabled": s.opts.ApplyEnabled, "validatorVersion": ValidatorVersion}, nil
}
func pageSize(n int) (int, error) {
	if n == 0 {
		return 20, nil
	}
	if n < 1 || n > 32 {
		return 0, ErrInvalid
	}
	return n, nil
}

type pageCursor struct {
	Token, Project, Section, Revision, Policy string
	Authz                                     int64
	Offset                                    int
}

func (s *Service) cursor(c pageCursor) string {
	b, _ := json.Marshal(c)
	m := hmac.New(sha256.New, s.cursorKey)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (s *Service) offset(raw string, c pageCursor) (int, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > 2048 {
		return 0, ErrInvalid
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return 0, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return 0, ErrInvalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return 0, ErrInvalid
	}
	m := hmac.New(sha256.New, s.cursorKey)
	m.Write(b)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return 0, ErrInvalid
	}
	var old pageCursor
	if json.Unmarshal(b, &old) != nil || old.Offset < 0 || old.Token != c.Token || old.Project != c.Project || old.Section != c.Section || old.Revision != c.Revision || old.Policy != c.Policy || old.Authz != c.Authz {
		return 0, ErrStale
	}
	return old.Offset, nil
}
