package doctrine

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type DBALReferenceRole uint8

const (
	DBALTableReference DBALReferenceRole = iota
	DBALColumnReference
	DBALAliasReference
)

type DBALReference struct {
	Role  DBALReferenceRole
	Name  string
	Table string
	Node  *phpsyntax.Node
	Range cst.TextRange
}

type DBALCompletionKind uint8

const (
	DBALTableCompletion DBALCompletionKind = iota
	DBALColumnCompletion
	DBALAliasCompletion
)

type DBALCompletion struct {
	Label  string
	Detail string
	Kind   DBALCompletionKind
}

func (idx *Index) DBALReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (DBALReference, bool) {
	if idx == nil || root == nil || node == nil {
		return DBALReference{}, false
	}
	literal := phpquery.StringAt(node)
	if literal == nil {
		return DBALReference{}, false
	}
	call := phpquery.CallAt(literal)
	if call == nil {
		return DBALReference{}, false
	}
	receiver := dbalReceiver(ctx, call)
	if receiver == dbalReceiverNone {
		return DBALReference{}, false
	}
	method := strings.ToLower(phpquery.CallMethodName(call))
	argument := phpquery.ArgumentIndex(call, literal)
	reference := DBALReference{
		Name: phpquery.StringValue(literal),
		Node: literal,
		Range: ReferenceRange(Reference{
			Kind: StringReference,
			Node: literal,
		}),
	}

	if receiver == dbalQueryBuilderReceiver {
		switch method {
		case "update", "insert", "from", "delete":
			if argument == 0 &&
				phpquery.ArgumentExpression(call, 0) == literal {
				reference.Role = DBALTableReference
				return reference, true
			}
		case "join", "innerjoin", "leftjoin", "rightjoin":
			if argument == 1 &&
				phpquery.ArgumentExpression(call, 1) == literal {
				reference.Role = DBALTableReference
				return reference, true
			}
			if argument == 2 &&
				phpquery.ArgumentExpression(call, 2) == literal {
				reference.Role = DBALAliasReference
				reference.Table = phpquery.StringValue(
					phpquery.StringArgument(call, 1),
				)
				return reference, true
			}
		}
		return DBALReference{}, false
	}

	if method != "insert" && method != "update" {
		return DBALReference{}, false
	}
	if argument == 0 && phpquery.ArgumentExpression(call, 0) == literal {
		reference.Role = DBALTableReference
		return reference, true
	}
	if argument != 1 || !isDBALColumnArrayLiteral(call, literal) {
		return DBALReference{}, false
	}
	reference.Role = DBALColumnReference
	reference.Table = phpquery.StringValue(phpquery.StringArgument(call, 0))
	return reference, reference.Table != ""
}

func (idx *Index) DBALCompletionsAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) []DBALCompletion {
	reference, found := idx.DBALReferenceAt(ctx, root, node)
	if !found {
		return nil
	}
	switch reference.Role {
	case DBALTableReference:
		models, err := idx.Models()
		if err != nil {
			return nil
		}
		var result []DBALCompletion
		for _, model := range models {
			if model.Table == "" {
				continue
			}
			result = append(result, DBALCompletion{
				Label:  model.Table,
				Detail: model.Class,
				Kind:   DBALTableCompletion,
			})
		}
		sort.Slice(result, func(left, right int) bool {
			return result[left].Label < result[right].Label
		})
		return result
	case DBALColumnReference:
		model, found, err := idx.ModelForTable(reference.Table)
		if err != nil || !found {
			return nil
		}
		fields, err := idx.Fields(model.Class)
		if err != nil {
			return nil
		}
		var result []DBALCompletion
		seen := make(map[string]struct{})
		for _, field := range fields {
			name := field.Column
			if name == "" {
				name = field.Name
			}
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, DBALCompletion{
				Label:  name,
				Detail: model.Class + "::" + field.Name,
				Kind:   DBALColumnCompletion,
			})
		}
		sort.Slice(result, func(left, right int) bool {
			return result[left].Label < result[right].Label
		})
		return result
	case DBALAliasReference:
		call := phpquery.CallAt(reference.Node)
		fromAlias := phpquery.StringValue(phpquery.StringArgument(call, 0))
		var result []DBALCompletion
		for _, alias := range dbalAliasSuggestions(fromAlias, reference.Table) {
			result = append(result, DBALCompletion{
				Label:  alias,
				Detail: "Doctrine DBAL join alias",
				Kind:   DBALAliasCompletion,
			})
		}
		return result
	default:
		return nil
	}
}

