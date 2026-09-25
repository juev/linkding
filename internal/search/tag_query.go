package search

import "strings"

// TagNames extracts the names represented by tag expressions in a search.
// In lax mode, ordinary terms can also identify tags.
func TagNames(query string, lax bool) []string {
	node, err := Parse(query)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	var visit func(Node)
	visit = func(node Node) {
		switch value := node.(type) {
		case Tag:
			key := strings.ToLower(value.Value)
			if !seen[key] {
				seen[key] = true
				names = append(names, key)
			}
		case Term:
			if lax {
				key := strings.ToLower(value.Value)
				if !seen[key] {
					seen[key] = true
					names = append(names, key)
				}
			}
		case And:
			visit(value.Left)
			visit(value.Right)
		case Or:
			visit(value.Left)
			visit(value.Right)
		case Not:
			visit(value.Operand)
		}
	}
	visit(node)
	return names
}

func IsTopLevelOr(query string) bool {
	node, err := Parse(query)
	if err != nil {
		return false
	}
	_, ok := node.(Or)
	return ok
}

// StripTag removes a selected tag and renders the remaining search like the
// pinned search parser. Legacy search removes matching space-separated words.
func StripTag(query, name string, lax, legacy bool) string {
	if legacy {
		var kept []string
		for _, part := range strings.Fields(query) {
			if strings.EqualFold(part, "#"+name) || lax && strings.EqualFold(part, name) {
				continue
			}
			kept = append(kept, part)
		}
		return strings.Join(kept, " ")
	}
	node, err := Parse(query)
	if err != nil {
		return query
	}
	return renderTagQuery(stripTagNode(node, name, lax))
}

func stripTagNode(node Node, name string, lax bool) Node {
	switch value := node.(type) {
	case Tag:
		if strings.EqualFold(value.Value, name) {
			return nil
		}
	case Term:
		if lax && strings.EqualFold(value.Value, name) {
			return nil
		}
	case Not:
		operand := stripTagNode(value.Operand, name, lax)
		if operand == nil {
			return nil
		}
		return Not{Operand: operand}
	case And:
		left, right := stripTagNode(value.Left, name, lax), stripTagNode(value.Right, name, lax)
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		return And{Left: left, Right: right}
	case Or:
		left, right := stripTagNode(value.Left, name, lax), stripTagNode(value.Right, name, lax)
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		return Or{Left: left, Right: right}
	}
	return node
}

func renderTagQuery(node Node) string {
	switch value := node.(type) {
	case Tag:
		return "#" + value.Value
	case Term:
		if strings.ContainsAny(value.Value, " ()\"'") {
			return "\"" + strings.ReplaceAll(strings.ReplaceAll(value.Value, "\\", "\\\\"), "\"", "\\\"") + "\""
		}
		return value.Value
	case Keyword:
		return "!" + value.Value
	case Not:
		operand := renderTagQuery(value.Operand)
		switch value.Operand.(type) {
		case And, Or:
			return "not (" + operand + ")"
		}
		return "not " + operand
	case And:
		left, right := renderTagQuery(value.Left), renderTagQuery(value.Right)
		if _, ok := value.Left.(Or); ok {
			left = "(" + left + ")"
		}
		if _, ok := value.Right.(Or); ok {
			right = "(" + right + ")"
		}
		return left + " " + right
	case Or:
		return renderTagQuery(value.Left) + " or " + renderTagQuery(value.Right)
	}
	return ""
}
