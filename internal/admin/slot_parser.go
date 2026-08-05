package admin

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	twigindex "github.com/shopware/shopware-lsp/internal/twig"
)

// TemplateParseResult contains slots and blocks extracted from a template
type TemplateParseResult struct {
	Slots  []VueComponentSlot
	Blocks []TwigBlock
}

// ParseSlotsFromTemplate parses slot definitions from a Twig template file
// It looks for <slot> and <slot name="..."> tags and returns slot info with line numbers
func ParseSlotsFromTemplate(templatePath string) ([]VueComponentSlot, error) {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}

	result := parseTemplateContent(string(content))
	setTemplateSourcePaths(&result, templatePath)
	return result.Slots, nil
}

// ParseTemplateFromFile parses both slots and blocks from a Twig template file
func ParseTemplateFromFile(templatePath string) (*TemplateParseResult, error) {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}

	result := parseTemplateContent(string(content))
	setTemplateSourcePaths(&result, templatePath)
	return &result, nil
}

func setTemplateSourcePaths(result *TemplateParseResult, templatePath string) {
	if result == nil {
		return
	}
	for index := range result.Slots {
		result.Slots[index].FilePath = templatePath
		for memberIndex := range result.Slots[index].Members {
			result.Slots[index].Members[memberIndex].FilePath = templatePath
		}
	}
	for index := range result.Blocks {
		result.Blocks[index].FilePath = templatePath
		for memberIndex := range result.Blocks[index].ScopeMembers {
			result.Blocks[index].ScopeMembers[memberIndex].FilePath = templatePath
		}
	}
}

// TwigBlockNameAt reports an opening block declaration name. Closing
// `{% endblock name %}` tokens deliberately do not resolve as extension
// declarations.
func TwigBlockNameAt(
	node *twigsyntax.Node,
	token *twigsyntax.Token,
) (string, bool) {
	if node == nil || token == nil {
		return "", false
	}
	blockNode := twigquery.BlockAt(node)
	block, ok := twigast.CastTwigBlock(blockNode)
	if !ok || block.Name() == nil || block.Name().Range() != token.Range() {
		return "", false
	}
	return block.Name().Text(), block.Name().Text() != ""
}

// TwigBlockScopeMemberAt resolves an implicit lexical input of the enclosing
// extensibility block. Effective components carry the scope contract inherited
// from the parent template even when the current override only contains the
// replacement block body.
func TwigBlockScopeMemberAt(
	component VueComponent,
	node *twigsyntax.Node,
	name string,
) (TwigBlockScopeMember, TwigBlock, bool) {
	if node == nil || name == "" {
		return TwigBlockScopeMember{}, TwigBlock{}, false
	}
	blockName := twigquery.BlockName(twigquery.BlockAt(node))
	block, found := component.ComponentBlock(blockName)
	if !found {
		return TwigBlockScopeMember{}, TwigBlock{}, false
	}
	member, found := block.ScopeMember(name)
	return member, block, found
}

// IsTwigBlockNameCompletionAt reports a cursor within or immediately after an
// opening block name, including a partially typed declaration.
func IsTwigBlockNameCompletionAt(node *twigsyntax.Node, offset uint32) bool {
	if node == nil {
		return false
	}
	blockNode := twigquery.BlockAt(node)
	block, ok := twigast.CastTwigBlock(blockNode)
	if !ok || block.Name() == nil {
		return false
	}
	nameRange := block.Name().Range()
	return offset >= nameRange.Start && offset <= nameRange.End
}

// parseTemplateContent extracts slots and blocks from template content
func parseTemplateContent(content string) TemplateParseResult {
	parsed := twigparser.Parse(content)
	return parseTemplateTree(
		parsed.Tree.Root, content, cst.NewLineIndex(content),
	)
}

// parseTemplateTree extracts the public template contract from an existing
// Twig CST. Vue SFCs use this path so embedded nodes keep their absolute file
// ranges and do not need to be parsed a second time or translated through an
// offset map.
func parseTemplateTree(
	root *twigsyntax.Node,
	content string,
	lineIndex *cst.LineIndex,
) TemplateParseResult {
	var result TemplateParseResult
	if root == nil {
		return result
	}
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(content)
	}
	slotPositions := make(map[string]int)
	seenBlocks := make(map[string]bool)
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		tag, ok := twigast.CastHtmlStartingTag(node)
		if !ok || tag.Name() == nil || tag.Name().Text() != "slot" {
			continue
		}
		slot, skipped := slotDeclarationIdentity(tag, lineIndex)
		if skipped {
			continue
		}
		line, _ := lineIndex.Position(tag.Name().Range().Start)
		slot.Line = int(line) + 1
		slot.Members, slot.MembersComplete = slotDeclarationMembers(
			tag, lineIndex,
		)
		key := slot.identityKey()
		if position, exists := slotPositions[key]; exists {
			result.Slots[position].Members = overlaySlotMembers(
				result.Slots[position].Members,
				slot.Members,
			)
			result.Slots[position].MembersComplete =
				result.Slots[position].MembersComplete && slot.MembersComplete
			continue
		}
		slotPositions[key] = len(result.Slots)
		result.Slots = append(result.Slots, slot)
	}
	for _, node := range twigquery.Nodes(root, twigsyntax.TwigBlock) {
		blockName := twigquery.BlockName(node)
		if blockName == "" || seenBlocks[blockName] {
			continue
		}
		seenBlocks[blockName] = true
		block, ok := twigast.CastTwigBlock(node)
		if !ok || block.Name() == nil {
			continue
		}
		nameRange := block.Name().Range()
		line, character := lineIndex.PositionUTF16(nameRange.Start)
		endLine, endCharacter := lineIndex.PositionUTF16(nameRange.End)
		result.Blocks = append(result.Blocks, TwigBlock{
			Name:       blockName,
			Deprecated: twigindex.BlockDeprecation(node, content),
			Line:       int(line) + 1,
			ScopeMembers: twigBlockScopeMembers(
				root, []byte(content), nameRange.Start, lineIndex,
			),
			NameRange: AdminSourceRange{
				StartLine: int(line), StartCharacter: int(character),
				EndLine: int(endLine), EndCharacter: int(endCharacter),
				Declaration: true, Identifier: true,
			},
		})
	}
	return result
}

