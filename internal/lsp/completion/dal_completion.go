package completion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
)

type DALCompletionProvider struct {
	index *dal.Index
}

func NewDALCompletionProvider(index *dal.Index) *DALCompletionProvider {
	return &DALCompletionProvider{index: index}
}

func (p *DALCompletionProvider) GetCompletions(
	_ context.Context,
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || params == nil || params.Node == nil {
		return nil
	}
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".js", ".ts", ".vue":
		if strings.HasSuffix(
			strings.ToLower(params.TextDocument.URI), ".vue",
		) && lsp.EffectiveSyntaxLanguage(language.Vue, params.Node) !=
			language.JavaScript {
			return nil
		}
		if dal.IsJSEntityReference(params.Node) {
			definitions, err := p.index.Definitions()
			if err != nil {
				return nil
			}
			items := make([]protocol.CompletionItem, 0, len(definitions))
			for _, definition := range definitions {
				items = append(items, protocol.CompletionItem{
					Label:  definition.Name,
					Kind:   int(protocol.ClassCompletion),
					Detail: definition.Class,
				})
			}
			return items
		}
		referenceKind := dal.JSFieldReferenceAt(params.Node)
		if referenceKind == dal.JSFieldReferenceNone {
			return nil
		}
		return p.jsFieldCompletions(
			referenceKind == dal.JSFieldReferenceAssociation,
		)
	case ".twig":
		if !strings.Contains(filepath.ToSlash(params.TextDocument.URI), "/Resources/scripts/") ||
			!dal.IsTwigEntityReference(params.Node) {
			return nil
		}
		definitions, err := p.index.Definitions()
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0, len(definitions))
		for _, definition := range definitions {
			items = append(items, protocol.CompletionItem{
				Label:  definition.Name,
				Kind:   int(protocol.ClassCompletion),
				Detail: definition.Class,
			})
		}
		return items
	case ".php":
		if !dal.IsPHPFieldReference(params.Node) {
			return nil
		}
		fields, err := p.index.Fields()
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0, len(fields))
		for _, field := range fields {
			items = append(items, protocol.CompletionItem{
				Label:  field.Name,
				Kind:   int(protocol.FieldCompletion),
				Detail: field.Type,
			})
		}
		return items
	}
	return nil
}

func (p *DALCompletionProvider) jsFieldCompletions(
	associationsOnly bool,
) []protocol.CompletionItem {
	definitions, err := p.index.Definitions()
	if err != nil {
		return nil
	}
	type candidate struct {
		name        string
		association bool
		types       map[string]struct{}
		entities    map[string]struct{}
	}
	candidates := make(map[string]*candidate)
	for _, definition := range definitions {
		for _, field := range definition.Fields {
			if associationsOnly && !field.Association {
				continue
			}
			current := candidates[field.Name]
			if current == nil {
				current = &candidate{
					name: field.Name, types: map[string]struct{}{},
					entities: map[string]struct{}{},
				}
				candidates[field.Name] = current
			}
			current.association = current.association || field.Association
			current.types[field.Type] = struct{}{}
			current.entities[definition.Name] = struct{}{}
		}
	}
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		current := candidates[name]
		types := sortedDALCompletionKeys(current.types)
		entities := sortedDALCompletionKeys(current.entities)
		detail := strings.Join(types, " | ")
		if len(entities) <= 3 {
			detail += " · " + strings.Join(entities, ", ")
		} else {
			detail += fmt.Sprintf(" · %d entities", len(entities))
		}
		kind := protocol.FieldCompletion
		if current.association {
			kind = protocol.PropertyCompletion
		}
		items = append(items, protocol.CompletionItem{
			Label: name, Kind: int(kind), Detail: detail,
		})
	}
	return items
}

func sortedDALCompletionKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (p *DALCompletionProvider) GetTriggerCharacters() []string { return nil }
