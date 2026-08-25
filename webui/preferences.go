package webui

// Preferences is what the agent is set to, and the console's only source for a
// preference: the reader mode, the card-type filter, the pinned reader, the
// port and the two policies come from here, so the console cannot show one
// thing while the agent does another.
//
// The agent holds these and nothing persists them. A change made here lasts as
// long as the agent runs.
type Preferences struct {
	// Mode is the reader access mode: "readwrite", "read" or "write".
	Mode string `json:"mode"`

	// CardTypes is the card-type allowlist. Empty allows every type, including
	// one this build has never heard of.
	CardTypes []string `json:"cardTypes"`

	// DevicePath pins a reader. Empty is auto-detect.
	DevicePath string `json:"devicePath"`

	// Port is the port the agent is set to serve on. The listener keeps the one
	// it is bound on until it is restarted.
	Port int `json:"port"`

	RequirePairedDevice bool `json:"requirePairedDevice"`
	ReaderFeedback      bool `json:"readerFeedback"`
}
