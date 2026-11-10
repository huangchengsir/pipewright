package kubernetes

import (
	"encoding/hex"
	"io"
	"path"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ReadReleaseGitFiles reads committed blobs from the exact resolved source SHA.
// A script-modified checkout is never evidence of repository-authored manifests.
func ReadReleaseGitFiles(root, commit, relative string) ([]ReleaseInputFile, error) {
	if len(commit) != 40 || relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../") || strings.ContainsAny(relative, "\\:\x00") || relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return nil, ErrManifest
	}
	if _, e := hex.DecodeString(commit); e != nil {
		return nil, ErrManifest
	}
	repository, e := git.PlainOpen(root)
	if e != nil {
		return nil, ErrManifest
	}
	revision, e := repository.CommitObject(plumbing.NewHash(commit))
	if e != nil {
		return nil, ErrManifest
	}
	tree, e := revision.Tree()
	if e != nil {
		return nil, ErrManifest
	}
	var files []*object.File
	prefix := ""
	direct := false
	if file, e := tree.File(relative); e == nil {
		direct = true
		files = append(files, file)
	} else {
		directory := tree
		if relative != "." {
			directory, e = tree.Tree(relative)
			if e != nil {
				return nil, ErrManifest
			}
			prefix = relative + "/"
		}
		iterator := directory.Files()
		defer iterator.Close()
		if e = iterator.ForEach(func(file *object.File) error {
			ext := strings.ToLower(path.Ext(file.Name))
			if ext != ".yaml" && ext != ".yml" && ext != ".json" {
				return nil
			}
			if file.Mode != filemode.Regular && file.Mode != filemode.Executable {
				return ErrManifest
			}
			files = append(files, file)
			if len(files) > MaxReleaseResources {
				return ErrManifest
			}
			return nil
		}); e != nil {
			return nil, ErrManifest
		}
	}
	if len(files) == 0 {
		return nil, ErrManifest
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	total := 0
	out := []ReleaseInputFile{}
	for _, file := range files {
		if file.Mode != filemode.Regular && file.Mode != filemode.Executable {
			return nil, ErrManifest
		}
		reader, e := file.Reader()
		if e != nil {
			return nil, ErrManifest
		}
		body, e := io.ReadAll(io.LimitReader(reader, int64(MaxReleaseInputBytes-total)+1))
		reader.Close()
		if e != nil || len(body) > MaxReleaseInputBytes-total {
			return nil, ErrManifest
		}
		total += len(body)
		name := prefix + file.Name
		if direct {
			name = relative
		}
		out = append(out, ReleaseInputFile{Path: name, Content: body, Digest: releaseByteDigest(body)})
	}
	return out, nil
}
