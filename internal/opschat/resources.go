package opschat

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type DiskResource struct {
	Filesystem     string `json:"filesystem"`
	Mount          string `json:"mount"`
	TotalBytes     int64  `json:"totalBytes"`
	UsedBytes      int64  `json:"usedBytes"`
	AvailableBytes int64  `json:"availableBytes"`
	UsedPercent    int    `json:"usedPercent"`
}
type MemoryResource struct {
	TotalBytes     int64 `json:"totalBytes"`
	UsedBytes      int64 `json:"usedBytes"`
	FreeBytes      int64 `json:"freeBytes"`
	SharedBytes    int64 `json:"sharedBytes"`
	CacheBytes     int64 `json:"cacheBytes"`
	AvailableBytes int64 `json:"availableBytes"`
	SwapTotalBytes int64 `json:"swapTotalBytes"`
	SwapUsedBytes  int64 `json:"swapUsedBytes"`
	SwapFreeBytes  int64 `json:"swapFreeBytes"`
}
type LoadResource struct {
	One           float64 `json:"one"`
	Five          float64 `json:"five"`
	Fifteen       float64 `json:"fifteen"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
}
type HostResources struct {
	Disks       []DiskResource  `json:"disks"`
	Memory      *MemoryResource `json:"memory"`
	Load        *LoadResource   `json:"load"`
	Unavailable []string        `json:"unavailable"`
}

func unsignedMetric(s string) (int64, bool) {
	if len(s) == 0 || len(s) > 19 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, e := strconv.ParseInt(s, 10, 64)
	return n, e == nil
}
func parseDisks(text string) ([]DiskResource, bool) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		return nil, false
	}
	header := strings.Fields(lines[0])
	if len(header) != 7 || header[0] != "Filesystem" || (header[1] != "1024-blocks" && header[1] != "1K-blocks") || header[2] != "Used" || header[3] != "Available" || (header[4] != "Capacity" && header[4] != "Use%") || header[5] != "Mounted" || header[6] != "on" {
		return nil, false
	}
	out := make([]DiskResource, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != 6 || len(fields[0]) > 1024 || len(fields[5]) > 1024 || !strings.HasPrefix(fields[5], "/") {
			return nil, false
		}
		for _, value := range []string{fields[0], fields[5]} {
			if strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return nil, false
			}
		}
		values := [3]int64{}
		for i := range values {
			n, ok := unsignedMetric(fields[i+1])
			if !ok || n > math.MaxInt64/1024 {
				return nil, false
			}
			values[i] = n * 1024
		}
		if values[0] == 0 || values[1] > values[0] || values[2] > values[0] || values[1] > values[0]-values[2] {
			return nil, false
		}
		if !strings.HasSuffix(fields[4], "%") {
			return nil, false
		}
		pct, ok := unsignedMetric(strings.TrimSuffix(fields[4], "%"))
		if !ok || pct > 100 {
			return nil, false
		}
		out = append(out, DiskResource{Filesystem: fields[0], Mount: fields[5], TotalBytes: values[0], UsedBytes: values[1], AvailableBytes: values[2], UsedPercent: int(pct)})
	}
	return out, true
}
func parseMemory(text string) (*MemoryResource, bool) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 || strings.Join(strings.Fields(lines[0]), " ") != "total used free shared buff/cache available" {
		return nil, false
	}
	mem, swap := strings.Fields(lines[1]), strings.Fields(lines[2])
	if len(mem) != 7 || mem[0] != "Mem:" || len(swap) != 4 || swap[0] != "Swap:" {
		return nil, false
	}
	values := [9]int64{}
	for i := 0; i < 6; i++ {
		n, ok := unsignedMetric(mem[i+1])
		if !ok {
			return nil, false
		}
		values[i] = n
	}
	for i := 0; i < 3; i++ {
		n, ok := unsignedMetric(swap[i+1])
		if !ok {
			return nil, false
		}
		values[i+6] = n
	}
	total := values[0]
	if total == 0 {
		return nil, false
	}
	for _, n := range values[1:6] {
		if n > total {
			return nil, false
		}
	}
	if values[1] > total-values[2] || values[4] > total-values[2] {
		return nil, false
	}
	if values[7] > values[6] || values[8] != values[6]-values[7] {
		return nil, false
	}
	return &MemoryResource{TotalBytes: total, UsedBytes: values[1], FreeBytes: values[2], SharedBytes: values[3], CacheBytes: values[4], AvailableBytes: values[5], SwapTotalBytes: values[6], SwapUsedBytes: values[7], SwapFreeBytes: values[8]}, true
}

var uptimePattern = regexp.MustCompile(`^([0-9]{1,2}):([0-9]{2}):([0-9]{2})[ \t]+up[ \t]+(?:([0-9]+)[ \t]+days?,[ \t]+)?(?:([0-9]+):([0-9]{2})|([0-9]+)[ \t]+min),[ \t]+([0-9]+)[ \t]+users?,[ \t]+load average:[ \t]+([0-9]+(?:\.[0-9]+)?),[ \t]+([0-9]+(?:\.[0-9]+)?),[ \t]+([0-9]+(?:\.[0-9]+)?)$`)

func parseLoad(text string) (*LoadResource, bool) {
	match := uptimePattern.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return nil, false
	}
	limits := map[int]int64{1: 23, 2: 59, 3: 59, 4: math.MaxInt64/86400 - 1, 5: 23, 6: 59, 7: 59, 8: math.MaxInt64}
	nums := [9]int64{}
	for i, limit := range limits {
		if match[i] != "" {
			n, ok := unsignedMetric(match[i])
			if !ok || n > limit {
				return nil, false
			}
			nums[i] = n
		}
	}
	seconds := nums[4]*86400 + nums[5]*3600 + nums[6]*60 + nums[7]*60
	values := [3]float64{}
	for i := range values {
		n, e := strconv.ParseFloat(match[9+i], 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1e9 {
			return nil, false
		}
		values[i] = n
	}
	return &LoadResource{One: values[0], Five: values[1], Fifteen: values[2], UptimeSeconds: seconds}, true
}
func (r *HostResources) unavailable(part string) {
	for _, old := range r.Unavailable {
		if old == part {
			return
		}
	}
	r.Unavailable = append(r.Unavailable, part)
}
func resourcePart(argv []string) string {
	if len(argv) > 2 && argv[0] == "env" && argv[1] == "LC_ALL=C" {
		return argv[2]
	}
	return ""
}
func (r *HostResources) collect(part, text string, valid bool) {
	switch part {
	case "df":
		if valid {
			if value, ok := parseDisks(text); ok {
				r.Disks = value
				return
			}
		}
		r.unavailable("disks")
	case "free":
		if valid {
			if value, ok := parseMemory(text); ok {
				r.Memory = value
				return
			}
		}
		r.unavailable("memory")
	case "uptime":
		if valid {
			if value, ok := parseLoad(text); ok {
				r.Load = value
				return
			}
		}
		r.unavailable("load")
	}
}
func resultBytes(c Call) int {
	n := len(c.Output) + len(c.Error)
	if c.Resources != nil {
		b, _ := json.Marshal(c.Resources)
		n += len(b)
	}
	return n
}
