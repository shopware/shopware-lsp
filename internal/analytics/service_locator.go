package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const LocateServicesCommand = "shopware/symfony/analytics/services/locate"

type ServiceLocatorProvider struct {
	index    *symfony.ServiceIndex
	phpIndex *php.PHPIndex
}

func NewServiceLocatorProvider(
	index *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ServiceLocatorProvider {
	return &ServiceLocatorProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

type ServiceLocatorRequest struct {
	Identifier string `json:"identifier"`
}

type ServiceDefinitionSourceEntry struct {
	Source     string `json:"source"`
	FileURI    string `json:"fileUri,omitempty"`
	SourceLine int    `json:"sourceLine,omitempty"`
	EndLine    int    `json:"endLine,omitempty"`
	Preview    string `json:"preview,omitempty"`
}

type ServiceLocatorEntry struct {
	ID                 string                         `json:"id"`
	ClassName          string                         `json:"className,omitempty"`
	ResolvedClass      string                         `json:"resolvedClass,omitempty"`
	AliasTarget        string                         `json:"aliasTarget,omitempty"`
	Decorates          string                         `json:"decorates,omitempty"`
	Parent             string                         `json:"parent,omitempty"`
	Autowire           bool                           `json:"autowire,omitempty"`
	AutowireConfigured bool                           `json:"autowireConfigured,omitempty"`
	Deprecated         bool                           `json:"deprecated,omitempty"`
	Deprecation        string                         `json:"deprecation,omitempty"`
	Tags               []string                       `json:"tags,omitempty"`
	ClassFileURI       string                         `json:"classFileUri,omitempty"`
	ClassLine          int                            `json:"classLine,omitempty"`
	Definitions        []ServiceDefinitionSourceEntry `json:"definitions"`
}

func (p *ServiceLocatorProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		LocateServicesCommand: p.locate,
	}
}

func (p *ServiceLocatorProvider) locate(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil, fmt.Errorf("service identifier is required")
	}
	var request ServiceLocatorRequest
	if err := json.Unmarshal(*raw, &request); err != nil {
		return nil, fmt.Errorf("invalid service locator request: %w", err)
	}
	return p.Locate(ctx, request)
}

