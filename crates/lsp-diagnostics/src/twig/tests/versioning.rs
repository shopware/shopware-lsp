use std::sync::Arc;

use swlsp_twig::{TwigIndexer, VersioningService};

use super::*;
use crate::twig::TwigVersioningAnalyzer;

fn join(root: &Path, path: &str) -> String {
    root.join(path).to_string_lossy().into_owned()
}

fn analyzer(root: &str, index: &Arc<TwigIndexer>, version: &str) -> TwigVersioningAnalyzer {
    TwigVersioningAnalyzer::new(Some(Arc::new(VersioningService::new(
        root,
        Some(index.clone()),
        version,
    ))))
}

const CORE_BASE: &str = "src/Storefront/Resources/views/storefront/page/base.html.twig";
const CORE_EXAMPLE: &str = "src/Storefront/Resources/views/storefront/page/example.html.twig";
const PLUGIN_EXAMPLE: &str =
    "custom/plugins/Example/src/Resources/views/storefront/page/example.html.twig";

#[test]
fn twig_versioning_analyzer_original_not_found_message() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    check_problems(
        &analyzer("/tmp", &index, ""),
        "file:///tmp/myext/Resources/views/storefront/page/checkout/foo.html.twig",
        "{% sw_extends '@Storefront/storefront/page/checkout/foo' %}{# shopware-block: abc123def456@6.4.15.0 #}{% block content %}test{% endblock %}",
    );
}

#[test]
fn twig_versioning_analyzer_nil_indexer_no_panic() {
    check_problems(
        &TwigVersioningAnalyzer::new(None),
        "file:///tmp/ext/Resources/views/storefront/page/bar.html.twig",
        "{% block foo %}{% endblock %}",
    );
}

#[test]
fn twig_versioning_analyzer_reports_deprecated_upstream_block() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    index_file(
        index.as_ref(),
        "/project/src/Storefront/Resources/views/storefront/page/example.html.twig",
        "{# @deprecated tag:v6.7.0 - use page_new #}\n{% block page_old %}old{% endblock %}",
    );
    let path =
        "/project/custom/plugins/Example/src/Resources/views/storefront/page/example.html.twig";
    let source = "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block {|twig.block.deprecated:{|twig.versioning.comment_missing:page_old|}|} %}custom{% endblock %}";
    let (_, problems) = check_problems(
        &analyzer("/project", &index, ""),
        &format!("file://{path}"),
        source,
    );
    for problem in problems
        .iter()
        .filter(|problem| problem.id.as_str() == "twig.block.deprecated")
    {
        assert!(problem.message.contains("tag:v6.7.0"));
        assert!(problem.message.contains("page_new"));
    }
}

#[test]
fn twig_versioning_analyzer_accepts_any_chain_candidate_and_reports_changes() {
    let root = tempdir();
    let index = twig_index(&root.path().join("cache"));
    let core = join(root.path(), CORE_BASE);
    let theme = join(
        root.path(),
        "custom/plugins/Theme/src/Resources/views/storefront/theme/base.html.twig",
    );
    index_file(
        index.as_ref(),
        &core,
        "{% block content %}core{% endblock %}",
    );
    index_file(
        index.as_ref(),
        &theme,
        "{% sw_extends '@Storefront/storefront/page/base.html.twig' %}\n{% block content %}theme{% endblock %}",
    );
    let core_hash = index
        .get_twig_block_hashes("content")
        .unwrap()
        .into_iter()
        .find(|hash| hash.absolute_path == core)
        .map(|hash| hash.hash)
        .expect("core hash");
    let plugin = join(
        root.path(),
        "custom/plugins/Plugin/src/Resources/views/storefront/custom/page.html.twig",
    );
    let analyzer = analyzer(&root.path().to_string_lossy(), &index, "6.7.2");
    let matching = format!(
        "{{% sw_extends '@Theme/storefront/theme/base.html.twig' %}}\n{{# shopware-block: {core_hash}@6.7.2.0 #}}\n{{% block content %}}local{{% endblock %}}"
    );
    check_problems(&analyzer, &format!("file://{plugin}"), &matching);

    let outdated = "{% sw_extends '@Theme/storefront/theme/base.html.twig' %}\n{|twig.versioning.outdated:{# shopware-block: deadbeef@6.6.0.0 #}|}\n{% block content %}local{% endblock %}";
    check_problems_v(&analyzer, &format!("file://{plugin}"), outdated, 2);
}

