package main

import (
	"log/slog"

	"github.com/aomtest/komari-slim-server/cmd"
	"github.com/aomtest/komari-slim-server/utils"
	logger "github.com/aomtest/komari-slim-server/utils/log"
)

func main() {
	if utils.VersionHash == "unknown" {
		logger.Setup(slog.LevelDebug)
	} else {
		logger.Setup(slog.LevelInfo)
	}

	logger.Infof("server", "komari-slim %s (hash: %s)", utils.CurrentVersion, utils.VersionHash)

	cmd.Execute()
}
