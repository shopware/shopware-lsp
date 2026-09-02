package translation

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type ReferenceRole uint8

const (
	ReferenceNone ReferenceRole = iota
	ReferenceKey
	ReferenceDomain
	ReferencePlaceholder
)

type PHPReferenceKind uint8

const (
	PHPReferenceNone PHPReferenceKind = iota
	PHPReferenceMethod
	PHPReferenceFunction
	PHPReferenceMessage
	PHPReferenceValidatorMessage
	PHPReferenceValidatorDomain
)

type Reference struct {
	Role      ReferenceRole
	Key       string
	Domain    string
	Node      *phpsyntax.Node
	Container *phpsyntax.Node
	PHPKind   PHPReferenceKind
	Class     string
}

func ReferenceAt(
	path string,
	node *cst.Node,
	source []byte,
) (Reference, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		if reference, ok := PHPReferenceAt(node); ok {
			return reference, true
		}
		return PHPPlaceholderReferenceAt(node)
	case ".twig":
		return TwigReferenceAt(node, source)
	default:
		return Reference{}, false
	}
}

func References(
	path string,
	root *cst.Node,
	source []byte,
) []Reference {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return PHPReferences(root)
	case ".twig":
		return TwigReferences(root, source)
	default:
		return nil
	}
}

func PHPReferenceAt(node *phpsyntax.Node) (Reference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return Reference{}, false
	}
	container, kind, keyIndex, domainIndex := phpTranslationContainer(literal)
	if container == nil {
		return phpValidatorReferenceAt(literal)
	}
	argument := phpArgumentContaining(container, literal)
	if argument == nil || phpquery.ArgumentExpression(
		container,
		phpquery.ArgumentIndex(container, literal),
	) != literal {
		return Reference{}, false
	}
	argumentName := phpquery.ArgumentName(argument)
	index := phpquery.ArgumentIndex(container, literal)

	role := ReferenceNone
	switch argumentName {
	case "id":
		role = ReferenceKey
	case "domain":
		role = ReferenceDomain
	case "":
		switch index {
		case keyIndex:
			role = ReferenceKey
		case domainIndex:
			role = ReferenceDomain
		}
	}
	if role == ReferenceNone {
		return Reference{}, false
	}

	key := phpStringArgument(container, "id", keyIndex)
	domain := phpStringArgument(container, "domain", domainIndex)
	if domain == "" && role == ReferenceKey {
		domain = "messages"
	}
	return Reference{
		Role:      role,
		Key:       key,
		Domain:    normalizeDomain(domain),
		Node:      literal,
		Container: container,
		PHPKind:   kind,
	}, true
}

func PHPPlaceholderReferenceAt(node *phpsyntax.Node) (Reference, bool) {
	literal := phpquery.StringAt(node)
	array := phpquery.ArrayAt(literal)
	if literal == nil || array == nil {
		return Reference{}, false
	}
	container, kind, keyIndex, domainIndex := phpTranslationContainer(array)
	if container == nil {
		return Reference{}, false
	}
	parameterIndex := 1
	if kind == PHPReferenceMethod &&
		strings.EqualFold(phpquery.CallMethodName(container), "transChoice") {
		parameterIndex = 2
	}
	expression := phpquery.ArgumentExpression(container, parameterIndex)
	if expression != array {
		return Reference{}, false
	}
	key := phpStringArgument(container, "id", keyIndex)
	if key == "" {
		return Reference{}, false
	}
	domain := phpStringArgument(container, "domain", domainIndex)
	if domain == "" {
		domain = "messages"
	}
	return Reference{
		Role:      ReferencePlaceholder,
		Key:       key,
		Domain:    normalizeDomain(domain),
		Node:      literal,
		Container: container,
		PHPKind:   kind,
	}, true
}

func PHPReferences(root *phpsyntax.Node) []Reference {
	var result []Reference
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		reference, ok := PHPReferenceAt(literal)
		if ok {
			result = append(result, reference)
		}
	}
	return result
}

