//! Audits the inputs inspections declare (`ProblemAnalyzer::input_revision`).
//!
//! A refresh reuses an inspection's diagnostics while its declared input
//! revision is unchanged, so a declaration missing an input leaves stale
//! diagnostics. This test changes one indexed file at a time and requires
//! every inspection whose revision did not change to report exactly what it
//! reported before. It catches an undeclared input as long as the fixture
//! exercises it; extend the fixture and the changes when declaring a new
//! input. A change only proves a declaration when it advances that input
//! alone: editing a PHP file also advances the PHP input, so each index fed
//! by other files (translations, snippets, Twig) gets its own non-PHP edit.

use std::collections::HashMap;
use std::path::Path;
use std::time::{Duration, Instant};

use serde_json::json;
use swlsp_lsp::{Context, InspectionAudit, Server};

use super::support::{TestClient, file_uri, write_file};
use crate::workspace_factory_in;

const CONSUMER: &str = r#"<?php
namespace Acme;

use Psr\Container\ContainerInterface;
use Symfony\Component\Console\Application;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
use Symfony\Contracts\Translation\TranslatorInterface;

class Consumer
{
    public function __construct(
        #[\Symfony\Component\DependencyInjection\Attribute\Autowire(service: 'acme.greeter')]
        private object $greeter,
        private ContainerInterface $container,
        private Application $console,
        private EventDispatcherInterface $events,
        private TranslatorInterface $translator,
    ) {}

    public function run(): string
    {
        $this->container->get('acme.greeter')->greet();
        $this->console->find('acme:hello');
        $this->events->dispatch(new \stdClass(), 'acme.ready');
        return $this->translator->trans('acme.title');
    }
}
"#;

const PAGE: &str = r#"{% import 'storefront/macros.html.twig' as macros %}
{% block acme_page %}
    {{ macros.badge('x') }}
    {{ 'acme.snippet'|trans }}
    {{ 'acme.title'|trans }}
    {{ asset('bundles/acme/logo.svg') }}
    {{ path('acme.page') }}
    {% include 'storefront/part.html.twig' %}
{% endblock %}
"#;

/// The indexed files, their initial content (`None`: created by the change)
/// and the version each change writes.
const CHANGES: [(&str, Option<&str>, &str); 11] = [
    (
        "src/Resources/config/services.xml",
        Some("<container><services/></container>\n"),
        "<container><services><service id=\"acme.greeter\" class=\"Acme\\Greeter\"/></services></container>\n",
    ),
    (
        "src/Greeter.php",
        Some("<?php\nnamespace Acme;\nclass Greeter {}\n"),
        "<?php\nnamespace Acme;\nclass Greeter { public function greet(): string { return ''; } }\n",
    ),
    (
        "src/Command/HelloCommand.php",
        Some(
            "<?php\nnamespace Acme\\Command;\n#[\\Symfony\\Component\\Console\\Attribute\\AsCommand('acme:other')]\nclass HelloCommand {}\n",
        ),
        "<?php\nnamespace Acme\\Command;\n#[\\Symfony\\Component\\Console\\Attribute\\AsCommand('acme:hello')]\nclass HelloCommand {}\n",
    ),
    (
        "src/Resources/translations/messages.en.yaml",
        Some("acme:\n  other: Other\n"),
        "acme:\n  title: Title\n",
    ),
    (
        "src/Resources/snippet/en_GB/messages.en-GB.json",
        Some("{\"acme\": {\"other\": \"Other\"}}\n"),
        "{\"acme\": {\"snippet\": \"Snippet\"}}\n",
    ),
    (
        "src/Resources/views/storefront/macros.html.twig",
        Some("{% macro other() %}{% endmacro %}\n"),
        "{% macro badge(label) %}{{ label }}{% endmacro %}\n",
    ),
    ("src/Resources/public/logo.svg", None, "<svg/>\n"),
    (
        "src/Resources/config/routes.yaml",
        Some("acme.other:\n  path: /other\n  controller: Acme\\Consumer::run\n"),
        "acme.page:\n  path: /page\n  controller: Acme\\Consumer::run\n",
    ),
    (
        "src/Resources/views/storefront/part.html.twig",
        None,
        "<p>part</p>\n",
    ),
    (
        "src/Resources/app/storefront/src/scss/variables.scss",
        None,
        "$acme-color: red;\n",
    ),
    // The Administration index: a replaced component is reported once its
    // registration carries a deprecation note.
    (
        "src/Resources/app/administration/src/component/sw-switch-field/index.js",
        Some("Component.register('sw-switch-field', {});\n"),
        "/**\n * @deprecated tag:v6.8.0 - Will be removed, use mt-switch instead.\n */\nComponent.register('sw-switch-field', {});\n",
    ),
];

fn audit(server: &Server, uris: &[String]) -> HashMap<(String, String), InspectionAudit> {
    let ctx = Context::background();
    uris.iter()
        .flat_map(|uri| {
            server
                .audit_inspections(&ctx, uri)
                .into_iter()
                .map(|audit| ((uri.clone(), audit.id.clone()), audit))
        })
        .collect()
}

