package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	contextMetadataStateCode lsp.DiagnosticID = "shopware.migration.context_metadata.state"
	fakerPropertyCallCode    lsp.DiagnosticID = "shopware.migration.faker.property_call"
	productStreamBuilderCode lsp.DiagnosticID = "shopware.migration.product_stream.enrich_criteria"

	shopwareContextClass               = "Shopware\\Core\\Framework\\Context"
	fakerGeneratorClass                = "Faker\\Generator"
	productStreamBuilderInterfaceClass = "Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface"
	productStreamMigrationTODO         = "// TODO: Replace buildFilters() call with AbstractProductStreamBuilder::enrichCriteria() - please check manually"
)

func (p *ShopwareMigrationAnalyzer) contextMetadataProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	var result []lsp.Problem
	for _, call := range phpquery.Nodes(root, phpsyntax.PhpMemberCall) {
		if ctx.Err() != nil {
			return result
		}
		if !strings.EqualFold(phpquery.CallMethodName(call), "addExtension") {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || !phpTypeIsSubtype(document.TypeOf(receiver).Type, snapshot, shopwareContextClass) {
			continue
		}
		constant := phpArgumentExpression(phpquery.Argument(call, 0))
		if constant == nil || constant.Kind() != phpsyntax.PhpMemberAccess {
			continue
		}
		constantName := phpMemberTargetName(constant)
		if !strings.EqualFold(constantName, "USE_INDEXING_QUEUE") &&
			!strings.EqualFold(constantName, "DISABLE_INDEXING") {
			continue
		}
		arguments := phpquery.Arguments(call)
		safe := len(arguments) == 2
		name := callTargetName(call)
		rng := call.RangeTrimmedTrivia()
		if name != nil {
			rng = name.RangeTrimmedTrivia()
		}
		result = append(result, lsp.Problem{
			ID:       contextMetadataStateCode,
			Range:    rng,
			Element:  call,
			Message:  "Shopware 6.5: store indexing flags as Context state instead of extension metadata",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule: "context-metadata-state",
				Kind: "method",
				Safe: safe,
			},
		})
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) fakerPropertyProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	var result []lsp.Problem
	for _, access := range phpquery.Nodes(root, phpsyntax.PhpMemberAccess) {
		if ctx.Err() != nil {
			return result
		}
		receiver := phpMemberReceiver(access)
		name := phpMemberTarget(access)
		if receiver == nil || name == nil ||
			!phpTypeIsSubtype(document.TypeOf(receiver).Type, snapshot, fakerGeneratorClass) {
			continue
		}
		result = append(result, lsp.Problem{
			ID:       fakerPropertyCallCode,
			Range:    name.RangeTrimmedTrivia(),
			Element:  access,
			Message:  "Shopware 6.5: call Faker formatter " + strings.TrimSpace(name.Text()) + "() as a method",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule: "faker-property-call",
				Kind: "member",
				Safe: !phpMemberIsWriteTarget(access),
			},
		})
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) productStreamBuilderProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	source string,
) []lsp.Problem {
	var result []lsp.Problem
	for _, call := range phpquery.Nodes(root, phpsyntax.PhpMemberCall) {
		if ctx.Err() != nil {
			return result
		}
		if !strings.EqualFold(phpquery.CallMethodName(call), "buildFilters") {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || !phpTypeIsSubtype(
			document.TypeOf(receiver).Type,
			snapshot,
			productStreamBuilderInterfaceClass,
		) {
			continue
		}
		payload := ShopwareMigrationPayload{
			Rule: "product-stream-enrich-criteria",
			Kind: "manual",
		}
		if len(phpquery.Arguments(call)) == 2 {
			if start, end, replacement, ok := productStreamAssignmentRewrite(call); ok {
				payload.Kind = "assignment"
				payload.Safe = true
				payload.Start = start
				payload.End = end
				payload.Replacement = replacement
			} else if start, end, replacement, ok := productStreamInlineRewrite(call); ok {
				payload.Kind = "inline"
				payload.Safe = true
				payload.Start = start
				payload.End = end
				payload.Replacement = replacement
			} else if start, replacement, ok := productStreamManualRewrite(call, source); ok {
				payload.Safe = true
				payload.Start = start
				payload.Replacement = replacement
			}
		}
		name := callTargetName(call)
		rng := call.RangeTrimmedTrivia()
		if name != nil {
			rng = name.RangeTrimmedTrivia()
		}
		message := "Shopware 6.8: replace ProductStreamBuilder::buildFilters() with enrichCriteria()"
		if payload.Kind == "manual" {
			message += " (manual migration required)"
		}
		result = append(result, lsp.Problem{
			ID:       productStreamBuilderCode,
			Range:    rng,
			Element:  call,
			Message:  message,
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload:  payload,
		})
	}
	return result
}

func productStreamManualRewrite(call *phpsyntax.Node, source string) (uint32, string, bool) {
	statement := nearestPHPStatement(call)
	if statement == nil {
		return 0, "", false
	}
	start := statement.RangeTrimmedTrivia().Start
	if start > uint32(len(source)) || productStreamTODOImmediatelyPrecedes(source, start) {
		return 0, "", false
	}
	lineStart := strings.LastIndex(source[:start], "\n") + 1
	indent := source[lineStart:start]
	if strings.TrimSpace(indent) != "" {
		indent = ""
	}
	return start, productStreamMigrationTODO + "\n" + indent, true
}

func productStreamTODOImmediatelyPrecedes(source string, start uint32) bool {
	if start > uint32(len(source)) {
		return false
	}
	lineStart := strings.LastIndex(source[:start], "\n") + 1
	if lineStart == 0 {
		return false
	}
	previousEnd := lineStart - 1
	previousStart := strings.LastIndex(source[:previousEnd], "\n") + 1
	return strings.TrimSpace(source[previousStart:previousEnd]) == productStreamMigrationTODO
}

func nearestPHPStatement(node *phpsyntax.Node) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpReturnStatement,
			phpsyntax.PhpIfStatement,
			phpsyntax.PhpSwitchStatement,
			phpsyntax.PhpWhileStatement,
			phpsyntax.PhpDoWhileStatement,
			phpsyntax.PhpForStatement,
			phpsyntax.PhpForeachStatement,
			phpsyntax.PhpTryStatement,
			phpsyntax.PhpThrowStatement,
			phpsyntax.PhpBreakStatement,
			phpsyntax.PhpContinueStatement,
			phpsyntax.PhpEchoStatement,
			phpsyntax.PhpGlobalStatement,
			phpsyntax.PhpStaticStatement,
			phpsyntax.PhpExpressionStatement:
			return current
		}
	}
	return nil
}

