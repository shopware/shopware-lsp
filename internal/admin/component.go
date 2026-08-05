package admin

import (
	"path/filepath"
	"strconv"
	"strings"
)

// VueComponent represents a Shopware 6 Admin Vue component
type VueComponent struct {
	// Name is the component name (e.g., "sw-base-filter")
	Name string

	// Deprecated contains the normalized @deprecated documentation attached to
	// this component's own registration or definition. It is deliberately not
	// inherited from an extended parent component.
	Deprecated string

	// Kind describes how the component participates in the registry. Older
	// cached entries decode to the empty value, which is treated as register.
	Kind ComponentRegistrationKind

	// TargetComponent is populated for extend and override registrations.
	// For extend it is the parent, for override it is the overridden component.
	TargetComponent string

	// ExtendsComponent is the parent component name if this component extends another (empty for register)
	ExtendsComponent string

	// ImportPath is the path from the import statement (e.g., "src/app/component/filter/sw-base-filter/index")
	ImportPath string

	// FilePath is the absolute path to the registration file
	FilePath string

	// DefinitionPath is the resolved absolute path to the component definition file
	DefinitionPath string

	// Line is the line number where the component is registered
	Line int

	// Props contains the component's props definitions
	Props []VueComponentProp

	// ModelProp and ModelEvent retain an explicit legacy Options API `model`
	// contract. Empty values use Vue 3's modelValue/update:modelValue convention
	// (with value/input recognized conservatively for compat components).
	ModelProp  string
	ModelEvent string

	// Emits contains the component's emitted events
	Emits []string

	// Events contains source-aware emitted event declarations. Emits remains
	// persisted for compatibility with older caches and manually constructed
	// component fixtures.
	Events []VueComponentEvent

	// Methods contains the component's method names
	Methods []string

	// Computed contains the component's computed property names
	Computed []string

	// Data contains names returned from the component's data() function.
	Data []string

	// Injected contains services exposed through Vue's inject option.
	Injected []string

	// Mixins contains registered Shopware mixins used by the component.
	Mixins []string

	// LocalComponents contains Options API components that are available only
	// in this component's template. These aliases are not part of Shopware's
	// global component registry.
	LocalComponents []VueLocalComponent

	// LocalDirectives contains Options API directives that are available only
	// in this component's template. A local directive shadows an equally named
	// global directive for that template.
	LocalDirectives []VueLocalDirective

	// Members records source locations for values available in the component
	// template. The legacy name slices above remain persisted for compatibility.
	Members []VueComponentMember

	// OpenRuntimeMembers records that spreads or unresolved mixins can add
	// further instance members at runtime. Known members remain useful for
	// completion and navigation, but closed-world typo diagnostics must stay
	// conservative for this component and its descendants.
	OpenRuntimeMembers bool

	// Assignments retains direct writes to the Vue instance. They are resolved
	// only on the effective component so writes from overrides, child
	// components, and mixins can refine data declared elsewhere.
	Assignments []VueComponentAssignment

	// Slots contains the component's slot definitions
	Slots []VueComponentSlot

	// Blocks contains the component's Twig block definitions
	Blocks []TwigBlock

	// TemplatePath is the path to the Twig template (from the template import)
	TemplatePath string

	// InlineDefinition contains the parsed definition for inline component registrations
	// This is only populated during indexing and not persisted (used to store in definition index)
	InlineDefinition *ComponentDefinition `msgpack:"-"`

	// liveTypeFiles contains compact TypeScript contexts parsed from an open Vue
	// document. It is deliberately unexported so persisted component generations
	// remain immutable and never retain source text or syntax trees.
	liveTypeFiles []AdminTypeFile
}

func componentLiveTypeFiles(component *VueComponent) []AdminTypeFile {
	if component == nil {
		return nil
	}
	return component.liveTypeFiles
}

// VueLocalComponent is one statically declared entry in an Options API
// `components` object. Symbol and ImportPath retain enough provenance to
// navigate aliases and, when possible, reuse an imported component contract.
type VueLocalComponent struct {
	Name       string
	Symbol     string
	ImportPath string
	FilePath   string
	Line       int
	NameRange  AdminSourceRange
	Shorthand  bool
	Quoted     bool
}

// VueLocalDirective is one statically declared entry in an Options API
// `directives` object. Its source identity is the component definition file;
// the name is stored without Vue's `v-` markup prefix.
type VueLocalDirective struct {
	Name      string
	FilePath  string
	Line      int
	NameRange AdminSourceRange
	Shorthand bool
	Quoted    bool
}

