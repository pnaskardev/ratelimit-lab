package middlewares

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/pnaskardev/ratelimit-lab/pkg/handlers"
	"github.com/pnaskardev/ratelimit-lab/pkg/utils"
	"github.com/redis/go-redis/v9"
)

const tokenBucketPath = "/api/token-bucket"

// The middleware calls time.Now() itself, so refill behaviour is driven by
// backdating last_refill_time in redis instead of sleeping out a real
// refillInterval. Injecting a clock (PLAN.md stage 1) would let these tests
// control time without reaching into the bucket's storage.
func newTokenBucketApp(t *testing.T) (*fiber.App, *redis.Client) {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr(),
		DB:       testRedisDB,
		Protocol: 2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		t.Skipf("redis unavailable at %s (%v) — start it with 'make redis-up'", redisAddr(), err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		client.Close()
		t.Fatalf("flush test db: %v", err)
	}

	t.Cleanup(func() {
		client.Close()
	})

	return newTokenBucketAppWithClient(client), client
}

func newTokenBucketAppWithClient(client *redis.Client) *fiber.App {
	app := fiber.New(fiber.Config{
		ProxyHeader:      fiber.HeaderXForwardedFor,
		TrustProxy:       true,
		TrustProxyConfig: fiber.TrustProxyConfig{Proxies: []string{testProxyIP}},
	})

	app.Get(tokenBucketPath, NewMiddlewares(client).TokenBucketMiddleware(), handlers.NewHandlers().DefaultHandler)

	return app
}

func get(t *testing.T, app *fiber.App, ip string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, tokenBucketPath, nil)
	req.Header.Set(fiber.HeaderXForwardedFor, ip)

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("request from %s: %v", ip, err)
	}
	return resp
}

func getStatus(t *testing.T, app *fiber.App, ip string) int {
	t.Helper()

	resp := get(t, app, ip)
	defer resp.Body.Close()
	return resp.StatusCode
}

// drainBucket spends every token in a fresh bucket, leaving it empty but not
// yet rejecting.
func drainBucket(t *testing.T, app *fiber.App, ip string) {
	t.Helper()

	for i := 1; i <= maxTokens; i++ {
		if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
			t.Fatalf("draining token %d/%d: got status %d, want %d", i, maxTokens, got, fiber.StatusAccepted)
		}
	}
}

func readBucket(t *testing.T, client *redis.Client, ip string) (tokens int64, lastRefill time.Time) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := utils.TokenBucketKey(ip)

	values, err := client.HMGet(ctx, key, "tokens", "last_refill_time").Result()
	if err != nil {
		t.Fatalf("read bucket %s: %v", key, err)
	}

	tokens, ok := hashInt64(values[0])
	if !ok {
		t.Fatalf("bucket %s has no usable tokens field: %#v", key, values[0])
	}
	seconds, ok := hashInt64(values[1])
	if !ok {
		t.Fatalf("bucket %s has no usable last_refill_time field: %#v", key, values[1])
	}

	return tokens, time.Unix(seconds, 0)
}

// backdateRefill rewinds a bucket's last_refill_time so the next request sees
// `age` worth of elapsed time.
func backdateRefill(t *testing.T, client *redis.Client, ip string, age time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := utils.TokenBucketKey(ip)
	if err := client.HSet(ctx, key, "last_refill_time", time.Now().Add(-age).Unix()).Err(); err != nil {
		t.Fatalf("backdate %s by %s: %v", key, age, err)
	}
}

// expectedRefillGrant is what an empty bucket receives after `intervals` whole
// refill intervals, capped at capacity.
func expectedRefillGrant(intervals int) int {
	return min(intervals*tokensPerRefill, maxTokens)
}

// Stage 3 deliverable from PLAN.md: a burst against an empty bucket admits
// exactly capacity, then rejects.
func TestTokenBucketAllowsBurstUpToCapacityThenRejects(t *testing.T) {
	app, _ := newTokenBucketApp(t)
	ip := uniqueIP()

	drainBucket(t, app, ip)

	for i := 1; i <= 3; i++ {
		if got := getStatus(t, app, ip); got != fiber.StatusTooManyRequests {
			t.Fatalf("request %d past capacity %d: got status %d, want %d", i, maxTokens, got, fiber.StatusTooManyRequests)
		}
	}
}

