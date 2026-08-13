// Package api api/api_services.go
package api

import (
	"github.com/0xdevelop/vllm-use/ability"
	"github.com/0xdevelop/vllm-use/api/api_config"
	"github.com/0xdevelop/vllm-use/api/api_jsonRPC"
	"github.com/0xdevelop/vllm-use/api/api_mcp"
	"github.com/0xdevelop/vllm-use/api/api_websocket"
	"github.com/george012/gtbox/gtbox_log"
)

func StartAPIServices(apiCfg *api_config.ApiConfig) {
	ability.LoadAbilityAPIMethods()
	api_config.CurrentApiCfg = apiCfg

	if api_config.CurrentApiCfg.APICfgJsonRPC != nil {
		if api_config.CurrentApiCfg.APICfgJsonRPC.Enabled == true {
			api_jsonRPC.StartAPIServiceWithJsonRPC(apiCfg.APICfgJsonRPC)
		}

	} else {
		gtbox_log.LogErrorf("StartAPIServices API not setup")
	}

	if api_config.CurrentApiCfg.APICfgMCP != nil {
		if api_config.CurrentApiCfg.APICfgMCP.Enabled == true {
			api_mcp.StartAPIServiceWithMCP(apiCfg.APICfgMCP)
		}

	} else {
		gtbox_log.LogErrorf("StartAPIServices API not setup")
	}

	if api_config.CurrentApiCfg.APICfgWebSocket != nil &&
		api_config.CurrentApiCfg.APICfgWebSocket.Enabled {
		api_websocket.StartAPIServiceWithWebSocket(
			apiCfg.APICfgWebSocket,
		)
	}

}

func StopApiServices() {
	api_websocket.StopApiServiceWithWebSocket()
	api_mcp.StopApiServiceWithMCP()
	api_jsonRPC.StopApiServiceWithJsonRPC()
}
