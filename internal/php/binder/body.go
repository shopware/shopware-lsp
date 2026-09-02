package binder

import (
	"fmt"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/phpdoc"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (b *documentBuilder) bindBody(
	body *phpsyntax.Node,
	functionScope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	if body == nil {
		return
	}
	b.bindBodyDeclarations(body, functionScope, context, owner)
	b.bindBodyReferences(body, functionScope, context, owner)
}

func (b *documentBuilder) bindBodyNode(
	node *phpsyntax.Node,
	functionScope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	if node == nil {
		return
	}
	b.bindBodyDeclaration(node, functionScope, context, owner)
	if !nestedFunction(node.Kind()) {
		b.bindBodyDeclarations(node, functionScope, context, owner)
	}
	b.bindBodyReference(node, functionScope, context, owner)
	if !nestedFunction(node.Kind()) {
		b.bindBodyReferences(node, functionScope, context, owner)
	}
}

func (b *documentBuilder) bindBodyDeclarations(
	parent *phpsyntax.Node,
	functionScope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	for index := 0; index < parent.ChildCount(); index++ {
		node, ok := parent.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		b.bindBodyDeclaration(node, functionScope, context, owner)
		if nestedFunction(node.Kind()) {
			continue
		}
		b.bindBodyDeclarations(node, functionScope, context, owner)
	}
}

func (b *documentBuilder) bindBodyDeclaration(
	node *phpsyntax.Node,
	functionScope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	switch node.Kind() {
	case phpsyntax.PhpClassDeclaration,
		phpsyntax.PhpInterfaceDeclaration,
		phpsyntax.PhpTraitDeclaration,
		phpsyntax.PhpEnumDeclaration:
		b.bindClass(node, functionScope, context)
	case phpsyntax.PhpAnonymousClass:
		b.bindAnonymousClass(node, functionScope, context)
	case phpsyntax.PhpAssignmentExpression:
		left := firstDirectNode(node)
		if left == nil {
			break
		}
		switch left.Kind() {
		case phpsyntax.PhpVariable:
			b.bindLocal(left, functionScope, owner, types.Unknown())
		case phpsyntax.PhpArrayAccess:
			base := firstDirectNode(left)
			if base != nil && base.Kind() == phpsyntax.PhpVariable {
				b.bindLocal(base, functionScope, owner, types.Unknown())
			}
		case phpsyntax.PhpArray:
			phpquery.Visit(
				left,
				func(variable *phpsyntax.Node) bool {
					b.bindLocal(
						variable,
						functionScope,
						owner,
						types.Unknown(),
					)
					return true
				},
				phpsyntax.PhpVariable,
			)
		}
	case phpsyntax.PhpForeachStatement:
		b.bindForeachLocals(node, functionScope, owner)
	case phpsyntax.PhpCatchClause:
		b.bindCatchLocal(node, functionScope, context, owner)
	case phpsyntax.PhpGlobalStatement, phpsyntax.PhpStaticStatement:
		for childIndex := 0; childIndex < node.ChildCount(); childIndex++ {
			variable, ok := node.Child(childIndex).(*phpsyntax.Node)
			if ok && variable.Kind() == phpsyntax.PhpVariable {
				b.bindLocal(
					variable,
					functionScope,
					owner,
					types.Unknown(),
				)
			}
		}
	case phpsyntax.PhpClosure, phpsyntax.PhpArrowFunction:
		b.bindClosure(node, functionScope, context, owner)
	case phpsyntax.PhpFunctionDeclaration:
		b.bindFunction(node, functionScope, context, "")
	case phpsyntax.PhpFunctionCall:
		b.bindClassAlias(node, functionScope, context)
	}
}

func (b *documentBuilder) bindClassAlias(
	call *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
) {
	name := strings.TrimPrefix(phpquery.CallName(call), "\\")
	if !strings.EqualFold(name, "class_alias") {
		return
	}
	targetNode := phpquery.ArgumentExpression(call, 0)
	aliasNode := phpquery.ArgumentExpression(call, 1)
	target, targetRange, targetLiteral, targetOK := staticClassAliasName(
		targetNode,
		context,
	)
	alias, aliasRange, _, aliasOK := staticClassAliasName(aliasNode, context)
	if !targetOK || !aliasOK || strings.EqualFold(target, alias) {
		return
	}
	aliasName := alias
	if separator := strings.LastIndexByte(aliasName, '\\'); separator >= 0 {
		aliasName = aliasName[separator+1:]
	}
	symbol := semantic.Symbol{
		ID: semantic.NewSymbolID(
			semantic.ClassSymbol,
			alias,
			b.document.Path,
			call.Range().Start,
		),
		Kind:           semantic.ClassSymbol,
		Name:           aliasName,
		FullyQualified: alias,
		Path:           b.document.Path,
		Range:          call.RangeTrimmedTrivia(),
		SelectionRange: aliasRange,
		Flags:          semantic.SyntheticFlag | semantic.ClassAliasFlag,
		Extends:        []string{target},
	}
	b.addSymbol(scope, symbol)
	if targetLiteral {
		reference := semantic.Reference{
			Name:  target,
			Kind:  semantic.ClassName,
			Range: targetRange,
			Scope: scope,
		}
		reference.SetQualifiedName(target)
		b.document.References = append(b.document.References, reference)
	}
}

