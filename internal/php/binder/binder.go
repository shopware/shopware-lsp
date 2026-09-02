// Package binder converts the lossless PHP CST into immutable semantic
// documents. Cross-file IDs are resolved by the separate linker pass.
package binder

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phpliteral "github.com/shopware/shopware-lsp/internal/php/literal"
	"github.com/shopware/shopware-lsp/internal/php/phpdoc"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type Binder struct{}

func New() *Binder {
	return &Binder{}
}

func (b *Binder) Bind(path string, version int, root *phpsyntax.Node) *semantic.Document {
	document := &semantic.Document{
		Path:    path,
		Version: version,
	}
	if root == nil {
		return document
	}
	symbolCapacity, referenceCapacity, literalCapacity, scopeCapacity :=
		estimatedDocumentCapacities(root)
	document.Symbols = make([]semantic.Symbol, 0, symbolCapacity)
	document.Scopes = make([]semantic.Scope, 0, scopeCapacity)
	document.References = make(
		[]semantic.Reference,
		0,
		referenceCapacity,
	)
	document.ReserveTypeFacts(
		estimatedNonLiteralTypeFactCapacity(
			root.Range().Len(),
			literalCapacity,
		),
	)
	builder := documentBuilder{
		document:     document,
		root:         root,
		declarations: make(map[semantic.NodeID]struct{}),
	}
	context := resolver.NewNameContext("")
	fileScope := builder.newScope(
		semantic.FileScope,
		0,
		root.Range(),
		"",
		context,
	)
	builder.bindTopLevel(root, fileScope, context)
	return document
}

func estimatedSymbolCapacity(sourceBytes uint32) int {
	if sourceBytes == 0 {
		return 0
	}
	const (
		// Reserve for declarations plus parameters and function-local symbols.
		// The hard cap protects large generated source files.
		estimatedBytesPerSymbol = 1536
		maxEstimatedSymbols     = 512
	)
	capacity := int(sourceBytes)/estimatedBytesPerSymbol + 1
	if capacity > maxEstimatedSymbols {
		return maxEstimatedSymbols
	}
	return capacity
}

func estimatedNonLiteralTypeFactCapacity(
	sourceBytes uint32,
	literalCapacity int,
) int {
	capacity := estimatedTypeFactCapacity(sourceBytes)
	if capacity == 0 {
		return 0
	}
	generalCapacity := capacity - literalCapacity
	if minimum := capacity/3 + 1; generalCapacity < minimum {
		return minimum
	}
	return generalCapacity
}

func estimatedDocumentSymbolCapacity(root *phpsyntax.Node) int {
	symbols, _, _, _ := estimatedDocumentCapacities(root)
	return symbols
}

func estimatedDocumentCapacities(
	root *phpsyntax.Node,
) (int, int, int, int) {
	if root == nil {
		return 0, 0, 0, 0
	}
	capacity := estimatedSymbolCapacity(root.Range().Len())
	structural, referenceNodes, memberNodes, literalNodes, scopeNodes :=
		structuralDocumentCounts(root)
	if structural > 0 {
		// PHPDoc-synthesized members are not represented by a one-to-one
		// declaration node. Local declaration candidates are counted below,
		// so only leave one slot of headroom.
		structural++
	}
	const maxStructuralSymbols = 4096
	if structural > maxStructuralSymbols {
		structural = maxStructuralSymbols
	}
	if structural > capacity {
		capacity = structural
	}
	return capacity,
		estimatedDocumentReferenceCapacity(referenceNodes, memberNodes),
		literalNodes,
		estimatedScopeCapacity(scopeNodes)
}

func structuralSymbolCount(node *phpsyntax.Node) int {
	symbols, _, _, _, _ := structuralDocumentCounts(node)
	return symbols
}

func estimatedScopeCapacity(scopeNodes int) int {
	const maxEstimatedScopes = 4096
	return min(scopeNodes+1, maxEstimatedScopes)
}

