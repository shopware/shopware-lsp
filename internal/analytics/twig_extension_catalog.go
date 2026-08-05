package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const ListTwigExtensionsCommand = "shopware/symfony/analytics/twig/extensions"

type TwigExtensionCatalogProvider struct {
	twig *twig.TwigIndexer
	php  *php.PHPIndex
}

func NewTwigExtensionCatalogProvider(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigExtensionCatalogProvider {
	return &TwigExtensionCatalogProvider{
		twig: twigIndex,
		php:  phpIndex,
	}
}

type TwigExtensionCatalogRequest struct {
	Search           string `json:"search,omitempty"`
	IncludeFilters   *bool  `json:"includeFilters,omitempty"`
	IncludeFunctions *bool  `json:"includeFunctions,omitempty"`
	IncludeTests     *bool  `json:"includeTests,omitempty"`
	IncludeTags      *bool  `json:"includeTags,omitempty"`
}

type TwigExtensionCatalogParameter struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type TwigExtensionCatalogEntry struct {
	Type        string                          `json:"type"`
	Name        string                          `json:"name"`
	ClassName   string                          `json:"className,omitempty"`
	MethodName  string                          `json:"methodName,omitempty"`
	Callable    string                          `json:"callable,omitempty"`
	Usage       string                          `json:"usage,omitempty"`
	Parameters  []TwigExtensionCatalogParameter `json:"parameters,omitempty"`
	FileURI     string                          `json:"fileUri,omitempty"`
	SourceLine  int                             `json:"sourceLine,omitempty"`
	Deprecated  bool                            `json:"deprecated,omitempty"`
	Deprecation string                          `json:"deprecation,omitempty"`
}

func (p *TwigExtensionCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListTwigExtensionsCommand: p.list,
	}
}

func (p *TwigExtensionCatalogProvider) list(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request TwigExtensionCatalogRequest
	if raw != nil && len(*raw) != 0 && string(*raw) != "null" {
		if err := json.Unmarshal(*raw, &request); err != nil {
			return nil, fmt.Errorf(
				"invalid Twig extension catalog request: %w",
				err,
			)
		}
	}
	return p.Catalog(ctx, request)
}

