package middlewares

import "github.com/gofiber/fiber/v3"

func (m *middlewares) SlidingWindowLogMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.Next()
	}
}