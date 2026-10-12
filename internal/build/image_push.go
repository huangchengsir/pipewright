package build

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
)

var (
	imageRepositoryPart = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	imageTagPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	registryHostLabel   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	imageManifestDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	// Docker-family login writes shared CLI auth state. Keep login/tag/push atomic
	// across jobs and runs without changing Driver or blocking cancellation.
	imagePushGate = make(chan struct{}, 1)
)

type imagePushTarget struct {
	host, reference, credentialID string
}

// Parse registry URLs structurally; credentials and invalid input never enter errors.
func parseImageRegistry(raw string) (host, namespace string, err error) {
	bad := errors.New("invalid image registry")
	if raw == "" || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.IndexFunc(raw, unicode.IsControl) >= 0 || strings.ContainsAny(raw, `\?#%@`) {
		return "", "", bad
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Host == "" || u.Opaque != "" {
		return "", "", bad
	}
	h := u.Hostname()
	if strings.Contains(h, ":") {
		if net.ParseIP(h) == nil || !strings.HasPrefix(u.Host, "[") {
			return "", "", bad
		}
	} else {
		if len(h) > 253 {
			return "", "", bad
		}
		for _, label := range strings.Split(h, ".") {
			if len(label) > 63 || !registryHostLabel.MatchString(label) {
				return "", "", bad
			}
		}
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return "", "", bad
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", "", bad
	}
	if !strings.ContainsAny(h, ".:") && u.Port() == "" && !strings.EqualFold(h, "localhost") {
		return "", "", bad // Docker otherwise interprets this as a Hub namespace.
	}
	if strings.Contains(u.Path, "//") {
		return "", "", bad
	}
	namespace = strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	if namespace != "" && !validImageRepository(namespace) {
		return "", "", bad
	}
	return strings.ToLower(u.Host), namespace, nil
}

func validImageRepository(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if !imageRepositoryPart.MatchString(part) {
			return false
		}
	}
	return true
}

func imageConfigString(cfg map[string]any, key string) (string, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid push_image %s", key)
	}
	return strings.TrimSpace(s), nil
}

func resolveImagePushTarget(cfg map[string]any, registry *pipeline.ImageRegistry, proj *project.Project, commit string) (imagePushTarget, error) {
	var target imagePushTarget
	raw, err := imageConfigString(cfg, "registry")
	if err != nil {
		return target, err
	}
	if raw == "" && registry != nil {
		raw = strings.TrimSpace(registry.URL)
	}
	host, ns, err := parseImageRegistry(raw)
	if err != nil {
		return target, err
	}
	name, err := imageConfigString(cfg, "imageName")
	if err != nil {
		return target, err
	}
	if name == "" && proj != nil {
		name = slugify(proj.Name)
	}
	// A registry-qualified imageName would silently redirect a supposedly relative name.
	first, _, hasSlash := strings.Cut(name, "/")
	if !validImageRepository(name) || (hasSlash && (strings.Contains(first, ".") || first == "localhost")) {
		return target, errors.New("invalid push_image imageName")
	}
	if ns != "" {
		name = ns + "/" + name
	}
	tag, err := imageConfigString(cfg, "tag")
	if err != nil {
		return target, err
	}
	if strings.Contains(tag, "${COMMIT_SHA}") {
		if commit == "" || commit == "latest" {
			return target, errors.New("push_image COMMIT_SHA unavailable")
		}
		tag = strings.ReplaceAll(tag, "${COMMIT_SHA}", commit)
	}
	if tag == "" {
		tag = commit
		if tag == "" {
			tag = "latest"
		}
	}
	if !imageTagPattern.MatchString(tag) || len(host+"/"+name) > 255 {
		return target, errors.New("invalid push_image reference or tag")
	}
	cred, err := imageConfigString(cfg, "credentialId")
	if err != nil {
		return target, err
	}
	if cred == "" && registry != nil {
		envHost, _, err := parseImageRegistry(strings.TrimSpace(registry.URL))
		if err == nil && envHost == host {
			cred = registry.CredentialID
		}
	}
	return imagePushTarget{host: host, reference: host + "/" + name + ":" + tag, credentialID: cred}, nil
}

