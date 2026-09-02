package diagnostics

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/phpanalysis"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/suppression"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	ShopwarePHPRepositoryInLoopCode           lsp.DiagnosticID = "shopware.php.repository.in_loop"
	ShopwarePHPInternalClassExtensionCode     lsp.DiagnosticID = "shopware.php.internal.class_extension"
	ShopwarePHPInternalFunctionCallCode       lsp.DiagnosticID = "shopware.php.internal.function_call"
	ShopwarePHPInternalMethodCallCode         lsp.DiagnosticID = "shopware.php.internal.method_call"
	ShopwarePHPSessionConstructorCode         lsp.DiagnosticID = "shopware.php.session.constructor"
	ShopwarePHPSessionPaymentHandlerCode      lsp.DiagnosticID = "shopware.php.session.payment_handler"
	ShopwarePHPSessionStoreAPICode            lsp.DiagnosticID = "shopware.php.session.store_api"
	ShopwarePHPScheduledTaskIntervalCode      lsp.DiagnosticID = "shopware.php.scheduled_task.interval"
	ShopwarePHPUserStoreTokenCode             lsp.DiagnosticID = "shopware.php.user.store_token"
	ShopwarePHPConcreteDecoratorExtensionCode lsp.DiagnosticID = "shopware.php.decorator.concrete_extension"

	entityRepositoryClass = "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepository"
	paymentHandlerClass   = "Shopware\\Core\\Checkout\\Payment\\Cart\\PaymentHandler\\AbstractPaymentHandler"
	scheduledTaskClass    = "Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTask"
	userEntityClass       = "Shopware\\Core\\System\\User\\UserEntity"
	sessionInterfaceClass = "Symfony\\Component\\HttpFoundation\\Session\\SessionInterface"
	routeAnnotationClass  = "Symfony\\Component\\Routing\\Annotation\\Route"
	routeAttributeClass   = "Symfony\\Component\\Routing\\Attribute\\Route"
)

// ShopwarePHPSemanticAnalyzer runs type-aware Shopware checks over one shared
// linked semantic document and one CST traversal.
type ShopwarePHPSemanticAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewShopwarePHPSemanticAnalyzer(
	phpIndex *php.PHPIndex,
) *ShopwarePHPSemanticAnalyzer {
	return &ShopwarePHPSemanticAnalyzer{phpIndex: phpIndex}
}

func (a *ShopwarePHPSemanticAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if a == nil || a.phpIndex == nil || document == nil ||
		document.SyntaxLanguage != language.PHP || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	state, err := phpanalysis.ForDocument(a.phpIndex, document)
	if err != nil {
		return nil, err
	}
	if state == nil || state.Document == nil || state.Snapshot == nil {
		return nil, nil
	}
	run := shopwarePHPSemanticRun{
		document:        document,
		semantic:        state.Document,
		snapshot:        state.Snapshot,
		storeAPIClasses: make(map[semantic.SymbolID]bool),
	}
	phpquery.Visit(
		document.SyntaxTree.Root,
		func(node *phpsyntax.Node) bool {
			if ctx.Err() != nil {
				return false
			}
			switch node.Kind() {
			case phpsyntax.PhpClassDeclaration:
				run.checkClass(node)
			case phpsyntax.PhpMemberCall:
				run.checkMemberCall(node)
			case phpsyntax.PhpFunctionCall:
				run.checkFunctionCall(node)
			}
			return true
		},
		phpsyntax.PhpClassDeclaration,
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpFunctionCall,
	)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	suppressions := suppression.Parse(document.Source)
	filtered := run.problems[:0]
	for _, problem := range run.problems {
		if suppressions.Suppresses(problem.Range.Start, string(problem.ID)) {
			continue
		}
		filtered = append(filtered, problem)
	}
	sort.SliceStable(filtered, func(left, right int) bool {
		if filtered[left].Range.Start != filtered[right].Range.Start {
			return filtered[left].Range.Start < filtered[right].Range.Start
		}
		return filtered[left].ID < filtered[right].ID
	})
	return filtered, nil
}

