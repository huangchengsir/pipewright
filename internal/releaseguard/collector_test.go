package releaseguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Exercise the actual Go templates against Docker-shaped maps. Returning
// prebuilt projection JSON from a fake executor would miss missing-key errors
// raised by Docker before a projection can reach the collector decoder.
func renderDockerProjection(t *testing.T, projection string, facts map[string]any) string {
	t.Helper()
	tmpl, err := template.New("inspect").Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			body, err := json.Marshal(value)
			return string(body), err
		},
	}).Parse(projection)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = tmpl.Execute(&out, facts); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("template did not render valid JSON: %s", out.Bytes())
	}
	return out.String()
}

func TestCollectorRendersOptionalDockerFieldsWithStrictMissingKeys(t *testing.T) {
	for _, health := range []string{"missing", "null", "healthy", "starting", "unhealthy"} {
		for _, variant := range []string{"missing", "", "v8"} {
			t.Run(health+"/variant="+variant, func(t *testing.T) {
				v := ruleSnapshot()
				state := map[string]any{
					"StartedAt": v.StartedAt, "FinishedAt": v.FinishedAt,
					"Running": true, "OOMKilled": false, "ExitCode": 0,
				}
				wantHealth := ""
				if health == "null" {
					state["Health"] = nil
				} else if health != "missing" {
					state["Health"] = map[string]any{"Status": health, "Log": "DO_NOT_PROJECT_HEALTH_OUTPUT"}
					wantHealth = health
				}
				container := map[string]any{
					"Id": v.ContainerID, "Created": v.CreatedAt, "State": state,
					"Image": v.ConfigID, "RestartCount": 0,
					"Config": map[string]any{"Image": v.DeclaredImage, "Env": []string{"SECRET=DO_NOT_PROJECT_ENV"}},
				}
				image := map[string]any{"RepoDigests": v.RepoDigests, "Os": "linux", "Architecture": "arm64"}
				wantVariant := ""
				if variant != "missing" {
					image["Variant"] = variant
					wantVariant = variant
				}
				f := &collectorExecutor{fn: func(i int, _ context.Context, args []string) (*target.LimitedResult, error) {
					facts := container
					if i == 1 {
						facts = image
					}
					text := renderDockerProjection(t, args[4], facts)
					if strings.Contains(text, "DO_NOT_PROJECT") {
						t.Fatal("optional fields broadened the inspect projection")
					}
					return &target.LimitedResult{Stdout: text}, nil
				}}
				got := (&DockerObserver{Executor: f}).Inspect(context.Background(), target.ConnectionSnapshot{}, ruleContainer)
				if got.Outcome != "ok" || !got.ImageAvailable || !got.Running || got.ContainerID != ruleContainer || got.Health != wantHealth || got.Platform.Variant != wantVariant || got.Platform.Architecture != "arm64" {
					t.Fatalf("strict Docker projection not decoded: %+v", got)
				}
				if len(f.calls) != 2 {
					t.Fatalf("expected container and image projections, got %d calls", len(f.calls))
				}
			})
		}
	}
}

type collectorCall struct {
	args     []string
	limits   target.ExecutionLimits
	deadline time.Time
}
type collectorExecutor struct {
	calls []collectorCall
	fn    func(int, context.Context, []string) (*target.LimitedResult, error)
}

func (f *collectorExecutor) Capture(context.Context, string) (target.ConnectionSnapshot, error) {
	return target.ConnectionSnapshot{}, nil
}
func (f *collectorExecutor) ExecLimited(ctx context.Context, _ target.ConnectionSnapshot, args []string, limits target.ExecutionLimits) (*target.LimitedResult, error) {
	deadline, _ := ctx.Deadline()
	f.calls = append(f.calls, collectorCall{append([]string{}, args...), limits, deadline})
	return f.fn(len(f.calls)-1, ctx, args)
}