func (p *ServiceLocatorProvider) Locate(
	ctx context.Context,
	request ServiceLocatorRequest,
) ([]ServiceLocatorEntry, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("service locator is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	identifier := strings.TrimSpace(request.Identifier)
	if identifier == "" {
		return nil, fmt.Errorf("service identifier is required")
	}
	definitions, err := p.index.GetAllServiceDefinitions()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]symfony.Service, len(definitions))
	for _, service := range definitions {
		if service.ID != "" {
			byID[strings.ToLower(service.ID)] = service
		}
	}
	classQuery := strings.Contains(identifier, `\`)
	normalizedClass := normalizeServiceClass(identifier)
	var matched []symfony.Service
	for _, service := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved := resolveCatalogServiceClass(service, byID)
		if classQuery {
			if strings.EqualFold(resolved, normalizedClass) ||
				strings.EqualFold(
					normalizeServiceClass(service.Class),
					normalizedClass,
				) ||
				strings.EqualFold(
					normalizeServiceClass(service.ID),
					normalizedClass,
				) {
				matched = append(matched, service)
			}
			continue
		}
		if strings.EqualFold(service.ID, identifier) {
			matched = append(matched, service)
		}
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("service %q was not found", identifier)
	}
	explicit, err := p.index.ServiceDefinitions()
	if err != nil {
		return nil, err
	}
	explicitSources := make(map[string]struct{}, len(explicit))
	for _, service := range explicit {
		explicitSources[serviceSourceKey(service)] = struct{}{}
	}
	lines := newSourceLineResolver()
	result := make([]ServiceLocatorEntry, 0, len(matched))
	for _, service := range matched {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		effective, found, effectiveErr := p.index.GetServiceByID(service.ID)
		if effectiveErr != nil {
			return nil, effectiveErr
		}
		if found {
			service = effective
		}
		resolvedClass := resolveCatalogServiceClass(service, byID)
		entry := ServiceLocatorEntry{
			ID:                 service.ID,
			ClassName:          normalizeServiceClass(service.Class),
			ResolvedClass:      resolvedClass,
			AliasTarget:        service.AliasTarget,
			Decorates:          service.Decorates,
			Parent:             service.Parent,
			Autowire:           service.Autowire,
			AutowireConfigured: service.AutowireSet,
			Deprecated:         service.Deprecated,
			Deprecation:        service.Deprecation,
			Tags:               serviceTagNames(service.Tags),
		}
		if p.phpIndex != nil && resolvedClass != "" {
			if symbol, classFound := p.phpIndex.FindClass(
				resolvedClass,
			); classFound {
				entry.ClassFileURI = uriutil.FileURI(symbol.Path)
				offset := symbol.SelectionRange.Start
				if symbol.SelectionRange.Len() == 0 {
					offset = symbol.Range.Start
				}
				entry.ClassLine = lines.line(symbol.Path, offset)
			}
		}
		declarations, declarationsErr := p.index.ServiceDeclarations(
			service.ID,
		)
		if declarationsErr != nil {
			return nil, declarationsErr
		}
		seenSources := make(map[string]struct{})
		for _, declaration := range declarations {
			key := serviceSourceKey(declaration)
			if _, duplicate := seenSources[key]; duplicate {
				continue
			}
			seenSources[key] = struct{}{}
			source := "prototype"
			if _, isExplicit := explicitSources[key]; isExplicit {
				source = "explicit"
			}
			entry.Definitions = append(
				entry.Definitions,
				serviceDefinitionSource(declaration, source, lines),
			)
		}
		if len(entry.Definitions) == 0 {
			entry.Definitions = []ServiceDefinitionSourceEntry{{
				Source: "compiled",
			}}
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].ID) <
			strings.ToLower(result[right].ID)
	})
	return result, nil
}

func resolveCatalogServiceClass(
	service symfony.Service,
	byID map[string]symfony.Service,
) string {
	visited := make(map[string]struct{})
	for {
		key := strings.ToLower(service.ID)
		if key != "" {
			if _, duplicate := visited[key]; duplicate {
				return ""
			}
			visited[key] = struct{}{}
		}
		if service.AliasTarget != "" {
			target, found := byID[strings.ToLower(service.AliasTarget)]
			if !found {
				return ""
			}
			service = target
			continue
		}
		if className := normalizeServiceClass(service.Class); className != "" &&
			isStaticServiceClass(className) {
			return className
		}
		if service.Parent != "" {
			parent, found := byID[strings.ToLower(service.Parent)]
			if found {
				service = parent
				continue
			}
		}
		if className := normalizeServiceClass(service.ID); className != "" &&
			strings.Contains(className, `\`) &&
			isStaticServiceClass(className) {
			return className
		}
		return ""
	}
}

func normalizeServiceClass(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), `\`)
}

func isStaticServiceClass(value string) bool {
	return value != "" && !strings.ContainsAny(value, "%${}@ \t")
}

func serviceTagNames(tags map[string]string) []string {
	result := make([]string, 0, len(tags))
	for tag := range tags {
		if tag != "" {
			result = append(result, tag)
		}
	}
	sort.Strings(result)
	return result
}

func serviceSourceKey(service symfony.Service) string {
	return strings.Join([]string{
		strings.ToLower(service.ID),
		service.Path,
		fmt.Sprint(service.Line),
		service.Range.String(),
	}, "\x00")
}

func serviceDefinitionSource(
	service symfony.Service,
	source string,
	lines *sourceLineResolver,
) ServiceDefinitionSourceEntry {
	entry := ServiceDefinitionSourceEntry{Source: source}
	if service.Path == "" {
		return entry
	}
	entry.FileURI = uriutil.FileURI(service.Path)
	entry.SourceLine = service.Line
	if entry.SourceLine == 0 {
		entry.SourceLine = lines.line(service.Path, service.Range.Start)
	}
	entry.Preview, entry.EndLine = serviceSourcePreview(
		service.Path,
		service.Range,
		entry.SourceLine,
		lines,
	)
	return entry
}

func serviceSourcePreview(
	path string,
	rng cst.TextRange,
	sourceLine int,
	lines *sourceLineResolver,
) (string, int) {
	source, err := os.ReadFile(path)
	if err != nil {
		return "", 0
	}
	start := int(rng.Start)
	end := int(rng.End)
	if rng.Len() == 0 || start < 0 || end > len(source) || start >= end {
		textLines := strings.Split(
			strings.ReplaceAll(string(source), "\r\n", "\n"),
			"\n",
		)
		if sourceLine <= 0 || sourceLine > len(textLines) {
			return "", 0
		}
		return truncateServicePreviewLines(
			[]string{textLines[sourceLine-1]},
		), sourceLine
	}
	endOffset := rng.End - 1
	endLine := lines.line(path, endOffset)
	previewLines := strings.Split(
		strings.ReplaceAll(string(source[start:end]), "\r\n", "\n"),
		"\n",
	)
	return truncateServicePreviewLines(previewLines), endLine
}

func truncateServicePreviewLines(lines []string) string {
	const (
		maxLines     = 80
		maxLineWidth = 120
	)
	if len(lines) > maxLines {
		lines = append(append([]string(nil), lines[:maxLines]...), "...")
	}
	for index, line := range lines {
		if len(line) > maxLineWidth {
			lines[index] = line[:maxLineWidth] + "..."
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