type shopwarePHPSemanticRun struct {
	document        *lsp.TextDocument
	semantic        *semantic.Document
	snapshot        *semantic.Snapshot
	problems        []lsp.Problem
	storeAPIClasses map[semantic.SymbolID]bool
}

func (r *shopwarePHPSemanticRun) checkClass(classNode *phpsyntax.Node) {
	class, found := r.classSymbol(classNode)
	if !found || class.Kind != semantic.ClassSymbol {
		return
	}
	for _, parentName := range class.Extends() {
		parent, parentFound := preferredSemanticClass(r.snapshot, parentName)
		if !parentFound {
			continue
		}
		rng := r.parentRange(classNode, parent)
		if internalShopwareUse(parent, classNamespace(class), r.snapshot) {
			r.problems = append(r.problems, shopwarePHPSemanticProblem(
				ShopwarePHPInternalClassExtensionCode,
				rng,
				fmt.Sprintf(
					"Class %s extends internal Shopware class %s; internal APIs are not extension points",
					class.Name,
					trimPHPName(parent.FullyQualified),
				),
			))
		}
		if abstraction, ok := r.decoratorAbstraction(parent); ok {
			r.problems = append(r.problems, shopwarePHPSemanticProblem(
				ShopwarePHPConcreteDecoratorExtensionCode,
				rng,
				fmt.Sprintf(
					"Class %s should extend decoration abstraction %s instead of concrete decorator %s",
					class.Name,
					trimPHPName(abstraction.FullyQualified),
					trimPHPName(parent.FullyQualified),
				),
			))
		}
	}
	r.checkScheduledTask(class)
}

func (r *shopwarePHPSemanticRun) checkScheduledTask(class semantic.Symbol) {
	if equalPHPName(class.FullyQualified, scheduledTaskClass) ||
		!r.snapshot.IsSubtypeOf(class.FullyQualified, scheduledTaskClass) {
		return
	}
	for _, method := range r.semantic.Symbols {
		if method.Kind != semantic.MethodSymbol || method.Container != class.ID ||
			!strings.EqualFold(method.Name, "getDefaultInterval") {
			continue
		}
		for _, returned := range method.LiteralReturns() {
			if returned.Type.Kind() != types.LiteralIntKind {
				continue
			}
			interval, err := strconv.ParseInt(
				strings.ReplaceAll(returned.Value, "_", ""),
				0,
				64,
			)
			if err != nil || interval >= 300 {
				continue
			}
			r.problems = append(r.problems, shopwarePHPSemanticProblem(
				ShopwarePHPScheduledTaskIntervalCode,
				returned.Range,
				fmt.Sprintf(
					"Scheduled task interval is %d seconds; use at least 300 seconds",
					interval,
				),
			))
			return
		}
	}
}

func (r *shopwarePHPSemanticRun) checkMemberCall(call *phpsyntax.Node) {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return
	}
	receiverType := r.semantic.TypeOf(receiver).Type
	methodName := phpquery.CallMethodName(call)
	target := callTargetName(call)
	rng := call.RangeTrimmedTrivia()
	if target != nil {
		rng = target.RangeTrimmedTrivia()
	}

	if phpTypeIsSubtype(receiverType, r.snapshot, entityRepositoryClass) &&
		insidePHPLoop(call) {
		r.problems = append(r.problems, shopwarePHPSemanticProblem(
			ShopwarePHPRepositoryInLoopCode,
			rng,
			"EntityRepository method calls inside loops cause N+1 database queries",
		))
	}
	if strings.EqualFold(methodName, "getStoreToken") &&
		phpTypeIsSubtype(receiverType, r.snapshot, userEntityClass) {
		r.problems = append(r.problems, shopwarePHPSemanticProblem(
			ShopwarePHPUserStoreTokenCode,
			rng,
			"Do not read UserEntity::getStoreToken(); the Shopware store token is internal",
		))
	}
	if phpTypeIsSubtype(receiverType, r.snapshot, sessionInterfaceClass) {
		r.checkSessionCall(call, rng)
	}
	if target == nil {
		return
	}
	sourceNamespace := r.namespaceAt(rng.Start)
	if sourceNamespace == "Shopware" || strings.HasPrefix(sourceNamespace, "Shopware\\") {
		return
	}
	for _, method := range r.resolvedSymbols(target, semantic.MethodSymbol) {
		if !internalShopwareUse(method, sourceNamespace, r.snapshot) {
			continue
		}
		r.problems = append(r.problems, shopwarePHPSemanticProblem(
			ShopwarePHPInternalMethodCallCode,
			rng,
			"Call to internal Shopware method "+trimPHPName(method.FullyQualified)+" is not supported",
		))
		break
	}
}

