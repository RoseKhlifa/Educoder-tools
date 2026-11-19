package educoder

import (
	"errors"
	"net/http"
	"sync"
)

// Upstream request limits. Every account's traffic (jobs, collection and page
// reads) shares one per-account slot pool, and all accounts share the global
// pool, because every request leaves through the same egress address. Slots
// are held only until response headers arrive; long-lived WebSocket streams
// do not keep a slot after their upgrade.
const (
	DefaultAccountUpstreamConcurrency = 6
	DefaultGlobalUpstreamConcurrency  = 64
)

var upstreamLimits = struct {
	sync.Mutex
	account int
	global  chan struct{}
}{account: DefaultAccountUpstreamConcurrency, global: make(chan struct{}, DefaultGlobalUpstreamConcurrency)}

// SetUpstreamLimits configures the limits for clients created afterwards. It
// is meant for process start-up, before any session client exists.
func SetUpstreamLimits(account, global int) error {
	if account < 1 || account > 256 || global < account || global > 4096 {
		return errors.New("invalid upstream concurrency limits")
	}
	upstreamLimits.Lock()
	defer upstreamLimits.Unlock()
	upstreamLimits.account = account
	upstreamLimits.global = make(chan struct{}, global)
	return nil
}

func newAccountTransport(base http.RoundTripper) *accountTransport {
	upstreamLimits.Lock()
	defer upstreamLimits.Unlock()
	return &accountTransport{base: base, account: make(chan struct{}, upstreamLimits.account), global: upstreamLimits.global}
}

// accountTransport bounds in-flight upstream requests for one account and for
// the whole process. Waiting honours the request context.
type accountTransport struct {
	base    http.RoundTripper
	account chan struct{}
	global  chan struct{}
}

func (t *accountTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	select {
	case t.account <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.account }()
	select {
	case t.global <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.global }()
	return t.base.RoundTrip(req)
}
