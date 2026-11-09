//! The in-process session client.

use std::time::Duration;

use serde_json::{Value, json};
use swlsp_lsp::async_io::{Output, spawn_output};
use tokio::io::AsyncReadExt;

use crate::Cancel;
use crate::session::RpcClient;

#[test]
fn cancelled_call_cancels_the_request_on_the_server() {
    let executor = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(1)
        .enable_all()
        .build()
        .unwrap();
    let (client, mut server) = {
        let _entered = executor.enter();
        let (client_end, server_end) = tokio::io::duplex(64 * 1024);
        let (frames, _written) = spawn_output(Output::Async(Box::new(client_end))).unwrap();
        (RpcClient::new(frames), server_end)
    };
    let cancel = Cancel::new();
    let canceller = {
        let cancel = cancel.clone();
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(50));
            cancel.cancel();
        })
    };

    let reply = client.call("textDocument/hover", json!({}), &cancel, None);
    canceller.join().unwrap();
    assert_eq!(reply, Err("context canceled".to_owned()));

    // Closing the client ends the stream, so everything sent can be read.
    client.close();
    let sent = executor.block_on(async move {
        let mut text = String::new();
        server.read_to_string(&mut text).await.unwrap();
        text
    });
    let messages: Vec<Value> = sent
        .split("\r\n\r\n")
        .skip(1)
        .filter_map(|body| {
            let body = body.split("Content-Length").next().unwrap_or(body);
            serde_json::from_str(body).ok()
        })
        .collect();
    assert_eq!(
        messages,
        [
            json!({"jsonrpc": "2.0", "id": 1, "method": "textDocument/hover", "params": {}}),
            json!({"jsonrpc": "2.0", "method": "$/cancelRequest", "params": {"id": 1}}),
        ]
    );
}
