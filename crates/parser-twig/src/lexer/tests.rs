use super::*;

/// Lexes `input` and asserts kind, text and range of every token.
fn assert_tokens(input: &str, want: &[(Kind, &str, u32, u32)]) {
    let got = lex(input);
    let dump: Vec<_> = got
        .iter()
        .map(|token| format!("{}@{:?} {:?}", token.kind.name(), token.range(), token.text))
        .collect();
    assert_eq!(
        got.len(),
        want.len(),
        "token count mismatch for {input:?}\ngot: {dump:#?}"
    );
    for (index, (token, &(kind, text, start, end))) in got.iter().zip(want).enumerate() {
        assert_eq!(
            token.kind,
            kind,
            "token {index} kind for {input:?}: got {}, want {}",
            token.kind.name(),
            kind.name()
        );
        assert_eq!(token.text, text, "token {index} text for {input:?}");
        assert_eq!(
            (token.start, token.end()),
            (start, end),
            "token {index} range for {input:?}"
        );
    }
}

/// The whole input is one token of `kind`.
fn single(input: &str, kind: Kind) {
    assert_tokens(input, &[(kind, input, 0, input.len() as u32)]);
}

#[test]
fn lex_simple_output() {
    // Ports lexer.rs `lex_simple_output`.
    assert_tokens(
        "</div>",
        &[
            (TK_LESS_THAN_SLASH, "</", 0, 2),
            (TK_WORD, "div", 2, 5),
            (TK_GREATER_THAN, ">", 5, 6),
        ],
    );
}

#[test]
fn lex_simple_expression() {
    // Ports lexer.rs `lex_simple_expression`.
    assert_tokens(
        "{{ not a }}",
        &[
            (TK_OPEN_CURLY_CURLY, "{{", 0, 2),
            (TK_WHITESPACE, " ", 2, 3),
            (TK_NOT, "not", 3, 6),
            (TK_WHITESPACE, " ", 6, 7),
            (TK_WORD, "a", 7, 8),
            (TK_WHITESPACE, " ", 8, 9),
            (TK_CLOSE_CURLY_CURLY, "}}", 9, 11),
        ],
    );
}

#[test]
fn lex_hashtag_curly_curly() {
    // Ports lexer.rs `lex_hashtag_curly_curly`.
    assert_tokens(
        "#{{",
        &[(TK_HASHTAG, "#", 0, 1), (TK_OPEN_CURLY_CURLY, "{{", 1, 3)],
    );
}

#[test]
fn lex_whitespace() {
    single("   ", TK_WHITESPACE);
    single(" \t  ", TK_WHITESPACE);
    single("\t", TK_WHITESPACE);
}

#[test]
fn lex_line_break() {
    single("\n", TK_LINE_BREAK);
    single("\n\n", TK_LINE_BREAK);
    single("\r\n", TK_LINE_BREAK);
    single("\r\n\r\n", TK_LINE_BREAK);
    single("\r\n\n\r\n", TK_LINE_BREAK);
}

#[test]
fn lex_word() {
    for w in [
        "hello",
        "hello123",
        "camelCase",
        "kebab-case",
        "snake_case",
        "#hello123",
        "@hello123",
        "block1",
        "block_",
        "blocks",
        "_blank",
        "$special",
    ] {
        single(w, TK_WORD);
    }
}

#[test]
fn lex_twig_component_name() {
    single("twig:a", TK_TWIG_COMPONENT_NAME);
    single("twig:a:b:c:d", TK_TWIG_COMPONENT_NAME);
    single("twig:my:component:name", TK_TWIG_COMPONENT_NAME);
    // `twig` alone is a plain word.
    single("twig", TK_WORD);
}

#[test]
fn lex_number() {
    for n in [
        "123",
        "0.0",
        "3.123456789",
        "3e+2",
        "3e-2",
        "10E-7",
        "10E+6",
        "1.23E+10",
        "42.3",
    ] {
        single(n, TK_NUMBER);
    }
}

