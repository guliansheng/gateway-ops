package relay

import "testing"

// 缓存命中率 = 缓存读 /（输入 + 缓存读 + 缓存写）；成功率为 成功 /（成功 + 失败）。
func TestAccountTrafficRatesComputesBothMetrics(t *testing.T) {
	stats := remoteUsageStats{
		TotalRequests:            78,
		TotalInputTokens:         1187,
		TotalCacheReadTokens:     14592,
		TotalCacheCreationTokens: 0,
	}
	cacheHit, success := accountTrafficRates(stats, 8)
	if cacheHit == nil {
		t.Fatal("缓存命中率不应为空")
	}
	if got := *cacheHit; got < 0.924 || got > 0.926 {
		t.Fatalf("缓存命中率 = %v，期望约 0.925", got)
	}
	if success == nil {
		t.Fatal("成功率不应为空")
	}
	if got := *success; got < 0.906 || got > 0.908 {
		t.Fatalf("成功率 = %v，期望约 0.907", got)
	}
}

// 窗口内没有任何 token 或请求时两项都为空，前端显示占位而不是 0%。
func TestAccountTrafficRatesWithoutData(t *testing.T) {
	cacheHit, success := accountTrafficRates(remoteUsageStats{}, 0)
	if cacheHit != nil || success != nil {
		t.Fatalf("无数据时应都为空，实际 cacheHit=%v success=%v", cacheHit, success)
	}
}

// 只有失败、没有成功时成功率是 0%，缓存命中率仍为空。
func TestAccountTrafficRatesOnlyFailures(t *testing.T) {
	cacheHit, success := accountTrafficRates(remoteUsageStats{}, 5)
	if cacheHit != nil {
		t.Fatalf("无 token 时缓存命中率应为空，实际 %v", *cacheHit)
	}
	if success == nil || *success != 0 {
		t.Fatalf("成功率应为 0，实际 %v", success)
	}
}

// 缓存写计入分母：写入量越大命中率越低。
func TestAccountTrafficRatesCountsCacheCreation(t *testing.T) {
	stats := remoteUsageStats{
		TotalRequests:            10,
		TotalInputTokens:         0,
		TotalCacheReadTokens:     50,
		TotalCacheCreationTokens: 50,
	}
	cacheHit, _ := accountTrafficRates(stats, 0)
	if cacheHit == nil || *cacheHit != 0.5 {
		t.Fatalf("缓存命中率 = %v，期望 0.5", cacheHit)
	}
}

// 全部成功时成功率是 100%。
func TestAccountTrafficRatesAllSuccess(t *testing.T) {
	_, success := accountTrafficRates(remoteUsageStats{TotalRequests: 12}, 0)
	if success == nil || *success != 1 {
		t.Fatalf("成功率 = %v，期望 1", success)
	}
}
