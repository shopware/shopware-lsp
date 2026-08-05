package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingTwigComponentCode  lsp.DiagnosticID = "twig.component.missing"
	missingComponentBlockCode lsp.DiagnosticID = "twig.component.block.missing"
	missingLiveActionCode     lsp.DiagnosticID = "twig.component.live_action.missing"
	missingLiveArgumentCode   lsp.DiagnosticID = "twig.component.live_argument.missing"
	mixedComponentSyntaxCode  lsp.DiagnosticID = "twig.component.mixed_syntax"
	componentSelfImportCode   lsp.DiagnosticID = "twig.component.self_macro_import"
)

type TwigComponentAnalyzer struct {
	index *twigcomponent.Index
}

func NewTwigComponentAnalyzer(
	index *twigcomponent.Index,
) *TwigComponentAnalyzer {
	return &TwigComponentAnalyzer{index: index}
}

func (p *TwigComponentAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".twig") {
		return nil, nil
	}
	names, err := p.index.Names()
	if err != nil {
		return nil, err
	}
	available := make(map[string]struct{}, len(names))
	for _, name := range names {
		available[name] = struct{}{}
	}
	path, _ := uriutil.Path(document.URI)
	var result []lsp.Problem
	for _, usage := range twigcomponent.UsagesInTwig(
		path,
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		if _, found := available[usage.Name]; found {
			continue
		}
		result = append(result, lsp.Problem{
			Range: usage.Range,
			Message: fmt.Sprintf(
				"Twig component '%s' not found",
				usage.Name,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "twig",
			ID:       missingTwigComponentCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					usage.Name,
					names,
				),
			},
		})
	}
	for _, usage := range twigcomponent.BlockUsagesInTwig(
		document.SyntaxTree.Root,
	) {
		if _, componentFound := available[usage.Component]; !componentFound {
			continue
		}
		blocks, blockErr := p.index.Blocks(usage.Component)
		if blockErr != nil {
			return nil, blockErr
		}
		var candidates []string
		found := false
		for _, block := range blocks {
			candidates = append(candidates, block.Name)
			if block.Name == usage.Name {
				found = true
			}
		}
		if found {
			continue
		}
		result = append(result, lsp.Problem{
			Range: usage.Range,
			Message: fmt.Sprintf(
				"Block '%s' not found in Twig component '%s'",
				usage.Name,
				usage.Component,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "twig",
			ID:       missingComponentBlockCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					usage.Name,
					uniqueComponentBlockNames(candidates),
				),
			},
		})
	}
	components, componentErr := p.index.ComponentsForTemplate(path)
	if componentErr != nil {
		return nil, componentErr
	}
	live := false
	for _, component := range components {
		if component.Live {
			live = true
			break
		}
	}
	if live {
		actions, actionErr := p.index.LiveActionsForTemplate(path)
		if actionErr != nil {
			return nil, actionErr
		}
		actionNames := make([]string, 0, len(actions))
		for _, action := range actions {
			actionNames = append(actionNames, action.Name)
		}
		for _, reference := range twigcomponent.LiveActionReferencesInTwig(
			path,
			document.SyntaxTree.Root,
		) {
			if reference.Name == "" ||
				containsFold(actionNames, reference.Name) {
				continue
			}
			result = append(result, lsp.Problem{
				Range: reference.Range,
				Message: fmt.Sprintf(
					"Live Action '%s' not found on this component",
					reference.Name,
				),
				Severity: protocol.DiagnosticSeverityWarning,
				Source:   "twig",
				ID:       missingLiveActionCode,
				Payload: map[string]any{
					"suggestions": suggestion.Similar(
						reference.Name,
						actionNames,
					),
				},
			})
		}
		for _, reference := range twigcomponent.LiveActionArgumentReferencesInTwig(
			path,
			document.SyntaxTree.Root,
		) {
			var parameters []twigcomponent.LiveActionParameter
			for _, action := range actions {
				if strings.EqualFold(action.Name, reference.Action) {
					parameters = append(parameters, action.Parameters...)
				}
			}
			if len(parameters) == 0 {
				continue
			}
			names := make([]string, 0, len(parameters))
			found := false
			for _, parameter := range parameters {
				names = append(names, parameter.Name)
				if strings.EqualFold(parameter.Name, reference.Name) {
					found = true
				}
			}
			if found {
				continue
			}
			suggestions := suggestion.Similar(reference.Name, names)
			for index := range suggestions {
				suggestions[index] = liveArgumentAttributeSegment(
					suggestions[index],
				)
			}
			result = append(result, lsp.Problem{
				Range: reference.Range,
				Message: fmt.Sprintf(
					"Live Action '%s' has no argument named '%s'",
					reference.Action,
					reference.Name,
				),
				Severity: protocol.DiagnosticSeverityWarning,
				Source:   "twig",
				ID:       missingLiveArgumentCode,
				Payload: map[string]any{
					"suggestions": suggestions,
				},
			})
		}
	}
	result = append(
		result,
		mixedComponentSyntaxDiagnostics(document)...,
	)
	result = append(
		result,
		componentSelfImportDiagnostics(document)...,
	)
	return result, nil
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

