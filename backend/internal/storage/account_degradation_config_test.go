package storage

import "testing"

func TestAccountDegradationConfigValidation(t *testing.T) {
	config := DefaultAccountDegradationConfig()
	config.Enabled = true
	config.Model = "candidate"
	config.Method = "both"
	config.HistoryLimit = 25
	config.DisableOnDegraded = true
	got, err := NormalizeAccountDegradationConfig(config)
	if err != nil || got.Method != "both" || got.HistoryLimit != 25 || !got.DisableOnDegraded {
		t.Fatalf("config=%+v err=%v", got, err)
	}
	config = DefaultAccountDegradationConfig()
	config.Enabled = true
	if _, err := NormalizeAccountDegradationConfig(config); err == nil {
		t.Fatal("accepted enabled config without model")
	}
	config = DefaultAccountDegradationConfig()
	config.HistoryLimit = 1001
	if _, err := NormalizeAccountDegradationConfig(config); err == nil {
		t.Fatal("accepted oversized history")
	}
}
