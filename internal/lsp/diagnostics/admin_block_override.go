package diagnostics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

const (
	AdminBlockOverrideNestedCode      lsp.DiagnosticID = "admin.block-override.nested"
	AdminBlockOverrideConditionalCode lsp.DiagnosticID = "admin.block-override.conditional"
	AdminBlockOverrideRepeatedCode    lsp.DiagnosticID = "admin.block-override.repeated"
	AdminBlockParentConditionalCode   lsp.DiagnosticID = "admin.block-parent.conditional"
	AdminBlockParentRepeatedCode      lsp.DiagnosticID = "admin.block-parent.repeated"
)

type AdminBlockOverrideAnalyzer struct{}

func NewAdminBlockOverrideAnalyzer() *AdminBlockOverrideAnalyzer {
	return &AdminBlockOverrideAnalyzer{}
}

type adminBlockMarkupKind uint8

const (
	adminBlockMarkupOther adminBlockMarkupKind = iota
	adminBlockMarkupOverride
	adminBlockMarkupParent
)

type adminBlockControl uint8

const (
	adminBlockConditional adminBlockControl = 1 << iota
	adminBlockRepeated
)

type adminBlockMarkupTag struct {
	node      *twigsyntax.Node
	nameRange cst.TextRange
	kind      adminBlockMarkupKind
	control   adminBlockControl
}

func (*AdminBlockOverrideAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		!strings.Contains(filepath.ToSlash(document.URI), "Resources/app/administration") {
		return []lsp.Problem{}, nil
	}
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".twig", ".vue":
	default:
		return []lsp.Problem{}, nil
	}

	tagsByNode := make(map[*twigsyntax.Node]adminBlockMarkupTag)
	var targets []adminBlockMarkupTag
	for node := range twigquery.IterateNodes(
		document.SyntaxTree.Root,
		twigsyntax.HtmlStartingTag,
	) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tag, ok := adminBlockMarkupTagAt(node)
		if !ok {
			continue
		}
		tagsByNode[tag.node] = tag
		if tag.kind != adminBlockMarkupOther {
			targets = append(targets, tag)
		}
	}

	problems := make([]lsp.Problem, 0, len(targets))
	for _, tag := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		control, nested := adminBlockAncestorContext(tag, tagsByNode)
		control |= tag.control
		switch tag.kind {
		case adminBlockMarkupOverride:
			if nested {
				problems = append(problems, adminBlockMarkupProblem(
					tag,
					AdminBlockOverrideNestedCode,
					"Nested <sw-block extends> declarations are not supported",
				))
			}
			if control&adminBlockConditional != 0 {
				problems = append(problems, adminBlockMarkupProblem(
					tag,
					AdminBlockOverrideConditionalCode,
					"Conditional <sw-block extends> declarations are not supported; override blocks must be statically present",
				))
			}
			if control&adminBlockRepeated != 0 {
				problems = append(problems, adminBlockMarkupProblem(
					tag,
					AdminBlockOverrideRepeatedCode,
					"Repeated <sw-block extends> declarations are not supported; override blocks must not be placed in a loop",
				))
			}
		case adminBlockMarkupParent:
			if control&adminBlockConditional != 0 {
				problems = append(problems, adminBlockMarkupProblem(
					tag,
					AdminBlockParentConditionalCode,
					"Conditional <sw-block-parent> usage is not supported; parent rendering must be statically present",
				))
			}
			if control&adminBlockRepeated != 0 {
				problems = append(problems, adminBlockMarkupProblem(
					tag,
					AdminBlockParentRepeatedCode,
					"Repeated <sw-block-parent> usage is not supported; parent rendering must not be placed in a loop",
				))
			}
		}
	}
	return problems, nil
}

func adminBlockMarkupTagAt(
	startNode *twigsyntax.Node,
) (adminBlockMarkupTag, bool) {
	starting, ok := twigast.CastHtmlStartingTag(startNode)
	if !ok || starting.Name() == nil {
		return adminBlockMarkupTag{}, false
	}
	tag, ok := starting.HtmlTag()
	if !ok {
		return adminBlockMarkupTag{}, false
	}
	control, hasExtends := adminBlockMarkupAttributes(starting)
	kind := adminBlockMarkupOther
	switch strings.ToLower(starting.Name().Text()) {
	case "sw-block":
		if hasExtends {
			kind = adminBlockMarkupOverride
		}
	case "sw-block-parent":
		kind = adminBlockMarkupParent
	}
	return adminBlockMarkupTag{
		node: tag.Syntax(), nameRange: starting.Name().Range(),
		kind: kind, control: control,
	}, true
}

func adminBlockMarkupAttributes(
	starting twigast.HtmlStartingTag,
) (adminBlockControl, bool) {
	var control adminBlockControl
	hasExtends := false
	for attribute := range starting.Attributes() {
		name := strings.ToLower(twigquery.HTMLAttributeName(attribute.Syntax()))
		switch name {
		case "extends":
			hasExtends = true
		case "v-if", "v-else-if", "v-else":
			control |= adminBlockConditional
		case "v-for":
			control |= adminBlockRepeated
		}
	}
	return control, hasExtends
}

func adminBlockAncestorContext(
	tag adminBlockMarkupTag,
	tagsByNode map[*twigsyntax.Node]adminBlockMarkupTag,
) (adminBlockControl, bool) {
	var control adminBlockControl
	nested := false
	for ancestor := tag.node.Parent(); ancestor != nil; ancestor = ancestor.Parent() {
		switch ancestor.Kind() {
		case twigsyntax.HtmlTag:
			parent, ok := tagsByNode[ancestor]
			if !ok {
				continue
			}
			control |= parent.control
			if parent.kind == adminBlockMarkupOverride {
				nested = true
			}
		case twigsyntax.TwigIf, twigsyntax.TwigIfBlock:
			control |= adminBlockConditional
		case twigsyntax.TwigFor, twigsyntax.TwigForBlock, twigsyntax.TwigForElseBlock:
			control |= adminBlockRepeated
		}
	}
	return control, nested
}

func adminBlockMarkupProblem(
	tag adminBlockMarkupTag,
	id lsp.DiagnosticID,
	message string,
) lsp.Problem {
	return lsp.Problem{
		ID: id, Range: tag.nameRange, Message: message,
		Source: "shopware-lsp", Severity: protocol.DiagnosticSeverityError,
	}
}
