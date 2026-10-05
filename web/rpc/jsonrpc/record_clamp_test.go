package jsonrpc

import (
	"testing"
	"time"
)

// getRecords 走 Allow("common:*", RoleGuest),访客可直接调用,而 hours/start/end
// 与 maxCount 此前都没有上限。
func TestClampRecordQuery_ClampsWindow(t *testing.T) {
	end := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	start := end.Add(-10 * 365 * 24 * time.Hour) // 10 年,远超上限

	gotStart, gotEnd, _ := clampRecordQuery(start, end, 100)

	if !gotEnd.Equal(end) {
		t.Fatalf("end must not change: got %v want %v", gotEnd, end)
	}
	if span := gotEnd.Sub(gotStart); span != maxQueryWindow {
		t.Fatalf("window should be clamped to %v, got %v", maxQueryWindow, span)
	}
}

func TestClampRecordQuery_KeepsWindowWithinLimit(t *testing.T) {
	end := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)

	gotStart, gotEnd, _ := clampRecordQuery(start, end, 100)

	if !gotStart.Equal(start) || !gotEnd.Equal(end) {
		t.Fatalf("a window within the limit must pass through unchanged")
	}
}

// -1 表示"不限制",此前会配合 uuid 为空(全部节点)把整张表读进内存。
func TestClampRecordQuery_UnlimitedMaxCountIsCapped(t *testing.T) {
	end := time.Now().UTC()
	start := end.Add(-time.Hour)

	_, _, got := clampRecordQuery(start, end, -1)
	if got != maxRecordsPerQuery {
		t.Fatalf("maxCount=-1 should clamp to %d, got %d", maxRecordsPerQuery, got)
	}
}

func TestClampRecordQuery_OversizedMaxCountIsCapped(t *testing.T) {
	end := time.Now().UTC()
	start := end.Add(-time.Hour)

	_, _, got := clampRecordQuery(start, end, 10_000_000)
	if got != maxRecordsPerQuery {
		t.Fatalf("oversized maxCount should clamp to %d, got %d", maxRecordsPerQuery, got)
	}
}

// 0 表示"未指定",必须保留为 0,由下游替换成默认值 4000。
func TestClampRecordQuery_ZeroMaxCountStaysZero(t *testing.T) {
	end := time.Now().UTC()
	start := end.Add(-time.Hour)

	_, _, got := clampRecordQuery(start, end, 0)
	if got != 0 {
		t.Fatalf("maxCount=0 must stay 0 so the caller applies its default, got %d", got)
	}
}

func TestClampRecordQuery_NormalMaxCountUntouched(t *testing.T) {
	end := time.Now().UTC()
	start := end.Add(-time.Hour)

	_, _, got := clampRecordQuery(start, end, 500)
	if got != 500 {
		t.Fatalf("a reasonable maxCount must pass through, got %d", got)
	}
}

// public.metric 的 hours 是 float,同样没有上限。
func TestMetricQueryHours_ClampsHugeValue(t *testing.T) {
	got := metricQueryHours(1e9) // 约 11 万年
	if got != maxQueryWindow {
		t.Fatalf("huge hours should clamp to %v, got %v", maxQueryWindow, got)
	}
}

func TestMetricQueryHours_DefaultAndNormal(t *testing.T) {
	if got := metricQueryHours(0); got != 4*time.Hour {
		t.Fatalf("hours<=0 should default to 4h, got %v", got)
	}
	if got := metricQueryHours(2); got != 2*time.Hour {
		t.Fatalf("hours=2 should be 2h, got %v", got)
	}
}

// max_points 此前只校验了必须为正整数,没有上界。
func TestResolveMetricMaxPoints_CapsOversized(t *testing.T) {
	got, err := resolveMetricMaxPoints("cpu", publicMetricQueryParams{MaxPoints: 1_000_000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != maxMetricQueryPoints {
		t.Fatalf("want cap %d, got %d", maxMetricQueryPoints, got)
	}
}

func TestResolveMetricMaxPoints_DefaultWhenUnset(t *testing.T) {
	got, err := resolveMetricMaxPoints("cpu", publicMetricQueryParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != defaultMetricQueryPoints {
		t.Fatalf("want default %d, got %d", defaultMetricQueryPoints, got)
	}
}

// 负数仍按原有行为报错(上限钳制不改变这一语义)。
func TestResolveMetricMaxPoints_StillRejectsNegative(t *testing.T) {
	if _, err := resolveMetricMaxPoints("cpu", publicMetricQueryParams{MaxPoints: -5}); err == nil {
		t.Fatal("negative max_points must still be rejected")
	}
}

// public.metric 的 start/end 是显式 time.Time,不经过 hours 换算。
// 曾经的漏洞:只钳了 metricQueryHours,显式起止直接透传,
// 访客传 start=1970-01-01&end=9999-12-31 就能拿到无上限窗口。
func TestClampQueryWindow_ClampsExplicitRange(t *testing.T) {
	end := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	start := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	gotStart, gotEnd := clampQueryWindow(start, end)

	if !gotEnd.Equal(end) {
		t.Fatalf("end must not change: got %v want %v", gotEnd, end)
	}
	if span := gotEnd.Sub(gotStart); span != maxQueryWindow {
		t.Fatalf("window should be clamped to %v, got %v", maxQueryWindow, span)
	}
}

func TestClampQueryWindow_KeepsRangeWithinLimit(t *testing.T) {
	end := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	start := end.Add(-7 * 24 * time.Hour)

	gotStart, gotEnd := clampQueryWindow(start, end)

	if !gotStart.Equal(start) || !gotEnd.Equal(end) {
		t.Fatal("a window within the limit must pass through unchanged")
	}
}

// 反向窗口(start 晚于 end)退化为零宽,而不是保留一个负区间。
func TestClampQueryWindow_ReversedRangeCollapses(t *testing.T) {
	end := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	start := end.Add(48 * time.Hour)

	gotStart, gotEnd := clampQueryWindow(start, end)

	if !gotStart.Equal(gotEnd) {
		t.Fatalf("reversed range should collapse to zero width, got %v..%v", gotStart, gotEnd)
	}
}

// clampRecordQuery 与 clampQueryWindow 必须给出相同的窗口结论,
// 否则两个公开入口的收紧程度会不一致。
func TestClampRecordQuery_UsesSameWindowAsClampQueryWindow(t *testing.T) {
	end := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	start := end.Add(-10 * 365 * 24 * time.Hour)

	wStart, wEnd := clampQueryWindow(start, end)
	rStart, rEnd, _ := clampRecordQuery(start, end, 100)

	if !wStart.Equal(rStart) || !wEnd.Equal(rEnd) {
		t.Fatalf("window clamp mismatch: window=%v..%v record=%v..%v", wStart, wEnd, rStart, rEnd)
	}
}
