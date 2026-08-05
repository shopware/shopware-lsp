package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// =============================================================================
// Helper Functions
// =============================================================================

// IsComponentTag recognizes both DOM custom-element spelling and Vue's
// PascalCase local-component spelling. Native HTML elements are lowercase and
// contain no hyphen, so they remain outside the component resolver.
func IsComponentTag(tagName string) bool {
	tagName = strings.TrimSpace(tagName)
	return tagName != "" && tagName != "template" &&
		(strings.Contains(tagName, "-") ||
			tagName[0] >= 'A' && tagName[0] <= 'Z')
}

// IsShopwareComponentTag reports component names owned by Shopware's public
// Administration registries. Arbitrary custom elements may also contain a
// dash, so missing-component diagnostics deliberately use the narrower sw-/mt-
// convention while completion and navigation continue to accept every
// registered component name.
func IsShopwareComponentTag(tagName string) bool {
	name := strings.ToLower(strings.TrimSpace(tagName))
	return strings.HasPrefix(name, "sw-") || strings.HasPrefix(name, "mt-")
}

// VueDirectiveReference is the registry-owned part of one Vue directive
// attribute. Range covers only the custom name, excluding `v-`, an argument,
// and modifiers, so navigation, references, rename, and typo fixes preserve
// the surrounding directive syntax.
type VueDirectiveReference struct {
	Name  string
	Range cst.TextRange
}

// VueAttributeReference is the contract-owned portion of one component
// attribute. Range excludes Vue shorthand/long-form prefixes and modifiers so
// navigation, rename, diagnostics, and quick fixes can edit only the public
// prop or event name.
type VueAttributeReference struct {
	Name  string
	Range cst.TextRange
}

// VueDirectiveName extracts a directive identity from an attribute such as
// `v-tooltip.bottom` or `v-custom:argument.modifier`.
func VueDirectiveName(attributeName string) string {
	name := strings.TrimSpace(attributeName)
	if !strings.HasPrefix(name, "v-") {
		return ""
	}
	name = strings.TrimPrefix(name, "v-")
	if end := strings.IndexAny(name, ":."); end >= 0 {
		name = name[:end]
	}
	return strings.TrimSpace(name)
}

// VueDirectiveReferenceForAttribute returns the custom directive identity and
// its source range within an HTML attribute name token.
func VueDirectiveReferenceForAttribute(
	attributeName string,
	nameRange cst.TextRange,
) (VueDirectiveReference, bool) {
	name := VueDirectiveName(attributeName)
	if name == "" || IsVueBuiltinDirective(name) {
		return VueDirectiveReference{}, false
	}
	start := nameRange.Start + uint32(len("v-"))
	return VueDirectiveReference{
		Name:  name,
		Range: cst.TextRange{Start: start, End: start + uint32(len(name))},
	}, true
}

// IsVueBuiltinDirective separates Vue's language-level directives from
// Shopware registry declarations. Built-ins remain handled by Vue markup
// semantics and must never produce missing-registry diagnostics.
func IsVueBuiltinDirective(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bind", "cloak", "else", "else-if", "for", "html", "if", "memo",
		"model", "on", "once", "pre", "show", "slot", "text":
		return true
	default:
		return false
	}
}

// NormalizePropName normalizes an attribute name to a prop name
// Removes Vue binding prefixes (:, v-bind:) and converts kebab-case to camelCase
// Returns empty string for event handlers (@, v-on:) and directives (v-if, v-for, etc.)
//
// Examples:
//
//	"label"                -> "label"
//	":disabled"            -> "disabled"
//	"v-bind:position-id"   -> "positionId"
//	"position-identifier"  -> "positionIdentifier"
//	"@click"               -> "" (event handler)
//	"v-if"                 -> "" (directive)
func NormalizePropName(attrName string) string {
	name := attrName

	// Remove Vue binding prefixes
	if strings.HasPrefix(name, "v-bind:") {
		name = strings.TrimPrefix(name, "v-bind:")
	} else if strings.HasPrefix(name, "v-on:") {
		return "" // Event handler
	} else if strings.HasPrefix(name, ":") {
		name = strings.TrimPrefix(name, ":")
	} else if strings.HasPrefix(name, "@") {
		return "" // Event handler shorthand
	} else if strings.HasPrefix(name, "#") {
		return "" // Slot shorthand
	} else if strings.HasPrefix(name, "v-") {
		return "" // Vue directive
	}
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}

	// Convert kebab-case to camelCase
	return KebabToCamel(name)
}

// VuePropReferenceForAttribute resolves a static component prop attribute and
// its source range. Directives, listeners, and slot shorthands are excluded.
func VuePropReferenceForAttribute(
	attributeName string,
	nameRange cst.TextRange,
) (VueAttributeReference, bool) {
	name := NormalizePropName(attributeName)
	if name == "" {
		return VueAttributeReference{}, false
	}
	rangeValue, found := vueAttributeArgumentRange(attributeName, nameRange)
	if !found {
		return VueAttributeReference{}, false
	}
	return VueAttributeReference{Name: name, Range: rangeValue}, true
}

// NormalizeEventName returns the canonical event identity for a Vue listener
// attribute. Listener modifiers are not part of the event name.
//
// Examples:
//
//	"@update:model-value"       -> "update:model-value"
//	"v-on:itemClick.stop"       -> "item-click"
//	":label"                    -> ""
func NormalizeEventName(attributeName string) string {
	name := strings.TrimSpace(attributeName)
	switch {
	case strings.HasPrefix(name, "@"):
		name = strings.TrimPrefix(name, "@")
	case strings.HasPrefix(name, "v-on:"):
		name = strings.TrimPrefix(name, "v-on:")
	default:
		return ""
	}
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}
	return CanonicalEventName(name)
}

