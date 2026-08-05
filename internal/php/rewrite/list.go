package phprewrite

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func (e *Editor) InsertArgument(container *phpsyntax.Node, index int, text string) error {
	list := argumentList(container)
	return e.insertCommaListItem(list, argumentItems(list), index, text)
}

func (e *Editor) AppendArgument(container *phpsyntax.Node, text string) error {
	list := argumentList(container)
	items := argumentItems(list)
	return e.insertCommaListItem(list, items, len(items), text)
}

func (e *Editor) RemoveArgument(container *phpsyntax.Node, index int) error {
	list := argumentList(container)
	return e.removeCommaListItem(list, argumentItems(list), index)
}

func (e *Editor) InsertParameter(functionLike *phpsyntax.Node, index int, text string) error {
	list := parameterList(functionLike)
	return e.insertCommaListItem(list, parameterItems(list), index, text)
}

func (e *Editor) AppendParameter(functionLike *phpsyntax.Node, text string) error {
	list := parameterList(functionLike)
	items := parameterItems(list)
	return e.insertCommaListItem(list, items, len(items), text)
}

func (e *Editor) RemoveParameter(functionLike *phpsyntax.Node, index int) error {
	list := parameterList(functionLike)
	return e.removeCommaListItem(list, parameterItems(list), index)
}

func argumentList(container *phpsyntax.Node) *phpsyntax.Node {
	if container == nil {
		return nil
	}
	if container.Kind() == phpsyntax.PhpArgumentList {
		return container
	}
	if call := phpquery.CallAt(container); call != nil {
		return directNode(call, phpsyntax.PhpArgumentList)
	}
	switch container.Kind() {
	case phpsyntax.PhpAttribute, phpsyntax.PhpObjectCreation:
		return directNode(container, phpsyntax.PhpArgumentList)
	default:
		return nil
	}
}

func parameterList(functionLike *phpsyntax.Node) *phpsyntax.Node {
	if functionLike == nil {
		return nil
	}
	if functionLike.Kind() == phpsyntax.PhpParameterList {
		return functionLike
	}
	for current := functionLike; current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpMethodDeclaration, phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpClosure, phpsyntax.PhpArrowFunction:
			return directNode(current, phpsyntax.PhpParameterList)
		}
	}
	return nil
}

func argumentItems(list *phpsyntax.Node) []*phpsyntax.Node {
	return directNodes(list, phpsyntax.PhpArgument, phpsyntax.PhpNamedArgument)
}

func parameterItems(list *phpsyntax.Node) []*phpsyntax.Node {
	return directNodes(list, phpsyntax.PhpParameter)
}

func directNodes(parent *phpsyntax.Node, kinds ...phpsyntax.Kind) []*phpsyntax.Node {
	if parent == nil {
		return nil
	}
	var result []*phpsyntax.Node
	for index := 0; index < parent.ChildCount(); index++ {
		child, ok := parent.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		for _, kind := range kinds {
			if child.Kind() == kind {
				result = append(result, child)
				break
			}
		}
	}
	return result
}

