use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_shopware_dal::Index as DalIndex;

use crate::SUGGESTION_FIX_ID;
use crate::shopware::*;
use crate::test_support::{apply_single, document, fix_context, inspect, quick_fix_with_id};

#[test]
fn dal_entity_inspection_builds_typo_replacement() {
    let dir = tempfile::tempdir().unwrap();
    let index = DalIndex::new(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/src/Core/Content/Product/ProductDefinition.php",
            br"<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([new IdField('id', 'id')]);
    }
}"
            .to_vec(),
        ))
        .unwrap();
    let document = document(
        "file:///project/src/Resources/app/administration/consumer.ts",
        "Shopware.EntityDefinition.get('prodcut');",
    );
    let inspection = new_dal_entity(Some(Arc::new(index)));
    let problems = inspect(inspection.as_ref(), &document);
    assert_eq!(problems.len(), 1);
    let problem = &problems[0];
    assert_eq!(problem.id.as_str(), "shopware.dal.entity-not-found");
    assert!(!problem.fixes.is_empty());
    let mut updated = String::new();
    for bound in &problem.fixes {
        if bound.id.as_str() != SUGGESTION_FIX_ID {
            continue;
        }
        let fix = quick_fix_with_id(inspection.as_ref(), bound.id.as_str());
        let candidate = apply_single(&fix, &fix_context(&document, problem, bound, None));
        if candidate.contains("'product'") {
            updated = candidate;
            break;
        }
    }
    assert_eq!(updated, "Shopware.EntityDefinition.get('product');");
}
