// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package mouser enriches an MPN using the Mouser Search API v1.0, authenticated
// with a single API key. Free to request; the published limits are 30 calls per
// minute and 1000 per day, which is why this provider sits behind Digi-Key in the
// default chain rather than in front of it.
package mouser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/firelabsca/firebin-api/internal/models"
)

const defaultBaseURL = "https://api.mouser.com/api/v1.0"

// ErrNotConfigured is returned when no Mouser API key is set.
var ErrNotConfigured = errors.New("mouser enrichment not configured")

// Credentials are resolved fresh on demand (from DB settings, falling back to
// env) so the key can be entered in the UI without a restart.
//
// Mouser has no client id and no OAuth step: one key, passed as a query
// parameter. Currency is the instance-wide preference, used only as a fallback
// when a price break arrives without its own currency code.
type Credentials struct {
	APIKey   string
	BaseURL  string // empty → production
	Currency string // e.g. "CAD"
}

// CredsFunc resolves the current credentials.
type CredsFunc func(ctx context.Context) Credentials

type Provider struct {
	creds CredsFunc
	http  *http.Client
}

func New(creds CredsFunc) *Provider {
	return &Provider{
		creds: creds,
		http:  &http.Client{Timeout: 20 * time.Second},
	}
}

// Name is the stable provider id.
func (p *Provider) Name() string { return "mouser" }

// Label is the human-facing provider name.
func (p *Provider) Label() string { return "Mouser" }

func (c Credentials) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

// Configured reports whether an API key is currently present.
func (p *Provider) Configured(ctx context.Context) bool {
	return p.creds(ctx).APIKey != ""
}

// Ping validates the key.
//
// Unlike Digi-Key, Mouser has no token endpoint, so there is no way to check a
// key without making a real search. This therefore spends one of the 1000 daily
// calls. It is only reached from the explicit "Test" button in settings, never
// automatically, so the cost is one call per time the user asks.
func (p *Provider) Ping(ctx context.Context) error {
	c := p.creds(ctx)
	if c.APIKey == "" {
		return ErrNotConfigured
	}
	// A part number that will not match is fine: an accepted key returns an
	// empty result set, a bad key returns an error envelope.
	_, err := p.search(ctx, c, "FIREBIN-PING")
	return err
}

// Enrich looks up an MPN and returns normalized part data, or (nil, nil) when
// there is no match.
func (p *Provider) Enrich(ctx context.Context, mpn string) (*models.EnrichedPart, error) {
	c := p.creds(ctx)
	if c.APIKey == "" {
		return nil, ErrNotConfigured
	}
	parts, err := p.search(ctx, c, mpn)
	if err != nil {
		return nil, err
	}
	prod := pickMatch(mpn, parts)
	if prod == nil {
		return nil, nil
	}
	return mapPart(*prod, c.Currency), nil
}

