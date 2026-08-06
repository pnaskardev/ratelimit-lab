package main

import (
	"log"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/pnaskardev/ratelimit-lab/infra/cache"
	"github.com/pnaskardev/ratelimit-lab/routes"
)

func main() {

	err := cache.InitCache()
	if err != nil {
		log.Fatal(err)
		return
	}

	cacheClient := cache.GetCacheClient()

	app := fiber.New()

	// Middlewares
	app.Use(requestid.New())

	routes.RegisterRoutes(app, cacheClient)

	app.Get("/", func(c fiber.Ctx) error {
		return c.SendString("Hello, World!")
	})

	log.Fatal(app.Listen(":3000"))
}
