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

// Endpoints published by compose.yaml, credentials are its dev defaults.
const (
	hubURLs = "tls://localhost:4222,tls://localhost:4232,tls://localhost:4242"
	caFile  = "../certs/ca.crt"
	// Sites reach their command consumer in the cloud through this prefix;
	// the hub maps it onto the fleet account (scripts/gen-hub-accounts.sh).
	cloudAPI = "_fleet.api"
)

var (
	siteURL     = map[string]string{"site1": "nats://localhost:4223", "site2": "nats://localhost:4224"}
	siteMonitor = map[string]string{"site1": "http://localhost:8223", "site2": "http://localhost:8224"}
)

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
func edgeApp(t *testing.T, site string) *nats.Conn {
	return connect(t, siteURL[site], "edge-app", env("EDGE_APP_PASS", "dev-edge"))
}
func siteOps(t *testing.T, site string) *nats.Conn {
	return connect(t, siteURL[site], "site-ops", env("SITE_OPS_PASS", "dev-ops"))
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

// robot returns a robot id unique to this test run, so tests don't see each other's data.
func robot(t *testing.T) string {
	return fmt.Sprintf("%s-%d", strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")), time.Now().UnixNano()%1e6)
}

func telemetry(site, robot string) string { return "fleet." + site + "." + robot + ".telemetry" }
func command(site, robot string) string   { return "fleet." + site + "." + robot + ".command" }

// publishTelemetry publishes events seq from..to as the edge app; each one is
// acked by the site's own server (its local stream), not the cloud.
func publishTelemetry(t *testing.T, site, robot string, from, to int) {
	t.Helper()
	js, _ := jetstream.New(edgeApp(t, site))
	for i := from; i <= to; i++ {
		_, err := js.Publish(ctx(t, 5*time.Second), telemetry(site, robot), []byte(fmt.Sprintf(`{"seq":%d}`, i)),
			jetstream.WithMsgID(fmt.Sprintf("%s-%d", robot, i)), jetstream.WithExpectStream(site+"-uplink"))
		if err != nil {
			t.Fatalf("publish seq %d: %v", i, err)
		}
	}
}

// cloudCount returns how many events for this robot are in fleet-uplink.
func cloudCount(t *testing.T, site, robot string) int {
	t.Helper()
	js, _ := jetstream.New(cloudApp(t))
	st, err := js.Stream(ctx(t, 5*time.Second), "fleet-uplink")
	if err != nil {
		return -1
	}
	subj := telemetry(site, robot)
	info, err := st.Info(ctx(t, 5*time.Second), jetstream.WithSubjectFilter(subj))
	if err != nil {
		return -1
	}
	return int(info.State.Subjects[subj]) // State.Msgs is the whole stream, not the filter
}

// waitCloudCount polls until the cloud has want events. After an outage the
// copy can start late: a failing source retries with a growing backoff,
// capped at 2 minutes (nats-server stream.go).
func waitCloudCount(t *testing.T, site, robot string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	got := 0
	for time.Now().Before(deadline) {
		if got = cloudCount(t, site, robot); got >= want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("cloud has %d/%d events for %s after %s", got, want, robot, timeout)
}

// assertInOrder reads this robot's events from fleet-uplink and checks seq 1..n, no gaps.
func assertInOrder(t *testing.T, site, robot string, n int) {
	t.Helper()
	js, _ := jetstream.New(cloudApp(t))
	st, err := js.Stream(ctx(t, 5*time.Second), "fleet-uplink")
	if err != nil {
		t.Fatal(err)
	}
	// Plain ephemeral consumer, retried: for ~3 minutes after a hub node dies,
	// about 1 in 3 ephemeral R1 creates time out (placed on the dead peer).
	var c jetstream.Consumer
	for try := 0; try < 10; try++ {
		c, err = st.CreateConsumer(ctx(t, 2*time.Second), jetstream.ConsumerConfig{
			FilterSubject: telemetry(site, robot), AckPolicy: jetstream.AckNonePolicy,
			MemoryStorage: true, Replicas: 1, InactiveThreshold: 30 * time.Second})
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	next := 1
	for next <= n {
		batch, err := c.Fetch(1000, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		for m := range batch.Messages() {
			var v struct{ Seq int }
			json.Unmarshal(m.Data(), &v)
			if v.Seq != next {
				t.Fatalf("expected seq %d, got %d", next, v.Seq)
			}
			next++
			got++
		}
		if got == 0 {
			t.Fatalf("stopped at seq %d of %d", next-1, n)
		}
	}
}

// queueCommand publishes a command from the cloud with a per-message TTL.
func queueCommand(t *testing.T, site, robot, id string, ttl time.Duration) {
	t.Helper()
	js, _ := jetstream.New(cloudApp(t))
	_, err := js.Publish(ctx(t, 5*time.Second), command(site, robot), []byte(`{"commandId":"`+id+`"}`),
		jetstream.WithMsgID(id), jetstream.WithMsgTTL(ttl))
	if err != nil {
		t.Fatalf("queue command %s: %v", id, err)
	}
}

// pullCommands fetches and acks this site's pending commands, returning their ids.
func pullCommands(t *testing.T, site string) ([]string, error) {
	t.Helper()
	js, _ := jetstream.NewWithAPIPrefix(edgeApp(t, site), cloudAPI)
	cons, err := js.Consumer(ctx(t, 5*time.Second), "fleet-commands", site+"-commands")
	if err != nil {
		return nil, err
	}
	batch, err := cons.Fetch(100, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		return nil, err
	}
	var ids []string
	for m := range batch.Messages() {
		var v struct{ CommandID string }
		json.Unmarshal(m.Data(), &v)
		ids = append(ids, v.CommandID)
		if err := m.DoubleAck(ctx(t, 5*time.Second)); err != nil {
			return ids, err
		}
	}
	return ids, batch.Error()
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

func mustAPI(t *testing.T, nc *nats.Conn) jetstream.JetStream {
	t.Helper()
	js, err := jetstream.NewWithAPIPrefix(nc, cloudAPI)
	if err != nil {
		t.Fatal(err)
	}
	return js
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
