use super::{Language, Options, format};

fn two_spaces() -> Options {
    Options {
        indent_width: 2,
        ..Options::default()
    }
}

#[test]
fn language_is_taken_from_the_extension() {
    assert_eq!(
        Language::from_path("file:///w/composer.json"),
        Some(Language::Json)
    );
    assert_eq!(
        Language::from_path("src/main.ts"),
        Some(Language::TypeScript)
    );
    assert_eq!(Language::from_path("src/base.scss"), Some(Language::Scss));
    assert_eq!(Language::from_path("a.b/README"), None);
    assert_eq!(Language::from_path("page.html.twig"), None);
}

#[test]
fn json_keeps_comments_and_has_one_member_per_line() {
    assert_eq!(
        format(
            Language::Json,
            "{\n// the name\n\"name\":\"swag/example\",\"keywords\":[\"a\",\"b\"],\"empty\":[ ]}",
            two_spaces()
        )
        .as_deref(),
        Some(
            "{\n  // the name\n  \"name\": \"swag/example\",\n  \"keywords\": [\n    \"a\",\n    \"b\"\n  ],\n  \"empty\": []\n}\n"
        )
    );
}

#[test]
fn scripts_follow_the_options() {
    let source = "const a = {b:\"c\"}\nfunction run(x:number){return x}\n";
    assert_eq!(
        format(Language::TypeScript, source, Options::default()).as_deref(),
        Some("const a = { b: 'c' };\nfunction run(x: number) {\n    return x;\n}\n")
    );
    let tabs = Options {
        insert_spaces: false,
        single_quotes: false,
        ..Options::default()
    };
    assert_eq!(
        format(Language::TypeScript, source, tabs).as_deref(),
        Some("const a = { b: \"c\" };\nfunction run(x: number) {\n\treturn x;\n}\n")
    );
    // A type annotation is a syntax error in JavaScript.
    assert_eq!(
        format(Language::JavaScript, source, Options::default()),
        None
    );
}

#[test]
fn lines_break_at_the_line_width() {
    let narrow = Options {
        line_width: 20,
        ..two_spaces()
    };
    assert_eq!(
        format(
            Language::JavaScript,
            "call(first, second, third);\n",
            narrow
        )
        .as_deref(),
        Some("call(\n  first,\n  second,\n  third,\n);\n")
    );
}

#[test]
fn styles_are_formatted() {
    assert_eq!(
        format(Language::Css, "a{color:red;margin:0}", two_spaces()).as_deref(),
        Some("a {\n  color: red;\n  margin: 0;\n}\n")
    );
    assert_eq!(
        format(
            Language::Scss,
            "$gap:4px;\n.card{padding:$gap;content:\"a\";&:hover{color:red}}",
            two_spaces()
        )
        .as_deref(),
        Some(
            "$gap: 4px;\n.card {\n  padding: $gap;\n  content: 'a';\n  &:hover {\n    color: red;\n  }\n}\n"
        )
    );
}

#[test]
fn a_document_with_syntax_errors_is_left_alone() {
    assert_eq!(
        format(Language::Json, "{\"name\":", Options::default()),
        None
    );
    assert_eq!(format(Language::Css, "a{color:", Options::default()), None);
}