#[test]
fn lex_html_escape_character() {
    for e in [
        "&NewLine;",
        "&nbsp;",
        "&#39;",
        "&#8721;",
        "&sup3;",
        "&#x00B3;",
        "&#10;",
    ] {
        single(e, TK_HTML_ESCAPE_CHARACTER);
    }
}

#[test]
fn lex_punctuation_and_delimiters() {
    // Ports the individual per-token tests in lexer.rs.
    let cases = [
        (".", TK_DOT),
        ("..", TK_DOUBLE_DOT),
        ("...", TK_TRIPLE_DOT),
        (",", TK_COMMA),
        (":", TK_COLON),
        (";", TK_SEMICOLON),
        ("!", TK_EXCLAMATION_MARK),
        ("!=", TK_EXCLAMATION_MARK_EQUALS),
        ("!==", TK_EXCLAMATION_MARK_DOUBLE_EQUALS),
        ("?", TK_QUESTION_MARK),
        ("??", TK_DOUBLE_QUESTION_MARK),
        ("%", TK_PERCENT),
        ("~", TK_TILDE),
        ("|", TK_SINGLE_PIPE),
        ("||", TK_DOUBLE_PIPE),
        ("&", TK_AMPERSAND),
        ("&&", TK_DOUBLE_AMPERSAND),
        ("/", TK_FORWARD_SLASH),
        ("//", TK_DOUBLE_FORWARD_SLASH),
        ("\\", TK_BACKWARD_SLASH),
        ("(", TK_OPEN_PARENTHESIS),
        (")", TK_CLOSE_PARENTHESIS),
        ("{", TK_OPEN_CURLY),
        ("}", TK_CLOSE_CURLY),
        ("[", TK_OPEN_SQUARE),
        ("]", TK_CLOSE_SQUARE),
        ("<", TK_LESS_THAN),
        ("<=", TK_LESS_THAN_EQUAL),
        ("<=>", TK_LESS_THAN_EQUAL_GREATER_THAN),
        ("</", TK_LESS_THAN_SLASH),
        ("<!", TK_LESS_THAN_EXCLAMATION_MARK),
        (">", TK_GREATER_THAN),
        (">=", TK_GREATER_THAN_EQUAL),
        ("=>", TK_EQUAL_GREATER_THAN),
        ("/>", TK_SLASH_GREATER_THAN),
        ("<!--", TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS),
        ("-->", TK_MINUS_MINUS_GREATER_THAN),
        ("=", TK_EQUAL),
        ("==", TK_DOUBLE_EQUAL),
        ("===", TK_TRIPLE_EQUAL),
        ("+", TK_PLUS),
        ("-", TK_MINUS),
        ("*", TK_STAR),
        ("**", TK_DOUBLE_STAR),
        ("\"", TK_DOUBLE_QUOTES),
        ("'", TK_SINGLE_QUOTES),
        ("`", TK_GRAVE_ACCENT_QUOTES),
        ("{%", TK_CURLY_PERCENT),
        ("{%-", TK_CURLY_PERCENT_MINUS),
        ("{%~", TK_CURLY_PERCENT_TILDE),
        ("%}", TK_PERCENT_CURLY),
        ("-%}", TK_MINUS_PERCENT_CURLY),
        ("~%}", TK_TILDE_PERCENT_CURLY),
        ("{{", TK_OPEN_CURLY_CURLY),
        ("{{-", TK_OPEN_CURLY_CURLY_MINUS),
        ("{{~", TK_OPEN_CURLY_CURLY_TILDE),
        ("}}", TK_CLOSE_CURLY_CURLY),
        ("-}}", TK_MINUS_CLOSE_CURLY_CURLY),
        ("~}}", TK_TILDE_CLOSE_CURLY_CURLY),
        ("{#", TK_OPEN_CURLY_HASHTAG),
        ("{#-", TK_OPEN_CURLY_HASHTAG_MINUS),
        ("{#~", TK_OPEN_CURLY_HASHTAG_TILDE),
        ("#}", TK_HASHTAG_CLOSE_CURLY),
        ("-#}", TK_MINUS_HASHTAG_CLOSE_CURLY),
        ("~#}", TK_TILDE_HASHTAG_CLOSE_CURLY),
        ("#", TK_HASHTAG),
    ];
    for (text, kind) in cases {
        single(text, kind);
    }
}

