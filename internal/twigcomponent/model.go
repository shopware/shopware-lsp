package twigcomponent

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type SourceKind uint8

const (
	AttributeSource SourceKind = iota
	ServiceSource
	CompiledContainerSource
	AnonymousTemplateSource
)

func (source SourceKind) String() string {
	switch source {
	case AttributeSource:
		return "PHP attribute"
	case ServiceSource:
		return "service tag"
	case CompiledContainerSource:
		return "compiled container"
	case AnonymousTemplateSource:
		return "anonymous template"
	default:
		return "component"
	}
}

type Declaration struct {
	Name               string
	Class              string
	Template           string
	TemplateFromMethod string
	File               string
	NameRange          cst.TextRange
	ClassRange         cst.TextRange
	TemplateRange      cst.TextRange
	Source             SourceKind
	ExposePublicProps  bool
	Live               bool
}

type Namespace struct {
	ClassPrefix       string
	TemplateDirectory string
	NamePrefix        string
	File              string
	Range             cst.TextRange
}

type Prop struct {
	Name         string
	Type         string
	DefaultValue string
	Description  string
	File         string
	Class        string
	Member       string
	Range        cst.TextRange
	Live         bool
	Writable     bool
}

type UsageKind uint8

const (
	FunctionUsage UsageKind = iota
	BlockUsage
	HTMLUsage
)

func (kind UsageKind) String() string {
	switch kind {
	case FunctionUsage:
		return "component()"
	case BlockUsage:
		return "component tag"
	case HTMLUsage:
		return "HTML component"
	default:
		return "component"
	}
}

type Usage struct {
	Name  string
	File  string
	Range cst.TextRange
	Kind  UsageKind
}

type PropUsage struct {
	Component string
	Name      string
	Range     cst.TextRange
	Dynamic   bool
}

type Block struct {
	Name string
	File string
	Line int
}

type ComponentBlockUsage struct {
	Component string
	Name      string
	Range     cst.TextRange
}

type LiveActionParameter struct {
	Name     string
	PHPName  string
	Type     string
	Optional bool
	LiveArg  bool
	Range    cst.TextRange
}

type LiveAction struct {
	Name       string
	Class      string
	Method     string
	File       string
	Range      cst.TextRange
	Parameters []LiveActionParameter
}

type LiveActionReferenceKind uint8

const (
	LiveActionHelperReference LiveActionReferenceKind = iota
	LiveActionAttributeReference
)

type LiveActionReference struct {
	Name         string
	File         string
	Range        cst.TextRange
	ContextRange cst.TextRange
	Kind         LiveActionReferenceKind
}

type LiveActionArgumentReference struct {
	Action string
	Name   string
	File   string
	Range  cst.TextRange
}

type LiveListener struct {
	Name        string
	Class       string
	Method      string
	File        string
	Range       cst.TextRange
	MethodRange cst.TextRange
	Parameters  []LiveActionParameter
}

type LiveEventReferenceKind uint8

const (
	LiveEventEmitReference LiveEventReferenceKind = iota
	LiveEventEmitUpReference
	LiveEventEmitSelfReference
	LiveEventAttributeReference
)

type LiveEventReference struct {
	Name         string
	File         string
	Class        string
	Range        cst.TextRange
	ContextRange cst.TextRange
	Kind         LiveEventReferenceKind
}

type LiveEventArgumentReference struct {
	Event string
	Name  string
	File  string
	Range cst.TextRange
}

type Record struct {
	File                 string
	Declarations         []Declaration
	Namespaces           []Namespace
	AnonymousDirectories []string
	Props                []Prop
	Usages               []Usage
	LiveActions          []LiveAction
	LiveActionReferences []LiveActionReference
	LiveActionArguments  []LiveActionArgumentReference
	LiveListeners        []LiveListener
	LiveEventReferences  []LiveEventReference
	LiveEventArguments   []LiveEventArgumentReference
}

type Component struct {
	Name               string
	Class              string
	Template           string
	TemplateFromMethod string
	File               string
	NameRange          cst.TextRange
	ClassRange         cst.TextRange
	TemplateRange      cst.TextRange
	Source             SourceKind
	ExposePublicProps  bool
	Live               bool
}

func normalizeClass(name string) string {
	return strings.Trim(strings.TrimSpace(name), `\`)
}

func normalizeDirectory(directory string) string {
	directory = strings.TrimSpace(strings.ReplaceAll(directory, `\`, "/"))
	directory = strings.Trim(directory, "/")
	if directory == "" {
		return ""
	}
	return directory + "/"
}

func normalizeTemplate(template string) string {
	template = strings.TrimSpace(strings.ReplaceAll(template, `\`, "/"))
	if template == "" {
		return ""
	}
	if strings.Count(template, ":") >= 1 &&
		!strings.HasPrefix(template, "@") &&
		!strings.Contains(template, "/") {
		template = strings.ReplaceAll(template, ":", "/")
		if !strings.HasSuffix(template, ".twig") {
			template += ".html.twig"
		}
	}
	return strings.TrimPrefix(template, "/")
}

func normalizeComponentName(name string) string {
	return strings.Trim(strings.TrimSpace(name), ":")
}

func componentKey(component Component) string {
	return strings.ToLower(component.Name) + "\x00" +
		strings.ToLower(component.Class) + "\x00" +
		component.Template + "\x00" + component.File + "\x00" +
		component.TemplateFromMethod + "\x00" +
		component.NameRange.String()
}

func sortComponents(components []Component) {
	sort.Slice(components, func(left, right int) bool {
		leftName := strings.ToLower(components[left].Name)
		rightName := strings.ToLower(components[right].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if components[left].Source != components[right].Source {
			return components[left].Source < components[right].Source
		}
		if components[left].File != components[right].File {
			return components[left].File < components[right].File
		}
		return components[left].NameRange.Start <
			components[right].NameRange.Start
	})
}

func sortUsages(usages []Usage) {
	sort.Slice(usages, func(left, right int) bool {
		if usages[left].File != usages[right].File {
			return usages[left].File < usages[right].File
		}
		return usages[left].Range.Start < usages[right].Range.Start
	})
}
