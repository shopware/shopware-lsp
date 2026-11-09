//! Initialization and workspace construction tests.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};

use serde_json::{Value, json};
use swlsp_indexer::FileScanner;
use swlsp_lsp_protocol as protocol;

use super::support::*;
use crate::server::{read, write};
use crate::workspace::workspace_root;
use crate::*;

fn initialize(server: &Server, params: &protocol::InitializeParams) -> Result<Value, String> {
    server
        .initialize(&Context::background(), params)
        .map_err(|error| error.to_string())
}

#[test]
fn workspace_root_uses_initialize_uri() {
    let dir = temp_dir();
    let root = dir.path().join("workspace with spaces");
    let resolved = workspace_root(&initialize_params(&root)).unwrap();
    assert_eq!(resolved, root.to_string_lossy());
}

#[test]
fn workspace_root_rejects_missing_and_multiple_roots() {
    let error = workspace_root(&protocol::InitializeParams::default())
        .unwrap_err()
        .to_string();
    assert!(error.contains("workspace root is required"), "{error}");
    let params = protocol::InitializeParams {
        workspace_folders: vec![
            protocol::WorkspaceFolder {
                uri: "file:///first".into(),
                ..Default::default()
            },
            protocol::WorkspaceFolder {
                uri: "file:///second".into(),
                ..Default::default()
            },
        ],
        ..Default::default()
    };
    let error = workspace_root(&params).unwrap_err().to_string();
    assert!(error.contains("multiple workspace folders"), "{error}");
}

#[test]
fn editor_initialize_is_inactive_for_unsupported_project() {
    let dir = temp_dir();
    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    let created = Arc::new(AtomicBool::new(false));
    let flag = created.clone();
    server.set_workspace_factory(Arc::new(move |_, _, _| {
        flag.store(true, Ordering::SeqCst);
        Err("must not create workspace".into())
    }));
    let result = initialize(&server, &initialize_params(dir.path())).unwrap();
    {
        let session = read(&server.session);
        assert!(session.initialized);
        assert!(session.inactive_project);
    }
    assert!(!created.load(Ordering::SeqCst));
    let capabilities = result["capabilities"].as_object().unwrap();
    assert_eq!(capabilities.len(), 1);
    let state = &capabilities["experimental"]["shopwareLSP"];
    assert_eq!(state["active"], false);
    assert_eq!(state["reason"], "unsupportedProject");
    server.close_all().unwrap();
}

#[test]
fn cli_initialize_rejects_unsupported_project_when_detection_is_required() {
    let dir = temp_dir();
    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    let mut params = initialize_params(dir.path());
    params.initialization_options.cli_mode = true;
    let error = initialize(&server, &params).unwrap_err();
    assert!(error.contains("unsupported project root"), "{error}");
    assert!(!read(&server.session).initialized);
}

