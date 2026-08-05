package symfony

import (
	"strings"

	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func IsYAMLServiceID(node *yamlsyntax.Node) bool {
	path := yamlquery.PairPath(node)
	return len(path) == 2 && path[0] == "services" && !strings.HasPrefix(path[1], "_")
}

func IsYAMLClassPropertyInService(node *yamlsyntax.Node) bool {
	path := yamlquery.PairPath(node)
	return len(path) == 3 && path[0] == "services" && path[2] == "class"
}

func IsYAMLArgumentServiceID(node *yamlsyntax.Node) bool {
	_, ok := YAMLServiceReferenceName(node)
	return ok
}

func YAMLContextValue(node *yamlsyntax.Node) string {
	if node == nil {
		return ""
	}
	if IsYAMLServiceID(node) {
		return yamlquery.ScalarValue(yamlquery.PairKey(yamlquery.AncestorPair(node)))
	}
	if node.Kind() == yamlsyntax.YamlScalar {
		return yamlquery.ScalarValue(node)
	}
	pair := yamlquery.AncestorPair(node)
	value := yamlquery.PairValue(pair)
	if value != nil && value.Kind() == yamlsyntax.YamlScalar {
		return yamlquery.ScalarValue(value)
	}
	return ""
}

func YAMLServiceReferenceName(node *yamlsyntax.Node) (string, bool) {
	if node == nil || node.Kind() != yamlsyntax.YamlScalar {
		return "", false
	}
	pair := yamlquery.AncestorPair(node)
	if pair == nil || yamlquery.PairKey(pair) == node {
		return "", false
	}
	path := yamlquery.PairPath(node)
	if len(path) < 2 || path[0] != "services" {
		return "", false
	}
	value := strings.TrimSpace(yamlquery.ScalarValue(node))
	if value == "@" || value == "@?" || value == "@!" {
		return "", true
	}
	if service, ok := NormalizeServiceReference(value); ok {
		return service, true
	}
	if len(path) >= 3 {
		switch path[2] {
		case "alias", "decorates", "parent":
			value = strings.TrimSpace(strings.TrimPrefix(value, "@"))
			return value, value != "" && !strings.ContainsAny(value, "%${}")
		}
	}
	return "", false
}

func YAMLClassReferenceName(node *yamlsyntax.Node) (string, bool) {
	if node == nil || node.Kind() != yamlsyntax.YamlScalar {
		return "", false
	}
	path := yamlquery.PairPath(node)
	if len(path) == 3 && path[0] == "services" && path[2] == "class" {
		value := yamlquery.ScalarValue(node)
		return value, staticPHPClassName(value)
	}
	if len(path) != 2 || path[0] != "services" {
		return "", false
	}
	pair := yamlquery.AncestorPair(node)
	if pair == nil || yamlquery.PairKey(pair) != node {
		return "", false
	}
	serviceID := yamlquery.ScalarValue(node)
	value := yamlquery.PairValue(pair)
	if yamlquery.IsMapping(value) &&
		(yamlquery.Property(value, "class") != nil ||
			yamlquery.Property(value, "resource") != nil ||
			yamlquery.Property(value, "exclude") != nil) {
		return "", false
	}
	return serviceID, staticPHPClassName(serviceID)
}

func staticPHPClassName(value string) bool {
	return value != "" && strings.Contains(value, "\\") &&
		!strings.ContainsAny(value, "%${}@")
}
