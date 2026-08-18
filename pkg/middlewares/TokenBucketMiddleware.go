package middlewares

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/log"
)

const maxTokens = 2
const refillInterval = 60
const tokensPerRefill = 2

func (m *middlewares) TokenBucketMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Second*1))
		defer cancel()
		// WHO, WO IS THE CLIENT?
		var ip = c.IP()

		key := fmt.Sprintf("rate_limit:%s", ip)

		existsBool, err := m.cache.Exists(ctx, key).Result()
		if err != nil {
			log.Errorf("REDIS ERROR : %v", err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		// the user cam for the first time and we need to set the key
		if existsBool == 0 {
			m.cache.HSet(ctx, key, "tokens", maxTokens)
			m.cache.HSet(ctx, key, "last_refill_time", time.Now().Unix())
		}

		// If already set dont do anything and move forward because tokens and time refill already exists
		var setTokens = 0
		var setRefillTimeUnix = time.Now()

		// We already have the key now try to get the last refill time
		values, err := m.cache.HMGet(ctx, key, "tokens", "last_refill_time").Result()
		if err != nil || len(values) != 2 {
			log.Errorf("FAILED TO GET TOKEN BUCKET; %v", err)
			// return c.SendStatus(fiber.StatusInternalServerError)
			setTokens = 0
			setRefillTimeUnix = time.Now()
		}
		setTokens, _ = strconv.Atoi(values[0].(string))
		lastRefill, _ := strconv.ParseInt(values[1].(string), 10, 64)

		setRefillTimeUnix = time.Unix(lastRefill, 0)

		// Refill TOKENS INTO THE BUCKET
		now := time.Now()
		elapsed := now.Sub(setRefillTimeUnix)

		if elapsed >= time.Duration(refillInterval*time.Second) {
			setTokens = min(setTokens+tokensPerRefill, maxTokens)
			m.cache.HSet(ctx, fmt.Sprintf("rate_limit:%s", ip), "tokens", setTokens)
			m.cache.HSet(ctx, fmt.Sprintf("rate_limit:%s", ip), "last_refill_time", now.Unix())
		}

		if setTokens > 0 {
			m.cache.HIncrBy(ctx, fmt.Sprintf("rate_limit:%s", ip), "tokens", -1)
			return c.Next()
		}

		return c.SendStatus(fiber.StatusTooManyRequests)
	}
}
