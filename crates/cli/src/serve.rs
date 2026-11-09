//! `serve`: the language server over stdio or one remote client (Go
//! `serve.go`).

use std::io::{self, Read, Write};
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll, ready};
use std::time::Duration;

use swlsp_lsp::async_io::{Input, Output as StreamOutput};
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt, ReadBuf};
use tokio::time::Instant;

use crate::args::Serve;
use crate::logger::{LogSink, set_log_sink};
use crate::{Cancel, CliError, Output, Runner};

/// How long a cancelled server may take to finish its running request.
const CANCEL_GRACE: Duration = Duration::from_secs(5);

pub(crate) fn run_serve(runner: &Runner, flags: &Serve) -> Result<(), CliError> {
    let timeout = match flags.listen_timeout.as_deref() {
        None => Duration::ZERO,
        Some(value) => humantime::parse_duration(value).map_err(|error| {
            CliError::usage(format!(
                "invalid --listen-timeout {value:?}: {error} (expected a duration such as 30s)"
            ))
        })?,
    };
    if let Some(log_file) = flags.logfile.as_deref() {
        let mut options = std::fs::OpenOptions::new();
        options.create(true).append(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o644);
        }
        let file = options
            .open(log_file)
            .map_err(|error| CliError::fail(format!("open log file {log_file}: {error}")))?;
        set_log_sink(LogSink::File(file));
    }
    log::info!("Shopware LSP version: {}", runner.version());
    let executor = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .thread_name("lsp-serve")
        .enable_all()
        .build()
        .map_err(|error| CliError::fail(format!("start language server runtime: {error}")))?;
    let result = executor.block_on(serve(runner, flags, timeout));
    // A stdin reader may still block in a read the client never ends.
    executor.shutdown_background();
    result
}

async fn serve(runner: &Runner, flags: &Serve, timeout: Duration) -> Result<(), CliError> {
    if let Some(debug_address) = flags.debug.as_deref() {
        start_debug_server(debug_address)?;
    }
    let rpc_trace = flags.rpc_trace.then(|| runner.err_out.clone());
    let Some(listen) = flags.listen.as_deref() else {
        let input = Input::Blocking(runner.take_input());
        let output = StreamOutput::Blocking(Box::new(runner.out.clone()));
        return serve_streams(runner, input, output, rpc_trace).await;
    };

    let (network, address) = parse_listen_address(listen);
    let listener = Listener::bind(network, &address)
        .map_err(|error| CliError::fail(format!("listen on {listen}: {error}")))?;
    if runner.verbose {
        runner.write_err(&format!("Listening on {network};{address}\n"))?;
    }
    let deadline = (!timeout.is_zero()).then(|| Instant::now() + timeout);
    let accepted = tokio::select! {
        accepted = listener.accept() => accepted,
        () = runner.cancel.cancelled() => {
            listener.cleanup(&address);
            return Err(CliError::canceled());
        }
        () = sleep_until(deadline) => {
            listener.cleanup(&address);
            return Err(CliError::fail(format!(
                "listen timeout after {}",
                humantime::format_duration(timeout)
            )));
        }
    };
    let (input, output) = match accepted {
        Ok(streams) => streams,
        Err(error) => {
            listener.cleanup(&address);
            return Err(CliError::fail(format!("accept connection: {error}")));
        }
    };
    let result = serve_streams(runner, input, output, rpc_trace).await;
    listener.cleanup(&address);
    result
}

/// Completes at `deadline`, or never without one.
async fn sleep_until(deadline: Option<Instant>) {
    match deadline {
        Some(deadline) => tokio::time::sleep_until(deadline).await,
        None => std::future::pending().await,
    }
}

