//! Client integration contract (`initializationOptions.shopwareClient`) and
//! command filtering for clients that announce the commands they execute.

use serde_json::{Map, Value, json};
use swlsp_lsp_protocol as protocol;

use crate::inspection::{InspectionDefinition, InspectionScope};
use crate::server::{ClientState, Server, read, write};

/// The client contract version this server speaks.
pub const CLIENT_PROTOCOL_VERSION: i32 = swlsp_integration::PROTOCOL_VERSION;

/// How much generic presentation a host IDE wants from the server.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PresentationProfile {
    Full,
    /// Withholds generic PHP presentation; the host IDE provides it.
    Framework,
}

impl PresentationProfile {
    pub fn as_str(self) -> &'static str {
        match self {
            PresentationProfile::Full => "full",
            PresentationProfile::Framework => "framework",
        }
    }
}

impl Server {
    pub(crate) fn configure_client_integration(
        &self,
        options: Option<&protocol::ShopwareClientOptions>,
    ) -> Result<(), String> {
        let mut state = ClientState {
            protocol_version: CLIENT_PROTOCOL_VERSION,
            presentation_profile: PresentationProfile::Full.as_str().to_owned(),
            ..ClientState::default()
        };
        let result = (|| {
            let Some(options) = options else {
                return Ok(());
            };
            if options.protocol_version != CLIENT_PROTOCOL_VERSION {
                return Err(format!(
                    "unsupported Shopware client protocol version {}; server supports {}",
                    options.protocol_version, CLIENT_PROTOCOL_VERSION
                ));
            }
            let profile = match options.presentation_profile.trim() {
                "" | "full" => PresentationProfile::Full,
                "framework" => PresentationProfile::Framework,
                other => {
                    return Err(format!(
                        "unsupported Shopware presentation profile {}",
                        swlsp_uriutil::go_quote(other)
                    ));
                }
            };
            state.presentation_profile = profile.as_str().to_owned();
            state.filter_commands = true;
            for command in &options.supported_commands {
                let command = command.trim();
                if !command.is_empty() {
                    state.supported_commands.insert(command.to_owned());
                }
            }
            Ok(())
        })();
        // Like Go, a rejected contract keeps the fields written so far.
        write(&self.session).client = state;
        result
    }

    pub fn presentation_profile(&self) -> PresentationProfile {
        match read(&self.session).client.presentation_profile.as_str() {
            "framework" => PresentationProfile::Framework,
            _ => PresentationProfile::Full,
        }
    }

    pub fn framework_presentation(&self) -> bool {
        self.presentation_profile() == PresentationProfile::Framework
    }

    pub(crate) fn supports_client_command(&self, command: &str) -> bool {
        let session = read(&self.session);
        !session.client.filter_commands
            || command.is_empty()
            || session.client.supported_commands.contains(command)
    }

    fn filters_client_commands(&self) -> bool {
        read(&self.session).client.filter_commands
    }

    /// Clients with a framework presentation profile report core-language
    /// problems themselves.
    pub(crate) fn inspection_presented_to_client(&self, definition: &InspectionDefinition) -> bool {
        definition.scope == InspectionScope::Framework || !self.framework_presentation()
    }

    pub(crate) fn negotiated_client_state(&self, active: bool, reason: &str) -> Value {
        let session = read(&self.session);
        let profile = match session.client.presentation_profile.as_str() {
            "" => "full",
            profile => profile,
        };
        let mut state = Map::new();
        state.insert("active".into(), json!(active));
        state.insert(
            "protocolVersion".into(),
            json!(session.client.protocol_version),
        );
        state.insert("presentationProfile".into(), json!(profile));
        if !reason.is_empty() {
            state.insert("reason".into(), json!(reason));
        }
        if session.client.filter_commands {
            state.insert(
                "supportedCommands".into(),
                json!(session.client.supported_commands.iter().collect::<Vec<_>>()),
            );
        }
        Value::Object(state)
    }

    pub(crate) fn filter_code_actions_for_client(
        &self,
        actions: Vec<protocol::CodeAction>,
    ) -> Vec<protocol::CodeAction> {
        if !self.filters_client_commands() {
            return actions;
        }
        actions
            .into_iter()
            .filter_map(|mut action| {
                let supported = action
                    .command
                    .as_ref()
                    .is_none_or(|command| self.supports_client_command(&command.command));
                if supported {
                    return Some(action);
                }
                action.edit.is_some().then(|| {
                    action.command = None;
                    action
                })
            })
            .collect()
    }

    pub(crate) fn filter_resolved_code_action_for_client(
        &self,
        mut action: protocol::CodeAction,
    ) -> protocol::CodeAction {
        let supported = action
            .command
            .as_ref()
            .is_none_or(|command| self.supports_client_command(&command.command));
        if supported {
            return action;
        }
        action.command = None;
        if action.edit.is_none() {
            action.disabled = Some(protocol::CodeActionDisabled {
                reason: "The client does not support the command required by this action".into(),
            });
        }
        action
    }

    pub(crate) fn filter_code_lenses_for_client(
        &self,
        lenses: Vec<protocol::CodeLens>,
    ) -> Vec<protocol::CodeLens> {
        if !self.filters_client_commands() {
            return lenses;
        }
        lenses
            .into_iter()
            .filter(|lens| {
                lens.command
                    .as_ref()
                    .is_none_or(|command| self.supports_client_command(&command.command))
            })
            .collect()
    }

    pub(crate) fn filter_completion_commands_for_client(
        &self,
        items: &mut [protocol::CompletionItem],
    ) {
        if !self.filters_client_commands() {
            return;
        }
        for item in items {
            let unsupported = item
                .command
                .as_ref()
                .and_then(|command| command.get("command"))
                .and_then(Value::as_str)
                .is_some_and(|command| !self.supports_client_command(command));
            if unsupported {
                item.command = None;
            }
        }
    }

    pub(crate) fn filter_inlay_hint_commands_for_client(&self, hints: &mut [protocol::InlayHint]) {
        if !self.filters_client_commands() {
            return;
        }
        for hint in hints {
            let Value::Array(parts) = &mut hint.label else {
                continue;
            };
            for part in parts {
                let unsupported = part
                    .get("command")
                    .and_then(|command| command.get("command"))
                    .and_then(Value::as_str)
                    .is_some_and(|command| !self.supports_client_command(command));
                if unsupported && let Value::Object(part) = part {
                    part.remove("command");
                }
            }
        }
    }
}
