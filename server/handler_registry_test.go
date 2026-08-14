package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

// mockHandlerFunc is a mock implementation of HandlerFunc for testing
func mockHandlerFunc(ctx context.Context, conn *websocket.Conn, req protocol.WebSocketRequest) error {
	return nil
}

func TestNewHandlerRegistry(t *testing.T) {
	registry := NewHandlerRegistry()
	if registry == nil {
		t.Fatal("NewHandlerRegistry returned nil")
	}
	if registry.handlers == nil {
		t.Fatal("handlers map not initialized")
	}
}

func TestHandlerRegistry_Handle(t *testing.T) {
	registry := NewHandlerRegistry()

	t.Run("register valid handler", func(t *testing.T) {
		err := registry.Handle("test", mockHandlerFunc)
		if err != nil {
			t.Fatalf("failed to register handler: %v", err)
		}
	})

	t.Run("register nil handler", func(t *testing.T) {
		err := registry.Handle("nil", nil)
		if err == nil {
			t.Fatal("expected error when registering nil handler")
		}
	})

	t.Run("register handler with empty message type", func(t *testing.T) {
		err := registry.Handle("", mockHandlerFunc)
		if err == nil {
			t.Fatal("expected error when registering handler with empty message type")
		}
	})

	t.Run("register duplicate handler", func(t *testing.T) {
		err := registry.Handle("duplicate", mockHandlerFunc)
		if err != nil {
			t.Fatalf("failed to register first handler: %v", err)
		}

		err = registry.Handle("duplicate", mockHandlerFunc)
		if err == nil {
			t.Fatal("expected error when registering duplicate handler")
		}
	})
}

func TestHandlerRegistry_Get(t *testing.T) {
	registry := NewHandlerRegistry()
	_ = registry.Handle("test", mockHandlerFunc)

	t.Run("get existing handler", func(t *testing.T) {
		retrieved, ok := registry.Get("test")
		if !ok {
			t.Fatal("handler not found")
		}
		if retrieved == nil {
			t.Fatal("retrieved handler is nil")
		}
	})

	t.Run("get non-existent handler", func(t *testing.T) {
		_, ok := registry.Get("nonexistent")
		if ok {
			t.Fatal("expected handler not to be found")
		}
	})
}

func TestHandlerRegistry_Has(t *testing.T) {
	registry := NewHandlerRegistry()
	_ = registry.Handle("test", mockHandlerFunc)

	t.Run("has existing handler", func(t *testing.T) {
		if !registry.Has("test") {
			t.Fatal("expected handler to exist")
		}
	})

	t.Run("has non-existent handler", func(t *testing.T) {
		if registry.Has("nonexistent") {
			t.Fatal("expected handler not to exist")
		}
	})
}

func TestHandlerRegistry_MessageTypes(t *testing.T) {
	registry := NewHandlerRegistry()

	t.Run("empty registry", func(t *testing.T) {
		types := registry.MessageTypes()
		if len(types) != 0 {
			t.Fatalf("expected 0 message types, got %d", len(types))
		}
	})

	t.Run("registry with handlers", func(t *testing.T) {
		_ = registry.Handle("type1", mockHandlerFunc)
		_ = registry.Handle("type2", mockHandlerFunc)
		_ = registry.Handle("type3", mockHandlerFunc)

		types := registry.MessageTypes()
		if len(types) != 3 {
			t.Fatalf("expected 3 message types, got %d", len(types))
		}

		// Check that all types are present
		typeMap := make(map[string]bool)
		for _, typ := range types {
			typeMap[typ] = true
		}

		if !typeMap["type1"] || !typeMap["type2"] || !typeMap["type3"] {
			t.Fatal("not all registered types are present")
		}
	})
}

func TestHandlerRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewHandlerRegistry()
	var wg sync.WaitGroup

	// Concurrent registration
	t.Run("concurrent registration", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = registry.Handle(string(rune('a'+i)), mockHandlerFunc)
			}(i)
		}
		wg.Wait()
	})

	// Concurrent reads
	t.Run("concurrent reads", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				registry.Get(string(rune('a' + i)))
				registry.Has(string(rune('a' + i)))
			}(i)
		}
		wg.Wait()
	})

	// Concurrent read and write
	t.Run("concurrent read and write", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				_ = registry.Handle("concurrent"+string(rune('a'+i)), mockHandlerFunc)
			}(i)
			go func(i int) {
				defer wg.Done()
				registry.Get("concurrent" + string(rune('a'+i)))
			}(i)
		}
		wg.Wait()
	})
}

