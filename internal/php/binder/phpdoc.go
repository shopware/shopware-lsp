package binder

import (
	"strings"

	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/phpdoc"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func leadingDocComment(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	rng := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	if trimmed.Start <= rng.Start {
		return ""
	}
	prefixLength := int(trimmed.Start - rng.Start)
	text := node.Text()
	if prefixLength > len(text) {
		prefixLength = len(text)
	}
	prefix := text[:prefixLength]
	start := strings.LastIndex(prefix, "/**")
	if start < 0 {
		return ""
	}
	end := strings.Index(prefix[start:], "*/")
	if end < 0 {
		return ""
	}
	end += start + 2
	if strings.TrimSpace(prefix[end:]) != "" {
		return ""
	}
	return prefix[start:end]
}

func effectiveType(native, documented types.Type) types.Type {
	if documented.IsUnknown() || documented.Kind() == types.ErrorKind {
		return native
	}
	if native.IsUnknown() || native.Kind() == types.MixedKind {
		return documented
	}
	if documented.Kind() == types.SelfKind &&
		native.Kind() == types.SelfKind {
		return documented
	}
	if documented.Kind() == types.ParentKind &&
		native.Kind() == types.ParentKind {
		return documented
	}
	if documented.Kind() == types.StaticKind {
		switch native.Kind() {
		case types.SelfKind, types.ObjectKind:
			return documented
		}
		if base, nullable := nullableNativeBase(native); nullable &&
			base.Kind() == types.ObjectKind {
			return types.Nullable(documented)
		}
	}
	if documented.Kind() == types.CallableKind &&
		native.Kind() == types.CallableKind {
		return documented
	}
	if documented.Kind() == types.ConditionalKind {
		return documented
	}
	if types.ContainsTemplate(documented) {
		return documented
	}
	// PHPDoc commonly narrows an interface or abstract native declaration to
	// the concrete object accepted by a particular implementation. Binding
	// happens before a workspace hierarchy is available, so two differently
	// named object types cannot be proven related here. Preserve the documented
	// contract and retain NativeType separately for later compatibility checks.
	if documented.Kind() == types.ObjectKind &&
		native.Kind() == types.ObjectKind {
		return documented
	}
	if base, nullable := nullableNativeBase(native); nullable &&
		(types.Relations{}).IsSubtype(documented, base) {
		// Native nullability remains part of the runtime contract when PHPDoc
		// only refines the non-null container/object payload. This common
		// spelling (`?array` plus `@return array<K,V>`) should retain both
		// pieces of information.
		return types.Nullable(documented)
	}
	if (types.Relations{}).IsSubtype(documented, native) {
		return documented
	}
	return native
}

func nullableNativeBase(value types.Type) (types.Type, bool) {
	if value.Kind() != types.UnionKind || value.ArgumentCount() != 2 {
		return types.Unknown(), false
	}
	left := value.Argument(0)
	right := value.Argument(1)
	switch {
	case left.Kind() == types.NullKind:
		return right, true
	case right.Kind() == types.NullKind:
		return left, true
	default:
		return types.Unknown(), false
	}
}

func bindTemplates(
	values []phpdoc.Template,
	context resolver.NameContext,
	inherited ...string,
) []semantic.TemplateParameter {
	names := append([]string(nil), inherited...)
	for _, value := range values {
		names = append(names, value.Name)
	}
	result := make([]semantic.TemplateParameter, 0, len(values))
	for _, value := range values {
		result = append(result, semantic.TemplateParameter{
			Name:          value.Name,
			Bound:         context.ResolvePHPDocType(value.Bound, names),
			Default:       context.ResolvePHPDocType(value.Default, names),
			Covariant:     value.Covariant,
			Contravariant: value.Contravariant,
		})
	}
	return result
}

func bindAssertions(
	values []phpdoc.Assertion,
	context resolver.NameContext,
	templates []string,
) []semantic.TypeAssertion {
	result := make([]semantic.TypeAssertion, 0, len(values))
	for _, value := range values {
		result = append(result, semantic.TypeAssertion{
			Target:      value.Target,
			Type:        context.ResolvePHPDocType(value.Type, templates),
			WhenTrue:    value.WhenTrue,
			Conditional: value.Conditional,
			Negated:     value.Negated,
		})
	}
	return result
}

func resolveDocTypes(
	values []types.Type,
	context resolver.NameContext,
	templates []string,
) []types.Type {
	result := make([]types.Type, 0, len(values))
	for _, value := range values {
		result = append(result, context.ResolvePHPDocType(value, templates))
	}
	return result
}

func (b *documentBuilder) withPHPDocAliases(
	context resolver.NameContext,
	className string,
	documentation phpdoc.Document,
	templates []string,
) resolver.NameContext {
	aliases := make(
		map[string]types.Type,
		len(context.PHPDocAliases)+len(documentation.Imports)+
			len(documentation.Aliases),
	)
	for name, value := range context.PHPDocAliases {
		aliases[name] = value
	}
	for localName, imported := range documentation.Imports {
		aliases[localName] = types.PHPDocAlias(
			context.ResolveClass(imported.From),
			imported.Name,
		)
	}
	for name := range documentation.Aliases {
		aliases[name] = types.PHPDocAlias(className, name)
	}
	if len(aliases) == 0 {
		return context
	}
	context.PHPDocAliases = aliases
	if b.document.TypeAliases == nil {
		b.document.TypeAliases = make(map[string]types.Type, len(aliases))
	}
	for localName := range documentation.Imports {
		b.document.TypeAliases[localName] = aliases[localName]
	}
	for name, value := range documentation.Aliases {
		b.document.TypeAliases[name] = context.ResolvePHPDocType(
			value,
			templates,
		)
	}
	return context
}

