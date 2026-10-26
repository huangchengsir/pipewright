package buildcache

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// All archive access uses a pinned root. Root prevents following paths outside
// that tree, including paths changed between metadata inspection and opening.
// It does not isolate mounts, device files, or an attacker that moves the root.
func writeTarGz(ctx context.Context, w io.Writer, workspace string, relPaths []string) (int64, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	var payload int64
	seen := make(map[string]bool)
	for _, rel := range relPaths {
		if err := ctx.Err(); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return 0, err
		}
		if hasSymlinkComponent(root, rel) {
			continue
		}
		walkErr := fs.WalkDir(root.FS(), filepath.ToSlash(rel), func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			local, err := archiveLocalName(name)
			if err != nil {
				return err
			}
			if seen[local] {
				return nil
			}
			seen[local] = true
			info, err := root.Lstat(local)
			if err != nil {
				return nil
			}
			var link string
			var file *os.File
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = root.Readlink(local)
				if err != nil {
					return nil
				}
				link = filepath.ToSlash(link)
				if _, err := localLinkTarget(local, link); err != nil {
					return nil
				}
				if err := validateLinkDestination(root, local, link); err != nil {
					return nil
				}
			} else if info.Mode().IsRegular() {
				file, err = root.Open(local)
				if err != nil {
					return err
				}
				defer file.Close()
				info, err = file.Stat()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return errors.New("buildcache: source file changed type")
				}
			} else if !info.IsDir() {
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(local)
			if info.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if file != nil {
				n, err := io.Copy(tw, cacheContextReader{ctx: ctx, r: file})
				if err != nil {
					return err
				}
				payload += n
			}
			return nil
		})
		if walkErr != nil {
			_ = tw.Close()
			_ = gz.Close()
			return 0, walkErr
		}
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		_ = gz.Close()
		return 0, err
	}
	return payload, gz.Close()
}

// This is only a compatibility policy for configured cache roots. The actual
// reads still use Root, so this check is not the security boundary.
func hasSymlinkComponent(root *os.Root, rel string) bool {
	current := ""
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

type cacheLink struct{ name, target string }
type cacheDirectory struct {
	name string
	mode os.FileMode
}

func extractTarGz(ctx context.Context, r io.Reader, dest string) (int64, error) {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	compressed := bufio.NewReader(r)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	var payload int64
	var links []cacheLink
	directoryModes := make(map[string]os.FileMode)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
		name := hdr.Name
		if hdr.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		local, err := archiveLocalName(name)
		if err != nil {
			return 0, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			mode := fileMode(hdr.Mode, 0o755)
			if err := root.MkdirAll(local, mode|0o700); err != nil {
				return 0, err
			}
			directoryModes[local] = mode
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(local), 0o755); err != nil {
				return 0, err
			}
			if err := writeFileFromTar(ctx, root, tr, local, fileMode(hdr.Mode, 0o644)); err != nil {
				return 0, err
			}
			payload += hdr.Size
		case tar.TypeSymlink:
			if _, err := localLinkTarget(local, hdr.Linkname); err != nil {
				return 0, err
			}
			links = append(links, cacheLink{name: local, target: hdr.Linkname})
		default:
			// No device, pipe, or hard-link entries are materialized.
		}
	}
	if err := drainTarPadding(ctx, gz); err != nil {
		return 0, err
	}
	if _, err := compressed.Peek(1); !errors.Is(err, io.EOF) {
		return 0, errors.New("buildcache: unexpected trailing gzip data")
	}
	// Delay links until regular payload is present. On Windows this also lets
	// Symlink identify directory targets rather than creating file links.
	if err := restoreCacheLinks(ctx, root, links); err != nil {
		return 0, err
	}
	// Restore explicit directory modes last, deepest entries first. A directory
	// handle avoids Root.Chmod's documented path race and umask does not erase
	// archive permissions. No chmod is performed through an unchecked host path.
	directories := make([]cacheDirectory, 0, len(directoryModes))
	for name, mode := range directoryModes {
		directories = append(directories, cacheDirectory{name: name, mode: mode})
	}
	sort.Slice(directories, func(i, j int) bool {
		a, b := strings.Count(directories[i].name, string(filepath.Separator)), strings.Count(directories[j].name, string(filepath.Separator))
		if a != b {
			return a > b
		}
		return directories[i].name < directories[j].name
	})
	for i := range directories {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		directory, err := root.Open(directories[i].name)
		if err != nil {
			return 0, err
		}
		info, statErr := directory.Stat()
		if statErr != nil {
			directory.Close()
			return 0, statErr
		}
		if !info.IsDir() {
			directory.Close()
			return 0, errors.New("buildcache: directory changed type")
		}
		chmodErr := directory.Chmod(directories[i].mode)
		closeErr := directory.Close()
		if chmodErr != nil {
			return 0, chmodErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
	}

	return payload, ctx.Err()
}

