use std::fs;
use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, DocumentLinkProvider, DocumentLinkRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_symfonyconfig::Index as ConfigIndex;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use super::RelatedProvider;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn write(path: &Path, content: &str) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, content).unwrap();
}

fn request(document: TextDocument) -> DocumentLinkRequest {
    let mut params = protocol::DocumentLinkParams::default();
    params.text_document.uri = document.uri.clone();
    DocumentLinkRequest {
        params,
        document: Arc::new(document),
    }
}

#[test]
fn related_document_links_resolve_unambiguous_twig_templates() {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(root.path().join("cache"), None).unwrap());
    let base = root.path().join("templates/base.html.twig");
    let fallback = root.path().join("templates/fallback.html.twig");
    for path in [&base, &fallback] {
        write(path, "template");
        index
            .index(&ParsedFile::new(p(path), b"template".to_vec()))
            .unwrap();
    }
    // The same logical name in two namespaces is deliberately ambiguous.
    for namespace in ["one", "two"] {
        let path = root
            .path()
            .join("vendor")
            .join(namespace)
            .join("templates/shared.html.twig");
        index
            .index(&ParsedFile::new(p(&path), b"shared".to_vec()))
            .unwrap();
    }
    let source = "{% extends 'base.html.twig' %}
{% include ['missing.html.twig', 'fallback.html.twig'] %}
{% include 'shared.html.twig' %}";
    let document = TextDocument::new(
        file_uri(&p(&root.path().join("templates/page.html.twig"))),
        source,
        1,
    );
    let links = RelatedProvider::new(Some(index), None, None)
        .get_document_links(&Context::background(), &request(document))
        .unwrap();
    assert_eq!(links.len(), 2);
    assert_eq!(links[0].target, file_uri(&p(&base)));
    assert_eq!(links[1].target, file_uri(&p(&fallback)));
    assert_eq!(links[0].range.start.line, 0);
    assert_eq!(links[1].range.start.line, 1);
    assert!(
        links[0]
            .tooltip
            .contains("extends template \"base.html.twig\"")
    );
}

#[test]
fn related_document_links_resolve_routing_and_configuration_resources() {
    let root = tempfile::tempdir().unwrap();
    let config_path = root.path().join("config/packages/app.yaml");
    let shared = root.path().join("config/packages/shared.yaml");
    let controller = root.path().join("src/Controller.php");
    let configuration_path = root
        .path()
        .join("src/DependencyInjection/Configuration.php");
    for path in [&shared, &controller, &configuration_path] {
        write(path, "target");
    }
    let configuration = Arc::new(ConfigIndex::new(root.path().join("config-cache"), None).unwrap());
    configuration
        .index(&ParsedFile::new(
            p(&configuration_path),
            br"<?php
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('framework');
    }
}
"
            .to_vec(),
        ))
        .unwrap();
    let source = "imports:
  - resource: shared.yaml
controllers:
  resource: ../../src/Controller.php
  type: attribute
framework: {}";
    let document = TextDocument::new(file_uri(&p(&config_path)), source, 1);
    let links = RelatedProvider::new(None, Some(configuration), None)
        .get_document_links(&Context::background(), &request(document))
        .unwrap();
    assert_eq!(links.len(), 3);
    let targets: Vec<_> = links.iter().map(|link| link.target.clone()).collect();
    assert_eq!(
        targets,
        vec![
            file_uri(&p(&shared)),
            file_uri(&p(&controller)),
            file_uri(&p(&configuration_path)),
        ]
    );
    let lines: Vec<_> = links.iter().map(|link| link.range.start.line).collect();
    assert_eq!(lines, vec![1, 3, 5]);
    assert_eq!(
        links[2].tooltip,
        "Open \"framework\" configuration declaration"
    );
}

#[test]
fn related_document_links_skip_glob_and_multiple_targets() {
    let root = tempfile::tempdir().unwrap();
    for name in ["one.yaml", "two.yaml"] {
        write(&root.path().join("config/routes").join(name), "route");
    }
    let document = TextDocument::new(
        file_uri(&p(&root.path().join("config/routes.yaml"))),
        "routes:
  resource: routes/*.yaml
  type: yaml",
        1,
    );
    let links = RelatedProvider::new(None, None, None)
        .get_document_links(&Context::background(), &request(document))
        .unwrap();
    assert!(links.is_empty());
}