func structuralDocumentCounts(
	node *phpsyntax.Node,
) (int, int, int, int, int) {
	if node == nil {
		return 0, 0, 0, 0, 0
	}
	symbols := 0
	referenceNodes := 0
	memberNodes := 0
	literalNodes := 0
	scopeNodes := 0
	if node.Kind() == phpsyntax.PhpName ||
		node.Kind() == phpsyntax.PhpVariable {
		referenceNodes++
	}
	switch node.Kind() {
	case phpsyntax.PhpString,
		phpsyntax.PhpNumber,
		phpsyntax.PhpBoolean,
		phpsyntax.PhpNull:
		literalNodes++
	case phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall,
		phpsyntax.PhpMemberAccess,
		phpsyntax.PhpScopedAccess:
		// The binder deliberately leaves these targets for the post-inference
		// member linker. Reserve their final slots during this existing tree
		// census so LinkMembersOwned does not repeatedly grow the slice.
		memberNodes++
	case phpsyntax.PhpClassDeclaration,
		phpsyntax.PhpAnonymousClass,
		phpsyntax.PhpInterfaceDeclaration,
		phpsyntax.PhpTraitDeclaration,
		phpsyntax.PhpEnumDeclaration,
		phpsyntax.PhpMethodDeclaration,
		phpsyntax.PhpFunctionDeclaration,
		phpsyntax.PhpClosure,
		phpsyntax.PhpArrowFunction,
		phpsyntax.PhpEnumCaseDeclaration:
		symbols++
		switch node.Kind() {
		case phpsyntax.PhpClassDeclaration,
			phpsyntax.PhpAnonymousClass,
			phpsyntax.PhpInterfaceDeclaration,
			phpsyntax.PhpTraitDeclaration,
			phpsyntax.PhpEnumDeclaration,
			phpsyntax.PhpMethodDeclaration,
			phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpClosure,
			phpsyntax.PhpArrowFunction:
			scopeNodes++
		}
	case phpsyntax.PhpNamespace:
		scopeNodes++
	case phpsyntax.PhpParameter:
		symbols++
		if declarationFlags(node).Has(semantic.PromotedFlag) {
			symbols++
		}
	case phpsyntax.PhpPropertyDeclaration:
		for index := 0; index < node.ChildCount(); index++ {
			child, ok := node.Child(index).(*phpsyntax.Node)
			if ok && child.Kind() == phpsyntax.PhpVariable {
				symbols++
			}
		}
	case phpsyntax.PhpConstDeclaration,
		phpsyntax.PhpClassConstDeclaration:
		symbols += directConstantCount(node)
	case phpsyntax.PhpAssignmentExpression:
		left := firstDirectNode(node)
		if left != nil {
			switch left.Kind() {
			case phpsyntax.PhpVariable:
				symbols++
			case phpsyntax.PhpArray:
				symbols += descendantKindCount(
					left,
					phpsyntax.PhpVariable,
				)
			}
		}
	case phpsyntax.PhpForeachStatement:
		symbols += foreachLocalCount(node)
	case phpsyntax.PhpCatchClause:
		symbols++
	case phpsyntax.PhpGlobalStatement, phpsyntax.PhpStaticStatement:
		symbols += descendantKindCount(node, phpsyntax.PhpVariable)
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok {
			childSymbols, childReferences, childMembers, childLiterals,
				childScopes :=
				structuralDocumentCounts(child)
			symbols += childSymbols
			referenceNodes += childReferences
			memberNodes += childMembers
			literalNodes += childLiterals
			scopeNodes += childScopes
		}
	}
	return symbols, referenceNodes, memberNodes, literalNodes, scopeNodes
}

func descendantKindCount(node *phpsyntax.Node, kind phpsyntax.Kind) int {
	if node == nil {
		return 0
	}
	count := 0
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == kind {
			count++
		}
		count += descendantKindCount(child, kind)
	}
	return count
}

func directConstantCount(node *phpsyntax.Node) int {
	count := 0
	expectName := false
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *phpsyntax.Token:
			if strings.EqualFold(child.Text(), "const") ||
				child.Kind() == phpsyntax.TkComma {
				expectName = true
			} else if child.Kind() == phpsyntax.TkEquals {
				expectName = false
			}
		case *phpsyntax.Node:
			if expectName && child.Kind() == phpsyntax.PhpName {
				count++
				expectName = false
			}
		}
	}
	return count
}

func foreachLocalCount(node *phpsyntax.Node) int {
	count := 0
	inTargets := false
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *phpsyntax.Token:
			if strings.EqualFold(child.Text(), "as") {
				inTargets = true
			} else if inTargets && child.Kind() == phpsyntax.TkCloseParen {
				return count
			}
		case *phpsyntax.Node:
			if inTargets {
				count += kindCount(child, phpsyntax.PhpVariable)
			}
		}
	}
	return count
}

func kindCount(node *phpsyntax.Node, kind phpsyntax.Kind) int {
	if node == nil {
		return 0
	}
	count := 0
	if node.Kind() == kind {
		count++
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok {
			count += kindCount(child, kind)
		}
	}
	return count
}

func estimatedReferenceCapacity(referenceNodes int) int {
	// Declaration names and member targets are not references themselves. A
	// three-quarter density keeps the estimate correlated with syntax shape
	// without reserving space for every name-like node.
	capacity := (referenceNodes*3 + 3) / 4
	if capacity > maxEstimatedReferences {
		return maxEstimatedReferences
	}
	return capacity
}

func estimatedLinkedReferenceCapacity(referenceNodes, memberNodes int) int {
	capacity := estimatedReferenceCapacity(referenceNodes) + memberNodes
	if capacity > maxEstimatedReferences {
		return maxEstimatedReferences
	}
	return capacity
}

func estimatedDocumentReferenceCapacity(referenceNodes, memberNodes int) int {
	const (
		numerator   = 9
		denominator = 10
	)
	capacity := estimatedLinkedReferenceCapacity(referenceNodes, memberNodes)
	if capacity == maxEstimatedReferences {
		return capacity
	}
	return (capacity*numerator + denominator - 1) / denominator
}

const maxEstimatedReferences = 4096

func estimatedTypeFactCapacity(sourceBytes uint32) int {
	return estimatedTypeFactCapacityForBytes(sourceBytes, 40)
}

func estimatedTypeFactCapacityForBytes(
	sourceBytes uint32,
	bytesPerFact int,
) int {
	const (
		maxEstimatedTypeFacts = 4096
	)
	if bytesPerFact <= 0 {
		return 0
	}
	capacity := int(sourceBytes) / bytesPerFact
	if capacity > maxEstimatedTypeFacts {
		return maxEstimatedTypeFacts
	}
	return capacity
}

