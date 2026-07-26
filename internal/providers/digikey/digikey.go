// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package digikey enriches an MPN using the Digi-Key Product Information V4 API,
// authenticated with a 2-legged (client-credentials) OAuth app. Free to use; no
// Digi-Key credit account required for product lookups.
package digikey

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/firelabsca/firebin-api/internal/models"
)

// Default production host. A sandbox app uses sandbox-api.digikey.com.
const defaultBaseURL = "https://api.digikey.com"

// ErrNotConfigured is returned when no Digi-Key credentials are set.
var ErrNotConfigured = errors.New("digikey enrichment not configured")

// Credentials are resolved fresh on demand (from DB settings, falling back to
// env) so the user can enter them in the UI without a restart. Locale controls
// the site/currency the catalogue and pricing are returned for.
type Credentials struct {
	ClientID     string
	ClientSecret string
	BaseURL      string // empty → production
	Site         string // ShipTo + locale site, e.g. "CA"
	Language     string // e.g. "en"
	Currency     string // e.g. "CAD"
}

// CredsFunc resolves the current credentials.
type CredsFunc func(ctx context.Context) Credentials

type Provider struct {
	creds CredsFunc
	http  *http.Client

	mu       sync.Mutex
	token    string
	tokenKey string // client_id the cached token was minted for
	tokenExp time.Time
}

func New(creds CredsFunc) *Provider {
	return &Provider{
		creds: creds,
		http:  &http.Client{Timeout: 20 * time.Second},
	}
}

// Name is the stable provider id.
func (p *Provider) Name() string { return "digikey" }

// Label is the human-facing provider name.
func (p *Provider) Label() string { return "Digi-Key" }

func (c Credentials) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

// Configured reports whether credentials are currently present.
func (p *Provider) Configured(ctx context.Context) bool {
	c := p.creds(ctx)
	return c.ClientID != "" && c.ClientSecret != ""
}

