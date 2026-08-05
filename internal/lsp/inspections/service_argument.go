package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

const (
	serviceArgumentsFixID lsp.FixID = "add-missing-service-arguments"
	serviceMethodFixID    lsp.FixID = "create-service-method"
)

func NewServiceArgument(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) lsp.Inspection {
	argumentFix := serviceArgumentFix{}
	methodFix := phpMethodFix{
		id:          serviceMethodFixID,
		titlePrefix: "service",
		phpIndex:    phpIndex,
	}
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID: "symfony.service.arguments",
			Languages: []language.ID{
				language.PHP,
				language.XML,
				language.YAML,
			},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.service.arguments.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.service.method.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.service.argument.type", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.service.named_argument.unknown", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewServiceArgumentAnalyzer(serviceIndex, phpIndex),
		fixes:    []lsp.QuickFix{argumentFix, methodFix, suggestionFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			bound := suggestionBoundFixes(payload)
			switch string(code) {
			case "symfony.service.arguments.missing":
				if _, found := payload["missingArguments"]; found {
					return append(bound, lsp.BindFix(serviceArgumentsFixID, struct{}{}))
				}
			case "symfony.service.method.missing":
				method := phpMethodPayload{
					ClassName:  mapString(payload, "className"),
					MethodName: mapString(payload, "methodName"),
				}
				if method.ClassName != "" && method.MethodName != "" {
					return append(bound, lsp.BindFix(serviceMethodFixID, method))
				}
			}
			return bound
		},
	}
}

type serviceArgumentFix struct{}

func (serviceArgumentFix) ID() lsp.FixID { return serviceArgumentsFixID }

func (serviceArgumentFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeProblemPayload[map[string]any](fixContext)
	if err != nil || payload["format"] == nil || payload["missingArguments"] == nil {
		return lsp.FixPresentation{}, false, err
	}
	return lsp.FixPresentation{
		Title:      "Symfony: Add missing service arguments",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, true, nil
}

func (serviceArgumentFix) Build(
	ctx context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	node, ok := element.(*cst.Node)
	if !ok {
		node = element.Parent()
	}
	if node == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("service definition anchor has no node")
	}
	payload, err := lsp.DecodeProblemPayload[map[string]any](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	arguments := missingServiceArguments(payload["missingArguments"])
	format, _ := payload["format"].(string)
	if len(arguments) == 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("missing service arguments are no longer available")
	}
	rng, newText, ok := serviceArgumentRewrite(
		fixContext.Document,
		node,
		format,
		arguments,
	)
	if !ok {
		return rewrite.WorkspacePlan{}, fmt.Errorf("service argument edit is no longer applicable")
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.ReplaceRange(rng, newText); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			fixContext.Document.URI,
			&version,
			fixContext.Document.Source,
			edits,
		),
	}}, nil
}