type documentBuilder struct {
	document     *semantic.Document
	root         *phpsyntax.Node
	declarations map[semantic.NodeID]struct{}
}

func (b *documentBuilder) bindTopLevel(
	parent *phpsyntax.Node,
	initialScope semantic.ScopeID,
	initialContext resolver.NameContext,
) {
	if parent == nil {
		return
	}
	scope := initialScope
	context := initialContext
	var openNamespaceScope *semantic.ScopeID

	for index := 0; index < parent.ChildCount(); index++ {
		node, ok := parent.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if node.Kind() == phpsyntax.PhpNamespace {
			if openNamespaceScope != nil {
				b.document.Scopes[*openNamespaceScope].Range.End = node.Range().Start
			}
			name := phpquery.NameValue(phpquery.DirectChild(node, phpsyntax.PhpName))
			namespaceContext := resolver.NewNameContext(name)
			block := phpquery.DirectChild(node, phpsyntax.PhpBlock)
			if block != nil {
				namespaceScope := b.newScope(
					semantic.NamespaceScope,
					initialScope,
					block.Range(),
					"",
					namespaceContext,
				)
				b.bindTopLevel(block, namespaceScope, namespaceContext)
				scope = initialScope
				context = initialContext
				openNamespaceScope = nil
				continue
			}
			namespaceRange := phpsyntax.TextRange{
				Start: node.Range().End,
				End:   b.root.Range().End,
			}
			namespaceScope := b.newScope(
				semantic.NamespaceScope,
				initialScope,
				namespaceRange,
				"",
				namespaceContext,
			)
			scope = namespaceScope
			context = namespaceContext
			openNamespaceScope = &namespaceScope
			if b.document.Namespace == "" {
				b.document.Namespace = name
			}
			continue
		}

		switch node.Kind() {
		case phpsyntax.PhpUseDeclaration:
			importsBefore := context.Imports
			context.AddUseDeclaration(node.Text())
			if (importsBefore.Classes == nil &&
				context.Imports.Classes != nil) ||
				(importsBefore.Functions == nil &&
					context.Imports.Functions != nil) ||
				(importsBefore.Constants == nil &&
					context.Imports.Constants != nil) {
				b.publishScopeImports(scope, context.Imports)
			}
		case phpsyntax.PhpClassDeclaration,
			phpsyntax.PhpInterfaceDeclaration,
			phpsyntax.PhpTraitDeclaration,
			phpsyntax.PhpEnumDeclaration:
			b.bindClass(node, scope, context)
		case phpsyntax.PhpFunctionDeclaration:
			b.bindFunction(node, scope, context, "")
		case phpsyntax.PhpConstDeclaration:
			b.bindConstants(node, scope, context, "", false)
		default:
			b.bindBodyNode(node, scope, context, "")
		}
	}
}

func (b *documentBuilder) publishScopeImports(
	root semantic.ScopeID,
	imports semantic.ImportTable,
) {
	for index := range b.document.Scopes {
		if b.scopeSharesImportContext(semantic.ScopeID(index), root) {
			b.document.Scopes[index].Imports = imports
		}
	}
}

func (b *documentBuilder) scopeSharesImportContext(
	scope,
	root semantic.ScopeID,
) bool {
	for scope != root {
		if int(scope) >= len(b.document.Scopes) {
			return false
		}
		current := b.document.Scopes[scope]
		if current.Kind == semantic.NamespaceScope {
			return false
		}
		if current.Parent == scope {
			return false
		}
		scope = current.Parent
	}
	return int(root) < len(b.document.Scopes)
}

