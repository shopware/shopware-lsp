package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineCompletionProvider struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
}

func NewDoctrineCompletionProvider(
	index *doctrine.Index,
	phpIndexes ...*php.PHPIndex,
) *DoctrineCompletionProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &DoctrineCompletionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *DoctrineCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Document == nil {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	extension := strings.ToLower(filepath.Ext(path))
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if registration, found := doctrine.TypeRegistrationReferenceAt(
		path,
		request.Root,
		offset,
	); found && registration.Role == doctrine.TypeRegistrationClass {
		return p.dbalTypeClassCompletions(
			registration,
			extension,
			request.Document.Source,
		)
	}
	if reference, found := doctrine.MappingReferenceAt(
		path,
		request.Root,
		request.Document.Source,
		offset,
	); found {
		if items := p.mappingCompletions(
			reference,
			path,
			request.Root,
			extension,
			request.Document.Source,
		); len(items) != 0 {
			return items
		}
	}
	if extension != ".php" {
		return nil
	}
	if request.Node == nil {
		return nil
	}
	if reference, found := php.AssistantArgumentReference(
		ctx,
		request.Node,
		"Entity",
	); found {
		return p.assistantEntityCompletions(request, reference)
	}
	if items := dbalCompletionItems(p.index.DBALCompletionsAt(
		ctx,
		request.Root,
		request.Node,
	)); len(items) != 0 {
		return items
	}
	reference, found := p.index.ReferenceAt(
		ctx,
		request.Root,
		request.Node,
	)
	if !found {
		queryItems := queryCompletionItems(p.index.QueryCompletionsAt(
			ctx,
			request.Root,
			request.Node,
			offset,
		))
		if len(queryItems) != 0 {
			return queryItems
		}
		return magicMethodCompletionItems(
			p.index.MagicMethodCompletionsAt(
				ctx,
				request.Root,
				request.Node,
			),
		)
	}
	switch reference.Role {
	case doctrine.EntityReference:
		return p.entityCompletions(reference)
	case doctrine.FieldReference:
		return p.fieldCompletions(reference)
	default:
		return nil
	}
}