func (component VueComponent) LocalDirective(
	name string,
) (VueLocalDirective, bool) {
	for _, local := range component.LocalDirectives {
		if strings.EqualFold(local.Name, name) {
			return local, true
		}
	}
	return VueLocalDirective{}, false
}

func (component VueComponent) LocalComponent(
	name string,
) (VueLocalComponent, bool) {
	for _, local := range component.LocalComponents {
		if strings.EqualFold(local.Name, name) {
			return local, true
		}
	}
	return VueLocalComponent{}, false
}

type ComponentRegistrationKind string

const (
	ComponentRegister ComponentRegistrationKind = "register"
	ComponentExtend   ComponentRegistrationKind = "extend"
	ComponentOverride ComponentRegistrationKind = "override"
)

// AdminMixin is a registered Administration mixin.
type AdminMixin struct {
	Name       string
	FilePath   string
	Line       int
	Definition ComponentDefinition
}

// AdminDirective is a custom Vue directive registered through Shopware's
// Administration directive registry. Name intentionally excludes the `v-`
// markup prefix so JavaScript registry calls and Twig attributes share one
// stable symbol identity.
type AdminDirective struct {
	Name      string
	FilePath  string
	Line      int
	NameRange AdminSourceRange
	Local     bool
}

// AdminFilter is a formatter registered through Shopware.Filter.register.
// Signature retains a statically declared callable contract where available,
// so hover and completion can explain more than the registry key alone.
type AdminFilter struct {
	Name      string
	FilePath  string
	Line      int
	NameRange AdminSourceRange
	Signature string
}

// AdminModule describes a Module.register declaration and its generated route
// names. Shopware derives route prefixes from the registration key by
// replacing dashes with dots.
type AdminModule struct {
	Name        string
	DisplayName string
	Type        string
	Title       string
	Description string
	FilePath    string
	Line        int
	Routes      []AdminModuleRoute
}

type AdminModuleRoute struct {
	Name      string
	LocalName string
	Path      string
	Component string
	Line      int
}

// VueComponentProp represents a prop definition in a Vue component
type VueComponentProp struct {
	// Name is the prop name
	Name string

	// Documentation is the source-owned public description attached to the
	// prop declaration. It intentionally excludes metadata represented by the
	// typed fields below, such as @deprecated, @type, @default and @required.
	Documentation string

	// NameRange is the exact source range of the public prop declaration when
	// the authoring form exposes one. Older cache entries and synthesized
	// runtime contracts fall back to Line.
	NameRange AdminSourceRange

	// Deprecated contains the normalized @deprecated documentation attached to
	// the prop declaration. Inherited props retain their source-owned metadata.
	Deprecated string

	// Type is the prop type (e.g., "String", "Boolean", "Object")
	Type string

	// Required indicates if the prop is required
	Required bool

	// Default is the default value (as string representation)
	Default string

	// AllowedValues contains statically declared literal values, for example
	// Shopware's Options API `validValues` arrays. Literal TypeScript unions are
	// derived from Type on demand so generated Meteor declarations use the same
	// completion and validation contract without duplicating source data.
	AllowedValues []string

	// AllowedValuesComplete is true when AllowedValues describes the complete
	// accepted static string domain. Partial TypeScript unions that contain an
	// unresolved alias still provide useful completion values, but must not
	// produce invalid-value diagnostics.
	AllowedValuesComplete bool

	// Line is the line number where the prop is defined (1-based)
	Line int

	// FilePath is the definition which owns the prop. It is required for
	// inherited prop navigation, where the effective component has members from
	// more than one JavaScript file.
	FilePath string
}

// VueComponentEvent represents an event exposed by a Vue component.
type VueComponentEvent struct {
	Name          string
	Type          string
	Documentation string
	FilePath      string
	Line          int
	NameRange     AdminSourceRange
}

// VueComponentModelBinding is the two-sided contract behind one v-model
// directive: a readable prop and the event which writes the new value back.
type VueComponentModelBinding struct {
	AttributeName string
	PropName      string
	EventName     string
	Prop          VueComponentProp
	Event         VueComponentEvent
}

