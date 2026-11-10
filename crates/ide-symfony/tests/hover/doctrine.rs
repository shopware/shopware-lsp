use std::sync::Arc;

use crate::common::*;
use swlsp_doctrine::Index;
use swlsp_ide_symfony::hover::DoctrineHoverProvider;
use swlsp_lsp::TextDocument;

#[test]
fn doctrine_hover_describes_type_registration() {
    let cache = tempfile::tempdir().unwrap();
    let doctrine = Arc::new(Index::new(cache.path().join("doctrine"), None).unwrap());
    let php = php_index(&cache.path().join("php"), &[]);
    let source = r"<?php
use App\Doctrine\MoneyType;

return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => MoneyType::class]],
    ]);
};";
    let document = TextDocument::new("file:///project/config/packages/doctrine.php", source, 1);
    let request = request_at(document, source.find("'money'").unwrap() + 1);
    let result = hover(
        &DoctrineHoverProvider::new(Some(doctrine), Some(php)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Doctrine DBAL type registration"));
    assert!(value.contains("money"));
    assert!(value.contains("App\\Doctrine\\MoneyType"));
}
