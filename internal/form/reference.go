package form

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type ReferenceRole uint8

const (
	ReferenceNone ReferenceRole = iota
	ReferenceType
	ReferenceOption
	ReferenceField
)

type ReferenceOrigin uint8

const (
	OriginUse ReferenceOrigin = iota
	OriginDefinition
	OriginFieldDeclaration
	OriginFieldAccess
)

type Reference struct {
	Role      ReferenceRole
	Origin    ReferenceOrigin
	Name      string
	Node      *cst.Node
	Container *cst.Node
	FormType  string
	Class     string
}

// FactoryTypeReference identifies a form type passed to a typed Symfony form
// factory/controller API. Name is either the resolved PHP class or the legacy
// form alias accepted by the form index.
type FactoryTypeReference struct {
	Name  string
	Range cst.TextRange
}

// FactoryTypeReferences returns the statically resolvable form types created
// in a PHP document. Receiver type checks keep unrelated create()/createForm()
// methods out of related-navigation and other cross-file features.
func FactoryTypeReferences(
	ctx context.Context,
	root *phpsyntax.Node,
) []FactoryTypeReference {
	if root == nil {
		return nil
	}
	nameResolver := php.NewNameResolver(root)
	seen := make(map[string]struct{})
	var result []FactoryTypeReference
	for _, call := range phpquery.Calls(root) {
		method := strings.ToLower(phpquery.CallMethodName(call))
		argumentIndex := -1
		valid := false
		switch method {
		case "createform":
			argumentIndex = 0
			valid = isFactoryCall(ctx, call)
		case "create", "createbuilder":
			argumentIndex = 0
			valid = isFormFactoryServiceCall(ctx, call)
		case "createnamed", "createnamedbuilder":
			argumentIndex = 1
			valid = isNamedFactoryCall(ctx, call)
		}
		if !valid {
			continue
		}
		expression := factoryTypeArgument(call, argumentIndex)
		name := formTypeExpression(expression, nameResolver, true)
		if name == "" || expression == nil {
			continue
		}
		rng := expression.RangeTrimmedTrivia()
		key := strings.ToLower(name) + "\x00" + rng.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, FactoryTypeReference{
			Name:  name,
			Range: rng,
		})
	}
	return result
}

func factoryTypeArgument(
	call *phpsyntax.Node,
	position int,
) *phpsyntax.Node {
	for _, argument := range phpquery.Arguments(call) {
		if strings.EqualFold(phpquery.ArgumentName(argument), "type") {
			for child := range argument.ChildNodes() {
				if child.Kind() == phpsyntax.PhpName {
					continue
				}
				return child
			}
			return nil
		}
	}
	return phpquery.ArgumentExpression(call, position)
}

func ReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (Reference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return Reference{}, false
	}
	reference := Reference{
		Name: phpquery.StringValue(literal),
		Node: literal,
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext != nil && phpContext.InsideClass != nil {
		reference.Class = phpContext.InsideClass.FullyQualified
	}
	if reference.Class == "" {
		reference.Class = normalizePHPName(
			php.NewNameResolver(root).Resolve(
				phpquery.ClassName(phpquery.ClassAt(literal)),
			),
		)
	}

	if arrayAccess := ancestorOfKind(
		literal,
		phpsyntax.PhpArrayAccess,
	); arrayAccess != nil &&
		isOptionsArrayAccess(arrayAccess, literal) &&
		isFormScope(literal) {
		reference.Role = ReferenceOption
		reference.Container = arrayAccess
		reference.FormType = reference.Class
		return reference, true
	}
	if method := phpquery.MethodAt(literal); method != nil &&
		strings.EqualFold(phpquery.MethodName(method), "getParent") &&
		ancestorOfKind(literal, phpsyntax.PhpReturnStatement) != nil {
		reference.Role = ReferenceType
		reference.Container = method
		return reference, true
	}

	call := phpquery.CallAt(literal)
	if call == nil {
		return Reference{}, false
	}
	reference.Container = call
	callName := strings.ToLower(phpquery.CallMethodName(call))
	argumentIndex := phpquery.ArgumentIndex(call, literal)
	if argumentIndex < 0 {
		return Reference{}, false
	}
	directArgument := phpquery.ArgumentExpression(call, argumentIndex) == literal

	switch callName {
	case "add", "create":
		if isBuilderCall(ctx, call) {
			switch {
			case argumentIndex == 0 && directArgument:
				reference.Role = ReferenceField
				reference.Origin = OriginFieldDeclaration
				reference.FormType = reference.Class
				return reference, true
			case argumentIndex == 1 && directArgument:
				reference.Role = ReferenceType
				return reference, true
			case argumentIndex == 2 &&
				isTopLevelArrayEntry(literal, phpquery.ArgumentExpression(call, 2)):
				reference.Role = ReferenceOption
				reference.FormType = callFormType(call, 1, root)
				return reference, true
			}
		}
		// FormFactoryInterface::create() shares the method name with builder
		// create(). It is intentionally checked after the typed builder branch.
		if callName == "create" && isFactoryCall(ctx, call) {
			return factoryReference(reference, call, argumentIndex, root)
		}
	case "createform", "createbuilder":
		if isFactoryCall(ctx, call) {
			return factoryReference(reference, call, argumentIndex, root)
		}
	case "createnamed", "createnamedbuilder":
		if !isNamedFactoryCall(ctx, call) {
			break
		}
		switch {
		case argumentIndex == 0 && directArgument:
			reference.Role = ReferenceField
			reference.Origin = OriginFieldDeclaration
			return reference, true
		case argumentIndex == 1 && directArgument:
			reference.Role = ReferenceType
			return reference, true
		case argumentIndex == 3 &&
			isTopLevelArrayEntry(literal, phpquery.ArgumentExpression(call, 3)):
			reference.Role = ReferenceOption
			reference.FormType = callFormType(call, 1, root)
			return reference, true
		}
	case "get", "has":
		if argumentIndex == 0 && directArgument &&
			isFormCall(ctx, call) {
			reference.Role = ReferenceField
			reference.Origin = OriginFieldAccess
			reference.FormType = assignedFormType(call, root)
			return reference, true
		}
	case "setdefault", "hasdefault", "isrequired", "ismissing",
		"setallowedvalues", "addallowedvalues",
		"setallowedtypes", "addallowedtypes":
		if argumentIndex == 0 && directArgument &&
			isOptionsResolverCall(ctx, call) {
			reference.Role = ReferenceOption
			reference.Origin = OriginDefinition
			reference.FormType = reference.Class
			return reference, true
		}
	case "setdefaults", "setrequired", "setoptional", "setdefined":
		if argumentIndex == 0 &&
			isTopLevelArrayEntry(literal, phpquery.ArgumentExpression(call, 0)) &&
			isOptionsResolverCall(ctx, call) {
			reference.Role = ReferenceOption
			reference.Origin = OriginDefinition
			reference.FormType = reference.Class
			return reference, true
		}
	}
	return Reference{}, false
}

