package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	ListFormTypesCommand   = "shopware/symfony/analytics/forms/types"
	ListFormOptionsCommand = "shopware/symfony/analytics/forms/typeOptions"
)

type FormCatalogProvider struct {
	root     string
	index    *form.Index
	phpIndex *php.PHPIndex
}

func NewFormCatalogProvider(
	root string,
	index *form.Index,
	phpIndex *php.PHPIndex,
) *FormCatalogProvider {
	return &FormCatalogProvider{
		root:     filepath.Clean(root),
		index:    index,
		phpIndex: phpIndex,
	}
}

type FormTypeCatalogRequest struct {
	Query    string `json:"query,omitempty"`
	FileGlob string `json:"fileGlob,omitempty"`
}

type FormTypeCatalogEntry struct {
	Name         string   `json:"name"`
	ClassName    string   `json:"className"`
	Aliases      []string `json:"aliases,omitempty"`
	Parent       string   `json:"parent,omitempty"`
	DataClass    string   `json:"dataClass,omitempty"`
	FileURI      string   `json:"fileUri,omitempty"`
	SourceLine   int      `json:"sourceLine,omitempty"`
	OptionCount  int      `json:"optionCount"`
	FieldCount   int      `json:"fieldCount"`
	ViewVarCount int      `json:"viewVarCount"`
}

type FormOptionCatalogRequest struct {
	FormType string `json:"formType"`
}

type FormOptionSourceEntry struct {
	Kind       string `json:"kind"`
	ClassName  string `json:"className,omitempty"`
	FileURI    string `json:"fileUri,omitempty"`
	SourceLine int    `json:"sourceLine,omitempty"`
}

type FormOptionCatalogEntry struct {
	Name         string                  `json:"name"`
	Kinds        []string                `json:"kinds"`
	AllowedTypes []string                `json:"allowedTypes,omitempty"`
	Default      string                  `json:"default,omitempty"`
	SourceClass  string                  `json:"sourceClass,omitempty"`
	FileURI      string                  `json:"fileUri,omitempty"`
	SourceLine   int                     `json:"sourceLine,omitempty"`
	Sources      []FormOptionSourceEntry `json:"sources,omitempty"`
}

func (p *FormCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListFormTypesCommand:   p.listTypes,
		ListFormOptionsCommand: p.listOptions,
	}
}

func (p *FormCatalogProvider) listTypes(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request FormTypeCatalogRequest
	if raw != nil && len(*raw) != 0 && string(*raw) != "null" {
		if err := json.Unmarshal(*raw, &request); err != nil {
			return nil, fmt.Errorf(
				"invalid form type catalog request: %w",
				err,
			)
		}
	}
	return p.Types(ctx, request)
}

func (p *FormCatalogProvider) listOptions(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil, fmt.Errorf("formType is required")
	}
	var request FormOptionCatalogRequest
	if err := json.Unmarshal(*raw, &request); err != nil {
		return nil, fmt.Errorf(
			"invalid form option catalog request: %w",
			err,
		)
	}
	return p.Options(ctx, request)
}

