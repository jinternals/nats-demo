package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Kafka as published by compose.yaml. Topic names: docs/naming.md, "Kafka".
const kafkaBrokers = "localhost:9094"

func commandTopic(site string) string { return "cloud.to.site." + site + ".command" }
func upTopic(typ string) string       { return "site.to.cloud." + typ }

func kafkaClient(t *testing.T, opts ...kgo.Opt) *kgo.Client {
	t.Helper()
	cl, err := kgo.NewClient(append([]kgo.Opt{kgo.SeedBrokers(kafkaBrokers)}, opts...)...)
	if err != nil {
		t.Fatalf("kafka: %v (is the stack up? run `make up`)", err)
	}
	t.Cleanup(cl.Close)
	return cl
}

// produceCommand plays a cloud service: it publishes a command for site to
// Kafka cloud.to.site.<site>.command. Extra headers are key/value pairs.
func produceCommand(t *testing.T, site, id string, headers ...string) {
	t.Helper()
	value, _ := json.Marshal(command{CommandID: id})
	r := &kgo.Record{Topic: commandTopic(site), Key: []byte(site), Value: value}
	r.Headers = append(r.Headers,
		kgo.RecordHeader{Key: "siteId", Value: []byte(site)},
		kgo.RecordHeader{Key: "Nats-Msg-Id", Value: []byte(id)},
	)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Headers = append(r.Headers, kgo.RecordHeader{Key: headers[i], Value: []byte(headers[i+1])})
	}
	if err := kafkaClient(t).ProduceSync(ctx(t, 10*time.Second), r).FirstErr(); err != nil {
		t.Fatalf("produce command %s: %v", id, err)
	}
}

// kmark records the end of each partition of the given topics, so a test
// reads only what was produced after it.
type kmark map[string]map[int32]kgo.Offset

func markKafka(t *testing.T, topics ...string) kmark {
	t.Helper()
	ends, err := kadm.NewClient(kafkaClient(t)).ListEndOffsets(ctx(t, 10*time.Second), topics...)
	if err != nil {
		t.Fatalf("list end offsets: %v", err)
	}
	m := kmark{}
	for topic, parts := range ends {
		m[topic] = map[int32]kgo.Offset{}
		for p, o := range parts {
			if o.Err != nil {
				t.Fatalf("end offset %s/%d: %v", topic, p, o.Err)
			}
			m[topic][p] = kgo.NewOffset().At(o.Offset)
		}
	}
	return m
}

// readKafka reads records produced since m until done says it has enough,
// or fails the test after timeout.
func readKafka(t *testing.T, m kmark, timeout time.Duration, done func([]*kgo.Record) bool) []*kgo.Record {
	t.Helper()
	cl := kafkaClient(t, kgo.ConsumePartitions(m))
	deadline := time.Now().Add(timeout)
	var got []*kgo.Record
	for time.Now().Before(deadline) {
		if done(got) {
			return got
		}
		c := ctx(t, time.Until(deadline))
		fetches := cl.PollFetches(c)
		fetches.EachError(func(topic string, p int32, err error) {
			if c.Err() == nil {
				t.Logf("fetch %s/%d: %v", topic, p, err)
			}
		})
		got = append(got, fetches.Records()...)
	}
	if done(got) {
		return got
	}
	t.Fatalf("not enough records after %s: got %d", timeout, len(got))
	return nil
}

// eventsOf returns this device's events in the order Kafka holds them.
func eventsOf(recs []*kgo.Record, dev string) []*kgo.Record {
	var out []*kgo.Record
	for _, r := range recs {
		var e event
		if json.Unmarshal(r.Value, &e) == nil && e.Device == dev {
			out = append(out, r)
		}
	}
	return out
}

// assertKafkaInOrder checks the device's events are seq 1..n, each once, all
// keyed by the site on one partition.
func assertKafkaInOrder(t *testing.T, recs []*kgo.Record, site, dev string, n int) {
	t.Helper()
	evs := eventsOf(recs, dev)
	if len(evs) != n {
		t.Fatalf("Kafka has %d events from %s, want %d", len(evs), dev, n)
	}
	for i, r := range evs {
		var e event
		json.Unmarshal(r.Value, &e)
		if e.Seq != i+1 {
			t.Fatalf("expected seq %d, got %d", i+1, e.Seq)
		}
		if string(r.Key) != site {
			t.Fatalf("seq %d: key %q, want %s", e.Seq, r.Key, site)
		}
		if r.Partition != evs[0].Partition {
			t.Fatalf("seq %d on partition %d, seq 1 on %d: a site must stay on one partition", e.Seq, r.Partition, evs[0].Partition)
		}
	}
}

// waitDelivered runs the site controller until it has delivered id, or fails.
// The command first has to cross the bridge, so it may take a few tries.
func waitDelivered(t *testing.T, site, id string, route func(command) string, timeout time.Duration) []string {
	t.Helper()
	var ids []string
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d, _ := runController(t, site, route)
		ids = append(ids, d...)
		for _, x := range ids {
			if x == id {
				return ids
			}
		}
	}
	t.Fatalf("command %s not delivered within %s; got %v", id, timeout, ids)
	return nil
}
