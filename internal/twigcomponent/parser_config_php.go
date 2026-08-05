package twigcomponent

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

const twigComponentExtensionName = "twig_component"

// configurationInPHP extracts Symfony bundle configuration from the portable
// PHP forms accepted by the Config component:
//
//   - return ['twig_component' => [...]];
//   - return App::config(['twig_component' => [...]]);
//   - App::config(['twig_component' => [...]]);
//   - a returned ContainerConfigurator closure calling extension().
func configurationInPHP(
	path string,
	root *phpsyntax.Node,
) ([]Namespace, []string) {
	if root == nil {
		return nil, nil
	}
	var namespaces []Namespace
	var anonymous []string
	visitConfig := func(array *phpsyntax.Node) {
		visitPHPConfigArray(
			path,
			array,
			&namespaces,
			&anonymous,
		)
	}

	for _, statement := range phpquery.Nodes(
		root,
		phpsyntax.PhpReturnStatement,
	) {
		if !isTopLevelPHPConfigNode(statement) {
			continue
		}
		if array := directReturnedPHPConfigArray(statement); array != nil {
			visitConfig(array)
		}
		if closure := directReturnedPHPConfigClosure(
			statement,
		); closure != nil {
			visitPHPExtensionConfigCalls(
				path,
				closure,
				&namespaces,
				&anonymous,
			)
		}
	}

	for _, call := range phpquery.Calls(root) {
		if !isPHPAppConfigCall(call) ||
			!isTopLevelPHPConfigNode(call) {
			continue
		}
		if array := phpquery.ArrayAt(
			phpquery.ArgumentExpression(call, 0),
		); array != nil {
			visitConfig(array)
		}
	}
	return uniqueNamespaces(namespaces), uniqueStrings(anonymous)
}

func directReturnedPHPConfigArray(
	statement *phpsyntax.Node,
) *phpsyntax.Node {
	for _, array := range phpquery.Nodes(statement, phpsyntax.PhpArray) {
		if closestPHPConfigAncestorOfKind(
			array.Parent(),
			statement,
			phpsyntax.PhpArray,
		) == nil {
			return array
		}
	}
	return nil
}

func directReturnedPHPConfigClosure(
	statement *phpsyntax.Node,
) *phpsyntax.Node {
	for _, closure := range phpquery.Nodes(statement, phpsyntax.PhpClosure) {
		current := closure.Parent()
		direct := true
		for current != nil && current != statement {
			switch current.Kind() {
			case phpsyntax.PhpArray,
				phpsyntax.PhpArrayItem,
				phpsyntax.PhpArgument,
				phpsyntax.PhpNamedArgument,
				phpsyntax.PhpFunctionCall,
				phpsyntax.PhpMemberCall,
				phpsyntax.PhpScopedCall:
				direct = false
			}
			if !direct {
				break
			}
			current = current.Parent()
		}
		if direct && current == statement {
			return closure
		}
	}
	return nil
}

func visitPHPConfigArray(
	path string,
	array *phpsyntax.Node,
	namespaces *[]Namespace,
	anonymous *[]string,
) {
	if array == nil {
		return
	}
	for _, item := range phpquery.ArrayItems(array) {
		keyNode := phpquery.ArrayItemKey(item)
		key, found := phpConfigString(keyNode)
		if !found {
			continue
		}
		value := phpquery.ArrayItemValue(item)
		switch {
		case key == twigComponentExtensionName:
			visitPHPTwigComponentConfig(
				path,
				phpquery.ArrayAt(value),
				namespaces,
				anonymous,
			)
		case strings.HasPrefix(key, "when@"):
			visitPHPConfigArray(
				path,
				phpquery.ArrayAt(value),
				namespaces,
				anonymous,
			)
		}
	}
}

func visitPHPTwigComponentConfig(
	path string,
	array *phpsyntax.Node,
	namespaces *[]Namespace,
	anonymous *[]string,
) {
	if array == nil {
		return
	}
	for _, item := range phpquery.ArrayItems(array) {
		key, found := phpConfigString(phpquery.ArrayItemKey(item))
		if !found {
			continue
		}
		value := phpquery.ArrayItemValue(item)
		switch key {
		case "defaults":
			visitPHPTwigComponentDefaults(
				path,
				phpquery.ArrayAt(value),
				namespaces,
			)
		case "anonymous_template_directory":
			if directory, ok := phpConfigString(value); ok {
				if directory = normalizeDirectory(directory); directory != "" {
					*anonymous = append(*anonymous, directory)
				}
			}
		}
	}
}