// accessToken returns a cached bearer token, refreshing it near expiry or when
// the credentials have changed. Digi-Key tokens last ~30 minutes.
func (p *Provider) accessToken(ctx context.Context) (string, error) {
	c := p.creds(ctx)
	if c.ClientID == "" || c.ClientSecret == "" {
		return "", ErrNotConfigured
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && p.tokenKey == c.ClientID && time.Now().Before(p.tokenExp.Add(-30*time.Second)) {
		return p.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("digikey token request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("digikey token status %d", res.StatusCode)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	p.token = body.AccessToken
	p.tokenKey = c.ClientID
	p.tokenExp = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return p.token, nil
}

// Ping validates the credentials by minting an access token. Token minting does
// NOT count as a product lookup, so this is a free credential check.
func (p *Provider) Ping(ctx context.Context) error {
	_, err := p.accessToken(ctx)
	return err
}

// Enrich looks up an MPN via Product Information V4 KeywordSearch and returns
// normalized part data. Prefers an exact manufacturer-part-number match.
func (p *Provider) Enrich(ctx context.Context, mpn string) (*models.EnrichedPart, error) {
	token, err := p.accessToken(ctx) // returns ErrNotConfigured if no creds
	if err != nil {
		return nil, err
	}
	c := p.creds(ctx)

	reqBody, _ := json.Marshal(map[string]any{
		"Keywords": mpn,
		"Limit":    10,
		"Offset":   0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/products/v4/search/keyword", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-DIGIKEY-Client-Id", c.ClientID)
	if c.Site != "" {
		req.Header.Set("X-DIGIKEY-Locale-Site", c.Site)
		req.Header.Set("X-DIGIKEY-Locale-ShipToCountry", c.Site)
	}
	if c.Language != "" {
		req.Header.Set("X-DIGIKEY-Locale-Language", c.Language)
	}
	if c.Currency != "" {
		req.Header.Set("X-DIGIKEY-Locale-Currency", c.Currency)
	}

	res, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("digikey search: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil // no match
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("digikey search status %d", res.StatusCode)
	}

	var body struct {
		Products     []dkProduct `json:"Products"`
		ExactMatches []dkProduct `json:"ExactMatches"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}

	prod := pickMatch(mpn, body.ExactMatches, body.Products)
	if prod == nil {
		return nil, nil // no match
	}
	return mapProduct(*prod, c.Currency), nil
}

// pickMatch prefers an exact-match result, then a product whose MPN equals the
// query (normalized), then the first product returned.
func pickMatch(mpn string, exact, products []dkProduct) *dkProduct {
	if len(exact) > 0 {
		return &exact[0]
	}
	want := normalizeMPN(mpn)
	for i := range products {
		if normalizeMPN(products[i].ManufacturerProductNumber) == want {
			return &products[i]
		}
	}
	if len(products) > 0 {
		return &products[0]
	}
	return nil
}

func normalizeMPN(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ── Digi-Key V4 response shapes (only the fields we map) ──────────────────────

type dkProduct struct {
	Description struct {
		ProductDescription  string `json:"ProductDescription"`
		DetailedDescription string `json:"DetailedDescription"`
	} `json:"Description"`
	Manufacturer struct {
		Name string `json:"Name"`
	} `json:"Manufacturer"`
	ManufacturerProductNumber string `json:"ManufacturerProductNumber"`
	DatasheetURL              string `json:"DatasheetUrl"`
	PhotoURL                  string `json:"PhotoUrl"`
	ProductURL                string `json:"ProductUrl"`
	Category                  struct {
		Name string `json:"Name"`
	} `json:"Category"`
	ProductVariations []struct {
		DigiKeyProductNumber string `json:"DigiKeyProductNumber"`
		PackageType          struct {
			Name string `json:"Name"`
		} `json:"PackageType"`
		StandardPricing []struct {
			BreakQuantity int     `json:"BreakQuantity"`
			UnitPrice     float64 `json:"UnitPrice"`
		} `json:"StandardPricing"`
	} `json:"ProductVariations"`
	Parameters []struct {
		ParameterText string `json:"ParameterText"`
		ValueText     string `json:"ValueText"`
	} `json:"Parameters"`
}

func mapProduct(p dkProduct, currency string) *models.EnrichedPart {
	if currency == "" {
		currency = "USD"
	}
	out := &models.EnrichedPart{
		MPN:          p.ManufacturerProductNumber,
		Description:  p.Description.ProductDescription,
		Manufacturer: p.Manufacturer.Name,
		Category:     p.Category.Name,
		DatasheetURL: p.DatasheetURL,
		ImageURL:     p.PhotoURL,
		Source:       "digikey",
	}

	for _, param := range p.Parameters {
		name := strings.TrimSpace(param.ParameterText)
		val := strings.TrimSpace(param.ValueText)
		if name == "" || val == "" || strings.EqualFold(val, "-") {
			continue
		}
		out.Parameters = append(out.Parameters, models.EnrichedParameter{Name: name, Value: val})
		switch strings.ToLower(name) {
		case "package / case", "supplier device package":
			if out.Package == "" {
				out.Package = val
			}
		}
	}

	// Each packaging variation is its own Digi-Key SKU with its own price breaks.
	for _, v := range p.ProductVariations {
		if v.DigiKeyProductNumber == "" {
			continue
		}
		sup := models.EnrichedSupplier{Name: "Digi-Key", SKU: v.DigiKeyProductNumber, URL: p.ProductURL, Packaging: v.PackageType.Name}
		for _, b := range v.StandardPricing {
			sup.Prices = append(sup.Prices, models.PriceBreak{
				Quantity: float64(b.BreakQuantity),
				Price:    b.UnitPrice,
				Currency: currency,
			})
		}
		out.Suppliers = append(out.Suppliers, sup)
		if out.Package == "" && v.PackageType.Name != "" {
			out.Package = v.PackageType.Name
		}
	}

	// Name is finalized server-side by the handler's deriveName from category +
	// parameters; seed it with the description as a sensible fallback.
	out.Name = out.Description
	return out
}
