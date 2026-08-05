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
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminAnalyzer provides diagnostics for Shopware Admin Vue components
type AdminAnalyzer struct {
	adminIndexer *admin.AdminComponentIndexer
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
	for _, call := range analysis.Calls() {
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
		arguments := jsquery.Arguments(call)
		switch operation {
		case "emit":
			if len(arguments) < 2 {
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
				path, eventName, event.Type, arguments[1], "emit",
			)
			if diagnosticErr != nil {
				return nil, diagnosticErr
			}
			if diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
			}
		case "on":
			if len(arguments) < 2 {
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
				path, eventName, "Function", arguments[1], "on",
			)
			if diagnosticErr != nil {
				return nil, diagnosticErr
			}
			if diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
			}
		case "off":
			if len(arguments) < 2 {
				continue
			}
			diagnostic, diagnosticErr := p.eventBusArgumentDiagnostic(
				path, eventName, "Function", arguments[1], "off",
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

	for _, call := range analysis.Calls(
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
func (p *AdminAnalyzer) twigDiagnostics(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	var diagnostics []lsp.Problem
	analysis, err := p.adminTwigDiagnosticDocument(document)
	if err != nil {
		return nil, err
	}
	directiveDiagnostics, err := p.twigDirectiveDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, directiveDiagnostics...)
	privilegeDiagnostics, err := p.twigPrivilegeDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, privilegeDiagnostics...)
	routeDiagnostics, err := p.twigModuleRouteDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, routeDiagnostics...)

	// Find all html_start_tag nodes
	if err := p.findHTMLStartTags(
		ctx,
		document.SyntaxTree.Root,
		document.Text,
		analysis,
		&diagnostics,
	); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	slotBindingDiagnostics, err := p.unknownScopedSlotBindingDiagnostics(
		ctx, document, analysis,
	)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, slotBindingDiagnostics...)
	slotMemberDiagnostics, err := p.unknownScopedSlotMemberDiagnostics(
		ctx, document, analysis,
	)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, slotMemberDiagnostics...)
	vueMemberDiagnostics, err := p.unknownVueBindingMemberDiagnostics(
		ctx, document, analysis,
	)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, vueMemberDiagnostics...)
	templateMemberDiagnostics, err :=
		p.unknownTwigComponentMemberDiagnostics(ctx, document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, templateMemberDiagnostics...)
	deprecatedMemberDiagnostics, err :=
		p.deprecatedTwigComponentMemberDiagnostics(ctx, document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, deprecatedMemberDiagnostics...)

	// Check for invalid block references in component overrides
	p.checkBlockReferences(
		document.SyntaxTree.Root, document.LineIndex, analysis, &diagnostics,
	)

	return diagnostics, nil
}

func (p *AdminAnalyzer) adminTwigDiagnosticDocument(
	document *lsp.TextDocument,
) (*adminTwigDiagnosticDocument, error) {
	templatePath, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	liveOwner, err := p.adminIndexer.GetComponentForDocument(
		templatePath, document.SyntaxTree.Root,
		document.Source, document.LineIndex,
	)
	if err != nil {
		return nil, err
	}
	result := &adminTwigDiagnosticDocument{
		templatePath: templatePath,
		liveOwner:    liveOwner,
		rootIdentifiers: admin.TwigVueExpressionRootIdentifiers(
			document.SyntaxTree.Root, document.Text,
		),
		memberAccesses: admin.TwigVueExpressionMemberAccesses(
			document.SyntaxTree.Root, document.Text,
		),
		registryReferences: admin.TwigRegistryReferences(
			document.SyntaxTree.Root,
		),
		directiveReferences: admin.TwigDirectiveReferences(
			document.SyntaxTree.Root,
		),
		components: make(map[string]adminTwigComponentResolution),
	}
	if liveOwner != nil {
		result.localIdentifiers = make(map[cst.TextRange]bool)
		for _, identifier := range result.rootIdentifiers {
			if admin.TwigVueRootIdentifierIsLocal(
				document.SyntaxTree.Root, document.Text, identifier,
			) {
				result.localIdentifiers[identifier.Range] = true
			}
		}
	}
	return result, nil
}

func (p *AdminAnalyzer) diagnosticComponentForTemplateTag(
	analysis *adminTwigDiagnosticDocument,
	name string,
) (*admin.VueComponent, bool, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if resolved, ok := analysis.components[key]; ok {
		return resolved.component, resolved.found, resolved.err
	}
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		analysis.templatePath, name, analysis.liveOwner,
	)
	analysis.components[key] = adminTwigComponentResolution{
		component: component, found: found, err: err,
	}
	return component, found, err
}

func (p *AdminAnalyzer) unknownTwigComponentMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil || analysis.liveOwner == nil {
		return nil, nil
	}
	component := analysis.liveOwner
	known := make(map[string]bool)
	var candidates []string
	addKnown := func(name string, suggest bool) {
		if name == "" || known[name] {
			return
		}
		known[name] = true
		if suggest {
			candidates = append(candidates, name)
		}
	}
	for _, member := range component.TemplateMembers() {
		addKnown(member.Name, !component.OpenRuntimeMembers)
	}
	for _, member := range admin.VueBuiltinMembers() {
		addKnown(member.Name, true)
	}
	for _, global := range admin.VueTemplateGlobals() {
		addKnown(global.Name, true)
	}

	var diagnostics []lsp.Problem
	seen := make(map[cst.TextRange]bool)
	for _, identifier := range analysis.rootIdentifiers {
		if ctx.Err() != nil {
			return nil, nil
		}
		if known[identifier.Name] || seen[identifier.Range] ||
			analysis.localIdentifiers[identifier.Range] {
			continue
		}
		if node := document.SyntaxTree.Root.NodeAtOffset(
			identifier.Range.Start,
		); node != nil {
			if _, _, blockLocal := admin.TwigBlockScopeMemberAt(
				*component, node, identifier.Name,
			); blockLocal {
				continue
			}
		}
		suggestions := adminNearbySuggestions(identifier.Name, candidates)
		// Runtime mixins and application plugins can add arbitrary template
		// globals. Report only a close misspelling of a statically known name.
		if len(suggestions) == 0 {
			continue
		}
		seen[identifier.Range] = true
		diagnostics = append(diagnostics, lsp.Problem{
			Range: identifier.Range,
			Message: fmt.Sprintf(
				"Unknown Administration component template member '%s'",
				identifier.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-template-member",
			Payload: map[string]any{
				"memberName":  identifier.Name,
				"suggestions": suggestions,
			},
		})
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) deprecatedInstanceMemberDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) []lsp.Problem {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
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
	var diagnostics []lsp.Problem
	seen := make(map[cst.TextRange]bool)
	for _, expression := range analysis.Nodes(jssyntax.JsMemberExpression) {
		name, matched := jsquery.ThisMember(expression)
		if !matched || name == "" {
			continue
		}
		nameNode := jsquery.ThisMemberNameNode(expression)
		if nameNode == nil {
			continue
		}
		rangeValue := nameNode.RangeTrimmedTrivia()
		if seen[rangeValue] {
			continue
		}
		message := commonDeprecatedAdminMember(components, name)
		if message == "" {
			continue
		}
		seen[rangeValue] = true
		diagnostics = append(diagnostics, deprecatedAdminMemberProblem(
			name, message, rangeValue,
		))
	}
	return diagnostics
}

func (p *AdminAnalyzer) deprecatedTwigComponentMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil || analysis.liveOwner == nil {
		return nil, nil
	}
	component := analysis.liveOwner
	var diagnostics []lsp.Problem
	for _, identifier := range analysis.rootIdentifiers {
		if ctx.Err() != nil {
			return nil, nil
		}
		if analysis.localIdentifiers[identifier.Range] {
			continue
		}
		member, found := component.TemplateMember(identifier.Name)
		if !found || member.Deprecated == "" {
			continue
		}
		diagnostics = append(diagnostics, deprecatedAdminMemberProblem(
			member.Name, member.Deprecated, identifier.Range,
		))
	}
	return diagnostics, nil
}

func commonDeprecatedAdminMember(
	components []admin.VueComponent,
	name string,
) string {
	var messages []string
	seen := make(map[string]bool)
	for _, component := range components {
		member, found := component.TemplateMember(name)
		if !found || member.Deprecated == "" {
			return ""
		}
		if !seen[member.Deprecated] {
			seen[member.Deprecated] = true
			messages = append(messages, member.Deprecated)
		}
	}
	return strings.Join(messages, " / ")
}