func productStreamAssignmentRewrite(call *phpsyntax.Node) (uint32, uint32, string, bool) {
	assignment := ancestorPHPNode(call, phpsyntax.PhpAssignmentExpression)
	statement := ancestorPHPNode(call, phpsyntax.PhpExpressionStatement)
	if assignment == nil || statement == nil || call.Parent() != assignment {
		return 0, 0, "", false
	}
	filtersVariable := phpquery.AssignedVariable(statement)
	if filtersVariable == "" {
		return 0, 0, "", false
	}
	next := nextPHPStatement(statement)
	addFilter := directPHPExpression(next)
	if addFilter == nil || addFilter.Kind() != phpsyntax.PhpMemberCall ||
		!strings.EqualFold(phpquery.CallMethodName(addFilter), "addFilter") ||
		len(phpquery.Arguments(addFilter)) != 1 {
		return 0, 0, "", false
	}
	argument := phpquery.Argument(addFilter, 0)
	filterExpression := phpArgumentExpression(argument)
	if argument == nil || argument.ChildTokenOfKind(phpsyntax.TkEllipsis) == nil ||
		filterExpression == nil || filterExpression.Kind() != phpsyntax.PhpVariable ||
		!strings.EqualFold(strings.TrimSpace(filterExpression.Text()), filtersVariable) {
		return 0, 0, "", false
	}
	replacement, ok := productStreamEnrichCriteriaCall(call, phpquery.CallReceiver(addFilter))
	if !ok {
		return 0, 0, "", false
	}
	firstRange := statement.RangeTrimmedTrivia()
	secondRange := next.RangeTrimmedTrivia()
	return firstRange.Start, secondRange.End, replacement + ";", true
}

