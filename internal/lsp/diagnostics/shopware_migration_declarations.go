package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

const (
	interfaceAbstractClassCode lsp.DiagnosticID = "shopware.migration.declaration.interface_to_abstract"
	addMethodParameterCode     lsp.DiagnosticID = "shopware.migration.declaration.parameter.add"
	nativeTypeMigrationCode    lsp.DiagnosticID = "shopware.migration.declaration.type"
)

type interfaceAbstractClassMigration struct {
	since         shopwareMigrationSince
	interfaceName string
	abstractClass string
}

type methodParameterMigration struct {
	since    shopwareMigrationSince
	class    string
	method   string
	position int
	name     string
	typeText string
}

type methodTypeMigration struct {
	since    shopwareMigrationSince
	class    string
	method   string
	position int
	typeText string
}

var interfaceAbstractClassMigrations = []interfaceAbstractClassMigration{
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface", "Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\Sitemap\\Provider\\UrlProviderInterface", "Shopware\\Core\\Content\\Sitemap\\Provider\\AbstractUrlProvider"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\System\\Snippet\\Files\\SnippetFileInterface", "Shopware\\Core\\System\\Snippet\\Files\\GenericSnippetFile"},
	{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface", "Shopware\\Core\\Content\\ProductStream\\Service\\AbstractProductStreamBuilder"},
}

var addedMethodParameterMigrations = []methodParameterMigration{
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Framework\\Captcha\\AbstractCaptcha", "supports", 1, "captchaConfig", "array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Framework\\Cache\\ReverseProxy\\AbstractReverseProxyGateway", "tag", 2, "response", "\\Symfony\\Component\\HttpFoundation\\Response"},
}

var parameterTypeMigrations = []methodTypeMigration{
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\DataAbstractionLayer\\Indexing\\EntityIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Theme\\DataAbstractionLayer\\ThemeIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\Flow\\Indexing\\FlowIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\Media\\DataAbstractionLayer\\MediaFolderConfigurationIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\Media\\DataAbstractionLayer\\MediaFolderIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\Media\\DataAbstractionLayer\\MediaIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\LandingPage\\DataAbstractionLayer\\LandingPageIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\ProductStream\\DataAbstractionLayer\\ProductStreamIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\Rule\\DataAbstractionLayer\\RuleIndexer", "iterate", 0, "?array"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Page\\Product\\Review\\ReviewLoaderResult", "setMatrix", 0, "\\Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\RatingMatrix"},
}

var returnTypeMigrations = []methodTypeMigration{
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\Adapter\\Twig\\TemplateIterator", "getIterator", -1, "\\Traversable"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\Cms\\DataResolver\\CriteriaCollection", "getIterator", -1, "\\Traversable"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Checkout\\Cart\\CartBehavior", "hasPermission", -1, "bool"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Page\\Product\\Review\\ReviewLoaderResult", "getMatrix", -1, "\\Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\RatingMatrix"},
	{shopwareMigrationSince{7, 0}, "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition", "buildTermQuery", -1, "\\OpenSearchDSL\\BuilderInterface"},
}

