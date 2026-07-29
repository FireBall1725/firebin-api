// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package api wires the HTTP routes for the FireBin API.
package api

import (
	"net/http"

	"github.com/firelabsca/firebin-api/internal/api/handlers"
	"github.com/firelabsca/firebin-api/internal/api/middleware"
)

// NewRouter builds the top-level HTTP handler with all routes and middleware,
// over a handler the caller already constructed (so main owns the job lifecycle).
func NewRouter(h *handlers.Handler) http.Handler {
	authn := middleware.NewAuthenticator(h.JWT, h.Tokens, h.Users)

	mux := http.NewServeMux()

	// ── Public ────────────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/v1/health", h.Health)

	// API docs (no auth): the raw OpenAPI spec and the Scalar reference UI.
	mux.HandleFunc("GET /api/openapi.json", h.ServeOpenAPISpec)
	mux.HandleFunc("GET /api/docs", h.ServeScalarUI)
	mux.HandleFunc("GET /api/v1/auth/setup", h.SetupStatus)
	mux.HandleFunc("POST /api/v1/auth/register", h.Register)
	mux.HandleFunc("POST /api/v1/auth/login", h.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.Logout)

	// ── Authenticated ───────────────────────────────────────────────────────
	// protected = signed in, and viewers are refused on state-changing methods
	// (read-only accounts). admin = instance admin only.
	protected := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, authn.RequireWriter(fn))
	}
	admin := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, authn.RequireAdmin(fn))
	}

	protected("GET /api/v1/me", h.Me)
	protected("PATCH /api/v1/users/me/password", h.ChangeMyPassword)

	// User management (admin only)
	admin("GET /api/v1/users", h.ListUsers)
	admin("POST /api/v1/users", h.CreateUser)
	admin("PATCH /api/v1/users/{id}", h.UpdateUser)
	admin("POST /api/v1/users/{id}/reset-password", h.ResetUserPassword)
	admin("DELETE /api/v1/users/{id}", h.DeleteUser)

	// Real-time change stream (SSE)
	protected("GET /api/v1/events", h.Events)

	// Dashboard
	protected("GET /api/v1/stats", h.GetStats)
	protected("GET /api/v1/parts/low-stock", h.LowStock)
	protected("GET /api/v1/stock/recent", h.RecentActivity)
	protected("POST /api/v1/tokens", h.CreatePAT)
	protected("GET /api/v1/tokens", h.ListPATs)
	protected("DELETE /api/v1/tokens/{id}", h.RevokePAT)

	// Categories
	protected("GET /api/v1/categories", h.ListCategories)
	protected("POST /api/v1/categories", h.CreateCategory)
	protected("PATCH /api/v1/categories/{id}", h.UpdateCategory)
	protected("DELETE /api/v1/categories/{id}", h.DeleteCategory)

	// Parts
	protected("GET /api/v1/parameter-templates", h.ListParameterTemplates)

	// KiCad library index. Reads are open to any signed-in user (the part
	// editor and the library viewer both need them); uploading replaces the
	// whole index, so it is admin-only.
	protected("GET /api/v1/parts/{id}/kicad/suggestions", h.SuggestKicadForPart)
	protected("GET /api/v1/kicad/libraries", h.ListKicadLibraries)
	protected("GET /api/v1/kicad/libraries/search", h.SearchKicadLibrary)
	protected("GET /api/v1/kicad/libraries/items", h.ListKicadLibraryItems)
	protected("GET /api/v1/kicad/libraries/drawing", h.GetKicadDrawing)
	protected("GET /api/v1/kicad/libraries/status", h.GetKicadIndexMeta)
	admin("POST /api/v1/kicad/libraries/batch", h.UploadKicadLibraryBatch)
	admin("POST /api/v1/kicad/libraries/finish", h.FinishKicadLibraryScan)
	protected("GET /api/v1/parts", h.ListParts)
	protected("POST /api/v1/parts", h.CreatePart)
	protected("POST /api/v1/parts/bulk/move", h.BulkMoveParts)
	protected("POST /api/v1/parts/bulk/enrich", h.BulkEnrichParts)
	protected("POST /api/v1/parts/bulk/minimum-stock", h.BulkSetMinimumStock)
	protected("GET /api/v1/parts/{id}", h.GetPart)
	protected("PATCH /api/v1/parts/{id}", h.UpdatePart)
	protected("DELETE /api/v1/parts/{id}", h.DeletePart)
	protected("POST /api/v1/parts/{id}/image", h.UploadPartImage)
	// Public so it works as a plain <img src>, like the static /symbols/*.svg.
	mux.HandleFunc("GET /api/v1/parts/{id}/image", h.GetPartImage)

	// Projects → boards → per-board BOM
	protected("GET /api/v1/projects", h.ListProjects)
	protected("POST /api/v1/projects", h.CreateProject)
	protected("GET /api/v1/projects/{id}", h.GetProject)
	protected("PATCH /api/v1/projects/{id}", h.UpdateProject)
	protected("DELETE /api/v1/projects/{id}", h.DeleteProject)
	protected("POST /api/v1/projects/{id}/boards/preview", h.PreviewBoard)
	protected("POST /api/v1/projects/{id}/matches", h.SetProjectMatch)
	protected("POST /api/v1/projects/{id}/cover", h.UploadProjectCover)
	protected("DELETE /api/v1/projects/{id}/cover", h.RemoveProjectCover)
	protected("POST /api/v1/projects/{id}/boards/blank", h.CreateBlankBoard)
	protected("POST /api/v1/projects/{id}/boards", h.CreateBoard)
	protected("GET /api/v1/projects/{id}/assets", h.ListProjectAssets)
	protected("GET /api/v1/boards/{id}", h.GetBoard)
	protected("PATCH /api/v1/boards/{id}", h.UpdateBoard)
	protected("DELETE /api/v1/boards/{id}", h.DeleteBoard)
	protected("POST /api/v1/boards/{id}/assets", h.UploadBoardAsset)
	protected("GET /api/v1/boards/{id}/pick-list", h.BoardPickList)
	protected("POST /api/v1/boards/{id}/lines", h.AddBOMLine)
	protected("PATCH /api/v1/lines/{id}", h.UpdateBOMLine)
	protected("DELETE /api/v1/lines/{id}", h.DeleteBOMLine)
	protected("GET /api/v1/assets/{id}", h.GetAsset)
	protected("DELETE /api/v1/assets/{id}", h.DeleteAsset)

	// Background jobs (tasks): enqueue via action endpoints, observe here
	protected("GET /api/v1/tasks", h.ListTasks)
	protected("GET /api/v1/tasks/{id}", h.GetTask)
	protected("GET /api/v1/tasks/{id}/logs", h.GetTaskLogs)
	protected("POST /api/v1/tasks/{id}/cancel", h.CancelTask)
	protected("POST /api/v1/tasks/{id}/retry", h.RetryTask)
	admin("DELETE /api/v1/tasks", h.ClearTasks)

	// Full-instance JSON export / import (admin) — application-level backup.
	admin("GET /api/v1/export", h.ExportData)
	admin("POST /api/v1/import", h.ImportData)

	// Scan a distributor barcode (EIGP 114) → parsed fields + part match
	protected("POST /api/v1/scan", h.Scan)

	// Labels (barcode/QR label-sheet generation → PDF)
	protected("GET /api/v1/labels/catalog", h.SearchLabelCatalog)
	protected("GET /api/v1/labels/media", h.ListLabelMedia)
	protected("POST /api/v1/labels/media", h.CreateLabelMedia)
	protected("DELETE /api/v1/labels/media/{id}", h.DeleteLabelMedia)
	protected("POST /api/v1/labels/print", h.PrintLabels)
	protected("POST /api/v1/labels/preview", h.PreviewLabel)
	protected("POST /api/v1/labels/resolve", h.ResolveLabel)
	protected("GET /api/v1/labels/templates", h.ListLabelTemplates)
	protected("POST /api/v1/labels/templates", h.CreateLabelTemplate)
	protected("PATCH /api/v1/labels/templates/{id}", h.UpdateLabelTemplate)
	protected("DELETE /api/v1/labels/templates/{id}", h.DeleteLabelTemplate)

	// Enrichment (Nexar / Octopart MPN lookup)
	protected("GET /api/v1/enrich", h.Enrich)
	protected("POST /api/v1/parts/{id}/enrich", h.EnrichPart)
	protected("GET /api/v1/enrich/status", h.EnrichmentStatus)

	// Enrichment settings (admin)
	admin("GET /api/v1/settings/enrichment", h.GetEnrichmentSettings)
	admin("PUT /api/v1/settings/enrichment", h.UpdateEnrichmentSettings)
	admin("POST /api/v1/settings/enrichment/test", h.TestEnrichment)

	// Stock settings (admin) — opt-in empty-lot cleanup (default off).
	admin("GET /api/v1/settings/stock", h.GetStockSettings)
	admin("PUT /api/v1/settings/stock", h.UpdateStockSettings)
	admin("POST /api/v1/stock/cleanup-empty", h.CleanupEmptyLots)

	// Manufacturer & supplier parts (commercial tree)
	protected("GET /api/v1/manufacturers", h.ListManufacturers)
	protected("GET /api/v1/suppliers", h.ListSuppliers)
	protected("POST /api/v1/parts/{id}/manufacturer-parts", h.CreateManufacturerPart)
	protected("PATCH /api/v1/manufacturer-parts/{id}", h.UpdateManufacturerPart)
	protected("DELETE /api/v1/manufacturer-parts/{id}", h.DeleteManufacturerPart)
	protected("POST /api/v1/manufacturer-parts/{id}/supplier-parts", h.CreateSupplierPart)
	protected("DELETE /api/v1/supplier-parts/{id}", h.DeleteSupplierPart)

	// Stock (scoped to a part)
	protected("GET /api/v1/parts/{id}/stock", h.ListPartStock)
	protected("GET /api/v1/parts/{id}/stock/history", h.ListPartStockHistory)
	protected("POST /api/v1/parts/{id}/stock/adjust", h.AdjustPartStock)
	protected("POST /api/v1/stock/move", h.MoveStock)

	// Stock lots (barcoded units cut off a reel — e.g. a mini spool)
	protected("GET /api/v1/stock/scan", h.ScanStockLot)
	protected("GET /api/v1/stock-items/{id}", h.GetStockLot)
	protected("POST /api/v1/stock/split", h.SplitStock)
	protected("POST /api/v1/stock/merge", h.MergeStock)
	protected("POST /api/v1/stock/relocate", h.RelocateStock)
	protected("POST /api/v1/stock/lot-adjust", h.AdjustStockLot)
	protected("POST /api/v1/stock/labels/print", h.PrintStockLabels)
	protected("POST /api/v1/stock/labels/resolve", h.ResolveStockLabel)

	// Locations
	protected("GET /api/v1/locations", h.ListLocations)
	protected("GET /api/v1/locations/scan", h.ScanLocation)
	protected("POST /api/v1/locations/labels/print", h.PrintLocationLabels)
	protected("POST /api/v1/locations/labels/resolve", h.ResolveLocationLabel)
	protected("POST /api/v1/locations", h.CreateLocation)
	protected("GET /api/v1/locations/{id}", h.GetLocation)
	protected("GET /api/v1/locations/{id}/stock", h.ListLocationStock)
	protected("PATCH /api/v1/locations/{id}", h.UpdateLocation)
	protected("DELETE /api/v1/locations/{id}", h.DeleteLocation)

	// Global middleware chain: security headers → request logging.
	return middleware.Chain(mux, middleware.SecurityHeaders, middleware.Logger)
}
