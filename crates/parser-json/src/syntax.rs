//! JSON syntax kinds (values identical to the Go implementation).

swlsp_syntax::language! {
    name: "json", base: 4096,
    trivia: [TK_WHITESPACE, TK_LINE_BREAK],
    tokens: {
        TK_WHITESPACE = ("JSON_WHITESPACE", "whitespace"),
        TK_LINE_BREAK = ("JSON_LINE_BREAK", "line break"),
        TK_OPEN_BRACE = ("JSON_OPEN_BRACE", "{"),
        TK_CLOSE_BRACE = ("JSON_CLOSE_BRACE", "}"),
        TK_OPEN_BRACKET = ("JSON_OPEN_BRACKET", "["),
        TK_CLOSE_BRACKET = ("JSON_CLOSE_BRACKET", "]"),
        TK_COLON = ("JSON_COLON", ":"),
        TK_COMMA = ("JSON_COMMA", ","),
        TK_STRING = ("JSON_STRING_TOKEN", "string"),
        TK_NUMBER = ("JSON_NUMBER_TOKEN", "number"),
        TK_TRUE = ("JSON_TRUE", "true"),
        TK_FALSE = ("JSON_FALSE", "false"),
        TK_NULL = ("JSON_NULL", "null"),
        TK_UNKNOWN = ("JSON_UNKNOWN", "unknown token"),
    },
    nodes: {
        JSON_DOCUMENT = "JSON_DOCUMENT",
        JSON_OBJECT = "JSON_OBJECT",
        JSON_PAIR = "JSON_PAIR",
        JSON_ARRAY = "JSON_ARRAY",
        JSON_STRING = "JSON_STRING",
        JSON_NUMBER = "JSON_NUMBER",
        JSON_BOOLEAN = "JSON_BOOLEAN",
        JSON_NULL_VALUE = "JSON_NULL_VALUE",
        ERROR = "JSON_ERROR",
    },
}
