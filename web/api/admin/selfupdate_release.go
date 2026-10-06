package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aomtest/komari-slim-server/utils"
)

// 固定的 release 来源。仓库地址、API 端点都是服务端常量,不接受任何请求参数 ——
// 否则这个接口就成了"让服务器下载并执行任意文件"的入口。
const (
	updateRepoOwner = "aomtest"
	updateRepoName  = "komari-slim-server"

	updateDownloadTimeout = 10 * time.Minute
	updateMetadataTimeout = 30 * time.Second
	// 二进制目前约 30MB,给足余量但必须有上限,避免被超大的响应拖死。
	updateMaxAssetSize = 200 << 20
)

// UpdateAsset 是目标 release 里与当前平台匹配的那个资产。
type UpdateAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // 形如 "sha256:..."
	URL    string `json:"-"`      // 不返回给前端
}

// UpdateInfo 是 check 接口的响应体。
type UpdateInfo struct {
	Current         string      `json:"current"`
	Latest          string      `json:"latest"`
	UpdateAvailable bool        `json:"update_available"`
	ReleaseID       int64       `json:"release_id"`
	Tag             string      `json:"tag"`
	Asset           UpdateAsset `json:"asset"`
	ReleaseNotes    string      `json:"release_notes"`
	PublishedAt     string      `json:"published_at"`
	CanUpdate       bool        `json:"can_update"`
	BlockedReason   string      `json:"blocked_reason,omitempty"`
	HasBackup       bool        `json:"has_backup"`
}

