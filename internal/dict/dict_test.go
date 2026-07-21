package dict

import (
	"regexp"
	"testing"
)

func TestInDictionary(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"the", true},
		{"of", true},
		{"blanket", true},
		{"through", true},
		{"had", true},
		{"been", true},
		{"unknownwordxyz", false},
		{"groan", true},     // in supplement
		{"uncouple", true},  // in supplement
		{"step", true},
		{"tide", true},          // in top-10k
		{"central", true},       // in top-10k
		{"processing", true},    // in supplement
		{"process", true},       // in top-10k
		{"finalize", true},      // in supplement
		{"finalized", true},     // in supplement
	}
	for _, tt := range tests {
		got := InDictionary(tt.word)
		if got != tt.want {
			t.Errorf("InDictionary(%q) = %v, want %v", tt.word, got, tt.want)
		}
	}
}

func TestWordOrStem(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		// Base words
		{"blanket", true},
		{"through", true},
		{"groan", true}, // in supplement

		// -ed forms (stemmed)
		{"groaned", true},   // stems to "groan"
		{"rested", true},    // stems to "rest" (in top-10k)
		{"started", true},   // stems to "start"
		{"coupled", true},   // in top-10k
		{"uncoupled", true}, // un- prefix + "coupled"

		// -ing forms (stemmed with double consonant)
		{"stepping", true},   // stems to "step"
		{"running", true},    // stems to "run"
		{"swimming", true},   // stems to "swim"
		{"processing", true}, // stems to "process"

		// -s / -es forms
		{"books", true},     // in top-10k
		{"boxes", true},     // stems to "box"
		{"processes", true}, // stems to "process"

		// Non-words that aren't valid stems
		{"xyzabc", false},
		{"qwerty", false},
		{"zzzxxx", false},

		// Compound words (should be in supplement or top-10k)
		{"breakthrough", true}, // in supplement
		{"overlook", true},     // in supplement
	}
	for _, tt := range tests {
		got := wordOrStem(tt.word)
		if got != tt.want {
			t.Errorf("wordOrStem(%q) = %v, want %v", tt.word, got, tt.want)
		}
	}
}

