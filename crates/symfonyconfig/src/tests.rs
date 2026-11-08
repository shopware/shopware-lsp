use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use super::*;

fn node_at(file: &ParsedFile, offset: usize) -> swlsp_syntax::SyntaxNode {
    file.syntax_tree()
        .unwrap()
        .root()
        .node_at(TextSize::from(offset as u32))
        .unwrap()
}

#[test]
fn roots_in_php_collect_modern_and_legacy_tree_builders() {
    let source = r"<?php
namespace App\DependencyInjection;

use Symfony\Component\Config\Definition\Builder\TreeBuilder;
use Symfony\Component\Config\Definition\ConfigurationInterface;

final class ModernConfiguration implements ConfigurationInterface
{
    public function getConfigTreeBuilder(): TreeBuilder
    {
        $treeBuilder = new TreeBuilder('app_modern');
        return $treeBuilder;
    }
}

final class LegacyConfiguration implements ConfigurationInterface
{
    public function getConfigTreeBuilder(): TreeBuilder
    {
        return (new TreeBuilder())->root('app_legacy');
    }

    private function helper(): TreeBuilder
    {
        return new TreeBuilder('not_a_root');
    }
}
";
    let file = ParsedFile::new(
        "/project/src/DependencyInjection/Configuration.php",
        source.as_bytes().to_vec(),
    );
    let roots = roots_in_php(file.path(), &file.syntax_tree().unwrap().root());
    assert_eq!(roots.len(), 2);
    assert_eq!(roots[0].name, "app_modern");
    assert_eq!(
        roots[0].class,
        "App\\DependencyInjection\\ModernConfiguration"
    );
    assert_eq!(&source[roots[0].range], "app_modern");
    assert_eq!(roots[1].name, "app_legacy");
    assert_eq!(&source[roots[1].range], "app_legacy");
}

#[test]
fn index_restores_and_removes_configuration_roots() {
    let dir = tempfile::tempdir().unwrap();
    let index = Index::new(dir.path(), None).unwrap();
    let path = "/project/src/DependencyInjection/Configuration.php";
    let source = r"<?php
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('app_root');
    }
}
";
    index
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
    assert_eq!(index.names().unwrap(), ["app_root"]);
    index.close().unwrap();

    let restored = Index::new(dir.path(), None).unwrap();
    let roots = restored.roots("APP_ROOT").unwrap();
    assert_eq!(roots.len(), 1);
    assert_eq!(roots[0].file, path);
    restored
        .index(&ParsedFile::new(
            path,
            b"<?php final class Configuration {}".to_vec(),
        ))
        .unwrap();
    assert!(restored.roots("app_root").unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn php_config_root_and_resource_references() {
    let source = r"<?php
return [
    'framework' => [],
    'when@prod' => [
        'twig' => [],
        'imports' => [
            ['resource' => 'legacy.php'],
        ],
    ],
    'nested' => ['framework' => []],
];
";
    let file = ParsedFile::new(
        "/project/config/packages/app.php",
        source.as_bytes().to_vec(),
    );
    for name in ["framework", "twig"] {
        let offset = source.find(&format!("'{name}'")).unwrap() + 2;
        let reference = root_reference_at(&node_at(&file, offset)).expect(name);
        assert_eq!(reference.name, name);
    }
    for name in ["when@prod", "imports"] {
        let offset = source.find(&format!("'{name}'")).unwrap() + 2;
        assert!(
            root_reference_at(&node_at(&file, offset)).is_none(),
            "{name}"
        );
    }
    let nested = source.rfind("'framework'").unwrap() + 2;
    assert!(root_reference_at(&node_at(&file, nested)).is_none());

    let resource_offset = source.find("legacy.php").unwrap() + 2;
    let resource = resource_reference_at(&node_at(&file, resource_offset)).unwrap();
    assert_eq!(resource.path, "legacy.php");
    assert_eq!(&source[resource.range], "legacy.php");
}

#[test]
fn php_config_references_accept_config_factory_and_reject_class_returns() {
    let source = r"<?php
App::config(['framework' => []]);
final class NotConfig {
    public function data(): array {
        return ['twig' => []];
    }
}
";
    let file = ParsedFile::new("/project/config/app.php", source.as_bytes().to_vec());
    let offset = source.find("framework").unwrap() + 2;
    assert!(root_reference_at(&node_at(&file, offset)).is_some());
    let offset = source.find("twig").unwrap() + 2;
    assert!(root_reference_at(&node_at(&file, offset)).is_none());
}

#[test]
fn yaml_config_root_and_resource_references() {
    let source = r"framework: {}
when@prod:
  twig: {}
  imports:
    - resource: 'legacy_*.yaml'
nested:
  framework: {}
services:
  App\Service\:
    resource: ../src/
";
    let file = ParsedFile::new(
        "/project/config/packages/app.yaml",
        source.as_bytes().to_vec(),
    );
    for name in ["framework", "twig"] {
        let offset = source.find(name).unwrap() + 2;
        let reference = root_reference_at(&node_at(&file, offset)).expect(name);
        assert_eq!(reference.name, name);
    }
    for name in ["when@prod", "imports"] {
        let offset = source.find(name).unwrap() + 2;
        assert!(
            root_reference_at(&node_at(&file, offset)).is_none(),
            "{name}"
        );
    }
    let nested = source.rfind("framework").unwrap() + 2;
    assert!(root_reference_at(&node_at(&file, nested)).is_none());

    let offset = source.find("legacy_*.yaml").unwrap() + 2;
    let reference = resource_reference_at(&node_at(&file, offset)).unwrap();
    assert_eq!(reference.path, "legacy_*.yaml");
    assert_eq!(&source[reference.range], "legacy_*.yaml");

    let offset = source.find("../src/").unwrap() + 2;
    assert!(resource_reference_at(&node_at(&file, offset)).is_none());
}

#[test]
fn resource_files_resolve_relative_paths_and_globs() {
    let dir = tempfile::tempdir().unwrap();
    let config = dir.path().join("config");
    std::fs::create_dir_all(config.join("packages/sub")).unwrap();
    for name in ["b.yaml", "a.yaml", "c.xml"] {
        std::fs::write(config.join("packages").join(name), "").unwrap();
    }
    std::fs::write(config.join("packages/sub/d.yaml"), "").unwrap();
    let current = config.join("services.yaml");
    let current = current.to_str().unwrap();
    let packages = config.join("packages");
    let packages = packages.to_str().unwrap();

    assert_eq!(
        resource_files(current, "packages/*.yaml"),
        [format!("{packages}/a.yaml"), format!("{packages}/b.yaml")]
    );
    assert_eq!(
        resource_files(current, "./packages/../packages/c.xml"),
        [format!("{packages}/c.xml")]
    );
    assert!(resource_files(current, "packages").is_empty());
    assert!(resource_files(current, "missing.yaml").is_empty());
    assert!(resource_files(current, "").is_empty());
    assert!(resource_files(current, "bad\0path").is_empty());
    assert_eq!(
        resource_files(current, &format!("{packages}/sub/*.yaml")),
        [format!("{packages}/sub/d.yaml")]
    );
}