func (r *shopwarePHPSemanticRun) checkSessionCall(
	call *phpsyntax.Node,
	rng cst.TextRange,
) {
	methodNode := phpquery.MethodAt(call)
	if methodNode != nil && strings.EqualFold(phpquery.MethodName(methodNode), "__construct") {
		r.problems = append(r.problems, shopwarePHPSemanticProblem(
			ShopwarePHPSessionConstructorCode,
			rng,
			"Do not access the Symfony session in a constructor; defer it to the method that needs it",
		))
	}
	classNode := phpquery.ClassAt(call)
	class, found := r.classSymbol(classNode)
	if !found {
		return
	}
	if !equalPHPName(class.FullyQualified, paymentHandlerClass) &&
		r.snapshot.IsSubtypeOf(class.FullyQualified, paymentHandlerClass) {
		r.problems = append(r.problems, shopwarePHPSemanticProblem(
			ShopwarePHPSessionPaymentHandlerCode,
			rng,
			"Session usage is not allowed in payment handlers",
		))
	}
	if r.isStoreAPIClass(classNode, class) {
		r.problems = append(r.problems, shopwarePHPSemanticProblem(
			ShopwarePHPSessionStoreAPICode,
			rng,
			"Session usage is not allowed in Store API controllers",
		))
	}
}

func (r *shopwarePHPSemanticRun) checkFunctionCall(call *phpsyntax.Node) {
	target := callTargetName(call)
	if target == nil {
		return
	}
	for _, function := range r.resolvedSymbols(target, semantic.FunctionSymbol) {
		if !internalShopwareUse(function, r.namespaceAt(target.Range().Start), r.snapshot) {
			continue
		}
		r.problems = append(r.problems, shopwarePHPSemanticProblem(
			ShopwarePHPInternalFunctionCallCode,
			target.RangeTrimmedTrivia(),
			"Call to internal Shopware function "+trimPHPName(function.FullyQualified)+" is not supported",
		))
		break
	}
}

func (r *shopwarePHPSemanticRun) classSymbol(
	node *phpsyntax.Node,
) (semantic.Symbol, bool) {
	if node == nil {
		return semantic.Symbol{}, false
	}
	name := phpquery.DirectChild(node, phpsyntax.PhpName)
	if name == nil {
		return semantic.Symbol{}, false
	}
	nameRange := name.RangeTrimmedTrivia()
	for _, symbol := range r.semantic.Symbols {
		if symbol.IsClassLike() && rangesTouch(nameRange, symbol.SelectionRange) {
			return symbol, true
		}
	}
	return semantic.Symbol{}, false
}

func (r *shopwarePHPSemanticRun) parentRange(
	classNode *phpsyntax.Node,
	parent semantic.Symbol,
) cst.TextRange {
	clause := phpquery.DirectChild(classNode, phpsyntax.PhpExtendsClause)
	for _, name := range phpquery.Nodes(clause, phpsyntax.PhpName) {
		for _, candidate := range r.resolvedSymbols(name, semantic.ClassSymbol) {
			if equalPHPName(candidate.FullyQualified, parent.FullyQualified) {
				return name.RangeTrimmedTrivia()
			}
		}
	}
	return classNode.RangeTrimmedTrivia()
}

