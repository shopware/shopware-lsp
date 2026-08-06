package phprewrite

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

// SetExtends adds or replaces the parent of a class declaration.
func (e *Editor) SetExtends(class *phpsyntax.Node, parent string) error {
	class = phpquery.ClassAt(class)
	parent = strings.TrimSpace(parent)
	if e == nil || e.builder == nil {
		return fmt.Errorf("set PHP parent: editor is nil")
	}
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return fmt.Errorf("set PHP parent: class declaration is unavailable")
	}
	if parent == "" {
		return fmt.Errorf("set PHP parent: parent is empty")
	}
	if clause := directNode(class, phpsyntax.PhpExtendsClause); clause != nil {
		if nodeHasComment(clause) {
			return fmt.Errorf("set PHP parent: extends clause contains comments")
		}
		return e.builder.ReplaceRange(clause.RangeTrimmedTrivia(), "extends "+parent)
	}
	anchor := directNode(class, phpsyntax.PhpImplementsClause)
	if anchor == nil {
		anchor = directNode(class, phpsyntax.PhpClassBody)
	}
	if anchor == nil {
		return fmt.Errorf("set PHP parent: class body is unavailable")
	}
	return e.builder.Insert(anchor.RangeTrimmedTrivia().Start, "extends "+parent+" ")
}

// RemoveExtends removes a class parent declaration when present.
func (e *Editor) RemoveExtends(class *phpsyntax.Node) (bool, error) {
	class = phpquery.ClassAt(class)
	if e == nil || e.builder == nil {
		return false, fmt.Errorf("remove PHP parent: editor is nil")
	}
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return false, fmt.Errorf("remove PHP parent: class declaration is unavailable")
	}
	clause := directNode(class, phpsyntax.PhpExtendsClause)
	if clause == nil {
		return false, nil
	}
	if nodeHasComment(clause) {
		return false, fmt.Errorf("remove PHP parent: extends clause contains comments")
	}
	return true, e.builder.ReplaceRange(clause.Range(), "")
}

// AddAttribute inserts a native PHP attribute before a declaration.
func (e *Editor) AddAttribute(owner *phpsyntax.Node, attribute string) error {
	attribute = strings.TrimSpace(attribute)
	if e == nil || e.builder == nil {
		return fmt.Errorf("add PHP attribute: editor is nil")
	}
	if owner == nil {
		return fmt.Errorf("add PHP attribute: declaration is unavailable")
	}
	if attribute == "" {
		return fmt.Errorf("add PHP attribute: attribute is empty")
	}
	if !strings.HasPrefix(attribute, "#[") {
		attribute = "#[" + strings.Trim(attribute, "[]#") + "]"
	}
	start := owner.RangeTrimmedTrivia().Start
	line := lineStart(e.source, start)
	indent, ok := whitespacePrefix(e.source, line, start)
	if !ok {
		indent = ""
	}
	return e.builder.Insert(start, attribute+"\n"+indent)
}

func (e *Editor) AddImplements(class *phpsyntax.Node, interfaceName string) error {
	class = phpquery.ClassAt(class)
	interfaceName = strings.TrimSpace(interfaceName)
	if e == nil || e.builder == nil {
		return fmt.Errorf("add PHP interface: editor is nil")
	}
	if class == nil || (class.Kind() != phpsyntax.PhpClassDeclaration && class.Kind() != phpsyntax.PhpEnumDeclaration) {
		return fmt.Errorf("add PHP interface: class declaration is unavailable")
	}
	if interfaceName == "" {
		return fmt.Errorf("add PHP interface: interface is empty")
	}
	if clause := directNode(class, phpsyntax.PhpImplementsClause); clause != nil {
		if nodeHasComment(clause) {
			return fmt.Errorf("add PHP interface: implements clause contains comments")
		}
		names := directNodes(clause, phpsyntax.PhpName)
		if len(names) == 0 {
			return fmt.Errorf("add PHP interface: implements clause has no names")
		}
		for _, name := range names {
			if samePHPName(name.Text(), interfaceName) {
				return nil
			}
		}
		return e.builder.Insert(names[len(names)-1].RangeTrimmedTrivia().End, ", "+interfaceName)
	}
	body := directNode(class, phpsyntax.PhpClassBody)
	if body == nil {
		return fmt.Errorf("add PHP interface: class body is unavailable")
	}
	return e.builder.Insert(body.RangeTrimmedTrivia().Start, "implements "+interfaceName+" ")
}

func (e *Editor) RemoveImplements(class *phpsyntax.Node, interfaceName string) (bool, error) {
	class = phpquery.ClassAt(class)
	interfaceName = strings.TrimSpace(interfaceName)
	if e == nil || e.builder == nil {
		return false, fmt.Errorf("remove PHP interface: editor is nil")
	}
	if class == nil {
		return false, fmt.Errorf("remove PHP interface: class declaration is unavailable")
	}
	clause := directNode(class, phpsyntax.PhpImplementsClause)
	if clause == nil {
		return false, nil
	}
	if nodeHasComment(clause) {
		return false, fmt.Errorf("remove PHP interface: implements clause contains comments")
	}
	names := directNodes(clause, phpsyntax.PhpName)
	match := -1
	for index, name := range names {
		if samePHPName(name.Text(), interfaceName) {
			match = index
			break
		}
	}
	if match < 0 {
		return false, nil
	}
	if len(names) == 1 {
		return true, e.builder.ReplaceRange(clause.Range(), "")
	}
	current := names[match].RangeTrimmedTrivia()
	if match+1 < len(names) {
		next := names[match+1].RangeTrimmedTrivia()
		return true, e.builder.ReplaceRange(cst.TextRange{Start: current.Start, End: next.Start}, "")
	}
	previous := names[match-1].RangeTrimmedTrivia()
	return true, e.builder.ReplaceRange(cst.TextRange{Start: previous.End, End: current.End}, "")
}

