package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/translation"
)

const (
	missingTranslationKeyCode    lsp.DiagnosticID = "symfony.translation.key.missing"
	missingTranslationDomainCode lsp.DiagnosticID = "symfony.translation.domain.missing"
)

type TranslationAnalyzer struct {
	index        *translation.Index
	phpIndex     *php.PHPIndex
	snippetIndex *snippet.SnippetIndexer
}

func NewTranslationAnalyzer(
	index *translation.Index,
	phpIndex *php.PHPIndex,
	snippetIndexes ...*snippet.SnippetIndexer,
) *TranslationAnalyzer {
	provider := &TranslationAnalyzer{
		index:    index,
		phpIndex: phpIndex,
	}
	if len(snippetIndexes) != 0 {
		provider.snippetIndex = snippetIndexes[0]
	}
	return provider
}

func (p *TranslationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	run, err := newTranslationDiagnosticsRun(ctx, document, p)
	if err != nil || run == nil {
		return nil, err
	}
	return run.analyze()
}

func (p *TranslationAnalyzer) translationExists(
	domain,
	key string,
) (bool, error) {
	found, err := p.index.HasMessage(domain, key)
	if err != nil || found {
		return found, err
	}
	if p.snippetIndex == nil || !strings.EqualFold(domain, "messages") {
		return false, nil
	}
	snippets, err := p.snippetIndex.GetFrontendSnippet(key)
	return len(snippets) != 0, err
}

func staticTranslationKey(
	extension string,
	reference translation.Reference,
) bool {
	if reference.Key == "" || strings.Contains(reference.Key, "$") {
		return false
	}
	if extension == ".twig" {
		return twigquery.StringIsStatic(reference.Node)
	}
	return true
}
