// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package nexar enriches an MPN using the Nexar API (Octopart data) over
// GraphQL, authenticated with an OAuth2 client-credentials app.
package nexar

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

const (
	tokenURL   = "https://identity.nexar.com/connect/token"
	graphqlURL = "https://api.nexar.com/graphql"
)

// ErrNotConfigured is returned when no Nexar credentials are set.
var ErrNotConfigured = errors.New("nexar enrichment not configured")

// Credentials are resolved fresh on demand (from DB settings, falling back to
// env) so the user can enter them in the UI without a restart.
type Credentials struct {
	ClientID     string
	ClientSecret string
	Scope        string
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

// Configured reports whether credentials are currently present.
func (p *Provider) Configured(ctx context.Context) bool {
	c := p.creds(ctx)
	return c.ClientID != "" && c.ClientSecret != ""
}

// accessToken returns a cached bearer token, refreshing it when near expiry or
// when the credentials have changed.
func (p *Provider) accessToken(ctx context.Context) (string, error) {
	c := p.creds(ctx)
	if c.ClientID == "" || c.ClientSecret == "" {
		return "", ErrNotConfigured
	}
	if c.Scope == "" {
		c.Scope = "supply.domain"
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
		"scope":         {c.Scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("nexar token request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nexar token status %d", res.StatusCode)
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

const mpnQuery = `
query MpnSearch($mpn: String!) {
  supSearchMpn(q: $mpn, limit: 1) {
    results {
      part {
        mpn
        name
        shortDescription
        manufacturer { name }
        bestDatasheet { url }
        bestImage { url }
        category { name }
        specs { attribute { name shortname } displayValue units }
        sellers(authorizedOnly: false) {
          company { name }
          offers { sku moq prices { quantity price currency } }
        }
        similarParts {
          mpn
          shortDescription
          manufacturer { name }
        }
      }
    }
  }
}`

// Ping validates the credentials by minting an access token. Token minting does
// NOT count against the Nexar query quota, so this is a free credential check.
func (p *Provider) Ping(ctx context.Context) error {
	_, err := p.accessToken(ctx)
	return err
}

// Enrich looks up a manufacturer part number and returns normalized part data.
func (p *Provider) Enrich(ctx context.Context, mpn string) (*models.EnrichedPart, error) {
	token, err := p.accessToken(ctx) // returns ErrNotConfigured if no creds
	if err != nil {
		return nil, err
	}

	reqBody, _ := json.Marshal(map[string]any{
		"query":     mpnQuery,
		"variables": map[string]string{"mpn": mpn},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nexar graphql: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nexar graphql status %d", res.StatusCode)
	}

	var gql struct {
		Data struct {
			SupSearchMpn struct {
				Results []struct {
					Part nexarPart `json:"part"`
				} `json:"results"`
			} `json:"supSearchMpn"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(res.Body).Decode(&gql); err != nil {
		return nil, err
	}
	if len(gql.Errors) > 0 {
		return nil, fmt.Errorf("nexar: %s", gql.Errors[0].Message)
	}
	if len(gql.Data.SupSearchMpn.Results) == 0 {
		return nil, nil // no match
	}
	return mapPart(gql.Data.SupSearchMpn.Results[0].Part), nil
}

// ── Nexar GraphQL shapes ─────────────────────────────────────────────────────

type nexarPart struct {
	MPN              string `json:"mpn"`
	Name             string `json:"name"`
	ShortDescription string `json:"shortDescription"`
	Manufacturer     struct {
		Name string `json:"name"`
	} `json:"manufacturer"`
	BestDatasheet struct {
		URL string `json:"url"`
	} `json:"bestDatasheet"`
	BestImage struct {
		URL string `json:"url"`
	} `json:"bestImage"`
	Category struct {
		Name string `json:"name"`
	} `json:"category"`
	Specs []struct {
		Attribute struct {
			Name      string `json:"name"`
			Shortname string `json:"shortname"`
		} `json:"attribute"`
		DisplayValue string `json:"displayValue"`
		Units        string `json:"units"`
	} `json:"specs"`
	Sellers []struct {
		Company struct {
			Name string `json:"name"`
		} `json:"company"`
		Offers []struct {
			SKU    string  `json:"sku"`
			MOQ    int     `json:"moq"`
			Prices []struct {
				Quantity int     `json:"quantity"`
				Price    float64 `json:"price"`
				Currency string  `json:"currency"`
			} `json:"prices"`
		} `json:"offers"`
	} `json:"sellers"`
	SimilarParts []struct {
		MPN              string `json:"mpn"`
		ShortDescription string `json:"shortDescription"`
		Manufacturer     struct {
			Name string `json:"name"`
		} `json:"manufacturer"`
	} `json:"similarParts"`
}

func mapPart(p nexarPart) *models.EnrichedPart {
	out := &models.EnrichedPart{
		MPN:          p.MPN,
		Description:  p.ShortDescription,
		Manufacturer: p.Manufacturer.Name,
		Category:     p.Category.Name,
		DatasheetURL: p.BestDatasheet.URL,
		ImageURL:     p.BestImage.URL,
		Source:       "nexar",
	}

	var primaryValue string // e.g. "4.7 µF" — the headline spec for naming
	for _, s := range p.Specs {
		name := s.Attribute.Name
		if name == "" {
			name = s.Attribute.Shortname
		}
		// displayValue already includes units (e.g. "4.7 µF"), so don't
		// duplicate them into the Units field.
		out.Parameters = append(out.Parameters, models.EnrichedParameter{
			Name:  name,
			Value: s.DisplayValue,
		})
		switch strings.ToLower(name) {
		case "case/package", "case / package", "package / case", "package":
			if out.Package == "" {
				out.Package = s.DisplayValue
			}
		case "capacitance", "resistance", "inductance":
			if primaryValue == "" {
				primaryValue = s.DisplayValue
			}
		}
	}

	// Build a clean, human part name from the headline spec + singular category
	// ("4.7 µF" + "Ceramic Capacitors" → "4.7 µF Ceramic Capacitor"), falling
	// back to the manufacturer name/description. The user can edit it.
	if cat := singularize(p.Category.Name); primaryValue != "" && cat != "" {
		out.Name = primaryValue + " " + cat
	} else if p.ShortDescription != "" {
		out.Name = p.ShortDescription
	} else {
		out.Name = p.Name
	}

	for _, sel := range p.Sellers {
		for _, off := range sel.Offers {
			if off.SKU == "" {
				continue
			}
			sup := models.EnrichedSupplier{Name: sel.Company.Name, SKU: off.SKU}
			for _, pr := range off.Prices {
				sup.Prices = append(sup.Prices, models.PriceBreak{
					Quantity: float64(pr.Quantity),
					Price:    pr.Price,
					Currency: pr.Currency,
				})
			}
			out.Suppliers = append(out.Suppliers, sup)
		}
	}

	for i, sp := range p.SimilarParts {
		if i >= 12 {
			break
		}
		if sp.MPN == "" {
			continue
		}
		out.Alternatives = append(out.Alternatives, models.EnrichedAlt{
			MPN:          sp.MPN,
			Manufacturer: sp.Manufacturer.Name,
			Description:  sp.ShortDescription,
		})
	}
	return out
}

// singularize crudely drops a trailing plural "s" from a category so
// "Ceramic Capacitors" → "Ceramic Capacitor". Good enough for a suggested name
// the user can edit.
func singularize(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "ses") {
		return strings.TrimSuffix(s, "es")
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}
