//! JSON-RPC transport and the ordered request dispatcher.
//!
//! Framing and message types come from rust-analyzer's `lsp-server` crate;
//! the threading model is our own and mirrors the Go server:
//!
//! * a reader thread decodes messages, handles `$/cancelRequest` directly,
//!   routes responses to pending outgoing calls and enqueues everything else
//!   on a bounded queue;
//! * one worker executes requests and notifications in arrival order, so
//!   document notifications can never race feature requests;
//! * a request cancelled before or during execution answers `-32800`;
//! * disconnect cancels every queued and running request before teardown.

use std::collections::HashMap;
use std::io::{self, BufReader, Read, Write};
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use crossbeam_channel::{Receiver, Sender, bounded, select};
use lsp_server::{Message, Notification, Request, RequestId, Response};
use serde::Serialize;
use serde_json::Value;

use crate::context::{CancellationToken, RpcError, lock};

const REQUEST_QUEUE_CAPACITY: usize = 128;

/// Error of an outgoing call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CallError {
    Closed,
    Cancelled,
    Timeout,
    Remote(RpcError),
    Io(String),
}

impl std::fmt::Display for CallError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            CallError::Closed => f.write_str("jsonrpc2: connection is closed"),
            CallError::Cancelled => f.write_str("context canceled"),
            CallError::Timeout => f.write_str("context deadline exceeded"),
            CallError::Remote(error) => error.fmt(f),
            CallError::Io(error) => f.write_str(error),
        }
    }
}

impl std::error::Error for CallError {}

/// The write half of a connection plus outgoing-call bookkeeping.
pub(crate) struct Connection {
    writer: Mutex<Box<dyn Write + Send>>,
    closed: AtomicBool,
    next_id: AtomicI32,
    pending: Mutex<HashMap<RequestId, Sender<Response>>>,
    disconnect: Sender<()>,
}

impl Connection {
    fn new(writer: Box<dyn Write + Send>, disconnect: Sender<()>) -> Self {
        Self {
            writer: Mutex::new(writer),
            closed: AtomicBool::new(false),
            next_id: AtomicI32::new(1),
            pending: Mutex::new(HashMap::new()),
            disconnect,
        }
    }

    fn send(&self, message: Message) -> Result<(), CallError> {
        if self.closed.load(Ordering::Acquire) {
            return Err(CallError::Closed);
        }
        let mut writer = lock(&self.writer);
        message
            .write(&mut *writer)
            .map_err(|error| CallError::Io(error.to_string()))
    }

    pub(crate) fn notify(&self, method: &str, params: impl Serialize) -> Result<(), CallError> {
        let params =
            serde_json::to_value(params).map_err(|error| CallError::Io(error.to_string()))?;
        self.send(Message::Notification(Notification {
            method: method.to_owned(),
            params,
        }))
    }

    pub(crate) fn respond(&self, response: Response) {
        if let Err(error) = self.send(Message::Response(response)) {
            log::debug!("jsonrpc2 handler: sending response: {error}");
        }
    }

