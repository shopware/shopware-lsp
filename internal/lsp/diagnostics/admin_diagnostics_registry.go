package diagnostics

import (
	"fmt"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/suggestion"
)

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
