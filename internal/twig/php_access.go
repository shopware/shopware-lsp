package twig

import (
	"strings"
	"unicode"

	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// PHPAccessResolver maps Twig's attribute syntax onto the shared PHP semantic
// graph. It is deliberately independent of LSP transport so completion,
// definition, hover, and diagnostics resolve the same target.
type PHPAccessResolver struct {
	PHP  *php.PHPIndex
	Twig *TwigIndexer

	typeAnnotationsRoot *twigsyntax.Node
	typeAnnotations     map[string]types.Type
}

type PHPMemberResolution struct {
	Name     string
	NameNode *twigsyntax.Node
	Receiver types.Type
	Members  []phpresolver.ResolvedMember
}

func (r PHPAccessResolver) ResolveName(
	templatePath string,
	root,
	nameNode *twigsyntax.Node,
) (PHPMemberResolution, bool) {
	if nameNode == nil || nameNode.Kind() != twigsyntax.TwigLiteralName {
		return PHPMemberResolution{}, false
	}
	for current := nameNode.Parent(); current != nil; current = current.Parent() {
		if current.Kind() != twigsyntax.TwigAccessor ||
			accessorMemberNameNode(current) != nameNode {
			continue
		}
		return r.ResolveAccessor(templatePath, root, current)
	}
	return PHPMemberResolution{}, false
}

func (r PHPAccessResolver) ResolveAccessor(
	templatePath string,
	root,
	accessor *twigsyntax.Node,
) (PHPMemberResolution, bool) {
	resolution, ok := r.accessorResolution(
		templatePath,
		root,
		accessor,
	)
	if !ok || len(resolution.Members) == 0 {
		return PHPMemberResolution{}, false
	}
	return resolution, true
}

// InspectAccessor resolves enough information to distinguish a definitely
// missing PHP member from an unknown/dynamic Twig access. It deliberately
// rejects arrays, collection-like objects, unresolved classes, broad object
// types, and classes with magic accessors to keep diagnostics low-noise.
func (r PHPAccessResolver) InspectAccessor(
	templatePath string,
	root,
	accessor *twigsyntax.Node,
) (PHPMemberResolution, bool) {
	resolution, ok := r.accessorResolution(
		templatePath,
		root,
		accessor,
	)
	if !ok || !definiteTwigObjectReceiver(
		r.snapshot(),
		resolution.Receiver,
	) {
		return PHPMemberResolution{}, false
	}
	return resolution, true
}

func (r PHPAccessResolver) accessorResolution(
	templatePath string,
	root,
	accessor *twigsyntax.Node,
) (PHPMemberResolution, bool) {
	nameNode := accessorMemberNameNode(accessor)
	if nameNode == nil {
		return PHPMemberResolution{}, false
	}
	name := strings.TrimSpace(nameNode.Text())
	receiver := r.AccessorReceiverType(templatePath, root, accessor)
	if receiver.IsUnknown() {
		return PHPMemberResolution{}, false
	}
	members := ResolvePHPMembers(r.snapshot(), receiver, name)
	return PHPMemberResolution{
		Name:     name,
		NameNode: nameNode,
		Receiver: receiver,
		Members:  members,
	}, true
}

func definiteTwigObjectReceiver(
	snapshot *semantic.Snapshot,
	receiver types.Type,
) bool {
	if snapshot == nil || receiver.IsUnknown() {
		return false
	}
	foundObject := false
	var inspect func(types.Type) bool
	inspect = func(value types.Type) bool {
		switch value.Kind() {
		case types.NullKind:
			return true
		case types.UnionKind, types.IntersectionKind:
			for index := 0; index < value.ArgumentCount(); index++ {
				if !inspect(value.Argument(index)) {
					return false
				}
			}
			return true
		case types.ObjectKind:
			name := strings.Trim(value.Name(), "\\")
			if name == "" || len(snapshot.Classes(name)) == 0 ||
				strings.EqualFold(name, "stdClass") ||
				snapshot.IsSubtypeOf(name, "ArrayAccess") ||
				snapshot.IsSubtypeOf(name, "Traversable") ||
				snapshot.IsSubtypeOf(name, "Iterator") ||
				snapshot.IsSubtypeOf(name, "IteratorAggregate") ||
				twigClassHasMagicAccess(snapshot, name) {
				return false
			}
			foundObject = true
			return true
		default:
			return false
		}
	}
	return inspect(receiver) && foundObject
}

func twigClassHasMagicAccess(
	snapshot *semantic.Snapshot,
	className string,
) bool {
	members := phpresolver.MemberResolver{Snapshot: snapshot}
	for _, name := range []string{"__get", "__call"} {
		if len(members.Methods(types.Named(className), name)) != 0 {
			return true
		}
	}
	return false
}

func (r PHPAccessResolver) AccessorReceiverType(
	templatePath string,
	root,
	accessor *twigsyntax.Node,
) types.Type {
	if accessor == nil || accessor.Kind() != twigsyntax.TwigAccessor {
		return types.Unknown()
	}
	if LoopAccessorInScope(accessor) {
		if loopType, found := LoopContextType(r.PHP); found {
			return loopType
		}
	}
	operands := directTwigChildren(accessor, twigsyntax.TwigOperand)
	if len(operands) == 0 {
		return types.Unknown()
	}
	return r.expressionType(templatePath, root, firstTwigChild(operands[0]))
}

func (r PHPAccessResolver) expressionType(
	templatePath string,
	root,
	node *twigsyntax.Node,
) types.Type {
	if node == nil {
		return types.Unknown()
	}
	switch node.Kind() {
	case twigsyntax.TwigExpression, twigsyntax.TwigOperand,
		twigsyntax.TwigParenthesesExpression:
		return r.expressionType(templatePath, root, firstTwigChild(node))
	case twigsyntax.TwigLiteralName:
		name := strings.TrimSpace(node.Text())
		if local, found := r.loopVariableType(
			templatePath,
			root,
			node,
			name,
		); found {
			return local
		}
		return r.rootNameType(
			templatePath,
			root,
			name,
		)
	case twigsyntax.TwigAccessor:
		nameNode := accessorMemberNameNode(node)
		if nameNode == nil {
			return types.Unknown()
		}
		receiver := r.AccessorReceiverType(templatePath, root, node)
		return ResolvePHPMemberType(
			r.snapshot(),
			receiver,
			strings.TrimSpace(nameNode.Text()),
		)
	case twigsyntax.TwigFunctionCall:
		operands := directTwigChildren(node, twigsyntax.TwigOperand)
		if len(operands) == 0 {
			return types.Unknown()
		}
		nameExpression := firstTwigChild(operands[0])
		if nameExpression != nil &&
			nameExpression.Kind() == twigsyntax.TwigLiteralName {
			return r.twigFunctionType(strings.TrimSpace(nameExpression.Text()))
		}
		return r.expressionType(templatePath, root, nameExpression)
	case twigsyntax.TwigFilter:
		return r.twigFilterType(twigquery.FilterName(node))
	case twigsyntax.TwigLiteralString:
		return types.String()
	case twigsyntax.TwigLiteralNumber:
		return types.Int()
	case twigsyntax.TwigLiteralBoolean:
		return types.Bool()
	case twigsyntax.TwigLiteralArray:
		return types.List(types.Mixed())
	case twigsyntax.TwigLiteralHash:
		return types.Array(types.String(), types.Mixed())
	default:
		return types.Unknown()
	}
}

func (r PHPAccessResolver) rootNameType(
	templatePath string,
	root *twigsyntax.Node,
	name string,
) types.Type {
	if name == "" {
		return types.Unknown()
	}
	annotations := r.typeAnnotations
	if r.typeAnnotationsRoot != root {
		annotations = TwigTypeAnnotations(root)
	}
	if annotated, exists := annotations[name]; exists {
		return annotated
	}
	var values []types.Type
	if r.PHP != nil {
		variables, _ := r.PHP.TwigTemplateVariables(
			TemplateNames(templatePath)...,
		)
		for _, variable := range variables {
			if variable.Name != name {
				continue
			}
			if value, err := types.Parse(variable.Type); err == nil &&
				!value.IsUnknown() {
				values = append(values, value)
			}
		}
	}
	if len(values) == 0 && r.Twig != nil {
		globals, _ := r.Twig.GetGlobals(name)
		for _, global := range globals {
			if value, err := types.Parse(global.Type); err == nil &&
				!value.IsUnknown() {
				values = append(values, value)
			}
		}
	}
	return unionKnownTypes(values)
}

// forDocument prepares immutable document-wide facts that may be consulted
// repeatedly while resolving a Twig tree. Resolvers are passed by value, so
// the cache remains scoped to the current operation and needs no locking.
func (r PHPAccessResolver) forDocument(
	root *twigsyntax.Node,
) PHPAccessResolver {
	if root == nil || r.typeAnnotationsRoot == root {
		return r
	}
	r.typeAnnotationsRoot = root
	r.typeAnnotations = TwigTypeAnnotations(root)
	return r
}

func (r PHPAccessResolver) twigFunctionType(name string) types.Type {
	if r.Twig == nil || name == "" {
		return types.Unknown()
	}
	definitions, _ := r.Twig.GetTwigFunction(name)
	var values []types.Type
	for _, definition := range definitions {
		values = append(
			values,
			r.callbackReturnTypes(definition.FilePath, definition.Method)...,
		)
	}
	return unionKnownTypes(values)
}

func (r PHPAccessResolver) twigFilterType(name string) types.Type {
	if r.Twig == nil || name == "" {
		return types.Unknown()
	}
	definitions, _ := r.Twig.GetTwigFilter(name)
	var values []types.Type
	for _, definition := range definitions {
		values = append(
			values,
			r.callbackReturnTypes(definition.FilePath, definition.Method)...,
		)
	}
	return unionKnownTypes(values)
}

func (r PHPAccessResolver) callbackReturnTypes(
	path,
	callback string,
) []types.Type {
	if r.PHP == nil {
		return nil
	}
	methodName := callbackMethodName(callback)
	if methodName == "" {
		return nil
	}
	var result []types.Type
	for _, symbol := range r.snapshot().SymbolsIn(path) {
		if !symbol.IsClassLike() ||
			!isSemanticTwigExtensionClass(r.snapshot(), symbol) {
			continue
		}
		for _, member := range (phpresolver.MemberResolver{
			Snapshot: r.snapshot(),
		}).Methods(types.Named(symbol.FullyQualified), methodName) {
			if !member.Type.IsUnknown() {
				result = append(result, member.Type)
			}
		}
	}
	if len(result) > 0 {
		return result
	}
	receiver := callbackReceiverClass(callback)
	if receiver == "" {
		return nil
	}
	for _, class := range r.snapshot().Classes(receiver) {
		for _, member := range (phpresolver.MemberResolver{
			Snapshot: r.snapshot(),
		}).Methods(types.Named(class.FullyQualified), methodName) {
			if !member.Type.IsUnknown() {
				result = append(result, member.Type)
			}
		}
	}
	return result
}

func (r PHPAccessResolver) snapshot() *semantic.Snapshot {
	if r.PHP == nil {
		return semantic.NewSnapshot(0, nil)
	}
	return r.PHP.SemanticSnapshot()
}

func ResolvePHPMembers(
	snapshot *semantic.Snapshot,
	receiver types.Type,
	name string,
) []phpresolver.ResolvedMember {
	if snapshot == nil || receiver.IsUnknown() || name == "" {
		return nil
	}
	var result []phpresolver.ResolvedMember
	for _, member := range (phpresolver.MemberResolver{
		Snapshot: snapshot,
	}).All(receiver) {
		symbol := member.Symbol
		if symbol.Visibility != semantic.Public ||
			symbol.Flags.Has(semantic.StaticFlag) {
			continue
		}
		switch symbol.Kind {
		case semantic.PropertySymbol:
			if strings.EqualFold(
				strings.TrimPrefix(symbol.Name, "$"),
				name,
			) {
				result = append(result, member)
			}
		case semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
			if strings.EqualFold(symbol.Name, name) {
				result = append(result, member)
			}
		case semantic.MethodSymbol:
			if strings.EqualFold(symbol.Name, name) ||
				strings.EqualFold(TwigAttributeName(symbol.Name), name) {
				result = append(result, member)
			}
		}
	}
	return result
}

func ResolvePHPMemberType(
	snapshot *semantic.Snapshot,
	receiver types.Type,
	name string,
) types.Type {
	var values []types.Type
	for _, member := range ResolvePHPMembers(snapshot, receiver, name) {
		value := resolveRelativeTwigMemberType(
			snapshot,
			receiver,
			member.Symbol,
			member.Type,
		)
		if !value.IsUnknown() {
			values = append(values, value)
		}
	}
	return unionKnownTypes(values)
}

func resolveRelativeTwigMemberType(
	snapshot *semantic.Snapshot,
	receiver types.Type,
	symbol semantic.Symbol,
	value types.Type,
) types.Type {
	switch value.Kind() {
	case types.SelfKind, types.StaticKind:
		return receiver
	case types.ParentKind:
		if snapshot == nil {
			return types.Unknown()
		}
		container, ok := snapshot.Symbol(symbol.Container)
		if !ok || len(container.Extends) == 0 {
			return types.Unknown()
		}
		return types.Named(container.Extends[0])
	case types.UnionKind:
		values := value.Arguments()
		for index := range values {
			values[index] = resolveRelativeTwigMemberType(
				snapshot,
				receiver,
				symbol,
				values[index],
			)
		}
		return unionKnownTypes(values)
	case types.IntersectionKind:
		values := value.Arguments()
		for index := range values {
			values[index] = resolveRelativeTwigMemberType(
				snapshot,
				receiver,
				symbol,
				values[index],
			)
		}
		return types.Intersection(values...)
	default:
		return value
	}
}

func TwigAttributeName(method string) string {
	for _, prefix := range []string{"get", "is", "has"} {
		if len(method) <= len(prefix) ||
			!strings.EqualFold(method[:len(prefix)], prefix) ||
			!unicode.IsUpper(rune(method[len(prefix)])) {
			continue
		}
		suffix := method[len(prefix):]
		return strings.ToLower(suffix[:1]) + suffix[1:]
	}
	return ""
}

// TwigTypeDeclaration is type and documentation metadata declared in a Twig
// template through a types tag or the portable @var compatibility syntax.
type TwigTypeDeclaration struct {
	Type          types.Type
	Optional      bool
	Documentation string
	FromTypesTag  bool
}

// TwigTypeDeclarations parses Twig's native types tag together with the
// portable "{# @var variable \Fully\Qualified\Type #}" compatibility form.
func TwigTypeDeclarations(
	root *twigsyntax.Node,
) map[string]TwigTypeDeclaration {
	result := make(map[string]TwigTypeDeclaration)
	if root == nil {
		return result
	}
	for _, comment := range twigquery.Nodes(root, twigsyntax.TwigComment) {
		source := comment.Text()
		for {
			index := strings.Index(source, "@var")
			if index < 0 {
				break
			}
			source = source[index+len("@var"):]
			line := source
			if end := strings.IndexAny(line, "\r\n#"); end >= 0 {
				line = line[:end]
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				name, typeName := fields[0], fields[1]
				if strings.HasPrefix(fields[1], "$") ||
					twigVarAnnotationTypeFirst(fields[0], fields[1]) {
					name, typeName = fields[1], fields[0]
				}
				name = strings.TrimPrefix(name, "$")
				typeName = strings.TrimPrefix(typeName, "\\")
				if value, err := parseTwigAnnotationType(typeName); err == nil &&
					!value.IsUnknown() && name != "" {
					result[name] = TwigTypeDeclaration{Type: value}
				}
			}
			if end := strings.IndexAny(source, "\r\n"); end >= 0 {
				source = source[end+1:]
				continue
			}
			break
		}
	}
	for _, declaration := range TypesTagDeclarations(
		[]byte(root.Text()),
	) {
		if declaration.Name == "" || declaration.Type == "" {
			continue
		}
		value, err := parseTwigAnnotationType(declaration.Type)
		if err == nil && !value.IsUnknown() {
			result[declaration.Name] = TwigTypeDeclaration{
				Type:          value,
				Optional:      declaration.Optional,
				Documentation: declaration.Documentation,
				FromTypesTag:  true,
			}
		}
	}
	return result
}

// TwigTypeAnnotations retains the type-only view used by inference consumers.
func TwigTypeAnnotations(root *twigsyntax.Node) map[string]types.Type {
	declarations := TwigTypeDeclarations(root)
	result := make(map[string]types.Type, len(declarations))
	for name, declaration := range declarations {
		result[name] = declaration.Type
	}
	return result
}

// Twig tooling has historically treated the postfix T[] annotation spelling
// as a list, while the PHPDoc type parser correctly models the same syntax as
// an array with an array-key. Preserve the Twig-facing convention at this
// boundary without changing PHP type semantics.
func parseTwigAnnotationType(source string) (types.Type, error) {
	source = strings.TrimSpace(source)
	listDepth := 0
	for strings.HasSuffix(source, "[]") {
		listDepth++
		source = strings.TrimSpace(source[:len(source)-2])
	}
	value, err := types.Parse(source)
	if err != nil {
		return value, err
	}
	for range listDepth {
		value = types.List(value)
	}
	return phpresolver.NewNameContext("").ResolveType(value), nil
}

func accessorMemberNameNode(accessor *twigsyntax.Node) *twigsyntax.Node {
	if accessor == nil || accessor.Kind() != twigsyntax.TwigAccessor {
		return nil
	}
	operands := directTwigChildren(accessor, twigsyntax.TwigOperand)
	if len(operands) < 2 {
		return nil
	}
	for _, child := range directTwigChildren(
		operands[1],
		twigsyntax.TwigLiteralName,
	) {
		return child
	}
	return nil
}

func directTwigChildren(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) []*twigsyntax.Node {
	if node == nil {
		return nil
	}
	var result []*twigsyntax.Node
	for child := range node.ChildNodes() {
		if child.Kind() == kind {
			result = append(result, child)
		}
	}
	return result
}

func firstTwigChild(node *twigsyntax.Node) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		return child
	}
	return nil
}

