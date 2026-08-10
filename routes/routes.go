package routes

import (
	"github.com/gofiber/fiber/v3"
	"github.com/pnaskardev/ratelimit-lab/pkg/handlers"
	"github.com/pnaskardev/ratelimit-lab/pkg/middlewares"
	"github.com/redis/go-redis/v9"
)

func RegisterRoutes(app fiber.Router, cacheClient *redis.Client) {

	rateLimitingMiddleware := middlewares.NewMiddlewares(cacheClient)
	newHandlers := handlers.NewHandlers()

	apiGroup := app.Group("/api")

	apiGroup.Post("/fixed-window", rateLimitingMiddleware.FixedWindowMiddleware(), newHandlers.DefaultHandler)
	apiGroup.Get("/token-bucket", rateLimitingMiddleware.TokenBucketMiddleware(), newHandlers.DefaultHandler)
}
