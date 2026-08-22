package agent

import "github.com/dotside-studios/davi-nfc-agent/settings"

// ApplySettings pushes stored settings onto a running agent. The port is not
// applied: rebinding the listener belongs in an explicit restart.
func (agent *Agent) ApplySettings(s settings.Settings) {
	if agent == nil {
		return
	}

	if reader := agent.Reader(); reader != nil {
		reader.SetMode(settings.ParseMode(s.Mode))
	}

	agent.ClearCardTypeFilter()
	for _, t := range s.CardTypes {
		agent.AllowCardType(t)
	}

	agent.SetRequirePairedDevice(s.RequirePairedDevice)
	agent.SetReaderFeedback(s.ReaderFeedback)
}
