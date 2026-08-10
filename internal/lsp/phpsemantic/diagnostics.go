package phpsemantic

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/phpanalysis"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/stubs"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (p *Provider) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || !isPHP(document.URI) || document.LineIndex == nil {
		return nil, nil
	}
	analysis, err := phpanalysis.ForDocument(p.index, document)
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		return nil, nil
	}
	run := newPHPDiagnosticRun(p, document, analysis.Document, analysis.Snapshot)
	return run.analyze(), ctx.Err()
}

func (p *Provider) unavailableExtensionDiagnostic(
	reference semantic.Reference,
) (*lsp.Problem, bool) {
	if p == nil || p.index == nil {
		return nil, false
	}
	var extension string
	for index := 0; index < reference.QualifiedNameCount(); index++ {
		if candidate, found := stubs.ExtensionForSymbol(
			reference.QualifiedNameAt(index),
		); found {
			extension = candidate
			break
		}
	}
	if extension == "" {
		var found bool
		extension, found = stubs.ExtensionForSymbol(reference.Name)
		if !found {
			return nil, false
		}
	}
	enabled, known := p.index.Project().ExtensionAvailability(extension)
	if enabled {
		return nil, false
	}
	if !known {
		// The symbol belongs to a real optional runtime extension, but Composer's
		// positive requirements do not prove that the local runtime lacks it.
		return nil, true
	}
	diagnostic := &lsp.Problem{
		Range:    reference.Range,
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "php.extension",
		Source:   "shopware-php",
		Message: fmt.Sprintf(
			"%s requires disabled PHP extension ext-%s",
			reference.Name,
			strings.ReplaceAll(extension, "_", "-"),
		),
		Payload: map[string]interface{}{
			"extension": extension,
		},
	}
	return diagnostic, true
}

func formatDeprecationHover(symbol semantic.Symbol) string {
	result := "\n\n**Deprecated**"
	details, found := semantic.DeprecationOf(symbol.Attributes)
	if !found {
		return result
	}
	if details.Since != "" {
		result += " since " + details.Since
	}
	if details.Reason != "" {
		result += "\n\n" + details.Reason
	}
	if details.Replacement != "" {
		result += "\n\nReplacement: `" +
			strings.ReplaceAll(details.Replacement, "`", "\\`") + "`"
	}
	return result
}

func formatDeprecationDiagnostic(symbol semantic.Symbol) string {
	message := symbol.Name + " is deprecated"
	details, found := semantic.DeprecationOf(symbol.Attributes)
	if !found {
		return message
	}
	if details.Since != "" {
		message += " since " + details.Since
	}
	if details.Reason != "" {
		message += ": " + details.Reason
	}
	if details.Replacement != "" {
		message += "; use " + details.Replacement
	}
	return message
}

func lateStaticMemberMayBeDeclaredBySubclass(
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) bool {
	if root == nil || snapshot == nil || !reference.Static {
		return false
	}
	node := root.NodeAtOffset(reference.Range.Start)
	lateStatic := false
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpMemberAccess, phpsyntax.PhpScopedAccess,
			phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
			nodes := directNodes(current)
			lateStatic = len(nodes) > 0 && strings.EqualFold(
				strings.TrimSpace(nodes[0].Text()),
				"static",
			)
		}
		if lateStatic {
			break
		}
	}
	if !lateStatic {
		return false
	}

	extensible := false
	found := false
	visitDiagnosticObjectAlternatives(reference.Receiver, func(receiver types.Type) {
		snapshot.VisitClassViews(receiver.Name(), func(classView semantic.SymbolView) bool {
			found = true
			if !classView.Materialize().Flags.Has(semantic.FinalFlag) {
				extensible = true
			}
			return !extensible
		})
	})
	return !found || extensible
}

func visitDiagnosticObjectAlternatives(
	value types.Type,
	visit func(types.Type),
) {
	if value.Kind() == types.ObjectKind {
		visit(value)
		return
	}
	if value.Kind() != types.UnionKind && value.Kind() != types.IntersectionKind {
		return
	}
	for _, alternative := range value.Arguments() {
		visitDiagnosticObjectAlternatives(alternative, visit)
	}
}