func deprecatedAdminMemberProblem(
	name,
	deprecation string,
	rangeValue cst.TextRange,
) lsp.Problem {
	return lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Administration component member '%s' is deprecated: %s",
			name, deprecation,
		),
		Source:   "shopware-lsp",
		Severity: protocol.DiagnosticSeverityHint,
		ID:       "admin.component.deprecated-member",
		Tags: []protocol.DiagnosticTag{
			protocol.DiagnosticTagDeprecated,
		},
	}
}

func (p *AdminAnalyzer) unknownScopedSlotBindingDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	root := document.SyntaxTree.Root
	templatePath := analysis.templatePath
	liveOwner := analysis.liveOwner
	var diagnostics []lsp.Problem
	seenScopes := make(map[cst.TextRange]bool)
	for _, attributeNode := range twigquery.Nodes(
		root, twigsyntax.HtmlAttribute,
	) {
		if err := ctx.Err(); err != nil {
			return nil, nil
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok || admin.NormalizeSlotName(
			twigquery.HTMLAttributeName(attributeNode),
		) == "" {
			continue
		}
		value, found := attribute.Value()
		if !found {
			continue
		}
		inner, found := value.GetInner()
		if !found {
			continue
		}
		scope, found := admin.TwigScopedSlotAtOffset(
			root, inner.Syntax().Range().Start,
		)
		if !found || seenScopes[scope.BindingRange] {
			continue
		}
		seenScopes[scope.BindingRange] = true
		resolved, resolveErr := p.adminIndexer.ResolveTwigScopedSlotForOwner(
			root, scope.BindingRange.Start, templatePath, liveOwner,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved == nil || !resolved.Slot.MembersComplete {
			continue
		}
		memberNames := make([]string, 0, len(resolved.Slot.Members))
		for _, member := range resolved.Slot.Members {
			memberNames = append(memberNames, member.Name)
		}
		componentName := resolved.Scope.ComponentName
		if componentName == "" {
			componentName = resolved.Component.Name
		}
		for _, binding := range scope.Bindings {
			if binding.WholeObject || binding.MemberName == "" ||
				binding.MemberRange.Len() == 0 {
				continue
			}
			if _, memberFound := resolved.Slot.Member(
				binding.MemberName,
			); memberFound {
				continue
			}
			diagnostics = append(diagnostics, lsp.Problem{
				Range: binding.MemberRange,
				Message: fmt.Sprintf(
					"Unknown scoped-slot prop '%s' on '%s'",
					binding.MemberName, resolved.QualifiedName(),
				),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.unknown-slot-prop",
				Payload: map[string]any{
					"componentName":  componentName,
					"componentNames": resolved.ComponentNames(),
					"slotName":       resolved.Scope.SlotName,
					"memberName":     binding.MemberName,
					"suggestions": suggestion.Similar(
						binding.MemberName, memberNames,
					),
				},
			})
		}
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) twigDirectiveDiagnostics(
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil || len(analysis.directiveReferences) == 0 {
		return nil, nil
	}
	directives, err := p.adminIndexer.GetAllDirectivesForTemplate(
		analysis.templatePath,
	)
	if err != nil || len(directives) == 0 {
		return nil, err
	}
	known := make(map[string]bool, len(directives))
	names := make([]string, 0, len(directives))
	for _, directive := range directives {
		if directive.Name == "" || known[directive.Name] {
			continue
		}
		known[directive.Name] = true
		names = append(names, directive.Name)
	}
	var result []lsp.Problem
	for _, reference := range analysis.directiveReferences {
		if known[reference.Name] {
			continue
		}
		suggestions := adminDirectiveSuggestions(reference.Name, names)
		// Custom Vue directives may be installed by third-party code outside the
		// Shopware registry. Report only likely misspellings of an indexed name.
		if len(suggestions) == 0 {
			continue
		}
		result = append(result, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Administration Vue directive 'v-%s' is not registered",
				reference.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.directive.not-found",
			Payload: map[string]any{
				"directiveName": reference.Name,
				"suggestions":   suggestions,
			},
		})
	}
	return result, nil
}

func adminDirectiveSuggestions(input string, candidates []string) []string {
	return adminNearbySuggestions(input, candidates)
}

func adminNearbySuggestions(input string, candidates []string) []string {
	var nearby []string
	for _, candidate := range candidates {
		if boundedAdminEditDistance(
			strings.ToLower(input), strings.ToLower(candidate), 2,
		) <= 2 {
			nearby = append(nearby, candidate)
		}
	}
	return suggestion.Similar(input, nearby)
}

func boundedAdminEditDistance(left, right string, limit int) int {
	leftRunes, rightRunes := []rune(left), []rune(right)
	if difference := len(leftRunes) - len(rightRunes); difference > limit ||
		difference < -limit {
		return limit + 1
	}
	previous := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current := make([]int, len(rightRunes)+1)
		current[0] = leftIndex + 1
		rowMinimum := current[0]
		for rightIndex, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[rightIndex+1] = min(
				current[rightIndex]+1,
				previous[rightIndex+1]+1,
				previous[rightIndex]+cost,
			)
			rowMinimum = min(rowMinimum, current[rightIndex+1])
		}
		if rowMinimum > limit {
			return limit + 1
		}
		previous = current
	}
	return previous[len(rightRunes)]
}

func (p *AdminAnalyzer) unknownVueBindingMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	templatePath := analysis.templatePath
	root := document.SyntaxTree.Root
	liveComponent := analysis.liveOwner
	var result []lsp.Problem
	for _, access := range analysis.memberAccesses {
		if err := ctx.Err(); err != nil {
			return nil, nil
		}
		if liveComponent == nil || analysis.localIdentifiers[access.RootRange] {
			node := root.NodeAtOffset(access.MemberRange.Start)
			resolvedSlot, err :=
				p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
					root, node, document.Text, access.MemberRange.Start,
					templatePath, liveComponent,
				)
			if err != nil {
				return nil, err
			}
			resolved, err := p.adminIndexer.ResolveTwigVueMemberForComponent(
				root, document.Text, access.MemberRange.Start,
				templatePath, liveComponent,
			)
			if err != nil {
				return nil, err
			}
			if resolved != nil {
				if resolved.MemberFound || !resolved.ReceiverFound ||
					!resolved.MembersComplete {
					continue
				}
				if resolvedSlot != nil && resolved.Binding.ScopeRange.Len() >
					resolvedSlot.Scope.TemplateRange.Len() {
					continue
				}
				result = append(result, unknownTypedVueMemberProblem(
					access, access.QualifiedName(), "typed Vue binding",
					twigVueMemberNames(resolved.ReceiverMembers),
				))
				continue
			}
			continue
		}
		resolvedInstance, instanceErr :=
			p.adminIndexer.ResolveTwigVueInstanceMemberAccessForComponent(
				root, document.Text, access,
				templatePath, liveComponent,
			)
		if instanceErr != nil {
			return nil, instanceErr
		}
		if resolvedInstance == nil || resolvedInstance.MemberFound ||
			!resolvedInstance.ReceiverFound ||
			!resolvedInstance.MembersComplete {
			continue
		}
		result = append(result, unknownTypedVueMemberProblem(
			access, resolvedInstance.QualifiedName(),
			"typed component member",
			twigVueMemberNames(resolvedInstance.ReceiverMembers),
		))
	}
	return result, nil
}

func twigVueMemberNames(members []admin.TwigVueMember) []string {
	result := make([]string, 0, len(members))
	for _, member := range members {
		result = append(result, member.Name)
	}
	return result
}

