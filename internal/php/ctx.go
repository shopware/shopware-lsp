package php

import (
	"context"

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
}

func GetPHPContext(ctx context.Context) *PHPContext {
	value, _ := ctx.Value(PHPContextKey).(*PHPContext)
	return value
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

func (p *PHPIndex) addAnalyzedDocumentContext(
	ctx context.Context,
	node *phpsyntax.Node,
	document *semantic.Document,
) context.Context {
	snapshot := p.SemanticSnapshot().WithDocument(document)
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
