package doctrine

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type ReferenceRole uint8

const (
	EntityReference ReferenceRole = iota
	FieldReference
)

type ReferenceKind uint8

const (
	StringReference ReferenceKind = iota
	ClassConstantReference
)

type Reference struct {
	Role   ReferenceRole
	Kind   ReferenceKind
	Name   string
	Entity string
	Node   *cst.Node
	Call   *cst.Node
}

// EntityReferencesInDocument returns statically resolvable entity arguments
// passed to typed Doctrine APIs. It is shared by diagnostics/navigation and
// related-item code lenses so every surface uses the same signature checks.
func (idx *Index) EntityReferencesInDocument(
	ctx context.Context,
	root *phpsyntax.Node,
) []Reference {
	if idx == nil || root == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var result []Reference
	for _, call := range phpquery.Calls(root) {
		expression := phpquery.ArgumentExpression(call, 0)
		if expression == nil {
			continue
		}
		reference, found := idx.ReferenceAt(ctx, root, expression)
		if !found || reference.Role != EntityReference ||
			reference.Name == "" {
			continue
		}
		rng := ReferenceRange(reference)
		key := strings.ToLower(reference.Name) + "\x00" + rng.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func (idx *Index) ReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (Reference, bool) {
	if idx == nil || root == nil || node == nil {
		return Reference{}, false
	}
	resolver := php.NewNameResolver(root)
	if literal := phpquery.StringAt(node); literal != nil {
		call := phpquery.CallAt(literal)
		if call == nil {
			return Reference{}, false
		}
		if isEntityAPIArgument(ctx, call, literal) {
			value := phpquery.StringValue(literal)
			if strings.Contains(value, ":") {
				value = idx.canonicalModelName(value)
			} else if strings.Contains(value, `\`) {
				value = normalizeClass(value)
			} else if value != "" {
				value = normalizeClass(resolver.Resolve(value))
			}
			return Reference{
				Role: EntityReference,
				Kind: StringReference,
				Name: value,
				Node: literal,
				Call: call,
			}, true
		}
		if isCriteriaField(call, literal) {
			entity := idx.repositoryEntityAt(ctx, call, resolver)
			if entity == "" {
				return Reference{}, false
			}
			return Reference{
				Role:   FieldReference,
				Kind:   StringReference,
				Name:   phpquery.StringValue(literal),
				Entity: entity,
				Node:   literal,
				Call:   call,
			}, true
		}
		return Reference{}, false
	}

	call := phpquery.CallAt(node)
	if call == nil || !isEntityAPIArgument(ctx, call, node) {
		return Reference{}, false
	}
	expression := phpquery.ArgumentExpression(call, 0)
	if expression == nil {
		return Reference{}, false
	}
	className := phpquery.ClassConstantName(expression)
	if className == "" && expression.Kind() == phpsyntax.PhpName {
		className = phpquery.NameValue(expression)
	}
	if className != "" {
		className = normalizeClass(resolver.Resolve(className))
	}
	return Reference{
		Role: EntityReference,
		Kind: ClassConstantReference,
		Name: className,
		Node: expression,
		Call: call,
	}, true
}

func isEntityAPIArgument(
	ctx context.Context,
	call,
	node *phpsyntax.Node,
) bool {
	if phpquery.ArgumentIndex(call, node) != 0 ||
		phpquery.ArgumentExpression(call, 0) == nil {
		return false
	}
	method := strings.ToLower(phpquery.CallMethodName(call))
	var targets []string
	switch method {
	case "getrepository", "getreference", "getpartialreference",
		"getclassmetadata":
		targets = []string{
			"Doctrine\\Persistence\\ObjectManager",
			"Doctrine\\Persistence\\ManagerRegistry",
			"Doctrine\\Common\\Persistence\\ObjectManager",
			"Doctrine\\Common\\Persistence\\ManagerRegistry",
			"Doctrine\\ORM\\EntityManagerInterface",
			"Doctrine\\ORM\\EntityManager",
		}
	case "getmanagerforclass":
		targets = []string{
			"Doctrine\\Persistence\\ManagerRegistry",
			"Doctrine\\Common\\Persistence\\ManagerRegistry",
		}
	case "find":
		targets = []string{
			"Doctrine\\Persistence\\ObjectManager",
			"Doctrine\\Common\\Persistence\\ObjectManager",
			"Doctrine\\ORM\\EntityManagerInterface",
			"Doctrine\\ORM\\EntityManager",
		}
	case "update", "delete", "from":
		targets = []string{"Doctrine\\ORM\\QueryBuilder"}
	case "getentitycacheregion", "containsentity", "evictentity",
		"evictentityregion", "containscollection", "evictcollection",
		"evictcollectionregion":
		targets = []string{"Doctrine\\ORM\\Cache"}
	default:
		return false
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return false
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return false
	}
	relations := phpContext.Snapshot.Relations()
	for _, className := range targets {
		if relations.IsSubtype(receiverType, types.Named(className)) {
			return true
		}
	}
	return false
}

func isCriteriaField(call, literal *phpsyntax.Node) bool {
	switch strings.ToLower(phpquery.CallMethodName(call)) {
	case "findby", "findoneby", "count":
	default:
		return false
	}
	if phpquery.ArgumentIndex(call, literal) != 0 {
		return false
	}
	array := phpquery.ArrayAt(literal)
	if array == nil ||
		phpquery.ArrayAt(phpquery.ArgumentExpression(call, 0)) != array {
		return false
	}
	item := phpquery.ArrayItemAt(literal)
	if item == nil {
		return false
	}
	key := phpquery.ArrayItemKey(item)
	if key != nil {
		return phpquery.StringAt(key) == literal
	}
	return phpquery.StringAt(phpquery.ArrayItemValue(item)) == literal
}

func (idx *Index) repositoryEntityAt(
	ctx context.Context,
	call *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return ""
	}
	// The common fluent form can be resolved even before framework type
	// inference has rewritten the nested getRepository() result.
	if nested := phpquery.CallAt(receiver); nested != nil &&
		strings.EqualFold(phpquery.CallMethodName(nested), "getRepository") {
		if entity := classExpression(
			phpquery.ArgumentExpression(nested, 0),
			resolver,
		); entity != "" {
			return idx.canonicalModelName(entity)
		}
	}

	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil {
		return ""
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if entity := genericRepositoryEntity(receiverType); entity != "" {
		return entity
	}
	if receiverType.Kind() == types.ObjectKind &&
		receiverType.Name() != "" {
		if model, found, err := idx.ModelForRepository(
			receiverType.Name(),
		); err == nil && found {
			return model.Class
		}
	}
	if phpContext.InsideClass != nil {
		if model, found, err := idx.ModelForRepository(
			phpContext.InsideClass.FullyQualified,
		); err == nil && found {
			return model.Class
		}
	}
	return ""
}

func (idx *Index) RepositoryEntityForCall(
	ctx context.Context,
	root,
	call *phpsyntax.Node,
) string {
	if idx == nil || root == nil || call == nil {
		return ""
	}
	entity := idx.repositoryEntityAt(
		ctx,
		call,
		php.NewNameResolver(root),
	)
	if entity != "" {
		return entity
	}
	return idx.canonicalModelName(serviceRepositoryEntity(root, call))
}

func genericRepositoryEntity(value types.Type) string {
	if value.Kind() == types.UnionKind ||
		value.Kind() == types.IntersectionKind {
		for _, member := range value.Arguments() {
			if entity := genericRepositoryEntity(member); entity != "" {
				return entity
			}
		}
		return ""
	}
	if value.Kind() != types.ObjectKind || len(value.Arguments()) == 0 {
		return ""
	}
	switch strings.ToLower(value.Name()) {
	case strings.ToLower(objectRepositoryClass),
		strings.ToLower("Doctrine\\Common\\Persistence\\ObjectRepository"),
		strings.ToLower("Doctrine\\ORM\\EntityRepository"),
		strings.ToLower("Doctrine\\Bundle\\DoctrineBundle\\Repository\\ServiceEntityRepository"):
	default:
		return ""
	}
	entity := value.Arguments()[0]
	if entity.Kind() != types.ObjectKind {
		return ""
	}
	return normalizeClass(entity.Name())
}

func classExpression(
	expression *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	if expression == nil {
		return ""
	}
	if className := phpquery.ClassConstantName(expression); className != "" {
		return normalizeClass(resolver.Resolve(className))
	}
	if literal := phpquery.StringAt(expression); literal != nil {
		value := phpquery.StringValue(literal)
		if strings.Contains(value, ":") {
			return strings.TrimSpace(value)
		}
		if strings.Contains(value, `\`) {
			return normalizeClass(value)
		}
		return normalizeClass(resolver.Resolve(value))
	}
	return ""
}

func ReferenceRange(reference Reference) cst.TextRange {
	if reference.Node == nil {
		return cst.TextRange{}
	}
	rng := reference.Node.RangeTrimmedTrivia()
	if reference.Kind != StringReference {
		return rng
	}
	text := strings.TrimSpace(reference.Node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}
