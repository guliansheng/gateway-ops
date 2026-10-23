package storage

import (
	"testing"
	"time"
)

func TestCombinedDegradationSchedule(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var pelican, candy *time.Time
	pelicans, candies := 0, 0
	for minute := 0; minute <= 60; minute += 10 {
		now := start.Add(time.Duration(minute) * time.Minute)
		method, skip := selectDegradationMethod("both", pelican, candy, now)
		if method == "pelican" {
			pelicans++
			pelican = advanceDegradationRun(pelican, now, 60)
			if skip {
				candy = advanceDegradationRun(candy, now, 10)
			}
		} else {
			candies++
			candy = advanceDegradationRun(candy, now, 10)
		}
		if minute == 60 && (!skip || method != "pelican" || !candy.Equal(start.Add(70*time.Minute))) {
			t.Fatalf("collision method=%s skip=%v candy=%v", method, skip, candy)
		}
	}
	if pelicans != 2 || candies != 5 {
		t.Fatalf("pelican=%d candy=%d", pelicans, candies)
	}
}

func TestCombinedDegradationLegacyAndClockIndependence(t *testing.T) {
	for _, tc := range []struct {
		method                   string
		interval, pelican, candy int
	}{{"pelican", 30, 30, 10}, {"candy", 15, 60, 15}} {
		p, c := DegradationIntervals(GroupDegradationConfig{Method: tc.method, IntervalMinutes: tc.interval})
		if p != tc.pelican || c != tc.candy {
			t.Fatalf("legacy %s => %d/%d", tc.method, p, c)
		}
	}
	now := time.Now().Truncate(time.Minute)
	p, c := now.Add(time.Hour), now.Add(10*time.Minute)
	config := GroupDegradationConfig{PelicanNextRunAt: &p, CandyNextRunAt: &c, LastRunAt: &now}
	gotP, gotC := degradationSchedule(config)
	if !gotP.Equal(p) || !gotC.Equal(c) {
		t.Fatal("last run overwrote method schedules")
	}
	late := now.Add(25 * time.Minute)
	if got := advanceDegradationRun(&c, late, 10); !got.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("late tick drifted: %v", got)
	}
	p, c = now.Add(30*time.Second), now
	method, skip := selectDegradationMethod("both", &p, &c, now)
	if method != "pelican" || !skip {
		t.Fatal("same-minute collision not skipped")
	}
}

func TestDegradationMethodSelectionGate(t *testing.T) {
	now := time.Now().Truncate(time.Minute)
	pelican, candy := now.Add(time.Hour), now.Add(2*time.Hour)
	if method, _ := selectDegradationMethod("pelican", nil, &candy, now); method != "pelican" {
		t.Fatalf("pelican-only due=%q", method)
	}
	if method, _ := selectDegradationMethod("candy", &pelican, nil, now); method != "candy" {
		t.Fatalf("candy-only due=%q", method)
	}
	if next := earliestSelectedDegradationRun("pelican", &pelican, nil); next == nil || !next.Equal(pelican) {
		t.Fatalf("pelican-only next=%v", next)
	}
	if next := earliestSelectedDegradationRun("candy", nil, &candy); next == nil || !next.Equal(candy) {
		t.Fatalf("candy-only next=%v", next)
	}
}

// A manual 「立即检测」 must not borrow the next automatic slot: only a due slot
// rolls forward, a pending one stays where it is.
func TestDegradationEarlyClaimKeepsPendingSlot(t *testing.T) {
	now := time.Now().Truncate(time.Minute)
	pending := now.Add(56 * time.Minute)
	if got := advanceDegradationRun(&pending, now, 60); got == nil || !got.Equal(pending) {
		t.Fatalf("early claim consumed pending slot: %v", got)
	}
	due := now.Add(-4 * time.Minute)
	if got := advanceDegradationRun(&due, now, 60); got == nil || !got.Equal(due.Add(time.Hour)) {
		t.Fatalf("due slot did not advance: %v", got)
	}
}
