package dal

import (
	"strings"

	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

type JSFieldReferenceKind uint8

const (
	JSFieldReferenceNone JSFieldReferenceKind = iota
	JSFieldReferenceField
	JSFieldReferenceAssociation
)

type JSEntityReferenceKind uint8

const (
	JSEntityReferenceNone JSEntityReferenceKind = iota
	JSEntityReferenceRepository
	JSEntityReferenceDefinitionGet
	JSEntityReferenceDefinitionHas
	JSEntityReferenceAppScript
)

type JSEntityReference struct {
	Name string
	Kind JSEntityReferenceKind
}

func IsJSEntityReference(node *jssyntax.Node) bool {
	_, found := JSEntityReferenceAt(node)
	return found
}

// JSEntityReferenceAt recognizes static Shopware DAL entity-name arguments in
// Administration repository and EntityDefinition APIs, plus App Script
// repository calls. The kind lets diagnostics distinguish a strict lookup
// from an intentional has() existence guard.
func JSEntityReferenceAt(node *jssyntax.Node) (JSEntityReference, bool) {
	if jsquery.StringAt(node) == nil || jsquery.StringArgumentIndex(node) != 0 {
		return JSEntityReference{}, false
	}
	name := jsquery.CallName(node)
	if strings.HasSuffix(name, ".create") &&
		strings.Contains(strings.ToLower(name), "repositoryfactory") {
		return jsEntityReference(node, JSEntityReferenceRepository)
	}
	switch name {
	case "EntityDefinition.get", "Shopware.EntityDefinition.get",
		"this.EntityDefinition.get":
		return jsEntityReference(node, JSEntityReferenceDefinitionGet)
	case "EntityDefinition.has", "Shopware.EntityDefinition.has",
		"this.EntityDefinition.has":
		return jsEntityReference(node, JSEntityReferenceDefinitionHas)
	case "services.repository.search", "services.repository.aggregate",
		"services.repository.searchIds":
		return jsEntityReference(node, JSEntityReferenceAppScript)
	default:
		return JSEntityReference{}, false
	}
}

func jsEntityReference(
	node *jssyntax.Node,
	kind JSEntityReferenceKind,
) (JSEntityReference, bool) {
	name := jsquery.StringValue(node)
	if name == "" {
		return JSEntityReference{}, false
	}
	return JSEntityReference{Name: name, Kind: kind}, true
}

// JSFieldReferenceAt recognizes Shopware Criteria field and association path
// arguments. Aggregation helpers use their first argument as the aggregation
// name and their second argument as the field path.
func JSFieldReferenceAt(node *jssyntax.Node) JSFieldReferenceKind {
	if jsquery.StringAt(node) == nil {
		return JSFieldReferenceNone
	}
	argumentIndex := jsquery.StringArgumentIndex(node)
	if argumentIndex < 0 {
		return JSFieldReferenceNone
	}
	callName := jsquery.CallName(node)
	methodName := jsquery.CallMethodName(node)
	switch methodName {
	case "addAssociation", "getAssociation", "hasAssociation":
		if argumentIndex == 0 {
			return JSFieldReferenceAssociation
		}
	case "addFields":
		return JSFieldReferenceField
	case "addGroupField":
		if argumentIndex == 0 {
			return JSFieldReferenceField
		}
	}
	if argumentIndex == 0 {
		switch callName {
		case "Criteria.equals", "Criteria.equalsAny", "Criteria.contains",
			"Criteria.prefix", "Criteria.suffix", "Criteria.range",
			"Criteria.sort", "Criteria.naturalSorting":
			return JSFieldReferenceField
		case "Criteria.hasAssociation":
			return JSFieldReferenceAssociation
		}
	}
	if argumentIndex == 1 {
		switch callName {
		case "Criteria.terms", "Criteria.count", "Criteria.avg",
			"Criteria.sum", "Criteria.max", "Criteria.min", "Criteria.stats":
			return JSFieldReferenceField
		}
	}
	return JSFieldReferenceNone
}

// JSFieldReferenceSegment returns the path segment under the cursor. A segment
// followed by a dot must itself be an association.
func JSFieldReferenceSegment(
	node *jssyntax.Node,
	offset uint32,
) (name string, association bool) {
	stringNode := jsquery.StringAt(node)
	if stringNode == nil || JSFieldReferenceAt(stringNode) == JSFieldReferenceNone {
		return "", false
	}
	value := jsquery.StringValue(stringNode)
	if value == "" {
		return "", false
	}
	raw := stringNode.Text()
	trimmed := strings.TrimSpace(raw)
	leading := strings.Index(raw, trimmed)
	contentStart := stringNode.Range().Start + uint32(max(leading, 0))
	if len(trimmed) != 0 &&
		(trimmed[0] == '\'' || trimmed[0] == '"' || trimmed[0] == '`') {
		contentStart++
	}
	relative := 0
	if offset > contentStart {
		relative = int(offset - contentStart)
	}
	if relative > len(value) {
		relative = len(value)
	}
	start := strings.LastIndex(value[:relative], ".") + 1
	end := len(value)
	if next := strings.Index(value[relative:], "."); next >= 0 {
		end = relative + next
	}
	if start > end {
		return "", false
	}
	return value[start:end], end < len(value)
}

func IsTwigEntityReference(node *twigsyntax.Node) bool {
	literal := twigquery.LiteralStringAt(node)
	call := twigquery.FunctionCallAt(node)
	if literal == nil || call == nil ||
		twigquery.FunctionArgumentIndex(literal) != 0 {
		return false
	}
	text := strings.Join(strings.Fields(call.Text()), "")
	parenthesis := strings.IndexByte(text, '(')
	if parenthesis >= 0 {
		text = text[:parenthesis]
	}
	switch text {
	case "services.repository.search", "services.repository.aggregate",
		"services.repository.searchIds":
		return true
	default:
		return false
	}
}

func IsPHPFieldReference(node *phpsyntax.Node) bool {
	stringNode := phpquery.StringAt(node)
	creation := phpquery.ObjectCreationAt(node)
	if stringNode == nil || creation == nil ||
		phpquery.ArgumentIndex(creation, stringNode) != 0 {
		return false
	}
	className := phpquery.ObjectClassName(creation)
	if separator := strings.LastIndex(className, `\`); separator >= 0 {
		className = className[separator+1:]
	}
	return strings.HasSuffix(className, "Filter") && className != "Filter"
}
