package logscan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeSource(t *testing.T, scan *Scan, name string, baseline bool, text string, chunk int) {
	t.Helper()
	if err := scan.Start(name, baseline); err != nil {
		t.Fatal(err)
	}
	for len(text) != 0 {
		part := min(chunk, len(text))
		if n, err := scan.Write([]byte(text[:part])); err != nil || n != part {
			t.Fatalf("write = %d, %v", n, err)
		}
		text = text[part:]
	}
	scan.End()
}

func TestChunkBoundariesAndNormalizedComparison(t *testing.T) {
	incident := "\xef\xbb\xbf2026-09-18T14:12:06.200Z ERROR db refused port 5432 request_id=new-one password=test-secret\r\n" +
		`{"timestamp":"2026-09-18T10:12:03-04:00","level":"error","msg":"db refused port 5432 request_id=new-two password=test-secret-2"}` + "\n" +
		"ERROR db refused port 6432\nWARN credentials refresh slow"
	baseline := "2026-09-18T13:12:06Z ERROR db refused port 5432 request_id=old password=old-secret\n" +
		strings.Repeat("INFO healthy\n", 7)
	scan := New()
	writeSource(t, scan, "api.log", false, incident, 1)
	writeSource(t, scan, "baseline.log", true, baseline, 9)
	r := scan.Report()
	batch := New()
	writeSource(t, batch, "api.log", false, incident, len(incident))
	writeSource(t, batch, "baseline.log", true, baseline, len(baseline))
	if !reflect.DeepEqual(r, batch.Report()) {
		t.Fatal("report depends on input chunk boundaries")
	}
	if r.Incident.Lines != 4 || r.Incident.Untimed != 2 || r.Baseline.Lines != 8 || len(r.Groups) != 3 {
		t.Fatalf("unexpected report: %+v", r)
	}
	top := r.Groups[0]
	if top.Incident.Count != 2 || top.Baseline.Count != 1 || top.Change != "increased" || top.Incident.Rate != 500 || top.Baseline.Rate != 125 {
		t.Fatalf("unexpected comparison: %+v", top)
	}
	if top.Incident.First != "2026-09-18T14:12:03Z" || top.Incident.Last != "2026-09-18T14:12:06.2Z" || top.Incident.Sample.Line != 1 {
		t.Fatalf("unexpected chronological summary: %+v", top.Incident)
	}
	reportJSON, err := json.Marshal(r)
	if err != nil || bytes.Contains(reportJSON, []byte("test-secret")) || bytes.Contains(reportJSON, []byte("old-secret")) {
		t.Fatalf("unmasked secret or JSON error: %v", err)
	}
}

func TestSkipsOversizedBinaryAndPrivateKeyLines(t *testing.T) {
	scan := New()
	input := "INFO healthy\n" + strings.Repeat("x", MaxLine+1) + "\ninvalid\x00\n" +
		"-----BEGIN PRIVATE KEY-----\nERROR synthetic-private-material\n-----END PRIVATE KEY-----\nERROR last line"
	writeSource(t, scan, "a.log", false, input, 500)
	r := scan.Report()
	if r.Incident.Lines != 2 || r.Incident.Skipped != 2 || r.Incident.Redacted != 3 || len(r.Groups) != 1 {
		t.Fatalf("unexpected scan: %+v", r)
	}
	if got := r.Groups[0].Incident.Sample.Line; got != 7 {
		t.Fatalf("evidence line = %d, want 7", got)
	}
}

func TestGroupCapKeepsKnownCountsAndTimeline(t *testing.T) {
	scan := New()
	var input strings.Builder
	for i := 0; i <= MaxGroups; i++ {
		fmt.Fprintf(&input, "2026-09-18T14:12:00Z ERROR service number %d expired\n", i)
	}
	input.WriteString("2026-09-18T14:12:01Z ERROR service number 0 expired")
	writeSource(t, scan, "unique.log", false, input.String(), 64*1024)
	r := scan.Report()
	if len(r.Groups) != MaxGroups || r.Incident.Overflow != 1 || r.Groups[0].Incident.Count != 2 {
		t.Fatalf("unexpected fingerprint cap: %d groups, %+v", len(r.Groups), r.Incident)
	}
	if len(r.Timeline.Buckets) != 1 || r.Timeline.Buckets[0].Count != MaxGroups+2 {
		t.Fatalf("unexpected timeline: %+v", r.Timeline)
	}
}

func TestTimelineCoarsensIndependentlyOfArrivalOrder(t *testing.T) {
	seconds := []int64{-121, -1, 0, 59, 60, 3600, 100000, 97000000}
	a, b := histogram{width: 60}, histogram{width: 60}
	for i, value := range seconds {
		a.add(value)
		b.add(seconds[len(seconds)-1-i])
	}
	left, right := a.report(), b.report()
	if !reflect.DeepEqual(left, right) || len(left.Buckets) > MaxBuckets {
		t.Fatalf("timeline depends on arrival order or grew past limit: %+v %+v", left, right)
	}
	var total int64
	for _, bin := range left.Buckets {
		if _, err := time.Parse(time.RFC3339, bin.Start); err != nil {
			t.Fatal(err)
		}
		total += bin.Count
	}
	if total != int64(len(seconds)) {
		t.Fatalf("lost events during coarsening: %d", total)
	}
}

func BenchmarkStreamLogs(b *testing.B) {
	var fixture strings.Builder
	for i := 0; i < 10000; i++ {
		if i%10 == 0 {
			fmt.Fprintf(&fixture, "{\"time\":\"2026-09-18T14:12:00Z\",\"level\":\"ERROR\",\"msg\":\"database connection refused request_id=req-%d\"}\n", i)
		} else {
			fmt.Fprintf(&fixture, "2026-09-18T14:12:00Z INFO successful request id=req-%d\n", i)
		}
	}
	data := []byte(fixture.String())
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scan := New()
		scan.Start("benchmark", false)
		for offset := 0; offset < len(data); offset += 64 * 1024 {
			scan.Write(data[offset:min(offset+64*1024, len(data))])
		}
		scan.End()
		if scan.Report().Incident.Events != 1000 {
			b.Fatal("unexpected event count")
		}
	}
}