// search calls the part-number endpoint. Despite the field being named
// mouserPartNumber, this endpoint matches manufacturer part numbers too, which
// is what makes it usable for MPN enrichment.
func (p *Provider) search(ctx context.Context, c Credentials, term string) ([]msPart, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"SearchByPartRequest": map[string]any{
			"mouserPartNumber":  term,
			"partSearchOptions": "Exact",
		},
	})

	endpoint := c.baseURL() + "/search/partnumber?apiKey=" + url.QueryEscape(c.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	res, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mouser search: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, errors.New("mouser rate limit reached (30/minute, 1000/day)")
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mouser search status %d", res.StatusCode)
	}

	var body struct {
		Errors []struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Errors"`
		SearchResults struct {
			NumberOfResult int      `json:"NumberOfResult"`
			Parts          []msPart `json:"Parts"`
		} `json:"SearchResults"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	// Mouser reports a bad key or a malformed request as an Errors array inside
	// a 200, so the status code alone is not enough to call this a success.
	if len(body.Errors) > 0 {
		e := body.Errors[0]
		msg := strings.TrimSpace(e.Message)
		if msg == "" {
			msg = e.Code
		}
		return nil, fmt.Errorf("mouser: %s", msg)
	}
	return body.SearchResults.Parts, nil
}

// pickMatch prefers a part whose MPN equals the query once normalized, and
// otherwise takes the first result.
func pickMatch(mpn string, parts []msPart) *msPart {
	want := normalizeMPN(mpn)
	for i := range parts {
		if normalizeMPN(parts[i].ManufacturerPartNumber) == want {
			return &parts[i]
		}
	}
	if len(parts) > 0 {
		return &parts[0]
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

// ── Mouser response shapes (only the fields we map) ──────────────────────────

type msPart struct {
	MouserPartNumber       string `json:"MouserPartNumber"`
	ManufacturerPartNumber string `json:"ManufacturerPartNumber"`
	Manufacturer           string `json:"Manufacturer"`
	Description            string `json:"Description"`
	DataSheetURL           string `json:"DataSheetUrl"`
	ImagePath              string `json:"ImagePath"`
	Category               string `json:"Category"`
	ProductDetailURL       string `json:"ProductDetailUrl"`
	Min                    string `json:"Min"`
	PriceBreaks            []struct {
		Quantity int    `json:"Quantity"`
		Price    string `json:"Price"`
		Currency string `json:"Currency"`
	} `json:"PriceBreaks"`
	ProductAttributes []struct {
		AttributeName  string `json:"AttributeName"`
		AttributeValue string `json:"AttributeValue"`
	} `json:"ProductAttributes"`
}

func mapPart(p msPart, currency string) *models.EnrichedPart {
	if currency == "" {
		currency = "USD"
	}
	out := &models.EnrichedPart{
		MPN:          p.ManufacturerPartNumber,
		Description:  p.Description,
		Manufacturer: p.Manufacturer,
		Category:     p.Category,
		DatasheetURL: p.DataSheetURL,
		ImageURL:     p.ImagePath,
		Source:       "mouser",
	}

	for _, a := range p.ProductAttributes {
		name := strings.TrimSpace(a.AttributeName)
		val := strings.TrimSpace(a.AttributeValue)
		if name == "" || val == "" || val == "-" {
			continue
		}
		out.Parameters = append(out.Parameters, models.EnrichedParameter{Name: name, Value: val})
		// Mouser's attribute naming differs from Digi-Key's; both spellings show
		// up depending on the manufacturer's data.
		switch strings.ToLower(name) {
		case "package / case", "supplier device package", "mounting style", "package":
			if out.Package == "" && !strings.EqualFold(name, "mounting style") {
				out.Package = val
			}
		}
	}

	sup := models.EnrichedSupplier{
		Name: "Mouser",
		SKU:  p.MouserPartNumber,
		URL:  p.ProductDetailURL,
	}
	// Min is Mouser's minimum order quantity, and like its prices it arrives as
	// a display string rather than a number. It has always been parsed off the
	// wire and never read, which is part of why supplier_parts.moq is empty for
	// every row.
	if moq, ok := parseQuantity(p.Min); ok {
		sup.MOQ = &moq
	}
	for _, b := range p.PriceBreaks {
		price, ok := parsePrice(b.Price)
		if !ok {
			continue
		}
		cur := strings.TrimSpace(b.Currency)
		if cur == "" {
			cur = currency
		}
		sup.Prices = append(sup.Prices, models.PriceBreak{
			Quantity: float64(b.Quantity),
			Price:    price,
			Currency: cur,
		})
	}
	if sup.SKU != "" {
		out.Suppliers = append(out.Suppliers, sup)
	}

	// Name is finalized server-side by the handler's deriveName; seed it with
	// the description as a sensible fallback, matching the other providers.
	out.Name = out.Description
	return out
}

// parsePrice turns Mouser's formatted price string into a number.
//
// Mouser returns price as a display string, not a number, and the formatting
// follows the account's locale: "$0.42", "0,42 €", "US$ 1.23". So the symbol has
// to go, and the decimal separator can be either a dot or a comma. Returns
// ok=false for anything that does not yield a number, so a single unparseable
// break is dropped rather than silently becoming 0.00 and looking free.
func parsePrice(s string) (float64, bool) {
	var digits strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' || r == '-' {
			digits.WriteRune(r)
		}
	}
	t := strings.TrimSpace(digits.String())
	if t == "" {
		return 0, false
	}

	lastDot, lastComma := strings.LastIndex(t, "."), strings.LastIndex(t, ",")
	switch {
	case lastDot >= 0 && lastComma >= 0:
		// Both present: the rightmost is the decimal separator, the other groups
		// thousands. "1.234,56" (de) and "1,234.56" (en) both land correctly.
		if lastComma > lastDot {
			t = strings.ReplaceAll(t, ".", "")
			t = strings.Replace(t, ",", ".", 1)
		} else {
			t = strings.ReplaceAll(t, ",", "")
		}
	case lastComma >= 0:
		// Only commas. Treat as a decimal separator when it looks like one, and
		// as thousands grouping otherwise: "0,42" is 0.42 but "1,234" is 1234.
		if len(t)-lastComma-1 <= 2 && strings.Count(t, ",") == 1 {
			t = strings.Replace(t, ",", ".", 1)
		} else {
			t = strings.ReplaceAll(t, ",", "")
		}
	}

	v, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseQuantity turns a Mouser quantity string into a number.
//
// Separate from parsePrice: that one strips currency symbols and copes with
// locale-swapped separators, which is the wrong set of rules for a count. A
// quantity is plainer, but it can still arrive thousands-separated ("1,000"),
// so commas and spaces come out before parsing.
func parseQuantity(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.NewReplacer(",", "", " ", "", "\u00a0", "").Replace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}
