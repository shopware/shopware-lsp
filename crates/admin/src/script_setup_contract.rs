//! Lazily resolved `<script setup>` type contracts (port of Go
//! `script_setup_contract.go`).

use crate::*;

impl AdminComponentIndexer {
    /// Materializes compiler-macro contracts from the current type index.
    /// Repeatable: the definition retains the original type expressions.
    pub(crate) fn enrich_script_setup_type_contracts(
        &self,
        definition: &mut ComponentDefinition,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<()> {
        if definition.file_path.is_empty() {
            return Ok(());
        }
        let defaults = script_setup_prop_defaults(&definition.script_setup_prop_defaults);
        let resolved_props = self.resolve_script_setup_props(definition, &defaults, live_files)?;
        definition.props = overlay_script_setup_props(&definition.props, &resolved_props);
        apply_resolved_script_setup_defaults(&mut definition.props, &defaults);
        definition.members =
            overlay_resolved_script_setup_prop_members(&definition.members, &resolved_props);
        definition.members = apply_script_setup_prop_binding_members(
            &definition.members,
            &definition.props,
            &definition.script_setup_prop_bindings,
            &definition.file_path,
        );
        self.enrich_script_setup_events(definition, live_files)?;
        self.enrich_script_setup_slots(definition, live_files)
    }

    pub(crate) fn resolve_script_setup_props(
        &self,
        definition: &ComponentDefinition,
        defaults: &HashMap<String, String>,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<Vec<VueComponentProp>> {
        let mut result = Vec::new();
        for type_expression in &definition.script_setup_prop_types {
            let shape =
                self.resolve_vue_type(type_expression, &definition.file_path, live_files)?;
            for member in shape.members {
                if member.name.is_empty() {
                    continue;
                }
                let file_path = if member.definition_path.is_empty() {
                    definition.file_path.clone()
                } else {
                    member.definition_path.clone()
                };
                let mut prop = VueComponentProp {
                    required: !member.optional,
                    name: member.name,
                    type_: member.type_,
                    documentation: member.documentation,
                    file_path,
                    line: member.definition_line,
                    name_range: member.definition_range,
                    ..Default::default()
                };
                if let Some(value) = defaults.get(&prop.name).filter(|value| !value.is_empty()) {
                    prop.default = value.clone();
                    prop.required = false;
                }
                result = overlay_script_setup_props(&result, &[prop]);
            }
        }
        Ok(result)
    }

    pub(crate) fn enrich_script_setup_events(
        &self,
        definition: &mut ComponentDefinition,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<()> {
        let type_expressions = definition.script_setup_event_types.clone();
        for type_expression in &type_expressions {
            let shape =
                self.resolve_vue_type(type_expression, &definition.file_path, live_files)?;
            for member in shape.members {
                if member.name.is_empty() {
                    continue;
                }
                let file_path = if member.definition_path.is_empty() {
                    definition.file_path.clone()
                } else {
                    member.definition_path.clone()
                };
                let name = member.name.clone();
                append_component_event(
                    &mut definition.events,
                    VueComponentEvent {
                        name: member.name,
                        type_: member.type_,
                        documentation: member.documentation,
                        file_path,
                        line: member.definition_line,
                        name_range: member.definition_range,
                    },
                );
                append_unique(&mut definition.emits, &name);
            }
            let call_events =
                self.resolve_vue_events(type_expression, &definition.file_path, live_files)?;
            for event in call_events {
                let name = event.name.clone();
                append_component_event(&mut definition.events, event);
                append_unique(&mut definition.emits, &name);
            }
        }
        Ok(())
    }

    pub(crate) fn enrich_script_setup_slots(
        &self,
        definition: &mut ComponentDefinition,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<()> {
        let type_expressions = definition.script_setup_slot_types.clone();
        for type_expression in &type_expressions {
            let shape =
                self.resolve_vue_type(type_expression, &definition.file_path, live_files)?;
            let mut slots = Vec::new();
            for member in &shape.members {
                if let Some(slot) =
                    self.resolve_script_setup_slot(member, &definition.file_path, live_files)?
                {
                    slots.push(slot);
                }
            }
            definition.slots = overlay_script_setup_slots(&definition.slots, &slots);
        }
        Ok(())
    }

    pub(crate) fn resolve_script_setup_slot(
        &self,
        member: &TwigVueMember,
        definition_path: &str,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<Option<VueComponentSlot>> {
        if member.name.is_empty() {
            return Ok(None);
        }
        let file_path = if member.definition_path.is_empty() {
            definition_path.to_owned()
        } else {
            member.definition_path.clone()
        };
        let mut slot = VueComponentSlot {
            name: member.name.clone(),
            file_path: file_path.clone(),
            line: member.definition_line,
            name_range: member.definition_range.clone(),
            payload_type: meteor_slot_payload_type(&member.type_),
            ..Default::default()
        };
        if slot.payload_type.is_empty() {
            slot.members_complete = true;
            return Ok(Some(slot));
        }
        let payload = self.resolve_vue_type(&slot.payload_type, &file_path, live_files)?;
        slot.members_complete = payload.complete;
        for payload_member in payload.members {
            let member_path = if payload_member.definition_path.is_empty() {
                file_path.clone()
            } else {
                payload_member.definition_path
            };
            let member_line = if payload_member.definition_line == 0 {
                slot.line
            } else {
                payload_member.definition_line
            };
            append_slot_member(
                &mut slot.members,
                VueComponentSlotMember {
                    name: payload_member.name,
                    type_: payload_member.type_,
                    file_path: member_path,
                    line: member_line,
                    name_range: payload_member.definition_range,
                },
            );
        }
        Ok(Some(slot))
    }
}

pub(crate) fn script_setup_prop_defaults(
    values: &[ScriptSetupPropDefault],
) -> HashMap<String, String> {
    values
        .iter()
        .filter(|value| !value.name.is_empty() && !value.value.is_empty())
        .map(|value| (value.name.clone(), value.value.clone()))
        .collect()
}

pub(crate) fn apply_resolved_script_setup_defaults(
    props: &mut [VueComponentProp],
    defaults: &HashMap<String, String>,
) {
    for prop in props {
        if let Some(value) = defaults.get(&prop.name).filter(|value| !value.is_empty()) {
            prop.default = value.clone();
            prop.required = false;
        }
    }
}

pub(crate) fn apply_script_setup_prop_binding_members(
    base: &[VueComponentMember],
    props: &[VueComponentProp],
    bindings: &[ScriptSetupPropBinding],
    definition_path: &str,
) -> Vec<VueComponentMember> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, member) in result.iter().enumerate() {
        positions.insert(member.name.clone(), index);
    }
    let props_by_name: HashMap<&str, &VueComponentProp> = props
        .iter()
        .map(|prop| (prop.name.as_str(), prop))
        .collect();
    for binding in bindings {
        let Some(prop) = props_by_name.get(binding.prop_name.as_str()) else {
            continue;
        };
        if binding.binding_name.is_empty() {
            continue;
        }
        let kind = if binding.binding_name == binding.prop_name {
            VueComponentMemberKind::Prop
        } else {
            VueComponentMemberKind::Data
        };
        let mut member = VueComponentMember {
            name: binding.binding_name.clone(),
            binding_name: binding.binding_name.clone(),
            kind,
            type_: prop.type_.clone(),
            file_path: definition_path.to_owned(),
            line: binding.line,
            name_range: binding.name_range.clone(),
            type_context_path: prop.file_path.clone(),
            ..Default::default()
        };
        if let Some(&index) = positions.get(&binding.binding_name) {
            let current = &result[index];
            member.source_expression = current.source_expression.clone();
            member.open_runtime_shape = current.open_runtime_shape;
            result[index] = member;
            continue;
        }
        positions.insert(binding.binding_name.clone(), result.len());
        result.push(member);
    }
    result
}

pub(crate) fn overlay_script_setup_slots(
    base: &[VueComponentSlot],
    typed: &[VueComponentSlot],
) -> Vec<VueComponentSlot> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, slot) in result.iter().enumerate() {
        positions.insert(slot.identity_key(), index);
    }
    for slot in typed {
        let key = slot.identity_key();
        if let Some(&index) = positions.get(&key) {
            let current = &mut result[index];
            current.payload_type = slot.payload_type.clone();
            current.members =
                overlay_typed_script_setup_slot_members(&current.members, &slot.members);
            current.members_complete = slot.members_complete;
            if current.file_path.is_empty() {
                current.file_path = slot.file_path.clone();
                current.line = slot.line;
            }
            if current.name_range == AdminSourceRange::default()
                && current.file_path == slot.file_path
            {
                current.name_range = slot.name_range.clone();
            }
            continue;
        }
        positions.insert(key, result.len());
        result.push(slot.clone());
    }
    result
}

pub(crate) fn overlay_typed_script_setup_slot_members(
    base: &[VueComponentSlotMember],
    typed: &[VueComponentSlotMember],
) -> Vec<VueComponentSlotMember> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, member) in result.iter().enumerate() {
        positions.insert(member.name.clone(), index);
    }
    for member in typed {
        if member.name.is_empty() {
            continue;
        }
        let mut member = member.clone();
        if let Some(&index) = positions.get(&member.name) {
            let current = &result[index];
            if member.type_.is_empty() {
                member.type_ = current.type_.clone();
            }
            if member.file_path.is_empty() {
                member.file_path = current.file_path.clone();
                member.line = current.line;
            }
            if member.name_range == AdminSourceRange::default()
                && member.file_path == current.file_path
            {
                member.name_range = current.name_range.clone();
            }
            result[index] = member;
            continue;
        }
        positions.insert(member.name.clone(), result.len());
        result.push(member);
    }
    result
}

pub(crate) fn overlay_resolved_script_setup_prop_members(
    base: &[VueComponentMember],
    props: &[VueComponentProp],
) -> Vec<VueComponentMember> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, member) in result.iter().enumerate() {
        positions.insert(member.name.clone(), index);
    }
    for prop in props {
        if prop.name.is_empty() {
            continue;
        }
        if let Some(&index) = positions.get(&prop.name) {
            if result[index].type_.is_empty() {
                result[index].type_ = prop.type_.clone();
            }
            continue;
        }
        positions.insert(prop.name.clone(), result.len());
        result.push(VueComponentMember {
            name: prop.name.clone(),
            kind: VueComponentMemberKind::Prop,
            type_: prop.type_.clone(),
            file_path: prop.file_path.clone(),
            line: prop.line,
            ..Default::default()
        });
    }
    result
}
