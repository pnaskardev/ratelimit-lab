package middlewares

import (
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

type middlewares struct {
	cache *redis.Client
}

type Middlewares interface {
	FixedWindowMiddleware() fiber.Handler
	TokenBucketMiddleware() fiber.Handler
	SlidingWindowLogMiddleware() fiber.Handler
}

func NewMiddlewares(cache *redis.Client) Middlewares {
	return &middlewares{
		cache: cache,
	}
}
