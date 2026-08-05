package parser

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/xml/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

type Error = parsekit.Error

type Result struct {
	Tree   *syntax.Tree
	Errors []Error
}

func Parse(source string) Result {
	tokens := lexer.LexInto(
		source,
		parsekit.AcquireTokenBuffer(len(source)/4+1),
	)
	parser := parsekit.NewOwned(tokens, parsekit.Config{
		GeneralRecoverySet: []syntax.Kind{
			syntax.TkOpenTag,
			syntax.TkOpenEndTag,
		},
		ErrorKind: syntax.Error,
	})
	root := parser.Start()

	for !parser.AtEnd() {
		parseDocumentItem(parser)
	}

	parser.Complete(root, syntax.XmlDocument)
	tree, errors := parser.Finish(source)
	return Result{Tree: tree, Errors: errors}
}

func parseDocumentItem(parser *parsekit.Parser) {
	switch {
	case parser.At(syntax.TkOpenTag):
		parseElement(parser)
	case parser.At(syntax.TkOpenEndTag):
		parser.AddError(parsekit.NewErrorBuilder("XML element"))
		parseEndTag(parser)
	case parser.At(syntax.TkText):
		parseLeaf(parser, syntax.XmlText)
	case parser.At(syntax.TkEntityReference):
		parseLeaf(parser, syntax.XmlEntityReference)
	case parser.At(syntax.TkComment):
		parseLeaf(parser, syntax.XmlComment)
	case parser.At(syntax.TkCdata):
		parseLeaf(parser, syntax.XmlCdata)
	case parser.At(syntax.TkProcessingInstruction):
		parseLeaf(parser, syntax.XmlProcessingInstruction)
	case parser.At(syntax.TkDoctype):
		parseLeaf(parser, syntax.XmlDoctype)
	default:
		parser.AddError(parsekit.NewErrorBuilder("XML document item"))
		parseLeaf(parser, syntax.Error)
	}
}

func parseElement(parser *parsekit.Parser) {
	element := parser.Start()
	name, empty := parseStartTag(parser)
	if empty {
		parser.Complete(element, syntax.XmlElement)
		return
	}

	content := parser.Start()
	for !parser.AtEnd() && !parser.At(syntax.TkOpenEndTag) {
		switch {
		case parser.At(syntax.TkOpenTag):
			parseElement(parser)
		case parser.At(syntax.TkText):
			parseLeaf(parser, syntax.XmlText)
		case parser.At(syntax.TkEntityReference):
			parseLeaf(parser, syntax.XmlEntityReference)
		case parser.At(syntax.TkComment):
			parseLeaf(parser, syntax.XmlComment)
		case parser.At(syntax.TkCdata):
			parseLeaf(parser, syntax.XmlCdata)
		case parser.At(syntax.TkProcessingInstruction):
			parseLeaf(parser, syntax.XmlProcessingInstruction)
		case parser.At(syntax.TkDoctype):
			parseLeaf(parser, syntax.XmlDoctype)
		default:
			parser.AddError(parsekit.NewErrorBuilder("XML content"))
			parseLeaf(parser, syntax.Error)
		}
	}
	parser.Complete(content, syntax.XmlContent)

	if parser.At(syntax.TkOpenEndTag) {
		closingName := parseEndTag(parser)
		if name != "" && closingName != "" && name != closingName {
			parser.AddError(parsekit.NewErrorBuilder("closing tag </" + name + ">"))
		}
	} else {
		parser.AddError(parsekit.NewErrorBuilder("closing tag </" + name + ">"))
	}
	parser.Complete(element, syntax.XmlElement)
}

func parseStartTag(parser *parsekit.Parser) (name string, empty bool) {
	tag := parser.Start()
	parser.Bump()

	if parser.At(syntax.TkName) {
		name = parseName(parser)
	} else {
		parser.AddError(parsekit.NewErrorBuilder("element name"))
	}

	for !parser.AtEnd() &&
		!parser.At(syntax.TkCloseTag) &&
		!parser.At(syntax.TkCloseEmptyTag) &&
		!parser.At(syntax.TkOpenTag) &&
		!parser.At(syntax.TkOpenEndTag) {
		if parser.At(syntax.TkName) {
			parseAttribute(parser)
			continue
		}
		parser.AddError(parsekit.NewErrorBuilder("attribute or tag close"))
		parseLeaf(parser, syntax.Error)
	}

	switch {
	case parser.At(syntax.TkCloseEmptyTag):
		parser.Bump()
		parser.Complete(tag, syntax.XmlEmptyElementTag)
		return name, true
	case parser.At(syntax.TkCloseTag):
		parser.Bump()
		parser.Complete(tag, syntax.XmlStartTag)
		return name, false
	default:
		parser.AddError(parsekit.NewErrorBuilder("\">\" or \"/>\""))
		parser.Complete(tag, syntax.XmlStartTag)
		return name, false
	}
}

func parseAttribute(parser *parsekit.Parser) {
	attribute := parser.Start()
	parseName(parser)

	if parser.At(syntax.TkEquals) {
		parser.Bump()
		if parser.At(syntax.TkAttributeValue) {
			value := parser.Start()
			token := parser.Bump()
			if !isClosedAttributeValue(token.Text()) {
				parser.AddError(parsekit.NewErrorBuilder("closing quote").AtToken(token))
			}
			parser.Complete(value, syntax.XmlAttributeValue)
		} else if parser.At(syntax.TkName) {
			// Preserve invalid unquoted values as usable editor context.
			value := parser.Start()
			parser.Bump()
			parser.AddError(parsekit.NewErrorBuilder("quoted attribute value"))
			parser.Complete(value, syntax.XmlAttributeValue)
		} else {
			parser.AddError(parsekit.NewErrorBuilder("attribute value"))
		}
	} else {
		parser.AddError(parsekit.NewErrorBuilder("\"=\""))
	}

	parser.Complete(attribute, syntax.XmlAttribute)
}

func parseName(parser *parsekit.Parser) string {
	name := parser.Start()
	token := parser.Bump()
	parser.Complete(name, syntax.XmlName)
	return token.Text()
}

func parseEndTag(parser *parsekit.Parser) string {
	tag := parser.Start()
	parser.Bump()

	name := ""
	if parser.At(syntax.TkName) {
		name = parseName(parser)
	} else {
		parser.AddError(parsekit.NewErrorBuilder("element name"))
	}

	parser.Expect(syntax.TkCloseTag, []syntax.Kind{
		syntax.TkOpenTag,
		syntax.TkOpenEndTag,
	})
	parser.Complete(tag, syntax.XmlEndTag)
	return name
}

func parseLeaf(parser *parsekit.Parser, kind syntax.Kind) {
	node := parser.Start()
	parser.Bump()
	parser.Complete(node, kind)
}

func isClosedAttributeValue(text string) bool {
	if len(text) < 2 {
		return false
	}
	quote := text[0]
	return (quote == '"' || quote == '\'') && strings.HasSuffix(text, string(quote))
}
