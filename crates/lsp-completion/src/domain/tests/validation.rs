use swlsp_lsp::CompletionProvider;

use super::*;
use crate::ValidationCompletionProvider;

#[test]
fn constraint_option_completion_includes_inherited_public_properties() {
    let cache = tempdir();
    let php = php_index(&cache);
    index_file(
        &php,
        "/project/vendor/Constraint.php",
        r"<?php
namespace Symfony\Component\Validator;
class Constraint {
    public string $message;
    public array $groups;
}
",
    );
    let source = r"<?php
namespace App;
class UniqueName extends \Symfony\Component\Validator\Constraint {
    public string $repositoryMethod;
    protected string $internal;
}
new UniqueName(['' => 'value']);
";
    let path = "/project/src/UniqueName.php";
    index_file(&php, path, source);
    let document = document(&format!("file://{path}"), source);
    let offset = source.rfind("''").unwrap() + 1;
    let items = ValidationCompletionProvider::new(Some(php))
        .get_completions(&ctx(), &request(&document, offset));
    require_completion(&items, "message");
    require_completion(&items, "groups");
    require_completion(&items, "repositoryMethod");
}
