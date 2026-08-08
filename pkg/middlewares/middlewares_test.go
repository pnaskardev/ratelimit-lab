package middlewares

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/pnaskardev/ratelimit-lab/pkg/handlers"
	"github.com/redis/go-redis/v9"
)

// The limiter's whole contract lives in redis (atomic INCR + a TTL pinned to the
// window boundary), so these tests run against a real server rather than a fake.
// DB 15 is used as a scratch database and flushed for every test.
const testRedisDB = 15

const routePath = "/api/fixed-window"

// The test connection always reports 0.0.0.0 as the remote address, so client
// identity is driven through X-Forwarded-For instead.
const testProxyIP = "0.0.0.0"

var ipCounter atomic.Uint32

func redisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

// newTestApp builds the same middleware/handler pair that routes.RegisterRoutes
// wires up, backed by a flushed scratch database.
func newTestApp(t *testing.T) (*fiber.App, *redis.Client) {
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

	app := fiber.New(fiber.Config{
		ProxyHeader:      fiber.HeaderXForwardedFor,
		TrustProxy:       true,
		TrustProxyConfig: fiber.TrustProxyConfig{Proxies: []string{testProxyIP}},
	})

	app.Post(routePath, NewMiddlewares(client).FixedWindowMiddleware(), handlers.NewHandlers().DefaultHandler)

	return app, client
}

// uniqueIP hands out a distinct TEST-NET-3 address so buckets never overlap
// between clients within a test.
func uniqueIP() string {
	return fmt.Sprintf("203.0.113.%d", ipCounter.Add(1)%254+1)
}

func post(t *testing.T, app *fiber.App, ip string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, routePath, nil)
	req.Header.Set(fiber.HeaderXForwardedFor, ip)

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("request from %s: %v", ip, err)
	}
	return resp
}

func postStatus(t *testing.T, app *fiber.App, ip string) int {
	t.Helper()

	resp := post(t, app, ip)
	defer resp.Body.Close()
	return resp.StatusCode
}

func windowStart() time.Time {
	return time.Now().UTC().Truncate(window)
}

func timeLeftInWindow() time.Duration {
	return time.Until(windowStart().Add(window))
}

// requireHeadroom keeps a test from straddling a window boundary, which would
// silently reset the counter mid-assertion.
func requireHeadroom(t *testing.T, needed time.Duration) {
	t.Helper()

	if remaining := timeLeftInWindow(); remaining < needed {
		t.Logf("only %s left in the current window, waiting for the next one", remaining.Truncate(time.Millisecond))
		time.Sleep(remaining + 100*time.Millisecond)
	}
}

func TestFixedWindowAllowsUpToLimitThenRejects(t *testing.T) {
	requireHeadroom(t, 5*time.Second)

	app, _ := newTestApp(t)
	ip := uniqueIP()

	for i := 1; i <= limit; i++ {
		if got := postStatus(t, app, ip); got != fiber.StatusAccepted {
			t.Fatalf("request %d/%d: got status %d, want %d", i, limit, got, fiber.StatusAccepted)
		}
	}

	for i := limit + 1; i <= limit+3; i++ {
		if got := postStatus(t, app, ip); got != fiber.StatusTooManyRequests {
			t.Fatalf("request %d (over limit %d): got status %d, want %d", i, limit, got, fiber.StatusTooManyRequests)
		}
	}
}

func TestFixedWindowAllowedRequestReachesHandler(t *testing.T) {
	requireHeadroom(t, 5*time.Second)

	app, _ := newTestApp(t)

	resp := post(t, app, uniqueIP())
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

func TestFixedWindowKeepsClientsIndependent(t *testing.T) {
	requireHeadroom(t, 5*time.Second)

	app, _ := newTestApp(t)
	noisy, quiet := uniqueIP(), uniqueIP()

	for i := 0; i < limit+1; i++ {
		postStatus(t, app, noisy)
	}
	if got := postStatus(t, app, noisy); got != fiber.StatusTooManyRequests {
		t.Fatalf("exhausted client: got status %d, want %d", got, fiber.StatusTooManyRequests)
	}

	if got := postStatus(t, app, quiet); got != fiber.StatusAccepted {
		t.Errorf("second client: got status %d, want %d — buckets are leaking across clients", got, fiber.StatusAccepted)
	}
}

// The TTL must be pinned to the window boundary, not refreshed per request —
// otherwise an active client's window slides forward and never resets.
func TestFixedWindowTTLExpiresAtWindowBoundary(t *testing.T) {
	requireHeadroom(t, 10*time.Second)

	app, client := newTestApp(t)
	ip := uniqueIP()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	postStatus(t, app, ip)

	key := fixedWindowKey(ip, windowStart())
	firstTTL, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("read ttl for %s: %v", key, err)
	}
	if firstTTL <= 0 || firstTTL > window {
		t.Fatalf("got ttl %s for %s, want a value in (0, %s]", firstTTL, key, window)
	}
	if diff := timeLeftInWindow() - firstTTL; diff < -time.Second || diff > time.Second {
		t.Errorf("ttl %s is %s off the time left in the window — it is not pinned to the boundary", firstTTL, diff)
	}

	postStatus(t, app, ip)

	secondTTL, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("read ttl for %s: %v", key, err)
	}
	if secondTTL > firstTTL {
		t.Errorf("ttl grew from %s to %s after a second request — the window is sliding", firstTTL, secondTTL)
	}
}

// Stage 2 deliverable from PLAN.md: 50 concurrent requests against one key must
// admit exactly `limit` of them. Run under -race.
func TestFixedWindowConcurrentRequestsAdmitExactlyLimit(t *testing.T) {
	requireHeadroom(t, 10*time.Second)

	app, _ := newTestApp(t)
	ip := uniqueIP()

	// Warm the app up on a throwaway client so goroutines below don't race on
	// fiber's lazy startup.
	postStatus(t, app, uniqueIP())

	const requests = 50

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

			switch status := postStatus(t, app, ip); status {
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

	if got := accepted.Load(); got != limit {
		t.Errorf("got %d accepted out of %d concurrent requests, want exactly %d", got, requests, limit)
	}
	if got := rejected.Load(); got != requests-limit {
		t.Errorf("got %d rejected out of %d concurrent requests, want %d", got, requests, requests-limit)
	}
}

// Costs up to one full window of wall clock, so it is skipped under -short.
func TestFixedWindowResetsAtNextWindow(t *testing.T) {
	if testing.Short() {
		t.Skipf("skipping: waits out a %s window boundary", window)
	}

	app, _ := newTestApp(t)
	ip := uniqueIP()

	for i := 0; i < limit; i++ {
		if got := postStatus(t, app, ip); got != fiber.StatusAccepted {
			t.Fatalf("request %d: got status %d, want %d", i+1, got, fiber.StatusAccepted)
		}
	}
	if got := postStatus(t, app, ip); got != fiber.StatusTooManyRequests {
		t.Fatalf("over limit: got status %d, want %d", got, fiber.StatusTooManyRequests)
	}

	remaining := timeLeftInWindow()
	t.Logf("waiting %s for the window to roll over", remaining.Truncate(time.Millisecond))
	time.Sleep(remaining + 250*time.Millisecond)

	if got := postStatus(t, app, ip); got != fiber.StatusAccepted {
		t.Errorf("first request of the new window: got status %d, want %d", got, fiber.StatusAccepted)
	}
}
