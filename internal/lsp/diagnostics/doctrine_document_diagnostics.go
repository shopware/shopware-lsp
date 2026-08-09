package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type doctrineDocumentAnalyzer struct {
	provider          *DoctrineAnalyzer
	ctx               context.Context
	document          *lsp.TextDocument
	path              string
	validationContext context.Context
	entityNames       []string
	dbal              *dbalSchemaCatalog
	seen              map[string]struct{}
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
	mappingResult, err := p.mappingDiagnostics(document, path)
	if err != nil {
		return nil, err
	}
	result = append(result, mappingResult...)
	if strings.ToLower(filepath.Ext(path)) != ".php" {
		return result, nil
	}
	entityNames, err := p.index.EntityNames()
	if err != nil {
		return nil, err
	}
	models, err := p.index.Models()
	if err != nil {
		return nil, err
	}
	analyzer := &doctrineDocumentAnalyzer{
		provider: p,
		ctx:      ctx,
		document: document,
		path:     path,
		validationContext: p.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		),
		entityNames: entityNames,
		dbal:        newDBALSchemaCatalog(p.index, p.dalIndex, models),
		seen:        make(map[string]struct{}),
		result:      result,
	}
	if err := analyzer.scanStringLiterals(); err != nil {
		return nil, err
	}
	if err := analyzer.scanDQLReferences(); err != nil {
		return nil, err
	}
	if err := analyzer.scanMagicMethods(); err != nil {
		return nil, err
	}
	return analyzer.result, nil
}

