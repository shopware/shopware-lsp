package console

import (
	"bytes"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
)

func isPHPCommandCandidate(content []byte) bool {
	return bytes.Contains(content, []byte("AsCommand")) ||
		bytes.Contains(content, []byte("$defaultName")) ||
		bytes.Contains(content, []byte("->setName(")) ||
		bytes.Contains(content, []byte("->addArgument(")) ||
		bytes.Contains(content, []byte("->addOption(")) ||
		bytes.Contains(content, []byte("#[Argument")) ||
		bytes.Contains(content, []byte("#[Option"))
}

func parsePHPCommands(file *indexer.ParsedFile) []Command {
	if file == nil {
		return nil
	}
	return ParsePHPCommandsTree(file.Path, file.SyntaxTree())
}

// ParsePHPCommandsTree extracts command declarations from an already parsed
// PHP document. On-demand LSP features use the open document's lossless tree
// so unsaved AsCommand names and input metadata stay current.
func ParsePHPCommandsTree(
	filePath string,
	tree *phpsyntax.Tree,
) []Command {
	if tree == nil || tree.Root == nil {
		return nil
	}
	return parsePHPCommandsRoot(filePath, tree.Root)
}

func parsePHPCommandsRoot(
	filePath string,
	root *phpsyntax.Node,
) []Command {
	namespace := phpquery.Namespace(root)
	var commands []Command
	for _, class := range phpquery.Classes(root) {
		className := phpquery.ClassName(class)
		if className == "" {
			continue
		}
		fqn := className
		if namespace != "" {
			fqn = namespace + `\` + className
		}
		classArguments, classOptions := classInputs(class, filePath)
		constants := classStringConstants(class)

		attributes := commandAttributes(class)
		if len(attributes) != 0 {
			var attributedCommands []Command
			for _, attribute := range attributes {
				attributedCommands = append(
					attributedCommands,
					commandsFromAttribute(
						attribute,
						constants,
						fqn,
						"",
						classArguments,
						classOptions,
						filePath,
					)...,
				)
			}
			if len(attributedCommands) != 0 {
				commands = append(commands, attributedCommands...)
			} else {
				commands = append(
					commands,
					traditionalCommands(
						class,
						fqn,
						classArguments,
						classOptions,
						filePath,
					)...,
				)
			}
		} else {
			commands = append(
				commands,
				traditionalCommands(
					class,
					fqn,
					classArguments,
					classOptions,
					filePath,
				)...,
			)
		}

		for _, method := range phpquery.Methods(class) {
			visibility := phpquery.DeclarationVisibility(method)
			if visibility == "private" || visibility == "protected" {
				continue
			}
			arguments, options := attributedInputs(method, filePath)
			for _, attribute := range commandAttributes(method) {
				commands = append(commands, commandsFromAttribute(
					attribute,
					constants,
					fqn,
					phpquery.MethodName(method),
					arguments,
					options,
					filePath,
				)...)
			}
		}
	}
	return commands
}

func commandAttributes(node *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, attribute := range phpquery.Attributes(node) {
		if attributeHasName(attribute, "AsCommand") {
			result = append(result, attribute)
		}
	}
	return result
}

func commandsFromAttribute(
	attribute *phpsyntax.Node,
	constants map[string]string,
	class,
	method string,
	arguments,
	options []Input,
	path string,
) []Command {
	nameNode := attributeArgument(attribute, "name", 0)
	primary := commandNameExpressionValue(nameNode, constants)
	if primary == "" {
		return nil
	}
	names := []commandAttributeName{{
		value: primary,
		node:  nameNode,
	}}
	if aliases := attributeArgument(attribute, "aliases", 2); aliases != nil &&
		aliases.Kind() == phpsyntax.PhpArray {
		for _, item := range phpquery.ArrayItems(aliases) {
			expression := phpquery.ArrayItemValue(item)
			if alias := commandNameExpressionValue(
				expression,
				constants,
			); alias != "" {
				names = append(names, commandAttributeName{
					value: alias,
					node:  expression,
				})
			}
		}
	}
	description := stringExpressionValue(
		attributeArgument(attribute, "description", 1),
	)
	result := make([]Command, 0, len(names))
	for _, name := range names {
		rng := nameNode.RangeTrimmedTrivia()
		if name.node != nil {
			rng = name.node.RangeTrimmedTrivia()
		}
		result = append(result, Command{
			Name:        name.value,
			Canonical:   primary,
			Description: description,
			Class:       class,
			Method:      method,
			File:        path,
			Range:       rng,
			Arguments:   arguments,
			Options:     options,
		})
	}
	return result
}

type commandAttributeName struct {
	value string
	node  *phpsyntax.Node
}

func commandNameExpressionValue(
	node *phpsyntax.Node,
	constants map[string]string,
) string {
	if value := stringExpressionValue(node); value != "" {
		return value
	}
	if node == nil ||
		(node.Kind() != phpsyntax.PhpScopedAccess &&
			node.Kind() != phpsyntax.PhpMemberAccess) {
		return ""
	}
	text := strings.Join(strings.Fields(node.Text()), "")
	separator := strings.LastIndex(text, "::")
	if separator <= 0 || separator+2 >= len(text) {
		return ""
	}
	scope := text[:separator]
	if !strings.EqualFold(scope, "self") &&
		!strings.EqualFold(scope, "static") {
		return ""
	}
	return constants[text[separator+2:]]
}

func classStringConstants(class *phpsyntax.Node) map[string]string {
	result := make(map[string]string)
	body := phpquery.ClassBody(class)
	if body == nil {
		return result
	}
	for child := range body.ChildNodes() {
		if child.Kind() != phpsyntax.PhpClassConstDeclaration {
			continue
		}
		expectName := false
		expectValue := false
		name := ""
		for index := 0; index < child.ChildCount(); index++ {
			element := child.Child(index)
			switch value := element.(type) {
			case *phpsyntax.Token:
				switch {
				case strings.EqualFold(value.Text(), "const"),
					value.Kind() == phpsyntax.TkComma:
					expectName = true
					expectValue = false
					name = ""
				case value.Kind() == phpsyntax.TkEquals:
					expectName = false
					expectValue = true
				}
			case *phpsyntax.Node:
				if expectName && value.Kind() == phpsyntax.PhpName {
					name = phpquery.NameValue(value)
					expectName = false
					continue
				}
				if !expectValue {
					continue
				}
				if name != "" && value.Kind() == phpsyntax.PhpString {
					if literal := phpquery.StringValue(value); literal != "" {
						result[name] = literal
					}
				}
				expectValue = false
			}
		}
	}
	return result
}

func traditionalCommands(
	class *phpsyntax.Node,
	fqn string,
	arguments,
	options []Input,
	path string,
) []Command {
	names, rng := traditionalCommandNames(class)
	commands := make([]Command, 0, len(names))
	for index, name := range names {
		command := Command{
			Name:      name,
			Canonical: names[0],
			Class:     fqn,
			File:      path,
			Range:     rng,
			Arguments: arguments,
			Options:   options,
		}
		if index == 0 {
			command.Canonical = name
		}
		commands = append(commands, command)
	}
	return commands
}

func traditionalCommandNames(
	class *phpsyntax.Node,
) ([]string, phpsyntax.TextRange) {
	for _, property := range phpquery.Properties(class) {
		found := false
		for _, variable := range phpquery.PropertyVariables(property) {
			if phpquery.VariableName(variable) == "defaultName" {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		literals := phpquery.Nodes(property, phpsyntax.PhpString)
		if len(literals) != 0 {
			return splitCommandNames(phpquery.StringValue(literals[0])),
				literals[0].RangeTrimmedTrivia()
		}
	}
	for _, method := range phpquery.Methods(class) {
		if !strings.EqualFold(phpquery.MethodName(method), "configure") {
			continue
		}
		for _, call := range phpquery.Nodes(
			method,
			phpsyntax.PhpMemberCall,
		) {
			if phpquery.FunctionLikeAt(call) != method ||
				!strings.EqualFold(phpquery.CallMethodName(call), "setName") {
				continue
			}
			literal := phpquery.StringArgument(call, 0)
			if literal != nil {
				return splitCommandNames(phpquery.StringValue(literal)),
					literal.RangeTrimmedTrivia()
			}
		}
	}
	return nil, phpsyntax.TextRange{}
}

func splitCommandNames(value string) []string {
	var result []string
	for _, name := range strings.Split(value, "|") {
		if name = strings.TrimSpace(name); name != "" {
			result = append(result, name)
		}
	}
	return result
}

func classInputs(
	class *phpsyntax.Node,
	path string,
) ([]Input, []Input) {
	var arguments, options []Input
	for _, method := range phpquery.Methods(class) {
		switch strings.ToLower(phpquery.MethodName(method)) {
		case "configure":
			for _, call := range phpquery.Nodes(
				method,
				phpsyntax.PhpMemberCall,
			) {
				if phpquery.FunctionLikeAt(call) != method {
					continue
				}
				switch strings.ToLower(phpquery.CallMethodName(call)) {
				case "addargument":
					if input, ok := inputFromArguments(call, Argument, path); ok {
						arguments = append(arguments, input)
					}
				case "addoption":
					if input, ok := inputFromArguments(call, Option, path); ok {
						options = append(options, input)
					}
				}
			}
			for _, creation := range phpquery.ObjectCreations(method) {
				switch shortPHPName(phpquery.ObjectClassName(creation)) {
				case "InputArgument":
					if input, ok := inputFromArguments(creation, Argument, path); ok {
						arguments = append(arguments, input)
					}
				case "InputOption":
					if input, ok := inputFromArguments(creation, Option, path); ok {
						options = append(options, input)
					}
				}
			}
		case "__invoke":
			attributedArguments, attributedOptions := attributedInputs(
				method,
				path,
			)
			arguments = append(arguments, attributedArguments...)
			options = append(options, attributedOptions...)
		}
	}
	return uniqueInputs(arguments), uniqueInputs(options)
}

// LegacyCommandInputs returns addArgument()/addOption() declarations from the
// class's own configure() method in source order. It is used by refactorings
// that must preserve the declaration order rather than the index's
// name-sorted representation.
func LegacyCommandInputs(
	class *phpsyntax.Node,
	path string,
) ([]Input, []Input) {
	if class == nil {
		return nil, nil
	}
	var arguments, options []Input
	seenArguments := make(map[string]struct{})
	seenOptions := make(map[string]struct{})
	for _, method := range phpquery.Methods(class) {
		if !strings.EqualFold(phpquery.MethodName(method), "configure") {
			continue
		}
		for _, call := range phpquery.Nodes(
			method,
			phpsyntax.PhpMemberCall,
		) {
			if phpquery.FunctionLikeAt(call) != method {
				continue
			}
			switch strings.ToLower(phpquery.CallMethodName(call)) {
			case "addargument":
				input, ok := inputFromArguments(
					call,
					Argument,
					path,
				)
				if !ok {
					continue
				}
				if _, exists := seenArguments[input.Name]; exists {
					continue
				}
				seenArguments[input.Name] = struct{}{}
				arguments = append(arguments, input)
			case "addoption":
				input, ok := inputFromArguments(
					call,
					Option,
					path,
				)
				if !ok {
					continue
				}
				if _, exists := seenOptions[input.Name]; exists {
					continue
				}
				seenOptions[input.Name] = struct{}{}
				options = append(options, input)
			}
		}
		break
	}
	return arguments, options
}

func attributedInputs(
	method *phpsyntax.Node,
	path string,
) ([]Input, []Input) {
	var arguments, options []Input
	for _, parameter := range phpquery.Parameters(method) {
		for _, attribute := range phpquery.Attributes(parameter) {
			var kind InputKind
			switch {
			case attributeHasName(attribute, "Argument"):
				kind = Argument
			case attributeHasName(attribute, "Option"):
				kind = Option
			default:
				continue
			}
			nameNode := attributeArgument(attribute, "name", -1)
			name := stringExpressionValue(nameNode)
			if name == "" {
				name = strings.TrimPrefix(phpquery.ParameterName(parameter), "$")
			}
			if name == "" {
				continue
			}
			rng := parameter.RangeTrimmedTrivia()
			if nameNode != nil {
				rng = nameNode.RangeTrimmedTrivia()
			}
			input := Input{
				Name: name,
				Kind: kind,
				Shortcut: stringExpressionValue(
					attributeArgument(attribute, "shortcut", -1),
				),
				Description: stringExpressionValue(
					attributeArgument(attribute, "description", -1),
				),
				Default: parameterDefault(parameter),
				File:    path,
				Range:   rng,
			}
			if kind == Argument {
				arguments = append(arguments, input)
			} else {
				options = append(options, input)
			}
		}
	}
	return uniqueInputs(arguments), uniqueInputs(options)
}

func inputFromArguments(
	call *phpsyntax.Node,
	kind InputKind,
	path string,
) (Input, bool) {
	nameNode := callArgument(call, "name", 0)
	name := stringExpressionValue(nameNode)
	if name == "" {
		return Input{}, false
	}
	input := Input{
		Name:  name,
		Kind:  kind,
		File:  path,
		Range: nameNode.RangeTrimmedTrivia(),
	}
	if kind == Argument {
		input.Mode = expressionText(callArgument(call, "mode", 1))
		input.Description = stringExpressionValue(
			callArgument(call, "description", 2),
		)
		input.Default = expressionText(callArgument(call, "default", 3))
	} else {
		input.Shortcut = stringExpressionValue(
			callArgument(call, "shortcut", 1),
		)
		input.Mode = expressionText(callArgument(call, "mode", 2))
		input.Description = stringExpressionValue(
			callArgument(call, "description", 3),
		)
		input.Default = expressionText(callArgument(call, "default", 4))
	}
	return input, true
}

func attributeArgument(
	attribute *phpsyntax.Node,
	name string,
	fallback int,
) *phpsyntax.Node {
	return callArgument(attribute, name, fallback)
}

func callArgument(
	call *phpsyntax.Node,
	name string,
	fallback int,
) *phpsyntax.Node {
	for index, argument := range phpquery.Arguments(call) {
		if strings.EqualFold(phpquery.ArgumentName(argument), name) {
			return phpquery.ArgumentExpression(call, index)
		}
	}
	if fallback < 0 {
		return nil
	}
	argument := phpquery.Argument(call, fallback)
	if argument == nil || phpquery.ArgumentName(argument) != "" {
		return nil
	}
	return phpquery.ArgumentExpression(call, fallback)
}

func attributeHasName(attribute *phpsyntax.Node, expected string) bool {
	return strings.EqualFold(
		shortPHPName(phpquery.AttributeName(attribute)),
		expected,
	)
}

func shortPHPName(name string) string {
	name = strings.TrimPrefix(name, `\`)
	if index := strings.LastIndex(name, `\`); index >= 0 {
		return name[index+1:]
	}
	return name
}

func stringExpressionValue(node *phpsyntax.Node) string {
	if node == nil || node.Kind() != phpsyntax.PhpString {
		return ""
	}
	return phpquery.StringValue(node)
}

func expressionText(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	return strings.TrimSpace(node.Text())
}

func parameterDefault(parameter *phpsyntax.Node) string {
	text := strings.TrimSpace(parameter.Text())
	if equals := strings.LastIndex(text, "="); equals >= 0 {
		return strings.TrimSpace(text[equals+1:])
	}
	return ""
}

func uniqueInputs(inputs []Input) []Input {
	unique := make(map[string]Input, len(inputs))
	for _, input := range inputs {
		unique[input.Name] = input
	}
	result := make([]Input, 0, len(unique))
	for _, input := range unique {
		result = append(result, input)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func parseXMLCommands(file *indexer.ParsedFile) []Command {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	var commands []Command
	for _, service := range xmlquery.Elements(tree.Root, "service") {
		attributes := xmlquery.AttributeValues(service)
		class := attributes["class"]
		if class == "" {
			class = attributes["id"]
		}
		for _, tag := range xmlquery.ChildElements(service, "tag") {
			tagAttributes := xmlquery.AttributeValues(tag)
			if tagAttributes["name"] != "console.command" ||
				tagAttributes["command"] == "" {
				continue
			}
			commandAttribute := xmlquery.Attribute(tag, "command")
			rng := tag.RangeTrimmedTrivia()
			if commandAttribute != nil {
				rng = commandAttribute.RangeTrimmedTrivia()
			}
			commands = append(commands, Command{
				Name:      tagAttributes["command"],
				Canonical: tagAttributes["command"],
				Class:     strings.TrimPrefix(class, `\`),
				File:      file.Path,
				Range:     rng,
			})
		}
	}
	return commands
}