/// Audits until a declared input revision differs from `previous`.
fn wait_for_index_change(
    server: &Server,
    uris: &[String],
    previous: &HashMap<(String, String), InspectionAudit>,
) -> Option<HashMap<(String, String), InspectionAudit>> {
    let deadline = Instant::now() + Duration::from_secs(20);
    while Instant::now() < deadline {
        let current = audit(server, uris);
        let advanced = current.iter().any(|(key, now)| {
            now.input_revision.is_some()
                && previous
                    .get(key)
                    .is_some_and(|before| before.input_revision != now.input_revision)
        });
        if advanced {
            // The rest of the scanner run may still publish: return once two
            // consecutive audits agree on every revision.
            let mut settled = current;
            loop {
                std::thread::sleep(Duration::from_millis(50));
                let next = audit(server, uris);
                let same = next.len() == settled.len()
                    && next.iter().all(|(key, audit)| {
                        settled
                            .get(key)
                            .is_some_and(|other| other.input_revision == audit.input_revision)
                    });
                settled = next;
                if same || Instant::now() >= deadline {
                    return Some(settled);
                }
            }
        }
        std::thread::sleep(Duration::from_millis(25));
    }
    None
}

fn open(client: &TestClient, root: &Path, relative: &str) -> String {
    let path = root.join(relative);
    let uri = file_uri(&path);
    client.open(&uri, 1, &std::fs::read_to_string(&path).unwrap());
    uri
}

#[test]
fn inspections_declaring_inputs_report_the_same_while_their_revision_is_unchanged() {
    let project = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = project.path();
    write_file(
        root,
        "composer.json",
        r#"{"name":"acme/plugin","type":"shopware-platform-plugin","require":{"shopware/core":"6.7.0.0"},"autoload":{"psr-4":{"Acme\\":"src/"}},"extra":{"shopware-plugin-class":"Acme\\AcmePlugin"}}"#,
    );
    write_file(
        root,
        "src/AcmePlugin.php",
        "<?php\nnamespace Acme;\nclass AcmePlugin {}\n",
    );
    write_file(root, "src/Consumer.php", CONSUMER);
    write_file(
        root,
        "src/Resources/app/storefront/src/scss/base.scss",
        ".acme { color: $acme-color; }\n",
    );
    write_file(root, "src/Resources/views/storefront/page.html.twig", PAGE);
    write_file(
        root,
        "src/Resources/app/administration/src/view.html.twig",
        "<sw-switch-field />\n",
    );
    for (path, before, _) in CHANGES {
        if let Some(before) = before {
            write_file(root, path, before);
        }
    }

    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    server.set_workspace_factory(workspace_factory_in(cache.path().to_path_buf()));
    let client = TestClient::start(&server);
    client.initialize(root, json!({}));
    client.wait_notification("shopware/indexingCompleted");
    let uris = [
        open(&client, root, "src/Consumer.php"),
        open(
            &client,
            root,
            "src/Resources/views/storefront/page.html.twig",
        ),
        open(&client, root, "src/Resources/config/services.xml"),
        open(
            &client,
            root,
            "src/Resources/app/storefront/src/scss/base.scss",
        ),
        open(&client, root, "composer.json"),
        open(
            &client,
            root,
            "src/Resources/app/administration/src/view.html.twig",
        ),
    ];
    let uri_refs: Vec<&str> = uris.iter().map(String::as_str).collect();
    client.wait_diagnostics(&uri_refs);

    let mut previous = audit(&server, &uris);
    // Every inspection declares its inputs, so refreshes can skip documents.
    let undeclared: Vec<&(String, String)> = previous
        .iter()
        .filter(|(_, audit)| audit.input_revision.is_none())
        .map(|(key, _)| key)
        .collect();
    assert!(
        undeclared.is_empty(),
        "inspections without inputs: {undeclared:?}"
    );
    let declared = previous
        .values()
        .filter(|audit| audit.input_revision.is_some())
        .count();
    assert!(declared > 10, "only {declared} declared inspections ran");
    let mut changed_results = 0;
    for (path, _, after) in CHANGES {
        write_file(root, path, after);
        // The watcher reindexes the file. Documents whose declared inputs
        // did not change are not republished, so wait for the index change
        // itself: some declared revision advances once it is committed.
        let current = wait_for_index_change(&server, &uris, &previous)
            .unwrap_or_else(|| panic!("no declared input changed after editing {path}"));
        for (key, now) in &current {
            let Some(before) = previous.get(key) else {
                continue;
            };
            if now.input_revision.is_some() && now.input_revision == before.input_revision {
                assert_eq!(
                    now.diagnostics, before.diagnostics,
                    "{} on {} changed after editing {path} although its declared inputs did not",
                    key.1, key.0
                );
            } else if now.diagnostics != before.diagnostics {
                changed_results += 1;
            }
        }
        previous = current;
    }
    // The changes must exercise the inputs, or the audit proves nothing.
    assert!(
        changed_results >= CHANGES.len(),
        "only {changed_results} results changed"
    );
    client.shutdown().unwrap();
}