func TestSplitUnknown_KnownCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"already clean", "the quick brown fox", "the quick brown fox"},

		// Function-word constraint cases
		{"blanketof", "blanketof tropical", "blanket of tropical"},
		{"enteredthrough", "enteredthrough the", "entered through the"},
		{"hadbeen", "hadbeen a", "had been a"},
		{"restedagainst", "restedagainst the", "rested against the"},
		{"groanedas", "groanedas Teo", "groaned as Teo"},
		{"steppingout", "steppingout from", "stepping out from"},
		{"mirrorwas", "mirrorwas a", "mirror was a"},
		{"grayof", "grayof the", "gray of the"},

		// "a" article cases (astrip is in knownSplits)
		{"experiencinga", "experiencinga lot", "experiencing a lot"},

		// "that" cases
		{"thatpulsed", "thatpulsed at", "that pulsed at"},
		{"thatonce", "thatonce the", "that once the"},

		// "was" cases
		{"wasfinalized", "wasfinalized the", "was finalized the"},
		{"somethingwas", "somethingwas done", "something was done"},

		// Known splits (no function word)
		{"risingtide", "risingtide the", "rising tide the"},
		{"centralprocessing", "centralprocessing unit", "central processing unit"},
		{"astrip known split", "astrip of", "a strip of"},

		// Pronouns
		{"Hekept", "Hekept the", "He kept the"},
		{"theywent", "theywent to", "they went to"},

		// Multiple splits in one string
		{"multiple", "blanketof and enteredthrough", "blanket of and entered through"},

		// Edge: word not in dict should remain unchanged
		{"unknown_compound", "xylophonezephyr", "xylophonezephyr"},

		// Edge: short words (< 4 chars) unchanged
		{"short_unchanged", "abc xyz", "abc xyz"},

		// Edge: word in dictionary unchanged
		{"dict_word_unchanged", "sunflower", "sunflower"},
	}
	for _, tt := range tests {
		got := SplitUnknown(tt.input)
		if got != tt.want {
			t.Errorf("SplitUnknown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitUnknown_RespectsFunctionWordConstraint(t *testing.T) {
	// These should NOT be split because neither part is a function word.
	tests := []struct {
		name  string
		input string
	}{
		{"sunflower unchanged", "sunflower"},
		{"notebook unchanged", "notebook"},
		{"daybreak unchanged", "daybreak"},
		{"both content words", "moonlight"},
	}
	for _, tt := range tests {
		got := SplitUnknown(tt.input)
		if got != tt.input {
			t.Errorf("SplitUnknown(%q) = %q, should remain unchanged (no function word)", tt.input, got)
		}
	}
}

func TestSplitUnknown_CompoundWordPrevention(t *testing.T) {
	// These compound words ARE in the dictionary (supplement), so they should
	// NOT be split.
	tests := []struct {
		name  string
		input string
	}{
		{"breakthrough", "breakthrough"},
		{"overlook", "overlook"},
		{"underworld", "underworld"},
		{"override", "override"},
		{"therein", "therein"},
	}
	for _, tt := range tests {
		got := SplitUnknown(tt.input)
		if got != tt.input {
			t.Errorf("SplitUnknown(%q) = %q, should remain unchanged (compound in dict)", tt.input, got)
		}
	}
}

// normalizeSpacing simulates the full pipeline from app.go / cmd/export.go:
// regex patterns first (periods, commas, wordof, wordwas, etc.), then
// dictionary-based SplitUnknown for remaining merged words.
func normalizeSpacing(s string) string {
	// Fix missing space after period followed by capital letter.
	s = regexp.MustCompile(`\.([A-Z])`).ReplaceAllString(s, ". $1")
	// Fix missing space after comma.
	s = regexp.MustCompile(`,([a-zA-Z])`).ReplaceAllString(s, ", $1")
	// Fix missing space after ? or !.
	s = regexp.MustCompile(`([?!])([a-zA-Z])`).ReplaceAllString(s, "$1 $2")

	// Fix "wordof" at end of word before space/punct.
	s = regexp.MustCompile(`([a-z])of([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 of$2")

	// Fix "wordwas" at end / " wasword" at start.
	s = regexp.MustCompile(`([a-zA-Z])was([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 was$2")
	// Fix " wasword" at start — $1 already contains the leading space/punct,
	// so no extra space is needed before "was".
	s = regexp.MustCompile(`([\s.,;!?\-])was([a-zA-Z])`).ReplaceAllString(s, "${1}was $2")

	// Fix "wordthat" at end / " thatword" at start.
	s = regexp.MustCompile(`([a-z])that([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 that$2")
	// Fix " thatword" at start — same logic, $1 provides the leading space.
	s = regexp.MustCompile(`([\s.,;!?\-])that([a-zA-Z])`).ReplaceAllString(s, "${1}that $2")

	// Fix article "a" at end of word before space/punct (e.g.,
	// "experiencinga " → "experiencing a "). Start-of-word "a" merging is
	// handled by SplitUnknown's position-1 check, which is dictionary-safe.
	s = regexp.MustCompile(`([a-zA-Z])a([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 a$2")

	// Dictionary-based word segmentation.
	s = SplitUnknown(s)

	return s
}

func TestNormalizeSpacingPipeline(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Regex-only patterns
		{"period+capital", "hello.The world", "hello. The world"},
		{"comma+letter", "hello,world", "hello, world"},
		{"question+letter", "hello?world", "hello? world"},
		{"exclamation+letter", "hello!world", "hello! world"},

		// Regex "wordof" at boundary
		{"wordof boundary", "blanketof tropical", "blanket of tropical"},
		{"wordof period", "blanketof.Hello", "blanket of. Hello"},

		// Regex "was" patterns
		{"was at start", " wasfinalized the", " was finalized the"},
		{"was at end", "mirrorwas the", "mirror was the"},

		// Regex "that" patterns
		{"that at start", " thatpulsed the", " that pulsed the"},
		{"that at end", "somethingthat went", "something that went"},

		// Regex "a" at end patterns (start-of-word "a" handled by SplitUnknown)
		{"a at end", "experiencinga lot", "experiencing a lot"},
		{"a at start via SplitUnknown", "amother", "a mother"},

		// Dict-only patterns
		{"enteredthrough", "enteredthrough the", "entered through the"},
		{"hadbeen", "hadbeen the", "had been the"},
		{"restedagainst", "restedagainst a", "rested against a"},
		{"groanedas", "groanedas Teo", "groaned as Teo"},
		{"steppingout", "steppingout from", "stepping out from"},
		{"Hekept", "Hekept the", "He kept the"},

		// Known splits
		{"risingtide", "risingtide the", "rising tide the"},
		{"centralprocessing", "centralprocessing unit", "central processing unit"},
		{"astrip", "astrip of", "a strip of"},

		// Combined: regex + dict together
		{"regex then dict", "blanketof.The enteredthrough the", "blanket of. The entered through the"},
		{"mixed pipeline", "wasfinalized.and hadbeen", "was finalized.and had been"},

		// Full sentences simulating LLM output
		{"full sentence",
			"The humidity of Recife did not clear.Hekept walking.The blanketof tropical air was heavy.",
			"The humidity of Recife did not clear. He kept walking. The blanket of tropical air was heavy."},

		// Edge: clean text unchanged
		{"clean text", "The quick brown fox jumps over the lazy dog.", "The quick brown fox jumps over the lazy dog."},
	}

	for _, tt := range tests {
		got := normalizeSpacing(tt.input)
		if got != tt.want {
			t.Errorf("%s:\n  normalizeSpacing(%q)\n  = %q\n  want %q", tt.name, tt.input, got, tt.want)
		}
	}
}

func TestSplitUnknown_PunctuationBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"period after", "blanketof.", "blanket of."},
		{"comma after", "blanketof,", "blanket of,"},
		{"exclamation after", "blanketof!", "blanket of!"},
		{"question after", "blanketof?", "blanket of?"},
		{"semicolon after", "blanketof;", "blanket of;"},
	}
	for _, tt := range tests {
		got := SplitUnknown(tt.input)
		if got != tt.want {
			t.Errorf("SplitUnknown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
