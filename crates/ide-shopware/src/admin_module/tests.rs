//! The module description of a plugin against indexed entities and files
//! on disk.

use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{BoxError, Context, DocumentResolver, DocumentSnapshot, Inspection, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_rewrite::WorkspacePlan;
use swlsp_shopware_adminmodule::CONFIG_RELATIVE_PATH;
use swlsp_shopware_entityschema as entityschema;
use swlsp_uriutil::file_uri;

use super::{Generator, new_admin_module};
use crate::inspections::test_support::{check_diagnostics, fix_context, quick_fix_with_id};

const FRAMEWORK: &str = "<?php
namespace Shopware\\Core\\Framework\\DataAbstractionLayer;
abstract class EntityDefinition {}
";

fn definition(extra: &str) -> String {
    format!(
        r"<?php declare(strict_types=1);

namespace Acme\Review\Core\Content\Review;

use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\BoolField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;

class ReviewDefinition extends EntityDefinition
{{
    public const ENTITY_NAME = 'acme_review';

    public function getEntityName(): string
    {{
        return self::ENTITY_NAME;
    }}

    protected function defineFields(): FieldCollection
    {{
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new PrimaryKey(), new Required()),
            (new StringField('title', 'title'))->addFlags(new Required()),
            new BoolField('active', 'active'),{extra}
            (new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new Required()),
            new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', false),
        ]);
    }}
}}
"
    )
}

/// A plugin on disk with its entity indexed.
struct Plugin {
    root: tempfile::TempDir,
    _caches: [tempfile::TempDir; 3],
    php: PhpIndex,
    sources: Arc<entityschema::SourceIndex>,
    inspection: Arc<dyn Inspection>,
}

impl Plugin {
    fn new() -> Self {
        let root = tempfile::tempdir().unwrap();
        let caches = [(); 3].map(|()| tempfile::tempdir().unwrap());
        let php = PhpIndex::open(caches[0].path(), None).unwrap();
        let sources = Arc::new(entityschema::SourceIndex::new(caches[1].path(), None).unwrap());
        let dal = Arc::new(swlsp_shopware_dal::Index::new(caches[2].path(), None).unwrap());
        let catalog = Arc::new(entityschema::IndexedCatalog::new(
            php.clone(),
            Some(sources.clone()),
            None,
        ));
        let inspection = new_admin_module(Generator::new(catalog, dal, Default::default()));
        let plugin = Plugin {
            root,
            _caches: caches,
            php,
            sources,
            inspection,
        };
        plugin.index("vendor/shopware/EntityDefinition.php", FRAMEWORK);
        plugin.index(
            "src/Core/Content/Review/ReviewDefinition.php",
            &definition(""),
        );
        plugin
    }

    fn path(&self, relative: &str) -> String {
        self.root
            .path()
            .join(relative)
            .to_string_lossy()
            .into_owned()
    }

    fn index(&self, relative: &str, source: &str) {
        let path = self.path(relative);
        std::fs::create_dir_all(Path::new(&path).parent().unwrap()).unwrap();
        std::fs::write(&path, source).unwrap();
        let file = ParsedFile::new(path.as_str(), source.as_bytes().to_vec());
        self.php.index(&file).unwrap();
        self.sources.index(&file).unwrap();
    }

    fn uri(&self) -> String {
        file_uri(&self.path(CONFIG_RELATIVE_PATH))
    }

    /// Checks the diagnostics of the description `fixture` marks.
    fn check(&self, fixture: &str) {
        check_diagnostics(self.inspection.as_ref(), &self.uri(), fixture);
    }

    /// Applies the fix of the one problem `fixture` marks to the disk and
    /// returns the paths it wrote and removed, relative to the plugin.
    fn generate(&self, fixture: &str) -> (Vec<String>, Vec<String>, Vec<String>) {
        let (document, problems) =
            check_diagnostics(self.inspection.as_ref(), &self.uri(), fixture);
        let problem = problems
            .iter()
            .find(|problem| !problem.fixes.is_empty())
            .expect("a problem with a fix");
        let fix = quick_fix_with_id(self.inspection.as_ref(), "generate-admin-modules");
        let context = fix_context(&document, problem, &problem.fixes[0], Some(Arc::new(Disk)));
        let plan = fix
            .as_rewrite()
            .unwrap()
            .build(&Context::background(), &context)
            .unwrap();
        self.apply(&plan)
    }

