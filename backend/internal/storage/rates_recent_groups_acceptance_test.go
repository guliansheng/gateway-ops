package storage

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

func TestRatesAcceptanceRecentGroupsRangeAndFirstSeen(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&Channel{}, &RateSnapshot{}, &RateChangeLog{}); err != nil {
		t.Fatal(err)
	}
	rates := NewRates(db)
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	live := Channel{Name: "recent-groups-live", Type: ChannelTypeNewAPI}
	deleted := Channel{Name: "recent-groups-deleted", Type: ChannelTypeNewAPI}
	for _, channel := range []*Channel{&live, &deleted} {
		if err := db.Create(channel).Error; err != nil {
			t.Fatal(err)
		}
	}
	old := RateSnapshot{ChannelID: live.ID, ModelName: "old", Ratio: 1, LastSeenAt: since.Add(-time.Second)}
	boundary := RateSnapshot{ChannelID: live.ID, ModelName: "boundary", Ratio: 2, LastSeenAt: since}
	recent := RateSnapshot{ChannelID: live.ID, ModelName: "recent", Ratio: 2, LastSeenAt: since.Add(time.Minute)}
	deletedGroup := RateSnapshot{ChannelID: deleted.ID, ModelName: "deleted-channel", Ratio: 1, LastSeenAt: since.Add(2 * time.Minute)}
	removed := RateSnapshot{ChannelID: live.ID, ModelName: "removed", Ratio: 1, LastSeenAt: since.Add(3 * time.Minute)}
	for _, snapshot := range []*RateSnapshot{&old, &boundary, &recent, &deletedGroup, &removed} {
		if _, err := rates.Upsert(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := rates.ListChangesSince(time.Time{}, 50)
	if err != nil || len(changes) != 0 {
		t.Fatalf("creating groups generated changes: %+v, err = %v", changes, err)
	}
	// A direct/legacy soft deletion leaves snapshots behind, unlike Channels.Delete.
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	var retained int64
	if err := db.Model(&RateSnapshot{}).Where("channel_id = ?", deleted.ID).Count(&retained).Error; err != nil || retained != 1 {
		t.Fatalf("soft-delete fixture lost its snapshot: count = %d, err = %v", retained, err)
	}
	if err := rates.Delete(removed.ID, live.ID); err != nil {
		t.Fatal(err)
	}
	// Later ratio observations must not turn an older group into a new group,
	// nor move a recent group ahead using last_seen_at instead of first_seen_at.
	for _, snapshot := range []RateSnapshot{
		{ChannelID: live.ID, ModelName: "old", Ratio: 9, FirstSeenAt: since.Add(4 * time.Hour), LastSeenAt: since.Add(4 * time.Hour)},
		{ChannelID: live.ID, ModelName: "boundary", Ratio: 3, FirstSeenAt: since.Add(3 * time.Hour), LastSeenAt: since.Add(3 * time.Hour)},
	} {
		if _, err := rates.Upsert(&snapshot); err != nil {
			t.Fatal(err)
		}
	}
	storedOld, err := rates.FindByID(old.ID, live.ID)
	if err != nil || !storedOld.FirstSeenAt.Equal(old.FirstSeenAt) {
		t.Fatalf("ratio update reset first discovery: %+v, err = %v", storedOld, err)
	}
	groups, err := rates.ListRecentGroupsSince(since, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].ID != recent.ID || groups[1].ID != boundary.ID {
		t.Fatalf("recent groups = %+v, want recent then boundary only", groups)
	}
	if groups[1].Ratio != 3 || !groups[1].FirstSeenAt.Equal(since) || !groups[1].LastSeenAt.Equal(since.Add(3*time.Hour)) {
		t.Fatalf("updated group lost its original discovery/current ratio: %+v", groups[1])
	}
	all, err := rates.ListRecentGroupsSince(time.Time{}, 50)
	if err != nil || len(all) != 3 || all[2].ID != old.ID {
		t.Fatalf("all-time groups = %+v, err = %v; want all 3 live snapshots", all, err)
	}
	empty, err := rates.ListRecentGroupsSince(since.Add(5*time.Hour), 50)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("out-of-range groups = %+v, err = %v; want non-nil empty slice", empty, err)
	}
}

