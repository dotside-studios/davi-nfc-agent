package virtualnfc

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func TestMessageBuilders(t *testing.T) {
	txt, err := TextMessage("hello", "")
	if err != nil {
		t.Fatalf("TextMessage: %v", err)
	}
	if _, err := txt.Encode(); err != nil {
		t.Fatalf("encode text message: %v", err)
	}

	uri, err := URIMessage("https://example.com")
	if err != nil {
		t.Fatalf("URIMessage: %v", err)
	}
	if _, err := uri.Encode(); err != nil {
		t.Fatalf("encode uri message: %v", err)
	}

	multi, err := Message(&nfc.NDEFText{Content: "a"}, &nfc.NDEFURI{Content: "https://x.y"})
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got := len(multi.Records()); got != 2 {
		t.Fatalf("Message records = %d, want 2", got)
	}

	if _, err := Message(); err == nil {
		t.Error("Message() with no records: want error")
	}
}
