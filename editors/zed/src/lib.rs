use std::fs;

use zed_extension_api::{
    self as zed,
    http_client::{HttpMethod, HttpRequest, RedirectPolicy},
    settings::{ContextServerSettings, LspSettings},
    Architecture, ContextServerConfiguration, ContextServerId, DownloadedFileType,
    LanguageServerId, LanguageServerInstallationStatus, Os, Project, Result,
};

const SERVER_NAME: &str = "shopware-lsp";

/// Reuse the platform binaries packaged for the VS Code extension on Open VSX.
const OPEN_VSX_API: &str = "https://open-vsx.org/api/shopware/shopware-lsp";

/// Must match the server's own `ClientProtocolVersion`. A mismatch makes
/// `initialize` fail outright, so the contract check pins it.
const CLIENT_PROTOCOL_VERSION: u32 = 1;

struct ShopwareLspExtension {
    /// A path that came from settings or `Worktree::which`, remembered so both
    /// hooks agree. Kept apart from `cached_download` on purpose: a download
    /// must never outrank a binary the user installed, which is what happens
    /// if the MCP server downloads before the language server looks at PATH.
    cached_binary_path: Option<String>,
    /// The managed download, remembered so a settings reload does not send
    /// another request to Open VSX.
    cached_download: Option<String>,
    /// Remembered from the language server, which is the only hook that gets a
    /// `Worktree`. `Project` exposes worktree IDs but no paths, so this is the
    /// only way the MCP server can learn the project root.
    cached_worktree_root: Option<String>,
    /// The normalized editor configuration, remembered for the same reason:
    /// the MCP hook has no `Worktree` and so cannot read Zed settings itself.
    cached_configuration: Option<String>,
    /// `GOMEMLIMIT` value, remembered for the MCP process for the same reason.
    cached_memory_limit: Option<String>,
}

/// Where the binary lives inside the downloaded `.vsix`, which is a plain zip.
fn binary_path_for(dir: &str, os: Os) -> String {
    match os {
        Os::Windows => format!("{dir}/extension/{SERVER_NAME}.exe"),
        _ => format!("{dir}/extension/{SERVER_NAME}"),
    }
}

/// Map a platform to an Open VSX target triple.
///
/// musl is deliberately absent: the extension API cannot tell glibc from musl,
/// so Alpine users have to point `binary.path` at an `alpine-*` build themselves.
fn target_for(os: Os, arch: Architecture) -> Result<&'static str> {
    match (os, arch) {
        (Os::Mac, Architecture::Aarch64) => Ok("darwin-arm64"),
        (Os::Mac, Architecture::X8664) => Ok("darwin-x64"),
        (Os::Linux, Architecture::Aarch64) => Ok("linux-arm64"),
        (Os::Linux, Architecture::X8664) => Ok("linux-x64"),
        (Os::Windows, Architecture::X8664) => Ok("win32-x64"),
        _ => Err(format!(
            "no published {SERVER_NAME} build for this platform. Set \
             lsp.{SERVER_NAME}.binary.path to a server you built yourself."
        )),
    }
}

fn open_vsx_target() -> Result<&'static str> {
    let (os, arch) = zed::current_platform();
    target_for(os, arch)
}

/// Read the version and artifact URL out of an Open VSX `/latest` payload.
fn parse_latest_release(target: &str, body: &[u8]) -> Result<(String, String)> {
    let payload: zed::serde_json::Value = zed::serde_json::from_slice(body)
        .map_err(|err| format!("Open VSX returned malformed JSON: {err}"))?;

    let version = payload["version"]
        .as_str()
        .ok_or_else(|| "Open VSX response has no version".to_string())?;
    let url = payload["files"]["download"]
        .as_str()
        .ok_or_else(|| format!("Open VSX has no download for {target}"))?;

    Ok((version.to_string(), url.to_string()))
}

/// Argument vector for the MCP server.
///
/// Global flags have to precede the subcommand; the binary rejects
/// `mcp -root ...` with "mcp takes no arguments".
fn mcp_args(root: Option<&str>) -> Vec<String> {
    match root {
        Some(root) => vec!["-root".into(), root.into(), "mcp".into()],
        None => vec!["mcp".into()],
    }
}

/// Tell the server which editor-side commands this client implements.
///
/// The server filters every command-backed code action *and code lens* down to
/// this allow-list, which is what stops ~20 generator entries appearing in the
/// menu and doing nothing. Diagnostic quickfixes are unaffected: they carry no
/// command.
///
/// `shopware.openReferences` is declared even though Zed cannot execute it,
/// because it backs all four code lenses and one of them prints the route and
/// methods on a Store-API controller. That is worth reading without ever
/// clicking, and omitting the command hides the text along with the action.
/// The generator actions use different command names, so they stay filtered.
///
/// `presentationProfile` stays `full` because Zed has no PHP intelligence of
/// its own; `framework` is for hosts like PhpStorm that do.
fn default_initialization_options() -> zed::serde_json::Value {
    zed::serde_json::json!({
        "shopwareClient": {
            "protocolVersion": CLIENT_PROTOCOL_VERSION,
            "presentationProfile": "full",
            "supportedCommands": ["shopware.openReferences"],
        }
    })
}

/// Recursively overlay `overlay` onto `base`, so a user can override any single
/// key without having to restate the whole object.
fn merge_json(base: &mut zed::serde_json::Value, overlay: zed::serde_json::Value) {
    match (base, overlay) {
        (zed::serde_json::Value::Object(target), zed::serde_json::Value::Object(source)) => {
            for (key, value) in source {
                merge_json(
                    target.entry(key).or_insert(zed::serde_json::Value::Null),
                    value,
                );
            }
        }
        (target, source) => *target = source,
    }
}