func (p *TwigExtensionCatalogProvider) Catalog(
	ctx context.Context,
	request TwigExtensionCatalogRequest,
) ([]TwigExtensionCatalogEntry, error) {
	if p == nil || p.twig == nil {
		return nil, fmt.Errorf("twig extension catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(request.Search))
	var result []TwigExtensionCatalogEntry
	if enabledByDefault(request.IncludeFilters) {
		filters, err := p.twig.GetAllTwigFilters()
		if err != nil {
			return nil, err
		}
		for _, filter := range filters {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if twigExtensionNameMatches(filter.Name, search) {
				result = append(result, p.callableEntry(
					"filter",
					filter.Name,
					filter.Method,
					filter.Usage,
					filter.Parameters,
					filter.FilePath,
					filter.Line,
					filter.Deprecated,
					filter.Deprecation,
				))
			}
		}
	}
	if enabledByDefault(request.IncludeFunctions) {
		functions, err := p.twig.GetAllTwigFunctions()
		if err != nil {
			return nil, err
		}
		for _, function := range functions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if twigExtensionNameMatches(function.Name, search) {
				result = append(result, p.callableEntry(
					"function",
					function.Name,
					function.Method,
					function.Usage,
					function.Parameters,
					function.FilePath,
					function.Line,
					function.Deprecated,
					function.Deprecation,
				))
			}
		}
	}
	if enabledByDefault(request.IncludeTests) {
		tests, err := p.twig.GetAllTwigTests()
		if err != nil {
			return nil, err
		}
		for _, test := range tests {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if twigExtensionNameMatches(test.Name, search) {
				result = append(result, p.callableEntry(
					"test",
					test.Name,
					test.Method,
					test.Usage,
					test.Parameters,
					test.FilePath,
					test.Line,
					test.Deprecated,
					test.Deprecation,
				))
			}
		}
	}
	if enabledByDefault(request.IncludeTags) {
		tags, err := p.twig.GetAllTwigTags()
		if err != nil {
			return nil, err
		}
		for _, tag := range tags {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !twigExtensionNameMatches(tag.Name, search) {
				continue
			}
			entry := TwigExtensionCatalogEntry{
				Type:        "tag",
				Name:        tag.Name,
				ClassName:   strings.TrimPrefix(tag.Class, `\`),
				FileURI:     twigExtensionFileURI(tag.FilePath),
				SourceLine:  tag.Line,
				Deprecated:  tag.Deprecated,
				Deprecation: tag.Deprecation,
			}
			result = append(result, entry)
		}
	}
	result = uniqueTwigExtensionEntries(result)
	sort.Slice(result, func(left, right int) bool {
		leftOrder := twigExtensionTypeOrder(result[left].Type)
		rightOrder := twigExtensionTypeOrder(result[right].Type)
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		if !strings.EqualFold(result[left].Name, result[right].Name) {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		if result[left].ClassName != result[right].ClassName {
			return result[left].ClassName < result[right].ClassName
		}
		if result[left].FileURI != result[right].FileURI {
			return result[left].FileURI < result[right].FileURI
		}
		return result[left].SourceLine < result[right].SourceLine
	})
	return result, nil
}

func (p *TwigExtensionCatalogProvider) callableEntry(
	kind,
	name,
	callable,
	usage string,
	parameters []twig.TwigParameter,
	path string,
	line int,
	deprecated bool,
	deprecation string,
) TwigExtensionCatalogEntry {
	className, methodName := p.resolveCallable(callable, path)
	return TwigExtensionCatalogEntry{
		Type:        kind,
		Name:        name,
		ClassName:   className,
		MethodName:  methodName,
		Callable:    callable,
		Usage:       usage,
		Parameters:  twigExtensionParameters(parameters),
		FileURI:     twigExtensionFileURI(path),
		SourceLine:  line,
		Deprecated:  deprecated,
		Deprecation: deprecation,
	}
}

func (p *TwigExtensionCatalogProvider) resolveCallable(
	callable,
	path string,
) (string, string) {
	if separator := strings.LastIndex(callable, "::"); separator >= 0 {
		return strings.TrimPrefix(callable[:separator], `\`),
			callable[separator+2:]
	}
	if separator := strings.LastIndex(callable, "->"); separator >= 0 {
		method := callable[separator+2:]
		if p != nil && p.php != nil && path != "" {
			for _, class := range p.php.ClassSymbolsIn(path) {
				for _, candidate := range p.php.FindMethods(
					class.FullyQualified,
					method,
				) {
					if candidate.Path == path {
						return class.FullyQualified, method
					}
				}
			}
		}
		return "", method
	}
	return "", callable
}

func twigExtensionParameters(
	parameters []twig.TwigParameter,
) []TwigExtensionCatalogParameter {
	if len(parameters) == 0 {
		return nil
	}
	result := make([]TwigExtensionCatalogParameter, 0, len(parameters))
	for _, parameter := range parameters {
		name := strings.TrimPrefix(strings.TrimSpace(parameter.Name), "$")
		if name == "" || strings.HasPrefix(name, "_") {
			continue
		}
		result = append(result, TwigExtensionCatalogParameter{
			Name:     name,
			Type:     parameter.Type,
			Optional: parameter.Optional,
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func enabledByDefault(value *bool) bool {
	return value == nil || *value
}

func twigExtensionNameMatches(name, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(name), search)
}

func twigExtensionFileURI(path string) string {
	if path == "" {
		return ""
	}
	return uriutil.FileURI(path)
}

func uniqueTwigExtensionEntries(
	entries []TwigExtensionCatalogEntry,
) []TwigExtensionCatalogEntry {
	seen := make(map[string]struct{}, len(entries))
	result := make([]TwigExtensionCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		key := strings.Join([]string{
			entry.Type,
			strings.ToLower(entry.Name),
			entry.Callable,
			entry.ClassName,
			entry.FileURI,
			fmt.Sprint(entry.SourceLine),
		}, "\x00")
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	return result
}

func twigExtensionTypeOrder(kind string) int {
	switch kind {
	case "filter":
		return 0
	case "function":
		return 1
	case "test":
		return 2
	default:
		return 3
	}
}

var _ lsp.CommandProvider = (*TwigExtensionCatalogProvider)(nil)
