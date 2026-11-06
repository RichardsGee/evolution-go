package whatsmeow_service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
)

func TestIsAppStateHashMismatch(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "snapshot lthash, wrapped as the library does",
			err:  fmt.Errorf("failed to verify snapshot: %w", fmt.Errorf("failed to verify patch v1690: %w", appstate.ErrMismatchingLTHash)),
			want: true,
		},
		{
			name: "patch mac",
			err:  fmt.Errorf("failed to verify patch v12: %w", appstate.ErrMismatchingPatchMAC),
			want: true,
		},
		{
			name: "rate limit from the fetch path",
			err:  &whatsmeow.IQError{Code: 429, Text: "rate-overlimit"},
			want: false,
		},
		{
			name: "missing key is resolved by a key request",
			err:  fmt.Errorf("failed to get key: %w", appstate.ErrKeyNotFound),
			want: false,
		},
		{
			name: "canceled context",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("boom"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAppStateHashMismatch(tt.err); got != tt.want {
				t.Fatalf("isAppStateHashMismatch(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestAppStateRecoveryThrottle(t *testing.T) {
	start := time.Date(2026, 11, 6, 18, 0, 0, 0, time.UTC)

	t.Run("blocks a repeat until the cooldown ends", func(t *testing.T) {
		throttle := &appStateRecoveryThrottle{last: make(map[string]time.Time)}

		if !throttle.allow("instance-a", appstate.WAPatchRegular, start) {
			t.Fatal("first request should be allowed")
		}
		if throttle.allow("instance-a", appstate.WAPatchRegular, start.Add(appStateRecoveryCooldown-time.Second)) {
			t.Fatal("request inside the cooldown should be blocked")
		}
		if !throttle.allow("instance-a", appstate.WAPatchRegular, start.Add(appStateRecoveryCooldown)) {
			t.Fatal("request after the cooldown should be allowed")
		}
	})

	t.Run("a blocked request does not extend the window", func(t *testing.T) {
		throttle := &appStateRecoveryThrottle{last: make(map[string]time.Time)}

		throttle.allow("instance-a", appstate.WAPatchRegular, start)
		throttle.allow("instance-a", appstate.WAPatchRegular, start.Add(appStateRecoveryCooldown-time.Second))

		if !throttle.allow("instance-a", appstate.WAPatchRegular, start.Add(appStateRecoveryCooldown)) {
			t.Fatal("cooldown should count from the last request that was allowed")
		}
	})

	t.Run("collections and instances are independent", func(t *testing.T) {
		throttle := &appStateRecoveryThrottle{last: make(map[string]time.Time)}

		throttle.allow("instance-a", appstate.WAPatchRegular, start)

		if !throttle.allow("instance-a", appstate.WAPatchRegularLow, start) {
			t.Fatal("another collection should not be blocked")
		}
		if !throttle.allow("instance-b", appstate.WAPatchRegular, start) {
			t.Fatal("another instance should not be blocked")
		}
	})
}
