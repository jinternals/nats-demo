// Test harness for the cloud-hub / site-leaf design.
// usage: harness <cmd> [args]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// repo root: $NATS_DEMO_ROOT, else the parent of the working directory (run from test/)
var root = func() string {
	if r := os.Getenv("NATS_DEMO_ROOT"); r != "" {
		return r
	}
	wd, _ := os.Getwd()
	return filepath.Dir(wd)
}()

// Sites reach their command consumer in the cloud through this prefix; the
// hub maps it, per site account, onto FLEET's $JS.API (see gen-hub-accounts.sh).
const hubAPI = "$JS.hub.API"

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

const hubURLs = "tls://localhost:4212,tls://localhost:4222,tls://localhost:4232"

func cloud() *nats.Conn { return conn(hubURLs, "cloud-app", "cloudpw", true) }
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
	if replicas > 0 {
		c.Replicas = replicas
	}
	return c
}

// every request gets its own 10s deadline; all are released on exit
var cancels []context.CancelFunc

func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cancels = append(cancels, cancel)
	return c
}

func main() {
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	cmd := os.Args[1]
	switch cmd {
	case "setup":
		for _, s := range []string{"site1", "site2"} {
			js, _ := jetstream.New(ops(s))
			_, err := js.CreateOrUpdateStream(ctx(), loadStream(strings.ToUpper(s)+"_UPLINK", 1))
			must(err)
		}
		js, _ := jetstream.New(cloud())
		_, err := js.CreateOrUpdateStream(ctx(), loadStream("FLEET_COMMANDS", 0))
		must(err)
		_, err = js.CreateOrUpdateStream(ctx(), loadStream("FLEET_UPLINK", 0))
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
		js, _ := jetstream.NewWithAPIPrefix(edge(s), hubAPI)
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
		js, _ := jetstream.NewWithAPIPrefix(ops(os.Args[2]), hubAPI)
		_, err := js.Consumer(ctx(), "FLEET_COMMANDS", os.Args[3]+"-commands")
		fmt.Println("ops-steal result:", err)

	case "source-info":
		js, _ := jetstream.New(cloud())
		st, err := js.Stream(ctx(), "FLEET_UPLINK")
		must(err)
		info, err := st.Info(ctx())
		must(err)
		for _, si := range info.Sources {
			fmt.Printf("source %s lag=%d last_seen=%s\n", si.Name, si.Lag, si.Active.Round(time.Millisecond))
		}

	case "sys-watch": // sys-watch seconds
		nc := conn(hubURLs, "sys", "syspw", true)
		sec, _ := strconv.Atoi(os.Args[2])
		nc.Subscribe("$SYS.ACCOUNT.*.>", func(m *nats.Msg) { // leaves bind to their SITEn account
			var ev struct {
				Type   string                            `json:"type"`
				Client struct{ Kind, User, Name string } `json:"client"`
				Reason string                            `json:"reason"`
			}
			json.Unmarshal(m.Data, &ev)
			if strings.Contains(m.Subject, "CONNECT") || strings.Contains(m.Subject, "DISCONNECT") {
				fmt.Printf("%s SYS %s kind=%s user=%s reason=%s\n", time.Now().Format("15:04:05"), m.Subject, ev.Client.Kind, ev.Client.User, ev.Reason)
			}
		})
		time.Sleep(time.Duration(sec) * time.Second)

	case "ops-sniff": // ops-sniff site seconds: full-rights site user listens on ">"
		nc := ops(os.Args[2])
		sec, _ := strconv.Atoi(os.Args[3])
		seen := map[string]int{}
		var mu sync.Mutex
		nc.Subscribe(">", func(m *nats.Msg) {
			p := strings.SplitN(m.Subject, ".", 4)
			k := strings.Join(p[:min(3, len(p))], ".")
			mu.Lock()
			seen[k]++
			mu.Unlock()
		})
		nc.Flush()
		time.Sleep(time.Duration(sec) * time.Second)
		mu.Lock()
		foreign := 0
		for k, n := range seen {
			if strings.Contains(strings.ToLower(k), "site2") {
				foreign += n
			}
			fmt.Printf("  %s seen on %s: %s x%d\n", os.Args[2], os.Args[2], k, n)
		}
		mu.Unlock()
		fmt.Printf("%s sniff: %d message(s) mentioning site2\n", os.Args[2], foreign)

	case "wait-uplink": // wait-uplink SITE N SECONDS: poll FLEET_UPLINK until SITE has N telemetry msgs
		js, _ := jetstream.New(cloud())
		want, _ := strconv.Atoi(os.Args[3])
		sec, _ := strconv.Atoi(os.Args[4])
		subj := "sites." + os.Args[2] + ".telemetry.>"
		t0 := time.Now()
		var got uint64
		for time.Since(t0) < time.Duration(sec)*time.Second {
			if st, err := js.Stream(ctx(), "FLEET_UPLINK"); err == nil {
				if info, err := st.Info(ctx(), jetstream.WithSubjectFilter(subj)); err == nil {
					got = 0
					for _, n := range info.State.Subjects {
						got += n
					}
					if got >= uint64(want) {
						break
					}
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		fmt.Printf("uplink %s: %d/%d in cloud after %.0fs\n", os.Args[2], got, want, time.Since(t0).Seconds())

	case "leader": // leader STREAM -> prints leader name only (for scripts)
		js, _ := jetstream.New(cloud())
		st, err := js.Stream(ctx(), os.Args[2])
		must(err)
		info, err := st.Info(ctx())
		must(err)
		fmt.Println(info.Cluster.Leader)

	case "replicas": // replicas STREAM...
		js, _ := jetstream.New(cloud())
		for _, n := range os.Args[2:] {
			st, err := js.Stream(ctx(), n)
			must(err)
			info, err := st.Info(ctx())
			must(err)
			fmt.Printf("%s R%d leader=%s msgs=%d", n, info.Config.Replicas, info.Cluster.Leader, info.State.Msgs)
			for _, r := range info.Cluster.Replicas {
				fmt.Printf(" | %s current=%v lag=%d", r.Name, r.Current, r.Lag)
			}
			fmt.Println()
		}

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
		// verify order + no gaps for site1.
		// Measured: for ~3 min after a hub node dies, about 1 in 3 ephemeral
		// R1 consumer creates time out (placed on the dead peer). nats.go's
		// OrderedConsumer is an R1 ephemeral and its Next() then blocked past
		// its own max-wait. So: a plain ephemeral, created with a short
		// timeout and retried. Durable R3 consumers were not affected.
		var c jetstream.Consumer
		for try := 1; ; try++ {
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			c, err = st.CreateConsumer(cctx, jetstream.ConsumerConfig{
				FilterSubject: "sites.site1.telemetry.>", AckPolicy: jetstream.AckNonePolicy,
				MemoryStorage: true, Replicas: 1, InactiveThreshold: 30 * time.Second})
			cancel()
			if err == nil || try == 10 {
				break
			}
		}
		must(err)
		last, gaps, n := 0, 0, 0
		for {
			b, err := c.Fetch(1000, jetstream.FetchMaxWait(time.Second))
			must(err)
			got := 0
			for m := range b.Messages() {
				var v struct{ Seq int }
				json.Unmarshal(m.Data(), &v)
				if v.Seq != last+1 {
					gaps++
				}
				last = v.Seq
				n++
				got++
			}
			if got == 0 {
				break
			}
		}
		fmt.Printf("site1 telemetry in cloud: count=%d last=%d out-of-order/gaps=%d\n", n, last, gaps)
	}
}

func mustDur(s string) time.Duration { d, err := time.ParseDuration(s); must(err); return d }
