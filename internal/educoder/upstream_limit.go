package educoder

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
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
	// accounts holds one slot pool per signed-in Educoder account, so every
	// session of that account (a second browser, or a re-login while a job
	// still runs on the old session) shares the same per-account bound.
	accounts map[string]*slotPool
}{account: DefaultAccountUpstreamConcurrency, global: make(chan struct{}, DefaultGlobalUpstreamConcurrency), accounts: map[string]*slotPool{}}

type slotPool struct{ slots chan struct{} }

func newSlotPool(size int) *slotPool { return &slotPool{slots: make(chan struct{}, size)} }

// sharedAccountPool returns the process-wide pool of one account.
func sharedAccountPool(key string) *slotPool {
	upstreamLimits.Lock()
	defer upstreamLimits.Unlock()
	pool, ok := upstreamLimits.accounts[key]
	if !ok {
		pool = newSlotPool(upstreamLimits.account)
		upstreamLimits.accounts[key] = pool
	}
	return pool
}

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
	upstreamLimits.accounts = map[string]*slotPool{}
	return nil
}

func newAccountTransport(base http.RoundTripper) *accountTransport {
	upstreamLimits.Lock()
	defer upstreamLimits.Unlock()
	t := &accountTransport{base: base, global: upstreamLimits.global}
	// Until login identifies the account, the session has a pool of its own.
	t.account.Store(newSlotPool(upstreamLimits.account))
	return t
}

// accountTransport bounds in-flight upstream requests for one account and for
// the whole process. Waiting honours the request context.
type accountTransport struct {
	base    http.RoundTripper
	account atomic.Pointer[slotPool]
	global  chan struct{}
}

// bindAccount moves later requests to the account's shared pool. A request
// already in flight returns its slot to the pool it took it from.
func (t *accountTransport) bindAccount(key string) {
	t.account.Store(sharedAccountPool(key))
}

func (t *accountTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	account := t.account.Load().slots
	select {
	case account <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-account }()
	select {
	case t.global <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.global }()
	return t.base.RoundTrip(req)
}