#[test]
fn twig_versioning_analyzer_reports_removal_only_for_resolvable_parent() {
    let root = tempdir();
    let index = twig_index(&root.path().join("cache"));
    index_file(
        index.as_ref(),
        &join(root.path(), CORE_BASE),
        "{% block other %}core{% endblock %}",
    );
    let analyzer = analyzer(&root.path().to_string_lossy(), &index, "");
    let plugin = join(root.path(), "custom/plugin.html.twig");
    let tracked = "{% sw_extends '@Storefront/storefront/page/base.html.twig' %}\n{# shopware-block: deadbeef@6.6.0.0 #}\n{% block {|twig.versioning.original_missing:removed|} %}local{% endblock %}";
    check_problems(&analyzer, &format!("file://{plugin}"), tracked);

    let standalone = plain(tracked).replacen(
        "@Storefront/storefront/page/base.html.twig",
        "@Missing/storefront/page/base.html.twig",
        1,
    );
    check_problems_v(&analyzer, &format!("file://{plugin}"), &standalone, 2);
}

#[test]
fn twig_versioning_analyzer_treats_malformed_comment_as_missing() {
    let root = tempdir();
    let index = twig_index(&root.path().join("cache"));
    index_file(
        index.as_ref(),
        &join(root.path(), CORE_BASE),
        "{% block content %}core{% endblock %}",
    );
    let plugin = join(root.path(), "custom/plugin.html.twig");
    let source = "{% sw_extends '@Storefront/storefront/page/base.html.twig' %}\n{|twig.versioning.comment_missing:{# shopware-block: invalid-value@6.6.0.0 #}|}\n{% block content %}local{% endblock %}";
    check_problems(
        &analyzer(&root.path().to_string_lossy(), &index, ""),
        &format!("file://{plugin}"),
        source,
    );
}

#[test]
fn twig_versioning_analyzer_reports_exact_resolved_parent_copy() {
    let root = tempdir();
    let index = twig_index(&root.path().join("cache"));
    let upstream = join(root.path(), CORE_EXAMPLE);
    let block = "{% block content %}\n    <div>same</div>\n{% endblock %}";
    index_file(index.as_ref(), &upstream, block);
    let plugin = join(root.path(), PLUGIN_EXAMPLE);
    let source = format!(
        "{{% sw_extends '@Storefront/storefront/page/example.html.twig' %}}\n{}",
        block.replacen("content", "{|twig.block.redundant_override:content|}", 1)
    );
    let (_, problems) = check_problems(
        &analyzer(&root.path().to_string_lossy(), &index, ""),
        &swlsp_uriutil::file_uri(&plugin),
        &source,
    );
    assert!(problems[0].message.contains("parent()"));
    assert_eq!(problems[0].related_information.len(), 1);
    assert_eq!(
        problems[0].related_information[0].location.uri,
        swlsp_uriutil::file_uri(&upstream)
    );
}

#[test]
fn twig_versioning_analyzer_does_not_report_different_or_fallback_block() {
    let root = tempdir();
    let index = twig_index(&root.path().join("cache"));
    index_file(
        index.as_ref(),
        &join(root.path(), CORE_EXAMPLE),
        "{% block other %}parent{% endblock %}",
    );
    let block = "{% block content %}same{% endblock %}";
    index_file(
        index.as_ref(),
        &join(
            root.path(),
            "custom/plugins/Other/src/Resources/views/storefront/page/example.html.twig",
        ),
        block,
    );
    let plugin = join(root.path(), PLUGIN_EXAMPLE);
    let analyzer = analyzer(&root.path().to_string_lossy(), &index, "");
    for source in [
        format!(
            "{{% sw_extends '@Storefront/storefront/page/example.html.twig' %}}\n{}",
            block.replacen("content", "{|twig.versioning.comment_missing:content|}", 1)
        ),
        "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block {|twig.versioning.comment_missing:other|} %}different{% endblock %}".to_owned(),
    ] {
        check_problems(&analyzer, &swlsp_uriutil::file_uri(&plugin), &source);
    }
}

#[test]
fn twig_versioning_analyzer_ignores_parent_delegation() {
    let root = tempdir();
    let index = twig_index(&root.path().join("cache"));
    index_file(
        index.as_ref(),
        &join(root.path(), CORE_EXAMPLE),
        "{% block content %}parent{% endblock %}",
    );
    let plugin = join(root.path(), "custom/plugin.html.twig");
    let source = "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block content %}\n    {{ parent() }}\n{% endblock %}";
    check_problems(
        &analyzer(&root.path().to_string_lossy(), &index, ""),
        &swlsp_uriutil::file_uri(&plugin),
        source,
    );
}
