package security

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

var securityExpressionPattern = regexp.MustCompile(
	`(?i)(?:is_granted|has_role)\s*\(\s*(['"])([^'"]*)`,
)

var phpDocIsGrantedPattern = regexp.MustCompile(
	`(?is)@IsGranted\s*\(\s*(['"])([^'"]*)`,
)

var phpDocSecurityPattern = regexp.MustCompile(
	`(?is)@Security\s*\(\s*(?:"([^"]*)"|'([^']*)')`,
)

type voterValue struct {
	name  string
	rng   cst.TextRange
	class string
}

func occurrencesInFile(file *indexer.ParsedFile) []Occurrence {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	switch file.Extension() {
	case ".php":
		return parsePHP(file.Path, tree.Root, file.Source)
	case ".twig":
		return parseTwig(file.Path, tree.Root)
	case ".yaml", ".yml":
		return parseYAML(file.Path, tree.Root)
	default:
		return nil
	}
}

// OccurrencesInDocument extracts declarations and references from an already
// parsed document. LSP providers use it to overlay unsaved editor content on
// top of the persisted workspace index.
func OccurrencesInDocument(
	path string,
	root *cst.Node,
	source string,
) []Occurrence {
	if root == nil {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return parsePHP(path, root, source)
	case ".twig":
		return parseTwig(path, root)
	case ".yaml", ".yml":
		return parseYAML(path, root)
	default:
		return nil
	}
}

func parsePHP(
	path string,
	root *phpsyntax.Node,
	source string,
) []Occurrence {
	result := parsePHPVoters(path, root)
	for _, stringNode := range phpquery.Nodes(root, phpsyntax.PhpString) {
		if reference, ok := phpCSTReferenceAt(
			context.Background(),
			root,
			stringNode,
			stringNode.Range().Start,
		); ok {
			result = append(result, occurrenceFromReference(path, reference))
		}
		result = append(
			result,
			expressionOccurrences(path, stringNode)...,
		)
	}
	result = append(result, phpDocOccurrences(path, source)...)
	return uniqueOccurrences(result)
}

