package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"path"
	"sort"
	"strings"
)

func ReadReleaseArchive(data []byte, selected string) ([]ReleaseInputFile, error) {
	if len(data) > MaxReleaseInputBytes {
		return nil, ErrManifest
	}
	if selected != "" && (path.IsAbs(selected) || path.Clean(selected) != selected || selected == ".." || strings.HasPrefix(selected, "../") || strings.ContainsAny(selected, "\\:\x00")) {
		return nil, ErrManifest
	}
	z, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return nil, ErrManifest
	}
	defer z.Close()
	reader := tar.NewReader(io.LimitReader(z, MaxReleaseInputBytes+1))
	out := []ReleaseInputFile{}
	total, entries := int64(0), 0
	seen := map[string]bool{}
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, ErrManifest
		}
		entries++
		name := strings.TrimSuffix(header.Name, "/")
		if entries > 8192 || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") || header.Size < 0 || header.Size > MaxReleaseInputBytes {
			return nil, ErrManifest
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, ErrManifest
		}
		total += header.Size
		if total > MaxReleaseInputBytes {
			return nil, ErrManifest
		}
		chosen := selected == "" || selected == "." || name == selected || strings.HasPrefix(name, selected+"/")
		if !chosen {
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if name != selected && ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		if seen[name] {
			return nil, ErrManifest
		}
		seen[name] = true
		body, e := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if e != nil || int64(len(body)) != header.Size {
			return nil, ErrManifest
		}
		out = append(out, ReleaseInputFile{Path: name, Content: body, Digest: releaseByteDigest(body)})
		if len(out) > MaxReleaseResources {
			return nil, ErrManifest
		}
	}
	if len(out) == 0 {
		return nil, ErrManifest
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
