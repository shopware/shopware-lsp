# Installation

> [!WARNING]
> Shopware Language Server is experimental. Features and editor integrations
> may change, and you may encounter bugs or incomplete results.

Choose your editor below. After opening a Shopware or Symfony project, allow
its initial workspace index to complete before trying completion and navigation.

## PhpStorm

The experimental LSP integration is available through the Shopware plugin's
EAP channel. You must add its custom plugin repository manually:

1. Open **Settings → Plugins** in PhpStorm.
2. Click the gear icon and select **Manage Plugin Repositories**.
3. Add the following repository URL:

   ```text
   https://plugins.jetbrains.com/plugins/eap/17632
   ```

4. Apply the repository settings, then install or update the **Shopware**
   plugin from the Plugins page.
5. Restart PhpStorm if prompted and open your Shopware project.

With the EAP plugin installed, Shopware-specific IntelliSense/autocomplete is
powered by the LSP server. PhpStorm continues to provide its native language
support; the LSP adds framework-aware intelligence.

This is an experimental preview channel, not the stable plugin channel. The
[PhpStorm integration guide](phpstorm-integration.md) describes the technical
contract for editor integrators; it is not required for installation.

## VS Code

Shopware Language Server requires VS Code 1.101 or newer and the extension's
**pre-release version**, not the stable release.

1. Open **Shopware Language Server** on the
   [Visual Studio Marketplace](https://marketplace.visualstudio.com/items?itemName=shopware.shopware-lsp)
   in VS Code.
2. Select **Install Pre-Release Version**. If the extension is already installed,
   select **Switch to Pre-Release Version** on its extension page.
3. Open a Shopware or Symfony workspace folder.
4. Start editing. The platform-specific extension package includes the server
   executable and starts it automatically for supported projects.

VSCodium and other Open VSX clients can install the pre-release version from
[Open VSX](https://open-vsx.org/extension/shopware/shopware-lsp).
If your client cannot select a pre-release, install a preview VSIX as described
below.

The default activation mode is conservative. The extension starts only when it
recognizes Shopware Composer metadata, a Shopware app manifest, Symfony's
`config/bundles.php`, or an explicit `.config/shopware/lsp.yaml` file. It does
not index unrelated PHP projects.

### Preview builds

The pre-release version is required for the experimental LSP. As an alternative
to selecting it in your editor's extension marketplace, install a preview VSIX
manually:

Odd-minor tags (for example `0.3.0`) run the
[Pre-release workflow](https://github.com/shopware/shopware-lsp/actions/workflows/pre-release.yml),
which builds platform-specific pre-release VSIX packages for macOS, Linux,
Alpine, and Windows and attaches them to a GitHub pre-release. The marketplaces
only accept numeric `major.minor.patch` versions, so the extension follows the
VS Code convention: odd minors are pre-releases, even minors are stable.

1. Open the latest pre-release on
   [GitHub Releases](https://github.com/shopware/shopware-lsp/releases).
2. Download the VSIX for your operating system and architecture.
3. Run **Extensions: Install from VSIX...** in VS Code.

After approval through the gated `preview` deployment environment, pre-release
packages are also published to the pre-release channel of the Marketplace and
Open VSX. A stable release channel does not mean the experimental language
server is production-ready.

## Zed

The [Zed extension](../editors/zed/README.md) provides language-server support,
MCP tools for the Agent Panel, snippets, and optional generator tasks. Install
it as a development extension by selecting `editors/zed` in Zed's **Install
Dev Extension** action. Rust via rustup is required to compile the extension;
the server binary is downloaded automatically.

## Other editors and standalone use

Download a server archive from
[GitHub Releases](https://github.com/shopware/shopware-lsp/releases), place the
`shopware-lsp` executable on your `PATH`, and configure your editor to start it
over stdin/stdout. Running the binary without a subcommand starts the language
server.

See [custom commands](custom-commands.md) for custom protocol commands and a
Neovim example.
