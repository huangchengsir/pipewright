package pipeline

import (
	"context"
	"errors"
	"testing"
)

// specWithNeeds 构造一个含源阶段 + 两个带依赖阶段的 spec(满足源阶段不变式)。
func specWithNeeds(buildNeeds, deployNeeds []string) Spec {
	return Spec{Stages: []Stage{
		{ID: "stg_src", Name: "源", Kind: KindSource, Jobs: []Job{{ID: "j_src", Name: "src", Type: "git_source"}}},
		{ID: "stg_build", Name: "构建", Kind: KindBuild, Needs: buildNeeds, Jobs: []Job{}},
		{ID: "stg_deploy", Name: "部署", Kind: KindDeploy, Needs: deployNeeds, Jobs: []Job{}},
	}}
}

func TestSaveValidStageNeedsRoundTrips(t *testing.T) {
	svc, _, projID := newSvc(t)
	ctx := context.Background()

	spec := specWithNeeds([]string{"stg_src"}, []string{"stg_build"})
	saved, err := svc.Save(ctx, projID, spec)
	if err != nil {
		t.Fatalf("Save valid needs: %v", err)
	}

	// 回读并核验 needs 持久化往返。
	got, err := svc.Get(ctx, projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = saved
	byID := map[string]Stage{}
	for _, st := range got.Spec.Stages {
		byID[st.ID] = st
	}
	if len(byID["stg_build"].Needs) != 1 || byID["stg_build"].Needs[0] != "stg_src" {
		t.Errorf("build.Needs = %v, want [stg_src]", byID["stg_build"].Needs)
	}
	if len(byID["stg_deploy"].Needs) != 1 || byID["stg_deploy"].Needs[0] != "stg_build" {
		t.Errorf("deploy.Needs = %v, want [stg_build]", byID["stg_deploy"].Needs)
	}
}

func TestSaveRejectsUnknownNeed(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := specWithNeeds([]string{"does_not_exist"}, nil)
	if _, err := svc.Save(context.Background(), projID, spec); !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("err = %v, want ErrInvalidStage", err)
	}
}

func TestSaveRejectsSelfNeed(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := specWithNeeds([]string{"stg_build"}, nil) // build 依赖自身
	if _, err := svc.Save(context.Background(), projID, spec); !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("err = %v, want ErrInvalidStage", err)
	}
}

func TestSaveRejectsCycle(t *testing.T) {
	svc, _, projID := newSvc(t)
	// build ↔ deploy 互相依赖成环。
	spec := specWithNeeds([]string{"stg_deploy"}, []string{"stg_build"})
	if _, err := svc.Save(context.Background(), projID, spec); !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("err = %v, want ErrInvalidStage", err)
	}
}

func TestSaveDedupesNeeds(t *testing.T) {
	svc, _, projID := newSvc(t)
	ctx := context.Background()
	spec := specWithNeeds([]string{"stg_src", "stg_src", " stg_src "}, nil)
	if _, err := svc.Save(ctx, projID, spec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _ := svc.Get(ctx, projID)
	for _, st := range got.Spec.Stages {
		if st.ID == "stg_build" {
			if len(st.Needs) != 1 || st.Needs[0] != "stg_src" {
				t.Errorf("build.Needs = %v, want deduped [stg_src]", st.Needs)
			}
		}
	}
}

func TestSaveNoNeedsStillValid(t *testing.T) {
	// 向后兼容:存量「直线」流水线(无 needs)照常保存。
	svc, _, projID := newSvc(t)
	if _, err := svc.Save(context.Background(), projID, validSpec()); err != nil {
		t.Fatalf("Save no-needs spec: %v", err)
	}
}
