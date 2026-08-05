package semantic

import "strings"

// AttributeNamed returns the first attribute matching either a short or fully
// qualified PHP class name. Attribute names are case-insensitive in PHP.
func AttributeNamed(attributes []Attribute, expected string) (*Attribute, bool) {
	expected = normalizedAttributeName(expected)
	for index := range attributes {
		candidate := normalizedAttributeName(attributes[index].Name)
		if candidate == expected ||
			(!strings.Contains(expected, "\\") &&
				strings.HasSuffix(candidate, "\\"+expected)) {
			return &attributes[index], true
		}
	}
	return nil, false
}

// Argument returns a named argument when present, otherwise the positional
// argument at index. This mirrors how JetBrains attribute constructors expose
// stable parameter names while remaining compatible with positional usage.
func (attribute *Attribute) Argument(
	name string,
	index int,
) (AttributeValue, bool) {
	if attribute == nil {
		return AttributeValue{}, false
	}
	for argumentIndex := range attribute.Arguments {
		argument := &attribute.Arguments[argumentIndex]
		if argument.Name != "" && strings.EqualFold(argument.Name, name) {
			return argument.Value, true
		}
	}
	if index >= 0 && index < len(attribute.Arguments) &&
		attribute.Arguments[index].Name == "" {
		return attribute.Arguments[index].Value, true
	}
	return AttributeValue{}, false
}

func normalizedAttributeName(name string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(name), "\\"))
}

// Deprecation describes the structured JetBrains/PHP attribute payload used
// by hover text, diagnostics, and future replacement quick-fixes.
type Deprecation struct {
	Reason      string
	Replacement string
	Since       string
}

// DeprecationOf extracts both JetBrains\PhpStorm\Deprecated and the native
// Deprecated attribute conventions. The boolean reports attribute presence,
// even when no structured details were supplied.
func DeprecationOf(attributes []Attribute) (Deprecation, bool) {
	attribute, found := AttributeNamed(attributes, "Deprecated")
	if !found {
		return Deprecation{}, false
	}
	result := Deprecation{}
	if value, ok := attribute.Argument("reason", 0); ok {
		result.Reason = attributeString(value)
	}
	if result.Reason == "" {
		if value, ok := attribute.Argument("message", 0); ok {
			result.Reason = attributeString(value)
		}
	}
	if value, ok := attribute.Argument("replacement", 1); ok {
		result.Replacement = attributeString(value)
	}
	if value, ok := attribute.Argument("since", 2); ok {
		result.Since = attributeString(value)
	}
	return result, true
}

func attributeString(value AttributeValue) string {
	if value.Kind != AttributeValueString {
		return ""
	}
	return value.Value
}
