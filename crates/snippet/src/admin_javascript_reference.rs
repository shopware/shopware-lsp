use swlsp_parser_js::query;
use swlsp_parser_js::syntax::{JS_OBJECT, JS_PROPERTY, JS_STRING};
use swlsp_syntax::SyntaxNode;

/// Whether `node` belongs to a static Administration snippet reference.
/// Besides translation calls, Shopware module manifests store snippet keys
/// directly in title, description, and navigation label properties.
pub fn admin_javascript_string_reference(node: &SyntaxNode) -> bool {
    let Some(string) = query::string_at(node) else {
        return false;
    };
    if !is_static_admin_javascript_string(&string) {
        return false;
    }
    if query::string_argument_index(node) == Some(0)
        && query::call_at(node).is_some_and(|call| is_admin_javascript_snippet_call(&call))
    {
        return true;
    }
    is_admin_module_snippet_property(&string)
}

/// Complete static keys from every supported Administration JavaScript
/// translation call and module manifest, in source order.
pub fn admin_javascript_string_references(root: &SyntaxNode) -> Vec<SyntaxNode> {
    let mut result = Vec::new();
    for call in query::iter_calls(root, &[]) {
        if !is_admin_javascript_snippet_call(&call) {
            continue;
        }
        if let Some(argument) = query::argument_expression(&call, 0)
            && argument.kind() == JS_STRING
            && is_static_admin_javascript_string(&argument)
        {
            result.push(argument);
        }
    }
    for string in root.descendants().filter(|node| node.kind() == JS_STRING) {
        if is_static_admin_javascript_string(&string) && is_admin_module_snippet_property(&string) {
            result.push(string);
        }
    }
    result.sort_by_key(|node| node.text_range().start());
    result
}

fn is_admin_module_snippet_property(string: &SyntaxNode) -> bool {
    let Some(property) = query::property_at(string) else {
        return false;
    };
    if query::property_value(&property).as_ref() != Some(string) {
        return false;
    }
    let Some(call) = query::call_at(string) else {
        return false;
    };
    if !matches!(
        query::call_name(&call).as_deref(),
        Some("Module.register" | "Shopware.Module.register")
    ) {
        return false;
    }
    let Some(config) =
        query::argument_expression(&call, 1).filter(|config| config.kind() == JS_OBJECT)
    else {
        return false;
    };
    let property_name = query::property_name(&property).unwrap_or_default();
    if property.parent().as_ref() == Some(&config) {
        return property_name == "title" || property_name == "description";
    }
    if property_name != "label" {
        return false;
    }
    property
        .ancestors()
        .skip(1)
        .take_while(|current| *current != config)
        .any(|current| {
            current.kind() == JS_PROPERTY
                && current.parent().as_ref() == Some(&config)
                && query::property_name(&current).as_deref() == Some("navigation")
        })
}

/// Template literals with an unescaped `${` are dynamic.
fn is_static_admin_javascript_string(node: &SyntaxNode) -> bool {
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() < 2 || text[0] != b'`' {
        return true;
    }
    for offset in 1..text.len() - 1 {
        if text[offset] != b'$' || text[offset + 1] != b'{' {
            continue;
        }
        let backslashes = text[..offset]
            .iter()
            .rev()
            .take_while(|byte| **byte == b'\\')
            .count();
        if backslashes % 2 == 0 {
            return false;
        }
    }
    true
}

fn is_admin_javascript_snippet_call(call: &SyntaxNode) -> bool {
    if matches!(query::call_method_name(call).as_deref(), Some("$t" | "$tc")) {
        return true;
    }
    matches!(
        query::call_name(call).as_deref(),
        Some("Shopware.Snippet.t" | "Shopware.Snippet.tc")
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn root(source: &str) -> SyntaxNode {
        swlsp_parser_js::parse(source).tree.root()
    }

    fn keys(references: &[SyntaxNode]) -> Vec<String> {
        references.iter().map(query::string_value).collect()
    }

    #[test]
    fn translation_call_references() {
        let source = concat!(
            "\nthis.$t('admin.this');\nthis.$root.$tc('admin.root');\ntranslator.$t('admin.injected');\n",
            "getApplicationRootReference().$t('admin.factory');\n$t('admin.bare');\nShopware.Snippet.t('admin.service');\n",
            "Shopware.Snippet.tc('admin.service-count');\nother.t('not.translation');\nthis.$t(dynamicKey);\n",
            "this.$t(`admin.${dynamic}`);"
        );
        assert_eq!(
            keys(&admin_javascript_string_references(&root(source))),
            [
                "admin.this",
                "admin.root",
                "admin.injected",
                "admin.factory",
                "admin.bare",
                "admin.service",
                "admin.service-count"
            ]
        );
    }

    #[test]
    fn single_reference() {
        let root = root("translator.$t('admin.title'); other.t('plain');");
        let arguments = admin_javascript_string_references(&root);
        assert_eq!(arguments.len(), 1);
        assert!(admin_javascript_string_reference(&arguments[0]));

        let calls = query::calls(&root, &["other.t"]);
        assert_eq!(calls.len(), 1);
        let plain = query::string_argument(&calls[0], 0).unwrap();
        assert!(!admin_javascript_string_reference(&plain));
    }

    #[test]
    fn module_snippet_references() {
        let source = r#"
Module.register('sw-demo', {
    title: 'sw-demo.general.title',
    description: 'sw-demo.general.description',
    routes: {
        index: {
            meta: { label: 'not-a-module-label' },
        },
    },
    navigation: [{
        label: 'sw-demo.general.navigation',
        path: 'sw.demo.index',
    }],
    settingsItem: {
        group: 'content',
    },
});
Shopware.Module.register('sw-second', {
    title: 'sw-second.general.title',
});
const unrelated = {
    title: 'ordinary.title',
    navigation: [{ label: 'ordinary.label' }],
};
"#;
        let references = admin_javascript_string_references(&root(source));
        for reference in &references {
            assert!(admin_javascript_string_reference(reference));
        }
        assert_eq!(
            keys(&references),
            [
                "sw-demo.general.title",
                "sw-demo.general.description",
                "sw-demo.general.navigation",
                "sw-second.general.title"
            ]
        );
    }

    #[test]
    fn escaped_template_placeholders_are_static() {
        let root = root(r"this.$t(`admin.\${x}`); this.$t(`admin.\\${x}`);");
        assert_eq!(admin_javascript_string_references(&root).len(), 1);
    }
}