func unknownTypedVueMemberProblem(
	access admin.TwigVueMemberAccess,
	qualified,
	receiverKind string,
	members []string,
) lsp.Problem {
	return lsp.Problem{
		Range: access.MemberRange,
		Message: fmt.Sprintf(
			"Unknown property '%s' on %s '%s'",
			access.Member, receiverKind, qualified,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.unknown-vue-member",
		Payload: map[string]any{
			"bindingName": access.Root,
			"memberName":  access.Member,
			"suggestions": suggestion.Similar(access.Member, members),
		},
	}
}

func (p *AdminAnalyzer) unknownScopedSlotMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	root := document.SyntaxTree.Root
	templatePath := analysis.templatePath
	liveOwner := analysis.liveOwner
	var diagnostics []lsp.Problem
	for _, access := range analysis.memberAccesses {
		if err := ctx.Err(); err != nil {
			return nil, nil
		}
		node := root.NodeAtOffset(access.MemberRange.Start)
		resolved, err := p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
			root, node, document.Text, access.MemberRange.Start,
			templatePath, liveOwner,
		)
		if err != nil {
			return nil, err
		}
		if resolved == nil || resolved.MemberFound ||
			!resolved.ReceiverFound || !resolved.MembersComplete {
			continue
		}
		if vueBinding, found := admin.TwigVueBindingAtOffset(
			root, document.Text, access.RootRange.Start,
		); found && vueBinding != nil &&
			vueBinding.ScopeRange.Len() <= resolved.Scope.TemplateRange.Len() {
			continue
		}
		memberNames := make([]string, 0, len(resolved.Slot.Members))
		for _, member := range resolved.Slot.Members {
			memberNames = append(memberNames, member.Name)
		}
		componentName := resolved.Scope.ComponentName
		if componentName == "" {
			componentName = resolved.Component.Name
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range: access.MemberRange,
			Message: fmt.Sprintf(
				"Unknown scoped-slot prop '%s' on '%s'",
				access.Member,
				resolved.QualifiedName(),
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-slot-prop",
			Payload: map[string]any{
				"componentName":  componentName,
				"componentNames": resolved.ComponentNames(),
				"slotName":       resolved.Scope.SlotName,
				"memberName":     access.Member,
				"suggestions": suggestion.Similar(
					access.Member, memberNames,
				),
			},
		})
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) javaScriptModuleRouteDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	known, names, err := p.moduleRouteCatalog()
	if err != nil || len(known) == 0 {
		return nil, err
	}
	var diagnostics []lsp.Problem
	for _, literal := range analysis.Nodes(jssyntax.JsString) {
		if !admin.IsJavaScriptModuleRouteReference(literal) {
			continue
		}
		name := jsquery.StringValue(literal)
		if name == "" {
			continue
		}
		if _, exists := known[name]; exists {
			continue
		}
		diagnostics = append(diagnostics, moduleRouteProblem(
			name,
			javaScriptStringContentRange(literal, document.Text),
			names,
		))
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) javaScriptRegistryDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	components, err := p.adminIndexer.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	modules, err := p.adminIndexer.GetAllModules()
	if err != nil {
		return nil, err
	}
	directives, err := p.adminIndexer.GetAllDirectives()
	if err != nil {
		return nil, err
	}
	filters, err := p.adminIndexer.GetAllFilters()
	if err != nil {
		return nil, err
	}
	cmsElements, err := p.adminIndexer.GetAllCMSRegistrationsByKind(
		admin.AdminCMSElement,
	)
	if err != nil {
		return nil, err
	}
	cmsBlocks, err := p.adminIndexer.GetAllCMSRegistrationsByKind(
		admin.AdminCMSBlock,
	)
	if err != nil {
		return nil, err
	}
	componentNames := uniqueAdminComponentNames(components)
	moduleNames := uniqueAdminModuleNames(modules)
	directiveNames := uniqueAdminDirectiveNames(directives)
	filterNames := uniqueAdminFilterNames(filters)
	knownComponents := stringSet(componentNames)
	knownModules := stringSet(moduleNames)
	knownDirectives := stringSet(directiveNames)
	knownFilters := stringSet(filterNames)
	cmsElementNames := uniqueAdminCMSNames(cmsElements)
	cmsBlockNames := uniqueAdminCMSNames(cmsBlocks)
	knownCMSElements := stringSet(cmsElementNames)
	knownCMSBlocks := stringSet(cmsBlockNames)

	var diagnostics []lsp.Problem
	for _, literal := range analysis.Nodes(jssyntax.JsString) {
		reference, found := admin.JavaScriptRegistryReferenceAt(literal)
		if !found {
			if target, componentLink :=
				admin.JavaScriptCMSComponentReferenceAt(literal); componentLink {
				reference = admin.JavaScriptRegistryReference{
					AdminSymbolTarget: target,
					Operation:         "cms-component",
				}
				found = true
			}
		}
		if !found || (reference.Operation != "get" &&
			reference.Operation != "cms-component") &&
			(reference.Kind != admin.AdminSymbolDirective &&
				reference.Kind != admin.AdminSymbolFilter ||
				reference.Operation != "getByName") {
			// A has() lookup is commonly an intentional existence guard.
			continue
		}
		var known map[string]struct{}
		var names []string
		var label, code, payloadKey string
		switch reference.Kind {
		case admin.AdminSymbolComponent:
			known, names = knownComponents, componentNames
			label = "Administration component"
			code = "admin.component.registry-not-found"
			payloadKey = "componentName"
		case admin.AdminSymbolModule:
			known, names = knownModules, moduleNames
			label = "Administration module"
			code = "admin.module.not-found"
			payloadKey = "moduleName"
		case admin.AdminSymbolDirective:
			known, names = knownDirectives, directiveNames
			label = "Administration Vue directive"
			code = "admin.directive.not-found"
			payloadKey = "directiveName"
		case admin.AdminSymbolFilter:
			known, names = knownFilters, filterNames
			label = "Administration filter"
			code = "admin.filter.not-found"
			payloadKey = "filterName"
		default:
			continue
		}
		// An empty catalog means indexing has not produced enough information to
		// make a reliable missing-symbol claim yet.
		if len(known) == 0 {
			continue
		}
		if _, exists := known[reference.Name]; exists {
			continue
		}
		suggestions := suggestion.Similar(reference.Name, names)
		if reference.Kind == admin.AdminSymbolDirective {
			suggestions = adminDirectiveSuggestions(reference.Name, names)
			if len(suggestions) == 0 {
				continue
			}
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range: javaScriptStringContentRange(literal, document.Text),
			Message: fmt.Sprintf(
				"%s '%s' is not registered", label, reference.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       lsp.DiagnosticID(code),
			Payload: map[string]any{
				payloadKey:    reference.Name,
				"suggestions": suggestions,
			},
		})

	}
	// Keep CMS registry diagnostics in the same literal pass so block-slot
	// references and explicit get-by-name lookups share exact source ranges.
	for _, literal := range analysis.Nodes(jssyntax.JsString) {
		reference, found := admin.JavaScriptCMSReferenceAt(literal)
		if !found || reference.Operation == "register" {
			continue
		}
		var known map[string]struct{}
		var names []string
		var label, code, payloadKey string
		switch reference.Kind {
		case admin.AdminSymbolCMSElement:
			known, names = knownCMSElements, cmsElementNames
			label = "Shopware CMS element"
			code = "admin.cms-element.not-found"
			payloadKey = "cmsElementName"
		case admin.AdminSymbolCMSBlock:
			known, names = knownCMSBlocks, cmsBlockNames
			label = "Shopware CMS block"
			code = "admin.cms-block.not-found"
			payloadKey = "cmsBlockName"
		default:
			continue
		}
		if len(known) == 0 {
			continue
		}
		if _, exists := known[reference.Name]; exists {
			continue
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range: javaScriptStringContentRange(literal, document.Text),
			Message: fmt.Sprintf(
				"%s '%s' is not registered", label, reference.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       lsp.DiagnosticID(code),
			Payload: map[string]any{
				payloadKey:    reference.Name,
				"suggestions": suggestion.Similar(reference.Name, names),
			},
		})
	}
	return diagnostics, nil
}

func uniqueAdminComponentNames(components []admin.VueComponent) []string {
	names := make([]string, 0, len(components))
	seen := make(map[string]struct{}, len(components))
	for _, component := range components {
		if component.Name == "" {
			continue
		}
		if _, exists := seen[component.Name]; exists {
			continue
		}
		seen[component.Name] = struct{}{}
		names = append(names, component.Name)
	}
	return names
}

func uniqueAdminDirectiveNames(directives []admin.AdminDirective) []string {
	names := make([]string, 0, len(directives))
	seen := make(map[string]struct{}, len(directives))
	for _, directive := range directives {
		if directive.Name == "" {
			continue
		}
		if _, exists := seen[directive.Name]; exists {
			continue
		}
		seen[directive.Name] = struct{}{}
		names = append(names, directive.Name)
	}
	return names
}

func uniqueAdminFilterNames(filters []admin.AdminFilter) []string {
	names := make([]string, 0, len(filters))
	seen := make(map[string]struct{}, len(filters))
	for _, filter := range filters {
		if filter.Name == "" {
			continue
		}
		if _, exists := seen[filter.Name]; exists {
			continue
		}
		seen[filter.Name] = struct{}{}
		names = append(names, filter.Name)
	}
	return names
}

func uniqueAdminCMSNames(
	registrations []admin.AdminCMSRegistration,
) []string {
	names := make([]string, 0, len(registrations))
	seen := make(map[string]struct{}, len(registrations))
	for _, registration := range registrations {
		if registration.Name == "" {
			continue
		}
		if _, exists := seen[registration.Name]; exists {
			continue
		}
		seen[registration.Name] = struct{}{}
		names = append(names, registration.Name)
	}
	return names
}