func projectedContainer(t *testing.T, change func(map[string]any)) string {
	t.Helper()
	v := ruleSnapshot()
	raw := map[string]any{"containerId": v.ContainerID, "createdAt": v.CreatedAt, "startedAt": v.StartedAt, "finishedAt": v.FinishedAt, "configId": v.ConfigID, "declaredImage": v.DeclaredImage, "running": v.Running, "oomKilled": v.OOMKilled, "exitCode": v.ExitCode, "restartCount": v.RestartCount, "health": v.Health}
	if change != nil {
		change(raw)
	}
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func projectedImage(t *testing.T) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"repoDigests": ruleSnapshot().RepoDigests, "os": "linux", "architecture": "amd64", "variant": ""})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestCollectorReadOnlyProjectionAndSharedDeadline(t *testing.T) {
	f := &collectorExecutor{}
	f.fn = func(i int, ctx context.Context, args []string) (*target.LimitedResult, error) {
		if i == 0 {
			time.Sleep(10 * time.Millisecond)
			return &target.LimitedResult{Stdout: projectedContainer(t, nil)}, nil
		}
		return &target.LimitedResult{Stdout: projectedImage(t)}, nil
	}
	before := time.Now()
	got := (&DockerObserver{Executor: f}).Inspect(context.Background(), target.ConnectionSnapshot{}, ruleContainer)
	if got.Outcome != "ok" || !got.ImageAvailable || got.ContainerID != ruleContainer || !got.Running {
		t.Fatalf("snapshot=%+v", got)
	}
	if !got.CollectedAt.After(before) || got.RepositoryDigestKind != deployattempt.DigestRepositoryUnknown || got.VerifiedRepositoryDigest != "" || got.RepositoryDigestSource != "" {
		t.Fatalf("server timestamp or invented media type: %+v", got)
	}
	if len(f.calls) != 2 || !f.calls[0].deadline.Equal(f.calls[1].deadline) || f.calls[0].deadline.Sub(before) > 5*time.Second+50*time.Millisecond {
		t.Fatalf("inspect calls do not share one five-second deadline: %+v", f.calls)
	}
	for i, c := range f.calls {
		if c.limits.Bytes != 8192 || c.limits.Lines != 1000 || len(c.args) != 6 || c.args[0] != "docker" || c.args[2] != "inspect" || c.args[3] != "--format" {
			t.Fatalf("unbounded/non-inspect call=%+v", c)
		}
		if strings.Contains(c.args[4], ".Env") || strings.Contains(c.args[4], ".Mounts") || strings.Contains(c.args[4], "{{json .Config}}") || strings.Contains(c.args[4], ".Health.Log") {
			t.Fatalf("broad sensitive projection: %v", c.args)
		}
		want := ruleContainer
		if i == 1 {
			want = ruleSnapshot().ConfigID
		}
		if c.args[5] != want {
			t.Fatalf("inspect object=%q want=%q", c.args[5], want)
		}
	}
}

func TestCollectorRejectsMalformedOrInjectedFacts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      func() string
		truncated bool
	}{
		{"missing running", func() string { return projectedContainer(t, func(m map[string]any) { delete(m, "running") }) }, false},
		{"missing health configuration signal", func() string { return projectedContainer(t, func(m map[string]any) { delete(m, "health") }) }, false},
		{"malformed state time", func() string { return projectedContainer(t, func(m map[string]any) { m["startedAt"] = "yesterday" }) }, false},
		{"negative restart count", func() string { return projectedContainer(t, func(m map[string]any) { m["restartCount"] = -1 }) }, false},
		{"injected server timestamp", func() string {
			return projectedContainer(t, func(m map[string]any) { m["collectedAt"] = "2099-01-01T00:00:00Z" })
		}, false},
		{"injected identity authority", func() string { return projectedContainer(t, func(m map[string]any) { m["imageAvailable"] = true }) }, false},
		{"duplicate contradictory state", func() string { return strings.TrimSuffix(projectedContainer(t, nil), "}") + `,"running":false}` }, false},
		{"trailing second object", func() string { return projectedContainer(t, nil) + ` {}` }, false},
		{"truncated projection", func() string { return projectedContainer(t, nil) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &collectorExecutor{fn: func(int, context.Context, []string) (*target.LimitedResult, error) {
				return &target.LimitedResult{Stdout: tc.body(), Truncated: tc.truncated}, nil
			}}
			v := (&DockerObserver{Executor: f}).Inspect(context.Background(), target.ConnectionSnapshot{}, ruleContainer)
			if v.Outcome != "unavailable" || v.ImageAvailable || len(f.calls) != 1 {
				t.Fatalf("invalid projection became evidence: %+v calls=%d", v, len(f.calls))
			}
		})
	}
}

func TestCollectorMissingContainerAndErrorsAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, stderr string
		err          error
		want         string
	}{
		{"Docker missing exact object", "Error: No such object: " + ruleContainer, nil, "not_found"},
		{"Docker missing container", "Error response from daemon: No such container: " + ruleContainer, nil, "not_found"},
		{"diagnostic phrase is not proof", "permission denied; No such container: " + ruleContainer, nil, "unavailable"},
		{"wrong missing object", "Error: No such object: other", nil, "unavailable"},
		{"SSH error", "No such container: " + ruleContainer, errors.New("ssh secret=DO_NOT_LEAK"), "unavailable"},
		{"target changed", "secret=DO_NOT_LEAK", target.ErrTargetChanged, "target_changed"},
		{"server deleted", "secret=DO_NOT_LEAK", target.ErrNotFound, "target_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &collectorExecutor{fn: func(int, context.Context, []string) (*target.LimitedResult, error) {
				return &target.LimitedResult{ExitCode: 1, Stderr: tc.stderr}, tc.err
			}}
			v := (&DockerObserver{Executor: f}).Inspect(context.Background(), target.ConnectionSnapshot{}, ruleContainer)
			body, _ := json.Marshal(v)
			if v.Outcome != tc.want || strings.Contains(string(body), "DO_NOT_LEAK") || strings.Contains(string(body), tc.stderr) && tc.stderr != "" {
				t.Fatalf("outcome=%+v raw=%s", v, body)
			}
		})
	}
}

func TestCollectorMissingContainerAllowsOnlyWhitespaceStdout(t *testing.T) {
	missing := "Error response from daemon: No such container: " + ruleContainer + "\n"
	for _, tc := range []struct {
		name, stdout, stderr, want string
	}{
		{"empty stdout", "", missing, "not_found"},
		{"Docker newline stdout", "\n", missing, "not_found"},
		{"whitespace stdout", " \t\r\n", missing, "not_found"},
		{"nonwhite stdout", "{}\n", missing, "unavailable"},
		{"diagnostic stdout", "warning\n", missing, "unavailable"},
		{"wrong full ID", "\n", "Error response from daemon: No such container: " + strings.Repeat("f", 64) + "\n", "unavailable"},
		{"partial requested ID", "\n", "Error response from daemon: No such container: " + ruleContainer[:12] + "\n", "unavailable"},
		{"extra stderr diagnostic", "\n", missing + "permission denied\n", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &collectorExecutor{fn: func(int, context.Context, []string) (*target.LimitedResult, error) {
				return &target.LimitedResult{ExitCode: 1, Stdout: tc.stdout, Stderr: tc.stderr}, nil
			}}
			v := (&DockerObserver{Executor: f}).Inspect(context.Background(), target.ConnectionSnapshot{}, ruleContainer)
			if v.Outcome != tc.want || v.ImageAvailable || len(f.calls) != 1 {
				t.Fatalf("missing container outcome=%+v calls=%d", v, len(f.calls))
			}
			if tc.want == "not_found" && v.Reason != "container_not_found" {
				t.Fatalf("missing container reason=%q", v.Reason)
			}
		})
	}
}

func TestCollectorSecondStepFailureKeepsContainerFacts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		err                error
		truncated, changed bool
	}{
		{"image SSH timeout", context.DeadlineExceeded, false, false},
		{"image truncated", nil, true, false},
		{"credential revoked after container exit", target.ErrCredentialNotFound, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &collectorExecutor{fn: func(i int, _ context.Context, _ []string) (*target.LimitedResult, error) {
				if i == 0 {
					return &target.LimitedResult{Stdout: projectedContainer(t, func(m map[string]any) { m["running"] = false; m["exitCode"] = 1 })}, nil
				}
				return &target.LimitedResult{Stdout: projectedImage(t), Truncated: tc.truncated}, tc.err
			}}
			v := (&DockerObserver{Executor: f}).Inspect(context.Background(), target.ConnectionSnapshot{}, ruleContainer)
			if v.Outcome != "ok" || v.Running || v.ImageAvailable || v.TargetChanged != tc.changed {
				t.Fatalf("lost actual container state: %+v", v)
			}
			target := ruleTarget()
			sampleRule(&target, v, ruleStart)
			requireRuleResult(t, &target, "failed")
		})
	}
}

