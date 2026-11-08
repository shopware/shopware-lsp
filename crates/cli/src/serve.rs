//! `serve`: the language server over stdio or one remote client (Go
//! `serve.go`).

use std::io::{self, Read, Write};
use std::net::TcpListener;
#[cfg(unix)]
use std::os::unix::net::UnixListener;
use std::sync::Arc;
use std::time::{Duration, Instant};

use crossbeam_channel::bounded;

use crate::args::Serve;
use crate::logger::{LogSink, set_log_sink};
use crate::{CliError, Output, Runner};

const POLL: Duration = Duration::from_millis(25);

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
    if let Some(debug_address) = flags.debug.as_deref() {
        start_debug_server(debug_address)?;
    }
    let rpc_trace = flags.rpc_trace;
    let Some(listen) = flags.listen.as_deref() else {
        let input = runner.take_input();
        let output = runner.out.clone();
        return serve_streams(runner, input, Box::new(output), rpc_trace, None);
    };

    let (network, address) = parse_listen_address(listen);
    let listener = Listener::bind(network, &address)
        .map_err(|error| CliError::fail(format!("listen on {listen}: {error}")))?;
    if runner.verbose {
        runner.write_err(&format!("Listening on {network};{address}\n"))?;
    }
    let deadline = (!timeout.is_zero()).then(|| Instant::now() + timeout);
    let accepted = loop {
        if runner.cancel.is_cancelled() {
            listener.cleanup(&address);
            return Err(CliError::canceled());
        }
        if let Some(deadline) = deadline
            && Instant::now() >= deadline
        {
            listener.cleanup(&address);
            return Err(CliError::fail(format!(
                "listen timeout after {}",
                humantime::format_duration(timeout)
            )));
        }
        match listener.accept() {
            Ok(Some(connection)) => break connection,
            Ok(None) => std::thread::sleep(POLL),
            Err(error) => {
                listener.cleanup(&address);
                return Err(CliError::fail(format!("accept connection: {error}")));
            }
        }
    };
    let (reader, writer, shutdown) = accepted;
    let result = serve_streams(runner, reader, writer, rpc_trace, Some(shutdown));
    listener.cleanup(&address);
    result
}

type Shutdown = Box<dyn Fn() + Send>;

fn serve_streams(
    runner: &Runner,
    input: Box<dyn Read + Send>,
    output: Box<dyn Write + Send>,
    rpc_trace: bool,
    shutdown: Option<Shutdown>,
) -> Result<(), CliError> {
    let (input, output): (Box<dyn Read + Send>, Box<dyn Write + Send>) = if rpc_trace {
        (
            Box::new(TraceReader {
                inner: input,
                trace: runner.err_out.clone(),
            }),
            Box::new(TraceWriter {
                inner: output,
                trace: runner.err_out.clone(),
            }),
        )
    } else {
        (input, output)
    };
    let application = Arc::new(swlsp_app::Application::with_options(
        runner.version(),
        swlsp_app::Options {
            allow_unsupported_project: runner.allow_unsupported_project,
        },
    ));
    let (done, finished) = bounded(1);
    let server = application.clone();
    std::thread::Builder::new()
        .name("lsp-serve".into())
        .spawn(move || {
            let _ = done.send(server.run(input, output));
        })
        .map_err(|error| CliError::fail(format!("start language server: {error}")))?;
    loop {
        match finished.recv_timeout(POLL) {
            Ok(result) => {
                let _ = application.close();
                return result.map_err(CliError::fail);
            }
            Err(crossbeam_channel::RecvTimeoutError::Disconnected) => {
                let _ = application.close();
                return Ok(());
            }
            Err(crossbeam_channel::RecvTimeoutError::Timeout) => {}
        }
        if runner.cancel.is_cancelled() {
            if let Some(shutdown) = &shutdown {
                shutdown();
            }
            let _ = application.close();
            let _ = finished.recv_timeout(Duration::from_secs(5));
            return Err(CliError::canceled());
        }
    }
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
    Tcp(TcpListener),
    #[cfg(unix)]
    Unix(UnixListener),
}

type Accepted = (Box<dyn Read + Send>, Box<dyn Write + Send>, Shutdown);

#[cfg(unix)]
fn bind_unix(address: &str) -> Result<Listener, String> {
    let listener = UnixListener::bind(address)
        .map_err(|error| format!("listen unix {address}: bind: {error}"))?;
    listener
        .set_nonblocking(true)
        .map_err(|error| error.to_string())?;
    Ok(Listener::Unix(listener))
}