// ComponentModel resolves a concrete v-model or v-model:argument attribute.
// A binding is returned only when both sides of the public component contract
// are indexed; partial/runtime-only contracts remain conservative.
func (component VueComponent) ComponentModel(
	attributeName string,
) (VueComponentModelBinding, bool) {
	argument, model := NormalizeModelArgument(attributeName)
	if !model {
		return VueComponentModelBinding{}, false
	}
	propName := argument
	eventName := ""
	if propName == "" {
		switch {
		case component.ModelProp != "" || component.ModelEvent != "":
			propName = component.ModelProp
			if propName == "" {
				propName = "value"
			}
			eventName = component.ModelEvent
			if eventName == "" {
				eventName = "input"
			}
		case componentHasModelPair(component, "modelValue", "update:modelValue"):
			propName, eventName = "modelValue", "update:modelValue"
		case componentHasModelPair(component, "value", "input"):
			propName, eventName = "value", "input"
		default:
			return VueComponentModelBinding{}, false
		}
	} else {
		eventName = "update:" + CamelToKebab(propName)
	}
	prop, propFound := component.ComponentProp(propName)
	event, eventFound := component.ComponentEvent(eventName)
	if !propFound || !eventFound {
		return VueComponentModelBinding{}, false
	}
	return VueComponentModelBinding{
		AttributeName: attributeName,
		PropName:      prop.Name,
		EventName:     CanonicalEventName(event.Name),
		Prop:          prop,
		Event:         event,
	}, true
}

// ComponentModels returns every statically complete model contract exposed by
// the component. The default model is followed by named update:* pairs.
func (component VueComponent) ComponentModels() []VueComponentModelBinding {
	var result []VueComponentModelBinding
	seen := make(map[string]bool)
	add := func(attributeName string) {
		binding, found := component.ComponentModel(attributeName)
		if !found || seen[attributeName] {
			return
		}
		seen[attributeName] = true
		result = append(result, binding)
	}
	add("v-model")
	for _, event := range component.ComponentEvents() {
		name := CanonicalEventName(event.Name)
		if !strings.HasPrefix(name, "update:") {
			continue
		}
		argument := strings.TrimPrefix(name, "update:")
		if argument == "" {
			continue
		}
		if defaultModel, found := component.ComponentModel("v-model"); found &&
			defaultModel.EventName == name {
			continue
		}
		add("v-model:" + argument)
	}
	return result
}

func componentHasModelPair(
	component VueComponent,
	propName,
	eventName string,
) bool {
	_, propFound := component.ComponentProp(propName)
	_, eventFound := component.ComponentEvent(eventName)
	return propFound && eventFound
}

// ComponentEvents returns the source-aware event API and synthesizes entries
// for components written before Events was introduced.
func (component VueComponent) ComponentEvents() []VueComponentEvent {
	result := append([]VueComponentEvent(nil), component.Events...)
	seen := make(map[string]bool, len(result))
	for _, event := range result {
		seen[CanonicalEventName(event.Name)] = true
	}
	for _, name := range component.Emits {
		canonical := CanonicalEventName(name)
		if canonical == "" || seen[canonical] {
			continue
		}
		seen[canonical] = true
		result = append(result, VueComponentEvent{
			Name: name, FilePath: component.DefinitionPath,
		})
	}
	return result
}

func (component VueComponent) ComponentEvent(
	name string,
) (VueComponentEvent, bool) {
	canonical := CanonicalEventName(name)
	for _, event := range component.ComponentEvents() {
		if CanonicalEventName(event.Name) == canonical {
			return event, true
		}
	}
	return VueComponentEvent{}, false
}

func (component VueComponent) ComponentProp(
	name string,
) (VueComponentProp, bool) {
	for _, prop := range component.Props {
		if prop.Name == name {
			return prop, true
		}
	}
	return VueComponentProp{}, false
}

// ComponentSlot resolves an exact slot first and then the most-specific
// statically known dynamic family. A declaration such as
// :name="`column-${column.property}`" therefore owns #column-name while an
// exact #column-name declaration still takes precedence.
func (component VueComponent) ComponentSlot(
	name string,
) (VueComponentSlot, bool) {
	best := -1
	var result VueComponentSlot
	for _, slot := range component.Slots {
		if slot.Name == name && name != "" {
			return slot, true
		}
		if !slot.MatchesName(name) {
			continue
		}
		specificity := len(slot.NamePrefix) + len(slot.NameSuffix)
		if specificity > best {
			best = specificity
			result = slot
		}
	}
	return result, best >= 0
}

