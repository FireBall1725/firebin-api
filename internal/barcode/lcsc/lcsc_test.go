// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package lcsc

import "testing"

// TestParseRealLCSC uses the exact QR payload the user scanned off an LCSC bag
// (Microchip MCP2515-I/SO, LCSC C12368, qty 30).
func TestParseRealLCSC(t *testing.T) {
	code := "{pbn:PICK2305180048,on:GB2305180983,pc:C12368,pm:MCP2515-I/SO,qty:30,mc:,cc:1,pdi:81161401,hp:0,wc:ZH}"
	if !IsLCSC(code) {
		t.Fatal("expected the payload to be recognised as LCSC")
	}
	p := Parse(code)
	if p.MPN != "MCP2515-I/SO" {
		t.Errorf("MPN = %q, want MCP2515-I/SO", p.MPN)
	}
	if p.Quantity != 30 {
		t.Errorf("Quantity = %d, want 30", p.Quantity)
	}
	if p.CustomerPart != "C12368" {
		t.Errorf("CustomerPart = %q, want C12368", p.CustomerPart)
	}
	if p.SalesOrder != "GB2305180983" {
		t.Errorf("SalesOrder = %q, want GB2305180983", p.SalesOrder)
	}
	if p.Distributor != "lcsc" {
		t.Errorf("Distributor = %q, want lcsc", p.Distributor)
	}
	// mc: has an empty value — must not create a spurious field.
	if _, ok := p.Fields["mc"]; ok {
		t.Errorf("empty-valued key mc should be skipped")
	}
}

func TestIsLCSC(t *testing.T) {
	if IsLCSC("[)>\x1e06\x1d1PABC") {
		t.Error("an EIGP envelope must not be treated as LCSC")
	}
	if IsLCSC("MCP2515-I/SO") {
		t.Error("a bare MPN must not be treated as LCSC")
	}
	if !IsLCSC("{pm:ABC,qty:5}") {
		t.Error("expected a brace pm: payload to be LCSC")
	}
}
