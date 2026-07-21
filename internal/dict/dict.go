// Package dict provides a dictionary-based word segmentation function that
// detects and fixes common LLM spacing errors (merged words like "blanketof"
// → "blanket of", "enteredthrough" → "entered through").
//
// The dictionary is built from the Google 10,000 English Words list plus a
// supplementary list of common inflected forms. Words are loaded into a map
// for O(1) lookups. The segmentation algorithm tries all possible split
// points for any word not found in the dictionary, accepting the split if
// both parts are valid words and at least one is a short function word.
package dict

import (
	_ "embed"
	"strings"
	"unicode"
)

//go:embed words.txt
var wordListContent string

//go:embed supplement.txt
var supplementContent string

// wordSet is the dictionary of known English words.
var wordSet map[string]bool

// funcWords is a set of common English function words (prepositions,
// conjunctions, articles, auxiliary verbs, pronouns, etc.) that commonly
// get merged with adjacent words in LLM output. We require at least one
// part of a split to be a function word to avoid false splits of real
// compound words (e.g., "sunflower" won't split because neither "sun"
// nor "flower" is a function word).
var funcWords map[string]bool

func init() {
	wordSet = make(map[string]bool, 12000)

	// Load the main word list.
	for _, w := range strings.Fields(wordListContent) {
		wordSet[strings.ToLower(w)] = true
	}

	// Load the supplementary word list (inflected forms, missing words).
	for _, w := range strings.Fields(supplementContent) {
		wordSet[strings.ToLower(w)] = true
	}

	funcWords = map[string]bool{
		"a": true, "an": true, "the": true,
		"and": true, "or": true, "but": true, "if": true,
		"of": true, "to": true, "in": true, "by": true, "for": true,
		"with": true, "from": true, "at": true, "as": true, "on": true,
		"up": true, "out": true, "down": true, "off": true, "over": true,
		"under": true, "into": true, "through": true, "during": true,
		"before": true, "after": true, "between": true, "among": true,
		"against": true, "without": true, "within": true, "along": true,
		"across": true, "around": true, "behind": true, "below": true,
		"beneath": true, "beside": true, "beyond": true, "inside": true,
		"outside": true, "upon": true, "about": true, "above": true,
		"toward": true, "until": true, "since": true, "via": true,
		"was": true, "were": true, "had": true, "has": true, "have": true,
		"been": true, "being": true, "is": true, "are": true, "am": true,
		"be": true, "not": true, "do": true, "does": true, "did": true,
		"done": true, "doing": true, "get": true, "got": true,
		"goes": true, "going": true, "went": true, "gone": true,
		"like": true, "just": true, "then": true, "than": true,
		"very": true, "also": true, "only": true, "now": true,
		"even": true, "still": true, "already": true, "yet": true,
		"so": true, "no": true,
		"he": true, "she": true, "it": true, "we": true, "they": true,
		"me": true, "him": true, "her": true, "us": true, "them": true,
		"my": true, "your": true, "his": true, "its": true,
		"our": true, "their": true,
		"this": true, "that": true, "these": true, "those": true,
		"there": true, "here": true, "where": true, "when": true,
		"why": true, "how": true, "what": true, "which": true,
		"who": true, "whom": true, "whose": true,
	}
}

// InDictionary reports whether the given word is a known English word.
func InDictionary(word string) bool {
	return wordSet[strings.ToLower(word)]
}

// wordOrStem checks whether a word is in the dictionary, or whether its
// stem (after removing common inflectional suffixes) is in the dictionary.
// This handles missing inflected forms like "groaned" (stem "groan").
func wordOrStem(word string) bool {
	lower := strings.ToLower(word)
	if wordSet[lower] {
		return true
	}

	// Try stripping common suffixes.
	// -ed (e.g., "groaned" → "groan", but "uncoupled" → "uncouple" via -d)
	if strings.HasSuffix(lower, "ed") && len(lower) > 4 {
		stem := lower[:len(lower)-2] // remove "ed"
		if wordSet[stem] {
			return true
		}
		stem = lower[:len(lower)-1] // remove "d" (for -e + d endings)
		if wordSet[stem] {
			return true
		}
	}
	// -ing (e.g., "stepping" → "stepp" → try double consonant: "step")
	if strings.HasSuffix(lower, "ing") && len(lower) > 5 {
		stem := lower[:len(lower)-3]
		if wordSet[stem] {
			return true
		}
		// Check for doubled consonant (e.g., "stepping" → "stepp" → "step")
		if len(stem) >= 2 && stem[len(stem)-1] == stem[len(stem)-2] {
			if wordSet[stem[:len(stem)-1]] {
				return true
			}
		}
	}
	// -s / -es (e.g., "boxes" → "box")
	if strings.HasSuffix(lower, "es") && len(lower) > 4 {
		if wordSet[lower[:len(lower)-2]] {
			return true
		}
	}
	if strings.HasSuffix(lower, "s") && len(lower) > 4 {
		if wordSet[lower[:len(lower)-1]] {
			return true
		}
	}
	// un- prefix (e.g., "uncoupled" → "coupled")
	if strings.HasPrefix(lower, "un") && len(lower) > 5 {
		stem := lower[2:]
		if wordSet[stem] {
			return true
		}
		// The stem itself may have inflectional suffix (e.g., "uncoupled" → "coupled" → "couple")
		return wordOrStem(stem)
	}

	return false
}

