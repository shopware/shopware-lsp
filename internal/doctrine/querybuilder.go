package doctrine

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

var dqlParameterPattern = regexp.MustCompile(
	`:[A-Za-z_][A-Za-z0-9_]*`,
)

type QueryAlias struct {
	Name   string
	Entity string
}

type QueryContext struct {
	Aliases    map[string]string
	Parameters []string
	Call       *cst.Node
	Literal    *cst.Node
	Entities   []QueryEntityReference
	Standalone bool
}

type QueryCompletionKind uint8

const (
	QueryFieldCompletion QueryCompletionKind = iota
	QueryParameterCompletion
	QueryEntityCompletion
	QueryKeywordCompletion
	QueryFunctionCompletion
)

type QueryCompletion struct {
	Label      string
	Detail     string
	InsertText string
	Kind       QueryCompletionKind
}

type QueryFieldReference struct {
	Alias  string
	Field  string
	Entity string
	Node   *cst.Node
	Range  cst.TextRange
}

type QueryEntityReference struct {
	Entity string
	Node   *cst.Node
	Range  cst.TextRange
}

func (idx *Index) QueryCompletionsAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
	offset uint32,
) []QueryCompletion {
	query, found := idx.queryContextAt(ctx, root, node)
	if !found {
		return nil
	}
	if query.Standalone {
		if completions := idx.standaloneDQLCompletions(
			query,
			offset,
		); len(completions) != 0 {
			return completions
		}
	}
	call := query.Call
	method := ""
	if call != nil {
		method = strings.ToLower(phpquery.CallMethodName(call))
	}
	if method == "setparameter" || method == "setparameters" {
		return parameterCompletions(query.Parameters, false)
	}
	literal := query.Literal
	before := stringBeforeOffset(literal, offset)
	if parameterPrefix(before) != "" ||
		strings.HasSuffix(strings.TrimSpace(before), ":") {
		parameters := append([]string(nil), query.Parameters...)
		parameters = append(parameters, inferredDQLParameters(before)...)
		return parameterCompletions(parameters, false)
	}
	if parameters := inferredDQLParameters(before); len(parameters) != 0 {
		return parameterCompletions(parameters, true)
	}

	aliasPrefix := aliasBeforeCursor(before)
	var aliases []string
	for alias := range query.Aliases {
		if aliasPrefix == "" || strings.EqualFold(alias, aliasPrefix) {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	var result []QueryCompletion
	seen := make(map[string]struct{})
	for _, alias := range aliases {
		entity := query.Aliases[alias]
		fields, err := idx.Fields(entity)
		if err != nil {
			continue
		}
		for _, field := range fields {
			label := alias + "." + field.Name
			key := strings.ToLower(label)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			detail := entity
			if field.Type != "" {
				detail += " · " + field.Type
			} else if field.Relation != "" {
				detail += " · " + field.RelationType + " " + field.Relation
			}
			result = append(result, QueryCompletion{
				Label:  label,
				Detail: detail,
				Kind:   QueryFieldCompletion,
			})
		}
	}
	if aliasPrefix == "" {
		result = append(result, idx.queryFunctionCompletions()...)
	}
	return result
}

func (idx *Index) queryFunctionCompletions() []QueryCompletion {
	functions, err := idx.DQLFunctions()
	if err != nil {
		return nil
	}
	result := make([]QueryCompletion, 0, len(functions))
	for _, function := range functions {
		name := strings.ToUpper(function.Name)
		result = append(result, QueryCompletion{
			Label:      name,
			Detail:     function.Class,
			InsertText: name + "(",
			Kind:       QueryFunctionCompletion,
		})
	}
	return result
}

func (idx *Index) QueryFieldReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
	offset uint32,
) (QueryFieldReference, bool) {
	query, found := idx.queryContextAt(ctx, root, node)
	if !found || query.Literal == nil {
		return QueryFieldReference{}, false
	}
	word, rng := dqlWordAt(query.Literal, offset)
	separator := strings.IndexByte(word, '.')
	if separator <= 0 || separator == len(word)-1 {
		return QueryFieldReference{}, false
	}
	alias := word[:separator]
	fieldName := word[separator+1:]
	entity := query.Aliases[alias]
	if entity == "" {
		for candidate, target := range query.Aliases {
			if strings.EqualFold(candidate, alias) {
				alias = candidate
				entity = target
				break
			}
		}
	}
	if entity == "" {
		return QueryFieldReference{}, false
	}
	rng.Start += uint32(separator + 1)
	return QueryFieldReference{
		Alias:  alias,
		Field:  fieldName,
		Entity: entity,
		Node:   query.Literal,
		Range:  rng,
	}, true
}

func (idx *Index) QueryEntityReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
	offset uint32,
) (QueryEntityReference, bool) {
	query, found := idx.queryContextAt(ctx, root, node)
	if !found || !query.Standalone {
		return QueryEntityReference{}, false
	}
	for _, reference := range query.Entities {
		if reference.Range.Contains(offset) ||
			offset == reference.Range.End && reference.Range.Len() != 0 {
			return reference, true
		}
	}
	return QueryEntityReference{}, false
}

