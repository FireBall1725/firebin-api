// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"testing"

	"github.com/firelabsca/firebin-api/internal/models"
)

func TestDeriveName(t *testing.T) {
	cases := []struct {
		name string
		part models.EnrichedPart
		want string
	}{
		{
			// Regression: a Sullins 50-pin header whose description trails with the
			// 105°C temp rating. The last comma-clause is a spec, not a type, so we
			// must fall through to the category rather than name it "105 C".
			name: "connector trailing spec falls to category",
			part: models.EnrichedPart{
				Description: `Male, 50 C, Straight, .050" CC; 3.00mm Head/2.30mm Tail, No Mt; Nylon 9T, Brass, 105 C`,
				Category:    "Card Edge Connectors",
			},
			want: "Card Edge Connector",
		},
		{
			// The last clause genuinely is the type name — keep taking it.
			name: "type name in last clause",
			part: models.EnrichedPart{
				Description: "5.5 mm x 2.1 mm, Panel Mount, Dc Power Jack Connector",
				Category:    "Barrel Power Connectors",
			},
			want: "DC Power Jack Connector",
		},
		{
			name: "resistance value builds passive name",
			part: models.EnrichedPart{
				Category:   "Chip Resistor - Surface Mount",
				Parameters: []models.EnrichedParameter{{Name: "Resistance", Value: "22", Units: "Ω"}},
			},
			want: "22 Ω Resistor",
		},
		{
			// No usable comma-clause, no value: singularized category.
			name: "category fallback",
			part: models.EnrichedPart{
				Description: "3.00mm",
				Category:    "Tactile Switches",
			},
			want: "Tactile Switch",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deriveName(&c.part); got != c.want {
				t.Errorf("deriveName() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestLooksLikeType(t *testing.T) {
	yes := []string{"Power Jack Connector", "Header", "Card Edge Connector", "Tactile Switch"}
	no := []string{"105 C", "3.00mm", "50 C", "9T", "No Mt", ""}
	for _, s := range yes {
		if !looksLikeType(s) {
			t.Errorf("looksLikeType(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if looksLikeType(s) {
			t.Errorf("looksLikeType(%q) = true, want false", s)
		}
	}
}