func (analyzer *doctrineDocumentAnalyzer) scanStringLiterals() error {
	for _, literal := range phpquery.Nodes(
		analyzer.document.SyntaxTree.Root,
		phpsyntax.PhpString,
	) {
		handled, err := analyzer.scanAssistantEntity(literal)
		if err != nil {
			return err
		}
		if handled {
			continue
		}
		handled, err = analyzer.scanDBALReference(literal)
		if err != nil {
			return err
		}
		if handled {
			continue
		}
		if err := analyzer.scanORMReference(literal); err != nil {
			return err
		}
	}
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) scanAssistantEntity(
	literal *phpsyntax.Node,
) (bool, error) {
	if _, tags := php.AssistantArgumentTags(
		analyzer.validationContext,
		literal,
		"Entity",
	); len(tags) == 0 {
		return false, nil
	}
	name := phpquery.StringValue(literal)
	if name == "" {
		return true, nil
	}
	_, exists, err := analyzer.provider.index.Model(name)
	if err != nil || exists {
		return true, err
	}
	rng := phpquery.StringContentRange(literal)
	key := fmt.Sprintf("assistant-entity:%d:%d", rng.Start, rng.End)
	if analyzer.markSeen(key) {
		return true, nil
	}
	analyzer.result = append(analyzer.result, doctrineDiagnostic(
		analyzer.document,
		rng,
		missingDoctrineEntityCode,
		fmt.Sprintf("Doctrine model '%s' not found", name),
		suggestion.Similar(name, analyzer.entityNames),
	))
	return true, nil
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

func (analyzer *doctrineDocumentAnalyzer) scanORMReference(
	literal *phpsyntax.Node,
) error {
	reference, found := analyzer.provider.index.ReferenceAt(
		analyzer.validationContext,
		analyzer.document.SyntaxTree.Root,
		literal,
	)
	if !found || reference.Name == "" {
		return nil
	}
	rng := doctrine.ReferenceRange(reference)
	key := fmt.Sprintf("%d:%d:%d", reference.Role, rng.Start, rng.End)
	if analyzer.markSeen(key) {
		return nil
	}
	switch reference.Role {
	case doctrine.EntityReference:
		return analyzer.validateORMEntity(reference.Name, rng)
	case doctrine.FieldReference:
		return analyzer.validateORMField(reference.Entity, reference.Name, rng)
	default:
		return nil
	}
}

func (analyzer *doctrineDocumentAnalyzer) validateORMEntity(
	name string,
	rng cst.TextRange,
) error {
	_, exists, err := analyzer.provider.index.Model(name)
	if err != nil || exists {
		return err
	}
	analyzer.result = append(analyzer.result, doctrineDiagnostic(
		analyzer.document,
		rng,
		missingDoctrineEntityCode,
		fmt.Sprintf("Doctrine model '%s' not found", name),
		suggestion.Similar(name, analyzer.entityNames),
	))
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) validateORMField(
	entity,
	name string,
	rng cst.TextRange,
) error {
	fields, err := analyzer.provider.index.Fields(entity)
	if err != nil || len(fields) == 0 || hasDoctrineField(fields, name) {
		return err
	}
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	analyzer.result = append(analyzer.result, doctrineDiagnostic(
		analyzer.document,
		rng,
		missingDoctrineFieldCode,
		fmt.Sprintf("Doctrine field '%s' is not mapped on '%s'", name, entity),
		suggestion.Similar(name, names),
	))
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) scanDQLReferences() error {
	for _, reference := range doctrine.ValidatedDQLReferencesInDocument(
		analyzer.provider.index,
		analyzer.validationContext,
		analyzer.document.SyntaxTree.Root,
		analyzer.path,
	) {
		key := fmt.Sprintf(
			"dql:%d:%d:%d",
			reference.Role,
			reference.Range.Start,
			reference.Range.End,
		)
		if analyzer.markSeen(key) {
			continue
		}
		var err error
		switch reference.Role {
		case doctrine.DQLEntityReference:
			err = analyzer.validateDQLEntity(reference)
		case doctrine.DQLFieldReference:
			err = analyzer.validateDQLField(reference)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) validateDQLEntity(
	reference doctrine.DQLReference,
) error {
	_, exists, err := analyzer.provider.index.Model(reference.Entity)
	if err != nil || exists {
		return err
	}
	candidates := suggestion.Similar(reference.Entity, analyzer.entityNames)
	analyzer.result = append(analyzer.result, doctrineDiagnostic(
		analyzer.document,
		reference.Range,
		missingDoctrineEntityCode,
		fmt.Sprintf("Doctrine model '%s' not found in DQL", reference.Entity),
		dqlEntitySuggestions(
			analyzer.document.Source,
			reference.Range,
			candidates,
		),
	))
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) validateDQLField(
	reference doctrine.DQLReference,
) error {
	fields, err := analyzer.provider.index.Fields(reference.Entity)
	if err != nil || len(fields) == 0 || hasDoctrineField(fields, reference.Field) {
		return err
	}
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	analyzer.result = append(analyzer.result, doctrineDiagnostic(
		analyzer.document,
		reference.Range,
		missingDoctrineFieldCode,
		fmt.Sprintf(
			"Doctrine field '%s' is not mapped on '%s' in DQL",
			reference.Field,
			reference.Entity,
		),
		suggestion.Similar(reference.Field, names),
	))
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) scanMagicMethods() error {
	for _, call := range phpquery.Calls(analyzer.document.SyntaxTree.Root) {
		magic, found := analyzer.provider.index.MagicMethodAt(
			analyzer.validationContext,
			analyzer.document.SyntaxTree.Root,
			call,
		)
		if !found || len(magic.Unknown) == 0 {
			continue
		}
		completions := analyzer.provider.index.MagicMethodCompletionsAt(
			analyzer.validationContext,
			analyzer.document.SyntaxTree.Root,
			call,
		)
		names := make([]string, 0, len(completions))
		for _, completion := range completions {
			names = append(names, completion.Name)
		}
		analyzer.result = append(analyzer.result, doctrineDiagnostic(
			analyzer.document,
			magic.NameRange,
			missingDoctrineMagicFieldCode,
			fmt.Sprintf(
				"Doctrine magic method '%s' references unmapped field criteria: %s",
				magic.Name,
				strings.Join(magic.Unknown, ", "),
			),
			suggestion.Similar(magic.Name, names),
		))
	}
	return nil
}

func (analyzer *doctrineDocumentAnalyzer) markSeen(key string) bool {
	if _, exists := analyzer.seen[key]; exists {
		return true
	}
	analyzer.seen[key] = struct{}{}
	return false
}