func liveArgumentAttributeSegment(value string) string {
	var result strings.Builder
	for index, char := range value {
		if char >= 'A' && char <= 'Z' {
			if index != 0 {
				result.WriteByte('-')
			}
			result.WriteByte(byte(char - 'A' + 'a'))
			continue
		}
		if char == '_' {
			result.WriteByte('-')
			continue
		}
		result.WriteRune(char)
	}
	return strings.Trim(result.String(), "-")
}

func uniqueComponentBlockNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func mixedComponentSyntaxDiagnostics(
	document *lsp.TextDocument,
) []lsp.Problem {
	var result []lsp.Problem
	for _, node := range twigquery.Nodes(
		document.SyntaxTree.Root,
		twigsyntax.TwigBlock,
	) {
		block, ok := twigast.CastTwigBlock(node)
		if !ok || !insideHTMLComponent(node) || block.Name() == nil {
			continue
		}
		result = append(result, lsp.Problem{
			Range: block.Name().Range(),
			Message: "Cannot use Twig block syntax inside an HTML " +
				"component; use <twig:block name=\"...\"> instead",
			Severity: protocol.DiagnosticSeverityError,
			Source:   "twig",
			ID:       mixedComponentSyntaxCode,
		})
	}
	return result
}

func insideHTMLComponent(node *twigsyntax.Node) bool {
	for current := node.Parent(); current != nil; current = current.Parent() {
		tag, ok := twigast.CastHtmlTag(current)
		if ok && tag.IsTwigComponent() {
			return true
		}
	}
	return false
}

func componentSelfImportDiagnostics(
	document *lsp.TextDocument,
) []lsp.Problem {
	var result []lsp.Problem
	for _, from := range twigquery.Nodes(
		document.SyntaxTree.Root,
		twigsyntax.TwigFrom,
	) {
		source := firstTwigLiteralName(from)
		if source == nil || strings.TrimSpace(source.Text()) != "_self" ||
			!insideTwigComponent(from) {
			continue
		}
		result = append(result, lsp.Problem{
			Range: source.RangeTrimmedTrivia(),
			Message: "Cannot use '_self' to import macros inside a Twig " +
				"component. Use the full template path instead.",
			Severity: protocol.DiagnosticSeverityError,
			Source:   "twig",
			ID:       componentSelfImportCode,
		})
	}
	return result
}

func firstTwigLiteralName(node *twigsyntax.Node) *twigsyntax.Node {
	names := twigquery.Nodes(node, twigsyntax.TwigLiteralName)
	if len(names) == 0 {
		return nil
	}
	return names[0]
}

func insideTwigComponent(node *twigsyntax.Node) bool {
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == twigsyntax.TwigComponent {
			return true
		}
		tag, ok := twigast.CastHtmlTag(current)
		if ok && tag.IsTwigComponent() {
			return true
		}
	}
	return false
}
