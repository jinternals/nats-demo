// End-to-end tests against the stack from `make up`. Run with `make test`.
// They cut WAN links and kill containers, so they run one at a time.
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

// ---------------------------------------------------------------- forwarding rules

// Up rule: device.to.site.<device>.<type> is stored by <site>-outbox as
// site.to.cloud.<site>.<type>, without the device id, and copied to
// cloud-inbox. Types not on the up list never leave the site.
func TestForwardUpDeviceToCloud(t *testing.T) {
	dev := deviceID(t)
	m := markInbox(t)
	for _, typ := range []string{"telemetry", "health", "result"} {
		publishEvents(t, "site1", dev, typ, 1, 3)
		waitArrived(t, m, siteToCloud("site1", typ), 3, 30*time.Second)
		assertInOrder(t, m, "site1", dev, typ, 3)
	}

	// A type not on the up list: no stream stores it, so it can't reach the cloud.
	if _, err := device(t, "site1").Request(deviceToSite(dev, "debug"), []byte("x"), time.Second); !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("device.to.site.*.debug: want nothing to store it, got %v", err)
	}
	// No subject in the cloud names a device.
	for _, msg := range readSince(t, m, ">", 100) {
		if strings.Contains(msg.Subject(), dev) || !strings.HasPrefix(msg.Subject(), "site.to.cloud.site1.") {
			t.Fatalf("cloud stored %s: the cloud must only see site.to.cloud.<site>.<type>", msg.Subject())
		}
	}
}