func methodExistsGuarded(
	root *phpsyntax.Node,
	reference semantic.Reference,
) bool {
	if root == nil {
		return false
	}
	call := phpquery.CallAt(root.NodeAtOffset(reference.Range.Start))
	if call == nil || call.Kind() != phpsyntax.PhpMemberCall {
		return false
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	receiverKey := normalizedGuardExpression(receiver.Text())
	if receiverKey == "" {
		return false
	}

	for current := call; current != nil && current.Parent() != nil; current = current.Parent() {
		parent := current.Parent()
		if parent.Kind() == phpsyntax.PhpTernaryExpression {
			nodes := directNodes(parent)
			if len(nodes) >= 3 {
				truth := nodes[len(nodes)-2].Range().Contains(reference.Range.Start)
				falsity := nodes[len(nodes)-1].Range().Contains(reference.Range.Start)
				if (truth || falsity) && conditionGuaranteesMethod(
					nodes[0],
					receiverKey,
					reference.Name,
					truth,
				) {
					return true
				}
			}
		}
		if parent.Kind() != phpsyntax.PhpIfStatement {
			continue
		}
		nodes := directNodes(parent)
		if len(nodes) < 2 ||
			!nodes[1].Range().Contains(reference.Range.Start) {
			continue
		}
		if conditionGuaranteesMethod(
			nodes[0],
			receiverKey,
			reference.Name,
			true,
		) {
			return true
		}
	}

	for current := call; current != nil; {
		statement := current
		for statement.Parent() != nil &&
			statement.Parent().Kind() != phpsyntax.PhpBlock {
			statement = statement.Parent()
		}
		block := statement.Parent()
		if block == nil {
			break
		}
		statements := directNodes(block)
		for index, candidate := range statements {
			if candidate != statement {
				continue
			}
			for previous := index - 1; previous >= 0; previous-- {
				guard := statements[previous]
				if statementAssignsExpression(guard, receiverKey) {
					return false
				}
				if guard.Kind() != phpsyntax.PhpIfStatement {
					continue
				}
				nodes := directNodes(guard)
				if len(nodes) == 2 && statementTerminates(nodes[1]) &&
					conditionGuaranteesMethod(
						nodes[0],
						receiverKey,
						reference.Name,
						false,
					) {
					return true
				}
			}
			break
		}
		current = block
	}
	return false
}

func classExistenceGuarded(
	root *phpsyntax.Node,
	document *semantic.Document,
	reference semantic.Reference,
) bool {
	if root == nil || document == nil {
		return false
	}
	names := diagnosticReferenceClassNames(document, reference)
	if len(names) == 0 {
		return false
	}
	node := root.NodeAtOffset(reference.Range.Start)
	call := phpquery.CallAt(node)
	if call != nil && classExistencePredicate(call) {
		argument := phpquery.ArgumentExpression(call, 0)
		if argument != nil && argument.Range().Contains(reference.Range.Start) &&
			guardExpressionMatchesClass(document, argument, names) {
			return true
		}
	}

	for current := node; current != nil && current.Parent() != nil; current = current.Parent() {
		parent := current.Parent()
		if parent.Kind() == phpsyntax.PhpTernaryExpression {
			nodes := directNodes(parent)
			if len(nodes) >= 3 {
				truth := nodes[len(nodes)-2].Range().Contains(reference.Range.Start)
				falsity := nodes[len(nodes)-1].Range().Contains(reference.Range.Start)
				if (truth || falsity) && conditionGuaranteesClass(
					nodes[0],
					document,
					names,
					truth,
				) {
					return true
				}
			}
		}
		if parent.Kind() != phpsyntax.PhpIfStatement {
			continue
		}
		nodes := directNodes(parent)
		if len(nodes) < 2 ||
			!nodes[1].Range().Contains(reference.Range.Start) {
			continue
		}
		if conditionGuaranteesClass(nodes[0], document, names, true) {
			return true
		}
	}

	for current := node; current != nil; {
		statement := current
		for statement.Parent() != nil &&
			statement.Parent().Kind() != phpsyntax.PhpBlock {
			statement = statement.Parent()
		}
		block := statement.Parent()
		if block == nil {
			break
		}
		statements := directNodes(block)
		for index, candidate := range statements {
			if candidate != statement {
				continue
			}
			for previous := index - 1; previous >= 0; previous-- {
				guard := statements[previous]
				if guard.Kind() != phpsyntax.PhpIfStatement {
					continue
				}
				nodes := directNodes(guard)
				if len(nodes) == 2 && statementTerminates(nodes[1]) &&
					conditionGuaranteesClass(
						nodes[0],
						document,
						names,
						false,
					) {
					return true
				}
			}
			break
		}
		current = block
	}
	return false
}

func diagnosticReferenceClassNames(
	document *semantic.Document,
	reference semantic.Reference,
) []string {
	names := make([]string, 0, reference.QualifiedNameCount()+1)
	for index := 0; index < reference.QualifiedNameCount(); index++ {
		if name := normalizeDiagnosticClassName(reference.QualifiedNameAt(index)); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 && reference.Name != "" {
		resolved := nameContextAt(document, reference.Range.Start).ResolveClass(reference.Name)
		if name := normalizeDiagnosticClassName(resolved); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func conditionGuaranteesClass(
	node *phpsyntax.Node,
	document *semantic.Document,
	names []string,
	truth bool,
) bool {
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpParenthesized {
		nodes := directNodes(node)
		return len(nodes) > 0 && conditionGuaranteesClass(
			nodes[0], document, names, truth,
		)
	}
	if node.Kind() == phpsyntax.PhpUnaryExpression &&
		diagnosticDirectOperator(node) == "!" {
		nodes := directNodes(node)
		return len(nodes) > 0 && conditionGuaranteesClass(
			nodes[len(nodes)-1], document, names, !truth,
		)
	}
	if node.Kind() == phpsyntax.PhpBinaryExpression {
		nodes := directNodes(node)
		if len(nodes) >= 2 {
			left := conditionGuaranteesClass(nodes[0], document, names, truth)
			right := conditionGuaranteesClass(
				nodes[len(nodes)-1], document, names, truth,
			)
			switch strings.ToLower(diagnosticDirectOperator(node)) {
			case "&&", "and":
				if truth {
					return left || right
				}
				return left && right
			case "||", "or":
				if truth {
					return left && right
				}
				return left || right
			}
		}
	}
	if !truth || !classExistencePredicate(node) {
		return false
	}
	return guardExpressionMatchesClass(
		document,
		phpquery.ArgumentExpression(node, 0),
		names,
	)
}

func classExistencePredicate(node *phpsyntax.Node) bool {
	if node == nil || node.Kind() != phpsyntax.PhpFunctionCall {
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(phpquery.CallMethodName(node), "\\")) {
	case "class_exists", "interface_exists", "trait_exists", "enum_exists":
		return true
	default:
		return false
	}
}

func guardExpressionMatchesClass(
	document *semantic.Document,
	expression *phpsyntax.Node,
	names []string,
) bool {
	guarded := guardedClassName(document, expression)
	if guarded == "" {
		return false
	}
	for _, name := range names {
		if guarded == name {
			return true
		}
	}
	return false
}

func guardedClassName(
	document *semantic.Document,
	expression *phpsyntax.Node,
) string {
	if expression == nil || document == nil {
		return ""
	}
	if expression.Kind() == phpsyntax.PhpString {
		return normalizeDiagnosticClassName(
			diagnosticPHPStringValue(expression),
		)
	}
	if expression.Kind() != phpsyntax.PhpMemberAccess &&
		expression.Kind() != phpsyntax.PhpScopedAccess {
		return ""
	}
	nodes := directNodes(expression)
	if len(nodes) < 2 ||
		!strings.EqualFold(compactName(nodes[len(nodes)-1].Text()), "class") {
		return ""
	}
	receiver := compactName(nodes[0].Text())
	if receiver == "" {
		return ""
	}
	resolved := nameContextAt(document, nodes[0].Range().Start).ResolveClass(receiver)
	return normalizeDiagnosticClassName(resolved)
}

func normalizeDiagnosticClassName(name string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(name), "\\"))
}

func diagnosticPHPStringValue(node *phpsyntax.Node) string {
	value := phpquery.StringValue(node)
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 {
		return value
	}
	switch text[0] {
	case '\'':
		return strings.NewReplacer(`\\`, `\`, `\'`, `'`).Replace(value)
	case '"':
		return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(value)
	default:
		return value
	}
}

func statementAssignsExpression(node *phpsyntax.Node, expression string) bool {
	if node == nil || expression == "" {
		return false
	}
	assigned := func(candidate *phpsyntax.Node) bool {
		if candidate == nil ||
			candidate.Kind() != phpsyntax.PhpAssignmentExpression {
			return false
		}
		nodes := directNodes(candidate)
		return len(nodes) > 0 &&
			normalizedGuardExpression(nodes[0].Text()) == expression
	}
	if assigned(node) {
		return true
	}
	for element := range node.Descendants() {
		candidate, ok := element.(*phpsyntax.Node)
		if ok && assigned(candidate) {
			return true
		}
	}
	return false
}

func conditionGuaranteesMethod(
	node *phpsyntax.Node,
	receiver,
	method string,
	truth bool,
) bool {
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpParenthesized {
		nodes := directNodes(node)
		if len(nodes) == 0 {
			return false
		}
		return conditionGuaranteesMethod(nodes[0], receiver, method, truth)
	}
	if node.Kind() == phpsyntax.PhpUnaryExpression &&
		diagnosticDirectOperator(node) == "!" {
		nodes := directNodes(node)
		if len(nodes) == 0 {
			return false
		}
		return conditionGuaranteesMethod(nodes[len(nodes)-1], receiver, method, !truth)
	}
	if node.Kind() == phpsyntax.PhpBinaryExpression {
		nodes := directNodes(node)
		if len(nodes) >= 2 {
			left := conditionGuaranteesMethod(
				nodes[0], receiver, method, truth,
			)
			right := conditionGuaranteesMethod(
				nodes[len(nodes)-1], receiver, method, truth,
			)
			switch strings.ToLower(diagnosticDirectOperator(node)) {
			case "&&", "and":
				if truth {
					return left || right
				}
				return left && right
			case "||", "or":
				if truth {
					return left && right
				}
				return left || right
			}
		}
	}
	if !truth || node.Kind() != phpsyntax.PhpFunctionCall ||
		!strings.EqualFold(
			strings.TrimPrefix(phpquery.CallMethodName(node), "\\"),
			"method_exists",
		) {
		return false
	}
	target := phpquery.ArgumentExpression(node, 0)
	name := phpquery.ArgumentExpression(node, 1)
	return target != nil && name != nil &&
		normalizedGuardExpression(target.Text()) == receiver &&
		strings.EqualFold(phpquery.StringValue(name), method)
}

func statementTerminates(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpBlock {
		nodes := directNodes(node)
		if len(nodes) == 0 {
			return false
		}
		return statementTerminates(nodes[len(nodes)-1])
	}
	switch node.Kind() {
	case phpsyntax.PhpReturnStatement,
		phpsyntax.PhpThrowStatement,
		phpsyntax.PhpBreakStatement,
		phpsyntax.PhpContinueStatement:
		return true
	default:
		return false
	}
}

func diagnosticDirectOperator(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	for index := 0; index < node.ChildCount(); index++ {
		token, ok := node.Child(index).(*phpsyntax.Token)
		if !ok || token.Kind().IsTrivia() {
			continue
		}
		switch token.Kind() {
		case phpsyntax.TkOperator, phpsyntax.TkKeyword:
			return token.Text()
		}
	}
	return ""
}

func normalizedGuardExpression(value string) string {
	value = strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return character
		}
	}, value)
	return strings.ReplaceAll(value, "?->", "->")
}

func isDeprecationSuppressedAtReference(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
	symbol semantic.Symbol,
) bool {
	for _, declaration := range document.Symbols {
		if declaration.Flags.Has(semantic.DeprecatedFlag) &&
			declaration.Range.Contains(reference.Range.Start) {
			return true
		}
	}
	scope, found := document.ScopeAt(reference.Range.Start)
	if !found {
		return false
	}
	for {
		if owner, exists := snapshot.Symbol(scope.Owner); exists {
			if owner.Flags.Has(semantic.DeprecatedFlag) ||
				symbol.ID == owner.ID || symbol.Container == owner.ID {
				return true
			}
			if owner.Container != "" {
				if container, exists := snapshot.Symbol(owner.Container); exists &&
					container.Flags.Has(semantic.DeprecatedFlag) {
					return true
				}
			}
		}
		if scope.ID == scope.Parent || int(scope.Parent) >= len(document.Scopes) {
			return false
		}
		scope = document.Scopes[scope.Parent]
	}
}

func referenceCandidates(
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) []semantic.Symbol {
	if snapshot == nil {
		return nil
	}
	candidates := reference.CandidateIDs()
	capacity := len(candidates)
	if reference.Resolved != "" {
		capacity++
	}
	result := make([]semantic.Symbol, 0, capacity)
	appendSymbol := func(id semantic.SymbolID) {
		for _, existing := range result {
			if existing.ID == id {
				return
			}
		}
		symbol, exists := snapshot.Symbol(id)
		if !exists {
			return
		}
		result = append(result, symbol)
	}
	if reference.Resolved != "" {
		appendSymbol(reference.Resolved)
	}
	for _, id := range candidates {
		appendSymbol(id)
	}
	return result
}

func diagnosableMemberReference(
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) bool {
	if snapshot == nil || reference.Receiver.IsUnknown() {
		return false
	}
	var check func(types.Type) bool
	check = func(receiver types.Type) bool {
		switch receiver.Kind() {
		case types.UnionKind, types.IntersectionKind:
			members := receiver.Arguments()
			if len(members) == 0 {
				return false
			}
			for _, member := range members {
				if !check(member) {
					return false
				}
			}
			return true
		case types.ObjectKind:
			if receiver.Name() == "" {
				return false
			}
			classes := snapshot.Classes(receiver.Name())
			if len(classes) == 0 {
				return false
			}
			for _, class := range classes {
				if classHasDynamicMemberFallback(snapshot, class, reference) {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	return check(reference.Receiver)
}

func classHasDynamicMemberFallback(
	snapshot *semantic.Snapshot,
	class semantic.Symbol,
	reference semantic.Reference,
) bool {
	if reference.TargetKind == semantic.PropertySymbol &&
		classAllowsDynamicProperties(snapshot, class, nil) {
		return true
	}
	resolver := resolver.MemberResolver{Snapshot: snapshot}
	receiver := types.Named(class.FullyQualified)
	var names []string
	switch reference.TargetKind {
	case semantic.MethodSymbol:
		if reference.Static {
			names = []string{"__callStatic"}
		} else {
			names = []string{"__call"}
		}
	case semantic.PropertySymbol:
		if reference.Static {
			return false
		}
		if reference.Write {
			names = []string{"__set"}
		} else {
			names = []string{"__get", "__isset"}
		}
	default:
		return false
	}
	for _, name := range names {
		if len(resolver.Methods(receiver, name)) > 0 {
			return true
		}
	}
	return false
}

func classAllowsDynamicProperties(
	snapshot *semantic.Snapshot,
	class semantic.Symbol,
	visited map[semantic.SymbolID]struct{},
) bool {
	if strings.EqualFold(class.FullyQualified, "stdClass") ||
		hasAllowDynamicProperties(class) {
		return true
	}
	if visited == nil {
		visited = make(map[semantic.SymbolID]struct{})
	}
	if _, duplicate := visited[class.ID]; duplicate {
		return false
	}
	visited[class.ID] = struct{}{}
	for _, parent := range class.Extends {
		allowed := false
		snapshot.VisitClassViews(
			parent,
			func(parentView semantic.SymbolView) bool {
				allowed = classAllowsDynamicProperties(
					snapshot,
					parentView.Materialize(),
					visited,
				)
				return !allowed
			},
		)
		if allowed {
			return true
		}
	}
	return false
}

func hasAllowDynamicProperties(class semantic.Symbol) bool {
	for _, attribute := range class.Attributes {
		name := strings.TrimPrefix(attribute.Name, "\\")
		if strings.EqualFold(name, "AllowDynamicProperties") ||
			strings.HasSuffix(
				strings.ToLower(name),
				"\\allowdynamicproperties",
			) {
			return true
		}
	}
	return false
}

func undefinedMemberMessage(reference semantic.Reference) string {
	name := strings.TrimPrefix(reference.Name, "$")
	switch reference.TargetKind {
	case semantic.MethodSymbol:
		return "Undefined method " + name + " on " + reference.Receiver.String()
	case semantic.ClassConstantSymbol:
		return "Undefined class constant " + name + " on " + reference.Receiver.String()
	default:
		return "Undefined property $" + name + " on " + reference.Receiver.String()
	}
}

func isImplicitTraitRequirement(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) bool {
	current, ok := enclosingClass(
		document,
		snapshot,
		reference.Range.Start,
	)
	if !ok || current.Kind != semantic.TraitSymbol {
		return false
	}
	var containsTrait func(types.Type) bool
	containsTrait = func(value types.Type) bool {
		switch value.Kind() {
		case types.ObjectKind:
			return strings.EqualFold(
				strings.TrimPrefix(value.Name(), "\\"),
				strings.TrimPrefix(current.FullyQualified, "\\"),
			)
		case types.UnionKind, types.IntersectionKind:
			for _, member := range value.Arguments() {
				if containsTrait(member) {
					return true
				}
			}
		}
		return false
	}
	return containsTrait(reference.Receiver)
}

func anyMemberAccessible(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	root *phpsyntax.Node,
	reference semantic.Reference,
	candidates []semantic.Symbol,
) bool {
	current, hasCurrent := enclosingClass(
		document,
		snapshot,
		reference.Range.Start,
	)
	boundClasses := closureBindingClasses(
		document,
		snapshot,
		root,
		reference.Range.Start,
	)
	for _, candidate := range candidates {
		visibility := candidate.Visibility
		if reference.Write && candidate.Kind == semantic.PropertySymbol {
			if candidate.Flags.Has(semantic.ReadonlyFlag) &&
				(!hasCurrent || candidate.Container != current.ID) &&
				!containsClassID(boundClasses, candidate.Container) {
				continue
			}
			if candidate.HasWriteVisibility {
				visibility = candidate.WriteVisibility
			}
		}
		if visibility == semantic.Public {
			return true
		}
		if hasCurrent && memberAccessibleFromClass(
			snapshot,
			current,
			candidate,
			visibility,
		) {
			return true
		}
		for _, boundClass := range boundClasses {
			if memberAccessibleFromClass(
				snapshot,
				boundClass,
				candidate,
				visibility,
			) {
				return true
			}
		}
	}
	for _, class := range receiverClasses(snapshot, reference.Receiver) {
		if classHasDynamicMemberFallback(snapshot, class, reference) {
			return true
		}
	}
	return false
}

func memberAccessibleFromClass(
	snapshot *semantic.Snapshot,
	current,
	candidate semantic.Symbol,
	visibility semantic.Visibility,
) bool {
	if candidate.Container == current.ID {
		return true
	}
	container, exists := snapshot.Symbol(candidate.Container)
	if !exists {
		return false
	}
	if container.Kind == semantic.TraitSymbol {
		if visibility == semantic.Private {
			return classDirectlyUsesTrait(
				snapshot,
				current.FullyQualified,
				container.FullyQualified,
				make(map[string]struct{}),
			)
		}
		if classUsesTrait(
			snapshot,
			current.FullyQualified,
			container.FullyQualified,
			make(map[string]struct{}),
		) {
			return true
		}
	}
	return visibility != semantic.Private &&
		snapshot.IsSubtypeOf(
			current.FullyQualified,
			container.FullyQualified,
		)
}

func containsClassID(classes []semantic.Symbol, id semantic.SymbolID) bool {
	for _, class := range classes {
		if class.ID == id {
			return true
		}
	}
	return false
}

func closureBindingClasses(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	root *phpsyntax.Node,
	offset uint32,
) []semantic.Symbol {
	if document == nil || snapshot == nil || root == nil {
		return nil
	}
	node := root.NodeAtOffset(offset)
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != phpsyntax.PhpClosure &&
			current.Kind() != phpsyntax.PhpArrowFunction {
			continue
		}
		call := phpquery.CallAt(current.Parent())
		if call == nil ||
			call.Kind() != phpsyntax.PhpScopedCall ||
			phpquery.ArgumentIndex(call, current) != 0 ||
			!strings.EqualFold(phpquery.CallMethodName(call), "bind") {
			return nil
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || receiver.Kind() != phpsyntax.PhpName ||
			!strings.EqualFold(
				nameContextAt(
					document,
					receiver.Range().Start,
				).ResolveClass(phpquery.NameValue(receiver)),
				"Closure",
			) {
			return nil
		}

		scopeExpression := phpquery.ArgumentExpression(call, 2)
		for index, argument := range phpquery.Arguments(call) {
			if strings.EqualFold(
				phpquery.ArgumentName(argument),
				"newScope",
			) {
				scopeExpression = phpquery.ArgumentExpression(call, index)
				break
			}
		}
		if scopeExpression == nil {
			return nil
		}
		var result []semantic.Symbol
		appendBindingClasses(
			snapshot,
			document.TypeOf(scopeExpression).Type,
			&result,
		)
		return result
	}
	return nil
}

func appendBindingClasses(
	snapshot *semantic.Snapshot,
	value types.Type,
	result *[]semantic.Symbol,
) {
	switch value.Kind() {
	case types.ClassStringKind:
		if value.ArgumentCount() > 0 {
			appendBindingClasses(snapshot, value.Argument(0), result)
		}
	case types.ObjectKind:
		if value.Name() == "" {
			return
		}
		snapshot.VisitClassViews(
			value.Name(),
			func(class semantic.SymbolView) bool {
				id := class.ID()
				for _, existing := range *result {
					if existing.ID == id {
						return true
					}
				}
				*result = append(*result, class.Materialize())
				return true
			},
		)
	case types.UnionKind, types.IntersectionKind:
		for _, member := range value.Arguments() {
			appendBindingClasses(snapshot, member, result)
		}
	}
}

func receiverClasses(
	snapshot *semantic.Snapshot,
	receiver types.Type,
) []semantic.Symbol {
	switch receiver.Kind() {
	case types.ObjectKind:
		return snapshot.Classes(receiver.Name())
	case types.UnionKind, types.IntersectionKind:
		var result []semantic.Symbol
		for _, member := range receiver.Arguments() {
			result = append(result, receiverClasses(snapshot, member)...)
		}
		return result
	default:
		return nil
	}
}

func classUsesTrait(
	snapshot *semantic.Snapshot,
	className,
	traitName string,
	visited map[string]struct{},
) bool {
	if snapshot == nil || className == "" || traitName == "" {
		return false
	}
	key := strings.ToLower(strings.TrimPrefix(className, "\\"))
	if _, seen := visited[key]; seen {
		return false
	}
	visited[key] = struct{}{}
	found := false
	snapshot.VisitClassViews(className, func(view semantic.SymbolView) bool {
		class := view.Materialize()
		for _, used := range class.Traits {
			if strings.EqualFold(
				strings.TrimPrefix(used, "\\"),
				strings.TrimPrefix(traitName, "\\"),
			) || classUsesTrait(snapshot, used, traitName, visited) {
				found = true
				return false
			}
		}
		for _, parent := range class.Extends {
			if classUsesTrait(snapshot, parent, traitName, visited) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func classDirectlyUsesTrait(
	snapshot *semantic.Snapshot,
	className,
	traitName string,
	visited map[string]struct{},
) bool {
	if snapshot == nil || className == "" || traitName == "" {
		return false
	}
	key := strings.ToLower(strings.TrimPrefix(className, "\\"))
	if _, seen := visited[key]; seen {
		return false
	}
	visited[key] = struct{}{}
	found := false
	snapshot.VisitClassViews(className, func(view semantic.SymbolView) bool {
		class := view.Materialize()
		for _, used := range class.Traits {
			if strings.EqualFold(
				strings.TrimPrefix(used, "\\"),
				strings.TrimPrefix(traitName, "\\"),
			) || classDirectlyUsesTrait(
				snapshot,
				used,
				traitName,
				visited,
			) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func enclosingClass(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	offset uint32,
) (semantic.Symbol, bool) {
	scope, ok := document.ScopeAt(offset)
	if !ok {
		return semantic.Symbol{}, false
	}
	for {
		if owner, exists := snapshot.Symbol(scope.Owner); exists {
			if owner.IsClassLike() {
				return owner, true
			}
			if owner.Container != "" {
				if container, found := snapshot.Symbol(owner.Container); found &&
					container.IsClassLike() {
					return container, true
				}
			}
		}
		if scope.ID == scope.Parent || int(scope.Parent) >= len(document.Scopes) {
			return semantic.Symbol{}, false
		}
		scope = document.Scopes[scope.Parent]
	}
}

func inaccessibleMemberMessage(
	reference semantic.Reference,
	symbol semantic.Symbol,
) string {
	visibility := symbol.Visibility
	if reference.Write && symbol.Kind == semantic.PropertySymbol &&
		symbol.HasWriteVisibility {
		visibility = symbol.WriteVisibility
	}
	return "Cannot access " + visibilityName(visibility) + " member " +
		strings.TrimPrefix(reference.Name, "$")
}
