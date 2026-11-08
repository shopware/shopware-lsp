//! Ports of `request_dispatcher_test.go`, `progress_test.go` and
//! `execute_command_test.go`.

use std::sync::Arc;

use crossbeam_channel::{bounded, unbounded};
use serde_json::{Value, json};
use swlsp_indexer::FileScanner;

use super::support::*;
use crate::*;

#[test]
fn request_cancellation_preserves_document_ordering() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    let (entered_sender, entered) = bounded(1);
    register_command(&server, "test/block", move |ctx, _| {
        let _ = entered_sender.try_send(());
        ctx.token().wait();
        Err(Box::new(Cancelled))
    });
    let documents = server.clone();
    register_command(&server, "test/version", move |_, _| {
        Ok(json!(
            documents
                .documents
                .get_document("file:///test.php")
                .map_or(-1, |document| document.version)
        ))
    });
    let client = Arc::new(start_client(&server, |_, _| {}));
    let result = client.call_async(42, "test/block", Value::Null);
    recv(&entered, "blocking request did not start");
    client.notify(
        "textDocument/didOpen",
        json!({"textDocument": {"uri": "file:///test.php", "text": "<?php", "version": 7, "languageId": "php"}}),
    );
    client.notify("$/cancelRequest", json!({"id": 42}));
    let error = recv(&result, "cancelled request did not answer").unwrap_err();
    assert_eq!(error.0, codes::REQUEST_CANCELLED);
    assert_eq!(client.call("test/version", Value::Null).unwrap(), json!(7));
    client.shutdown().unwrap();
}

#[test]
fn disconnect_cancels_running_request_before_teardown() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    let (entered_sender, entered) = bounded(1);
    let (finished_sender, finished) = bounded(1);
    register_command(&server, "test/block", move |ctx, _| {
        let _ = entered_sender.try_send(());
        ctx.token().wait();
        let _ = finished_sender.try_send(());
        Err(Box::new(Cancelled))
    });
    let client = Arc::new(start_client(&server, |_, _| {}));
    let returned = client.call_async(1, "test/block", Value::Null);
    recv(&entered, "blocking request did not start");
    client.close();
    recv(&finished, "disconnect did not cancel the active request");
    let _ = recv(&returned, "call did not return");
    client.shutdown().unwrap();
}

#[test]
fn indexing_publishes_standard_and_legacy_progress() {
    let dir = temp_dir();
    let state = temp_dir();
    let scanner =
        FileScanner::new(root_string(&dir), state.path().join("scanner.db"), None).unwrap();
    let server = Server::new(Some(scanner), "", "test");
    let (progress_sender, progress) = unbounded();
    let (legacy_sender, legacy) = unbounded();
    let client = start_client(&server, move |method: &str, params: &Value| match method {
        "window/workDoneProgress/create" => {
            let _ = progress_sender.send("create".to_owned());
        }
        "$/progress" => {
            let kind = params["value"]["kind"]
                .as_str()
                .unwrap_or_default()
                .to_owned();
            let _ = progress_sender.send(kind);
        }
        "shopware/indexingStarted" | "shopware/indexingCompleted" => {
            let _ = legacy_sender.send(method.to_owned());
        }
        _ => {}
    });
    client
        .call(
            "initialize",
            json!({
                "rootUri": file_uri(dir.path()),
                "capabilities": {"window": {"workDoneProgress": true}},
            }),
        )
        .unwrap();
    client.notify("initialized", json!({}));
    assert_eq!(recv(&progress, "missing create"), "create");
    assert_eq!(recv(&progress, "missing begin"), "begin");
    assert_eq!(recv(&progress, "missing end"), "end");
    assert_eq!(recv(&legacy, "missing start"), "shopware/indexingStarted");
    assert_eq!(
        recv(&legacy, "missing completion"),
        "shopware/indexingCompleted"
    );
    client.call("shutdown", json!({})).unwrap();
    client.notify("exit", json!({}));
    client.shutdown().unwrap();
}

#[test]
fn execute_command_uses_registered_production_command() {
    let server = Server::new(None, "", "test");
    register_command(&server, "shopware/test", |_, raw| {
        let value = raw
            .and_then(|raw| raw.get("value"))
            .cloned()
            .unwrap_or(Value::Null);
        Ok(json!({"value": value}))
    });
    let result = server
        .handle_execute_command(
            &Context::background(),
            json!({"command": "shopware/test", "arguments": [{"value": "ok"}]}),
        )
        .unwrap();
    assert_eq!(result, json!({"value": "ok"}));
}

#[test]
fn execute_command_validates_command_and_argument_envelope() {
    let server = Server::new(None, "", "test");
    register_command(&server, "shopware/test", |_, _| Ok(Value::Null));
    let error = server
        .handle_execute_command(
            &Context::background(),
            json!({"command": "shopware/missing"}),
        )
        .unwrap_err();
    assert!(
        error.message.contains("Unknown Shopware command"),
        "{error}"
    );
    assert_eq!(error.code, codes::METHOD_NOT_FOUND);
    let error = server
        .handle_execute_command(
            &Context::background(),
            json!({"command": "shopware/test", "arguments": [{}, {}]}),
        )
        .unwrap_err();
    assert!(
        error.message.contains("at most one JSON argument"),
        "{error}"
    );
}

#[test]
fn unknown_requests_fail_and_notifications_are_ignored() {
    let server = Server::new(None, "", "test");
    let ctx = Context::background();
    let error = server
        .handle(&ctx, "custom/unknown", Value::Null, false)
        .unwrap_err();
    assert_eq!(error.code, codes::METHOD_NOT_FOUND);
    assert_eq!(error.message, "Method not implemented: custom/unknown");
    assert_eq!(
        server
            .handle(&ctx, "custom/unknown", Value::Null, true)
            .unwrap(),
        Value::Null
    );
    let error = server
        .handle(&ctx, "textDocument/hover", Value::Null, false)
        .unwrap_err();
    assert_eq!(error.message, "missing request parameters");
}

#[test]
fn disabled_features_return_well_formed_empty_results() {
    let server = Server::new(None, "", "test");
    {
        let mut state = crate::server::write(&server.configuration);
        for feature in [
            "completion",
            "hover",
            "semanticTokens",
            "references",
            "commands",
        ] {
            state.effective.features.insert(feature.into(), false);
        }
    }
    register_command(&server, "shopware/test", |_, _| Ok(json!("ok")));
    register_command(&server, "shopware/configuration/test", |_, _| {
        Ok(json!("config"))
    });
    let ctx = Context::background();
    let params =
        json!({"textDocument": {"uri": "file:///a.twig"}, "position": {"line": 0, "character": 0}});
    assert_eq!(
        server
            .handle(&ctx, "textDocument/completion", params.clone(), false)
            .unwrap(),
        json!({"isIncomplete": false, "items": []})
    );
    assert_eq!(
        server
            .handle(&ctx, "textDocument/hover", params.clone(), false)
            .unwrap(),
        Value::Null
    );
    assert_eq!(
        server
            .handle(
                &ctx,
                "textDocument/semanticTokens/full",
                params.clone(),
                false
            )
            .unwrap(),
        json!({"data": []})
    );
    assert_eq!(
        server
            .handle(&ctx, "textDocument/references", params, false)
            .unwrap(),
        json!([])
    );
    let error = server
        .handle(&ctx, "shopware/test", Value::Null, false)
        .unwrap_err();
    assert_eq!(
        error.message,
        "Shopware commands are disabled by configuration"
    );
    assert_eq!(
        server
            .handle(&ctx, "shopware/configuration/test", Value::Null, false)
            .unwrap(),
        json!("config")
    );
}
