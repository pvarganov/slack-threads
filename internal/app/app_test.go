package app

import (
	"context"
	"testing"
)

func TestNewReturnsUsableApp(t *testing.T) {
	a := New()
	if a == nil {
		t.Fatal("New() returned nil")
	}

	if a.ctx != nil {
		t.Fatalf("fresh App must not carry a context, got %v", a.ctx)
	}
}

func TestStartupStoresContext(t *testing.T) {
	a := New()

	type key struct{}

	ctx := context.WithValue(context.Background(), key{}, "v")

	a.startup(ctx)

	if a.ctx == nil {
		t.Fatal("startup did not store the context")
	}

	if got := a.ctx.Value(key{}); got != "v" {
		t.Fatalf("stored context lost its value: got %v, want %q", got, "v")
	}
}

func TestVersionDefaultsToDev(t *testing.T) {
	if got := New().Version(); got != Version {
		t.Fatalf("Version() = %q, want %q", got, Version)
	}

	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
