package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/0xdevelop/vllm-use/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), app.ShutdownSignals...)
	defer stop()
	os.Exit(app.Run(ctx, os.Args[1:], os.Stderr))
}
