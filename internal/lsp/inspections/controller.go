package inspections

import (
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

const controllerMethodFixID lsp.FixID = "create-controller-method"

func NewController(
	routeIndex *symfony.RouteIndexer,
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) lsp.Inspection {
	fix := phpMethodFix{
		id:          controllerMethodFixID,
		titlePrefix: "controller",
		phpIndex:    phpIndex,
	}
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID: "symfony.routing",
			Languages: []language.ID{
				language.PHP,
				language.YAML,
				language.XML,
				language.Twig,
			},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.controller.target.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.controller.method.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.controller.deprecated", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "symfony.route.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityError},
			},
		},
		analyzers: []ProblemAnalyzer{
			diagnostics.NewControllerAnalyzer(serviceIndex, phpIndex),
			diagnostics.NewRouteAnalyzer(routeIndex, serviceIndex, phpIndex),
		},
		fixes: []lsp.QuickFix{fix, suggestionFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			bound := suggestionBoundFixes(payload)
			if string(code) != "symfony.controller.method.missing" {
				return bound
			}
			method := phpMethodPayload{
				ClassName:  mapString(payload, "className"),
				MethodName: mapString(payload, "methodName"),
				Parameters: mapStrings(payload, "routeParameters"),
			}
			if method.ClassName == "" || method.MethodName == "" {
				return bound
			}
			return append(bound, lsp.BindFix(controllerMethodFixID, method))
		},
	}
}

func mapString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func mapStrings(values map[string]any, key string) []string {
	items, ok := values[key].([]any)
	if !ok {
		if typed, typedOK := values[key].([]string); typedOK {
			return append([]string(nil), typed...)
		}
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok && value != "" {
			result = append(result, value)
		}
	}
	return result
}
