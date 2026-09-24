package relay

import (
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestLimitLatencySamplesPerModelKeepsSixtyRecentCallsForEachModel(t *testing.T) {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	samples := make([]storage.RelayLatencySample, 0, 130)
	for i := 0; i < 100; i++ {
		samples = append(samples, storage.RelayLatencySample{Model: "model-a", CreatedAt: base.Add(time.Duration(i) * time.Minute)})
	}
	for i := 0; i < 30; i++ {
		samples = append(samples, storage.RelayLatencySample{Model: "model-b", CreatedAt: base.Add(time.Duration(200+i) * time.Minute)})
	}

	got := limitLatencySamplesPerModel(samples, 60)
	counts := map[string]int{}
	oldest := map[string]time.Time{}
	for _, sample := range got {
		counts[sample.Model]++
		if previous, ok := oldest[sample.Model]; !ok || sample.CreatedAt.Before(previous) {
			oldest[sample.Model] = sample.CreatedAt
		}
	}
	if counts["model-a"] != 60 || counts["model-b"] != 30 {
		t.Fatalf("per-model counts = %#v", counts)
	}
	if want := base.Add(40 * time.Minute); !oldest["model-a"].Equal(want) {
		t.Fatalf("model-a oldest sample = %s, want %s", oldest["model-a"], want)
	}
}
