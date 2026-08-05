package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const ListTwigComponentsCommand = "shopware/symfony/analytics/twig/components"

type TwigComponentCatalogProvider struct {
	index *twigcomponent.Index
}

func NewTwigComponentCatalogProvider(
	index *twigcomponent.Index,
) *TwigComponentCatalogProvider {
	return &TwigComponentCatalogProvider{index: index}
}

type TwigComponentCatalogRequest struct {
	Search string `json:"search,omitempty"`
}

type TwigComponentDeclarationEntry struct {
	Class              string `json:"class,omitempty"`
	Template           string `json:"template,omitempty"`
	TemplateFromMethod string `json:"templateFromMethod,omitempty"`
	Source             string `json:"source"`
	FileURI            string `json:"fileUri,omitempty"`
	SourceLine         int    `json:"sourceLine,omitempty"`
	Live               bool   `json:"live,omitempty"`
	ExposePublicProps  bool   `json:"exposePublicProps,omitempty"`
}

type TwigComponentTemplateEntry struct {
	Template string `json:"template,omitempty"`
	FileURI  string `json:"fileUri"`
}

type TwigComponentPropEntry struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
	Description  string `json:"description,omitempty"`
	Class        string `json:"class,omitempty"`
	Member       string `json:"member,omitempty"`
	FileURI      string `json:"fileUri,omitempty"`
	SourceLine   int    `json:"sourceLine,omitempty"`
	Live         bool   `json:"live,omitempty"`
	Writable     bool   `json:"writable,omitempty"`
}

type TwigComponentBlockEntry struct {
	Name    string `json:"name"`
	FileURI string `json:"fileUri,omitempty"`
	Line    int    `json:"line,omitempty"`
	Print   string `json:"print"`
	Compose string `json:"compose"`
}

type TwigComponentUsageEntry struct {
	Syntax  string `json:"syntax"`
	FileURI string `json:"fileUri"`
	Line    int    `json:"line,omitempty"`
}

type TwigComponentSyntaxEntry struct {
	HTMLTag     string `json:"htmlTag"`
	Function    string `json:"function"`
	Composition string `json:"composition"`
}

type TwigComponentCatalogEntry struct {
	Name         string                          `json:"name"`
	Declarations []TwigComponentDeclarationEntry `json:"declarations,omitempty"`
	Templates    []TwigComponentTemplateEntry    `json:"templates,omitempty"`
	Props        []TwigComponentPropEntry        `json:"props,omitempty"`
	Computed     []TwigComponentPropEntry        `json:"computed,omitempty"`
	Blocks       []TwigComponentBlockEntry       `json:"blocks,omitempty"`
	Usages       []TwigComponentUsageEntry       `json:"usages,omitempty"`
	Syntax       TwigComponentSyntaxEntry        `json:"syntax"`
}

func (p *TwigComponentCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListTwigComponentsCommand: p.list,
	}
}

func (p *TwigComponentCatalogProvider) list(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request TwigComponentCatalogRequest
	if raw != nil && len(*raw) != 0 && string(*raw) != "null" {
		if err := json.Unmarshal(*raw, &request); err != nil {
			return nil, fmt.Errorf(
				"invalid twig component catalog request: %w",
				err,
			)
		}
	}
	return p.Catalog(ctx, request)
}

