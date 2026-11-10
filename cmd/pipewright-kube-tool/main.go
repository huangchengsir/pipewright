package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/huangchengsir/pipewright/internal/kubeagent"
	"github.com/huangchengsir/pipewright/internal/kubeprobe"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"strings"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "probe" {
		var spec kubeprobe.Spec
		decoder := json.NewDecoder(strings.NewReader(os.Args[2]))
		decoder.DisallowUnknownFields()
		if len(os.Args[2]) > 8192 || decoder.Decode(&spec) != nil || decoder.Decode(new(any)) != io.EOF {
			os.Exit(2)
		}
		if kubeprobe.Run(context.Background(), spec) != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if kubeagent.New().Serve(ctx, kubeprotocol.SocketPath) != nil && ctx.Err() == nil {
			os.Exit(1)
		}
	case "rpc":
		var frame kubeprotocol.Frame
		b, e := io.ReadAll(io.LimitReader(os.Stdin, (256<<10)+1))
		if e != nil || len(b) > 256<<10 || json.Unmarshal(b, &frame) != nil {
			os.Exit(2)
		}
		clear(b)
		result, e := kubeagent.Call(context.Background(), frame)
		if e != nil {
			os.Exit(1)
		}
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			os.Exit(1)
		}
	case "version":
		_, _ = os.Stdout.WriteString("pipewright-kube-tool/2\n")
	default:
		os.Exit(2)
	}
}
