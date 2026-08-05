package inspections

import (
	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

const eventMethodFixID lsp.FixID = "create-event-listener-method"

func NewEvent(
	index *event.Index,
	phpIndex *php.PHPIndex,
	serviceIndex *symfony.ServiceIndex,
) lsp.Inspection {
	methodFix := phpMethodFix{
		id:          eventMethodFixID,
		titlePrefix: "event listener",
		phpIndex:    phpIndex,
	}
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID: "symfony.event",
			Languages: []language.ID{
				language.PHP,
				language.XML,
				language.YAML,
			},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.event.listener_method.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.event.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewEventAnalyzer(index, phpIndex, serviceIndex),
		fixes:    []lsp.QuickFix{suggestionFix{}, methodFix},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			bound := suggestionBoundFixes(payload)
			if string(code) != "symfony.event.listener_method.missing" {
				return bound
			}
			method := phpMethodPayload{
				ClassName:  mapString(payload, "className"),
				MethodName: mapString(payload, "methodName"),
			}
			types := mapStrings(payload, "eventTypes")
			if len(types) != 0 {
				method.TypedParameters = []phpMethodParameter{{
					Name:  "event",
					Types: types,
				}}
			}
			if method.ClassName == "" || method.MethodName == "" {
				return bound
			}
			return append(bound, lsp.BindFix(eventMethodFixID, method))
		},
	}
}
