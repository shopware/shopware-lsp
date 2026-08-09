package semantic

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminMarkupProvider gives registry-backed Vue component markup semantic
// meaning in Twig. It deliberately stays out of JavaScript and TypeScript,
// where the client's native language service remains authoritative.
type AdminMarkupProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminMarkupProvider(index *admin.AdminComponentIndexer) *AdminMarkupProvider {
	return &AdminMarkupProvider{index: index}
}

type adminMarkupTokenCollector struct {
	ctx          context.Context
	provider     *AdminMarkupProvider
	request      *lsp.SemanticTokensRequest
	templatePath string
	root         *twigsyntax.Node
	owner        *admin.VueComponent

	registeredDirectives map[string]bool
	effective            map[string]*admin.VueComponent
	resolved             map[string]bool
	result               []lsp.SemanticToken
	seenLexical          map[cst.TextRange]bool
}

func (p *AdminMarkupProvider) GetSemanticTokens(
	ctx context.Context,
	request *lsp.SemanticTokensRequest,
) ([]lsp.SemanticToken, error) {
	if !p.supportsAdminMarkup(request) {
		return nil, nil
	}
	templatePath, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return nil, err
	}
	root := request.Document.SyntaxTree.Root
	owner, err := p.index.GetComponentForDocument(
		templatePath,
		root,
		string(request.Document.Text),
		request.Document.LineIndex,
	)
	if err != nil {
		return nil, err
	}
	collector := &adminMarkupTokenCollector{
		ctx:                  ctx,
		provider:             p,
		request:              request,
		templatePath:         templatePath,
		root:                 root,
		owner:                owner,
		registeredDirectives: make(map[string]bool),
		effective:            make(map[string]*admin.VueComponent),
		resolved:             make(map[string]bool),
		seenLexical:          make(map[cst.TextRange]bool),
	}
	if err := collector.loadDirectives(); err != nil {
		return nil, err
	}
	if err := collector.collectMarkup(); err != nil {
		return nil, err
	}
	collector.markExistingRanges()
	if err := collector.collectLexicalBindings(); err != nil {
		return nil, err
	}
	if err := collector.collectMemberAccesses(); err != nil {
		return nil, err
	}
	if err := collector.collectRootIdentifiers(); err != nil {
		return nil, err
	}
	sort.Slice(collector.result, func(left, right int) bool {
		if collector.result[left].Range.Start != collector.result[right].Range.Start {
			return collector.result[left].Range.Start < collector.result[right].Range.Start
		}
		return collector.result[left].Range.End < collector.result[right].Range.End
	})
	return collector.result, nil
}

func (p *AdminMarkupProvider) supportsAdminMarkup(
	request *lsp.SemanticTokensRequest,
) bool {
	if p == nil || p.index == nil || request == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil || request.Document.SyntaxTree.Root == nil {
		return false
	}
	uri := strings.ToLower(request.Document.URI)
	return (strings.HasSuffix(uri, ".twig") || strings.HasSuffix(uri, ".vue")) &&
		strings.Contains(filepathSlash(request.Document.URI), "/Resources/app/administration/")
}

func (collector *adminMarkupTokenCollector) loadDirectives() error {
	directives, err := collector.provider.index.GetAllDirectivesForTemplate(
		collector.templatePath,
	)
	if err != nil {
		return err
	}
	for _, directive := range directives {
		collector.registeredDirectives[directive.Name] = true
	}
	return nil
}

func (collector *adminMarkupTokenCollector) markExistingRanges() {
	for _, token := range collector.result {
		collector.seenLexical[token.Range] = true
	}
}
