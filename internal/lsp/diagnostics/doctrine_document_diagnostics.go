package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/phpanalysis"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type doctrineDocumentAnalyzer struct {
	provider          *DoctrineAnalyzer
	ctx               context.Context
	document          *lsp.TextDocument
	validationContext context.Context
	dbal              *dbalSchemaCatalog
	result            []lsp.Problem
}

func (p *DoctrineAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	result := p.typeRegistrationDiagnostics(document, path)
	if strings.ToLower(filepath.Ext(path)) != ".php" {
		return result, nil
	}
	validationContext, err := phpanalysis.ContextForDocument(ctx, p.phpIndex, document)
	if err != nil {
		return nil, err
	}
	analyzer := &doctrineDocumentAnalyzer{
		provider:          p,
		ctx:               ctx,
		document:          document,
		validationContext: validationContext,
		dbal:              newDBALSchemaCatalog(p.dalIndex),
		result:            result,
	}
	if err := analyzer.scanStringLiterals(); err != nil {
		return nil, err
	}
	return analyzer.result, nil
}

func (analyzer *doctrineDocumentAnalyzer) scanStringLiterals() error {
	for _, literal := range phpquery.Nodes(
		analyzer.document.SyntaxTree.Root,
		phpsyntax.PhpString,
	) {
		if analyzer.ctx.Err() != nil {
			return analyzer.ctx.Err()
		}
		if _, err := analyzer.scanDBALReference(literal); err != nil {
			return err
		}
	}
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) scanDBALReference(
	literal *phpsyntax.Node,
) (bool, error) {
	reference, found := analyzer.provider.index.DBALReferenceAt(
		analyzer.validationContext,
		analyzer.document.SyntaxTree.Root,
		literal,
	)
	if !found {
		return false, nil
	}
	switch reference.Role {
	case doctrine.DBALTableReference:
		return true, analyzer.validateDBALTable(reference)
	case doctrine.DBALColumnReference:
		return true, analyzer.validateDBALColumn(reference)
	default:
		return true, nil
	}
}

func (analyzer *doctrineDocumentAnalyzer) validateDBALTable(
	reference doctrine.DBALReference,
) error {
	exists, err := analyzer.dbal.HasTable(reference.Name)
	if err != nil || exists {
		return err
	}
	names, err := analyzer.dbal.TableNames()
	if err != nil {
		return err
	}
	suggestions := adminNearbySuggestions(reference.Name, names)
	if len(suggestions) == 0 {
		return nil
	}
	analyzer.result = append(analyzer.result, doctrineDiagnostic(
		analyzer.document,
		reference.Range,
		missingDoctrineTableCode,
		fmt.Sprintf("Doctrine DBAL table '%s' not found", reference.Name),
		suggestions,
	))
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) validateDBALColumn(
	reference doctrine.DBALReference,
) error {
	columns, tableExists, err := analyzer.dbal.Columns(reference.Table)
	if err != nil || !tableExists || hasDBALSchemaName(columns, reference.Name) {
		return err
	}
	suggestions := adminNearbySuggestions(reference.Name, columns)
	if len(suggestions) == 0 {
		return nil
	}
	analyzer.result = append(analyzer.result, doctrineDiagnostic(
		analyzer.document,
		reference.Range,
		missingDoctrineColumnCode,
		fmt.Sprintf(
			"Doctrine DBAL column '%s' not found on table '%s'",
			reference.Name,
			reference.Table,
		),
		suggestions,
	))
	return nil
}