func (b *documentBuilder) bindClass(
	node *phpsyntax.Node,
	parentScope semantic.ScopeID,
	context resolver.NameContext,
) {
	nameNode := phpquery.DirectChild(node, phpsyntax.PhpName)
	name := phpquery.NameValue(nameNode)
	if name == "" {
		return
	}
	fullyQualified := qualify(context.Namespace, name)
	documentation := phpdoc.Parse(leadingDocComment(node))
	classTemplates := templateNames(documentation.Templates)
	context = b.withPHPDocAliases(
		context,
		fullyQualified,
		documentation,
		classTemplates,
	)
	kind := semantic.ClassSymbol
	switch node.Kind() {
	case phpsyntax.PhpInterfaceDeclaration:
		kind = semantic.InterfaceSymbol
	case phpsyntax.PhpTraitDeclaration:
		kind = semantic.TraitSymbol
	case phpsyntax.PhpEnumDeclaration:
		kind = semantic.EnumSymbol
	}
	symbol := semantic.Symbol{
		ID:             semantic.NewSymbolID(kind, fullyQualified, b.document.Path, node.Range().Start),
		Kind:           kind,
		Name:           name,
		FullyQualified: fullyQualified,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: selectionRange(nameNode, node),
		Flags:          declarationFlags(node),
		Extends:        resolveNames(context, phpquery.ClassExtends(node)),
		Implements:     resolveNames(context, phpquery.ClassImplements(node)),
		Attributes:     bindAttributes(node, context),
		DocSummary:     documentation.Summary,
	}
	applyDocumentFlags(&symbol, documentation)
	applyAttributeTypeSemantics(&symbol, context)
	applyAttributeFlags(&symbol)
	symbol.Templates = bindTemplates(documentation.Templates, context)
	symbol.ExtendsTypes = resolveDocTypes(
		documentation.Extends,
		context,
		classTemplates,
	)
	symbol.ImplementsTypes = resolveDocTypes(
		documentation.Implements,
		context,
		classTemplates,
	)
	symbol.TraitTypes = resolveDocTypes(
		documentation.Uses,
		context,
		classTemplates,
	)
	for _, extended := range symbol.ExtendsTypes {
		symbol.Extends = appendUnique(symbol.Extends, extended.Name())
	}
	for _, implemented := range symbol.ImplementsTypes {
		symbol.Implements = appendUnique(symbol.Implements, implemented.Name())
	}
	for _, used := range symbol.TraitTypes {
		symbol.Traits = appendUnique(symbol.Traits, used.Name())
	}
	if body := phpquery.DirectChild(node, phpsyntax.PhpClassBody); body != nil {
		symbol.BodyRange = body.RangeTrimmedTrivia()
	}
	if kind == semantic.InterfaceSymbol {
		symbol.Implements = append(symbol.Implements, symbol.Extends...)
		symbol.Extends = nil
	}
	enumBacking := types.Unknown()
	if kind == semantic.EnumSymbol {
		symbol.Implements = appendUnique(symbol.Implements, "UnitEnum")
		enumBacking = b.enumBackingType(node, context)
		if !enumBacking.IsUnknown() {
			symbol.Implements = appendUnique(symbol.Implements, "BackedEnum")
		}
	}
	b.addSymbol(parentScope, symbol)
	b.markDeclaration(nameNode)
	b.bindClassClauses(node, parentScope, context)
	b.bindAttributeReferences(node, parentScope, context)

	body := phpquery.DirectChild(node, phpsyntax.PhpClassBody)
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
	b.bindTypeAliases(
		documentation,
		classScope,
		context,
		symbol.ID,
		classTemplates,
	)
	if kind == semantic.EnumSymbol {
		b.bindEnumRuntimeMembers(
			classScope,
			symbol,
			enumBacking,
		)
	}
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
		case phpsyntax.PhpEnumCaseDeclaration:
			b.bindEnumCase(member, classScope, context, symbol.ID)
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
	b.bindSyntheticMembers(documentation, classScope, context, symbol.ID)
}

