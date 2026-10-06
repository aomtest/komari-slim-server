package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aomtest/komari-slim-server/database/auditlog"
	"github.com/aomtest/komari-slim-server/utils"
	logger "github.com/aomtest/komari-slim-server/utils/log"
	api "github.com/aomtest/komari-slim-server/web/api"
	"github.com/gin-gonic/gin"
)

// taskStatePath 是任务状态的持久化位置。
//
// 为什么必须落盘:更新成功后进程会被 systemd 重启,内存里的任务状态随之丢失。
// 而"重启之后到底成没成功"恰恰是前端最需要知道的信息。新进程读回这个文件,
// 结合自己的版本号就能给出结论。
func taskStatePath() string {
	return filepath.Join("data", "self-update-task.json")
}

// persistTask 把任务状态写入磁盘。
func persistTask(task *UpdateTask) {
	if task == nil {
		return
	}
	if err := os.MkdirAll("data", 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return
	}
	// 先写临时文件再 rename,避免读到写了一半的内容。
	tmp := taskStatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	os.Rename(tmp, taskStatePath())
}

// loadPersistedTask 读回上次的任务状态。
func loadPersistedTask() *UpdateTask {
	data, err := os.ReadFile(taskStatePath())
	if err != nil {
		return nil
	}
	var task UpdateTask
	if err := json.Unmarshal(data, &task); err != nil {
		return nil
	}
	return &task
}

// resolveRestartOutcome 处理"重启前留下的任务"。
//
// 上一次进程在 restarting 阶段被杀掉,现在我们已经起来了。用当前版本号
// 判断结果:版本对上了就是成功,否则标记为"状态未知"。
//
// 这里刻意不标记为 failed —— 进程确实重启了,我们只是无法从内部确认。
// 直接断言失败会误导用户去回滚一个可能其实正常的版本。
func resolveRestartOutcome(task *UpdateTask) *UpdateTask {
	if task == nil || task.Stage != StageRestarting {
		return task
	}
	current := utils.CurrentVersion
	target := strings.TrimPrefix(task.ToVersion, "v")
	currentTrimmed := strings.TrimPrefix(current, "v")

	if currentTrimmed == target {
		task.Stage = StageSucceeded
		task.UpdatedAt = time.Now().UTC()
		persistTask(task)
		return task
	}

	// 还没到超时窗口就再等等 —— 可能是前端在我们刚起来时就来问了。
	if time.Since(task.UpdatedAt) < updateHealthTimeout {
		return task
	}
	task.Stage = StageUnknown
	task.Error = fmt.Sprintf("restarted but the running version is still %s (expected %s)", current, task.ToVersion)
	task.UpdatedAt = time.Now().UTC()
	persistTask(task)
	return task
}

// currentTask 返回当前任务:优先内存,内存没有则读持久化文件。
func currentTask() *UpdateTask {
	if t := updateTasks.Snapshot(); t != nil {
		return resolveRestartOutcome(t)
	}
	return resolveRestartOutcome(loadPersistedTask())
}

// ---------------------------------------------------------------------------
// CSRF 防护
// ---------------------------------------------------------------------------