func (b *Builder) pushImageTarget(ctx context.Context, sink run.StepSink, ordinal int, local string, target imagePushTarget) (string, string, error) {
	if ctx.Err() != nil {
		return "", "", run.ErrCanceled
	}
	if !validLocalImageReference(local) {
		return "", "", errors.New("invalid source image reference")
	}
	select {
	case imagePushGate <- struct{}{}:
		defer func() { <-imagePushGate }()
	case <-ctx.Done():
		return "", "", run.ErrCanceled
	}
	if ctx.Err() != nil {
		return "", "", run.ErrCanceled
	}
	onLine := b.lineSink(sink, ordinal)
	var user, pass string
	if target.credentialID != "" {
		if b.vault == nil {
			return "", "", errors.New("registry credential unavailable")
		}
		plain, err := b.vault.Reveal(target.credentialID)
		if err != nil {
			return "", "", errors.New("registry credential unavailable")
		}
		user, pass, _ = strings.Cut(plain, ":")
		if user == "" || pass == "" || strings.IndexFunc(user, unicode.IsSpace) >= 0 || strings.IndexFunc(user, unicode.IsControl) >= 0 || strings.HasPrefix(user, "-") {
			return "", "", errors.New("registry credential requires username and password")
		}
		// Retain masking for later tag/push output too; never depend solely on sink setup.
		base := onLine
		onLine = func(stream, line string) {
			line = strings.ReplaceAll(line, plain, "***")
			line = strings.ReplaceAll(line, pass, "***")
			base(stream, line)
		}
		run.RecordExecution(sink, run.ExecutionReal)
		code, err := b.driver.Login(ctx, target.host, user, pass, onLine)
		if err != nil || code != 0 {
			if ctx.Err() != nil {
				return "", "", run.ErrCanceled
			}
			return "", "", errors.New("registry login failed")
		}
	}
	if ctx.Err() != nil {
		return "", "", run.ErrCanceled
	}
	run.RecordExecution(sink, run.ExecutionReal)
	code, err := b.driver.Tag(ctx, local, target.reference, onLine)
	if err != nil || code != 0 {
		if ctx.Err() != nil {
			return "", "", run.ErrCanceled
		}
		return "", "", errors.New("image tag failed")
	}
	if ctx.Err() != nil {
		return "", "", run.ErrCanceled
	}
	code, err = b.driver.Push(ctx, target.reference, onLine)
	if err != nil || code != 0 {
		if ctx.Err() != nil {
			return "", "", run.ErrCanceled
		}
		return "", "", errors.New("image push failed")
	}
	if ctx.Err() != nil {
		return "", "", run.ErrCanceled
	}
	digest, _, err := b.driver.InspectImage(ctx, target.reference)
	if ctx.Err() != nil {
		return "", "", run.ErrCanceled
	}
	if err != nil || !imageManifestDigest.MatchString(digest) {
		return "", "", errors.New("pushed image repository digest unavailable")
	}
	return target.reference, digest, nil
}

func validLocalImageReference(ref string) bool {
	if strings.Contains(ref, "://") || strings.TrimSpace(ref) != ref || strings.IndexFunc(ref, unicode.IsControl) >= 0 {
		return false
	}
	i := strings.LastIndex(ref, ":")
	if i < 1 || !imageTagPattern.MatchString(ref[i+1:]) {
		return false
	}
	name := ref[:i]
	if validImageRepository(name) {
		return true
	}
	host, repo, err := parseImageRegistry(name)
	return err == nil && host != "" && repo != "" && len(name) <= 255
}

