package agent

// estimateTokens estimates token count using character-based heuristics.
// CJK Unified Ideographs (U+4E00–U+9FFF): ~2 chars/token.
// ASCII and other characters: ~4 chars/token.
//
// Precision: ±20–30% for mixed content. Sufficient for threshold-based guards
// (CostGuard budget, ContextGuard window monitoring).
// Does NOT cover CJK Extension A/B or CJK punctuation (U+3000–U+303F, U+FF00–U+FFEF).
func estimateTokens(text string) int {
	var cjk, other int
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
		} else {
			other++
		}
	}
	return cjk/2 + other/4 + 1 // +1 avoids zero for short strings
}