#[test]
fn lex_case_insensitive_keywords() {
    // DOCTYPE / true / false / none / null / ludtwig-ignore(-file) ignore case.
    single("DOCTYPE", TK_DOCTYPE);
    single("doctype", TK_DOCTYPE);
    single("DocType", TK_DOCTYPE);
    single("true", TK_TRUE);
    single("TRUE", TK_TRUE);
    single("True", TK_TRUE);
    single("false", TK_FALSE);
    single("FALSE", TK_FALSE);
    single("none", TK_NONE);
    single("NONE", TK_NONE);
    single("null", TK_NULL);
    single("NULL", TK_NULL);
    single("ludtwig-ignore-file", TK_LUDTWIG_IGNORE_FILE);
    single("LUDTWIG-IGNORE-FILE", TK_LUDTWIG_IGNORE_FILE);
    single("ludtwig-ignore", TK_LUDTWIG_IGNORE);
    single("Ludtwig-Ignore", TK_LUDTWIG_IGNORE);
}

#[test]
fn lex_keywords() {
    // Ports every single-word keyword test in lexer.rs.
    let cases = [
        ("block", TK_BLOCK),
        ("endblock", TK_ENDBLOCK),
        ("if", TK_IF),
        ("elseif", TK_ELSE_IF),
        ("else", TK_ELSE),
        ("endif", TK_ENDIF),
        ("apply", TK_APPLY),
        ("endapply", TK_ENDAPPLY),
        ("autoescape", TK_AUTOESCAPE),
        ("endautoescape", TK_ENDAUTOESCAPE),
        ("cache", TK_CACHE),
        ("endcache", TK_ENDCACHE),
        ("deprecated", TK_DEPRECATED),
        ("do", TK_DO),
        ("embed", TK_EMBED),
        ("endembed", TK_ENDEMBED),
        ("extends", TK_EXTENDS),
        ("flush", TK_FLUSH),
        ("for", TK_FOR),
        ("endfor", TK_ENDFOR),
        ("from", TK_FROM),
        ("import", TK_IMPORT),
        ("macro", TK_MACRO),
        ("endmacro", TK_ENDMACRO),
        ("sandbox", TK_SANDBOX),
        ("endsandbox", TK_ENDSANDBOX),
        ("set", TK_SET),
        ("endset", TK_ENDSET),
        ("use", TK_USE),
        ("verbatim", TK_VERBATIM),
        ("endverbatim", TK_ENDVERBATIM),
        ("only", TK_ONLY),
        ("with", TK_WITH),
        ("endwith", TK_ENDWITH),
        ("ttl", TK_TTL),
        ("tags", TK_TAGS),
        ("props", TK_PROPS),
        ("component", TK_COMPONENT),
        ("endcomponent", TK_ENDCOMPONENT),
        ("not", TK_NOT),
        ("or", TK_OR),
        ("and", TK_AND),
        ("b-or", TK_BINARY_OR),
        ("b-xor", TK_BINARY_XOR),
        ("b-and", TK_BINARY_AND),
        ("in", TK_IN),
        ("matches", TK_MATCHES),
        ("is", TK_IS),
        ("even", TK_EVEN),
        ("odd", TK_ODD),
        ("defined", TK_DEFINED),
        ("as", TK_AS),
        ("constant", TK_CONSTANT),
        ("empty", TK_EMPTY),
        ("iterable", TK_ITERABLE),
        ("max", TK_MAX),
        ("min", TK_MIN),
        ("range", TK_RANGE),
        ("cycle", TK_CYCLE),
        ("random", TK_RANDOM),
        ("date", TK_DATE),
        ("include", TK_INCLUDE),
        ("source", TK_SOURCE),
        ("trans", TK_TRANS),
        ("endtrans", TK_ENDTRANS),
        ("sw_extends", TK_SW_EXTENDS),
        ("sw_silent_feature_call", TK_SW_SILENT_FEATURE_CALL),
        ("endsw_silent_feature_call", TK_ENDSW_SILENT_FEATURE_CALL),
        ("sw_include", TK_SW_INCLUDE),
        ("return", TK_RETURN),
        ("sw_icon", TK_SW_ICON),
        ("sw_thumbnails", TK_SW_THUMBNAILS),
        ("style", TK_STYLE),
        ("sw_embed", TK_SW_EMBED),
        ("sw_end_embed", TK_SW_END_EMBED),
        ("sw_use", TK_SW_USE),
        ("sw_import", TK_SW_IMPORT),
        ("sw_from", TK_SW_FROM),
    ];
    for (text, kind) in cases {
        single(text, kind);
    }
}

