//! End-to-end diagnostics of one document through the production LSP
//! session (`swlsp_cli::CliSession`, the path `check` uses): every
//! registered inspection runs against an indexed Shopware plugin. Each
//! iteration edits the document, so nothing is served from a cache. Guards
//! against inspections that rebuild index-wide data per document.

use std::path::Path;

use criterion::{Criterion, criterion_group, criterion_main};
use serde_json::json;
use swlsp_cli::{CliSession, SessionOptions};

fn write(root: &Path, relative: &str, content: &str) {
    let path = root.join(relative);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

/// A plugin whose documents reach the service, console, event,
/// translation, snippet, Twig and PHP indexes.
fn fixture(root: &Path) {
    write(
        root,
        "composer.json",
        r#"{"name":"acme/plugin","type":"shopware-platform-plugin","require":{"shopware/core":"6.6.0.0"},"autoload":{"psr-4":{"Acme\\":"src/"}},"extra":{"shopware-plugin-class":"Acme\\AcmePlugin"}}"#,
    );
    write(
        root,
        "src/AcmePlugin.php",
        "<?php\nnamespace Acme;\nclass AcmePlugin {}\n",
    );
    let mut services = String::from("<container><services>\n");
    let mut translations = String::from("acme:\n");
    let mut snippets = String::from("{\"acme\": {");
    for index in 0..60 {
        services.push_str(&format!(
            "<service id=\"acme.service_{index}\" class=\"Acme\\Service\\Service{index}\"><argument type=\"service\" id=\"acme.service_{}\"/></service>\n",
            (index + 1) % 60
        ));
        write(
            root,
            &format!("src/Service/Service{index}.php"),
            &format!(
                "<?php\nnamespace Acme\\Service;\nclass Service{index} {{ public function run(): string {{ return ''; }} }}\n"
            ),
        );
        translations.push_str(&format!("  key_{index}: Value {index}\n"));
        snippets.push_str(&format!("\"snippet_{index}\": \"Value\","));
    }
    services.push_str("</services></container>\n");
    snippets.push_str("\"last\": \"Value\"}}\n");
    write(root, "src/Resources/config/services.xml", &services);
    write(
        root,
        "src/Resources/translations/messages.en.yaml",
        &translations,
    );
    write(
        root,
        "src/Resources/snippet/en_GB/messages.en-GB.json",
        &snippets,
    );
    write(
        root,
        "src/Resources/views/storefront/macros.html.twig",
        "{% macro badge(label) %}<span>{{ label }}</span>{% endmacro %}\n",
    );
}

fn controller() -> String {
    let mut methods = String::new();
    for index in 0..40 {
        methods.push_str(&format!(
            r#"
    #[\Symfony\Component\Routing\Attribute\Route('/acme/{index}', name: 'acme.page_{index}')]
    public function page{index}(): string
    {{
        $this->container->get('acme.service_{index}')->run();
        $this->console->find('acme:command_{index}');
        $this->events->dispatch(new \stdClass(), 'acme.event_{index}');
        return $this->translator->trans('acme.key_{index}');
    }}
"#
        ));
    }
    format!(
        r#"<?php
namespace Acme\Controller;

use Psr\Container\ContainerInterface;
use Symfony\Component\Console\Application;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
use Symfony\Contracts\Translation\TranslatorInterface;

class PageController
{{
    public function __construct(
        private ContainerInterface $container,
        private Application $console,
        private EventDispatcherInterface $events,
        private TranslatorInterface $translator,
    ) {{}}
{methods}}}
"#
    )
}

fn template() -> String {
    let mut body = String::from(
        "{% sw_extends '@Storefront/storefront/base.html.twig' %}\n{% import 'storefront/macros.html.twig' as macros %}\n",
    );
    for index in 0..40 {
        body.push_str(&format!(
            "{{% block acme_block_{index} %}}\n    {{{{ macros.badge('x') }}}}\n    {{{{ 'acme.snippet_{index}'|trans }}}}\n    {{{{ 'acme.key_{index}'|trans }}}}\n    {{{{ path('acme.page_{index}') }}}}\n{{% endblock %}}\n"
        ));
    }
    body
}

struct Bench {
    session: CliSession,
    version: i32,
}

impl Bench {
    fn diagnose(&mut self, uri: &str, source: &str) {
        self.version += 1;
        self.session
            .notify(
                "textDocument/didChange",
                json!({
                    "textDocument": {"uri": uri, "version": self.version},
                    "contentChanges": [{"text": source}],
                }),
            )
            .unwrap();
        std::hint::black_box(
            self.session
                .call_raw(
                    "textDocument/diagnostic",
                    json!({"textDocument": {"uri": uri}}),
                )
                .unwrap(),
        );
    }
}

fn diagnostics(c: &mut Criterion) {
    let project = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = project.path();
    fixture(root);
    let php = controller();
    let twig = template();
    write(root, "src/Controller/PageController.php", &php);
    write(root, "src/Resources/views/storefront/page.html.twig", &twig);
    // SAFETY: set before the session starts any thread.
    unsafe { std::env::set_var("SHOPWARE_LSP_CACHE_DIR", cache.path()) };
    let session = CliSession::new(
        &swlsp_cli::Cancel::new(),
        SessionOptions {
            root: root.to_string_lossy().into_owned(),
            version: "bench".to_owned(),
            err_out: None,
            start_index: true,
            allow_unsupported_project: false,
            watch_files: false,
            editor_configuration: None,
        },
    )
    .unwrap();
    session.wait_for_index().unwrap();
    let php_document = session
        .open_document(
            &root
                .join("src/Controller/PageController.php")
                .to_string_lossy(),
        )
        .unwrap();
    let twig_document = session
        .open_document(
            &root
                .join("src/Resources/views/storefront/page.html.twig")
                .to_string_lossy(),
        )
        .unwrap();
    let mut bench = Bench {
        session,
        version: 1,
    };

    c.bench_function("DiagnosticsPhpController", |b| {
        b.iter(|| bench.diagnose(&php_document.uri, &php))
    });
    c.bench_function("DiagnosticsTwigTemplate", |b| {
        b.iter(|| bench.diagnose(&twig_document.uri, &twig))
    });
    bench.session.close().unwrap();
}

criterion_group!(benches, diagnostics);
criterion_main!(benches);
