package entityschema

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// IndexedCatalog discovers class-based entity schemas from the immutable PHP
// semantic generation. It deliberately does not own another watcher or cache.
type IndexedCatalog struct {
	php       *php.PHPIndex
	sources   *SourceIndex
	readiness *indexer.WorkspaceSymbolCatalog
}

var ErrIndexNotReady = errors.New("entity schema index is not ready; wait for workspace indexing to finish")

func NewIndexedCatalog(
	index *php.PHPIndex,
	sources *SourceIndex,
	readiness ...*indexer.WorkspaceSymbolCatalog,
) *IndexedCatalog {
	if index == nil {
		return nil
	}
	catalog := &IndexedCatalog{php: index, sources: sources}
	if len(readiness) != 0 {
		catalog.readiness = readiness[0]
	}
	return catalog
}

func (catalog *IndexedCatalog) Scan(
	pluginRoot string,
	external RelationLookup,
) (Schema, []ScannedDefinition, error) {
	return catalog.ScanContext(context.Background(), pluginRoot, external)
}

func (catalog *IndexedCatalog) ScanContext(
	ctx context.Context,
	pluginRoot string,
	external RelationLookup,
) (Schema, []ScannedDefinition, error) {
	if catalog == nil || catalog.php == nil {
		return EmptySchema(), nil, nil
	}
	if catalog.readiness != nil {
		ready, err := catalog.readiness.Ready(ctx)
		if err != nil {
			return Schema{}, nil, err
		}
		if !ready {
			return Schema{}, nil, ErrIndexNotReady
		}
	}
	root := filepath.Clean(pluginRoot)
	symbols := catalog.php.ClassSymbolsView()
	snapshot := catalog.php.SemanticSnapshot()
	declarations := make([]IndexedClassDeclaration, 0)
	sourceByPath := make(map[string]string)
	for _, symbol := range symbols {
		if !symbol.IsClassLike() || symbol.Path == "" ||
			!withinDirectory(root, symbol.Path) ||
			!classBasedDALSubtype(snapshot, symbol.FullyQualified) {
			continue
		}
		declaration := IndexedClassDeclaration{
			Path:     symbol.Path,
			Class:    symbol.FullyQualified,
			Parents:  append([]string(nil), symbol.Extends()...),
			Abstract: symbol.Flags.Has(semantic.AbstractFlag),
		}
		if catalog.sources != nil && !declaration.Abstract {
			source, cached := sourceByPath[symbol.Path]
			if !cached {
				var found bool
				var err error
				source, found, err = catalog.sources.Source(symbol.Path)
				if err != nil {
					return Schema{}, nil, err
				}
				if found {
					sourceByPath[symbol.Path] = source
				}
			}
			declaration.Source = source
		}
		declarations = append(declarations, declaration)
	}
	if catalog.sources != nil {
		return scanIndexedPluginSchemaWithEnricher(root, declarations, external, false, catalog.EnrichSpec)
	}
	return scanIndexedPluginSchemaWithEnricher(root, declarations, external, true, catalog.EnrichSpec)
}

// EnrichSpec resolves information which is semantic rather than present in a
// class-based field constructor. In particular EnumField accepts an enum case,
// while its physical SQL type comes from the declaration's backing type.
func (catalog *IndexedCatalog) EnrichSpec(spec *EntitySpec) {
	if catalog == nil || catalog.php == nil || spec == nil {
		return
	}
	enrich := func(fields []FieldSpec) {
		for index := range fields {
			catalog.enrichEnumField(&fields[index])
		}
	}
	enrich(spec.Fields)
	if spec.DefinitionBehavior != nil {
		enrich(spec.DefinitionBehavior.DefaultFields)
		enrich(spec.DefinitionBehavior.BaseFields)
	}
	if spec.Translation != nil && spec.Translation.DefinitionBehavior != nil {
		enrich(spec.Translation.DefinitionBehavior.DefaultFields)
		enrich(spec.Translation.DefinitionBehavior.BaseFields)
	}
	for index := range spec.BulkExtensions {
		enrich(spec.BulkExtensions[index].Fields)
	}
}

func (catalog *IndexedCatalog) enrichEnumField(field *FieldSpec) {
	if field == nil || field.Kind != FieldEnum || field.EnumBackingType != "" || field.EnumClass == "" {
		return
	}
	enum, found := catalog.php.FindClass(field.EnumClass)
	if !found || enum.Kind != semantic.EnumSymbol {
		return
	}
	for _, member := range catalog.php.SemanticSnapshot().Members(enum.ID, "value") {
		if member.Kind != semantic.PropertySymbol {
			continue
		}
		switch member.Type.String() {
		case "string":
			field.EnumBackingType = "string"
		case "int":
			field.EnumBackingType = "int"
		}
		if field.EnumBackingType != "" {
			return
		}
	}
}

type classSubtypeSnapshot interface {
	IsSubtypeOf(className, targetName string) bool
}

func classBasedDALSubtype(snapshot classSubtypeSnapshot, className string) bool {
	if snapshot == nil || className == "" {
		return false
	}
	for _, base := range []string{
		`Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition`,
		`Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition`,
		`Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition`,
		`Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension`,
		`Shopware\Core\Framework\DataAbstractionLayer\EntityExtension`,
	} {
		if snapshot.IsSubtypeOf(className, base) {
			return true
		}
	}
	return false
}
