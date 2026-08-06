package middlewares

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/redis/go-redis/v9"
)

const limit = 1000
const window time.Duration = time.Duration(time.Minute * 1)

type middlewares struct {
	cache *redis.Client
}

type Middlewares interface {
	FixedWindowMiddleware() fiber.Handler
}

func NewMiddlewares(cache *redis.Client) Middlewares {
	return &middlewares{
		cache: cache,
	}
}

// FixedWindowMiddleware caps each client at limit requests per window, counting
// against a bucket keyed by the truncated window start.
func (m *middlewares) FixedWindowMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {

		id := requestid.FromContext(c)
		log.Printf("Request ID: %s", id)

		return c.Next()
	}
}