func uniqueAdminModuleNames(modules []admin.AdminModule) []string {
	names := make([]string, 0, len(modules))
	seen := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		if module.Name == "" {
			continue
		}
		if _, exists := seen[module.Name]; exists {
			continue
		}
		seen[module.Name] = struct{}{}
		names = append(names, module.Name)
	}
	return names
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func (p *AdminAnalyzer) twigModuleRouteDiagnostics(
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil || analysis == nil {
		return nil, nil
	}
	var references []admin.AdminTwigRegistryReference
	for _, reference := range analysis.registryReferences {
		if reference.Kind == admin.AdminSymbolModuleRoute &&
			reference.Name != "" {
			references = append(references, reference)
		}
	}
	if len(references) == 0 {
		return nil, nil
	}
	known, names, err := p.moduleRouteCatalog()
	if err != nil || len(known) == 0 {
		return nil, err
	}
	var diagnostics []lsp.Problem
	for _, reference := range references {
		if _, exists := known[reference.Name]; exists {
			continue
		}
		diagnostics = append(diagnostics, moduleRouteProblem(
			reference.Name,
			reference.Range,
			names,
		))
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) moduleRouteCatalog() (map[string]struct{}, []string, error) {
	if p == nil || p.adminIndexer == nil {
		return nil, nil, nil
	}
	routes, err := p.adminIndexer.GetAllModuleRoutes()
	if err != nil {
		return nil, nil, err
	}
	known := make(map[string]struct{}, len(routes))
	names := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.Name == "" {
			continue
		}
		if _, exists := known[route.Name]; exists {
			continue
		}
		known[route.Name] = struct{}{}
		names = append(names, route.Name)
	}
	return known, names, nil
}

func moduleRouteProblem(
	name string,
	rangeValue jssyntax.TextRange,
	names []string,
) lsp.Problem {
	return lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Administration module route '%s' is not registered",
			name,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.module-route.not-found",
		Payload: map[string]any{
			"routeName": name,
			"suggestions": suggestion.Similar(
				name,
				names,
			),
		},
	}
}

func javaScriptStringContentRange(
	node *jssyntax.Node,
	source []byte,
) jssyntax.TextRange {
	rangeValue := node.RangeTrimmedTrivia()
	if rangeValue.End-rangeValue.Start < 2 ||
		int(rangeValue.End) > len(source) {
		return rangeValue
	}
	quote := source[rangeValue.Start]
	if (quote == '\'' || quote == '"' || quote == '`') &&
		source[rangeValue.End-1] == quote {
		rangeValue.Start++
		rangeValue.End--
	}
	return rangeValue
}

func (p *AdminAnalyzer) twigPrivilegeDiagnostics(
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	var references []admin.AdminTwigRegistryReference
	for _, reference := range analysis.registryReferences {
		if reference.Kind == admin.AdminSymbolPrivilege && reference.Name != "" {
			references = append(references, reference)
		}
	}
	if len(references) == 0 {
		return nil, nil
	}
	privileges, err := p.adminIndexer.GetAllPrivileges()
	if err != nil || len(privileges) == 0 {
		return nil, err
	}
	known := make(map[string]struct{}, len(privileges))
	names := make([]string, 0, len(privileges))
	hasProjectPrivileges := false
	for _, privilege := range privileges {
		if privilege.Name == "" {
			continue
		}
		if !privilege.IsBuiltin() {
			hasProjectPrivileges = true
		}
		if _, exists := known[privilege.Name]; exists {
			continue
		}
		known[privilege.Name] = struct{}{}
		names = append(names, privilege.Name)
	}
	// Preserve the analyzer's fail-open behavior for an empty or not-yet-built
	// project index. The built-in administrator key remains available to
	// completion and hover without making it look like a complete ACL registry.
	if !hasProjectPrivileges {
		return nil, nil
	}
	var diagnostics []lsp.Problem
	for _, reference := range references {
		if _, exists := known[reference.Name]; exists {
			continue
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Administration privilege '%s' is not registered",
				reference.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.privilege.not-found",
			Payload: map[string]any{
				"privilegeName": reference.Name,
				"suggestions": suggestion.Similar(
					reference.Name,
					names,
				),
			},
		})
	}
	return diagnostics, nil
}

// checkBlockReferences checks block overrides against the source registration's
// parent contract. Both Component.extend and Component.override templates use
// the same resolution path.
func (p *AdminAnalyzer) checkBlockReferences(
	rootNode *twigsyntax.Node,
	lineIndex *twigsyntax.LineIndex,
	analysis *adminTwigDiagnosticDocument,
	diagnostics *[]lsp.Problem,
) {
	if analysis == nil || analysis.liveOwner == nil ||
		analysis.liveOwner.Kind != admin.ComponentExtend &&
			analysis.liveOwner.Kind != admin.ComponentOverride &&
			analysis.liveOwner.ExtendsComponent == "" {
		return
	}

	parent, err := p.adminIndexer.GetParentComponentForTemplate(
		analysis.templatePath,
	)
	if err != nil || parent == nil {
		return
	}
	blocks := make(map[string]admin.TwigBlock, len(parent.Blocks))
	names := make([]string, 0, len(parent.Blocks))
	for _, block := range parent.Blocks {
		if block.Name == "" {
			continue
		}
		blocks[block.Name] = block
		names = append(names, block.Name)
	}
	p.findBlockTags(
		rootNode, lineIndex, parent.Name, blocks, names, diagnostics,
	)
}

// findBlockTags finds all {% block %} tags and checks if they exist in valid blocks
func (p *AdminAnalyzer) findBlockTags(
	node *twigsyntax.Node,
	_ *twigsyntax.LineIndex,
	parentName string,
	validBlocks map[string]admin.TwigBlock,
	blockNames []string,
	diagnostics *[]lsp.Problem,
) {
	if node == nil {
		return
	}

	for _, blockNode := range twigquery.Nodes(node, twigsyntax.TwigBlock) {
		blockName := twigquery.BlockName(blockNode)
		block, cast := twigast.CastTwigBlock(blockNode)
		if blockName == "" || !cast || block.Name() == nil {
			continue
		}
		parentBlock, exists := validBlocks[blockName]
		if !exists {
			suggestions := adminNearbySuggestions(blockName, blockNames)
			if len(suggestions) == 0 {
				// Component.extend/override templates may introduce their own
				// extensibility blocks; absence from the parent is not itself an
				// error. Only a close parent-block spelling gives enough evidence
				// to report a likely typo.
				continue
			}
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: block.Name().Range(),
				Message: fmt.Sprintf(
					"Block '%s' does not exist in parent component '%s'",
					blockName, parentName,
				),
				Source:   "shopware-lsp",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.block-not-found",
				Payload: map[string]any{
					"blockName": blockName, "suggestions": suggestions,
				},
			})
			continue
		}
		if parentBlock.Deprecated != "" {
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: block.Name().Range(),
				Message: fmt.Sprintf(
					"Administration Twig block '%s' is deprecated: %s",
					blockName, parentBlock.Deprecated,
				),
				Source:   "shopware-lsp",
				Severity: protocol.DiagnosticSeverityHint,
				ID:       "admin.component.deprecated-block",
				Tags: []protocol.DiagnosticTag{
					protocol.DiagnosticTagDeprecated,
				},
			})
		}
	}
}

// findHTMLStartTags recursively finds all html_start_tag nodes and checks for missing required props
func (p *AdminAnalyzer) findHTMLStartTags(
	ctx context.Context,
	root *twigsyntax.Node,
	content []byte,
	analysis *adminTwigDiagnosticDocument,
	diagnostics *[]lsp.Problem,
) error {
	if root == nil || analysis == nil {
		return nil
	}

	for _, startTag := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		if ctx.Err() != nil {
			return nil
		}
		if err := p.checkComponentSlotNames(
			startTag, analysis.templatePath, analysis.liveOwner, diagnostics,
		); err != nil {
			return err
		}
		if err := p.checkComponentProps(
			root, content, startTag, analysis, diagnostics,
		); err != nil {
			return err
		}
	}
	return nil
}

