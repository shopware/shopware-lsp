package codeaction

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

var routeActionParameters = []phpSuggestedParameter{
	{
		className:    "Symfony\\Component\\HttpFoundation\\Request",
		variableName: "request",
	},
	{
		className: "Symfony\\Component\\Security\\Core\\User\\" +
			"UserInterface",
		variableName: "user",
	},
}

// RouteActionParameterCodeActionProvider ports the Symfony plugin's
// chooser-based RouteActionParameterIntention to one LSP action per available
// parameter type.
type RouteActionParameterCodeActionProvider struct{}

func NewRouteActionParameterCodeActionProvider() *RouteActionParameterCodeActionProvider {
	return &RouteActionParameterCodeActionProvider{}
}

func (p *RouteActionParameterCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *RouteActionParameterCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || request == nil ||
		request.CodeActionParams == nil || request.Document == nil ||
		request.Root == nil || request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	method := phpquery.MethodAt(request.Node)
	class := phpquery.ClassAt(method)
	if method == nil || class == nil {
		return nil
	}
	visibility := phpquery.DeclarationVisibility(method)
	if visibility != "" && visibility != "public" {
		return nil
	}
	resolver := php.NewNameResolver(request.Root)
	if !isPHPRouteAction(method, class, resolver) {
		return nil
	}

	existingTypes := phpExistingParameterTypes(method, resolver)
	var result []protocol.CodeAction
	for _, parameter := range routeActionParameters {
		if _, found := existingTypes[strings.ToLower(parameter.className)]; found {
			continue
		}
		qualifier, importEdit := phpClassQualifier(
			request,
			parameter.className,
		)
		parameterEdit, found := phpRequiredParameterEdit(
			request,
			class,
			method,
			qualifier,
			parameter.variableName,
		)
		if !found {
			continue
		}
		edits := []protocol.TextEdit{parameterEdit}
		if importEdit != nil {
			edits = append(edits, *importEdit)
		}
		result = append(result, protocol.CodeAction{
			Title: "Symfony: Add " + phpClassShortName(
				parameter.className,
			) + " parameter to route action",
			Kind: protocol.CodeActionRefactorRewrite,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[string][]protocol.TextEdit{
					request.TextDocument.URI: edits,
				},
			},
		})
	}
	return result
}

func isPHPRouteAction(
	method,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	if hasPHPRouteMarker(method, resolver) {
		return true
	}
	return strings.EqualFold(phpquery.MethodName(method), "__invoke") &&
		hasPHPRouteMarker(class, resolver)
}

func hasPHPRouteMarker(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	return hasResolvedPHPAttribute(
		node,
		resolver,
		routeAttributeClasses...,
	) || phpDocHasResolvedAnnotation(
		leadingPHPDocComment(node),
		resolver,
		routeAttributeClasses...,
	)
}

func leadingPHPDocComment(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	rng := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	if trimmed.Start <= rng.Start {
		return ""
	}
	prefixLength := int(trimmed.Start - rng.Start)
	text := node.Text()
	if prefixLength > len(text) {
		prefixLength = len(text)
	}
	prefix := text[:prefixLength]
	start := strings.LastIndex(prefix, "/**")
	if start < 0 {
		return ""
	}
	end := strings.Index(prefix[start:], "*/")
	if end < 0 {
		return ""
	}
	end += start + 2
	if strings.TrimSpace(prefix[end:]) != "" {
		return ""
	}
	return prefix[start:end]
}

func phpDocHasResolvedAnnotation(
	documentation string,
	resolver *php.NameResolver,
	targets ...string,
) bool {
	documentation = strings.TrimSpace(documentation)
	documentation = strings.TrimPrefix(documentation, "/**")
	documentation = strings.TrimSuffix(documentation, "*/")
	for _, line := range strings.Split(documentation, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if !strings.HasPrefix(line, "@") {
			continue
		}
		start := 1
		end := start
		for end < len(line) && isPHPAnnotationNameByte(line[end]) {
			end++
		}
		if end == start {
			continue
		}
		name := strings.Trim(resolver.Resolve(
			line[start:end],
		), `\`)
		for _, target := range targets {
			if strings.EqualFold(name, strings.Trim(target, `\`)) {
				return true
			}
		}
	}
	return false
}

func isPHPAnnotationNameByte(value byte) bool {
	return value == '\\' || value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}
