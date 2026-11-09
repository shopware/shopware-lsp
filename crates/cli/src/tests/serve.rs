//! `serve` over a socket: the Tokio listener, async streams and tracing.

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::time::{Duration, Instant};

use serde_json::{Value, json};

use super::common::{run_with, temp_dir, test_options};
use crate::Cancel;

/// A port that was free a moment ago.
fn free_port() -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    listener.local_addr().unwrap().port()
}

fn connect(address: &str) -> TcpStream {
    let deadline = Instant::now() + Duration::from_secs(30);
    loop {
        match TcpStream::connect(address) {
            Ok(stream) => return stream,
            Err(error) if Instant::now() < deadline => {
                let _ = error;
                std::thread::sleep(Duration::from_millis(20));
            }
            Err(error) => panic!("connect to {address}: {error}"),
        }
    }
}

fn send(stream: &mut TcpStream, message: &Value) {
    let body = serde_json::to_vec(message).unwrap();
    write!(stream, "Content-Length: {}\r\n\r\n", body.len()).unwrap();
    stream.write_all(&body).unwrap();
}

/// The bodies of every framed message in `text`.
fn messages(text: &str) -> Vec<Value> {
    text.split("Content-Length: ")
        .filter_map(|frame| frame.split_once("\r\n\r\n"))
        .filter_map(|(_, body)| serde_json::from_str(body).ok())
        .collect()
}

#[test]
fn remote_serve_answers_a_tcp_client_and_traces_traffic() {
    let root = temp_dir();
    let address = format!("127.0.0.1:{}", free_port());
    let server = {
        let address = address.clone();
        std::thread::spawn(move || {
            run_with(
                test_options(),
                &Cancel::new(),
                &[
                    "--allow-unsupported-project",
                    "serve",
                    "--listen",
                    &address,
                    "--listen-timeout",
                    "30s",
                    "--rpc-trace",
                ],
            )
        })
    };
    let mut stream = connect(&address);
    stream
        .set_read_timeout(Some(Duration::from_secs(60)))
        .unwrap();
    let root_uri = swlsp_uriutil::file_uri(root.path().to_str().unwrap());
    send(
        &mut stream,
        &json!({"jsonrpc": "2.0", "id": 1, "method": "initialize",
            "params": {"processId": null, "rootUri": root_uri, "capabilities": {}}}),
    );
    send(
        &mut stream,
        &json!({"jsonrpc": "2.0", "id": 2, "method": "shutdown"}),
    );
    send(&mut stream, &json!({"jsonrpc": "2.0", "method": "exit"}));
    let mut received = String::new();
    stream.read_to_string(&mut received).unwrap();

    let messages = messages(&received);
    let initialize = messages
        .iter()
        .find(|message| message["id"] == 1)
        .unwrap_or_else(|| panic!("no initialize response in {received}"));
    assert!(
        initialize["result"]["capabilities"].is_object(),
        "{initialize}"
    );
    assert!(
        messages.iter().any(|message| message["id"] == 2),
        "{received}"
    );

    let run = server.join().unwrap();
    assert!(run.result.is_ok(), "{:?}\n{}", run.result, run.err);
    assert!(run.err.contains("[client -> server]"), "{}", run.err);
    assert!(run.err.contains("[server -> client]"), "{}", run.err);
    assert!(run.err.contains("\"method\":\"initialize\""), "{}", run.err);
}

#[test]
fn remote_serve_stops_after_the_listen_timeout() {
    let address = format!("127.0.0.1:{}", free_port());
    let run = run_with(
        test_options(),
        &Cancel::new(),
        &["serve", "--listen", &address, "--listen-timeout", "50ms"],
    );
    let error = run
        .result
        .expect_err("serve without a client must time out");
    assert_eq!(error.message, "serve: listen timeout after 50ms");
}