func imageJobLocalTag(proj *project.Project, runID, stageID, jobID, commit string) string {
	key := sha256.Sum256([]byte(runID + "\x00" + stageID + "\x00" + jobID))
	return localTagPrefix + "/" + slugify(proj.Name) + ":" + commit + "-" + fmt.Sprintf("%x", key[:8])
}

// Only the stage lifetime is retained here; cross-stage transfer uses persisted artifacts.
type stageImageArtifacts struct {
	mu   sync.Mutex
	arts []run.Artifact
}

type imageStageReporter struct {
	dagrun.StageReporter
	images *stageImageArtifacts
	stage  pipeline.Stage
	job    pipeline.Job
}

func (r *imageStageReporter) RecordExecution(mode string) { run.RecordExecution(r.StageReporter, mode) }
func (r *imageStageReporter) JobReporter(id string) dagrun.StageReporter {
	var jb pipeline.Job
	for _, j := range r.stage.Jobs {
		if j.ID == id {
			jb = j
			break
		}
	}
	return &imageStageReporter{StageReporter: r.StageReporter.JobReporter(id), images: r.images, stage: r.stage, job: jb}
}

func (r *imageStageReporter) EmitArtifact(ctx context.Context, a run.Artifact) error {
	if a.Type == run.ArtifactImage && isBuildImageJob(r.job.Type) {
		r.images.mu.Lock()
		r.images.arts = append(r.images.arts, a)
		r.images.mu.Unlock()
		if imageBuildConsumedByPush(r.stage, r.job) {
			return nil // Only consumed intermediates are hidden from deployment defaults.
		}
	}
	return r.StageReporter.EmitArtifact(ctx, a)
}

func imageBuildConsumedByPush(stage pipeline.Stage, build pipeline.Job) bool {
	for _, job := range stage.Jobs {
		if strings.TrimSpace(job.Type) == "push_image" && pushImageSourceJobs(stage, job)[build.ID] {
			return true
		}
	}
	return false
}

func pushImageSourceJobs(stage pipeline.Stage, job pipeline.Job) map[string]bool {
	byID := make(map[string]pipeline.Job)
	for _, jb := range stage.Jobs {
		byID[jb.ID] = jb
	}
	found, visited := map[string]bool{}, map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		jb := byID[id]
		if isBuildImageJob(jb.Type) && (cfgString(jb.Config, "artifactType") == "" || cfgString(jb.Config, "artifactType") == pipeline.ArtifactImage) {
			found[id] = true
		}
		for _, dep := range jb.Needs {
			visit(dep)
		}
	}
	if len(job.Needs) > 0 {
		for _, dep := range job.Needs {
			visit(dep)
		}
	} else {
		for i, jb := range stage.Jobs {
			if isBuildImageJob(jb.Type) && (cfgString(jb.Config, "artifactType") == "" || cfgString(jb.Config, "artifactType") == pipeline.ArtifactImage) {
				if found[jb.ID] {
					found[fmt.Sprintf("duplicate-source-%d", i)] = true
				} else {
					found[jb.ID] = true
				}
			}
		}
	}
	return found
}

