// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

// pricePer1M is USD per million tokens, input and output.
type pricePer1M struct {
	InUSD  float64
	OutUSD float64
}

// Pricing tables are best-effort and go stale, which is why an unknown model
// reports "cost unknown" rather than a guess. A wrong number on a spend readout
// is worse than an honest blank, because the wrong number gets believed.
var anthropicPricing = map[string]pricePer1M{
	"claude-opus-5":    {InUSD: 5.00, OutUSD: 25.00},
	"claude-sonnet-5":  {InUSD: 3.00, OutUSD: 15.00},
	"claude-haiku-4-5": {InUSD: 1.00, OutUSD: 5.00},
}

var openAIPricing = map[string]pricePer1M{
	"gpt-4o":       {InUSD: 2.50, OutUSD: 10.00},
	"gpt-4o-mini":  {InUSD: 0.15, OutUSD: 0.60},
	"gpt-4.1":      {InUSD: 2.00, OutUSD: 8.00},
	"gpt-4.1-mini": {InUSD: 0.40, OutUSD: 1.60},
}

// usage builds a UsageInfo for a metered provider, marking the cost as unknown
// when the model is not in the table.
func usage(table map[string]pricePer1M, model string, in, out int) UsageInfo {
	u := UsageInfo{ModelID: model, InputTokens: in, OutputTokens: out}
	if p, ok := table[model]; ok {
		u.EstimatedCostUSD = (float64(in)/1e6)*p.InUSD + (float64(out)/1e6)*p.OutUSD
		u.CostKnown = true
	}
	return u
}

// localUsage builds a UsageInfo for a provider running on your own hardware.
// The cost is zero and that is a fact, not a missing table entry, so CostKnown
// is set.
func localUsage(model string, in, out int) UsageInfo {
	return UsageInfo{ModelID: model, InputTokens: in, OutputTokens: out, CostKnown: true}
}
