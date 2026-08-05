package console

import (
	"context"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type ReferenceRole uint8

const (
	ReferenceNone ReferenceRole = iota
	ReferenceCommand
	ReferenceArgument
	ReferenceOption
)

type Reference struct {
	Role      ReferenceRole
	Name      string
	Node      *phpsyntax.Node
	Container *phpsyntax.Node
}

func ReferenceAt(node *phpsyntax.Node) (Reference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return Reference{}, false
	}
	call := phpquery.CallAt(literal)
	if call == nil ||
		phpquery.ArgumentIndex(call, literal) != 0 ||
		phpquery.ArgumentExpression(call, 0) != literal {
		return Reference{}, false
	}
	var role ReferenceRole
	switch strings.ToLower(phpquery.CallMethodName(call)) {
	case "getargument", "hasargument":
		role = ReferenceArgument
	case "getoption", "hasoption":
		role = ReferenceOption
	case "find", "get", "has":
		role = ReferenceCommand
	default:
		return Reference{}, false
	}
	return Reference{
		Role:      role,
		Name:      phpquery.StringValue(literal),
		Node:      literal,
		Container: call,
	}, true
}

func ValidateReference(
	ctx context.Context,
	reference Reference,
) bool {
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil ||
		phpContext.Document == nil ||
		phpContext.Snapshot == nil ||
		reference.Container == nil {
		return false
	}
	receiver := phpquery.CallReceiver(reference.Container)
	if receiver == nil {
		return false
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return false
	}
	target := "Symfony\\Component\\Console\\Input\\InputInterface"
	if reference.Role == ReferenceCommand {
		target = "Symfony\\Component\\Console\\Application"
	}
	return phpContext.Snapshot.Relations().IsSubtype(
		receiverType,
		types.Named(target),
	)
}

func InputsForReference(
	ctx context.Context,
	index *Index,
	reference Reference,
) ([]Input, error) {
	if index == nil ||
		(reference.Role != ReferenceArgument &&
			reference.Role != ReferenceOption) {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil {
		return nil, nil
	}
	className := ""
	if phpContext.InsideClass != nil {
		className = phpContext.InsideClass.FullyQualified
	} else if reference.Node != nil {
		offset := reference.Node.Range().Start
		for _, symbol := range phpContext.Document.Symbols {
			if symbol.IsClassLike() && symbol.Range.Contains(offset) {
				className = symbol.FullyQualified
				break
			}
		}
	}
	if className == "" {
		return nil, nil
	}
	method := phpquery.MethodName(reference.Node)
	kind := Argument
	if reference.Role == ReferenceOption {
		kind = Option
	}
	return index.InputsForTarget(
		className,
		method,
		kind,
	)
}
