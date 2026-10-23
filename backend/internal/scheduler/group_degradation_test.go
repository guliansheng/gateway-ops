package scheduler

import (
	"testing"

	"github.com/guliansheng/gateway-ops/internal/relay"
)

func TestGroupDegradationSchedulerNoServiceAndOverlap(t *testing.T) {
	var missing Scheduler
	missing.runGroupDegradation()
	if missing.groupDegradationRunning.Load() {
		t.Fatal("missing service reserved a running slot")
	}
	s := Scheduler{relay: &relay.Service{}}
	s.groupDegradationRunning.Store(true)
	s.runGroupDegradation()
	if !s.groupDegradationRunning.Load() {
		t.Fatal("overlapping invocation cleared the original job's running flag")
	}
	s.groupDegradationRunning.Store(false)
	s.runGroupDegradation()
	if s.groupDegradationRunning.Load() {
		t.Fatal("completed job retained the running flag")
	}
}
