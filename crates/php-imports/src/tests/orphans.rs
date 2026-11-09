use swlsp_rewrite::{Builder, Edit};

use crate::remove_orphaned_imports;

/// Applies `edit` (from `old` to `new` at the first occurrence after
/// `anchor`) plus optional inserted imports, then the orphan removal.
fn rewrite(source: &str, edits: impl FnOnce(&mut Builder)) -> (Vec<Edit>, String) {
    let mut builder = Builder::new(source);
    edits(&mut builder);
    let edits = remove_orphaned_imports(source, builder.finish().unwrap()).unwrap();
    let result = swlsp_rewrite::apply(source, &edits).unwrap();
    (edits, result)
}

fn offset(source: &str, needle: &str) -> u32 {
    source.find(needle).expect(needle) as u32
}

fn replace(builder: &mut Builder, source: &str, needle: &str, text: &str) {
    let start = offset(source, needle);
    let range = swlsp_syntax::TextRange::new(start.into(), (start + needle.len() as u32).into());
    builder.replace_range(range, text).unwrap();
}

const SOURCE: &str = "<?php\nnamespace App;\n\nuse Vendor\\Old;\nuse Vendor\\Other;\nuse Vendor\\Unused;\n\nfunction run(Old $old, Other $other): void {}\n";

#[test]
fn removes_imports_a_rewrite_orphans_and_keeps_the_rest() {
    let (_, result) = rewrite(SOURCE, |builder| {
        replace(builder, SOURCE, "Old $old", "New $old");
        builder
            .insert(
                offset(SOURCE, "use Vendor\\Unused;\n") + 19,
                "use Vendor\\New;\n",
            )
            .unwrap();
    });
    assert_eq!(
        result,
        "<?php\nnamespace App;\n\nuse Vendor\\Other;\nuse Vendor\\Unused;\nuse Vendor\\New;\n\nfunction run(New $old, Other $other): void {}\n"
    );
}

#[test]
fn an_import_inserted_next_to_the_orphan_survives() {
    let (_, result) = rewrite(SOURCE, |builder| {
        replace(builder, SOURCE, "Old $old", "New $old");
        builder
            .insert(offset(SOURCE, "use Vendor\\Other;"), "use Vendor\\New;\n")
            .unwrap();
    });
    assert_eq!(
        result,
        "<?php\nnamespace App;\n\nuse Vendor\\New;\nuse Vendor\\Other;\nuse Vendor\\Unused;\n\nfunction run(New $old, Other $other): void {}\n"
    );
}

#[test]
fn leaves_rewrites_without_orphans_unchanged() {
    let mut builder = Builder::new(SOURCE);
    replace(&mut builder, SOURCE, "$other", "$renamed");
    let edits = builder.finish().unwrap();
    assert_eq!(
        remove_orphaned_imports(SOURCE, edits.clone()).unwrap(),
        edits
    );
}

#[test]
fn grouped_and_aliased_imports_lose_only_the_orphaned_clause() {
    let source = "<?php\nuse Vendor\\{Old as Legacy, Other};\nnew Legacy(); new Other();\n";
    let (_, result) = rewrite(source, |builder| {
        replace(builder, source, "new Legacy()", "new \\Vendor\\Fresh()");
    });
    assert_eq!(
        result,
        "<?php\nuse Vendor\\{Other};\nnew \\Vendor\\Fresh(); new Other();\n"
    );
}

#[test]
fn removals_inside_rewritten_text_fall_back_to_one_edit() {
    let (edits, result) = rewrite(SOURCE, |builder| {
        // The rewrite itself re-emits the import block.
        replace(
            builder,
            SOURCE,
            "use Vendor\\Old;\nuse Vendor\\Other;",
            "use Vendor\\Old;\nuse Vendor\\Other;\nuse Vendor\\New;",
        );
        replace(builder, SOURCE, "Old $old", "New $old");
    });
    assert_eq!(edits.len(), 1);
    assert_eq!(
        result,
        "<?php\nnamespace App;\n\nuse Vendor\\Other;\nuse Vendor\\New;\nuse Vendor\\Unused;\n\nfunction run(New $old, Other $other): void {}\n"
    );
}
