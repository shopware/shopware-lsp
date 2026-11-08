mod common;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::HttpClientHoverProvider;

#[test]
fn http_client_option_hover_shows_type_and_default() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        cache.path(),
        &[(
            &p(&root.path().join("vendor/HttpClientInterface.php")),
            r"<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
    ];
    public function withOptions(array $options): static;
}
",
        )],
    );
    let source = r"<?php
use Symfony\Contracts\HttpClient\HttpClientInterface;
function configure(HttpClientInterface $client): void {
    $client->withOptions(['timeout' => 2.0]);
}";
    let document = TextDocument::new(uri(&root.path().join("src/Consumer.php")), source, 1);
    let request = request_at(document, source.rfind("'timeout'").unwrap() + 2);
    let result = hover(&HttpClientHoverProvider::new(Some(php)), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Symfony HttpClient option"));
    assert!(value.contains("`timeout`"));
    assert!(value.contains("Default type: `null`"));
    assert!(value.contains("```php\nnull\n```"));
    let range = result.range.expect("range");
    assert_eq!(range.start.character, 27);
    assert_eq!(range.end.character, 34);
}