func unionKnownTypes(values []types.Type) types.Type {
	result := make([]types.Type, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value.IsUnknown() || value.Kind() == types.ErrorKind {
			continue
		}
		key := value.Key()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	switch len(result) {
	case 0:
		return types.Unknown()
	case 1:
		return result[0]
	default:
		return types.Union(result...)
	}
}

func callbackReceiverClass(callback string) string {
	index := strings.LastIndex(callback, "::")
	if index < 0 {
		return ""
	}
	receiver := strings.TrimPrefix(callback[:index], "\\")
	receiver = strings.TrimSuffix(receiver, "::class")
	switch strings.ToLower(receiver) {
	case "", "self", "static", "parent", "self::class", "static::class":
		return ""
	default:
		return receiver
	}
}

func isSemanticTwigExtensionClass(
	snapshot *semantic.Snapshot,
	class semantic.Symbol,
) bool {
	for _, parent := range append(
		append([]string(nil), class.Extends...),
		class.Implements...,
	) {
		switch lastNamePart(parent) {
		case "AbstractExtension", "ExtensionInterface",
			"Twig_Extension", "Twig_ExtensionInterface":
			return true
		}
	}
	if snapshot == nil {
		return false
	}
	for _, target := range []string{
		"Twig\\Extension\\AbstractExtension",
		"Twig\\Extension\\ExtensionInterface",
		"Twig_Extension",
		"Twig_ExtensionInterface",
	} {
		if snapshot.IsSubtypeOf(class.FullyQualified, target) {
			return true
		}
	}
	return false
}
