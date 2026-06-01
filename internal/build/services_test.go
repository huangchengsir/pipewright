package build

import (
	"context"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// svcDriver 实现 Driver + ServiceRunner,记录服务生命周期 + 脚本容器看到的网络。
type svcDriver struct {
	createdNetwork string
	ranServices    []string // alias
	ranOnNetwork   string
	scriptNetwork  string
	stopped        []string
	removedNetwork string
}

func (d *svcDriver) Binary() string { return "fake" }
func (d *svcDriver) RunToolchain(_ context.Context, _, _, _ string, _ []string, _ []string, res pipeline.Resource, _ func(string, string)) (int, error) {
	d.scriptNetwork = res.Network
	return 0, nil
}
func (d *svcDriver) Build(context.Context, string, string, string, []string, []string, func(string, string)) (int, error) {
	return 0, nil
}
func (d *svcDriver) Tag(context.Context, string, string, func(string, string)) (int, error) {
	return 0, nil
}
func (d *svcDriver) Login(context.Context, string, string, string, func(string, string)) (int, error) {
	return 0, nil
}
func (d *svcDriver) Push(context.Context, string, func(string, string)) (int, error) { return 0, nil }
func (d *svcDriver) InspectImage(context.Context, string) (string, int64, error) {
	return "", 0, nil
}

// ServiceRunner
func (d *svcDriver) CreateNetwork(_ context.Context, network string, _ func(string, string)) (int, error) {
	d.createdNetwork = network
	return 0, nil
}
func (d *svcDriver) RemoveNetwork(_ context.Context, network string, _ func(string, string)) (int, error) {
	d.removedNetwork = network
	return 0, nil
}
func (d *svcDriver) RunService(_ context.Context, _, alias, _, network string, _, _ []string, _ func(string, string)) (int, error) {
	d.ranServices = append(d.ranServices, alias)
	d.ranOnNetwork = network
	return 0, nil
}
func (d *svcDriver) StopService(_ context.Context, containerName string, _ func(string, string)) (int, error) {
	d.stopped = append(d.stopped, containerName)
	return 0, nil
}

func servicesStage() pipeline.Stage {
	return pipeline.Stage{
		ID: "test", Name: "测试", Kind: pipeline.KindBuild,
		Services: []pipeline.ServiceSpec{{Name: "testdb", Image: "postgres:16", Env: []string{"POSTGRES_PASSWORD=x"}}},
		Jobs:     []pipeline.Job{scriptJob("it", "busybox", "psql -h testdb -c 'select 1'")},
	}
}

// 旁挂服务:建网 → 起服务 → 脚本容器加入同网 → 拆除。
func TestStageServicesLifecycle(t *testing.T) {
	drv := &svcDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	if err := exec(context.Background(), &run.Run{ID: "run12345678abc", ProjectID: "p1"}, servicesStage(), &fakeReporter{}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if drv.createdNetwork == "" {
		t.Fatal("应创建服务网络")
	}
	if len(drv.ranServices) != 1 || drv.ranServices[0] != "testdb" {
		t.Errorf("应起服务 testdb;ran=%v", drv.ranServices)
	}
	if drv.ranOnNetwork != drv.createdNetwork {
		t.Errorf("服务应起在所建网络上;svc-net=%q created=%q", drv.ranOnNetwork, drv.createdNetwork)
	}
	if drv.scriptNetwork != drv.createdNetwork {
		t.Errorf("脚本容器应加入服务网络;script-net=%q created=%q", drv.scriptNetwork, drv.createdNetwork)
	}
	// 拆除:停服务 + 删网。
	if len(drv.stopped) != 1 || drv.removedNetwork != drv.createdNetwork {
		t.Errorf("应拆除服务容器+网络;stopped=%v removedNet=%q", drv.stopped, drv.removedNetwork)
	}
}

// 驱动不支持容器网络能力(无 ServiceRunner)+ 阶段声明 services → 明确失败(不静默假跑)。
func TestStageServicesUnsupportedDriverFails(t *testing.T) {
	drv := &recordingDriver{code: 0} // 不实现 ServiceRunner
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	rep := &fakeReporter{}
	err := exec(context.Background(), &run.Run{ID: "r1", ProjectID: "p1"}, servicesStage(), rep)
	if err == nil {
		t.Fatal("不支持服务能力时,声明 services 的阶段应失败")
	}
	if !strings.Contains(strings.Join(rep.logs, "\n"), "不支持容器网络能力") {
		t.Errorf("应有明确的能力不支持日志;logs=%v", rep.logs)
	}
	// 脚本不应被执行(依赖未就绪)。
	if drv.callCount != 0 {
		t.Errorf("服务起不来不应跑脚本;callCount=%d", drv.callCount)
	}
}
