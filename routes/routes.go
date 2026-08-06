package routes

import (
	"github.com/gofiber/fiber/v3"
	"github.com/pnaskardev/ratelimit-lab/pkg/handlers"
)

func RegisterRoutes(app fiber.Router) {

	newHandlers := handlers.NewHandlers()

	apiGroup := app.Group("/api")

	apiGroup.Post("/fixed-window", newHandlers.DefaultHandler)
}
