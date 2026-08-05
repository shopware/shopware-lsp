package validation

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	ConstraintClass          = "Symfony\\Component\\Validator\\Constraint"
	ConstraintValidatorClass = "Symfony\\Component\\Validator\\ConstraintValidatorInterface"
)

type OptionReference struct {
	Name       string
	Constraint string
	Node       *cst.Node
	Container  *cst.Node
}

func OptionReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (OptionReference, bool) {
	literal := phpquery.StringAt(node)
	item := phpquery.ArrayItemAt(literal)
	if literal == nil || item == nil ||
		phpquery.ArrayItemKey(item) != literal {
		return OptionReference{}, false
	}
	array := phpquery.ArrayAt(item)
	if array == nil || item.Parent() != array {
		return OptionReference{}, false
	}
	object := ancestorOfKind(array, phpsyntax.PhpObjectCreation)
	if object == nil || phpquery.ArgumentExpression(object, 0) != array {
		return OptionReference{}, false
	}
	className := strings.TrimPrefix(
		php.NewNameResolver(root).Resolve(
			phpquery.ObjectClassName(object),
		),
		`\`,
	)
	if className == "" || !IsConstraint(ctx, className) {
		return OptionReference{}, false
	}
	return OptionReference{
		Name:       phpquery.StringValue(literal),
		Constraint: className,
		Node:       literal,
		Container:  object,
	}, true
}

func IsConstraint(ctx context.Context, className string) bool {
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Snapshot == nil {
		return false
	}
	return phpContext.Snapshot.Relations().IsSubtype(
		types.Named(strings.TrimPrefix(className, `\`)),
		types.Named(ConstraintClass),
	)
}

func ConstraintProperties(
	phpIndex *php.PHPIndex,
	className string,
) []semantic.Symbol {
	if phpIndex == nil || className == "" {
		return nil
	}
	return constraintProperties(
		phpIndex.SemanticSnapshot(),
		className,
	)
}

func ConstraintPropertiesInContext(
	ctx context.Context,
	className string,
) []semantic.Symbol {
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Snapshot == nil {
		return nil
	}
	return constraintProperties(phpContext.Snapshot, className)
}

func constraintProperties(
	snapshot *semantic.Snapshot,
	className string,
) []semantic.Symbol {
	members := (resolver.MemberResolver{
		Snapshot: snapshot,
	}).All(types.Named(strings.TrimPrefix(className, `\`)))
	seen := make(map[string]struct{})
	var result []semantic.Symbol
	for _, member := range members {
		symbol := member.Symbol
		if symbol.Kind != semantic.PropertySymbol ||
			symbol.Visibility != semantic.Public {
			continue
		}
		key := strings.ToLower(symbol.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, symbol)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result
}

func FindConstraintProperty(
	properties []semantic.Symbol,
	name string,
) (semantic.Symbol, bool) {
	for _, property := range properties {
		if strings.EqualFold(property.Name, name) {
			return property, true
		}
	}
	return semantic.Symbol{}, false
}

func CounterpartClass(
	phpIndex *php.PHPIndex,
	class semantic.Symbol,
) (semantic.Symbol, bool) {
	if phpIndex == nil || !class.IsClassLike() {
		return semantic.Symbol{}, false
	}
	snapshot := phpIndex.SemanticSnapshot()
	classType := types.Named(class.FullyQualified)
	if snapshot.Relations().IsSubtype(
		classType,
		types.Named(ConstraintClass),
	) {
		return phpIndex.FindClass(class.FullyQualified + "Validator")
	}
	if snapshot.Relations().IsSubtype(
		classType,
		types.Named(ConstraintValidatorClass),
	) && strings.HasSuffix(class.FullyQualified, "Validator") {
		return phpIndex.FindClass(
			strings.TrimSuffix(class.FullyQualified, "Validator"),
		)
	}
	return semantic.Symbol{}, false
}

func ancestorOfKind(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}
