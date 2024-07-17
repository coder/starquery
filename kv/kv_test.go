package kv_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/coder/starquery/kv"
)

func TestMemoryStore(t *testing.T) {
	t.Parallel()

	t.Run("SetexAndGet", func(t *testing.T) {
		t.Parallel()
		store := kv.NewMemory()
		ctx := context.Background()

		pairs := [][2]string{
			{"key1", "value1"},
			{"key2", "value2"},
		}

		if err := store.Setex(ctx, 1, pairs); err != nil {
			t.Fatalf("Setex() error = %v", err)
		}

		for _, p := range pairs {
			got, err := store.Get(ctx, p[0])
			if err != nil {
				t.Errorf("Get(%q) error = %v", p[0], err)
			}
			if got != p[1] {
				t.Errorf("Get(%q) = %q, want %q", p[0], got, p[1])
			}
		}
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		store := kv.NewMemory()
		ctx := context.Background()

		const (
			key   = "key-to-delete"
			value = "value-to-delete"
		)

		if err := store.Setex(ctx, 1, [][2]string{{key, value}}); err != nil {
			t.Fatalf("Setex() error = %v", err)
		}

		if err := store.Delete(ctx, key); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got != "" {
			t.Errorf("Get() = %q, want empty string", got)
		}
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		t.Parallel()
		store := kv.NewMemory()
		ctx := context.Background()

		const goroutines = 100
		var wg sync.WaitGroup
		wg.Add(goroutines)

		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				key := fmt.Sprintf("concurrent-key-%d", id)
				value := fmt.Sprintf("concurrent-value-%d", id)

				if err := store.Setex(ctx, 1, [][2]string{{key, value}}); err != nil {
					t.Errorf("Setex() error = %v", err)
					return
				}

				got, err := store.Get(ctx, key)
				if err != nil {
					t.Errorf("Get(%q) error = %v", key, err)
					return
				}
				if got != value {
					t.Errorf("Get(%q) = %q, want %q", key, got, value)
				}
			}(i)
		}

		wg.Wait()
	})
}
