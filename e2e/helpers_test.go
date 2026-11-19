package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Endpoints published by compose.yaml; credentials are its dev defaults.
const (
	hubURLs = "tls://localhost:4222,tls://localhost:4232,tls://localhost:4242"
	caFile  = "../certs/ca.crt"
	// A site controller reaches its consumer on cloud-outbox through this
	// prefix; the hub maps it into the cloud account (scripts/generate.sh).
	edgeAPI = "_edge.api"
)

var (
	siteURL     = map[string]string{"site1": "nats://localhost:4223", "site2": "nats://localhost:4224"}
	siteMonitor = map[string]string{"site1": "http://localhost:8223", "site2": "http://localhost:8224"}
)

// Subjects, one function per hop (docs/naming.md).
func cloudToSite(site, typ string) string    { return "cloud.to.site." + site + "." + typ }
func siteToCloud(site, typ string) string    { return "site.to.cloud." + site + "." + typ }
func siteToDevice(device, typ string) string { return "site.to.device." + device + "." + typ }
func deviceToSite(device, typ string) string { return "device.to.site." + device + "." + typ }

// The event payload carries the device id and a sequence number. The subject
// the cloud sees has no device id; tests read it from the payload.
type event struct {
	Device string `json:"device"`
	Seq    int    `json:"seq"`
}

