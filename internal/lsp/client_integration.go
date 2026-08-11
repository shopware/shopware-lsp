package lsp

import (
	"fmt"
	"sort"
	"strings"

	integrationcatalog "github.com/shopware/shopware-lsp/internal/integration"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

const ClientProtocolVersion = integrationcatalog.ProtocolVersion

type PresentationProfile string

const (
	PresentationProfileFull      PresentationProfile = "full"
	PresentationProfileFramework PresentationProfile = "framework"
)

func (s *Server) configureClientIntegration(
	options *protocol.ShopwareClientOptions,
) error {
	s.clientProtocolVersion = ClientProtocolVersion
	s.presentationProfile = string(PresentationProfileFull)
	s.filterClientCommands = false
	s.supportedClientCommands = make(map[string]struct{})
	if options == nil {
		return nil
	}
	if options.ProtocolVersion != ClientProtocolVersion {
		return fmt.Errorf(
			"unsupported Shopware client protocol version %d; server supports %d",
			options.ProtocolVersion,
			ClientProtocolVersion,
		)
	}
	profile := PresentationProfile(strings.TrimSpace(options.PresentationProfile))
	if profile == "" {
		profile = PresentationProfileFull
	}
	switch profile {
	case PresentationProfileFull, PresentationProfileFramework:
	default:
		return fmt.Errorf("unsupported Shopware presentation profile %q", profile)
	}
	s.presentationProfile = string(profile)
	s.filterClientCommands = true
	for _, command := range options.SupportedCommands {
		command = strings.TrimSpace(command)
		if command != "" {
			s.supportedClientCommands[command] = struct{}{}
		}
	}
	return nil
}

func (s *Server) PresentationProfile() PresentationProfile {
	profile := PresentationProfile(s.presentationProfile)
	if profile == "" {
		return PresentationProfileFull
	}
	return profile
}

func (s *Server) FrameworkPresentation() bool {
	return s.PresentationProfile() == PresentationProfileFramework
}

func (s *Server) supportsClientCommand(command string) bool {
	if !s.filterClientCommands || command == "" {
		return true
	}
	_, supported := s.supportedClientCommands[command]
	return supported
}

func (s *Server) inspectionPresentedToClient(id string) bool {
	if !s.FrameworkPresentation() {
		return true
	}
	switch id {
	case "php.semantic", "symfony.embedded_language":
		return false
	default:
		return true
	}
}

func (s *Server) negotiatedClientState(active bool, reason string) map[string]interface{} {
	state := map[string]interface{}{
		"active":              active,
		"protocolVersion":     s.clientProtocolVersion,
		"presentationProfile": string(s.PresentationProfile()),
	}
	if reason != "" {
		state["reason"] = reason
	}
	if s.filterClientCommands {
		commands := make([]string, 0, len(s.supportedClientCommands))
		for command := range s.supportedClientCommands {
			commands = append(commands, command)
		}
		sort.Strings(commands)
		state["supportedCommands"] = commands
	}
	return state
}
