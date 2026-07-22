// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package eigp114

import "testing"

const (
	tRS = "\x1e"
	tGS = "\x1d"
	tEOT = "\x04"
)

func TestParseDigiKeyLabel(t *testing.T) {
	// A realistic Digi-Key Data Matrix payload.
	code := "[)>" + tRS + "06" + tGS +
		"P296-1234-1-ND" + tGS +
		"1PSN74LVC1G08DBVR" + tGS +
		"K" + tGS +
		"1KSO12345678" + tGS +
		"10K87654321" + tGS +
		"11K12345678" + tGS +
		"4LUS" + tGS +
		"Q10" + tGS +
		"9D2340" + tGS +
		"1TN/T" + tRS + tEOT

	p := Parse(code)
	if p.MPN != "SN74LVC1G08DBVR" {
		t.Errorf("MPN = %q, want SN74LVC1G08DBVR", p.MPN)
	}
	if p.Quantity != 10 {
		t.Errorf("Quantity = %d, want 10", p.Quantity)
	}
	if p.CustomerPart != "296-1234-1-ND" {
		t.Errorf("CustomerPart = %q, want 296-1234-1-ND", p.CustomerPart)
	}
	if p.SalesOrder != "SO12345678" {
		t.Errorf("SalesOrder = %q, want SO12345678", p.SalesOrder)
	}
	if p.CountryOfOrigin != "US" {
		t.Errorf("CountryOfOrigin = %q, want US", p.CountryOfOrigin)
	}
	if p.DateCode != "2340" {
		t.Errorf("DateCode = %q, want 2340", p.DateCode)
	}
	if p.LotCode != "" {
		t.Errorf("LotCode = %q, want empty (N/T)", p.LotCode)
	}
	if p.Distributor != "digikey" {
		t.Errorf("Distributor = %q, want digikey", p.Distributor)
	}
}

// TestParseRealDigiKeyLabel uses the exact string decoded from a real Digi-Key
// bag (Samsung CL31A475KOHNNNE, 4.7uF 16V X5R 1206, qty 100).
func TestParseRealDigiKeyLabel(t *testing.T) {
	code := "[)>" + tRS + "06" + tGS +
		"P1276-3059-1-ND" + tGS +
		"1PCL31A475KOHNNNE" + tGS +
		"K" + tGS +
		"1K80301470" + tGS +
		"10K95945419" + tGS +
		"11K1" + tGS +
		"4LCN" + tGS +
		"Q100" + tGS +
		"11ZPICK" + tGS +
		"12Z3891145" + tGS +
		"13Z210599" + tGS +
		"20Z" + "000000000000000000"

	p := Parse(code)
	if p.MPN != "CL31A475KOHNNNE" {
		t.Errorf("MPN = %q, want CL31A475KOHNNNE", p.MPN)
	}
	if p.Quantity != 100 {
		t.Errorf("Quantity = %d, want 100", p.Quantity)
	}
	if p.CustomerPart != "1276-3059-1-ND" {
		t.Errorf("CustomerPart = %q, want 1276-3059-1-ND", p.CustomerPart)
	}
	if p.SalesOrder != "80301470" {
		t.Errorf("SalesOrder = %q, want 80301470", p.SalesOrder)
	}
	if p.Invoice != "95945419" {
		t.Errorf("Invoice = %q, want 95945419", p.Invoice)
	}
	if p.CountryOfOrigin != "CN" {
		t.Errorf("CountryOfOrigin = %q, want CN", p.CountryOfOrigin)
	}
	if p.Distributor != "digikey" {
		t.Errorf("Distributor = %q, want digikey", p.Distributor)
	}
}

func TestParseLenientNoHeader(t *testing.T) {
	// Minimal, header-less, MPN + qty only.
	code := "1PRC0603FR-071KL" + tGS + "Q5000"
	p := Parse(code)
	if p.MPN != "RC0603FR-071KL" {
		t.Errorf("MPN = %q, want RC0603FR-071KL", p.MPN)
	}
	if p.Quantity != 5000 {
		t.Errorf("Quantity = %d, want 5000", p.Quantity)
	}
}

func TestLongestPrefixMatch(t *testing.T) {
	// 10K must win over 1K/K, 1P over P.
	code := "10KINV99" + tGS + "1PABC" + tGS + "P123"
	p := Parse(code)
	if p.Invoice != "INV99" {
		t.Errorf("Invoice = %q, want INV99", p.Invoice)
	}
	if p.MPN != "ABC" {
		t.Errorf("MPN = %q, want ABC", p.MPN)
	}
	if p.CustomerPart != "123" {
		t.Errorf("CustomerPart = %q, want 123", p.CustomerPart)
	}
}

func TestIsEIGP(t *testing.T) {
	if !IsEIGP("[)>" + tRS + "06" + tGS + "1PABC") {
		t.Error("expected header form to be EIGP")
	}
	if !IsEIGP("1PABC" + tGS + "Q5") {
		t.Error("expected GS-containing form to be EIGP")
	}
	if IsEIGP("just-a-plain-mpn") {
		t.Error("plain string should not be EIGP")
	}
}