// checkComponentProps checks if a component tag has all required props
// <sw-button<caret>> - checks that all required props are present
func (p *AdminAnalyzer) checkComponentProps(
	root *twigsyntax.Node,
	content []byte,
	startTag *twigsyntax.Node,
	analysis *adminTwigDiagnosticDocument,
	diagnostics *[]lsp.Problem,
) error {
	// Get the tag name
	tagName := p.getTagName(startTag)
	if tagName == "" {
		return nil
	}
	componentRange := cst.TextRange{}
	if tagNameNode := p.getTagNameNode(startTag); tagNameNode != nil {
		componentRange = tagNameNode.Range()
	}
	selector, dynamicComponent := admin.TwigDynamicComponentSelector(startTag)
	if dynamicComponent {
		for _, candidate := range selector.Candidates {
			component, found, resolveErr := p.diagnosticComponentForTemplateTag(
				analysis, candidate.Name,
			)
			if resolveErr != nil {
				return resolveErr
			}
			if !found || component == nil {
				p.checkUnknownDynamicComponent(candidate, diagnostics)
			} else {
				p.checkDeprecatedComponent(
					*component, candidate.Name, candidate.Range, diagnostics,
				)
			}
		}
		resolvedSelector, components, complete, resolveErr :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				analysis.templatePath, selector, analysis.liveOwner, startTag,
			)
		if resolveErr != nil {
			return resolveErr
		}
		if !complete {
			return nil
		}
		p.checkUnknownComponentContractAttributes(
			startTag, components, selector.AttributeName, tagName, diagnostics,
		)
		p.checkDeprecatedComponentContractAttributes(
			startTag, components, diagnostics,
		)
		for _, component := range components {
			candidateRange := componentRange
			for _, candidate := range resolvedSelector.Candidates {
				if candidate.Name == component.Name {
					if candidate.Range.Len() > 0 {
						candidateRange = candidate.Range
					} else if resolvedSelector.ExpressionRange.Len() > 0 {
						candidateRange = resolvedSelector.ExpressionRange
					}
					break
				}
			}
			if err := p.checkResolvedComponentProps(
				root, content, startTag, analysis.templatePath,
				component, component.Name, candidateRange, true,
				analysis.liveOwner, diagnostics,
			); err != nil {
				return err
			}
		}
		return nil
	}

	// Skip non-component tags (standard HTML elements and template)
	if !admin.IsComponentTag(tagName) {
		return nil
	}

	// Get the component definition
	component, found, err := p.diagnosticComponentForTemplateTag(
		analysis, tagName,
	)
	if err != nil || !found || component == nil {
		if !dynamicComponent {
			p.checkUnknownComponent(tagName, startTag, diagnostics)
		}
		return nil
	}
	p.checkUnknownComponentContractAttributes(
		startTag, []admin.VueComponent{*component}, "", tagName, diagnostics,
	)
	p.checkDeprecatedComponent(*component, tagName, componentRange, diagnostics)
	p.checkDeprecatedComponentContractAttributes(
		startTag, []admin.VueComponent{*component}, diagnostics,
	)

	return p.checkResolvedComponentProps(
		root, content, startTag, analysis.templatePath,
		*component, tagName, componentRange, false, analysis.liveOwner, diagnostics,
	)
}

func (p *AdminAnalyzer) checkDeprecatedComponent(
	component admin.VueComponent,
	displayName string,
	rangeValue cst.TextRange,
	diagnostics *[]lsp.Problem,
) {
	if rangeValue.Len() == 0 || strings.TrimSpace(component.Deprecated) == "" {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Administration component '%s' is deprecated: %s",
			displayName, component.Deprecated,
		),
		Source: "shopware", Severity: protocol.DiagnosticSeverityHint,
		ID:   "admin.component.deprecated",
		Tags: []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
	})
}

func (p *AdminAnalyzer) checkDeprecatedComponentContractAttributes(
	startTag *twigsyntax.Node,
	components []admin.VueComponent,
	diagnostics *[]lsp.Problem,
) {
	if startTag == nil || len(components) == 0 {
		return
	}
	ownerNames := make([]string, 0, len(components))
	for _, component := range components {
		ownerNames = append(ownerNames, component.Name)
	}
	owner := strings.Join(ownerNames, " | ")
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return
	}
	for _, attribute := range tag.Attributes() {
		nameToken := attribute.Name()
		if nameToken == nil {
			continue
		}
		attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
		if attributeName == "v-bind" {
			value, valueOK := attribute.Value()
			if !valueOK {
				continue
			}
			inner, innerOK := value.GetInner()
			if !innerOK {
				continue
			}
			fields, _ := admin.VueObjectBindingFields(
				inner.Syntax().Text(), inner.Syntax().Range().Start,
			)
			for _, field := range fields {
				name := admin.NormalizePropName(field.Name)
				if message := commonDeprecatedAdminProp(
					components, name,
				); message != "" {
					appendDeprecatedAdminPropProblem(
						diagnostics, owner, name, message, field.NameRange,
					)
				}
			}
			continue
		}
		if _, modelAttribute := admin.NormalizeModelArgument(attributeName); modelAttribute {
			message, propName := commonDeprecatedAdminModel(
				components, attributeName,
			)
			if message != "" {
				appendDeprecatedAdminPropProblem(
					diagnostics, owner, propName, message, nameToken.Range(),
				)
			}
			continue
		}
		reference, found := admin.VuePropReferenceForAttribute(
			attributeName, nameToken.Range(),
		)
		if !found {
			continue
		}
		if message := commonDeprecatedAdminProp(
			components, reference.Name,
		); message != "" {
			appendDeprecatedAdminPropProblem(
				diagnostics, owner, reference.Name, message, reference.Range,
			)
		}
	}
}

func commonDeprecatedAdminProp(
	components []admin.VueComponent,
	name string,
) string {
	seen := make(map[string]bool)
	var messages []string
	for _, component := range components {
		prop, found := component.ComponentProp(name)
		if !found || strings.TrimSpace(prop.Deprecated) == "" {
			return ""
		}
		if !seen[prop.Deprecated] {
			seen[prop.Deprecated] = true
			messages = append(messages, prop.Deprecated)
		}
	}
	return strings.Join(messages, " / ")
}

func commonDeprecatedAdminModel(
	components []admin.VueComponent,
	attributeName string,
) (string, string) {
	seen := make(map[string]bool)
	var messages []string
	propName := ""
	for _, component := range components {
		model, found := component.ComponentModel(attributeName)
		if !found || strings.TrimSpace(model.Prop.Deprecated) == "" {
			return "", ""
		}
		if propName == "" {
			propName = model.PropName
		}
		if !seen[model.Prop.Deprecated] {
			seen[model.Prop.Deprecated] = true
			messages = append(messages, model.Prop.Deprecated)
		}
	}
	return strings.Join(messages, " / "), propName
}

func appendDeprecatedAdminPropProblem(
	diagnostics *[]lsp.Problem,
	owner,
	propName,
	message string,
	rangeValue cst.TextRange,
) {
	if diagnostics == nil || rangeValue.Len() == 0 {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Prop '%s' on Administration component '%s' is deprecated: %s",
			propName, owner, message,
		),
		Source: "shopware", Severity: protocol.DiagnosticSeverityHint,
		ID:   "admin.component.deprecated-prop",
		Tags: []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
	})
}

func (p *AdminAnalyzer) checkUnknownComponentContractAttributes(
	startTag *twigsyntax.Node,
	components []admin.VueComponent,
	selectorAttribute,
	tagName string,
	diagnostics *[]lsp.Problem,
) {
	if startTag == nil || len(components) == 0 {
		return
	}
	knownProps := make(map[string]bool)
	propNames := make([]string, 0)
	knownEvents := make(map[string]bool)
	eventNames := make([]string, 0)
	knownModels := make(map[string]bool)
	modelNames := make([]string, 0)
	for _, component := range components {
		for _, prop := range component.Props {
			name := strings.TrimSpace(prop.Name)
			if name == "" || knownProps[name] {
				continue
			}
			knownProps[name] = true
			propNames = append(propNames, name)
		}
		for _, event := range component.ComponentEvents() {
			name := admin.CanonicalEventName(event.Name)
			if name == "" || knownEvents[name] {
				continue
			}
			knownEvents[name] = true
			eventNames = append(eventNames, name)
		}
		for _, prop := range component.Props {
			attributeName := "v-model:" + admin.CamelToKebab(prop.Name)
			if _, found := component.ComponentModel(attributeName); !found ||
				knownModels[prop.Name] {
				continue
			}
			knownModels[prop.Name] = true
			modelNames = append(modelNames, prop.Name)
		}
	}
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return
	}
	for _, attribute := range tag.Attributes() {
		nameToken := attribute.Name()
		if nameToken == nil {
			continue
		}
		attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
		if attributeName == "" || attributeName == selectorAttribute {
			continue
		}
		if attributeName == "v-bind" {
			if value, found := attribute.Value(); found {
				if inner, innerFound := value.GetInner(); innerFound {
					fields, _ := admin.VueObjectBindingFields(
						inner.Syntax().Text(), inner.Syntax().Range().Start,
					)
					for _, field := range fields {
						p.checkUnknownComponentObjectBindingField(
							field, knownProps, propNames, tagName, diagnostics,
						)
					}
				}
			}
			continue
		}
		if reference, found := admin.VueModelReferenceForAttribute(
			attributeName, nameToken.Range(),
		); found {
			if knownModels[reference.Name] {
				continue
			}
			suggestions := adminNearbySuggestions(reference.Name, modelNames)
			for index := range suggestions {
				suggestions[index] = admin.CamelToKebab(suggestions[index])
			}
			if len(suggestions) == 0 {
				continue
			}
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: reference.Range,
				Message: fmt.Sprintf(
					"Unknown model '%s' on component '%s'",
					reference.Name, tagName,
				),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.unknown-model",
				Payload: map[string]any{
					"componentName": tagName,
					"modelName":     reference.Name,
					"suggestions":   suggestions,
				},
			})
			continue
		}
		if reference, found := admin.VueEventReferenceForAttribute(
			attributeName, nameToken.Range(),
		); found {
			if knownEvents[reference.Name] ||
				isNativeAdministrationEvent(reference.Name) {
				continue
			}
			suggestions := adminNearbySuggestions(reference.Name, eventNames)
			if len(suggestions) == 0 {
				continue
			}
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: reference.Range,
				Message: fmt.Sprintf(
					"Unknown event '%s' on component '%s'",
					reference.Name, tagName,
				),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.unknown-event",
				Payload: map[string]any{
					"componentName": tagName,
					"eventName":     reference.Name,
					"suggestions":   suggestions,
				},
			})
			continue
		}
		reference, found := admin.VuePropReferenceForAttribute(
			attributeName, nameToken.Range(),
		)
		if !found || knownProps[reference.Name] ||
			isAdministrationFallthroughAttribute(
				attributeName, reference.Name,
			) {
			continue
		}
		suggestions := adminNearbySuggestions(reference.Name, propNames)
		for index := range suggestions {
			suggestions[index] = admin.CamelToKebab(suggestions[index])
		}
		if len(suggestions) == 0 {
			continue
		}
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Unknown prop '%s' on component '%s'",
				reference.Name, tagName,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-prop",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      reference.Name,
				"suggestions":   suggestions,
			},
		})
	}
}

