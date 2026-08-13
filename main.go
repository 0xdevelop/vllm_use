package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0xdevelop/vllm-use/app"
	"github.com/0xdevelop/vllm-use/config"
	"github.com/0xdevelop/vllm-use/custom_cmd"
	"github.com/george012/gtbox"
	"github.com/george012/gtbox/gtbox_cmd"
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
	config.CurrentApp = config.NewApp(
		config.ProjectName,
		config.ProjectBundleID,
		fmt.Sprintf("%s service", config.ProjectName),
		runMode,
	)
	if runMode == gtbox.RunModeDebug {
		result := gtbox_cmd.RunWith(map[string]string{
			"git_commit_hash": "git show -s --format=%H",
			"git_commit_time": "git show -s --format=\"%ci\" | cut -d ' ' -f 1,2 | sed 's/ /_/'",
			"build_os":        "go env GOOS",
			"go_version":      "go version | awk '{print $3}'",
		})
		mGitCommitHash = result["git_commit_hash"]
		mGitCommitTime = result["git_commit_time"]
		mPackageOS = result["build_os"]
		mGoVersion = result["go_version"]
		mPackageTime = time.Now().UTC().Format("2006-01-02_15:04:05")
	}
	config.CurrentApp.GitCommitHash = mGitCommitHash
	config.CurrentApp.GitCommitTime = mGitCommitTime
	config.CurrentApp.GoVersion = mGoVersion
	config.CurrentApp.PackageOS = mPackageOS
	config.CurrentApp.PackageTime = mPackageTime

	custom_cmd.HandleCustomCmds(os.Args, config.CurrentApp)
	gtbox.SetupGTBox(
		config.CurrentApp.AppName,
		config.CurrentApp.CurrentRunMode,
		config.CurrentApp.AppLogPath,
		30,
		gtbox_log.GTLogSaveHours,
		config.CurrentApp.HTTPRequestTimeOut,
	)
}

func main() {
	setupApp()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(app.Run(ctx, os.Args[1:], os.Stderr))
}
