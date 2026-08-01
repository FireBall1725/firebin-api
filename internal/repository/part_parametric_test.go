// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository_test

import (
	"context"
	"testing"

	"github.com/firelabsca/firebin-api/internal/db"
	"github.com/firelabsca/firebin-api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSearchParametric exercises specification search against a real database.
//
// The fixture is deliberately the shape that defeats the existing catalogue
// search: parts named after one spec, packaged in another column, with the value
// that matters living in part_parameters. The 100 kΩ and 100 Ω rows are the pair
// this whole feature turns on — they differ only by a prefix stored as text, so
// a naive query returns both for either question.
//
// Needs a disposable Postgres via DATABASE_URL and TRUNCATEs parts, so it skips
// when that is unset. CI provides one; do not point it at real data.
func TestSearchParametric(t *testing.T) {
	url := dbURL(t)
	ctx := context.Background()

	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Only parts, which cascades to part_parameters. parameter_templates is
	// seeded by migration 000003 and shared with other tests in this package, so
	// truncating it here failed one of them from a distance.
	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE parts CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	// Get-or-create by name, since these two are usually already seeded. The
	// no-op SET is what makes RETURNING fire on the conflict path.
	template := func(name string) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx,
			`INSERT INTO parameter_templates (name) VALUES ($1)
			 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, name).Scan(&id)
		if err != nil {
			t.Fatalf("template %q: %v", name, err)
		}
		return id
	}
	tmpl := map[string]string{
		"Resistance":  template("Resistance"),
		"Capacitance": template("Capacitance"),
	}

	// name, package, parameter, value, units
	fixtures := []struct {
		id, name, pkg, tmpl, value, unit string
	}{
		{"cccccccc-0000-0000-0000-0000000000b1", "100 kΩ Resistor", "0603 (1608 Metric)", "Resistance", "100", "kΩ"},
		{"cccccccc-0000-0000-0000-0000000000b2", "100 Ω Resistor", "0603 (1608 Metric)", "Resistance", "100", "Ω"},
		{"cccccccc-0000-0000-0000-0000000000b3", "220 Ω Resistor", "0805 (2012 Metric)", "Resistance", "220", "Ω"},
		{"cccccccc-0000-0000-0000-0000000000b4", "100 nF Capacitor", "0603 (1608 Metric)", "Capacitance", "100", "nF"},
	}
	for _, f := range fixtures {
		mustExec(t, pool, ctx,
			`INSERT INTO parts (id, name, package) VALUES ($1, $2, $3)`, f.id, f.name, f.pkg)
		mustExec(t, pool, ctx,
			`INSERT INTO part_parameters (part_id, template_id, value, units)
			 VALUES ($1, $2, $3, $4)`,
			f.id, tmpl[f.tmpl], f.value, f.unit)
	}

	repo := repository.NewPartRepo(pool)
	names := func(t *testing.T, opts repository.ParametricOptions) []string {
		t.Helper()
		got, err := repo.SearchParametric(ctx, opts)
		if err != nil {
			t.Fatalf("SearchParametric: %v", err)
		}
		out := []string{}
		for _, p := range got {
			out = append(out, p.Name)
		}
		return out
	}
	only := func(t *testing.T, got []string, want string) {
		t.Helper()
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %v, want exactly [%s]", got, want)
		}
	}

	// The failure this feature exists to prevent: the prefix is not decoration.
	t.Run("prefix separates 100 ohm from 100 kilohm", func(t *testing.T) {
		only(t, names(t, repository.ParametricOptions{Value: "100 ohm"}), "100 Ω Resistor")
		only(t, names(t, repository.ParametricOptions{Value: "100k"}), "100 kΩ Resistor")
	})

	// The unit disambiguates two parts whose magnitude is identical.
	t.Run("unit separates 100 nF from 100 ohm", func(t *testing.T) {
		only(t, names(t, repository.ParametricOptions{Value: "100nF"}), "100 nF Capacitor")
	})

	// Package is its own column, so it must be its own filter. The catalogue
	// search cannot do this at all.
	t.Run("package filters by substring", func(t *testing.T) {
		got := names(t, repository.ParametricOptions{Package: "0603"})
		if len(got) != 3 {
			t.Errorf("got %v, want the three 0603 parts", got)
		}
		only(t, names(t, repository.ParametricOptions{Package: "0805"}), "220 Ω Resistor")
	})

	// The headline question that could not be asked before this existed.
	t.Run("package and value together", func(t *testing.T) {
		if got := names(t, repository.ParametricOptions{Package: "0603", Value: "220 ohm"}); len(got) != 0 {
			t.Errorf("got %v, want none: no 0603 220 Ω is stocked", got)
		}
		only(t, names(t, repository.ParametricOptions{Package: "0805", Value: "220 ohm"}), "220 Ω Resistor")
	})

	t.Run("parameter name narrows the value", func(t *testing.T) {
		got := names(t, repository.ParametricOptions{Parameter: "Capacitance", Value: "100"})
		only(t, got, "100 nF Capacitor")
	})

	// A bare number means the magnitude a person reads on the part, so it spans
	// units on purpose. Documented here because it is a design choice, not a bug.
	t.Run("bare number matches every unit", func(t *testing.T) {
		if got := names(t, repository.ParametricOptions{Value: "100"}); len(got) != 3 {
			t.Errorf("got %v, want all three parts whose value reads 100", got)
		}
	})

	t.Run("result reports which parameter matched", func(t *testing.T) {
		got, err := repo.SearchParametric(ctx, repository.ParametricOptions{Value: "220 ohm"})
		if err != nil {
			t.Fatalf("SearchParametric: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d results, want 1", len(got))
		}
		if len(got[0].Matched) != 1 || got[0].Matched[0].TemplateName != "Resistance" {
			t.Errorf("matched %+v, want the Resistance parameter", got[0].Matched)
		}
		if len(got[0].Parameters) == 0 {
			t.Error("the full parameter list should come back too, to avoid a fetch per candidate")
		}
	})

	// No value filter is not the same as a value filter that matches nothing.
	t.Run("no filters returns everything", func(t *testing.T) {
		if got := names(t, repository.ParametricOptions{}); len(got) != 4 {
			t.Errorf("got %v, want all four parts", got)
		}
	})
}
