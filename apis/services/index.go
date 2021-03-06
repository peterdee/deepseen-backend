package services

import (
	"github.com/gofiber/fiber/v2"

	"deepseen-backend/middlewares"
)

// APIs setup
func Setup(app *fiber.App) {
	group := app.Group("/api/services")

	group.Get("/image/:id", middlewares.AuthorizeServices, getImage)
}
