package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
	"strings"
)

// containerLogMasker reads only the selected container's configuration into a
// local masker. This is not a reveal API; no raw configuration leaves this
// function. Missing, truncated or invalid configuration fails closed.
func containerLogMasker(ctx context.Context, svc target.Service, serverID, containerID string) (*mask.Masker, string, error) {
	if !reDockerName.MatchString(containerID) || len(containerID) > 256 {
		return nil, "", errors.New("invalid container")
	}
	res, err := svc.Exec(ctx, serverID, []string{"docker", "inspect", containerID})
	if err != nil {
		return nil, "", err
	}
	if res == nil || res.ExitCode != 0 || len(res.Stdout) > 2<<20 {
		return nil, "", errors.New("container configuration unavailable")
	}
	var objects []dockerInspectRaw
	if json.Unmarshal([]byte(res.Stdout), &objects) != nil || len(objects) != 1 || !reDockerName.MatchString(objects[0].ID) || len(objects[0].ID) > 256 {
		return nil, "", errors.New("container configuration unavailable")
	}
	if _, err = svc.Get(ctx, serverID); err != nil {
		return nil, "", err
	}
	m := mask.NewMasker()
	for _, entry := range objects[0].Config.Env {
		if _, value, ok := strings.Cut(entry, "="); ok {
			m.RegisterExactSecret(value)
		}
	}
	for _, value := range objects[0].Config.Labels {
		m.RegisterExactSecret(value)
	}
	for _, value := range objects[0].Args {
		m.RegisterExactSecret(value)
	}
	for _, value := range objects[0].Config.Cmd {
		m.RegisterExactSecret(value)
	}
	return m, objects[0].ID, nil
}
