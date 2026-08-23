package twig

import (
	"slices"
	"strings"

	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// DocumentationCommentText returns the normalized contents of a Twig 3.29
// documentation comment. It accepts both construct comments (`{## ... #}` or
// `{## ... ##}`) and line-scoped binding comments (`## ...`).
func DocumentationCommentText(source string) (string, bool) {
	value := strings.TrimSpace(source)
	if strings.HasPrefix(value, "{##") {
		openingEnd := len("{##")
		if openingEnd < len(value) &&
			(value[openingEnd] == '-' || value[openingEnd] == '~') {
			openingEnd++
		}
		for _, closing := range []string{
			"-##}", "~##}", "##}", "-#}", "~#}", "#}",
		} {
			if !strings.HasSuffix(value, closing) {
				continue
			}
			closingStart := len(value) - len(closing)
			// `{##}` is Twig's empty regular comment, not documentation.
			if closingStart < openingEnd {
				return "", false
			}
			return normalizeDocumentation(
				value[openingEnd:closingStart],
			), true
		}
		return "", false
	}
	if strings.HasPrefix(value, "##") {
		line := value[len("##"):]
		if end := strings.IndexAny(line, "\r\n"); end >= 0 {
			line = line[:end]
		}
		return normalizeDocumentation(line), true
	}
	return "", false
}

// DocumentationBefore returns consecutive documentation comments associated
// with node. Regular Twig comments do not interrupt attachment, matching
// Twig's lexer, while any other sibling is a semantic boundary.
func DocumentationBefore(node *twigsyntax.Node) string {
	if node == nil {
		return ""
	}
	var documentation []string
	for sibling := node.PrevSibling(); sibling != nil; {
		comment, ok := sibling.(*twigsyntax.Node)
		if !ok || comment.Kind() != twigsyntax.TwigComment {
			break
		}
		value, documented := DocumentationCommentText(comment.Text())
		if documented && value != "" {
			documentation = append(documentation, value)
		}
		sibling = comment.PrevSibling()
	}
	slices.Reverse(documentation)
	return strings.Join(documentation, "\n")
}

// DocumentationForNode finds documentation attached to the construct or
// binding containing node. A body is a boundary so block/tag documentation is
// not incorrectly shown while hovering arbitrary content inside the body.
func DocumentationForNode(node *twigsyntax.Node) string {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == twigsyntax.Body {
			return ""
		}
		if documentation := DocumentationBefore(current); documentation != "" {
			return documentation
		}
	}
	return ""
}

func normalizeDocumentation(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}
