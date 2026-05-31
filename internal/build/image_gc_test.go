package build

import (
	"context"
	"strings"
	"testing"
)

func TestPruneDanglingRunsImagePrune(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("image", fakeCmd{exitCode: 0, stdoutLines: []string{"Total reclaimed space: 1.2GB"}})
	drv := &shellDriver{bin: "docker", cmdr: cmdr}

	ok, err := drv.PruneDangling(context.Background(), func(string, string) {})
	if err != nil || !ok {
		t.Fatalf("PruneDangling ok=%v err=%v", ok, err)
	}
	// 只能是 `docker image prune -f`(只清悬空,绝不带 -a / 不删有 tag 镜像)。
	all := cmdr.allArgsText()
	if !strings.Contains(all, "image prune -f") {
		t.Fatalf("应执行 image prune -f,实际:%s", all)
	}
	if strings.Contains(all, "prune -a") || strings.Contains(all, "--all") {
		t.Fatalf("绝不应带 -a/--all(会删有 tag 镜像):%s", all)
	}
}

func TestPruneImagesBestEffortRunsWhenEnabled(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("image", fakeCmd{exitCode: 0})
	b := &Builder{driver: &shellDriver{bin: "docker", cmdr: cmdr}}
	b.pruneImagesBestEffort(context.Background(), func(string, string) {})
	if !strings.Contains(cmdr.allArgsText(), "image prune -f") {
		t.Fatalf("默认应清悬空镜像,实际:%s", cmdr.allArgsText())
	}
}

func TestPruneImagesBestEffortSkipsWhenDisabled(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("image", fakeCmd{exitCode: 0})
	b := &Builder{driver: &shellDriver{bin: "docker", cmdr: cmdr}, disableImageGC: true}
	b.pruneImagesBestEffort(context.Background(), func(string, string) {})
	if strings.Contains(cmdr.allArgsText(), "prune") {
		t.Fatalf("关闭 GC 后不应清镜像,实际:%s", cmdr.allArgsText())
	}
}

// 驱动不支持镜像清理(如远程/桩)→ 静默跳过,不 panic、不报错。
func TestPruneImagesBestEffortNoopForNonPruner(t *testing.T) {
	b := &Builder{driver: &recordingDriver{}} // recordingDriver 不实现 imagePruner
	b.pruneImagesBestEffort(context.Background(), func(string, string) {})
}