func (b *documentBuilder) bindFunction(
	node *phpsyntax.Node,
	parentScope semantic.ScopeID,
	context resolver.NameContext,
	container semantic.SymbolID,
) {
	nameNode := phpquery.DirectChild(node, phpsyntax.PhpName)
	name := phpquery.NameValue(nameNode)
	if name == "" {
		return
	}
	kind := semantic.FunctionSymbol
	fullyQualified := qualify(context.Namespace, name)
	if container != "" {
		kind = semantic.MethodSymbol
		if class, ok := b.symbol(container); ok {
			fullyQualified = class.FullyQualified + "::" + name
		}
	}
	nativeReturn := b.bindNativeType(phpquery.MethodReturnType(node), context)
	if strings.EqualFold(name, "__construct") && nativeReturn.IsUnknown() {
		nativeReturn = types.Void()
	}
	documentation := phpdoc.Parse(leadingDocComment(node))
	inheritedTemplates := b.containerTemplateNames(container)
	docTemplates := append(
		append([]string(nil), inheritedTemplates...),
		templateNames(documentation.Templates)...,
	)
	docReturn := context.ResolvePHPDocType(documentation.Return, docTemplates)
	parameters := phpquery.IterateParameters(node)
	plannedParameters := b.bindPlannedParameters(node, context)
	symbol := semantic.Symbol{
		ID:             semantic.NewSymbolID(kind, fullyQualified, b.document.Path, node.Range().Start),
		Kind:           kind,
		Name:           name,
		FullyQualified: fullyQualified,
		Container:      container,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: selectionRange(nameNode, node),
		Visibility:     declarationVisibility(node),
		Flags:          declarationFlags(node),
		NativeType:     nativeReturn,
		DocType:        docReturn,
		ReturnType:     effectiveType(nativeReturn, docReturn),
		Attributes:     bindAttributes(node, context),
		DocSummary:     documentation.Summary,
		Throws:         resolveDocTypes(documentation.Throws, context, docTemplates),
		Assertions:     bindAssertions(documentation.Assertions, context, docTemplates),
	}
	if parameterCount := parameters.Len() + len(plannedParameters); parameterCount != 0 {
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
	applyDocumentFlags(&symbol, documentation)
	applyAttributeTypeSemantics(&symbol, context)
	applyAttributeFlags(&symbol)
	body := phpquery.DirectChild(node, phpsyntax.PhpBlock)
	if body != nil {
		symbol.BodyRange = body.RangeTrimmedTrivia()
		symbol.LiteralReturns, symbol.ConstantReturns =
			returnMetadata(body, context)
	}
	b.addSymbol(parentScope, symbol)
	b.markDeclaration(nameNode)

	scopeRange := node.Range()
	if body != nil {
		scopeRange = body.Range()
	}
	functionScope := b.newScope(
		semantic.FunctionScope,
		parentScope,
		scopeRange,
		symbol.ID,
		context,
	)
	b.bindDirectTypeReferences(node, functionScope, context, container)
	b.bindAttributeReferences(node, functionScope, context)
	symbolIndex := len(b.document.Symbols) - 1
	parameterIndex := 0
	for parameters.Next() {
		parameterNode := parameters.Node()
		parameter := b.bindParameter(
			parameterNode,
			functionScope,
			context,
			symbol.ID,
			documentation.Params,
			documentation.ParamTags,
			docTemplates,
		)
		b.document.Symbols[symbolIndex].Parameters[parameterIndex] = parameter
		parameterIndex++
		if container != "" && strings.EqualFold(name, "__construct") &&
			parameter.Flags.Has(semantic.PromotedFlag) {
			b.bindPromotedProperty(
				parameterNode,
				parameter,
				parentScope,
				container,
				context,
			)
		}
	}
	copy(
		b.document.Symbols[symbolIndex].Parameters[parameterIndex:],
		plannedParameters,
	)
	if body != nil {
		b.bindBody(body, functionScope, context, symbol.ID)
	}
}

func (b *documentBuilder) bindPromotedProperty(
	node *phpsyntax.Node,
	parameter semantic.Parameter,
	classScope semantic.ScopeID,
	container semantic.SymbolID,
	context resolver.NameContext,
) {
	name := strings.TrimPrefix(parameter.Name, "$")
	if b.memberExists(container, semantic.PropertySymbol, name) {
		return
	}
	variable := phpquery.DirectChild(node, phpsyntax.PhpVariable)
	fullyQualified := string(container) + "::$" + name
	writeVisibility, hasWriteVisibility := declarationWriteVisibility(node)
	symbol := semantic.Symbol{
		ID:                 semantic.NewSymbolID(semantic.PropertySymbol, fullyQualified, b.document.Path, node.Range().Start),
		Kind:               semantic.PropertySymbol,
		Name:               name,
		FullyQualified:     fullyQualified,
		Container:          container,
		Path:               b.document.Path,
		Range:              node.RangeTrimmedTrivia(),
		SelectionRange:     selectionRange(variable, node),
		Visibility:         declarationVisibility(node),
		WriteVisibility:    writeVisibility,
		HasWriteVisibility: hasWriteVisibility,
		Flags:              parameter.Flags | semantic.PromotedFlag,
		Type:               parameter.Type,
		NativeType:         parameter.NativeType,
		DocType:            parameter.DocType,
		Attributes:         bindAttributes(node, context),
	}
	applyAttributeTypeSemantics(&symbol, context)
	applyAttributeFlags(&symbol)
	b.addSymbol(classScope, symbol)
}

func (b *documentBuilder) bindParameter(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	container semantic.SymbolID,
	documented map[string]types.Type,
	assistantTags map[string][]string,
	templates []string,
) semantic.Parameter {
	name := phpquery.ParameterName(node)
	variable := phpquery.DirectChild(node, phpsyntax.PhpVariable)
	nativeType := b.bindNativeType(phpquery.ParameterType(node), context)
	docType := context.ResolvePHPDocType(documented[name], templates)
	if docType.IsUnknown() {
		// A promoted property may carry its property PHPDoc directly before
		// the constructor parameter instead of on the constructor itself.
		// Treat an inline @var as the parameter's documented type so both the
		// parameter and the synthetic promoted property retain that contract.
		inlineDocumentation := phpdoc.Parse(leadingDocComment(node))
		inlineType := inlineDocumentation.Vars["$"+strings.TrimPrefix(name, "$")]
		if inlineType.IsUnknown() {
			inlineType = inlineDocumentation.Vars[""]
		}
		docType = context.ResolvePHPDocType(inlineType, templates)
	}
	effective := effectiveType(nativeType, docType)
	if effective.IsUnknown() {
		effective = types.Mixed()
	}
	flags := declarationFlags(node)
	if hasToken(node, phpsyntax.TkEllipsis, "") {
		flags |= semantic.VariadicFlag
	}
	fullyQualified := string(container) + ":" + name
	parameterID := semantic.NewSymbolID(
		semantic.ParameterSymbol,
		fullyQualified,
		b.document.Path,
		node.Range().Start,
	)
	attributes := bindAttributes(node, context)
	effective = attributeRefinedType(effective, attributes, context)
	defaultNode := phpquery.ParameterDefault(node)
	defaultRange := cst.TextRange{}
	var defaultValue *semantic.AttributeValue
	if defaultNode != nil {
		defaultRange = defaultNode.RangeTrimmedTrivia()
		if _, hasContract := semantic.AttributeNamed(
			attributes,
			"ReturnTypeContract",
		); hasContract {
			value := bindAttributeValue(defaultNode, context, 0)
			defaultValue = &value
		}
	}
	symbol := semantic.Symbol{
		ID:             parameterID,
		Kind:           semantic.ParameterSymbol,
		Name:           name,
		FullyQualified: fullyQualified,
		Container:      container,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: selectionRange(variable, node),
		Flags:          flags,
		Type:           effective,
		NativeType:     nativeType,
		DocType:        docType,
		Attributes:     attributes,
	}
	applyAttributeFlags(&symbol)
	flags = symbol.Flags
	b.addSymbol(scope, symbol)
	b.markDeclaration(variable)
	b.bindDirectTypeReferences(node, scope, context, container)
	b.bindAttributeReferences(node, scope, context)
	b.document.SetTypeFact(semantic.NodeIdentity(variable), semantic.TypeFact{
		Type:       effective,
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.NativeSource,
		Origin:     node.RangeTrimmedTrivia(),
	})
	return semantic.Parameter{
		ID:             parameterID,
		Name:           name,
		Type:           effective,
		NativeType:     nativeType,
		DocType:        docType,
		AssistantTags:  append([]string(nil), assistantTags[name]...),
		Attributes:     attributes,
		DefaultValue:   defaultValue,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: selectionRange(variable, node),
		DefaultRange:   defaultRange,
		Flags:          flags,
		Optional:       phpquery.ParameterOptional(node),
	}
}

func (b *documentBuilder) bindProperties(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	container semantic.SymbolID,
) {
	nativeType := b.bindNativeType(phpquery.PropertyType(node), context)
	b.bindDirectTypeReferences(node, scope, context, container)
	b.bindAttributeReferences(node, scope, context)
	documentation := phpdoc.Parse(leadingDocComment(node))
	templates := b.containerTemplateNames(container)
	for index := 0; index < node.ChildCount(); index++ {
		variable, ok := node.Child(index).(*phpsyntax.Node)
		if !ok || variable.Kind() != phpsyntax.PhpVariable {
			continue
		}
		name := phpquery.VariableName(variable)
		docType := documentation.Vars["$"+name]
		if docType.IsUnknown() {
			docType = documentation.Vars[""]
		}
		docType = context.ResolvePHPDocType(docType, templates)
		effective := effectiveType(nativeType, docType)
		fullyQualified := string(container) + "::$" + name
		writeVisibility, hasWriteVisibility := declarationWriteVisibility(node)
		symbol := semantic.Symbol{
			ID:                 semantic.NewSymbolID(semantic.PropertySymbol, fullyQualified, b.document.Path, variable.Range().Start),
			Kind:               semantic.PropertySymbol,
			Name:               name,
			FullyQualified:     fullyQualified,
			Container:          container,
			Path:               b.document.Path,
			Range:              node.RangeTrimmedTrivia(),
			SelectionRange:     variable.RangeTrimmedTrivia(),
			Visibility:         declarationVisibility(node),
			WriteVisibility:    writeVisibility,
			HasWriteVisibility: hasWriteVisibility,
			Flags:              declarationFlags(node),
			Type:               effective,
			NativeType:         nativeType,
			DocType:            docType,
			Attributes:         bindAttributes(node, context),
		}
		applyDocumentFlags(&symbol, documentation)
		applyAttributeTypeSemantics(&symbol, context)
		applyAttributeFlags(&symbol)
		b.addSymbol(scope, symbol)
		b.markDeclaration(variable)
		b.document.SetTypeFact(semantic.NodeIdentity(variable), semantic.TypeFact{
			Type:       effective,
			Confidence: semantic.DeclaredConfidence,
			Source:     semantic.NativeSource,
			Origin:     node.RangeTrimmedTrivia(),
		})
	}
}

func (b *documentBuilder) bindConstants(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	container semantic.SymbolID,
	classConstant bool,
) {
	nativeType := types.Unknown()
	if typeNode := phpquery.DirectChild(node, phpsyntax.PhpType); typeNode != nil {
		nativeType = b.bindNativeType(typeNode.Text(), context)
	}
	b.bindDirectTypeReferences(node, scope, context, container)
	b.bindAttributeReferences(node, scope, context)
	documentation := phpdoc.Parse(leadingDocComment(node))
	attributes := bindAttributes(node, context)
	expectName := false
	expectValue := false
	currentSymbol := -1
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		switch value := element.(type) {
		case *phpsyntax.Token:
			if strings.EqualFold(value.Text(), "const") || value.Kind() == phpsyntax.TkComma {
				expectName = true
				expectValue = false
				currentSymbol = -1
			} else if value.Kind() == phpsyntax.TkEquals {
				expectName = false
				expectValue = true
			}
		case *phpsyntax.Node:
			if expectValue && currentSymbol >= 0 {
				b.document.Symbols[currentSymbol].ConstantArray =
					constantArrayItems(value)
				if nativeType.IsUnknown() {
					inferred := constantLiteralType(value)
					if !inferred.IsUnknown() {
						b.document.Symbols[currentSymbol].Type = inferred
					}
				}
				expectValue = false
				continue
			}
			if !expectName || value.Kind() != phpsyntax.PhpName {
				continue
			}
			name := phpquery.NameValue(value)
			kind := semantic.GlobalConstantSymbol
			fullyQualified := qualify(context.Namespace, name)
			if classConstant {
				kind = semantic.ClassConstantSymbol
				fullyQualified = string(container) + "::" + name
			}
			symbol := semantic.Symbol{
				ID:             semantic.NewSymbolID(kind, fullyQualified, b.document.Path, value.Range().Start),
				Kind:           kind,
				Name:           name,
				FullyQualified: fullyQualified,
				Container:      container,
				Path:           b.document.Path,
				Range:          node.RangeTrimmedTrivia(),
				SelectionRange: value.RangeTrimmedTrivia(),
				Visibility:     declarationVisibility(node),
				Flags:          declarationFlags(node),
				Type:           nativeType,
				NativeType:     nativeType,
				Attributes:     attributes,
				DocSummary:     documentation.Summary,
			}
			applyDocumentFlags(&symbol, documentation)
			applyAttributeTypeSemantics(&symbol, context)
			applyAttributeFlags(&symbol)
			currentSymbol = len(b.document.Symbols)
			b.addSymbol(scope, symbol)
			b.markDeclaration(value)
			expectName = false
		}
	}
}

