package config

import (
	"github.com/george012/gtbox"
	"github.com/george012/gtbox/gtbox_app"
)

const (
	ProjectName        = "vllm-use"
	ProjectVersion     = "v0.0.1"
	ProjectBundleID    = "com.0xdevelop.vllm-use"
	ProjectDescription = "Native vLLM management and inference service"
)

var CurrentApp *ExtendApp

type ExtendApp struct {
	*gtbox_app.App
}

func NewApp(runMode gtbox.RunMode) *ExtendApp {
	return &ExtendApp{App: gtbox_app.NewApp(
		ProjectName,
		ProjectVersion,
		ProjectBundleID,
		ProjectDescription,
		runMode,
	)}
}