/// Translate the VS Code style `shopwareLSP.*` block into the shape the server
/// actually reads.
///
/// The server has no `shopwareLSP` configuration namespace; that name only
/// appears in the capabilities it sends back. Editor settings arrive either as
/// three named `initializationOptions` fields or inside `configuration`, which
/// is the same `.config/shopware/lsp.yaml` shape. Forwarding the wrapper
/// unchanged means every setting is silently discarded.
///
/// Returns the `configuration` object, empty when nothing maps.
fn project_configuration(shopware: &zed::serde_json::Value) -> zed::serde_json::Value {
    let mut config = zed::serde_json::Map::new();

    for key in ["features", "domains"] {
        if let Some(value) = shopware.get(key) {
            config.insert(key.to_string(), value.clone());
        }
    }

    // Nested blocks keep their own key names, minus the parts the editor owns:
    // `mcp.enabled` decides whether Zed registers a context server at all, so
    // the server never sees it.
    for (section, keys) in [
        ("indexing", &["enabled", "maxFileSizeMiB", "exclude"][..]),
        (
            "diagnostics",
            &["enabled", "inspections", "rules", "overrides"][..],
        ),
        ("mcp", &["tools"][..]),
    ] {
        let source = match shopware.get(section) {
            Some(value) => value,
            None => continue,
        };
        let mut target = zed::serde_json::Map::new();
        for key in keys {
            if let Some(value) = source.get(*key) {
                target.insert((*key).to_string(), value.clone());
            }
        }
        if !target.is_empty() {
            config.insert(section.to_string(), zed::serde_json::Value::Object(target));
        }
    }

    zed::serde_json::Value::Object(config)
}

/// The one normalized configuration, in the server's `Partial` shape.
///
/// Every surface has to agree. `didChangeConfiguration` *replaces* the editor
/// overlay wholesale, so a hook that builds a smaller object than initialize
/// did silently drops settings the user still has set. `raw_override` is the
/// user's own `initialization_options.configuration`, merged last so it wins.
fn normalized_configuration(
    shopware: &zed::serde_json::Value,
    raw_override: Option<&zed::serde_json::Value>,
) -> zed::serde_json::Value {
    let mut config = project_configuration(shopware);

    // php.* and shopware.* also exist as named initializationOptions fields,
    // but only initialize reads those, and they belong to the same Partial. Put
    // them in the object every surface shares so an update cannot drop them.
    let mut php = zed::serde_json::Map::new();
    for (from, to) in [
        ("phpExtensions", "extensions"),
        ("disabledPhpExtensions", "disabledExtensions"),
    ] {
        if let Some(zed::serde_json::Value::Array(values)) = shopware.get(from) {
            if !values.is_empty() {
                php.insert(
                    to.to_string(),
                    zed::serde_json::Value::Array(values.clone()),
                );
            }
        }
    }
    if !php.is_empty() {
        merge_json(
            &mut config,
            zed::serde_json::json!({ "php": zed::serde_json::Value::Object(php) }),
        );
    }

    if let Some(zed::serde_json::Value::String(version)) = shopware.get("shopwareTargetVersion") {
        if !version.is_empty() {
            merge_json(
                &mut config,
                zed::serde_json::json!({ "shopware": { "targetVersion": version } }),
            );
        }
    }

    if let Some(extra) = raw_override {
        merge_json(&mut config, extra.clone());
    }

    config
}

/// `GOMEMLIMIT` for `shopwareLSP.memoryLimitMiB`.
///
/// The server honours the variable through `internal/runtimeconfig`, but has no
/// setting for it, so the client has to apply it when spawning. Zero means the
/// server's own balanced policy, so nothing is set.
fn memory_limit_env(shopware: &zed::serde_json::Value) -> Option<String> {
    let mib = shopware.get("memoryLimitMiB")?.as_i64()?;
    (mib > 0).then(|| format!("{mib}MiB"))
}

/// Everything the language-server hook reads from the host.
///
/// Gathering it into a struct is what makes the decision testable: the hook
/// becomes "collect, plan, execute", and only the collecting needs Zed.
#[derive(Default)]
struct LanguageServerFacts {
    configured_binary: Option<String>,
    arguments: Vec<String>,
    shopware: zed::serde_json::Value,
    on_path: Option<String>,
    shell_env: Vec<(String, String)>,
}

/// What the hook should do, with no host calls left in it.
#[derive(Debug, PartialEq)]
struct LanguageServerPlan {
    /// `None` means nothing was found and the caller must download.
    binary: Option<String>,
    args: Vec<String>,
    env: Vec<(String, String)>,
    memory_limit: Option<String>,
}

fn plan_language_server(
    facts: LanguageServerFacts,
    cached_binary: Option<String>,
) -> LanguageServerPlan {
    let memory_limit = memory_limit_env(&facts.shopware);

    let mut env = facts.shell_env;
    if let Some(limit) = memory_limit.clone() {
        env.push(("GOMEMLIMIT".to_string(), limit));
    }

    LanguageServerPlan {
        binary: resolve_server(facts.configured_binary, cached_binary, || facts.on_path),
        args: facts.arguments,
        env,
        memory_limit,
    }
}

/// Everything the MCP hook reads from the host.
#[derive(Default)]
struct ContextServerFacts {
    command_path: Option<String>,
    command_arguments: Option<Vec<String>>,
    command_env: Vec<(String, String)>,
    settings_root: Option<String>,
}