func constantLiteralType(node *phpsyntax.Node) types.Type {
	if node == nil {
		return types.Unknown()
	}
	if value, ok := phpliteral.TypeOf(node); ok {
		return value
	}
	switch node.Kind() {
	case phpsyntax.PhpUnaryExpression:
		text := strings.ReplaceAll(strings.TrimSpace(node.Text()), "_", "")
		if len(text) > 1 && (text[0] == '+' || text[0] == '-') {
			number := text[1:]
			if numericLiteralIsFloat(number) {
				return types.LiteralFloat(text)
			}
			return types.LiteralInt(text)
		}
	}
	return types.Unknown()
}

func constantArrayItems(
	node *phpsyntax.Node,
) []semantic.ConstantArrayItem {
	if node == nil || node.Kind() != phpsyntax.PhpArray {
		return nil
	}
	var result []semantic.ConstantArrayItem
	for index := 0; index < node.ChildCount(); index++ {
		item, ok := node.Child(index).(*phpsyntax.Node)
		if !ok || item.Kind() != phpsyntax.PhpArrayItem {
			continue
		}
		key := phpquery.ArrayItemKey(item)
		value := phpquery.ArrayItemValue(item)
		if key == nil || key.Kind() != phpsyntax.PhpString ||
			value == nil {
			continue
		}
		name := phpquery.StringValue(key)
		if name == "" {
			continue
		}
		result = append(result, semantic.ConstantArrayItem{
			Key:        name,
			KeyRange:   key.RangeTrimmedTrivia(),
			Value:      strings.TrimSpace(value.Text()),
			ValueRange: value.RangeTrimmedTrivia(),
			Type:       constantArrayValueType(value),
		})
	}
	return result
}