// Down path: the cloud sends cloud.to.site.<site>.command (no device); the
// site controller picks the device and sends site.to.device.<device>.command;
// the device's result goes back up as site.to.cloud.<site>.result.
func TestForwardDownCloudToDevice(t *testing.T) {
	drainCommands(t, "site1")
	dev := deviceID(t)
	got := fakeDevice(t, "site1", dev)
	m := markInbox(t)
	id := dev + "-cmd"
	queueCommand(t, "site1", id, 15*time.Minute)

	ids, err := runController(t, "site1", func(command) string { return dev })
	if err != nil || !slices.Contains(ids, id) {
		t.Fatalf("controller delivered %v, err %v", ids, err)
	}
	select {
	case c := <-got:
		if c != id {
			t.Fatalf("device got %s, want %s", c, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("device never received the command")
	}
	waitArrived(t, m, siteToCloud("site1", "result"), 1, 30*time.Second)
	var r struct{ CommandID string }
	json.Unmarshal(readSince(t, m, siteToCloud("site1", "result"), 1)[0].Data(), &r)
	if r.CommandID != id {
		t.Fatalf("result for %q, want %q", r.CommandID, id)
	}
	// Acked by the controller, so nothing is left queued for site1.
	if ids, _ := runController(t, "site1", func(command) string { return dev }); len(ids) != 0 {
		t.Fatalf("command redelivered after ack: %v", ids)
	}
}

// ---------------------------------------------------------------- naming is enforced

// Each role can use only its own hop.
func TestRolesUseOnlyTheirHop(t *testing.T) {
	expectDenied(t, device(t, "site1"), "publish", siteToCloud("site1", "telemetry"))
	expectDenied(t, device(t, "site1"), "publish", cloudToSite("site1", "command"))
	expectDenied(t, device(t, "site1"), "publish", siteToDevice("arm-01", "command"))
	expectDenied(t, device(t, "site1"), "subscribe", "device.to.site.>")

	expectDenied(t, controller(t, "site1"), "publish", deviceToSite("arm-01", "telemetry"))
	expectDenied(t, controller(t, "site1"), "publish", siteToCloud("site1", "result"))

	expectDenied(t, cloudApp(t), "publish", siteToCloud("site1", "telemetry"))
	expectDenied(t, cloudApp(t), "publish", siteToDevice("arm-01", "command"))
}

// ---------------------------------------------------------------- reliability

// Events published while the WAN is down are acked by the site and reach the
// cloud, in order, once the link is back.
func TestUplinkSurvivesWANOutage(t *testing.T) {
	dev := deviceID(t)
	subj := siteToCloud("site1", "telemetry")
	m := markInbox(t)
	publishEvents(t, "site1", dev, "telemetry", 1, 10)
	waitArrived(t, m, subj, 10, 30*time.Second)

	restore := cutWAN(t, "site1")
	publishEvents(t, "site1", dev, "telemetry", 11, 30) // acked by site1's disk
	time.Sleep(2 * time.Second)
	if got := arrived(t, m, subj); got != 10 {
		t.Fatalf("during outage the cloud has %d events, want 10", got)
	}

	restore()
	waitArrived(t, m, subj, 30, 3*time.Minute)
	assertInOrder(t, m, "site1", dev, "telemetry", 30)
}

// Commands wait in the cloud during an outage; one whose TTL runs out is
// deleted there and never reaches the site.
func TestStaleCommandsExpireInCloud(t *testing.T) {
	drainCommands(t, "site1")
	dev := deviceID(t)
	got := fakeDevice(t, "site1", dev)
	restore := cutWAN(t, "site1")
	queueCommand(t, "site1", dev+"-fresh", 15*time.Minute)
	queueCommand(t, "site1", dev+"-stale", 5*time.Second)
	time.Sleep(7 * time.Second)
	restore()

	var ids []string
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) && !slices.Contains(ids, dev+"-fresh") {
		d, _ := runController(t, "site1", func(command) string { return dev }) // no responders until site1 reconnects
		ids = append(ids, d...)
	}
	if !slices.Contains(ids, dev+"-fresh") {
		t.Fatalf("fresh command not delivered; got %v", ids)
	}
	if slices.Contains(ids, dev+"-stale") {
		t.Fatal("stale command was delivered")
	}
	if c := <-got; c != dev+"-fresh" {
		t.Fatalf("device got %s", c)
	}
}

// A site server killed mid-outage loses nothing it had acked.
func TestSiteServerCrashDuringOutage(t *testing.T) {
	dev := deviceID(t)
	m := markInbox(t)
	restore := cutWAN(t, "site1")
	publishEvents(t, "site1", dev, "telemetry", 1, 20)
	docker(t, "kill", "-s", "KILL", "site1")
	docker(t, "start", "site1")
	waitForSite(t, "site1")
	publishEvents(t, "site1", dev, "telemetry", 21, 30)
	restore()
	waitArrived(t, m, siteToCloud("site1", "telemetry"), 30, 3*time.Minute)
	assertInOrder(t, m, "site1", dev, "telemetry", 30)
}

// Losing the hub node that leads cloud-inbox in the middle of a catch-up
// loses nothing; another node takes over.
func TestHubLeaderLossDuringCatchUp(t *testing.T) {
	const n = 20000
	drainCommands(t, "site1")
	dev := deviceID(t)
	subj := siteToCloud("site1", "telemetry")
	m := markInbox(t)
	restore := cutWAN(t, "site1")
	publishEvents(t, "site1", dev, "telemetry", 1, n)
	queueCommand(t, "site1", dev+"-cmd", 15*time.Minute)
	restore()

	deadline := time.Now().Add(3 * time.Minute)
	for arrived(t, m, subj) <= 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	leader := streamLeader(t, "cloud-inbox")
	copied := arrived(t, m, subj)
	docker(t, "kill", "-s", "KILL", leader)
	t.Cleanup(func() { docker(t, "start", leader) })
	t.Logf("killed %s with %d/%d copied", leader, copied, n)

	waitArrived(t, m, subj, n, 3*time.Minute)
	assertInOrder(t, m, "site1", dev, "telemetry", n)
	if streamLeader(t, "cloud-inbox") == leader {
		t.Fatal("leader did not change")
	}
	fakeDevice(t, "site1", dev)
	ids, err := runController(t, "site1", func(command) string { return dev })
	if err != nil || !slices.Contains(ids, dev+"-cmd") {
		t.Fatalf("queued command not delivered after node loss: %v %v", ids, err)
	}
}

// ---------------------------------------------------------------- isolation

// Each site sees only its own data. Isolation comes from per-site hub accounts.
func TestSitesAreIsolated(t *testing.T) {
	t.Run("site cannot pull another site's commands", func(t *testing.T) {
		// site-ops has full rights on site1's own server, so only the hub stops it.
		js, _ := newEdgeAPI(siteOps(t, "site1"))
		_, err := js.Consumer(ctx(t, 5*time.Second), "cloud-outbox", "site2-controller")
		if !errors.Is(err, nats.ErrNoResponders) {
			t.Fatalf("want no responders (not imported into site1's account), got %v", err)
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
		drainCommands(t, "site2")
		dev := deviceID(t)
		fakeDevice(t, "site2", dev)
		m := markInbox(t)
		publishEvents(t, "site2", dev, "telemetry", 1, 20)
		queueCommand(t, "site2", dev+"-cmd", time.Minute)
		runController(t, "site2", func(command) string { return dev })
		waitArrived(t, m, siteToCloud("site2", "telemetry"), 20, time.Minute)
		time.Sleep(time.Second)
		mu.Lock()
		defer mu.Unlock()
		if len(leaked) > 0 {
			t.Fatalf("site1 saw site2 traffic: %v", leaked)
		}
	})
}

// ---------------------------------------------------------------- delivery and monitoring

// The site-to-cloud copy moves one NATS message per event, not batches.
// Measured with the site server's own counters for its leaf connection.
// out_msgs counts messages written to the connection: during a cut the site
// keeps writing into the dead connection (up to the 2 MB flow-control window)
// until pings notice, so the baseline is taken before the cut.
func TestUplinkCopiesEventByEvent(t *testing.T) {
	const n = 2000
	dev := deviceID(t)
	subj := siteToCloud("site1", "telemetry")
	m := markInbox(t)
	msgs0, bytes0 := leafOut(t, "site1")
	restore := cutWAN(t, "site1")
	publishEvents(t, "site1", dev, "telemetry", 1, n)
	restore()
	waitArrived(t, m, subj, n, 3*time.Minute)
	msgs1, bytes1 := leafOut(t, "site1")
	if msgs1 < msgs0 { // the leaf reconnected, so its counters restarted
		msgs0, bytes0 = 0, 0
	}
	msgs, bytes := msgs1-msgs0, bytes1-bytes0
	t.Logf("leaf sent %d messages, %d bytes (%.0f bytes/msg) for %d events", msgs, bytes, float64(bytes)/float64(msgs), n)
	// Batching would send fewer messages than events. More than n is normal:
	// heartbeats and flow control, plus a resend of anything written into the
	// dead connection. The cloud still stores each event exactly once.
	if msgs < n {
		t.Fatalf("leaf sent %d messages for %d events: fewer messages than events means batching", msgs, n)
	}
	time.Sleep(2 * time.Second)
	if got := arrived(t, m, subj); got != n {
		t.Fatalf("cloud stored %d events, want exactly %d", got, n)
	}
	assertInOrder(t, m, "site1", dev, "telemetry", n)
}

// The hub reports a site going offline and coming back as system events,
// with the site's certificate identity.
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
