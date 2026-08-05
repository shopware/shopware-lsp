// Package languagelevel centralizes the PHP version required by language
// syntax understood by the permissive PHP parser.
package languagelevel

import (
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/php/project"
)

// Feature is a stable identifier for a version-gated PHP language feature.
type Feature string

const (
	Attributes           Feature = "attributes"
	NamedArguments       Feature = "named-arguments"
	MatchExpressions     Feature = "match-expressions"
	NullsafeOperator     Feature = "nullsafe-operator"
	PropertyPromotion    Feature = "property-promotion"
	ThrowExpressions     Feature = "throw-expressions"
	UnionTypes           Feature = "union-types"
	Enums                Feature = "enums"
	IntersectionTypes    Feature = "intersection-types"
	ReadonlyProperties   Feature = "readonly-properties"
	DNFTypes             Feature = "dnf-types"
	ReadonlyClasses      Feature = "readonly-classes"
	TypedClassConstants  Feature = "typed-class-constants"
	AsymmetricVisibility Feature = "asymmetric-visibility"
	PropertyHooks        Feature = "property-hooks"
)

// Definition describes the first PHP release supporting a feature.
type Definition struct {
	Feature Feature
	Name    string
	Major   int
	Minor   int
}

var definitions = map[Feature]Definition{
	Attributes:           {Attributes, "Attributes", 8, 0},
	NamedArguments:       {NamedArguments, "Named arguments", 8, 0},
	MatchExpressions:     {MatchExpressions, "Match expressions", 8, 0},
	NullsafeOperator:     {NullsafeOperator, "The nullsafe operator", 8, 0},
	PropertyPromotion:    {PropertyPromotion, "Constructor property promotion", 8, 0},
	ThrowExpressions:     {ThrowExpressions, "Throw expressions", 8, 0},
	UnionTypes:           {UnionTypes, "Union types", 8, 0},
	Enums:                {Enums, "Enums", 8, 1},
	IntersectionTypes:    {IntersectionTypes, "Intersection types", 8, 1},
	ReadonlyProperties:   {ReadonlyProperties, "Readonly properties", 8, 1},
	DNFTypes:             {DNFTypes, "Disjunctive normal form types", 8, 2},
	ReadonlyClasses:      {ReadonlyClasses, "Readonly classes", 8, 2},
	TypedClassConstants:  {TypedClassConstants, "Typed class constants", 8, 3},
	AsymmetricVisibility: {AsymmetricVisibility, "Asymmetric property visibility", 8, 4},
	PropertyHooks:        {PropertyHooks, "Property hooks", 8, 4},
}

// Lookup returns a feature definition.
func Lookup(feature Feature) (Definition, bool) {
	definition, found := definitions[feature]
	return definition, found
}

// Supports reports whether version supports feature. Unknown feature IDs are
// deliberately unsupported so a newly added feature cannot silently leak into
// older projects without a registry entry.
func Supports(version project.Version, feature Feature) bool {
	definition, found := Lookup(feature)
	return found && version.AtLeast(definition.Major, definition.Minor)
}

// All returns registry definitions in deterministic version/name order.
func All() []Definition {
	result := make([]Definition, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, definition)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Major != result[right].Major {
			return result[left].Major < result[right].Major
		}
		if result[left].Minor != result[right].Minor {
			return result[left].Minor < result[right].Minor
		}
		return result[left].Feature < result[right].Feature
	})
	return result
}

// UnsupportedMessage formats the user-facing version diagnostic.
func UnsupportedMessage(definition Definition, configured project.Version) string {
	return fmt.Sprintf(
		"%s require PHP %d.%d; the project is configured for PHP %d.%d",
		definition.Name,
		definition.Major,
		definition.Minor,
		configured.Major,
		configured.Minor,
	)
}