// SymbolSource returns the declaration source that owns an effective
// component event or slot. Inherited symbols therefore keep the same identity
// when consumed through a child component.
func (component VueComponent) SymbolSource(
	kind AdminSymbolKind,
	name string,
) (string, bool) {
	switch kind {
	case AdminSymbolComponentProp:
		prop, found := component.ComponentProp(name)
		if !found {
			return "", false
		}
		owner := prop.FilePath
		if owner == "" {
			owner = component.DefinitionPath
		}
		if owner == "" {
			owner = component.FilePath
		}
		return owner, owner != ""
	case AdminSymbolComponentEvent:
		event, found := component.ComponentEvent(name)
		if !found {
			return "", false
		}
		owner := event.FilePath
		if owner == "" {
			owner = component.DefinitionPath
		}
		if owner == "" {
			owner = component.FilePath
		}
		return owner, owner != ""
	case AdminSymbolComponentSlot:
		slot, found := component.ComponentSlot(name)
		if !found {
			return "", false
		}
		owner := slot.FilePath
		if owner == "" {
			owner = component.TemplatePath
		}
		return owner, owner != ""
	}
	return "", false
}

type VueComponentMemberKind string

const (
	ComponentMemberProp     VueComponentMemberKind = "prop"
	ComponentMemberData     VueComponentMemberKind = "data"
	ComponentMemberComputed VueComponentMemberKind = "computed"
	ComponentMemberMethod   VueComponentMemberKind = "method"
	ComponentMemberInject   VueComponentMemberKind = "inject"
)

// VueComponentMember is a source-aware value available to an Administration
// component template.
type VueComponentMember struct {
	Name     string
	Kind     VueComponentMemberKind
	Type     string
	FilePath string
	Line     int

	// Deprecated contains normalized @deprecated documentation owned by this
	// public member declaration. Effective overrides retain inherited metadata.
	Deprecated string

	// NameRange is the exact public-name declaration range. BindingName and
	// Shorthand preserve enough syntax to turn `{ value }` into
	// `{ renamed: value }` without renaming the private JavaScript binding.
	NameRange   AdminSourceRange
	BindingName string
	Shorthand   bool

	// SourceExpression retains a safely reusable initial-value or return
	// expression from legacy JavaScript. It is resolved only after the
	// effective component has been assembled, when inherited members, mixins,
	// stores, and generated Shopware entity types are available.
	SourceExpression string

	// ReturnExpressions retains every return value owned by a computed value or
	// method. SourceExpression deliberately stays empty for branch-dependent
	// functions, while this lossless alternative set lets consumers such as
	// dynamic-component resolution recover finite literal unions. ReturnsComplete
	// is true only when control-flow inspection proves that the function cannot
	// fall through without returning.
	ReturnExpressions []string
	ReturnsComplete   bool

	// OpenRuntimeShape records that the member's structural type was inferred
	// from a JavaScript object literal rather than declared as a closed
	// TypeScript contract. Options API data and computed objects are routinely
	// extended after initialization, so their known fields remain useful for
	// navigation and completion but cannot safely drive typo diagnostics.
	OpenRuntimeShape bool

	// CMSRegistryKind records that the member is derived from one of
	// Shopware's CMS registries. It lets markup consumers resolve dynamic
	// selectors such as `cmsElements[element.type].configComponent` without
	// pretending that arbitrary runtime objects have a closed component set.
	CMSRegistryKind AdminCMSRegistrationKind

	// ElementMembers are properties assigned to values while iterating this
	// member (for example `this.rows.forEach(row => row.selected = true)`).
	// They augment the collection element contract without making every entity
	// globally open or weakening typo diagnostics for unrelated components.
	ElementMembers []VueComponentElementMember

	// TypeContextPath is the declaration context in which Type must be
	// resolved. It can differ from FilePath when a computed value forwards an
	// inherited member whose type is imported by the parent definition.
	TypeContextPath string
}

func (member VueComponentMember) Renameable() bool {
	switch member.Kind {
	case ComponentMemberData, ComponentMemberComputed, ComponentMemberMethod:
		return member.Name != "" && member.FilePath != "" &&
			(member.NameRange.EndLine != 0 ||
				member.NameRange.EndCharacter != 0 ||
				member.NameRange.StartLine != 0 ||
				member.NameRange.StartCharacter != 0)
	default:
		return false
	}
}