/// Remembered from the language server, because the MCP hook gets neither a
/// `Worktree` nor Zed settings and so cannot work any of it out itself.
#[derive(Default)]
struct CarriedOver {
    binary: Option<String>,
    root: Option<String>,
    configuration: Option<String>,
    memory_limit: Option<String>,
}

#[derive(Debug, PartialEq)]
struct ContextServerPlan {
    binary: Option<String>,
    args: Vec<String>,
    env: Vec<(String, String)>,
}

fn plan_context_server(facts: ContextServerFacts, carried: CarriedOver) -> ContextServerPlan {
    // The MCP process is spawned separately, so editor configuration reaches it
    // only through the environment. Upstream uses the same two variables.
    let mut env: Vec<(String, String)> = Vec::new();
    if let Some(limit) = carried.memory_limit {
        env.push(("GOMEMLIMIT".to_string(), limit));
    }
    if let Some(configuration) = carried.configuration.filter(|value| value != "{}") {
        env.push((
            "SHOPWARE_LSP_EDITOR_CONFIGURATION".to_string(),
            configuration,
        ));
    }
    // An explicit env block wins over both.
    env.extend(facts.command_env);

    if let Some((path, args)) =
        command_override(facts.command_path.clone(), facts.command_arguments)
    {
        return ContextServerPlan {
            binary: Some(path),
            args,
            env,
        };
    }

    ContextServerPlan {
        binary: resolve_server(facts.command_path, carried.binary, || None),
        args: mcp_args(mcp_root(facts.settings_root.as_deref(), carried.root).as_deref()),
        env,
    }
}

/// Where a given release unpacks to, and the binary inside it.
///
/// The directory name carries version and target so several can coexist while
/// `is_superseded_download` prunes the rest, and so a version bump lands in a
/// new directory rather than half-overwriting the old one.
fn download_layout(version: &str, target: &str, os: Os) -> (String, String) {
    let dir = format!("{SERVER_NAME}-{version}-{target}");
    let binary = binary_path_for(&dir, os);
    (dir, binary)
}

/// Project root for the MCP server.
///
/// An explicit `root` setting wins, then whatever the language server saw.
/// `None` leaves the server to use its working directory, which is the only
/// remaining option: `Project` exposes no paths and `zed::Command` has no cwd.
fn mcp_root(configured: Option<&str>, cached: Option<String>) -> Option<String> {
    configured
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_string)
        .or(cached)
}

/// Whether a context-server `command` block replaces discovery outright.
///
/// Only a path *with* arguments is treated as a full override; a bare path
/// still goes through resolution so it keeps the `-root ... mcp` arguments the
/// server needs rather than being spawned bare.
///
/// That only holds when this hook runs at all. Observed on Zed 1.18, any
/// `command` block in `context_servers.shopware-lsp` makes Zed treat the entry
/// as a complete custom-server definition and skip `context_server_command`
/// entirely, so a bare path is spawned bare by Zed and the MCP handshake times
/// out after 30s. The fallback below cannot rescue it, which is why the docs
/// tell users to pass `args` whenever they pass `path`.
fn command_override(
    path: Option<String>,
    arguments: Option<Vec<String>>,
) -> Option<(String, Vec<String>)> {
    match (path, arguments) {
        (Some(path), Some(arguments)) if !path.trim().is_empty() => Some((path, arguments)),
        _ => None,
    }
}

/// Pick a server from the sources both hooks share, in priority order.
///
/// Nothing here is stat'd. The wasm sandbox only preopens the extension work
/// directory, so a path from settings or `Worktree::which` cannot be checked
/// for existence from inside the extension; filtering on that made valid
/// settings look missing. Zed reports a bad path when it fails to spawn.
///
/// `None` means the caller should fall back to a managed download.
fn resolve_server(
    configured: Option<String>,
    cached: Option<String>,
    lookup: impl FnOnce() -> Option<String>,
) -> Option<String> {
    configured
        .filter(|path| !path.trim().is_empty())
        .or(cached)
        .or_else(lookup)
}

/// Whether a managed download is present in the extension's work directory.
///
/// Only the work dir is preopened for the wasm sandbox, so `fs` can answer for
/// downloads and nothing else. A path from settings or `Worktree::which` lives
/// outside it and always reads as missing, which is why neither is stat'd.
fn download_present(path: &str) -> bool {
    !path.trim().is_empty()
        && fs::metadata(path)
            .map(|stat| stat.is_file())
            .unwrap_or(false)
}

/// Whether a work-dir entry is an older managed download.
///
/// Deliberately requires the `shopware-lsp-` prefix rather than `shopware-lsp`,
/// so a binary someone dropped into the work directory is never deleted.
fn is_superseded_download(name: &str, keep: &str) -> bool {
    name.strip_prefix(SERVER_NAME)
        .is_some_and(|rest| rest.starts_with('-'))
        && name != keep
}

impl ShopwareLspExtension {
    /// Ask Open VSX for the newest build for this platform.
    fn latest_release(target: &str) -> Result<(String, String)> {
        let response = HttpRequest::builder()
            .method(HttpMethod::Get)
            .url(format!("{OPEN_VSX_API}/{target}/latest"))
            .header("Accept", "application/json")
            .redirect_policy(RedirectPolicy::FollowAll)
            .build()?
            .fetch()?;

        parse_latest_release(target, &response.body)
    }

