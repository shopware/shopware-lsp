package doctrine

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type dqlTokenKind uint8

const (
	dqlWordToken dqlTokenKind = iota
	dqlDotToken
	dqlColonToken
	dqlCommaToken
	dqlOtherToken
)

type dqlToken struct {
	kind       dqlTokenKind
	text       string
	start, end int
}

type DQLReferenceRole uint8

const (
	DQLEntityReference DQLReferenceRole = iota
	DQLFieldReference
)

type DQLReference struct {
	Role   DQLReferenceRole
	Entity string
	Field  string
	File   string
	Range  cst.TextRange
}

func (idx *Index) standaloneDQLContextAt(
	ctx context.Context,
	root,
	literal *phpsyntax.Node,
	strict bool,
) (QueryContext, bool) {
	value, base, static := dqlLiteralValue(literal)
	if !static || !isStandaloneDQLLiteral(ctx, root, literal, strict) {
		return QueryContext{}, false
	}
	tokens := lexDQL(value)
	// Empty strings are valid editing states inside createQuery(), setDQL(),
	// and a $dql assignment. Keep the context alive so keyword completion can
	// bootstrap the query.
	if len(tokens) != 0 && !looksLikeDQL(tokens) {
		return QueryContext{}, false
	}
	query := QueryContext{
		Aliases:    make(map[string]string),
		Call:       phpquery.CallAt(literal),
		Literal:    literal,
		Standalone: true,
	}
	query.Entities = parseDQLRootAliases(tokens, base, literal, query.Aliases)
	for position := range query.Entities {
		query.Entities[position].Entity = idx.canonicalModelName(
			query.Entities[position].Entity,
		)
	}
	for alias, entity := range query.Aliases {
		query.Aliases[alias] = idx.canonicalModelName(entity)
	}
	resolveDQLJoinAliases(idx, tokens, query.Aliases)
	for _, token := range tokens {
		if token.kind != dqlColonToken {
			continue
		}
		absolute := base + uint32(token.start)
		namespaceSeparator := false
		for _, entity := range query.Entities {
			if absolute >= entity.Range.Start &&
				absolute < entity.Range.End {
				namespaceSeparator = true
				break
			}
		}
		if namespaceSeparator {
			continue
		}
		position := dqlTokenIndex(tokens, token.start)
		if position+1 >= len(tokens) ||
			tokens[position+1].kind != dqlWordToken {
			continue
		}
		query.Parameters = appendUniqueString(
			query.Parameters,
			tokens[position+1].text,
		)
	}
	return query, true
}

func DQLReferencesInDocument(
	idx *Index,
	ctx context.Context,
	root *phpsyntax.Node,
	path string,
) []DQLReference {
	return dqlReferencesInDocument(idx, ctx, root, path, false)
}

func ValidatedDQLReferencesInDocument(
	idx *Index,
	ctx context.Context,
	root *phpsyntax.Node,
	path string,
) []DQLReference {
	return dqlReferencesInDocument(idx, ctx, root, path, true)
}

