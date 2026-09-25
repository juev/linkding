package bookmarks

import (
	"slices"
	"testing"
)

func TestParseTagStringMatchesPinnedPython(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{" Go lang , go-lang, BETA, beta, , A  B ", []string{"A--B", "beta", "go-lang"}},
		{"МОСКВА,Москва,Äpfel,äPFEL", []string{"äPFEL", "Москва"}},
		{"İstanbul,istanbul,Istanbul,ıstanbul,ΟΣ,ος", []string{"Istanbul", "İstanbul", "ıstanbul", "ος"}},
	}
	for _, tc := range cases {
		if got := ParseTagString(tc.input, ","); !slices.Equal(got, tc.want) {
			t.Errorf("ParseTagString(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