/// Rust's standard library has no Unix domain sockets on this platform.
#[cfg(not(unix))]
fn bind_unix(address: &str) -> Result<Listener, String> {
    Err(format!(
        "listen unix {address}: unix domain sockets are not supported on this platform"
    ))
}

impl Listener {
    fn bind(network: &str, address: &str) -> Result<Self, String> {
        let listener = if network == "unix" {
            bind_unix(address)?
        } else {
            let resolved = if address.starts_with(':') {
                format!("0.0.0.0{address}")
            } else {
                address.to_owned()
            };
            let listener =
                TcpListener::bind(&resolved).map_err(|error| tcp_listen_error(address, &error))?;
            listener
                .set_nonblocking(true)
                .map_err(|error| error.to_string())?;
            Listener::Tcp(listener)
        };
        Ok(listener)
    }

    fn accept(&self) -> io::Result<Option<Accepted>> {
        let result = match self {
            Listener::Tcp(listener) => listener.accept().and_then(|(stream, _)| {
                stream.set_nonblocking(false)?;
                let reader = stream.try_clone()?;
                Ok(stream.into_accepted(reader))
            }),
            #[cfg(unix)]
            Listener::Unix(listener) => listener.accept().and_then(|(stream, _)| {
                stream.set_nonblocking(false)?;
                let reader = stream.try_clone()?;
                Ok(stream.into_accepted(reader))
            }),
        };
        match result {
            Ok(accepted) => Ok(Some(accepted)),
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => Ok(None),
            Err(error) => Err(error),
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

trait IntoAccepted {
    fn into_accepted(self, reader: Self) -> Accepted;
}

impl IntoAccepted for std::net::TcpStream {
    fn into_accepted(self, reader: Self) -> Accepted {
        let shutdown_handle = self.try_clone().ok();
        (
            Box::new(reader),
            Box::new(self),
            Box::new(move || {
                if let Some(stream) = &shutdown_handle {
                    let _ = stream.shutdown(std::net::Shutdown::Both);
                }
            }),
        )
    }
}

#[cfg(unix)]
impl IntoAccepted for std::os::unix::net::UnixStream {
    fn into_accepted(self, reader: Self) -> Accepted {
        let shutdown_handle = self.try_clone().ok();
        (
            Box::new(reader),
            Box::new(self),
            Box::new(move || {
                if let Some(stream) = &shutdown_handle {
                    let _ = stream.shutdown(std::net::Shutdown::Both);
                }
            }),
        )
    }
}

/// Go `rpcTraceWriter` on the read side (`io.TeeReader`).
struct TraceReader {
    inner: Box<dyn Read + Send>,
    trace: Output,
}

impl Read for TraceReader {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let read = self.inner.read(buf)?;
        if read > 0 {
            let mut chunk = b"\n[client -> server]\n".to_vec();
            chunk.extend_from_slice(&buf[..read]);
            let _ = self.trace.write_all(&chunk);
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
            let mut chunk = b"\n[server -> client]\n".to_vec();
            chunk.extend_from_slice(&buf[..written]);
            let _ = self.trace.write_all(&chunk);
        }
        Ok(written)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

/// Go serves `net/http/pprof` here. The Rust runtime has no equivalent
/// profiler, so the address is bound (with Go's error text) and every request
/// receives `501 Not Implemented`.
fn start_debug_server(address: &str) -> Result<(), CliError> {
    let resolved = if address.starts_with(':') {
        format!("0.0.0.0{address}")
    } else {
        address.to_owned()
    };
    let listener = TcpListener::bind(&resolved).map_err(|error| {
        CliError::fail(format!(
            "start debug server: {}",
            tcp_listen_error(address, &error)
        ))
    })?;
    let local = listener
        .local_addr()
        .map(|address| address.to_string())
        .unwrap_or_else(|_| address.to_owned());
    log::info!("Debug server listening on http://{local}/debug/pprof/");
    let _ = std::thread::Builder::new()
        .name("debug-server".into())
        .spawn(move || {
            for mut stream in listener.incoming().flatten() {
                let mut request = [0u8; 4096];
                let _ = stream.read(&mut request);
                let body = "runtime profiling endpoints are not available in this build\n";
                let _ = write!(
                    stream,
                    "HTTP/1.1 501 Not Implemented\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                );
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
