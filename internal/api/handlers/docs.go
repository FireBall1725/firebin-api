// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"net/http"

	"github.com/swaggo/swag"

	// Registers the generated OpenAPI spec with the swag package so ReadDoc works.
	_ "github.com/firelabsca/firebin-api/docs"
)

// ServeOpenAPISpec returns the generated OpenAPI (Swagger 2.0) document as JSON.
// It is public so the docs page and external tools can read it without a token.
func (h *Handler) ServeOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	doc, err := swag.ReadDoc()
	if err != nil {
		http.Error(w, "spec unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write([]byte(doc))
}

// ServeScalarUI serves the Scalar API reference, a single HTML page that loads
// the Scalar bundle from a CDN and renders the spec at /api/openapi.json.
func (h *Handler) ServeScalarUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(scalarHTML))
}

const scalarHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>FireBin API Reference</title>
    <link rel="icon" href="data:," />
    <style>
      :root { --scalar-color-accent: #d98200; }
      body { margin: 0; }
    </style>
  </head>
  <body>
    <script
      id="api-reference"
      data-url="/api/openapi.json"
      data-configuration='{"theme":"default","darkMode":true,"hideDownloadButton":false}'></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  </body>
</html>`