/// Serves one client. Cancellation stops serving and gives the running
/// request [`CANCEL_GRACE`] to finish.
async fn serve_streams(
    runner: &Runner,
    input: Input,
    output: StreamOutput,
    rpc_trace: Option<Output>,
) -> Result<(), CliError> {
    let (input, output) = match rpc_trace {
        Some(trace) => traced(input, output, trace),
        None => (input, output),
    };
    let application = Arc::new(swlsp_app::Application::with_options(
        runner.version(),
        swlsp_app::Options {
            allow_unsupported_project: runner.allow_unsupported_project,
        },
    ));
    let cancel: Cancel = runner.cancel.clone();
    let serving = application.serve(input, output, cancel.cancelled());
    tokio::pin!(serving);
    tokio::select! {
        result = &mut serving => return result.map_err(CliError::fail),
        () = runner.cancel.cancelled() => {}
    }
    let _ = tokio::time::timeout(CANCEL_GRACE, serving).await;
    Err(CliError::canceled())
}

fn parse_listen_address(value: &str) -> (&'static str, String) {
    if let Some(path) = value.strip_prefix("unix;") {
        return ("unix", path.to_owned());
    }
    if let Some(address) = value.strip_prefix("tcp;") {
        return ("tcp", address.to_owned());
    }
    ("tcp", value.to_owned())
}

enum Listener {
    Tcp(tokio::net::TcpListener),
    #[cfg(unix)]
    Unix(tokio::net::UnixListener),
}

#[cfg(unix)]
fn bind_unix(address: &str) -> Result<Listener, String> {
    let listener = std::os::unix::net::UnixListener::bind(address)
        .map_err(|error| format!("listen unix {address}: bind: {error}"))?;
    listener
        .set_nonblocking(true)
        .map_err(|error| error.to_string())?;
    tokio::net::UnixListener::from_std(listener)
        .map(Listener::Unix)
        .map_err(|error| error.to_string())
}

/// Rust's standard library has no Unix domain sockets on this platform.
#[cfg(not(unix))]
fn bind_unix(address: &str) -> Result<Listener, String> {
    Err(format!(
        "listen unix {address}: unix domain sockets are not supported on this platform"
    ))
}

/// Binds a TCP address the way Go's `net.Listen` reads it (`:port` means
/// every interface), on the current runtime.
fn bind_tcp(address: &str) -> Result<tokio::net::TcpListener, String> {
    let resolved = if address.starts_with(':') {
        format!("0.0.0.0{address}")
    } else {
        address.to_owned()
    };
    let listener = std::net::TcpListener::bind(&resolved)
        .map_err(|error| tcp_listen_error(address, &error))?;
    listener
        .set_nonblocking(true)
        .map_err(|error| error.to_string())?;
    tokio::net::TcpListener::from_std(listener).map_err(|error| error.to_string())
}

impl Listener {
    fn bind(network: &str, address: &str) -> Result<Self, String> {
        if network == "unix" {
            bind_unix(address)
        } else {
            bind_tcp(address).map(Listener::Tcp)
        }
    }

    async fn accept(&self) -> io::Result<(Input, StreamOutput)> {
        match self {
            Listener::Tcp(listener) => {
                let (stream, _) = listener.accept().await?;
                let (reader, writer) = stream.into_split();
                Ok((
                    Input::Async(Box::new(reader)),
                    StreamOutput::Async(Box::new(writer)),
                ))
            }
            #[cfg(unix)]
            Listener::Unix(listener) => {
                let (stream, _) = listener.accept().await?;
                let (reader, writer) = stream.into_split();
                Ok((
                    Input::Async(Box::new(reader)),
                    StreamOutput::Async(Box::new(writer)),
                ))
            }
        }
    }

    fn cleanup(&self, address: &str) {
        #[cfg(unix)]
        if let Listener::Unix(_) = self {
            let _ = std::fs::remove_file(address);
        }
        #[cfg(not(unix))]
        let _ = address;
    }
}

/// Copies the traffic of `input` and `output` to `trace` (`--rpc-trace`).
fn traced(input: Input, output: StreamOutput, trace: Output) -> (Input, StreamOutput) {
    let input = match input {
        Input::Blocking(inner) => Input::Blocking(Box::new(TraceReader {
            inner,
            trace: trace.clone(),
        })),
        Input::Async(inner) => Input::Async(Box::new(AsyncTraceReader {
            inner,
            trace: trace.clone(),
        })),
    };
    let output = match output {
        StreamOutput::Blocking(inner) => {
            StreamOutput::Blocking(Box::new(TraceWriter { inner, trace }))
        }
        StreamOutput::Async(inner) => {
            StreamOutput::Async(Box::new(AsyncTraceWriter { inner, trace }))
        }
    };
    (input, output)
}

