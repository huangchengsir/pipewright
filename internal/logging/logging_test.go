package logging

import (
	"bytes"
	"errors"
	stdlog "log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/i18n"
)

func captureStandardLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous, flags, prefix := stdlog.Writer(), stdlog.Flags(), stdlog.Prefix()
	var output bytes.Buffer
	stdlog.SetOutput(&output)
	stdlog.SetFlags(0)
	stdlog.SetPrefix("fixture: ")
	t.Cleanup(func() { stdlog.SetOutput(previous); stdlog.SetFlags(flags); stdlog.SetPrefix(prefix) })
	return &output
}

func TestStandardLoggerCaptureAndOpaqueValues(t *testing.T) {
	out := captureStandardLog(t)
	Printf("data db: %s", "fixture.db")
	want := "fixture: " + i18n.LogMessage(processLocale, "data db: %s")
	want = strings.Replace(want, "%s", "fixture.db", 1) + "\n"
	if out.String() != want {
		t.Fatalf("capture changed: %q, want %q", out.String(), want)
	}
	out.Reset()
	Printf("server: %v", errors.New("opaque 中文 diagnostic"))
	if !strings.Contains(out.String(), "opaque 中文 diagnostic") {
		t.Fatal("opaque diagnostics were translated")
	}
}

func TestControlCharactersCannotForgeLogLines(t *testing.T) {
	out := captureStandardLog(t)
	Printf("data db: %s", "value\r\nforged\t\x00\x1b[31m\u0085\u2028\u2029\u202e")
	actual := out.String()
	if strings.Count(actual, "\n") != 1 {
		t.Fatalf("unexpected physical lines: %q", actual)
	}
	for _, expected := range []string{`\r\nforged\t\u0000\u001b[31m\u0085\u2028\u2029\u202e`} {
		if !strings.Contains(actual, expected) {
			t.Fatalf("missing escaped controls: %q", actual)
		}
	}
}

func TestCallerFlagsStillIdentifyTheCallSite(t *testing.T) {
	out := captureStandardLog(t)
	stdlog.SetFlags(stdlog.Lshortfile)
	Printf("[run] worker pool started")
	if !regexp.MustCompile(`logging_test.go:[0-9]+:`).MatchString(out.String()) {
		t.Fatalf("lost standard caller information: %q", out.String())
	}
}

func helperEnv(mode, locale string) []string {
	env := []string{}
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		switch strings.ToUpper(key) {
		case "LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE", "PW_PROCESS_LOG_HELPER":
			continue
		}
		env = append(env, value)
	}
	return append(env, "PW_PROCESS_LOG_HELPER="+mode, "LC_ALL="+locale)
}

func TestProcessEnvironmentIsCapturedOnce(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessLogChild$")
	cmd.Env = helperEnv("snapshot", "zh_CN.UTF-8")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v", err)
	}
	if !bytes.Contains(output, []byte("[run] 运行工作池已启动")) || bytes.Contains(output, []byte("worker pool started")) {
		t.Fatalf("process locale changed after environment mutation: %q", output)
	}
}

func TestFatalLogsAndExitsWithOne(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessLogChild$")
	cmd.Env = helperEnv("fatal", "C")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("Fatalf exit=%v, output=%q", err, output)
	}
	if strings.TrimSpace(string(output)) != "fixture: server: fixture unavailable" {
		t.Fatalf("Fatalf output changed: %q", output)
	}
}

func TestProcessLogChild(t *testing.T) {
	mode := os.Getenv("PW_PROCESS_LOG_HELPER")
	if mode == "" {
		return
	}
	stdlog.SetFlags(0)
	stdlog.SetPrefix("fixture: ")
	switch mode {
	case "snapshot":
		_ = os.Setenv("LC_ALL", "en_US.UTF-8")
		Printf("[run] worker pool started")
	case "fatal":
		Fatalf("server: %v", errors.New("fixture unavailable"))
		t.Fatal("Fatalf returned")
	default:
		t.Fatal("unknown helper mode")
	}
}

func TestInvalidUTF8DiagnosticsRemainDistinctAndValidReplacementSurvives(t *testing.T) {
	first := singleLine(string([]byte{0xff}))
	second := singleLine(string([]byte{0xfe}))
	if first != `\xff` || second != `\xfe` || first == second {
		t.Fatalf("invalid diagnostic bytes collapsed: %q / %q", first, second)
	}
	if got := singleLine("valid \ufffd 中文"); got != "valid \ufffd 中文" {
		t.Fatal("valid replacement rune was escaped")
	}
	if got := singleLine(string([]byte{0xc3, 0x28, 0xff}) + "\n"); got != `\xc3(\xff\n` {
		t.Fatalf("mixed invalid UTF8 changed: %q", got)
	}
}