func staticClassAliasName(
	node *phpsyntax.Node,
	context resolver.NameContext,
) (string, phpsyntax.TextRange, bool, bool) {
	if node == nil {
		return "", phpsyntax.TextRange{}, false, false
	}
	if name := phpquery.ClassConstantName(node); name != "" {
		textRange := node.RangeTrimmedTrivia()
		if receiver := phpquery.DirectChild(node, phpsyntax.PhpName); receiver != nil {
			textRange = receiver.RangeTrimmedTrivia()
		}
		return strings.Trim(context.ResolveClass(name), "\\"), textRange, false, true
	}
	if node.Kind() != phpsyntax.PhpString || strings.Contains(node.Text(), "$") {
		return "", phpsyntax.TextRange{}, false, false
	}
	value := phpquery.StringValue(node)
	value = strings.NewReplacer(`\\`, `\`, `\'`, `'`, `\"`, `"`).Replace(value)
	value = strings.Trim(strings.TrimSpace(value), "\\")
	if value == "" {
		return "", phpsyntax.TextRange{}, false, false
	}
	return value, phpquery.StringContentRange(node), true, true
}

func (b *documentBuilder) bindBodyReferences(
	parent *phpsyntax.Node,
	functionScope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	for index := 0; index < parent.ChildCount(); index++ {
		node, ok := parent.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		b.bindBodyReference(node, functionScope, context, owner)
		if nestedFunction(node.Kind()) {
			continue
		}
		b.bindBodyReferences(node, functionScope, context, owner)
	}
}

func (b *documentBuilder) bindBodyReference(
	node *phpsyntax.Node,
	functionScope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	if _, declared := b.declarations[semantic.NodeIdentity(node)]; declared {
		return
	}
	switch node.Kind() {
	case phpsyntax.PhpVariable:
		if staticPropertyName(node) {
			return
		}
		name := phpquery.VariableKey(node)
		b.document.References = append(b.document.References, semantic.Reference{
			Name:  name,
			Kind:  semantic.VariableName,
			Range: node.RangeTrimmedTrivia(),
			Scope: functionScope,
		})
	case phpsyntax.PhpObjectCreation:
		nameNode := phpquery.DirectChild(node, phpsyntax.PhpName)
		if nameNode != nil {
			name := phpquery.NameValue(nameNode)
			if strings.HasPrefix(name, "$") {
				b.document.References = append(
					b.document.References,
					semantic.Reference{
						Name:  name,
						Kind:  semantic.VariableName,
						Range: nameNode.RangeTrimmedTrivia(),
						Scope: functionScope,
					},
				)
			} else {
				b.addResolvedClassReference(
					nameNode,
					functionScope,
					context,
					owner,
				)
			}
		}
	case phpsyntax.PhpFunctionCall:
		nameNode := firstDirect(node, phpsyntax.PhpName)
		if nameNode != nil {
			b.addReference(
				nameNode,
				semantic.FunctionName,
				functionScope,
				context.ResolveFunction(phpquery.NameValue(nameNode)),
			)
		}
	case phpsyntax.PhpScopedCall, phpsyntax.PhpScopedAccess:
		receiver := firstDirectNode(node)
		if receiver != nil && receiver.Kind() == phpsyntax.PhpName {
			b.addResolvedClassReference(receiver, functionScope, context, owner)
		}
	case phpsyntax.PhpMemberAccess:
		if hasToken(node, phpsyntax.TkScopeResolution, "") {
			receiver := firstDirectNode(node)
			if receiver != nil && receiver.Kind() == phpsyntax.PhpName {
				b.addResolvedClassReference(receiver, functionScope, context, owner)
			}
		}
	case phpsyntax.PhpBinaryExpression:
		b.bindInstanceofReference(node, functionScope, context, owner)
	}
}

func staticPropertyName(node *phpsyntax.Node) bool {
	if node == nil || node.Parent() == nil {
		return false
	}
	nameNode := node
	parent := node.Parent()
	if parent.Kind() == phpsyntax.PhpName && parent.Parent() != nil {
		nameNode = parent
		parent = parent.Parent()
	}
	if parent.Kind() != phpsyntax.PhpScopedAccess &&
		(parent.Kind() != phpsyntax.PhpMemberAccess ||
			!hasToken(parent, phpsyntax.TkScopeResolution, "")) {
		return false
	}
	var last *phpsyntax.Node
	count := 0
	for index := 0; index < parent.ChildCount(); index++ {
		child, ok := parent.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		last = child
		count++
	}
	return count >= 2 && last == nameNode
}

func nestedFunction(kind phpsyntax.Kind) bool {
	switch kind {
	case phpsyntax.PhpClosure, phpsyntax.PhpArrowFunction,
		phpsyntax.PhpFunctionDeclaration,
		phpsyntax.PhpClassDeclaration,
		phpsyntax.PhpAnonymousClass,
		phpsyntax.PhpInterfaceDeclaration,
		phpsyntax.PhpTraitDeclaration,
		phpsyntax.PhpEnumDeclaration:
		return true
	default:
		return false
	}
}

func (b *documentBuilder) bindAnonymousClass(
	node *phpsyntax.Node,
	parentScope semantic.ScopeID,
	context resolver.NameContext,
) {
	if node == nil {
		return
	}
	fullyQualified := semantic.AnonymousClassName(
		b.document.Path,
		node.RangeTrimmedTrivia().Start,
	)
	symbol := semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.ClassSymbol, fullyQualified, b.document.Path, node.Range().Start),
		Kind:           semantic.ClassSymbol,
		Name:           "{anonymous}",
		FullyQualified: fullyQualified,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: node.RangeTrimmedTrivia(),
		Flags:          declarationFlags(node) | semantic.SyntheticFlag,
		Extends:        resolveNames(context, phpquery.ClassExtends(node)),
		Implements:     resolveNames(context, phpquery.ClassImplements(node)),
	}
	body := phpquery.DirectChild(node, phpsyntax.PhpClassBody)
	if body != nil {
		symbol.BodyRange = body.RangeTrimmedTrivia()
	}
	b.addSymbol(parentScope, symbol)
	b.bindClassClauses(node, parentScope, context)
	b.bindAttributeReferences(node, parentScope, context)

	// Constructor arguments are evaluated in the enclosing scope, not the
	// anonymous class scope.
	if arguments := phpquery.DirectChild(node, phpsyntax.PhpArgumentList); arguments != nil {
		b.bindBody(arguments, parentScope, context, b.document.Scopes[parentScope].Owner)
	}
	if body == nil {
		return
	}
	classScope := b.newScope(
		semantic.ClassScope,
		parentScope,
		body.Range(),
		symbol.ID,
		context,
	)
	symbolIndex := len(b.document.Symbols) - 1
	for index := 0; index < body.ChildCount(); index++ {
		member, ok := body.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		switch member.Kind() {
		case phpsyntax.PhpMethodDeclaration:
			b.bindFunction(member, classScope, context, symbol.ID)
		case phpsyntax.PhpPropertyDeclaration:
			b.bindProperties(member, classScope, context, symbol.ID)
		case phpsyntax.PhpClassConstDeclaration:
			b.bindConstants(member, classScope, context, symbol.ID, true)
		case phpsyntax.PhpTraitUseDeclaration:
			var usedTraits []string
			for childIndex := 0; childIndex < member.ChildCount(); childIndex++ {
				trait, ok := member.Child(childIndex).(*phpsyntax.Node)
				if !ok || trait.Kind() != phpsyntax.PhpName {
					continue
				}
				resolvedTrait := context.ResolveClass(phpquery.NameValue(trait))
				usedTraits = append(usedTraits, resolvedTrait)
				b.document.Symbols[symbolIndex].Traits = append(
					b.document.Symbols[symbolIndex].Traits,
					resolvedTrait,
				)
				b.addSingleReference(
					trait,
					semantic.ClassName,
					classScope,
					context.ResolveClass(phpquery.NameValue(trait)),
				)
			}
			b.document.Symbols[symbolIndex].TraitAliases = append(
				b.document.Symbols[symbolIndex].TraitAliases,
				bindTraitAliases(member, context, usedTraits)...,
			)
		}
	}
}

func (b *documentBuilder) bindLocal(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	owner semantic.SymbolID,
	value types.Type,
) {
	if node == nil || node.Kind() != phpsyntax.PhpVariable {
		return
	}
	name := phpquery.VariableKey(node)
	if name == "" || b.scopeHas(scope, name) {
		return
	}
	symbol := semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.LocalSymbol, "", b.document.Path, node.Range().Start),
		Kind:           semantic.LocalSymbol,
		Name:           name,
		FullyQualified: string(owner) + ":" + name,
		Container:      owner,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: node.RangeTrimmedTrivia(),
		Type:           value,
	}
	b.addSymbol(scope, symbol)
	b.markDeclaration(node)
	if !value.IsUnknown() {
		b.document.SetTypeFact(semantic.NodeIdentity(node), semantic.TypeFact{
			Type:       value,
			Confidence: semantic.InferredConfidence,
			Source:     semantic.AssignmentSource,
			Origin:     node.RangeTrimmedTrivia(),
		})
	}
}

func (b *documentBuilder) bindForeachLocals(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	owner semantic.SymbolID,
) {
	inTargets := false
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *phpsyntax.Token:
			if strings.EqualFold(child.Text(), "as") {
				inTargets = true
			} else if inTargets && child.Kind() == phpsyntax.TkCloseParen {
				return
			}
		case *phpsyntax.Node:
			if inTargets {
				b.bindForeachTargetLocals(child, scope, owner)
			}
		}
	}
}

func (b *documentBuilder) bindForeachTargetLocals(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	owner semantic.SymbolID,
) {
	if node.Kind() == phpsyntax.PhpVariable {
		b.bindLocal(node, scope, owner, types.Unknown())
		return
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok {
			b.bindForeachTargetLocals(child, scope, owner)
		}
	}
}

func (b *documentBuilder) bindCatchLocal(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	b.bindDirectTypeReferences(node, scope, context, owner)
	var caught types.Type
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok && isTypeNode(child.Kind()) {
			caught = b.bindNativeType(child.Text(), context)
			break
		}
	}
	for index := 0; index < node.ChildCount(); index++ {
		variable, ok := node.Child(index).(*phpsyntax.Node)
		if ok && variable.Kind() == phpsyntax.PhpVariable {
			b.bindLocal(variable, scope, owner, caught)
			break
		}
	}
}

func (b *documentBuilder) bindClosure(
	node *phpsyntax.Node,
	parentScope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	documentation := phpdoc.Parse(leadingDocComment(node))
	nativeReturn := b.bindNativeType(phpquery.MethodReturnType(node), context)
	inheritedTemplates := b.containerTemplateNames(owner)
	docTemplates := append(
		append([]string(nil), inheritedTemplates...),
		templateNames(documentation.Templates)...,
	)
	docReturn := context.ResolvePHPDocType(documentation.Return, docTemplates)
	parameters := phpquery.IterateParameters(node)
	id := semantic.NewSymbolID(
		semantic.ClosureSymbol,
		"",
		b.document.Path,
		node.Range().Start,
	)
	symbol := semantic.Symbol{
		ID:             id,
		Kind:           semantic.ClosureSymbol,
		Name:           "{closure}",
		FullyQualified: fmt.Sprintf("%s:{closure}@%d", owner, node.Range().Start),
		Container:      owner,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: phpsyntax.TextRange{
			Start: node.RangeTrimmedTrivia().Start,
			End:   node.RangeTrimmedTrivia().Start,
		},
		Flags:      declarationFlags(node),
		NativeType: nativeReturn,
		DocType:    docReturn,
		ReturnType: effectiveType(nativeReturn, docReturn),
		DocSummary: documentation.Summary,
		Assertions: bindAssertions(documentation.Assertions, context, docTemplates),
	}
	if parameterCount := parameters.Len(); parameterCount != 0 {
		symbol.Parameters = make(
			[]semantic.Parameter,
			parameterCount,
		)
	}
	symbol.Templates = bindTemplates(
		documentation.Templates,
		context,
		inheritedTemplates...,
	)
	b.addSymbol(parentScope, symbol)

	scopeParent := parentScope
	if node.Kind() == phpsyntax.PhpClosure {
		scopeParent = b.nonFunctionParent(parentScope)
	}
	closureScope := b.newScope(
		semantic.ClosureScope,
		scopeParent,
		node.Range(),
		id,
		context,
	)
	symbolIndex := len(b.document.Symbols) - 1
	parameterIndex := 0
	for parameters.Next() {
		parameter := b.bindParameter(
			parameters.Node(),
			closureScope,
			context,
			id,
			documentation.Params,
			documentation.ParamTags,
			docTemplates,
		)
		b.document.Symbols[symbolIndex].Parameters[parameterIndex] = parameter
		parameterIndex++
	}
	b.bindDirectTypeReferences(node, closureScope, context, owner)

	if node.Kind() == phpsyntax.PhpClosure {
		for index := 0; index < node.ChildCount(); index++ {
			child, ok := node.Child(index).(*phpsyntax.Node)
			if !ok || child.Kind() != phpsyntax.PhpVariable {
				continue
			}
			name := phpquery.VariableKey(child)
			b.document.References = append(b.document.References, semantic.Reference{
				Name:  name,
				Kind:  semantic.VariableName,
				Range: child.RangeTrimmedTrivia(),
				Scope: parentScope,
			})
			value := b.localType(parentScope, name)
			b.bindClosureCapture(child, closureScope, id, value)
		}
	}

	if block := phpquery.DirectChild(node, phpsyntax.PhpBlock); block != nil {
		b.bindBody(block, closureScope, context, id)
		return
	}
	for index := node.ChildCount() - 1; index >= 0; index-- {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == phpsyntax.PhpParameterList || isTypeNode(child.Kind()) {
			continue
		}
		b.bindBody(child, closureScope, context, id)
		break
	}
}

func (b *documentBuilder) bindClosureCapture(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	owner semantic.SymbolID,
	value types.Type,
) {
	name := phpquery.VariableKey(node)
	symbol := semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.LocalSymbol, "", b.document.Path, node.Range().Start),
		Kind:           semantic.LocalSymbol,
		Name:           name,
		FullyQualified: string(owner) + ":" + name,
		Container:      owner,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: node.RangeTrimmedTrivia(),
		Type:           value,
	}
	b.addSymbol(scope, symbol)
	b.markDeclaration(node)
}

func (b *documentBuilder) bindInstanceofReference(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	if !hasDirectText(node, "instanceof") {
		return
	}
	for index := node.ChildCount() - 1; index >= 0; index-- {
		nameNode, ok := node.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if nameNode.Kind() == phpsyntax.PhpName {
			b.addResolvedClassReference(nameNode, scope, context, owner)
		}
		return
	}
}

func hasDirectText(node *phpsyntax.Node, text string) bool {
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if ok && strings.EqualFold(token.Text(), text) {
			return true
		}
	}
	return false
}

func (b *documentBuilder) nonFunctionParent(scope semantic.ScopeID) semantic.ScopeID {
	for int(scope) < len(b.document.Scopes) {
		current := b.document.Scopes[scope]
		if current.Kind != semantic.FunctionScope && current.Kind != semantic.ClosureScope {
			return scope
		}
		if current.ID == current.Parent {
			return scope
		}
		scope = current.Parent
	}
	return 0
}

func (b *documentBuilder) localType(scope semantic.ScopeID, name string) types.Type {
	for int(scope) < len(b.document.Scopes) {
		current := b.document.Scopes[scope]
		for id := range current.SymbolIDs(b.document.Symbols, name) {
			if symbol, ok := b.symbol(id); ok {
				return symbol.Type
			}
		}
		if current.ID == current.Parent {
			break
		}
		scope = current.Parent
	}
	return types.Unknown()
}

func (b *documentBuilder) addReference(
	node *phpsyntax.Node,
	kind semantic.NameKind,
	scope semantic.ScopeID,
	qualified []string,
) {
	if node == nil {
		return
	}
	reference := semantic.Reference{
		Name:  compactName(node.Text()),
		Kind:  kind,
		Range: node.RangeTrimmedTrivia(),
		Scope: scope,
	}
	reference.SetQualifiedNames(qualified)
	b.document.References = append(b.document.References, reference)
}

func (b *documentBuilder) addSingleReference(
	node *phpsyntax.Node,
	kind semantic.NameKind,
	scope semantic.ScopeID,
	qualified string,
) {
	if node == nil {
		return
	}
	reference := semantic.Reference{
		Name:  compactName(node.Text()),
		Kind:  kind,
		Range: node.RangeTrimmedTrivia(),
		Scope: scope,
	}
	reference.SetQualifiedName(qualified)
	b.document.References = append(b.document.References, reference)
}

func (b *documentBuilder) scopeHas(scope semantic.ScopeID, name string) bool {
	for int(scope) < len(b.document.Scopes) {
		current := b.document.Scopes[scope]
		if current.HasSymbol(b.document.Symbols, name) {
			return true
		}
		if current.ID == current.Parent {
			return false
		}
		scope = current.Parent
	}
	return false
}

func firstDirectNode(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		if child, ok := element.(*phpsyntax.Node); ok {
			return child
		}
	}
	return nil
}
