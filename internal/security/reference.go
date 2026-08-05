package security

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type Reference struct {
	Name      string
	Origin    Origin
	Node      *cst.Node
	Container *cst.Node
	Range     cst.TextRange
	Class     string
}

func ReferenceAt(
	ctx context.Context,
	path string,
	root,
	node *cst.Node,
	source string,
	offset uint32,
) (Reference, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		if reference, ok := phpCSTReferenceAt(
			ctx,
			root,
			node,
			offset,
		); ok {
			return reference, true
		}
		return rawPHPReferenceAt(root, source, offset)
	case ".twig":
		return twigReferenceAt(node)
	case ".yaml", ".yml":
		return yamlReferenceAt(node)
	default:
		return Reference{}, false
	}
}

// ReferencesInDocument returns every static authorization attribute use in an
// open PHP or Twig document. PHP call sites honor semantic receiver types when
// they are available in ctx.
func ReferencesInDocument(
	ctx context.Context,
	path string,
	root *cst.Node,
	source string,
) []Reference {
	if root == nil {
		return nil
	}
	var result []Reference
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
			offset := stringValueRange(literal).Start
			if reference, ok := phpCSTReferenceAt(
				ctx,
				root,
				literal,
				offset,
			); ok {
				result = append(result, reference)
			}
			for _, occurrence := range expressionOccurrences("", literal) {
				result = append(result, Reference{
					Name:   occurrence.Name,
					Origin: occurrence.Origin,
					Node:   literal,
					Range:  occurrence.Range,
				})
			}
		}
		for _, occurrence := range phpDocOccurrences("", source) {
			result = append(result, Reference{
				Name:   occurrence.Name,
				Origin: occurrence.Origin,
				Node:   root,
				Range:  occurrence.Range,
			})
		}
	case ".twig":
		for _, literal := range twigquery.Nodes(
			root,
			twigsyntax.TwigLiteralString,
		) {
			if reference, ok := twigReferenceAt(literal); ok {
				result = append(result, reference)
			}
		}
	default:
		return nil
	}
	return uniqueReferences(result)
}

func phpCSTReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
	offset uint32,
) (Reference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return Reference{}, false
	}
	reference := Reference{
		Name:  phpquery.StringValue(literal),
		Node:  literal,
		Range: stringValueRange(literal),
	}
	if ctx != nil {
		if phpContext := php.GetPHPContext(ctx); phpContext != nil &&
			phpContext.InsideClass != nil {
			reference.Class = phpContext.InsideClass.FullyQualified
		}
	}

	if attribute := phpquery.AttributeAt(literal); attribute != nil {
		resolver := php.NewNameResolver(root)
		resolved := strings.TrimPrefix(
			resolver.Resolve(phpquery.AttributeName(attribute)),
			`\`,
		)
		argumentIndex := phpquery.ArgumentIndex(attribute, literal)
		if argumentIndex == 0 && isIsGrantedAttribute(resolved) {
			reference.Origin = OriginPHPAttribute
			reference.Container = attribute
			return reference, true
		}
		if argumentIndex == 0 && isSecurityExpressionAttribute(resolved) {
			value := phpquery.StringValue(literal)
			base := stringValueRange(literal).Start
			for _, match := range securityExpressionPattern.
				FindAllStringSubmatchIndex(value, -1) {
				if len(match) < 6 {
					continue
				}
				rng := cst.TextRange{
					Start: base + uint32(match[4]),
					End:   base + uint32(match[5]),
				}
				if !rangeContainsCursor(rng, offset) {
					continue
				}
				reference.Name = value[match[4]:match[5]]
				reference.Range = rng
				reference.Origin = OriginPHPExpression
				reference.Container = attribute
				return reference, true
			}
			return Reference{}, false
		}
	}

	call := phpquery.CallAt(literal)
	if call == nil {
		return Reference{}, false
	}
	index := phpquery.ArgumentIndex(call, literal)
	expected := phpAuthorizationArgument(phpquery.CallMethodName(call))
	if expected < 0 || index != expected ||
		!isDescendantOf(
			literal,
			phpquery.ArgumentExpression(call, expected),
		) ||
		!isAuthorizationCall(ctx, call) {
		return Reference{}, false
	}
	reference.Origin = OriginPHPCall
	reference.Container = call
	return reference, true
}

func rawPHPReferenceAt(
	root *phpsyntax.Node,
	source string,
	offset uint32,
) (Reference, bool) {
	occurrences := phpDocOccurrences("", source)
	for _, occurrence := range occurrences {
		if !rangeContainsCursor(occurrence.Range, offset) {
			continue
		}
		return Reference{
			Name:   occurrence.Name,
			Origin: occurrence.Origin,
			Node:   root,
			Range:  occurrence.Range,
		}, true
	}
	return Reference{}, false
}

func twigReferenceAt(node *twigsyntax.Node) (Reference, bool) {
	literal := twigquery.LiteralStringAt(node)
	if literal == nil || !twigquery.StringIsStatic(literal) {
		return Reference{}, false
	}
	call := twigquery.FunctionCallAt(literal)
	if call == nil {
		return Reference{}, false
	}
	expected := twigAuthorizationArgument(twigquery.FunctionName(call))
	if expected < 0 || twigquery.FunctionArgumentIndex(literal) != expected {
		return Reference{}, false
	}
	return Reference{
		Name:      twigquery.StringValue(literal),
		Origin:    OriginTwig,
		Node:      literal,
		Container: call,
		Range:     twigValueRange(literal),
	}, true
}

func yamlReferenceAt(node *yamlsyntax.Node) (Reference, bool) {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != yamlsyntax.YamlScalar {
			continue
		}
		path := yamlquery.PairPath(current)
		origin := OriginUnknown
		if containsPath(path, "security", "role_hierarchy") {
			origin = OriginRoleHierarchy
		} else if containsPath(path, "security", "access_control") &&
			yamlScalarIsRolesValue(current) {
			origin = OriginAccessControl
		}
		if origin == OriginUnknown {
			return Reference{}, false
		}
		return Reference{
			Name:   yamlquery.ScalarValue(current),
			Origin: origin,
			Node:   current,
			Range:  yamlValueRange(current),
		}, true
	}
	return Reference{}, false
}

func phpAuthorizationArgument(method string) int {
	switch strings.ToLower(method) {
	case "isgranted", "denyaccessunlessgranted":
		return 0
	case "isgrantedforuser":
		return 1
	default:
		return -1
	}
}

func twigAuthorizationArgument(function string) int {
	switch strings.ToLower(function) {
	case "is_granted", "access_decision":
		return 0
	case "is_granted_for_user", "access_decision_for_user":
		return 1
	default:
		return -1
	}
}

func isAuthorizationCall(
	ctx context.Context,
	call *phpsyntax.Node,
) bool {
	if ctx == nil {
		return true
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return true
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return true
	}
	for _, target := range []string{
		"Symfony\\Component\\Security\\Core\\Authorization\\AuthorizationCheckerInterface",
		"Symfony\\Component\\Security\\Core\\Authorization\\AuthorizationChecker",
		"Symfony\\Component\\Security\\Core\\Authorization\\UserAuthorizationCheckerInterface",
		"Symfony\\Bundle\\FrameworkBundle\\Controller\\Controller",
		"Symfony\\Bundle\\FrameworkBundle\\Controller\\ControllerTrait",
		"Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController",
	} {
		if phpContext.Snapshot.Relations().IsSubtype(
			receiverType,
			types.Named(target),
		) {
			return true
		}
	}
	return false
}

func isIsGrantedAttribute(name string) bool {
	return strings.EqualFold(
		name,
		"Sensio\\Bundle\\FrameworkExtraBundle\\Configuration\\IsGranted",
	) || strings.EqualFold(
		name,
		"Symfony\\Component\\Security\\Http\\Attribute\\IsGranted",
	) || strings.EqualFold(name, "IsGranted")
}

func isSecurityExpressionAttribute(name string) bool {
	return strings.EqualFold(
		name,
		"Sensio\\Bundle\\FrameworkExtraBundle\\Configuration\\Security",
	) || strings.EqualFold(name, "Security")
}

func containsPath(path []string, first, second string) bool {
	for index := 0; index+1 < len(path); index++ {
		if path[index] == first && path[index+1] == second {
			return true
		}
	}
	return false
}

func yamlScalarIsRolesValue(node *yamlsyntax.Node) bool {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != yamlsyntax.YamlPair {
			continue
		}
		return yamlquery.ScalarValue(yamlquery.PairKey(current)) == "roles"
	}
	return false
}

func isDescendantOf(node, ancestor *cst.Node) bool {
	if node == nil || ancestor == nil {
		return false
	}
	return ancestor.Range().Start <= node.Range().Start &&
		node.Range().End <= ancestor.Range().End
}

func rangeContainsCursor(rng cst.TextRange, offset uint32) bool {
	return rng.Contains(offset) || offset == rng.End
}

func twigValueRange(node *twigsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"' || text[0] == '`') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}

func uniqueReferences(values []Reference) []Reference {
	seen := make(map[string]struct{}, len(values))
	result := make([]Reference, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value.Name) + ":" + value.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
