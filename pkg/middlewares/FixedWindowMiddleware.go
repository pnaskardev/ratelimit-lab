package middlewares

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/pnaskardev/ratelimit-lab/pkg/utils"
)

const limit = 5
const window time.Duration = time.Duration(time.Second * 30)



// In a particular window of time you can make a fixed number of requests
// Anything more than the particular number of requests are blocked
func (m *middlewares) FixedWindowMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {

		// id := requestid.FromContext(c)

		// WHO, WO IS THE CLIENT?
		var ip = c.IP()

		// When is the first request made
		var windowStart time.Time = time.Now().UTC().Truncate(window)

		var key string = utils.FixedWindowKey(ip, windowStart)

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
