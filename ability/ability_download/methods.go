package ability_download

import (
	"context"
	"errors"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

const (
	MethodList   = "downloads.list"
	MethodStart  = "downloads.start"
	MethodStatus = "downloads.status"
	MethodLogs   = "downloads.logs"
	MethodCancel = "downloads.cancel"
	MethodRetry  = "downloads.retry"
)

var currentDownloader *Downloader

func Setup(downloader *Downloader) { currentDownloader = downloader }

func LoadAPIMethods() {
	add(MethodList, "列出下载任务", nil, nil, func(context.Context, interface{}) (interface{}, error) { return downloader().List(), nil })
	add(MethodStart, "启动模型下载", requestProperties(), nil, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in Request
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		return downloader().DownloadRequest(context.WithoutCancel(ctx), in)
	})
	add(MethodStatus, "读取下载状态", map[string]interface{}{"id": str()}, []string{"id"}, func(_ context.Context, input interface{}) (interface{}, error) {
		id, err := inputID(input)
		if err != nil {
			return nil, err
		}
		job, ok := downloader().Status(id)
		if !ok {
			return nil, sqlite.ErrNotFound
		}
		return job, nil
	})
	add(MethodLogs, "读取下载日志", map[string]interface{}{"id": str()}, []string{"id"}, func(_ context.Context, input interface{}) (interface{}, error) {
		id, err := inputID(input)
		if err != nil {
			return nil, err
		}
		return downloader().Logs(id)
	})
	add(MethodCancel, "取消下载", map[string]interface{}{"id": str()}, []string{"id"}, func(_ context.Context, input interface{}) (interface{}, error) {
		id, err := inputID(input)
		if err != nil {
			return nil, err
		}
		err = downloader().Cancel(id)
		return map[string]bool{"canceled": err == nil}, err
	})
	add(MethodRetry, "重试下载", map[string]interface{}{"id": str(), "token": str()}, []string{"id"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		return downloader().Retry(ctx, in.ID, in.Token)
	})
}

func requestProperties() map[string]interface{} {
	return map[string]interface{}{"id": str(), "model_id": str(), "repository": str(), "destination": str(), "token": str(), "revision": str()}
}
func inputID(input interface{}) (string, error) {
	var in struct {
		ID string `json:"id"`
	}
	err := api_supported_methods.DecodeArguments(input, &in)
	return in.ID, err
}
func downloader() *Downloader {
	if currentDownloader == nil {
		panic(errors.New("download ability is not initialized"))
	}
	return currentDownloader
}
func add(name, description string, properties map[string]interface{}, required []string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: "mcp.models", InputSchema: api_supported_methods.ObjectSchema(properties, required), Async: name == MethodStart, Execute: execute})
}
func str() map[string]interface{} { return map[string]interface{}{"type": "string"} }
