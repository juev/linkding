package search

import (
	"fmt"
	"strings"
	"unicode"
)

// Node is the expression accepted by linkding's v1.47.0 search grammar.
type Node interface{ isNode() }

type Term struct{ Value string }
type Tag struct{ Value string }
type Keyword struct{ Value string }
type And struct{ Left, Right Node }
type Or struct{ Left, Right Node }
type Not struct{ Operand Node }

func (Term) isNode()    {}
func (Tag) isNode()     {}
func (Keyword) isNode() {}
func (And) isNode()     {}
func (Or) isNode()      {}
func (Not) isNode()     {}

type tokenKind uint8

const (
	termToken tokenKind = iota
	tagToken
	keywordToken
	andToken
	orToken
	notToken
	leftToken
	rightToken
	endToken
)

type token struct {
	kind     tokenKind
	value    string
	position int
}

func tokenize(query string) []token {
	input := []rune(strings.TrimSpace(query))
	tokens := make([]token, 0, len(input)/2+1)
	for at := 0; at < len(input); {
		if unicode.IsSpace(input[at]) {
			at++
			continue
		}
		start := at
		switch input[at] {
		case '(':
			tokens = append(tokens, token{leftToken, "(", at})
			at++
		case ')':
			tokens = append(tokens, token{rightToken, ")", at})
			at++
		case '\'', '"':
			quote := input[at]
			at++
			var value strings.Builder
			for at < len(input) && input[at] != quote {
				if input[at] == '\\' {
					at++
					if at == len(input) {
						break
					}
					switch input[at] {
					case 'n':
						value.WriteByte('\n')
					case 't':
						value.WriteByte('\t')
					case 'r':
						value.WriteByte('\r')
					default:
						value.WriteRune(input[at])
					}
				} else {
					value.WriteRune(input[at])
				}
				at++
			}
			if at < len(input) {
				at++
			}
			tokens = append(tokens, token{termToken, value.String(), start})
		case '#', '!':
			kind := tagToken
			if input[at] == '!' {
				kind = keywordToken
			}
			at++
			begin := at
			for at < len(input) && !unicode.IsSpace(input[at]) && !strings.ContainsRune("()\"'", input[at]) {
				at++
			}
			if at > begin {
				tokens = append(tokens, token{kind, string(input[begin:at]), start})
			}
		default:
			for at < len(input) && !unicode.IsSpace(input[at]) && !strings.ContainsRune("()\"'#!", input[at]) {
				at++
			}
			value := string(input[start:at])
			kind := termToken
			switch strings.ToLower(value) {
			case "and":
				kind = andToken
			case "or":
				kind = orToken
			case "not":
				kind = notToken
			}
			tokens = append(tokens, token{kind, value, start})
		}
	}
	return append(tokens, token{kind: endToken, position: len(input)})
}

type parser struct {
	tokens []token
	at     int
}

func (p *parser) current() token { return p.tokens[p.at] }
func (p *parser) advance() {
	if p.at < len(p.tokens)-1 {
		p.at++
	}
}

// Parse returns nil for an empty query. Invalid syntax returns an error; callers
// map it to an empty result set, as linkding does.
func Parse(query string) (Node, error) {
	p := parser{tokens: tokenize(query)}
	if p.current().kind == endToken {
		return nil, nil
	}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.current().kind != endToken {
		return nil, fmt.Errorf("unexpected token at %d", p.current().position)
	}
	return node, nil
}

func (p *parser) parseOr() (Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.current().kind == orToken {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = Or{left, right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for {
		kind := p.current().kind
		if kind != andToken && kind != termToken && kind != tagToken && kind != keywordToken && kind != leftToken && kind != notToken {
			break
		}
		if kind == andToken {
			p.advance()
		}
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = And{left, right}
	}
	return left, nil
}

func (p *parser) parseNot() (Node, error) {
	if p.current().kind == notToken {
		p.advance()
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return Not{operand}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.current()
	switch t.kind {
	case termToken:
		p.advance()
		return Term{t.value}, nil
	case tagToken:
		p.advance()
		return Tag{t.value}, nil
	case keywordToken:
		p.advance()
		return Keyword{t.value}, nil
	case leftToken:
		p.advance()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.current().kind != rightToken {
			return nil, fmt.Errorf("expected closing parenthesis at %d", p.current().position)
		}
		p.advance()
		return inner, nil
	default:
		return nil, fmt.Errorf("unexpected token at %d", t.position)
	}
}