func twigBlockScopeMembers(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	lineIndex *cst.LineIndex,
) []TwigBlockScopeMember {
	positions := make(map[string]int)
	var result []TwigBlockScopeMember
	add := func(name, memberType string, rangeValue cst.TextRange) {
		if name == "" {
			return
		}
		member := TwigBlockScopeMember{Name: name, Type: memberType}
		if rangeValue.Len() > 0 && lineIndex != nil {
			startLine, startCharacter := lineIndex.PositionUTF16(rangeValue.Start)
			endLine, endCharacter := lineIndex.PositionUTF16(rangeValue.End)
			member.Line = int(startLine) + 1
			member.NameRange = AdminSourceRange{
				StartLine: int(startLine), StartCharacter: int(startCharacter),
				EndLine: int(endLine), EndCharacter: int(endCharacter),
				Declaration: true, Identifier: true,
			}
		}
		if position, exists := positions[name]; exists {
			result[position] = member
			return
		}
		positions[name] = len(result)
		result = append(result, member)
	}
	for _, binding := range TwigVueBindingsAtOffset(root, content, offset) {
		add(binding.Name, binding.Type, binding.DeclarationRange)
	}
	for _, scope := range TwigScopedSlotsAtOffset(root, offset) {
		for _, binding := range scope.Bindings {
			add(binding.LocalName, "", binding.LocalRange)
		}
	}
	return result
}

func slotDeclarationIdentity(
	tag twigast.HtmlStartingTag,
	lineIndex *cst.LineIndex,
) (VueComponentSlot, bool) {
	nameToken := tag.Name()
	if nameToken == nil {
		return VueComponentSlot{}, true
	}
	result := VueComponentSlot{
		Name: "default",
		NameRange: sourceRangeAt(
			lineIndex, nameToken.Range().Start, nameToken.Range().End, false,
		),
	}
	for _, attribute := range tag.Attributes() {
		attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
		switch attributeName {
		case ":name", "v-bind:name":
			value, valueRange, ok := staticHTMLAttributeValueRange(attribute)
			if !ok {
				return VueComponentSlot{}, true
			}
			name, prefix, suffix, resolvable := slotBoundName(value)
			if !resolvable {
				return VueComponentSlot{}, true
			}
			result.Name = name
			result.NamePrefix = prefix
			result.NameSuffix = suffix
			if name != "" && len(value) >= 2 &&
				(value[0] == value[len(value)-1]) &&
				strings.ContainsRune("'\"`", rune(value[0])) {
				valueRange.Start++
				valueRange.End--
			}
			result.NameRange = sourceRangeAt(
				lineIndex, valueRange.Start, valueRange.End, false,
			)
		case "name":
			value, valueRange, ok := staticHTMLAttributeValueRange(attribute)
			if !ok || value == "" {
				return VueComponentSlot{}, true
			}
			result.Name = value
			result.NamePrefix = ""
			result.NameSuffix = ""
			result.NameRange = sourceRangeAt(
				lineIndex, valueRange.Start, valueRange.End, false,
			)
		}
	}
	return result, false
}

// SlotDeclaration returns the statically known identity and payload contract
// for one live <slot> tag. Runtime-only forwarded names remain excluded.
func SlotDeclaration(
	tag twigast.HtmlStartingTag,
	lineIndex *cst.LineIndex,
) (VueComponentSlot, bool) {
	slot, skipped := slotDeclarationIdentity(tag, lineIndex)
	if skipped {
		return VueComponentSlot{}, false
	}
	slot.Members, slot.MembersComplete = slotDeclarationMembers(tag, lineIndex)
	return slot, true
}