// SourceIdentity distinguishes same-named members declared in one file and is
// stable across component inheritance, overrides, and cache restoration.
func (member VueComponentMember) SourceIdentity() string {
	if member.FilePath == "" || member.Name == "" {
		return ""
	}
	return filepath.Clean(member.FilePath) + "\x00" + string(member.Kind) +
		"\x00" + strconv.Itoa(member.NameRange.StartLine) + ":" +
		strconv.Itoa(member.NameRange.StartCharacter) + "\x00" + member.Name
}

// VueComponentElementMember is one component-local extension of a collection
// element observed in executable component code.
type VueComponentElementMember struct {
	Name     string
	Type     string
	FilePath string
	Line     int
}

// VueComponentAssignment is one statically direct `this.member = value`
// write. Dynamic member writes and compound assignments are intentionally not
// represented because they do not provide safe type evidence for markup.
type VueComponentAssignment struct {
	Target     string
	Expression string
	FilePath   string
	Line       int
}

// TemplateMembers returns the effective Vue instance scope in a stable order.
// It also synthesizes source-aware entries for caches written before Members
// was introduced.
func (component VueComponent) TemplateMembers() []VueComponentMember {
	result := append([]VueComponentMember(nil), component.Members...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[member.Name] = index
	}
	add := func(member VueComponentMember) {
		if member.Name == "" {
			return
		}
		if index, exists := positions[member.Name]; exists {
			// Component-local declarations are appended after inherited members
			// by the effective resolver and therefore own the public scope name.
			result[index] = member
			return
		}
		positions[member.Name] = len(result)
		result = append(result, member)
	}
	for _, prop := range component.Props {
		filePath := prop.FilePath
		if filePath == "" {
			filePath = component.DefinitionPath
		}
		if index, exists := positions[prop.Name]; exists {
			// Script-setup bindings carry their local declaration range and a
			// separate type context. Preserve that richer symbol while applying
			// public prop metadata from the resolved contract.
			member := result[index]
			member.Kind = ComponentMemberProp
			if member.Type == "" {
				member.Type = prop.Type
			}
			if member.TypeContextPath == "" {
				member.TypeContextPath = filePath
			}
			if member.Deprecated == "" {
				member.Deprecated = prop.Deprecated
			}
			result[index] = member
			continue
		}
		add(VueComponentMember{
			Name: prop.Name, Kind: ComponentMemberProp, Type: prop.Type,
			FilePath: filePath, Line: prop.Line, Deprecated: prop.Deprecated,
			NameRange: prop.NameRange, TypeContextPath: filePath,
		})
	}
	for _, entry := range []struct {
		kind  VueComponentMemberKind
		names []string
	}{
		{ComponentMemberData, component.Data},
		{ComponentMemberComputed, component.Computed},
		{ComponentMemberMethod, component.Methods},
		{ComponentMemberInject, component.Injected},
	} {
		for _, name := range entry.names {
			if _, exists := positions[name]; exists {
				continue
			}
			add(VueComponentMember{
				Name: name, Kind: entry.kind,
				FilePath: component.DefinitionPath,
			})
		}
	}
	return result
}

func (component VueComponent) TemplateMember(name string) (VueComponentMember, bool) {
	for _, member := range component.TemplateMembers() {
		if member.Name == name {
			return member, true
		}
	}
	return VueComponentMember{}, false
}

