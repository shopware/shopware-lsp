package codeaction

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const twigAbstractExtensionClass = "Twig\\Extension\\AbstractExtension"

type twigExtensionRegistrySpec struct {
	methodName     string
	registration   string
	attributeClass string
}

var twigExtensionRegistrySpecs = []twigExtensionRegistrySpec{
	{
		methodName:     "getFilters",
		registration:   "Twig\\TwigFilter",
		attributeClass: "Twig\\Attribute\\AsTwigFilter",
	},
	{
		methodName:     "getFunctions",
		registration:   "Twig\\TwigFunction",
		attributeClass: "Twig\\Attribute\\AsTwigFunction",
	},
	{
		methodName:     "getTests",
		registration:   "Twig\\TwigTest",
		attributeClass: "Twig\\Attribute\\AsTwigTest",
	},
}

type twigExtensionTransformation struct {
	name           string
	options        string
	attributeClass string
	targetMethod   *phpsyntax.Node
	arrayItem      *phpsyntax.Node
}

type twigExtensionRegistryPlan struct {
	method          *phpsyntax.Node
	array           *phpsyntax.Node
	transformations []twigExtensionTransformation
	removeMethod    bool
}

type phpSourceReplacement struct {
	start uint32
	end   uint32
	text  string
}

type TwigExtensionAttributeCodeActionProvider struct {
	phpIndex *php.PHPIndex
}

func NewTwigExtensionAttributeCodeActionProvider(
	phpIndex *php.PHPIndex,
) *TwigExtensionAttributeCodeActionProvider {
	return &TwigExtensionAttributeCodeActionProvider{phpIndex: phpIndex}
}

func (p *TwigExtensionAttributeCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *TwigExtensionAttributeCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.CodeActionParams == nil ||
		request.Document == nil || request.Root == nil ||
		request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	class := phpquery.ClassAt(request.Node)
	if class == nil || !p.isTwigExtension(request, class) {
		return nil
	}
	resolver := php.NewNameResolver(request.Root)
	methods := phpMethodsByName(class)
	var plans []twigExtensionRegistryPlan
	var transformations []twigExtensionTransformation
	for _, spec := range twigExtensionRegistrySpecs {
		method := methods[strings.ToLower(spec.methodName)]
		if method == nil {
			continue
		}
		array := directPHPReturnArray(method)
		if array == nil {
			continue
		}
		items := phpquery.ArrayItems(array)
		var current []twigExtensionTransformation
		for _, item := range items {
			transformation, found := twigExtensionArrayTransformation(
				item,
				method,
				methods,
				resolver,
				spec,
			)
			if found {
				current = append(current, transformation)
			}
		}
		if len(current) == 0 {
			continue
		}
		plans = append(plans, twigExtensionRegistryPlan{
			method:          method,
			array:           array,
			transformations: current,
			removeMethod:    len(current) == len(items),
		})
		transformations = append(transformations, current...)
	}
	if len(transformations) == 0 {
		return nil
	}

	rewritten, found := buildTwigExtensionAttributeRewrite(
		request,
		class,
		resolver,
		methods,
		plans,
		transformations,
	)
	if !found || rewritten == request.Document.Source {
		return nil
	}
	return []protocol.CodeAction{{
		Title: "Migrate to TwigExtension attributes",
		Kind:  protocol.CodeActionRefactorRewrite,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				request.TextDocument.URI: {
					{
						Range: offsetRange(
							request,
							0,
							uint32(len(request.Document.Source)),
						),
						NewText: rewritten,
					},
				},
			},
		},
	}}
}

