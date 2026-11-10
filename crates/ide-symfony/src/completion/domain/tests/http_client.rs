use swlsp_lsp::CompletionProvider;
use swlsp_lsp_protocol::CompletionItemKind;

use super::*;
use crate::completion::HttpClientCompletionProvider;

fn fixture(cache: &tempfile::TempDir, root: &str) -> swlsp_php::PhpIndex {
    let php = php_index(cache);
    index_file(
        &php,
        &format!("{root}/vendor/HttpClientInterface.php"),
        r"<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
        'verify_peer' => true,
    ];
    public function request(string $method, string $url, array $options = []): object;
    public function withOptions(array $options): static;
}
",
    );
    php
}

#[test]
fn http_client_option_completion_uses_typed_interface_defaults() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let php = fixture(&cache, &root);
    let source = r"<?php
namespace App;
use Symfony\Contracts\HttpClient\HttpClientInterface;
function fetch(HttpClientInterface $client): void {
    $client->request('GET', '/', [
        'headers' => [],
        'ti' => null,
    ]);
}";
    let document = document(
        &swlsp_uriutil::file_uri(&format!("{root}/src/Consumer.php")),
        source,
    );
    let offset = source.find("'ti'").unwrap() + 2;
    let items = HttpClientCompletionProvider::new(Some(php))
        .get_completions(&ctx(), &request(&document, offset));
    let timeout = require_completion(&items, "timeout");
    assert_eq!(timeout.kind, Some(CompletionItemKind::PROPERTY));
    assert!(timeout.detail.contains("null"), "{}", timeout.detail);
    assert!(
        timeout
            .documentation
            .as_ref()
            .unwrap()
            .value
            .contains("OPTIONS_DEFAULTS")
    );
    let edit = text_edit(timeout);
    assert_eq!(range_text(&document, edit.range), "ti");
    assert_eq!(edit.new_text, "timeout");
    let labels = labels(&items);
    assert!(!labels.contains(&"headers".to_string()));
    assert!(labels.contains(&"verify_peer".to_string()));
}

#[test]
fn http_client_option_completion_supports_named_with_options() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let php = fixture(&cache, &root);
    let source = r"<?php
namespace App;
use Symfony\Contracts\HttpClient\HttpClientInterface;
function configure(HttpClientInterface $client): void {
    $client->withOptions(options: ['' => null]);
}";
    let document = document(
        &swlsp_uriutil::file_uri(&format!("{root}/src/Configure.php")),
        source,
    );
    let offset = source.find("[''").unwrap() + 2;
    let mut labels = labels(
        &HttpClientCompletionProvider::new(Some(php))
            .get_completions(&ctx(), &request(&document, offset)),
    );
    labels.sort();
    assert_eq!(labels, ["headers", "timeout", "verify_peer"]);
}

#[test]
fn http_client_option_completion_requires_typed_client() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let php = fixture(&cache, &root);
    let source = r"<?php
namespace App;
class CustomClient {
    public function request(string $method, string $url, array $options): void {}
}
function fetch(CustomClient $client): void {
    $client->request('GET', '/', ['timeout' => null]);
}";
    let document = document(
        &swlsp_uriutil::file_uri(&format!("{root}/src/Custom.php")),
        source,
    );
    let offset = source.find("'timeout'").unwrap() + 2;
    assert!(
        HttpClientCompletionProvider::new(Some(php))
            .get_completions(&ctx(), &request(&document, offset))
            .is_empty()
    );
}
