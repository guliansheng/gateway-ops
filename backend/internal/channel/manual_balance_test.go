package channel

import (
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestResetManualBalanceClearsUsageBaseline(t *testing.T) {
	previousBalance, previousBaseline := 7.5, 2.5
	channel := &storage.Channel{ManualBalance: 12.34, LastBalance: &previousBalance, ManualUsageBaseline: &previousBaseline, ManualUsageBasis: "old-basis"}
	at := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	resetManualBalance(channel, at)
	if channel.LastBalance == nil || *channel.LastBalance != 12.34 || channel.LastBalanceAt == nil || !channel.LastBalanceAt.Equal(at) || channel.ManualUsageBaseline != nil || channel.ManualUsageBasis != "" {
		t.Fatalf("manual reset did not start a fresh period: %#v", channel)
	}
}
