use swlsp_lsp::CompletionProvider;

use super::*;
use crate::ResponseConstantCompletionProvider;

#[test]
fn response_constants_complete_set_status_code_and_comparisons() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let php = php_index(&cache);
    index_file(
        &php,
        &format!("{root}/vendor/Response.php"),
        r"<?php
namespace Symfony\Component\HttpFoundation;
class Response {
    public const HTTP_OK = 200;
    public const HTTP_NOT_FOUND = 404;
    public const INTERNAL_VALUE = 'not a status';
    public function setStatusCode(int $code): static { return $this; }
    public function getStatusCode(): int { return 200; }
}",
    );
    let provider = ResponseConstantCompletionProvider::new(Some(php));
    let tests = [
        (
            "SetStatus.php",
            r"<?php
namespace App;
use Symfony\Component\HttpFoundation\Response;
function update(Response $response): void {
    $response->setStatusCode();
}",
            "setStatusCode(",
        ),
        (
            "CompareStatus.php",
            r"<?php
namespace App;
use Symfony\Component\HttpFoundation\Response as HttpResponse;
function missing(HttpResponse $response): bool {
    return $response->getStatusCode() === 0;
}",
            "=== ",
        ),
    ];
    for (index, (file, source, marker)) in tests.into_iter().enumerate() {
        let document = document(
            &swlsp_uriutil::file_uri(&format!("{root}/src/{file}")),
            source,
        );
        let offset = if marker.ends_with('(') {
            source.find(marker).unwrap() + marker.len()
        } else {
            source.rfind('0').unwrap()
        };
        let labels = labels(&provider.get_completions(&ctx(), &request(&document, offset)));
        if index == 0 {
            assert!(
                labels.contains(&"Response::HTTP_OK".to_string()),
                "{labels:?}"
            );
            assert!(labels.contains(&"Response::HTTP_NOT_FOUND".to_string()));
        } else {
            assert!(
                labels.contains(&"HttpResponse::HTTP_OK".to_string()),
                "{labels:?}"
            );
        }
        assert!(labels.iter().all(|label| !label.contains("INTERNAL_VALUE")));
    }
}

#[test]
fn response_constants_require_typed_symfony_response_context() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let php = php_index(&cache);
    let source = r"<?php
class CustomResponse {
    public function setStatusCode(int $code): void {}
}
function update(CustomResponse $response): void {
    $response->setStatusCode();
}";
    let path = format!("{root}/Custom.php");
    index_file(&php, &path, source);
    let document = document(&swlsp_uriutil::file_uri(&path), source);
    let offset = source.find("setStatusCode(").unwrap() + "setStatusCode(".len();
    assert!(
        ResponseConstantCompletionProvider::new(Some(php))
            .get_completions(&ctx(), &request(&document, offset))
            .is_empty()
    );
}

#[test]
fn response_constants_are_empty_without_php_index() {
    let root = tempdir();
    let path = format!("{}/SetStatus.php", root.path().display());
    let source = r"<?php
use Symfony\Component\HttpFoundation\Response;
function update(Response $response): void {
    $response->setStatusCode();
}";
    let document = document(&swlsp_uriutil::file_uri(&path), source);
    let offset = source.find("setStatusCode(").unwrap() + "setStatusCode(".len();
    assert!(
        ResponseConstantCompletionProvider::new(None)
            .get_completions(&ctx(), &request(&document, offset))
            .is_empty()
    );
}
