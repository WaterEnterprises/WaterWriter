package dict

import (
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
