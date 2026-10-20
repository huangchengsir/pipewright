package httpapi

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/notify"
)

type anomalyNotifyService struct {
	notify.Service
	route func(context.Context, string, string, notify.TemplateVars) error
}

func (s anomalyNotifyService) RouteEventForProject(ctx context.Context, projectID, event string, vars notify.TemplateVars) error {
	return s.route(ctx, projectID, event, vars)
}

func TestAnomalyNotifierAsyncDrainWaitsAndRejectsNewJobs(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	var calls atomic.Int32
	n := NewAnomalyNotifier(anomalyNotifyService{route: func(ctx context.Context, projectID, event string, vars notify.TemplateVars) error {
		calls.Add(1)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 12*time.Second {
			t.Error("send has no bounded live context")
		}
		if projectID != "" || event != notify.EventAnomalyDetected || vars.Project != "server" || vars.Status != "original alert" || vars.AIAnalysis != "" {
			t.Errorf("off-rule notification changed: %+v", vars)
		}
		close(started)
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}).(*anomalyNotifier)
	defer n.cancel()
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	alert := &anomaly.Alert{ID: "alert", ServerName: "server", Message: "original alert"}
	returned := make(chan struct{})
	go func() { n.NotifyAlert(requestCtx, alert); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("NotifyAlert blocked off-rule detection")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("notification did not start independently of request cancellation")
	}
	drained := make(chan error, 1)
	go func() { drained <- n.Drain(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		n.mu.Lock()
		closed := n.closed
		n.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("drain did not start")
		}
		time.Sleep(time.Millisecond)
	}
	n.NotifyAlert(context.Background(), alert)
	select {
	case err := <-drained:
		t.Errorf("Drain returned while notification gated: %v", err)
	default:
	}
	close(gate)
	select {
	case err := <-drained:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain did not wait for notification completion")
	}
	n.NotifyAlert(context.Background(), alert)
	if err := n.Drain(context.Background()); err != nil || calls.Load() != 1 {
		t.Fatalf("post-drain job accepted: calls=%d err=%v", calls.Load(), err)
	}
}

func TestAnomalyNotifierDrainDeadlineCancelsGatedSend(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	n := NewAnomalyNotifier(anomalyNotifyService{route: func(ctx context.Context, _, _ string, _ notify.TemplateVars) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}}).(*anomalyNotifier)
	defer n.cancel()
	n.NotifyAlert(context.Background(), &anomaly.Alert{ID: "alert"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("send did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := n.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain error=%v, want deadline exceeded", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("drain deadline did not cancel send context")
	}
	if err := n.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAnomalyNotifierNotifyRacesDrain(t *testing.T) {
	var calls atomic.Int32
	n := NewAnomalyNotifier(anomalyNotifyService{route: func(context.Context, string, string, notify.TemplateVars) error {
		calls.Add(1)
		return nil
	}}).(*anomalyNotifier)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				n.NotifyAlert(context.Background(), &anomaly.Alert{ID: "alert"})
			}
		}()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := n.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	count := calls.Load()
	n.NotifyAlert(context.Background(), &anomaly.Alert{ID: "after-drain"})
	if err := n.Drain(ctx); err != nil || calls.Load() != count {
		t.Fatalf("Add after drain: count=%d calls=%d error=%v", count, calls.Load(), err)
	}
}
