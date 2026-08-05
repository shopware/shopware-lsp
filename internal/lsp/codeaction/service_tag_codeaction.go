package codeaction

import (
	"context"
	"fmt"
	"html"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

// ServiceTagCodeActionProvider ports the Symfony plugin's XML/YAML "Add Tags"
// intentions. LSP clients receive one deterministic action per inferred tag
// instead of an editor-specific chooser popup.
type ServiceTagCodeActionProvider struct {
	phpIndex *php.PHPIndex
}

func NewServiceTagCodeActionProvider(
	phpIndex *php.PHPIndex,
) *ServiceTagCodeActionProvider {
	return &ServiceTagCodeActionProvider{phpIndex: phpIndex}
}

func (p *ServiceTagCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *ServiceTagCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.CodeActionParams == nil ||
		request.Document == nil || request.Root == nil ||
		request.Node == nil {
		return nil
	}

	var target serviceTagActionTarget
	switch request.Document.SyntaxLanguage {
	case language.XML:
		target = xmlServiceTagTarget(request.Node)
	case language.YAML:
		target = yamlServiceTagTarget(request.Node)
	default:
		return nil
	}
	className := strings.Trim(
		strings.TrimSpace(target.className),
		"\\",
	)
	if target.node == nil || className == "" ||
		strings.Contains(className, "%") {
		return nil
	}
	if _, found := p.phpIndex.FindClass(className); !found {
		return nil
	}

	suggested := symfony.SuggestedServiceTags(
		className,
		p.phpIndex.SemanticSnapshot(),
	)
	result := make([]protocol.CodeAction, 0, len(suggested))
	for _, tag := range suggested {
		if ctx.Err() != nil {
			return nil
		}
		if _, exists := target.tags[tag]; exists {
			continue
		}
		var edit *protocol.TextEdit
		switch request.Document.SyntaxLanguage {
		case language.XML:
			edit = xmlServiceTagEdit(request, target.node, tag)
		case language.YAML:
			edit = yamlServiceTagEdit(request, target.node, tag)
		}
		if edit == nil {
			continue
		}
		result = append(result, protocol.CodeAction{
			Title: fmt.Sprintf(
				"Symfony: Add service tag '%s'",
				tag,
			),
			Kind: protocol.CodeActionRefactorRewrite,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[string][]protocol.TextEdit{
					request.TextDocument.URI: {*edit},
				},
			},
		})
	}
	return result
}

type serviceTagActionTarget struct {
	node      *cst.Node
	className string
	tags      map[string]struct{}
}

func xmlServiceTagTarget(node *cst.Node) serviceTagActionTarget {
	service := xmlquery.ElementAt(node)
	for service != nil && xmlquery.ElementName(service) != "service" {
		service = xmlquery.ParentElement(service)
	}
	if service == nil {
		return serviceTagActionTarget{}
	}
	className := xmlquery.AttributeValue(
		xmlquery.Attribute(service, "class"),
	)
	if className == "" {
		className = xmlquery.AttributeValue(
			xmlquery.Attribute(service, "id"),
		)
	}
	tags := make(map[string]struct{})
	for _, child := range xmlquery.ChildElements(service, "tag") {
		if tag := xmlquery.AttributeValue(
			xmlquery.Attribute(child, "name"),
		); tag != "" {
			tags[tag] = struct{}{}
		}
	}
	return serviceTagActionTarget{
		node:      service,
		className: className,
		tags:      tags,
	}
}

func yamlServiceTagTarget(node *cst.Node) serviceTagActionTarget {
	pair := yamlquery.AncestorPair(node)
	for pair != nil {
		path := yamlquery.PairPath(pair)
		if len(path) == 2 && path[0] == "services" {
			break
		}
		pair = yamlAncestorPair(pair)
	}
	if pair == nil {
		return serviceTagActionTarget{}
	}
	serviceID := yamlquery.ScalarValue(yamlquery.PairKey(pair))
	if serviceID == "" || strings.HasPrefix(serviceID, "_") {
		return serviceTagActionTarget{}
	}
	config := yamlquery.PairValue(pair)
	if !yamlquery.IsMapping(config) {
		return serviceTagActionTarget{}
	}
	if yamlquery.Property(config, "alias") != nil {
		return serviceTagActionTarget{}
	}
	className := yamlquery.ScalarValue(
		yamlquery.Property(config, "class"),
	)
	if className == "" {
		className = serviceID
	}
	return serviceTagActionTarget{
		node:      pair,
		className: className,
		tags:      yamlConfiguredServiceTags(config),
	}
}

func yamlAncestorPair(node *cst.Node) *cst.Node {
	if node == nil {
		return nil
	}
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == yamlsyntax.YamlPair {
			return parent
		}
	}
	return nil
}

func yamlConfiguredServiceTags(config *cst.Node) map[string]struct{} {
	result := make(map[string]struct{})
	tags := yamlquery.Property(config, "tags")
	if tags == nil {
		return result
	}
	if yamlquery.IsSequence(tags) {
		for _, item := range yamlquery.Items(tags) {
			if tag := yamlConfiguredServiceTag(
				yamlquery.ItemValue(item),
			); tag != "" {
				result[tag] = struct{}{}
			}
		}
		return result
	}
	if tag := yamlConfiguredServiceTag(tags); tag != "" {
		result[tag] = struct{}{}
	}
	return result
}

