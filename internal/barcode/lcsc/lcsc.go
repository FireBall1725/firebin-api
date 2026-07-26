// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package lcsc parses the QR codes LCSC prints on its bags. Unlike the ECIA
// EIGP 114 Data Matrix (see package eigp114), LCSC uses a brace-wrapped,
// comma-separated key:value list, e.g.
//
//	{pbn:PICK2305180048,on:GB2305180983,pc:C12368,pm:MCP2515-I/SO,qty:30,mc:,cc:1,pdi:81161401,hp:0,wc:ZH}
//
// The fields we care about: pm = manufacturer part number, pc = LCSC part code
// (the C-number), qty = quantity, on = order number.
package lcsc

import (
	"strconv"
	"strings"

	"github.com/firelabsca/firebin-api/internal/barcode/eigp114"
)

// IsLCSC reports whether a decoded string looks like an LCSC QR payload.
func IsLCSC(code string) bool {
	c := strings.TrimSpace(code)
	return strings.HasPrefix(c, "{") && strings.HasSuffix(c, "}") &&
		(strings.Contains(c, "pm:") || strings.Contains(c, "pc:"))
}

// Parse decodes an LCSC QR code into the shared Parsed shape so the scan flow
// treats it like any other distributor label.
func Parse(code string) *eigp114.Parsed {
	p := &eigp114.Parsed{Fields: map[string]string{}, Distributor: "lcsc"}
	body := strings.TrimSpace(code)
	body = strings.TrimPrefix(body, "{")
	body = strings.TrimSuffix(body, "}")
	for _, kv := range strings.Split(body, ",") {
		k, v, ok := strings.Cut(kv, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p.Fields[k] = v
		switch k {
		case "pm": // manufacturer part number
			p.MPN = v
		case "pc": // LCSC part code (C-number)
			p.CustomerPart = v
		case "qty":
			if n, err := strconv.Atoi(v); err == nil {
				p.Quantity = n
			}
		case "on": // order number
			p.SalesOrder = v
		}
	}
	return p
}