func (p *ShopwareMigrationAnalyzer) declarationMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	resolver := php.NewNameResolver(root)
	var result []lsp.Problem
	for _, class := range phpquery.Classes(root) {
		if ctx.Err() != nil {
			return result
		}
		result = append(result, p.interfaceAbstractClassProblems(class, root, resolver)...)
		result = append(result, p.addedMethodParameterProblems(class, root, snapshot)...)
		result = append(result, p.nativeTypeProblems(class, root, snapshot, resolver)...)
	}
	for _, parameter := range phpquery.Nodes(root, phpsyntax.PhpParameter) {
		if phpquery.ClassAt(parameter) != nil {
			continue
		}
		result = append(result, p.interfaceTypeProblem(parameter, resolver, "interface-parameter")...)
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) interfaceAbstractClassProblems(
	class *phpsyntax.Node,
	root *phpsyntax.Node,
	resolver *php.NameResolver,
) []lsp.Problem {
	var result []lsp.Problem
	implements := phpquery.DirectChild(class, phpsyntax.PhpImplementsClause)
	if implements != nil {
		for _, name := range directPHPChildrenOfKind(implements, phpsyntax.PhpName) {
			resolved := strings.Trim(resolver.Resolve(strings.TrimSpace(name.Text())), "\\")
			for _, rule := range interfaceAbstractClassMigrations {
				if !rule.since.active(p) || !strings.EqualFold(resolved, rule.interfaceName) {
					continue
				}
				safe := classCanAdoptParent(class, resolver, rule.abstractClass)
				result = append(result, declarationMigrationProblem(
					interfaceAbstractClassCode,
					class,
					"Shopware "+rule.since.label()+": extend "+rule.abstractClass+" instead of implementing "+rule.interfaceName,
					"interface-class",
					strings.TrimSpace(name.Text()),
					"\\"+rule.abstractClass,
					0,
					safe,
				))
			}
		}
	}
	for _, parameter := range phpquery.Nodes(class, phpsyntax.PhpParameter) {
		if phpquery.ClassAt(parameter) != class {
			continue
		}
		result = append(result, p.interfaceTypeProblem(parameter, resolver, "interface-parameter")...)
	}
	for _, property := range phpquery.Properties(class) {
		result = append(result, p.interfaceTypeProblem(property, resolver, "interface-property")...)
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) interfaceTypeProblem(
	declaration *phpsyntax.Node,
	resolver *php.NameResolver,
	kind string,
) []lsp.Problem {
	typeText := ""
	if kind == "interface-parameter" {
		typeText = phpquery.ParameterType(declaration)
	} else {
		typeText = phpquery.PropertyType(declaration)
	}
	if typeText == "" || strings.ContainsAny(typeText, "|&?") {
		return nil
	}
	resolved := strings.Trim(resolver.Resolve(typeText), "\\")
	for _, rule := range interfaceAbstractClassMigrations {
		if rule.since.active(p) && strings.EqualFold(resolved, rule.interfaceName) {
			return []lsp.Problem{declarationMigrationProblem(
				interfaceAbstractClassCode,
				declaration,
				"Shopware "+rule.since.label()+": replace "+rule.interfaceName+" type with "+rule.abstractClass,
				kind,
				typeText,
				"\\"+rule.abstractClass,
				0,
				true,
			)}
		}
	}
	return nil
}

func (p *ShopwareMigrationAnalyzer) addedMethodParameterProblems(
	class *phpsyntax.Node,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	var result []lsp.Problem
	for _, rule := range addedMethodParameterMigrations {
		if !rule.since.active(p) || !phpClassIsSubtype(class, root, snapshot, rule.class) {
			continue
		}
		method := phpOwnMethodForMigration(class, rule.method)
		if method == nil || methodHasParameter(method, "$"+rule.name) {
			continue
		}
		result = append(result, declarationMigrationProblem(
			addMethodParameterCode,
			method,
			"Shopware "+rule.since.label()+": add $"+rule.name+" to "+rule.method+"()",
			"parameter-add",
			"",
			rule.typeText+" $"+rule.name,
			rule.position,
			true,
		))
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) nativeTypeProblems(
	class *phpsyntax.Node,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
	resolver *php.NameResolver,
) []lsp.Problem {
	var result []lsp.Problem
	for _, rule := range parameterTypeMigrations {
		if !rule.since.active(p) || !phpClassIsSubtype(class, root, snapshot, rule.class) {
			continue
		}
		method := phpOwnMethodForMigration(class, rule.method)
		parameter := methodParameterAt(method, rule.position)
		if parameter == nil || nativeTypeMatches(phpquery.ParameterType(parameter), rule.typeText, resolver) {
			continue
		}
		result = append(result, declarationMigrationProblem(
			nativeTypeMigrationCode,
			parameter,
			"Shopware "+rule.since.label()+": use "+rule.typeText+" for parameter "+phpquery.ParameterName(parameter),
			"parameter-type",
			phpquery.ParameterType(parameter),
			rule.typeText,
			rule.position,
			true,
		))
	}
	for _, rule := range returnTypeMigrations {
		if !rule.since.active(p) || !phpClassIsSubtype(class, root, snapshot, rule.class) {
			continue
		}
		method := phpOwnMethodForMigration(class, rule.method)
		if method == nil || nativeTypeMatches(phpquery.MethodReturnType(method), rule.typeText, resolver) {
			continue
		}
		result = append(result, declarationMigrationProblem(
			nativeTypeMigrationCode,
			method,
			"Shopware "+rule.since.label()+": use "+rule.typeText+" as "+rule.method+"() return type",
			"return-type",
			phpquery.MethodReturnType(method),
			rule.typeText,
			-1,
			true,
		))
	}
	return result
}

func declarationMigrationProblem(
	code lsp.DiagnosticID,
	element *phpsyntax.Node,
	message string,
	kind string,
	original string,
	replacement string,
	position int,
	safe bool,
) lsp.Problem {
	rng := element.RangeTrimmedTrivia()
	if element.Kind() == phpsyntax.PhpClassDeclaration {
		if name := phpquery.DirectChild(element, phpsyntax.PhpName); name != nil {
			rng = name.RangeTrimmedTrivia()
		}
	}
	return lsp.Problem{
		ID:       code,
		Range:    rng,
		Element:  element,
		Message:  message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-rector",
		Payload: ShopwareMigrationPayload{
			Rule:          "declaration-migration",
			Kind:          kind,
			Safe:          safe,
			Original:      original,
			Replacement:   replacement,
			ArgumentIndex: position,
		},
	}
}

func classCanAdoptParent(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
	target string,
) bool {
	extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
	if extends == nil {
		return true
	}
	name := phpquery.DirectChild(extends, phpsyntax.PhpName)
	return name != nil && strings.EqualFold(
		strings.Trim(resolver.Resolve(strings.TrimSpace(name.Text())), "\\"),
		target,
	)
}

func methodHasParameter(method *phpsyntax.Node, name string) bool {
	parameters := phpquery.IterateParameters(method)
	for parameters.Next() {
		if strings.EqualFold(phpquery.ParameterName(parameters.Node()), name) {
			return true
		}
	}
	return false
}

func methodParameterAt(method *phpsyntax.Node, position int) *phpsyntax.Node {
	if method == nil || position < 0 {
		return nil
	}
	parameters := phpquery.IterateParameters(method)
	for index := 0; parameters.Next(); index++ {
		if index == position {
			return parameters.Node()
		}
	}
	return nil
}

func nativeTypeMatches(
	actual string,
	expected string,
	resolver *php.NameResolver,
) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if actual == "" || expected == "" {
		return actual == expected
	}
	if strings.ContainsAny(expected, "|&?") || !strings.Contains(expected, "\\") {
		return strings.EqualFold(strings.ReplaceAll(actual, " ", ""), strings.ReplaceAll(expected, " ", ""))
	}
	return strings.EqualFold(
		strings.Trim(resolver.Resolve(actual), "\\"),
		strings.Trim(expected, "\\"),
	)
}

func directPHPChildrenOfKind(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) []*phpsyntax.Node {
	if node == nil {
		return nil
	}
	var result []*phpsyntax.Node
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok && child.Kind() == kind {
			result = append(result, child)
		}
	}
	return result
}
