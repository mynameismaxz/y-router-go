package handlers

import (
	"html/template"
	"strings"

	"anthropic-openai-gateway/internal/static"

	"github.com/gofiber/fiber/v2"
)

// StaticHandler handles static content serving
type StaticHandler struct {
	indexTemplate   *template.Template
	termsTemplate   *template.Template
	privacyTemplate *template.Template
}

// NewStaticHandler creates a new static handler instance
func NewStaticHandler() (*StaticHandler, error) {
	// Read template files from embedded filesystem
	indexContent, err := static.StaticFiles.ReadFile("index.html")
	if err != nil {
		return nil, err
	}

	termsContent, err := static.StaticFiles.ReadFile("terms.html")
	if err != nil {
		return nil, err
	}

	privacyContent, err := static.StaticFiles.ReadFile("privacy.html")
	if err != nil {
		return nil, err
	}

	// Parse templates
	indexTemplate, err := template.New("index").Parse(string(indexContent))
	if err != nil {
		return nil, err
	}

	termsTemplate, err := template.New("terms").Parse(string(termsContent))
	if err != nil {
		return nil, err
	}

	privacyTemplate, err := template.New("privacy").Parse(string(privacyContent))
	if err != nil {
		return nil, err
	}

	return &StaticHandler{
		indexTemplate:   indexTemplate,
		termsTemplate:   termsTemplate,
		privacyTemplate: privacyTemplate,
	}, nil
}

// TemplateData holds data for template rendering
type TemplateData struct {
	FaviconURL string
}

// ServeIndex handles GET requests to "/"
func (h *StaticHandler) ServeIndex(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")

	var buf strings.Builder
	data := TemplateData{
		FaviconURL: static.FaviconDataURL(),
	}

	err := h.indexTemplate.Execute(&buf, data)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Internal Server Error")
	}

	return c.SendString(buf.String())
}

// ServeTerms handles GET requests to "/terms"
func (h *StaticHandler) ServeTerms(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")

	var buf strings.Builder
	data := TemplateData{
		FaviconURL: static.FaviconDataURL(),
	}

	err := h.termsTemplate.Execute(&buf, data)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Internal Server Error")
	}

	return c.SendString(buf.String())
}

// ServePrivacy handles GET requests to "/privacy"
func (h *StaticHandler) ServePrivacy(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")

	var buf strings.Builder
	data := TemplateData{
		FaviconURL: static.FaviconDataURL(),
	}

	err := h.privacyTemplate.Execute(&buf, data)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Internal Server Error")
	}

	return c.SendString(buf.String())
}

// ServeInstallScript handles GET requests to "/install.sh"
func (h *StaticHandler) ServeInstallScript(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/plain; charset=utf-8")
	c.Set("Content-Disposition", "attachment; filename=\"install.sh\"")

	scriptContent, err := static.StaticFiles.ReadFile("install.sh")
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Internal Server Error")
	}

	return c.Send(scriptContent)
}
