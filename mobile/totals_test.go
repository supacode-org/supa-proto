package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"supaproto/internal/stats"
)

// fakeCounters stands in for a running engine's byte counters.
type fakeCounters struct{ up, down atomic.Int64 }

func (f *fakeCounters) read() trafficTotals {
	return trafficTotals{Up: f.up.Load(), Down: f.down.Load()}
}

func readTotalsFile(t *testing.T, path string) trafficTotals {
	t.Helper()
	got, err := loadTotals(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTotalsContinueAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-totals.json")

	first := &fakeCounters{}
	r := startTotals(path, first.read)
	first.up.Store(100)
	first.down.Store(1000)
	if got := r.current(); got != (trafficTotals{100, 1000}) {
		t.Fatalf("first run totals = %+v", got)
	}
	r.close()
	if got := readTotalsFile(t, path); got != (trafficTotals{100, 1000}) {
		t.Fatalf("saved after first run = %+v", got)
	}

	second := &fakeCounters{}
	r = startTotals(path, second.read)
	second.up.Store(5)
	second.down.Store(50)
	if got := r.current(); got != (trafficTotals{105, 1050}) {
		t.Fatalf("second run totals = %+v", got)
	}
	r.save()
	if got := readTotalsFile(t, path); got != (trafficTotals{105, 1050}) {
		t.Fatalf("saved during second run = %+v", got)
	}
	r.close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("totals file mode = %v", mode)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestTotalsStartFromZeroOnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-totals.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	counters := &fakeCounters{}
	counters.up.Store(7)
	r := startTotals(path, counters.read)
	if got := r.current(); got != (trafficTotals{7, 0}) {
		t.Fatalf("totals = %+v", got)
	}
	r.close()
	if got := readTotalsFile(t, path); got != (trafficTotals{7, 0}) {
		t.Fatalf("saved = %+v", got)
	}
}

func TestTotalsWithoutFile(t *testing.T) {
	counters := &fakeCounters{}
	counters.down.Store(9)
	r := startTotals("", counters.read)
	if got := r.current(); got != (trafficTotals{0, 9}) {
		t.Fatalf("totals = %+v", got)
	}
	r.close()
}

func TestStatsReplyFields(t *testing.T) {
	data, err := json.Marshal(statsReply{Snapshot: stats.Snapshot{Time: 1, Up: 2, Down: 3}, TotalUp: 20, TotalDown: 30})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]int64
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["up"] != 2 || fields["down"] != 3 || fields["total_up"] != 20 || fields["total_down"] != 30 {
		t.Fatalf("stats reply = %s", data)
	}
}
