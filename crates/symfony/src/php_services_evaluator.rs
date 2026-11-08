//! Statement evaluation of the PHP service configurator.

use swlsp_parser_php::query as phpquery;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use crate::model::Service;
use crate::php_services::{PhpServiceEvaluator, method_calls, php_argument_content_range};

impl PhpServiceEvaluator<'_> {
    pub(crate) fn evaluate_statement(&mut self, statement: &SyntaxNode) {
        let calls = phpquery::calls(statement, &[]);
        if calls.is_empty() {
            return;
        }
        let assigned_variable = phpquery::assigned_variable(statement);
        if self.capture_accessor_assignment(&calls, &assigned_variable)
            || self.evaluate_standalone_configurator(&calls)
        {
            return;
        }
        let definition_id = self.resolve_definition(&calls);
        if definition_id.is_empty() {
            return;
        }
        self.configure_definition(&definition_id, &assigned_variable, &calls);
    }

    fn capture_accessor_assignment(
        &mut self,
        calls: &[SyntaxNode],
        assigned_variable: &str,
    ) -> bool {
        if self.is_accessor_assignment(calls, "services") {
            if !assigned_variable.is_empty()
                && !self
                    .service_variables
                    .iter()
                    .any(|v| v == assigned_variable)
            {
                self.service_variables.push(assigned_variable.to_owned());
            }
            return true;
        }
        if !self.is_accessor_assignment(calls, "parameters") {
            return false;
        }
        if !assigned_variable.is_empty()
            && !self
                .parameter_variables
                .iter()
                .any(|v| v == assigned_variable)
        {
            self.parameter_variables.push(assigned_variable.to_owned());
        }
        true
    }

    fn evaluate_standalone_configurator(&mut self, calls: &[SyntaxNode]) -> bool {
        if let Some(set) = self.rooted_call(calls, &self.parameter_variables, "parameters", "set") {
            self.set_parameter(&set);
            return true;
        }
        if let Some(remove) = self.rooted_call(calls, &self.service_variables, "services", "remove")
        {
            self.remove_service(&remove);
            return true;
        }
        if self
            .rooted_call(calls, &self.service_variables, "services", "defaults")
            .is_some()
        {
            self.configure_defaults(calls);
            return true;
        }
        if let Some(instance_of) =
            self.rooted_call(calls, &self.service_variables, "services", "instanceof")
        {
            self.configure_instanceof(&instance_of, calls);
            return true;
        }
        if let Some(load) = self.rooted_call(calls, &self.service_variables, "services", "load") {
            self.set_prototype(&load, calls);
            return true;
        }
        false
    }

    fn remove_service(&mut self, call: &SyntaxNode) {
        let Some(id) = self.argument_value(call, 0) else {
            return;
        };
        self.services.remove(&id);
        self.definition_vars
            .retain(|(_, definition_id)| *definition_id != id);
    }

    fn configure_defaults(&mut self, calls: &[SyntaxNode]) {
        for call in method_calls(calls, "autowire") {
            if self.call_has_configurator(&phpquery::call_name(&call), "defaults") {
                (self.default_autowire, self.default_autowire_set) = self.php_autowire_call(&call);
            }
        }
        for call in method_calls(calls, "tag") {
            if !self.call_has_configurator(&phpquery::call_name(&call), "defaults") {
                continue;
            }
            if let Some(tag) = self.argument_value(&call, 0) {
                self.default_tags.insert(tag, String::new());
            }
        }
    }

    fn configure_instanceof(&mut self, instance_of: &SyntaxNode, calls: &[SyntaxNode]) {
        let Some(type_name) = self.argument_value(instance_of, 0) else {
            return;
        };
        for call in method_calls(calls, "tag") {
            if !self.call_has_configurator(&phpquery::call_name(&call), "instanceof") {
                continue;
            }
            if let Some(tag) = self.argument_value(&call, 0) {
                self.instanceof_tags.insert(tag, type_name.clone());
            }
        }
    }

    fn resolve_definition(&mut self, calls: &[SyntaxNode]) -> String {
        if let Some(call) = self.rooted_call(calls, &self.service_variables, "services", "set") {
            return self.set_service(&call);
        }
        if let Some(call) = self.invoked_services_call(calls) {
            return self.set_service(&call);
        }
        if let Some(call) = self.rooted_call(calls, &self.service_variables, "services", "alias") {
            return self.set_alias(&call);
        }
        if let Some(call) = self.rooted_call(calls, &self.service_variables, "services", "stack") {
            return self.set_stack(&call);
        }
        if let Some(call) = self.rooted_call(calls, &self.service_variables, "services", "get") {
            return self.argument_value(&call, 0).unwrap_or_default();
        }
        self.definition_for_calls(calls)
    }

    fn configure_definition(
        &mut self,
        definition_id: &str,
        assigned_variable: &str,
        calls: &[SyntaxNode],
    ) {
        if !assigned_variable.is_empty() {
            match self
                .definition_vars
                .iter_mut()
                .find(|(variable, _)| variable == assigned_variable)
            {
                Some(entry) => entry.1 = definition_id.to_owned(),
                None => self
                    .definition_vars
                    .push((assigned_variable.to_owned(), definition_id.to_owned())),
            }
        }
        let Some(mut service) = self.services.get(definition_id).cloned() else {
            return;
        };
        self.configure_service(&mut service, definition_id, calls);
        if self.collect_references {
            let references = self.service_argument_references(&service, calls);
            self.references.extend(references);
            let method_references = self.service_method_references(&service, calls);
            self.method_references.extend(method_references);
        }
        self.services.insert(definition_id.to_owned(), service);
    }

    fn configure_service(&self, service: &mut Service, definition_id: &str, calls: &[SyntaxNode]) {
        let targets = |call: &SyntaxNode| self.call_targets_definition(call, definition_id);
        for call in method_calls(calls, "class") {
            if targets(&call)
                && let Some(class_name) = self.argument_value(&call, 0)
            {
                service.class = class_name;
            }
        }
        for call in method_calls(calls, "tag") {
            if targets(&call)
                && let Some(tag) = self.argument_value(&call, 0)
            {
                service.tags.insert(tag, String::new());
            }
        }
        for call in method_calls(calls, "autowire") {
            if targets(&call) {
                (service.autowire, service.autowire_set) = self.php_autowire_call(&call);
            }
        }
        for call in method_calls(calls, "decorate") {
            if targets(&call)
                && let Some(target) = self.argument_value(&call, 0)
            {
                service.decorates = target;
                service.decorates_range = php_argument_content_range(&call, 0);
            }
        }
        for call in method_calls(calls, "parent") {
            if targets(&call)
                && let Some(target) = self.argument_value(&call, 0)
            {
                service.parent = target;
                service.parent_range = php_argument_content_range(&call, 0);
            }
        }
        for call in method_calls(calls, "deprecate") {
            if !targets(&call) {
                continue;
            }
            service.deprecated = true;
            service.deprecated_range = call.range_trimmed_trivia();
            if let Some(message) = self.argument_value(&call, 2) {
                service.deprecation = message;
            }
        }
    }
}
