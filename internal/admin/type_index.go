package admin

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

var (
	adminTypeDeclarationPattern = regexp.MustCompile(
		`\b(interface|type)\s+([A-Za-z_$][A-Za-z0-9_$]*)`,
	)
	adminTypeImportPattern = regexp.MustCompile(
		`(?ms)^\s*import\s+(.+?)\s+from\s+['"]([^'"]+)['"]\s*;?`,
	)
	adminTypeCallEventPattern = regexp.MustCompile(
		`\(\s*[A-Za-z_$][A-Za-z0-9_$]*\s*:\s*['"]([^'"]+)['"]`,
	)
)

// AdminTypeFile is the compact TypeScript type context retained for one
// Administration source file. Keeping imports beside declarations lets the
// resolver follow local aliases without embedding a TypeScript compiler.
type AdminTypeFile struct {
	FilePath     string
	Imports      []AdminTypeImport
	Declarations []AdminTypeDeclaration
}

type AdminTypeImport struct {
	LocalName    string
	ImportedName string
	Source       string
}

// AdminTypeDeclaration describes a structural interface or type alias.
// Members contain source locations, while Alias and Extends preserve the
// references required to compose imported and inherited shapes lazily.
type AdminTypeDeclaration struct {
	Name            string
	FilePath        string
	Line            int
	DefinitionRange AdminSourceRange
	Parameters      []string
	Members         []TwigVueMember
	Extends         []string
	Alias           string
	Interface       bool
	Open            bool
	Default         bool
	CallEvents      []VueComponentEvent
}

// VueTypeShape is the resolved structural view of a TypeScript expression.
// Complete is deliberately separate from Members: an unresolved intersection
// may still contribute useful completion fields but must not drive typo
// diagnostics.
type VueTypeShape struct {
	Type     string
	Members  []TwigVueMember
	Complete bool
}

func parseAdminTypeFile(
	filePath,
	source string,
	lineIndex *cst.LineIndex,
) AdminTypeFile {
	result := AdminTypeFile{
		FilePath: filePath,
		Imports:  parseAdminTypeImports(source),
	}
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(source)
	}
	for _, match := range adminTypeDeclarationPattern.FindAllStringSubmatchIndex(
		source, -1,
	) {
		if len(match) < 6 || !adminTypeCodePosition(source, match[0]) {
			continue
		}
		kind := source[match[2]:match[3]]
		name := source[match[4]:match[5]]
		cursor := match[5]
		parameters, afterParameters := parseAdminTypeParameters(source, cursor)
		cursor = afterParameters
		declaration := AdminTypeDeclaration{
			Name: name, FilePath: filePath, Parameters: parameters,
			Interface: kind == "interface",
		}
		line, _ := lineIndex.Position(uint32(match[4]))
		declaration.Line = int(line) + 1
		if declaration.Interface {
			open := indexAdminTypeToken(source, cursor, '{')
			if open < 0 {
				continue
			}
			header := strings.TrimSpace(source[cursor:open])
			if strings.HasPrefix(header, "extends") {
				declaration.Extends = splitAdminTypeReferences(
					strings.TrimSpace(strings.TrimPrefix(header, "extends")), ',',
				)
			}
			declaration.Members = adminTypeMembers(
				source, open, filePath, lineIndex, 0,
			)
			declaration.Open = adminObjectTypeHasIndexSignature(source, open)
			if close := balancedBraceEnd(source, open); close > open {
				declaration.CallEvents = adminTypeCallEvents(
					source, open, close+1, filePath, lineIndex,
				)
			}
			result.Declarations = append(result.Declarations, declaration)
			continue
		}

		equals := indexAdminTypeToken(source, cursor, '=')
		if equals < 0 {
			// Avoid treating `import { type Foo }` as a declaration.
			continue
		}
		end := adminTypeAliasEnd(source, equals+1)
		aliasStart := equals + 1
		for aliasStart < end && unicode.IsSpace(rune(source[aliasStart])) {
			aliasStart++
		}
		declaration.Alias = strings.TrimRightFunc(
			source[aliasStart:end], unicode.IsSpace,
		)
		declaration.CallEvents = adminTypeCallEvents(
			source, equals+1, end, filePath, lineIndex,
		)
		for _, open := range adminTopLevelObjectStarts(declaration.Alias) {
			members := adminTypeMembers(
				declaration.Alias, open, filePath, lineIndex, aliasStart,
			)
			declaration.Members = mergeTwigVueMembers(
				declaration.Members, members,
			)
		}
		result.Declarations = append(result.Declarations, declaration)
	}
	result.Declarations = append(
		result.Declarations,
		parseShopwareUtilityValueDeclarations(filePath, source, lineIndex)...,
	)
	return result
}

