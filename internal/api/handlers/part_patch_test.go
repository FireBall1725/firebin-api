// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"encoding/json"
	"testing"

	"github.com/firelabsca/firebin-api/internal/models"
)

// PATCH used to rebuild the whole part from the request and write every column,
// so any field a client did not send was overwritten with its zero value. Both
// real clients were losing data through it, in different columns and in opposite
// directions, and neither noticed. These tests pin the fix.

func ptr[T any](v T) *T { return &v }

// storedPart is a part with every field a client might fail to mention already
// populated, so anything dropped shows up.
func storedPart() *models.Part {
	return &models.Part{
		Name:           "10k Resistor",
		Keywords:       ptr("resistor 0603 thick film"),
		Barcode:        ptr("BIN-A4-0012"),
		KicadSymbol:    ptr("Device:R"),
		KicadFootprint: ptr("Resistor_SMD:R_0603_1608Metric"),
		Description:    ptr("chip resistor"),
		IsTemplate:     true,
		IsComponent:    true,
		IsPurchaseable: true,
		IsTrackable:    true,
		MinimumStock:   25,
	}
}

func patch(t *testing.T, cur *models.Part, body string) *models.Part {
	t.Helper()
	var req partRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &present); err != nil {
		t.Fatalf("decode key set: %v", err)
	}
	applyPartPatch(cur, &req, present)
	return cur
}

// The shape the web edit form actually sends. It has no control for keywords,
// barcode or default location, so it never mentions them; before the fix each
// edit silently erased all three.
func TestWebEditKeepsFieldsItDoesNotSend(t *testing.T) {
	got := patch(t, storedPart(), `{
		"name": "10k Resistor",
		"category_id": null,
		"ipn": "FB-ABCD1234",
		"kicad_symbol": "Device:R",
		"kicad_footprint": "Resistor_SMD:R_0603_1608Metric",
		"description": "chip resistor",
		"minimum_stock": 25,
		"parameters": []
	}`)

	if got.Keywords == nil || *got.Keywords != "resistor 0603 thick film" {
		t.Errorf("keywords = %v, want them left alone", got.Keywords)
	}
	if got.Barcode == nil || *got.Barcode != "BIN-A4-0012" {
		t.Errorf("barcode = %v, want it left alone", got.Barcode)
	}
	// The form has no template control, so omitting the field must not demote.
	if !got.IsTemplate {
		t.Error("is_template was cleared by a request that never mentioned it")
	}
}

// The shape MCP update_part sends. Its request struct predates the KiCad columns,
// so it never mentions them; before the fix every call through it erased the
// symbol and footprint mapping.
func TestMCPUpdateKeepsKicadMapping(t *testing.T) {
	got := patch(t, storedPart(), `{
		"name": "10k Resistor",
		"keywords": "resistor 0603 thick film",
		"barcode": "BIN-A4-0012",
		"is_template": true,
		"is_assembly": false,
		"minimum_stock": 25,
		"parameters": []
	}`)

	if got.KicadSymbol == nil || *got.KicadSymbol != "Device:R" {
		t.Errorf("kicad_symbol = %v, want it left alone", got.KicadSymbol)
	}
	if got.KicadFootprint == nil || *got.KicadFootprint != "Resistor_SMD:R_0603_1608Metric" {
		t.Errorf("kicad_footprint = %v, want it left alone", got.KicadFootprint)
	}
}

// Absent and null must stay distinguishable, or clearing a field becomes
// impossible. Pointer fields alone cannot tell them apart; the key set can.
func TestExplicitNullClearsButAbsenceDoesNot(t *testing.T) {
	cleared := patch(t, storedPart(), `{"description": null}`)
	if cleared.Description != nil {
		t.Errorf("description = %v, want nil: null was sent explicitly", cleared.Description)
	}

	kept := patch(t, storedPart(), `{"name": "10k Resistor"}`)
	if kept.Description == nil {
		t.Error("description was cleared by a request that never mentioned it")
	}
}

// These three have no request field at all. The old code hardcoded two to true
// and left the third at its zero value on every write, so they were reset on
// every edit. Patching must not touch them.
func TestFlagsWithNoRequestFieldAreUntouched(t *testing.T) {
	got := patch(t, storedPart(), `{"name": "renamed"}`)
	if !got.IsComponent || !got.IsPurchaseable || !got.IsTrackable {
		t.Errorf("component=%v purchaseable=%v trackable=%v, want all preserved",
			got.IsComponent, got.IsPurchaseable, got.IsTrackable)
	}
}

func TestReferenceOnlyRoundTrips(t *testing.T) {
	marked := patch(t, storedPart(), `{"reference_only": true}`)
	if !marked.ReferenceOnly {
		t.Error("reference_only was not applied")
	}
	// And a client that does not know the field cannot clear it.
	kept := patch(t, marked, `{"name": "renamed"}`)
	if !kept.ReferenceOnly {
		t.Error("reference_only was cleared by a request that never mentioned it")
	}
}
