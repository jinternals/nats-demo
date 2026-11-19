// End-to-end tests against the stack from `make up`. Run with `make test`.
// They cut WAN links and kill containers, so run them one at a time (no t.Parallel).
package e2e

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// Events published while the WAN is down are acked by the site and arrive
// in the cloud, in order, once the link is back.
func TestUplinkSurvivesWANOutage(t *testing.T) {
	r := robot(t)
	publishTelemetry(t, "site1", r, 1, 10)
	waitCloudCount(t, "site1", r, 10, 30*time.Second)

	restore := cutWAN(t, "site1")
	publishTelemetry(t, "site1", r, 11, 30) // succeeds: acked by site1's disk
	time.Sleep(2 * time.Second)
	if got := cloudCount(t, "site1", r); got != 10 {
		t.Fatalf("during outage cloud has %d events, want 10", got)
	}

	restore()
	waitCloudCount(t, "site1", r, 30, 3*time.Minute)
	assertInOrder(t, "site1", r, 30)
}

// Commands wait in the cloud during an outage; one whose TTL runs out is
// deleted there and never reaches the site.
func TestStaleCommandsExpireInCloud(t *testing.T) {
	pullCommands(t, "site1") // drain leftovers from earlier runs
	r := robot(t)
	restore := cutWAN(t, "site1")
	queueCommand(t, "site1", r, r+"-fresh", 15*time.Minute)
	queueCommand(t, "site1", r, r+"-stale", 5*time.Second)
	time.Sleep(7 * time.Second)
	restore()

	var ids []string
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) && !slices.Contains(ids, r+"-fresh") {
		got, _ := pullCommands(t, "site1") // fails with "no responders" until site1 reconnects
		ids = append(ids, got...)
	}
	if !slices.Contains(ids, r+"-fresh") {
		t.Fatalf("fresh command not delivered; got %v", ids)
	}
	if slices.Contains(ids, r+"-stale") {
		t.Fatalf("stale command was delivered")
	}
}

// Each site sees only its own data. Isolation comes from per-site hub accounts.
func TestSitesAreIsolated(t *testing.T) {
	t.Run("site cannot pull another site's commands", func(t *testing.T) {
		// site-ops has full rights on site1's own server, so only the hub stops it.
		js := mustAPI(t, siteOps(t, "site1"))
		_, err := js.Consumer(ctx(t, 5*time.Second), "fleet-commands", "site2-commands")
		if !errors.Is(err, nats.ErrNoResponders) {
			t.Fatalf("want no responders (not imported into site1's account), got %v", err)
		}
	})
	t.Run("edge app cannot publish commands", func(t *testing.T) {
		errs := make(chan error, 1)
		nc := edgeApp(t, "site1")
		nc.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errs <- err })
		nc.Publish(command("site1", "arm-01"), []byte("forged"))
		nc.Flush()
		select {
		case err := <-errs:
			if !strings.Contains(err.Error(), "Permissions Violation") {
				t.Fatalf("unexpected error %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("publish was not rejected")
		}
	})
	t.Run("site sees no traffic of another site", func(t *testing.T) {
		var mu sync.Mutex
		var leaked []string
		sub, _ := siteOps(t, "site1").Subscribe(">", func(m *nats.Msg) {
			if strings.Contains(m.Subject, "site2") || strings.Contains(string(m.Data), "site2") {
				mu.Lock()
				leaked = append(leaked, m.Subject)
				mu.Unlock()
			}
		})
		defer sub.Unsubscribe()
		r := robot(t)
		publishTelemetry(t, "site2", r, 1, 20)
		queueCommand(t, "site2", r, r+"-cmd", time.Minute)
		pullCommands(t, "site2")
		waitCloudCount(t, "site2", r, 20, time.Minute)
		time.Sleep(time.Second)
		mu.Lock()
		defer mu.Unlock()
		if len(leaked) > 0 {
			t.Fatalf("site1 saw site2 traffic: %v", leaked)
		}
	})
}

// A site server killed mid-outage loses nothing it had acked.
func TestSiteServerCrashDuringOutage(t *testing.T) {
	r := robot(t)
	restore := cutWAN(t, "site1")
	publishTelemetry(t, "site1", r, 1, 20)
	docker(t, "kill", "-s", "KILL", "site1")
	docker(t, "start", "site1")
	waitForSite(t, "site1")
	publishTelemetry(t, "site1", r, 21, 30)
	restore()
	waitCloudCount(t, "site1", r, 30, 3*time.Minute)
	assertInOrder(t, "site1", r, 30)
}

