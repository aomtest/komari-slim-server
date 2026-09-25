package server

import (
	"context"

	"github.com/aomtest/komari-slim-server/utils/geoip"
	"github.com/aomtest/komari-slim-server/utils/messageSender"
)

// InitProviders initializes providers needed by the normal application.
//
// 裁剪后不再初始化 OAuth / OIDC 提供者：外部登录已随 web/oauth 模块一并移除。
func (a *App) InitProviders() error {
	go geoip.InitGeoIp()
	a.addCleanup("geoip", func(context.Context) error { return geoip.Shutdown() })

	messageSender.Initialize()
	a.addCleanup("message-sender", func(context.Context) error { return messageSender.Shutdown() })
	return nil
}
