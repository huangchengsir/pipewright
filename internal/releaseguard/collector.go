package releaseguard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
)

type Observer interface {
	Capture(context.Context, string) (target.ConnectionSnapshot, error)
	Inspect(context.Context, target.ConnectionSnapshot, string) RuntimeSnapshot
	Baseline(context.Context, target.ConnectionSnapshot, string) RuntimeSnapshot
	Logs(context.Context, target.ConnectionSnapshot, string, time.Time, time.Time) (string, bool, string)
}

type DockerObserver struct {
	Executor target.LimitedExecutor
	Masker   *mask.Masker
}

func (d *DockerObserver) Capture(ctx context.Context, id string) (target.ConnectionSnapshot, error) {
	if d == nil || d.Executor == nil {
		return target.ConnectionSnapshot{}, ErrUnavailable
	}
	return d.Executor.Capture(ctx, id)
}

// Docker's template renderer rejects missing map keys even in an if action.
// Health and Variant are optional API fields; index safely handles their absence
// while required identity/state fields retain strict field access.
const containerProjection = `{"containerId":{{json .Id}},"createdAt":{{json .Created}},"startedAt":{{json .State.StartedAt}},"finishedAt":{{json .State.FinishedAt}},"configId":{{json .Image}},"declaredImage":{{json .Config.Image}},"running":{{json .State.Running}},"oomKilled":{{json .State.OOMKilled}},"exitCode":{{json .State.ExitCode}},"restartCount":{{json .RestartCount}},"health":{{with index .State "Health"}}{{json .Status}}{{else}}""{{end}}}`
const imageProjection = `{"repoDigests":{{json .RepoDigests}},"os":{{json .Os}},"architecture":{{json .Architecture}},"variant":{{with index . "Variant"}}{{json .}}{{else}}""{{end}}}`

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var configDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var platformPart = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)
var sensitiveLine = regexp.MustCompile(`(?im)"?(authorization|password|passwd|secret|token|api[_-]?key|private[_-]?key)"?\s*[:=]\s*[^\r\n]*`)
var bearerSecret = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[^\s,;]+`)
var privateKeyBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|\z)`)

