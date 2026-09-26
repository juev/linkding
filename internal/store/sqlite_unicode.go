package store

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"modernc.org/sqlite"
)

var unicodeRegistration struct {
	once sync.Once
	err  error
}

var rootCollators = sync.Pool{New: func() any { return collate.New(language.Und) }}

func registerSQLiteUnicode() error {
	unicodeRegistration.once.Do(func() {
		if err := sqlite.RegisterCollationUtf8("LD_ROOT", func(left, right string) int {
			c := rootCollators.Get().(*collate.Collator)
			result := c.CompareString(left, right)
			rootCollators.Put(c)
			return result
		}); err != nil {
			unicodeRegistration.err = err
			return
		}
		functions := []struct {
			name string
			argc int32
			fn   func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error)
		}{
			{"ld_lower", 1, sqliteLower},
			{"ld_fold", 1, sqliteFold},
			{"ld_ci_equal", 2, sqliteCIEqual},
			{"ld_ci_contains", 2, sqliteCIContains},
			{"ld_ci_contains_any", 5, sqliteCIContainsAny},
			{"ld_like", 3, sqliteLike},
		}
		for _, fn := range functions {
			if err := sqlite.RegisterScalarFunction(fn.name, fn.argc, fn.fn); err != nil {
				unicodeRegistration.err = fmt.Errorf("%s: %w", fn.name, err)
				return
			}
		}
	})
	return unicodeRegistration.err
}

func sqlString(value driver.Value) (string, bool, error) {
	switch v := value.(type) {
	case nil:
		return "", false, nil
	case string:
		return v, true, nil
	case []byte:
		return string(v), true, nil
	default:
		return "", false, fmt.Errorf("expected SQL text, got %T", value)
	}
}

func sqliteLower(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	s, valid, err := sqlString(args[0])
	if err != nil || !valid {
		return nil, err
	}
	return cases.Lower(language.Und).String(s), nil
}

func sqliteFold(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	s, valid, err := sqlString(args[0])
	if err != nil || !valid {
		return nil, err
	}
	return simpleCaseFold(s), nil
}

func sqliteCIEqual(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	left, valid, err := sqlString(args[0])
	if err != nil || !valid {
		return nil, err
	}
	right, valid, err := sqlString(args[1])
	if err != nil || !valid {
		return nil, err
	}
	if isASCII(left) && isASCII(right) {
		if strings.EqualFold(left, right) {
			return int64(1), nil
		}
		return int64(0), nil
	}
	if simpleCaseFold(left) == simpleCaseFold(right) {
		return int64(1), nil
	}
	return int64(0), nil
}

func sqliteCIContains(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	text, valid, err := sqlString(args[0])
	if err != nil || !valid {
		return nil, err
	}
	needle, valid, err := sqlString(args[1])
	if err != nil || !valid {
		return nil, err
	}
	if ciContains(text, needle) {
		return int64(1), nil
	}
	return int64(0), nil
}

func sqliteCIContainsAny(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	needle, valid, err := sqlString(args[4])
	if err != nil || !valid {
		return nil, err
	}
	hasNull := false
	for _, arg := range args[:4] {
		text, valid, err := sqlString(arg)
		if err != nil {
			return nil, err
		}
		if !valid {
			hasNull = true
			continue
		}
		if ciContains(text, needle) {
			return int64(1), nil
		}
	}
	if hasNull {
		return nil, nil
	}
	return int64(0), nil
}

func ciContains(text, needle string) bool {
	if isASCII(text) && isASCII(needle) {
		return asciiContainsFold(text, needle)
	}
	return strings.Contains(simpleCaseFold(text), simpleCaseFold(needle))
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}

func asciiContainsFold(text, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(text); i++ {
		matched := true
		for j := 0; j < len(needle); j++ {
			left, right := text[i+j], needle[j]
			if left >= 'a' && left <= 'z' {
				left -= 'a' - 'A'
			}
			if right >= 'a' && right <= 'z' {
				right -= 'a' - 'A'
			}
			if left != right {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// sqliteLike is the first pure-Go candidate for SQLite ICU LIKE. The differential
// suite against the pinned upstream image is the release gate for Unicode cases.
func sqliteLike(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	pattern, valid, err := sqlString(args[0])
	if err != nil || !valid {
		return nil, err
	}
	value, valid, err := sqlString(args[1])
	if err != nil || !valid {
		return nil, err
	}
	escape, valid, err := sqlString(args[2])
	if err != nil || !valid {
		return nil, err
	}
	if len(pattern) > 50000 {
		return nil, errors.New("LIKE pattern exceeds SQLite ICU length limit")
	}
	if len([]rune(escape)) > 1 {
		return nil, errors.New("LIKE escape must contain one character")
	}
	matched, err := likeMatch(pattern, value, escape)
	if err != nil {
		return nil, err
	}
	if matched {
		return int64(1), nil
	}
	return int64(0), nil
}

type likeToken struct {
	kind rune
	text []rune
}

// ICU LIKE compares folded code points. Full Unicode folding can expand one
// code point (ß -> ss), which gives different wildcard and equality results.
func simpleCaseFold(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, r := range value {
		min := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < min {
				min = next
			}
		}
		result.WriteRune(min)
	}
	return result.String()
}

func likeMatch(pattern, value, escape string) (bool, error) {
	var escapeRune rune
	if escape != "" {
		escapeRune = []rune(escape)[0]
	}
	var tokens []likeToken
	patternRunes := []rune(pattern)
	for i := 0; i < len(patternRunes); i++ {
		r := patternRunes[i]
		if escapeRune != 0 && r == escapeRune {
			i++
			if i == len(patternRunes) {
				return false, errors.New("LIKE pattern ends with escape")
			}
			tokens = append(tokens, likeToken{text: []rune(simpleCaseFold(string(patternRunes[i])))})
		} else if r == '%' || r == '_' {
			tokens = append(tokens, likeToken{kind: r})
		} else {
			tokens = append(tokens, likeToken{text: []rune(simpleCaseFold(string(r)))})
		}
	}
	text := []rune(simpleCaseFold(value))
	current := make([]bool, len(text)+1)
	current[0] = true
	for _, token := range tokens {
		next := make([]bool, len(current))
		switch token.kind {
		case '%':
			next[0] = current[0]
			for i := 1; i < len(next); i++ {
				next[i] = current[i] || next[i-1]
			}
		case '_':
			for i := 1; i < len(next); i++ {
				next[i] = current[i-1]
			}
		default:
			for i := len(token.text); i < len(next); i++ {
				if current[i-len(token.text)] && string(text[i-len(token.text):i]) == string(token.text) {
					next[i] = true
				}
			}
		}
		current = next
	}
	return current[len(text)], nil
}
