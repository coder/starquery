package kv

import (
	"context"
	"sync"

	"github.com/coder/redjet"
)

type Store interface {
	Setex(ctx context.Context, seconds uint, pairs [][2]string) error
	Get(ctx context.Context, key string) (string, error)
	Delete(ctx context.Context, key string) error
}

func NewRedis(addr string) Store {
	return &redis{
		Client: redjet.New(addr),
	}
}

type redis struct {
	Client *redjet.Client
}

func (r *redis) Setex(ctx context.Context, seconds uint, pairs [][2]string) error {
	var p *redjet.Pipeline
	for _, pair := range pairs {
		p = r.Client.Pipeline(ctx, p, "SET", pair[0], pair[1], "EX", seconds)
	}
	return p.Ok()
}

func (r *redis) Get(ctx context.Context, key string) (string, error) {
	return r.Client.Command(ctx, "GET", key).String()
}

func (r *redis) Delete(ctx context.Context, key string) error {
	// DEL returns the number of records deleted.
	// We don't care if it exists or not for our impl.
	_, err := r.Client.Command(ctx, "DEL", key).Int()
	if err != nil {
		return err
	}
	return nil
}

func NewMemory() Store {
	return &memory{
		data: make(map[string]string),
	}
}

type memory struct {
	data map[string]string
	mu   sync.RWMutex
}

func (m *memory) Setex(ctx context.Context, seconds uint, pairs [][2]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, pair := range pairs {
		m.data[pair[0]] = pair[1]
	}
	return nil
}

func (m *memory) Get(ctx context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data[key], nil
}

func (m *memory) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}
