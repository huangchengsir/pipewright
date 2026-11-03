package kubernetesowner

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnerChild(t *testing.T) {
	dir := os.Getenv("K8S_OWNER_CHILD_DIR")
	if dir == "" {
		return
	}
	g, e := Acquire(dir)
	if e != nil {
		os.Exit(11)
	}
	fmt.Println(g.Owner())
	if os.Getenv("K8S_OWNER_CHILD_WAIT") == "1" {
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(0)
}
func TestOwnerCrossProcessDeathAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnerChild$")
	cmd.Env = append(os.Environ(), "K8S_OWNER_CHILD_DIR="+dir, "K8S_OWNER_CHILD_WAIT=1")
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	line, e := bufio.NewReader(pipe).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	old := strings.TrimSpace(line)
	if g, e := Acquire(dir); !errors.Is(e, ErrLocked) {
		if g != nil {
			g.Close()
		}
		t.Fatalf("concurrent process accepted: %v", e)
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = cmd.Wait()
	g, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	if len(g.DepartedOwners()) != 1 || g.DepartedOwners()[0] != old {
		t.Fatal("prior owner not proven")
	}
	owner := g.Owner()
	g.Close()
	next, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if len(next.DepartedOwners()) != 2 || next.DepartedOwners()[1] != owner {
		t.Fatal("normal restart lost owner")
	}
}
func TestOwnerJournalCopyCorruptionAndReplacementFailClosed(t *testing.T) {
	for _, mode := range []string{"copy", "corrupt", "missing", "replace-lock"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			g, e := Acquire(dir)
			if e != nil {
				t.Fatal(e)
			}
			g.Close()
			jp := filepath.Join(dir, journalName)
			switch mode {
			case "copy":
				other := filepath.Join(t.TempDir(), "other")
				o, e := Acquire(other)
				if e != nil {
					t.Fatal(e)
				}
				o.Close()
				raw, e := os.ReadFile(filepath.Join(other, journalName))
				if e != nil {
					t.Fatal(e)
				}
				e = os.WriteFile(jp, raw, 0600)
				if e != nil {
					t.Fatal(e)
				}
			case "corrupt":
				if e = os.WriteFile(jp, []byte(`{"schema":1}`), 0600); e != nil {
					t.Fatal(e)
				}
			case "missing":
				if e = os.Remove(jp); e != nil {
					t.Fatal(e)
				}
			case "replace-lock":
				lp := filepath.Join(dir, lockName)
				raw, e := os.ReadFile(lp)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(lp, lp+".old"); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(lp, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if acquired, e := Acquire(dir); !errors.Is(e, ErrJournal) {
				if acquired != nil {
					acquired.Close()
				}
				t.Fatalf("unsafe journal accepted: %v", e)
			}
		})
	}
}

func TestOwnerNormalRestartsBeyondHistoryLimitWithPendingOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	first, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	pending := first.Owner()
	first.Close()
	for i := 0; i < 1026; i++ {
		g, e := Acquire(dir)
		if e != nil {
			t.Fatalf("restart %d: %v", i, e)
		}
		safe := []string{}
		for _, owner := range g.DepartedOwners() {
			if owner != pending {
				safe = append(safe, owner)
			}
		}
		if e = g.PruneDeparted(safe); e != nil {
			g.Close()
			t.Fatalf("compact restart %d: %v", i, e)
		}
		if len(g.DepartedOwners()) != 1 || g.DepartedOwners()[0] != pending {
			g.Close()
			t.Fatal("unresolved owner dropped")
		}
		g.Close()
	}
}
func TestOwnerIncompleteInitializationNeverProvesDeparture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if e := os.MkdirAll(filepath.Join(dir, journalName), 0700); e != nil {
		t.Fatal(e)
	}
	if g, e := Acquire(dir); !errors.Is(e, ErrJournal) {
		if g != nil {
			g.Close()
		}
		t.Fatal("initial journal failure accepted")
	}
	if e := os.Remove(filepath.Join(dir, journalName)); e != nil {
		t.Fatal(e)
	}
	if g, e := Acquire(dir); !errors.Is(e, ErrJournal) {
		if g != nil {
			g.Close()
		}
		t.Fatal("missing journal adopted as predecessor")
	}
}
