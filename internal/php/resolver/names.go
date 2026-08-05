// Package resolver implements PHP name, hierarchy, member, and signature
// resolution over semantic snapshots.
package resolver

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// NameContext is the namespace/import environment at one source position.
type NameContext struct {
	Namespace     string
	Imports       semantic.ImportTable
	PHPDocAliases map[string]types.Type
}

func NewNameContext(namespace string) NameContext {
	return NameContext{
		Namespace: strings.Trim(namespace, "\\"),
	}
}

func (c NameContext) ResolveClass(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "\\") {
		return strings.TrimPrefix(name, "\\")
	}
	if hasPrefixFold(name, "namespace\\") {
		return qualify(c.Namespace, name[len("namespace\\"):])
	}
	head, tail := splitHead(name)
	if len(c.Imports.Classes) != 0 {
		if imported, ok := lookupFoldedImport(c.Imports.Classes, head); ok {
			return imported + tail
		}
	}
	return qualify(c.Namespace, name)
}

func (c NameContext) ResolveFunction(name string) []string {
	return c.resolveCallableOrConstant(
		name,
		c.Imports.Functions,
		true,
	).slice()
}

func (c NameContext) ResolveConstant(name string) []string {
	return c.resolveCallableOrConstant(
		name,
		c.Imports.Constants,
		false,
	).slice()
}

// VisitFunctionNames visits resolved function candidates in PHP lookup order
// without materializing the compatibility result slice.
func (c NameContext) VisitFunctionNames(
	name string,
	visit func(string) bool,
) bool {
	return c.resolveCallableOrConstant(
		name,
		c.Imports.Functions,
		true,
	).visit(visit)
}

// VisitConstantNames visits resolved constant candidates in PHP lookup order
// without materializing the compatibility result slice.
func (c NameContext) VisitConstantNames(
	name string,
	visit func(string) bool,
) bool {
	return c.resolveCallableOrConstant(
		name,
		c.Imports.Constants,
		false,
	).visit(visit)
}

func (c NameContext) resolveCallableOrConstant(
	name string,
	imports map[string]string,
	foldCase bool,
) resolvedNameSet {
	name = strings.TrimSpace(name)
	if name == "" {
		return resolvedNameSet{}
	}
	if strings.HasPrefix(name, "\\") {
		return oneResolvedName(strings.TrimPrefix(name, "\\"))
	}
	if hasPrefixFold(name, "namespace\\") {
		return oneResolvedName(
			qualify(c.Namespace, name[len("namespace\\"):]),
		)
	}
	head, tail := splitHead(name)
	if len(imports) != 0 {
		key := head
		if foldCase {
			if imported, ok := lookupFoldedImport(imports, key); ok {
				return oneResolvedName(imported + tail)
			}
		} else if imported, ok := imports[key]; ok {
			return oneResolvedName(imported + tail)
		}
	}
	if strings.Contains(name, "\\") {
		return oneResolvedName(qualify(c.Namespace, name))
	}
	if c.Namespace == "" {
		return oneResolvedName(name)
	}
	// Unqualified functions and constants fall back to the global namespace.
	return resolvedNameSet{
		first:  qualify(c.Namespace, name),
		second: name,
		count:  2,
	}
}

type resolvedNameSet struct {
	first  string
	second string
	count  uint8
}

func oneResolvedName(name string) resolvedNameSet {
	return resolvedNameSet{first: name, count: 1}
}

func (s resolvedNameSet) slice() []string {
	switch s.count {
	case 0:
		return nil
	case 1:
		return []string{s.first}
	default:
		return []string{s.first, s.second}
	}
}

func (s resolvedNameSet) visit(visit func(string) bool) bool {
	if visit == nil || s.count == 0 {
		return true
	}
	if !visit(s.first) {
		return false
	}
	return s.count < 2 || visit(s.second)
}

func hasPrefixFold(source, prefix string) bool {
	return len(source) >= len(prefix) &&
		strings.EqualFold(source[:len(prefix)], prefix)
}