func parsePHPVoters(
	path string,
	root *phpsyntax.Node,
) []Occurrence {
	nameResolver := php.NewNameResolver(root)
	var result []Occurrence
	for _, class := range phpquery.Classes(root) {
		if !isVoterClass(class, nameResolver) {
			continue
		}
		className := strings.TrimPrefix(
			nameResolver.Resolve(phpquery.ClassName(class)),
			`\`,
		)
		constants := voterConstants(class, className)
		properties := voterProperties(class, className)
		for _, method := range phpquery.Methods(class) {
			methodName := strings.ToLower(phpquery.MethodName(method))
			var attributeVariables []string
			switch methodName {
			case "supports", "voteonattribute":
				parameters := phpquery.Parameters(method)
				if len(parameters) != 0 {
					attributeVariables = append(
						attributeVariables,
						phpquery.ParameterName(parameters[0]),
					)
				}
			case "vote":
				parameters := phpquery.Parameters(method)
				if len(parameters) >= 3 {
					attributeVariables = append(
						attributeVariables,
						phpquery.ParameterName(parameters[2]),
					)
					attributeVariables = append(
						attributeVariables,
						foreachValueVariables(
							method,
							phpquery.ParameterName(parameters[2]),
						)...,
					)
				}
			default:
				continue
			}
			values := supportedVoterValues(
				method,
				attributeVariables,
				constants,
				properties,
			)
			for _, value := range values {
				result = append(result, Occurrence{
					Name:   value.name,
					Role:   DeclarationOccurrence,
					Origin: OriginVoter,
					File:   path,
					Range:  value.rng,
					Class:  className,
				})
			}
		}
	}
	return result
}

func isVoterClass(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	var names []string
	names = append(names, phpquery.ClassExtends(class)...)
	names = append(names, phpquery.ClassImplements(class)...)
	for _, name := range names {
		resolved := strings.TrimPrefix(resolver.Resolve(name), `\`)
		if strings.EqualFold(
			resolved,
			"Symfony\\Component\\Security\\Core\\Authorization\\Voter\\Voter",
		) || strings.EqualFold(
			resolved,
			"Symfony\\Component\\Security\\Core\\Authorization\\Voter\\VoterInterface",
		) || strings.EqualFold(name, "Voter") ||
			strings.EqualFold(name, "VoterInterface") {
			return true
		}
	}
	return false
}

func voterConstants(
	class *phpsyntax.Node,
	className string,
) map[string][]voterValue {
	result := make(map[string][]voterValue)
	body := phpquery.ClassBody(class)
	if body == nil {
		return result
	}
	for child := range body.ChildNodes() {
		if child.Kind() != phpsyntax.PhpClassConstDeclaration {
			continue
		}
		name := phpquery.NameValue(
			phpquery.DirectChild(child, phpsyntax.PhpName),
		)
		if name == "" {
			continue
		}
		for _, literal := range phpquery.Nodes(child, phpsyntax.PhpString) {
			value := phpquery.StringValue(literal)
			if value == "" {
				continue
			}
			result[strings.ToLower(name)] = append(
				result[strings.ToLower(name)],
				voterValue{
					name:  value,
					rng:   stringValueRange(literal),
					class: className,
				},
			)
		}
	}
	return result
}

func voterProperties(
	class *phpsyntax.Node,
	className string,
) map[string][]voterValue {
	result := make(map[string][]voterValue)
	for _, property := range phpquery.Properties(class) {
		for _, variable := range phpquery.PropertyVariables(property) {
			name := strings.ToLower(phpquery.VariableName(variable))
			if name == "" {
				continue
			}
			for _, literal := range phpquery.Nodes(
				property,
				phpsyntax.PhpString,
			) {
				value := phpquery.StringValue(literal)
				if value == "" {
					continue
				}
				result[name] = append(result[name], voterValue{
					name:  value,
					rng:   stringValueRange(literal),
					class: className,
				})
			}
		}
	}
	return result
}

func supportedVoterValues(
	method *phpsyntax.Node,
	variables []string,
	constants,
	properties map[string][]voterValue,
) []voterValue {
	var result []voterValue
	for _, binary := range phpquery.Nodes(
		method,
		phpsyntax.PhpBinaryExpression,
	) {
		if !containsAnyVariable(binary, variables) {
			continue
		}
		result = append(
			result,
			valuesInExpression(binary, constants, properties)...,
		)
	}
	for _, call := range phpquery.Calls(method, "in_array") {
		if !strings.EqualFold(phpquery.CallMethodName(call), "in_array") &&
			!strings.EqualFold(phpquery.CallName(call), "in_array") {
			continue
		}
		if !containsAnyVariable(call, variables) {
			continue
		}
		result = append(
			result,
			valuesInExpression(call, constants, properties)...,
		)
	}
	for _, switchNode := range phpquery.Nodes(
		method,
		phpsyntax.PhpSwitchStatement,
	) {
		condition := phpquery.DirectChild(
			switchNode,
			phpsyntax.PhpParenthesized,
		)
		if !containsAnyVariable(condition, variables) {
			continue
		}
		for _, clause := range phpquery.Nodes(
			switchNode,
			phpsyntax.PhpCaseClause,
		) {
			condition := firstDirectExpression(clause)
			if condition == nil {
				continue
			}
			result = append(
				result,
				valuesInExpression(condition, constants, properties)...,
			)
		}
	}
	for _, matchNode := range phpquery.Nodes(
		method,
		phpsyntax.PhpMatchExpression,
	) {
		if !containsAnyVariable(matchNode, variables) {
			continue
		}
		for _, arm := range phpquery.Nodes(
			matchNode,
			phpsyntax.PhpMatchArm,
		) {
			result = append(
				result,
				valuesInExpression(arm, constants, properties)...,
			)
		}
	}
	return uniqueVoterValues(result)
}

func firstDirectExpression(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		return child
	}
	return nil
}

func valuesInExpression(
	node *phpsyntax.Node,
	constants,
	properties map[string][]voterValue,
) []voterValue {
	if node == nil {
		return nil
	}
	var result []voterValue
	for _, literal := range phpquery.Nodes(node, phpsyntax.PhpString) {
		if value := phpquery.StringValue(literal); value != "" {
			result = append(result, voterValue{
				name: value,
				rng:  stringValueRange(literal),
			})
		}
	}
	for _, access := range phpquery.Nodes(
		node,
		phpsyntax.PhpScopedAccess,
		phpsyntax.PhpMemberAccess,
	) {
		text := compactPHPExpression(access.Text())
		if separator := strings.LastIndex(text, "::"); separator >= 0 {
			name := strings.ToLower(text[separator+2:])
			result = append(result, constants[name]...)
		}
		if separator := strings.LastIndex(text, "->"); separator >= 0 {
			name := strings.ToLower(text[separator+2:])
			result = append(result, properties[name]...)
		}
	}
	return result
}

func containsAnyVariable(
	node *phpsyntax.Node,
	variables []string,
) bool {
	if node == nil {
		return false
	}
	for _, variable := range phpquery.Nodes(node, phpsyntax.PhpVariable) {
		name := "$" + phpquery.VariableName(variable)
		for _, candidate := range variables {
			if candidate != "" && strings.EqualFold(name, candidate) {
				return true
			}
		}
	}
	return false
}

func foreachValueVariables(
	method *phpsyntax.Node,
	collection string,
) []string {
	var result []string
	for _, statement := range phpquery.Nodes(
		method,
		phpsyntax.PhpForeachStatement,
	) {
		var variables []string
		for _, variable := range phpquery.Nodes(
			statement,
			phpsyntax.PhpVariable,
		) {
			variables = append(
				variables,
				"$"+phpquery.VariableName(variable),
			)
		}
		if len(variables) >= 2 &&
			strings.EqualFold(variables[0], collection) {
			result = append(result, variables[1])
		}
	}
	return result
}

func parseTwig(
	path string,
	root *twigsyntax.Node,
) []Occurrence {
	var result []Occurrence
	for literal := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigLiteralString,
	) {
		reference, ok := twigReferenceAt(literal)
		if !ok {
			continue
		}
		result = append(
			result,
			occurrenceFromReference(path, reference),
		)
	}
	return uniqueOccurrences(result)
}

func parseYAML(
	path string,
	root *yamlsyntax.Node,
) []Occurrence {
	var result []Occurrence
	security := yamlquery.Property(yamlquery.RootValue(root), "security")
	if !yamlquery.IsMapping(security) {
		return nil
	}
	hierarchy := yamlquery.Property(security, "role_hierarchy")
	if yamlquery.IsMapping(hierarchy) {
		for _, pair := range yamlquery.Pairs(hierarchy) {
			result = appendYAMLSecurityValue(
				result,
				path,
				yamlquery.PairKey(pair),
				OriginRoleHierarchy,
			)
			result = appendYAMLSecurityValue(
				result,
				path,
				yamlquery.PairValue(pair),
				OriginRoleHierarchy,
			)
		}
	}
	accessControl := yamlquery.Property(security, "access_control")
	if yamlquery.IsSequence(accessControl) {
		for _, item := range yamlquery.Items(accessControl) {
			mapping := yamlquery.ItemValue(item)
			if !yamlquery.IsMapping(mapping) {
				continue
			}
			result = appendYAMLSecurityValue(
				result,
				path,
				yamlquery.Property(mapping, "roles"),
				OriginAccessControl,
			)
		}
	}
	return uniqueOccurrences(result)
}

func appendYAMLSecurityValue(
	result []Occurrence,
	path string,
	node *yamlsyntax.Node,
	origin Origin,
) []Occurrence {
	if node == nil {
		return result
	}
	if node.Kind() == yamlsyntax.YamlScalar {
		name := yamlquery.ScalarValue(node)
		if name != "" {
			result = append(result, Occurrence{
				Name:   name,
				Role:   DeclarationOccurrence,
				Origin: origin,
				File:   path,
				Range:  yamlValueRange(node),
			})
		}
		return result
	}
	if yamlquery.IsSequence(node) {
		for _, item := range yamlquery.Items(node) {
			result = appendYAMLSecurityValue(
				result,
				path,
				yamlquery.ItemValue(item),
				origin,
			)
		}
	}
	return result
}

func expressionOccurrences(
	path string,
	stringNode *phpsyntax.Node,
) []Occurrence {
	attribute := phpquery.AttributeAt(stringNode)
	if attribute == nil {
		return nil
	}
	resolver := php.NewNameResolver(rootOf(stringNode))
	resolved := strings.TrimPrefix(
		resolver.Resolve(phpquery.AttributeName(attribute)),
		`\`,
	)
	if !isSecurityExpressionAttribute(resolved) {
		return nil
	}
	value := phpquery.StringValue(stringNode)
	base := stringValueRange(stringNode).Start
	matches := securityExpressionPattern.FindAllStringSubmatchIndex(value, -1)
	result := make([]Occurrence, 0, len(matches))
	for _, match := range matches {
		if len(match) < 6 || match[4] < 0 || match[5] < match[4] {
			continue
		}
		name := value[match[4]:match[5]]
		result = append(result, Occurrence{
			Name:   name,
			Role:   ReferenceOccurrence,
			Origin: OriginPHPExpression,
			File:   path,
			Range: cst.TextRange{
				Start: base + uint32(match[4]),
				End:   base + uint32(match[5]),
			},
		})
	}
	return result
}

func phpDocOccurrences(path, source string) []Occurrence {
	var result []Occurrence
	for _, match := range phpDocIsGrantedPattern.FindAllStringSubmatchIndex(
		source,
		-1,
	) {
		if len(match) < 6 {
			continue
		}
		result = append(result, Occurrence{
			Name:   source[match[4]:match[5]],
			Role:   ReferenceOccurrence,
			Origin: OriginPHPDoc,
			File:   path,
			Range: cst.TextRange{
				Start: uint32(match[4]),
				End:   uint32(match[5]),
			},
		})
	}
	for _, outer := range phpDocSecurityPattern.FindAllStringSubmatchIndex(
		source,
		-1,
	) {
		if len(outer) < 6 {
			continue
		}
		valueStart, valueEnd := outer[2], outer[3]
		if valueStart < 0 {
			valueStart, valueEnd = outer[4], outer[5]
		}
		if valueStart < 0 {
			continue
		}
		value := source[valueStart:valueEnd]
		for _, inner := range securityExpressionPattern.FindAllStringSubmatchIndex(
			value,
			-1,
		) {
			if len(inner) < 6 {
				continue
			}
			start := valueStart + inner[4]
			end := valueStart + inner[5]
			result = append(result, Occurrence{
				Name:   source[start:end],
				Role:   ReferenceOccurrence,
				Origin: OriginPHPDoc,
				File:   path,
				Range: cst.TextRange{
					Start: uint32(start),
					End:   uint32(end),
				},
			})
		}
	}
	return result
}

func stringValueRange(node *phpsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 {
		quote := text[0]
		if (quote == '\'' || quote == '"' || quote == '`') &&
			text[len(text)-1] == quote &&
			rng.End > rng.Start+1 {
			rng.Start++
			rng.End--
		}
	}
	return rng
}

func yamlValueRange(node *yamlsyntax.Node) cst.TextRange {
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}

func compactPHPExpression(value string) string {
	return strings.Join(strings.Fields(value), "")
}

func rootOf(node *phpsyntax.Node) *phpsyntax.Node {
	for node != nil && node.Parent() != nil {
		node = node.Parent()
	}
	return node
}

func occurrenceFromReference(path string, reference Reference) Occurrence {
	return Occurrence{
		Name:   reference.Name,
		Role:   ReferenceOccurrence,
		Origin: reference.Origin,
		File:   path,
		Range:  reference.Range,
		Class:  reference.Class,
	}
}

func uniqueVoterValues(values []voterValue) []voterValue {
	seen := make(map[string]struct{}, len(values))
	result := make([]voterValue, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value.name) + ":" +
			value.rng.String()
		if value.name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueOccurrences(values []Occurrence) []Occurrence {
	seen := make(map[string]struct{}, len(values))
	result := make([]Occurrence, 0, len(values))
	for _, value := range values {
		if value.Name == "" && value.Range.Len() != 0 {
			continue
		}
		key := strings.ToLower(value.Name) + ":" +
			value.File + ":" + value.Range.String() + ":" +
			string(rune(value.Role)) + ":" + string(rune(value.Origin))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