func ValidatePHPReference(
	ctx context.Context,
	reference Reference,
	phpIndex *php.PHPIndex,
	content []byte,
) bool {
	if reference.PHPKind == PHPReferenceNone {
		return true
	}
	if phpIndex == nil {
		return true
	}
	switch reference.PHPKind {
	case PHPReferenceMethod:
		for _, className := range []string{
			"Symfony\\Component\\Translation\\TranslatorInterface",
			"Symfony\\Contracts\\Translation\\TranslatorInterface",
			"Symfony\\Bundle\\FrameworkBundle\\Templating\\Helper\\TranslatorHelper",
		} {
			if phpIndex.IsMethodCalledOnClass(
				ctx,
				reference.Container,
				content,
				className,
			) {
				return true
			}
		}
		return false
	case PHPReferenceFunction:
		name := strings.TrimPrefix(
			phpquery.CallName(reference.Container),
			"\\",
		)
		return name == "Symfony\\Component\\Translation\\t" ||
			name == "t"
	case PHPReferenceMessage:
		return strings.EqualFold(
			lastNamePart(phpquery.ObjectClassName(reference.Container)),
			"TranslatableMessage",
		)
	case PHPReferenceValidatorMessage:
		if reference.Class != "" {
			phpContext := php.GetPHPContext(ctx)
			return phpContext != nil && phpContext.Snapshot != nil &&
				phpContext.Snapshot.Relations().IsSubtype(
					types.Named(reference.Class),
					types.Named(
						"Symfony\\Component\\Validator\\Constraint",
					),
				)
		}
		for _, className := range []string{
			"Symfony\\Component\\Validator\\Context\\ExecutionContextInterface",
			"Symfony\\Component\\Validator\\Context\\ExecutionContext",
		} {
			if phpIndex.IsMethodCalledOnClass(
				ctx,
				reference.Container,
				content,
				className,
			) {
				return true
			}
		}
		return false
	case PHPReferenceValidatorDomain:
		for _, className := range []string{
			"Symfony\\Component\\Validator\\Violation\\ConstraintViolationBuilderInterface",
			"Symfony\\Component\\Validator\\Violation\\ConstraintViolationBuilder",
		} {
			if phpIndex.IsMethodCalledOnClass(
				ctx,
				reference.Container,
				content,
				className,
			) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func phpValidatorReferenceAt(
	literal *phpsyntax.Node,
) (Reference, bool) {
	if call := phpquery.CallAt(literal); call != nil {
		index := phpquery.ArgumentIndex(call, literal)
		if index == 0 &&
			phpquery.ArgumentExpression(call, 0) == literal {
			switch strings.ToLower(phpquery.CallMethodName(call)) {
			case "addviolation", "buildviolation":
				return Reference{
					Role:      ReferenceKey,
					Key:       phpquery.StringValue(literal),
					Domain:    "validators",
					Node:      literal,
					Container: call,
					PHPKind:   PHPReferenceValidatorMessage,
				}, true
			case "settranslationdomain":
				return Reference{
					Role:      ReferenceDomain,
					Domain:    normalizeDomain(phpquery.StringValue(literal)),
					Node:      literal,
					Container: call,
					PHPKind:   PHPReferenceValidatorDomain,
				}, true
			}
		}
	}

	root := phpRoot(literal)
	nameResolver := php.NewNameResolver(root)
	if object := phpAncestor(
		literal,
		phpsyntax.PhpObjectCreation,
	); object != nil {
		if property, found := constraintMessageProperty(
			object,
			literal,
		); found {
			className := strings.TrimPrefix(
				nameResolver.Resolve(phpquery.ObjectClassName(object)),
				`\`,
			)
			return Reference{
				Role:      ReferenceKey,
				Key:       phpquery.StringValue(literal),
				Domain:    "validators",
				Node:      literal,
				Container: object,
				PHPKind:   PHPReferenceValidatorMessage,
				Class:     className,
			}, isConstraintMessageProperty(property)
		}
	}
	if attribute := phpquery.AttributeAt(literal); attribute != nil {
		if property, found := constraintMessageProperty(
			attribute,
			literal,
		); found {
			className := strings.TrimPrefix(
				nameResolver.Resolve(phpquery.AttributeName(attribute)),
				`\`,
			)
			return Reference{
				Role:      ReferenceKey,
				Key:       phpquery.StringValue(literal),
				Domain:    "validators",
				Node:      literal,
				Container: attribute,
				PHPKind:   PHPReferenceValidatorMessage,
				Class:     className,
			}, isConstraintMessageProperty(property)
		}
	}
	if property := phpAncestor(
		literal,
		phpsyntax.PhpPropertyDeclaration,
	); property != nil {
		for _, variable := range phpquery.PropertyVariables(property) {
			name := phpquery.VariableName(variable)
			if !isConstraintMessageProperty(name) {
				continue
			}
			className := strings.TrimPrefix(
				nameResolver.Resolve(
					phpquery.ClassName(phpquery.ClassAt(property)),
				),
				`\`,
			)
			return Reference{
				Role:      ReferenceKey,
				Key:       phpquery.StringValue(literal),
				Domain:    "validators",
				Node:      literal,
				Container: property,
				PHPKind:   PHPReferenceValidatorMessage,
				Class:     className,
			}, true
		}
	}
	return Reference{}, false
}

func constraintMessageProperty(
	container,
	literal *phpsyntax.Node,
) (string, bool) {
	for index, argument := range phpquery.Arguments(container) {
		if argument.Range().Start > literal.Range().Start ||
			literal.Range().End > argument.Range().End {
			continue
		}
		if name := phpquery.ArgumentName(argument); name != "" &&
			phpquery.ArgumentExpression(container, index) == literal {
			return name, true
		}
		array := phpquery.ArrayAt(phpquery.ArgumentExpression(container, index))
		item := phpquery.ArrayItemAt(literal)
		if index != 0 || array == nil || item == nil ||
			item.Parent() != array ||
			phpquery.ArrayItemValue(item) != literal {
			continue
		}
		key := phpquery.ArrayItemKey(item)
		if key == nil || key.Kind() != phpsyntax.PhpString {
			continue
		}
		return phpquery.StringValue(key), true
	}
	return "", false
}

func isConstraintMessageProperty(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "message") ||
		strings.HasSuffix(lower, "message")
}

func phpAncestor(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func phpRoot(node *phpsyntax.Node) *phpsyntax.Node {
	for node != nil && node.Parent() != nil {
		node = node.Parent()
	}
	return node
}

func phpTranslationContainer(
	node *phpsyntax.Node,
) (*phpsyntax.Node, PHPReferenceKind, int, int) {
	if call := phpquery.CallAt(node); call != nil {
		method := strings.ToLower(phpquery.CallMethodName(call))
		switch method {
		case "trans":
			if call.Kind() == phpsyntax.PhpFunctionCall &&
				lastNamePart(phpquery.CallName(call)) == "t" {
				return call, PHPReferenceFunction, 0, 2
			}
			return call, PHPReferenceMethod, 0, 2
		case "transchoice":
			return call, PHPReferenceMethod, 0, 3
		case "t":
			if call.Kind() == phpsyntax.PhpFunctionCall {
				return call, PHPReferenceFunction, 0, 2
			}
		}
	}
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != phpsyntax.PhpObjectCreation {
			continue
		}
		if strings.EqualFold(
			lastNamePart(phpquery.ObjectClassName(current)),
			"TranslatableMessage",
		) {
			return current, PHPReferenceMessage, 0, 2
		}
		break
	}
	return nil, PHPReferenceNone, -1, -1
}

func phpArgumentContaining(
	container,
	node *phpsyntax.Node,
) *phpsyntax.Node {
	for _, argument := range phpquery.Arguments(container) {
		if argument.Range().Start <= node.Range().Start &&
			node.Range().End <= argument.Range().End {
			return argument
		}
	}
	return nil
}

func phpStringArgument(
	container *phpsyntax.Node,
	name string,
	fallbackIndex int,
) string {
	for index, argument := range phpquery.Arguments(container) {
		if phpquery.ArgumentName(argument) != name {
			continue
		}
		expression := phpquery.ArgumentExpression(container, index)
		if expression != nil && expression.Kind() == phpsyntax.PhpString {
			return phpquery.StringValue(expression)
		}
		return ""
	}
	expression := phpquery.ArgumentExpression(container, fallbackIndex)
	if expression == nil || expression.Kind() != phpsyntax.PhpString {
		return ""
	}
	return phpquery.StringValue(expression)
}

func TwigReferenceAt(
	node *twigsyntax.Node,
	source []byte,
) (Reference, bool) {
	literal := twigquery.LiteralStringAt(node)
	if literal != nil {
		if twigquery.StringInFilter(literal, "trans", "transchoice") {
			domain := twigFilterDomain(literal)
			if domain == "" {
				domain = twigDefaultDomainBefore(source, literal.Range().Start)
			}
			if domain == "" {
				domain = "messages"
			}
			return Reference{
				Role:   ReferenceKey,
				Key:    twigquery.StringValue(literal),
				Domain: normalizeDomain(domain),
				Node:   literal,
			}, true
		}

		filter := twigquery.ClosestNodeOfKind(literal, twigsyntax.TwigFilter)
		if filter != nil {
			name := strings.ToLower(twigquery.FilterName(filter))
			parameterIndex := -1
			expectedIndex := -1
			switch name {
			case "trans":
				parameterIndex = 0
				expectedIndex = 1
			case "transchoice":
				parameterIndex = 1
				expectedIndex = 2
			}
			if parameterIndex >= 0 &&
				twigFilterArgumentIndex(filter, literal) == parameterIndex &&
				twigquery.HashAt(literal) != nil {
				key := twigFilterKey(filter)
				domain := twigFilterDomain(keyNodeInFilter(filter))
				if domain == "" {
					domain = twigDefaultDomainBefore(source, literal.Range().Start)
				}
				if domain == "" {
					domain = "messages"
				}
				return Reference{
					Role:   ReferencePlaceholder,
					Key:    key,
					Domain: normalizeDomain(domain),
					Node:   literal,
				}, key != ""
			}
			if expectedIndex >= 0 &&
				twigFilterArgumentIndex(filter, literal) == expectedIndex {
				key := twigFilterKey(filter)
				return Reference{
					Role:   ReferenceDomain,
					Key:    key,
					Domain: normalizeDomain(twigquery.StringValue(literal)),
					Node:   literal,
				}, true
			}
		}
	}

	domain, domainNode, ok := twigDefaultDomainAt(node, source)
	if !ok {
		return Reference{}, false
	}
	return Reference{
		Role:   ReferenceDomain,
		Domain: normalizeDomain(domain),
		Node:   domainNode,
	}, true
}

func TwigReferences(root *twigsyntax.Node, source []byte) []Reference {
	var result []Reference
	seen := make(map[string]struct{})
	for node := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigLiteralString,
		twigsyntax.TwigLiteralName,
		twigsyntax.HtmlText,
	) {
		reference, ok := TwigReferenceAt(node, source)
		if !ok {
			continue
		}
		key := referenceIdentity(reference)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func twigFilterDomain(key *twigsyntax.Node) string {
	filter := twigquery.ClosestNodeOfKind(key, twigsyntax.TwigFilter)
	if filter == nil {
		return ""
	}
	index := -1
	switch strings.ToLower(twigquery.FilterName(filter)) {
	case "trans":
		index = 1
	case "transchoice":
		index = 2
	}
	if index < 0 {
		return ""
	}
	if literal := twigFilterStringArgument(filter, index); literal != nil {
		return twigquery.StringValue(literal)
	}
	return ""
}

func twigFilterKey(filter *twigsyntax.Node) string {
	if filter == nil {
		return ""
	}
	for literal := range twigquery.IterateNodes(filter, twigsyntax.TwigLiteralString) {
		if twigquery.StringInFilter(literal, "trans", "transchoice") {
			return twigquery.StringValue(literal)
		}
	}
	return ""
}

func keyNodeInFilter(filter *twigsyntax.Node) *twigsyntax.Node {
	for literal := range twigquery.IterateNodes(filter, twigsyntax.TwigLiteralString) {
		if twigquery.StringInFilter(literal, "trans", "transchoice") {
			return literal
		}
	}
	return nil
}

func twigFilterStringArgument(
	filter *twigsyntax.Node,
	index int,
) *twigsyntax.Node {
	arguments := firstTwigNode(filter, twigsyntax.TwigArguments)
	if arguments == nil {
		return nil
	}
	currentIndex := 0
	for child := range arguments.ChildNodes() {
		if child.Kind() != twigsyntax.TwigExpression {
			continue
		}
		if currentIndex == index {
			for literal := range twigquery.IterateNodes(
				child,
				twigsyntax.TwigLiteralString,
			) {
				return literal
			}
			return nil
		}
		currentIndex++
	}
	return nil
}

func twigFilterArgumentIndex(
	filter,
	node *twigsyntax.Node,
) int {
	arguments := firstTwigNode(filter, twigsyntax.TwigArguments)
	if arguments == nil {
		return -1
	}
	index := 0
	for child := range arguments.ChildNodes() {
		if child.Kind() != twigsyntax.TwigExpression {
			continue
		}
		if child.Range().Start <= node.Range().Start &&
			node.Range().End <= child.Range().End {
			return index
		}
		index++
	}
	return -1
}

func firstTwigNode(
	root *twigsyntax.Node,
	kind twigsyntax.Kind,
) *twigsyntax.Node {
	for node := range twigquery.IterateNodes(root, kind) {
		return node
	}
	return nil
}

func twigDefaultDomainBefore(source []byte, offset uint32) string {
	if int(offset) > len(source) {
		offset = uint32(len(source))
	}
	prefix := source[:offset]
	for {
		index := bytes.LastIndex(prefix, []byte("trans_default_domain"))
		if index < 0 {
			return ""
		}
		open := bytes.LastIndex(prefix[:index], []byte("{%"))
		close := bytes.Index(prefix[index:], []byte("%}"))
		if open >= 0 && close >= 0 {
			value := parseDefaultDomainTag(
				string(prefix[open : index+close+2]),
			)
			if value != "" {
				return value
			}
		}
		prefix = prefix[:index]
	}
}

// TwigDefaultDomainBefore returns the active trans_default_domain declaration
// before offset. Callers should use the conventional "messages" domain when
// this returns an empty string.
func TwigDefaultDomainBefore(source []byte, offset uint32) string {
	return twigDefaultDomainBefore(source, offset)
}

func twigDefaultDomainAt(
	node *twigsyntax.Node,
	source []byte,
) (string, *twigsyntax.Node, bool) {
	if node == nil || len(source) == 0 {
		return "", nil, false
	}
	offset := int(node.Range().Start)
	if offset > len(source) {
		offset = len(source)
	}
	open := bytes.LastIndex(source[:offset], []byte("{%"))
	if open < 0 {
		open = bytes.LastIndex(source[:min(offset+1, len(source))], []byte("{%"))
	}
	closeRelative := bytes.Index(source[offset:], []byte("%}"))
	if open < 0 || closeRelative < 0 {
		return "", nil, false
	}
	close := offset + closeRelative + 2
	if close > len(source) {
		return "", nil, false
	}
	value, valid := defaultDomainTagValue(string(source[open:close]))
	if !valid {
		return "", nil, false
	}
	domainNode := twigquery.LiteralStringAt(node)
	if domainNode == nil {
		domainNode = node
	}
	return value, domainNode, true
}

func parseDefaultDomainTag(tag string) string {
	value, _ := defaultDomainTagValue(tag)
	return value
}

func defaultDomainTagValue(tag string) (string, bool) {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, "{%")
	tag = strings.TrimSuffix(tag, "%}")
	tag = strings.TrimSpace(tag)
	const name = "trans_default_domain"
	if !strings.HasPrefix(tag, name) {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(tag, name))
	if value == "" {
		return "", true
	}
	if index := strings.IndexAny(value, " \t\r\n"); index >= 0 {
		value = value[:index]
	}
	return strings.Trim(strings.TrimSpace(value), `"'`), true
}

func referenceIdentity(reference Reference) string {
	if reference.Node == nil {
		return ""
	}
	return strconv.Itoa(int(reference.Role)) + ":" +
		strconv.FormatUint(uint64(reference.Node.Range().Start), 10) + ":" +
		strconv.FormatUint(uint64(reference.Node.Range().End), 10)
}
