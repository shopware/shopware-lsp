use swlsp_shopware::ResolvedVersion;

use super::super::new_storefront_javascript;
use crate::inspections::REWRITE_NODE_FIX_ID;
use crate::inspections::test_support::{check_diagnostics, check_fix};

#[allow(clippy::field_reassign_with_default)]
fn version(minor: i64) -> ResolvedVersion {
    let mut version = ResolvedVersion::default();
    version.known = true;
    version.version.major = 6;
    version.version.minor = minor;
    version
}

const URI: &str = "file:///project/src/Resources/app/storefront/src/plugin/example.plugin.js";

const SOURCE: &str = r#"{|storefront.plugin-system.import:import Plugin from 'src/plugin-system/plugin.class';|}
{|storefront.plugin-system.import:import PluginManager from 'src/plugin-system/plugin.manager';|}
{|storefront.plugin-system.import:import { other } from 'src/plugin-system/plugin.manager';|}
{|storefront.dom-access.deprecated:import DomAccess from 'src/helper/dom-access.helper';|}
{|storefront.http-client.deprecated:import HttpClient from 'src/service/http-client.service';|}
{|storefront.query-string.deprecated:import queryString from 'query-string';|}
import Debouncer from 'src/helper/debouncer.helper';

export default class ExamplePlugin extends Plugin {
    init() {
        this.button = {|storefront.dom-access.deprecated:DomAccess.querySelector(this.el, '.btn')|};
        this.items = {|storefront.dom-access.deprecated:DomAccess.querySelectorAll(document, this.options.selector, false)|};
        this.url = {|storefront.dom-access.deprecated:DomAccess.getDataAttribute(this.el, 'data-ajax-url')|};
        this.other = {|storefront.dom-access.deprecated:DomAccess.getDataAttribute(this.el, name)|};
        this.label = {|storefront.dom-access.deprecated:DomAccess.getAttribute(a || b, 'title')|};
        if ({|storefront.dom-access.deprecated:DomAccess.isNode(this.el)|}) {}
        this.params = {|storefront.query-string.deprecated:queryString.parse(window.location.search)|};
        this.query = {|storefront.query-string.deprecated:queryString.stringify(this.params)|};
        this.custom = {|storefront.query-string.deprecated:queryString.stringify(this.params, { arrayFormat: 'bracket' })|};
        this.client = new HttpClient();
        other.querySelector(this.el, '.btn');
    }
}
"#;

#[test]
fn storefront_rules_follow_the_shopware_version() {
    let plain = SOURCE
        .lines()
        .map(|line| {
            let mut line = line.to_owned();
            while let Some(start) = line.find("{|") {
                let colon = start + line[start..].find(':').unwrap();
                line.replace_range(start..=colon, "");
                let end = line.find("|}").unwrap();
                line.replace_range(end..end + 2, "");
            }
            line
        })
        .collect::<Vec<_>>()
        .join("\n");
    // Unknown and old versions say nothing; 6.6 knows the plugin globals.
    for inspection in [
        new_storefront_javascript(ResolvedVersion::default()),
        new_storefront_javascript(version(5)),
    ] {
        check_diagnostics(inspection.as_ref(), URI, &plain);
    }
    let (_, problems) = check_diagnostics(new_storefront_javascript(version(6)).as_ref(), URI, &{
        let mut text = String::new();
        for (index, line) in plain.lines().enumerate() {
            if index < 3 {
                text.push_str(SOURCE.lines().nth(index).unwrap());
            } else {
                text.push_str(line);
            }
            text.push('\n');
        }
        text
    });
    assert_eq!(problems.len(), 3);
    check_diagnostics(new_storefront_javascript(version(7)).as_ref(), URI, SOURCE);
    // The Storefront bundle itself, and other code, is left alone.
    for uri in [
        "file:///project/vendor/shopware/storefront/Resources/app/storefront/src/plugin/a.plugin.js",
        "file:///project/src/Storefront/Resources/app/storefront/src/plugin/a.plugin.js",
    ] {
        check_diagnostics(new_storefront_javascript(version(7)).as_ref(), uri, &plain);
    }
    check_diagnostics(
        new_storefront_javascript(version(7)).as_ref(),
        "file:///project/src/Resources/app/administration/src/main.js",
        &plain,
    );
}

