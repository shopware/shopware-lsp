package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const formClassConstantFixID lsp.FixID = "replace-form-alias-with-class-constant"

type formClassPayload struct {
	ClassName string `json:"className"`
}

func NewForm(index *form.Index, phpIndex *php.PHPIndex) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "symfony.form",
			Languages: []language.ID{language.PHP, language.Twig},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.form.field.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.form.option.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.form.type.legacy_alias", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "symfony.form.type.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.form.view_var.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewFormAnalyzer(index, phpIndex),
		fixes:    []lsp.QuickFix{suggestionFix{}, formClassConstantFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			bound := suggestionBoundFixes(payload)
			if string(code) != "symfony.form.type.legacy_alias" {
				return bound
			}
			className := mapString(payload, "className")
			if className == "" {
				return bound
			}
			return append(bound, lsp.BindFix(
				formClassConstantFixID,
				formClassPayload{ClassName: className},
			))
		},
	}
}

type formClassConstantFix struct{}

func (formClassConstantFix) ID() lsp.FixID { return formClassConstantFixID }

func (formClassConstantFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[formClassPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Symfony: Use class constant",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.ClassName != "", err
}

func (formClassConstantFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[formClassPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	literal := ancestorNode(element, phpsyntax.PhpString)
	if literal == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("form alias string is no longer available")
	}
	qualifier, importOffset, importText := phpClassReferenceRewrite(
		fixContext.Document.SyntaxTree.Root,
		payload.ClassName,
	)
	if qualifier == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("form class name is invalid")
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.ReplaceRange(
		literal.RangeTrimmedTrivia(),
		qualifier+"::class",
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if importText != "" {
		if err := builder.Insert(importOffset, importText); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
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

func phpClassReferenceRewrite(
	root *phpsyntax.Node,
	className string,
) (qualifier string, importOffset uint32, importText string) {
	className = strings.Trim(className, "\\")
	if className == "" {
		return "", 0, ""
	}
	lastSeparator := strings.LastIndex(className, "\\")
	shortName := className
	classNamespace := ""
	if lastSeparator >= 0 {
		shortName = className[lastSeparator+1:]
		classNamespace = className[:lastSeparator]
	}
	if root == nil {
		return "\\" + className, 0, ""
	}
	conflict := false
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind != phpresolver.ClassImport {
				continue
			}
			if strings.EqualFold(strings.Trim(imported.Target, "\\"), className) {
				return imported.Alias, 0, ""
			}
			if strings.EqualFold(imported.Alias, shortName) {
				conflict = true
			}
		}
	}
	if conflict {
		return "\\" + className, 0, ""
	}
	if strings.EqualFold(strings.Trim(phpquery.Namespace(root), "\\"), classNamespace) {
		return shortName, 0, ""
	}
	importOffset = phpImportInsertionOffset(root)
	importText = "\nuse " + className + ";"
	if len(phpquery.UseDeclarations(root)) == 0 {
		importText = "\n\nuse " + className + ";"
	}
	return shortName, importOffset, importText
}

func phpImportInsertionOffset(root *phpsyntax.Node) uint32 {
	var result uint32
	for _, declaration := range phpquery.UseDeclarations(root) {
		if declaration.Range().End > result {
			result = declaration.Range().End
		}
	}
	if result != 0 {
		return result
	}
	namespaces := phpquery.Nodes(root, phpsyntax.PhpNamespace)
	if len(namespaces) != 0 {
		namespace := namespaces[0]
		if end := strings.IndexAny(namespace.Text(), ";{"); end >= 0 {
			return namespace.Range().Start + uint32(end+1)
		}
	}
	if openTag := root.FirstToken(); openTag != nil && openTag.Kind() == phpsyntax.TkOpenTag {
		return openTag.Range().End
	}
	return 0
}
