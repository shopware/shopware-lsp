//! The action on an entity definition and the description it writes.

use std::sync::Arc;

use serde_json::json;

use swlsp_extension::ExtensionIndexer;
use swlsp_ide::codeaction::testing::{GenerationTestHost, request_at};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_shopware_adminmodule::parse;
use tempfile::TempDir;

use super::{ADD_ADMIN_MODULE_ACTION, AdminModuleActionProvider, addition, module_name};

const FRAMEWORK: &str = r"<?php
namespace Shopware\Core\Framework;
abstract class Bundle {}
abstract class Plugin extends Bundle {}
namespace Shopware\Core\Framework\DataAbstractionLayer;
abstract class EntityDefinition {}
";
const PLUGIN: &str = r"<?php
namespace Acme\Review;
use Shopware\Core\Framework\Plugin;
class AcmeReview extends Plugin {}
";
const DEFINITION: &str = r"<?php
namespace Acme\Review\Core\Content\Review;

use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;

class ReviewDefinition extends EntityDefinition
{
    public function getEntityName(): string
    {
        return 'acme_review';
    }

    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([new IdField('id', 'id')]);
    }
}
";

struct Project {
    provider: AdminModuleActionProvider,
    definition: String,
    description: String,
    _root: TempDir,
    _cache: TempDir,
}

fn project(description: Option<&str>) -> Project {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let base = root.path().canonicalize().unwrap();
    let plugin = format!("{}/custom/plugins/AcmeReview/src", base.to_str().unwrap());
    let php = PhpIndex::open(cache.path().join("php"), None).unwrap();
    let extensions =
        Arc::new(ExtensionIndexer::new(cache.path().join("extensions"), None).unwrap());
    extensions.set_php_index(Some(php.clone()));
    let dal = Arc::new(swlsp_shopware_dal::Index::new(cache.path().join("dal"), None).unwrap());
    let definition = format!("{plugin}/Core/Content/Review/ReviewDefinition.php");
    let write = |path: &str, source: &str| {
        std::fs::create_dir_all(std::path::Path::new(path).parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
    };
    for (path, source) in [
        (
            format!("{plugin}/../../../../vendor/framework.php"),
            FRAMEWORK,
        ),
        (format!("{plugin}/AcmeReview.php"), PLUGIN),
        (definition.clone(), DEFINITION),
    ] {
        write(&path, source);
        let file = ParsedFile::new(path, source.as_bytes().to_vec());
        php.index(&file).unwrap();
        extensions.index(&file).unwrap();
        dal.index(&file).unwrap();
    }
    let description_path = format!("{plugin}/Resources/shopware-lsp/admin-modules.yaml");
    if let Some(source) = description {
        write(&description_path, source);
    }
    Project {
        provider: AdminModuleActionProvider::new(
            dal,
            extensions,
            Arc::new(GenerationTestHost::default()),
        ),
        definition,
        description: description_path,
        _root: root,
        _cache: cache,
    }
}

fn actions(project: &Project, marker: &str) -> Vec<protocol::CodeAction> {
    let offset = DEFINITION.find(marker).unwrap() as u32;
    let uri = swlsp_uriutil::file_uri(&project.definition);
    project.provider.get_code_actions(
        &Context::background(),
        &request_at(&uri, DEFINITION, offset, offset),
    )
}

/// The description after the edit the command of `action` answers with.
fn written(project: &Project, action: &protocol::CodeAction) -> String {
    let command = action.command.as_ref().unwrap();
    assert_eq!(command.command, ADD_ADMIN_MODULE_ACTION);
    let response = project
        .provider
        .describe(
            &Context::background(),
            Some(&json!({ "uri": command.arguments[0], "className": command.arguments[1] })),
        )
        .unwrap();
    let uri = swlsp_uriutil::file_uri(&project.description);
    assert_eq!(response.primary_file_uri, uri);
    assert_eq!(response.module, "acme-review");
    let changes = &response.edit.as_ref().unwrap().document_changes;
    assert_eq!(response.created, changes[0].kind == "create");
    if changes[0].kind == "create" {
        assert_eq!(changes[0].uri, uri);
        return changes[1].edits[0].new_text.clone();
    }
    assert_eq!(changes.len(), 1);
    assert_eq!(changes[0].text_document.as_ref().unwrap().uri, uri);
    let source = std::fs::read_to_string(&project.description).unwrap();
    added(&source)
}

/// `source` with the module of the entity added.
fn added(source: &str) -> String {
    let (offset, text) = addition(source, "acme-review", "acme_review").unwrap();
    let mut result = source.to_owned();
    result.insert_str(usize::from(offset), &text);
    result
}

#[test]
fn the_class_of_an_entity_creates_the_description() {
    let project = project(None);
    let found = actions(&project, "ReviewDefinition");
    assert_eq!(found.len(), 1);
    assert_eq!(
        found[0].title,
        "Shopware: Add Administration module acme-review for acme_review"
    );
    assert_eq!(
        written(&project, &found[0]),
        "modules:\n  acme-review:\n    entity: acme_review\n"
    );
    // Not in the body of the class.
    assert_eq!(actions(&project, "getEntityName").len(), 0);
}

#[test]
fn an_existing_description_gets_the_module_added() {
    let source =
        "modules:\n    acme-other:\n        entity: acme_other\n        icon: regular-star\n";
    let project = project(Some(source));
    let found = actions(&project, "ReviewDefinition");
    assert_eq!(found.len(), 1);
    let result = written(&project, &found[0]);
    assert_eq!(
        result,
        "modules:\n    acme-other:\n        entity: acme_other\n        icon: regular-star\n    acme-review:\n        entity: acme_review\n"
    );
    assert_eq!(parse(&result).unwrap().modules.len(), 2);
}

#[test]
fn an_entity_with_a_module_gets_no_action() {
    let project = project(Some("modules:\n  acme-reviews:\n    entity: acme_review\n"));
    assert_eq!(actions(&project, "ReviewDefinition").len(), 0);
    let uri = swlsp_uriutil::file_uri(&project.definition);
    assert!(
        project
            .provider
            .describe(&Context::background(), Some(&json!({ "uri": uri })))
            .is_err()
    );
}

#[test]
fn the_module_is_added_wherever_the_description_leaves_room() {
    assert_eq!(
        added(""),
        "modules:\n  acme-review:\n    entity: acme_review\n"
    );
    assert_eq!(
        added("# Modules\nversion: 1"),
        "# Modules\nversion: 1\nmodules:\n  acme-review:\n    entity: acme_review\n"
    );
    assert_eq!(
        added("modules:\nversion: 1\n"),
        "modules:\n  acme-review:\n    entity: acme_review\nversion: 1\n"
    );
    // A description that does not decode, and a flow mapping, are left.
    assert_eq!(addition("modules: [", "acme-review", "acme_review"), None);
    assert_eq!(
        addition(
            "modules: { acme-other: { entity: acme_other } }\n",
            "acme-review",
            "acme_review"
        ),
        None
    );
}

#[test]
fn a_module_name_needs_a_prefix() {
    assert_eq!(
        module_name("acme_review_vote").as_deref(),
        Some("acme-review-vote")
    );
    assert_eq!(module_name("review"), None);
}
