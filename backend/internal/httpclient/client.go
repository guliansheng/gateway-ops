// Package httpclient provides bounded HTTP clients for non-relay integrations.
package httpclient

import "github.com/go-resty/resty/v2"

// MaxResponseBodyBytes limits the decompressed response body, including errors.
// It is deliberately generous for connector group lists while bounding memory
// consumed by malformed or compromised integration endpoints.
const MaxResponseBodyBytes = 16 << 20

// New creates a client that never follows redirects. Integration requests can
// carry credentials in arbitrary headers or JSON bodies; net/http's standard
// sensitive-header filtering does not protect either custom headers or replayed
// POST bodies. Redirects must fail rather than turn a 3xx into apparent success.
func New() *resty.Client {
	return resty.New().
		SetRedirectPolicy(resty.NoRedirectPolicy()).
		SetResponseBodyLimit(MaxResponseBodyBytes)
}
