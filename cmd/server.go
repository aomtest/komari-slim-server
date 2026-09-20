package cmd

import (
	"errors"
	"os"

	appserver "github.com/komari-monitor/komari/internal/server"
	logger "github.com/komari-monitor/komari/utils/log"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/spf13/cobra"
)

var ServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the server",
	Long:  `Start the server`,
	Run: func(cmd *cobra.Command, args []string) {
		RunServer()
	},
}

func init() {
	// 从环境变量获取监听地址
	listenAddr := GetEnv("KOMARI_LISTEN", "0.0.0.0:25774")
	ServerCmd.PersistentFlags().StringVarP(&flags.Listen, "listen", "l", listenAddr, "监听地址 [env: KOMARI_LISTEN]")
	RootCmd.AddCommand(ServerCmd)
}

// RunServer 按显式的生命周期阶段启动服务端。
//
// 具体各阶段的职责与顺序见 internal/server.App。这里只负责串联：
// 任一初始化阶段失败即中止启动，避免在半初始化状态下对外提供服务。
//
// 裁剪说明：移除了数据库结构迁移向导与 Metric Store 恢复向导。
// 首次安装引导（RunInstallGuide）保留，它是全新部署的必经路径。
// Metric Store 连接失败时直接中止启动，由运维修正配置后重启。
func RunServer() {
	app := appserver.New(appserver.Options{ListenAddr: flags.Listen})
	if err := app.Bootstrap(); err != nil {
		_ = app.Shutdown()
		logger.Fatalf("server", "server startup failed at %q: %v", "bootstrap", err)
	}

	installRequired, err := app.InstallRequired()
	if err != nil {
		_ = app.Shutdown()
		logger.Fatalf("server", "server startup failed at %q: %v", "detect-first-run-install", err)
	}
	if installRequired {
		completed, err := app.RunInstallGuide()
		if err != nil {
			_ = app.Shutdown()
			logger.Fatalf("server", "server startup failed at %q: %v", "run-first-run-install", err)
		}
		if !completed {
			return
		}
	}

	if err := app.ConnectMetricStoreWithRetry(); err != nil {
		_ = app.Shutdown()
		logger.Fatalf("server", "server startup failed at %q: %v", "connect-metric-store", err)
	}

	// 其余初始化阶段：任一步失败都不应继续对外服务。
	type stage struct {
		name string
		fn   func() error
	}
	stages := []stage{
		{"init-stores", app.InitStores},
		{"init-providers", app.InitProviders},
		{"start-background", app.StartBackground},
		{"build-router", app.BuildRouter},
	}
	for _, s := range stages {
		if err := s.fn(); err != nil {
			// 已登记的资源尽力回收，再退出。
			_ = app.Shutdown()
			logger.Fatalf("server", "server startup failed at %q: %v", s.name, err)
		}
	}

	if err := app.Run(); err != nil {
		if errors.Is(err, appserver.ErrRestartRequested) {
			logger.Infof("server", "Service stopped for restart: %v", err)
			os.Exit(1)
		}
		logger.Fatalf("server", "server exited with error: %v", err)
	}
}
