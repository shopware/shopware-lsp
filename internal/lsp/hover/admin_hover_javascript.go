package hover

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *AdminHoverProvider) jsHover(_ context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	node := params.Node
	if _, eventName, matched := admin.JavaScriptShopwareEventBusEventAt(
		node,
	); matched && eventName != "" {
		return p.shopwareEventBusEventHover(
			params.TextDocument.URI, eventName,
		)
	}
	if receiver, memberName, matched :=
		admin.JavaScriptShopwareUtilsMember(node); matched && memberName != "" {
		return p.shopwareUtilsMemberHover(
			params.TextDocument.URI, strings.Join(receiver, "."), memberName,
		)
	}
	if receiver, memberName, matched :=
		admin.JavaScriptShopwareContextMember(node); matched && memberName != "" {
		return p.shopwareContextMemberHover(
			params.TextDocument.URI, strings.Join(receiver, "."), memberName,
		)
	}
	if admin.IsApplicationContainerNameReference(node) {
		if container, found := admin.ApplicationContainerNamed(
			jsquery.StringValue(node),
		); found {
			return &protocol.Hover{Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Application container** `%s`\n\n%s.\n\nType: `%s`.",
					container.Name, container.Description, container.InterfaceName,
				),
			}}, nil
		}
	}
	if containerName, memberName, matched :=
		admin.JavaScriptApplicationContainerMember(node); matched && memberName != "" {
		return p.applicationContainerMemberHover(
			params.TextDocument.URI, containerName, memberName,
		)
	}
	if storeName, memberName, matched := jsquery.StoreMember(node); matched && memberName != "" {
		return p.storeMemberHover(storeName, memberName)
	}
	if member, matched := jsquery.ThisMember(node); matched && member != "" {
		return p.thisMemberHover(params.TextDocument.URI, member)
	}
	if admin.IsServiceReference(node) {
		return p.serviceHover(jsquery.StringValue(node))
	}
	if admin.IsStoreReference(node) {
		return p.storeHover(jsquery.StringValue(node))
	}
	if admin.IsPrivilegeReference(node) {
		return p.privilegeHover(jsquery.StringValue(node))
	}
	if admin.IsJavaScriptModuleRouteReference(node) {
		return p.moduleRouteHover(jsquery.StringValue(node))
	}
	if path, err := uriutil.Path(params.TextDocument.URI); err == nil {
		if indexedTarget, indexedFound, indexedErr :=
			p.adminIndexer.JavaScriptSymbolAt(path, node); indexedErr != nil {
			return nil, indexedErr
		} else if indexedFound &&
			indexedTarget.Kind == admin.AdminSymbolDirective &&
			indexedTarget.Owner != "" {
			return p.directiveHoverTarget(indexedTarget)
		}
	}

	target, found := admin.JavaScriptSymbolAt(node)
	if !found {
		return nil, nil
	}
	switch target.Kind {
	case admin.AdminSymbolMixin:
		return p.mixinHover(target.Name)
	case admin.AdminSymbolDirective:
		return p.directiveHover(target.Name)
	case admin.AdminSymbolFilter:
		return p.filterHover(target.Name)
	case admin.AdminSymbolCMSElement:
		return p.cmsHover(admin.AdminCMSElement, target.Name)
	case admin.AdminSymbolCMSBlock:
		return p.cmsHover(admin.AdminCMSBlock, target.Name)
	case admin.AdminSymbolModule:
		return p.moduleHover(target.Name)
	case admin.AdminSymbolComponent:
		// Continue with the component lookup below.
	default:
		return nil, nil
	}

	componentName := target.Name
	if componentName == "" {
		return nil, nil
	}

	// Look up the component with its definition
	components, err := p.adminIndexer.GetComponentWithDefinition(componentName)
	if err != nil || len(components) == 0 {
		return nil, nil
	}

	// Build markdown content for the hover
	markdown := p.buildHoverContent(components)

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      params.Position.Line,
				Character: params.Position.Character,
			},
			End: protocol.Position{
				Line:      params.Position.Line,
				Character: params.Position.Character + len(componentName),
			},
		},
	}, nil
}

