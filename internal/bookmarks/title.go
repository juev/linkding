package bookmarks

import "unicode"

const MaxTitleLength = 512

// NormalizeTitle makes a title a single line and bounds it in Unicode codepoints.
// Formatting characters, including joiners used in emoji, remain intact.
func NormalizeTitle(title string) string {
	runes := make([]rune, 0, MaxTitleLength)
	space := false
	for _, char := range title {
		if unicode.IsSpace(char) {
			space = len(runes) > 0
			continue
		}
		if unicode.IsControl(char) {
			continue
		}
		if space {
			runes = append(runes, ' ')
			space = false
		}
		if len(runes) == MaxTitleLength {
			break
		}
		runes = append(runes, char)
		if len(runes) == MaxTitleLength {
			break
		}
	}
	if len(runes) > 0 && runes[len(runes)-1] == ' ' {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