#[test]
fn lex_multi_word_keywords() {
    // The space-containing keywords lex as a SINGLE token.
    single("not in", TK_NOT_IN);
    single("is not", TK_IS_NOT);
    single("starts with", TK_STARTS_WITH);
    single("ends with", TK_ENDS_WITH);
    single("same as", TK_SAME_AS);
    single("divisible by", TK_DIVISIBLE_BY);
    single("ignore missing", TK_IGNORE_MISSING);
}

// TestLexMultiWordBoundaries covers the cb_not / cb_is boundary tricks and the
// longest-match behaviour of the other multi-word keywords.
#[test]
fn lex_multi_word_boundaries() {
    // `not insider` must NOT fuse to TK_NOT_IN: the char after " in" is 's'.
    assert_tokens(
        "not insider",
        &[
            (TK_NOT, "not", 0, 3),
            (TK_WHITESPACE, " ", 3, 4),
            (TK_WORD, "insider", 4, 11),
        ],
    );
    // `not inside` likewise stays TK_NOT + word.
    assert_tokens(
        "not inside",
        &[
            (TK_NOT, "not", 0, 3),
            (TK_WHITESPACE, " ", 3, 4),
            (TK_WORD, "inside", 4, 10),
        ],
    );
    // `notin` is longer than `not`, so it is a single word (maximal munch).
    single("notin", TK_WORD);
    // `not in` at EOF fuses.
    single("not in", TK_NOT_IN);
    // `not in ` (trailing space) fuses, then whitespace.
    assert_tokens(
        "not in ",
        &[(TK_NOT_IN, "not in", 0, 6), (TK_WHITESPACE, " ", 6, 7)],
    );
    // `not in\n` fuses; '\n' is a valid boundary.
    assert_tokens(
        "not in\n",
        &[(TK_NOT_IN, "not in", 0, 6), (TK_LINE_BREAK, "\n", 6, 7)],
    );
    // `not in\t` fuses; '\t' is a valid boundary.
    assert_tokens(
        "not in\t",
        &[(TK_NOT_IN, "not in", 0, 6), (TK_WHITESPACE, "\t", 6, 7)],
    );
    // `not iny` does not fuse (char after " in" is 'y').
    assert_tokens(
        "not iny",
        &[
            (TK_NOT, "not", 0, 3),
            (TK_WHITESPACE, " ", 3, 4),
            (TK_WORD, "iny", 4, 7),
        ],
    );

    // `is nothing` must NOT fuse to TK_IS_NOT: char after " not" is 'h'.
    assert_tokens(
        "is nothing",
        &[
            (TK_IS, "is", 0, 2),
            (TK_WHITESPACE, " ", 2, 3),
            (TK_WORD, "nothing", 3, 10),
        ],
    );
    // `is not` at EOF fuses.
    single("is not", TK_IS_NOT);
    // `is not ` fuses then whitespace.
    assert_tokens(
        "is not ",
        &[(TK_IS_NOT, "is not", 0, 6), (TK_WHITESPACE, " ", 6, 7)],
    );

    // The other multi-word keywords are pure longest-match literals: no
    // trailing boundary check. `starts withheld` -> `starts with` + `held`.
    assert_tokens(
        "starts withheld",
        &[
            (TK_STARTS_WITH, "starts with", 0, 11),
            (TK_WORD, "held", 11, 15),
        ],
    );
    // `starts wit` (incomplete) -> word + ws + word.
    assert_tokens(
        "starts wit",
        &[
            (TK_WORD, "starts", 0, 6),
            (TK_WHITESPACE, " ", 6, 7),
            (TK_WORD, "wit", 7, 10),
        ],
    );
    // Two spaces break the literal `starts with`.
    assert_tokens(
        "starts  with",
        &[
            (TK_WORD, "starts", 0, 6),
            (TK_WHITESPACE, "  ", 6, 8),
            (TK_WITH, "with", 8, 12),
        ],
    );
    // `ignore foo` -> `ignore` is just a word (ignore is not a keyword alone).
    assert_tokens(
        "ignore foo",
        &[
            (TK_WORD, "ignore", 0, 6),
            (TK_WHITESPACE, " ", 6, 7),
            (TK_WORD, "foo", 7, 10),
        ],
    );
    single("divisible by", TK_DIVISIBLE_BY);
    single("same as", TK_SAME_AS);
}