func visitPHPTwigComponentDefaults(
	path string,
	array *phpsyntax.Node,
	namespaces *[]Namespace,
) {
	if array == nil {
		return
	}
	for _, item := range phpquery.ArrayItems(array) {
		keyNode := phpquery.ArrayItemKey(item)
		classPrefix, found := phpConfigString(keyNode)
		if !found {
			continue
		}
		value := phpquery.ArrayItemValue(item)
		directory := ""
		namePrefix := ""
		if configured, ok := phpConfigString(value); ok {
			directory = configured
		} else if options := phpquery.ArrayAt(value); options != nil {
			directory = "components"
			for _, option := range phpquery.ArrayItems(options) {
				name, keyFound := phpConfigString(
					phpquery.ArrayItemKey(option),
				)
				if !keyFound {
					continue
				}
				configured, valueFound := phpConfigString(
					phpquery.ArrayItemValue(option),
				)
				if !valueFound {
					continue
				}
				switch name {
				case "template_directory":
					directory = configured
				case "name_prefix":
					namePrefix = configured
				}
			}
		}
		classPrefix = normalizeClass(classPrefix)
		directory = normalizeDirectory(directory)
		if classPrefix == "" || directory == "" {
			continue
		}
		*namespaces = append(*namespaces, Namespace{
			ClassPrefix:       classPrefix,
			TemplateDirectory: directory,
			NamePrefix:        normalizeComponentName(namePrefix),
			File:              path,
			Range:             phpquery.StringContentRange(keyNode),
		})
	}
}

func visitPHPExtensionConfigCalls(
	path string,
	closure *phpsyntax.Node,
	namespaces *[]Namespace,
	anonymous *[]string,
) {
	parameters := make(map[string]struct{})
	for _, parameter := range phpquery.Parameters(closure) {
		name := strings.TrimPrefix(phpquery.ParameterName(parameter), "$")
		if name != "" {
			parameters[name] = struct{}{}
		}
	}
	if len(parameters) == 0 {
		return
	}
	for _, call := range phpquery.Calls(closure) {
		if phpquery.CallMethodName(call) != "extension" ||
			closestPHPConfigFunctionLike(call) != closure {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil {
			continue
		}
		if _, accepted := parameters[phpquery.VariableName(receiver)]; !accepted {
			continue
		}
		extension, found := phpConfigString(
			phpquery.ArgumentExpression(call, 0),
		)
		if !found || extension != twigComponentExtensionName {
			continue
		}
		visitPHPTwigComponentConfig(
			path,
			phpquery.ArrayAt(phpquery.ArgumentExpression(call, 1)),
			namespaces,
			anonymous,
		)
	}
}

func phpConfigString(node *phpsyntax.Node) (string, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil || node == nil ||
		literal.Range() != node.Range() {
		return "", false
	}
	text := strings.TrimSpace(literal.Text())
	if len(text) < 2 {
		return "", false
	}
	quote := text[0]
	if (quote != '\'' && quote != '"') ||
		text[len(text)-1] != quote {
		return "", false
	}
	value := phpquery.StringValue(literal)
	if quote == '"' && strings.Contains(value, "$") {
		return "", false
	}
	value = strings.ReplaceAll(value, `\\`, `\`)
	if quote == '\'' {
		value = strings.ReplaceAll(value, `\'`, `'`)
	} else {
		value = strings.ReplaceAll(value, `\"`, `"`)
	}
	return value, true
}

func isPHPAppConfigCall(call *phpsyntax.Node) bool {
	name := strings.TrimPrefix(
		strings.ToLower(strings.TrimSpace(phpquery.CallName(call))),
		`\`,
	)
	return name == "app::config" ||
		strings.HasSuffix(name, `\app::config`)
}

func isTopLevelPHPConfigNode(node *phpsyntax.Node) bool {
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpMethodDeclaration,
			phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpClosure,
			phpsyntax.PhpArrowFunction,
			phpsyntax.PhpClassDeclaration,
			phpsyntax.PhpAnonymousClass:
			return false
		}
	}
	return true
}

func closestPHPConfigFunctionLike(
	node *phpsyntax.Node,
) *phpsyntax.Node {
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpMethodDeclaration,
			phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpClosure,
			phpsyntax.PhpArrowFunction:
			return current
		}
	}
	return nil
}

func closestPHPConfigAncestorOfKind(
	node,
	stop *phpsyntax.Node,
	kind phpsyntax.Kind,
) *phpsyntax.Node {
	for current := node; current != nil && current != stop; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}