// githubRelease 是 GitHub releases API 的响应子集。
type githubRelease struct {
	ID         int64  `json:"id"`
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	Published  string `json:"published_at"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	Assets     []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	} `json:"assets"`
}

// newUpdateTaskID 生成不可预测的任务 ID。
//
// 刻意不用版本号或时间戳拼接:任务 ID 会出现在 URL 和日志里,可预测的 ID
// 会让"猜任务 ID"变成一个可能的攻击面。
func newUpdateTaskID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 随机源不可用时退化为时间戳,仍然可用但可预测性变差。
		return fmt.Sprintf("srv-upd-%d", time.Now().UnixNano())
	}
	return "srv-upd-" + hex.EncodeToString(b[:])
}

// currentAssetName 返回当前平台对应的资产文件名。
func currentAssetName() (string, error) {
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "linux":
		return "komari-linux-" + arch, nil
	case "windows":
		return "komari-windows-" + arch + ".exe", nil
	default:
		return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// fetchLatestRelease 拉取最新正式 release 元数据。
func fetchLatestRelease() (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest",
		updateRepoOwner, updateRepoName)

	client := &http.Client{Timeout: updateMetadataTimeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "komari-slim-server")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach the release API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release API returned HTTP %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("failed to parse the release response: %w", err)
	}
	if rel.Draft || rel.Prerelease {
		return nil, fmt.Errorf("latest release is not a stable release")
	}
	return &rel, nil
}

// pickAsset 从 release 里挑出当前平台对应的资产。
func pickAsset(rel *githubRelease) (*UpdateAsset, error) {
	want, err := currentAssetName()
	if err != nil {
		return nil, err
	}
	for _, a := range rel.Assets {
		if a.Name != want {
			continue
		}
		if a.Digest == "" {
			// 没有 digest 就不能验证下载内容。宁可拒绝更新,也不降级成
			// "下载了就装" —— 那等于开了一条没有完整性校验的更新通道。
			return nil, fmt.Errorf("asset %q has no digest; refusing to update without integrity data", want)
		}
		if !strings.HasPrefix(a.Digest, "sha256:") {
			return nil, fmt.Errorf("asset %q uses an unsupported digest format: %s", want, a.Digest)
		}
		return &UpdateAsset{
			ID:     a.ID,
			Name:   a.Name,
			Size:   a.Size,
			Digest: a.Digest,
			URL:    a.URL,
		}, nil
	}
	return nil, fmt.Errorf("release %s has no asset named %q", rel.TagName, want)
}

// buildUpdateInfo 组装 check 响应。
func buildUpdateInfo() (*UpdateInfo, error) {
	rel, err := fetchLatestRelease()
	if err != nil {
		return nil, err
	}
	asset, err := pickAsset(rel)
	if err != nil {
		return nil, err
	}

	current := utils.CurrentVersion
	latest := rel.TagName

	info := &UpdateInfo{
		Current:         current,
		Latest:          latest,
		UpdateAvailable: compareVersions(current, latest),
		ReleaseID:       rel.ID,
		Tag:             latest,
		Asset:           *asset,
		ReleaseNotes:    rel.Body,
		PublishedAt:     rel.Published,
		HasBackup:       hasUsableBackup(),
	}
	info.CanUpdate, info.BlockedReason = checkUpdatePreconditions()
	return info, nil
}

// verifyAssetConsistency 在 apply 阶段重新获取元数据,并与 check 阶段的结果比对。
//
// 目的是防"检查时看到的是 A,执行时下载的是 B" —— 即使前后只差几分钟,
// release 也可能被替换过。
func verifyAssetConsistency(expected *UpdateAsset, expectedTag string) (*githubRelease, error) {
	rel, err := fetchLatestRelease()
	if err != nil {
		return nil, err
	}
	if rel.TagName != expectedTag {
		return nil, fmt.Errorf("release changed between check and apply: was %s, now %s",
			expectedTag, rel.TagName)
	}
	asset, err := pickAsset(rel)
	if err != nil {
		return nil, err
	}
	if asset.ID != expected.ID || asset.Digest != expected.Digest || asset.Size != expected.Size {
		return nil, fmt.Errorf("asset metadata changed between check and apply")
	}
	return rel, nil
}

// downloadAsset 把资产下载到 targetDir 下的临时文件,边下边算 SHA-256。
//
// 返回临时文件路径和计算出的摘要(不含 "sha256:" 前缀)。
func downloadAsset(asset UpdateAsset, targetDir, taskID string,
	onProgress func(downloaded, total int64)) (string, string, error) {

	client := &http.Client{Timeout: updateDownloadTimeout}
	req, err := http.NewRequest(http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "komari-slim-server")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	// 有 Content-Length 时先比一次大小,能在下载前就发现不对。
	if resp.ContentLength > 0 && resp.ContentLength != asset.Size {
		return "", "", fmt.Errorf("asset size mismatch: expected %d, server reports %d",
			asset.Size, resp.ContentLength)
	}

	// 临时文件建在目标二进制同目录,保证后续 rename 是同文件系统操作。
	// 用 0600:更新期间不希望其他用户能读到或替换这个文件。
	tmp, err := os.CreateTemp(targetDir, ".komari-update-*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("failed to create a temp file next to the binary: %w", err)
	}
	tmpPath := tmp.Name()

	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}

	hasher := sha256.New()
	// 限制读取上限:即使服务端谎报 Content-Length,也不会写出一个超大文件。
	limited := io.LimitReader(resp.Body, updateMaxAssetSize+1)
	written, err := io.Copy(io.MultiWriter(tmp, hasher), &progressReader{
		r: limited, total: asset.Size, onProgress: onProgress,
	})
	if err != nil {
		cleanup()
		return "", "", fmt.Errorf("download interrupted: %w", err)
	}
	if written > updateMaxAssetSize {
		cleanup()
		return "", "", fmt.Errorf("asset exceeds the %d byte limit", int64(updateMaxAssetSize))
	}
	if written != asset.Size {
		cleanup()
		return "", "", fmt.Errorf("size mismatch: expected %d bytes, got %d", asset.Size, written)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", "", fmt.Errorf("failed to flush the downloaded file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("failed to close the downloaded file: %w", err)
	}

	return tmpPath, hex.EncodeToString(hasher.Sum(nil)), nil
}

// progressReader 在读取过程中回调进度。
type progressReader struct {
	r          io.Reader
	read       int64
	total      int64
	onProgress func(downloaded, total int64)
	lastReport time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	// 节流:每个数据块都回调会产生大量事件,对 SSE 没有意义。
	if p.onProgress != nil && time.Since(p.lastReport) > 200*time.Millisecond {
		p.lastReport = time.Now()
		p.onProgress(p.read, p.total)
	}
	return n, err
}

// nonStableChannel 判断当前版本是否来自非稳定通道,并返回通道名。
//
// 三种构建方式注入的版本号格式不同(见 .github/workflows/):
//   stable      -> v0.1.25          (tag 名)
//   prerelease  -> prerelease-<hash> (prerelease 分支)
//   snapshot    -> Snapshot-<时间戳> (main 分支)
//
// 非稳定通道一律不支持在线更新:它们从分支构建,代码比正式版新,
// 走"更新"通道只会被降级到更旧的正式版。切换通道应该用安装脚本,
// 那里有明确的通道选择和确认流程。
func nonStableChannel(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(s, "snapshot"):
		return "snapshot"
	case strings.HasPrefix(s, "prerelease"):
		return "prerelease"
	}
	return ""
}

// compareVersions 判断 latest 是否比 current 新。
func compareVersions(current, latest string) bool {
	// 非稳定通道不参与版本比较 —— 它们不是"旧版本",而是另一条线上的构建。
	if nonStableChannel(current) != "" {
		return false
	}
	c := parseVersion(current)
	l := parseVersion(latest)

	// latest 解析不出来就无从比较,不提示更新。
	if len(l) == 0 {
		return false
	}
	// current 解析不出来(常见于构建时没有注入版本号,值为 "unknown"),
	// 这时按"可能有更新"处理 —— 用户至少应该看到有个新版本存在。
	if len(c) == 0 {
		return true
	}

	for i := 0; i < len(c) || i < len(l); i++ {
		var cv, lv int
		if i < len(c) {
			cv = c[i]
		}
		if i < len(l) {
			lv = l[i]
		}
		if lv != cv {
			return lv > cv
		}
	}
	return false
}

// parseVersion 把版本号拆成整数段。容忍 "v" 前缀和预发布后缀。
//
// "v0.1.25"          -> [0,1,25]
// "1.5.1"            -> [1,5,1]
// "Snapshot-260920"  -> []  (无法比较,调用方按"不更新"处理)
func parseVersion(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" || strings.HasPrefix(strings.ToLower(v), "snapshot") {
		return nil
	}
	// 去掉预发布后缀(如 1.2.3-rc1)与构建元数据
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// backupDir 返回备份目录。固定值,不可由请求指定。
//
// 必须和二进制在同一目录树内 —— 替换用的是 rename,跨文件系统会失败。
// 所以这里以二进制路径为准,而不是工作目录。拿不到二进制路径就返回错误,
// 不能退化成相对路径(那样会碰巧因为 WorkingDirectory 而"看起来能用")。
func backupDir() (string, error) {
	p, err := binaryPath()
	if err != nil {
		return "", fmt.Errorf("cannot determine the backup directory: %w", err)
	}
	return filepath.Join(filepath.Dir(p), "backup"), nil
}