func (e *Editor) insertCommaListItem(
	list *phpsyntax.Node,
	items []*phpsyntax.Node,
	index int,
	text string,
) error {
	if e == nil || e.builder == nil {
		return fmt.Errorf("insert PHP list item: editor is nil")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("insert PHP list item: text is empty")
	}
	if list == nil {
		return fmt.Errorf("insert PHP list item: list is unavailable")
	}
	if index < 0 || index > len(items) {
		return fmt.Errorf("insert PHP list item: index %d outside 0..%d", index, len(items))
	}
	open := list.ChildTokenOfKind(phpsyntax.TkOpenParen)
	close := list.ChildTokenOfKind(phpsyntax.TkCloseParen)
	if open == nil || close == nil {
		return fmt.Errorf("insert PHP list item: delimiters are unavailable")
	}
	multiline := strings.Contains(e.source[open.Range().End:close.Range().Start], "\n")
	if index < len(items) {
		current := items[index].RangeTrimmedTrivia()
		if rangeHasToken(
			list,
			items[index].Range().Start,
			current.Start,
			phpsyntax.TkLineComment,
			phpsyntax.TkBlockComment,
		) {
			return fmt.Errorf("insert PHP list item: target item has a leading comment")
		}
		if multiline {
			start := lineStart(e.source, current.Start)
			if indent, ok := whitespacePrefix(e.source, start, current.Start); ok && start > open.Range().Start {
				return e.builder.Insert(start, indent+text+",\n")
			}
		}
		return e.builder.Insert(current.Start, text+", ")
	}

	if !multiline {
		if len(items) == 0 {
			return e.builder.Insert(close.Range().Start, text)
		}
		lastEnd := items[len(items)-1].RangeTrimmedTrivia().End
		suffix := e.source[lastEnd:close.Range().Start]
		if rangeHasToken(list, lastEnd, close.Range().Start, phpsyntax.TkComma) {
			prefix := ""
			if suffix == "" || !isWhitespaceByte(suffix[len(suffix)-1]) {
				prefix = " "
			}
			return e.builder.Insert(close.Range().Start, prefix+text)
		}
		return e.builder.Insert(close.Range().Start, ", "+text)
	}

	closeLineStart := lineStart(e.source, close.Range().Start)
	closeIndent, closeOnOwnLine := whitespacePrefix(e.source, closeLineStart, close.Range().Start)
	if !closeOnOwnLine || closeLineStart <= open.Range().Start {
		return e.builder.Insert(close.Range().Start, ", "+text)
	}
	itemIndent := closeIndent + "    "
	if len(items) != 0 {
		last := items[len(items)-1].RangeTrimmedTrivia()
		lastLineStart := lineStart(e.source, last.Start)
		if indent, ok := whitespacePrefix(e.source, lastLineStart, last.Start); ok {
			itemIndent = indent
		}
		if !rangeHasToken(list, last.End, close.Range().Start, phpsyntax.TkComma) {
			if err := e.builder.Insert(last.End, ","); err != nil {
				return err
			}
		}
	}
	return e.builder.Insert(closeLineStart, itemIndent+text+",\n")
}

func (e *Editor) removeCommaListItem(
	list *phpsyntax.Node,
	items []*phpsyntax.Node,
	index int,
) error {
	if e == nil || e.builder == nil {
		return fmt.Errorf("remove PHP list item: editor is nil")
	}
	if list == nil {
		return fmt.Errorf("remove PHP list item: list is unavailable")
	}
	if index < 0 || index >= len(items) {
		return fmt.Errorf("remove PHP list item: index %d outside 0..%d", index, len(items)-1)
	}
	open := list.ChildTokenOfKind(phpsyntax.TkOpenParen)
	close := list.ChildTokenOfKind(phpsyntax.TkCloseParen)
	if open == nil || close == nil {
		return fmt.Errorf("remove PHP list item: delimiters are unavailable")
	}
	if len(items) == 1 {
		return e.builder.ReplaceRange(cst.TextRange{
			Start: open.Range().End,
			End:   close.Range().Start,
		}, "")
	}
	current := items[index].RangeTrimmedTrivia()
	if index+1 < len(items) {
		next := items[index+1].RangeTrimmedTrivia()
		if rangeHasToken(
			list,
			current.End,
			next.Start,
			phpsyntax.TkLineComment,
			phpsyntax.TkBlockComment,
		) {
			return fmt.Errorf("remove PHP list item: separator contains a comment")
		}
		return e.builder.ReplaceRange(cst.TextRange{Start: current.Start, End: next.Start}, "")
	}

	previous := items[index-1].RangeTrimmedTrivia()
	if rangeHasToken(
		list,
		previous.End,
		current.Start,
		phpsyntax.TkLineComment,
		phpsyntax.TkBlockComment,
	) {
		return fmt.Errorf("remove PHP list item: separator contains a comment")
	}
	multiline := strings.Contains(e.source[open.Range().End:close.Range().Start], "\n")
	if multiline {
		currentLineStart := lineStart(e.source, current.Start)
		closeLineStart := lineStart(e.source, close.Range().Start)
		if _, ok := whitespacePrefix(e.source, currentLineStart, current.Start); ok &&
			closeLineStart > currentLineStart {
			return e.builder.ReplaceRange(cst.TextRange{Start: currentLineStart, End: closeLineStart}, "")
		}
	}
	end := current.End
	if rangeHasToken(list, current.End, close.Range().Start, phpsyntax.TkComma) {
		end = close.Range().Start
	}
	return e.builder.ReplaceRange(cst.TextRange{Start: previous.End, End: end}, "")
}

func rangeHasToken(list *phpsyntax.Node, start, end uint32, kinds ...phpsyntax.Kind) bool {
	if list == nil || start > end {
		return false
	}
	for element := range list.Descendants() {
		token, ok := element.(*phpsyntax.Token)
		if !ok || token.Range().Start < start || token.Range().End > end {
			continue
		}
		for _, kind := range kinds {
			if token.Kind() == kind {
				return true
			}
		}
	}
	return false
}

func isWhitespaceByte(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}
