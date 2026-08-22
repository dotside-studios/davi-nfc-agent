package surface_test

import (
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/surface"
)

// stub is a plugin that records being attached and nothing else.
type stub struct {
	info     surface.Info
	err      error
	attached int
	detached int
}

func (s *stub) Describe() surface.Info { return s.info }

func (s *stub) Attach(surface.Host) error {
	s.attached++
	return s.err
}

func (s *stub) Detach() { s.detached++ }

func TestRegistryKeepsRegistrationOrder(t *testing.T) {
	registry := surface.NewRegistry()

	for _, id := range []string{"pairing", "turnstile", "backup"} {
		if err := registry.Add(&stub{info: surface.Info{ID: id}}); err != nil {
			t.Fatalf("Add(%q): %v", id, err)
		}
	}

	var got []string
	for _, plugin := range registry.Plugins() {
		got = append(got, plugin.Describe().ID)
	}
	if len(got) != 3 || got[0] != "pairing" || got[1] != "turnstile" || got[2] != "backup" {
		t.Fatalf("registry reads %v, want them in the order they were added", got)
	}
}

func TestRegisteringTwiceReplaces(t *testing.T) {
	registry := surface.NewRegistry()

	first := &stub{info: surface.Info{ID: "turnstile", Title: "Turnstile"}}
	second := &stub{info: surface.Info{ID: "turnstile", Title: "Turnstile Mk II"}}

	if err := registry.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Add(second); err != nil {
		t.Fatal(err)
	}

	if registry.Len() != 1 {
		t.Fatalf("registered %d plugins under one ID, want 1", registry.Len())
	}
	plugin, ok := registry.Get("turnstile")
	if !ok || plugin != surface.Plugin(second) {
		t.Fatal("the second registration did not replace the first")
	}
}

func TestRegistryRefusesAPluginWithNoID(t *testing.T) {
	registry := surface.NewRegistry()

	err := registry.Add(&stub{info: surface.Info{Title: "Nameless"}})
	if !errors.Is(err, surface.ErrNoID) {
		t.Fatalf("Add returned %v, want it to refuse a plugin with no ID", err)
	}
	if registry.Len() != 0 {
		t.Error("a plugin with no ID was registered anyway")
	}
}

func TestRegistryRemove(t *testing.T) {
	registry := surface.NewRegistry()
	if err := registry.Add(&stub{info: surface.Info{ID: "turnstile"}}); err != nil {
		t.Fatal(err)
	}

	if !registry.Remove("turnstile") {
		t.Fatal("Remove reported nothing to remove")
	}
	if registry.Remove("turnstile") || registry.Len() != 0 {
		t.Fatal("the plugin is still registered")
	}
}

func TestRegisterLandsInTheDefaultRegistry(t *testing.T) {
	plugin := &stub{info: surface.Info{ID: "surface_test.default"}}
	t.Cleanup(func() { surface.Default().Remove(plugin.info.ID) })

	surface.Register(plugin)

	if _, ok := surface.Default().Get(plugin.info.ID); !ok {
		t.Fatal("a registered plugin is not in the default registry")
	}
}

func TestRegisterPanicsWithoutAnID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register accepted a plugin with no ID")
		}
	}()

	surface.Register(&stub{})
}

func TestInfoNameFallsBackToTheID(t *testing.T) {
	if got := (surface.Info{ID: "turnstile"}).Name(); got != "turnstile" {
		t.Errorf("Name = %q, want the ID when there is no title", got)
	}
	if got := (surface.Info{ID: "turnstile", Title: "  "}).Name(); got != "turnstile" {
		t.Errorf("Name = %q, want the ID when the title is blank", got)
	}
	if got := (surface.Info{ID: "turnstile", Title: "Turnstile"}).Name(); got != "Turnstile" {
		t.Errorf("Name = %q, want the title", got)
	}
}