    /// Download the server on demand, reusing an existing copy when possible.
    ///
    /// `status_id` is absent when the MCP server triggers the download, because
    /// installation status is a language-server-only concept in Zed.
    fn download_server(&mut self, status_id: Option<&LanguageServerId>) -> Result<String> {
        if let Some(path) = self.cached_download.clone().filter(|p| download_present(p)) {
            return Ok(path);
        }

        if let Some(id) = status_id {
            zed::set_language_server_installation_status(
                id,
                &LanguageServerInstallationStatus::CheckingForUpdate,
            );
        }

        let target = open_vsx_target()?;
        let (version, url) = Self::latest_release(target)?;

        let (version_dir, binary) = download_layout(&version, target, zed::current_platform().0);

        if !download_present(&binary) {
            if let Some(id) = status_id {
                zed::set_language_server_installation_status(
                    id,
                    &LanguageServerInstallationStatus::Downloading,
                );
            }

            zed::download_file(&url, &version_dir, DownloadedFileType::Zip).map_err(|err| {
                format!("failed to download {SERVER_NAME} {version} for {target}: {err}")
            })?;
            zed::make_file_executable(&binary)?;

            Self::remove_other_versions(&version_dir);
        }

        if let Some(id) = status_id {
            zed::set_language_server_installation_status(
                id,
                &LanguageServerInstallationStatus::None,
            );
        }

        self.cached_download = Some(binary.clone());
        Ok(binary)
    }

    /// Each server is roughly 31 MB, so drop superseded downloads.
    fn remove_other_versions(keep: &str) {
        let entries = match fs::read_dir(".") {
            Ok(entries) => entries,
            Err(_) => return,
        };

        for entry in entries.flatten() {
            let name = entry.file_name();
            if is_superseded_download(&name.to_string_lossy(), keep) {
                fs::remove_dir_all(entry.path()).ok();
            }
        }
    }

    /// Build the editor configuration and remember it for the MCP process.
    ///
    /// Both configuration hooks go through here. They previously built the
    /// object separately and only initialize cached it, so after a settings
    /// change the language server saw the new configuration while a later MCP
    /// restart still got the old one, `mcp.tools` included.
    fn remember_configuration(&mut self, worktree: &zed::Worktree) -> zed::serde_json::Value {
        let user = LspSettings::for_worktree(SERVER_NAME, worktree)
            .ok()
            .and_then(|settings| settings.initialization_options);

        let configuration = normalized_configuration(
            &Self::shopware_settings(worktree),
            user.as_ref().and_then(|value| value.get("configuration")),
        );
        self.cached_configuration = zed::serde_json::to_string(&configuration).ok();
        configuration
    }

    /// The `shopwareLSP` block from Zed settings, or an empty object.
    fn shopware_settings(worktree: &zed::Worktree) -> zed::serde_json::Value {
        LspSettings::for_worktree(SERVER_NAME, worktree)
            .ok()
            .and_then(|settings| settings.settings)
            .and_then(|settings| settings.get("shopwareLSP").cloned())
            .unwrap_or_else(|| zed::serde_json::json!({}))
    }
}

impl zed::Extension for ShopwareLspExtension {
    fn new() -> Self {
        Self {
            cached_binary_path: None,
            cached_download: None,
            cached_worktree_root: None,
            cached_configuration: None,
            cached_memory_limit: None,
        }
    }