func (p *FormCatalogProvider) Types(
	ctx context.Context,
	request FormTypeCatalogRequest,
) ([]FormTypeCatalogEntry, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("form type catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	types, err := p.index.GetTypes()
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(request.Query))
	fileGlob := filepath.ToSlash(strings.TrimSpace(request.FileGlob))
	lines := newSourceLineResolver()
	var result []FormTypeCatalogEntry
	for _, current := range types {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if query != "" && !formTypeCatalogMatches(
			FormTypeCatalogEntry{
				Name:      current.Class,
				ClassName: current.Class,
				Aliases:   current.Aliases,
				Parent:    current.Parent,
				DataClass: current.DataClass,
			},
			query,
		) {
			continue
		}
		file, offset := p.formTypeSource(current)
		if fileGlob != "" {
			relative := p.relativePath(file)
			if relative == "" || !antPathMatch(fileGlob, relative) {
				continue
			}
		}
		options, optionsErr := p.index.EffectiveOptions(current.Class)
		if optionsErr != nil {
			return nil, optionsErr
		}
		fields, fieldsErr := p.index.EffectiveFields(current.Class)
		if fieldsErr != nil {
			return nil, fieldsErr
		}
		viewVars, viewVarsErr := p.index.EffectiveViewVars(current.Class)
		if viewVarsErr != nil {
			return nil, viewVarsErr
		}
		names := append([]string{current.Class}, current.Aliases...)
		for _, name := range uniqueFoldedStrings(names) {
			entry := FormTypeCatalogEntry{
				Name:         name,
				ClassName:    current.Class,
				Aliases:      append([]string(nil), current.Aliases...),
				Parent:       current.Parent,
				DataClass:    current.DataClass,
				OptionCount:  len(options),
				FieldCount:   len(fields),
				ViewVarCount: countFormViewVars(viewVars),
			}
			if file != "" {
				entry.FileURI = uriutil.FileURI(file)
				entry.SourceLine = lines.line(file, offset)
			}
			if query != "" && !formTypeCatalogMatches(entry, query) {
				continue
			}
			result = append(result, entry)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if !strings.EqualFold(result[left].Name, result[right].Name) {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		return strings.ToLower(result[left].ClassName) <
			strings.ToLower(result[right].ClassName)
	})
	return result, nil
}

func (p *FormCatalogProvider) Options(
	ctx context.Context,
	request FormOptionCatalogRequest,
) ([]FormOptionCatalogEntry, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("form type catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(strings.TrimSpace(request.FormType), `\`)
	if name == "" {
		return nil, fmt.Errorf("formType is required")
	}
	current, found, err := p.index.GetType(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("form type %q was not found", name)
	}
	declarations, err := p.index.EffectiveOptionDeclarations(current.Class)
	if err != nil {
		return nil, err
	}
	if len(declarations) == 0 {
		return nil, fmt.Errorf("form type %q has no options", name)
	}
	lines := newSourceLineResolver()
	grouped := make(map[string]*FormOptionCatalogEntry)
	for _, option := range declarations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if option.Name == "" {
			continue
		}
		key := strings.ToLower(option.Name)
		entry := grouped[key]
		if entry == nil {
			entry = &FormOptionCatalogEntry{Name: option.Name}
			grouped[key] = entry
		}
		kind := formOptionKind(option.Kind)
		entry.Kinds = appendUniqueString(entry.Kinds, kind)
		entry.AllowedTypes = appendUniqueString(
			entry.AllowedTypes,
			option.AllowedTypes...,
		)
		if entry.Default == "" && option.Default != "" {
			entry.Default = option.Default
		}
		source := FormOptionSourceEntry{
			Kind:      kind,
			ClassName: option.Class,
		}
		if option.File != "" {
			source.FileURI = uriutil.FileURI(option.File)
			source.SourceLine = lines.line(option.File, option.Range.Start)
		}
		if !containsFormOptionSource(entry.Sources, source) {
			entry.Sources = append(entry.Sources, source)
		}
		if entry.SourceClass == "" {
			entry.SourceClass = source.ClassName
			entry.FileURI = source.FileURI
			entry.SourceLine = source.SourceLine
		}
	}
	result := make([]FormOptionCatalogEntry, 0, len(grouped))
	for _, entry := range grouped {
		sortFormOptionKinds(entry.Kinds)
		sort.Strings(entry.AllowedTypes)
		sort.Slice(entry.Sources, func(left, right int) bool {
			if entry.Sources[left].FileURI != entry.Sources[right].FileURI {
				return entry.Sources[left].FileURI <
					entry.Sources[right].FileURI
			}
			if entry.Sources[left].SourceLine !=
				entry.Sources[right].SourceLine {
				return entry.Sources[left].SourceLine <
					entry.Sources[right].SourceLine
			}
			return entry.Sources[left].Kind < entry.Sources[right].Kind
		})
		result = append(result, *entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (p *FormCatalogProvider) formTypeSource(
	current form.Type,
) (string, uint32) {
	if p.phpIndex != nil {
		if symbol, found := p.phpIndex.FindClass(current.Class); found {
			offset := symbol.SelectionRange.Start
			if symbol.SelectionRange.Len() == 0 {
				offset = symbol.Range.Start
			}
			return symbol.Path, offset
		}
	}
	offset := current.NameRange.Start
	if current.NameRange.Len() == 0 {
		offset = current.Range.Start
	}
	return current.File, offset
}

func (p *FormCatalogProvider) relativePath(path string) string {
	if path == "" || p.root == "" {
		return ""
	}
	relative, err := filepath.Rel(p.root, filepath.Clean(path))
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

func formTypeCatalogMatches(
	entry FormTypeCatalogEntry,
	query string,
) bool {
	values := append([]string{
		entry.Name,
		entry.ClassName,
		entry.Parent,
		entry.DataClass,
	}, entry.Aliases...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func uniqueFoldedStrings(values []string) []string {
	var result []string
	for _, value := range values {
		value = strings.TrimPrefix(strings.TrimSpace(value), `\`)
		if value == "" {
			continue
		}
		found := false
		for _, current := range result {
			if strings.EqualFold(current, value) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result
}

func countFormViewVars(viewVars []form.ViewVar) int {
	seen := make(map[string]struct{})
	for _, viewVar := range viewVars {
		if viewVar.Name != "" {
			seen[strings.ToLower(viewVar.Name)] = struct{}{}
		}
	}
	return len(seen)
}

func formOptionKind(kind form.OptionKind) string {
	switch kind {
	case form.RequiredOption:
		return "required"
	case form.DefinedOption:
		return "defined"
	case form.AllowedValuesOption:
		return "allowedValues"
	case form.AllowedTypesOption:
		return "allowedTypes"
	default:
		return "default"
	}
}

func appendUniqueString(target []string, values ...string) []string {
	for _, value := range values {
		if value == "" {
			continue
		}
		exists := false
		for _, current := range target {
			if strings.EqualFold(current, value) {
				exists = true
				break
			}
		}
		if !exists {
			target = append(target, value)
		}
	}
	return target
}

func containsFormOptionSource(
	sources []FormOptionSourceEntry,
	target FormOptionSourceEntry,
) bool {
	for _, source := range sources {
		if source.Kind == target.Kind &&
			source.ClassName == target.ClassName &&
			source.FileURI == target.FileURI &&
			source.SourceLine == target.SourceLine {
			return true
		}
	}
	return false
}

func sortFormOptionKinds(kinds []string) {
	order := map[string]int{
		"default":       0,
		"required":      1,
		"defined":       2,
		"allowedValues": 3,
		"allowedTypes":  4,
	}
	sort.Slice(kinds, func(left, right int) bool {
		return order[kinds[left]] < order[kinds[right]]
	})
}
