package admin

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// AdminTwigRegistryReference is a literal registry identity used by a Vue
// expression embedded in an Administration Twig template. Vue expressions in
// HTML attributes are intentionally tokenized losslessly instead of being
// parsed as server-side Twig, so these references need a small contextual
// recognizer of their own.
type AdminTwigRegistryReference struct {
	Kind  AdminSymbolKind
	Name  string
	Range cst.TextRange
}

// TwigRegistryReferences returns complete registry references from executable
// Vue/Twig expression contexts. Plain HTML text, ordinary attribute strings,
// Twig comments, and verbatim bodies are excluded.
func TwigRegistryReferences(
	root *twigsyntax.Node,
) []AdminTwigRegistryReference {
	if root == nil {
		return nil
	}
	source := root.Text()
	var result []AdminTwigRegistryReference
	for offset := 0; offset < len(source); {
		reference, next, matched := scanTwigACLReference(source, offset)
		if !matched {
			offset++
			continue
		}
		offset = next
		if reference.Name == "" ||
			!twigRegistryReferenceAllowed(root, reference.Range.Start) {
			continue
		}
		result = append(result, reference)
	}
	result = append(result, twigRouteReferences(root, false)...)
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

// TwigRegistryReferenceAtOffset also recognizes an unfinished literal. That
// lets completion work for acl.can('<caret>') before a closing quote exists.
func TwigRegistryReferenceAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (AdminTwigRegistryReference, bool) {
	if root == nil {
		return AdminTwigRegistryReference{}, false
	}
	source := root.Text()
	for cursor := 0; cursor < len(source); {
		reference, next, matched := scanTwigACLReference(source, cursor)
		if !matched {
			cursor++
			continue
		}
		cursor = next
		if offset < reference.Range.Start || offset > reference.Range.End ||
			!twigRegistryReferenceAllowed(root, reference.Range.Start) {
			continue
		}
		return reference, true
	}
	for _, reference := range twigRouteReferences(root, true) {
		if offset >= reference.Range.Start && offset <= reference.Range.End {
			return reference, true
		}
	}
	return AdminTwigRegistryReference{}, false
}

func twigRouteReferences(
	root *twigsyntax.Node,
	includeIncomplete bool,
) []AdminTwigRegistryReference {
	var result []AdminTwigRegistryReference
	for node := range twigquery.IterateNodes(root, twigsyntax.HtmlAttribute) {
		attribute, ok := twigast.CastHtmlAttribute(node)
		if !ok {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		inner, ok := value.GetInner()
		if !ok || !isAdminRouteAttribute(
			twigquery.HTMLAttributeName(node),
			inner.Syntax().Text(),
		) {
			continue
		}
		for _, reference := range scanTwigRouteNames(
			inner.Syntax().Text(),
			inner.Syntax().Range().Start,
		) {
			if reference.Name != "" || includeIncomplete {
				result = append(result, reference)
			}
		}
	}
	return result
}

func isAdminRouteAttribute(name, value string) bool {
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}
	switch name {
	case ":to", "v-bind:to", ":route", "v-bind:route",
		":router-link", "v-bind:router-link":
		return true
	}
	if !strings.HasPrefix(name, ":") && !strings.HasPrefix(name, "@") &&
		!strings.HasPrefix(name, "v-") {
		return false
	}
	return strings.Contains(value, "$router.push") ||
		strings.Contains(value, "$router.replace") ||
		strings.Contains(value, "$router.resolve")
}

func scanTwigRouteNames(
	source string,
	base uint32,
) []AdminTwigRegistryReference {
	var result []AdminTwigRegistryReference
	for offset := 0; offset < len(source); {
		const property = "name"
		if offset+len(property) > len(source) ||
			source[offset:offset+len(property)] != property ||
			(offset > 0 && isAdminIdentifierByte(source[offset-1])) ||
			(offset+len(property) < len(source) &&
				isAdminIdentifierByte(source[offset+len(property)])) {
			offset++
			continue
		}
		cursor := skipAdminExpressionSpace(source, offset+len(property))
		if cursor >= len(source) || source[cursor] != ':' {
			offset += len(property)
			continue
		}
		cursor = skipAdminExpressionSpace(source, cursor+1)
		if cursor >= len(source) ||
			(source[cursor] != '\'' && source[cursor] != '"') {
			offset = cursor + 1
			continue
		}
		quote := source[cursor]
		valueStart := cursor + 1
		cursor = valueStart
		for cursor < len(source) && source[cursor] != quote &&
			isAdminRegistryNameByte(source[cursor]) {
			cursor++
		}
		result = append(result, AdminTwigRegistryReference{
			Kind: AdminSymbolModuleRoute,
			Name: source[valueStart:cursor],
			Range: cst.TextRange{
				Start: base + uint32(valueStart),
				End:   base + uint32(cursor),
			},
		})
		offset = max(cursor+1, offset+1)
	}
	return result
}

func scanTwigACLReference(
	source string,
	start int,
) (AdminTwigRegistryReference, int, bool) {
	const receiver = "acl"
	if start+len(receiver) > len(source) ||
		source[start:start+len(receiver)] != receiver ||
		(start > 0 && isAdminIdentifierByte(source[start-1])) ||
		(start+len(receiver) < len(source) &&
			isAdminIdentifierByte(source[start+len(receiver)])) {
		return AdminTwigRegistryReference{}, start + 1, false
	}
	cursor := skipAdminExpressionSpace(source, start+len(receiver))
	if cursor >= len(source) || source[cursor] != '.' {
		return AdminTwigRegistryReference{}, start + len(receiver), false
	}
	cursor = skipAdminExpressionSpace(source, cursor+1)
	const method = "can"
	if cursor+len(method) > len(source) ||
		source[cursor:cursor+len(method)] != method ||
		(cursor+len(method) < len(source) &&
			isAdminIdentifierByte(source[cursor+len(method)])) {
		return AdminTwigRegistryReference{}, cursor + 1, false
	}
	cursor = skipAdminExpressionSpace(source, cursor+len(method))
	if cursor >= len(source) || source[cursor] != '(' {
		return AdminTwigRegistryReference{}, cursor + 1, false
	}
	cursor = skipAdminExpressionSpace(source, cursor+1)
	if cursor >= len(source) || (source[cursor] != '\'' && source[cursor] != '"') {
		return AdminTwigRegistryReference{}, cursor + 1, false
	}
	quote := source[cursor]
	valueStart := cursor + 1
	cursor = valueStart
	for cursor < len(source) && source[cursor] != quote &&
		isAdminRegistryNameByte(source[cursor]) {
		cursor++
	}
	return AdminTwigRegistryReference{
		Kind: AdminSymbolPrivilege,
		Name: source[valueStart:cursor],
		Range: cst.TextRange{
			Start: uint32(valueStart),
			End:   uint32(cursor),
		},
	}, max(cursor+1, start+1), true
}

func twigRegistryReferenceAllowed(root *twigsyntax.Node, offset uint32) bool {
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
		return strings.HasPrefix(name, ":") ||
			strings.HasPrefix(name, "@") ||
			strings.HasPrefix(name, "#") ||
			strings.HasPrefix(name, "v-")
	}
	return twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigFunctionCall,
	) != nil
}

func skipAdminExpressionSpace(source string, offset int) int {
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

func isAdminIdentifierByte(value byte) bool {
	return value == '_' || value == '$' ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func isAdminRegistryNameByte(value byte) bool {
	return isAdminIdentifierByte(value) || value == '.' || value == ':' ||
		value == '-'
}