// IsLegacyBuilderTypeAlias reports whether a resolved type reference is the
// string alias argument of FormBuilderInterface::add()/create().
func IsLegacyBuilderTypeAlias(
	ctx context.Context,
	reference Reference,
) bool {
	if reference.Role != ReferenceType || reference.Node == nil ||
		reference.Container == nil ||
		strings.Contains(reference.Name, `\`) {
		return false
	}
	method := strings.ToLower(
		phpquery.CallMethodName(reference.Container),
	)
	if method != "add" && method != "create" ||
		phpquery.ArgumentIndex(reference.Container, reference.Node) != 1 ||
		phpquery.ArgumentExpression(reference.Container, 1) != reference.Node {
		return false
	}
	return isBuilderCall(ctx, reference.Container)
}

func factoryReference(
	reference Reference,
	call *phpsyntax.Node,
	argumentIndex int,
	root *phpsyntax.Node,
) (Reference, bool) {
	directArgument := phpquery.ArgumentExpression(call, argumentIndex) ==
		reference.Node
	switch {
	case argumentIndex == 0 && directArgument:
		reference.Role = ReferenceType
		return reference, true
	case argumentIndex == 2 &&
		isTopLevelArrayEntry(
			reference.Node,
			phpquery.ArgumentExpression(call, 2),
		):
		reference.Role = ReferenceOption
		reference.FormType = callFormType(call, 0, root)
		return reference, true
	default:
		return Reference{}, false
	}
}

func callFormType(
	call *phpsyntax.Node,
	index int,
	root *phpsyntax.Node,
) string {
	return formTypeExpression(
		phpquery.ArgumentExpression(call, index),
		php.NewNameResolver(root),
		true,
	)
}

func assignedFormType(
	call,
	root *phpsyntax.Node,
) string {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil || receiver.Kind() != phpsyntax.PhpVariable {
		return ""
	}
	name := "$" + phpquery.VariableName(receiver)
	function := phpquery.FunctionLikeAt(call)
	if function == nil {
		return ""
	}
	for _, candidate := range phpquery.Calls(function) {
		if phpquery.FunctionLikeAt(candidate) != function ||
			phpquery.AssignedVariable(candidate) != name {
			continue
		}
		method := strings.ToLower(phpquery.CallMethodName(candidate))
		if method != "createform" && method != "create" &&
			method != "createbuilder" {
			continue
		}
		if value := callFormType(candidate, 0, root); value != "" {
			return value
		}
	}
	return ""
}

func isBuilderCall(ctx context.Context, call *phpsyntax.Node) bool {
	if receiverIsAnySubtype(ctx, call,
		"Symfony\\Component\\Form\\FormBuilderInterface",
		"Symfony\\Component\\Form\\FormInterface",
	) {
		return true
	}
	return strings.EqualFold(
		phpquery.MethodName(call),
		"buildForm",
	) && declaredBuilderReceiver(call)
}

func isFactoryCall(ctx context.Context, call *phpsyntax.Node) bool {
	return receiverIsAnySubtype(ctx, call,
		"Symfony\\Component\\Form\\FormFactoryInterface",
		"Symfony\\Component\\Form\\FormFactory",
		"Symfony\\Bundle\\FrameworkBundle\\Controller\\Controller",
		"Symfony\\Bundle\\FrameworkBundle\\Controller\\ControllerTrait",
		"Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController",
	)
}

func isFormFactoryServiceCall(
	ctx context.Context,
	call *phpsyntax.Node,
) bool {
	return receiverIsAnySubtype(ctx, call,
		"Symfony\\Component\\Form\\FormFactoryInterface",
		"Symfony\\Component\\Form\\FormFactory",
	)
}

func isNamedFactoryCall(ctx context.Context, call *phpsyntax.Node) bool {
	return isFormFactoryServiceCall(ctx, call)
}

func isFormCall(ctx context.Context, call *phpsyntax.Node) bool {
	return receiverIsAnySubtype(ctx, call,
		"Symfony\\Component\\Form\\FormInterface",
	)
}

func isOptionsResolverCall(
	ctx context.Context,
	call *phpsyntax.Node,
) bool {
	if receiverIsAnySubtype(ctx, call,
		"Symfony\\Component\\OptionsResolver\\OptionsResolver",
		"Symfony\\Component\\OptionsResolver\\OptionsResolverInterface",
	) {
		return true
	}
	return isFormScope(call) && declaredOptionsReceiver(call)
}

func receiverIsAnySubtype(
	ctx context.Context,
	call *phpsyntax.Node,
	targets ...string,
) bool {
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return false
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return false
	}
	for _, target := range targets {
		if phpContext.Snapshot.Relations().IsSubtype(
			receiverType,
			types.Named(target),
		) {
			return true
		}
	}
	return false
}

func declaredBuilderReceiver(call *phpsyntax.Node) bool {
	return declaredReceiverContains(
		call,
		"FormBuilderInterface",
		"FormInterface",
	)
}

func declaredOptionsReceiver(call *phpsyntax.Node) bool {
	return declaredReceiverContains(
		call,
		"OptionsResolver",
		"OptionsResolverInterface",
	)
}

func declaredReceiverContains(
	call *phpsyntax.Node,
	names ...string,
) bool {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil || receiver.Kind() != phpsyntax.PhpVariable {
		return false
	}
	receiverName := phpquery.VariableName(receiver)
	for _, parameter := range phpquery.Parameters(
		phpquery.FunctionLikeAt(call),
	) {
		if !strings.EqualFold(
			strings.TrimPrefix(phpquery.ParameterName(parameter), "$"),
			receiverName,
		) {
			continue
		}
		declared := phpquery.ParameterType(parameter)
		for _, name := range names {
			if strings.Contains(declared, name) {
				return true
			}
		}
	}
	return false
}

func isFormScope(node *phpsyntax.Node) bool {
	method := phpquery.MethodAt(node)
	if method == nil {
		return false
	}
	switch strings.ToLower(phpquery.MethodName(method)) {
	case "buildform", "buildview", "finishview",
		"configureoptions", "setdefaultoptions":
		return true
	default:
		return false
	}
}

func isOptionsArrayAccess(
	arrayAccess,
	literal *phpsyntax.Node,
) bool {
	if arrayAccess == nil || literal == nil ||
		!nodeContains(arrayAccess, literal) {
		return false
	}
	text := strings.TrimSpace(arrayAccess.Text())
	return strings.HasPrefix(text, "$options[")
}

func isTopLevelArrayEntry(
	literal,
	argument *phpsyntax.Node,
) bool {
	array := phpquery.ArrayAt(argument)
	item := phpquery.ArrayItemAt(literal)
	if array == nil || item == nil || item.Parent() != array {
		return false
	}
	key := phpquery.ArrayItemKey(item)
	if key == nil {
		return phpquery.ArrayItemValue(item) == literal
	}
	return key == literal
}

func nodeContains(parent, child *phpsyntax.Node) bool {
	if parent == nil || child == nil {
		return false
	}
	return parent.Range().Start <= child.Range().Start &&
		child.Range().End <= parent.Range().End
}
