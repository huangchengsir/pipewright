package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

const uploadChunkSize = 8 << 20

var (
	ErrUploadBusy      = errors.New("deploy: artifact upload is locked by another session")
	ErrUploadIntegrity = errors.New("deploy: artifact checksum or size mismatch")
	ErrUploadSpace     = errors.New("deploy: insufficient upload staging space")
	ErrUploadTools     = errors.New("deploy: target requires sha256sum and standard POSIX file utilities")
	ErrUploadLease     = errors.New("deploy: upload lease lost")
)

// All variable data is passed as argv. The gate serializes lease ownership changes;
// it deliberately fails closed if its short-lived owner dies while holding it.
// Writers only upload token-scoped temporary files. Every commit is fenced again.
const uploadScript = `set -eu
umask 077
action=$1; dir=$2; owner=$3; shift 3
guard() {
  [ ! -L "$dir" ] && [ "$(cat "$dir/owner" 2>/dev/null)" = "$owner" ] || exit 74
  now=$(date +%s); until=$(cat "$dir/until" 2>/dev/null)
  [ "$until" -gt "$now" ] || exit 74
}
check() {
  [ -f "$1" ] && [ ! -L "$1" ] || return 1
  [ "$(wc -c < "$1" | tr -d ' ')" = "$2" ] || return 1
  sum=$(sha256sum < "$1"); [ "${sum%% *}" = "$3" ]
}
gate() {
  if ! mkdir "$dir/.gate" 2>/dev/null; then
    now=$(date +%s); previous_until=$(cat "$dir/until" 2>/dev/null || printf 0)
    # A crashed owner can leave the short-lived gate behind. Reclaim it only
    # after the lease expired and the empty gate has aged past ten minutes.
    [ "$previous_until" -le "$now" ] || exit 73
    stale=$(find "$dir/.gate" -maxdepth 0 -type d -mmin +10 -print 2>/dev/null)
    [ -n "$stale" ] || exit 73
    rmdir "$dir/.gate" 2>/dev/null || exit 73
    mkdir "$dir/.gate" 2>/dev/null || exit 73
  fi
  trap 'rmdir "$dir/.gate"' EXIT
}
case "$action" in
acquire)
  for tool in sha256sum wc tr df awk date cat mv mkdir rm find; do command -v "$tool" >/dev/null || exit 76; done
  root=$(dirname "$dir"); mkdir -p "$root"
  [ ! -L "$root" ] || exit 74
  # Only expired, inactive sessions in this dedicated staging root may be removed.
  for old in "$root"/*; do
    [ -d "$old" ] && [ ! -L "$old" ] && [ "$old" != "$dir" ] || continue
    [ -d "$old/.gate" ] && continue
    old_until=$(cat "$old/until" 2>/dev/null || printf 0)
    old_touched=$(cat "$old/touched" 2>/dev/null || printf 0)
    now=$(date +%s)
    if [ "$old_until" -le "$now" ] && [ "$((now-old_touched))" -ge 86400 ]; then rm -rf -- "$old"; fi
  done
  mkdir -p "$dir"; [ ! -L "$dir" ] || exit 74
  gate
  now=$(date +%s); until=$(cat "$dir/until" 2>/dev/null || printf 0)
  [ "$until" -le "$now" ] || exit 73
  touched=$(cat "$dir/touched" 2>/dev/null || printf 0)
  if [ "$((now-touched))" -ge 86400 ]; then
    find "$dir" -maxdepth 1 -type f \( -name 'chunk-*' -o -name '*.tmp' \) -delete
  fi
  available=$(df -Pk "$dir" | awk 'END {print $4}')
  [ "$available" -ge "$1" ] || exit 75
  printf '%s' "$owner" > "$dir/owner"
  printf '%s' "$((now+180))" > "$dir/until"
  printf '%s' "$now" > "$dir/touched"
  ;;
renew)
  gate; guard
  printf '%s' "$((now+180))" > "$dir/until"
  printf '%s' "$now" > "$dir/touched"
  ;;
probe)
  guard; check "$dir/chunk-$1" "$2" "$3" || exit 77
  ;;
commit)
  guard; check "$dir/$owner-$1.tmp" "$2" "$3" || exit 78
  gate; guard
  mv -f "$dir/$owner-$1.tmp" "$dir/chunk-$1"
  ;;
assemble)
  guard
  out="$dir/$owner-assembled.tmp"; : > "$out"
  i=0; while [ "$i" -lt "$1" ]; do
    guard; cat "$dir/chunk-$i" >> "$out"; i=$((i+1))
  done
  check "$out" "$2" "$3" || exit 78
  gate; guard
  mkdir -p "$(dirname "$4")"
  mv -f "$out" "$4"
  ;;
release|discard)
  gate; guard
  if [ "$action" = discard ]; then
    find "$dir" -maxdepth 1 -type f \( -name 'chunk-*' -o -name '*.tmp' \) -delete
  else
    find "$dir" -maxdepth 1 -type f -name "$owner-*.tmp" -delete
  fi
  rm -f "$dir/owner" "$dir/until"
  ;;
esac
`