func (p *TwigComponentCatalogProvider) Catalog(
	ctx context.Context,
	request TwigComponentCatalogRequest,
) ([]TwigComponentCatalogEntry, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("twig component catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	components, err := p.index.Components()
	if err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(request.Search))
	grouped := make(map[string][]twigcomponent.Component)
	names := make(map[string]string)
	for _, component := range components {
		if component.Name == "" ||
			search != "" &&
				!strings.Contains(strings.ToLower(component.Name), search) {
			continue
		}
		key := strings.ToLower(component.Name)
		grouped[key] = append(grouped[key], component)
		if names[key] == "" {
			names[key] = component.Name
		}
	}
	lines := newSourceLineResolver()
	result := make([]TwigComponentCatalogEntry, 0, len(grouped))
	for key, declarations := range grouped {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry, entryErr := p.catalogEntry(
			names[key],
			declarations,
			lines,
		)
		if entryErr != nil {
			return nil, entryErr
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (p *TwigComponentCatalogProvider) catalogEntry(
	name string,
	components []twigcomponent.Component,
	lines *sourceLineResolver,
) (TwigComponentCatalogEntry, error) {
	literal := strings.ReplaceAll(name, `'`, `\'`)
	entry := TwigComponentCatalogEntry{
		Name: name,
		Syntax: TwigComponentSyntaxEntry{
			HTMLTag:  "<twig:" + name + "></twig:" + name + ">",
			Function: "{{ component('" + literal + "') }}",
		},
	}
	for _, component := range components {
		declaration := TwigComponentDeclarationEntry{
			Class:              component.Class,
			Template:           component.Template,
			TemplateFromMethod: component.TemplateFromMethod,
			Source:             twigComponentSource(component.Source),
			Live:               component.Live,
			ExposePublicProps:  component.ExposePublicProps,
		}
		if component.File != "" {
			declaration.FileURI = uriutil.FileURI(component.File)
			offset := component.NameRange.Start
			if component.NameRange.Len() == 0 {
				offset = component.ClassRange.Start
			}
			declaration.SourceLine = lines.line(component.File, offset)
		}
		entry.Declarations = append(entry.Declarations, declaration)
		files, err := p.index.TemplateFiles(component)
		if err != nil {
			return TwigComponentCatalogEntry{}, err
		}
		for _, file := range files {
			entry.Templates = append(entry.Templates, TwigComponentTemplateEntry{
				Template: component.Template,
				FileURI:  uriutil.FileURI(file),
			})
		}
	}
	props, err := p.index.Props(name)
	if err != nil {
		return TwigComponentCatalogEntry{}, err
	}
	entry.Props = twigComponentProps(props, lines)
	computed, err := p.index.Computed(name)
	if err != nil {
		return TwigComponentCatalogEntry{}, err
	}
	entry.Computed = twigComponentProps(computed, lines)
	blocks, err := p.index.Blocks(name)
	if err != nil {
		return TwigComponentCatalogEntry{}, err
	}
	var composition strings.Builder
	composition.WriteString("{% component '")
	composition.WriteString(literal)
	composition.WriteString("' %}")
	for _, block := range blocks {
		blockName := strings.ReplaceAll(block.Name, `'`, "")
		entry.Blocks = append(entry.Blocks, TwigComponentBlockEntry{
			Name:    block.Name,
			FileURI: twigComponentFileURI(block.File),
			Line:    block.Line,
			Print:   "{{ block('" + blockName + "') }}",
			Compose: "{% block " + blockName + " %}{% endblock %}",
		})
		composition.WriteString("{% block ")
		composition.WriteString(blockName)
		composition.WriteString(" %}{% endblock %}")
	}
	composition.WriteString("{% endcomponent %}")
	entry.Syntax.Composition = composition.String()
	usages, err := p.index.Usages(name)
	if err != nil {
		return TwigComponentCatalogEntry{}, err
	}
	for _, usage := range usages {
		entry.Usages = append(entry.Usages, TwigComponentUsageEntry{
			Syntax:  usage.Kind.String(),
			FileURI: twigComponentFileURI(usage.File),
			Line:    lines.line(usage.File, usage.Range.Start),
		})
	}
	entry.Declarations = uniqueComponentDeclarations(entry.Declarations)
	entry.Templates = uniqueComponentTemplates(entry.Templates)
	entry.Usages = uniqueComponentUsages(entry.Usages)
	return entry, nil
}

func twigComponentProps(
	props []twigcomponent.Prop,
	lines *sourceLineResolver,
) []TwigComponentPropEntry {
	result := make([]TwigComponentPropEntry, 0, len(props))
	for _, prop := range props {
		entry := TwigComponentPropEntry{
			Name:         prop.Name,
			Type:         prop.Type,
			DefaultValue: prop.DefaultValue,
			Description:  prop.Description,
			Class:        prop.Class,
			Member:       prop.Member,
			Live:         prop.Live,
			Writable:     prop.Writable,
		}
		if prop.File != "" {
			entry.FileURI = uriutil.FileURI(prop.File)
			entry.SourceLine = lines.line(prop.File, prop.Range.Start)
		}
		result = append(result, entry)
	}
	return result
}

func twigComponentSource(source twigcomponent.SourceKind) string {
	switch source {
	case twigcomponent.ServiceSource:
		return "service"
	case twigcomponent.CompiledContainerSource:
		return "compiledContainer"
	case twigcomponent.AnonymousTemplateSource:
		return "anonymousTemplate"
	default:
		return "attribute"
	}
}

func twigComponentFileURI(path string) string {
	if path == "" {
		return ""
	}
	return uriutil.FileURI(path)
}

func uniqueComponentDeclarations(
	values []TwigComponentDeclarationEntry,
) []TwigComponentDeclarationEntry {
	seen := make(map[string]struct{}, len(values))
	result := make([]TwigComponentDeclarationEntry, 0, len(values))
	for _, value := range values {
		key := value.Class + "\x00" + value.Template + "\x00" +
			value.FileURI + "\x00" + fmt.Sprint(value.SourceLine)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueComponentTemplates(
	values []TwigComponentTemplateEntry,
) []TwigComponentTemplateEntry {
	seen := make(map[string]struct{}, len(values))
	result := make([]TwigComponentTemplateEntry, 0, len(values))
	for _, value := range values {
		key := value.Template + "\x00" + value.FileURI
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].FileURI < result[right].FileURI
	})
	return result
}

func uniqueComponentUsages(
	values []TwigComponentUsageEntry,
) []TwigComponentUsageEntry {
	seen := make(map[string]struct{}, len(values))
	result := make([]TwigComponentUsageEntry, 0, len(values))
	for _, value := range values {
		key := value.Syntax + "\x00" + value.FileURI + "\x00" +
			fmt.Sprint(value.Line)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].FileURI != result[right].FileURI {
			return result[left].FileURI < result[right].FileURI
		}
		return result[left].Line < result[right].Line
	})
	return result
}

var _ lsp.CommandProvider = (*TwigComponentCatalogProvider)(nil)
