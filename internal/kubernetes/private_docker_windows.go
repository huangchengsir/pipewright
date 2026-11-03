//go:build windows

package kubernetes

import (
	"context"
	"github.com/Microsoft/go-winio"
	"net"
)

const localDockerEndpoint = "npipe:////./pipe/docker_engine"
const privateDockerConfig = "NUL"

func dialLocalDocker(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeContext(ctx, `\\.\pipe\docker_engine`)
}
