package completion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigIncludeParameterCompletionProvider struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigIncludeParameterCompletionProvider(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigIncludeParameterCompletionProvider {
	return &TwigIncludeParameterCompletionProvider{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigIncludeParameterCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.twigIndex == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	includeContext, found := twig.IncludeParameterContextAt(
		request.Root,
		request.Node,
		offset,
	)
	if !found {
		return nil
	}
	currentName := ""
	if parameter, parameterFound := twig.IncludeParameterAt(
		request.Root,
		request.Node,
		offset,
	); parameterFound {
		currentName = parameter.Name
	}
	existing := make(map[string]struct{}, len(includeContext.Existing))
	for _, name := range includeContext.Existing {
		if name != currentName {
			existing[name] = struct{}{}
		}
	}

	candidates := p.parameterCandidates(request, includeContext.Template)
	items := make([]protocol.CompletionItem, 0, len(candidates))
	for _, candidate := range candidates {
		if _, alreadyProvided := existing[candidate.name]; alreadyProvided {
			continue
		}
		detail := "Twig template input"
		if candidate.phpType != "" {
			detail = candidate.phpType
		}
		item := protocol.CompletionItem{
			Label:  candidate.name,
			Kind:   int(protocol.VariableCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		switch {
		case candidate.file != "":
			item.Documentation.Value = fmt.Sprintf(
				"Read by `%s` in `%s`.",
				candidate.name,
				filepath.Base(candidate.file),
			)
		case candidate.phpFile != "":
			item.Documentation.Value = fmt.Sprintf(
				"PHP context value provided to `%s` by `%s`.",
				includeContext.Template,
				filepath.Base(candidate.phpFile),
			)
		default:
			item.Documentation.Value = fmt.Sprintf(
				"Input for `%s`.",
				includeContext.Template,
			)
		}
		items = append(items, item)
	}
	return items
}

func (p *TwigIncludeParameterCompletionProvider) GetTriggerCharacters() []string {
	return []string{"{", ",", "'", "\""}
}

type includeParameterCandidate struct {
	name    string
	file    string
	phpType string
	phpFile string
}

func (p *TwigIncludeParameterCompletionProvider) parameterCandidates(
	request *lsp.CompletionRequest,
	template string,
) []includeParameterCandidate {
	variables, _ := p.twigIndex.GetTemplateVariables(template)
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	currentIsTarget := templateMatchesPath(template, currentPath)
	if currentIsTarget {
		overlay := twig.TemplateInputVariablesInDocument(
			currentPath,
			request.Root,
		)
		filtered := variables[:0]
		for _, variable := range variables {
			if variable.FilePath != currentPath {
				filtered = append(filtered, variable)
			}
		}
		variables = append(filtered, overlay...)
	}

	candidates := make(map[string]includeParameterCandidate)
	for _, variable := range variables {
		if _, exists := candidates[variable.Name]; exists {
			continue
		}
		candidates[variable.Name] = includeParameterCandidate{
			name: variable.Name,
			file: variable.FilePath,
		}
	}
	if p.phpIndex != nil {
		phpVariables, _ := p.phpIndex.TwigTemplateVariables(template)
		for _, variable := range phpVariables {
			candidate := candidates[variable.Name]
			candidate.name = variable.Name
			candidate.phpType = variable.Type
			candidate.phpFile = variable.File
			candidates[variable.Name] = candidate
		}
	}
	result := make([]includeParameterCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].name) <
			strings.ToLower(result[right].name)
	})
	return result
}

func templateMatchesPath(template, path string) bool {
	for _, name := range twig.TemplateNames(path) {
		if name == template {
			return true
		}
	}
	return false
}
