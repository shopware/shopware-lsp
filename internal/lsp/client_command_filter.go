package lsp

import "github.com/shopware/shopware-lsp/internal/lsp/protocol"

func (s *Server) filterCodeActionsForClient(
	actions []protocol.CodeAction,
) []protocol.CodeAction {
	if !s.filterClientCommands {
		return actions
	}
	result := actions[:0]
	for _, action := range actions {
		if action.Command == nil || s.supportsClientCommand(action.Command.Command) {
			result = append(result, action)
			continue
		}
		if action.Edit != nil {
			action.Command = nil
			result = append(result, action)
		}
	}
	return result
}

func (s *Server) filterResolvedCodeActionForClient(
	action protocol.CodeAction,
) protocol.CodeAction {
	if action.Command == nil || s.supportsClientCommand(action.Command.Command) {
		return action
	}
	action.Command = nil
	if action.Edit == nil {
		action.Disabled = &protocol.CodeActionDisabled{
			Reason: "The client does not support the command required by this action",
		}
	}
	return action
}

func (s *Server) filterCodeLensesForClient(
	lenses []protocol.CodeLens,
) []protocol.CodeLens {
	if !s.filterClientCommands {
		return lenses
	}
	result := lenses[:0]
	for _, lens := range lenses {
		if lens.Command == nil || s.supportsClientCommand(lens.Command.Command) {
			result = append(result, lens)
		}
	}
	return result
}

func (s *Server) filterCompletionCommandsForClient(
	items []protocol.CompletionItem,
) {
	if !s.filterClientCommands {
		return
	}
	for index := range items {
		switch command := items[index].Command.(type) {
		case protocol.Command:
			if !s.supportsClientCommand(command.Command) {
				items[index].Command = nil
			}
		case *protocol.Command:
			if command != nil && !s.supportsClientCommand(command.Command) {
				items[index].Command = nil
			}
		case protocol.CommandAction:
			if !s.supportsClientCommand(command.Command) {
				items[index].Command = nil
			}
		case *protocol.CommandAction:
			if command != nil && !s.supportsClientCommand(command.Command) {
				items[index].Command = nil
			}
		}
	}
}

func (s *Server) filterInlayHintCommandsForClient(
	hints []protocol.InlayHint,
) {
	if !s.filterClientCommands {
		return
	}
	for hintIndex := range hints {
		parts, ok := hints[hintIndex].Label.([]protocol.InlayHintLabelPart)
		if !ok {
			continue
		}
		for partIndex := range parts {
			command := parts[partIndex].Command
			if command != nil && !s.supportsClientCommand(command.Command) {
				parts[partIndex].Command = nil
			}
		}
		hints[hintIndex].Label = parts
	}
}