func (p *TwigExtensionAttributeCodeActionProvider) isTwigExtension(
	request *lsp.CodeActionRequest,
	class *phpsyntax.Node,
) bool {
	resolver := php.NewNameResolver(request.Root)
	for _, parent := range phpquery.ClassExtends(class) {
		if strings.EqualFold(
			strings.Trim(resolver.Resolve(parent), `\`),
			twigAbstractExtensionClass,
		) {
			return true
		}
	}
	path, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return false
	}
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		request.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	className := phpquery.ClassName(class)
	if namespace := phpquery.Namespace(request.Root); namespace != "" {
		className = namespace + `\` + className
	}
	return snapshot.IsSubtypeOf(className, twigAbstractExtensionClass)
}

func phpMethodsByName(class *phpsyntax.Node) map[string]*phpsyntax.Node {
	result := map[string]*phpsyntax.Node{}
	for _, method := range phpquery.Methods(class) {
		name := strings.ToLower(phpquery.MethodName(method))
		if name != "" {
			result[name] = method
		}
	}
	return result
}

func directPHPReturnArray(method *phpsyntax.Node) *phpsyntax.Node {
	body := phpquery.DirectChild(method, phpsyntax.PhpBlock)
	if body == nil {
		return nil
	}
	var result *phpsyntax.Node
	for _, phpReturn := range phpquery.Nodes(
		method,
		phpsyntax.PhpReturnStatement,
	) {
		if phpquery.FunctionLikeAt(phpReturn) != method ||
			phpReturn.Parent() != body {
			continue
		}
		var expression *phpsyntax.Node
		for child := range phpReturn.ChildNodes() {
			expression = child
			break
		}
		if expression == nil || expression.Kind() != phpsyntax.PhpArray {
			continue
		}
		if result != nil {
			return nil
		}
		result = expression
	}
	return result
}

func twigExtensionArrayTransformation(
	item,
	registryMethod *phpsyntax.Node,
	methods map[string]*phpsyntax.Node,
	resolver *php.NameResolver,
	spec twigExtensionRegistrySpec,
) (twigExtensionTransformation, bool) {
	value := phpquery.ArrayItemValue(item)
	if value == nil || value.Kind() != phpsyntax.PhpObjectCreation ||
		!strings.EqualFold(
			strings.Trim(
				resolver.Resolve(phpquery.ObjectClassName(value)),
				`\`,
			),
			spec.registration,
		) {
		return twigExtensionTransformation{}, false
	}
	nameExpression := phpquery.ArgumentExpression(value, 0)
	if nameExpression == nil || nameExpression.Kind() != phpsyntax.PhpString {
		return twigExtensionTransformation{}, false
	}
	name := phpquery.StringValue(nameExpression)
	if name == "" {
		return twigExtensionTransformation{}, false
	}
	callback := phpquery.ArgumentExpression(value, 1)
	methodName := twigThisCallableMethod(callback)
	targetMethod := methods[strings.ToLower(methodName)]
	if methodName == "" || targetMethod == nil ||
		targetMethod == registryMethod {
		return twigExtensionTransformation{}, false
	}
	options, valid := twigAttributeOptions(
		phpquery.ArgumentExpression(value, 2),
	)
	if !valid {
		return twigExtensionTransformation{}, false
	}
	return twigExtensionTransformation{
		name:           name,
		options:        options,
		attributeClass: spec.attributeClass,
		targetMethod:   targetMethod,
		arrayItem:      item,
	}, true
}

func twigThisCallableMethod(expression *phpsyntax.Node) string {
	if expression == nil {
		return ""
	}
	if expression.Kind() == phpsyntax.PhpArray {
		items := phpquery.ArrayItems(expression)
		if len(items) < 2 {
			return ""
		}
		receiver := phpquery.ArrayItemValue(items[0])
		method := phpquery.ArrayItemValue(items[1])
		if receiver == nil || method == nil ||
			strings.TrimSpace(receiver.Text()) != "$this" ||
			method.Kind() != phpsyntax.PhpString {
			return ""
		}
		return phpquery.StringValue(method)
	}
	if expression.Kind() != phpsyntax.PhpMemberCall ||
		!strings.Contains(expression.Text(), "...") {
		return ""
	}
	receiver := phpquery.CallReceiver(expression)
	if receiver == nil || strings.TrimSpace(receiver.Text()) != "$this" {
		return ""
	}
	return phpquery.CallMethodName(expression)
}

func twigAttributeOptions(
	expression *phpsyntax.Node,
) (string, bool) {
	if expression == nil {
		return "", true
	}
	if expression.Kind() != phpsyntax.PhpArray {
		return "", false
	}
	var options []string
	for _, item := range phpquery.ArrayItems(expression) {
		key := phpquery.ArrayItemKey(item)
		value := phpquery.ArrayItemValue(item)
		if key == nil || key.Kind() != phpsyntax.PhpString ||
			value == nil {
			continue
		}
		name := lowerCamelCase(phpquery.StringValue(key))
		if name == "" {
			continue
		}
		options = append(
			options,
			name+": "+strings.TrimSpace(value.Text()),
		)
	}
	return strings.Join(options, ", "), true
}

func lowerCamelCase(value string) string {
	parts := strings.FieldsFunc(value, func(current rune) bool {
		return current == '_' || current == '-' || current == ' '
	})
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	if len(parts) == 1 {
		return strings.ToLower(result[:1]) + result[1:]
	}
	result = strings.ToLower(result)
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		result += strings.ToUpper(part[:1]) + part[1:]
	}
	return result
}

func buildTwigExtensionAttributeRewrite(
	request *lsp.CodeActionRequest,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
	methods map[string]*phpsyntax.Node,
	plans []twigExtensionRegistryPlan,
	transformations []twigExtensionTransformation,
) (string, bool) {
	source := request.Document.Source
	var replacements []phpSourceReplacement
	removedMethods := map[*phpsyntax.Node]struct{}{}
	for _, plan := range plans {
		if plan.removeMethod {
			rng := plan.method.Range()
			replacements = append(replacements, phpSourceReplacement{
				start: rng.Start,
				end:   rng.End,
			})
			removedMethods[plan.method] = struct{}{}
			continue
		}
		for _, transformation := range plan.transformations {
			start := transformation.arrayItem.RangeTrimmedTrivia().Start
			end := transformation.arrayItem.RangeTrimmedTrivia().End
			if comma := phpArrayItemTrailingComma(
				plan.array,
				end,
			); comma != nil {
				end = comma.Range().End
			}
			replacements = append(replacements, phpSourceReplacement{
				start: start,
				end:   end,
			})
		}
	}

	qualifiers := map[string]string{}
	var imports []string
	for _, transformation := range transformations {
		if _, found := qualifiers[transformation.attributeClass]; found {
			continue
		}
		qualifier, importEdit := phpClassQualifier(
			request,
			transformation.attributeClass,
		)
		if qualifier == "" {
			return "", false
		}
		qualifiers[transformation.attributeClass] = qualifier
		if importEdit != nil {
			imports = append(imports, transformation.attributeClass)
		}
	}
	if len(imports) != 0 {
		replacements = append(replacements, phpSourceReplacement{
			start: phpImportInsertionOffset(request.Root),
			end:   phpImportInsertionOffset(request.Root),
			text:  phpImportBlock(request.Root, imports),
		})
	}

	attributesByMethod := map[*phpsyntax.Node][]string{}
	for _, transformation := range transformations {
		attribute := "#[" + qualifiers[transformation.attributeClass] +
			"('" + escapePHPSingleQuoted(transformation.name) + "'"
		if transformation.options != "" {
			attribute += ", " + transformation.options
		}
		attribute += ")]"
		attributesByMethod[transformation.targetMethod] = append(
			attributesByMethod[transformation.targetMethod],
			attribute,
		)
	}
	for method, attributes := range attributesByMethod {
		if _, removed := removedMethods[method]; removed {
			return "", false
		}
		offset := method.RangeTrimmedTrivia().Start
		indent := phpLineIndentation(source, offset)
		replacements = append(replacements, phpSourceReplacement{
			start: offset,
			end:   offset,
			text:  strings.Join(attributes, "\n"+indent) + "\n" + indent,
		})
	}

	if twigRegistryMethodsAllRemoved(methods, removedMethods) {
		if extends := directTwigAbstractExtensionClause(
			class,
			resolver,
		); extends != nil {
			start, end := horizontalWhitespaceRange(
				source,
				extends.RangeTrimmedTrivia().Start,
				extends.RangeTrimmedTrivia().End,
			)
			replacements = append(replacements, phpSourceReplacement{
				start: start,
				end:   end,
			})
			if declaration := standalonePHPImport(
				request.Root,
				twigAbstractExtensionClass,
			); declaration != nil &&
				!phpClassNameUsedOutside(
					request.Root,
					resolver,
					twigAbstractExtensionClass,
					declaration,
					extends,
				) {
				rng := declaration.Range()
				replacements = append(
					replacements,
					phpSourceReplacement{start: rng.Start, end: rng.End},
				)
			}
		}
	}
	return applyPHPSourceReplacements(source, replacements)
}

func phpArrayItemTrailingComma(
	array *phpsyntax.Node,
	itemEnd uint32,
) *phpsyntax.Token {
	for token := range array.ChildTokens() {
		if token.Kind() == phpsyntax.TkComma &&
			token.Range().Start >= itemEnd {
			return token
		}
	}
	return nil
}

func phpImportBlock(root *phpsyntax.Node, classes []string) string {
	prefix := "\n"
	if len(phpquery.UseDeclarations(root)) == 0 {
		prefix = "\n\n"
	}
	return prefix + "use " + strings.Join(classes, ";\nuse ") + ";"
}

func twigRegistryMethodsAllRemoved(
	methods map[string]*phpsyntax.Node,
	removed map[*phpsyntax.Node]struct{},
) bool {
	for _, spec := range twigExtensionRegistrySpecs {
		if method := methods[strings.ToLower(spec.methodName)]; method != nil {
			if _, found := removed[method]; !found {
				return false
			}
		}
	}
	return true
}

func directTwigAbstractExtensionClause(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) *phpsyntax.Node {
	clause := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
	if clause == nil {
		return nil
	}
	for _, parent := range phpquery.ClassExtends(class) {
		if strings.EqualFold(
			strings.Trim(resolver.Resolve(parent), `\`),
			twigAbstractExtensionClass,
		) {
			return clause
		}
	}
	return nil
}

func standalonePHPImport(
	root *phpsyntax.Node,
	className string,
) *phpsyntax.Node {
	for _, declaration := range phpquery.UseDeclarations(root) {
		imports := phpresolver.ParseUseDeclaration(declaration.Text())
		if len(imports) == 1 &&
			imports[0].Kind == phpresolver.ClassImport &&
			strings.EqualFold(
				strings.Trim(imports[0].Target, `\`),
				strings.Trim(className, `\`),
			) {
			return declaration
		}
	}
	return nil
}

func phpClassNameUsedOutside(
	root *phpsyntax.Node,
	resolver *php.NameResolver,
	className string,
	excluded ...*phpsyntax.Node,
) bool {
	for _, name := range phpquery.Nodes(root, phpsyntax.PhpName) {
		skip := false
		for _, node := range excluded {
			if node != nil &&
				name.Range().Start >= node.Range().Start &&
				name.Range().End <= node.Range().End {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if strings.EqualFold(
			strings.Trim(
				resolver.Resolve(phpquery.NameValue(name)),
				`\`,
			),
			strings.Trim(className, `\`),
		) {
			return true
		}
	}
	return false
}

func horizontalWhitespaceRange(
	source string,
	start,
	end uint32,
) (uint32, uint32) {
	for start > 0 &&
		(source[start-1] == ' ' || source[start-1] == '\t') {
		start--
	}
	for int(end) < len(source) &&
		(source[end] == ' ' || source[end] == '\t') {
		end++
	}
	return start, end
}

func escapePHPSingleQuoted(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

func applyPHPSourceReplacements(
	source string,
	replacements []phpSourceReplacement,
) (string, bool) {
	sort.SliceStable(replacements, func(left, right int) bool {
		if replacements[left].start != replacements[right].start {
			return replacements[left].start > replacements[right].start
		}
		return replacements[left].end > replacements[right].end
	})
	boundary := uint32(len(source))
	for _, replacement := range replacements {
		if replacement.start > replacement.end ||
			int(replacement.end) > len(source) ||
			replacement.end > boundary {
			return "", false
		}
		source = source[:replacement.start] + replacement.text +
			source[replacement.end:]
		boundary = replacement.start
	}
	return source, true
}