// VueEventReferenceForAttribute resolves a static Vue listener and its public
// event-name range while preserving @/v-on prefixes and listener modifiers.
func VueEventReferenceForAttribute(
	attributeName string,
	nameRange cst.TextRange,
) (VueAttributeReference, bool) {
	name := NormalizeEventName(attributeName)
	if name == "" {
		return VueAttributeReference{}, false
	}
	rangeValue, found := vueAttributeArgumentRange(attributeName, nameRange)
	if !found {
		return VueAttributeReference{}, false
	}
	return VueAttributeReference{Name: name, Range: rangeValue}, true
}

func vueAttributeArgumentRange(
	attributeName string,
	nameRange cst.TextRange,
) (cst.TextRange, bool) {
	prefixLength := 0
	switch {
	case strings.HasPrefix(attributeName, "@"),
		strings.HasPrefix(attributeName, "#"):
		prefixLength = 1
	case strings.HasPrefix(attributeName, "v-on:"):
		prefixLength = len("v-on:")
	case strings.HasPrefix(attributeName, "v-slot:"):
		prefixLength = len("v-slot:")
	case strings.HasPrefix(attributeName, "v-model:"):
		prefixLength = len("v-model:")
	case strings.HasPrefix(attributeName, "v-bind:"):
		prefixLength = len("v-bind:")
	case strings.HasPrefix(attributeName, ":"):
		prefixLength = 1
	}
	name := attributeName[prefixLength:]
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}
	if name == "" {
		return cst.TextRange{}, false
	}
	start := nameRange.Start + uint32(prefixLength)
	end := start + uint32(len(name))
	if end > nameRange.End {
		return cst.TextRange{}, false
	}
	return cst.TextRange{Start: start, End: end}, true
}

// NormalizeModelArgument recognizes static Vue model directives. The empty
// argument represents default `v-model`; named arguments are returned in the
// camelCase spelling used by component prop declarations. Dynamic arguments
// remain unresolved.
func NormalizeModelArgument(attributeName string) (string, bool) {
	name := strings.TrimSpace(attributeName)
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}
	if name == "v-model" {
		return "", true
	}
	if !strings.HasPrefix(name, "v-model:") {
		return "", false
	}
	argument := strings.TrimSpace(strings.TrimPrefix(name, "v-model:"))
	if argument == "" || strings.HasPrefix(argument, "[") {
		return "", false
	}
	return KebabToCamel(argument), true
}

// VueModelReferenceForAttribute resolves the argument of a named v-model
// directive. The default v-model has no independently replaceable argument and
// is therefore intentionally excluded.
func VueModelReferenceForAttribute(
	attributeName string,
	nameRange cst.TextRange,
) (VueAttributeReference, bool) {
	argument, found := NormalizeModelArgument(attributeName)
	if !found || argument == "" {
		return VueAttributeReference{}, false
	}
	rangeValue, found := vueAttributeArgumentRange(attributeName, nameRange)
	if !found {
		return VueAttributeReference{}, false
	}
	return VueAttributeReference{Name: argument, Range: rangeValue}, true
}

// CanonicalEventName normalizes JavaScript camelCase event declarations to
// the kebab-case spelling used by Administration Twig markup.
func CanonicalEventName(name string) string {
	return CamelToKebab(strings.TrimSpace(name))
}

// NormalizeSlotName extracts a static Vue slot name from its shorthand or
// long-form attribute spelling.
func NormalizeSlotName(attributeName string) string {
	name := strings.TrimSpace(attributeName)
	switch {
	case name == "v-slot":
		return "default"
	case strings.HasPrefix(name, "#"):
		name = strings.TrimPrefix(name, "#")
	case strings.HasPrefix(name, "v-slot:"):
		name = strings.TrimPrefix(name, "v-slot:")
	default:
		return ""
	}
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}
	if strings.HasPrefix(name, "[") {
		return ""
	}
	return strings.TrimSpace(name)
}

// VueSlotReferenceForAttribute resolves an explicitly named slot consumer.
// Bare v-slot denotes the default slot but has no argument range to rewrite.
func VueSlotReferenceForAttribute(
	attributeName string,
	nameRange cst.TextRange,
) (VueAttributeReference, bool) {
	name := NormalizeSlotName(attributeName)
	if name == "" || strings.TrimSpace(attributeName) == "v-slot" {
		return VueAttributeReference{}, false
	}
	rangeValue, found := vueAttributeArgumentRange(attributeName, nameRange)
	if !found {
		return VueAttributeReference{}, false
	}
	return VueAttributeReference{Name: name, Range: rangeValue}, true
}

// KebabToCamel converts kebab-case to camelCase
//
// Examples:
//
//	"position-identifier" -> "positionIdentifier"
//	"my-prop-name"        -> "myPropName"
//	"label"               -> "label"
func KebabToCamel(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// CamelToKebab converts camelCase to kebab-case
//
// Examples:
//
//	"positionIdentifier" -> "position-identifier"
//	"myPropName"         -> "my-prop-name"
//	"label"              -> "label"
func CamelToKebab(s string) string {
	var result []byte
	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				result = append(result, '-')
			}
			result = append(result, byte(c+'a'-'A'))
		} else {
			result = append(result, byte(c))
		}
	}
	return string(result)
}
