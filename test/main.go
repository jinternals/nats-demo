// Test harness for the cloud-hub / site-leaf design.
// usage: harness <cmd> [args]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const root = "/home/claude/nats-edge"

func must(err error) {
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
}

func conn(url, user, pass string, tls bool) *nats.Conn {
	opts := []nats.Option{nats.UserInfo(user, pass), nats.Timeout(3 * time.Second)}
	if tls {
		opts = append(opts, nats.RootCAs(root+"/certs/ca.crt"))
	}
	nc, err := nats.Connect(url, opts...)
	must(err)
	return nc
}

func cloud() *nats.Conn { return conn("tls://localhost:4222", "cloud-app", "cloudpw", true) }
func site(n string) string {
	return map[string]string{"site1": "nats://localhost:4223", "site2": "nats://localhost:4224"}[n]
}
func edge(s string) *nats.Conn { return conn(site(s), "edge-app", "edgepw", false) }
func ops(s string) *nats.Conn  { return conn(site(s), "site-ops", "opspw", false) }

func loadStream(name string, replicas int) jetstream.StreamConfig {
	b, err := os.ReadFile(root + "/streams/" + name + ".json")
	must(err)
	var c jetstream.StreamConfig
	must(json.Unmarshal(b, &c))
	c.Replicas = replicas // single-node test
	return c
}

func ctx() context.Context { c, _ := context.WithTimeout(context.Background(), 10*time.Second); return c }

func main() {
	cmd := os.Args[1]
	switch cmd {
	case "setup":
		for _, s := range []string{"site1", "site2"} {
			js, _ := jetstream.New(ops(s))
			_, err := js.CreateOrUpdateStream(ctx(), loadStream(strings.ToUpper(s)+"_UPLINK", 1))
			must(err)
		}
		js, _ := jetstream.New(cloud())
		_, err := js.CreateOrUpdateStream(ctx(), loadStream("FLEET_COMMANDS", 1))
		must(err)
		_, err = js.CreateOrUpdateStream(ctx(), loadStream("FLEET_UPLINK", 1))
		must(err)
		b, _ := os.ReadFile(root + "/streams/site1-commands.consumer.json")
		for _, s := range []string{"site1", "site2"} {
			var cc jetstream.ConsumerConfig
			must(json.Unmarshal([]byte(strings.ReplaceAll(string(b), "site1", s)), &cc))
			_, err = js.CreateOrUpdateConsumer(ctx(), "FLEET_COMMANDS", cc)
			must(err)
		}
		fmt.Println("setup ok")

	case "edge-pub": // edge-pub site from to
		s := os.Args[2]
		from, _ := strconv.Atoi(os.Args[3])
		to, _ := strconv.Atoi(os.Args[4])
		js, _ := jetstream.New(edge(s))
		for i := from; i <= to; i++ {
			subj := fmt.Sprintf("sites.%s.telemetry.arm-01", s)
			_, err := js.Publish(ctx(), subj, []byte(fmt.Sprintf(`{"seq":%d}`, i)),
				jetstream.WithMsgID(fmt.Sprintf("%s-arm-01-%d", s, i)),
				jetstream.WithExpectStream(strings.ToUpper(s)+"_UPLINK"))
			must(err)
		}
		fmt.Printf("edge %s published+acked seq %d..%d (ack from site disk)\n", s, from, to)

	case "cloud-cmd": // cloud-cmd site robot id ttl
		js, _ := jetstream.New(cloud())
		subj := fmt.Sprintf("sites.%s.command.%s.req", os.Args[2], os.Args[3])
		ack, err := js.Publish(ctx(), subj, []byte(`{"commandId":"`+os.Args[4]+`","type":"PICK_ITEM"}`),
			jetstream.WithMsgID(os.Args[4]), jetstream.WithMsgTTL(mustDur(os.Args[5])))
		must(err)
		fmt.Printf("cloud queued %s ttl=%s stream=%s seq=%d\n", os.Args[4], os.Args[5], ack.Stream, ack.Sequence)

	case "edge-pull": // edge-pull site consumerSite
		s, cs := os.Args[2], os.Args[3]
		js, _ := jetstream.NewWithDomain(edge(s), "cloud")
		cons, err := js.Consumer(ctx(), "FLEET_COMMANDS", cs+"-commands")
		must(err)
		batch, err := cons.Fetch(10, jetstream.FetchMaxWait(3*time.Second))
		must(err)
		n := 0
		for m := range batch.Messages() {
			fmt.Printf("edge %s got %s %s\n", s, m.Subject(), string(m.Data()))
			must(m.DoubleAck(ctx()))
			n++
		}
		if batch.Error() != nil {
			fmt.Println("fetch error:", batch.Error())
		}
		fmt.Printf("edge %s pulled %d command(s)\n", s, n)

	case "ops-steal": // full-permission site user tries another site's commands; hub must refuse
		js, _ := jetstream.NewWithDomain(ops(os.Args[2]), "cloud")
		_, err := js.Consumer(ctx(), "FLEET_COMMANDS", os.Args[3]+"-commands")
		fmt.Println("ops-steal result:", err)

	case "edge-inject": // site tries to forge a command
		nc := edge(os.Args[2])
		errs := make(chan error, 1)
		nc.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) { errs <- e })
		nc.Publish(fmt.Sprintf("sites.%s.command.arm-01.req", os.Args[2]), []byte("forged"))
		nc.Flush()
		select {
		case e := <-errs:
			fmt.Println("inject rejected:", e)
		case <-time.After(time.Second):
			fmt.Println("inject: no error (CHECK)")
		}

	case "cloud-count":
		js, _ := jetstream.New(cloud())
		st, err := js.Stream(ctx(), "FLEET_UPLINK")
		must(err)
		info, err := st.Info(ctx(), jetstream.WithSubjectFilter("sites.>"))
		must(err)
		fmt.Printf("FLEET_UPLINK msgs=%d per-subject=%v\n", info.State.Msgs, info.State.Subjects)
		cq, _ := js.Stream(ctx(), "FLEET_COMMANDS")
		ci, _ := cq.Info(ctx())
		fmt.Printf("FLEET_COMMANDS pending=%d\n", ci.State.Msgs)
		// verify order + no gaps for site1
		c, err := st.OrderedConsumer(ctx(), jetstream.OrderedConsumerConfig{FilterSubjects: []string{"sites.site1.telemetry.>"}})
		must(err)
		last, gaps, n := 0, 0, 0
		for {
			m, err := c.Next(jetstream.FetchMaxWait(time.Second))
			if err != nil {
				break
			}
			var v struct{ Seq int }
			json.Unmarshal(m.Data(), &v)
			if v.Seq != last+1 {
				gaps++
			}
			last = v.Seq
			n++
		}
		fmt.Printf("site1 telemetry in cloud: count=%d last=%d out-of-order/gaps=%d\n", n, last, gaps)
	}
}

func mustDur(s string) time.Duration { d, err := time.ParseDuration(s); must(err); return d }
