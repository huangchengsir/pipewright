package opschat

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

const validDF = "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 1048576 262144 720896 27% /\ntmpfs 1024 0 1024 0% /run\n"
const validFree = "total used free shared buff/cache available\nMem: 1073741824 268435456 134217728 1024 671088640 805306368\nSwap: 1024 256 768\n"
const validUptime = "10:00:01 up 2 days, 3:04, 2 users, load average: 0.10, 1.20, 2.30\n"

func TestResourcesStrictParsingAndLargeValues(t *testing.T) {
	disks, ok := parseDisks(validDF)
	if !ok || len(disks) != 2 || disks[0].TotalBytes != 1073741824 || disks[0].UsedBytes != 268435456 || disks[0].AvailableBytes != 738197504 {
		t.Fatal(disks, ok)
	}
	memory, ok := parseMemory(validFree)
	if !ok || memory.TotalBytes != 1073741824 || memory.SwapUsedBytes != 256 {
		t.Fatal(memory, ok)
	}
	load, ok := parseLoad(validUptime)
	if !ok || load.One != .10 || load.Fifteen != 2.3 || load.UptimeSeconds != 183840 {
		t.Fatal(load, ok)
	}
	largeDF := fmt.Sprintf("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 %d 0 0 0%% /\n", int64(math.MaxInt64/1024))
	if disk, ok := parseDisks(largeDF); !ok || disk[0].TotalBytes < 0 {
		t.Fatal(disk, ok)
	}
	maxFree := fmt.Sprintf("total used free shared buff/cache available\nMem: %d 0 %d 0 0 %d\nSwap: 0 0 0\n", int64(math.MaxInt64), int64(math.MaxInt64), int64(math.MaxInt64))
	if m, ok := parseMemory(maxFree); !ok || m.TotalBytes != math.MaxInt64 {
		t.Fatal(m, ok)
	}
	badDF := []string{
		strings.Replace(validDF, "1048576", "9007199254740992", 1),
		strings.Replace(validDF, "1048576", "-1", 1),
		strings.Replace(validDF, "1048576", "1.5", 1),
		strings.Replace(validDF, "720896", "9999999", 1),
		strings.Replace(validDF, "27%", "NaN", 1),
		strings.Replace(validDF, "Mounted on", "unfamiliar", 1),
		validDF + "malformed-row\n",
		strings.Replace(validDF, " /\n", " /path with space\n", 1),
	}
	for _, in := range badDF {
		if _, ok := parseDisks(in); ok {
			t.Fatal("invalid disk accepted", in)
		}
	}
	badFree := []string{
		strings.Replace(validFree, "1073741824", "9223372036854775808", 1),
		strings.Replace(validFree, "268435456", "1073741825", 1),
		strings.Replace(validFree, "available", "unknown", 1),
		strings.Replace(validFree, "Swap: 1024 256 768", "Swap: 1024 256 769", 1),
		validFree + "Mem: 1 0 1 0 0 1\n",
	}
	for _, in := range badFree {
		if _, ok := parseMemory(in); ok {
			t.Fatal("invalid memory accepted", in)
		}
	}
	badLoad := []string{
		strings.Replace(validUptime, "10:00:01", "24:00:01", 1),
		strings.Replace(validUptime, "3:04", "3:99", 1),
		strings.Replace(validUptime, "2 days", "9223372036854775807 days", 1),
		strings.Replace(validUptime, "0.10", "NaN", 1),
		strings.Replace(validUptime, "0.10", "-1", 1),
		strings.Replace(validUptime, "0.10", "1e999", 1),
		strings.Replace(validUptime, "0.10", "1000000001", 1),
		strings.Replace(validUptime, "load average", "load averages", 1),
		validUptime + "unexpected\n",
	}
	for _, in := range badLoad {
		if _, ok := parseLoad(in); ok {
			t.Fatal("invalid load accepted", in)
		}
	}
	if _, ok := parseLoad("01:02:03 up 5 min, 1 user, load average: 0.00, 0.01, 0.02"); !ok {
		t.Fatal("minute uptime rejected")
	}
}
func TestHostResourcesTypedSuccessAndPartialUnavailable(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
				if len(argv) < 3 || argv[0] != "env" || argv[1] != "LC_ALL=C" {
					t.Error("not fixed locale argv", argv)
				}
				switch argv[2] {
				case "df":
					if broken {
						return &target.LimitedResult{Stdout: "not a Linux df response"}, nil
					}
					return &target.LimitedResult{Stdout: validDF}, nil
				case "free":
					return &target.LimitedResult{Stdout: validFree}, nil
				case "uptime":
					return &target.LimitedResult{Stdout: validUptime}, nil
				}
				return nil, ErrInvalid
			}
			s, _, _ := fixture(t, f, nil)
			c := chat(t, s, id)
			r := submitTool(t, s, c, "host_resources", "{}")
			status := Succeeded
			if broken {
				status = PartialFailed
			}
			snap := waitRun(t, s, c.ID, r.ID, status)
			v := snap.Calls[0]
			if v.Status != status || v.Resources == nil || v.Output != "" || v.Resources.Memory == nil || v.Resources.Load == nil {
				t.Fatal(v)
			}
			if broken {
				if v.Resources.Disks != nil || len(v.Resources.Unavailable) != 1 || v.Resources.Unavailable[0] != "disks" {
					t.Fatal(v.Resources)
				}
			} else if len(v.Resources.Disks) != 2 || len(v.Resources.Unavailable) != 0 {
				t.Fatal(v.Resources)
			}
			encoded, _ := json.Marshal(v.Resources)
			if strings.Contains(string(encoded), "query") || strings.Contains(string(encoded), "Mem:") {
				t.Fatal(string(encoded))
			}
		})
	}
}