#[test]
fn storefront_fixes_rewrite_the_reported_code() {
    let inspection = new_storefront_javascript(version(7));
    for (before, after, equivalent) in [
        (
            "{|storefront.plugin-system.import:import Plugin from 'src/plugin-system/plugin.class';|}\n",
            "const Plugin = window.PluginBaseClass;\n",
            true,
        ),
        (
            "{|storefront.plugin-system.import:import PluginManager from 'src/plugin-system/plugin.manager';|}\n",
            "const PluginManager = window.PluginManager;\n",
            true,
        ),
        // The import goes once nothing uses it.
        (
            "import a from './a';\n{|storefront.dom-access.deprecated:import DomAccess from 'src/helper/dom-access.helper';|}\nimport b from './b';\n",
            "import a from './a';\nimport b from './b';\n",
            true,
        ),
        (
            "{|storefront.query-string.deprecated:import queryString from 'query-string';|}\n",
            "",
            true,
        ),
    ] {
        let presentation = check_fix(inspection.as_ref(), URI, before, REWRITE_NODE_FIX_ID, after);
        assert_eq!(presentation.preferred, equivalent, "{before}");
    }
    // Calls: the DOM API returns null where DomAccess throws, and
    // URLSearchParams treats repeated keys differently, so a person decides.
    let import = "{|storefront.dom-access.deprecated:import DomAccess from 'src/helper/dom-access.helper';|}\n";
    for (call, replacement) in [
        (
            "DomAccess.querySelector(this.el, '.btn')",
            "this.el.querySelector('.btn')",
        ),
        (
            "DomAccess.querySelectorAll(document, this.options.selector, false)",
            "document.querySelectorAll(this.options.selector)",
        ),
        (
            "DomAccess.hasAttribute(el, 'disabled')",
            "el.hasAttribute('disabled')",
        ),
        (
            "DomAccess.getAttribute(a || b, 'title')",
            "(a || b).getAttribute('title')",
        ),
        (
            "DomAccess.getDataAttribute(this.el, 'data-ajax-url')",
            "this.el.dataset.ajaxUrl",
        ),
    ] {
        let before =
            format!("{import}const value = {{|storefront.dom-access.deprecated:{call}|}};\n");
        let (_, problems) = check_diagnostics(inspection.as_ref(), URI, &before);
        // The import still has a user: only the call offers a fix.
        assert_eq!(
            problems
                .iter()
                .map(|problem| problem.fixes.len())
                .collect::<Vec<_>>(),
            [0, 1]
        );
        let after = format!(
            "import DomAccess from 'src/helper/dom-access.helper';\nconst value = {replacement};\n"
        );
        let presentation = check_fix(
            inspection.as_ref(),
            URI,
            &before,
            REWRITE_NODE_FIX_ID,
            &after,
        );
        assert!(!presentation.preferred, "{call}");
    }
    let import = "{|storefront.query-string.deprecated:import queryString from 'query-string';|}\n";
    for (call, replacement) in [
        (
            "queryString.parse(window.location.search)",
            "Object.fromEntries(new URLSearchParams(window.location.search).entries())",
        ),
        (
            "queryString.stringify(params)",
            "new URLSearchParams(params).toString()",
        ),
    ] {
        let before =
            format!("{import}const value = {{|storefront.query-string.deprecated:{call}|}};\n");
        let after =
            format!("import queryString from 'query-string';\nconst value = {replacement};\n");
        let presentation = check_fix(
            inspection.as_ref(),
            URI,
            &before,
            REWRITE_NODE_FIX_ID,
            &after,
        );
        assert!(!presentation.preferred, "{call}");
    }
    // What has no counterpart is reported without a fix.
    for source in [
        "{|storefront.dom-access.deprecated:import DomAccess from 'src/helper/dom-access.helper';|}\n{|storefront.dom-access.deprecated:DomAccess.isNode(el)|};\n{|storefront.dom-access.deprecated:DomAccess.getDataAttribute(el, name)|};\n",
        "{|storefront.query-string.deprecated:import queryString from 'query-string';|}\n{|storefront.query-string.deprecated:queryString.stringify(params, { arrayFormat: 'bracket' })|};\n",
        "{|storefront.plugin-system.import:import { PluginManager } from 'src/plugin-system/plugin.manager';|}\n",
        "{|storefront.http-client.deprecated:import HttpClient from 'src/service/http-client.service';|}\n",
    ] {
        let (_, problems) = check_diagnostics(inspection.as_ref(), URI, source);
        assert!(
            problems.iter().all(|problem| problem.fixes.is_empty()),
            "{source}"
        );
    }
}
