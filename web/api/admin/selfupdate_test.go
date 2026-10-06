package admin

import (
	"runtime"
	"strings"
	"testing"

	"github.com/aomtest/komari-slim-server/utils"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"v0.1.25", []int{0, 1, 25}},
		{"0.1.25", []int{0, 1, 25}},
		{"1.5.1", []int{1, 5, 1}},
		{"V2.3.18", []int{2, 3, 18}},
		{"v1.0.2-rc1", []int{1, 0, 2}},
		{"v1.0.2+build.7", []int{1, 0, 2}},
		{"", nil},
		{"unknown", nil},
		{"Snapshot-2609200737", nil},
		{"v1.x", nil},
	}
	for _, c := range cases {
		got := parseVersion(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parseVersion(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseVersion(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.25", "v0.1.26", true},
		{"v0.1.25", "v0.1.25", false},
		{"v0.1.26", "v0.1.25", false},
		{"0.1.25", "v0.1.26", true},   // 容忍 v 前缀不一致
		{"v0.1.25", "v0.2.0", true},
		{"v0.1.25", "v1.0.0", true},
		{"v1.0.2", "v1.0.10", true},   // 不能按字符串比较
		{"v1.0.10", "v1.0.2", false},
		// 无法解析的版本(如快照)一律按"不更新"处理,避免误报。
		{"unknown", "v0.1.26", true},
		{"Snapshot-2609200737", "v0.1.26", false},
		{"prerelease-a1b2c3d", "v0.1.26", false},
		{"v0.1.25", "Snapshot-2609200737", false},
	}
	for _, c := range cases {
		if got := compareVersions(c.current, c.latest); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %v, want %v",
				c.current, c.latest, got, c.want)
		}
	}
}

func TestNonStableChannel(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"v0.1.25", ""},
		{"0.1.25", ""},
		{"unknown", ""},
		{"", ""},
		{"Snapshot-2609200737", "snapshot"},
		{"snapshot-abc", "snapshot"},
		{"SNAPSHOT-1", "snapshot"},
		{"prerelease-a1b2c3d", "prerelease"},
		{"Prerelease-ABC", "prerelease"},
	}
	for _, c := range cases {
		if got := nonStableChannel(c.version); got != c.want {
			t.Errorf("nonStableChannel(%q) = %q, want %q", c.version, got, c.want)
		}
	}
}

// 非稳定通道必须被明确拒绝,而不是"检测不到更新" —— 后者会让用户
// 看不出为什么没有更新按钮。
func TestCheckUpdatePreconditionsRejectsNonStableChannel(t *testing.T) {
	original := utils.CurrentVersion
	defer func() { utils.CurrentVersion = original }()

	for _, version := range []string{"Snapshot-2609200737", "prerelease-a1b2c3d"} {
		utils.CurrentVersion = version
		ok, reason := checkUpdatePreconditions()
		if ok {
			t.Errorf("version %q must not be updatable online", version)
		}
		if !strings.Contains(reason, "channel") {
			t.Errorf("version %q: the reason should name the channel, got %q", version, reason)
		}
	}
}

func TestCurrentAssetName(t *testing.T) {
	name, err := currentAssetName()
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		if err == nil {
			t.Fatalf("expected an error on unsupported platform %s", runtime.GOOS)
		}
		return
	}
	if err != nil {
		t.Fatalf("currentAssetName() error = %v", err)
	}
	// 命名规则必须与 release workflow 产出的资产名一致,否则更新时找不到文件。
	switch runtime.GOOS {
	case "linux":
		want := "komari-linux-" + runtime.GOARCH
		if name != want {
			t.Errorf("currentAssetName() = %q, want %q", name, want)
		}
	case "windows":
		want := "komari-windows-" + runtime.GOARCH + ".exe"
		if name != want {
			t.Errorf("currentAssetName() = %q, want %q", name, want)
		}
	}
}

