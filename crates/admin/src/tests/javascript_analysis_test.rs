#![allow(unused_imports)]
use super::support::*;
use crate::*;

#[test]
fn javascript_document_analysis_precomputes_lexical_bindings() {
    let source = "
const utils = Shopware.Utils;
const { chunk: chunkArray } = Shopware.Utils.array;
const services = Application.getContainer('service');
utils.format.date(value);
chunkArray(values, 2);
services.repositoryFactory;
";
    let root = parse_js(source);
    let analysis = JavaScriptDocumentAnalysis::new(&root);
    let declarations = analysis.nodes(&[jssyntax::JS_VARIABLE_DECLARATION]);
    assert_eq!(declarations.len(), 3);

    let binding = analysis
        .variable_binding(&declarations[1], "chunkArray")
        .expect("chunkArray binding");
    assert_eq!(binding.source_name, "chunk");
    assert_eq!(binding.initializer, "Shopware.Utils.array");
    let (name, initializer) = analysis
        .const_initializer(&declarations[2])
        .expect("const initializer");
    assert_eq!(name, "services");
    assert_eq!(initializer, "Application.getContainer('service')");

    let mut checked = 0;
    for member in analysis.nodes(&[jssyntax::JS_MEMBER_EXPRESSION]) {
        let text = member.text().to_string();
        let text = text.trim();
        if text.ends_with("utils.format.date") {
            let (receiver, name) = analysis
                .shopware_utils_member(&member)
                .expect("utils member");
            assert_eq!(receiver, ["format"]);
            assert_eq!(name, "date");
            checked += 1;
        } else if text.ends_with("services.repositoryFactory") {
            let (container, name) = analysis
                .application_container_member(&member)
                .expect("container member");
            assert_eq!(container, "service");
            assert_eq!(name, "repositoryFactory");
            checked += 1;
        }
    }
    assert_eq!(checked, 2);

    assert_eq!(analysis.calls(&[]), jsquery::calls(&root, &[]));
}