func TestHandlerRegistry_HandleExecution(t *testing.T) {
	registry := NewHandlerRegistry()

	called := false
	handler := func(ctx context.Context, conn *websocket.Conn, req protocol.WebSocketRequest) error {
		called = true
		return nil
	}

	_ = registry.Handle("test", handler)

	t.Run("execute handler", func(t *testing.T) {
		h, ok := registry.Get("test")
		if !ok {
			t.Fatal("handler not found")
		}

		err := h(context.Background(), nil, protocol.WebSocketRequest{})
		if err != nil {
			t.Fatalf("handler execution failed: %v", err)
		}

		if !called {
			t.Fatal("handler was not called")
		}
	})

	t.Run("handler returns error", func(t *testing.T) {
		expectedErr := errors.New("test error")
		errorHandler := func(ctx context.Context, conn *websocket.Conn, req protocol.WebSocketRequest) error {
			return expectedErr
		}

		_ = registry.Handle("error", errorHandler)

		h, _ := registry.Get("error")
		err := h(context.Background(), nil, protocol.WebSocketRequest{})
		if err != expectedErr {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestHandlerRegistry_LifecycleHandlers(t *testing.T) {
	t.Run("register lifecycle function", func(t *testing.T) {
		registry := NewHandlerRegistry()
		starter := func(ctx context.Context) {}

		registry.RegisterLifecycle(starter)

		if len(registry.lifecycleStarters) != 1 {
			t.Fatalf("expected 1 lifecycle starter, got %d", len(registry.lifecycleStarters))
		}
	})

	t.Run("register multiple lifecycle functions", func(t *testing.T) {
		registry := NewHandlerRegistry()

		starter1 := func(ctx context.Context) {}
		starter2 := func(ctx context.Context) {}
		starter3 := func(ctx context.Context) {}

		registry.RegisterLifecycle(starter1)
		registry.RegisterLifecycle(starter2)
		registry.RegisterLifecycle(starter3)

		if len(registry.lifecycleStarters) != 3 {
			t.Fatalf("expected 3 lifecycle starters, got %d", len(registry.lifecycleStarters))
		}
	})
}

func TestHandlerRegistry_StartLifecycleHandlers(t *testing.T) {
	t.Run("start all lifecycle functions", func(t *testing.T) {
		registry := NewHandlerRegistry()
		count := 0
		var mu sync.Mutex

		starter1 := func(ctx context.Context) {
			mu.Lock()
			defer mu.Unlock()
			count++
		}
		starter2 := func(ctx context.Context) {
			mu.Lock()
			defer mu.Unlock()
			count++
		}
		starter3 := func(ctx context.Context) {
			mu.Lock()
			defer mu.Unlock()
			count++
		}

		registry.RegisterLifecycle(starter1)
		registry.RegisterLifecycle(starter2)
		registry.RegisterLifecycle(starter3)

		ctx := context.Background()
		registry.StartLifecycleHandlers(ctx)

		mu.Lock()
		defer mu.Unlock()
		if count != 3 {
			t.Fatalf("expected 3 lifecycle starters to be called, got %d", count)
		}
	})

	t.Run("start with no lifecycle handlers", func(t *testing.T) {
		registry := NewHandlerRegistry()
		_ = registry.Handle("regular", mockHandlerFunc)

		// Should not panic
		ctx := context.Background()
		registry.StartLifecycleHandlers(ctx)
	})

	t.Run("start empty registry", func(t *testing.T) {
		registry := NewHandlerRegistry()

		// Should not panic
		ctx := context.Background()
		registry.StartLifecycleHandlers(ctx)
	})

	t.Run("lifecycle function receives context", func(t *testing.T) {
		registry := NewHandlerRegistry()
		var receivedCtx context.Context

		starter := func(ctx context.Context) {
			receivedCtx = ctx
		}

		registry.RegisterLifecycle(starter)

		ctx := context.Background()
		registry.StartLifecycleHandlers(ctx)

		if receivedCtx == nil {
			t.Fatal("lifecycle function did not receive context")
		}
	})
}