func (idx *Index) queryContextAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (QueryContext, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return QueryContext{}, false
	}
	if query, found := idx.standaloneDQLContextAt(
		ctx,
		root,
		literal,
		true,
	); found {
		return query, true
	}
	call := phpquery.CallAt(literal)
	if call == nil || !isDoctrineQueryCall(call, literal) {
		return QueryContext{}, false
	}
	function := phpquery.FunctionLikeAt(call)
	scope := root
	if function != nil {
		scope = function
	}
	anchor := queryBuilderChainAnchor(call)
	queryVariable := ""
	if anchor == nil {
		queryVariable = queryBuilderVariable(call)
	}
	if queryVariable == "" && anchor == nil {
		return QueryContext{}, false
	}

	query := QueryContext{
		Aliases: make(map[string]string),
		Call:    call,
		Literal: literal,
	}
	calls := phpquery.Calls(scope)
	sort.SliceStable(calls, func(left, right int) bool {
		if calls[left].Range().Start == calls[right].Range().Start {
			return calls[left].Range().End < calls[right].Range().End
		}
		return calls[left].Range().Start < calls[right].Range().Start
	})

	if anchor == nil {
		anchor = queryBuilderAnchorForVariable(calls, queryVariable)
	}
	rootEntity := ""
	if anchor != nil {
		rootEntity = idx.RepositoryEntityForCall(ctx, root, anchor)
		alias := phpquery.StringValue(phpquery.StringArgument(anchor, 0))
		if alias != "" && rootEntity != "" {
			query.Aliases[alias] = rootEntity
		}
	}
	if rootEntity == "" {
		rootEntity = idx.repositoryEntityForQueryBuilder(
			ctx,
			root,
			calls,
			queryVariable,
		)
	}
	for _, candidate := range calls {
		method := strings.ToLower(phpquery.CallMethodName(candidate))
		if phpquery.AssignedVariable(candidate) == queryVariable &&
			method == "createquerybuilder" {
			alias := phpquery.StringValue(
				phpquery.StringArgument(candidate, 0),
			)
			if alias != "" && rootEntity != "" {
				query.Aliases[alias] = rootEntity
			}
		}
		if !queryBuilderCallBelongs(
			candidate,
			queryVariable,
			anchor,
		) {
			continue
		}
		switch method {
		case "from":
			entity := idx.queryClassArgument(candidate, 0, root)
			alias := phpquery.StringValue(
				phpquery.StringArgument(candidate, 1),
			)
			if entity != "" && alias != "" {
				query.Aliases[alias] = entity
			}
		case "join", "innerjoin", "leftjoin", "rightjoin":
			idx.addQueryJoin(query.Aliases, candidate, root)
		}
		for _, stringNode := range phpquery.Nodes(
			candidate,
			phpsyntax.PhpString,
		) {
			for _, parameter := range dqlParameterPattern.FindAllString(
				phpquery.StringValue(stringNode),
				-1,
			) {
				query.Parameters = appendUniqueString(
					query.Parameters,
					strings.TrimPrefix(parameter, ":"),
				)
			}
		}
	}
	if len(query.Aliases) == 0 {
		return QueryContext{}, false
	}
	return query, true
}

func isDoctrineQueryCall(
	call,
	literal *phpsyntax.Node,
) bool {
	method := strings.ToLower(phpquery.CallMethodName(call))
	argument := phpquery.ArgumentIndex(call, literal)
	switch method {
	case "select", "addselect", "where", "andwhere", "orwhere",
		"groupby", "addgroupby", "having", "andhaving", "orhaving",
		"set",
		"andx", "orx", "eq", "neq", "lt", "lte", "gt", "gte",
		"avg", "max", "min", "count", "diff", "sum", "quot",
		"in", "notin", "like", "notlike", "concat", "between":
		return argument >= 0
	case "orderby", "addorderby":
		return argument == 0
	case "join", "innerjoin", "leftjoin", "rightjoin":
		return argument == 0 || argument == 3 || argument == 4
	case "createquerybuilder":
		return argument == 1
	case "from":
		return argument == 2
	case "setparameter":
		return argument == 0
	case "setparameters":
		return argument == 0
	default:
		return false
	}
}