func (b *Builder) runPushImageJob(ctx context.Context, r *run.Run, stage pipeline.Stage, job pipeline.Job, rep dagrun.StageReporter, proj *project.Project, settings *pipeline.Settings, images *stageImageArtifacts) error {
	if ctx.Err() != nil {
		return run.ErrCanceled
	}
	images.mu.Lock()
	arts := append([]run.Artifact(nil), images.arts...)
	images.mu.Unlock()
	sources := pushImageSourceJobs(stage, job)
	if len(sources) > 1 {
		_ = rep.Log(ctx, streamStderr, "push_image source image is ambiguous")
		return ErrBuildFailed
	}
	if len(sources) == 0 {
		for _, jb := range stage.Jobs {
			if isBuildImageJob(jb.Type) {
				_ = rep.Log(ctx, streamStderr, "push_image has no image build in its dependencies")
				return ErrBuildFailed
			}
		}
	}
	if len(sources) == 0 && b.artifactLister != nil {
		var err error
		arts, err = b.artifactLister(ctx, r.ID)
		if err != nil {
			_ = rep.Log(ctx, streamStderr, "push_image source artifacts unavailable")
			return ErrBuildFailed
		}
		// Independent parallel stages may already have published images. A direct
		// stage dependency with image outputs takes precedence over unrelated stages.
		var upstream []run.Artifact
		for _, art := range arts {
			if art.Type != run.ArtifactImage {
				continue
			}
			for _, dep := range stage.Needs {
				if metadataString(art, "sourceStageId") == dep {
					upstream = append(upstream, art)
					break
				}
			}
		}
		if len(stage.Needs) > 0 {
			arts = upstream
		}
	}
	unique := map[string]run.Artifact{}
	for _, art := range arts {
		if art.Type != run.ArtifactImage {
			continue
		}
		if len(sources) > 0 && !sources[metadataString(art, "sourceJobId")] {
			continue
		}
		local := metadataString(art, "localReference")
		if local == "" {
			local = art.Reference // Compatibility with artifacts produced before this fix.
		}
		unique[local] = art
	}
	if len(unique) != 1 {
		_ = rep.Log(ctx, streamStderr, "push_image requires exactly one source image; missing or ambiguous")
		return ErrBuildFailed
	}
	var local string
	var source run.Artifact
	for local, source = range unique {
	}
	commit := metadataString(source, "commitTag")
	if commit == "" {
		commit = strings.TrimSpace(r.Trigger.Commit)
		if len(commit) > 7 {
			commit = commit[:7]
		}
	}
	target, err := resolveImagePushTarget(job.Config, b.resolveRegistry(settings, r.Trigger.ResolvedEnvironment), proj, commit)
	if err == nil {
		var remote, digest string
		remote, digest, err = b.pushImageTarget(ctx, &reporterSink{rep: rep}, 0, local, target)
		if err == nil {
			meta := make(map[string]any, len(source.Metadata)+5)
			for k, v := range source.Metadata {
				meta[k] = v
			}
			meta["localReference"], meta["commitTag"] = local, commit
			meta["sourceStageId"], meta["sourceStage"] = stage.ID, stage.Name
			meta["sourceJobId"], meta["sourceJob"] = job.ID, job.Name
			meta["pushed"], meta["declarationIndex"] = true, 0
			meta["digestRepository"] = imageReferenceRepository(remote)
			delete(meta, "digest")
			if digest != "" {
				meta["digest"] = digest
			}
			source.ID = ""
			source.CreatedAt = time.Time{}
			source.Reference, source.Metadata = remote, meta
			if err := rep.EmitArtifact(ctx, source); err != nil {
				_ = rep.Log(ctx, streamStderr, "push_image artifact registration failed")
				return ErrBuildFailed
			}
			_ = rep.Log(ctx, streamStdout, "image pushed: "+remote)
			return nil
		}
	}
	if errors.Is(err, run.ErrCanceled) || ctx.Err() != nil {
		return run.ErrCanceled
	}
	_ = rep.Log(ctx, streamStderr, "push_image failed: "+err.Error())
	return ErrBuildFailed
}

func metadataString(a run.Artifact, key string) string {
	s, _ := a.Metadata[key].(string)
	return s
}

