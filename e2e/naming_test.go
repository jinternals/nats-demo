package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The naming convention (docs/naming.md), checked against every config file.
// Needs no running stack, only config/generated (created by `make up`).

// Hop families and the types each may carry.
var hops = map[string][]string{
	"cloud.to.site":  {"command"},
	"site.to.device": {"command"},
	"site.to.cloud":  {"telemetry", "health", "result"},
	"device.to.site": {"telemetry", "health", "result"},
}

// Prefixes outside the four hops: our plumbing, and NATS's own.
var reserved = []string{"_edge.", "$JS.", "$SYS.", "_INBOX."}

var (
	idToken      = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*|\*)$`)
	streamName   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-(inbox|outbox)$`)
	consumerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-controller$`)
)

func checkSubject(s string) string {
	for _, p := range reserved {
		if strings.HasPrefix(s, p) {
			return ""
		}
	}
	tok := strings.Split(s, ".")
	if len(tok) < 4 {
		return "too short"
	}
	types, ok := hops[strings.Join(tok[:3], ".")]
	if !ok {
		return "not one of the four hops (cloud.to.site, site.to.cloud, site.to.device, device.to.site) or a reserved prefix"
	}
	if len(tok) == 4 && tok[3] == ">" {
		return ""
	}
	if len(tok) != 5 {
		return "want <from>.to.<to>.<id>.<type>"
	}
	if !idToken.MatchString(tok[3]) {
		return "bad id token " + tok[3]
	}
	if typ := tok[4]; typ != "*" && typ != ">" && !strings.HasPrefix(typ, "{{wildcard(") && !slices.Contains(types, typ) {
		return "type " + typ + " not allowed on " + strings.Join(tok[:3], ".") + " (allowed: " + strings.Join(types, ", ") + ")"
	}
	return ""
}

func TestNamingConvention(t *testing.T) {
	files, _ := filepath.Glob("../config/generated/streams/*.json")
	if len(files) == 0 {
		t.Fatal("config/generated is missing: run `make up` (or scripts/generate.sh site1 site2)")
	}
	more, _ := filepath.Glob("../config/streams/*.json")
	files = append(files, more...)

	for _, f := range files {
		var c struct {
			Name             string   `json:"name"`
			Durable          string   `json:"durable_name"`
			Subjects         []string `json:"subjects"`
			FilterSubject    string   `json:"filter_subject"`
			SubjectTransform *struct {
				Src  string `json:"src"`
				Dest string `json:"dest"`
			} `json:"subject_transform"`
		}
		b, _ := os.ReadFile(f)
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		base := filepath.Base(f)
		if c.Name != "" && !streamName.MatchString(c.Name) {
			t.Errorf("%s: stream name %q: want <place>-inbox or <place>-outbox", base, c.Name)
		}
		if c.Durable != "" && !consumerName.MatchString(c.Durable) {
			t.Errorf("%s: consumer name %q: want <reader>-controller", base, c.Durable)
		}
		subjects := append([]string{}, c.Subjects...)
		if c.FilterSubject != "" {
			subjects = append(subjects, c.FilterSubject)
		}
		if c.SubjectTransform != nil {
			subjects = append(subjects, c.SubjectTransform.Src, c.SubjectTransform.Dest)
		}
		for _, s := range subjects {
			if why := checkSubject(s); why != "" {
				t.Errorf("%s: subject %q: %s", base, s, why)
			}
		}
	}

	// Subjects in server configs: quoted dotted strings that aren't paths, URLs or names.
	confs, _ := filepath.Glob("../config/*.conf")
	more, _ = filepath.Glob("../config/generated/*.conf")
	quoted := regexp.MustCompile(`"([^"\s]+)"`)
	subjectLike := regexp.MustCompile(`^[A-Za-z0-9_$*>{}()-]+(\.[A-Za-z0-9_$*>{}()-]+)+$`)
	checked := 0
	for _, f := range append(confs, more...) {
		b, _ := os.ReadFile(f)
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for _, m := range quoted.FindAllStringSubmatch(line, -1) {
				s := m[1]
				if !subjectLike.MatchString(s) || strings.HasSuffix(s, ".conf") {
					continue
				}
				checked++
				if why := checkSubject(s); why != "" {
					t.Errorf("%s: subject %q: %s", filepath.Base(f), s, why)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no subjects in the server configs; the extraction is broken")
	}
	t.Logf("checked %d stream/consumer files and %d subjects in server configs", len(files), checked)
}

// The checker itself: names that must pass and names that must fail.
func TestNamingCheckerRejectsBadNames(t *testing.T) {
	for _, s := range []string{"cloud.to.site.site1.command", "site.to.cloud.*.telemetry", "device.to.site.arm-01.result", "site.to.device.>", "_edge.copy.site1.>", "$JS.API.STREAM.INFO.x"} {
		if why := checkSubject(s); why != "" {
			t.Errorf("%s should pass: %s", s, why)
		}
	}
	for _, s := range []string{"fleet.site1.arm-01.telemetry", "site.to.cloud.site1.command", "cloud.to.site.site1.telemetry", "device.to.site.arm-01.cmd", "site.to.cloud.Site1.health", "site.>", "device.to.site.arm-01.telemetry.extra"} {
		if checkSubject(s) == "" {
			t.Errorf("%s should fail", s)
		}
	}
}