func (r *shopwarePHPSemanticRun) resolvedSymbols(
	node *phpsyntax.Node,
	kind semantic.SymbolKind,
) []semantic.Symbol {
	if node == nil {
		return nil
	}
	rng := node.RangeTrimmedTrivia()
	seen := make(map[semantic.SymbolID]struct{})
	var result []semantic.Symbol
	for _, reference := range r.semantic.References {
		if !referenceCanResolveSymbolKind(reference, kind) ||
			!rangesTouch(rng, reference.Range) {
			continue
		}
		ids := append([]semantic.SymbolID{reference.Resolved}, reference.CandidateIDs()...)
		for _, id := range ids {
			if id == "" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			symbol, found := r.snapshot.Symbol(id)
			if !found || symbol.Kind != kind {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, symbol)
		}
	}
	return result
}

func referenceCanResolveSymbolKind(
	reference semantic.Reference,
	kind semantic.SymbolKind,
) bool {
	switch kind {
	case semantic.ClassSymbol:
		return reference.Kind == semantic.ClassName
	case semantic.FunctionSymbol:
		return reference.Kind == semantic.FunctionName
	case semantic.MethodSymbol:
		return reference.Kind == semantic.MemberName &&
			reference.TargetKind == semantic.MethodSymbol
	default:
		return reference.TargetKind == kind
	}
}

func (r *shopwarePHPSemanticRun) namespaceAt(offset uint32) string {
	result := r.semantic.Namespace
	bestWidth := ^uint32(0)
	for _, scope := range r.semantic.Scopes {
		if scope.Namespace == "" || !scope.Range.Contains(offset) ||
			scope.Range.Len() > bestWidth {
			continue
		}
		result = scope.Namespace
		bestWidth = scope.Range.Len()
	}
	return trimPHPName(result)
}

func (r *shopwarePHPSemanticRun) decoratorAbstraction(
	concrete semantic.Symbol,
) (semantic.Symbol, bool) {
	if concrete.Flags.Has(semantic.AbstractFlag) {
		return semantic.Symbol{}, false
	}
	members := (resolver.MemberResolver{Snapshot: r.snapshot}).Methods(
		types.Named(trimPHPName(concrete.FullyQualified)),
		"getDecorated",
	)
	if len(members) == 0 {
		return semantic.Symbol{}, false
	}
	queue := append([]string(nil), concrete.Extends()...)
	seen := make(map[string]struct{})
	for len(queue) != 0 {
		name := queue[0]
		queue = queue[1:]
		key := strings.ToLower(trimPHPName(name))
		if _, visited := seen[key]; visited {
			continue
		}
		seen[key] = struct{}{}
		parent, found := preferredSemanticClass(r.snapshot, name)
		if !found {
			continue
		}
		if parent.Flags.Has(semantic.AbstractFlag) {
			return parent, true
		}
		queue = append(queue, parent.Extends()...)
	}
	return semantic.Symbol{}, false
}

func (r *shopwarePHPSemanticRun) isStoreAPIClass(
	classNode *phpsyntax.Node,
	class semantic.Symbol,
) bool {
	if cached, found := r.storeAPIClasses[class.ID]; found {
		return cached
	}
	result := false
	for _, attributeNode := range phpquery.Attributes(classNode) {
		if !r.isRouteAttribute(attributeNode, class.Attributes()) {
			continue
		}
		defaults := localCallArgument(attributeNode, "defaults", 4)
		routeScope, found := r.arrayStringEntry(defaults, "_routeScope")
		if !found {
			continue
		}
		routeScope = unwrapPHPParentheses(routeScope)
		if r.expressionStringEquals(routeScope, "store-api") {
			result = true
			break
		}
		if routeScope == nil || routeScope.Kind() != phpsyntax.PhpArray {
			continue
		}
		for _, item := range phpquery.ArrayItems(routeScope) {
			if r.expressionStringEquals(phpquery.ArrayItemValue(item), "store-api") {
				result = true
				break
			}
		}
		if result {
			break
		}
	}
	r.storeAPIClasses[class.ID] = result
	return result
}

