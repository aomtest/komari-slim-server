package admin

import (
	"github.com/aomtest/komari-slim-server/web/upload"
)

// NewArchiveUploadHandler 注册分片上传支持的归档用途。
//
// 裁剪后只剩主题安装一种用途：备份还原（PurposeBackup）与插件安装
// （PurposePlugin）已随备份/插件功能一并移除。
func NewArchiveUploadHandler() *upload.Handler {
	return upload.NewHandler(upload.DefaultStore, map[upload.Purpose]upload.Finalizer{
		upload.PurposeTheme: finalizeThemeUpload,
	})
}

func finalizeThemeUpload(session upload.Session) (upload.Result, error) {
	info, err := extractAndValidateTheme(session.ArchivePath)
	if err != nil {
		return upload.Result{}, err
	}
	return upload.Result{Message: "主题上传成功", Data: info}, nil
}
