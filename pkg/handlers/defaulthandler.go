package handlers

import (
	"github.com/gofiber/fiber/v3"
)

type handlers struct {
}

type Handlers interface {
	DefaultHandler(c fiber.Ctx) error
}

func NewHandlers() Handlers {
	return &handlers{}
}

func (r *handlers) DefaultHandler(c fiber.Ctx) error {

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"status": "ACCEPTED",
	})

}