func constantArrayValueType(node *phpsyntax.Node) types.Type {
	if node == nil {
		return types.Unknown()
	}
	switch node.Kind() {
	case phpsyntax.PhpArray:
		return types.Array(types.Mixed(), types.Mixed())
	case phpsyntax.PhpString:
		return types.String()
	case phpsyntax.PhpNumber:
		if numericLiteralIsFloat(node.Text()) {
			return types.Float()
		}
		return types.Int()
	case phpsyntax.PhpBoolean:
		return types.Bool()
	case phpsyntax.PhpNull:
		return types.Null()
	case phpsyntax.PhpUnaryExpression:
		if literal := constantLiteralType(node); !literal.IsUnknown() {
			if numericLiteralIsFloat(node.Text()) {
				return types.Float()
			}
			return types.Int()
		}
	}
	return types.Unknown()
}

func numericLiteralIsFloat(value string) bool {
	value = strings.TrimLeft(
		strings.ReplaceAll(strings.TrimSpace(value), "_", ""),
		"+-",
	)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "0x") ||
		strings.HasPrefix(lower, "0b") ||
		strings.HasPrefix(lower, "0o") {
		return false
	}
	return strings.ContainsAny(lower, ".e")
}

func returnMetadata(
	body *phpsyntax.Node,
	context resolver.NameContext,
) ([]semantic.LiteralReturn, []semantic.ConstantReturn) {
	if body == nil {
		return nil, nil
	}
	var literalResult []semantic.LiteralReturn
	var constantResult []semantic.ConstantReturn
	visitDirectFunctionReturns(body, func(statement *phpsyntax.Node) {
		var value *phpsyntax.Node
		for index := 0; index < statement.ChildCount(); index++ {
			if child, ok := statement.Child(index).(*phpsyntax.Node); ok {
				value = child
				break
			}
		}
		literalType := constantLiteralType(value)
		if value != nil && !literalType.IsUnknown() {
			literalValue := strings.TrimSpace(value.Text())
			if value.Kind() == phpsyntax.PhpString {
				literalValue = phpquery.StringValue(value)
			}
			literalResult = append(literalResult, semantic.LiteralReturn{
				Value: literalValue,
				Range: value.RangeTrimmedTrivia(),
				Type:  literalType,
			})
		}
		if value == nil ||
			(value.Kind() != phpsyntax.PhpScopedAccess &&
				value.Kind() != phpsyntax.PhpMemberAccess) {
			return
		}
		text := strings.TrimSpace(value.Text())
		separator := strings.LastIndex(text, "::")
		if separator <= 0 || separator+2 >= len(text) {
			return
		}
		receiver := strings.TrimSpace(text[:separator])
		name := strings.TrimSpace(text[separator+2:])
		if receiver == "" || name == "" ||
			strings.EqualFold(name, "class") {
			return
		}
		switch strings.ToLower(receiver) {
		case "self", "static", "parent":
			receiver = strings.ToLower(receiver)
		default:
			receiver = context.ResolveClass(receiver)
		}
		constantResult = append(constantResult, semantic.ConstantReturn{
			Receiver: receiver,
			Name:     name,
			Range:    value.RangeTrimmedTrivia(),
		})
	})
	return literalResult, constantResult
}

func visitDirectFunctionReturns(
	parent *phpsyntax.Node,
	visit func(*phpsyntax.Node),
) {
	for index := 0; index < parent.ChildCount(); index++ {
		child, ok := parent.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		switch child.Kind() {
		case phpsyntax.PhpReturnStatement:
			visit(child)
		case phpsyntax.PhpMethodDeclaration,
			phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpClosure,
			phpsyntax.PhpArrowFunction:
			continue
		default:
			visitDirectFunctionReturns(child, visit)
		}
	}
}