// lookupFoldedImport probes the lowercase import maps without allocating an
// ephemeral folded string for ordinary ASCII PHP identifiers. Conversion from
// the local byte buffer is allocation-free in the map-index expression.
func lookupFoldedImport(
	imports map[string]string,
	name string,
) (string, bool) {
	const maxInlineImportName = 128
	if len(name) > maxInlineImportName {
		value, ok := imports[strings.ToLower(name)]
		return value, ok
	}
	var folded [maxInlineImportName]byte
	for index := range len(name) {
		current := name[index]
		switch {
		case current >= 'A' && current <= 'Z':
			folded[index] = current + ('a' - 'A')
		case current < 0x80:
			folded[index] = current
		default:
			value, ok := imports[strings.ToLower(name)]
			return value, ok
		}
	}
	value, ok := imports[string(folded[:len(name)])]
	return value, ok
}

// ResolveType resolves every named object nested in a semantic type.
func (c NameContext) ResolveType(value types.Type) types.Type {
	return c.resolveType(value, nil)
}

// ResolvePHPDocType resolves names while preserving template identifiers from
// the surrounding class and function PHPDoc scopes.
func (c NameContext) ResolvePHPDocType(
	value types.Type,
	templateNames []string,
) types.Type {
	templates := make(map[string]struct{}, len(templateNames))
	for _, name := range templateNames {
		templates[name] = struct{}{}
	}
	return c.resolveType(value, templates)
}

func (c NameContext) resolveType(
	value types.Type,
	templates map[string]struct{},
) types.Type {
	args := value.Arguments()
	for index := range args {
		args[index] = c.resolveType(args[index], templates)
	}
	switch value.Kind() {
	case types.ObjectKind:
		if value.Name() == "" {
			return types.Object()
		}
		if _, exists := templates[value.Name()]; exists &&
			!strings.Contains(value.Name(), "\\") {
			return types.Template(value.Name())
		}
		if !strings.Contains(value.Name(), "\\") {
			if alias, exists := c.PHPDocAliases[value.Name()]; exists {
				return alias
			}
		}
		return types.Named(c.ResolveClass(value.Name()), args...)
	case types.SelfKind:
		return types.Self(args...)
	case types.StaticKind:
		return types.Static(args...)
	case types.ParentKind:
		return types.Parent(args...)
	case types.ArrayKind:
		return types.Array(args[0], args[1])
	case types.NonEmptyArrayKind:
		return types.NonEmptyArray(args[0], args[1])
	case types.ListKind:
		return types.List(args[0])
	case types.NonEmptyListKind:
		return types.NonEmptyList(args[0])
	case types.IterableKind:
		return types.Iterable(args[0], args[1])
	case types.ClassStringKind:
		return types.ClassString(args[0])
	case types.UnionKind:
		return types.Union(args...)
	case types.IntersectionKind:
		return types.Intersection(args...)
	case types.ConditionalKind:
		return types.Conditional(args[0], args[1], args[2], args[3])
	case types.CallableKind:
		parameters := value.Parameters()
		for index := range parameters {
			parameters[index].Type = c.resolveType(parameters[index].Type, templates)
		}
		return types.Callable(
			parameters,
			c.resolveType(value.Result(), templates),
		)
	case types.ArrayShapeKind, types.ObjectShapeKind:
		fields := value.Fields()
		for index := range fields {
			fields[index].Type = c.resolveType(fields[index].Type, templates)
		}
		if value.Kind() == types.ArrayShapeKind {
			return types.ArrayShape(fields, value.IsOpenShape())
		}
		return types.ObjectShape(fields, value.IsOpenShape())
	default:
		return value
	}
}

func qualify(namespace, name string) string {
	name = strings.Trim(name, "\\")
	namespace = strings.Trim(namespace, "\\")
	if namespace == "" {
		return name
	}
	if name == "" {
		return namespace
	}
	return namespace + "\\" + name
}

func splitHead(name string) (string, string) {
	if separator := strings.IndexByte(name, '\\'); separator >= 0 {
		return name[:separator], name[separator:]
	}
	return name, ""
}