// archiveLocalName accepts portable, slash-separated archive names. In
// particular Windows drive/UNC/ADS names and parent components are rejected on
// every OS, rather than being silently rewritten into another cache path.
func archiveLocalName(name string) (string, error) {
	if strings.ContainsAny(name, "\\:\x00") || !fs.ValidPath(name) || name == "." {
		return "", fmt.Errorf("buildcache: invalid archive entry: %q", name)
	}
	local, err := filepath.Localize(name)
	if err != nil || !filepath.IsLocal(local) {
		return "", fmt.Errorf("buildcache: invalid archive entry: %q", name)
	}
	return local, nil
}

// Root.Symlink deliberately does not validate its target. Check the target
// separately, allowing ../ used by npm/pnpm only when it stays within the root.
func localLinkTarget(name, target string) (string, error) {
	if target == "" || strings.ContainsAny(target, "\\:\x00") || path.IsAbs(target) {
		return "", errors.New("buildcache: invalid symbolic link target")
	}
	resolved := path.Clean(path.Join(path.Dir(filepath.ToSlash(name)), target))
	if !fs.ValidPath(resolved) {
		return "", errors.New("buildcache: symbolic link escapes root")
	}
	local, err := filepath.Localize(resolved)
	if err != nil || !filepath.IsLocal(local) {
		return "", errors.New("buildcache: symbolic link escapes root")
	}
	return local, nil
}
func validateLinkDestination(root *os.Root, name, target string) error {
	resolved, err := localLinkTarget(name, target)
	if err != nil {
		return err
	}
	if _, err := root.Stat(resolved); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
func validExistingTarget(root *os.Root, name string) error {
	if _, err := root.Stat(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func restoreCacheLinks(ctx context.Context, root *os.Root, links []cacheLink) error {
	var created []string
	fail := func(err error) error {
		for _, name := range created {
			_ = root.Remove(name)
		}
		return err
	}
	pending := append([]cacheLink(nil), links...)
	for len(pending) > 0 {
		next := make([]cacheLink, 0, len(pending))
		progress := false
		for _, link := range pending {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			resolved, err := localLinkTarget(link.name, link.target)
			if err != nil {
				return fail(err)
			}
			if _, err := root.Stat(resolved); errors.Is(err, fs.ErrNotExist) {
				next = append(next, link)
				continue
			} else if err != nil {
				return fail(err)
			}
			if err := restoreCacheLink(root, link); err != nil {
				return fail(err)
			}
			created = append(created, link.name)
			progress = true
		}
		if !progress {
			// Root still protects subsequent accesses through dangling relative links.
			for _, link := range next {
				if err := ctx.Err(); err != nil {
					return fail(err)
				}
				if err := restoreCacheLink(root, link); err != nil {
					return fail(err)
				}
				created = append(created, link.name)
			}
			break
		}
		pending = next
	}
	// Validate actual resolution too: an existing directory alias may cause a
	// relative target to resolve differently from its lexical location.
	for _, name := range created {
		if err := validExistingTarget(root, name); err != nil {
			return fail(err)
		}
	}
	return nil
}
func restoreCacheLink(root *os.Root, link cacheLink) error {
	if err := validateLinkDestination(root, link.name, link.target); err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(link.name), 0o755); err != nil {
		return err
	}
	if err := validExistingTarget(root, link.name); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(link.name), ".cache-link-"+rand.Text())
	if err := root.Symlink(filepath.FromSlash(link.target), temp); err != nil {
		return err
	}
	defer root.Remove(temp)
	return root.Rename(temp, link.name)
}

type cacheContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cacheContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func createRootTemp(root *os.Root, dir, prefix string, mode os.FileMode) (*os.File, string, error) {
	for range 10 {
		name := filepath.Join(dir, prefix+rand.Text())
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return file, name, err
	}
	return nil, "", errors.New("buildcache: temporary file name collision")
}

// Never truncate an existing destination. Rename replaces the directory entry,
// preserving bytes referenced by a pre-existing hard link or symbolic link.
func writeFileFromTar(ctx context.Context, root *os.Root, r io.Reader, name string, mode os.FileMode) error {
	if err := validExistingTarget(root, name); err != nil {
		return err
	}
	file, temp, err := createRootTemp(root, filepath.Dir(name), ".cache-file-", mode)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, copyErr := io.Copy(file, cacheContextReader{ctx: ctx, r: r})
	if copyErr != nil {
		file.Close()
		return copyErr
	}
	// OpenFile permissions are filtered by umask. Chmod the already-opened new
	// file, never a pathname that could have become an outside link.
	chmodErr := file.Chmod(mode)
	closeErr := file.Close()
	if chmodErr != nil {
		return chmodErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temp, name)
}

// Generated archives have no trailing payload. Allow only bounded zero padding.
func drainTarPadding(ctx context.Context, r io.Reader) error {
	remaining := 64 * 1024
	var buf [4096]byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf[:])
		remaining -= n
		if remaining < 0 {
			return errors.New("buildcache: excessive tar padding")
		}
		for _, b := range buf[:n] {
			if b != 0 {
				return errors.New("buildcache: unexpected tar padding")
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}
func fileMode(mode int64, fallback os.FileMode) os.FileMode {
	m := os.FileMode(mode).Perm()
	if m == 0 {
		return fallback
	}
	return m
}
