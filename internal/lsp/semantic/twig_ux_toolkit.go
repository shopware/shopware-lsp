package semantic

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

var (
	twigUXPropAnnotationPattern = regexp.MustCompile(
		`(?m)(@prop)[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]+(\S+)[ \t]+[^\r\n#]+`,
	)
	twigUXBlockAnnotationPattern = regexp.MustCompile(
		`(?m)(@block)[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]+[^\r\n#]+`,
	)
	twigVarFirstAnnotationPattern = regexp.MustCompile(
		`(?m)(@var)[ \t]+(\$?[A-Za-z_][A-Za-z0-9_]*)[ \t]+([\w\\\[\]]+(?:\|[\w\\\[\]]+)*)`,
	)
	twigTypeFirstVarAnnotationPattern = regexp.MustCompile(
		`(?m)(@var)[ \t]+([\w\\\[\]]+(?:\|[\w\\\[\]]+)*)[ \t]+(\$?[A-Za-z_][A-Za-z0-9_]*)`,
	)
)

// TwigUXToolkitProvider colors Symfony UX Toolkit @prop and @block comments,
// plus the reference plugin's two supported Twig @var annotation orders, with
// standard LSP semantic token kinds.
type TwigUXToolkitProvider struct{}

func NewTwigUXToolkitProvider() *TwigUXToolkitProvider {
	return &TwigUXToolkitProvider{}
}

func (p *TwigUXToolkitProvider) GetSemanticTokens(
	_ context.Context,
	request *lsp.SemanticTokensRequest,
) ([]lsp.SemanticToken, error) {
	if request == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.Document.URI),
			".twig",
		) {
		return nil, nil
	}

	var result []lsp.SemanticToken
	for comment := range twigquery.IterateNodes(
		request.Document.SyntaxTree.Root,
		twigsyntax.TwigComment,
	) {
		result = appendTwigVarAnnotationTokens(result, comment)
		result = appendTwigUXAnnotationTokens(
			result,
			comment,
			twigUXPropAnnotationPattern,
			[]uint32{
				protocol.SemanticTokenKeyword,
				protocol.SemanticTokenProperty,
				protocol.SemanticTokenType,
			},
		)
		result = appendTwigUXAnnotationTokens(
			result,
			comment,
			twigUXBlockAnnotationPattern,
			[]uint32{
				protocol.SemanticTokenKeyword,
				protocol.SemanticTokenProperty,
			},
		)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].Range.End < result[right].Range.End
	})
	return result, nil
}

func appendTwigVarAnnotationTokens(
	result []lsp.SemanticToken,
	comment *twigsyntax.Node,
) []lsp.SemanticToken {
	if comment == nil || !strings.Contains(comment.Text(), "@var") {
		return result
	}
	handled := make(map[int]struct{})
	base := comment.Range().Start
	for _, match := range twigVarFirstAnnotationPattern.FindAllStringSubmatchIndex(
		comment.Text(),
		-1,
	) {
		if len(match) < 8 ||
			!twigVarAnnotationBoundary(comment.Text(), match[1]) {
			continue
		}
		handled[match[2]] = struct{}{}
		result = appendSemanticMatchTokens(
			result,
			base,
			match,
			[]uint32{
				protocol.SemanticTokenKeyword,
				protocol.SemanticTokenVariable,
				protocol.SemanticTokenType,
			},
		)
	}
	for _, match := range twigTypeFirstVarAnnotationPattern.FindAllStringSubmatchIndex(
		comment.Text(),
		-1,
	) {
		if len(match) < 8 ||
			!twigVarAnnotationBoundary(comment.Text(), match[1]) {
			continue
		}
		if _, exists := handled[match[2]]; exists {
			continue
		}
		result = appendSemanticMatchTokens(
			result,
			base,
			match,
			[]uint32{
				protocol.SemanticTokenKeyword,
				protocol.SemanticTokenType,
				protocol.SemanticTokenVariable,
			},
		)
	}
	return result
}

func twigVarAnnotationBoundary(source string, end int) bool {
	if end == len(source) {
		return true
	}
	if end < 0 || end > len(source) {
		return false
	}
	switch source[end] {
	case ' ', '\t', '\r', '\n', '#':
		return true
	default:
		return false
	}
}

func appendTwigUXAnnotationTokens(
	result []lsp.SemanticToken,
	comment *twigsyntax.Node,
	pattern *regexp.Regexp,
	tokenTypes []uint32,
) []lsp.SemanticToken {
	if comment == nil {
		return result
	}
	base := comment.Range().Start
	for _, match := range pattern.FindAllStringSubmatchIndex(
		comment.Text(),
		-1,
	) {
		result = appendSemanticMatchTokens(
			result,
			base,
			match,
			tokenTypes,
		)
	}
	return result
}

func appendSemanticMatchTokens(
	result []lsp.SemanticToken,
	base uint32,
	match []int,
	tokenTypes []uint32,
) []lsp.SemanticToken {
	for group, tokenType := range tokenTypes {
		position := 2 * (group + 1)
		if position+1 >= len(match) ||
			match[position] < 0 ||
			match[position+1] <= match[position] {
			continue
		}
		result = append(result, lsp.SemanticToken{
			Range: twigsyntax.TextRange{
				Start: base + uint32(match[position]),
				End:   base + uint32(match[position+1]),
			},
			Type: tokenType,
		})
	}
	return result
}

var _ lsp.SemanticTokensProvider = (*TwigUXToolkitProvider)(nil)