func TestPickAssetRejectsMissingDigest(t *testing.T) {
	rel := &githubRelease{TagName: "v9.9.9"}
	rel.Assets = append(rel.Assets, struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	}{ID: 1, Name: mustAssetName(t), Size: 100, Digest: ""})

	// 没有 digest 就必须拒绝,不能降级成"下载了就装"。
	if _, err := pickAsset(rel); err == nil {
		t.Fatal("pickAsset must reject an asset without a digest")
	}
}

func TestPickAssetRejectsUnsupportedDigestFormat(t *testing.T) {
	rel := &githubRelease{TagName: "v9.9.9"}
	rel.Assets = append(rel.Assets, struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	}{ID: 1, Name: mustAssetName(t), Size: 100, Digest: "md5:abc"})

	if _, err := pickAsset(rel); err == nil {
		t.Fatal("pickAsset must reject a non-sha256 digest")
	}
}

func TestPickAssetAcceptsValidAsset(t *testing.T) {
	rel := &githubRelease{TagName: "v9.9.9"}
	rel.Assets = append(rel.Assets, struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	}{ID: 7, Name: mustAssetName(t), Size: 123, Digest: "sha256:deadbeef"})

	asset, err := pickAsset(rel)
	if err != nil {
		t.Fatalf("pickAsset() error = %v", err)
	}
	if asset.ID != 7 || asset.Size != 123 || asset.Digest != "sha256:deadbeef" {
		t.Errorf("pickAsset() = %+v, unexpected", asset)
	}
}

func TestCheckUpdatePreconditionsRejectsNonLinux(t *testing.T) {
	ok, reason := checkUpdatePreconditions()
	if runtime.GOOS != "linux" {
		// Windows / macOS 上必须明确拒绝,并且给出可读的原因。
		if ok {
			t.Fatalf("expected preconditions to fail on %s", runtime.GOOS)
		}
		if reason == "" {
			t.Fatal("a blocked platform must come with a reason")
		}
		t.Logf("blocked as expected: %s", reason)
	}
}

func TestNewUpdateTaskIDIsUnpredictable(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		id := newUpdateTaskID()
		if id == "" {
			t.Fatal("empty task id")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate task id: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestTaskManagerRejectsConcurrentTasks(t *testing.T) {
	m := &UpdateTaskManager{watchers: make(map[chan UpdateTask]struct{})}

	first, err := m.Begin("update", "v1", "v2")
	if err != nil {
		t.Fatalf("first Begin() error = %v", err)
	}
	if _, err := m.Begin("rollback", "v2", "v1"); err == nil {
		t.Fatal("a second task must be rejected while the first is running")
	}

	// 任务进入终态后应允许新的任务。
	m.Finish(first.ID, StageSucceeded, false)
	if _, err := m.Begin("rollback", "v2", "v1"); err != nil {
		t.Fatalf("Begin() after the previous task finished error = %v", err)
	}
}

func TestTaskIsTerminal(t *testing.T) {
	terminal := []string{StageSucceeded, StageFailed, StageRolledBack, StageUnknown}
	running := []string{StageQueued, StageDownloading, StageVerifying,
		StageBackingUp, StageReplacing, StageRestarting, StageHealthCheck}

	for _, s := range terminal {
		if !(&UpdateTask{Stage: s}).IsTerminal() {
			t.Errorf("stage %q should be terminal", s)
		}
	}
	for _, s := range running {
		if (&UpdateTask{Stage: s}).IsTerminal() {
			t.Errorf("stage %q should not be terminal", s)
		}
	}
}

func TestBackupNameMatchesPattern(t *testing.T) {
	// listBackups 依赖 "komari.bak." 前缀来识别自己的文件,命名规则不能变。
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	if got := listBackups(); len(got) != 0 {
		t.Fatalf("expected no backups in a fresh dir, got %v", got)
	}
}

func mustAssetName(t *testing.T) string {
	t.Helper()
	name, err := currentAssetName()
	if err != nil {
		t.Skipf("unsupported platform %s: %v", runtime.GOOS, err)
	}
	return name
}