    fn apply(&self, plan: &WorkspacePlan) -> (Vec<String>, Vec<String>, Vec<String>) {
        let relative = |uri: &str| {
            let path = swlsp_uriutil::path(uri).unwrap();
            let root = self.path("");
            path.strip_prefix(root.trim_end_matches('/'))
                .unwrap()
                .trim_start_matches('/')
                .to_owned()
        };
        let mut result = (Vec::new(), Vec::new(), Vec::new());
        for create in &plan.creates {
            let path = swlsp_uriutil::path(&create.uri).unwrap();
            assert!(!Path::new(&path).exists(), "{path} exists");
            std::fs::create_dir_all(Path::new(&path).parent().unwrap()).unwrap();
            std::fs::write(&path, &create.content).unwrap();
            result.0.push(relative(&create.uri));
        }
        for document in &plan.documents {
            let path = swlsp_uriutil::path(&document.uri).unwrap();
            std::fs::write(&path, document.apply().unwrap()).unwrap();
            result.1.push(relative(&document.uri));
        }
        for delete in &plan.deletes {
            std::fs::remove_file(swlsp_uriutil::path(&delete.uri).unwrap()).unwrap();
            result.2.push(relative(&delete.uri));
        }
        result
    }

    fn read(&self, relative: &str) -> String {
        std::fs::read_to_string(self.path(relative)).unwrap()
    }
}

/// Reads every document from disk.
struct Disk;

impl DocumentResolver for Disk {
    fn resolve_document(&self, _ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        let source = std::fs::read_to_string(swlsp_uriutil::path(uri)?)?;
        Ok(DocumentSnapshot {
            document: Arc::new(TextDocument::new(uri.to_owned(), &source, 0)),
            version: None,
        })
    }
}

const MODULE: &str = "src/Resources/app/administration/src/module/acme-review";
const MAIN: &str = "src/Resources/app/administration/src/main.js";
const DESCRIBED: &str = "modules:\n  acme-review:\n    entity: acme_review\n";
const OUTDATED: &str =
    "modules:\n  {|shopware.admin_module.outdated:acme-review|}:\n    entity: acme_review\n";

#[test]
fn a_described_module_is_generated_and_then_up_to_date() {
    let plugin = Plugin::new();
    let (created, updated, deleted) = plugin.generate(OUTDATED);
    assert_eq!(
        created,
        [
            MAIN.to_owned(),
            format!("{MODULE}/generated/acl.js"),
            format!("{MODULE}/generated/index.js"),
            format!("{MODULE}/generated/page/acme-review-detail/acme-review-detail.html.twig"),
            format!("{MODULE}/generated/page/acme-review-detail/index.js"),
            format!("{MODULE}/generated/page/acme-review-list/acme-review-list.html.twig"),
            format!("{MODULE}/generated/page/acme-review-list/index.js"),
            format!("{MODULE}/generated/snippet/de-DE.json"),
            format!("{MODULE}/generated/snippet/en-GB.json"),
        ]
    );
    assert_eq!((updated, deleted), (Vec::new(), Vec::new()));
    assert_eq!(
        plugin.read(MAIN),
        "import './module/acme-review/generated';\n"
    );
    // The form is derived from the entity: the association is written
    // through its foreign key, the identifier has no input.
    let detail = plugin.read(&format!(
        "{MODULE}/generated/page/acme-review-detail/acme-review-detail.html.twig"
    ));
    assert!(detail.contains("v-model=\"item.title\""), "{detail}");
    assert!(
        detail.contains("v-model:value=\"item.productId\""),
        "{detail}"
    );
    assert!(detail.contains("entity=\"product\""), "{detail}");
    assert!(!detail.contains("item.id\""), "{detail}");
    plugin.check(DESCRIBED);
}

