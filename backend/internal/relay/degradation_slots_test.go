package relay

import "testing"

// A due-group cron batch holds its slots for minutes. It must never consume the
// pool a manual test needs, otherwise the user gets "busy" for an unrelated
// account while nothing they started is actually running.
func TestDegradationSlotPoolsAreIndependentAndBounded(t *testing.T) {
	service := &Service{}

	scheduled := exhaust(t, "scheduled", service.acquireScheduledDegradationSlot)
	if len(scheduled) != len(service.scheduledDegradationSlots) {
		t.Fatalf("scheduled pool = %d slots, want %d", len(scheduled), len(service.scheduledDegradationSlots))
	}
	if len(scheduled) != 2 {
		t.Fatalf("scheduled pool = %d slots, want 2", len(scheduled))
	}
	if release, ok := service.acquireScheduledDegradationSlot(); ok {
		release()
		t.Fatal("scheduled pool accepted a third cron batch")
	}

	manual := exhaust(t, "manual", service.acquireGroupDegradationSlot)
	if len(manual) != len(service.manualDegradationSlots) {
		t.Fatalf("manual pool = %d slots, want %d", len(manual), len(service.manualDegradationSlots))
	}
	if len(manual) != 3 {
		t.Fatalf("manual pool = %d slots, want 3", len(manual))
	}
	if release, ok := service.acquireGroupDegradationSlot(); ok {
		release()
		t.Fatal("manual pool accepted a fourth run")
	}

	// Releasing one pool never frees the other one's slots.
	for _, release := range scheduled {
		release()
	}
	if release, ok := service.acquireScheduledDegradationSlot(); !ok {
		t.Fatal("scheduled slot stayed locked after release")
	} else {
		release()
	}
	if release, ok := service.acquireGroupDegradationSlot(); ok {
		release()
		t.Fatal("releasing scheduled slots freed a manual slot")
	}

	for _, release := range manual {
		release()
	}
	if release, ok := service.acquireGroupDegradationSlot(); !ok {
		t.Fatal("manual slot stayed locked after release")
	} else {
		release()
	}
}

func exhaust(t *testing.T, name string, acquire func() (func(), bool)) []func() {
	t.Helper()
	releases := []func(){}
	for {
		release, ok := acquire()
		if !ok {
			return releases
		}
		releases = append(releases, release)
	}
}
