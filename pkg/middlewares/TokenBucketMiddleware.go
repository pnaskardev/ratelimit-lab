package middlewares

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
)

type TokenBucketMiddleware interface {
	TokenBucketMiddleware() fiber.Handler
}

func (m *middlewares) TokenBucketMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {

		// WHO, WO IS THE CLIENT?
		var ip = c.IP()
		fmt.Println(ip)
		return c.Next()
	}
}
