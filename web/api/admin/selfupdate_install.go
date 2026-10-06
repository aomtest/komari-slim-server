package admin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aomtest/komari-slim-server/utils"
)

const (
	// 保留的备份数量。再多也没意义 —— 真要回退到更早的版本,
	// 应该走 install 脚本或手动处理。
	updateMaxBackups = 3

	// 重启后等待健康检查的总时长。
	updateHealthTimeout = 60 * time.Second
	// 重启命令发起后,等进程被 systemd 终止的时间。
	updateRestartGrace = 10 * time.Second
)

// binaryPath 返回当前进程的真实可执行文件路径。
//
// 解析符号链接:如果二进制是通过软链暴露的,我们要替换的是真实文件,
// 而不是把软链本身换掉。
func binaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot determine the running binary path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the binary path: %w", err)
	}
	return resolved, nil
}

// isDocker 检测是否运行在容器里。
//
// 容器内替换二进制会随容器重建而丢失,所以本功能在容器里直接拒绝,
// 而不是做一个"看起来能用、实际下次重启就回滚"的实现。
func isDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if os.Getenv("DOCKER") == "1" || os.Getenv("container") != "" {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if strings.Contains(content, "docker") ||
			strings.Contains(content, "kubepods") ||
			strings.Contains(content, "containerd") {
			return true
		}
	}
	return false
}

var unitNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)

// systemdUnitName 推断当前进程所属的 systemd unit。
//
// 优先从 cgroup 读取 —— 用户可能用 --install-service-name 改过服务名,
// 写死 "komari" 会在那种情况下把服务重启错。
func systemdUnitName() (string, error) {
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			idx := strings.LastIndex(line, "/")
			if idx < 0 {
				continue
			}
			name := strings.TrimSpace(line[idx+1:])
			if unitNamePattern.MatchString(name) {
				return name, nil
			}
		}
	}
	// cgroup v1 的另一种形态:/system.slice/komari.service 出现在行尾
	if data, err := os.ReadFile("/proc/1/cmdline"); err == nil {
		if strings.Contains(string(data), "systemd") {
			return "komari.service", nil
		}
	}
	return "", fmt.Errorf("cannot determine the systemd unit for this process")
}

// checkUpdatePreconditions 汇总所有"能不能更新"的前置条件。
func checkUpdatePreconditions() (bool, string) {
	// 非稳定通道明确拒绝,而不是"检测不到更新"。
	//
	// 快照版/预发布版从分支构建,代码比任何正式版都新。让它们走"更新"通道
	// 只会降级到更旧的正式版,而且用户看不出为什么"没有更新"。
	// 切通道应该用安装脚本 —— 那里有通道选择和确认流程。
	if ch := nonStableChannel(utils.CurrentVersion); ch != "" {
		return false, fmt.Sprintf(
			"this build comes from the %s channel; online update is not available. "+
				"Use the install script to switch channels.", ch)
	}
	if runtime.GOOS != "linux" {
		return false, "self-update is only supported on Linux; please update manually"
	}
	if isDocker() {
		return false, "running inside a container; update the image instead"
	}
	if _, err := systemdUnitName(); err != nil {
		return false, "this process does not appear to be managed by systemd"
	}
	path, err := binaryPath()
	if err != nil {
		return false, err.Error()
	}
	dir := filepath.Dir(path)
	if err := checkDirWritable(dir); err != nil {
		return false, fmt.Sprintf("the binary directory %s is not writable: %v", dir, err)
	}

	// 备份目录必须可用 —— 没有备份的替换是不安全的。
	backupDirPath, err := backupDir()
	if err != nil {
		return false, err.Error()
	}
	if err := os.MkdirAll(backupDirPath, 0o755); err != nil {
		return false, fmt.Sprintf("cannot create the backup directory %s: %v", backupDirPath, err)
	}
	// MkdirAll 成功不代表目录可写:目录可能早就存在、只是权限不对。
	// 必须再实测一次,否则会在"备份"这一步才失败 —— 那时二进制已经被 rename 走了。
	if err := checkDirWritable(backupDirPath); err != nil {
		return false, fmt.Sprintf("the backup directory %s is not writable: %v", backupDirPath, err)
	}
	return true, ""
}

