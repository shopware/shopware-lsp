use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_shopware_dal::Index as DalIndex;

use swlsp_lsp_diagnostics::DalEntityAnalyzer;

use crate::test_support::{apply_single, check_diagnostics, fix_context, quick_fix_with_id};
use crate::{SUGGESTION_FIX_ID, analyzer_inspection};

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
    let inspection = analyzer_inspection(DalEntityAnalyzer::new(Some(Arc::new(index))));
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        "file:///project/src/Resources/app/administration/consumer.ts",
        "Shopware.EntityDefinition.get('{|shopware.dal.entity-not-found:prodcut|}');",
    );
    let problem = &problems[0];
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
