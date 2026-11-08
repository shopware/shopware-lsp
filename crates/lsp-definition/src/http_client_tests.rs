use swlsp_uriutil::file_uri;

use super::*;
use crate::php_common::test_support::*;

#[test]
fn http_client_option_definition_navigates_interface_constant_key() {
    let root = tempfile::tempdir().unwrap();
    let interface_path = root.path().join("vendor/HttpClientInterface.php");
    let interface_source = r"<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
    ];
    public function request(string $method, string $url, array $options = []): object;
}";
    write(&interface_path, interface_source);
    let php = php_index(&root.path().join("cache"));
    index(&php, &p(&interface_path), interface_source);
    let consumer_path = p(&root.path().join("src/Consumer.php"));
    let consumer_source = r"<?php
use Symfony\Contracts\HttpClient\HttpClientInterface;
function fetch(HttpClientInterface $client): void {
    $client->request('GET', '/', ['timeout' => null]);
}";
    let document = document(&file_uri(&consumer_path), consumer_source);
    let offset = last_index_of(consumer_source, "'timeout'") + 2;
    let locations = definitions(
        &HttpClientDefinitionProvider::new(Some(php)),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&p(&interface_path)));
    assert_eq!(locations[0].range.start.line, 4);
    assert_eq!(locations[0].range.start.character, 8);
    assert_eq!(locations[0].range.end.character, 17);
}