// knownSplits is a map of specific merged-word pairs that should always be
// split, even when neither part is a function word. These are common LLM
// merge errors where both parts are content words (nouns, verbs, adjectives)
// rather than function words, so the general function-word constraint can't
// catch them.
var knownSplits = map[string]string{
	"risingtide":       "rising tide",
	"centralprocessing": "central processing",
	"astrip":           "a strip",
}

// SplitUnknown scans the input string for words that are not in the
// dictionary and attempts to split them at valid word boundaries where at
// least one part is a common function word. It returns the string with
// detected merged words separated by a space.
//
// For example, "blanketof" → "blanket of" because "of" is a function word
// and both "blanket" and "of" are in the dictionary. But "sunflower" is not
// split because neither "sun" nor "flower" is a function word.
func SplitUnknown(s string) string {
	result := []byte(s)
	i := 0
	for i < len(result) {
		// Find the start of the next alphabetic token.
		if !unicode.IsLetter(rune(result[i])) {
			i++
			continue
		}
		start := i
		for i < len(result) && unicode.IsLetter(rune(result[i])) {
			i++
		}
		end := i

		word := string(result[start:end])
		if len(word) <= 3 {
			continue
		}
		// If the word is already in the dictionary, skip it.
		if wordSet[strings.ToLower(word)] {
			continue
		}

		// Check known-splits first (catches merges where neither part is a
		// function word, like "risingtide" → "rising tide").
		if fix, ok := knownSplits[strings.ToLower(word)]; ok {
			var buf strings.Builder
			buf.Write(result[:start])
			buf.WriteString(fix)
			buf.Write(result[end:])
			result = []byte(buf.String())
			i = start + len(fix)
			continue
		}

		// Special case: single-character article "a" merged with a word.
		// The main loop below starts at split=2 (minimum 2 chars per part),
		// so it never tries the split "a" + rest at position 1. Check it
		// here before falling through to the general split loop.
		// Only applies when the full word is NOT in the dictionary (already
		// ensured by the wordSet check above), so dictionary words like
		// "alone", "and", "air" are never split.
		if len(word) > 2 && (word[0] == 'a' || word[0] == 'A') {
			rest := word[1:]
			if wordSet[strings.ToLower(rest)] || wordOrStem(rest) {
				var buf strings.Builder
				buf.Write(result[:start+1])
				buf.WriteByte(' ')
				buf.Write(result[start+1:])
				result = []byte(buf.String())
				i = start + 2 // skip past "a "
				continue
			}
		}

		// Try all split points.
		bestSplit := 0
		for split := 2; split < len(word); split++ {
			first := word[:split]
			second := word[split:]

			firstIsFunc := funcWords[strings.ToLower(first)]
			secondIsFunc := funcWords[strings.ToLower(second)]

			// Accept the split if at least one part is a function word AND
			// both parts are valid words (either in the dictionary or
			// recognizable as a stemmed inflected form).
			if !firstIsFunc && !secondIsFunc {
				continue
			}

			firstOk := wordOrStem(first)
			secondOk := wordOrStem(second)
			if firstOk && secondOk {
				bestSplit = split
				break
			}
		}

		if bestSplit > 0 {
			// Insert a space at the split point.
			var buf strings.Builder
			buf.Write(result[:start+bestSplit])
			buf.WriteByte(' ')
			buf.Write(result[start+bestSplit:])
			result = []byte(buf.String())
			i = start + bestSplit + 2 // skip past the inserted space
		}
	}
	return string(result)
}
