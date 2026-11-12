package relay

import "testing"

func TestNormalizeBaseURLRejectsUserinfo(t *testing.T) {
	for _, raw := range []string{
		"http://user@example.invalid/base",
	} {
		t.Run(raw, func(t *testing.T) {
			if normalized, err := normalizeBaseURL(raw); err == nil || normalized != "" {
				t.Fatalf("normalizeBaseURL(%q) = %q, %v; want a rejection", raw, normalized, err)
			}
		})
	}

	got, err := normalizeBaseURL("https://example.invalid/base/")
	if err != nil || got != "https://example.invalid/base" {
		t.Fatalf("normalizeBaseURL without userinfo = %q, %v", got, err)
	}
}