#[test]
fn initialize_accepts_configured_or_explicitly_allowed_project() {
    let configured = temp_dir();
    write_project_configuration(configured.path(), r#"{"version":1}"#);
    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    initialize(&server, &initialize_params(configured.path())).unwrap();
    server.close_all().unwrap();

    let unsupported = temp_dir();
    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    let mut params = initialize_params(unsupported.path());
    params.initialization_options.allow_unsupported_project = true;
    initialize(&server, &params).unwrap();
    assert!(!read(&server.session).inactive_project);
    server.close_all().unwrap();
}

#[test]
fn initialize_propagates_workspace_construction_failure() {
    let server = Server::new(None, "", "test");
    server.set_workspace_factory(Arc::new(|_, _, _| Err("open repository".into())));
    let params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    let error = initialize(&server, &params).unwrap_err();
    assert!(error.contains("open repository"), "{error}");
    server.close_all().unwrap();
}

#[test]
fn initialize_rejects_second_initialization() {
    let server = Server::new(None, "", "test");
    let mut params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    initialize(&server, &params).unwrap();
    params.root_uri = "file:///other".into();
    let error = initialize(&server, &params).unwrap_err();
    assert!(error.contains("already initialized"), "{error}");
    server.close_all().unwrap();
}

#[test]
fn initialize_publishes_php_stub_extension_options_to_workspace() {
    let server = Server::new(None, "", "test");
    let received = Arc::new(std::sync::Mutex::new(None));
    let slot = received.clone();
    server.set_workspace_factory(Arc::new(move |_, _, current: &Server| {
        *slot.lock().unwrap() = Some(current.initialization_options());
        Err("stop after options".into())
    }));
    let mut params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    params.initialization_options.php_extensions = vec!["redis".into()];
    params.initialization_options.disabled_php_extensions = vec!["imagick".into()];
    params.initialization_options.shopware_target_version = "6.8.0".into();
    let error = initialize(&server, &params).unwrap_err();
    assert!(error.contains("stop after options"), "{error}");
    let options = received.lock().unwrap().clone().unwrap();
    assert_eq!(options.php_extensions, vec!["redis"]);
    assert_eq!(options.disabled_php_extensions, vec!["imagick"]);
    assert_eq!(options.shopware_target_version, "6.8.0");
}

struct Nothing;

macro_rules! empty_provider {
    ($trait:ident { $($body:tt)* }) => {
        impl $trait for Nothing { $($body)* }
    };
}

empty_provider!(FileRenameProvider {
    fn will_rename_files(&self, _: &Context, _: &FileRenameRequest) -> ProviderResult<Option<protocol::WorkspaceEdit>> { Ok(None) }
});
empty_provider!(WorkspaceSymbolProvider {
    fn workspace_symbols(&self, _: &Context, _: &str) -> ProviderResult<Vec<protocol::SymbolInformation>> { Ok(vec![]) }
});
empty_provider!(DocumentSymbolProvider {
    fn get_document_symbols(&self, _: &Context, _: &DocumentSymbolRequest) -> ProviderResult<Vec<protocol::DocumentSymbol>> { Ok(vec![]) }
});
empty_provider!(DocumentHighlightProvider {
    fn get_document_highlights(&self, _: &Context, _: &DocumentHighlightRequest) -> ProviderResult<Vec<protocol::DocumentHighlight>> { Ok(vec![]) }
});
empty_provider!(LinkedEditingRangeProvider {
    fn get_linked_editing_ranges(&self, _: &Context, _: &LinkedEditingRangeRequest) -> ProviderResult<Option<protocol::LinkedEditingRanges>> { Ok(None) }
});
empty_provider!(FoldingRangeProvider {
    fn get_folding_ranges(&self, _: &Context, _: &FoldingRangeRequest) -> ProviderResult<Vec<protocol::FoldingRange>> { Ok(vec![]) }
});
empty_provider!(SelectionRangeProvider {
    fn get_selection_ranges(&self, _: &Context, _: &SelectionRangeRequest) -> ProviderResult<Vec<protocol::SelectionRange>> { Ok(vec![]) }
});
empty_provider!(DocumentColorProvider {
    fn get_document_colors(&self, _: &Context, _: &DocumentColorRequest) -> ProviderResult<Vec<protocol::ColorInformation>> { Ok(vec![]) }
    fn get_color_presentations(&self, _: &Context, _: &ColorPresentationRequest) -> ProviderResult<Vec<protocol::ColorPresentation>> { Ok(vec![]) }
});
empty_provider!(InlayHintProvider {
    fn get_inlay_hints(&self, _: &Context, _: &InlayHintRequest) -> ProviderResult<Vec<protocol::InlayHint>> { Ok(vec![]) }
});
empty_provider!(ImplementationProvider {
    fn get_implementation(&self, _: &Context, _: &ImplementationRequest) -> Vec<protocol::Location> { vec![] }
});
empty_provider!(TypeHierarchyProvider {
    fn prepare_type_hierarchy(&self, _: &Context, _: &TypeHierarchyPrepareRequest) -> Vec<protocol::TypeHierarchyItem> { vec![] }
    fn supertypes(&self, _: &Context, _: &protocol::TypeHierarchyItem) -> Vec<protocol::TypeHierarchyItem> { vec![] }
    fn subtypes(&self, _: &Context, _: &protocol::TypeHierarchyItem) -> Vec<protocol::TypeHierarchyItem> { vec![] }
});
empty_provider!(CallHierarchyProvider {
    fn prepare_call_hierarchy(&self, _: &Context, _: &CallHierarchyPrepareRequest) -> ProviderResult<Vec<protocol::CallHierarchyItem>> { Ok(vec![]) }
    fn incoming_calls(&self, _: &Context, _: &CallHierarchyCallsRequest) -> ProviderResult<Vec<protocol::CallHierarchyIncomingCall>> { Ok(vec![]) }
    fn outgoing_calls(&self, _: &Context, _: &CallHierarchyCallsRequest) -> ProviderResult<Vec<protocol::CallHierarchyOutgoingCall>> { Ok(vec![]) }
});
empty_provider!(DocumentLinkProvider {
    fn get_document_links(&self, _: &Context, _: &DocumentLinkRequest) -> ProviderResult<Vec<protocol::DocumentLink>> { Ok(vec![]) }
});
empty_provider!(SemanticTokensProvider {
    fn get_semantic_tokens(&self, _: &Context, _: &SemanticTokensRequest) -> ProviderResult<Vec<SemanticToken>> { Ok(vec![]) }
});

#[test]
fn initialize_advertises_twig_file_rename_edits() {
    let server = Server::new(None, "", "test");
    let nothing = Arc::new(Nothing);
    server.register_file_rename_provider(nothing.clone());
    server.register_workspace_symbol_provider(nothing.clone());
    server.register_document_symbol_provider(nothing.clone());
    server.register_document_highlight_provider(nothing.clone());
    server.register_linked_editing_range_provider(nothing.clone());
    server.register_folding_range_provider(nothing.clone());
    server.register_selection_range_provider(nothing.clone());
    server.register_document_color_provider(nothing.clone());
    server.register_inlay_hint_provider(nothing.clone());
    server.register_implementation_provider(nothing.clone());
    server.register_type_hierarchy_provider(nothing.clone());
    server.register_call_hierarchy_provider(nothing.clone());
    server.register_document_link_provider(nothing.clone());
    server.register_semantic_tokens_provider(nothing);
    let params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    let result = initialize(&server, &params).unwrap();
    let capabilities = &result["capabilities"];
    let filter = &capabilities["workspace"]["fileOperations"]["willRename"]["filters"][0];
    assert_eq!(filter["pattern"]["glob"], "**/*.twig");
    assert_eq!(filter["pattern"]["matches"], "file");
    assert_eq!(filter["scheme"], "file");
    for name in [
        "workspaceSymbolProvider",
        "documentSymbolProvider",
        "documentHighlightProvider",
        "linkedEditingRangeProvider",
        "foldingRangeProvider",
        "selectionRangeProvider",
        "colorProvider",
        "inlayHintProvider",
        "implementationProvider",
        "typeHierarchyProvider",
        "callHierarchyProvider",
    ] {
        assert_eq!(capabilities[name], true, "{name}");
    }
    assert_eq!(
        capabilities["documentLinkProvider"]["resolveProvider"],
        false
    );
    let semantic = &capabilities["semanticTokensProvider"];
    assert_eq!(
        semantic["legend"]["tokenTypes"],
        json!(protocol::SEMANTIC_TOKEN_TYPES)
    );
    // An empty legend list must serialize as an array, never null.
    assert_eq!(semantic["legend"]["tokenModifiers"], json!([]));
    assert_eq!(semantic["full"], true);
    assert_eq!(semantic["range"], false);
    server.close_all().unwrap();
}

#[test]
fn initialize_omits_capabilities_without_providers() {
    let server = Server::new(None, "", "test");
    let params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    let result = initialize(&server, &params).unwrap();
    let capabilities = result["capabilities"].as_object().unwrap();
    assert!(capabilities.contains_key("textDocumentSync"));
    assert!(capabilities.contains_key("experimental"));
    assert!(!capabilities.contains_key("definitionProvider"));
    assert!(!capabilities.contains_key("diagnosticProvider"));
    assert!(!capabilities.contains_key("workspace"));
    assert_eq!(
        result["serverInfo"],
        json!({"name": "shopware-lsp", "version": "test"})
    );
    server.close_all().unwrap();
}

#[test]
fn initialize_negotiates_framework_presentation() {
    let server = Server::new(None, "", "test");
    register_command(&server, "shopware/test", |_, _| Ok(Value::Null));
    let mut params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    params.initialization_options.shopware_client = Some(protocol::ShopwareClientOptions {
        protocol_version: CLIENT_PROTOCOL_VERSION,
        presentation_profile: "framework".into(),
        supported_commands: vec![
            "shopware.openReferences".into(),
            "shopware.openReferences".into(),
            " ".into(),
        ],
    });
    let result = initialize(&server, &params).unwrap();
    assert!(server.framework_presentation());
    assert!(server.supports_client_command("shopware.openReferences"));
    assert!(!server.supports_client_command("shopware.unsupported"));
    let capabilities = &result["capabilities"];
    let state = &capabilities["experimental"]["shopwareLSP"];
    assert_eq!(state["protocolVersion"], CLIENT_PROTOCOL_VERSION);
    assert_eq!(state["presentationProfile"], "framework");
    assert_eq!(
        state["supportedCommands"],
        json!(["shopware.openReferences"])
    );
    assert_eq!(
        capabilities["executeCommandProvider"]["commands"],
        json!(["shopware/test"])
    );
    server.close_all().unwrap();
}

#[test]
fn initialize_can_omit_execute_command_provider() {
    let server = Server::new(None, "", "test");
    register_command(&server, "shopware/test", |_, _| Ok(json!("ok")));
    let mut params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    params.initialization_options.omit_execute_command_provider = true;
    let result = initialize(&server, &params).unwrap();
    assert!(
        result["capabilities"]
            .get("executeCommandProvider")
            .is_none()
    );
    let response = server
        .handle_execute_command(&Context::background(), json!({"command": "shopware/test"}))
        .unwrap();
    assert_eq!(response, json!("ok"));
    server.close_all().unwrap();
}

#[test]
fn file_watching_follows_client_kind_and_cli_opt_in() {
    for (name, options, watches) in [
        ("editor", "{}", true),
        (
            "editor without the key",
            r#"{"omitExecuteCommandProvider":true}"#,
            true,
        ),
        ("one-shot CLI command", r#"{"cliMode":true}"#, false),
        (
            "CLI command opting out",
            r#"{"cliMode":true,"watchFiles":false}"#,
            false,
        ),
        (
            "long-lived MCP session",
            r#"{"cliMode":true,"watchFiles":true}"#,
            true,
        ),
    ] {
        let params: protocol::InitializeParams =
            serde_json::from_str(&format!(r#"{{"initializationOptions":{options}}}"#)).unwrap();
        let server = Server::new(None, "", "test");
        write(&server.session).options = params.initialization_options;
        assert_eq!(server.file_watching_enabled(), watches, "{name}");
        server.close_all().unwrap();
    }
}

#[test]
fn initialize_rejects_unsupported_client_contract() {
    for (name, options) in [
        (
            "version",
            protocol::ShopwareClientOptions {
                protocol_version: CLIENT_PROTOCOL_VERSION + 1,
                ..Default::default()
            },
        ),
        (
            "profile",
            protocol::ShopwareClientOptions {
                protocol_version: CLIENT_PROTOCOL_VERSION,
                presentation_profile: "unknown".into(),
                ..Default::default()
            },
        ),
    ] {
        let server = Server::new(None, "", "test");
        let mut params = protocol::InitializeParams {
            root_uri: "file:///workspace".into(),
            ..Default::default()
        };
        params.initialization_options.shopware_client = Some(options);
        assert!(initialize(&server, &params).is_err(), "{name}");
        assert!(!read(&server.session).initialized, "{name}");
        server.close_all().unwrap();
    }
}

#[test]
fn initialize_negotiates_code_action_resolution() {
    let server = Server::new(None, "", "test");
    server.register_inspection(Arc::new(TestInspection));
    let mut params = protocol::InitializeParams {
        root_uri: "file:///workspace".into(),
        ..Default::default()
    };
    params.capabilities.text_document.code_action = Some(protocol::CodeActionClientCapabilities {
        data_support: true,
        resolve_support: protocol::CodeActionResolveSupport {
            properties: vec!["edit".into()],
        },
    });
    let result = initialize(&server, &params).unwrap();
    let actions = &result["capabilities"]["codeActionProvider"];
    assert_eq!(actions["resolveProvider"], true);
    assert!(
        actions["codeActionKinds"]
            .as_array()
            .unwrap()
            .contains(&json!("quickfix"))
    );
    server.close_all().unwrap();
}

struct TestWorkspaceRuntime {
    root: String,
    scanner: Arc<FileScanner>,
    close_calls: Arc<AtomicUsize>,
}

impl WorkspaceRuntime for TestWorkspaceRuntime {
    fn root(&self) -> &str {
        &self.root
    }

    fn scanner(&self) -> Option<Arc<FileScanner>> {
        Some(self.scanner.clone())
    }

    fn initial_force_reindex(&self) -> bool {
        false
    }

    fn close(&self) -> Result<(), BoxError> {
        self.close_calls.fetch_add(1, Ordering::SeqCst);
        Ok(self.scanner.close()?)
    }
}

#[test]
fn server_closes_workspace_exactly_once() {
    let dir = temp_dir();
    let root = root_string(&dir);
    let close_calls = Arc::new(AtomicUsize::new(0));
    let server = Server::new(None, "", "test");
    let calls = close_calls.clone();
    server.set_workspace_factory(Arc::new(move |_, root: &str, _: &Server| {
        let scanner = FileScanner::new(root, std::path::Path::new(root).join("scanner.db"), None)?;
        Ok(Box::new(TestWorkspaceRuntime {
            root: root.to_owned(),
            scanner: Arc::new(scanner),
            close_calls: calls.clone(),
        }))
    }));
    initialize(&server, &initialize_params(std::path::Path::new(&root))).unwrap();
    server.close_all().unwrap();
    server.close_all().unwrap();
    assert_eq!(close_calls.load(Ordering::SeqCst), 1);
}

#[test]
fn server_close_cancels_and_waits_for_background_work() {
    let server = Server::new(None, "", "test");
    let (started_sender, started) = crossbeam_channel::bounded(1);
    let (cancelled_sender, cancelled) = crossbeam_channel::bounded(1);
    let (release_sender, release) = crossbeam_channel::bounded::<()>(1);
    assert!(server.start_background(move |ctx| {
        started_sender.send(()).unwrap();
        ctx.token().wait();
        cancelled_sender.send(()).unwrap();
        let _ = release.recv();
    }));
    recv(&started, "background work did not start");
    let closing = server.clone();
    let (closed_sender, closed) = crossbeam_channel::bounded(1);
    std::thread::spawn(move || closed_sender.send(closing.close_all()).unwrap());
    recv(&cancelled, "background context was not canceled");
    assert!(
        closed.try_recv().is_err(),
        "close_all returned before background work stopped"
    );
    release_sender.send(()).unwrap();
    recv(&closed, "close_all did not return").unwrap();
    assert!(!server.start_background(|_| {}));
}
