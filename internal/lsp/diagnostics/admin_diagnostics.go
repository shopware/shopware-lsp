package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminAnalyzer provides diagnostics for Shopware Admin Vue components
type AdminAnalyzer struct {
	adminIndexer                   *admin.AdminComponentIndexer
	suppressedComponentDiagnostics map[string]struct{}
}

type adminTwigDiagnosticDocument struct {
	templatePath        string
	liveOwner           *admin.VueComponent
	rootIdentifiers     []admin.TwigVueMember
	localIdentifiers    map[cst.TextRange]bool
	memberAccesses      []admin.TwigVueMemberAccess
	registryReferences  []admin.AdminTwigRegistryReference
	directiveReferences []admin.VueDirectiveReference
	components          map[string]adminTwigComponentResolution
}

type adminTwigComponentResolution struct {
	component *admin.VueComponent
	found     bool
	err       error
}

// NewAdminAnalyzer creates a new admin diagnostics provider
func NewAdminAnalyzer(adminIndexer *admin.AdminComponentIndexer) *AdminAnalyzer {
	return &AdminAnalyzer{adminIndexer: adminIndexer}
}

// SuppressComponentDiagnostics lets a more specific, version-aware migration
// diagnostic own selected component tags without producing duplicate generic
// deprecation or not-found problems on the same source range.
func (p *AdminAnalyzer) SuppressComponentDiagnostics(names ...string) *AdminAnalyzer {
	if p == nil {
		return p
	}
	if p.suppressedComponentDiagnostics == nil {
		p.suppressedComponentDiagnostics = make(map[string]struct{}, len(names))
	}
	for _, name := range names {
		p.suppressedComponentDiagnostics[name] = struct{}{}
	}
	return p
}

// Analyze returns diagnostics for admin component files.
func (p *AdminAnalyzer) Analyze(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	uri := document.URI
	// Only process files in administration directory
	if !strings.Contains(uri, "Resources/app/administration") {
		return []lsp.Problem{}, nil
	}

	ext := strings.ToLower(filepath.Ext(uri))

	// Handle JS/TS files
	if ext == ".js" || ext == ".ts" {
		return p.jsDiagnostics(ctx, document)
	}

	// Handle Twig files
	if ext == ".twig" {
		return p.twigDiagnostics(ctx, document)
	}
	if ext == ".vue" {
		javascriptDiagnostics, err := p.jsDiagnostics(ctx, document)
		if err != nil {
			return nil, err
		}
		templateDiagnostics, err := p.twigDiagnostics(ctx, document)
		if err != nil {
			return nil, err
		}
		return append(javascriptDiagnostics, templateDiagnostics...), nil
	}

	return []lsp.Problem{}, nil
}

func (p *AdminAnalyzer) jsDiagnostics(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	var diagnostics []lsp.Problem
	root := document.SyntaxTree.Root
	analysis := admin.NewJavaScriptDocumentAnalysis(root)

	// Find all Component.extend calls
	extendCalls := analysis.Calls("Component.extend", "Shopware.Component.extend")

	for _, callNode := range extendCalls {
		if ctx.Err() != nil {
			return nil, nil
		}
		parentNameNode := jsquery.StringArgument(callNode, 1)
		if parentNameNode == nil {
			continue
		}

		parentName := jsquery.StringValue(parentNameNode)
		if parentName == "" {
			continue
		}

		// Check if parent component exists
		components, err := p.adminIndexer.GetComponent(parentName)
		if err != nil || len(components) == 0 {
			stringRange := parentNameNode.RangeTrimmedTrivia()
			diagnostics = append(diagnostics, lsp.Problem{
				Range:    stringRange,
				Message:  fmt.Sprintf("Parent component '%s' is not registered", parentName),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.parent-not-found",
				Payload: map[string]any{
					"componentName": parentName,
				},
			})
		}
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	diagnostics = append(
		diagnostics,
		p.unknownInstanceMemberDiagnostics(document, analysis)...,
	)
	diagnostics = append(
		diagnostics,
		p.deprecatedInstanceMemberDiagnostics(document, analysis)...,
	)
	containerDiagnostics, err := p.applicationContainerDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, containerDiagnostics...)
	contextDiagnostics, err := p.shopwareContextDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, contextDiagnostics...)
	utilsDiagnostics, err := p.shopwareUtilsDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, utilsDiagnostics...)
	eventBusDiagnostics, err := p.shopwareEventBusDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, eventBusDiagnostics...)
	routeDiagnostics, err := p.javaScriptModuleRouteDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, routeDiagnostics...)
	registryDiagnostics, err := p.javaScriptRegistryDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, registryDiagnostics...)

	return diagnostics, nil
}

