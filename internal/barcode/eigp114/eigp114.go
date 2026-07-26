// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package eigp114 parses the 2D Data Matrix codes printed on electronics
// distributor bags (Digi-Key, Mouser, …). These follow ECIA EIGP 114, which
// wraps ANSI MH10.8.2 Data Identifiers inside an ISO/IEC 15434 "Format 06"
// envelope. The two fields we care about are 1P (manufacturer part number) and
// Q (quantity); the rest is captured best-effort.
package eigp114

import (
	"strconv"
	"strings"
)

// Control characters used by the ISO 15434 envelope.
const (
	rs  = '\x1e' // record separator
	gs  = '\x1d' // group separator (between fields)
	eot = '\x04' // end of transmission
)

// escSeparators normalises the VT function-key escape sequences some keyboard-
// wedge scanners substitute for the raw separators (GS = F8/F7, RS = F9) back
// to the real control characters. The web client already does this, but a
// wedge string could reach the API by another path, so we repeat it defensively.
var escSeparators = strings.NewReplacer(
	"\x1b[19~", string(gs), // F8
	"\x1b[18~", string(gs), // F7
	"\x1b[20~", string(rs), // F9
)

// Parsed holds the decoded fields of a distributor label.
type Parsed struct {
	MPN             string            `json:"mpn"`               // 1P — manufacturer part number
	Quantity        int               `json:"quantity"`          // Q
	CustomerPart    string            `json:"customer_part"`     // P — distributor's own SKU (e.g. Digi-Key -ND)
	DistributorPart string            `json:"distributor_part"`  // 30P — alternate distributor SKU
	SalesOrder      string            `json:"sales_order"`       // 1K
	Invoice         string            `json:"invoice"`           // 10K
	PackingList     string            `json:"packing_list"`      // 11K
	CustomerPO      string            `json:"customer_po"`       // K
	DateCode        string            `json:"date_code"`         // 9D (YYWW)
	LotCode         string            `json:"lot_code"`          // 1T
	CountryOfOrigin string            `json:"country_of_origin"` // 4L
	Distributor     string            `json:"distributor"`       // best-effort guess
	Fields          map[string]string `json:"fields"`            // every DI → value, for anything unmapped
}

// dataIdentifiers lists the DIs we recognise, longest-first so the greedy
// prefix match picks "1P" over "P", "10K" over "1K"/"K", etc.
var dataIdentifiers = []string{
	"30P", "10K", "11K", "12Z", "13Z", "20Z",
	"1P", "1K", "1T", "4L", "9D",
	"P", "Q", "K", "V", "S", "T", "D", "L", "E",
}

// IsEIGP reports whether a decoded string looks like an EIGP 114 / ISO 15434
// envelope (starts with the "[)>" message header or contains group separators).
func IsEIGP(code string) bool {
	return strings.HasPrefix(code, "[)>") || strings.ContainsRune(code, gs) ||
		strings.Contains(code, "\x1b[19~") || strings.Contains(code, "\x1b[18~")
}

// Parse decodes an EIGP 114 string. It is lenient: it tolerates a missing
// message header and recovers each GS-delimited field independently, so a
// slightly-noncompliant label (Mouser/Arrow vary) still yields what it can.
func Parse(code string) *Parsed {
	p := &Parsed{Fields: map[string]string{}}

	body := escSeparators.Replace(code)
	// Strip the message header: "[)>" RS "06" GS  (any of these may be absent).
	body = strings.TrimPrefix(body, "[)>")
	body = strings.TrimLeft(body, string(rs))
	body = strings.TrimPrefix(body, "06")
	// Strip trailer: RS EOT.
	if i := strings.IndexRune(body, eot); i >= 0 {
		body = body[:i]
	}
	body = strings.Trim(body, string(rs)+string(gs))

	for _, token := range strings.Split(body, string(gs)) {
		token = strings.Trim(token, string(rs)+"\r\n ")
		if token == "" {
			continue
		}
		di, value := splitDI(token)
		if di == "" {
			continue
		}
		p.Fields[di] = value
		switch di {
		case "1P":
			p.MPN = value
		case "P":
			p.CustomerPart = value
		case "30P":
			p.DistributorPart = value
		case "Q":
			p.Quantity = parseQty(value)
		case "1K":
			p.SalesOrder = value
		case "10K":
			p.Invoice = value
		case "11K":
			p.PackingList = value
		case "K":
			p.CustomerPO = value
		case "9D":
			p.DateCode = value
		case "1T":
			if value != "N/T" { // "Not Traceable"
				p.LotCode = value
			}
		case "4L":
			p.CountryOfOrigin = value
		}
	}

	p.Distributor = guessDistributor(p)
	return p
}

// splitDI returns the longest matching Data Identifier prefix of token and the
// remaining value.
func splitDI(token string) (di, value string) {
	for _, cand := range dataIdentifiers {
		if strings.HasPrefix(token, cand) {
			return cand, token[len(cand):]
		}
	}
	return "", ""
}

func parseQty(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// guessDistributor makes a best-effort guess from label shape so the caller can
// pick the right enrichment provider. Not authoritative.
func guessDistributor(p *Parsed) string {
	switch {
	case strings.HasSuffix(p.CustomerPart, "-ND"), strings.HasPrefix(p.SalesOrder, "SO"):
		return "digikey"
	case strings.HasPrefix(p.CustomerPart, "0") && len(p.CustomerPart) >= 10:
		// Mouser SKUs are often like 581-... but vary; leave loose.
		return "mouser"
	default:
		return ""
	}
}