func VueBuiltinMembers() []VueComponentMember {
	method := ComponentMemberMethod
	data := ComponentMemberData
	return []VueComponentMember{
		{
			Name: "$emit", Kind: method,
			Type: "(event: string, ...args: unknown[]) => void",
		},
		{Name: "$forceUpdate", Kind: method, Type: "() => void"},
		{
			Name: "$nextTick", Kind: method,
			Type: "(callback?: Function) => Promise<void>",
		},
		{
			Name: "$sanitize", Kind: method,
			Type: "(html: string, config?: Record<string, unknown>) => string",
		},
		{Name: "$super", Kind: method},
		{
			Name: "$t", Kind: method,
			Type: "(key: string, values?: Record<string, unknown>, plural?: number) => string",
		},
		{
			Name: "$tc", Kind: method,
			Type: "(key: string, plural?: number, values?: Record<string, unknown>) => string",
		},
		{
			Name: "$te", Kind: method,
			Type: "(key: string, locale?: string) => boolean",
		},
		{
			Name: "$watch", Kind: method,
			Type: "(source: string | Function, callback: Function) => Function",
		},
		{Name: "$attrs", Kind: data, Type: "Record<string, unknown>"},
		{Name: "$createTitle", Kind: method},
		{Name: "$device", Kind: data},
		{Name: "$el", Kind: data},
		{Name: "$i18n", Kind: data},
		{Name: "$listeners", Kind: data, Type: "Record<string, Function>"},
		{Name: "$options", Kind: data},
		{Name: "$parent", Kind: data},
		{Name: "$props", Kind: data, Type: "Record<string, unknown>"},
		{Name: "$refs", Kind: data, Type: "Record<string, unknown>"},
		{Name: "$root", Kind: data},
		{Name: "$route", Kind: data},
		{Name: "$router", Kind: data},
		{
			Name: "$scopedSlots", Kind: data,
			Type: "Record<string, Function | undefined>",
		},
		{
			Name: "$slots", Kind: data,
			Type: "Record<string, Function | undefined>",
		},
		{Name: "$store", Kind: data},
		{Name: "$swLegacyBlockElse", Kind: data},
		{Name: "$swLegacyBlockElseIf", Kind: data},
		{Name: "$swLegacyBlockIf", Kind: data},
	}
}

func IsVueBuiltinMember(name string) bool {
	_, found := VueBuiltinMember(name)
	return found
}

func VueBuiltinMember(name string) (VueComponentMember, bool) {
	for _, member := range VueBuiltinMembers() {
		if member.Name == name {
			return member, true
		}
	}
	return VueComponentMember{}, false
}

// VueTemplateGlobals returns JavaScript values that Vue deliberately exposes
// to template expressions without resolving them through the component
// instance. Keeping this finite catalog shared lets completion, hover, and
// diagnostics agree that Object.keys(), Math.round(), and similar expressions
// are valid while still detecting close misspellings.
func VueTemplateGlobals() []VueComponentMember {
	method := ComponentMemberMethod
	data := ComponentMemberData
	return []VueComponentMember{
		{Name: "Infinity", Kind: data, Type: "number"},
		{Name: "undefined", Kind: data, Type: "undefined"},
		{Name: "NaN", Kind: data, Type: "number"},
		{Name: "isFinite", Kind: method, Type: "(value: unknown) => boolean"},
		{Name: "isNaN", Kind: method, Type: "(value: unknown) => boolean"},
		{Name: "parseFloat", Kind: method, Type: "(value: string) => number"},
		{Name: "parseInt", Kind: method, Type: "(value: string, radix?: number) => number"},
		{Name: "decodeURI", Kind: method, Type: "(value: string) => string"},
		{Name: "decodeURIComponent", Kind: method, Type: "(value: string) => string"},
		{Name: "encodeURI", Kind: method, Type: "(value: string) => string"},
		{Name: "encodeURIComponent", Kind: method, Type: "(value: string) => string"},
		{Name: "Array", Kind: data, Type: "ArrayConstructor"},
		{Name: "BigInt", Kind: data, Type: "BigIntConstructor"},
		{Name: "Boolean", Kind: data, Type: "BooleanConstructor"},
		{Name: "Date", Kind: data, Type: "DateConstructor"},
		{Name: "Error", Kind: data, Type: "ErrorConstructor"},
		{Name: "Intl", Kind: data, Type: "typeof Intl"},
		{Name: "JSON", Kind: data, Type: "JSON"},
		{Name: "Map", Kind: data, Type: "MapConstructor"},
		{Name: "Math", Kind: data, Type: "Math"},
		{Name: "Number", Kind: data, Type: "NumberConstructor"},
		{Name: "Object", Kind: data, Type: "ObjectConstructor"},
		{Name: "RegExp", Kind: data, Type: "RegExpConstructor"},
		{Name: "Set", Kind: data, Type: "SetConstructor"},
		{Name: "String", Kind: data, Type: "StringConstructor"},
		{Name: "Symbol", Kind: data, Type: "SymbolConstructor"},
		{Name: "console", Kind: data, Type: "Console"},
		{Name: "Shopware", Kind: data, Type: "ShopwareGlobal"},
		{Name: "document", Kind: data, Type: "Document"},
		{Name: "window", Kind: data, Type: "Window"},
	}
}

func VueTemplateGlobal(name string) (VueComponentMember, bool) {
	for _, global := range VueTemplateGlobals() {
		if global.Name == name {
			return global, true
		}
	}
	return VueComponentMember{}, false
}