// TestLexKeywordWordBoundary makes sure keywords are only recognised when the
// whole scanned word matches (maximal munch): `blocks` and `block_` are words.
#[test]
fn lex_keyword_word_boundary() {
    single("blocks", TK_WORD);
    single("block_", TK_WORD);
    single("block1", TK_WORD);
    single("iffy", TK_WORD);
    single("ifs", TK_WORD);
}

// TestLexUnknown covers un-lexable input: one TkUnknown token per UTF-8 rune.
#[test]
fn lex_unknown() {
    // A single multi-byte rune (`€` is 3 bytes) is one TkUnknown token.
    single("€", TK_UNKNOWN);
    // A lone '\r' is not a line break and becomes TkUnknown.
    single("\r", TK_UNKNOWN);
    // Consecutive unknown runes split into one token each (`€` is 3 bytes,
    // `£` is 2 bytes).
    assert_tokens("€£", &[(TK_UNKNOWN, "€", 0, 3), (TK_UNKNOWN, "£", 3, 5)]);
    // Unknown surrounded by known tokens.
    assert_tokens(
        "a€b",
        &[
            (TK_WORD, "a", 0, 1),
            (TK_UNKNOWN, "€", 1, 4),
            (TK_WORD, "b", 4, 5),
        ],
    );
}

// TestLexNumberBoundaries exercises the number regex edge cases (mandatory
// exponent sign, fractional part needing digits).
#[test]
fn lex_number_boundaries() {
    // Missing exponent sign: `3e2` -> number `3` then word `e2`.
    assert_tokens("3e2", &[(TK_NUMBER, "3", 0, 1), (TK_WORD, "e2", 1, 3)]);
    // Trailing dot with no fraction: `3.` -> number `3` then dot.
    assert_tokens("3.", &[(TK_NUMBER, "3", 0, 1), (TK_DOT, ".", 1, 2)]);
    // Exponent sign but no digits: `3e+` -> number `3`, word `e`, plus.
    assert_tokens(
        "3e+",
        &[
            (TK_NUMBER, "3", 0, 1),
            (TK_WORD, "e", 1, 2),
            (TK_PLUS, "+", 2, 3),
        ],
    );
    // Range operator: `1..5` -> number, `..`, number.
    assert_tokens(
        "1..5",
        &[
            (TK_NUMBER, "1", 0, 1),
            (TK_DOUBLE_DOT, "..", 1, 3),
            (TK_NUMBER, "5", 3, 4),
        ],
    );
}

