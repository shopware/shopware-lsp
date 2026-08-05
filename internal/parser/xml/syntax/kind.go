package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const xmlBase Kind = 16384

const (
	TkWhitespace Kind = xmlBase + iota
	TkLineBreak
	TkOpenTag
	TkOpenEndTag
	TkCloseTag
	TkCloseEmptyTag
	TkEquals
	TkName
	TkAttributeValue
	TkText
	TkEntityReference
	TkComment
	TkCdata
	TkProcessingInstruction
	TkDoctype
	TkUnknown

	XmlDocument
	XmlElement
	XmlStartTag
	XmlEmptyElementTag
	XmlEndTag
	XmlName
	XmlAttribute
	XmlAttributeValue
	XmlContent
	XmlText
	XmlEntityReference
	XmlComment
	XmlCdata
	XmlProcessingInstruction
	XmlDoctype
	Error

	xmlKindCount
)

func init() {
	names := make([]string, int(xmlKindCount-xmlBase))
	texts := make([]string, len(names))

	set := func(kind Kind, name, text string) {
		index := int(kind - xmlBase)
		names[index] = name
		texts[index] = text
	}

	set(TkWhitespace, "XML_WHITESPACE", "whitespace")
	set(TkLineBreak, "XML_LINE_BREAK", "line break")
	set(TkOpenTag, "XML_OPEN_TAG", "<")
	set(TkOpenEndTag, "XML_OPEN_END_TAG", "</")
	set(TkCloseTag, "XML_CLOSE_TAG", ">")
	set(TkCloseEmptyTag, "XML_CLOSE_EMPTY_TAG", "/>")
	set(TkEquals, "XML_EQUALS", "=")
	set(TkName, "XML_NAME_TOKEN", "name")
	set(TkAttributeValue, "XML_ATTRIBUTE_VALUE_TOKEN", "attribute value")
	set(TkText, "XML_TEXT_TOKEN", "text")
	set(TkEntityReference, "XML_ENTITY_REFERENCE_TOKEN", "entity reference")
	set(TkComment, "XML_COMMENT_TOKEN", "comment")
	set(TkCdata, "XML_CDATA_TOKEN", "CDATA section")
	set(TkProcessingInstruction, "XML_PROCESSING_INSTRUCTION_TOKEN", "processing instruction")
	set(TkDoctype, "XML_DOCTYPE_TOKEN", "doctype")
	set(TkUnknown, "XML_UNKNOWN", "unknown token")

	set(XmlDocument, "XML_DOCUMENT", "")
	set(XmlElement, "XML_ELEMENT", "")
	set(XmlStartTag, "XML_START_TAG", "")
	set(XmlEmptyElementTag, "XML_EMPTY_ELEMENT_TAG", "")
	set(XmlEndTag, "XML_END_TAG", "")
	set(XmlName, "XML_NAME", "")
	set(XmlAttribute, "XML_ATTRIBUTE", "")
	set(XmlAttributeValue, "XML_ATTRIBUTE_VALUE", "")
	set(XmlContent, "XML_CONTENT", "")
	set(XmlText, "XML_TEXT", "")
	set(XmlEntityReference, "XML_ENTITY_REFERENCE", "")
	set(XmlComment, "XML_COMMENT", "")
	set(XmlCdata, "XML_CDATA", "")
	set(XmlProcessingInstruction, "XML_PROCESSING_INSTRUCTION", "")
	set(XmlDoctype, "XML_DOCTYPE", "")
	set(Error, "XML_ERROR", "")

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:       "xml",
		Base:       xmlBase,
		KindNames:  names,
		TokenTexts: texts,
		FirstNode:  XmlDocument,
		TriviaKinds: []Kind{
			TkWhitespace,
			TkLineBreak,
		},
	})
}