    /// Sends a request and blocks until its response, cancellation or the
    /// optional timeout.
    pub(crate) fn call(
        &self,
        method: &str,
        params: impl Serialize,
        cancel: &CancellationToken,
        timeout: Option<Duration>,
    ) -> Result<Value, CallError> {
        if cancel.is_cancelled() {
            return Err(CallError::Cancelled);
        }
        let id = RequestId::from(self.next_id.fetch_add(1, Ordering::Relaxed));
        let (sender, receiver) = bounded(1);
        lock(&self.pending).insert(id.clone(), sender);
        let params =
            serde_json::to_value(params).map_err(|error| CallError::Io(error.to_string()))?;
        if let Err(error) = self.send(Message::Request(Request {
            id: id.clone(),
            method: method.to_owned(),
            params,
        })) {
            lock(&self.pending).remove(&id);
            return Err(error);
        }
        // Poll the token instead of registering a cancel callback, so
        // long-lived tokens (the server lifecycle) do not accumulate one
        // callback per call.
        const POLL: Duration = Duration::from_millis(25);
        let deadline = timeout.map(|timeout| Instant::now() + timeout);
        let result = loop {
            if cancel.is_cancelled() {
                break Err(CallError::Cancelled);
            }
            let mut wait = POLL;
            if let Some(deadline) = deadline {
                let now = Instant::now();
                if now >= deadline {
                    break Err(CallError::Timeout);
                }
                wait = wait.min(deadline - now);
            }
            match receiver.recv_timeout(wait) {
                Ok(response) => {
                    break match response.error {
                        Some(error) => {
                            Err(CallError::Remote(RpcError::new(error.code, error.message)))
                        }
                        None => Ok(response.result.unwrap_or(Value::Null)),
                    };
                }
                Err(crossbeam_channel::RecvTimeoutError::Timeout) => {}
                Err(crossbeam_channel::RecvTimeoutError::Disconnected) => {
                    break Err(CallError::Closed);
                }
            }
        };
        lock(&self.pending).remove(&id);
        result
    }

    fn route_response(&self, response: Response) {
        if let Some(sender) = lock(&self.pending).remove(&response.id) {
            let _ = sender.try_send(response);
        }
    }

    /// Marks the connection closed, fails pending calls and wakes `start`.
    pub(crate) fn close(&self) {
        if self.closed.swap(true, Ordering::AcqRel) {
            return;
        }
        lock(&self.pending).clear();
        let _ = self.disconnect.try_send(());
    }
}

/// One queued request or notification.
pub(crate) struct Work {
    pub id: Option<RequestId>,
    pub method: String,
    pub params: Value,
    pub cancel: CancellationToken,
}

/// Executes one unit of work; implemented by the server.
pub(crate) trait Handler: Send + Sync + 'static {
    /// Handles a request or notification. Notifications ignore the result.
    fn handle(&self, conn: &Arc<Connection>, work: &Work) -> Result<Value, RpcError>;

    /// Lets the server move selected work off the ordered worker (CLI pull
    /// diagnostics). Returns the work back when it must run in order.
    fn offload(&self, dispatcher: &Arc<Dispatcher>, work: Work) -> Option<Work>;
}

/// Tracks queued and running requests so the reader can cancel them.
pub(crate) struct Dispatcher {
    root: CancellationToken,
    requests: Mutex<HashMap<RequestId, CancellationToken>>,
    conn: Arc<Connection>,
    handler: Arc<dyn Handler>,
}

impl Dispatcher {
    fn cancel_request(&self, params: &Value) {
        let Some(id) = params.get("id") else {
            return;
        };
        let Ok(id) = serde_json::from_value::<RequestId>(id.clone()) else {
            return;
        };
        if let Some(token) = lock(&self.requests).get(&id) {
            token.cancel();
        }
    }

    /// Runs one unit of work and replies to requests.
    pub(crate) fn execute(&self, work: Work) {
        let result = if work.cancel.is_cancelled() && work.id.is_some() {
            Err(RpcError::cancelled())
        } else {
            self.handler.handle(&self.conn, &work)
        };
        if let Some(id) = &work.id {
            lock(&self.requests).remove(id);
            let response = match result {
                Ok(value) => Response {
                    id: id.clone(),
                    result: Some(value),
                    error: None,
                },
                Err(error) => Response::new_err(id.clone(), error.code, error.message),
            };
            self.conn.respond(response);
        } else if let Err(error) = result {
            log::warn!(
                "jsonrpc2 handler: notification {:?} handling error: {}",
                work.method,
                error.message
            );
        }
        work.cancel.cancel();
    }
}

