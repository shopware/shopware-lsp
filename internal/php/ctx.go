package php

import (
	"context"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// phpContextKey is a custom type for the context key to avoid collisions
type phpContextKey string

// PHPContextKey is the key used to store PHP context in the context.Context
const PHPContextKey phpContextKey = "php.context"

type PHPContext struct {
	InsideClass *semantic.Symbol
	Node        *phpsyntax.Node
	Document    *semantic.Document
	Snapshot    *semantic.Snapshot

	resolverMu   sync.Mutex
	resolver     *NameResolver
	resolverRoot *phpsyntax.Node
}

func GetPHPContext(ctx context.Context) *PHPContext {
	value, _ := ctx.Value(PHPContextKey).(*PHPContext)
	return value
}

// NameResolverFor returns the import resolver for root, sharing one resolver
// through the request's PHP context when the same tree is queried repeatedly.
// Reference lookups inspect many literals of one document; building a fresh
// resolver per literal re-walks the whole syntax tree each time.
func NameResolverFor(ctx context.Context, root *phpsyntax.Node) *NameResolver {
	if phpContext := GetPHPContext(ctx); phpContext != nil {
		return phpContext.nameResolver(root)
	}
	return NewNameResolver(root)
}

func (p *PHPContext) nameResolver(root *phpsyntax.Node) *NameResolver {
	p.resolverMu.Lock()
	defer p.resolverMu.Unlock()
	if p.resolver == nil || p.resolverRoot != root {
		p.resolver = NewNameResolver(root)
		p.resolverRoot = root
	}
	return p.resolver
}

func (p *PHPIndex) AddContext(ctx context.Context, node *phpsyntax.Node, documentContent []byte) context.Context {
	root := node
	for root != nil && root.Parent() != nil {
		root = root.Parent()
	}
	return p.AddDocumentContext(ctx, "", 0, node, root)
}

func (p *PHPIndex) AddDocumentContext(
	ctx context.Context,
	path string,
	version int,
	node *phpsyntax.Node,
	root *phpsyntax.Node,
) context.Context {
	document := p.AnalyzeDocument(path, version, root)
	return p.addAnalyzedDocumentContext(ctx, node, document)
}

// AddParsedFileContext enriches a context with the semantic document shared by
// indexers preparing one immutable file.
func (p *PHPIndex) AddParsedFileContext(
	ctx context.Context,
	file *indexer.ParsedFile,
	node *phpsyntax.Node,
) context.Context {
	return p.addAnalyzedDocumentContext(
		ctx,
		node,
		p.AnalyzeParsedFile(file),
	)
}

// AddAnalyzedDocumentContext enriches a context with an existing linked
// document. Request-time LSP features use it to share one semantic analysis
// instead of rebuilding the same open document for assistant-tag queries.
func (p *PHPIndex) AddAnalyzedDocumentContext(
	ctx context.Context,
	node *phpsyntax.Node,
	document *semantic.Document,
) context.Context {
	return p.addAnalyzedDocumentContext(ctx, node, document)
}

func (p *PHPIndex) addAnalyzedDocumentContext(
	ctx context.Context,
	node *phpsyntax.Node,
	document *semantic.Document,
) context.Context {
	return p.AddAnalyzedSnapshotContext(
		ctx,
		node,
		document,
		p.SemanticSnapshot().WithDocument(document),
	)
}

// AddAnalyzedSnapshotContext enriches a request with a semantic document and
// its already-constructed overlay snapshot. LSP requests use this boundary to
// share one revision-aware analysis instead of rebuilding the open document
// and its overlay for every provider fan-out.
func (p *PHPIndex) AddAnalyzedSnapshotContext(
	ctx context.Context,
	node *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
) context.Context {
	if snapshot == nil {
		snapshot = p.SemanticSnapshot().WithDocument(document)
	}
	var class *semantic.Symbol
	if node != nil {
		offset := node.Range().Start
		for _, symbol := range document.Symbols {
			if !symbol.IsClassLike() || !symbol.Range.Contains(offset) {
				continue
			}
			candidate := symbol
			class = &candidate
			break
		}
	}

	return context.WithValue(ctx, PHPContextKey, &PHPContext{
		InsideClass: class,
		Node:        node,
		Document:    document,
		Snapshot:    snapshot,
	})
}
