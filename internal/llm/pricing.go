package llm

import "strings"

// modelPricing is USD per 1M tokens (input, output) for models whose rates we
// know. Anything not listed (e.g. OpenAI models, which change often) returns no
// cost — token counts still surface, just without a dollar estimate.
var modelPricing = map[string]struct{ in, out float64 }{
	"claude-opus-4-8":   {5, 25},
	"claude-opus-4-7":   {5, 25},
	"claude-opus-4-6":   {5, 25},
	"claude-opus-4-5":   {5, 25},
	"claude-sonnet-5":   {3, 15},
	"claude-sonnet-4-6": {3, 15},
	"claude-sonnet-4-5": {3, 15},
	"claude-haiku-4-5":  {1, 5},
}

// EstimateCost returns the approximate USD cost of a call given its token usage,
// and whether a rate was known for the model. Matching is by known-ID prefix so a
// dated snapshot (e.g. claude-haiku-4-5-20251001) still resolves.
func EstimateCost(model string, u Usage) (float64, bool) {
	for id, p := range modelPricing {
		if strings.HasPrefix(model, id) {
			return float64(u.InputTokens)/1e6*p.in + float64(u.OutputTokens)/1e6*p.out, true
		}
	}
	return 0, false
}
