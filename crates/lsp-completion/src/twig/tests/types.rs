use swlsp_lsp_protocol::CompletionItemKind;
use tempfile::TempDir;

use super::*;
use crate::twig::TwigCompletionProvider;

pub(super) const PAGE: &str = "file:///project/templates/page.html.twig";

fn types_fixture() -> (TwigCompletionProvider, Vec<TempDir>) {
    let (php_dir, twig_dir) = (tmp(), tmp());
    let php = php_index(&php_dir);
    index(
        &php,
        "/project/src/User.php",
        r#"<?php
namespace App;
class User {
    public string $displayName;
    public bool $active;
    /** @var User[] */
    public array $friends;
    public function getEmail(): string { return ''; }
}
interface UserProvider {}
enum UserState { case ACTIVE; }"#,
    );
    (
        TwigCompletionProvider::new("/project", Some(twig_index(&twig_dir)), None, Some(php)),
        vec![php_dir, twig_dir],
    )
}

#[test]
fn twig_types_tag_completes_php_classes_and_preserves_type_syntax() {
    let (provider, _dirs) = types_fixture();
    let items = caret_items(&provider, PAGE, r"{% types { user: 'App\\Us<caret>' } %}");
    let user = require_completion(&items, "App\\User");
    assert_eq!(user.kind, Some(CompletionItemKind::CLASS));
    assert_eq!(edit_new_text(&user), r"App\\User");

    let items = caret_items(
        &provider,
        PAGE,
        r"{% types { users: 'list<App\\Us<caret>>' } %}",
    );
    assert_eq!(
        edit_new_text(&require_completion(&items, "App\\User")),
        r"App\\User"
    );
}

#[test]
fn twig_types_tag_feeds_variable_and_member_completion() {
    let (provider, _dirs) = types_fixture();
    let items = caret_items(
        &provider,
        PAGE,
        "{% types { user: 'App\\\\User' } %}\n{{ us<caret> }}",
    );
    assert_eq!(require_completion(&items, "user").detail, "App\\User");

    let items = caret_items(
        &provider,
        PAGE,
        "{% types { user?: 'App\\\\User' } %}\n{{ user.<caret> }}",
    );
    let names = labels(&items);
    assert!(names.contains(&"displayName".to_string()));
    assert!(names.contains(&"email".to_string()));
}

#[test]
fn twig_types_tag_completion_includes_documentation_and_optionality() {
    let (provider, _dirs) = types_fixture();
    let items = caret_items(
        &provider,
        PAGE,
        "{% types {\n    ## User shown in the profile card.\n    user?: 'App\\\\User',\n} %}\n{{ us<caret> }}",
    );
    let user = require_completion(&items, "user");
    assert_eq!(user.detail, "App\\User");
    assert!(documentation(&user).contains("Optional variable"));
    assert!(documentation(&user).contains("User shown in the profile card."));
}

#[test]
fn twig_types_tag_feeds_for_loop_element_completion() {
    let (provider, _dirs) = types_fixture();
    let items = caret_items(
        &provider,
        PAGE,
        "{% types { users: 'App\\\\User[]' } %}\n{% for user in users %}\n{{ user.<caret> }}\n{% endfor %}",
    );
    let names = labels(&items);
    assert!(names.contains(&"displayName".to_string()));
    assert!(names.contains(&"email".to_string()));

    let items = caret_items(
        &provider,
        PAGE,
        "{% types { users: 'App\\\\User[]' } %}\n{% for user in users %}{% else %}\n{{ user.<caret> }}\n{% endfor %}",
    );
    let names = labels(&items);
    assert!(!names.contains(&"displayName".to_string()));
    assert!(!names.contains(&"email".to_string()));
}

#[test]
fn twig_types_tag_feeds_incomplete_statement_completion() {
    let (provider, _dirs) = types_fixture();
    let items = caret_items(
        &provider,
        PAGE,
        "{% types { user: 'App\\\\User' } %}\n{% if<caret> %}",
    );
    assert!(labels(&items).contains(&"if user.active".to_string()));
    let items = caret_items(
        &provider,
        PAGE,
        "{% types { user: 'App\\\\User' } %}\n{% fo<caret> %}",
    );
    assert!(labels(&items).contains(&"for friend in user.friends".to_string()));
}