// checkDirWritable 通过实际创建再删除一个临时文件来判断目录可写。
//
// 不直接用 os.Access:它在某些文件系统上不可靠,而"能不能写"这件事
// 我们等下真的要写,不如直接试。
func checkDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".komari-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// hasUsableBackup 判断是否存在可用的备份(用于 check 响应里的 has_backup)。
func hasUsableBackup() bool {
	return len(listBackups()) > 0
}

// listBackups 返回按时间倒序排列的备份文件路径。
// 备份目录不可用时返回空 —— 调用方按"没有备份"处理。
func listBackups() []string {
	dir, err := backupDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "komari.bak.") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	// 文件名里带时间戳,字典序即时间序
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// backupBinary 把当前二进制原子重命名为备份文件。
//
// 用 rename 而不是 copy:copy 会留下一个"原文件还在、副本也在"的中间状态,
// 而 rename 之后原路径立刻空出来,下一步可以原子地把新文件放上去。
// 进程在二进制被 rename 走后仍能继续运行 —— inode 还在内存里。
func backupBinary(currentBinary string) (string, error) {
	dir, err := backupDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create the backup directory: %w", err)
	}

	name := fmt.Sprintf("komari.bak.%s.%s", utils.CurrentVersion,
		time.Now().Format("20060102-150405"))
	target := filepath.Join(dir, name)

	if err := os.Rename(currentBinary, target); err != nil {
		return "", fmt.Errorf("failed to move the current binary to %s: %w", target, err)
	}
	return target, nil
}

// cleanOldBackups 只保留最近 updateMaxBackups 个备份。
func cleanOldBackups() {
	backups := listBackups()
	if len(backups) <= updateMaxBackups {
		return
	}
	for _, path := range backups[updateMaxBackups:] {
		// 删之前确认文件名符合我们的命名规则,避免误删同目录下的其他文件。
		if !strings.HasPrefix(filepath.Base(path), "komari.bak.") {
			continue
		}
		os.Remove(path)
	}
}

// replaceBinary 把验证过的临时文件原子放到目标路径。
//
// 前置条件:目标路径已经空出来(由 backupBinary 完成)。
// 这里只做一次 rename —— 失败时调用方负责把备份还原回去。
func replaceBinary(tmpPath, target string) error {
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("failed to place the new binary: %w", err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return fmt.Errorf("failed to set the binary permissions: %w", err)
	}
	return nil
}

// restoreBackup 把备份还原到目标路径。用于替换失败或新版本起不来时。
func restoreBackup(backupPath, target string) error {
	// 目标位置可能残留一个不完整的新文件,先清掉。
	if _, err := os.Stat(target); err == nil {
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("cannot clear the incomplete binary at %s: %w", target, err)
		}
	}
	if err := os.Rename(backupPath, target); err != nil {
		return fmt.Errorf("failed to restore the backup: %w", err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return fmt.Errorf("failed to restore the binary permissions: %w", err)
	}
	return nil
}

// restartService 通过 systemd 重启服务。
//
// 为什么用 systemctl 而不是 syscall.Exec:本功能已限定只支持 systemd 环境,
// 用 systemd 管理生命周期才是一致的选择。syscall.Exec 之后 PID 不变,
// systemd 感知不到重启,unit 状态不刷新、日志不轮转,等于让服务脱管。
//
// 注意:restart 会终止当前进程自身(我们就是被重启的服务),所以必须异步发起。
func restartService(unit string) error {
	// unit 名来自 cgroup 推断且已通过正则校验,这里仍然不使用 shell,
	// 避免任何拼接注入的可能。
	if !unitNamePattern.MatchString(unit) {
		return fmt.Errorf("refusing to restart an unexpected unit name: %q", unit)
	}
	cmd := exec.Command("systemctl", "restart", unit)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to invoke systemctl: %w", err)
	}
	// 不等它返回 —— systemctl 会先停掉我们。
	go func() {
		time.Sleep(updateRestartGrace)
		// 如果到这里我们还没被杀掉,说明 restart 没有生效,记录一下。
		_ = cmd.Wait()
	}()
	return nil
}

// 注意:这里**没有**进程内的健康检查。
//
// 更新流程最后一步是 systemd restart,当前进程会在那一刻被终止 ——
// 同一个进程无法检查自己的重启结果。结果判定放在新进程里做:
// 见 selfupdate_api.go 的 resolveRestartOutcome,它用新进程自己的版本号
// 与目标任务的目标版本比对。前端则通过 status 接口轮询拿到这个结论。
