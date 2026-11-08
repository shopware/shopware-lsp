use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use super::*;

fn node_at(source: &str, offset: usize) -> SyntaxNode {
    swlsp_parser_php::parse(source)
        .tree
        .root()
        .node_at(TextSize::from(offset as u32))
        .unwrap()
}

#[test]
fn options_use_persisted_interface_constant_metadata() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/vendor/HttpClientInterface.php",
            br"<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
        'verify_peer' => true,
    ];
}
"
            .to_vec(),
        ))
        .unwrap();

    let options = options(&index);
    assert_eq!(options.len(), 3);
    assert_eq!(options[0].name, "headers");
    assert_eq!(options[0].ty.to_string(), "array");
    assert_eq!(options[0].default, "[]");
    assert_eq!(options[1].name, "timeout");
    assert_eq!(options[1].ty.to_string(), "null");
    assert_eq!(options[2].name, "verify_peer");
    assert_eq!(options[2].ty.to_string(), "bool");
    index.close().unwrap();
}

#[test]
fn reference_at_recognizes_only_top_level_http_client_option_keys() {
    let cases = [
        (
            "request positional",
            "<?php\n$client->request('GET', '/', ['timeout' => null]);\n",
            true,
        ),
        (
            "request named",
            "<?php\n$client->request(options: ['timeout' => null], method: 'GET', url: '/');\n",
            true,
        ),
        (
            "with options",
            "<?php\n$client->withOptions(['timeout' => null]);\n",
            true,
        ),
        (
            "nested header",
            "<?php\n$client->request('GET', '/', ['headers' => ['timeout' => null]]);\n",
            false,
        ),
        (
            "option value",
            "<?php\n$client->request('GET', '/', ['label' => 'timeout']);\n",
            false,
        ),
        (
            "wrong argument",
            "<?php\n$client->request('GET', ['timeout' => null]);\n",
            false,
        ),
        (
            "unrelated method",
            "<?php\n$client->send(['timeout' => null]);\n",
            false,
        ),
    ];
    for (name, source, found) in cases {
        let node = node_at(source, source.find("timeout").unwrap() + 1);
        let reference = reference_at(&node);
        assert_eq!(reference.is_some(), found, "{name}");
        if let Some(reference) = reference {
            assert_eq!(reference.name, "timeout");
            assert_eq!(&source[reference.range], "timeout");
        }
    }
}

#[test]
fn used_option_names_excludes_current_key() {
    let source = "<?php\n$client->request('GET', '/', [\n    'headers' => [],\n    'timeout' => null,\n]);\n";
    let reference = reference_at(&node_at(source, source.find("timeout").unwrap() + 1)).unwrap();
    assert_eq!(
        used_option_names(&reference),
        BTreeSet::from(["headers".to_owned()])
    );
}

#[test]
fn used_option_names_keeps_separate_duplicate_of_current_key() {
    let source = "<?php\n$client->withOptions([\n    'timeout' => 1,\n    'timeout' => 2,\n]);\n";
    let reference = reference_at(&node_at(source, source.rfind("'timeout'").unwrap() + 1)).unwrap();
    assert_eq!(
        used_option_names(&reference),
        BTreeSet::from(["timeout".to_owned()])
    );
}