// slotBoundName accepts only identities whose complete static shape is known.
// Arbitrary forwarding (:name="name") remains excluded. A single template
// interpolation is represented as a prefix/suffix family.
func slotBoundName(value string) (string, string, string, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 &&
		((value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"')) {
		name := value[1 : len(value)-1]
		return name, "", "", name != "" && isSlotNameLiteral(name)
	}
	if len(value) < 2 || value[0] != '`' || value[len(value)-1] != '`' {
		return "", "", "", false
	}
	body := value[1 : len(value)-1]
	interpolation := strings.Index(body, "${")
	if interpolation < 0 {
		return body, "", "", body != "" && isSlotNameLiteral(body)
	}
	open := interpolation + 2 // body index immediately after the opening '{'
	close := matchingSlotDelimiter(body, open-1, '{', '}')
	if close < 0 || strings.Contains(body[close+1:], "${") {
		return "", "", "", false
	}
	prefix := body[:interpolation]
	suffix := body[close+1:]
	if (prefix == "" && suffix == "") || !isSlotNameLiteral(prefix) ||
		!isSlotNameLiteral(suffix) {
		return "", "", "", false
	}
	return "", prefix, suffix, true
}

func isSlotNameLiteral(value string) bool {
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current >= 'a' && current <= 'z' ||
			current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' ||
			strings.ContainsRune("_.:-", rune(current)) {
			continue
		}
		return false
	}
	return true
}

func slotDeclarationMembers(
	tag twigast.HtmlStartingTag,
	lineIndex *cst.LineIndex,
) ([]VueComponentSlotMember, bool) {
	var members []VueComponentSlotMember
	complete := true
	for _, attribute := range tag.Attributes() {
		attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
		if attributeName == "v-bind" {
			if value, valueRange, ok := staticHTMLAttributeValueRange(attribute); ok &&
				attribute.Name() != nil {
				fields, objectComplete := VueObjectBindingFields(
					value, valueRange.Start,
				)
				complete = complete && objectComplete
				for _, field := range fields {
					line, _ := lineIndex.Position(field.NameRange.Start)
					members = appendSlotMember(members, VueComponentSlotMember{
						Name: field.Name, Line: int(line) + 1,
						NameRange: sourceRangeAt(
							lineIndex, field.NameRange.Start,
							field.NameRange.End, false,
						),
					})
				}
			} else {
				complete = false
			}
			continue
		}
		if attributeName == "name" || attributeName == ":name" ||
			attributeName == "v-bind:name" {
			continue
		}
		if attribute.Name() == nil {
			continue
		}
		prop, found := VuePropReferenceForAttribute(
			attributeName, attribute.Name().Range(),
		)
		if !found || prop.Name == "" {
			continue
		}
		line, _ := lineIndex.Position(attribute.Name().Range().Start)
		members = appendSlotMember(members, VueComponentSlotMember{
			Name: prop.Name, Line: int(line) + 1,
			NameRange: sourceRangeAt(
				lineIndex, prop.Range.Start, prop.Range.End, false,
			),
		})
	}
	return members, complete
}

func staticHTMLAttributeValueRange(
	attribute twigast.HtmlAttribute,
) (string, cst.TextRange, bool) {
	value, ok := attribute.Value()
	if !ok {
		return "", cst.TextRange{}, false
	}
	inner, ok := value.GetInner()
	if !ok {
		return "", cst.TextRange{}, false
	}
	raw := inner.Syntax().Text()
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", cst.TextRange{}, false
	}
	start := strings.Index(raw, trimmed)
	if start < 0 {
		return "", cst.TextRange{}, false
	}
	rangeValue := inner.Syntax().Range()
	rangeValue.Start += uint32(start)
	rangeValue.End = rangeValue.Start + uint32(len(trimmed))
	return trimmed, rangeValue, true
}

func appendSlotMember(
	members []VueComponentSlotMember,
	member VueComponentSlotMember,
) []VueComponentSlotMember {
	if member.Name == "" {
		return members
	}
	for index := range members {
		if members[index].Name == member.Name {
			if members[index].Type == "" {
				members[index].Type = member.Type
			}
			if members[index].FilePath == "" {
				members[index].FilePath = member.FilePath
				members[index].Line = member.Line
			}
			if members[index].NameRange == (AdminSourceRange{}) &&
				members[index].FilePath == member.FilePath {
				members[index].NameRange = member.NameRange
			}
			return members
		}
	}
	return append(members, member)
}

func overlaySlotMembers(
	base,
	overlay []VueComponentSlotMember,
) []VueComponentSlotMember {
	result := append([]VueComponentSlotMember(nil), base...)
	for _, member := range overlay {
		result = appendSlotMember(result, member)
	}
	return result
}

// parseSlotsFromContent extracts slot names and line numbers from template content (for tests)
func parseSlotsFromContent(content string) []VueComponentSlot {
	return parseTemplateContent(content).Slots
}

// ResolveTemplatePath resolves the template import path to an absolute file path
// relative to the component definition file
func ResolveTemplatePath(definitionPath, templateImport string) string {
	if templateImport == "" {
		return ""
	}

	dir := filepath.Dir(definitionPath)

	// Handle relative paths
	if strings.HasPrefix(templateImport, "./") || strings.HasPrefix(templateImport, "../") {
		return filepath.Join(dir, templateImport)
	}

	// If it's just a filename, assume it's in the same directory
	return filepath.Join(dir, templateImport)
}
