//! Port of Go `migration_test.go` and `real_world_integration_test.go`.

use std::collections::HashSet;

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{HTML_STARTING_TAG, HTML_TAG};

use swlsp_syntax::SyntaxNode;

use super::*;

fn compile_source(source: &str, rule: &Rule) -> String {
    let parse = swlsp_parser_twig::parse(source);
    assert!(parse.errors.is_empty(), "{:?}", parse.errors);
    for tag in twigquery::nodes(&parse.root(), &[HTML_TAG]) {
        let starts = twigquery::nodes(&tag, &[HTML_STARTING_TAG]);
        if starts
            .first()
            .and_then(twigquery::html_tag_name)
            .is_some_and(|name| name == rule.source_tag)
        {
            let edits = compile(source, &tag, rule).expect("compile");
            return swlsp_rewrite::apply(source, &edits).expect("apply");
        }
    }
    panic!("source tag not found: {}", rule.source_tag);
}

#[test]
fn compile_administration_twig_migrations() {
    let tests = [
        (
            "alert variant",
            "alert",
            r#"<sw-alert variant="error">Failure</sw-alert>"#,
            r#"<mt-banner variant="critical">Failure</mt-banner>"#,
        ),
        (
            "button route and variant",
            "button",
            r#"<sw-button variant="ghost-danger" :router-link="detailRoute">Open</sw-button>"#,
            r#"<mt-button variant="critical" @click="$router.push(detailRoute)" ghost>Open</mt-button>"#,
        ),
        (
            "card",
            "card",
            r#"<sw-card>Content</sw-card>"#,
            r#"<mt-card>Content</mt-card>"#,
        ),
        (
            "checkbox",
            "checkbox-field",
            r#"<sw-checkbox-field v-model:value="enabled"><template #label>Enabled</template></sw-checkbox-field>"#,
            r#"<mt-checkbox v-model:checked="enabled" label="Enabled"></mt-checkbox>"#,
        ),
        (
            "colorpicker",
            "colorpicker",
            r#"<sw-colorpicker :value="color"><template #label>{{ $t('color') }}</template></sw-colorpicker>"#,
            r#"<mt-colorpicker :model-value="color" :label="$t('color')"></mt-colorpicker>"#,
        ),
        (
            "datepicker",
            "datepicker",
            r#"<sw-datepicker v-model:value="date" />"#,
            r#"<mt-datepicker v-model="date" />"#,
        ),
        (
            "email",
            "email-field",
            r#"<sw-email-field value="a@example.com" size="medium" />"#,
            r#"<mt-email-field model-value="a@example.com" size="default" />"#,
        ),
        (
            "icon",
            "icon",
            r#"<sw-icon name="regular-times-s" small />"#,
            r#"<mt-icon name="regular-times-s" size="16px" />"#,
        ),
        ("loader", "loader", r#"<sw-loader />"#, r#"<mt-loader />"#),
        (
            "number",
            "number-field",
            r#"<sw-number-field :value="amount" @update:value="changed" />"#,
            r#"<mt-number-field :model-value="amount" @update:model-value="changed" />"#,
        ),
        (
            "password slots",
            "password-field",
            r#"<sw-password-field><template #label>Password</template><template #hint>{{ hint }}</template></sw-password-field>"#,
            r#"<mt-password-field label="Password" :hint="hint"></mt-password-field>"#,
        ),
        (
            "progress",
            "progress-bar",
            r#"<sw-progress-bar value="5" />"#,
            r#"<mt-progress-bar model-value="5" />"#,
        ),
        (
            "select prop",
            "select-field",
            r#"<sw-select-field :value="selected" :options="options" />"#,
            r#"<mt-select :model-value="selected" :options="options" />"#,
        ),
        (
            "select option children",
            "select-field",
            r#"<sw-select-field><option value="one">One</option><option :value="other">{{ otherLabel }}</option></sw-select-field>"#,
            r#"<mt-select :options="[{ label: 'One', value: 'one' }, { label: otherLabel, value: other }]"></mt-select>"#,
        ),
        (
            "skeleton",
            "skeleton-bar",
            r#"<sw-skeleton-bar />"#,
            r#"<mt-skeleton-bar />"#,
        ),
        (
            "switch uses Meteor model API",
            "switch-field",
            r#"<sw-switch-field value="true" @update:value="changed" />"#,
            r#"<mt-switch model-value="true" @update:model-value="changed" />"#,
        ),
        (
            "text uses one-way model value",
            "text-field",
            r#"<sw-text-field :value="computedName" :required="true" />"#,
            r#"<mt-text-field :model-value="computedName" required />"#,
        ),
        (
            "textarea event",
            "textarea-field",
            r#"<sw-textarea-field v-model:value="description" @update:value="changed" />"#,
            r#"<mt-textarea v-model="description" @update:model-value="changed" />"#,
        ),
        (
            "url",
            "url-field",
            r#"<sw-url-field :value="url"><template #label>URL</template></sw-url-field>"#,
            r#"<mt-url-field :model-value="url" label="URL"></mt-url-field>"#,
        ),
        (
            "popover preserves structural condition",
            "popover",
            r#"<sw-popover v-if="open" :resizeWidth="true">Content</sw-popover>"#,
            r#"<mt-floating-ui v-if="open" :match-reference-width="true" :is-opened="true">Content</mt-floating-ui>"#,
        ),
    ];
    let mut covered = HashSet::new();
    for (name, rule_id, before, expected) in tests {
        let rule = rule_by_id(rule_id).expect(name);
        let updated = compile_source(before, rule);
        assert_eq!(updated, expected, "{name}");
        assert!(
            swlsp_parser_twig::parse(&updated).errors.is_empty(),
            "{name}"
        );
        covered.insert(rule_id);
    }
    for rule in rules() {
        assert!(covered.contains(rule.id), "missing fixture for {}", rule.id);
    }
}

#[test]
fn compile_rejects_lossy_upstream_fixes() {
    let tests = [
        ("card", "<sw-card aiBadge>Content</sw-card>"),
        (
            "card",
            r#"<sw-card :contentPadding="hasPadding">Content</sw-card>"#,
        ),
        (
            "checkbox-field",
            "<sw-checkbox-field><template #hint>Do not lose me</template></sw-checkbox-field>",
        ),
        (
            "email-field",
            r#"<sw-email-field @base-field-mounted="mounted" />"#,
        ),
        ("select-field", r#"<sw-select-field :aside="aside" />"#),
        ("button", r#"<sw-button :variant="variant" />"#),
        ("popover", r#"<sw-popover :zIndex="level" />"#),
    ];
    for (rule_id, source) in tests {
        let rule = rule_by_id(rule_id).expect(rule_id);
        let parse = swlsp_parser_twig::parse(source);
        assert!(parse.errors.is_empty());
        let tags = twigquery::nodes(&parse.root(), &[HTML_TAG]);
        assert!(!tags.is_empty());
        let error = compile(source, &tags[0], rule).expect_err(source);
        assert!(error.is_unsafe(), "{source}: {error}");
    }
}

#[test]
fn rules_exclude_unavailable_external_link_migration() {
    assert!(rule_by_id("external-link").is_none());
    assert!(rule_for_tag("sw-external-link").is_none());
}

#[test]
fn err_unsafe_is_stable() {
    let error = unsafe_("reason");
    assert!(error.is_unsafe());
    assert_eq!(
        error.to_string(),
        "administration Twig migration is not safely automatable: reason"
    );
}

fn real_world_root() -> std::path::PathBuf {
    std::env::var_os("SHOPWARE_LSP_REAL_WORLD_ROOT")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|| {
            std::path::PathBuf::from(std::env::var_os("HOME").unwrap_or_default())
                .join("Developer/sw-trunk")
        })
}

fn administration_templates(root: &std::path::Path) -> Vec<std::path::PathBuf> {
    fn walk(directory: &std::path::Path, result: &mut Vec<std::path::PathBuf>) {
        let Ok(entries) = std::fs::read_dir(directory) else {
            return;
        };
        let mut entries: Vec<_> = entries.flatten().collect();
        entries.sort_by_key(|entry| entry.file_name());
        for entry in entries {
            let path = entry.path();
            let Ok(kind) = entry.file_type() else {
                continue;
            };
            if kind.is_dir() {
                if matches!(
                    entry.file_name().to_str(),
                    Some(".git" | "node_modules" | "vendor")
                ) {
                    continue;
                }
                walk(&path, result);
            } else if path.extension().is_some_and(|ext| ext == "twig")
                && path
                    .to_string_lossy()
                    .contains("/Resources/app/administration/")
            {
                result.push(path);
            }
        }
    }
    let mut result = Vec::new();
    walk(root, &mut result);
    result
}

fn count_tag(root: &SyntaxNode, name: &str) -> usize {
    twigquery::nodes(root, &[HTML_STARTING_TAG])
        .iter()
        .filter(|start| twigquery::html_tag_name(start).is_some_and(|tag| tag == name))
        .count()
}

/// Port of Go `TestAdministrationTwigMigrationsOnTrunk`: dry-runs every
/// currently applicable fix against a real checkout; never writes to it.
#[test]
#[ignore = "needs a real-world Shopware checkout"]
fn administration_twig_migrations_on_trunk() {
    let root = real_world_root();
    if !root.is_dir() {
        eprintln!(
            "real-world Shopware checkout is unavailable: {}",
            root.display()
        );
        return;
    }
    // (reported, fixed, manual) per rule id.
    let mut counts: std::collections::HashMap<&str, (usize, usize, usize)> =
        std::collections::HashMap::new();
    for path in administration_templates(&root) {
        let Ok(content) = std::fs::read(&path) else {
            continue;
        };
        let source = String::from_utf8_lossy(&content).into_owned();
        let parsed = swlsp_parser_twig::parse(&source);
        let parsed_root = parsed.root();
        for node in twigquery::nodes(&parsed_root, &[HTML_TAG]) {
            let Some(name) = twigquery::nodes(&node, &[HTML_STARTING_TAG])
                .first()
                .and_then(twigquery::html_tag_name)
            else {
                continue;
            };
            let Some(rule) = rule_for_tag(&name) else {
                continue;
            };
            let total = counts.entry(rule.id).or_default();
            total.0 += 1;
            let edits = match compile(&source, &node, rule) {
                Ok(edits) => edits,
                Err(error) if error.is_unsafe() => {
                    total.2 += 1;
                    continue;
                }
                Err(error) => panic!("{} in {}: {error}", rule.id, path.display()),
            };
            let updated = swlsp_rewrite::apply(&source, &edits)
                .unwrap_or_else(|error| panic!("{} in {}: {error}", rule.id, path.display()));
            let updated_parse = swlsp_parser_twig::parse(&updated);
            assert!(
                updated_parse.errors.len() <= parsed.errors.len(),
                "{} introduced a Twig parse error in {}",
                rule.id,
                path.display()
            );
            assert!(
                count_tag(&updated_parse.root(), rule.source_tag)
                    < count_tag(&parsed_root, rule.source_tag),
                "{} did not remove its source tag in {}",
                rule.id,
                path.display()
            );
            total.1 += 1;
        }
    }
    let mut all = (0, 0, 0);
    for rule in rules() {
        let total = counts.get(rule.id).copied().unwrap_or_default();
        all.0 += total.0;
        all.1 += total.1;
        all.2 += total.2;
        if total.0 != 0 {
            eprintln!(
                "{}: reported={} fixed={} manual={}",
                rule.id, total.0, total.1, total.2
            );
        }
    }
    assert!(
        all.0 > 0,
        "trunk should contain at least one legacy Administration component"
    );
    assert_eq!(all.0, all.1 + all.2);
    eprintln!("total: reported={} fixed={} manual={}", all.0, all.1, all.2);
}
