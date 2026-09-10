package app

import (
	"context"
	"testing"
)

func TestNewReturnsUsableApp(t *testing.T) {
	a := New(WithTokenStore(&fakeStore{}))
	if a == nil {
		t.Fatal("New() returned nil")
	}

	if a.ctx != nil {
		t.Fatalf("fresh App must not carry a context, got %v", a.ctx)
	}
}

func TestNewUsesProductionDefaults(t *testing.T) {
	a := New()

	if a.tokens == nil {
		t.Error("New() left the token store unset")
	}

	if a.checker == nil {
		t.Error("New() left the token checker unset")
	}
}

func TestStartupStoresContext(t *testing.T) {
	a := New(WithTokenStore(&fakeStore{}), WithTokenChecker(okChecker(nil)))

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

func TestStartupChecksTheToken(t *testing.T) {
	a := New(WithTokenStore(&fakeStore{token: "xoxp-good"}), WithTokenChecker(okChecker(nil)))

	a.startup(context.Background())

	status := a.TokenStatus()
	if !status.OK {
		t.Fatalf("TokenStatus after startup = %+v, want OK", status)
	}

	if status.User != "pavel" || status.Team != "Acme" {
		t.Errorf("TokenStatus = %+v", status)
	}
}

func TestVersionDefaultsToDev(t *testing.T) {
	if got := New(WithTokenStore(&fakeStore{})).Version(); got != Version {
		t.Fatalf("Version() = %q, want %q", got, Version)
	}

	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
