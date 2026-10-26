package build

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// One bounded read reconciles an ambiguous receipt. Names are lookup keys only;
// cleanup always uses a full ID bearing this invocation's random owner label.
func (d *shellDriver) reconcileServiceCreate(parent context.Context, kind, name, owner string, namedReceipt bool) (int, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	args := []string{"inspect", "--type", "container", "--format", `{{.Id}} {{index .Config.Labels "io.pipewright.service.owner"}}`, name}
	if kind == "network" {
		args = []string{"network", "inspect", "--format", `{{.ID}} {{index .Labels "io.pipewright.service.owner"}}`, name}
	}
	proof, _, code, err := d.cmdr.Run(ctx, d.bin, args, "")
	fields := strings.Fields(proof)
	failure := func(detail string) (int, error) {
		// Ownership labels and full object IDs are non-sensitive recovery evidence.
		if parent.Err() != nil {
			return -1, fmt.Errorf("build: service %s %s; owner=%s: %w", kind, detail, owner, parent.Err())
		}
		return -1, fmt.Errorf("build: service %s %s; owner=%s", kind, detail, owner)
	}
	if err != nil || code != 0 || len(fields) != 2 || !serviceObjectID.MatchString(fields[0]) || fields[1] != owner {
		return failure("create outcome and ownership unconfirmed")
	}
	id := fields[0]
	if kind == "network" {
		d.serviceNetworks.Store(name, id)
	} else {
		d.serviceContainers.Store(name, id)
	}
	if namedReceipt {
		return 0, nil
	}
	if kind == "network" {
		code, err = d.RemoveNetwork(ctx, name, nil)
	} else {
		code, err = d.StopService(ctx, name, nil)
	}
	if err != nil || code != 0 {
		return failure("create receipt unavailable; cleanup incomplete for confirmed id=" + id)
	}
	return failure("create receipt unavailable; removed confirmed id=" + id)
}