func TestRatesAcceptanceRecentGroupsLimitAndStableOrder(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&Channel{}, &RateSnapshot{}); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	live := Channel{Name: "recent-limit-live", Type: ChannelTypeNewAPI}
	deleted := Channel{Name: "recent-limit-deleted", Type: ChannelTypeNewAPI}
	for _, channel := range []*Channel{&live, &deleted} {
		if err := db.Create(channel).Error; err != nil {
			t.Fatal(err)
		}
	}
	want := make([]RateSnapshot, 60)
	for i := range want {
		// Deliberately scramble discovery time relative to insertion order.
		at := since.Add(time.Duration((i*7)%11) * time.Minute)
		want[i] = RateSnapshot{ChannelID: live.ID, ModelName: fmt.Sprintf("group-%02d", i), Ratio: 1, FirstSeenAt: at, LastSeenAt: at}
	}
	if err := db.Create(&want).Error; err != nil {
		t.Fatal(err)
	}
	// More recent rows from a deleted channel cannot occupy any of the 50 slots.
	for i := 0; i < 55; i++ {
		snapshot := RateSnapshot{ChannelID: deleted.ID, ModelName: fmt.Sprintf("deleted-%02d", i), Ratio: 1, FirstSeenAt: since.Add(time.Hour), LastSeenAt: since.Add(time.Hour)}
		if err := db.Create(&snapshot).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].FirstSeenAt.Equal(want[j].FirstSeenAt) {
			return want[i].ID > want[j].ID
		}
		return want[i].FirstSeenAt.After(want[j].FirstSeenAt)
	})
	rates := NewRates(db)
	for _, limit := range []int{50, 0, -1, 7} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			count := limit
			if count <= 0 {
				count = 50
			}
			groups, err := rates.ListRecentGroupsSince(since, limit)
			if err != nil || len(groups) != count {
				t.Fatalf("recent group count = %d, err = %v; want %d", len(groups), err, count)
			}
			for i := range groups {
				if groups[i].ID != want[i].ID {
					t.Fatalf("group at %d = %+v, want %+v", i, groups[i], want[i])
				}
			}
		})
	}
}

func TestRatesAcceptanceRecentChangesLimitAndStableOrder(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&RateChangeLog{}); err != nil {
		t.Fatal(err)
	}
	rates := NewRates(db)
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	oldRatio, oldCompletion := 1.0, 1.0
	want := make([]RateChangeLog, 60)
	for i := range want {
		want[i] = RateChangeLog{ChannelID: 1, ModelName: fmt.Sprintf("changed-%02d", i), OldRatio: &oldRatio, NewRatio: 2, ChangedAt: since.Add(time.Duration((i*7)%11) * time.Minute)}
	}
	if err := db.Create(&want).Error; err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []RateChangeLog{
		{ChannelID: 1, ModelName: "before-range", OldRatio: &oldRatio, NewRatio: 2, ChangedAt: since.Add(-time.Second)},
		{ChannelID: 1, ModelName: "legacy-null-old-ratio", NewRatio: 2, ChangedAt: since.Add(time.Hour)},
		{ChannelID: 1, ModelName: "completion-only", OldRatio: &oldRatio, NewRatio: 1, OldCompletionRatio: &oldCompletion, NewCompletionRatio: 2, ChangedAt: since.Add(time.Hour)},
	} {
		if err := db.Create(&excluded).Error; err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].ChangedAt.Equal(want[j].ChangedAt) {
			return want[i].ID > want[j].ID
		}
		return want[i].ChangedAt.After(want[j].ChangedAt)
	})
	changes, err := rates.ListChangesSince(since, 50)
	if err != nil || len(changes) != 50 {
		t.Fatalf("recent change count = %d, err = %v; want 50", len(changes), err)
	}
	for i := range changes {
		if changes[i].ID != want[i].ID {
			t.Fatalf("change at %d = %+v, want %+v", i, changes[i], want[i])
		}
	}
	count, err := rates.CountChangesSince(since)
	if err != nil || count != 60 {
		t.Fatalf("change count = %d, err = %v; want all 60 qualifying changes", count, err)
	}
}
