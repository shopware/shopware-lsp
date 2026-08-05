package codeaction

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

// XMLServiceSuggestionCodeActionProvider ports the Symfony plugin's "Suggest
// Service" XML intention. Each compatible constructor service is exposed as a
// separate deterministic LSP rewrite.
type XMLServiceSuggestionCodeActionProvider struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewXMLServiceSuggestionCodeActionProvider(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *XMLServiceSuggestionCodeActionProvider {
	return &XMLServiceSuggestionCodeActionProvider{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

func (p *XMLServiceSuggestionCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *XMLServiceSuggestionCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.serviceIndex == nil ||
		p.phpIndex == nil || request == nil ||
		request.CodeActionParams == nil || request.Document == nil ||
		request.Root == nil || request.Node == nil ||
		request.Document.SyntaxLanguage != language.XML {
		return nil
	}
	argument := xmlquery.ElementAt(request.Node)
	for argument != nil &&
		xmlquery.ElementName(argument) != "argument" {
		argument = xmlquery.ParentElement(argument)
	}
	if argument == nil {
		return nil
	}
	service := xmlquery.ParentElement(argument)
	for service != nil && xmlquery.ElementName(service) != "service" {
		service = xmlquery.ParentElement(service)
	}
	if service == nil {
		return nil
	}
	className := xmlquery.AttributeValue(
		xmlquery.Attribute(service, "class"),
	)
	if className == "" {
		className = xmlquery.AttributeValue(
			xmlquery.Attribute(service, "id"),
		)
	}
	className = strings.Trim(
		strings.TrimSpace(className),
		"\\",
	)
	if className == "" || strings.Contains(className, "%") {
		return nil
	}
	parameter, found := xmlConstructorArgumentParameter(
		p.phpIndex,
		className,
		service,
		argument,
	)
	if !found || !xmlInjectableParameterType(parameter.Type) {
		return nil
	}
	candidates, err := p.xmlCompatibleServices(
		parameter.Type,
	)
	if err != nil {
		return nil
	}
	currentID := xmlquery.AttributeValue(
		xmlquery.Attribute(argument, "id"),
	)
	currentType := xmlquery.AttributeValue(
		xmlquery.Attribute(argument, "type"),
	)
	ownerID := xmlquery.AttributeValue(
		xmlquery.Attribute(service, "id"),
	)
	result := make([]protocol.CodeAction, 0, len(candidates))
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		if candidate.ID == ownerID ||
			(candidate.ID == currentID &&
				strings.EqualFold(currentType, "service")) {
			continue
		}
		replacement := xmlSuggestedServiceArgument(
			argument,
			candidate.ID,
		)
		if replacement == "" {
			continue
		}
		result = append(result, protocol.CodeAction{
			Title: fmt.Sprintf(
				"Symfony: Use service '%s' for %s",
				candidate.ID,
				xmlParameterDisplayName(parameter),
			),
			Kind: protocol.CodeActionRefactorRewrite,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[string][]protocol.TextEdit{
					request.TextDocument.URI: {
						{
							Range: offsetRange(
								request,
								argument.Range().Start,
								argument.Range().End,
							),
							NewText: replacement,
						},
					},
				},
			},
		})
	}
	return result
}

func xmlConstructorArgumentParameter(
	phpIndex *php.PHPIndex,
	className string,
	service,
	target *cst.Node,
) (semantic.Parameter, bool) {
	constructors := phpIndex.FindMethods(className, "__construct")
	if len(constructors) == 0 {
		return semantic.Parameter{}, false
	}
	key := strings.TrimPrefix(
		xmlquery.AttributeValue(xmlquery.Attribute(target, "key")),
		"$",
	)
	if key != "" {
		for _, parameter := range constructors[0].Parameters {
			if strings.EqualFold(
				strings.TrimPrefix(parameter.Name, "$"),
				key,
			) {
				return parameter, true
			}
		}
		return semantic.Parameter{}, false
	}
	index := 0
	for _, argument := range xmlquery.ChildElements(
		service,
		"argument",
	) {
		if argument == target {
			break
		}
		if xmlquery.AttributeValue(
			xmlquery.Attribute(argument, "key"),
		) == "" {
			index++
		}
	}
	if index < 0 || index >= len(constructors[0].Parameters) {
		return semantic.Parameter{}, false
	}
	return constructors[0].Parameters[index], true
}

func xmlInjectableParameterType(value types.Type) bool {
	switch value.Kind() {
	case types.ObjectKind:
		return value.Name() != ""
	case types.UnionKind, types.IntersectionKind:
		for _, member := range value.Arguments() {
			if xmlInjectableParameterType(member) {
				return true
			}
		}
	}
	return false
}

func xmlNamedParameterTypes(value types.Type) []string {
	switch value.Kind() {
	case types.ObjectKind:
		if value.Name() != "" {
			return []string{value.Name()}
		}
	case types.UnionKind, types.IntersectionKind:
		var result []string
		for _, member := range value.Arguments() {
			result = append(
				result,
				xmlNamedParameterTypes(member)...,
			)
		}
		return result
	}
	return nil
}

func (p *XMLServiceSuggestionCodeActionProvider) xmlCompatibleServices(
	expected types.Type,
) ([]symfony.Service, error) {
	relations := p.phpIndex.SemanticSnapshot().Relations()
	byID := make(map[string]symfony.Service)
	for _, typeName := range xmlNamedParameterTypes(expected) {
		services, err := p.serviceIndex.GetServicesByType(typeName)
		if err != nil {
			return nil, err
		}
		for _, service := range services {
			className := strings.TrimPrefix(service.Class, "\\")
			if className == "" &&
				strings.Contains(service.ID, "\\") {
				className = strings.TrimPrefix(service.ID, "\\")
			}
			if className == "" || !relations.IsAssignableTo(
				types.Named(className),
				expected,
			) {
				continue
			}
			byID[strings.ToLower(service.ID)] = service
		}
	}
	result := make([]symfony.Service, 0, len(byID))
	for _, service := range byID {
		result = append(result, service)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].ID) <
			strings.ToLower(result[right].ID)
	})
	return result, nil
}

func xmlSuggestedServiceArgument(
	argument *cst.Node,
	serviceID string,
) string {
	if argument == nil || serviceID == "" {
		return ""
	}
	attributes := make([]string, 0, len(
		xmlquery.Attributes(argument),
	)+2)
	hasType, hasID := false, false
	for _, attribute := range xmlquery.Attributes(argument) {
		switch xmlquery.AttributeName(attribute) {
		case "type":
			attributes = append(attributes, `type="service"`)
			hasType = true
		case "id":
			attributes = append(attributes, `id="`+
				html.EscapeString(serviceID)+`"`)
			hasID = true
		default:
			attributes = append(
				attributes,
				strings.TrimSpace(attribute.Text()),
			)
		}
	}
	if !hasType {
		attributes = append(attributes, `type="service"`)
	}
	if !hasID {
		attributes = append(attributes, `id="`+
			html.EscapeString(serviceID)+`"`)
	}
	return "<argument " + strings.Join(attributes, " ") + "/>"
}

func xmlParameterDisplayName(parameter semantic.Parameter) string {
	name := strings.TrimPrefix(parameter.Name, "$")
	if name == "" {
		return "constructor argument"
	}
	return "$" + name
}
