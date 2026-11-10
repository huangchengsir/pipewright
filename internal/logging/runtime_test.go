package logging_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/cron"
)

type localeSchedules struct{}

func (localeSchedules) ListEnabled(context.Context) ([]cron.Entry, error) {
	return []cron.Entry{{ProjectID: "fixture-ok", Expression: "* * * * *"}, {ProjectID: "fixture-fail", Expression: "* * * * *"}}, nil
}

type localeRuns struct{}

func (localeRuns) CreateScheduledRun(_ context.Context, project, branch string) (string, error) {
	if project == "fixture-fail" {
		return "", errors.New("fixture unavailable")
	}
	return "fixture-run", nil
}

// Runs the actual runtime scheduler's success and failure logging paths in a
// new process for each locale. Only storage/run creation are inert fixtures.
func TestSchedulerProcessLocales(t *testing.T) {
	for _, locale := range []string{"C", "en_US.UTF-8", "zh_CN.UTF-8"} {
		t.Run(locale, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSchedulerLocaleChild$")
			env := []string{}
			for _, v := range os.Environ() {
				k := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
				if k != "LC_ALL" && k != "LC_MESSAGES" && k != "LANG" && k != "LANGUAGE" && k != "PW_SCHEDULER_LOCALE_CHILD" {
					env = append(env, v)
				}
			}
			cmd.Env = append(env, "LC_ALL="+locale, "PW_SCHEDULER_LOCALE_CHILD=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("scheduler helper: %v", err)
			}
			actual := string(out)
			success, failure := "scheduled trigger", "failed to create scheduled run"
			if strings.HasPrefix(locale, "zh") {
				success, failure = "定时触发", "定时触发创建运行失败"
			}
			for _, text := range []string{success, failure, "fixture-ok", "fixture-fail", "fixture unavailable", "2026-11-08T09:00"} {
				if !strings.Contains(actual, text) {
					t.Fatalf("missing %q from %q", text, actual)
				}
			}
			t.Logf("Actual scheduler process output (%s):\n%s", locale, actual)
		})
	}
}

func TestSchedulerLocaleChild(t *testing.T) {
	if os.Getenv("PW_SCHEDULER_LOCALE_CHILD") != "1" {
		return
	}
	cron.NewScheduler(localeSchedules{}, localeRuns{}).Tick(context.Background(), time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC))
}