// Select only the deployment's own branch; storage order is not a dependency.
func (b *Builder) preferPublishedImage(ctx context.Context, runID string, cfg map[string]string, stage pipeline.Stage, job pipeline.Job) error {
	prefer := strings.ToLower(strings.TrimSpace(cfg["artifactType"]))
	if b.artifactLister == nil || cfg["artifactSource"] != "" || (prefer != "" && prefer != run.ArtifactImage) {
		return nil
	}
	arts, err := b.artifactLister(ctx, runID)
	if err != nil {
		return errors.New("image deployment artifacts unavailable")
	}
	if prefer == "" {
		hasImage := false
		for _, a := range arts {
			hasImage = hasImage || a.Type == run.ArtifactImage
			if a.Type != run.ArtifactImage {
				return nil // Default file/script deployment remains owned by deploy.
			}
		}
		if !hasImage {
			return nil
		}
	}
	ancestors := imageJobAncestors(stage, job)
	var scoped []run.Artifact
	for _, a := range arts {
		inScope := metadataString(a, "sourceStageId") == stage.ID && (len(job.Needs) == 0 || ancestors[metadataString(a, "sourceJobId")])
		if !inScope {
			for _, dep := range stage.Needs {
				if metadataString(a, "sourceStageId") == dep {
					inScope = true
					break
				}
			}
		}
		if inScope {
			scoped = append(scoped, a)
		}
	}
	// Preserve existing default file precedence, but unrelated images cannot win.
	if prefer == "" {
		for _, a := range scoped {
			if a.Type != run.ArtifactImage {
				return nil
			}
		}
	}
	var candidates []run.Artifact
	for _, a := range scoped {
		if a.Type != run.ArtifactImage {
			continue
		}
		shadowed := false
		if a.Metadata["pushed"] != true {
			for _, p := range scoped {
				if p.Type == run.ArtifactImage && p.Metadata["pushed"] == true && metadataString(p, "localReference") == a.Reference {
					shadowed = true
					break
				}
			}
		}
		if !shadowed {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return errors.New("image deployment has no image in its dependencies; select an artifact source")
	}
	if len(candidates) > 1 {
		return errors.New("dependency image is ambiguous; select an artifact source")
	}
	a := candidates[0]
	stageID, jobID := metadataString(a, "sourceStageId"), metadataString(a, "sourceJobId")
	if stageID == "" || jobID == "" {
		return errors.New("image deployment source identity unavailable; select an artifact source")
	}
	selector, err := json.Marshal(struct {
		StageID          string `json:"stageId"`
		JobID            string `json:"jobId"`
		DeclarationIndex int    `json:"declarationIndex"`
	}{stageID, jobID, 0})
	if err != nil {
		return err
	}
	cfg["artifactSource"] = string(selector)
	return nil
}

func imageJobAncestors(stage pipeline.Stage, job pipeline.Job) map[string]bool {
	byID := make(map[string]pipeline.Job, len(stage.Jobs))
	for _, j := range stage.Jobs {
		byID[j.ID] = j
	}
	found := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if found[id] {
			return
		}
		found[id] = true
		for _, dep := range byID[id].Needs {
			visit(dep)
		}
	}
	for _, dep := range job.Needs {
		visit(dep)
	}
	return found
}

func imageReferenceRepository(ref string) string {
	if repo, _, ok := strings.Cut(ref, "@"); ok {
		return repo
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i]
	}
	return ref
}

// An image config ID or another repository's digest cannot pin a remote tag.
func inspectImageReference(out, ref string) (string, int64, error) {
	var shape inspectShape
	if err := json.Unmarshal([]byte(out), &shape); err != nil {
		var shapes []inspectShape
		if err := json.Unmarshal([]byte(out), &shapes); err != nil || len(shapes) != 1 {
			return "", 0, errors.New("image inspection unavailable")
		}
		shape = shapes[0]
	}
	repo := imageReferenceRepository(ref)
	digest := ""
	for _, rd := range shape.RepoDigests {
		r, d, ok := strings.Cut(rd, "@")
		if !ok || r != repo || !imageManifestDigest.MatchString(d) {
			continue
		}
		if digest != "" && digest != d {
			return "", shape.Size, errors.New("image repository digest ambiguous")
		}
		digest = d
	}
	if digest != "" {
		return digest, shape.Size, nil
	}
	first, _, _ := strings.Cut(repo, "/")
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return "", shape.Size, errors.New("image repository digest unavailable")
	}
	return shape.ID, shape.Size, nil // Local build artifact compatibility only.
}