    fn language_server_command(
        &mut self,
        language_server_id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<zed::Command> {
        self.cached_worktree_root = Some(worktree.root_path());

        let lsp = LspSettings::for_worktree(SERVER_NAME, worktree).ok();
        let binary_settings = lsp.as_ref().and_then(|settings| settings.binary.as_ref());
        let facts = LanguageServerFacts {
            configured_binary: binary_settings.and_then(|binary| binary.path.clone()),
            arguments: binary_settings
                .and_then(|binary| binary.arguments.clone())
                .unwrap_or_default(),
            shopware: Self::shopware_settings(worktree),
            on_path: worktree.which(SERVER_NAME),
            shell_env: worktree.shell_env(),
        };

        let plan = plan_language_server(facts, self.cached_binary_path.clone());
        self.cached_memory_limit = plan.memory_limit;

        let binary = match plan.binary {
            Some(path) => {
                self.cached_binary_path = Some(path.clone());
                path
            }
            None => self.download_server(Some(language_server_id))?,
        };

        Ok(zed::Command {
            command: binary,
            // No subcommand: the binary starts a stdio language server by default.
            args: plan.args,
            env: plan.env,
        })
    }

    /// Answer `didChangeConfiguration` with the shape the server decodes.
    ///
    /// It reads `{"settings": Partial}`, and it *replaces* the editor overlay,
    /// so this has to be the same object initialize sent. Building a smaller
    /// one here drops PHP extensions, the target version and any raw
    /// `configuration` override the moment a setting changes.
    fn language_server_workspace_configuration(
        &mut self,
        _language_server_id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<Option<zed::serde_json::Value>> {
        Ok(Some(self.remember_configuration(worktree)))
    }

    fn language_server_initialization_options(
        &mut self,
        _language_server_id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<Option<zed::serde_json::Value>> {
        let configuration = self.remember_configuration(worktree);
        let user = LspSettings::for_worktree(SERVER_NAME, worktree)
            .ok()
            .and_then(|settings| settings.initialization_options);

        let mut options = default_initialization_options();
        if configuration.as_object().is_some_and(|map| !map.is_empty()) {
            merge_json(
                &mut options,
                zed::serde_json::json!({ "configuration": configuration }),
            );
        }

        // A raw block still wins, for anything the translation does not cover.
        if let Some(user) = user {
            merge_json(&mut options, user);
        }
        Ok(Some(options))
    }

    /// Expose the server's MCP tools to Zed's Agent Panel.
    ///
    /// `shopware-lsp mcp` refuses to start outside a Shopware or Symfony
    /// project, so the root matters. It is taken from the `root` setting first,
    /// then from whatever the language server reported, and otherwise left to
    /// the process working directory.
    fn context_server_command(
        &mut self,
        context_server_id: &ContextServerId,
        project: &Project,
    ) -> Result<zed::Command> {
        let settings = ContextServerSettings::for_project(context_server_id.as_ref(), project).ok();
        let command = settings.as_ref().and_then(|s| s.command.as_ref());

        let facts = ContextServerFacts {
            command_path: command.and_then(|c| c.path.clone()),
            command_arguments: command.and_then(|c| c.arguments.clone()),
            command_env: command
                .and_then(|c| c.env.clone())
                .map(|values| values.into_iter().collect())
                .unwrap_or_default(),
            settings_root: settings
                .as_ref()
                .and_then(|s| s.settings.as_ref())
                .and_then(|s| s["root"].as_str())
                .map(str::to_string),
        };

        let plan = plan_context_server(
            facts,
            CarriedOver {
                binary: self.cached_binary_path.clone(),
                root: self.cached_worktree_root.clone(),
                configuration: self.cached_configuration.clone(),
                memory_limit: self.cached_memory_limit.clone(),
            },
        );

        let binary = match plan.binary {
            Some(path) => {
                self.cached_binary_path = Some(path.clone());
                path
            }
            None => self.download_server(None)?,
        };

        Ok(zed::Command {
            command: binary,
            args: plan.args,
            env: plan.env,
        })
    }

    fn context_server_configuration(
        &mut self,
        _context_server_id: &ContextServerId,
        _project: &Project,
    ) -> Result<Option<ContextServerConfiguration>> {
        Ok(Some(ContextServerConfiguration {
            installation_instructions: include_str!("../docs/mcp-instructions.md").to_string(),
            settings_schema: include_str!("../docs/mcp-settings-schema.json").to_string(),
            default_settings: "{}\n".to_string(),
        }))
    }
}

zed::register_extension!(ShopwareLspExtension);

#[cfg(test)]
mod tests {
    use super::*;

    /// Fixture trimmed from a real https://open-vsx.org/api/.../latest response.
    const LATEST_JSON: &[u8] = br#"{
        "namespace": "shopware",
        "name": "shopware-lsp",
        "version": "0.3.52",
        "targetPlatform": "darwin-arm64",
        "files": {
            "download": "https://open-vsx.org/api/shopware/shopware-lsp/darwin-arm64/0.3.52/file/shopware.shopware-lsp-0.3.52@darwin-arm64.vsix",
            "manifest": "https://open-vsx.org/api/shopware/shopware-lsp/darwin-arm64/0.3.52/file/package.json"
        }
    }"#;

    #[test]
    fn maps_every_published_platform_to_its_open_vsx_target() {
        // These strings are a wire contract with Open VSX. A typo here is a
        // download that 404s on someone else's machine.
        assert_eq!(
            target_for(Os::Mac, Architecture::Aarch64),
            Ok("darwin-arm64")
        );
        assert_eq!(target_for(Os::Mac, Architecture::X8664), Ok("darwin-x64"));
        assert_eq!(
            target_for(Os::Linux, Architecture::Aarch64),
            Ok("linux-arm64")
        );
        assert_eq!(target_for(Os::Linux, Architecture::X8664), Ok("linux-x64"));
        assert_eq!(
            target_for(Os::Windows, Architecture::X8664),
            Ok("win32-x64")
        );
    }

    #[test]
    fn rejects_platforms_without_a_published_build() {
        // No 32-bit builds exist, and Windows on ARM is not published either.
        for (os, arch) in [
            (Os::Mac, Architecture::X86),
            (Os::Linux, Architecture::X86),
            (Os::Windows, Architecture::X86),
            (Os::Windows, Architecture::Aarch64),
        ] {
            let error = target_for(os, arch).expect_err("must not invent a target");
            assert!(
                error.contains("binary.path"),
                "the error has to name the escape hatch, got: {error}"
            );
        }
    }

    #[test]
    fn locates_the_binary_inside_the_vsix() {
        // The vsix puts everything under extension/; the archive root only
        // holds extension.vsixmanifest and [Content_Types].xml.
        assert_eq!(
            binary_path_for("shopware-lsp-0.3.52-darwin-arm64", Os::Mac),
            "shopware-lsp-0.3.52-darwin-arm64/extension/shopware-lsp"
        );
        assert_eq!(
            binary_path_for("dir", Os::Linux),
            "dir/extension/shopware-lsp"
        );
        assert_eq!(
            binary_path_for("dir", Os::Windows),
            "dir/extension/shopware-lsp.exe"
        );
    }

    #[test]
    fn reads_version_and_download_url_from_open_vsx() {
        let (version, url) = parse_latest_release("darwin-arm64", LATEST_JSON).unwrap();
        assert_eq!(version, "0.3.52");
        assert!(url.ends_with("@darwin-arm64.vsix"), "got {url}");
    }

    #[test]
    fn reports_which_part_of_the_open_vsx_payload_is_missing() {
        let err = parse_latest_release("darwin-arm64", b"not json").unwrap_err();
        assert!(err.contains("malformed JSON"), "got {err}");

        let err = parse_latest_release("darwin-arm64", br#"{"files":{"download":"u"}}"#)
            .expect_err("a payload without a version must fail");
        assert!(err.contains("version"), "got {err}");

        let err = parse_latest_release("linux-x64", br#"{"version":"1.0.0","files":{}}"#)
            .expect_err("a payload without a download must fail");
        assert!(
            err.contains("linux-x64"),
            "the error has to name the target, got: {err}"
        );
    }

    #[test]
    fn passes_the_root_before_the_mcp_subcommand() {
        // `mcp -root x` is rejected by the binary: "mcp takes no arguments;
        // use -root before the command".
        assert_eq!(
            mcp_args(Some("/srv/shop")),
            vec!["-root", "/srv/shop", "mcp"]
        );
    }

    #[test]
    fn falls_back_to_the_working_directory_without_a_known_root() {
        assert_eq!(mcp_args(None), vec!["mcp"]);
    }

    #[test]
    fn declares_only_the_code_lens_command() {
        // The allow-list is per command name. openReferences is in so the code
        // lenses render; every generator command stays out so its dead menu
        // entry never appears.
        let options = default_initialization_options();
        let client = &options["shopwareClient"];
        assert_eq!(client["protocolVersion"], 1);
        assert_eq!(client["presentationProfile"], "full");

        let commands: Vec<&str> = client["supportedCommands"]
            .as_array()
            .unwrap()
            .iter()
            .map(|value| value.as_str().unwrap())
            .collect();
        assert_eq!(commands, ["shopware.openReferences"]);

        for generator in [
            "shopware.insertSnippet",
            "shopware.twig.extendBlock",
            "shopware.symfony.generateService",
            "shopware.admin.extendComponent",
        ] {
            assert!(
                !commands.contains(&generator),
                "{generator} would resurrect a menu entry that cannot work"
            );
        }
    }

    #[test]
    fn user_initialization_options_override_the_defaults() {
        let mut options = default_initialization_options();
        merge_json(
            &mut options,
            zed::serde_json::json!({
                "shopwareClient": {"supportedCommands": ["shopware.openReferences"]},
                "allowUnsupportedProject": true,
            }),
        );

        // Overridden leaf.
        assert_eq!(
            options["shopwareClient"]["supportedCommands"][0],
            "shopware.openReferences"
        );
        // Sibling keys inside the same object survive the merge.
        assert_eq!(options["shopwareClient"]["protocolVersion"], 1);
        assert_eq!(options["shopwareClient"]["presentationProfile"], "full");
        // New top-level keys are added.
        assert_eq!(options["allowUnsupportedProject"], true);
    }

    #[test]
    fn resolution_order_prefers_what_the_user_controls() {
        let found = || Some("/from/lookup/shopware-lsp".to_string());

        // An explicit setting wins, and is never stat'd: the sandbox preopens
        // only the work dir, so checking it would reject every valid path.
        assert_eq!(
            resolve_server(Some("/configured".into()), Some("/cached".into()), found),
            Some("/configured".to_string())
        );
        // Then whatever the language server already resolved.
        assert_eq!(
            resolve_server(None, Some("/cached".into()), found),
            Some("/cached".to_string())
        );
        // Then the lookup, which is Worktree::which and only the LSP hook has it.
        assert_eq!(
            resolve_server(None, None, found),
            Some("/from/lookup/shopware-lsp".to_string())
        );
        // The MCP hook passes no lookup, so it lands on a managed download.
        assert_eq!(resolve_server(None, None, || None), None);
        // A blank setting must not shadow the rest.
        assert_eq!(
            resolve_server(Some("   ".into()), Some("/cached".into()), || None),
            Some("/cached".to_string())
        );
    }

    #[test]
    fn translates_editor_settings_into_the_server_shape() {
        let settings = zed::serde_json::json!({
            "phpExtensions": ["redis", "imagick"],
            "disabledPhpExtensions": ["xdebug"],
            "shopwareTargetVersion": "6.8",
            "features": {"semanticTokens": false},
            "domains": {"twig": true},
            "indexing": {"enabled": true, "maxFileSizeMiB": 4, "exclude": ["var/**"]},
            "diagnostics": {"enabled": true, "rules": {"php.version": "off"}},
            "mcp": {"enabled": false, "tools": {"shopware_hover": false}},
            // Editor-only: the server has no setting for any of these.
            "activationMode": "auto",
            "memoryLimitMiB": 512,
            "phpExecutable": "php",
            "serverPath": "/somewhere/shopware-lsp",
        });

        let config = normalized_configuration(&settings, None);

        assert_eq!(config["php"]["extensions"][0], "redis");
        assert_eq!(config["php"]["disabledExtensions"][0], "xdebug");
        assert_eq!(config["shopware"]["targetVersion"], "6.8");
        assert_eq!(config["features"]["semanticTokens"], false);
        assert_eq!(config["domains"]["twig"], true);
        assert_eq!(config["indexing"]["maxFileSizeMiB"], 4);
        assert_eq!(config["indexing"]["exclude"][0], "var/**");
        assert_eq!(config["diagnostics"]["rules"]["php.version"], "off");
        assert_eq!(config["mcp"]["tools"]["shopware_hover"], false);

        // mcp.enabled decides whether Zed registers a context server, so the
        // server must never see it; the rest are client concerns too. The
        // server decodes this with DisallowUnknownFields, so a stray key is
        // not merely ignored, it fails the whole payload.
        assert!(config["mcp"].get("enabled").is_none());
        for key in [
            "activationMode",
            "memoryLimitMiB",
            "phpExecutable",
            "serverPath",
            "shopwareLSP",
            "phpExtensions",
        ] {
            assert!(config.get(key).is_none(), "{key} leaked into configuration");
        }
    }

    #[test]
    fn the_partial_builder_is_not_interchangeable_with_the_section_builder() {
        // didChangeConfiguration replaces the editor overlay rather than
        // merging it, so both hooks must build the *same* object. They are two
        // call sites in the untestable Extension impl, so what is pinned here
        // is the thing that makes swapping them wrong: project_configuration
        // alone loses php, shopware and any raw override.
        let settings = zed::serde_json::json!({
            "phpExtensions": ["redis"],
            "shopwareTargetVersion": "6.8",
            "features": {"hover": true},
        });
        let raw = zed::serde_json::json!({"check": {"failOn": "error"}});

        let full = normalized_configuration(&settings, Some(&raw));
        let sections_only = project_configuration(&settings);
        assert_ne!(
            full, sections_only,
            "if these ever match, the hooks could use either and the \
             difference this test guards has gone"
        );

        assert_eq!(full["php"]["extensions"][0], "redis");
        assert_eq!(full["shopware"]["targetVersion"], "6.8");
        assert_eq!(full["check"]["failOn"], "error");
        assert!(sections_only.get("php").is_none());
        assert!(sections_only.get("shopware").is_none());
        assert!(sections_only.get("check").is_none());

        // The shared section survives either way, so the loss is silent.
        assert_eq!(full["features"], sections_only["features"]);
    }

    #[test]
    fn empty_settings_produce_no_configuration_noise() {
        let empty = zed::serde_json::json!({});
        assert_eq!(project_configuration(&empty), zed::serde_json::json!({}));
        assert_eq!(
            normalized_configuration(&empty, None),
            zed::serde_json::json!({})
        );

        // Blank values must not be forwarded as real settings.
        let blank = zed::serde_json::json!({
            "shopwareTargetVersion": "",
            "phpExtensions": [],
        });
        assert_eq!(
            normalized_configuration(&blank, None),
            zed::serde_json::json!({})
        );
    }

    #[test]
    fn memory_limit_becomes_a_go_memory_limit() {
        assert_eq!(
            memory_limit_env(&zed::serde_json::json!({"memoryLimitMiB": 512})),
            Some("512MiB".to_string())
        );
        // Zero is documented as "use the server's balanced policy".
        assert_eq!(
            memory_limit_env(&zed::serde_json::json!({"memoryLimitMiB": 0})),
            None
        );
        assert_eq!(memory_limit_env(&zed::serde_json::json!({})), None);
    }

    #[test]
    fn language_server_plan_covers_the_whole_hook() {
        let facts = LanguageServerFacts {
            configured_binary: None,
            arguments: vec!["-rpc.trace".into()],
            shopware: zed::serde_json::json!({"memoryLimitMiB": 512}),
            on_path: Some("/usr/local/bin/shopware-lsp".into()),
            shell_env: vec![("PATH".into(), "/usr/bin".into())],
        };
        let plan = plan_language_server(facts, None);

        assert_eq!(plan.binary.as_deref(), Some("/usr/local/bin/shopware-lsp"));
        assert_eq!(plan.args, vec!["-rpc.trace".to_string()]);
        assert_eq!(plan.memory_limit.as_deref(), Some("512MiB"));
        // The shell environment is preserved, with the limit appended.
        assert_eq!(plan.env[0], ("PATH".to_string(), "/usr/bin".to_string()));
        assert_eq!(
            plan.env[1],
            ("GOMEMLIMIT".to_string(), "512MiB".to_string())
        );
    }

    #[test]
    fn language_server_plan_asks_for_a_download_when_nothing_is_found() {
        let plan = plan_language_server(LanguageServerFacts::default(), None);
        assert_eq!(plan.binary, None, "None is the signal to download");
        assert!(plan.env.is_empty());
        assert_eq!(plan.memory_limit, None);
    }

    #[test]
    fn context_server_plan_carries_editor_settings_across() {
        // The MCP process reads neither Zed settings nor the initialize
        // payload, so this environment is the only way those reach it.
        let plan = plan_context_server(
            ContextServerFacts::default(),
            CarriedOver {
                binary: Some("/usr/local/bin/shopware-lsp".into()),
                root: Some("/srv/shop".into()),
                configuration: Some(r#"{"features":{"hover":true}}"#.into()),
                memory_limit: Some("512MiB".into()),
            },
        );

        assert_eq!(plan.binary.as_deref(), Some("/usr/local/bin/shopware-lsp"));
        assert_eq!(plan.args, vec!["-root", "/srv/shop", "mcp"]);
        assert_eq!(
            plan.env,
            vec![
                ("GOMEMLIMIT".to_string(), "512MiB".to_string()),
                (
                    "SHOPWARE_LSP_EDITOR_CONFIGURATION".to_string(),
                    r#"{"features":{"hover":true}}"#.to_string()
                ),
            ]
        );
    }

    #[test]
    fn the_configuration_the_editor_gets_is_the_one_the_agent_gets() {
        // remember_configuration serializes whatever it returns, and the MCP
        // plan forwards that string. Pinning the join means a settings change
        // reaching the language server also reaches the Agent Panel, which it
        // did not while only the initialize hook refreshed the cache.
        let settings = zed::serde_json::json!({
            "mcp": {"tools": {"shopware_hover": false}},
            "features": {"hover": true},
        });

        let configuration = normalized_configuration(&settings, None);
        let cached = zed::serde_json::to_string(&configuration).unwrap();

        let plan = plan_context_server(
            ContextServerFacts::default(),
            CarriedOver {
                configuration: Some(cached.clone()),
                ..Default::default()
            },
        );

        let forwarded = plan
            .env
            .iter()
            .find(|(key, _)| key == "SHOPWARE_LSP_EDITOR_CONFIGURATION")
            .map(|(_, value)| value.clone())
            .expect("the agent must receive the editor configuration");

        // Same bytes, and still the shape the server decodes strictly.
        assert_eq!(forwarded, cached);
        let parsed: zed::serde_json::Value = zed::serde_json::from_str(&forwarded).unwrap();
        assert_eq!(parsed["mcp"]["tools"]["shopware_hover"], false);
        assert_eq!(parsed, configuration);
    }

    #[test]
    fn context_server_plan_skips_an_empty_configuration() {
        let plan = plan_context_server(
            ContextServerFacts::default(),
            CarriedOver {
                configuration: Some("{}".into()),
                ..Default::default()
            },
        );
        // An empty object is noise, and the server decodes this strictly.
        assert!(plan.env.is_empty());
        assert_eq!(plan.binary, None);
        assert_eq!(plan.args, vec!["mcp".to_string()]);
    }

    #[test]
    fn context_server_plan_lets_an_explicit_command_win() {
        let plan = plan_context_server(
            ContextServerFacts {
                command_path: Some("/opt/sw".into()),
                command_arguments: Some(vec!["mcp".into()]),
                command_env: vec![("GOMEMLIMIT".into(), "256MiB".into())],
                settings_root: Some("/ignored".into()),
            },
            CarriedOver {
                memory_limit: Some("512MiB".into()),
                ..Default::default()
            },
        );

        assert_eq!(plan.binary.as_deref(), Some("/opt/sw"));
        assert_eq!(plan.args, vec!["mcp".to_string()]);
        // The explicit env comes last, so it overrides the carried value.
        assert_eq!(plan.env.last().unwrap().1, "256MiB");
    }

    #[test]
    fn download_layout_keeps_versions_apart() {
        let (dir, binary) = download_layout("0.3.53", "darwin-arm64", Os::Mac);
        assert_eq!(dir, "shopware-lsp-0.3.53-darwin-arm64");
        assert_eq!(
            binary,
            "shopware-lsp-0.3.53-darwin-arm64/extension/shopware-lsp"
        );

        // A different version must not reuse the directory, or an upgrade
        // half-overwrites the old one.
        let (other, _) = download_layout("0.3.54", "darwin-arm64", Os::Mac);
        assert_ne!(dir, other);
        // And the pruning predicate has to recognise the one we keep.
        assert!(is_superseded_download(&other, &dir));
        assert!(!is_superseded_download(&dir, &dir));

        let (_, exe) = download_layout("0.3.53", "win32-x64", Os::Windows);
        assert!(exe.ends_with("shopware-lsp.exe"));
    }

    #[test]
    fn mcp_root_prefers_the_explicit_setting() {
        assert_eq!(
            mcp_root(Some("/srv/shop"), Some("/from/lsp".into())),
            Some("/srv/shop".to_string())
        );
        // Falls back to whatever the language server reported.
        assert_eq!(
            mcp_root(None, Some("/from/lsp".into())),
            Some("/from/lsp".to_string())
        );
        // Blank is not a root; without one the server uses its working dir.
        assert_eq!(
            mcp_root(Some("   "), Some("/from/lsp".into())),
            Some("/from/lsp".to_string())
        );
        assert_eq!(mcp_root(None, None), None);
    }

    #[test]
    fn only_a_path_with_arguments_replaces_discovery() {
        assert_eq!(
            command_override(Some("/bin/sw".into()), Some(vec!["mcp".into()])),
            Some(("/bin/sw".to_string(), vec!["mcp".to_string()]))
        );
        // A bare path must still go through resolution, so it keeps the
        // "-root <root> mcp" arguments instead of being spawned bare.
        assert_eq!(command_override(Some("/bin/sw".into()), None), None);
        assert_eq!(command_override(None, Some(vec!["mcp".into()])), None);
        assert_eq!(command_override(Some("  ".into()), Some(vec![])), None);
    }

    #[test]
    fn a_download_never_outranks_an_installed_binary() {
        // The two caches are separate so that whichever hook runs first cannot
        // pin the other to a download. Only authoritative paths, settings or
        // Worktree::which, reach resolve_server as `cached`.
        let which = || Some("/usr/local/bin/shopware-lsp".to_string());

        // MCP downloading first must not stop the language server using PATH.
        assert_eq!(
            resolve_server(None, None, which),
            Some("/usr/local/bin/shopware-lsp".to_string())
        );
        // An authoritative path, once known, is shared with the MCP hook.
        assert_eq!(
            resolve_server(None, Some("/usr/local/bin/shopware-lsp".into()), || None),
            Some("/usr/local/bin/shopware-lsp".to_string())
        );
    }

    #[test]
    fn download_presence_only_speaks_for_the_work_dir() {
        assert!(!download_present(""));
        assert!(!download_present("   "));
        assert!(!download_present("/nonexistent/shopware-lsp"));
        // A directory is not a server.
        assert!(!download_present("/tmp"));
        assert!(download_present(
            std::env::current_exe().unwrap().to_str().unwrap()
        ));
    }

    #[test]
    fn prunes_only_older_managed_downloads() {
        let keep = "shopware-lsp-0.3.52-darwin-arm64";

        assert!(is_superseded_download(
            "shopware-lsp-0.3.50-darwin-arm64",
            keep
        ));
        assert!(is_superseded_download(
            "shopware-lsp-0.3.52-linux-x64",
            keep
        ));

        assert!(
            !is_superseded_download(keep, keep),
            "must keep the current version"
        );
        // A binary dropped into the work dir by hand, and unrelated neighbours.
        assert!(!is_superseded_download("shopware-lsp", keep));
        assert!(!is_superseded_download("shopware-lsp.exe", keep));
        assert!(!is_superseded_download("some-other-server-1.0", keep));
    }
}