// VueComponentSlot represents a slot definition in a Vue component template
type VueComponentSlot struct {
	// Name is the slot name (e.g., "default", "actions", "header")
	Name string

	// NamePrefix and NameSuffix describe one safely resolvable dynamic slot
	// family. Name remains empty for these declarations. For example,
	// `column-${column.property}` is represented as prefix "column-" and an
	// empty suffix, and matches #column-name without inventing a concrete name.
	NamePrefix string
	NameSuffix string

	// Line is the line number where the slot is defined in the template (1-based)
	Line int

	// NameRange is the exact UTF-16 declaration range in FilePath. Named slots
	// point at the literal or type member; the implicit default slot points at
	// the <slot> tag name, and dynamic families point at their bound expression.
	NameRange AdminSourceRange

	// FilePath is the template or declaration that owns the slot.
	FilePath string

	// PayloadType is the complete scoped-slot payload type when a declaration
	// exposes it (notably Meteor's vue-tsc declarations).
	PayloadType string

	// Members are the values a consumer may destructure from the scoped slot.
	Members []VueComponentSlotMember

	// MembersComplete is true when the declaration proves the entire payload
	// shape. Runtime v-bind forwarding keeps this false so diagnostics remain
	// conservative while completion can still expose statically known members.
	MembersComplete bool
}

func (slot VueComponentSlot) IsDynamicName() bool {
	return slot.Name == "" && (slot.NamePrefix != "" || slot.NameSuffix != "")
}

func (slot VueComponentSlot) MatchesName(name string) bool {
	if slot.Name != "" {
		return slot.Name == name
	}
	if !slot.IsDynamicName() ||
		len(name) < len(slot.NamePrefix)+len(slot.NameSuffix) {
		return false
	}
	return strings.HasPrefix(name, slot.NamePrefix) &&
		strings.HasSuffix(name, slot.NameSuffix)
}

func (slot VueComponentSlot) DisplayName() string {
	if slot.Name != "" {
		return slot.Name
	}
	if slot.IsDynamicName() {
		return slot.NamePrefix + "*" + slot.NameSuffix
	}
	return ""
}

func (slot VueComponentSlot) identityKey() string {
	if slot.Name != "" {
		return "exact\x00" + slot.Name
	}
	return "dynamic\x00" + slot.NamePrefix + "\x00" + slot.NameSuffix
}

type VueComponentSlotMember struct {
	Name      string
	Type      string
	FilePath  string
	Line      int
	NameRange AdminSourceRange
}

func (slot VueComponentSlot) Member(name string) (VueComponentSlotMember, bool) {
	for _, member := range slot.Members {
		if member.Name == name {
			return member, true
		}
	}
	return VueComponentSlotMember{}, false
}

// TwigBlock represents a Twig block definition in a component template
type TwigBlock struct {
	// Name is the block name (e.g., "sw_page_content", "sw_page_smart_bar")
	Name string

	// Deprecated contains normalized @deprecated documentation attached to the
	// block declaration. Overrides inherit this public contract metadata.
	Deprecated string

	// Line is the line number where the block is defined in the template (1-based)
	Line int

	// NameRange is the exact UTF-16 declaration range in FilePath.
	NameRange AdminSourceRange

	// FilePath is the template that owns the block.
	FilePath string

	// ScopeMembers are lexical values supplied by the parent block context,
	// such as the item and column variables inside sw-data-grid extension
	// blocks. Overrides inherit this public block contract.
	ScopeMembers []TwigBlockScopeMember
}

// TwigBlockScopeMember is one lexical input available while rendering a Twig
// extensibility block. Its declaration normally belongs to the parent
// template's v-for or scoped-slot binding.
type TwigBlockScopeMember struct {
	Name      string
	Type      string
	FilePath  string
	Line      int
	NameRange AdminSourceRange
}

func (block TwigBlock) ScopeMember(name string) (TwigBlockScopeMember, bool) {
	for _, member := range block.ScopeMembers {
		if member.Name == name {
			return member, true
		}
	}
	return TwigBlockScopeMember{}, false
}

// ComponentBlock resolves one public Twig extensibility block from the
// effective component contract.
func (component VueComponent) ComponentBlock(name string) (TwigBlock, bool) {
	for _, block := range component.Blocks {
		if block.Name == name {
			return block, true
		}
	}
	return TwigBlock{}, false
}
