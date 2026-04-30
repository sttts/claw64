package serial

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestListenAndStartUntilReturnsStartedProcessExit(t *testing.T) {
	exited := make(chan error, 1)
	exited <- errors.New("vice exited")

	_, err := ListenAndStartUntil("127.0.0.1:0", func() (<-chan error, error) {
		return exited, nil
	})
	if err == nil {
		t.Fatal("ListenAndStartUntil returned nil error")
	}
	if !strings.Contains(err.Error(), "vice exited") {
		t.Fatalf("ListenAndStartUntil error = %q, want VICE exit cause", err)
	}
}

func TestListenAndStartUntilReturnsStartError(t *testing.T) {
	startErr := errors.New("spawn failed")

	_, err := ListenAndStartUntil("127.0.0.1:0", func() (<-chan error, error) {
		return nil, startErr
	})
	if !errors.Is(err, startErr) {
		t.Fatalf("ListenAndStartUntil error = %v, want %v", err, startErr)
	}
}

func TestListenAndStartUntilDoesNotHangAfterProcessExit(t *testing.T) {
	exited := make(chan error, 1)

	errCh := make(chan error, 1)
	go func() {
		_, err := ListenAndStartUntil("127.0.0.1:0", func() (<-chan error, error) {
			return exited, nil
		})
		errCh <- err
	}()

	exited <- errors.New("early exit")

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "early exit") {
			t.Fatalf("ListenAndStartUntil error = %v, want early exit", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndStartUntil hung after process exit")
	}
}