// TestLexTwigComponentBoundaries exercises the twig:component-name matcher.
#[test]
fn lex_twig_component_boundaries() {
    // `twiggy` is a plain word (no colon after `twig`).
    single("twiggy", TK_WORD);
    // `twigx:a` -> `twigx` is a word (base is not exactly `twig`).
    assert_tokens(
        "twigx:a",
        &[
            (TK_WORD, "twigx", 0, 5),
            (TK_COLON, ":", 5, 6),
            (TK_WORD, "a", 6, 7),
        ],
    );
    // Empty segment `twig::a` -> word `twig`, two colons, word.
    assert_tokens(
        "twig::a",
        &[
            (TK_WORD, "twig", 0, 4),
            (TK_COLON, ":", 4, 5),
            (TK_COLON, ":", 5, 6),
            (TK_WORD, "a", 6, 7),
        ],
    );
    // Trailing colon: `twig:a:` -> component `twig:a` then colon.
    assert_tokens(
        "twig:a:",
        &[
            (TK_TWIG_COMPONENT_NAME, "twig:a", 0, 6),
            (TK_COLON, ":", 6, 7),
        ],
    );
    // Kebab and digits inside segments are allowed.
    single("twig:my-comp:v2", TK_TWIG_COMPONENT_NAME);
}

// TestLexAmpersandBoundaries checks the `&` vs `&&` vs escape interplay.
#[test]
fn lex_ampersand_boundaries() {
    single("&&", TK_DOUBLE_AMPERSAND);
    single("&", TK_AMPERSAND);
    // `&amp` without a semicolon is not an escape: `&` then word.
    assert_tokens("&amp", &[(TK_AMPERSAND, "&", 0, 1), (TK_WORD, "amp", 1, 4)]);
    // `&nbsp;` is a full escape.
    single("&nbsp;", TK_HTML_ESCAPE_CHARACTER);
}