func queryBuilderVariable(call *phpsyntax.Node) string {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return ""
	}
	if receiver.Kind() == phpsyntax.PhpVariable {
		name := phpquery.VariableName(receiver)
		if name != "" {
			return "$" + strings.TrimPrefix(name, "$")
		}
	}
	for _, variable := range phpquery.Nodes(
		receiver,
		phpsyntax.PhpVariable,
	) {
		name := phpquery.VariableName(variable)
		if name != "" {
			return "$" + strings.TrimPrefix(name, "$")
		}
	}
	return ""
}

func callUsesVariable(call *phpsyntax.Node, variable string) bool {
	if variable == "" {
		return false
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	if receiver.Kind() == phpsyntax.PhpVariable {
		return "$"+strings.TrimPrefix(
			phpquery.VariableName(receiver),
			"$",
		) == variable
	}
	for _, candidate := range phpquery.Nodes(
		receiver,
		phpsyntax.PhpVariable,
	) {
		if "$"+strings.TrimPrefix(
			phpquery.VariableName(candidate),
			"$",
		) == variable {
			return true
		}
	}
	return false
}

func queryBuilderChainAnchor(call *phpsyntax.Node) *phpsyntax.Node {
	if call == nil {
		return nil
	}
	if strings.EqualFold(
		phpquery.CallMethodName(call),
		"createQueryBuilder",
	) {
		return call
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return nil
	}
	if strings.EqualFold(
		phpquery.CallMethodName(receiver),
		"createQueryBuilder",
	) {
		return receiver
	}
	for _, candidate := range phpquery.Calls(receiver) {
		if strings.EqualFold(
			phpquery.CallMethodName(candidate),
			"createQueryBuilder",
		) {
			return candidate
		}
	}
	return nil
}

func queryBuilderAnchorForVariable(
	calls []*phpsyntax.Node,
	variable string,
) *phpsyntax.Node {
	if variable == "" {
		return nil
	}
	for _, candidate := range calls {
		if phpquery.AssignedVariable(candidate) != variable {
			continue
		}
		if anchor := queryBuilderChainAnchor(candidate); anchor != nil {
			return anchor
		}
	}
	return nil
}

func queryBuilderCallBelongs(
	call *phpsyntax.Node,
	variable string,
	anchor *phpsyntax.Node,
) bool {
	if callUsesVariable(call, variable) {
		return true
	}
	if variable != "" &&
		phpquery.AssignedVariable(call) == variable &&
		queryBuilderChainAnchor(call) != nil {
		return true
	}
	if anchor == nil {
		return false
	}
	candidateAnchor := queryBuilderChainAnchor(call)
	return candidateAnchor != nil &&
		candidateAnchor.Range() == anchor.Range()
}

func (idx *Index) repositoryEntityForQueryBuilder(
	ctx context.Context,
	root *phpsyntax.Node,
	calls []*phpsyntax.Node,
	variable string,
) string {
	phpContext := php.GetPHPContext(ctx)
	if phpContext != nil && phpContext.InsideClass != nil {
		if model, found, err := idx.ModelForRepository(
			phpContext.InsideClass.FullyQualified,
		); err == nil && found {
			return model.Class
		}
	}
	for _, call := range calls {
		if phpquery.AssignedVariable(call) != variable ||
			!strings.EqualFold(
				phpquery.CallMethodName(call),
				"createQueryBuilder",
			) {
			continue
		}
		if entity := serviceRepositoryEntity(root, call); entity != "" {
			return idx.canonicalModelName(entity)
		}
	}
	return ""
}

func serviceRepositoryEntity(
	root,
	call *phpsyntax.Node,
) string {
	class := phpquery.ClassAt(call)
	if class == nil {
		return ""
	}
	resolver := php.NewNameResolver(root)
	for _, method := range phpquery.Methods(class) {
		if !strings.EqualFold(phpquery.MethodName(method), "__construct") {
			continue
		}
		for _, constructor := range phpquery.Calls(method) {
			if !strings.EqualFold(
				phpquery.CallMethodName(constructor),
				"__construct",
			) {
				continue
			}
			receiver := phpquery.CallReceiver(constructor)
			if receiver == nil ||
				!strings.Contains(strings.ToLower(receiver.Text()), "parent") {
				continue
			}
			if entity := classExpression(
				phpquery.ArgumentExpression(constructor, 1),
				resolver,
			); entity != "" {
				return entity
			}
		}
	}
	return ""
}

func (idx *Index) queryClassArgument(
	call *phpsyntax.Node,
	position int,
	root *phpsyntax.Node,
) string {
	return idx.canonicalModelName(classExpression(
		phpquery.ArgumentExpression(call, position),
		php.NewNameResolver(root),
	))
}

func (idx *Index) addQueryJoin(
	aliases map[string]string,
	call,
	root *phpsyntax.Node,
) {
	alias := phpquery.StringValue(phpquery.StringArgument(call, 1))
	if alias == "" {
		return
	}
	expression := phpquery.ArgumentExpression(call, 0)
	if className := phpquery.ClassConstantName(expression); className != "" {
		aliases[alias] = idx.canonicalModelName(
			php.NewNameResolver(root).Resolve(className),
		)
		return
	}
	value := phpquery.StringValue(phpquery.StringAt(expression))
	if strings.Contains(value, ":") {
		aliases[alias] = idx.canonicalModelName(value)
		return
	}
	if strings.Contains(value, `\`) {
		aliases[alias] = idx.canonicalModelName(value)
		return
	}
	separator := strings.IndexByte(value, '.')
	if separator <= 0 || separator == len(value)-1 {
		return
	}
	parentEntity := aliases[value[:separator]]
	if parentEntity == "" {
		return
	}
	fields, err := idx.Fields(parentEntity)
	if err != nil {
		return
	}
	fieldName := value[separator+1:]
	for _, field := range fields {
		if strings.EqualFold(field.Name, fieldName) &&
			field.Relation != "" {
			aliases[alias] = field.Relation
			return
		}
	}
}

func parameterCompletions(
	parameters []string,
	includeColon bool,
) []QueryCompletion {
	parameters = uniqueSortedStrings(parameters)
	result := make([]QueryCompletion, 0, len(parameters))
	for _, parameter := range parameters {
		label := parameter
		if includeColon {
			label = ":" + parameter
		}
		result = append(result, QueryCompletion{
			Label:      label,
			Detail:     "Doctrine query parameter",
			InsertText: label,
			Kind:       QueryParameterCompletion,
		})
	}
	return result
}

func inferredDQLParameters(before string) []string {
	if before == "" ||
		(before[len(before)-1] != ' ' &&
			before[len(before)-1] != '\t' &&
			before[len(before)-1] != '\r' &&
			before[len(before)-1] != '\n') {
		return nil
	}
	before = strings.TrimSpace(before)
	operators := []string{"=", ">=", "<=", "<>", "!=", ">", "<"}
	for _, operator := range operators {
		position := strings.LastIndex(before, operator)
		if position < 0 {
			continue
		}
		left := strings.TrimSpace(before[:position])
		word := trailingDQLWord(left)
		separator := strings.IndexByte(word, '.')
		if separator <= 0 || separator == len(word)-1 {
			return nil
		}
		alias := word[:separator]
		field := word[separator+1:]
		return []string{
			field,
			alias + "_" + field,
			alias + strings.ToUpper(field[:1]) + field[1:],
		}
	}
	return nil
}

func parameterPrefix(before string) string {
	for position := len(before) - 1; position >= 0; position-- {
		character := before[position]
		if character == ':' {
			return before[position:]
		}
		if !isDQLWordByte(character) {
			break
		}
	}
	return ""
}

func aliasBeforeCursor(before string) string {
	word := trailingDQLWord(before)
	if separator := strings.IndexByte(word, '.'); separator >= 0 {
		return word[:separator]
	}
	return ""
}

func trailingDQLWord(value string) string {
	end := len(value)
	start := end
	for start > 0 && isDQLWordByte(value[start-1]) {
		start--
	}
	return value[start:end]
}

func dqlWordAt(
	literal *phpsyntax.Node,
	offset uint32,
) (string, cst.TextRange) {
	rng := ReferenceRange(Reference{
		Kind: StringReference,
		Node: literal,
	})
	text := phpquery.StringValue(literal)
	if offset < rng.Start {
		offset = rng.Start
	}
	position := int(offset - rng.Start)
	if position > len(text) {
		position = len(text)
	}
	start := position
	for start > 0 && isDQLWordByte(text[start-1]) {
		start--
	}
	end := position
	for end < len(text) && isDQLWordByte(text[end]) {
		end++
	}
	return text[start:end], cst.TextRange{
		Start: rng.Start + uint32(start),
		End:   rng.Start + uint32(end),
	}
}

func stringBeforeOffset(
	literal *phpsyntax.Node,
	offset uint32,
) string {
	rng := ReferenceRange(Reference{
		Kind: StringReference,
		Node: literal,
	})
	value := phpquery.StringValue(literal)
	if offset <= rng.Start {
		return ""
	}
	position := int(offset - rng.Start)
	if position > len(value) {
		position = len(value)
	}
	return value[:position]
}

func isDQLWordByte(value byte) bool {
	return value == '_' || value == '.' ||
		value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z'
}

func appendUniqueString(values []string, value string) []string {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return values
		}
	}
	return append(values, value)
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]string)
	for _, value := range values {
		value = strings.TrimPrefix(strings.TrimSpace(value), ":")
		if value != "" {
			seen[strings.ToLower(value)] = value
		}
	}
	result := make([]string, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
