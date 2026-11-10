use std::sync::Arc;

use swlsp_lsp::CompletionProvider;

use super::*;
use crate::completion::DoctrineCompletionProvider;

#[test]
fn doctrine_type_registration_class_completion() {
    let (doctrine_cache, php_cache) = (tempdir(), tempdir());
    let doctrine = Arc::new(swlsp_doctrine::Index::new(doctrine_cache.path(), None).unwrap());
    let php = php_index(&php_cache);
    index_file(
        &php,
        "/project/vendor/dbal.php",
        r"<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}",
    );
    index_file(
        &php,
        "/project/src/Types.php",
        r"<?php
namespace App;
class Product {}
class MoneyType extends \Doctrine\DBAL\Types\Type {}",
    );
    let provider = DoctrineCompletionProvider::new(doctrine, Some(php));

    let yaml = "doctrine:\n  dbal:\n    types:\n      money:\n        class:\n";
    let xml = r#"<container xmlns:doctrine="urn:doctrine"><doctrine:config><doctrine:dbal><doctrine:type name="money" class=""/></doctrine:dbal></doctrine:config></container>"#;
    let php_string = r"<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => '']],
    ]);
};";
    let php_empty = r"<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => ]],
    ]);
};";
    let add_type = r"<?php
use Doctrine\DBAL\Types\Type;
Type::addType('money', );";
    let register = r"<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', );";
    let register_new = r"<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', new );";
    let tests: [(&str, &str, usize, &str, &str); 7] = [
        (
            "/project/config/packages/doctrine.yaml",
            yaml,
            yaml.trim_end().len(),
            "App\\MoneyType",
            "",
        ),
        (
            "/project/config/packages/doctrine.xml",
            xml,
            xml.find(r#"class="""#).unwrap() + r#"class=""#.len(),
            "App\\MoneyType",
            "",
        ),
        (
            "/project/config/packages/doctrine.php",
            php_string,
            php_string.find("=> '']").unwrap() + "=> '".len(),
            "App\\MoneyType",
            "",
        ),
        (
            "/project/config/packages/doctrine.php",
            php_empty,
            php_empty.find("=> ]]").unwrap() + "=> ".len(),
            "MoneyType",
            r"\App\MoneyType::class",
        ),
        (
            "/project/bootstrap.php",
            add_type,
            add_type.rfind(')').unwrap(),
            "MoneyType",
            r"\App\MoneyType::class",
        ),
        (
            "/project/bootstrap.php",
            register,
            register.rfind(')').unwrap(),
            "MoneyType",
            r"new \App\MoneyType()",
        ),
        (
            "/project/bootstrap.php",
            register_new,
            register_new.rfind(')').unwrap(),
            "MoneyType",
            r"\App\MoneyType()",
        ),
    ];
    for (path, source, offset, label, insert_text) in tests {
        let document = document(&format!("file://{path}"), source);
        let items = provider.get_completions(&ctx(), &request(&document, offset));
        let item = require_completion(&items, label);
        assert_eq!(item.detail, "Doctrine DBAL type class");
        if !insert_text.is_empty() {
            assert_eq!(item.insert_text, insert_text, "{path}");
        }
        assert!(
            items
                .iter()
                .all(|candidate| candidate.label != "App\\Product")
        );
    }
}
