package doctrine

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php"
)

// DiscriminatorClasses returns the mapped base class and every source subtype
// eligible for a Doctrine discriminator-map entry.
func DiscriminatorClasses(
	index *php.PHPIndex,
	owner string,
) []string {
	if index == nil || normalizeClass(owner) == "" {
		return nil
	}
	owner = normalizeClass(owner)
	snapshot := index.SemanticSnapshot()
	seen := make(map[string]struct{})
	var result []string
	for _, symbol := range index.ClassSymbols() {
		name := normalizeClass(symbol.FullyQualified)
		if !strings.EqualFold(name, owner) &&
			!snapshot.IsSubtypeOf(name, owner) {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result
}

func IsDiscriminatorClass(
	index *php.PHPIndex,
	owner,
	class string,
) bool {
	if index == nil || owner == "" || class == "" {
		return false
	}
	if strings.EqualFold(normalizeClass(owner), normalizeClass(class)) {
		_, found := index.FindClass(class)
		return found
	}
	return index.SemanticSnapshot().IsSubtypeOf(
		normalizeClass(class),
		normalizeClass(owner),
	)
}
