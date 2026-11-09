package kubernetes

import (
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReleaseGitUsesCommittedBytesAndPreservesRootFilename(t *testing.T) {
	root := t.TempDir()
	repository, e := git.PlainInit(root, false)
	if e != nil {
		t.Fatal(e)
	}
	tree, e := repository.Worktree()
	if e != nil {
		t.Fatal(e)
	}
	const committed = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n"
	if e = os.WriteFile(filepath.Join(root, "config.yaml"), []byte(committed), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = tree.Add("config.yaml"); e != nil {
		t.Fatal(e)
	}
	hash, e := tree.Commit("fixture", &git.CommitOptions{Author: &object.Signature{Name: "fixture", Email: "fixture@invalid", When: time.Unix(1, 0)}})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "config.yaml"), []byte("modified by script"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{".", "config.yaml"} {
		files, e := ReadReleaseGitFiles(root, hash.String(), path)
		if e != nil || len(files) != 1 || files[0].Path != "config.yaml" || string(files[0].Content) != committed {
			t.Fatal("working tree used or identity lost", path, files, e)
		}
	}
}
func TestAdmittedResourceRequiresCompleteFrozenTemplate(t *testing.T) {
	resource := ReleaseResource{ID: ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "web"}, Object: releaseTestObject(`{"spec":{"template":{"spec":{"containers":[{"name":"web","image":"app@sha256:abc"}]}}}}`), Expected: releaseTestObject(`{"spec":{"template":{"spec":{"containers":[{"name":"web","image":"app@sha256:abc"},{"name":"admitted-sidecar","image":"sidecar@sha256:abc"}]}}}}`)}
	actual := releaseObjectCopy(resource.Expected)
	plan := ReleasePlan{Resources: []ReleaseResource{resource}}
	if !releaseExpectedObserved(plan, resource.ID, actual) {
		t.Fatal("approved admission addition rejected")
	}
	releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(actual["spec"])["template"])["spec"])["containers"])[1])["image"] = "foreign"
	if releaseExpectedObserved(plan, resource.ID, actual) {
		t.Fatal("admitted sidecar mutation hidden")
	}
}
