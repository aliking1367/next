package api

import (
	"testing"
	"time"
)

func TestGregorianToJalali(t *testing.T) {
	for _, test := range []struct{ gy, gm, gd, jy, jm, jd int }{
		{2026, 11, 16, 1405, 8, 25},
		{2026, 3, 21, 1405, 1, 1},
		{2025, 3, 20, 1403, 12, 30},
		{2024, 9, 22, 1403, 7, 1},
	} {
		jy, jm, jd := gregorianToJalali(test.gy, test.gm, test.gd)
		if jy != test.jy || jm != test.jm || jd != test.jd {
			t.Fatalf("%d-%d-%d = %d/%d/%d, want %d/%d/%d", test.gy, test.gm, test.gd, jy, jm, jd, test.jy, test.jm, test.jd)
		}
	}
}

func TestTunnelUsageBucketsFollowIranTime(t *testing.T) {
	// 2026-11-16 20:45 UTC is 00:15 on Tuesday the 17th in Tehran.
	at := time.Date(2026, 11, 16, 20, 45, 0, 0, time.UTC)
	if got := tunnelUsageBucketStart("hour", at, true).UTC(); !got.Equal(time.Date(2026, 11, 16, 20, 30, 0, 0, time.UTC)) {
		t.Fatalf("hour = %v", got)
	}
	day := tunnelUsageBucketStart("day", at, true)
	if local := day.In(tunnelUsageZone); local.Day() != 17 || local.Hour() != 0 {
		t.Fatalf("day = %v", local)
	}
	week := tunnelUsageBucketStart("week", at, true).In(tunnelUsageZone)
	if week.Weekday() != time.Saturday || week.Day() != 14 {
		t.Fatalf("week = %v", week)
	}
	month := tunnelUsageBucketStart("month", at, true)
	if label := tunnelUsageLabel("month", month, true); label != "1405/08" {
		t.Fatalf("jalali month = %q", label)
	}
	if label := tunnelUsageLabel("day", month, true); label != "1405/08/01" {
		t.Fatalf("jalali month start = %q", label)
	}
	starts := tunnelUsageSeriesStarts("month", 3, at, true)
	if got := tunnelUsageLabel("month", starts[0], true); got != "1405/06" {
		t.Fatalf("first of three months = %q", got)
	}
}

func TestSummarizeTunnelUsagePeaksAndTotals(t *testing.T) {
	now := time.Date(2026, 11, 16, 18, 10, 0, 0, time.UTC) // 21:40 in Tehran
	var rows []tunnelUsageRow
	// Two days of history: 1 GB in Tehran's 21:00 hour, 100 MB at 03:00.
	for _, day := range []int{14, 15} {
		evening := time.Date(2026, 11, day, 17, 30, 0, 0, time.UTC) // 21:00 Tehran
		rows = append(rows,
			tunnelUsageRow{bucket: evening, tag: "g", up: 100e6, down: 400e6},
			tunnelUsageRow{bucket: evening.Add(30 * time.Minute), tag: "g", up: 100e6, down: 400e6},
			tunnelUsageRow{bucket: time.Date(2026, 11, day, 23, 30, 0, 0, time.UTC), tag: "g", down: 100e6},
		)
	}
	rows = append(rows, tunnelUsageRow{bucket: time.Date(2026, 11, 16, 17, 30, 0, 0, time.UTC), tag: "g", down: 50e6})
	starts := tunnelUsageSeriesStarts("day", 3, now, true)
	summary := summarizeTunnelUsage(rows, "day", starts, now, true)

	if len(summary.PeakHours) == 0 || summary.PeakHours[0].Hour != 21 {
		t.Fatalf("peak hours = %#v", summary.PeakHours)
	}
	if summary.Busiest == nil || summary.Busiest.Bytes != 1e9 {
		t.Fatalf("busiest = %#v", summary.Busiest)
	}
	// Today in Tehran holds the 16th's 03:00 (23:30 UTC on the 15th) and 21:00.
	if summary.Today.Down != 150e6 {
		t.Fatalf("today = %#v", summary.Today)
	}
	if summary.Last30d.Up+summary.Last30d.Down != 2*1e9+2*100e6+50e6 {
		t.Fatalf("30d = %#v", summary.Last30d)
	}
	var seriesTotal int64
	for _, point := range summary.Series {
		seriesTotal += point.Up + point.Down
	}
	if seriesTotal != summary.Last30d.Up+summary.Last30d.Down {
		t.Fatalf("series total %d != %d", seriesTotal, summary.Last30d.Up+summary.Last30d.Down)
	}
	if len(summary.Series) != 3 || summary.Series[2].Label != "1405/08/25" {
		t.Fatalf("series = %#v", summary.Series)
	}
}