func (p *AdminAnalyzer) shopwareEventBusDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, nil
	}
	var diagnostics []lsp.Problem
	for call := range analysis.IterateCalls() {
		eventNode := jsquery.StringArgument(call, 0)
		operation, eventName, matched :=
			analysis.ShopwareEventBusEventAt(eventNode)
		if !matched || eventName == "" {
			continue
		}
		event, found, resolveErr :=
			p.adminIndexer.ResolveShopwareEventBusEvent(eventName, path)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !found {
			// The core EventBus map is open to extensions and legacy events.
			continue
		}
		secondArgument := jsquery.Argument(call, 1)
		switch operation {
		case "emit":
			if secondArgument == nil {
				if !admin.VueTypeAllowsUndefined(event.Type) {
					diagnostics = append(diagnostics, lsp.Problem{
						Range: javaScriptStringContentRange(
							eventNode, document.Text,
						),
						Message: fmt.Sprintf(
							"EventBus event '%s' requires a payload of type %s",
							eventName, event.Type,
						),
						Source: "shopware", Severity: protocol.DiagnosticSeverityWarning,
						ID: "admin.event-bus.missing-payload",
						Payload: map[string]any{
							"eventName": eventName, "expectedType": event.Type,
						},
					})
				}
				continue
			}
			diagnostic, diagnosticErr := p.eventBusArgumentDiagnostic(
				path, eventName, event.Type, secondArgument, "emit",
			)
			if diagnosticErr != nil {
				return nil, diagnosticErr
			}
			if diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
			}
		case "on":
			if secondArgument == nil {
				diagnostics = append(diagnostics, lsp.Problem{
					Range: javaScriptStringContentRange(eventNode, document.Text),
					Message: fmt.Sprintf(
						"EventBus.on('%s') requires an event handler", eventName,
					),
					Source: "shopware", Severity: protocol.DiagnosticSeverityWarning,
					ID:      "admin.event-bus.missing-handler",
					Payload: map[string]any{"eventName": eventName},
				})
				continue
			}
			diagnostic, diagnosticErr := p.eventBusArgumentDiagnostic(
				path, eventName, "Function", secondArgument, "on",
			)
			if diagnosticErr != nil {
				return nil, diagnosticErr
			}
			if diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
			}
		case "off":
			if secondArgument == nil {
				continue
			}
			diagnostic, diagnosticErr := p.eventBusArgumentDiagnostic(
				path, eventName, "Function", secondArgument, "off",
			)
			if diagnosticErr != nil {
				return nil, diagnosticErr
			}
			if diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
			}
		}
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) eventBusArgumentDiagnostic(
	path,
	eventName,
	expectedType string,
	argument *jssyntax.Node,
	operation string,
) (*lsp.Problem, error) {
	if argument == nil {
		return nil, nil
	}
	cursor := argument.ChildNodeCursor()
	if !cursor.Next() {
		return nil, nil
	}
	expression := cursor.Node()
	actualType, found, err := p.adminIndexer.ResolveJavaScriptExpressionType(
		expression.Text(), path,
	)
	if err != nil || !found ||
		!admin.VueTypesProvablyIncompatible(expectedType, actualType) {
		return nil, err
	}
	id := lsp.DiagnosticID("admin.event-bus.payload-type")
	message := fmt.Sprintf(
		"EventBus event '%s' expects payload %s, but the argument has type %s",
		eventName, expectedType, actualType,
	)
	if operation == "on" || operation == "off" {
		id = lsp.DiagnosticID("admin.event-bus.handler-type")
		message = fmt.Sprintf(
			"EventBus.%s('%s') expects a function handler, but the argument has type %s",
			operation, eventName, actualType,
		)
	}
	return &lsp.Problem{
		Range: expression.RangeTrimmedTrivia(), Message: message,
		Source: "shopware", Severity: protocol.DiagnosticSeverityWarning,
		ID: id,
		Payload: map[string]any{
			"eventName": eventName, "expectedType": expectedType,
			"actualType": actualType,
		},
	}, nil
}