func (d *DockerObserver) exec(ctx context.Context, s target.ConnectionSnapshot, args []string, bytes int) (*target.LimitedResult, error) {
	if d == nil || d.Executor == nil {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.Executor.ExecLimited(ctx, s, args, target.ExecutionLimits{Bytes: bytes, Lines: 1000})
}

func unavailableSnapshot(reason string) RuntimeSnapshot {
	return RuntimeSnapshot{Outcome: "unavailable", Reason: reason, CollectedAt: time.Now().UTC(), RepoDigests: []string{}}
}

func (d *DockerObserver) Inspect(ctx context.Context, s target.ConnectionSnapshot, id string) RuntimeSnapshot {
	if !deployattempt.ValidContainerID(id) {
		return unavailableSnapshot("container_identity_unavailable")
	}
	return d.inspect(ctx, s, id)
}
func (d *DockerObserver) Baseline(ctx context.Context, s target.ConnectionSnapshot, name string) RuntimeSnapshot {
	if !containerName.MatchString(name) {
		return unavailableSnapshot("container_name_invalid")
	}
	return d.inspect(ctx, s, name)
}

// The remote projection cannot supply server timestamps, outcomes or proof fields.
type containerFacts struct {
	ContainerID   string  `json:"containerId"`
	CreatedAt     string  `json:"createdAt"`
	StartedAt     string  `json:"startedAt"`
	FinishedAt    string  `json:"finishedAt"`
	ConfigID      string  `json:"configId"`
	DeclaredImage string  `json:"declaredImage"`
	Running       *bool   `json:"running"`
	OOMKilled     *bool   `json:"oomKilled"`
	ExitCode      *int    `json:"exitCode"`
	RestartCount  *int    `json:"restartCount"`
	Health        *string `json:"health"`
}

func decodeProjection(text string, value any) bool {
	keys := json.NewDecoder(strings.NewReader(text))
	if token, err := keys.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for keys.More() {
		token, err := keys.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		if keys.Decode(new(json.RawMessage)) != nil {
			return false
		}
	}
	if token, err := keys.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if dec.Decode(value) != nil {
		return false
	}
	return errors.Is(dec.Decode(new(any)), io.EOF)
}
func targetChanged(err error) bool {
	return errors.Is(err, target.ErrTargetChanged) || errors.Is(err, target.ErrNotFound) || errors.Is(err, target.ErrCredentialNotFound)
}
func dockerContainerMissing(stderr, requested string) bool {
	line := strings.TrimSpace(stderr)
	for _, prefix := range []string{"Error response from daemon: No such container: ", "Error: No such container: ", "Error: No such object: "} {
		if line == prefix+requested {
			return true
		}
	}
	return false
}

func (d *DockerObserver) inspect(ctx context.Context, s target.ConnectionSnapshot, id string) RuntimeSnapshot {
	// Both calls share a single five-second collection budget.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v := unavailableSnapshot("inspection_unavailable")
	res, err := d.exec(ctx, s, []string{"docker", "container", "inspect", "--format", containerProjection, id}, 8192)
	v.CollectedAt = time.Now().UTC()
	if targetChanged(err) {
		v.Outcome = "target_changed"
		v.Reason = "target_configuration_changed"
		return v
	}
	if err != nil || res == nil {
		return v
	}
	if res.Truncated || len(res.Stdout)+len(res.Stderr) > 8192 {
		v.Truncated = true
		v.Reason = "inspection_truncated"
		return v
	}
	if res.ExitCode != 0 {
		if strings.TrimSpace(res.Stdout) == "" && dockerContainerMissing(res.Stderr, id) {
			v.Outcome = "not_found"
			v.Reason = "container_not_found"
		}
		return v
	}
	var raw containerFacts
	if !decodeProjection(res.Stdout, &raw) || !deployattempt.ValidContainerID(raw.ContainerID) || !configDigest.MatchString(raw.ConfigID) || raw.Running == nil || raw.OOMKilled == nil || raw.ExitCode == nil || raw.RestartCount == nil || raw.Health == nil || len(raw.DeclaredImage) > 1024 || strings.ContainsAny(raw.DeclaredImage, "\r\n\x00") || len(*raw.Health) > 32 || *raw.RestartCount < 0 || *raw.RestartCount > 10000000 || *raw.ExitCode < 0 || *raw.ExitCode > 255 {
		return unavailableSnapshot("inspection_invalid")
	}
	for _, stamp := range []string{raw.CreatedAt, raw.StartedAt, raw.FinishedAt} {
		if len(stamp) > 64 {
			return unavailableSnapshot("inspection_invalid_time")
		}
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			return unavailableSnapshot("inspection_invalid_time")
		}
	}
	created, _ := time.Parse(time.RFC3339Nano, raw.CreatedAt)
	started, _ := time.Parse(time.RFC3339Nano, raw.StartedAt)
	if created.IsZero() || (*raw.Running && started.IsZero()) {
		return unavailableSnapshot("inspection_invalid_time")
	}
	v.ContainerID, v.CreatedAt, v.StartedAt, v.FinishedAt = raw.ContainerID, raw.CreatedAt, raw.StartedAt, raw.FinishedAt
	v.ConfigID, v.DeclaredImage = raw.ConfigID, raw.DeclaredImage
	v.Running, v.OOMKilled, v.ExitCode, v.RestartCount = *raw.Running, *raw.OOMKilled, *raw.ExitCode, *raw.RestartCount
	v.Health = *raw.Health
	switch v.Health {
	case "", "starting", "healthy", "unhealthy":
	default:
		v.Health = "unknown"
	}
	v.Outcome = "ok"
	v.Reason = "container_inspected"
	v.RepositoryDigestKind = deployattempt.DigestRepositoryUnknown
	var image struct {
		RepoDigests  []string `json:"repoDigests"`
		OS           string   `json:"os"`
		Architecture string   `json:"architecture"`
		Variant      string   `json:"variant"`
	}
	im, err := d.exec(ctx, s, []string{"docker", "image", "inspect", "--format", imageProjection, v.ConfigID}, 8192)
	if targetChanged(err) {
		v.TargetChanged = true
		v.Reason = "target_configuration_changed"
		return v
	}
	if err != nil || im == nil || im.ExitCode != 0 {
		v.Reason = "image_identity_unavailable"
		return v
	}
	if im.Truncated || len(im.Stdout)+len(im.Stderr) > 8192 {
		v.Truncated = true
		v.Reason = "image_identity_truncated"
		return v
	}
	if !decodeProjection(im.Stdout, &image) || len(image.RepoDigests) > 32 || !platformPart.MatchString(image.OS) || !platformPart.MatchString(image.Architecture) || (image.Variant != "" && !platformPart.MatchString(image.Variant)) {
		v.Reason = "image_identity_invalid"
		return v
	}
	for _, ref := range image.RepoDigests {
		if _, _, ok := repositoryDigest(ref); !ok {
			v.Reason = "image_identity_invalid"
			return v
		}
	}
	v.RepoDigests = image.RepoDigests
	if v.RepoDigests == nil {
		v.RepoDigests = []string{}
	}
	v.Platform = deployattempt.Platform{OS: image.OS, Architecture: image.Architecture, Variant: image.Variant}
	v.ImageAvailable = true
	return v
}

