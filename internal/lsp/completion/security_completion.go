package completion

import (
	"context"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SecurityCompletionProvider struct {
	index *security.Index
}

func NewSecurityCompletionProvider(
	index *security.Index,
) *SecurityCompletionProvider {
	return &SecurityCompletionProvider{index: index}
}

func (p *SecurityCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if reference, found := security.ConfigReferenceAt(
		request.Node,
	); found && reference.Role == security.ConfigReference {
		return p.configNameCompletions(request, reference.Kind)
	}
	if strings.HasSuffix(
		strings.ToLower(request.TextDocument.URI),
		".yaml",
	) || strings.HasSuffix(
		strings.ToLower(request.TextDocument.URI),
		".yml",
	) {
		if options := security.ConfigOptionsAt(request.Node); len(options) != 0 {
			items := make([]protocol.CompletionItem, 0, len(options))
			for _, option := range options {
				items = append(items, protocol.CompletionItem{
					Label:  option.Name,
					Kind:   int(protocol.PropertyCompletion),
					Detail: option.Detail,
				})
			}
			sortCompletionItems(items)
			return items
		}
	}
	_, ok := security.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
		string(request.DocumentContent),
		offset,
	)
	if !ok {
		return nil
	}

	attributes, err := p.index.Attributes()
	if err != nil {
		return nil
	}
	byName := make(map[string]security.Attribute, len(attributes))
	for _, attribute := range attributes {
		if len(attribute.Declarations()) != 0 {
			byName[strings.ToLower(attribute.Name)] = attribute
		}
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	for _, occurrence := range security.OccurrencesInDocument(
		path,
		request.Root,
		string(request.DocumentContent),
	) {
		if occurrence.Role != security.DeclarationOccurrence ||
			occurrence.Name == "" {
			continue
		}
		key := strings.ToLower(occurrence.Name)
		attribute := byName[key]
		if attribute.Name == "" {
			attribute.Name = occurrence.Name
		}
		attribute.Occurrences = append(
			attribute.Occurrences,
			occurrence,
		)
		byName[key] = attribute
	}

	items := make([]protocol.CompletionItem, 0, len(byName))
	for _, attribute := range byName {
		items = append(items, protocol.CompletionItem{
			Label:  attribute.Name,
			Kind:   int(protocol.EnumMemberCompletion),
			Detail: securityAttributeDetail(attribute),
		})
	}
	sortCompletionItems(items)
	return items
}

func (p *SecurityCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\"", ":"}
}

func (p *SecurityCompletionProvider) configNameCompletions(
	request *lsp.CompletionRequest,
	kind security.ConfigKind,
) []protocol.CompletionItem {
	names, err := p.index.ConfigNames(kind)
	if err != nil {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	for _, occurrence := range security.ConfigOccurrencesInDocument(
		path,
		request.Root,
	) {
		if occurrence.Kind != kind ||
			occurrence.Role != security.ConfigDeclaration ||
			containsCompletionLabel(names, occurrence.Name) {
			continue
		}
		names = append(names, occurrence.Name)
	}
	items := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		detail := "Symfony user provider"
		if kind == security.ConfigFirewall {
			detail = "Symfony firewall"
		}
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: detail,
		})
	}
	sortCompletionItems(items)
	return items
}

func containsCompletionLabel(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}

func securityAttributeDetail(attribute security.Attribute) string {
	for _, declaration := range attribute.Declarations() {
		switch declaration.Origin {
		case security.OriginVoter:
			if declaration.Class != "" {
				return "Symfony voter · " + declaration.Class
			}
			return "Symfony voter attribute"
		case security.OriginRoleHierarchy:
			return "Symfony role hierarchy"
		case security.OriginAccessControl:
			return "Symfony access control"
		case security.OriginBuiltIn:
			return "Symfony security attribute"
		}
	}
	return "Symfony security attribute"
}

func sortCompletionItems(items []protocol.CompletionItem) {
	slices.SortFunc(items, func(left, right protocol.CompletionItem) int {
		return strings.Compare(
			strings.ToLower(left.Label),
			strings.ToLower(right.Label),
		)
	})
}