func (r *shopwarePHPSemanticRun) isRouteAttribute(
	node *phpsyntax.Node,
	attributes []semantic.Attribute,
) bool {
	rng := node.RangeTrimmedTrivia()
	for _, attribute := range attributes {
		if !rangesTouch(rng, attribute.Range) {
			continue
		}
		name := trimPHPName(attribute.Name)
		return equalPHPName(name, routeAnnotationClass) ||
			equalPHPName(name, routeAttributeClass)
	}
	return false
}

func (r *shopwarePHPSemanticRun) arrayStringEntry(
	array *phpsyntax.Node,
	name string,
) (*phpsyntax.Node, bool) {
	array = unwrapPHPParentheses(array)
	if array == nil || array.Kind() != phpsyntax.PhpArray {
		return nil, false
	}
	for _, item := range phpquery.ArrayItems(array) {
		key := phpquery.ArrayItemKey(item)
		if key == nil || !r.expressionStringEquals(key, name) {
			continue
		}
		return phpquery.ArrayItemValue(item), true
	}
	return nil, false
}

func (r *shopwarePHPSemanticRun) expressionStringEquals(
	node *phpsyntax.Node,
	expected string,
) bool {
	node = unwrapPHPParentheses(node)
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpString {
		return strings.EqualFold(phpquery.StringValue(node), expected)
	}
	for _, constant := range r.resolvedSymbols(node, semantic.ClassConstantSymbol) {
		if constant.Type.Kind() == types.LiteralStringKind &&
			strings.EqualFold(constant.Type.Name(), expected) {
			return true
		}
	}
	return false
}

func preferredSemanticClass(
	snapshot *semantic.Snapshot,
	name string,
) (semantic.Symbol, bool) {
	if snapshot == nil {
		return semantic.Symbol{}, false
	}
	classes := snapshot.Classes(trimPHPName(name))
	if len(classes) == 0 {
		return semantic.Symbol{}, false
	}
	for _, class := range classes {
		if !class.Flags.Has(semantic.GeneratedStubFlag) {
			return class, true
		}
	}
	return classes[0], true
}

func internalShopwareUse(
	target semantic.Symbol,
	sourceNamespace string,
	snapshot *semantic.Snapshot,
) bool {
	if !target.Flags.Has(semantic.InternalFlag) {
		return false
	}
	targetNamespace := symbolNamespace(target, snapshot)
	if targetNamespace != "Shopware" && !strings.HasPrefix(targetNamespace, "Shopware\\") {
		return false
	}
	return !samePHPNamespacePackage(sourceNamespace, targetNamespace)
}

func symbolNamespace(symbol semantic.Symbol, snapshot *semantic.Snapshot) string {
	if symbol.Container != "" && snapshot != nil {
		if container, found := snapshot.Symbol(symbol.Container); found {
			return classNamespace(container)
		}
	}
	return phpNamespace(symbol.FullyQualified)
}

func classNamespace(class semantic.Symbol) string {
	return phpNamespace(class.FullyQualified)
}

func phpNamespace(name string) string {
	name = trimPHPName(name)
	if separator := strings.LastIndex(name, "\\"); separator >= 0 {
		return name[:separator]
	}
	return ""
}

func samePHPNamespacePackage(left, right string) bool {
	left = trimPHPName(left)
	right = trimPHPName(right)
	return left == right || left != "" && right != "" &&
		(strings.HasPrefix(left, right+"\\") || strings.HasPrefix(right, left+"\\"))
}

func insidePHPLoop(node *phpsyntax.Node) bool {
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpForStatement,
			phpsyntax.PhpForeachStatement,
			phpsyntax.PhpWhileStatement,
			phpsyntax.PhpDoWhileStatement:
			return true
		}
	}
	return false
}

func rangesTouch(left, right cst.TextRange) bool {
	return left.Contains(right.Start) || right.Contains(left.Start) || left == right
}

func trimPHPName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "\\")
}

func equalPHPName(left, right string) bool {
	return strings.EqualFold(trimPHPName(left), trimPHPName(right))
}

func shopwarePHPSemanticProblem(
	code lsp.DiagnosticID,
	rng cst.TextRange,
	message string,
) lsp.Problem {
	return lsp.Problem{
		ID:       code,
		Range:    rng,
		Message:  message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-lsp",
	}
}
