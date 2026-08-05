package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const invokableReturnTypeFixID lsp.FixID = "add-invokable-command-return-type"

func NewInvokableCommand(phpIndex *php.PHPIndex) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "symfony.console.invoke",
			Languages: []language.ID{language.PHP},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.console.invoke.return_type", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "symfony.console.invoke.return_value", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewInvokableCommandAnalyzer(phpIndex),
		fixes:    []lsp.QuickFix{invokableReturnTypeFix{}},
		bind: func(code lsp.DiagnosticID, _ map[string]any) []lsp.BoundFix {
			if string(code) != "symfony.console.invoke.return_type" {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(invokableReturnTypeFixID, struct{}{})}
		},
	}
}

type invokableReturnTypeFix struct{}

func (invokableReturnTypeFix) ID() lsp.FixID { return invokableReturnTypeFixID }

func (invokableReturnTypeFix) Present(
	_ context.Context,
	_ lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	return lsp.FixPresentation{
		Title:      "Symfony: Use int return type for command __invoke()",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, true, nil
}

func (invokableReturnTypeFix) Build(
	_ context.Context,
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
	method := ancestorNode(element, phpsyntax.PhpMethodDeclaration)
	if method == nil || phpquery.MethodName(method) != "__invoke" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("command __invoke() method is no longer available")
	}
	rng, newText, ok := invokableReturnTypeEdit(method)
	if !ok {
		return rewrite.WorkspacePlan{}, fmt.Errorf("command return type cannot be edited")
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

func ancestorNode(element cst.Element, kind cst.Kind) *cst.Node {
	for current := element; current != nil; current = current.Parent() {
		if node, ok := current.(*cst.Node); ok && node.Kind() == kind {
			return node
		}
	}
	return nil
}

func invokableReturnTypeEdit(method *cst.Node) (cst.TextRange, string, bool) {
	for child := range method.ChildNodes() {
		switch child.Kind() {
		case phpsyntax.PhpType, phpsyntax.PhpNullableType,
			phpsyntax.PhpUnionType, phpsyntax.PhpIntersectionType:
			return child.RangeTrimmedTrivia(), "int", true
		}
	}
	parameters := phpquery.DirectChild(method, phpsyntax.PhpParameterList)
	if parameters == nil {
		return cst.TextRange{}, "", false
	}
	offset := parameters.RangeTrimmedTrivia().End
	return cst.TextRange{Start: offset, End: offset}, ": int", true
}