func (s *service) uploadOp(ctx context.Context, serverID, action, dir, owner string, args ...string) error {
	cmd := append([]string{"sh", "-c", uploadScript, "pw-upload", action, dir, owner}, args...)
	out, err := s.targets.Exec(ctx, serverID, cmd)
	if err != nil {
		return err
	}
	if out == nil {
		return errors.New("deploy: missing upload acknowledgement")
	}
	switch out.ExitCode {
	case 0:
		return nil
	case 73:
		return ErrUploadBusy
	case 74:
		return ErrUploadLease
	case 75:
		return ErrUploadSpace
	case 76:
		return ErrUploadTools
	case 77:
		return errChunkMissing
	case 78:
		return ErrUploadIntegrity
	default:
		return fmt.Errorf("deploy: upload %s failed (exit %d)", action, out.ExitCode)
	}
}

var errChunkMissing = errors.New("deploy: chunk missing or corrupt")

func (s *service) uploadArtifact(ctx context.Context, serverID string, a run.Artifact, base, dest string) (retErr error) {
	p := policyFrom(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if p.total > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, p.total)
		defer stop()
	}
	rc, err := s.artStore.Open(a.Reference)
	if err != nil {
		return err
	}
	defer rc.Close()
	size, err := s.artStore.Stat(a.Reference)
	if err != nil {
		return err
	}
	identity := sha256.Sum256([]byte(serverID + "\x00" + path.Clean(base) + "\x00" + a.Reference))
	dir := path.Join(base, ".pipewright-uploads", fmt.Sprintf("%x", identity))
	owner := uuid.NewString()
	control := func(c context.Context, action string, args ...string) error {
		c, stop := context.WithTimeout(c, p.command)
		defer stop()
		return s.uploadOp(c, serverID, action, dir, owner, args...)
	}
	// Reserve the worst case: all chunks plus a full assembled copy and one chunk.
	if err := control(ctx, "acquire", strconv.FormatInt(2*(size/1024+1)+uploadChunkSize/1024, 10)); err != nil {
		return err
	}
	done := make(chan struct{})
	var heartbeatFailed atomic.Bool
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				for attempt := 0; attempt < 3; attempt++ {
					c, stop := context.WithTimeout(heartbeatCtx, 15*time.Second)
					err := s.uploadOp(c, serverID, "renew", dir, owner)
					stop()
					if err == nil {
						break
					}
					if !errors.Is(err, ErrUploadBusy) || attempt == 2 {
						heartbeatFailed.Store(true)
						cancel()
						return
					}
					select {
					case <-heartbeatCtx.Done():
						return
					case <-time.After(time.Second):
					}
				}
			}
		}
	}()
	defer func() {
		stopHeartbeat()
		<-done
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer stop()
		action := "release"
		if retErr == nil || errors.Is(retErr, ErrUploadIntegrity) || (errors.Is(retErr, context.Canceled) && !heartbeatFailed.Load()) {
			action = "discard"
		}
		if err := s.uploadOp(cleanup, serverID, action, dir, owner); err != nil {
			cmdLogFrom(ctx)(cmdStreamStderr, "Upload cleanup unconfirmed: "+humanExecError(err))
			if retErr != nil {
				retErr = fmt.Errorf("%w; upload cleanup unconfirmed", retErr)
			}
		}
	}()
	buf := make([]byte, uploadChunkSize)
	whole := sha256.New()
	var transferred int64
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := io.ReadFull(rc, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		if n == 0 {
			break
		}
		_, _ = whole.Write(buf[:n])
		digest := fmt.Sprintf("%x", sha256.Sum256(buf[:n]))
		args := []string{strconv.Itoa(count), strconv.Itoa(n), digest}
		var chunkErr error
		for attempt := 1; attempt <= 3; attempt++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunkErr = control(ctx, "probe", args...)
			if chunkErr == nil {
				break
			}
			if errors.Is(chunkErr, errChunkMissing) {
				uCtx := target.WithTransferTimeouts(ctx, p.connect, p.idle)
				chunkErr = s.targets.Upload(uCtx, serverID, bytes.NewReader(buf[:n]), path.Join(dir, owner+"-"+args[0]+".tmp"))
				if chunkErr == nil {
					chunkErr = control(ctx, "commit", args...)
				}
				if chunkErr == nil {
					break
				}
			}
			if !errors.Is(chunkErr, target.ErrUnreachable) {
				return chunkErr
			}
			if attempt < 3 {
				cmdLogFrom(ctx)(cmdStreamStdout, fmt.Sprintf("Upload retry %d/3; verified bytes %d/%d", attempt+1, transferred, size))
				timer := time.NewTimer(time.Duration(1<<(attempt-1)) * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		if chunkErr != nil {
			return chunkErr
		}
		transferred += int64(n)
		count++
		cmdLogFrom(ctx)(cmdStreamStdout, fmt.Sprintf("Upload verified %d/%d bytes", transferred, size))
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if transferred != size || fmt.Sprintf("%x", whole.Sum(nil)) != a.Reference {
		return ErrUploadIntegrity
	}
	// Local hashing and remote assembly have separate budgets from short control commands.
	assembleBudget := 10 * time.Minute
	if estimated := time.Duration(size/(1<<20)) * 5 * time.Second; estimated > assembleBudget {
		assembleBudget = estimated
	}
	if assembleBudget > 24*time.Hour {
		assembleBudget = 24 * time.Hour
	}
	assembleCtx, stopAssemble := context.WithTimeout(ctx, assembleBudget)
	defer stopAssemble()
	return s.uploadOp(assembleCtx, serverID, "assemble", dir, owner, strconv.Itoa(count), strconv.FormatInt(size, 10), a.Reference, dest)
}