fn trace_chunk(trace: &Output, direction: &[u8], data: &[u8]) {
    let mut chunk = direction.to_vec();
    chunk.extend_from_slice(data);
    let _ = trace.clone().write_all(&chunk);
}

const CLIENT_TO_SERVER: &[u8] = b"\n[client -> server]\n";
const SERVER_TO_CLIENT: &[u8] = b"\n[server -> client]\n";

/// Go `rpcTraceWriter` on the read side (`io.TeeReader`).
struct TraceReader {
    inner: Box<dyn Read + Send>,
    trace: Output,
}

impl Read for TraceReader {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let read = self.inner.read(buf)?;
        if read > 0 {
            trace_chunk(&self.trace, CLIENT_TO_SERVER, &buf[..read]);
        }
        Ok(read)
    }
}

/// Go `tracedWriter`.
struct TraceWriter {
    inner: Box<dyn Write + Send>,
    trace: Output,
}

impl Write for TraceWriter {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        let written = self.inner.write(buf)?;
        if written > 0 {
            trace_chunk(&self.trace, SERVER_TO_CLIENT, &buf[..written]);
        }
        Ok(written)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

/// [`TraceReader`] for a socket.
struct AsyncTraceReader {
    inner: Box<dyn AsyncRead + Unpin + Send>,
    trace: Output,
}

impl AsyncRead for AsyncTraceReader {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let before = buf.filled().len();
        ready!(Pin::new(&mut self.inner).poll_read(cx, buf))?;
        let read = &buf.filled()[before..];
        if !read.is_empty() {
            trace_chunk(&self.trace, CLIENT_TO_SERVER, read);
        }
        Poll::Ready(Ok(()))
    }
}

/// [`TraceWriter`] for a socket.
struct AsyncTraceWriter {
    inner: Box<dyn AsyncWrite + Unpin + Send>,
    trace: Output,
}

impl AsyncWrite for AsyncTraceWriter {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let written = ready!(Pin::new(&mut self.inner).poll_write(cx, buf))?;
        if written > 0 {
            trace_chunk(&self.trace, SERVER_TO_CLIENT, &buf[..written]);
        }
        Poll::Ready(Ok(written))
    }

    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_flush(cx)
    }

    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_shutdown(cx)
    }
}

/// Go serves `net/http/pprof` here. The Rust runtime has no equivalent
/// profiler, so the address is bound (with Go's error text) and every request
/// receives `501 Not Implemented`.
fn start_debug_server(address: &str) -> Result<(), CliError> {
    let listener = bind_tcp(address)
        .map_err(|error| CliError::fail(format!("start debug server: {error}")))?;
    let local = listener
        .local_addr()
        .map(|address| address.to_string())
        .unwrap_or_else(|_| address.to_owned());
    log::info!("Debug server listening on http://{local}/debug/pprof/");
    tokio::spawn(async move {
        while let Ok((mut stream, _)) = listener.accept().await {
            tokio::spawn(async move {
                let mut request = [0u8; 4096];
                let _ = tokio::io::AsyncReadExt::read(&mut stream, &mut request).await;
                let body = "runtime profiling endpoints are not available in this build\n";
                let response = format!(
                    "HTTP/1.1 501 Not Implemented\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                );
                let _ = stream.write_all(response.as_bytes()).await;
            });
        }
    });
    Ok(())
}

/// Go's `net.Listen("tcp", address)` error text for the common failures.
fn tcp_listen_error(address: &str, error: &io::Error) -> String {
    if error.kind() == io::ErrorKind::InvalidInput {
        match address.rsplit_once(':') {
            Some((_, port)) if error.to_string().contains("invalid port") => {
                return format!("listen tcp: address {port}: invalid port");
            }
            None => return format!("listen tcp: address {address}: missing port in address"),
            _ => {}
        }
    }
    if error.raw_os_error().is_some() {
        return format!("listen tcp {address}: bind: {error}");
    }
    format!("listen tcp {address}: {error}")
}
