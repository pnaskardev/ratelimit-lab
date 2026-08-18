package middlewares

import (
	"context"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/log"
	"github.com/pnaskardev/ratelimit-lab/pkg/utils"
)

const maxTokens = 2
const refillInterval time.Duration = time.Duration(time.Second * 60)
const tokensPerRefill = 2

// A bucket that has been idle long enough to refill completely is
// indistinguishable from a brand new one, so it can be dropped rather than kept
// around forever for an IP that may never come back.
const bucketTTL time.Duration = (maxTokens/tokensPerRefill + 1) * refillInterval

// Each client gets a bucket of tokens. A request spends one, and tokens drip
// back in over time. Bursts are allowed up to the bucket size; the sustained
// rate is capped by the refill rate.
func (m *middlewares) TokenBucketMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		// WHO, WO IS THE CLIENT?
		var ip = c.IP()

		key := utils.TokenBucketKey(ip)

		now := time.Now()

		// An unknown client starts with a full bucket, so a missing key needs no
		// separate seeding round trip.
		tokens := maxTokens
		lastRefill := now

		values, err := m.cache.HMGet(ctx, key, "tokens", "last_refill_time").Result()
		if err != nil {
			log.Errorf("TOKEN BUCKET: FAILED TO READ %s: %v", key, err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		// Redis reports a missing key or a missing field as a nil element, so
		// every element has to be checked before it is used.
		if len(values) == 2 {
			storedTokens, tokensOK := hashInt64(values[0])
			storedRefill, refillOK := hashInt64(values[1])
			if tokensOK && refillOK {
				tokens = int(storedTokens)
				lastRefill = time.Unix(storedRefill, 0)
			}
		}

		// Refill TOKENS INTO THE BUCKET
		if intervals := int(now.Sub(lastRefill) / refillInterval); intervals > 0 {
			tokens = min(tokens+intervals*tokensPerRefill, maxTokens)
			// Advance by whole intervals only; carrying the remainder forward
			// keeps the long-run refill rate exact.
			lastRefill = lastRefill.Add(time.Duration(intervals) * refillInterval)
		}

		if tokens <= 0 {
			// Nothing changed, so the stored state is still accurate as is.
			return c.SendStatus(fiber.StatusTooManyRequests)
		}

		tokens--

		if err := m.cache.HSet(ctx, key, "tokens", tokens, "last_refill_time", lastRefill.Unix()).Err(); err != nil {
			log.Errorf("TOKEN BUCKET: FAILED TO WRITE %s: %v", key, err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if err := m.cache.Expire(ctx, key, bucketTTL).Err(); err != nil {
			log.Errorf("TOKEN BUCKET: FAILED TO SET TTL ON %s: %v", key, err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		return c.Next()
	}
}

// hashInt64 reads one element of an HMGet reply, which is nil when the key or
// the field is absent.
func hashInt64(value any) (int64, bool) {
	str, ok := value.(string)
	if !ok {
		return 0, false
	}

	parsed, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, false
	}

	return parsed, true
}
