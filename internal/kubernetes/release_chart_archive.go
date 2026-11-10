package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"path"
	"strings"
)

func SelectReleaseChartArchive(data []byte, selected string) ([]byte, error) {
	if len(data) > MaxReleaseInputBytes || selected == "" || path.IsAbs(selected) || path.Clean(selected) != selected || selected == ".." || strings.HasPrefix(selected, "../") || strings.ContainsAny(selected, "\\:\x00") {
		return nil, ErrManifest
	}
	zip, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return nil, ErrManifest
	}
	defer zip.Close()
	reader := tar.NewReader(io.LimitReader(zip, MaxReleaseInputBytes+1))
	var buffer bytes.Buffer
	outputZip := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(outputZip)
	total, count, chosen := int64(0), 0, 0
	seen := map[string]bool{}
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, ErrManifest
		}
		name := strings.TrimSuffix(header.Name, "/")
		count++
		if count > 4096 || header.Size < 0 || header.Size > MaxReleaseInputBytes || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") || seen[name] {
			return nil, ErrManifest
		}
		seen[name] = true
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
		if name == selected {
			body, e := io.ReadAll(io.LimitReader(reader, MaxReleaseInputBytes+1))
			if e != nil || int64(len(body)) != header.Size {
				return nil, ErrManifest
			}
			return body, nil
		}
		if !strings.HasPrefix(name, selected+"/") {
			continue
		}
		chosen++
		relative := strings.TrimPrefix(name, selected+"/")
		if e = writer.WriteHeader(&tar.Header{Name: "chart/" + relative, Mode: 0600, Size: header.Size, Typeflag: tar.TypeReg}); e != nil {
			return nil, e
		}
		if _, e = io.Copy(writer, reader); e != nil {
			return nil, e
		}
	}
	if chosen == 0 || writer.Close() != nil || outputZip.Close() != nil || buffer.Len() > MaxReleaseInputBytes {
		return nil, ErrManifest
	}
	return buffer.Bytes(), nil
}