#[test]
fn a_changed_entity_or_an_edited_file_is_reported_and_regenerated() {
    let plugin = Plugin::new();
    plugin.generate(OUTDATED);
    plugin.index(
        "src/Core/Content/Review/ReviewDefinition.php",
        &definition("\n            new StringField('author', 'author'),"),
    );
    let (created, updated, deleted) = plugin.generate(OUTDATED);
    assert_eq!((created, deleted), (Vec::new(), Vec::new()));
    assert!(
        updated.contains(&format!(
            "{MODULE}/generated/page/acme-review-detail/acme-review-detail.html.twig"
        )),
        "{updated:?}"
    );
    plugin.check(DESCRIBED);

    // An edited generated file is a difference like any other.
    let list = format!("{MODULE}/generated/page/acme-review-list/index.js");
    std::fs::write(plugin.path(&list), plugin.read(&list) + "// mine\n").unwrap();
    let (_, updated, _) = plugin.generate(OUTDATED);
    assert_eq!(updated, [list]);
    plugin.check(DESCRIBED);
}

#[test]
fn a_page_taken_over_keeps_its_content_and_is_never_written_again() {
    let plugin = Plugin::new();
    plugin.generate(OUTDATED);
    let generated = format!("{MODULE}/generated/page/acme-review-list/index.js");
    let edited = plugin.read(&generated) + "// mine\n";
    std::fs::write(plugin.path(&generated), &edited).unwrap();
    let taken = "modules:\n  {|shopware.admin_module.outdated:acme-review|}:\n    entity: acme_review\n    listing: { managed: false }\n";
    let (created, updated, deleted) = plugin.generate(taken);
    let own = format!("{MODULE}/page/acme-review-list");
    assert_eq!(
        created,
        [
            format!("{own}/acme-review-list.html.twig"),
            format!("{own}/index.js")
        ]
    );
    assert_eq!(updated, [format!("{MODULE}/generated/index.js")]);
    assert_eq!(
        deleted,
        [
            format!("{MODULE}/generated/page/acme-review-list/acme-review-list.html.twig"),
            generated
        ]
    );
    assert_eq!(plugin.read(&format!("{own}/index.js")), edited);
    assert!(
        plugin
            .read(&format!("{MODULE}/generated/index.js"))
            .contains("import('../page/acme-review-list')")
    );
    // The page is the plugin's now: a change of it is no difference.
    std::fs::write(
        plugin.path(&format!("{own}/index.js")),
        "export default {};\n",
    )
    .unwrap();
    plugin.check(
        "modules:\n  acme-review:\n    entity: acme_review\n    listing: { managed: false }\n",
    );
}

#[test]
fn a_module_no_longer_described_is_removed_with_its_import() {
    let plugin = Plugin::new();
    plugin.generate(OUTDATED);
    let own = format!("{MODULE}/override.js");
    std::fs::write(plugin.path(&own), "// mine\n").unwrap();
    std::fs::write(
        plugin.path(MAIN),
        "import './other';\nimport './module/acme-review/generated';\n",
    )
    .unwrap();
    let (created, updated, deleted) =
        plugin.generate("{|shopware.admin_module.outdated:modules|}: {}\n");
    assert_eq!(created, Vec::<String>::new());
    assert_eq!(updated, [MAIN]);
    assert_eq!(deleted.len(), 8);
    assert!(
        deleted
            .iter()
            .all(|path| path.starts_with(&format!("{MODULE}/generated/")))
    );
    assert_eq!(plugin.read(MAIN), "import './other';\n");
    assert_eq!(plugin.read(&own), "// mine\n");
    plugin.check("modules: {}\n");
}

#[test]
fn a_description_that_does_not_fit_is_reported_where_it_is_wrong() {
    let plugin = Plugin::new();
    plugin.check(
        "modules:
  acme-review:
    entity: acme_review
    {|shopware.admin_module.invalid:labelField|}: nope
  acme-other:
    {|shopware.admin_module.invalid:entity|}: acme_other
",
    );
    plugin.check(
        "modules:
  acme-review:
    entity: acme_review
    listing:
      columns:
        {|shopware.admin_module.invalid:nope|}: {}
    detail:
      cards:
        general:
          fields:
            {|shopware.admin_module.invalid:id|}: {}
",
    );
    // An unknown key is reported on the key, a later version at the start.
    plugin.check(
        "modules:
  acme-review:
    entity: acme_review
    {|shopware.admin_module.invalid:bogus|}: 1
",
    );
    plugin.check("{|shopware.admin_module.invalid:|}version: 2\n");
    // Another file of that name is none of the inspection's.
    check_diagnostics(
        plugin.inspection.as_ref(),
        "file:///project/config/admin-modules.yaml",
        "modules: 1\n",
    );
}