func (p *AdminAnalyzer) checkComponentSlotNames(
	startTag *twigsyntax.Node,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	if p == nil || p.adminIndexer == nil || startTag == nil {
		return nil
	}
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return nil
	}
	var references []admin.VueAttributeReference
	for _, attribute := range tag.Attributes() {
		nameToken := attribute.Name()
		if nameToken == nil {
			continue
		}
		if reference, found := admin.VueSlotReferenceForAttribute(
			twigquery.HTMLAttributeName(attribute.Syntax()), nameToken.Range(),
		); found {
			references = append(references, reference)
		}
	}
	if len(references) == 0 {
		return nil
	}
	components, complete, err := p.adminIndexer.ResolveTwigSlotConsumerComponents(
		templatePath, startTag, liveOwner,
	)
	if err != nil || !complete || len(components) == 0 {
		return err
	}
	knownNames := make([]string, 0)
	known := make(map[string]bool)
	componentNames := make([]string, 0, len(components))
	for _, component := range components {
		componentNames = append(componentNames, component.Name)
		for _, slot := range component.Slots {
			if slot.Name == "" || known[slot.Name] {
				continue
			}
			known[slot.Name] = true
			knownNames = append(knownNames, slot.Name)
		}
	}
	owner := strings.Join(componentNames, " | ")
	for _, reference := range references {
		valid := false
		for _, component := range components {
			if _, found := component.ComponentSlot(reference.Name); found {
				valid = true
				break
			}
		}
		if valid {
			continue
		}
		suggestions := adminNearbySuggestions(reference.Name, knownNames)
		if len(suggestions) == 0 {
			continue
		}
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Unknown slot '%s' on component '%s'",
				reference.Name, owner,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-slot",
			Payload: map[string]any{
				"componentName": owner,
				"slotName":      reference.Name,
				"suggestions":   suggestions,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkUnknownComponentObjectBindingField(
	field admin.VueObjectBindingField,
	knownProps map[string]bool,
	propNames []string,
	tagName string,
	diagnostics *[]lsp.Problem,
) {
	name := admin.NormalizePropName(field.Name)
	if name == "" || knownProps[name] ||
		isAdministrationFallthroughAttribute(field.Name, name) {
		return
	}
	suggestions := adminNearbySuggestions(name, propNames)
	if strings.Contains(field.Name, "-") {
		for index := range suggestions {
			suggestions[index] = admin.CamelToKebab(suggestions[index])
		}
	}
	if len(suggestions) == 0 {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: field.NameRange,
		Message: fmt.Sprintf(
			"Unknown v-bind prop '%s' on component '%s'", name, tagName,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.unknown-prop",
		Payload: map[string]any{
			"componentName": tagName,
			"propName":      name,
			"suggestions":   suggestions,
		},
	})
}

func isAdministrationFallthroughAttribute(raw, normalized string) bool {
	raw = strings.ToLower(strings.TrimLeft(
		strings.TrimSpace(raw), ":",
	))
	raw = strings.TrimPrefix(raw, "v-bind:")
	if strings.HasPrefix(raw, "data-") || strings.HasPrefix(raw, "aria-") ||
		strings.HasPrefix(normalized, "on") && len(normalized) > 2 &&
			normalized[2] >= 'A' && normalized[2] <= 'Z' {
		return true
	}
	switch strings.ToLower(normalized) {
	case "accept", "accesskey", "action", "alt", "autocomplete", "autofocus",
		"checked", "class", "contenteditable", "controls", "crossorigin",
		"disabled", "draggable", "enctype", "for", "form", "height", "hidden",
		"href", "id", "inert", "inputmode", "is", "key", "lang", "loop",
		"max", "maxlength", "method", "min", "minlength", "multiple", "muted",
		"name", "nonce", "novalidate", "part", "pattern", "placeholder",
		"playsinline", "poster", "preload", "readonly", "ref", "rel", "required",
		"role", "rows", "selected", "size", "slot", "spellcheck", "src", "step",
		"style", "tabindex", "target", "title", "translate", "type", "value",
		"width", "wrap":
		return true
	default:
		return false
	}
}

func isNativeAdministrationEvent(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "abort", "animationcancel", "animationend", "animationiteration",
		"animationstart", "auxclick", "beforeinput", "beforetoggle", "blur",
		"cancel", "canplay", "canplaythrough", "change", "click", "close",
		"compositionend", "compositionstart", "compositionupdate", "contextmenu",
		"copy", "cuechange", "cut", "dblclick", "drag", "dragend", "dragenter",
		"dragleave", "dragover", "dragstart", "drop", "durationchange", "emptied",
		"ended", "error", "focus", "focusin", "focusout", "formdata",
		"fullscreenchange", "fullscreenerror", "gotpointercapture", "input",
		"invalid", "keydown", "keypress", "keyup", "load", "loadeddata",
		"loadedmetadata", "loadstart", "lostpointercapture", "mousedown",
		"mouseenter", "mouseleave", "mousemove", "mouseout", "mouseover",
		"mouseup", "paste", "pause", "play", "playing", "pointercancel",
		"pointerdown", "pointerenter", "pointerleave", "pointermove", "pointerout",
		"pointerover", "pointerup", "progress", "ratechange", "reset", "resize",
		"scroll", "scrollend", "securitypolicyviolation", "seeked", "seeking",
		"select", "selectionchange", "selectstart", "slotchange", "stalled",
		"submit", "suspend", "timeupdate", "toggle", "touchcancel", "touchend",
		"touchmove", "touchstart", "transitioncancel", "transitionend",
		"transitionrun", "transitionstart", "volumechange", "waiting", "wheel":
		return true
	default:
		return false
	}
}

func (p *AdminAnalyzer) checkResolvedComponentProps(
	root *twigsyntax.Node,
	content []byte,
	startTag *twigsyntax.Node,
	templatePath string,
	comp admin.VueComponent,
	tagName string,
	componentRange cst.TextRange,
	dynamicComponent bool,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	// Get the attributes present on the tag
	presentAttrs, hasUnknownObjectBinding := p.getTagAttributes(startTag)
	props := make(map[string]admin.VueComponentProp, len(comp.Props))
	for _, prop := range comp.Props {
		props[prop.Name] = prop
	}
	if dynamicComponent {
		delete(props, "is")
		delete(presentAttrs, "is")
	}
	p.checkStaticPropTypes(tagName, startTag, props, diagnostics)
	p.checkStaticPropValues(tagName, startTag, props, diagnostics)
	if err := p.checkBoundPropTypes(
		root, content, tagName, startTag, props, templatePath,
		liveOwner, diagnostics,
	); err != nil {
		return err
	}
	if err := p.checkModelBindings(
		root, content, tagName, startTag, comp, templatePath,
		liveOwner, diagnostics,
	); err != nil {
		return err
	}
	if hasUnknownObjectBinding {
		// An arbitrary v-bind expression may contain any required prop. Absence
		// cannot be proven without evaluating the component expression.
		return nil
	}

	// Check for missing required props
	for _, prop := range comp.Props {
		if !prop.Required {
			continue
		}

		// Check if prop is present (also check Vue binding variants)
		if p.isPropPresent(prop.Name, presentAttrs) ||
			componentModelPropPresent(comp, prop.Name, presentAttrs) {
			continue
		}

		// Get the tag name node for the diagnostic range
		if componentRange.Len() == 0 {
			continue
		}

		*diagnostics = append(*diagnostics, lsp.Problem{
			Range:    componentRange,
			Message:  fmt.Sprintf("Missing required prop '%s' on component '%s'", prop.Name, tagName),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.missing-required-prop",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkUnknownDynamicComponent(
	candidate admin.VueDynamicComponentCandidate,
	diagnostics *[]lsp.Problem,
) {
	if p == nil || p.adminIndexer == nil ||
		!admin.IsShopwareComponentTag(candidate.Name) {
		return
	}
	components, err := p.adminIndexer.GetAllComponentsView()
	if err != nil || len(components) == 0 {
		return
	}
	names := make([]string, 0, len(components))
	seen := make(map[string]bool, len(components))
	for _, component := range components {
		if component.Name == "" || seen[component.Name] {
			continue
		}
		seen[component.Name] = true
		names = append(names, component.Name)
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: candidate.Range,
		Message: fmt.Sprintf(
			"Administration component '%s' is not registered",
			candidate.Name,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.not-found",
		Payload: map[string]any{
			"componentName": candidate.Name,
			"suggestions":   suggestion.Similar(candidate.Name, names),
		},
	})
}

func (p *AdminAnalyzer) checkModelBindings(
	root *twigsyntax.Node,
	content []byte,
	tagName string,
	startTag *twigsyntax.Node,
	component admin.VueComponent,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	for _, attributeNode := range twigquery.Nodes(
		startTag, twigsyntax.HtmlAttribute,
	) {
		attributeName := twigquery.HTMLAttributeName(attributeNode)
		if _, modelAttribute := admin.NormalizeModelArgument(attributeName); !modelAttribute {
			continue
		}
		model, found := component.ComponentModel(attributeName)
		if !found {
			continue
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		inner, ok := value.GetInner()
		if !ok {
			continue
		}
		expressionRange := inner.Syntax().RangeTrimmedTrivia()
		if expressionRange.Len() == 0 || expressionRange.End > uint32(len(content)) {
			continue
		}
		expression := strings.TrimSpace(inner.Syntax().Text())
		if !admin.VueModelExpressionAssignable(expression) {
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: expressionRange,
				Message: fmt.Sprintf(
					"Model binding '%s' on component '%s' requires an assignable expression",
					attributeName, tagName,
				),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.model-not-assignable",
				Payload: map[string]any{
					"componentName": tagName,
					"modelName":     attributeName,
					"expression":    expression,
				},
			})
			continue
		}
		actualType, resolved, err :=
			p.adminIndexer.ResolveTwigVueExpressionTypeForComponent(
				root, content, expression, expressionRange.Start,
				templatePath, liveOwner,
			)
		if err != nil {
			return err
		}
		if !resolved {
			continue
		}
		expectedTypes := []string{model.Prop.Type}
		if payloadType := admin.VueEventPayloadType(model.Event.Type); payloadType != "" {
			expectedTypes = append(expectedTypes, payloadType)
		}
		expectedType := ""
		incompatible := false
		for _, candidate := range expectedTypes {
			if !admin.VueTypesProvablyIncompatible(candidate, actualType) {
				continue
			}
			incompatible = true
			expectedType = admin.VuePropValueType(candidate)
			break
		}
		if !incompatible {
			continue
		}
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: expressionRange,
			Message: fmt.Sprintf(
				"Model binding '%s' on component '%s' expects %s, but the expression has type %s",
				attributeName, tagName, expectedType, actualType,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.model-type",
			Payload: map[string]any{
				"componentName": tagName,
				"modelName":     attributeName,
				"propName":      model.PropName,
				"eventName":     model.EventName,
				"expectedType":  expectedType,
				"actualType":    actualType,
			},
		})
	}
	return nil
}

func componentModelPropPresent(
	component admin.VueComponent,
	propName string,
	attributes map[string]bool,
) bool {
	for attributeName := range attributes {
		argument, model := admin.NormalizeModelArgument(attributeName)
		if !model {
			continue
		}
		if argument != "" {
			prop, found := component.ComponentProp(argument)
			if found && prop.Name == propName {
				return true
			}
			continue
		}

		modelPropName := component.ModelProp
		if modelPropName == "" && component.ModelEvent != "" {
			modelPropName = "value"
		}
		if modelPropName == "" {
			if _, found := component.ComponentProp("modelValue"); found {
				modelPropName = "modelValue"
			} else if _, found := component.ComponentProp("value"); found {
				modelPropName = "value"
			}
		}
		prop, found := component.ComponentProp(modelPropName)
		if found && prop.Name == propName {
			return true
		}
	}
	return false
}

func (p *AdminAnalyzer) checkBoundPropTypes(
	root *twigsyntax.Node,
	content []byte,
	tagName string,
	startTag *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	if p == nil || p.adminIndexer == nil || root == nil {
		return nil
	}
	for _, attributeNode := range twigquery.Nodes(
		startTag, twigsyntax.HtmlAttribute,
	) {
		attributeName := twigquery.HTMLAttributeName(attributeNode)
		if attributeName == "v-bind" {
			if err := p.checkObjectBoundPropTypes(
				root, content, tagName, attributeNode, props,
				templatePath, liveOwner, diagnostics,
			); err != nil {
				return err
			}
			continue
		}
		if !strings.HasPrefix(attributeName, ":") &&
			!strings.HasPrefix(attributeName, "v-bind:") {
			continue
		}
		propName := admin.NormalizePropName(attributeName)
		prop, exists := props[propName]
		if !exists {
			continue
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		inner, ok := value.GetInner()
		if !ok {
			continue
		}
		expressionRange := inner.Syntax().RangeTrimmedTrivia()
		if expressionRange.Len() == 0 || expressionRange.End > uint32(len(content)) {
			continue
		}
		expression := strings.TrimSpace(inner.Syntax().Text())
		if actual, start, end, literal := admin.VueStaticStringLiteral(
			inner.Syntax().Text(),
		); literal {
			literalRange := cst.TextRange{
				Start: inner.Syntax().Range().Start + start,
				End:   inner.Syntax().Range().Start + end,
			}
			if problem, invalid := invalidAdminPropValueProblem(
				tagName, prop, actual, literalRange,
			); invalid {
				*diagnostics = append(*diagnostics, problem)
				continue
			}
		}
		if strings.TrimSpace(prop.Type) == "" {
			continue
		}
		actualType, resolved, err :=
			p.adminIndexer.ResolveTwigVueExpressionTypeForComponent(
				root, content, expression, expressionRange.Start,
				templatePath, liveOwner,
			)
		if err != nil {
			return err
		}
		if !resolved || !admin.VueTypesProvablyIncompatible(
			prop.Type, actualType,
		) {
			continue
		}
		expectedType := admin.VuePropValueType(prop.Type)
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: expressionRange,
			Message: fmt.Sprintf(
				"Prop '%s' on component '%s' expects %s, but the bound expression has type %s",
				prop.Name, tagName, expectedType, actualType,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.bound-prop-type",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
				"expectedType":  expectedType,
				"actualType":    actualType,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkObjectBoundPropTypes(
	root *twigsyntax.Node,
	content []byte,
	tagName string,
	attributeNode *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	attribute, ok := twigast.CastHtmlAttribute(attributeNode)
	if !ok {
		return nil
	}
	value, ok := attribute.Value()
	if !ok {
		return nil
	}
	inner, ok := value.GetInner()
	if !ok {
		return nil
	}
	fields, _ := admin.VueObjectBindingFields(
		inner.Syntax().Text(), inner.Syntax().Range().Start,
	)
	for _, field := range fields {
		propName := admin.NormalizePropName(field.Name)
		prop, exists := props[propName]
		if !exists || field.Expression == "" || field.ExpressionRange.Len() == 0 {
			continue
		}
		if actual, start, end, literal := admin.VueStaticStringLiteral(
			field.Expression,
		); literal {
			literalRange := cst.TextRange{
				Start: field.ExpressionRange.Start + start,
				End:   field.ExpressionRange.Start + end,
			}
			if problem, invalid := invalidAdminPropValueProblem(
				tagName, prop, actual, literalRange,
			); invalid {
				*diagnostics = append(*diagnostics, problem)
				continue
			}
		}
		if strings.TrimSpace(prop.Type) == "" {
			continue
		}
		actualType, resolved, err :=
			p.adminIndexer.ResolveTwigVueExpressionTypeForComponent(
				root, content, field.Expression, field.ExpressionRange.Start,
				templatePath, liveOwner,
			)
		if err != nil {
			return err
		}
		if !resolved || !admin.VueTypesProvablyIncompatible(
			prop.Type, actualType,
		) {
			continue
		}
		expectedType := admin.VuePropValueType(prop.Type)
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: field.ExpressionRange,
			Message: fmt.Sprintf(
				"Prop '%s' on component '%s' expects %s, but the v-bind field has type %s",
				prop.Name, tagName, expectedType, actualType,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.bound-prop-type",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
				"expectedType":  expectedType,
				"actualType":    actualType,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkUnknownComponent(
	tagName string,
	startTag *twigsyntax.Node,
	diagnostics *[]lsp.Problem,
) {
	if p == nil || p.adminIndexer == nil ||
		!admin.IsShopwareComponentTag(tagName) {
		return
	}
	components, err := p.adminIndexer.GetAllComponentsView()
	if err != nil || len(components) == 0 {
		// During an initial or partial index there is not enough evidence to
		// claim that a registry-owned tag is missing.
		return
	}
	names := make([]string, 0, len(components))
	seen := make(map[string]struct{}, len(components))
	for _, component := range components {
		name := strings.TrimSpace(component.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	nameNode := p.getTagNameNode(startTag)
	if nameNode == nil {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: nameNode.Range(),
		Message: fmt.Sprintf(
			"Administration component '%s' is not registered",
			tagName,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.not-found",
		Payload: map[string]any{
			"componentName": tagName,
			"suggestions":   suggestion.Similar(tagName, names),
		},
	})
}

func (p *AdminAnalyzer) checkStaticPropTypes(
	tagName string,
	startTag *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	diagnostics *[]lsp.Problem,
) {
	for _, attribute := range twigquery.Nodes(startTag, twigsyntax.HtmlAttribute) {
		attributeName := twigquery.HTMLAttributeName(attribute)
		if strings.HasPrefix(attributeName, ":") ||
			strings.HasPrefix(attributeName, "v-bind:") {
			continue
		}
		propName := admin.NormalizePropName(attributeName)
		prop, exists := props[propName]
		if !exists || !staticPropNeedsBinding(prop, attributeName, attribute.Text()) {
			continue
		}
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: attribute.RangeTrimmedTrivia(),
			Message: fmt.Sprintf(
				"Prop '%s' on component '%s' expects %s; bind the value with ':'",
				prop.Name,
				tagName,
				prop.Type,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.static-prop-type",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
				"attributeName": attributeName,
			},
		})
	}
}

func (p *AdminAnalyzer) checkStaticPropValues(
	tagName string,
	startTag *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	diagnostics *[]lsp.Problem,
) {
	for _, attributeNode := range twigquery.Nodes(
		startTag, twigsyntax.HtmlAttribute,
	) {
		attributeName := twigquery.HTMLAttributeName(attributeNode)
		if attributeName == "" || strings.HasPrefix(attributeName, ":") ||
			strings.HasPrefix(attributeName, "v-") ||
			strings.HasPrefix(attributeName, "@") ||
			strings.HasPrefix(attributeName, "#") {
			continue
		}
		prop, exists := props[admin.NormalizePropName(attributeName)]
		if !exists {
			continue
		}
		allowed, complete := admin.VuePropAllowedValues(prop)
		if !complete || len(allowed) == 0 {
			continue
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		actual := ""
		var valueRange cst.TextRange
		if inner, innerOK := value.GetInner(); innerOK {
			actual = inner.Syntax().Text()
			valueRange = inner.Syntax().Range()
		} else {
			opening := value.GetOpeningQuote()
			closing := value.GetClosingQuote()
			if opening == nil || closing == nil {
				continue
			}
			valueRange = cst.TextRange{
				Start: opening.Range().End, End: closing.Range().Start,
			}
		}
		if strings.Contains(actual, "{{") || strings.Contains(actual, "{%") ||
			strings.Contains(actual, "{#") {
			continue
		}
		valid := false
		for _, candidate := range allowed {
			if actual == candidate {
				valid = true
				break
			}
		}
		if valid {
			continue
		}
		problem, _ := invalidAdminPropValueProblem(
			tagName, prop, actual, valueRange,
		)
		*diagnostics = append(*diagnostics, problem)
	}
}

func invalidAdminPropValueProblem(
	tagName string,
	prop admin.VueComponentProp,
	actual string,
	rangeValue cst.TextRange,
) (lsp.Problem, bool) {
	allowed, complete := admin.VuePropAllowedValues(prop)
	if !complete || len(allowed) == 0 {
		return lsp.Problem{}, false
	}
	for _, candidate := range allowed {
		if actual == candidate {
			return lsp.Problem{}, false
		}
	}
	return lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Prop '%s' on component '%s' does not accept value %q",
			prop.Name, tagName, actual,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.invalid-prop-value",
		Payload: map[string]any{
			"componentName": tagName,
			"propName":      prop.Name,
			"value":         actual,
			"allowedValues": allowed,
			"suggestions":   suggestion.Similar(actual, allowed),
		},
	}, true
}

func staticPropNeedsBinding(
	prop admin.VueComponentProp,
	attributeName,
	attributeText string,
) bool {
	propType := strings.ToLower(strings.TrimSpace(prop.Type))
	if propType == "" || strings.Contains(propType, "string") {
		return false
	}
	if !strings.Contains(attributeText, "=") {
		return false
	}
	switch {
	case strings.Contains(propType, "boolean"):
		value := strings.TrimSpace(attributeText[strings.IndexByte(attributeText, '=')+1:])
		value = strings.Trim(value, "'\"")
		return value != "" && value != attributeName
	case strings.Contains(propType, "number"),
		strings.Contains(propType, "array"),
		strings.Contains(propType, "object"),
		strings.Contains(propType, "function"):
		return true
	default:
		return false
	}
}

// getTagName extracts the tag name from an html_start_tag node
func (p *AdminAnalyzer) getTagName(startTag *twigsyntax.Node) string {
	return twigquery.HTMLTagName(startTag)
}

// getTagNameNode returns the html_tag_name node from an html_start_tag
func (p *AdminAnalyzer) getTagNameNode(startTag *twigsyntax.Node) *twigsyntax.Token {
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return nil
	}
	return tag.Name()
}

// getTagAttributes extracts all attribute names from an html_start_tag
func (p *AdminAnalyzer) getTagAttributes(
	startTag *twigsyntax.Node,
) (map[string]bool, bool) {
	attrs := make(map[string]bool)
	hasUnknownObjectBinding := false

	for _, attribute := range twigquery.Nodes(startTag, twigsyntax.HtmlAttribute) {
		attrName := p.getAttributeName(attribute)
		if attrName != "" {
			attrs[attrName] = true
		}
		if attrName != "v-bind" {
			continue
		}
		value, ok := twigast.CastHtmlAttribute(attribute)
		if !ok {
			continue
		}
		attributeValue, ok := value.Value()
		if !ok {
			continue
		}
		inner, ok := attributeValue.GetInner()
		if !ok {
			continue
		}
		names, complete := admin.VueObjectBindingNames(
			strings.TrimSpace(inner.Syntax().Text()),
		)
		for _, name := range names {
			attrs[name] = true
		}
		if !complete {
			hasUnknownObjectBinding = true
		}
	}

	return attrs, hasUnknownObjectBinding
}

// getAttributeName extracts the attribute name from an html_attribute node
func (p *AdminAnalyzer) getAttributeName(attrNode *twigsyntax.Node) string {
	return twigquery.HTMLAttributeName(attrNode)
}

// isPropPresent checks if a prop is present in the attributes
// It checks for the prop name directly, as well as Vue binding variants (:prop, v-bind:prop)
// Also handles camelCase to kebab-case conversion (positionIdentifier -> position-identifier)
func (p *AdminAnalyzer) isPropPresent(propName string, attrs map[string]bool) bool {
	// Get both camelCase and kebab-case versions
	kebabName := camelToKebab(propName)

	// Check both variants
	namesToCheck := []string{propName, kebabName}

	for _, name := range namesToCheck {
		// Direct attribute
		if attrs[name] {
			return true
		}

		// Vue shorthand binding :propName
		if attrs[":"+name] {
			return true
		}

		// Vue v-bind:propName
		if attrs["v-bind:"+name] {
			return true
		}
	}

	return false
}

// camelToKebab converts camelCase to kebab-case (delegates to shared function)
func camelToKebab(s string) string {
	return admin.CamelToKebab(s)
}