func (b *documentBuilder) bindEnumCase(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	container semantic.SymbolID,
) {
	nameNode := phpquery.DirectChild(node, phpsyntax.PhpName)
	name := phpquery.NameValue(nameNode)
	if name == "" {
		return
	}
	fullyQualified := string(container) + "::" + name
	symbol := semantic.Symbol{
		ID:             semantic.NewSymbolID(semantic.EnumCaseSymbol, fullyQualified, b.document.Path, node.Range().Start),
		Kind:           semantic.EnumCaseSymbol,
		Name:           name,
		FullyQualified: fullyQualified,
		Container:      container,
		Path:           b.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: selectionRange(nameNode, node),
		Attributes:     bindAttributes(node, context),
	}
	applyDocumentFlags(&symbol, phpdoc.Parse(leadingDocComment(node)))
	applyAttributeTypeSemantics(&symbol, context)
	applyAttributeFlags(&symbol)
	b.addSymbol(scope, symbol)
	b.markDeclaration(nameNode)
	b.bindAttributeReferences(node, scope, context)
}

func (b *documentBuilder) bindNativeType(source string, context resolver.NameContext) types.Type {
	source = strings.TrimSpace(source)
	if source == "" {
		return types.Unknown()
	}
	value, err := types.ParseNative(source)
	if err != nil {
		return types.Error()
	}
	return resolveNativeType(value, source, context)
}

func resolveNativeType(
	value types.Type,
	source string,
	context resolver.NameContext,
) types.Type {
	switch value.Kind() {
	case types.ObjectKind:
		if nativeTypeIsFullyQualified(source, value.Name()) {
			return types.Named(
				strings.TrimPrefix(value.Name(), "\\"),
				value.Arguments()...,
			)
		}
		return context.ResolveType(value)
	case types.UnionKind:
		values := value.Arguments()
		for index := range values {
			values[index] = resolveNativeType(values[index], source, context)
		}
		return types.Union(values...)
	case types.IntersectionKind:
		values := value.Arguments()
		for index := range values {
			values[index] = resolveNativeType(values[index], source, context)
		}
		return types.Intersection(values...)
	default:
		return context.ResolveType(value)
	}
}

func nativeTypeIsFullyQualified(source, name string) bool {
	if source == "" || name == "" {
		return false
	}
	needle := "\\" + strings.TrimPrefix(name, "\\")
	for start := 0; start < len(source); {
		index := strings.Index(source[start:], needle)
		if index < 0 {
			return false
		}
		index += start
		if index == 0 || !isPHPTypeNameByte(source[index-1]) {
			return true
		}
		start = index + len(needle)
	}
	return false
}

func isPHPTypeNameByte(value byte) bool {
	return value == '\\' || value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value >= 0x80
}

func (b *documentBuilder) addSymbol(scope semantic.ScopeID, symbol semantic.Symbol) {
	b.document.Symbols = append(b.document.Symbols, symbol)
	key := symbolLookupKey(symbol)
	current := &b.document.Scopes[scope]
	current.AddSymbol(key, symbol.ID)
}

func (b *documentBuilder) symbol(id semantic.SymbolID) (semantic.Symbol, bool) {
	for _, symbol := range b.document.Symbols {
		if symbol.ID == id {
			return symbol, true
		}
	}
	return semantic.Symbol{}, false
}

func (b *documentBuilder) newScope(
	kind semantic.ScopeKind,
	parent semantic.ScopeID,
	rng phpsyntax.TextRange,
	owner semantic.SymbolID,
	context resolver.NameContext,
) semantic.ScopeID {
	id := semantic.ScopeID(len(b.document.Scopes))
	b.document.Scopes = append(b.document.Scopes, semantic.Scope{
		ID:        id,
		Kind:      kind,
		Parent:    parent,
		Range:     rng,
		Owner:     owner,
		Namespace: context.Namespace,
		Imports:   context.Imports,
	})
	return id
}

func (b *documentBuilder) markDeclaration(node *phpsyntax.Node) {
	if node != nil {
		b.declarations[semantic.NodeIdentity(node)] = struct{}{}
	}
}

func selectionRange(node, fallback *phpsyntax.Node) phpsyntax.TextRange {
	if node != nil {
		return node.RangeTrimmedTrivia()
	}
	if fallback != nil {
		return fallback.RangeTrimmedTrivia()
	}
	return phpsyntax.TextRange{}
}

func qualify(namespace, name string) string {
	namespace = strings.Trim(namespace, "\\")
	name = strings.Trim(name, "\\")
	if namespace == "" {
		return name
	}
	return namespace + "\\" + name
}

func resolveNames(context resolver.NameContext, names []string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, context.ResolveClass(name))
	}
	return result
}

func symbolLookupKey(symbol semantic.Symbol) string {
	switch symbol.Kind {
	case semantic.ParameterSymbol, semantic.LocalSymbol:
		return symbol.Name
	case semantic.GlobalConstantSymbol, semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
		return symbol.Name
	default:
		return strings.ToLower(strings.TrimPrefix(symbol.Name, "$"))
	}
}
