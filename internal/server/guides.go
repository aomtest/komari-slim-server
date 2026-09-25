package server

import (
	"github.com/aomtest/komari-slim-server/database/dbcore"
	"github.com/aomtest/komari-slim-server/database/models"
	installweb "github.com/aomtest/komari-slim-server/web/install"
)

// InstallRequired reports whether the instance still needs the first-run guide.
func (a *App) InstallRequired() (bool, error) {
	var count int64
	if err := dbcore.GetDBInstance().Model(&models.User{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

// RunInstallGuide exposes only first-run installation APIs. It intentionally
// does not mount authentication or normal application routes.
//
// 数据库结构迁移向导与 Metric Store 恢复向导已随 web/migration、web/recovery 一并移除。
func (a *App) RunInstallGuide() (bool, error) {
	return a.runGuideServer(installweb.NewController(dbcore.GetDBInstance()), guideServerConfig{
		pagePath:   installweb.PagePath,
		missingAPI: "Not found in install mode",
		logMessage: "First-run installation guide is available on %s",
	})
}