func (d *DockerObserver) Logs(ctx context.Context, s target.ConnectionSnapshot, id string, from, to time.Time) (string, bool, string) {
	if !deployattempt.ValidContainerID(id) || !to.After(from) || to.Sub(from) > time.Minute+time.Second {
		return "", false, "log_scope_invalid"
	}
	r, err := d.exec(ctx, s, []string{"docker", "logs", "--timestamps", "--since", from.UTC().Format(time.RFC3339Nano), "--until", to.UTC().Format(time.RFC3339Nano), "--tail", "1000", id}, 32<<10)
	if err != nil || r == nil || r.ExitCode != 0 {
		return "", false, "logs_unavailable"
	}
	remaining := 32 << 10
	truncated := r.Truncated
	partial := false
	lines := []string{}
	for _, stream := range []string{r.Stdout, r.Stderr} {
		if len(stream) > remaining {
			stream = stream[:remaining]
			truncated = true
		}
		remaining -= len(stream)
		// Scrub each stream before joining, including any truncated secret suffix.
		stream = d.Masker.ScrubTruncated(stream)
		stream = privateKeyBlock.ReplaceAllString(stream, "[REDACTED]")
		stream = sensitiveLine.ReplaceAllString(stream, "[REDACTED]")
		stream = bearerSecret.ReplaceAllString(stream, "[REDACTED]")
		for _, line := range strings.Split(stream, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			stamp, _, ok := strings.Cut(line, " ")
			at, parseErr := time.Parse(time.RFC3339Nano, stamp)
			if !ok || parseErr != nil || at.Before(from) || !at.Before(to) {
				partial = true
				continue
			}
			if len(lines) == 1000 {
				truncated = true
				break
			}
			lines = append(lines, line)
		}
	}
	text := strings.Join(lines, "\n")
	if len(text) > 32<<10 {
		text = text[:32<<10]
		if last := strings.LastIndexByte(text, '\n'); last >= 0 {
			text = text[:last]
		} else {
			text = ""
		}
		truncated = true
	}
	if partial {
		return text, true, "logs_window_partial"
	}
	return text, truncated, "untrusted_windowed_logs"
}