func (p *AdminHoverProvider) shopwareEventBusEventHover(
	uri,
	eventName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	event, found, err := p.adminIndexer.ResolveShopwareEventBusEvent(
		eventName, path,
	)
	if err != nil || !found {
		return nil, err
	}
	value := fmt.Sprintf("**Shopware EventBus event** `%s`", eventName)
	if event.Type != "" {
		value += "\n\nPayload: `" + event.Type + "`."
	}
	if event.DefinitionPath != "" {
		value += fmt.Sprintf(
			"\n\nDeclared in `%s:%d`.",
			p.makeRelativePath(event.DefinitionPath), event.DefinitionLine,
		)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}

func (p *AdminHoverProvider) shopwareUtilsMemberHover(
	uri,
	receiver,
	memberName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	shape, err := p.adminIndexer.ResolveShopwareUtils(receiver, path)
	if err != nil {
		return nil, err
	}
	for _, member := range shape.Members {
		if member.Name != memberName {
			continue
		}
		qualified := "Shopware.Utils"
		if receiver != "" {
			qualified += "." + receiver
		}
		qualified += "." + memberName
		value := fmt.Sprintf("**Shopware utility** `%s`", qualified)
		if member.Type != "" {
			value += "\n\nType: `" + member.Type + "`."
		}
		if member.DefinitionPath != "" {
			value += fmt.Sprintf(
				"\n\nExported in `%s:%d`.",
				p.makeRelativePath(member.DefinitionPath), member.DefinitionLine,
			)
		}
		return &protocol.Hover{Contents: protocol.MarkupContent{
			Kind: protocol.Markdown, Value: value,
		}}, nil
	}
	return nil, nil
}

func (p *AdminHoverProvider) shopwareContextMemberHover(
	uri,
	receiver,
	memberName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	shape, err := p.adminIndexer.ResolveShopwareContext(receiver, path)
	if err != nil {
		return nil, err
	}
	for _, member := range shape.Members {
		if member.Name != memberName {
			continue
		}
		qualified := "Shopware.Context"
		if receiver != "" {
			qualified += "." + receiver
		}
		qualified += "." + memberName
		value := fmt.Sprintf("**Shopware context member** `%s`", qualified)
		if member.Type != "" {
			value += "\n\nType: `" + member.Type + "`."
		}
		if member.DefinitionPath != "" {
			value += fmt.Sprintf(
				"\n\nDefined in `%s:%d`.",
				p.makeRelativePath(member.DefinitionPath), member.DefinitionLine,
			)
		}
		return &protocol.Hover{Contents: protocol.MarkupContent{
			Kind: protocol.Markdown, Value: value,
		}}, nil
	}
	return nil, nil
}

func (p *AdminHoverProvider) applicationContainerMemberHover(
	uri,
	containerName,
	memberName string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil, err
	}
	shape, err := p.adminIndexer.ResolveApplicationContainer(
		containerName, path,
	)
	if err != nil {
		return nil, err
	}
	var resolved *admin.TwigVueMember
	for index := range shape.Members {
		if shape.Members[index].Name == memberName {
			resolved = &shape.Members[index]
			break
		}
	}
	if containerName == "service" {
		service, serviceErr := p.serviceHover(memberName)
		if serviceErr != nil {
			return nil, serviceErr
		}
		if service != nil {
			if resolved != nil && resolved.Type != "" {
				service.Contents.Value += "\n\nContainer type: `" +
					resolved.Type + "`."
			}
			return service, nil
		}
	}
	if resolved == nil {
		return nil, nil
	}
	value := fmt.Sprintf(
		"**Application `%s` container member** `%s`",
		containerName, memberName,
	)
	if resolved.Type != "" {
		value += "\n\nType: `" + resolved.Type + "`."
	}
	if resolved.DefinitionPath != "" {
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(resolved.DefinitionPath), resolved.DefinitionLine,
		)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}
