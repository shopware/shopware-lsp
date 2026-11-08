//! The `shopware-lsp` executable (port of Go `main.go`).

use std::io::Write;

// musl's malloc takes a global lock and scales poorly across the indexer's
// threads; the static musl release builds use mimalloc instead.
#[cfg(target_env = "musl")]
#[global_allocator]
static GLOBAL: mimalloc::MiMalloc = mimalloc::MiMalloc;

const LICENSE: &str = include_str!("../../../LICENSE");
const THIRD_PARTY_NOTICES: &str = include_str!("../../../THIRD_PARTY_NOTICES.md");

fn main() {
    let version = option_env!("SHOPWARE_LSP_VERSION").unwrap_or("dev");
    let cancel = swlsp_cli::Cancel::new();
    install_interrupt_handler(cancel.clone());
    let mut runner = swlsp_cli::Runner::new(swlsp_cli::Options {
        version: version.to_owned(),
        license: LICENSE.to_owned(),
        third_party_notices: THIRD_PARTY_NOTICES.to_owned(),
    });
    let args: Vec<String> = std::env::args().skip(1).collect();
    let result = runner.run(
        &cancel,
        &args,
        Box::new(std::io::stdin()),
        swlsp_cli::Output::stdout(),
        swlsp_cli::Output::stderr(),
    );
    if let Err(error) = &result {
        let mut stderr = std::io::stderr();
        let _ = writeln!(stderr, "{}", error.message);
        let _ = stderr.flush();
    }
    let _ = std::io::stdout().flush();
    std::process::exit(swlsp_cli::exit_code(&result));
}

/// Go `signal.NotifyContext(ctx, os.Interrupt)`: the first SIGINT cancels
/// the command; later ones are absorbed until exit. SIGINT is blocked in
/// every thread and received by a dedicated `sigwait` thread.
#[cfg(unix)]
fn install_interrupt_handler(cancel: swlsp_cli::Cancel) {
    // SAFETY: plain libc signal-mask calls on a local, initialized set.
    unsafe {
        let mut set: libc::sigset_t = std::mem::zeroed();
        libc::sigemptyset(&mut set);
        libc::sigaddset(&mut set, libc::SIGINT);
        libc::pthread_sigmask(libc::SIG_BLOCK, &set, std::ptr::null_mut());
        let set_copy = set;
        let _ = std::thread::Builder::new()
            .name("signals".into())
            .spawn(move || {
                loop {
                    let mut signal: libc::c_int = 0;
                    if libc::sigwait(&set_copy, &mut signal) == 0 && signal == libc::SIGINT {
                        cancel.cancel();
                    }
                }
            });
    }
}

/// Windows: Ctrl+C / Ctrl+Break cancel the command (Go `os.Interrupt`).
#[cfg(windows)]
fn install_interrupt_handler(cancel: swlsp_cli::Cancel) {
    use std::sync::OnceLock;
    use windows_sys::Win32::Foundation::{FALSE, TRUE};
    use windows_sys::Win32::System::Console::{
        CTRL_BREAK_EVENT, CTRL_C_EVENT, SetConsoleCtrlHandler,
    };

    static CANCEL: OnceLock<swlsp_cli::Cancel> = OnceLock::new();

    unsafe extern "system" fn handler(event: u32) -> windows_sys::core::BOOL {
        if event == CTRL_C_EVENT || event == CTRL_BREAK_EVENT {
            if let Some(cancel) = CANCEL.get() {
                cancel.cancel();
            }
            return TRUE;
        }
        FALSE
    }

    let _ = CANCEL.set(cancel);
    // SAFETY: registers a static handler function; no data is borrowed.
    unsafe {
        SetConsoleCtrlHandler(Some(handler), TRUE);
    }
}

#[cfg(not(any(unix, windows)))]
fn install_interrupt_handler(_cancel: swlsp_cli::Cancel) {}