// InsertClassMember inserts a declaration before the closing class brace and
// indents every non-empty line to match the surrounding class body.
func (e *Editor) InsertClassMember(class *phpsyntax.Node, declaration string) error {
	class = phpquery.ClassAt(class)
	if e == nil || e.builder == nil {
		return fmt.Errorf("insert PHP class member: editor is nil")
	}
	if class == nil {
		return fmt.Errorf("insert PHP class member: class declaration is unavailable")
	}
	body := phpquery.ClassBody(class)
	if body == nil {
		return fmt.Errorf("insert PHP class member: class body is unavailable")
	}
	close := body.ChildTokenOfKind(phpsyntax.TkCloseBrace)
	open := body.ChildTokenOfKind(phpsyntax.TkOpenBrace)
	if open == nil || close == nil {
		return fmt.Errorf("insert PHP class member: class braces are unavailable")
	}
	declaration = dedent(declaration)
	if declaration == "" {
		return fmt.Errorf("insert PHP class member: declaration is empty")
	}
	closeLineStart := lineStart(e.source, close.Range().Start)
	closeIndent, closeOnOwnLine := whitespacePrefix(e.source, closeLineStart, close.Range().Start)
	memberIndent := closeIndent + "    "
	for index := 0; index < body.ChildCount(); index++ {
		member, ok := body.Child(index).(*phpsyntax.Node)
		if !ok || !isClassMember(member.Kind()) {
			continue
		}
		start := member.RangeTrimmedTrivia().Start
		if indent, ok := whitespacePrefix(e.source, lineStart(e.source, start), start); ok {
			memberIndent = indent
		}
		break
	}
	formatted := indentBlock(declaration, memberIndent)
	if closeOnOwnLine && closeLineStart > open.Range().End {
		return e.builder.Insert(closeLineStart, formatted+"\n")
	}
	return e.builder.Insert(close.Range().Start, "\n"+formatted+"\n"+closeIndent)
}

func (e *Editor) RemoveClassMember(member *phpsyntax.Node) error {
	if e == nil || e.builder == nil {
		return fmt.Errorf("remove PHP class member: editor is nil")
	}
	if member == nil || !isClassMember(member.Kind()) ||
		member.Parent() == nil || member.Parent().Kind() != phpsyntax.PhpClassBody {
		return fmt.Errorf("remove PHP class member: direct class member is unavailable")
	}
	trimmed := member.RangeTrimmedTrivia()
	ownedStart := trimmed.Start
	if doc := leadingPHPDoc(member); doc != nil {
		ownedStart = doc.Range().Start
	}
	start := lineStart(e.source, ownedStart)
	if _, ok := whitespacePrefix(e.source, start, ownedStart); !ok {
		start = member.Range().Start
	}
	body := member.Parent()
	end := trimmed.End
	found := false
	for index := 0; index < body.ChildCount(); index++ {
		child, ok := body.Child(index).(*phpsyntax.Node)
		if !ok || !isClassMember(child.Kind()) {
			continue
		}
		if found {
			nextStart := child.RangeTrimmedTrivia().Start
			candidate := lineStart(e.source, nextStart)
			if _, ok := whitespacePrefix(e.source, candidate, nextStart); ok && candidate >= end {
				end = candidate
			}
			break
		}
		if child == member {
			found = true
		}
	}
	if found && end == trimmed.End {
		if close := body.ChildTokenOfKind(phpsyntax.TkCloseBrace); close != nil {
			candidate := lineStart(e.source, close.Range().Start)
			if _, ok := whitespacePrefix(e.source, candidate, close.Range().Start); ok && candidate >= end {
				end = candidate
			}
		}
	}
	if start > end {
		return fmt.Errorf("remove PHP class member: invalid member range")
	}
	return e.builder.ReplaceRange(cst.TextRange{Start: start, End: end}, "")
}

func nodeHasComment(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	for element := range node.Descendants() {
		token, ok := element.(*phpsyntax.Token)
		if !ok {
			continue
		}
		if token.Kind() == phpsyntax.TkLineComment || token.Kind() == phpsyntax.TkBlockComment {
			return true
		}
	}
	return false
}

func samePHPName(left, right string) bool {
	normalize := func(value string) string {
		value = strings.ReplaceAll(value, " ", "")
		value = strings.ReplaceAll(value, "\t", "")
		return strings.Trim(value, "\\")
	}
	return strings.EqualFold(normalize(left), normalize(right))
}

func isClassMember(kind phpsyntax.Kind) bool {
	switch kind {
	case phpsyntax.PhpMethodDeclaration, phpsyntax.PhpPropertyDeclaration,
		phpsyntax.PhpClassConstDeclaration, phpsyntax.PhpEnumCaseDeclaration,
		phpsyntax.PhpTraitUseDeclaration:
		return true
	default:
		return false
	}
}

func dedent(source string) string {
	lines := strings.Split(strings.Trim(source, "\r\n"), "\n")
	for len(lines) != 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) != 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	minimum := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minimum < 0 || indent < minimum {
			minimum = indent
		}
	}
	if minimum > 0 {
		for index := range lines {
			if len(lines[index]) >= minimum {
				lines[index] = lines[index][minimum:]
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func indentBlock(source, indent string) string {
	lines := strings.Split(source, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[index] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}