func dqlReferencesInDocument(
	idx *Index,
	ctx context.Context,
	root *phpsyntax.Node,
	path string,
	strict bool,
) []DQLReference {
	if idx == nil || root == nil {
		return nil
	}
	var result []DQLReference
	seen := make(map[string]struct{})
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		query, found := idx.standaloneDQLContextAt(
			ctx,
			root,
			literal,
			strict,
		)
		if !found {
			continue
		}
		for _, entity := range query.Entities {
			addDQLReference(&result, seen, DQLReference{
				Role:   DQLEntityReference,
				Entity: entity.Entity,
				File:   path,
				Range:  entity.Range,
			})
		}
		value, base, _ := dqlLiteralValue(literal)
		tokens := lexDQL(value)
		for position := 0; position+2 < len(tokens); position++ {
			left, dot, right := tokens[position], tokens[position+1], tokens[position+2]
			if left.kind != dqlWordToken ||
				dot.kind != dqlDotToken ||
				right.kind != dqlWordToken {
				continue
			}
			entity := dqlAliasEntity(query.Aliases, left.text)
			if entity == "" {
				continue
			}
			addDQLReference(&result, seen, DQLReference{
				Role:   DQLFieldReference,
				Entity: entity,
				Field:  right.text,
				File:   path,
				Range: cst.TextRange{
					Start: base + uint32(right.start),
					End:   base + uint32(right.end),
				},
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

func (idx *Index) DQLReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
	offset uint32,
	path string,
) (DQLReference, bool) {
	if node == nil {
		return DQLReference{}, false
	}
	for _, reference := range ValidatedDQLReferencesInDocument(
		idx,
		ctx,
		root,
		path,
	) {
		if reference.Range.Contains(offset) ||
			(offset == reference.Range.End && reference.Range.Len() != 0) {
			return reference, true
		}
	}
	return DQLReference{}, false
}

func addDQLReference(
	result *[]DQLReference,
	seen map[string]struct{},
	reference DQLReference,
) {
	key := reference.File + "\x00" + reference.Range.String() + "\x00" +
		string(rune(reference.Role))
	if _, duplicate := seen[key]; duplicate {
		return
	}
	seen[key] = struct{}{}
	*result = append(*result, reference)
}

func DQLReferenceKey(reference DQLReference) string {
	entity := strings.ToLower(normalizeDQLClass(reference.Entity))
	if entity == "" {
		return ""
	}
	switch reference.Role {
	case DQLEntityReference:
		return "entity:" + entity
	case DQLFieldReference:
		if reference.Field == "" {
			return ""
		}
		return "field:" + entity + "::" + strings.ToLower(reference.Field)
	default:
		return ""
	}
}

func (idx *Index) standaloneDQLCompletions(
	query QueryContext,
	offset uint32,
) []QueryCompletion {
	before := stringBeforeOffset(query.Literal, offset)
	if dqlEntityPosition(before) {
		models, err := idx.Models()
		if err != nil {
			return nil
		}
		var result []QueryCompletion
		for _, model := range models {
			if model.Kind == MappedSuperclassModel ||
				model.Kind == EmbeddableModel {
				continue
			}
			result = append(result, QueryCompletion{
				Label:      model.Class,
				Detail:     "Doctrine " + model.Kind.String(),
				InsertText: dqlPHPStringInsertText(query.Literal, model.Class),
				Kind:       QueryEntityCompletion,
			})
		}
		aliases, aliasErr := idx.ModelAliases()
		if aliasErr == nil {
			for _, alias := range aliases {
				result = append(result, QueryCompletion{
					Label: alias.Name,
					Detail: alias.Class +
						" · Doctrine namespace shortcut",
					InsertText: dqlPHPStringInsertText(
						query.Literal,
						alias.Name,
					),
					Kind: QueryEntityCompletion,
				})
			}
		}
		return result
	}
	if strings.TrimSpace(before) == "" {
		return []QueryCompletion{
			{Label: "SELECT", Detail: "DQL query", Kind: QueryKeywordCompletion},
			{Label: "UPDATE", Detail: "DQL query", Kind: QueryKeywordCompletion},
			{Label: "DELETE", Detail: "DQL query", Kind: QueryKeywordCompletion},
		}
	}
	return nil
}

func dqlPHPStringInsertText(literal *phpsyntax.Node, value string) string {
	if literal == nil {
		return value
	}
	text := strings.TrimSpace(literal.Text())
	if len(text) != 0 && text[0] == '"' {
		return strings.ReplaceAll(value, `\`, `\\`)
	}
	return value
}

func dqlEntityPosition(before string) bool {
	words := dqlSignificantWords(before)
	if len(words) == 0 {
		return false
	}
	last := strings.ToUpper(words[len(words)-1])
	if last == "FROM" || last == "UPDATE" {
		return true
	}
	return len(words) >= 2 &&
		strings.ToUpper(words[len(words)-2]) == "DELETE" &&
		last == "FROM"
}

func dqlSignificantWords(value string) []string {
	tokens := lexDQL(value)
	var result []string
	for _, token := range tokens {
		if token.kind == dqlWordToken {
			result = append(result, token.text)
		}
	}
	if len(value) != 0 && isDQLIdentifierByte(value[len(value)-1]) &&
		len(result) != 0 {
		// The current incomplete word is not a structural keyword.
		result = result[:len(result)-1]
	}
	return result
}

func isStandaloneDQLLiteral(
	ctx context.Context,
	_ *phpsyntax.Node,
	literal *phpsyntax.Node,
	strict bool,
) bool {
	if phpquery.AssignedVariable(literal) == "$dql" {
		return isDirectDQLAssignmentValue(literal)
	}
	call := phpquery.CallAt(literal)
	if call == nil || phpquery.ArgumentIndex(call, literal) != 0 ||
		phpquery.ArgumentExpression(call, 0) != literal {
		return false
	}
	method := strings.ToLower(phpquery.CallMethodName(call))
	if method != "createquery" && method != "setdql" {
		return false
	}
	if !strict {
		return true
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
	targets := []string{
		"Doctrine\\ORM\\EntityManager",
		"Doctrine\\ORM\\EntityManagerInterface",
	}
	if method == "setdql" {
		targets = []string{"Doctrine\\ORM\\Query"}
	}
	for _, target := range targets {
		if phpContext.Snapshot.Relations().IsSubtype(
			receiverType,
			types.Named(target),
		) {
			return true
		}
	}
	return false
}

func isDirectDQLAssignmentValue(literal *phpsyntax.Node) bool {
	for current := literal.Parent(); current != nil; current = current.Parent() {
		if current.Kind() != phpsyntax.PhpAssignmentExpression {
			continue
		}
		var expressions []*phpsyntax.Node
		for child := range current.ChildNodes() {
			expressions = append(expressions, child)
		}
		return len(expressions) >= 2 &&
			expressions[len(expressions)-1] == literal
	}
	// Some recovered/incomplete assignments have no explicit assignment CST
	// node. Accept only when the literal itself is a direct statement child.
	parent := literal.Parent()
	return parent != nil && parent.Kind() == phpsyntax.PhpExpressionStatement
}

func dqlLiteralValue(
	literal *phpsyntax.Node,
) (string, uint32, bool) {
	if literal == nil {
		return "", 0, false
	}
	rng := literal.RangeTrimmedTrivia()
	text := strings.TrimSpace(literal.Text())
	if len(text) < 2 ||
		(text[0] != '\'' && text[0] != '"') ||
		text[len(text)-1] != text[0] {
		return "", 0, false
	}
	if text[0] == '"' && strings.Contains(text[1:len(text)-1], "$") {
		return "", 0, false
	}
	return text[1 : len(text)-1],
		rng.Start + 1,
		true
}

func lexDQL(value string) []dqlToken {
	var result []dqlToken
	for position := 0; position < len(value); {
		character := value[position]
		if character == '\\' && position+1 < len(value) &&
			(value[position+1] == '\'' || value[position+1] == '"') {
			// A quoted DQL value inside a same-quoted PHP literal is escaped
			// in source. Skip it without losing raw source offsets.
			quote := value[position+1]
			position += 2
			for position < len(value) {
				if value[position] == '\\' && position+1 < len(value) {
					if value[position+1] == quote {
						position += 2
						break
					}
					position += 2
					continue
				}
				position++
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote := character
			position++
			for position < len(value) {
				if value[position] == '\\' && position+1 < len(value) {
					position += 2
					continue
				}
				if value[position] == quote {
					position++
					break
				}
				position++
			}
			continue
		}
		if character == '-' && position+1 < len(value) &&
			value[position+1] == '-' {
			position += 2
			for position < len(value) && value[position] != '\n' {
				position++
			}
			continue
		}
		if character == '/' && position+1 < len(value) &&
			value[position+1] == '*' {
			position += 2
			for position+1 < len(value) &&
				(value[position] != '*' || value[position+1] != '/') {
				position++
			}
			if position+1 < len(value) {
				position += 2
			}
			continue
		}
		if isDQLIdentifierByte(character) {
			start := position
			for position < len(value) &&
				isDQLIdentifierByte(value[position]) {
				position++
			}
			result = append(result, dqlToken{
				kind: dqlWordToken, text: value[start:position],
				start: start, end: position,
			})
			continue
		}
		kind := dqlOtherToken
		switch character {
		case '.':
			kind = dqlDotToken
		case ':':
			kind = dqlColonToken
		case ',':
			kind = dqlCommaToken
		}
		if kind != dqlOtherToken {
			result = append(result, dqlToken{
				kind: kind, text: value[position : position+1],
				start: position, end: position + 1,
			})
		}
		position++
	}
	return result
}

func isDQLIdentifierByte(value byte) bool {
	return value == '_' || value == '\\' ||
		value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z'
}

func looksLikeDQL(tokens []dqlToken) bool {
	for _, token := range tokens {
		if token.kind != dqlWordToken {
			continue
		}
		switch strings.ToUpper(token.text) {
		case "SELECT", "UPDATE", "DELETE":
			return true
		default:
			return false
		}
	}
	return false
}

func parseDQLRootAliases(
	tokens []dqlToken,
	base uint32,
	literal *phpsyntax.Node,
	aliases map[string]string,
) []QueryEntityReference {
	var result []QueryEntityReference
	for position := 0; position < len(tokens); position++ {
		keyword := strings.ToUpper(tokens[position].text)
		if tokens[position].kind != dqlWordToken ||
			(keyword != "FROM" && keyword != "UPDATE") {
			continue
		}
		cursor := position + 1
		for cursor < len(tokens) {
			if tokens[cursor].kind != dqlWordToken {
				cursor++
				continue
			}
			entityToken := tokens[cursor]
			if isDQLClauseKeyword(entityToken.text) {
				break
			}
			entity := normalizeDQLClass(entityToken.text)
			cursor++
			entityEnd := entityToken.end
			if cursor+1 < len(tokens) &&
				tokens[cursor].kind == dqlColonToken &&
				tokens[cursor+1].kind == dqlWordToken {
				entity += ":" + tokens[cursor+1].text
				entityEnd = tokens[cursor+1].end
				cursor += 2
			}
			if cursor < len(tokens) &&
				strings.EqualFold(tokens[cursor].text, "AS") {
				cursor++
			}
			if cursor >= len(tokens) ||
				tokens[cursor].kind != dqlWordToken ||
				isDQLClauseKeyword(tokens[cursor].text) {
				break
			}
			alias := tokens[cursor].text
			aliases[alias] = entity
			result = append(result, QueryEntityReference{
				Entity: entity,
				Node:   literal,
				Range: cst.TextRange{
					Start: base + uint32(entityToken.start),
					End:   base + uint32(entityEnd),
				},
			})
			cursor++
			if cursor >= len(tokens) ||
				tokens[cursor].kind != dqlCommaToken {
				break
			}
			cursor++
		}
	}
	return result
}

func resolveDQLJoinAliases(
	idx *Index,
	tokens []dqlToken,
	aliases map[string]string,
) {
	for position := 0; position < len(tokens); position++ {
		if tokens[position].kind != dqlWordToken ||
			!strings.EqualFold(tokens[position].text, "JOIN") {
			continue
		}
		cursor := position + 1
		if cursor < len(tokens) &&
			strings.EqualFold(tokens[cursor].text, "FETCH") {
			cursor++
		}
		if cursor+3 >= len(tokens) ||
			tokens[cursor].kind != dqlWordToken ||
			tokens[cursor+1].kind != dqlDotToken ||
			tokens[cursor+2].kind != dqlWordToken {
			continue
		}
		parent := dqlAliasEntity(aliases, tokens[cursor].text)
		if parent == "" {
			continue
		}
		aliasPosition := cursor + 3
		if aliasPosition < len(tokens) &&
			strings.EqualFold(tokens[aliasPosition].text, "AS") {
			aliasPosition++
		}
		if aliasPosition >= len(tokens) ||
			tokens[aliasPosition].kind != dqlWordToken {
			continue
		}
		fields, err := idx.Fields(parent)
		if err != nil {
			continue
		}
		for _, field := range fields {
			if strings.EqualFold(field.Name, tokens[cursor+2].text) &&
				field.Relation != "" {
				aliases[tokens[aliasPosition].text] = field.Relation
				break
			}
		}
	}
}

func normalizeDQLClass(value string) string {
	for strings.Contains(value, `\\`) {
		value = strings.ReplaceAll(value, `\\`, `\`)
	}
	return strings.TrimLeft(value, `\`)
}

func dqlAliasEntity(aliases map[string]string, alias string) string {
	if entity := aliases[alias]; entity != "" {
		return entity
	}
	for candidate, entity := range aliases {
		if strings.EqualFold(candidate, alias) {
			return entity
		}
	}
	return ""
}

func dqlTokenIndex(tokens []dqlToken, start int) int {
	for position := range tokens {
		if tokens[position].start == start {
			return position
		}
	}
	return -1
}

func isDQLClauseKeyword(value string) bool {
	switch strings.ToUpper(value) {
	case "WHERE", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER",
		"GROUP", "HAVING", "ORDER", "SET", "WITH":
		return true
	default:
		return false
	}
}
