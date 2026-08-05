package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const ListTwigTemplateVariablesCommand = "shopware/symfony/analytics/twig/templateVariables"

type TwigTemplateVariableCatalogProvider struct {
	root       string
	twig       *twig.TwigIndexer
	php        *php.PHPIndex
	components *twigcomponent.Index
}

func NewTwigTemplateVariableCatalogProvider(
	root string,
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
	components *twigcomponent.Index,
) *TwigTemplateVariableCatalogProvider {
	return &TwigTemplateVariableCatalogProvider{
		root:       filepath.Clean(root),
		twig:       twigIndex,
		php:        phpIndex,
		components: components,
	}
}

type TwigTemplateVariableCatalogRequest struct {
	Template string `json:"template,omitempty"`
	FileGlob string `json:"fileGlob,omitempty"`
}

type TwigTemplateVariableSource struct {
	Kind    string `json:"kind"`
	FileURI string `json:"fileUri,omitempty"`
	Line    int    `json:"line,omitempty"`
}

type TwigTemplateVariableProperty struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Type       string `json:"type,omitempty"`
	Class      string `json:"class,omitempty"`
	FileURI    string `json:"fileUri,omitempty"`
	SourceLine int    `json:"sourceLine,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

type TwigTemplateVariableEntry struct {
	Name       string                         `json:"name"`
	Type       string                         `json:"type"`
	Types      []string                       `json:"types,omitempty"`
	Properties []TwigTemplateVariableProperty `json:"properties,omitempty"`
	Sources    []TwigTemplateVariableSource   `json:"sources,omitempty"`
}

type TwigTemplateVariableCatalogEntry struct {
	Template  string                       `json:"template"`
	Files     []TwigTemplateSourceLocation `json:"files,omitempty"`
	Variables []TwigTemplateVariableEntry  `json:"variables,omitempty"`
}

type twigVariableAccumulator struct {
	name    string
	types   map[string]string
	sources []TwigTemplateVariableSource
}

func (p *TwigTemplateVariableCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListTwigTemplateVariablesCommand: p.list,
	}
}

func (p *TwigTemplateVariableCatalogProvider) list(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request TwigTemplateVariableCatalogRequest
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil, fmt.Errorf(
			"at least one of template or fileGlob is required",
		)
	}
	if err := json.Unmarshal(*raw, &request); err != nil {
		return nil, fmt.Errorf(
			"invalid twig template variable request: %w",
			err,
		)
	}
	return p.Catalog(ctx, request)
}

func (p *TwigTemplateVariableCatalogProvider) Catalog(
	ctx context.Context,
	request TwigTemplateVariableCatalogRequest,
) ([]TwigTemplateVariableCatalogEntry, error) {
	if p == nil || p.twig == nil || p.php == nil {
		return nil, fmt.Errorf("twig template variable catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Template) == "" &&
		strings.TrimSpace(request.FileGlob) == "" {
		return nil, fmt.Errorf(
			"at least one of template or fileGlob is required",
		)
	}
	names, err := p.resolveTemplateVariableNames(request)
	if err != nil {
		return nil, err
	}
	globals, err := p.twig.GetAllGlobals()
	if err != nil {
		return nil, err
	}
	lines := newSourceLineResolver()
	result := make([]TwigTemplateVariableCatalogEntry, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files, fileErr := p.twig.GetTwigFilesByRelPath(name)
		if fileErr != nil {
			return nil, fileErr
		}
		files = uniqueTwigFiles(files)
		entry, entryErr := p.variableEntry(name, files, globals, lines)
		if entryErr != nil {
			return nil, entryErr
		}
		result = append(result, entry)
	}
	return result, nil
}

func (p *TwigTemplateVariableCatalogProvider) resolveTemplateVariableNames(
	request TwigTemplateVariableCatalogRequest,
) ([]string, error) {
	selected := make(map[string]struct{})
	for _, input := range strings.Split(request.Template, ",") {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		files, err := p.twig.GetTwigFilesByRelPath(input)
		if err != nil {
			return nil, err
		}
		if len(files) != 0 {
			selected[input] = struct{}{}
		}
		for _, name := range p.templateNamesForPathInput(input) {
			selected[name] = struct{}{}
		}
	}
	fileGlob := filepath.ToSlash(strings.TrimSpace(request.FileGlob))
	if fileGlob != "" {
		names, err := p.twig.GetAllTemplateFiles()
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			files, fileErr := p.twig.GetTwigFilesByRelPath(name)
			if fileErr != nil {
				return nil, fileErr
			}
			if p.templateFilesMatchGlob(uniqueTwigFiles(files), fileGlob) {
				selected[name] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(selected))
	for name := range selected {
		result = append(result, name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result, nil
}

func (p *TwigTemplateVariableCatalogProvider) variableEntry(
	template string,
	files []twig.TwigFile,
	globals []twig.Global,
	lines *sourceLineResolver,
) (TwigTemplateVariableCatalogEntry, error) {
	entry := TwigTemplateVariableCatalogEntry{Template: template}
	values := make(map[string]*twigVariableAccumulator)
	add := func(
		name,
		typeName string,
		source TwigTemplateVariableSource,
	) {
		name = strings.TrimPrefix(strings.TrimSpace(name), "$")
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		current := values[key]
		if current == nil {
			current = &twigVariableAccumulator{
				name:  name,
				types: make(map[string]string),
			}
			values[key] = current
		}
		addTwigVariableTypes(current.types, typeName)
		current.sources = append(current.sources, source)
	}

	for _, file := range files {
		entry.Files = append(entry.Files, TwigTemplateSourceLocation{
			FileURI: uriutil.FileURI(file.Path),
			Line:    1,
		})
	}
	inputs, err := p.twig.GetTemplateVariables(template)
	if err != nil {
		return TwigTemplateVariableCatalogEntry{}, err
	}
	for _, variable := range inputs {
		add(variable.Name, "", TwigTemplateVariableSource{
			Kind:    "templateInput",
			FileURI: twigComponentFileURI(variable.FilePath),
			Line:    lines.line(variable.FilePath, variable.Range.Start),
		})
	}
	provided, err := p.php.TwigTemplateVariables(template)
	if err != nil {
		return TwigTemplateVariableCatalogEntry{}, err
	}
	for _, variable := range provided {
		add(variable.Name, variable.Type, TwigTemplateVariableSource{
			Kind:    "controller",
			FileURI: twigComponentFileURI(variable.File),
			Line:    lines.line(variable.File, variable.Range.Start),
		})
	}
	for _, global := range globals {
		add(global.Name, global.Type, TwigTemplateVariableSource{
			Kind:    "global",
			FileURI: twigComponentFileURI(global.File),
			Line:    lines.line(global.File, global.Range.Start),
		})
	}
	for _, file := range files {
		annotations, annotationErr := twigTemplateAnnotations(file.Path)
		if annotationErr != nil {
			return TwigTemplateVariableCatalogEntry{}, annotationErr
		}
		for name, typeName := range annotations {
			add(name, typeName, TwigTemplateVariableSource{
				Kind:    "annotation",
				FileURI: uriutil.FileURI(file.Path),
				Line:    1,
			})
		}
		if p.components == nil {
			continue
		}
		_, props, propErr := p.components.ContextForTemplate(file.Path, nil)
		if propErr != nil {
			return TwigTemplateVariableCatalogEntry{}, propErr
		}
		for _, prop := range props {
			add(prop.Name, prop.Type, TwigTemplateVariableSource{
				Kind:    "componentProp",
				FileURI: twigComponentFileURI(prop.File),
				Line:    lines.line(prop.File, prop.Range.Start),
			})
		}
	}

	for _, value := range values {
		typeNames := sortedTwigVariableTypes(value.types)
		typeName := strings.Join(typeNames, "|")
		if typeName == "" {
			typeName = "unknown"
		}
		entry.Variables = append(entry.Variables, TwigTemplateVariableEntry{
			Name:       value.name,
			Type:       typeName,
			Types:      typeNames,
			Properties: p.twigVariableProperties(typeNames, lines),
			Sources:    uniqueTwigVariableSources(value.sources),
		})
	}
	sort.Slice(entry.Variables, func(left, right int) bool {
		return strings.ToLower(entry.Variables[left].Name) <
			strings.ToLower(entry.Variables[right].Name)
	})
	return entry, nil
}

func twigTemplateAnnotations(path string) (map[string]string, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := twigparser.Parse(string(source))
	annotations := twig.TwigTypeAnnotations(result.Tree.Root)
	values := make(map[string]string, len(annotations))
	for name, value := range annotations {
		values[name] = value.String()
	}
	return values, nil
}

func addTwigVariableTypes(target map[string]string, value string) {
	parsed, err := types.Parse(strings.TrimSpace(value))
	if err == nil && !parsed.IsUnknown() {
		for _, member := range flattenTwigVariableTypes(parsed) {
			rendered := member.String()
			if rendered != "" && rendered != "unknown" {
				target[strings.ToLower(rendered)] = rendered
			}
		}
		return
	}
	for _, member := range strings.Split(value, "|") {
		member = strings.TrimSpace(member)
		if member != "" && !strings.EqualFold(member, "unknown") {
			target[strings.ToLower(member)] = member
		}
	}
}

func flattenTwigVariableTypes(value types.Type) []types.Type {
	if value.Kind() == types.UnionKind {
		var result []types.Type
		for index := 0; index < value.ArgumentCount(); index++ {
			result = append(
				result,
				flattenTwigVariableTypes(value.Argument(index))...,
			)
		}
		return result
	}
	return []types.Type{value}
}

func sortedTwigVariableTypes(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result
}

func (p *TwigTemplateVariableCatalogProvider) twigVariableProperties(
	typeNames []string,
	lines *sourceLineResolver,
) []TwigTemplateVariableProperty {
	classes := make(map[string]string)
	var shapes []types.Type
	for _, typeName := range typeNames {
		value, err := types.Parse(typeName)
		if err != nil {
			continue
		}
		collectTwigVariableClasses(value, classes, &shapes)
	}
	entries := make(map[string]TwigTemplateVariableProperty)
	for _, shape := range shapes {
		for fieldIndex := 0; fieldIndex < shape.FieldCount(); fieldIndex++ {
			field := shape.Field(fieldIndex)
			key := strings.ToLower(field.Name)
			entries[key] = TwigTemplateVariableProperty{
				Name: field.Name,
				Kind: "shapeField",
				Type: field.Type.String(),
			}
		}
	}
	for _, className := range classes {
		for _, property := range p.php.Properties(className) {
			if property.Visibility != semantic.Public ||
				property.Flags.Has(semantic.StaticFlag) {
				continue
			}
			name := strings.TrimPrefix(property.Name, "$")
			key := strings.ToLower(name)
			entries[key] = TwigTemplateVariableProperty{
				Name:       name,
				Kind:       "property",
				Type:       property.Type.String(),
				Class:      className,
				FileURI:    twigComponentFileURI(property.Path),
				SourceLine: lines.line(property.Path, property.SelectionRange.Start),
				Deprecated: property.Flags.Has(semantic.DeprecatedFlag),
			}
		}
		for _, method := range p.php.Methods(className) {
			if method.Visibility != semantic.Public ||
				method.Flags.Has(semantic.StaticFlag) {
				continue
			}
			lower := strings.ToLower(method.Name)
			if strings.HasPrefix(lower, "set") ||
				strings.HasPrefix(lower, "__") {
				continue
			}
			name := twig.TwigAttributeName(method.Name)
			kind := "getter"
			if name == "" {
				name = method.Name
				kind = "method"
			}
			key := strings.ToLower(name)
			if _, propertyWins := entries[key]; propertyWins {
				continue
			}
			entries[key] = TwigTemplateVariableProperty{
				Name:       name,
				Kind:       kind,
				Type:       method.ReturnType.String(),
				Class:      className,
				FileURI:    twigComponentFileURI(method.Path),
				SourceLine: lines.line(method.Path, method.SelectionRange.Start),
				Deprecated: method.Flags.Has(semantic.DeprecatedFlag),
			}
		}
	}
	result := make([]TwigTemplateVariableProperty, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result
}

func collectTwigVariableClasses(
	value types.Type,
	classes map[string]string,
	shapes *[]types.Type,
) {
	switch value.Kind() {
	case types.ObjectKind:
		if value.Name() != "" {
			classes[strings.ToLower(value.Name())] = value.Name()
		}
	case types.ObjectShapeKind, types.ArrayShapeKind:
		*shapes = append(*shapes, value)
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		collectTwigVariableClasses(value.Argument(index), classes, shapes)
	}
}

func uniqueTwigVariableSources(
	values []TwigTemplateVariableSource,
) []TwigTemplateVariableSource {
	seen := make(map[string]struct{}, len(values))
	result := make([]TwigTemplateVariableSource, 0, len(values))
	for _, value := range values {
		key := value.Kind + "\x00" + value.FileURI + "\x00" +
			fmt.Sprint(value.Line)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Kind != result[right].Kind {
			return result[left].Kind < result[right].Kind
		}
		if result[left].FileURI != result[right].FileURI {
			return result[left].FileURI < result[right].FileURI
		}
		return result[left].Line < result[right].Line
	})
	return result
}

func (p *TwigTemplateVariableCatalogProvider) templateNamesForPathInput(
	input string,
) []string {
	input = strings.TrimSpace(input)
	if input == "" || filepath.IsAbs(input) {
		return nil
	}
	path := filepath.Clean(filepath.Join(p.root, filepath.FromSlash(input)))
	relative, err := filepath.Rel(p.root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	if file, found, _ := p.twig.GetTwigFileByPath(path); found {
		return twig.TemplateNames(file.Path)
	}
	return nil
}

func (p *TwigTemplateVariableCatalogProvider) templateFilesMatchGlob(
	files []twig.TwigFile,
	glob string,
) bool {
	for _, file := range files {
		relative, err := filepath.Rel(p.root, file.Path)
		if err != nil || relative == ".." ||
			strings.HasPrefix(
				relative,
				".."+string(filepath.Separator),
			) {
			continue
		}
		if antPathMatch(glob, filepath.ToSlash(relative)) {
			return true
		}
	}
	return false
}

var _ lsp.CommandProvider = (*TwigTemplateVariableCatalogProvider)(nil)
