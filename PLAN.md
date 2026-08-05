# Rate Limiter From Scratch — Build Guide

A self-guided build plan for implementing rate limiting algorithms in Go, from first principles. Each stage should compile and pass a manual `curl` test before moving to the next. The goal is understanding, not a finished library — resist the urge to look up reference implementations before you've derived the logic yourself.

---

## Stage 1: Interface First, No Algorithm Yet

Before writing any limiter, design the contract.

**Questions to answer on paper before coding:**
- What does `Allow(key string)` need to return besides a bool? Think about what the HTTP layer needs to set `Retry-After` and `X-RateLimit-*` headers.
- Should the interface be `Allow(key string) bool`, or should it accept a `cost int`? Some requests (bulk endpoints, expensive queries) may be "worth" more than 1 token. You don't have to support this now — but decide consciously whether to leave room for it.
- Where does "now" come from? If you hardcode `time.Now()` inside each limiter, you can't unit-test time-dependent behavior deterministically. Consider injecting a clock function.

**Deliverable:** the interface, plus a fake in-memory struct with a stub method that always returns `true`. Wire it into an `http.Handler` middleware. Get the plumbing working end to end before any real algorithm exists.

---

## Stage 2: Fixed Window (Build Your Test Harness Here)

Intentionally the simplest algorithm — use it to validate your *scaffolding*, not the algorithm itself.

**Design questions:**
- What's your key granularity — one counter per client, or per (client, endpoint)?
- How do you reset the counter at a window boundary — a stored `windowStart` timestamp you compare against, or a background sweep?
- **Concurrency:** two goroutines call `Allow` at the same instant. Walk through what happens with a plain `map[string]int` and no lock. Then fix it. Try both `sync.Mutex` and `sync.RWMutex` — which is correct here, and why? (Hint: think about whether reads ever happen without a write.)

**Deliverable:** a test that fires 50 concurrent requests at one key and asserts the count is exactly right, run under `go test -race`. If the test doesn't fail *without* a lock, the test isn't exercising the race — fix the test before fixing the code.

---

## Stage 3: Token Bucket (The Important One)

The industry-default algorithm. Derive it yourself.

**Design questions:**
- State per key: what's the *minimum* you need to store? You don't need a history of timestamps — just current token count + last refill time, computed lazily.
- Refill logic: background ticker per key, or lazy computation on each `Allow()` call via `tokens_now = min(capacity, tokens_last + elapsed * rate)`? Lazy computation avoids a goroutine-per-key problem — think about why that matters at scale (10,000 keys ≠ 10,000 goroutines).
- Float or integer tokens? Fractional refill rates (e.g., 2.5/sec) will bite you if you use ints.

**Deliverable:** a burst test — hammer it with 20 requests instantly against a capacity of 10, confirm exactly 10 pass, wait, confirm refill behaves correctly.

---

## Stage 4: Leaky Bucket

Build this *after* token bucket so the contrast is fresh — it's close to the token bucket's mirror image (draining vs. refilling).

**Design question:**
- Does leaky bucket need to actually queue/delay requests, or can you model it as "reject if bucket is full" without a real queue? Both are valid depending on whether you want **shaping** (delay + release) or **policing** (accept/reject) semantics. Decide which one you're building and state it explicitly — people conflate these two constantly.

---

## Stage 5: Sliding Window Counter

Forces you to understand the weighted-average approximation instead of copy-pasting a formula.

**Work out on paper before coding:**
- Previous window had 8 requests, current window has 3 so far, you're 40% into the current window. What's the estimated count?
- Derive the formula yourself — you'll internalize *why* it approximates the sliding log, not just *that* it does.

---

## Stage 6: Concurrency Stress Test Across All Implementations

One shared test harness that fires N goroutines at each limiter and checks invariants:
- Never exceeds capacity
- Never undercounts
- Behaves identically under `-race`

This is also a strong "here's the harness" section for the article, independent of which algorithms you've implemented.

---

## Traps to Avoid

1. **Time-based tests are flaky if you `sleep()` in tests.** Design the limiter to accept an injectable clock from Stage 1 — painful to retrofit later.
2. **`defer mu.Unlock()` has non-zero cost.** Fine for correctness, but if you're benchmarking limiter overhead for the article, measure it rather than assume it's free.
3. **Map access without a lock won't always panic.** Go's race detector will catch it; casual manual testing often won't. Don't trust "it worked when I tried it" — always verify under `-race`.

---

## Suggested Article Arc (mirrors this build order)

1. Interface + middleware pattern — how pluggable rate limiting is architected
2. Fixed window — simplest, demonstrate the boundary-burst flaw with a curl script
3. Sliding log — accurate but show memory growth under load
4. Sliding window counter — the practical fix
5. Token bucket — burst-friendly, the "default" choice
6. Leaky bucket — contrast queuing/smoothing vs. rejecting
7. GCRA (optional/bonus) — same guarantees as token bucket, O(1) storage
8. Bonus: distributed version with Redis + Lua, and the race condition it solves