func TestTokenBucketAllowedRequestReachesHandler(t *testing.T) {
	app, _ := newTokenBucketApp(t)

	resp := get(t, app, uniqueIP())
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusAccepted {
		t.Fatalf("got status %d, want %d", resp.StatusCode, fiber.StatusAccepted)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	if payload["status"] != "ACCEPTED" {
		t.Errorf("got body %q, want status ACCEPTED", body)
	}
}

func TestTokenBucketSpendsOneTokenPerRequest(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	for spent := 1; spent <= maxTokens; spent++ {
		if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
			t.Fatalf("request %d: got status %d, want %d", spent, got, fiber.StatusAccepted)
		}

		tokens, _ := readBucket(t, client, ip)
		if want := int64(maxTokens - spent); tokens != want {
			t.Errorf("after %d requests: bucket holds %d tokens, want %d", spent, tokens, want)
		}
	}
}

func TestTokenBucketKeepsClientsIndependent(t *testing.T) {
	app, _ := newTokenBucketApp(t)
	noisy, quiet := uniqueIP(), uniqueIP()

	drainBucket(t, app, noisy)
	if got := getStatus(t, app, noisy); got != fiber.StatusTooManyRequests {
		t.Fatalf("exhausted client: got status %d, want %d", got, fiber.StatusTooManyRequests)
	}

	if got := getStatus(t, app, quiet); got != fiber.StatusAccepted {
		t.Errorf("second client: got status %d, want %d — buckets are leaking across clients", got, fiber.StatusAccepted)
	}
}

// A rejected request must not consume or reset anything, otherwise a client
// that keeps hammering pushes its own refill further away.
func TestTokenBucketRejectionLeavesStateUntouched(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	drainBucket(t, app, ip)

	// Backdating first is what makes a slide visible: last_refill_time is stored
	// in whole seconds, so a bucket drained microseconds ago would look
	// unchanged even if the rejected path did reset it to now.
	backdateRefill(t, client, ip, refillInterval/2)

	tokensBefore, refillBefore := readBucket(t, client, ip)

	for i := 0; i < 3; i++ {
		if got := getStatus(t, app, ip); got != fiber.StatusTooManyRequests {
			t.Fatalf("got status %d, want %d", got, fiber.StatusTooManyRequests)
		}
	}

	tokensAfter, refillAfter := readBucket(t, client, ip)
	if tokensAfter != tokensBefore {
		t.Errorf("tokens moved from %d to %d across rejected requests", tokensBefore, tokensAfter)
	}
	if !refillAfter.Equal(refillBefore) {
		t.Errorf("last_refill_time moved from %s to %s across rejected requests — the refill clock is sliding", refillBefore, refillAfter)
	}
}

func TestTokenBucketDoesNotRefillBeforeInterval(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	drainBucket(t, app, ip)
	backdateRefill(t, client, ip, refillInterval/2)

	if got := getStatus(t, app, ip); got != fiber.StatusTooManyRequests {
		t.Errorf("got status %d after half an interval, want %d — tokens are arriving early", got, fiber.StatusTooManyRequests)
	}
}

func TestTokenBucketRefillsAfterOneInterval(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	drainBucket(t, app, ip)
	backdateRefill(t, client, ip, refillInterval)

	grant := expectedRefillGrant(1)
	for i := 1; i <= grant; i++ {
		if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
			t.Fatalf("refilled token %d/%d: got status %d, want %d", i, grant, got, fiber.StatusAccepted)
		}
	}

	if got := getStatus(t, app, ip); got != fiber.StatusTooManyRequests {
		t.Errorf("got status %d after spending the %d refilled tokens, want %d", got, grant, fiber.StatusTooManyRequests)
	}
}

// A bucket idle for many intervals refills to capacity and no further — the
// min() clamp is what keeps an idle client from banking unlimited burst.
func TestTokenBucketRefillNeverExceedsCapacity(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	drainBucket(t, app, ip)
	backdateRefill(t, client, ip, 100*refillInterval)

	for i := 1; i <= maxTokens; i++ {
		if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
			t.Fatalf("token %d/%d after a long idle: got status %d, want %d", i, maxTokens, got, fiber.StatusAccepted)
		}
	}

	if got := getStatus(t, app, ip); got != fiber.StatusTooManyRequests {
		t.Errorf("got status %d after spending capacity %d, want %d — an idle bucket banked more than capacity", got, maxTokens, fiber.StatusTooManyRequests)
	}
}

// last_refill_time must advance by whole intervals only. Snapping it to now
// would silently discard the leftover time and stretch the effective rate.
func TestTokenBucketRefillCarriesRemainder(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	const wholeIntervals = 2
	remainder := refillInterval / 2

	drainBucket(t, app, ip)
	backdateRefill(t, client, ip, wholeIntervals*refillInterval+remainder)

	if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
		t.Fatalf("got status %d after %d intervals, want %d", got, wholeIntervals, fiber.StatusAccepted)
	}

	_, lastRefill := readBucket(t, client, ip)

	// The unused remainder should still be pending, so the stored timestamp sits
	// one remainder in the past rather than at now.
	age := time.Since(lastRefill)
	const tolerance = 2 * time.Second
	if age < remainder-tolerance || age > remainder+tolerance {
		t.Errorf("last_refill_time is %s old, want ~%s — the remainder was dropped instead of carried", age.Truncate(time.Second), remainder)
	}
}

