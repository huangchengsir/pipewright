//go:build !windows

package kubernetes

import (
	"context"
	"net"
)

const localDockerEndpoint = "unix:///var/run/docker.sock"
const privateDockerConfig = "/dev/null"

func dialLocalDocker(ctx context.Context) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
}
