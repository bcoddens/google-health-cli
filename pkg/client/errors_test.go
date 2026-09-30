package client

import (
	"errors"
	"fmt"
	"testing"
)

func TestAsCLIError(t *testing.T) {
	want := NewValidationError("bad", "")
	got, ok := AsCLIError(fmt.Errorf("context: %w", want))
	if !ok || got != want {
		t.Fatalf("wrapped CLIError not unwrapped: got %v, ok=%v", got, ok)
	}
	if got, ok := AsCLIError(want); !ok || got != want {
		t.Fatalf("direct CLIError not recognised: got %v, ok=%v", got, ok)
	}
	if got, ok := AsCLIError(errors.New("plain")); ok || got != nil {
		t.Fatalf("plain error must not match: got %v, ok=%v", got, ok)
	}
	if _, ok := AsCLIError(nil); ok {
		t.Fatal("nil error must not match")
	}
}
