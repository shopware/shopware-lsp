use super::types::PAGE;
use super::*;
use crate::twig::TwigCompletionProvider;

#[test]
fn twig_member_completions_mark_deprecated_php_members() {
    let cache = tmp();
    let php = php_index(&cache);
    let twig = twig_index(&cache);
    twig.set_dependencies(Some(php.clone()), None);
    let path = "/project/src/Twig/StringExtension.php";
    let source = r"<?php
namespace Foo {
    class Bar {
        /** @deprecated */
        public string $deprecatedProperty;

        public function getActive(): string { return ''; }

        /** @deprecated */
        public function getDeprecated(): static { return $this; }
    }
}
namespace Twig\Extra\String {
    class StringExtension extends \Twig_Extension {
        public function getFilters(): array {
            return [new \Twig_SimpleFilter('u', [$this, 'createBar'])];
        }
        public function getFunctions(): array {
            return [new \Twig_SimpleFunction('ustring', [$this, 'createBar'])];
        }
        public function createBar(?string $text = null): \Foo\Bar {
            return new \Foo\Bar();
        }
    }
}";
    index(&php, path, source);
    index(twig.as_ref(), path, source);
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, Some(php));
    for source in [
        r"{# @var bar \Foo\Bar #} {{ bar. }}",
        "{{ ustring('value'). }}",
        "{{ 'value'|u. }}",
    ] {
        let items = complete(
            &provider,
            &twig_completion_at(PAGE, source, source.rfind('.').unwrap() + 1),
        );
        let deprecated = require_completion(&items, "deprecated");
        assert!(deprecated.deprecated, "{source}");
        assert!(documentation(&deprecated).contains("Deprecated"));
        assert!(!require_completion(&items, "active").deprecated, "{source}");
    }
    let source = r"{# @var bar \Foo\Bar #} {{ bar. }}";
    let items = complete(
        &provider,
        &twig_completion_at(PAGE, source, source.rfind('.').unwrap() + 1),
    );
    assert!(require_completion(&items, "deprecatedProperty").deprecated);
}