func (p *DoctrineCompletionProvider) dbalTypeClassCompletions(
	reference doctrine.TypeRegistrationReference,
	extension,
	source string,
) []protocol.CompletionItem {
	if p.phpIndex == nil {
		return nil
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	var classes []string
	for _, symbol := range p.phpIndex.ClassSymbols() {
		if strings.EqualFold(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) || !snapshot.IsSubtypeOf(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) {
			continue
		}
		classes = append(classes, symbol.FullyQualified)
	}
	items := mappingClassCompletionItems(
		classes,
		doctrine.MappingReference{Range: reference.Range},
		extension,
		source,
		"Doctrine DBAL type class",
		reference.ClassConstant,
	)
	if !reference.ObjectCreation {
		return items
	}
	for index := range items {
		className := items[index].Label
		if items[index].FilterText != "" {
			className = items[index].FilterText
		}
		short := className
		if separator := strings.LastIndex(short, `\`); separator >= 0 {
			short = short[separator+1:]
		}
		items[index].Label = short
		items[index].FilterText = className
		prefix := `new `
		if reference.ObjectCreationStarted {
			prefix = ""
		}
		items[index].InsertText = prefix + `\` + className + "()"
	}
	return items
}

func (p *DoctrineCompletionProvider) assistantEntityCompletions(
	request *lsp.CompletionRequest,
	reference cst.TextRange,
) []protocol.CompletionItem {
	items := p.entityCompletions(doctrine.Reference{})
	if request == nil || request.LineIndex == nil {
		return items
	}
	replacement := namedArgumentCompletionRange(reference, request.LineIndex)
	for index := range items {
		items[index].TextEdit = protocol.TextEdit{
			Range:   replacement,
			NewText: items[index].Label,
		}
	}
	return items
}

func dbalCompletionItems(
	completions []doctrine.DBALCompletion,
) []protocol.CompletionItem {
	result := make([]protocol.CompletionItem, 0, len(completions))
	for _, completion := range completions {
		kind := protocol.FieldCompletion
		switch completion.Kind {
		case doctrine.DBALTableCompletion:
			kind = protocol.StructCompletion
		case doctrine.DBALAliasCompletion:
			kind = protocol.VariableCompletion
		}
		result = append(result, protocol.CompletionItem{
			Label:  completion.Label,
			Kind:   int(kind),
			Detail: completion.Detail,
		})
	}
	return result
}

func (p *DoctrineCompletionProvider) mappingCompletions(
	reference doctrine.MappingReference,
	path string,
	root *cst.Node,
	extension,
	source string,
) []protocol.CompletionItem {
	if p.phpIndex == nil {
		return nil
	}
	switch reference.Role {
	case doctrine.MappingType:
		var result []protocol.CompletionItem
		seen := make(map[string]struct{})
		for _, name := range doctrine.BuiltInTypes() {
			seen[strings.ToLower(name)] = struct{}{}
			result = append(result, protocol.CompletionItem{
				Label:  name,
				Kind:   int(protocol.ValueCompletion),
				Detail: "Doctrine built-in type",
			})
		}
		declarations := doctrine.TypeDeclarationsForMapping(
			path,
			p.index.TypeDeclarations(p.phpIndex),
		)
		for _, declaration := range declarations {
			key := strings.ToLower(declaration.Name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, protocol.CompletionItem{
				Label:  declaration.Name,
				Kind:   int(protocol.ValueCompletion),
				Detail: declaration.Class,
			})
		}
		return result
	case doctrine.MappingProperty:
		properties := p.phpIndex.Properties(reference.Owner)
		result := make([]protocol.CompletionItem, 0, len(properties))
		for _, property := range properties {
			result = append(result, protocol.CompletionItem{
				Label:  property.Name,
				Kind:   int(protocol.FieldCompletion),
				Detail: reference.Owner,
			})
		}
		return result
	case doctrine.MappingConstraintField,
		doctrine.MappingConstraintColumn:
		fields := p.mappingConstraintFields(
			reference.Owner,
			path,
			root,
			source,
		)
		result := make([]protocol.CompletionItem, 0, len(fields))
		seen := make(map[string]struct{}, len(fields))
		for _, field := range fields {
			label := field.Name
			detail := reference.Owner
			if reference.Role == doctrine.MappingConstraintColumn {
				label = field.Column
				if label == "" {
					label = field.Name
				}
				detail += " · $" + field.Name
			}
			key := strings.ToLower(label)
			if label == "" {
				continue
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, protocol.CompletionItem{
				Label:  label,
				Kind:   int(protocol.FieldCompletion),
				Detail: detail,
			})
		}
		return result
	case doctrine.MappingLifecycleMethod:
		methods := p.phpIndex.Methods(reference.Owner)
		result := make([]protocol.CompletionItem, 0, len(methods))
		for _, method := range methods {
			result = append(result, protocol.CompletionItem{
				Label:  method.Name,
				Kind:   int(protocol.MethodCompletion),
				Detail: reference.Owner,
			})
		}
		return result
	case doctrine.MappingDiscriminatorClass:
		return mappingClassCompletionItems(
			doctrine.DiscriminatorClasses(
				p.phpIndex,
				reference.Owner,
			),
			reference,
			extension,
			source,
			"Doctrine discriminator class",
			false,
		)
	case doctrine.MappingRepositoryClass:
		var symbols []string
		snapshot := p.phpIndex.SemanticSnapshot()
		for _, symbol := range p.phpIndex.ClassSymbols() {
			name := symbol.FullyQualified
			if snapshot.IsSubtypeOf(
				name,
				"Doctrine\\Persistence\\ObjectRepository",
			) || snapshot.IsSubtypeOf(
				name,
				"Doctrine\\Common\\Persistence\\ObjectRepository",
			) || snapshot.IsSubtypeOf(
				name,
				"Doctrine\\ORM\\EntityRepository",
			) || strings.HasSuffix(
				strings.ToLower(symbol.Name),
				"repository",
			) {
				symbols = append(symbols, name)
			}
		}
		return mappingClassCompletionItems(
			symbols,
			reference,
			extension,
			source,
			"Doctrine repository",
			false,
		)
	case doctrine.MappingModelClass,
		doctrine.MappingTargetClass,
		doctrine.MappingEmbeddedClass,
		doctrine.MappingEnumClass:
		return mappingClassCompletionItems(
			p.phpIndex.ClassNames(),
			reference,
			extension,
			source,
			"PHP class",
			false,
		)
	default:
		return nil
	}
}

func (p *DoctrineCompletionProvider) mappingConstraintFields(
	owner,
	path string,
	root *cst.Node,
	source string,
) []doctrine.Field {
	var result []doctrine.Field
	for _, model := range doctrine.ModelsInDocument(
		path,
		root,
		source,
	) {
		if strings.EqualFold(model.Class, owner) {
			result = append(result, model.Fields...)
		}
	}
	if p.index != nil {
		if fields, err := p.index.Fields(owner); err == nil {
			result = append(result, fields...)
		}
	}
	return result
}

func mappingClassCompletionItems(
	classNames []string,
	reference doctrine.MappingReference,
	extension,
	source,
	detail string,
	forceClassConstant bool,
) []protocol.CompletionItem {
	classConstant := forceClassConstant ||
		(extension == ".php" &&
			mappingClassConstantAt(source, reference.Range.End))
	result := make([]protocol.CompletionItem, 0, len(classNames))
	seen := make(map[string]struct{}, len(classNames))
	for _, className := range classNames {
		className = strings.TrimPrefix(className, `\`)
		key := strings.ToLower(className)
		if className == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		item := protocol.CompletionItem{
			Label:  className,
			Kind:   int(protocol.ClassCompletion),
			Detail: detail,
		}
		if classConstant {
			short := className
			if separator := strings.LastIndex(short, `\`); separator >= 0 {
				short = short[separator+1:]
			}
			item.Label = short
			item.FilterText = className
			item.InsertText = `\` + className + "::class"
		}
		result = append(result, item)
	}
	return result
}

func mappingClassConstantAt(source string, end uint32) bool {
	if int(end) > len(source) {
		return false
	}
	suffix := source[end:]
	if len(suffix) > len("::class") {
		suffix = suffix[:len("::class")]
	}
	return strings.EqualFold(suffix, "::class")
}

func magicMethodCompletionItems(
	completions []doctrine.MagicMethodCompletion,
) []protocol.CompletionItem {
	result := make([]protocol.CompletionItem, 0, len(completions))
	for _, completion := range completions {
		result = append(result, protocol.CompletionItem{
			Label: completion.Name,
			Kind:  int(protocol.MethodCompletion),
			Detail: completion.ReturnType + " · Doctrine field " +
				completion.Field.Name,
		})
	}
	return result
}

func (p *DoctrineCompletionProvider) entityCompletions(
	reference doctrine.Reference,
) []protocol.CompletionItem {
	models, err := p.index.Models()
	if err != nil {
		return nil
	}
	var result []protocol.CompletionItem
	for _, model := range models {
		if model.Kind == doctrine.MappedSuperclassModel ||
			model.Kind == doctrine.EmbeddableModel {
			continue
		}
		item := protocol.CompletionItem{
			Label:  model.Class,
			Kind:   int(protocol.ClassCompletion),
			Detail: "Doctrine " + model.Kind.String(),
		}
		if model.Table != "" {
			item.Detail += " · " + model.Table
		}
		if reference.Kind == doctrine.ClassConstantReference {
			short := model.Class
			if separator := strings.LastIndex(short, `\`); separator >= 0 {
				short = short[separator+1:]
			}
			item.Label = short
			item.FilterText = model.Class
			item.InsertText = `\` + model.Class + "::class"
		}
		result = append(result, item)
	}
	if reference.Kind == doctrine.StringReference {
		aliases, aliasErr := p.index.ModelAliases()
		if aliasErr == nil {
			for _, alias := range aliases {
				result = append(result, protocol.CompletionItem{
					Label: alias.Name,
					Kind:  int(protocol.ClassCompletion),
					Detail: "Doctrine namespace shortcut · " +
						alias.Class,
				})
			}
		}
	}
	return result
}

func (p *DoctrineCompletionProvider) fieldCompletions(
	reference doctrine.Reference,
) []protocol.CompletionItem {
	fields, err := p.index.Fields(reference.Entity)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var result []protocol.CompletionItem
	for _, field := range fields {
		key := strings.ToLower(field.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		detail := field.Type
		if field.IsAssociation() {
			detail = field.RelationType
			if field.Relation != "" {
				detail += " · " + field.Relation
			}
		} else if field.IsEmbedded() {
			detail = "Embedded · " + field.EmbeddedClass
		}
		result = append(result, protocol.CompletionItem{
			Label:  field.Name,
			Kind:   int(protocol.FieldCompletion),
			Detail: detail,
		})
	}
	return result
}

func (p *DoctrineCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\"", ".", ":"}
}

func queryCompletionItems(
	completions []doctrine.QueryCompletion,
) []protocol.CompletionItem {
	result := make([]protocol.CompletionItem, 0, len(completions))
	for _, completion := range completions {
		kind := protocol.FieldCompletion
		switch completion.Kind {
		case doctrine.QueryParameterCompletion:
			kind = protocol.VariableCompletion
		case doctrine.QueryEntityCompletion:
			kind = protocol.ClassCompletion
		case doctrine.QueryKeywordCompletion:
			kind = protocol.KeywordCompletion
		case doctrine.QueryFunctionCompletion:
			kind = protocol.FunctionCompletion
		}
		result = append(result, protocol.CompletionItem{
			Label:      completion.Label,
			Kind:       int(kind),
			Detail:     completion.Detail,
			InsertText: completion.InsertText,
		})
	}
	return result
}