func TestCollectorCancelsWholeInspection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &collectorExecutor{fn: func(int, context.Context, []string) (*target.LimitedResult, error) {
		cancel()
		return &target.LimitedResult{Stdout: projectedContainer(t, nil)}, nil
	}}
	v := (&DockerObserver{Executor: f}).Inspect(ctx, target.ConnectionSnapshot{}, ruleContainer)
	if len(f.calls) != 1 || v.ImageAvailable {
		t.Fatalf("cancelled inspection made a second call: %+v calls=%d", v, len(f.calls))
	}
}

func TestCollectorRejectsUnboundedSelectors(t *testing.T) {
	f := &collectorExecutor{fn: func(int, context.Context, []string) (*target.LimitedResult, error) {
		t.Fatal("invalid selector executed")
		return nil, nil
	}}
	d := &DockerObserver{Executor: f}
	for _, id := range []string{"app", "--all", ruleContainer + ";restart", ""} {
		if d.Inspect(context.Background(), target.ConnectionSnapshot{}, id).Outcome != "unavailable" {
			t.Fatal("invalid container ID accepted")
		}
	}
	for _, name := range []string{"--all", "app;restart", "$(id)", "a/b"} {
		if d.Baseline(context.Background(), target.ConnectionSnapshot{}, name).Outcome != "unavailable" {
			t.Fatal("invalid baseline name accepted")
		}
	}
	for _, window := range [][2]time.Time{{ruleStart, ruleStart}, {ruleStart.Add(time.Second), ruleStart}, {ruleStart, ruleStart.Add(62 * time.Second)}} {
		if _, _, reason := d.Logs(context.Background(), target.ConnectionSnapshot{}, ruleContainer, window[0], window[1]); reason != "log_scope_invalid" {
			t.Fatal("invalid log window accepted")
		}
	}
}

func TestCollectorLogWindowAndPerStreamRedaction(t *testing.T) {
	m := mask.NewMasker()
	m.RegisterSecret("KNOWN-SECRET-VALUE")
	stamp := ruleStart.Add(time.Second).Format(time.RFC3339Nano)
	out := stamp + ` {"password": "secret with spaces", "other": 1}` + "\n" + stamp + " Authorization: Bearer abc123secret\n" + stamp + " KNOWN-SEC"
	errout := stamp + " diagnostic\n" + ruleStart.Add(-time.Second).Format(time.RFC3339Nano) + " before-window\n" + ruleStart.Add(time.Minute).Format(time.RFC3339Nano) + " at-exclusive-end\nraw stderr without timestamp"
	f := &collectorExecutor{fn: func(int, context.Context, []string) (*target.LimitedResult, error) {
		return &target.LimitedResult{Stdout: out, Stderr: errout, Truncated: true}, nil
	}}
	text, truncated, reason := (&DockerObserver{Executor: f, Masker: m}).Logs(context.Background(), target.ConnectionSnapshot{}, ruleContainer, ruleStart, ruleStart.Add(time.Minute))
	for _, secret := range []string{"secret with spaces", "abc123secret", "KNOWN-SEC", "before-window", "at-exclusive-end", "raw stderr"} {
		if strings.Contains(text, secret) {
			t.Fatalf("unsafe/out-of-window text %q leaked: %s", secret, text)
		}
	}
	if !strings.Contains(text, "diagnostic") || !strings.Contains(text, "[MASKED]") || !truncated || reason != "logs_window_partial" {
		t.Fatalf("log redaction/window result text=%q truncated=%v reason=%s", text, truncated, reason)
	}
	want := []string{"docker", "logs", "--timestamps", "--since", ruleStart.Format(time.RFC3339Nano), "--until", ruleStart.Add(time.Minute).Format(time.RFC3339Nano), "--tail", "1000", ruleContainer}
	if !reflect.DeepEqual(f.calls[0].args, want) || f.calls[0].limits.Bytes != 32<<10 {
		t.Fatalf("log call not bounded to approved window: %+v", f.calls[0])
	}
}

func TestCollectorLogErrorNeverReturnsRawStderr(t *testing.T) {
	f := &collectorExecutor{fn: func(int, context.Context, []string) (*target.LimitedResult, error) {
		return &target.LimitedResult{ExitCode: 1, Stderr: "secret=DO_NOT_LEAK"}, nil
	}}
	text, _, reason := (&DockerObserver{Executor: f}).Logs(context.Background(), target.ConnectionSnapshot{}, ruleContainer, ruleStart, ruleStart.Add(time.Minute))
	if text != "" || reason != "logs_unavailable" {
		t.Fatalf("raw Docker error exposed: %q %s", text, reason)
	}
}