// Losing the hub node that leads fleet-uplink in the middle of a catch-up
// loses nothing; another node takes over and the killed node rejoins.
func TestHubLeaderLossDuringCatchUp(t *testing.T) {
	const n = 20000
	r := robot(t)
	restore := cutWAN(t, "site1")
	publishTelemetry(t, "site1", r, 1, n)
	queueCommand(t, "site1", r, r+"-cmd", 15*time.Minute)
	restore()

	// Wait until the copy has started, then kill the leader mid-copy.
	deadline := time.Now().Add(3 * time.Minute)
	for cloudCount(t, "site1", r) <= 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	leader := streamLeader(t, "fleet-uplink")
	copied := cloudCount(t, "site1", r)
	docker(t, "kill", "-s", "KILL", leader)
	t.Cleanup(func() { docker(t, "start", leader) })
	t.Logf("killed %s with %d/%d copied", leader, copied, n)

	waitCloudCount(t, "site1", r, n, 3*time.Minute)
	assertInOrder(t, "site1", r, n)
	if newLeader := streamLeader(t, "fleet-uplink"); newLeader == leader {
		t.Fatalf("leader did not change")
	}
	ids, err := pullCommands(t, "site1")
	if err != nil || !slices.Contains(ids, r+"-cmd") {
		t.Fatalf("queued command not delivered after node loss: %v %v", ids, err)
	}
}

// The site-to-cloud copy moves one NATS message per event, not batches.
// Measured with the site server's own counters for its leaf connection.
// out_msgs counts messages written to the connection: during a cut the site
// keeps writing into the dead connection (up to the 2 MB flow-control window)
// until pings notice, so the baseline is taken before the cut. If the leaf
// reconnects instead, the copy resumes on the new connection from the last
// event the cloud stored, so the new connection's counters cover it.
func TestUplinkCopiesEventByEvent(t *testing.T) {
	const n = 2000
	r := robot(t)
	msgs0, bytes0 := leafOut(t, "site1")
	restore := cutWAN(t, "site1")
	publishTelemetry(t, "site1", r, 1, n)
	restore()
	waitCloudCount(t, "site1", r, n, 3*time.Minute)
	msgs1, bytes1 := leafOut(t, "site1")
	if msgs1 < msgs0 { // the leaf reconnected, so its counters restarted
		msgs0, bytes0 = 0, 0
	}
	msgs, bytes := msgs1-msgs0, bytes1-bytes0
	t.Logf("leaf sent %d messages, %d bytes (%.0f bytes/msg) for %d events", msgs, bytes, float64(bytes)/float64(msgs), n)
	// Batching would send fewer messages than events. More than n is normal:
	// heartbeats and flow control, plus a resend of everything written into
	// the dead connection when the copy restarts from the cloud's last event.
	// The cloud still stores each event exactly once (checked below).
	if msgs < n {
		t.Fatalf("leaf sent %d messages for %d events: fewer messages than events means batching", msgs, n)
	}
	time.Sleep(2 * time.Second)
	if got := cloudCount(t, "site1", r); got != n {
		t.Fatalf("cloud stored %d events, want exactly %d", got, n)
	}
	assertInOrder(t, "site1", r, n)
}

// The hub reports a site going offline and coming back as system events,
// with the site's certificate identity. This is the site-health signal.
func TestSiteOfflineEvent(t *testing.T) {
	nc := connect(t, hubURLs, "sys", env("SYS_PASS", "dev-sys"))
	events := make(chan string, 10)
	sub, _ := nc.Subscribe("$SYS.ACCOUNT.site1.*", func(m *nats.Msg) {
		var ev struct {
			Client struct{ Kind, User string } `json:"client"`
		}
		json.Unmarshal(m.Data, &ev)
		if ev.Client.Kind == "Leafnode" {
			events <- m.Subject + " " + ev.Client.User
		}
	})
	defer sub.Unsubscribe()
	nc.Flush()

	restore := cutWAN(t, "site1")
	expectEvent(t, events, "$SYS.ACCOUNT.site1.DISCONNECT CN=site1,O=fleet", 90*time.Second)
	restore()
	expectEvent(t, events, "$SYS.ACCOUNT.site1.CONNECT CN=site1,O=fleet", 90*time.Second)
}

func expectEvent(t *testing.T, events chan string, want string, timeout time.Duration) {
	t.Helper()
	start := time.Now()
	deadline := time.After(timeout)
	for {
		select {
		case got := <-events:
			if got == want {
				t.Logf("%s after %s", want, time.Since(start).Round(time.Second))
				return
			}
		case <-deadline:
			t.Fatalf("no %q within %s", want, timeout)
		}
	}
}
