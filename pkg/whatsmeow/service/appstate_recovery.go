package whatsmeow_service

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types/events"
)

const (
	// appStateRecoveryCooldown limits how often the phone is asked for a snapshot
	// of the same collection. Each request is a message to the user's own phone.
	appStateRecoveryCooldown = 30 * time.Minute
	appStateRecoveryTimeout  = 30 * time.Second
)

// appStateRecoveryThrottle lives outside MyClient because a new MyClient is
// created on every StartClient, and a reconnect must not reopen the window.
type appStateRecoveryThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var appStateRecoveries = &appStateRecoveryThrottle{last: make(map[string]time.Time)}

func (t *appStateRecoveryThrottle) allow(instanceID string, name appstate.WAPatchName, now time.Time) bool {
	key := instanceID + "/" + string(name)

	t.mu.Lock()
	defer t.mu.Unlock()

	if last, ok := t.last[key]; ok && now.Sub(last) < appStateRecoveryCooldown {
		return false
	}
	t.last[key] = now
	return true
}

// isAppStateHashMismatch reports whether local state cannot be verified against
// the server's. Rate limits and fetch failures also reach AppStateSyncError, and
// a snapshot from the phone would not fix those.
func isAppStateHashMismatch(err error) bool {
	return errors.Is(err, appstate.ErrMismatchingLTHash) || errors.Is(err, appstate.ErrMismatchingPatchMAC)
}

// handleAppStateSyncError asks the phone for a snapshot of the collection that
// failed verification. Handling this event is left to the application by
// whatsmeow, and without it the collection stays broken: when a collection has
// no local row, each fetch is a full sync that fails the same way and stores
// nothing.
//
// Full sync is not attempted here because version 0 already forces one, and the
// fatal notification is not sent because it unlinks every device.
func (mycli *MyClient) handleAppStateSyncError(evt *events.AppStateSyncError) {
	log := mycli.loggerWrapper.GetLogger(mycli.userID)

	if !isAppStateHashMismatch(evt.Error) {
		log.LogWarn("[%s] App state sync error on %s (fullSync=%v), no recovery: %v", mycli.userID, evt.Name, evt.FullSync, evt.Error)
		return
	}
	if !appStateRecoveries.allow(mycli.userID, evt.Name, time.Now()) {
		log.LogWarn("[%s] App state mismatch on %s (fullSync=%v), recovery requested less than %s ago: %v", mycli.userID, evt.Name, evt.FullSync, appStateRecoveryCooldown, evt.Error)
		return
	}

	log.LogWarn("[%s] App state mismatch on %s (fullSync=%v), requesting recovery snapshot: %v", mycli.userID, evt.Name, evt.FullSync, evt.Error)

	// The library dispatches this event synchronously while fetching app state, so
	// sending from here would block that path until the send returns.
	client := mycli.WAClient
	userID := mycli.userID
	name := evt.Name
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), appStateRecoveryTimeout)
		defer cancel()

		if _, err := client.SendPeerMessage(ctx, whatsmeow.BuildAppStateRecoveryRequest(name)); err != nil {
			log.LogError("[%s] Failed to send app state recovery request for %s: %v", userID, name, err)
			return
		}
		log.LogInfo("[%s] App state recovery request sent for %s", userID, name)
	}()
}
