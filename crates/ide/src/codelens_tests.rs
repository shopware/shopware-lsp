use super::*;

// -- protocol shape --

#[test]
fn related_lens_serializes_command_arguments() {
    let lens = related_lens(line_range(2), "Open", vec!["file:///a#1".to_owned()]);
    assert_eq!(
        serde_json::to_string(&lens).unwrap(),
        r#"{"range":{"start":{"line":2,"character":0},"end":{"line":2,"character":0}},"command":{"title":"Open","command":"shopware.openReferences","arguments":[["file:///a#1"]]}}"#
    );
    assert_eq!(related_target("", 3), "");
    assert!(related_target("/a", 0).ends_with("#1"));
}
