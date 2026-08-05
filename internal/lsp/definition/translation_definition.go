package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TranslationDefinitionProvider struct {
	index    *translation.Index
	phpIndex *php.PHPIndex
}

func NewTranslationDefinitionProvider(
	index *translation.Index,
	phpIndex *php.PHPIndex,
) *TranslationDefinitionProvider {
	return &TranslationDefinitionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TranslationDefinitionProvider) GetDefinition(
	ctx context.Context,
	params *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || params == nil || params.Node == nil {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	if extension != ".php" && extension != ".twig" {
		return nil
	}
	if extension == ".php" {
		if _, found := php.AssistantArgumentReference(
			ctx,
			params.Node,
			"TranslationDomain",
		); found {
			return p.domainDefinitions(
				phpquery.StringValue(params.Node),
			)
		}
		if _, found := php.AssistantArgumentReference(
			ctx,
			params.Node,
			"TranslationKey",
		); found {
			domain := "messages"
			if sibling, siblingFound := php.AssistantSiblingStringArgument(
				ctx,
				params.Node,
				"TranslationDomain",
			); siblingFound {
				domain = sibling
			}
			return p.keyDefinitions(
				domain,
				phpquery.StringValue(params.Node),
			)
		}
	}
	reference, ok := translation.ReferenceAt(
		params.TextDocument.URI,
		params.Node,
		params.DocumentContent,
	)
	if !ok || extension == ".php" &&
		!translation.ValidatePHPReference(
			ctx,
			reference,
			p.phpIndex,
			params.DocumentContent,
		) {
		return nil
	}

	var messages []translation.Message
	var err error
	switch reference.Role {
	case translation.ReferenceKey:
		messages, err = p.index.GetMessages(reference.Domain, reference.Key)
	case translation.ReferenceDomain:
		messages, err = p.index.GetDomainMessages(reference.Domain)
	default:
		return nil
	}
	if err != nil || ctx.Err() != nil {
		return nil
	}
	if reference.Role == translation.ReferenceDomain {
		messages = firstTranslationPerFile(messages)
	}
	locations := make([]protocol.Location, 0, len(messages))
	for _, message := range messages {
		locations = append(locations, translationLocation(message))
	}
	return locations
}

func (p *TranslationDefinitionProvider) domainDefinitions(
	domain string,
) []protocol.Location {
	messages, err := p.index.GetDomainMessages(domain)
	if err != nil {
		return nil
	}
	return translationLocations(firstTranslationPerFile(messages))
}

func (p *TranslationDefinitionProvider) keyDefinitions(
	domain,
	key string,
) []protocol.Location {
	messages, err := p.index.GetMessages(domain, key)
	if err != nil {
		return nil
	}
	return translationLocations(messages)
}

func translationLocations(
	messages []translation.Message,
) []protocol.Location {
	locations := make([]protocol.Location, 0, len(messages))
	for _, message := range messages {
		locations = append(locations, translationLocation(message))
	}
	return locations
}

func firstTranslationPerFile(messages []translation.Message) []translation.Message {
	result := make([]translation.Message, 0, len(messages))
	seen := make(map[string]struct{})
	for _, message := range messages {
		if _, exists := seen[message.File]; exists {
			continue
		}
		seen[message.File] = struct{}{}
		result = append(result, message)
	}
	return result
}

func translationLocation(message translation.Message) protocol.Location {
	return protocol.Location{
		URI: uriutil.FileURI(message.File),
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      message.Line,
				Character: message.Character,
			},
			End: protocol.Position{
				Line:      message.EndLine,
				Character: message.EndCharacter,
			},
		},
	}
}
