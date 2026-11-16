package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPipelineSnapshotBindsSameCloneHEADAndUntruncatedYAML(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		absent, rejected bool
	}{{name: "valid", body: "stages: []\n"}, {name: "absent", absent: true}, {name: "binary", body: "secret\x00data", rejected: true}, {name: "oversize", body: strings.Repeat("x", maxBlobBytes+1), rejected: true}} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"README.md": "test"}
			if !tc.absent {
				files[".pipewright.yml"] = tc.body
			}
			repo := makeBareSourceRepo(t, files)
			reader := goGitSourceReader{allowInsecure: true}
			result, err := reader.PipelineSnapshot(context.Background(), repo, "", "", "main")
			if tc.rejected {
				if !errors.Is(err, ErrPipelineSnapshot) || result.CommitSHA != "" || result.YAML != "" {
					t.Fatal("unsafe snapshot accepted", err)
				}
				return
			}
			if err != nil || len(result.CommitSHA) != 40 || result.Found == tc.absent || result.YAML != tc.body {
				t.Fatal(result, err)
			}
			// Old browsing still uses its unchanged DTO and truncation behavior.
			if !tc.absent {
				blob, err := reader.Blob(context.Background(), repo, "", "", "main", ".pipewright.yml")
				if err != nil || blob.Content != tc.body {
					t.Fatal(blob, err)
				}
			}
		})
	}
}