func (idx *Index) ModelForTable(
	table string,
) (Model, bool, error) {
	if idx == nil || table == "" {
		return Model{}, false, nil
	}
	models, err := idx.Models()
	if err != nil {
		return Model{}, false, err
	}
	for _, model := range models {
		if strings.EqualFold(model.Table, table) {
			return model, true, nil
		}
	}
	return Model{}, false, nil
}

func (idx *Index) FieldForColumn(
	table,
	column string,
) (Model, Field, bool, error) {
	model, found, err := idx.ModelForTable(table)
	if err != nil || !found {
		return Model{}, Field{}, false, err
	}
	fields, err := idx.Fields(model.Class)
	if err != nil {
		return Model{}, Field{}, false, err
	}
	for _, field := range fields {
		name := field.Column
		if name == "" {
			name = field.Name
		}
		if strings.EqualFold(name, column) {
			return model, field, true, nil
		}
	}
	return model, Field{}, false, nil
}

type dbalReceiverKind uint8

const (
	dbalReceiverNone dbalReceiverKind = iota
	dbalQueryBuilderReceiver
	dbalConnectionReceiver
)

func dbalReceiver(
	ctx context.Context,
	call *phpsyntax.Node,
) dbalReceiverKind {
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return dbalReceiverNone
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return dbalReceiverNone
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return dbalReceiverNone
	}
	relations := phpContext.Snapshot.Relations()
	if relations.IsSubtype(
		receiverType,
		types.Named("Doctrine\\DBAL\\Query\\QueryBuilder"),
	) {
		return dbalQueryBuilderReceiver
	}
	if relations.IsSubtype(
		receiverType,
		types.Named("Doctrine\\DBAL\\Connection"),
	) {
		return dbalConnectionReceiver
	}
	return dbalReceiverNone
}

func isDBALColumnArrayLiteral(
	call,
	literal *phpsyntax.Node,
) bool {
	array := phpquery.ArrayAt(phpquery.ArgumentExpression(call, 1))
	if array == nil || phpquery.ArrayAt(literal) != array {
		return false
	}
	item := phpquery.ArrayItemAt(literal)
	if item == nil {
		return false
	}
	if key := phpquery.ArrayItemKey(item); key != nil {
		return phpquery.StringAt(key) == literal
	}
	return phpquery.StringAt(phpquery.ArrayItemValue(item)) == literal
}

func dbalAliasSuggestions(fromAlias, table string) []string {
	table = strings.TrimSpace(table)
	if table == "" {
		return nil
	}
	seen := make(map[string]struct{})
	add := func(value string) {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	add(table)
	add(dbalCamelize(table))
	add(table[:1])
	add(dbalInitials(table))
	if fromAlias != "" {
		add(fromAlias + "_" + table)
		add(dbalCamelize(fromAlias + "_" + table))
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func dbalCamelize(value string) string {
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == '_' || character == '-' || unicode.IsSpace(character)
	})
	if len(parts) == 0 {
		return ""
	}
	result := strings.ToLower(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		result += strings.ToUpper(lower[:1]) + lower[1:]
	}
	return result
}

func dbalInitials(value string) string {
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == '_' || character == '-' || unicode.IsSpace(character)
	})
	var result strings.Builder
	for _, part := range parts {
		if part != "" {
			result.WriteByte(part[0])
		}
	}
	return result.String()
}
