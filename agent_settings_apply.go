package main

import "github.com/dotside-studios/davi-nfc-agent/settings"

// applySettings pushes stored settings onto a running agent. The port is not
// applied: rebinding the listener belongs in an explicit restart.
func applySettings(agent *Agent, s settings.Settings) {
	if agent == nil {
		return
	}

	if agent.Reader != nil {
		agent.Reader.SetMode(settings.ParseMode(s.Mode))
	}

	// Mutated in place, never replaced: the running device server was handed
	// this same map at construction, so assigning a new one here would leave it
	// filtering on a snapshot that no longer changes.
	for t := range agent.AllowedCardTypes {
		delete(agent.AllowedCardTypes, t)
	}
	if len(s.CardTypes) == 0 {
		agent.AllowAllCardTypes()
	} else {
		for _, t := range s.CardTypes {
			agent.AllowCardType(t)
		}
	}

	agent.SetRequirePairedDevice(s.RequirePairedDevice)
}
