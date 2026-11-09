//! Vue SFC syntax kinds. Kind values are persisted in caches; do not renumber them.

swlsp_syntax::language! {
    name: "vue", base: 32768,
    trivia: [],
    tokens: {
        TK_TEXT = ("VUE_TEXT", "text"),
        TK_SECTION_OPEN = ("VUE_SECTION_OPEN", "section opening tag"),
        TK_SECTION_CLOSE = ("VUE_SECTION_CLOSE", "section closing tag"),
    },
    nodes: {
        VUE_DOCUMENT = "VUE_DOCUMENT",
        VUE_TEMPLATE_SECTION = "VUE_TEMPLATE_SECTION",
        VUE_SCRIPT_SECTION = "VUE_SCRIPT_SECTION",
        VUE_STYLE_SECTION = "VUE_STYLE_SECTION",
        VUE_CUSTOM_SECTION = "VUE_CUSTOM_SECTION",
    },
}
