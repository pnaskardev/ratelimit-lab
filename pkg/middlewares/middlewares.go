package middlewares

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

const limit = 5
const window time.Duration = time.Duration(time.Second * 30)

type middlewares struct {
	cache *redis.Client
}

type Middlewares interface {
	FixedWindowMiddleware() fiber.Handler
	TokenBucketMiddleware() fiber.Handler
}

func NewMiddlewares(cache *redis.Client) Middlewares {
	return &middlewares{
		cache: cache,
	}
}

// fixedWindowKey is the redis key holding the request count for one client
// within one window.
func fixedWindowKey(ip string, windowStart time.Time) string {
	return fmt.Sprintf("ratelimit:fixed:%s:%d", ip, windowStart.Unix())
}

func (m *middlewares) FixedWindowMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {

		// id := requestid.FromContext(c)

		// WHO, WO IS THE CLIENT?
		var ip = c.IP()

		// When is the first request made
		var windowStart time.Time = time.Now().UTC().Truncate(window)

		var key string = fixedWindowKey(ip, windowStart)

		log.Printf("rate limit key: %s", key)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		// Will create the key if it doesn't exists and set it to 1 with increment
		count, err := m.cache.Incr(ctx, key).Result()
		if err != nil {
			return err
		}

		fmt.Println(count)

		if count == 1 {
			var ttl time.Duration = time.Until(windowStart.Add(window))
			if err := m.cache.Expire(ctx, key, ttl).Err(); err != nil {
				return err
			}
		}

		if count > limit {
			return c.SendStatus(fiber.StatusTooManyRequests)
		}

		return c.Next()
	}
}

func (m *middlewares) TokenBucketMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {

		return c.Next()
	}
}
