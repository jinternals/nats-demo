// The Kafka side of the cloud: cloud services publish commands to Kafka and
// read site events from it; nats-io/nats-kafka moves them to and from the hub.
package e2e

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Down and back up through Kafka: a command produced to Kafka
// cloud.to.site.site1.command reaches a device at site1, and the device's
// result arrives in Kafka site.to.cloud.result keyed by site1.
func TestKafkaCommandToDeviceAndResultBack(t *testing.T) {
	drainCommands(t, "site1")
	dev := deviceID(t)
	got := fakeDevice(t, "site1", dev)
	m := markKafka(t, upTopic("result"))
	id := dev + "-cmd"

	produceCommand(t, "site1", id, "Nats-TTL", "15m")
	waitDelivered(t, "site1", id, func(command) string { return dev }, 30*time.Second)
	select {
	case c := <-got:
		if c != id {
			t.Fatalf("device got %s, want %s", c, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("device never received the command")
	}

	recs := readKafka(t, m, time.Minute, func(rs []*kgo.Record) bool { return len(eventsOf(rs, dev)) > 0 })
	r := eventsOf(recs, dev)[0]
	var res struct{ CommandID string }
	json.Unmarshal(r.Value, &res)
	if res.CommandID != id {
		t.Fatalf("result for %q, want %q", res.CommandID, id)
	}
	if string(r.Key) != "site1" {
		t.Fatalf("result key %q, want site1", r.Key)
	}
}

// Every up type lands on its own topic, keyed by site, and the device id
// never appears in the Kafka key (only in the payload).
func TestKafkaEventTopicsAndKeys(t *testing.T) {
	dev := deviceID(t)
	types := []string{"telemetry", "health", "result"}
	topics := []string{}
	for _, typ := range types {
		topics = append(topics, upTopic(typ))
	}
	m := markKafka(t, topics...)
	for _, typ := range types {
		publishEvents(t, "site2", dev, typ, 1, 3)
	}
	recs := readKafka(t, m, time.Minute, func(rs []*kgo.Record) bool { return len(eventsOf(rs, dev)) >= 9 })
	perTopic := map[string]int{}
	for _, r := range eventsOf(recs, dev) {
		perTopic[r.Topic]++
		if string(r.Key) != "site2" {
			t.Fatalf("%s: key %q, want site2", r.Topic, r.Key)
		}
		if strings.Contains(string(r.Key), dev) {
			t.Fatalf("%s: key names the device", r.Topic)
		}
	}
	for _, topic := range topics {
		if perTopic[topic] != 3 {
			t.Fatalf("%s has %d events from %s, want 3 (all: %v)", topic, perTopic[topic], dev, perTopic)
		}
	}
}

// Events published at a site during a WAN outage reach Kafka in order, once
// each, on one partition, after the link comes back.
func TestKafkaUplinkSurvivesWANOutage(t *testing.T) {
	dev := deviceID(t)
	m := markKafka(t, upTopic("telemetry"))
	restore := cutWAN(t, "site1")
	publishEvents(t, "site1", dev, "telemetry", 1, 50) // acked by site1's disk
	restore()
	recs := readKafka(t, m, 4*time.Minute, func(rs []*kgo.Record) bool { return len(eventsOf(rs, dev)) >= 50 })
	assertKafkaInOrder(t, recs, "site1", dev, 50)
}

// The bridge killed with -9: nothing is lost in either direction. Events wait
// in cloud-inbox, commands wait in Kafka, and both move once it's back.
func TestKafkaBridgeCrashLosesNothing(t *testing.T) {
	drainCommands(t, "site1")
	dev := deviceID(t)
	got := fakeDevice(t, "site1", dev)
	m := markKafka(t, upTopic("telemetry"))

	docker(t, "kill", "-s", "KILL", "kafka-bridge")
	t.Cleanup(func() { docker(t, "start", "kafka-bridge") })
	publishEvents(t, "site1", dev, "telemetry", 1, 30)
	produceCommand(t, "site1", dev+"-cmd")
	time.Sleep(2 * time.Second)
	if ids, _ := runController(t, "site1", func(command) string { return dev }); len(ids) != 0 {
		t.Fatalf("delivered %v with the bridge down", ids)
	}
	docker(t, "start", "kafka-bridge")

	waitDelivered(t, "site1", dev+"-cmd", func(command) string { return dev }, time.Minute)
	if c := <-got; c != dev+"-cmd" {
		t.Fatalf("device got %s", c)
	}
	recs := readKafka(t, m, 2*time.Minute, func(rs []*kgo.Record) bool { return len(eventsOf(rs, dev)) >= 30 })
	assertKafkaInOrder(t, recs, "site1", dev, 30)
}

// The bridge's NATS user can only do its job: no device hop, no deleting
// streams, no pulling site command consumers, no direct pub/sub on site.to.cloud.
func TestKafkaBridgeRoleIsLimited(t *testing.T) {
	bridge := func() *nats.Conn { return connect(t, hubURLs, "kafka-bridge", env("KAFKA_BRIDGE_PASS", "dev-bridge")) }
	expectDenied(t, bridge(), "publish", siteToCloud("site1", "telemetry"))
	expectDenied(t, bridge(), "publish", siteToDevice("arm-01", "command"))
	expectDenied(t, bridge(), "publish", "$JS.API.STREAM.DELETE.cloud-inbox")
	expectDenied(t, bridge(), "publish", "$JS.API.CONSUMER.MSG.NEXT.cloud-outbox.site1-controller")
	expectDenied(t, bridge(), "subscribe", "site.to.cloud.>")
}

// Kafka topics follow the naming rules in docs/naming.md:
//   down: cloud.to.site.<site>.command
//   up:   site.to.cloud.<type>
func TestKafkaTopicNames(t *testing.T) {
	topics, err := kadm.NewClient(kafkaClient(t)).ListTopics(ctx(t, 10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	downPat := regexp.MustCompile(`^cloud\.to\.site\.([a-z0-9][a-z0-9-]*)\.command$`)
	upPat := regexp.MustCompile(`^site\.to\.cloud\.([a-z]+)$`)
	var names []string
	for _, name := range topics.Names() {
		if strings.HasPrefix(name, "_") { // Kafka's own (__consumer_offsets)
			continue
		}
		names = append(names, name)
		if downPat.MatchString(name) {
			continue
		}
		if m := upPat.FindStringSubmatch(name); m != nil {
			if !slices.Contains(hops["site.to.cloud"], m[1]) {
				t.Errorf("topic %s: type %s not allowed on site.to.cloud (allowed: %v)", name, m[1], hops["site.to.cloud"])
			}
			continue
		}
		t.Errorf("topic %s: want cloud.to.site.<site>.command or site.to.cloud.<type>", name)
	}
	for _, want := range []string{
		commandTopic("site1"), commandTopic("site2"),
		upTopic("telemetry"), upTopic("health"), upTopic("result"),
	} {
		if !slices.Contains(names, want) {
			t.Errorf("topic %s missing (have %v)", want, names)
		}
	}
}
