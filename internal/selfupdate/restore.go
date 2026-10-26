package selfupdate

import (
	"context"
	"os/exec"
	"time"
)

func restoreExecutableContext(path string) {
	bin, e := exec.LookPath("restorecon")
	if e != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, bin, path).Run()
}
