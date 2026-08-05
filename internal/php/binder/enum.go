package binder

import (
	"strings"

	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (b *documentBuilder) enumBackingType(
	node *phpsyntax.Node,
	context resolver.NameContext,
) types.Type {
	afterColon := false
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *phpsyntax.Node:
			if child.Kind() == phpsyntax.PhpClassBody {
				return types.Unknown()
			}
		case *phpsyntax.Token:
			if child.Kind() == phpsyntax.TkColon {
				afterColon = true
				continue
			}
			if !afterColon || child.Kind().IsTrivia() {
				continue
			}
			switch strings.ToLower(child.Text()) {
			case "int", "string":
				return b.bindNativeType(child.Text(), context)
			default:
				return types.Unknown()
			}
		}
	}
	return types.Unknown()
}

func (b *documentBuilder) bindEnumRuntimeMembers(
	scope semantic.ScopeID,
	enum semantic.Symbol,
	backing types.Type,
) {
	b.bindEnumRuntimeMethod(
		scope,
		enum,
		"cases",
		types.List(types.Named(enum.FullyQualified)),
	)
	b.bindEnumRuntimeProperty(scope, enum, "name", types.String())
	if !backing.IsUnknown() {
		b.bindEnumRuntimeProperty(scope, enum, "value", backing)
		valueParameter := semantic.Parameter{
			Name:       "$value",
			Type:       backing,
			NativeType: backing,
		}
		b.bindEnumRuntimeMethod(
			scope,
			enum,
			"from",
			types.Named(enum.FullyQualified),
			valueParameter,
		)
		b.bindEnumRuntimeMethod(
			scope,
			enum,
			"tryFrom",
			types.Nullable(types.Named(enum.FullyQualified)),
			valueParameter,
		)
	}
}

func (b *documentBuilder) bindEnumRuntimeMethod(
	scope semantic.ScopeID,
	enum semantic.Symbol,
	name string,
	result types.Type,
	parameters ...semantic.Parameter,
) {
	fullyQualified := enum.FullyQualified + "::" + name
	b.addSymbol(scope, semantic.Symbol{
		ID: semantic.NewSymbolID(
			semantic.MethodSymbol,
			fullyQualified,
			b.document.Path,
			0,
		),
		Kind:           semantic.MethodSymbol,
		Name:           name,
		FullyQualified: fullyQualified,
		Container:      enum.ID,
		Path:           b.document.Path,
		Visibility:     semantic.Public,
		Flags: semantic.StaticFlag |
			semantic.InternalFlag |
			semantic.SyntheticFlag,
		ReturnType: result,
		NativeType: result,
		Parameters: parameters,
	})
}

func (b *documentBuilder) bindEnumRuntimeProperty(
	scope semantic.ScopeID,
	enum semantic.Symbol,
	name string,
	value types.Type,
) {
	fullyQualified := enum.FullyQualified + "::$" + name
	b.addSymbol(scope, semantic.Symbol{
		ID: semantic.NewSymbolID(
			semantic.PropertySymbol,
			fullyQualified,
			b.document.Path,
			0,
		),
		Kind:           semantic.PropertySymbol,
		Name:           name,
		FullyQualified: fullyQualified,
		Container:      enum.ID,
		Path:           b.document.Path,
		Visibility:     semantic.Public,
		Flags: semantic.ReadonlyFlag |
			semantic.InternalFlag |
			semantic.SyntheticFlag,
		Type:       value,
		NativeType: value,
	})
}
