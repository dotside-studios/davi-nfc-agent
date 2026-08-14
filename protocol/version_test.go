package protocol

import (
	"encoding/json"
	"testing"
)

func TestVersionFromSubprotocol(t *testing.T) {
	tests := []struct {
		sub  string
		want int
	}{
		{SubprotocolDeviceV1, DeviceProtocolV1},
		{"", DeviceProtocolV0},
		{"davi-nfc-device.v99", DeviceProtocolV0},
		{"something-else", DeviceProtocolV0},
	}

	for _, tt := range tests {
		if got := VersionFromSubprotocol(tt.sub); got != tt.want {
			t.Errorf("VersionFromSubprotocol(%q) = %d, want %d", tt.sub, got, tt.want)
		}
	}
}

func TestNegotiateDeviceVersion(t *testing.T) {
	tests := []struct {
		name     string
		declared int
		want     int
	}{
		{"omitted is raised to v1", 0, DeviceProtocolV1},
		{"negative is raised to v1", -3, DeviceProtocolV1},
		{"exact match", DeviceProtocolV1, DeviceProtocolV1},
		{"future version clamps to max", 99, DeviceProtocolMax},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NegotiateDeviceVersion(tt.declared); got != tt.want {
				t.Errorf("NegotiateDeviceVersion(%d) = %d, want %d", tt.declared, got, tt.want)
			}
		})
	}
}

// The registration fields must stay at the top level of the hello payload rather
// than nesting under an embedded object, so a v1 payload is a v0 payload plus
// protocolVersion.
func TestHelloRequestFlattensRegistration(t *testing.T) {
	raw := []byte(`{
		"protocolVersion": 1,
		"deviceName": "Test Device",
		"platform": "android",
		"appVersion": "2.0.0",
		"capabilities": {"canRead": true, "canWrite": true, "nfcType": "nfca"}
	}`)

	var hello HelloRequest
	if err := json.Unmarshal(raw, &hello); err != nil {
		t.Fatalf("unmarshal hello: %v", err)
	}

	if hello.ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d, want 1", hello.ProtocolVersion)
	}
	if hello.DeviceName != "Test Device" {
		t.Errorf("DeviceName = %q, want %q", hello.DeviceName, "Test Device")
	}
	if !hello.Capabilities.CanWrite {
		t.Error("Capabilities.CanWrite = false, want true")
	}

	out, err := json.Marshal(hello)
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}

	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	for _, key := range []string{"protocolVersion", "deviceName", "platform", "capabilities"} {
		if _, ok := round[key]; !ok {
			t.Errorf("marshalled hello missing top-level key %q: %s", key, out)
		}
	}
}

func TestHelloResponseFlattensRegistration(t *testing.T) {
	out, err := json.Marshal(HelloResponse{
		ProtocolVersion:            DeviceProtocolV1,
		DeviceRegistrationResponse: DeviceRegistrationResponse{DeviceID: "dev_1"},
	})
	if err != nil {
		t.Fatalf("marshal hello response: %v", err)
	}

	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if round["deviceID"] != "dev_1" {
		t.Errorf("deviceID = %v, want dev_1: %s", round["deviceID"], out)
	}
	if round["protocolVersion"] != float64(DeviceProtocolV1) {
		t.Errorf("protocolVersion = %v, want %d: %s", round["protocolVersion"], DeviceProtocolV1, out)
	}
}