func yamlConfiguredServiceTag(node *cst.Node) string {
	if node == nil {
		return ""
	}
	if yamlquery.IsMapping(node) {
		return yamlquery.ScalarValue(yamlquery.Property(node, "name"))
	}
	return yamlquery.ScalarValue(node)
}

func xmlServiceTagEdit(
	request *lsp.CodeActionRequest,
	service *cst.Node,
	tag string,
) *protocol.TextEdit {
	if service == nil {
		return nil
	}
	content := request.DocumentContent
	text := service.Text()
	serviceIndent := indentationAt(content, service.Range().Start)
	childIndent := serviceIndent + "    "
	if children := xmlquery.ChildElements(service); len(children) != 0 {
		if indent := indentationAt(
			content,
			children[0].Range().Start,
		); len(indent) > len(serviceIndent) {
			childIndent = indent
		}
	}
	tagText := fmt.Sprintf(
		`%s<tag name="%s"/>`,
		childIndent,
		html.EscapeString(tag),
	)

	if index := strings.LastIndex(text, "/>"); index >= 0 &&
		strings.TrimSpace(text[index:]) == "/>" {
		start := service.Range().Start + uint32(index)
		return &protocol.TextEdit{
			Range: offsetRange(request, start, start+2),
			NewText: ">\n" + tagText + "\n" +
				serviceIndent + "</service>",
		}
	}
	index := strings.LastIndex(text, "</service")
	if index < 0 {
		return nil
	}
	closingOffset := service.Range().Start + uint32(index)
	lineStart := lineStartOffset(content, closingOffset)
	if strings.TrimSpace(string(content[lineStart:closingOffset])) == "" {
		return &protocol.TextEdit{
			Range:   offsetRange(request, lineStart, lineStart),
			NewText: tagText + "\n",
		}
	}
	return &protocol.TextEdit{
		Range: offsetRange(request, closingOffset, closingOffset),
		NewText: "\n" + tagText + "\n" +
			serviceIndent,
	}
}

func yamlServiceTagEdit(
	request *lsp.CodeActionRequest,
	servicePair *cst.Node,
	tag string,
) *protocol.TextEdit {
	if servicePair == nil {
		return nil
	}
	config := yamlquery.PairValue(servicePair)
	if !yamlquery.IsMapping(config) {
		return nil
	}
	tags := yamlquery.Property(config, "tags")
	if tags != nil {
		return yamlExistingServiceTagsEdit(request, tags, tag)
	}

	if config.Kind() == yamlsyntax.YamlFlowMapping {
		offset, ok := yamlFlowClosingOffset(config, '}')
		if !ok {
			return nil
		}
		prefix := ""
		if len(yamlquery.Pairs(config)) != 0 {
			prefix = ", "
		}
		return &protocol.TextEdit{
			Range:   offsetRange(request, offset, offset),
			NewText: prefix + "tags: [" + tag + "]",
		}
	}

	serviceIndent := indentationAt(
		request.DocumentContent,
		yamlquery.PairKey(servicePair).Range().Start,
	)
	propertyIndent := serviceIndent + "  "
	itemIndent := propertyIndent + "  "
	offset := yamlBlockContentEnd(request.DocumentContent, config)
	return &protocol.TextEdit{
		Range: offsetRange(request, offset, offset),
		NewText: "\n" + propertyIndent + "tags:\n" +
			itemIndent + "- " + tag,
	}
}

func yamlExistingServiceTagsEdit(
	request *lsp.CodeActionRequest,
	tags *cst.Node,
	tag string,
) *protocol.TextEdit {
	switch tags.Kind() {
	case yamlsyntax.YamlFlowSequence:
		offset, ok := yamlFlowClosingOffset(tags, ']')
		if !ok {
			return nil
		}
		prefix := ""
		if len(yamlquery.Items(tags)) != 0 {
			prefix = ", "
		}
		return &protocol.TextEdit{
			Range:   offsetRange(request, offset, offset),
			NewText: prefix + tag,
		}
	case yamlsyntax.YamlSequence:
		indent := indentationAt(
			request.DocumentContent,
			tags.Range().Start,
		)
		if items := yamlquery.Items(tags); len(items) != 0 {
			indent = indentationAt(
				request.DocumentContent,
				items[0].Range().Start,
			)
		}
		offset := yamlBlockContentEnd(request.DocumentContent, tags)
		return &protocol.TextEdit{
			Range:   offsetRange(request, offset, offset),
			NewText: "\n" + indent + "- " + tag,
		}
	case yamlsyntax.YamlScalar,
		yamlsyntax.YamlFlowMapping,
		yamlsyntax.YamlMapping:
		raw := strings.TrimSpace(tags.Text())
		if raw == "" {
			return nil
		}
		return &protocol.TextEdit{
			Range: offsetRange(
				request,
				tags.RangeTrimmedTrivia().Start,
				tags.RangeTrimmedTrivia().End,
			),
			NewText: "[" + raw + ", " + tag + "]",
		}
	default:
		return nil
	}
}

func yamlBlockContentEnd(content []byte, node *cst.Node) uint32 {
	if node == nil {
		return 0
	}
	end := node.Range().End
	if end > uint32(len(content)) {
		end = uint32(len(content))
	}
	for end > node.Range().Start {
		switch content[end-1] {
		case ' ', '\t', '\r', '\n':
			end--
		default:
			return end
		}
	}
	return end
}