func (b *documentBuilder) bindTypeAliases(
	documentation phpdoc.Document,
	scope semantic.ScopeID,
	context resolver.NameContext,
	container semantic.SymbolID,
	templates []string,
) {
	for name, value := range documentation.Aliases {
		resolved := context.ResolvePHPDocType(value, templates)
		fullyQualified := string(container) + "::" + name
		b.addSymbol(scope, semantic.Symbol{
			ID: semantic.NewSymbolID(
				semantic.TypeAliasSymbol,
				fullyQualified,
				b.document.Path,
				0,
			),
			Kind:           semantic.TypeAliasSymbol,
			Name:           name,
			FullyQualified: fullyQualified,
			Container:      container,
			Path:           b.document.Path,
			Flags:          semantic.SyntheticFlag,
			Type:           resolved,
			DocType:        resolved,
		})
	}
}

func applyDocumentFlags(symbol *semantic.Symbol, documentation phpdoc.Document) {
	if documentation.Deprecated {
		symbol.Flags |= semantic.DeprecatedFlag
	}
	if documentation.Internal {
		symbol.Flags |= semantic.InternalFlag
	}
	if documentation.Final {
		symbol.Flags |= semantic.SoftFinalFlag
	}
}

func applyAttributeFlags(symbol *semantic.Symbol) {
	if symbol == nil {
		return
	}
	for _, attribute := range symbol.Attributes() {
		name := strings.TrimPrefix(attribute.Name, "\\")
		if strings.EqualFold(name, "Deprecated") ||
			strings.HasSuffix(strings.ToLower(name), "\\deprecated") {
			symbol.Flags |= semantic.DeprecatedFlag
			return
		}
	}
}

func (b *documentBuilder) bindSyntheticMembers(
	documentation phpdoc.Document,
	scope semantic.ScopeID,
	context resolver.NameContext,
	container semantic.SymbolID,
) {
	templates := b.containerTemplateNames(container)
	for _, property := range documentation.Properties {
		if b.memberExists(container, semantic.PropertySymbol, property.Name) {
			continue
		}
		value := context.ResolvePHPDocType(property.Type, templates)
		flags := semantic.SyntheticFlag
		if property.ReadOnly {
			flags |= semantic.ReadonlyFlag
		}
		name := string(container) + "::$" + property.Name
		b.addSymbol(scope, semantic.Symbol{
			ID:             semantic.NewSymbolID(semantic.PropertySymbol, name, b.document.Path, 0),
			Kind:           semantic.PropertySymbol,
			Name:           property.Name,
			FullyQualified: name,
			Container:      container,
			Path:           b.document.Path,
			Visibility:     semantic.Public,
			Flags:          flags,
			Type:           value,
			DocType:        value,
		})
	}
	for _, method := range documentation.Methods {
		if b.memberExists(container, semantic.MethodSymbol, method.Name) {
			continue
		}
		name := string(container) + "::" + method.Name
		returnType := context.ResolvePHPDocType(method.ReturnType, templates)
		symbol := semantic.Symbol{
			ID:             semantic.NewSymbolID(semantic.MethodSymbol, name, b.document.Path, 0),
			Kind:           semantic.MethodSymbol,
			Name:           method.Name,
			FullyQualified: name,
			Container:      container,
			Path:           b.document.Path,
			Visibility:     semantic.Public,
			Flags:          semantic.SyntheticFlag,
			ReturnType:     returnType,
			DocType:        returnType,
		}
		if method.Static {
			symbol.Flags |= semantic.StaticFlag
		}
		if len(method.Parameters) != 0 {
			symbol.Parameters = make(
				[]semantic.Parameter,
				len(method.Parameters),
			)
		}
		for index, parameter := range method.Parameters {
			value := context.ResolvePHPDocType(parameter.Type, templates)
			id := semantic.NewSymbolID(
				semantic.ParameterSymbol,
				name+":"+parameter.Name,
				b.document.Path,
				uint32(index),
			)
			flags := semantic.Flags(0)
			if parameter.Variadic {
				flags |= semantic.VariadicFlag
			}
			symbol.Parameters[index] = semantic.Parameter{
				ID:            id,
				Name:          parameter.Name,
				Type:          value,
				DocType:       value,
				AssistantTags: append([]string(nil), parameter.Tags...),
				Optional:      parameter.Optional,
				Flags:         flags,
			}
		}
		b.addSymbol(scope, symbol)
	}
}

func (b *documentBuilder) containerTemplateNames(
	container semantic.SymbolID,
) []string {
	var result []string
	seen := make(map[string]struct{})
	for container != "" {
		symbol, ok := b.symbol(container)
		if !ok {
			break
		}
		for _, template := range symbol.Templates() {
			if _, exists := seen[template.Name]; exists {
				continue
			}
			seen[template.Name] = struct{}{}
			result = append(result, template.Name)
		}
		container = symbol.Container
	}
	return result
}

func templateNames(values []phpdoc.Template) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Name)
	}
	return result
}

func (b *documentBuilder) memberExists(
	container semantic.SymbolID,
	kind semantic.SymbolKind,
	name string,
) bool {
	for _, symbol := range b.document.Symbols {
		if symbol.Container == container && symbol.Kind == kind &&
			strings.EqualFold(symbol.Name, name) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
