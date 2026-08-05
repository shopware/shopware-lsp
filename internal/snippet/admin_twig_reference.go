package snippet

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// AdminTwigReference is a static Administration translation key used by
// $t() or $tc(). Administration templates embed Vue expressions directly in
// HTML attributes, where the Twig CST intentionally keeps the value lossless
// instead of interpreting it as server-side Twig.
type AdminTwigReference struct {
	Key   string
	Range cst.TextRange
}

// AdminTwigReferences returns complete static translation references from
// both Twig expressions and executable Vue attribute values.
func AdminTwigReferences(root *twigsyntax.Node) []AdminTwigReference {
	if root == nil {
		return nil
	}
	source := root.Text()
	var result []AdminTwigReference
	for offset := 0; offset < len(source); {
		reference, next, complete, matched := scanAdminTwigReference(source, offset)
		if !matched {
			offset++
			continue
		}
		offset = next
		if !complete || reference.Key == "" ||
			!adminTwigReferenceAllowed(root, reference.Range.Start) {
			continue
		}
		result = append(result, reference)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

// AdminTwigReferenceAtOffset also accepts an unfinished first argument so
// completion works before the closing quote or parenthesis has been typed.
func AdminTwigReferenceAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (AdminTwigReference, bool) {
	if root == nil {
		return AdminTwigReference{}, false
	}
	source := root.Text()
	for cursor := 0; cursor < len(source); {
		reference, next, complete, matched := scanAdminTwigReference(source, cursor)
		if !matched {
			cursor++
			continue
		}
		cursor = next
		if offset < reference.Range.Start || offset > reference.Range.End ||
			!adminTwigReferenceAllowed(root, reference.Range.Start) {
			continue
		}
		if !complete && offset < reference.Range.End {
			reference.Range.End = offset
			reference.Key = source[reference.Range.Start:offset]
		}
		return reference, true
	}
	return AdminTwigReference{}, false
}

func scanAdminTwigReference(
	source string,
	start int,
) (AdminTwigReference, int, bool, bool) {
	cursor := start
	if strings.HasPrefix(source[cursor:], "this.") {
		cursor += len("this.")
	}
	if cursor >= len(source) || source[cursor] != '$' ||
		(start > 0 && isSnippetIdentifierByte(source[start-1])) {
		return AdminTwigReference{}, start + 1, false, false
	}
	cursor++
	if cursor >= len(source) || source[cursor] != 't' {
		return AdminTwigReference{}, start + 1, false, false
	}
	cursor++
	if cursor < len(source) && source[cursor] == 'c' {
		cursor++
	}
	if cursor < len(source) && isSnippetIdentifierByte(source[cursor]) {
		return AdminTwigReference{}, start + 1, false, false
	}
	cursor = skipSnippetSpace(source, cursor)
	if cursor >= len(source) || source[cursor] != '(' {
		return AdminTwigReference{}, start + 1, false, false
	}
	cursor = skipSnippetSpace(source, cursor+1)
	if cursor >= len(source) ||
		(source[cursor] != '\'' && source[cursor] != '"' && source[cursor] != '`') {
		return AdminTwigReference{}, start + 1, false, false
	}
	quote := source[cursor]
	valueStart := cursor + 1
	cursor = valueStart
	complete := false
	for cursor < len(source) {
		if source[cursor] == '\\' {
			cursor += 2
			continue
		}
		if source[cursor] == quote {
			complete = true
			break
		}
		if source[cursor] == '\r' || source[cursor] == '\n' ||
			(quote == '`' && strings.HasPrefix(source[cursor:], "${")) {
			break
		}
		cursor++
	}
	return AdminTwigReference{
		Key: source[valueStart:cursor],
		Range: cst.TextRange{
			Start: uint32(valueStart),
			End:   uint32(cursor),
		},
	}, max(cursor+1, start+1), complete, true
}

func adminTwigReferenceAllowed(root *twigsyntax.Node, offset uint32) bool {
	node := root.NodeAtOffset(offset)
	if node == nil && offset > 0 {
		node = root.NodeAtOffset(offset - 1)
	}
	if node == nil || twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigComment,
		twigsyntax.TwigVerbatim,
	) != nil {
		return false
	}
	if attribute := twigquery.HTMLAttributeAt(node); attribute != nil {
		name := twigquery.HTMLAttributeName(attribute)
		if strings.HasPrefix(name, ":") || strings.HasPrefix(name, "@") ||
			strings.HasPrefix(name, "#") || strings.HasPrefix(name, "v-") {
			return true
		}
	}
	return twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigFunctionCall,
	) != nil
}

func skipSnippetSpace(source string, offset int) int {
	for offset < len(source) {
		switch source[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
		default:
			return offset
		}
	}
	return offset
}

func isSnippetIdentifierByte(value byte) bool {
	return value == '_' || value == '$' ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}
