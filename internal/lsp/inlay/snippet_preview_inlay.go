package inlay

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// SnippetPreviewProvider is the portable counterpart of the reference
// plugin's translation folding. It keeps the source key visible and adds the
// preferred translation beside it, with navigation to the JSON declaration.
type SnippetPreviewProvider struct {
	index *snippet.SnippetIndexer
}

func NewSnippetPreviewProvider(index *snippet.SnippetIndexer) *SnippetPreviewProvider {
	return &SnippetPreviewProvider{index: index}
}

type snippetHintCandidate struct {
	key      string
	admin    bool
	position uint32
}

func (p *SnippetPreviewProvider) GetInlayHints(
	ctx context.Context,
	request *lsp.InlayHintRequest,
) ([]protocol.InlayHint, error) {
	if ctx.Err() != nil || p == nil || p.index == nil || request == nil ||
		request.Document == nil || request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil || request.Document.LineIndex == nil {
		return nil, nil
	}
	start, end := inlayHintByteRange(request)
	var result []protocol.InlayHint
	seen := make(map[string]struct{})
	for _, candidate := range snippetHintCandidates(request.Document) {
		if candidate.key == "" || candidate.position < start || candidate.position > end {
			continue
		}
		identity := fmt.Sprintf("%d\x00%t\x00%s", candidate.position, candidate.admin, candidate.key)
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		var translations []snippet.Snippet
		var err error
		if candidate.admin {
			translations, err = p.index.GetAdminSnippet(candidate.key)
		} else {
			translations, err = p.index.GetFrontendSnippet(candidate.key)
		}
		if err != nil {
			return nil, err
		}
		if len(translations) == 0 {
			continue
		}
		sort.SliceStable(translations, func(left, right int) bool {
			leftRank := snippetLocaleRank(translations[left].File)
			rightRank := snippetLocaleRank(translations[right].File)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			return translations[left].File < translations[right].File
		})
		preferred := translations[0]
		line, character := request.Document.LineIndex.PositionUTF16(candidate.position)
		part := protocol.InlayHintLabelPart{
			Value:   "→ " + compactSnippetPreview(preferred.Text),
			Tooltip: "Open translation for " + candidate.key,
		}
		if preferred.File != "" && preferred.Line > 0 {
			part.Location = &protocol.Location{
				URI: uriutil.FileURI(preferred.File),
				Range: protocol.Range{
					Start: protocol.Position{Line: preferred.Line - 1},
					End:   protocol.Position{Line: preferred.Line - 1},
				},
			}
		}
		result = append(result, protocol.InlayHint{
			Position:    protocol.Position{Line: int(line), Character: int(character)},
			Label:       []protocol.InlayHintLabelPart{part},
			Kind:        protocol.InlayHintKindType,
			Tooltip:     snippetPreviewTooltip(candidate.key, translations),
			PaddingLeft: true,
		})
	}
	return result, nil
}

func snippetHintCandidates(document *lsp.TextDocument) []snippetHintCandidate {
	root := document.SyntaxTree.Root
	var result []snippetHintCandidate
	switch document.SyntaxLanguage {
	case language.Twig:
		for _, reference := range snippet.AdminTwigReferences(root) {
			result = append(result, snippetHintCandidate{
				key: reference.Key, admin: true,
				position: reference.Range.End + 1,
			})
		}
		for _, literal := range twigquery.Nodes(root, twigsyntax.TwigLiteralString) {
			if !twigquery.StringInFilter(literal, "trans") {
				continue
			}
			result = append(result, snippetHintCandidate{
				key:      twigquery.StringValue(literal),
				position: literal.RangeTrimmedTrivia().End,
			})
		}
	case language.JavaScript:
		for _, literal := range snippet.AdminJavaScriptStringReferences(root) {
			result = append(result, snippetHintCandidate{
				key: jsquery.StringValue(literal), admin: true,
				position: literal.RangeTrimmedTrivia().End,
			})
		}
	case language.PHP:
		for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
			if !phpquery.StringInCall(literal, 0, "trans") {
				continue
			}
			result = append(result, snippetHintCandidate{
				key:      phpquery.StringValue(literal),
				position: literal.RangeTrimmedTrivia().End,
			})
		}
	}
	return result
}

func snippetLocaleRank(path string) int {
	normalized := strings.ToLower(filepath.ToSlash(path))
	switch {
	case strings.Contains(normalized, "en-gb"),
		strings.Contains(normalized, "en_gb"),
		strings.HasSuffix(normalized, "/en.json"):
		return 0
	case strings.Contains(normalized, "de-de"), strings.Contains(normalized, "de_de"):
		return 1
	default:
		return 2
	}
}

func compactSnippetPreview(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	const limit = 72
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return value
}

func snippetPreviewTooltip(key string, translations []snippet.Snippet) string {
	lines := []string{"Snippet \"" + key + "\""}
	for index, value := range translations {
		if index == 4 {
			lines = append(lines, fmt.Sprintf("… and %d more", len(translations)-index))
			break
		}
		lines = append(lines, filepath.Base(value.File)+": "+compactSnippetPreview(value.Text))
	}
	return strings.Join(lines, "\n")
}

var _ lsp.InlayHintProvider = (*SnippetPreviewProvider)(nil)
