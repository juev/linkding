package media

import (
	"fmt"
	"strings"
	"unicode"
)

// splitShellWords covers the POSIX shlex syntax used by LD_SINGLEFILE_OPTIONS.
func splitShellWords(input string) ([]string, error) {
	runes := []rune(input)
	var result []string
	var token strings.Builder
	var quote rune
	started := false
	for at := 0; at < len(runes); at++ {
		char := runes[at]
		switch {
		case quote == 39:
			if char == 39 {
				quote = 0
			} else {
				token.WriteRune(char)
			}
		case quote == '"':
			if char == '"' {
				quote = 0
				continue
			}
			if char == '\\' && at+1 < len(runes) {
				next := runes[at+1]
				if next == '$' || next == '`' || next == '"' || next == '\\' || next == '\n' {
					at++
					if next != '\n' {
						token.WriteRune(next)
					}
					continue
				}
			}
			token.WriteRune(char)
		case unicode.IsSpace(char):
			if started {
				result = append(result, token.String())
				token.Reset()
				started = false
			}
		case char == 39 || char == '"':
			quote = char
			started = true
		case char == '\\':
			at++
			if at == len(runes) {
				return nil, fmt.Errorf("unfinished escape in shell options")
			}
			if runes[at] != '\n' {
				token.WriteRune(runes[at])
			}
			started = true
		default:
			token.WriteRune(char)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unfinished quote in shell options")
	}
	if started {
		result = append(result, token.String())
	}
	return result, nil
}