// TestLexAllTokensChainedTogether ports lexer.rs `lex_all_tokens_chained_together`:
// each token followed by a space, verifying kinds in a long stream. It also
// verifies the losslessness invariant (concatenated text == source).
#[test]
fn lex_all_tokens_chained_together() {
    let entries = [
        ("\n", TK_LINE_BREAK),
        ("word", TK_WORD),
        ("twig:my:component:name", TK_TWIG_COMPONENT_NAME),
        ("42.3", TK_NUMBER),
        ("&#10;", TK_HTML_ESCAPE_CHARACTER),
        (".", TK_DOT),
        ("..", TK_DOUBLE_DOT),
        ("...", TK_TRIPLE_DOT),
        (",", TK_COMMA),
        (":", TK_COLON),
        (";", TK_SEMICOLON),
        ("!", TK_EXCLAMATION_MARK),
        ("!=", TK_EXCLAMATION_MARK_EQUALS),
        ("!==", TK_EXCLAMATION_MARK_DOUBLE_EQUALS),
        ("?", TK_QUESTION_MARK),
        ("??", TK_DOUBLE_QUESTION_MARK),
        ("%", TK_PERCENT),
        ("~", TK_TILDE),
        ("|", TK_SINGLE_PIPE),
        ("||", TK_DOUBLE_PIPE),
        ("&", TK_AMPERSAND),
        ("&&", TK_DOUBLE_AMPERSAND),
        ("/", TK_FORWARD_SLASH),
        ("//", TK_DOUBLE_FORWARD_SLASH),
        ("\\", TK_BACKWARD_SLASH),
        ("(", TK_OPEN_PARENTHESIS),
        (")", TK_CLOSE_PARENTHESIS),
        ("{", TK_OPEN_CURLY),
        ("}", TK_CLOSE_CURLY),
        ("[", TK_OPEN_SQUARE),
        ("]", TK_CLOSE_SQUARE),
        ("<", TK_LESS_THAN),
        ("<=", TK_LESS_THAN_EQUAL),
        ("<=>", TK_LESS_THAN_EQUAL_GREATER_THAN),
        ("</", TK_LESS_THAN_SLASH),
        ("<!", TK_LESS_THAN_EXCLAMATION_MARK),
        ("doctype", TK_DOCTYPE),
        (">", TK_GREATER_THAN),
        (">=", TK_GREATER_THAN_EQUAL),
        ("=>", TK_EQUAL_GREATER_THAN),
        ("/>", TK_SLASH_GREATER_THAN),
        ("<!--", TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS),
        ("-->", TK_MINUS_MINUS_GREATER_THAN),
        ("=", TK_EQUAL),
        ("==", TK_DOUBLE_EQUAL),
        ("===", TK_TRIPLE_EQUAL),
        ("+", TK_PLUS),
        ("-", TK_MINUS),
        ("*", TK_STAR),
        ("**", TK_DOUBLE_STAR),
        ("\"", TK_DOUBLE_QUOTES),
        ("'", TK_SINGLE_QUOTES),
        ("`", TK_GRAVE_ACCENT_QUOTES),
        ("{%", TK_CURLY_PERCENT),
        ("{%-", TK_CURLY_PERCENT_MINUS),
        ("{%~", TK_CURLY_PERCENT_TILDE),
        ("%}", TK_PERCENT_CURLY),
        ("-%}", TK_MINUS_PERCENT_CURLY),
        ("~%}", TK_TILDE_PERCENT_CURLY),
        ("{{", TK_OPEN_CURLY_CURLY),
        ("{{-", TK_OPEN_CURLY_CURLY_MINUS),
        ("{{~", TK_OPEN_CURLY_CURLY_TILDE),
        ("}}", TK_CLOSE_CURLY_CURLY),
        ("-}}", TK_MINUS_CLOSE_CURLY_CURLY),
        ("~}}", TK_TILDE_CLOSE_CURLY_CURLY),
        ("{#", TK_OPEN_CURLY_HASHTAG),
        ("{#-", TK_OPEN_CURLY_HASHTAG_MINUS),
        ("{#~", TK_OPEN_CURLY_HASHTAG_TILDE),
        ("#", TK_HASHTAG),
        ("#}", TK_HASHTAG_CLOSE_CURLY),
        ("-#}", TK_MINUS_HASHTAG_CLOSE_CURLY),
        ("~#}", TK_TILDE_HASHTAG_CLOSE_CURLY),
        ("true", TK_TRUE),
        ("false", TK_FALSE),
        ("block", TK_BLOCK),
        ("endblock", TK_ENDBLOCK),
        ("if", TK_IF),
        ("elseif", TK_ELSE_IF),
        ("else", TK_ELSE),
        ("endif", TK_ENDIF),
        ("apply", TK_APPLY),
        ("endapply", TK_ENDAPPLY),
        ("autoescape", TK_AUTOESCAPE),
        ("endautoescape", TK_ENDAUTOESCAPE),
        ("cache", TK_CACHE),
        ("endcache", TK_ENDCACHE),
        ("deprecated", TK_DEPRECATED),
        ("do", TK_DO),
        ("embed", TK_EMBED),
        ("endembed", TK_ENDEMBED),
        ("extends", TK_EXTENDS),
        ("flush", TK_FLUSH),
        ("for", TK_FOR),
        ("endfor", TK_ENDFOR),
        ("from", TK_FROM),
        ("import", TK_IMPORT),
        ("macro", TK_MACRO),
        ("endmacro", TK_ENDMACRO),
        ("sandbox", TK_SANDBOX),
        ("endsandbox", TK_ENDSANDBOX),
        ("set", TK_SET),
        ("endset", TK_ENDSET),
        ("use", TK_USE),
        ("verbatim", TK_VERBATIM),
        ("endverbatim", TK_ENDVERBATIM),
        ("only", TK_ONLY),
        ("ignore missing", TK_IGNORE_MISSING),
        ("with", TK_WITH),
        ("endwith", TK_ENDWITH),
        ("ttl", TK_TTL),
        ("tags", TK_TAGS),
        ("props", TK_PROPS),
        ("component", TK_COMPONENT),
        ("endcomponent", TK_ENDCOMPONENT),
        ("not", TK_NOT),
        ("not in", TK_NOT_IN),
        ("or", TK_OR),
        ("and", TK_AND),
        ("b-or", TK_BINARY_OR),
        ("b-xor", TK_BINARY_XOR),
        ("b-and", TK_BINARY_AND),
        ("in", TK_IN),
        ("matches", TK_MATCHES),
        ("starts with", TK_STARTS_WITH),
        ("ends with", TK_ENDS_WITH),
        ("is", TK_IS),
        ("is not", TK_IS_NOT),
        ("even", TK_EVEN),
        ("odd", TK_ODD),
        ("defined", TK_DEFINED),
        ("same as", TK_SAME_AS),
        ("as", TK_AS),
        ("none", TK_NONE),
        ("null", TK_NULL),
        ("divisible by", TK_DIVISIBLE_BY),
        ("constant", TK_CONSTANT),
        ("empty", TK_EMPTY),
        ("iterable", TK_ITERABLE),
        ("max", TK_MAX),
        ("min", TK_MIN),
        ("range", TK_RANGE),
        ("cycle", TK_CYCLE),
        ("random", TK_RANDOM),
        ("date", TK_DATE),
        ("include", TK_INCLUDE),
        ("source", TK_SOURCE),
        ("sw_extends", TK_SW_EXTENDS),
        ("sw_silent_feature_call", TK_SW_SILENT_FEATURE_CALL),
        ("endsw_silent_feature_call", TK_ENDSW_SILENT_FEATURE_CALL),
        ("sw_include", TK_SW_INCLUDE),
        ("return", TK_RETURN),
        ("sw_icon", TK_SW_ICON),
        ("sw_thumbnails", TK_SW_THUMBNAILS),
        ("style", TK_STYLE),
        ("ludtwig-ignore-file", TK_LUDTWIG_IGNORE_FILE),
        ("ludtwig-ignore", TK_LUDTWIG_IGNORE),
        ("€", TK_UNKNOWN),
        ("trans", TK_TRANS),
        ("endtrans", TK_ENDTRANS),
        ("sw_embed", TK_SW_EMBED),
        ("sw_end_embed", TK_SW_END_EMBED),
        ("sw_use", TK_SW_USE),
        ("sw_import", TK_SW_IMPORT),
        ("sw_from", TK_SW_FROM),
    ];

    let mut source = String::new();
    let mut want = Vec::new();
    for (text, kind) in entries {
        source.push_str(text);
        source.push(' ');
        want.push(kind);
        want.push(TK_WHITESPACE);
    }
    let got = lex(&source);
    assert_eq!(got.len(), want.len(), "token count mismatch");
    for (index, (token, kind)) in got.iter().zip(&want).enumerate() {
        assert_eq!(
            token.kind,
            *kind,
            "token {index} {:?}: got {}, want {}",
            token.text,
            token.kind.name(),
            kind.name()
        );
    }
    let reconstructed: String = got.iter().map(|token| token.text).collect();
    assert_eq!(reconstructed, source, "lossless invariant violated");
}

/// General property check: the concatenation of all token texts equals the
/// source and ranges are contiguous.
#[test]
fn lex_lossless() {
    let inputs = [
        "",
        "{% if foo.bar is not empty %}{{ x|title }}{% endif %}",
        "<div class=\"a\">text &nbsp; {# comment #}</div>",
        "€ not insider is nothing starts withheld",
        "\r lone carriage return",
    ];
    for input in inputs {
        let tokens = lex(input);
        let mut previous_end = 0;
        let mut reconstructed = String::new();
        for (index, token) in tokens.iter().enumerate() {
            assert_eq!(
                token.start, previous_end,
                "input {input:?} token {index}: non-contiguous range"
            );
            previous_end = token.end();
            reconstructed.push_str(token.text);
        }
        assert_eq!(reconstructed, input);
        assert_eq!(previous_end as usize, input.len());
    }
}
