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
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	routeAttributeClass     = "Symfony\\Component\\Routing\\Attribute\\Route"
	abstractControllerClass = "Symfony\\Bundle\\FrameworkBundle\\Controller\\" +
		"AbstractController"
	asControllerAttributeClass = "Symfony\\Component\\HttpKernel\\Attribute\\" +
		"AsController"
)

var routeAttributeClasses = []string{
	routeAttributeClass,
	"Symfony\\Component\\Routing\\Annotation\\Route",
	"Sensio\\Bundle\\FrameworkExtraBundle\\Configuration\\Route",
}

type RouteAttributeCodeActionProvider struct {
	phpIndex *php.PHPIndex
}

func NewRouteAttributeCodeActionProvider(
	phpIndex *php.PHPIndex,
) *RouteAttributeCodeActionProvider {
	return &RouteAttributeCodeActionProvider{phpIndex: phpIndex}
}

func (p *RouteAttributeCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *RouteAttributeCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.CodeActionParams == nil ||
		request.Document == nil || request.Root == nil ||
		request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	if _, found := p.phpIndex.FindClass(routeAttributeClass); !found {
		return nil
	}
	method := phpquery.MethodAt(request.Node)
	class := phpquery.ClassAt(method)
	if method == nil || class == nil ||
		!isPublicInstancePHPMethod(method) {
		return nil
	}
	resolver := php.NewNameResolver(request.Root)
	if hasResolvedPHPAttribute(
		method,
		resolver,
		routeAttributeClasses...,
	) || !p.isControllerClass(request, class, resolver) {
		return nil
	}

	qualifier, importEdit := phpClassQualifier(
		request,
		routeAttributeClass,
	)
	if qualifier == "" {
		return nil
	}
	className := phpquery.ClassName(class)
	methodName := phpquery.MethodName(method)
	namespace := phpquery.Namespace(request.Root)
	classFQN := className
	if namespace != "" {
		classFQN = namespace + `\` + className
	}
	path := generatedRoutePath(classFQN, methodName)
	if path == "" {
		path = "/"
	}
	name := generatedRouteName(classFQN, methodName)
	insertOffset := method.RangeTrimmedTrivia().Start
	indent := phpLineIndentation(
		request.Document.Source,
		insertOffset,
	)
	newText := "#[" + qualifier + "('" + path +
		"', name: '" + name + "')]\n" + indent
	edits := []protocol.TextEdit{{
		Range: offsetRange(
			request,
			insertOffset,
			insertOffset,
		),
		NewText: newText,
	}}
	if importEdit != nil {
		edits = append(edits, *importEdit)
	}
	return []protocol.CodeAction{{
		Title: "Symfony: Add Route attribute",
		Kind:  protocol.CodeActionRefactorRewrite,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				request.TextDocument.URI: edits,
			},
		},
	}}
}

func (p *RouteAttributeCodeActionProvider) isControllerClass(
	request *lsp.CodeActionRequest,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	className := phpquery.ClassName(class)
	if strings.HasSuffix(className, "Controller") ||
		hasResolvedPHPAttribute(
			class,
			resolver,
			asControllerAttributeClass,
			routeAttributeClass,
		) {
		return true
	}
	for _, method := range phpquery.Methods(class) {
		if isPublicInstancePHPMethod(method) &&
			hasResolvedPHPAttribute(
				method,
				resolver,
				routeAttributeClasses...,
			) {
			return true
		}
	}
	for _, parent := range phpquery.ClassExtends(class) {
		if strings.EqualFold(
			strings.Trim(resolver.Resolve(parent), `\`),
			abstractControllerClass,
		) {
			return true
		}
	}

	path, _ := uriutil.Path(request.Document.URI)
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		request.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	classFQN := className
	if namespace := phpquery.Namespace(request.Root); namespace != "" {
		classFQN = namespace + `\` + className
	}
	return snapshot.IsSubtypeOf(classFQN, abstractControllerClass)
}

func isPublicInstancePHPMethod(method *phpsyntax.Node) bool {
	if method == nil {
		return false
	}
	visibility := phpquery.DeclarationVisibility(method)
	if visibility != "" && visibility != "public" {
		return false
	}
	for element := range method.Descendants() {
		token, ok := element.(*phpsyntax.Token)
		if !ok {
			continue
		}
		switch strings.ToLower(token.Text()) {
		case "static":
			return false
		case "function":
			return true
		}
	}
	return true
}

func hasResolvedPHPAttribute(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
	targets ...string,
) bool {
	for _, attribute := range phpquery.Attributes(node) {
		name := strings.Trim(resolver.Resolve(
			phpquery.AttributeName(attribute),
		), `\`)
		for _, target := range targets {
			if strings.EqualFold(name, strings.Trim(target, `\`)) {
				return true
			}
		}
	}
	return false
}

func generatedRouteName(classFQN, methodName string) string {
	methodName = strings.TrimSuffix(methodName, "Action")
	var result []string
	for _, part := range strings.Split(strings.Trim(classFQN, `\`), `\`) {
		if part == "" || strings.EqualFold(part, "controller") {
			continue
		}
		part = strings.ToLower(part)
		switch {
		case strings.HasSuffix(part, "bundle") && part != "bundle":
			part = strings.TrimSuffix(part, "bundle")
		case strings.HasSuffix(part, "controller") && part != "controller":
			part = strings.TrimSuffix(part, "controller")
		}
		if part != "" {
			result = append(result, part)
		}
	}
	name := strings.ToLower(methodName)
	if len(result) == 0 {
		return name
	}
	if name != "" {
		result = append(result, name)
	}
	return strings.Join(result, "_")
}

func generatedRoutePath(classFQN, methodName string) string {
	methodName = strings.TrimSuffix(methodName, "Action")
	foundController := false
	var result []string
	for _, part := range strings.Split(strings.Trim(classFQN, `\`), `\`) {
		if part == "" {
			continue
		}
		if strings.EqualFold(part, "controller") {
			foundController = true
			continue
		}
		if !foundController {
			continue
		}
		part = strings.ToLower(part)
		part = strings.TrimSuffix(part, "controller")
		if part != "" {
			result = append(result, part)
		}
	}
	if !strings.EqualFold(methodName, "index") && methodName != "" {
		result = append(result, strings.ToLower(methodName))
	}
	if len(result) == 0 {
		return ""
	}
	return "/" + strings.Join(result, "/")
}

func phpLineIndentation(source string, offset uint32) string {
	if int(offset) > len(source) {
		return ""
	}
	lineStart := strings.LastIndexAny(source[:offset], "\r\n") + 1
	line := source[lineStart:offset]
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}
