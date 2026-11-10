//! Doctrine DBAL completion and definitions against the Shopware schema.

use std::sync::Arc;

use swlsp_lsp::{
    CompletionProvider, CompletionRequest, Context, DefinitionRequest, GotoDefinitionProvider,
    HoverProvider, HoverRequest, SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use super::workspace_test::{index_all, open_workspace, write_files};

#[test]
fn workspace_doctrine_dbal_uses_shopware_schema() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    let definition_path = root.join("ProductDefinition.php");
    write_files(
        root,
        &[
            (
                "ProductDefinition.php",
                r#"<?php
class ProductDefinition extends EntityDefinition {
 public const ENTITY_NAME = 'product';
 protected function defineFields(): FieldCollection {
  return new FieldCollection([
   new StringField('product_name', 'name'),
   new ManyToOneAssociationField('category', 'category_id', CategoryDefinition::class),
  ]);
 }
}"#,
            ),
            (
                "dbal.php",
                "<?php\nnamespace Doctrine\\DBAL;\nclass Connection { public function insert(string $table, array $data): void {} }\n",
            ),
            (
                "orm.php",
                r#"<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\Table(name: 'orm_only')]
class ORMEntity { #[ORM\Column] private string $ormField; }
"#,
            ),
        ],
    );
    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let php = workspace.services().php.clone();
    let dbal = workspace.services().doctrine.clone();
    let source = r#"<?php
use Doctrine\DBAL\Connection;
function write(Connection $connection): void {
 $connection->insert('product', ['product_name' => 'value']);
}"#;
    let uri = swlsp_uriutil::file_uri(&root.join("usage.php").to_string_lossy());
    let document = Arc::new(TextDocument::new(uri.clone(), source, 1));
    let definition_uri = swlsp_uriutil::file_uri(&definition_path.to_string_lossy());
    let ctx = Context::background();
    for needle in ["product'", "product_name'"] {
        let offset = (source.find(needle).unwrap() + 1) as u32;
        let root_node = document.root();
        let node = root_node
            .as_ref()
            .and_then(|root| root.node_at(TextSize::from(offset)));
        let syntax = SyntaxContext {
            language: document.language,
            document: Some(document.clone()),
            root: root_node,
            token: None,
            node,
        };
        let (line, character) = document.line_index().position_utf16(offset);
        let position = protocol::Position { line, character };
        let mut completion_params = protocol::CompletionParams::default();
        completion_params.text_document.uri = uri.clone();
        completion_params.position = position;
        let mut definition_params = protocol::DefinitionParams::default();
        definition_params.text_document.uri = uri.clone();
        definition_params.position = position;
        let mut hover_params = protocol::HoverParams::default();
        hover_params.text_document.uri = uri.clone();
        hover_params.position = position;

        let items = swlsp_ide_symfony::completion::DoctrineCompletionProvider::new(
            dbal.clone(),
            Some(php.clone()),
        )
        .get_completions(
            &ctx,
            &CompletionRequest {
                params: completion_params,
                syntax: syntax.clone(),
            },
        );
        let labels: Vec<&str> = items.iter().map(|item| item.label.as_str()).collect();
        if needle == "product'" {
            assert!(labels.contains(&"product"), "{needle}: {labels:?}");
            assert!(!labels.contains(&"orm_only"), "{needle}: {labels:?}");
        } else {
            assert!(labels.contains(&"product_name"), "{needle}: {labels:?}");
            assert!(!labels.contains(&"category_id"), "{needle}: {labels:?}");
        }
        let locations = swlsp_ide_symfony::definition::DoctrineDefinitionProvider::new(
            Some(dbal.clone()),
            Some(php.clone()),
        )
        .get_definition(
            &ctx,
            &DefinitionRequest {
                params: definition_params,
                syntax: syntax.clone(),
            },
        );
        assert_eq!(1, locations.len(), "{needle}: {locations:?}");
        assert_eq!(definition_uri, locations[0].uri);
        let hover = swlsp_ide_symfony::hover::DoctrineHoverProvider::new(
            Some(dbal.clone()),
            Some(php.clone()),
        )
        .get_hover(
            &ctx,
            &HoverRequest {
                params: hover_params,
                syntax,
            },
        )
        .unwrap()
        .expect("hover");
        assert!(
            hover.contents.value.contains("ProductDefinition"),
            "{needle}: {}",
            hover.contents.value
        );
    }
    workspace.close_workspace().unwrap();
}