// RequireSameOriginFetch 要求请求带 X-Requested-With: XMLHttpRequest。
//
// 项目现有的 CORS 中间件已经会拒绝带不匹配 Origin 的请求,但那个保护受
// cors_origin_check_enabled 开关控制,关掉之后就没了。而这个接口能替换服务端
// 二进制,不应该依赖一个可以被配置关掉的防护。
//
// 自定义 header 不受该开关影响:浏览器对跨站请求要设置自定义 header,
// 必须先通过 CORS 预检,而预检同样会被 CORS 中间件拦下 —— 两道防线叠加。
//
// 注意这只是纵深防御的一层,不是唯一手段。真正的门槛是 RequireRole(RoleAdmin)。
func RequireSameOriginFetch() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.EqualFold(c.GetHeader("X-Requested-With"), "XMLHttpRequest") {
			api.RespondError(c, http.StatusForbidden,
				"missing X-Requested-With header; this endpoint must be called from the panel")
			c.Abort()
			return
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// CheckSelfUpdate 返回当前版本、最新 release 元数据和能否更新的判断。
//
// GET /api/admin/self-update/check
func CheckSelfUpdate(c *gin.Context) {
	info, err := buildUpdateInfo()
	if err != nil {
		// 元数据拿不到不等于环境有问题,所以不在这里写审计日志 ——
		// 否则网络抖动会刷满日志。
		c.JSON(200, gin.H{
			"status":  "error",
			"message": err.Error(),
			"data": gin.H{
				"current":    utils.CurrentVersion,
				"can_update": false,
			},
		})
		return
	}

	// 任务进行中时不允许再发起更新。
	if t := currentTask(); t != nil && !t.IsTerminal() {
		info.CanUpdate = false
		info.BlockedReason = fmt.Sprintf("a %s task is already running (stage: %s)", t.Kind, t.Stage)
	}

	c.JSON(200, gin.H{"status": "success", "message": "", "data": info})
}

// ApplySelfUpdate 创建更新任务并立即返回任务 ID。
//
// POST /api/admin/self-update/apply
//
// 请求体只接受 check 阶段拿到的版本号与资产摘要,用于二次确认"要装的确实是
// 你看到的那个"。下载地址、文件路径一律由服务端决定,不接受任何输入。
func ApplySelfUpdate(c *gin.Context) {
	var req struct {
		Tag    string `json:"tag"`
		Digest string `json:"digest"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, 400, "invalid request body")
		return
	}
	if req.Tag == "" || req.Digest == "" {
		api.RespondError(c, 400, "tag and digest are required")
		return
	}

	uuid, _ := c.Get("uuid")
	operator, _ := uuid.(string)

	ok, reason := checkUpdatePreconditions()
	if !ok {
		api.RespondError(c, 400, reason)
		return
	}

	rel, err := fetchLatestRelease()
	if err != nil {
		api.RespondError(c, 502, "cannot reach the release API: "+err.Error())
		return
	}
	if rel.TagName != req.Tag {
		api.RespondError(c, 409, fmt.Sprintf("the latest release is now %s, not %s; please check again", rel.TagName, req.Tag))
		return
	}
	asset, err := pickAsset(rel)
	if err != nil {
		api.RespondError(c, 502, err.Error())
		return
	}
	if asset.Digest != req.Digest {
		api.RespondError(c, 409, "the asset digest changed since the last check; please check again")
		return
	}

	task, err := updateTasks.Begin("update", utils.CurrentVersion, rel.TagName)
	if err != nil {
		api.RespondError(c, 409, err.Error())
		return
	}
	persistTask(task)

	auditlog.Log(c.ClientIP(), operator,
		fmt.Sprintf("self-update started: %s -> %s (task %s)", utils.CurrentVersion, rel.TagName, task.ID),
		"info")

	go runUpdate(task.ID, *asset, rel.TagName)

	c.JSON(200, gin.H{"status": "success", "message": "", "data": gin.H{
		"task_id":        task.ID,
		"target_version": rel.TagName,
	}})
}

// SelfUpdateEvents 用 SSE 推送任务进度。
//
// GET /api/admin/self-update/events
//
// 只订阅进度,不承担更新本身 —— 更新是后台任务,这个连接随时可以断。
func SelfUpdateEvents(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 让 nginx 不要缓冲

	ch, cancel := updateTasks.Subscribe()
	defer cancel()

	// 先补发一次当前状态,避免刚连上时界面是空的。
	if t := currentTask(); t != nil {
		writeSSE(c, "stage", t)
		if t.IsTerminal() {
			writeSSE(c, "done", t)
			return
		}
	}

	// 心跳:重启期间没有事件,没有心跳的话前端无法区分"卡住了"和"正在重启"。
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case task, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(c, "stage", &task)
			if task.IsTerminal() {
				writeSSE(c, "done", &task)
				return
			}
		case <-ticker.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		}
	}
}

// writeSSE 输出一条 SSE 事件。
func writeSSE(c *gin.Context, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
	c.Writer.Flush()
}

// SelfUpdateStatus 查询任务状态。
//
// GET /api/admin/self-update/status
//
// 这是重启之后前端唯一的确认手段 —— SSE 连接早就断了。
func SelfUpdateStatus(c *gin.Context) {
	task := currentTask()
	if task == nil {
		c.JSON(200, gin.H{"status": "success", "message": "", "data": gin.H{
			"task":          nil,
			"current":       utils.CurrentVersion,
			"has_backup":    hasUsableBackup(),
		}})
		return
	}
	c.JSON(200, gin.H{"status": "success", "message": "", "data": gin.H{
		"task":       task,
		"current":    utils.CurrentVersion,
		"has_backup": hasUsableBackup(),
	}})
}

// RollbackSelfUpdate 创建回滚任务。
//
// POST /api/admin/self-update/rollback
//
// 与 apply 走完全相同的环境检查、备份序列和重启流程 —— 它同样是"替换服务端
// 二进制"的能力,少一道校验就等于把 apply 的防护绕过去了。
func RollbackSelfUpdate(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	operator, _ := uuid.(string)

	ok, reason := checkUpdatePreconditions()
	if !ok {
		api.RespondError(c, 400, reason)
		return
	}

	backups := listBackups()
	if len(backups) == 0 {
		api.RespondError(c, 400, "no backup is available to roll back to")
		return
	}

	task, err := updateTasks.Begin("rollback", utils.CurrentVersion, "")
	if err != nil {
		api.RespondError(c, 409, err.Error())
		return
	}
	persistTask(task)

	auditlog.Log(c.ClientIP(), operator,
		fmt.Sprintf("self-update rollback started (task %s)", task.ID), "warn")

	go runRollback(task.ID, backups[0])

	c.JSON(200, gin.H{"status": "success", "message": "", "data": gin.H{"task_id": task.ID}})
}

// ---------------------------------------------------------------------------
// 任务编排
// ---------------------------------------------------------------------------

// runUpdate 是更新的主流程。任何失败都要走恢复路径,不允许留下
// "二进制没了、服务起不来"的状态。
func runUpdate(taskID string, asset UpdateAsset, targetTag string) {
	path, err := binaryPath()
	if err != nil {
		updateTasks.Fail(taskID, StageQueued, err)
		persistTask(updateTasks.Snapshot())
		return
	}
	dir := filepath.Dir(path)

	// ② 重新获取元数据并比对,防"检查时是 A、执行时是 B"。
	updateTasks.SetStage(taskID, StageVerifying)
	persistTask(updateTasks.Snapshot())
	if _, err := verifyAssetConsistency(&asset, targetTag); err != nil {
		updateTasks.Fail(taskID, StageVerifying, err)
		persistTask(updateTasks.Snapshot())
		return
	}

	// ③ 下载
	updateTasks.SetStage(taskID, StageDownloading)
	persistTask(updateTasks.Snapshot())
	tmpPath, gotDigest, err := downloadAsset(asset, dir, taskID, func(done, total int64) {
		updateTasks.SetProgress(taskID, done, total)
	})
	if err != nil {
		updateTasks.Fail(taskID, StageDownloading, err)
		persistTask(updateTasks.Snapshot())
		return
	}
	// 下载失败或后续任何一步失败,都要清掉临时文件。
	keepTmp := false
	defer func() {
		if !keepTmp {
			os.Remove(tmpPath)
		}
	}()

	// ④ 校验摘要
	updateTasks.SetStage(taskID, StageVerifying)
	wantDigest := strings.TrimPrefix(asset.Digest, "sha256:")
	if !strings.EqualFold(gotDigest, wantDigest) {
		updateTasks.Fail(taskID, StageVerifying,
			fmt.Errorf("checksum mismatch: expected %s, got %s", wantDigest, gotDigest))
		persistTask(updateTasks.Snapshot())
		return
	}

	// ⑤ 备份:把当前二进制 rename 走,而不是删掉。
	// 这样下一步失败时备份还在原位,可以原子恢复。
	updateTasks.SetStage(taskID, StageBackingUp)
	persistTask(updateTasks.Snapshot())
	backupPath, err := backupBinary(path)
	if err != nil {
		updateTasks.Fail(taskID, StageBackingUp, err)
		persistTask(updateTasks.Snapshot())
		return
	}

	// ⑥ 替换
	updateTasks.SetStage(taskID, StageReplacing)
	persistTask(updateTasks.Snapshot())
	if err := replaceBinary(tmpPath, path); err != nil {
		// 替换失败 —— 把备份还原回去,服务仍然可用。
		if rerr := restoreBackup(backupPath, path); rerr != nil {
			// 连恢复都失败了,这是最坏情况,必须让用户知道需要人工介入。
			updateTasks.Fail(taskID, StageReplacing,
				fmt.Errorf("%v; additionally failed to restore the backup: %v", err, rerr))
		} else {
			updateTasks.Fail(taskID, StageReplacing, err)
			updateTasks.Finish(taskID, StageFailed, true)
		}
		persistTask(updateTasks.Snapshot())
		return
	}
	keepTmp = true // 已经被 rename 走了

	cleanOldBackups()

	// ⑦ 重启。进程会在这里被 systemd 终止,所以这是本任务在本进程内能做的最后一步。
	updateTasks.SetStage(taskID, StageRestarting)
	persistTask(updateTasks.Snapshot())

	unit, err := systemdUnitName()
	if err != nil {
		updateTasks.Fail(taskID, StageRestarting, err)
		persistTask(updateTasks.Snapshot())
		return
	}
	if err := restartService(unit); err != nil {
		// 重启都没能发起,尝试把旧版本还原回去。
		if rerr := restoreBackup(backupPath, path); rerr != nil {
			updateTasks.Fail(taskID, StageRestarting,
				fmt.Errorf("%v; additionally failed to restore the backup: %v", err, rerr))
		} else {
			updateTasks.Fail(taskID, StageRestarting, err)
			updateTasks.Finish(taskID, StageFailed, true)
		}
		persistTask(updateTasks.Snapshot())
		return
	}

	// 到这里 systemctl 已经在停我们的服务了。后续结果由新进程在
	// resolveRestartOutcome 里判定。
	logger.Info("self-update", "restart issued", "task", taskID, "unit", unit, "target", targetTag)
}

// runRollback 把最近的备份还原回去并重启。
func runRollback(taskID string, backupPath string) {
	path, err := binaryPath()
	if err != nil {
		updateTasks.Fail(taskID, StageQueued, err)
		persistTask(updateTasks.Snapshot())
		return
	}

	// 备份文件名形如 komari.bak.<版本>.<时间戳>,从中取出要回退到的版本。
	rolledTo := ""
	if parts := strings.Split(filepath.Base(backupPath), "."); len(parts) >= 3 {
		rolledTo = parts[2]
	}

	updateTasks.SetStage(taskID, StageBackingUp)
	persistTask(updateTasks.Snapshot())

	// 回滚前也先备份当前版本,这样"回滚错了"还能再回滚回来。
	currentBackup, err := backupBinary(path)
	if err != nil {
		updateTasks.Fail(taskID, StageBackingUp, err)
		persistTask(updateTasks.Snapshot())
		return
	}

	updateTasks.SetStage(taskID, StageReplacing)
	persistTask(updateTasks.Snapshot())
	if err := restoreBackup(backupPath, path); err != nil {
		// 还原失败 —— 把刚才备份的当前版本放回去。
		if rerr := restoreBackup(currentBackup, path); rerr != nil {
			updateTasks.Fail(taskID, StageReplacing,
				fmt.Errorf("%v; additionally failed to restore the current binary: %v", err, rerr))
		} else {
			updateTasks.Fail(taskID, StageReplacing, err)
			updateTasks.Finish(taskID, StageFailed, true)
		}
		persistTask(updateTasks.Snapshot())
		return
	}

	updateTasks.SetStage(taskID, StageRestarting)
	persistTask(updateTasks.Snapshot())

	unit, err := systemdUnitName()
	if err != nil {
		updateTasks.Fail(taskID, StageRestarting, err)
		persistTask(updateTasks.Snapshot())
		return
	}
	if err := restartService(unit); err != nil {
		updateTasks.Fail(taskID, StageRestarting, err)
		persistTask(updateTasks.Snapshot())
		return
	}

	logger.Info("self-update", "rollback restart issued", "task", taskID, "rolled_to", rolledTo)
}