// Buckets must expire, otherwise every IP ever seen is retained forever. The
// TTL only has to outlive a full refill, since a fully refilled bucket and a
// brand new one are the same thing.
func TestTokenBucketSetsTTLOnBucket(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
		t.Fatalf("got status %d, want %d", got, fiber.StatusAccepted)
	}

	key := utils.TokenBucketKey(ip)
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("read ttl for %s: %v", key, err)
	}

	if ttl <= 0 || ttl > bucketTTL {
		t.Fatalf("got ttl %s for %s, want a value in (0, %s]", ttl, key, bucketTTL)
	}
	if fullRefill := (maxTokens / tokensPerRefill) * refillInterval; ttl < fullRefill {
		t.Errorf("ttl %s is shorter than the %s a bucket needs to refill completely", ttl, fullRefill)
	}
}

// Regression: HMGet reports a missing field as a nil element. Asserting it to a
// string used to panic and take the process down with it.
func TestTokenBucketTreatsPartialHashAsFreshBucket(t *testing.T) {
	app, client := newTokenBucketApp(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, tc := range []struct {
		name  string
		field string
	}{
		{name: "only tokens", field: "tokens"},
		{name: "only last_refill_time", field: "last_refill_time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip := uniqueIP()
			if err := client.HSet(ctx, utils.TokenBucketKey(ip), tc.field, 1).Err(); err != nil {
				t.Fatalf("seed partial bucket: %v", err)
			}

			if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
				t.Errorf("got status %d for a bucket holding only %q, want %d", got, tc.field, fiber.StatusAccepted)
			}
		})
	}
}

// Regression: a field that is present but not a number must not panic either.
func TestTokenBucketTreatsMalformedHashAsFreshBucket(t *testing.T) {
	app, client := newTokenBucketApp(t)
	ip := uniqueIP()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.HSet(ctx, utils.TokenBucketKey(ip), "tokens", "plenty", "last_refill_time", "yesterday").Err(); err != nil {
		t.Fatalf("seed malformed bucket: %v", err)
	}

	if got := getStatus(t, app, ip); got != fiber.StatusAccepted {
		t.Errorf("got status %d for a malformed bucket, want %d", got, fiber.StatusAccepted)
	}
}

// A limiter that cannot reach its store must fail closed with a 500 rather than
// waving traffic through or crashing.
func TestTokenBucketReturns500WhenRedisIsUnreachable(t *testing.T) {
	// Port 1 is reserved and never listening, so this connection is refused
	// rather than left hanging.
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 500 * time.Millisecond,
		MaxRetries:  -1,
		Protocol:    2,
	})
	t.Cleanup(func() {
		client.Close()
	})

	app := newTokenBucketAppWithClient(client)

	if got := getStatus(t, app, uniqueIP()); got != fiber.StatusInternalServerError {
		t.Errorf("got status %d with redis down, want %d", got, fiber.StatusInternalServerError)
	}
}

// Known gap, PLAN.md stage 8: read-modify-write across HMGet and HSet is not
// atomic, so concurrent requests from one client all read the same token count
// and admit far more than capacity — 199 of these 200 have been observed getting
// through against a capacity of 2. Un-skip once the decision moves into a Lua
// script or a WATCH loop; 200 requests is enough to expose the race on every run,
// where 50 only caught it about two runs in three.
func TestTokenBucketConcurrentRequestsAdmitExactlyCapacity(t *testing.T) {
	t.Skip("known failure: the token bucket read-modify-write is not atomic yet — PLAN.md stage 8")

	app, _ := newTokenBucketApp(t)
	ip := uniqueIP()

	// Warm the app up on a throwaway client so goroutines below don't race on
	// fiber's lazy startup.
	getStatus(t, app, uniqueIP())

	const requests = 200

	var (
		accepted atomic.Int32
		rejected atomic.Int32
		start    = make(chan struct{})
		wg       sync.WaitGroup
	)

	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			switch status := getStatus(t, app, ip); status {
			case fiber.StatusAccepted:
				accepted.Add(1)
			case fiber.StatusTooManyRequests:
				rejected.Add(1)
			default:
				t.Errorf("unexpected status %d", status)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := accepted.Load(); got != maxTokens {
		t.Errorf("got %d accepted out of %d concurrent requests, want exactly %d", got, requests, maxTokens)
	}
	if got := rejected.Load(); got != requests-maxTokens {
		t.Errorf("got %d rejected out of %d concurrent requests, want %d", got, requests, requests-maxTokens)
	}
}