func (p *AdminAnalyzer) shopwareUtilsDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, nil
	}
	type utilityContract struct {
		complete bool
		known    map[string]bool
		names    []string
	}
	contracts := make(map[string]utilityContract)
	var diagnostics []lsp.Problem
	seen := make(map[string]bool)
	for _, expression := range analysis.Nodes(jssyntax.JsMemberExpression) {
		receiver, memberName, matched :=
			analysis.ShopwareUtilsMember(expression)
		if !matched || memberName == "" {
			continue
		}
		receiverPath := strings.Join(receiver, ".")
		contract, resolved := contracts[receiverPath]
		if !resolved {
			shape, resolveErr := p.adminIndexer.ResolveShopwareUtils(
				receiverPath, path,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			contract = utilityContract{
				complete: shape.Complete,
				known:    make(map[string]bool, len(shape.Members)),
				names:    make([]string, 0, len(shape.Members)),
			}
			for _, member := range shape.Members {
				contract.known[member.Name] = true
				contract.names = append(contract.names, member.Name)
			}
			contracts[receiverPath] = contract
		}
		if !contract.complete || contract.known[memberName] {
			continue
		}
		suggestions := adminNearbySuggestions(memberName, contract.names)
		if len(suggestions) == 0 {
			continue
		}
		nameNode := analysis.ShopwareUtilsMemberNameNode(expression)
		if nameNode == nil {
			continue
		}
		key := nameNode.RangeTrimmedTrivia().String()
		if seen[key] {
			continue
		}
		seen[key] = true
		qualifiedReceiver := "Shopware.Utils"
		if receiverPath != "" {
			qualifiedReceiver += "." + receiverPath
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range: nameNode.RangeTrimmedTrivia(),
			Message: fmt.Sprintf(
				"Unknown member '%s' on %s", memberName, qualifiedReceiver,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.shopware-utils.unknown-member",
			Payload: map[string]any{
				"receiver": receiverPath, "memberName": memberName,
				"suggestions": suggestions,
			},
		})
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) shopwareContextDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, nil
	}
	type contextContract struct {
		complete bool
		known    map[string]bool
		names    []string
	}
	contracts := make(map[string]contextContract)
	var diagnostics []lsp.Problem
	seen := make(map[string]bool)
	for _, expression := range analysis.Nodes(jssyntax.JsMemberExpression) {
		receiver, memberName, matched :=
			admin.JavaScriptShopwareContextMember(expression)
		if !matched || memberName == "" {
			continue
		}
		receiverPath := strings.Join(receiver, ".")
		contract, resolved := contracts[receiverPath]
		if !resolved {
			shape, resolveErr := p.adminIndexer.ResolveShopwareContext(
				receiverPath, path,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			contract = contextContract{
				complete: shape.Complete,
				known:    make(map[string]bool, len(shape.Members)),
				names:    make([]string, 0, len(shape.Members)),
			}
			for _, member := range shape.Members {
				contract.known[member.Name] = true
				contract.names = append(contract.names, member.Name)
			}
			contracts[receiverPath] = contract
		}
		if !contract.complete || contract.known[memberName] {
			continue
		}
		suggestions := adminNearbySuggestions(memberName, contract.names)
		if len(suggestions) == 0 {
			continue
		}
		nameNode := admin.JavaScriptShopwareContextMemberNameNode(expression)
		if nameNode == nil {
			continue
		}
		key := nameNode.RangeTrimmedTrivia().String()
		if seen[key] {
			continue
		}
		seen[key] = true
		diagnostics = append(diagnostics, lsp.Problem{
			Range: nameNode.RangeTrimmedTrivia(),
			Message: fmt.Sprintf(
				"Unknown member '%s' on Shopware.Context.%s",
				memberName, receiverPath,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.shopware-context.unknown-member",
			Payload: map[string]any{
				"receiver":    receiverPath,
				"memberName":  memberName,
				"suggestions": suggestions,
			},
		})
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) applicationContainerDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, nil
	}
	type containerContract struct {
		complete bool
		known    map[string]bool
		names    []string
	}
	contracts := make(map[string]containerContract)
	var diagnostics []lsp.Problem
	seen := make(map[string]bool)
	for _, expression := range analysis.Nodes(jssyntax.JsMemberExpression) {
		containerName, memberName, matched :=
			analysis.ApplicationContainerMember(expression)
		if !matched || memberName == "" {
			continue
		}
		contract, resolved := contracts[containerName]
		if !resolved {
			shape, resolveErr := p.adminIndexer.ResolveApplicationContainer(
				containerName, path,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			contract = containerContract{
				complete: shape.Complete,
				known:    make(map[string]bool, len(shape.Members)),
				names:    make([]string, 0, len(shape.Members)),
			}
			for _, member := range shape.Members {
				contract.known[member.Name] = true
				contract.names = append(contract.names, member.Name)
			}
			contracts[containerName] = contract
		}
		if !contract.complete || contract.known[memberName] {
			continue
		}
		suggestions := adminNearbySuggestions(memberName, contract.names)
		if len(suggestions) == 0 {
			continue
		}
		nameNode := analysis.ApplicationContainerMemberNameNode(expression)
		if nameNode == nil {
			continue
		}
		key := nameNode.RangeTrimmedTrivia().String()
		if seen[key] {
			continue
		}
		seen[key] = true
		diagnostics = append(diagnostics, lsp.Problem{
			Range: nameNode.RangeTrimmedTrivia(),
			Message: fmt.Sprintf(
				"Unknown member '%s' on Application '%s' container",
				memberName, containerName,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.application-container.unknown-member",
			Payload: map[string]any{
				"containerName": containerName,
				"memberName":    memberName,
				"suggestions":   suggestions,
			},
		})
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) unknownInstanceMemberDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) []lsp.Problem {
	if p.adminIndexer == nil || document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(path)
	if err != nil || len(components) == 0 {
		return nil
	}
	scopes := adminInstanceMemberScopes(
		document.SyntaxTree.Root,
		document.LineIndex,
		path,
		analysis,
		components,
	)
	if len(scopes) == 0 {
		return nil
	}

	var diagnostics []lsp.Problem
	seen := make(map[string]bool)
	for _, memberExpression := range analysis.Nodes(jssyntax.JsMemberExpression) {
		name, matched := jsquery.ThisMember(memberExpression)
		if !matched || name == "" || strings.HasPrefix(name, "$") {
			continue
		}
		scope := smallestAdminInstanceMemberScope(memberExpression, scopes)
		if scope == nil || scope.open || scope.known[name] {
			continue
		}
		nameNode := jsquery.ThisMemberNameNode(memberExpression)
		if nameNode == nil {
			continue
		}
		key := nameNode.RangeTrimmedTrivia().String()
		if seen[key] {
			continue
		}
		seen[key] = true
		diagnostics = append(diagnostics, lsp.Problem{
			Range: nameNode.RangeTrimmedTrivia(),
			Message: fmt.Sprintf(
				"Unknown Administration component instance member '%s'",
				name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-instance-member",
			Payload: map[string]any{
				"memberName": name,
				"suggestions": adminNearbySuggestions(
					name, scope.knownNames,
				),
			},
		})
	}
	return diagnostics
}

type adminInstanceMemberScope struct {
	object     *jssyntax.Node
	components []admin.VueComponent
	current    *admin.ComponentDefinition
	known      map[string]bool
	knownNames []string
	open       bool
}

func adminInstanceMemberScopes(
	root *jssyntax.Node,
	lineIndex *cst.LineIndex,
	filePath string,
	analysis *admin.JavaScriptDocumentAnalysis,
	components []admin.VueComponent,
) []adminInstanceMemberScope {
	byName := make(map[string][]admin.VueComponent, len(components))
	for _, component := range components {
		byName[component.Name] = append(byName[component.Name], component)
	}

	var scopes []adminInstanceMemberScope
	seen := make(map[string]bool)
	add := func(object *jssyntax.Node, effective []admin.VueComponent) {
		if object == nil {
			return
		}
		key := object.RangeTrimmedTrivia().String()
		if seen[key] {
			return
		}
		current := admin.ParseComponentObject(object, filePath, lineIndex)
		if current == nil {
			return
		}
		seen[key] = true
		scope := adminInstanceMemberScope{
			object: object, components: effective, current: current,
		}
		populateAdminInstanceMemberScope(&scope)
		scopes = append(scopes, scope)
	}

	for call := range analysis.IterateCalls(
		"Component.register",
		"Shopware.Component.register",
		"Component.extend",
		"Shopware.Component.extend",
		"Component.override",
		"Shopware.Component.override",
	) {
		definitionIndex := 1
		if strings.HasSuffix(jsquery.CallName(call), ".extend") {
			definitionIndex = 2
		}
		object := admin.ComponentDefinitionObject(
			jsquery.ArgumentExpression(call, definitionIndex),
		)
		name := jsquery.StringValue(jsquery.StringArgument(call, 0))
		add(object, byName[name])
	}
	for _, export := range jsquery.ExportDefaults(root) {
		object := admin.ComponentDefinitionObject(
			jsquery.ExportDefaultExpression(export),
		)
		add(object, components)
	}
	return scopes
}

func populateAdminInstanceMemberScope(scope *adminInstanceMemberScope) {
	if scope == nil {
		return
	}
	scope.known = make(map[string]bool)
	add := func(name string) {
		if name == "" || scope.known[name] {
			return
		}
		scope.known[name] = true
		scope.knownNames = append(scope.knownNames, name)
	}
	for _, member := range admin.VueBuiltinMembers() {
		add(member.Name)
	}
	if scope.current != nil {
		scope.open = scope.current.OpenRuntimeMembers
		for _, member := range scope.current.Members {
			add(member.Name)
		}
		for _, assignment := range scope.current.Assignments {
			add(assignment.Target)
		}
	}
	if len(scope.components) == 0 {
		return
	}

	common := adminComponentInstanceMemberNames(scope.components[0])
	for _, component := range scope.components {
		scope.open = scope.open || component.OpenRuntimeMembers
	}
	for _, component := range scope.components[1:] {
		names := adminComponentInstanceMemberNames(component)
		for name := range common {
			if !names[name] {
				delete(common, name)
			}
		}
	}
	for _, member := range scope.components[0].TemplateMembers() {
		if common[member.Name] {
			add(member.Name)
		}
	}
	for _, assignment := range scope.components[0].Assignments {
		if common[assignment.Target] {
			add(assignment.Target)
		}
	}
}

func adminComponentInstanceMemberNames(
	component admin.VueComponent,
) map[string]bool {
	result := make(map[string]bool)
	for _, member := range component.TemplateMembers() {
		if member.Name != "" {
			result[member.Name] = true
		}
	}
	for _, assignment := range component.Assignments {
		if assignment.Target != "" {
			result[assignment.Target] = true
		}
	}
	return result
}

func smallestAdminInstanceMemberScope(
	node *jssyntax.Node,
	scopes []adminInstanceMemberScope,
) *adminInstanceMemberScope {
	if node == nil {
		return nil
	}
	nodeRange := node.RangeTrimmedTrivia()
	var result *adminInstanceMemberScope
	for index := range scopes {
		scopeRange := scopes[index].object.RangeTrimmedTrivia()
		if nodeRange.Start < scopeRange.Start || nodeRange.End > scopeRange.End {
			continue
		}
		if result == nil {
			result = &scopes[index]
			continue
		}
		resultRange := result.object.RangeTrimmedTrivia()
		if scopeRange.End-scopeRange.Start < resultRange.End-resultRange.Start {
			result = &scopes[index]
		}
	}
	return result
}

// twigDiagnostics checks Twig templates for missing required props on components
