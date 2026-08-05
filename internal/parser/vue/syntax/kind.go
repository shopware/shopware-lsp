package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const vueBase Kind = 32768

const (
	TkText Kind = vueBase + iota
	TkSectionOpen
	TkSectionClose

	VueDocument
	VueTemplateSection
	VueScriptSection
	VueStyleSection
	VueCustomSection

	vueKindCount
)

func init() {
	names := make([]string, int(vueKindCount-vueBase))
	texts := make([]string, len(names))
	set := func(kind Kind, name, text string) {
		index := int(kind - vueBase)
		names[index] = name
		texts[index] = text
	}
	set(TkText, "VUE_TEXT", "text")
	set(TkSectionOpen, "VUE_SECTION_OPEN", "section opening tag")
	set(TkSectionClose, "VUE_SECTION_CLOSE", "section closing tag")
	set(VueDocument, "VUE_DOCUMENT", "")
	set(VueTemplateSection, "VUE_TEMPLATE_SECTION", "")
	set(VueScriptSection, "VUE_SCRIPT_SECTION", "")
	set(VueStyleSection, "VUE_STYLE_SECTION", "")
	set(VueCustomSection, "VUE_CUSTOM_SECTION", "")
	cst.RegisterLanguage(cst.LanguageSpec{
		Name:       "vue",
		Base:       vueBase,
		KindNames:  names,
		TokenTexts: texts,
		FirstNode:  VueDocument,
	})
}
