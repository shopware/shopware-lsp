use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{Context, DocumentSnapshot, Problem, RewriteQuickFix, TextDocument, bind_fix};
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::file_uri;

use crate::inspections::shopware::ADD_APP_READ_PERMISSION_FIX_ID;
use crate::inspections::shopware::app_script::{AppPermissionPayload, AppReadPermissionFix};
use crate::inspections::test_support::{StaticDocumentResolver, fix_context};

#[test]
fn app_read_permission_fix_updates_manifest_structurally() {
    let cases = [
        (
            "creates permissions",
            "<?xml version=\"1.0\"?>\n<manifest>\n    <meta/>\n</manifest>\n",
            "    <permissions>\n        <read>product</read>\n    </permissions>\n",
        ),
        (
            "appends read permission",
            "<?xml version=\"1.0\"?>\n<manifest>\n    <permissions>\n        <read>order</read>\n    </permissions>\n</manifest>\n",
            "        <read>order</read>\n        <read>product</read>\n",
        ),
    ];
    for (name, manifest_source, expect) in cases {
        let script_source = "{{ services.repository.search('product') }}\n";
        let script = Arc::new(TextDocument::new(
            "file:///project/Resources/scripts/hook/script.twig",
            script_source,
            1,
        ));
        let start = script_source.find("product").unwrap() as u32;
        let range = TextRange::new(TextSize::from(start), TextSize::from(start + 7));
        let problem = Problem {
            id: "app_script.permission-missing".into(),
            range,
            element: script.root().unwrap().descendant_for_range(range),
            ..Problem::default()
        };
        let dir = tempfile::tempdir().unwrap();
        let manifest_path = dir
            .path()
            .join("manifest.xml")
            .to_string_lossy()
            .into_owned();
        let manifest_uri = file_uri(&manifest_path);
        let manifest = Arc::new(TextDocument::new(manifest_uri.clone(), manifest_source, 3));
        let bound = bind_fix(
            ADD_APP_READ_PERMISSION_FIX_ID,
            AppPermissionPayload {
                entity: "product".to_owned(),
                manifest: manifest_path,
            },
        );
        let resolver = StaticDocumentResolver(HashMap::from([(
            manifest_uri.clone(),
            DocumentSnapshot {
                document: manifest.clone(),
                version: Some(3),
            },
        )]));
        let plan = AppReadPermissionFix
            .build(
                &Context::background(),
                &fix_context(&script, &problem, &bound, Some(Arc::new(resolver))),
            )
            .unwrap();
        assert_eq!(plan.documents.len(), 1, "{name}");
        let updated = plan.documents[0].apply().unwrap();
        assert!(updated.contains(expect), "{name}: {updated}");
        assert!(
            TextDocument::new(manifest_uri, &updated, 4)
                .parse_errors()
                .is_empty(),
            "{name}"
        );
    }
}