func adminTypeCallEvents(
	source string,
	start,
	end int,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponentEvent {
	if start < 0 || end <= start || end > len(source) {
		return nil
	}
	var result []VueComponentEvent
	for _, match := range adminTypeCallEventPattern.FindAllStringSubmatchIndex(
		source[start:end], -1,
	) {
		if len(match) < 4 {
			continue
		}
		open := start + match[0]
		if !adminTypeCodePosition(source, open) {
			continue
		}
		nameStart, nameEnd := start+match[2], start+match[3]
		name := source[nameStart:nameEnd]
		close := matchingSlotDelimiter(source, open, '(', ')')
		if close < open || close >= end {
			continue
		}
		line := 0
		if lineIndex != nil {
			lineValue, _ := lineIndex.Position(uint32(nameStart))
			line = int(lineValue) + 1
		}
		result = appendComponentEvent(result, VueComponentEvent{
			Name: name, Type: strings.TrimSpace(source[open : close+1]),
			Documentation: adminTypeFieldDocumentation(source, open),
			FilePath:      filePath, Line: line,
			NameRange: sourceRangeAt(
				lineIndex, uint32(nameStart), uint32(nameEnd), false,
			),
		})
	}
	return result
}

func adminTypeMembers(
	source string,
	open int,
	filePath string,
	lineIndex *cst.LineIndex,
	baseOffset int,
) []TwigVueMember {
	fields := parseTypeDeclarationFields(source, open)
	result := make([]TwigVueMember, 0, len(fields))
	for _, field := range fields {
		if field.name == "" {
			continue
		}
		line := 0
		if lineIndex != nil && field.offset >= 0 && field.offset <= len(source) {
			lineValue, _ := lineIndex.Position(uint32(baseOffset + field.offset))
			line = int(lineValue) + 1
		}
		member := TwigVueMember{
			Name: field.name, Type: strings.TrimSpace(field.value),
			Documentation:  adminTypeFieldDocumentation(source, field.offset),
			Optional:       field.optional,
			DefinitionPath: filePath, DefinitionLine: line,
			DefinitionRange: typeDeclarationFieldNameRange(
				field, lineIndex, baseOffset,
			),
		}
		if field.valueOffset >= 0 && strings.HasPrefix(member.Type, "{") &&
			balancedBraceEnd(member.Type, 0) == len(member.Type)-1 {
			member.NestedMembers = adminTypeMembers(
				source, field.valueOffset, filePath, lineIndex, baseOffset,
			)
			member.NestedComplete = !adminObjectTypeHasIndexSignature(
				source, field.valueOffset,
			)
		}
		result = append(result, member)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func adminTypeFieldDocumentation(source string, offset int) string {
	if offset <= 0 || offset > len(source) {
		return ""
	}
	end := offset
	for end > 0 && unicode.IsSpace(rune(source[end-1])) {
		end--
	}
	if end < 2 || source[end-2:end] != "*/" {
		return ""
	}
	start := strings.LastIndex(source[:end-2], "/**")
	if start < 0 {
		return ""
	}
	return parseJavaScriptDocumentation(source[start:end])
}

func parseAdminTypeImports(source string) []AdminTypeImport {
	var result []AdminTypeImport
	seen := make(map[string]bool)
	for _, match := range adminTypeImportPattern.FindAllStringSubmatchIndex(
		source, -1,
	) {
		if len(match) < 6 || !adminTypeCodePosition(source, match[0]) {
			continue
		}
		specifier := strings.TrimSpace(source[match[2]:match[3]])
		path := strings.TrimSpace(source[match[4]:match[5]])
		if path == "" {
			continue
		}
		specifier = strings.TrimSpace(strings.TrimPrefix(specifier, "type "))
		if open := strings.IndexByte(specifier, '{'); open >= 0 {
			if close := matchingSlotDelimiter(specifier, open, '{', '}'); close > open {
				for _, entry := range splitSlotTopLevel(
					specifier[open+1:close], ',',
				) {
					entry = strings.TrimSpace(strings.TrimPrefix(
						strings.TrimSpace(entry), "type ",
					))
					parts := strings.Fields(entry)
					if len(parts) == 0 {
						continue
					}
					imported, local := parts[0], parts[0]
					if len(parts) >= 3 && parts[len(parts)-2] == "as" {
						local = parts[len(parts)-1]
					}
					appendAdminTypeImport(
						&result, seen, local, imported, path,
					)
				}
			}
			specifier = strings.TrimSpace(specifier[:open])
			specifier = strings.TrimSuffix(specifier, ",")
		}
		if strings.HasPrefix(specifier, "*") {
			parts := strings.Fields(specifier)
			if len(parts) >= 3 && parts[len(parts)-2] == "as" {
				appendAdminTypeImport(
					&result, seen, parts[len(parts)-1], "*", path,
				)
			}
			continue
		}
		if defaultName := strings.TrimSpace(specifier); defaultName != "" {
			appendAdminTypeImport(
				&result, seen, defaultName, "default", path,
			)
		}
	}
	return result
}

func appendAdminTypeImport(
	result *[]AdminTypeImport,
	seen map[string]bool,
	local,
	imported,
	source string,
) {
	if local == "" || source == "" {
		return
	}
	key := local + "\x00" + source
	if seen[key] {
		return
	}
	seen[key] = true
	*result = append(*result, AdminTypeImport{
		LocalName: local, ImportedName: imported, Source: source,
	})
}

func parseAdminTypeParameters(source string, cursor int) ([]string, int) {
	for cursor < len(source) && unicode.IsSpace(rune(source[cursor])) {
		cursor++
	}
	if cursor >= len(source) || source[cursor] != '<' {
		return nil, cursor
	}
	close := matchingSlotDelimiter(source, cursor, '<', '>')
	if close < 0 {
		return nil, cursor
	}
	var result []string
	for _, parameter := range splitSlotTopLevel(source[cursor+1:close], ',') {
		name := strings.Fields(strings.TrimSpace(parameter))
		if len(name) > 0 && isAdminTypeIdentifier(name[0]) {
			result = append(result, name[0])
		}
	}
	return result, close + 1
}

func indexAdminTypeToken(source string, cursor int, token byte) int {
	state := declarationScanState{}
	for index := cursor; index < len(source); index++ {
		state.consume(source, &index)
		if state.inLiteralOrComment() {
			continue
		}
		if source[index] == token {
			return index
		}
		if source[index] == ';' || source[index] == '\n' && token == '=' {
			return -1
		}
	}
	return -1
}

func adminTypeAliasEnd(source string, start int) int {
	state := declarationScanState{}
	angles := 0
	for index := start; index < len(source); index++ {
		state.consume(source, &index)
		if state.inLiteralOrComment() {
			continue
		}
		switch source[index] {
		case '{':
			state.braces++
		case '}':
			if state.braces > 0 {
				state.braces--
			}
		case '(':
			state.parens++
		case ')':
			if state.parens > 0 {
				state.parens--
			}
		case '[':
			state.brackets++
		case ']':
			if state.brackets > 0 {
				state.brackets--
			}
		case '<':
			angles++
		case '>':
			if angles > 0 {
				angles--
			}
		case ';':
			if state.braces == 0 && state.parens == 0 &&
				state.brackets == 0 && angles == 0 {
				return index
			}
		}
	}
	return len(source)
}

func adminTopLevelObjectStarts(value string) []int {
	var result []int
	state := declarationScanState{}
	angles := 0
	for index := 0; index < len(value); index++ {
		state.consume(value, &index)
		if state.inLiteralOrComment() {
			continue
		}
		switch value[index] {
		case '<':
			angles++
		case '>':
			if angles > 0 {
				angles--
			}
		case '{':
			if state.braces == 0 && state.parens == 0 &&
				state.brackets == 0 && angles == 0 {
				result = append(result, index)
				if close := balancedBraceEnd(value, index); close > index {
					index = close
				}
			}
		case '(':
			state.parens++
		case ')':
			if state.parens > 0 {
				state.parens--
			}
		case '[':
			state.brackets++
		case ']':
			if state.brackets > 0 {
				state.brackets--
			}
		}
	}
	return result
}

func splitAdminTypeReferences(value string, separator byte) []string {
	var result []string
	for _, entry := range splitAdminTypeTopLevel(value, separator) {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			result = append(result, entry)
		}
	}
	return result
}

func adminTypeCodePosition(source string, position int) bool {
	state := declarationScanState{}
	for index := 0; index < position && index < len(source); index++ {
		state.consume(source, &index)
	}
	return !state.inLiteralOrComment()
}

func isAdminTypeIdentifier(value string) bool {
	if value == "" || !isSlotIdentifierStart(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isSlotIdentifierContinue(value[index]) {
			return false
		}
	}
	return true
}

func adminTypeImportCandidates(filePath, importPath string) []string {
	if importPath == "" || (!strings.HasPrefix(importPath, "./") &&
		!strings.HasPrefix(importPath, "../") &&
		!strings.HasPrefix(importPath, "src/")) {
		return nil
	}
	var base string
	if strings.HasPrefix(importPath, "src/") {
		normalized := filepath.ToSlash(filePath)
		marker := "/Resources/app/administration/"
		position := strings.Index(normalized, marker)
		if position < 0 {
			return nil
		}
		base = filepath.Join(
			filepath.FromSlash(normalized[:position+len(marker)]), importPath,
		)
	} else {
		base = filepath.Join(filepath.Dir(filePath), importPath)
	}
	base = filepath.Clean(base)
	if strings.HasSuffix(base, ".ts") || strings.HasSuffix(base, ".d.ts") ||
		strings.HasSuffix(base, ".js") || strings.HasSuffix(base, ".vue") {
		return []string{base}
	}
	return []string{
		base + ".ts", base + ".d.ts", base + ".js", base + ".vue",
		filepath.Join(base, "index.ts"), filepath.Join(base, "index.d.ts"),
		filepath.Join(base, "index.js"), filepath.Join(base, "index.vue"),
	}
}

// ResolveVueType resolves a structural TypeScript expression in the lexical
// import context of contextPath. It supports the shapes commonly used by
// Options API components: interfaces, object aliases, intersections, nullable
// unions, utility wrappers, imports, inheritance, and generic substitution.
func (idx *AdminComponentIndexer) ResolveVueType(
	typeExpression,
	contextPath string,
	liveFiles ...AdminTypeFile,
) (VueTypeShape, error) {
	if idx == nil || idx.typeIndex == nil {
		return VueTypeShape{Type: strings.TrimSpace(typeExpression)}, nil
	}
	return idx.resolveVueType(
		strings.TrimSpace(typeExpression), contextPath,
		make(map[string]bool), nil, idx.liveTypeFileOverlays(liveFiles),
	)
}

// ResolveVueEvents resolves callable defineEmits signatures from local or
// imported TypeScript declarations. Object-map event members are exposed by
// ResolveVueType; this companion preserves overload-style declarations such
// as `(event: 'save', id: string): void` without treating them as object
// properties in unrelated type analysis.
func (idx *AdminComponentIndexer) ResolveVueEvents(
	typeExpression,
	contextPath string,
	liveFiles ...AdminTypeFile,
) ([]VueComponentEvent, error) {
	if idx == nil || idx.typeIndex == nil {
		return nil, nil
	}
	return idx.resolveVueEvents(
		strings.TrimSpace(typeExpression), contextPath,
		make(map[string]bool), nil, idx.liveTypeFileOverlays(liveFiles),
	)
}

func adminTypeFileOverlays(files []AdminTypeFile) map[string]AdminTypeFile {
	if len(files) == 0 {
		return nil
	}
	result := make(map[string]AdminTypeFile, len(files))
	for _, file := range files {
		if file.FilePath == "" {
			continue
		}
		result[filepath.Clean(file.FilePath)] = file
	}
	return result
}

func (idx *AdminComponentIndexer) resolveVueEvents(
	typeExpression,
	contextPath string,
	seen map[string]bool,
	substitutions map[string]string,
	liveFiles map[string]AdminTypeFile,
) ([]VueComponentEvent, error) {
	value := trimAdminTypeParentheses(strings.TrimSpace(
		substituteAdminType(typeExpression, substitutions),
	))
	if value == "" {
		return nil, nil
	}
	for _, wrapper := range []string{
		"Readonly", "Partial", "Required", "NonNullable",
	} {
		if inner, matched := adminTypeGenericInner(value, wrapper); matched {
			return idx.resolveVueEvents(
				inner, contextPath, seen, substitutions, liveFiles,
			)
		}
	}
	for _, separator := range []byte{'|', '&'} {
		if branches := splitAdminTypeTopLevel(value, separator); len(branches) > 1 {
			var result []VueComponentEvent
			for _, branch := range branches {
				events, err := idx.resolveVueEvents(
					branch, contextPath, seen, substitutions, liveFiles,
				)
				if err != nil {
					return nil, err
				}
				for _, event := range events {
					result = appendComponentEvent(result, event)
				}
			}
			return result, nil
		}
	}
	name, arguments := parseAdminNamedType(value)
	if name == "" {
		return nil, nil
	}
	key := filepath.Clean(contextPath) + "\x00events\x00" + name + "\x00" +
		strings.Join(arguments, "\x00")
	if seen[key] {
		return nil, nil
	}
	seen[key] = true
	defer delete(seen, key)
	declaration, declarationContext, found, err :=
		idx.resolveAdminTypeDeclaration(contextPath, name, liveFiles)
	if err != nil || !found {
		return nil, err
	}
	localSubstitutions := make(map[string]string, len(substitutions)+len(arguments))
	for substitutionName, substitutionValue := range substitutions {
		localSubstitutions[substitutionName] = substitutionValue
	}
	for parameterIndex, parameter := range declaration.Parameters {
		if parameterIndex < len(arguments) {
			localSubstitutions[parameter] = substituteAdminType(
				arguments[parameterIndex], substitutions,
			)
		}
	}
	result := append([]VueComponentEvent(nil), declaration.CallEvents...)
	for eventIndex := range result {
		result[eventIndex].Type = substituteAdminType(
			result[eventIndex].Type, localSubstitutions,
		)
	}
	for _, parent := range declaration.Extends {
		events, parentErr := idx.resolveVueEvents(
			parent, declarationContext, seen, localSubstitutions, liveFiles,
		)
		if parentErr != nil {
			return nil, parentErr
		}
		for _, event := range events {
			result = appendComponentEvent(result, event)
		}
	}
	if declaration.Alias != "" {
		events, aliasErr := idx.resolveVueEvents(
			declaration.Alias, declarationContext, seen, localSubstitutions, liveFiles,
		)
		if aliasErr != nil {
			return nil, aliasErr
		}
		for _, event := range events {
			result = appendComponentEvent(result, event)
		}
	}
	return result, nil
}

func (idx *AdminComponentIndexer) resolveVueType(
	typeExpression,
	contextPath string,
	seen map[string]bool,
	substitutions map[string]string,
	liveFiles map[string]AdminTypeFile,
) (VueTypeShape, error) {
	value := strings.TrimSpace(substituteAdminType(typeExpression, substitutions))
	result := VueTypeShape{Type: value}
	if value == "" {
		return result, nil
	}
	if asserted := vueTypeAssertion(value); asserted != "" {
		value = asserted
		result.Type = value
	}
	value = trimAdminTypeParentheses(value)
	result.Type = value

	for _, wrapper := range []string{
		"Readonly", "Partial", "Required", "NonNullable", "PropType",
	} {
		if inner, matched := adminTypeGenericInner(value, wrapper); matched {
			shape, err := idx.resolveVueType(
				inner, contextPath, seen, substitutions, liveFiles,
			)
			if wrapper == "Partial" || wrapper == "Required" {
				for memberIndex := range shape.Members {
					shape.Members[memberIndex].Optional = wrapper == "Partial"
				}
			}
			shape.Type = value
			return shape, err
		}
	}

	if union := splitAdminTypeTopLevel(value, '|'); len(union) > 1 {
		var shapes []VueTypeShape
		for _, branch := range union {
			branch = strings.TrimSpace(branch)
			if branch == "null" || branch == "undefined" || branch == "never" {
				continue
			}
			shape, err := idx.resolveVueType(
				branch, contextPath, seen, substitutions, liveFiles,
			)
			if err != nil {
				return result, err
			}
			shapes = append(shapes, shape)
		}
		if len(shapes) == 1 {
			shapes[0].Type = value
			return shapes[0], nil
		}
		if len(shapes) == 0 {
			result.Complete = true
			return result, nil
		}
		result.Members = unionVueTypeMembers(shapes)
		result.Complete = true
		for _, shape := range shapes {
			result.Complete = result.Complete && shape.Complete
		}
		return result, nil
	}

	if intersection := splitAdminTypeTopLevel(value, '&'); len(intersection) > 1 {
		result.Complete = true
		for _, branch := range intersection {
			shape, err := idx.resolveVueType(
				branch, contextPath, seen, substitutions, liveFiles,
			)
			if err != nil {
				return result, err
			}
			result.Members = mergeTwigVueMembers(
				result.Members, shape.Members,
			)
			result.Complete = result.Complete && shape.Complete
		}
		return result, nil
	}
	arrayValue := strings.TrimSpace(strings.TrimPrefix(value, "readonly "))
	if strings.HasSuffix(arrayValue, "[]") {
		shape, _ := adminJavaScriptBuiltinShape(
			value, "Array",
			[]string{strings.TrimSpace(strings.TrimSuffix(arrayValue, "[]"))},
		)
		return shape, nil
	}

	// Only a top-level object literal describes the current receiver. Generic
	// arguments such as Array<{ id: string }> contain braces too, but exposing
	// the element fields as array members incorrectly makes `length` unknown.
	if strings.HasPrefix(value, "{") &&
		balancedBraceEnd(value, 0) == len(value)-1 {
		open := 0
		result.Members = VueTypeMembers(value)
		for memberIndex := range result.Members {
			result.Members[memberIndex].DefinitionPath = contextPath
		}
		result.Complete = !adminObjectTypeHasIndexSignature(value, open)
		return result, nil
	}

	name, arguments := parseAdminNamedType(value)
	if name == "" {
		result.Complete = adminPrimitiveType(value)
		return result, nil
	}
	if builtin, matched := adminJavaScriptBuiltinShape(value, name, arguments); matched {
		return builtin, nil
	}
	if (name == "Pick" || name == "Omit") && len(arguments) == 2 {
		shape, err := idx.resolveVueType(
			arguments[0], contextPath, seen, substitutions, liveFiles,
		)
		if err != nil {
			return result, err
		}
		keys, complete := vueStringLiteralUnionValues(
			substituteAdminType(arguments[1], substitutions),
		)
		selected := make(map[string]bool, len(keys))
		for _, key := range keys {
			selected[key] = true
		}
		if name == "Pick" && !complete {
			return VueTypeShape{Type: value}, nil
		}
		members := make([]TwigVueMember, 0, len(shape.Members))
		for _, member := range shape.Members {
			include := selected[member.Name]
			if name == "Omit" {
				include = !include
			}
			if include {
				members = append(members, member)
			}
		}
		shape.Type = value
		shape.Members = members
		shape.Complete = shape.Complete && complete
		return shape, nil
	}
	if entityName, matched := adminNamedStringGeneric(
		name, arguments, "Repository",
	); matched {
		return adminRepositoryShape(value, entityName), nil
	}
	if storeName, matched := adminNamedStringGeneric(
		name, arguments, "AdminStore",
	); matched {
		return idx.resolveAdminStoreType(value, storeName)
	}
	if entityName, collection, matched := adminEntityGeneric(
		name, arguments,
	); matched {
		if collection {
			return adminEntityCollectionShape(value, entityName), nil
		}
		return idx.resolveAdminEntityType(
			value, entityName, contextPath, seen, liveFiles,
		)
	}
	key := filepath.Clean(contextPath) + "\x00" + name + "\x00" +
		strings.Join(arguments, "\x00")
	if seen[key] {
		return result, nil
	}
	seen[key] = true
	declaration, declarationContext, found, err := idx.resolveAdminTypeDeclaration(
		contextPath, name, liveFiles,
	)
	if err != nil || !found {
		delete(seen, key)
		result.Complete = adminPrimitiveType(value)
		return result, err
	}
	localSubstitutions := make(map[string]string, len(substitutions)+len(arguments))
	for substitutionName, substitutionValue := range substitutions {
		localSubstitutions[substitutionName] = substitutionValue
	}
	for parameterIndex, parameter := range declaration.Parameters {
		if parameterIndex < len(arguments) {
			localSubstitutions[parameter] = substituteAdminType(
				arguments[parameterIndex], substitutions,
			)
		}
	}
	result.Members = substituteVueTypeMembers(
		declaration.Members, localSubstitutions,
	)
	result.Complete = declaration.Interface && !declaration.Open
	for _, parent := range declaration.Extends {
		shape, parentErr := idx.resolveVueType(
			parent, declarationContext, seen, localSubstitutions, liveFiles,
		)
		if parentErr != nil {
			delete(seen, key)
			return result, parentErr
		}
		result.Members = mergeTwigVueMembers(shape.Members, result.Members)
		result.Complete = result.Complete && shape.Complete
	}
	if declaration.Alias != "" {
		shape, aliasErr := idx.resolveVueType(
			declaration.Alias, declarationContext, seen, localSubstitutions, liveFiles,
		)
		if aliasErr != nil {
			delete(seen, key)
			return result, aliasErr
		}
		result.Members = mergeTwigVueMembers(shape.Members, result.Members)
		result.Complete = shape.Complete
	}
	delete(seen, key)
	return result, nil
}

func adminJavaScriptBuiltinShape(
	typeExpression,
	name string,
	arguments []string,
) (VueTypeShape, bool) {
	method := func(name, signature string) TwigVueMember {
		return TwigVueMember{Name: name, Type: signature}
	}
	property := func(name, memberType string) TwigVueMember {
		return TwigVueMember{Name: name, Type: memberType}
	}
	result := VueTypeShape{Type: typeExpression}
	switch name {
	case "string", "String":
		result.Members = []TwigVueMember{
			property("length", "number"),
			method("charAt", "(index: number) => string"),
			method("endsWith", "(search: string) => boolean"),
			method("includes", "(search: string) => boolean"),
			method("replace", "(search: string | RegExp, replacement: string) => string"),
			method("slice", "(start?: number, end?: number) => string"),
			method("split", "(separator: string | RegExp) => string[]"),
			method("startsWith", "(search: string) => boolean"),
			method("substring", "(start: number, end?: number) => string"),
			method("toLowerCase", "() => string"),
			method("toUpperCase", "() => string"),
			method("trim", "() => string"),
		}
	case "number", "Number":
		result.Members = []TwigVueMember{
			method("toFixed", "(digits?: number) => string"),
			method("toLocaleString", "() => string"),
			method("toString", "(radix?: number) => string"),
			method("valueOf", "() => number"),
		}
	case "boolean", "Boolean":
		result.Members = []TwigVueMember{
			method("toString", "() => string"),
			method("valueOf", "() => boolean"),
		}
	case "Function":
		result.Members = []TwigVueMember{
			method("apply", "(thisArg: unknown, args?: unknown[]) => unknown"),
			method("bind", "(thisArg: unknown, ...args: unknown[]) => Function"),
			method("call", "(thisArg: unknown, ...args: unknown[]) => unknown"),
			property("length", "number"),
			property("name", "string"),
			method("toString", "() => string"),
		}
	case "Array", "ReadonlyArray":
		elementType := "unknown"
		if len(arguments) == 1 && strings.TrimSpace(arguments[0]) != "" {
			elementType = strings.TrimSpace(arguments[0])
		}
		result.Members = []TwigVueMember{
			method("at", "(index: number) => "+elementType+" | undefined"),
			method("filter", "(predicate: Function) => "+typeExpression),
			method("find", "(predicate: Function) => "+elementType+" | undefined"),
			method("forEach", "(callback: Function) => void"),
			method("includes", "(value: "+elementType+") => boolean"),
			method("join", "(separator?: string) => string"),
			property("length", "number"),
			method("map", "(callback: Function) => Array<unknown>"),
			method("slice", "(start?: number, end?: number) => "+typeExpression),
		}
	case "Record":
		// Record keys are dynamic, so there are no safe named completion
		// members. Its generic key/value arguments are still consumed by
		// v-for binding inference.
	case "Map", "ReadonlyMap":
		keyType, valueType := "unknown", "unknown"
		if len(arguments) > 0 && strings.TrimSpace(arguments[0]) != "" {
			keyType = strings.TrimSpace(arguments[0])
		}
		if len(arguments) > 1 && strings.TrimSpace(arguments[1]) != "" {
			valueType = strings.TrimSpace(arguments[1])
		}
		result.Members = []TwigVueMember{
			method("clear", "() => void"),
			method("delete", "(key: "+keyType+") => boolean"),
			method("get", "(key: "+keyType+") => "+valueType+" | undefined"),
			method("has", "(key: "+keyType+") => boolean"),
			method("set", "(key: "+keyType+", value: "+valueType+") => "+typeExpression),
			property("size", "number"),
		}
	case "Emitter":
		eventsType := "Record<string | symbol, unknown>"
		if len(arguments) > 0 && strings.TrimSpace(arguments[0]) != "" {
			eventsType = strings.TrimSpace(arguments[0])
		}
		result.Members = []TwigVueMember{
			property("all", "Map<keyof "+eventsType+", Function[]>"),
			method("emit", "(type: keyof "+eventsType+", event?: unknown) => void"),
			method("off", "(type: keyof "+eventsType+", handler?: Function) => void"),
			method("on", "(type: keyof "+eventsType+", handler: Function) => void"),
		}
	case "Promise", "PromiseLike":
		resultType := "unknown"
		if len(arguments) == 1 && strings.TrimSpace(arguments[0]) != "" {
			resultType = strings.TrimSpace(arguments[0])
		}
		result.Members = []TwigVueMember{
			method("then", "(onFulfilled: (value: "+resultType+") => unknown) => Promise<unknown>"),
			method("catch", "(onRejected: Function) => Promise<"+resultType+">"),
			method("finally", "(onFinally: Function) => Promise<"+resultType+">"),
		}
	default:
		return VueTypeShape{}, false
	}
	// JavaScript built-ins have a much broader version-dependent surface. The
	// retained members improve completion without claiming diagnostic closure.
	result.Complete = false
	return result, true
}

func adminNamedStringGeneric(
	name string,
	arguments []string,
	wanted string,
) (string, bool) {
	if len(arguments) != 1 {
		return "", false
	}
	shortName := name
	if separator := strings.LastIndexByte(shortName, '.'); separator >= 0 {
		shortName = shortName[separator+1:]
	}
	if shortName != wanted {
		return "", false
	}
	value := adminTypeStringLiteral(arguments[0])
	return value, value != ""
}

func adminRepositoryShape(typeExpression, entityName string) VueTypeShape {
	entityType := "Entity<'" + entityName + "'>"
	collectionType := "EntityCollection<'" + entityName + "'>"
	method := func(name, signature string) TwigVueMember {
		return TwigVueMember{Name: name, Type: signature}
	}
	property := func(name, memberType string) TwigVueMember {
		return TwigVueMember{Name: name, Type: memberType}
	}
	return VueTypeShape{
		Type: typeExpression,
		Members: []TwigVueMember{
			method("assign", "(id: string, context?: apiContext) => Promise<unknown>"),
			method("clone", "(entityId: string, behavior: object, context?: apiContext) => Promise<unknown>"),
			method("create", "(context?: apiContext, id?: string | null) => "+entityType),
			method("createVersion", "(entityId: string, context?: apiContext, versionId?: string | null, versionName?: string | null) => Promise<apiContext>"),
			method("delete", "(id: string, context?: apiContext) => Promise<unknown>"),
			method("deleteVersion", "(entityId: string, versionId: string, context?: apiContext) => Promise<unknown>"),
			method("discard", "(entity: "+entityType+") => void"),
			property("entityName", "'"+entityName+"'"),
			method("get", "(id: string, context?: apiContext, criteria?: Criteria | null) => Promise<"+entityType+" | null>"),
			method("hasChanges", "(entity: "+entityType+") => boolean"),
			method("iterateIds", "(criteria: Criteria, callback: Function, context?: apiContext) => Promise<unknown>"),
			method("mergeVersion", "(versionId: string, context?: apiContext) => Promise<unknown>"),
			property("route", "string"),
			property("schema", "EntityDefinition"),
			method("save", "(entity: "+entityType+", context?: apiContext) => Promise<void | unknown>"),
			method("saveAll", "(entities: "+collectionType+", context?: apiContext) => Promise<unknown>"),
			method("search", "(criteria: Criteria, context?: apiContext) => Promise<"+collectionType+">"),
			method("searchIds", "(criteria: Criteria, context?: apiContext) => Promise<IdSearchResult>"),
			method("sync", "(entities: "+collectionType+", context?: apiContext) => Promise<unknown>"),
			method("syncDeleted", "(ids: string[], context?: apiContext) => Promise<void>"),
		},
		// Repository decorators can add runtime methods, so its core surface is
		// useful for completion without enabling closed-world diagnostics.
		Complete: false,
	}
}

func (idx *AdminComponentIndexer) resolveAdminStoreType(
	typeExpression,
	storeName string,
) (VueTypeShape, error) {
	result := VueTypeShape{Type: typeExpression}
	stores, err := idx.GetStore(storeName)
	if err != nil {
		return result, err
	}
	for _, store := range stores {
		for _, member := range store.Members {
			result.Members = mergeTwigVueMembers(
				result.Members,
				[]TwigVueMember{{
					Name:           member.Name,
					Type:           member.Type,
					DefinitionPath: member.FilePath,
					DefinitionLine: member.Line,
				}},
			)
		}
	}
	// Pinia adds plugin/runtime members and permits store augmentation.
	result.Complete = false
	return result, nil
}

func adminEntityGeneric(
	name string,
	arguments []string,
) (entityName string, collection, matched bool) {
	if len(arguments) != 1 {
		return "", false, false
	}
	shortName := name
	if separator := strings.LastIndexByte(shortName, '.'); separator >= 0 {
		shortName = shortName[separator+1:]
	}
	switch shortName {
	case "Entity":
	case "EntityCollection":
		collection = true
	default:
		return "", false, false
	}
	entityName = adminTypeStringLiteral(arguments[0])
	if entityName == "" {
		return "", false, false
	}
	return entityName, collection, true
}

func adminTypeStringLiteral(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != value[len(value)-1] ||
		(value[0] != '\'' && value[0] != '"' && value[0] != '`') {
		return ""
	}
	return strings.TrimSpace(value[1 : len(value)-1])
}

// resolveAdminEntityType maps Shopware's global Entity<'technical_name'>
// helper to the generated EntitySchema interface. Interfaces are deliberately
// merged across files because extensions augment the global entity namespace.
func (idx *AdminComponentIndexer) resolveAdminEntityType(
	typeExpression,
	entityName,
	contextPath string,
	seen map[string]bool,
	liveFiles map[string]AdminTypeFile,
) (VueTypeShape, error) {
	result := VueTypeShape{Type: typeExpression}
	if idx == nil || idx.typeIndex == nil || entityName == "" {
		return result, nil
	}
	key := "entity\x00" + entityName
	if seen[key] {
		return result, nil
	}
	seen[key] = true
	defer delete(seen, key)

	files, err := idx.allAdminTypeFiles(liveFiles)
	if err != nil {
		return result, err
	}
	found := false
	complete := true
	for _, file := range files {
		for _, declaration := range file.Declarations {
			if declaration.Name != entityName || !declaration.Interface {
				continue
			}
			found = true
			result.Members = mergeTwigVueMembers(
				result.Members, declaration.Members,
			)
			for _, parent := range declaration.Extends {
				shape, parentErr := idx.resolveVueType(
					parent, file.FilePath, seen, nil, liveFiles,
				)
				if parentErr != nil {
					return result, parentErr
				}
				result.Members = mergeTwigVueMembers(
					shape.Members, result.Members,
				)
				complete = complete && shape.Complete
			}
		}
	}
	if !found {
		return result, nil
	}
	result.Members = mergeTwigVueMembers(
		result.Members, adminEntityRuntimeMembers(typeExpression, contextPath),
	)
	result.Complete = complete
	return result, nil
}

func adminEntityRuntimeMembers(
	typeExpression,
	contextPath string,
) []TwigVueMember {
	return []TwigVueMember{
		{Name: "_draft", Type: typeExpression, DefinitionPath: contextPath},
		{Name: "_entityName", Type: "string", DefinitionPath: contextPath},
		{Name: "_isDirty", Type: "boolean", DefinitionPath: contextPath},
		{Name: "_isNew", Type: "boolean", DefinitionPath: contextPath},
		{Name: "_origin", Type: typeExpression, DefinitionPath: contextPath},
		{Name: "__identifier__", Type: "() => string", DefinitionPath: contextPath},
		{Name: "getDraft", Type: "() => " + typeExpression, DefinitionPath: contextPath},
		{Name: "getEntityName", Type: "() => string", DefinitionPath: contextPath},
		{Name: "getIsDirty", Type: "() => boolean", DefinitionPath: contextPath},
		{Name: "getOrigin", Type: "() => " + typeExpression, DefinitionPath: contextPath},
		{Name: "isNew", Type: "() => boolean", DefinitionPath: contextPath},
		{Name: "markAsNew", Type: "() => void", DefinitionPath: contextPath},
	}
}

func adminEntityCollectionShape(
	typeExpression,
	entityName string,
) VueTypeShape {
	entityType := "Entity<'" + entityName + "'>"
	arrayType := "Array<" + entityType + ">"
	arrayShape, _ := adminJavaScriptBuiltinShape(
		arrayType, "Array", []string{entityType},
	)
	return VueTypeShape{
		Type: typeExpression,
		Members: mergeTwigVueMembers(arrayShape.Members, []TwigVueMember{
			{Name: "__identifier__", Type: "() => string"},
			{Name: "add", Type: "(entity: " + entityType + ") => void"},
			{Name: "addAt", Type: "(entity: " + entityType + ", index: number) => void"},
			{Name: "aggregations", Type: "Record<string, unknown> | null"},
			{Name: "context", Type: "Record<string, unknown>"},
			{Name: "criteria", Type: "Criteria | null"},
			{Name: "entity", Type: "'" + entityName + "'"},
			{Name: "first", Type: "() => " + entityType + " | null"},
			{Name: "get", Type: "(id: string) => " + entityType + " | null"},
			{Name: "getAt", Type: "(index: number) => " + entityType + " | null"},
			{Name: "getIds", Type: "() => string[]"},
			{Name: "has", Type: "(id: string) => boolean"},
			{Name: "last", Type: "() => " + entityType + " | null"},
			{Name: "length", Type: "number"},
			{Name: "moveItem", Type: "(from: number, to: number) => " + entityType + " | null"},
			{Name: "remove", Type: "(id: string) => boolean"},
			{Name: "source", Type: "string"},
			{Name: "total", Type: "number | null"},
		}),
		// EntityCollection extends Array, whose complete built-in surface is not
		// duplicated here. Suggestions are useful, but typo diagnostics remain
		// disabled for the collection object itself.
		Complete: false,
	}
}

func (idx *AdminComponentIndexer) resolveAdminTypeDeclaration(
	contextPath,
	name string,
	liveFiles map[string]AdminTypeFile,
) (AdminTypeDeclaration, string, bool, error) {
	typeFile, found, err := idx.adminTypeFileForResolution(
		contextPath, liveFiles,
	)
	if err != nil {
		return AdminTypeDeclaration{}, "", false, err
	}
	if found {
		if declaration, declarationFound := adminTypeDeclarationNamed(
			typeFile, name,
		); declarationFound {
			return declaration, contextPath, true, nil
		}
		for _, typeImport := range typeFile.Imports {
			if typeImport.LocalName != name {
				continue
			}
			for _, candidate := range adminTypeImportCandidates(
				contextPath, typeImport.Source,
			) {
				importedFile, importedFound, importedErr :=
					idx.adminTypeFileForResolution(candidate, liveFiles)
				if importedErr != nil {
					return AdminTypeDeclaration{}, "", false, importedErr
				}
				if !importedFound {
					continue
				}
				importedName := typeImport.ImportedName
				if importedName == "default" {
					if declaration, declarationFound :=
						adminTypeDefaultDeclaration(importedFile); declarationFound {
						return declaration, candidate, true, nil
					}
					importedName = name
				}
				if declaration, declarationFound := adminTypeDeclarationNamed(
					importedFile, importedName,
				); declarationFound {
					return declaration, candidate, true, nil
				}
			}
		}
	}

	// Ambient/global declarations are useful in Administration projects, but a
	// duplicated short name is ambiguous. Resolve only a unique declaration.
	files, err := idx.allAdminTypeFiles(liveFiles)
	if err != nil {
		return AdminTypeDeclaration{}, "", false, err
	}
	var match AdminTypeDeclaration
	matchedPath := ""
	count := 0
	for _, candidate := range files {
		if declaration, declarationFound := adminTypeDeclarationNamed(
			candidate, name,
		); declarationFound {
			match = declaration
			matchedPath = candidate.FilePath
			count++
			if count > 1 {
				return AdminTypeDeclaration{}, "", false, nil
			}
		}
	}
	return match, matchedPath, count == 1, nil
}

func (idx *AdminComponentIndexer) adminTypeFile(
	filePath string,
) (AdminTypeFile, bool, error) {
	if idx == nil || idx.typeIndex == nil || filePath == "" {
		return AdminTypeFile{}, false, nil
	}
	values, err := idx.typeIndex.GetValuesByPath(filepath.Clean(filePath))
	if err != nil || len(values) == 0 {
		return AdminTypeFile{}, false, err
	}
	return values[0], true, nil
}

func (idx *AdminComponentIndexer) adminTypeFileForResolution(
	filePath string,
	liveFiles map[string]AdminTypeFile,
) (AdminTypeFile, bool, error) {
	cleaned := filepath.Clean(filePath)
	if file, found := liveFiles[cleaned]; found {
		return file, true, nil
	}
	return idx.adminTypeFile(cleaned)
}

func (idx *AdminComponentIndexer) allAdminTypeFiles(
	liveFiles map[string]AdminTypeFile,
) ([]AdminTypeFile, error) {
	files, err := idx.typeIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	if len(liveFiles) == 0 {
		return files, nil
	}
	result := make([]AdminTypeFile, 0, len(files)+len(liveFiles))
	seen := make(map[string]bool, len(files)+len(liveFiles))
	for _, file := range files {
		path := filepath.Clean(file.FilePath)
		if _, replaced := liveFiles[path]; replaced {
			continue
		}
		seen[path] = true
		result = append(result, file)
	}
	for path, file := range liveFiles {
		if seen[path] {
			continue
		}
		result = append(result, file)
	}
	return result, nil
}

func adminTypeDeclarationNamed(
	file AdminTypeFile,
	name string,
) (AdminTypeDeclaration, bool) {
	for _, declaration := range file.Declarations {
		if declaration.Name == name {
			return declaration, true
		}
	}
	return AdminTypeDeclaration{}, false
}

func adminTypeDefaultDeclaration(
	file AdminTypeFile,
) (AdminTypeDeclaration, bool) {
	for _, declaration := range file.Declarations {
		if declaration.Default {
			return declaration, true
		}
	}
	return AdminTypeDeclaration{}, false
}

func parseAdminNamedType(value string) (string, []string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	end := 0
	for end < len(value) && (isSlotIdentifierContinue(value[end]) ||
		value[end] == '.') {
		end++
	}
	if end == len(value) && isAdminTypeQualifiedIdentifier(value) {
		return value, nil
	}
	if end == 0 || end >= len(value) || value[end] != '<' {
		return "", nil
	}
	close := matchingSlotDelimiter(value, end, '<', '>')
	if close != len(value)-1 {
		return "", nil
	}
	return value[:end], splitAdminTypeTopLevel(value[end+1:close], ',')
}

func isAdminTypeQualifiedIdentifier(value string) bool {
	for _, part := range strings.Split(value, ".") {
		if !isAdminTypeIdentifier(part) {
			return false
		}
	}
	return value != ""
}

func adminTypeGenericInner(value, wrapper string) (string, bool) {
	prefix := wrapper + "<"
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	open := len(wrapper)
	close := matchingSlotDelimiter(value, open, '<', '>')
	if close != len(value)-1 {
		return "", false
	}
	return strings.TrimSpace(value[open+1 : close]), true
}

func trimAdminTypeParentheses(value string) string {
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func splitAdminTypeTopLevel(value string, separator byte) []string {
	var result []string
	start := 0
	state := declarationScanState{}
	angles := 0
	for index := 0; index < len(value); index++ {
		state.consume(value, &index)
		if state.inLiteralOrComment() {
			continue
		}
		switch value[index] {
		case '{':
			state.braces++
		case '}':
			if state.braces > 0 {
				state.braces--
			}
		case '(':
			state.parens++
		case ')':
			if state.parens > 0 {
				state.parens--
			}
		case '[':
			state.brackets++
		case ']':
			if state.brackets > 0 {
				state.brackets--
			}
		case '<':
			angles++
		case '>':
			if angles > 0 {
				angles--
			}
		}
		if value[index] == separator && state.braces == 0 &&
			state.parens == 0 && state.brackets == 0 && angles == 0 {
			result = append(result, strings.TrimSpace(value[start:index]))
			start = index + 1
		}
	}
	result = append(result, strings.TrimSpace(value[start:]))
	return result
}

func substituteAdminType(value string, substitutions map[string]string) string {
	if len(substitutions) == 0 || value == "" {
		return value
	}
	var builder strings.Builder
	for index := 0; index < len(value); {
		if !isSlotIdentifierStart(value[index]) {
			builder.WriteByte(value[index])
			index++
			continue
		}
		end := index + 1
		for end < len(value) && isSlotIdentifierContinue(value[end]) {
			end++
		}
		name := value[index:end]
		if replacement := substitutions[name]; replacement != "" {
			builder.WriteString(replacement)
		} else {
			builder.WriteString(name)
		}
		index = end
	}
	return builder.String()
}

func substituteVueTypeMembers(
	members []TwigVueMember,
	substitutions map[string]string,
) []TwigVueMember {
	result := append([]TwigVueMember(nil), members...)
	for index := range result {
		result[index].Type = substituteAdminType(result[index].Type, substitutions)
		result[index].NestedMembers = substituteVueTypeMembers(
			result[index].NestedMembers, substitutions,
		)
	}
	return result
}

func unionVueTypeMembers(shapes []VueTypeShape) []TwigVueMember {
	if len(shapes) == 0 {
		return nil
	}
	var result []TwigVueMember
	positions := make(map[string]int)
	presence := make(map[string]int)
	for _, shape := range shapes {
		seen := make(map[string]bool, len(shape.Members))
		for _, member := range shape.Members {
			if member.Name == "" || seen[member.Name] {
				continue
			}
			seen[member.Name] = true
			presence[member.Name]++
			if position, found := positions[member.Name]; found {
				if result[position].Documentation == "" {
					result[position].Documentation = member.Documentation
				}
				result[position].Type = mergeVueTypes(
					result[position].Type, member.Type,
				)
				result[position].Optional =
					result[position].Optional || member.Optional
				continue
			}
			positions[member.Name] = len(result)
			result = append(result, member)
		}
	}
	for index := range result {
		if presence[result[index].Name] < len(shapes) {
			result[index].Type = mergeVueTypes(
				result[index].Type, "undefined",
			)
			result[index].Optional = true
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func adminPrimitiveType(_ string) bool {
	// Primitive built-ins with a known surface are handled before this fallback.
	// Unresolved, nullish, and top types do not provide a closed object shape
	// suitable for unknown-member diagnostics. In particular, null and empty
	// values are common initial Vue data seeds that are populated at runtime.
	return false
}

func adminObjectTypeHasIndexSignature(value string, open int) bool {
	if open < 0 || open >= len(value) || value[open] != '{' {
		return false
	}
	close := balancedBraceEnd(value, open)
	if close <= open {
		return false
	}
	state := slotScanState{}
	for index := open + 1; index < close; index++ {
		if state.topLevel() && value[index] == '[' {
			bracketClose := matchingSlotDelimiter(value, index, '[', ']')
			if bracketClose > index && bracketClose < close {
				cursor := bracketClose + 1
				for cursor < close && isSlotSpace(value[cursor]) {
					cursor++
				}
				if cursor < close && value[cursor] == ':' {
					return true
				}
			}
		}
		state.consume(value[index])
	}
	return false
}
