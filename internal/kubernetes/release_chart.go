package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"io"
	"path"
	"sort"
	"strings"
)

func ReadReleaseGitChart(root, commit, relative string) ([]byte, error) {
	if len(commit) != 40 || relative == "" || path.Clean(relative) != relative || path.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, "../") || strings.ContainsAny(relative, "\\:\x00") || relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return nil, ErrManifest
	}
	if _, e := hex.DecodeString(commit); e != nil {
		return nil, ErrManifest
	}
	repo, e := git.PlainOpen(root)
	if e != nil {
		return nil, ErrManifest
	}
	revision, e := repo.CommitObject(plumbing.NewHash(commit))
	if e != nil {
		return nil, ErrManifest
	}
	tree, e := revision.Tree()
	if e != nil {
		return nil, ErrManifest
	}
	if file, e := tree.File(relative); e == nil {
		if file.Mode != filemode.Regular && file.Mode != filemode.Executable {
			return nil, ErrManifest
		}
		reader, e := file.Reader()
		if e != nil {
			return nil, ErrManifest
		}
		defer reader.Close()
		body, e := io.ReadAll(io.LimitReader(reader, MaxReleaseInputBytes+1))
		if e != nil || len(body) > MaxReleaseInputBytes {
			return nil, ErrManifest
		}
		return body, nil
	}
	if relative != "." {
		tree, e = tree.Tree(relative)
		if e != nil {
			return nil, ErrManifest
		}
	}
	files := []*object.File{}
	iterator := tree.Files()
	defer iterator.Close()
	size := int64(0)
	if e = iterator.ForEach(func(file *object.File) error {
		if file.Mode != filemode.Regular && file.Mode != filemode.Executable {
			return ErrManifest
		}
		size += file.Size
		files = append(files, file)
		if len(files) > 4096 || size > MaxReleaseInputBytes || file.Size > MaxReleaseObjectBytes {
			return ErrManifest
		}
		return nil
	}); e != nil {
		return nil, e
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(zip)
	for _, file := range files {
		if e = writer.WriteHeader(&tar.Header{Name: "chart/" + file.Name, Mode: 0600, Size: file.Size, Typeflag: tar.TypeReg}); e != nil {
			return nil, e
		}
		reader, e := file.Reader()
		if e != nil {
			return nil, e
		}
		_, e = io.Copy(writer, reader)
		reader.Close()
		if e != nil {
			return nil, e
		}
	}
	if writer.Close() != nil || zip.Close() != nil || buffer.Len() > MaxReleaseInputBytes {
		return nil, ErrManifest
	}
	return buffer.Bytes(), nil
}
