package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	appservice "github.com/0xdevelop/vllm-use/app"
	projectconfig "github.com/0xdevelop/vllm-use/config"
	"github.com/george012/gtbox"
	"github.com/george012/gtbox/gtbox_log"
)

var (
	mRunMode       = ""
	mGitCommitHash = ""
	mGitCommitTime = ""
	mPackageOS     = ""
	mPackageTime   = ""
	mGoVersion     = ""
)

func setupApp() {
	runMode := gtbox.RunModeDebug
	switch mRunMode {
	case "test":
		runMode = gtbox.RunModeTest
	case "release":
		runMode = gtbox.RunModeRelease
	}
	projectconfig.CurrentApp = projectconfig.NewApp(runMode)
	projectconfig.CurrentApp.GitCommitHash = mGitCommitHash
	projectconfig.CurrentApp.GitCommitTime = mGitCommitTime
	projectconfig.CurrentApp.GoVersion = mGoVersion
	projectconfig.CurrentApp.PackageOS = mPackageOS
	projectconfig.CurrentApp.PackageTime = mPackageTime
	gtbox.SetupGTBox(
		projectconfig.CurrentApp.AppName,
		projectconfig.CurrentApp.CurrentRunMode,
		projectconfig.CurrentApp.AppLogPath,
		30,
		gtbox_log.GTLogSaveHours,
		projectconfig.CurrentApp.HTTPRequestTimeOut,
	)
}

func main() {
	setupApp()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	gtbox_log.LogInfof("starting %s version=%s", projectconfig.CurrentApp.AppName, projectconfig.CurrentApp.Version)
	os.Exit(appservice.Run(ctx, os.Args[1:], os.Stderr))
}

func init() {
	if mGoVersion == "" {
		mGoVersion = runtime.Version()
	}
	if mPackageOS == "" {
		mPackageOS = runtime.GOOS
	}
	if mPackageTime == "" {
		mPackageTime = time.Now().UTC().Format("2006-01-02_15:04:05")
	}
}