type command struct {
	CommandID string `json:"commandId"`
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func connect(t *testing.T, url, user, pass string, opts ...nats.Option) *nats.Conn {
	t.Helper()
	opts = append(opts, nats.UserInfo(user, pass), nats.Timeout(3*time.Second), nats.MaxReconnects(-1))
	if strings.HasPrefix(url, "tls://") {
		opts = append(opts, nats.RootCAs(caFile))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		t.Fatalf("connect %s as %s: %v (is the stack up? run `make up`)", url, user, err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func cloudApp(t *testing.T) *nats.Conn {
	return connect(t, hubURLs, "cloud-app", env("CLOUD_APP_PASS", "dev-cloud"))
}
func device(t *testing.T, site string) *nats.Conn {
	return connect(t, siteURL[site], "device", env("DEVICE_PASS", "dev-device"))
}
func controller(t *testing.T, site string) *nats.Conn {
	return connect(t, siteURL[site], "site-controller", env("SITE_CONTROLLER_PASS", "dev-controller"))
}
func siteOps(t *testing.T, site string) *nats.Conn {
	return connect(t, siteURL[site], "site-ops", env("SITE_OPS_PASS", "dev-ops"))
}

func newEdgeAPI(nc *nats.Conn) (jetstream.JetStream, error) {
	return jetstream.NewWithAPIPrefix(nc, edgeAPI)
}

func ctx(t *testing.T, d time.Duration) context.Context {
	c, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return c
}

func docker(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// cutWAN disconnects a site from the WAN network and restores it when the test ends.
func cutWAN(t *testing.T, site string) (restore func()) {
	docker(t, "network", "disconnect", "nats-edge-wan", site)
	restored := false
	restore = func() {
		if !restored {
			restored = true
			docker(t, "network", "connect", "nats-edge-wan", site)
		}
	}
	t.Cleanup(restore)
	return restore
}

// deviceID returns a device id unique to this test run.
func deviceID(t *testing.T) string {
	name := strings.ToLower(strings.TrimPrefix(t.Name(), "Test"))
	name = strings.NewReplacer("/", "-", "_", "-", " ", "-", "'", "").Replace(name)
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano()%1e6)
}

// publishEvents publishes events seq from..to as a device. Each is acked by
// the site's own server (<site>-outbox), not by the cloud.
func publishEvents(t *testing.T, site, dev, typ string, from, to int) {
	t.Helper()
	js, _ := jetstream.New(device(t, site))
	for i := from; i <= to; i++ {
		data, _ := json.Marshal(event{Device: dev, Seq: i})
		_, err := js.Publish(ctx(t, 5*time.Second), deviceToSite(dev, typ), data,
			jetstream.WithMsgID(fmt.Sprintf("%s-%s-%d", dev, typ, i)), jetstream.WithExpectStream(site+"-outbox"))
		if err != nil {
			t.Fatalf("publish %s seq %d: %v", deviceToSite(dev, typ), i, err)
		}
	}
}

func cloudInbox(t *testing.T) jetstream.Stream {
	t.Helper()
	js, _ := jetstream.New(cloudApp(t))
	st, err := js.Stream(ctx(t, 5*time.Second), "cloud-inbox")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// mark records where cloud-inbox stands, so a test counts and reads only
// what arrived after it. Tests run one at a time.
type mark struct {
	lastSeq uint64
	count   map[string]uint64
}

func markInbox(t *testing.T) mark {
	t.Helper()
	info, err := cloudInbox(t).Info(ctx(t, 5*time.Second), jetstream.WithSubjectFilter("site.to.cloud.>"))
	if err != nil {
		t.Fatal(err)
	}
	return mark{info.State.LastSeq, info.State.Subjects}
}

// arrived returns how many messages on subj reached cloud-inbox since m,
// or -1 if the hub didn't answer (e.g. while a stream leader changes).
func arrived(t *testing.T, m mark, subj string) int {
	t.Helper()
	js, _ := jetstream.New(cloudApp(t))
	st, err := js.Stream(ctx(t, 5*time.Second), "cloud-inbox")
	if err != nil {
		return -1
	}
	info, err := st.Info(ctx(t, 5*time.Second), jetstream.WithSubjectFilter(subj))
	if err != nil {
		return -1
	}
	return int(info.State.Subjects[subj] - m.count[subj])
}

// waitArrived polls until want messages on subj arrived since m. After an
// outage the copy can start late: a failing source retries with a growing
// backoff, capped at 2 minutes (nats-server stream.go).
func waitArrived(t *testing.T, m mark, subj string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	got := 0
	for time.Now().Before(deadline) {
		if got = arrived(t, m, subj); got >= want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%d/%d messages on %s after %s", got, want, subj, timeout)
}

// readSince returns messages on subj in cloud-inbox after m, in stream order.
func readSince(t *testing.T, m mark, subj string, max int) []jetstream.Msg {
	t.Helper()
	st := cloudInbox(t)
	// Created with a short timeout and retried: for a few minutes after a hub
	// node dies, some short-lived (R1) consumer creates time out.
	var c jetstream.Consumer
	var err error
	for try := 0; try < 10; try++ {
		c, err = st.CreateConsumer(ctx(t, 2*time.Second), jetstream.ConsumerConfig{
			FilterSubject: subj, DeliverPolicy: jetstream.DeliverByStartSequencePolicy, OptStartSeq: m.lastSeq + 1,
			AckPolicy: jetstream.AckNonePolicy, MemoryStorage: true, Replicas: 1, InactiveThreshold: 30 * time.Second})
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []jetstream.Msg
	for len(out) < max {
		batch, err := c.Fetch(1000, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for msg := range batch.Messages() {
			out = append(out, msg)
			n++
		}
		if n == 0 {
			break
		}
	}
	return out
}

// assertInOrder checks this device's events arrived as seq 1..n, no gaps or repeats.
func assertInOrder(t *testing.T, m mark, site, dev, typ string, n int) {
	t.Helper()
	next := 1
	for _, msg := range readSince(t, m, siteToCloud(site, typ), 1<<30) {
		var e event
		json.Unmarshal(msg.Data(), &e)
		if e.Device != dev {
			continue
		}
		if e.Seq != next {
			t.Fatalf("expected seq %d, got %d", next, e.Seq)
		}
		next++
	}
	if next-1 != n {
		t.Fatalf("cloud has %d events from %s, want %d", next-1, dev, n)
	}
}

// queueCommand publishes a command for a site from the cloud, with a TTL.
// The cloud names only the site; the site controller picks the device.
func queueCommand(t *testing.T, site, id string, ttl time.Duration) {
	t.Helper()
	js, _ := jetstream.New(cloudApp(t))
	data, _ := json.Marshal(command{CommandID: id})
	_, err := js.Publish(ctx(t, 5*time.Second), cloudToSite(site, "command"), data,
		jetstream.WithMsgID(id), jetstream.WithMsgTTL(ttl))
	if err != nil {
		t.Fatalf("queue command %s: %v", id, err)
	}
}

// runController plays the site controller once: pulls the site's pending
// commands from the cloud, sends each to the device chosen by route as a
// request on site.to.device.<device>.command, and acks it in the cloud only
// after the device confirmed. Returns the ids it delivered.
func runController(t *testing.T, site string, route func(command) string) ([]string, error) {
	t.Helper()
	nc := controller(t, site)
	js, _ := newEdgeAPI(nc)
	cons, err := js.Consumer(ctx(t, 5*time.Second), "cloud-outbox", site+"-controller")
	if err != nil {
		return nil, err
	}
	batch, err := cons.Fetch(100, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		return nil, err
	}
	var ids []string
	for msg := range batch.Messages() {
		var c command
		json.Unmarshal(msg.Data(), &c)
		dev := route(c)
		if _, err := nc.Request(siteToDevice(dev, "command"), msg.Data(), 5*time.Second); err != nil {
			msg.Nak()
			return ids, fmt.Errorf("device %s: %w", dev, err)
		}
		if err := msg.DoubleAck(ctx(t, 5*time.Second)); err != nil {
			return ids, err
		}
		ids = append(ids, c.CommandID)
	}
	return ids, batch.Error()
}

// drainCommands acks everything pending for a site, so a test starts clean.
func drainCommands(t *testing.T, site string) {
	t.Helper()
	js, _ := newEdgeAPI(controller(t, site))
	cons, err := js.Consumer(ctx(t, 5*time.Second), "cloud-outbox", site+"-controller")
	if err != nil {
		t.Fatalf("drain %s: %v", site, err)
	}
	batch, _ := cons.Fetch(1000, jetstream.FetchMaxWait(time.Second))
	for msg := range batch.Messages() {
		msg.DoubleAck(ctx(t, 5*time.Second))
	}
}

// fakeDevice answers commands on site.to.device.<dev>.command and reports a
// result as device.to.site.<dev>.result. Received ids go to the channel.
func fakeDevice(t *testing.T, site, dev string) <-chan string {
	t.Helper()
	nc := device(t, site)
	js, _ := jetstream.New(nc)
	got := make(chan string, 100)
	_, err := nc.Subscribe(siteToDevice(dev, "command"), func(m *nats.Msg) {
		var c command
		json.Unmarshal(m.Data, &c)
		m.Respond([]byte("ok"))
		res, _ := json.Marshal(map[string]string{"device": dev, "commandId": c.CommandID, "status": "done"})
		js.Publish(context.Background(), deviceToSite(dev, "result"), res, jetstream.WithMsgID(c.CommandID+"-result"))
		got <- c.CommandID
	})
	if err != nil {
		t.Fatal(err)
	}
	nc.Flush()
	return got
}

// streamLeader returns the hub node (= container name) leading a stream.
func streamLeader(t *testing.T, stream string) string {
	t.Helper()
	js, _ := jetstream.New(cloudApp(t))
	st, err := js.Stream(ctx(t, 5*time.Second), stream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx(t, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return info.Cluster.Leader
}

// waitForSite blocks until a restarted site server accepts connections.
func waitForSite(t *testing.T, site string) {
	t.Helper()
	for i := 0; i < 60; i++ {
		if nc, err := nats.Connect(siteURL[site], nats.UserInfo("site-ops", env("SITE_OPS_PASS", "dev-ops"))); err == nil {
			nc.Close()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s did not come back", site)
}

// leafOut returns messages and bytes the site has sent over its current leaf
// connection to the hub (from the server's /leafz monitoring endpoint).
func leafOut(t *testing.T, site string) (msgs, bytes int64) {
	t.Helper()
	resp, err := http.Get(siteMonitor[site] + "/leafz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var z struct {
		Leafs []struct {
			OutMsgs  int64 `json:"out_msgs"`
			OutBytes int64 `json:"out_bytes"`
		} `json:"leafs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&z); err != nil || len(z.Leafs) != 1 {
		t.Fatalf("leafz: %v (%d leafs)", err, len(z.Leafs))
	}
	return z.Leafs[0].OutMsgs, z.Leafs[0].OutBytes
}

// expectDenied publishes (or subscribes) on a fresh connection and expects a
// permissions violation.
func expectDenied(t *testing.T, nc *nats.Conn, action, subj string) {
	t.Helper()
	errs := make(chan error, 4)
	nc.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errs <- err })
	if action == "publish" {
		nc.Publish(subj, []byte("x"))
	} else {
		sub, _ := nc.SubscribeSync(subj)
		defer sub.Unsubscribe()
	}
	nc.Flush()
	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "Permissions Violation") {
			t.Fatalf("%s %s: unexpected error %v", action, subj, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s %s was allowed", action, subj)
	}
}