func productStreamInlineRewrite(call *phpsyntax.Node) (uint32, uint32, string, bool) {
	argument := call.Parent()
	if argument == nil || argument.Kind() != phpsyntax.PhpArgument ||
		argument.ChildTokenOfKind(phpsyntax.TkEllipsis) == nil ||
		phpArgumentExpression(argument) != call {
		return 0, 0, "", false
	}
	for current := argument.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == phpsyntax.PhpExpressionStatement {
			break
		}
		if current.Kind() != phpsyntax.PhpMemberCall ||
			!strings.EqualFold(phpquery.CallMethodName(current), "addFilter") ||
			len(phpquery.Arguments(current)) != 1 {
			continue
		}
		replacement, ok := productStreamEnrichCriteriaCall(call, phpquery.CallReceiver(current))
		if !ok {
			return 0, 0, "", false
		}
		rng := current.RangeTrimmedTrivia()
		return rng.Start, rng.End, replacement, true
	}
	return 0, 0, "", false
}

func productStreamEnrichCriteriaCall(
	buildFilters *phpsyntax.Node,
	criteria *phpsyntax.Node,
) (string, bool) {
	builder := phpquery.CallReceiver(buildFilters)
	streamID := phpArgumentExpression(phpquery.Argument(buildFilters, 0))
	shopwareContext := phpArgumentExpression(phpquery.Argument(buildFilters, 1))
	if builder == nil || criteria == nil || streamID == nil || shopwareContext == nil {
		return "", false
	}
	return strings.TrimSpace(builder.Text()) + "->enrichCriteria(" +
		strings.TrimSpace(criteria.Text()) + ", " +
		strings.TrimSpace(streamID.Text()) + ", " +
		strings.TrimSpace(shopwareContext.Text()) + ")", true
}

func phpTypeIsSubtype(
	value types.Type,
	snapshot *semantic.Snapshot,
	target string,
) bool {
	if snapshot == nil {
		return false
	}
	if value.Kind() == types.ObjectKind && value.Name() != "" {
		return snapshot.IsSubtypeOf(value.Name(), target)
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		if phpTypeIsSubtype(value.Argument(index), snapshot, target) {
			return true
		}
	}
	return false
}

func phpMemberReceiver(member *phpsyntax.Node) *phpsyntax.Node {
	if member == nil {
		return nil
	}
	for index := 0; index < member.ChildCount(); index++ {
		if child, ok := member.Child(index).(*phpsyntax.Node); ok {
			return child
		}
	}
	return nil
}

func phpMemberTarget(member *phpsyntax.Node) *phpsyntax.Node {
	if member == nil {
		return nil
	}
	var target *phpsyntax.Node
	for index := 0; index < member.ChildCount(); index++ {
		if child, ok := member.Child(index).(*phpsyntax.Node); ok {
			target = child
		}
	}
	return target
}

func phpMemberTargetName(member *phpsyntax.Node) string {
	target := phpMemberTarget(member)
	if target == nil {
		return ""
	}
	return strings.TrimSpace(target.Text())
}

func phpMemberIsWriteTarget(member *phpsyntax.Node) bool {
	if member == nil || member.Parent() == nil {
		return false
	}
	parent := member.Parent()
	switch parent.Kind() {
	case phpsyntax.PhpAssignmentExpression:
		return directPHPExpression(parent) == member
	case phpsyntax.PhpUnaryExpression:
		return strings.Contains(parent.Text(), "++") || strings.Contains(parent.Text(), "--")
	default:
		return false
	}
}

func nextPHPStatement(statement *phpsyntax.Node) *phpsyntax.Node {
	if statement == nil || statement.Parent() == nil {
		return nil
	}
	found := false
	for index := 0; index < statement.Parent().ChildCount(); index++ {
		child, ok := statement.Parent().Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if found {
			return child
		}
		if child == statement {
			found = true
		}
	}
	return nil
}

func directPHPExpression(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		if child, ok := node.Child(index).(*phpsyntax.Node); ok {
			return child
		}
	}
	return nil
}
