use std::sync::Arc;

use swlsp_lsp::CompletionProvider;

use super::*;
use crate::SymfonyConfigCompletionProvider;

fn config_index(cache: &tempfile::TempDir) -> Arc<swlsp_symfonyconfig::Index> {
    Arc::new(swlsp_symfonyconfig::Index::new(cache.path(), None).unwrap())
}

#[test]
fn symfony_config_completion_for_root_and_conditional_php_arrays() {
    let cache = tempdir();
    let index = config_index(&cache);
    index_file(
        index.as_ref(),
        "/project/src/DependencyInjection/Configuration.php",
        r"<?php
namespace App\DependencyInjection;
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('app_root');
    }
}
",
    );
    let provider = SymfonyConfigCompletionProvider::new(index);
    for (uri, source) in [
        (
            "file:///project/config/packages/app.php",
            "<?php return ['app_' => []];",
        ),
        (
            "file:///project/config/packages/app.php",
            "<?php return ['when@prod' => ['app_' => []]];",
        ),
        ("file:///project/config/packages/app.yaml", "app_: {}\n"),
        (
            "file:///project/config/packages/app.yaml",
            "when@prod:\n  app_: {}\n",
        ),
    ] {
        let document = document(uri, source);
        let offset = source.rfind("app_").unwrap() + 2;
        let items = provider.get_completions(&ctx(), &request(&document, offset));
        require_completion(&items, "app_root");
    }
}

#[test]
fn symfony_config_completion_rejects_nested_ordinary_arrays() {
    let cache = tempdir();
    let index = config_index(&cache);
    for (uri, source) in [
        (
            "file:///project/config/packages/app.php",
            "<?php return ['data' => ['app_' => []]];",
        ),
        (
            "file:///project/config/packages/app.yaml",
            "data:\n  app_: {}\n",
        ),
    ] {
        let document = document(uri, source);
        let offset = source.rfind("app_").unwrap() + 2;
        assert!(
            SymfonyConfigCompletionProvider::new(index.clone())
                .get_completions(&ctx(), &request(&document, offset))
                .is_empty()
        );
    }
}