/// Serves one connection until the client disconnects or sends `exit`.
/// Returns after every queued and running request finished.
pub(crate) fn serve(
    input: impl Read + Send + 'static,
    output: impl Write + Send + 'static,
    handler: Arc<dyn Handler>,
    on_connect: impl FnOnce(&Arc<Connection>),
) -> Arc<Connection> {
    let (disconnect_sender, disconnect) = bounded::<()>(2);
    let conn = Arc::new(Connection::new(Box::new(output), disconnect_sender.clone()));
    on_connect(&conn);
    let dispatcher = Arc::new(Dispatcher {
        root: CancellationToken::new(),
        requests: Mutex::new(HashMap::new()),
        conn: conn.clone(),
        handler,
    });
    let (queue_sender, queue) = bounded::<Work>(REQUEST_QUEUE_CAPACITY);
    let (stop_sender, stop) = bounded::<()>(1);

    let worker = {
        let dispatcher = dispatcher.clone();
        thread::Builder::new()
            .name("lsp-worker".into())
            .spawn(move || run_worker(&dispatcher, &queue, &stop))
            .expect("spawn LSP worker thread")
    };
    {
        let dispatcher = dispatcher.clone();
        let conn = conn.clone();
        // The reader is detached: a blocking read cannot be interrupted, and
        // the process (or the client closing its pipe) ends it.
        let _ = thread::Builder::new()
            .name("lsp-reader".into())
            .spawn(move || {
                read_messages(input, &dispatcher, &queue_sender);
                conn.close();
                let _ = disconnect_sender.try_send(());
            })
            .expect("spawn LSP reader thread");
    }

    let _ = disconnect.recv();
    conn.close();
    dispatcher.root.cancel();
    let _ = stop_sender.try_send(());
    let _ = worker.join();
    conn
}

fn read_messages(input: impl Read, dispatcher: &Arc<Dispatcher>, queue: &Sender<Work>) {
    let mut reader = BufReader::new(input);
    loop {
        if dispatcher.conn.closed.load(Ordering::Acquire) {
            return;
        }
        let message = match Message::read(&mut reader) {
            Ok(Some(message)) => message,
            Ok(None) => return,
            Err(error) => {
                if error.kind() != io::ErrorKind::UnexpectedEof {
                    log::warn!("jsonrpc2: reading message: {error}");
                }
                return;
            }
        };
        let (id, method, params) = match message {
            Message::Response(response) => {
                dispatcher.conn.route_response(response);
                continue;
            }
            Message::Notification(notification) => {
                if notification.method == "$/cancelRequest" {
                    dispatcher.cancel_request(&notification.params);
                    continue;
                }
                (None, notification.method, notification.params)
            }
            Message::Request(request) => (Some(request.id), request.method, request.params),
        };
        let cancel = dispatcher.root.child();
        if let Some(id) = &id {
            lock(&dispatcher.requests).insert(id.clone(), cancel.clone());
        }
        let mut work = Work {
            id,
            method,
            params,
            cancel,
        };
        // Block while the queue is full, but give up once the dispatcher is
        // shutting down.
        loop {
            match queue.send_timeout(work, Duration::from_millis(50)) {
                Ok(()) => break,
                Err(crossbeam_channel::SendTimeoutError::Timeout(returned)) => {
                    if dispatcher.root.is_cancelled() {
                        return;
                    }
                    work = returned;
                }
                Err(crossbeam_channel::SendTimeoutError::Disconnected(_)) => return,
            }
        }
    }
}

fn run_worker(dispatcher: &Arc<Dispatcher>, queue: &Receiver<Work>, stop: &Receiver<()>) {
    loop {
        if dispatcher.root.is_cancelled() {
            return;
        }
        let work = select! {
            recv(queue) -> work => match work {
                Ok(work) => work,
                Err(_) => return,
            },
            recv(stop) -> _ => return,
        };
        if dispatcher.root.is_cancelled() {
            return;
        }
        if let Some(work) = dispatcher.handler.offload(dispatcher, work) {
            dispatcher.execute(work);
        }
    }
}
