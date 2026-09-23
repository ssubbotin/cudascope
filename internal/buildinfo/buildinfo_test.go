package buildinfo

import (
	"runtime/debug"
	"testing"
)

func recorded(settings map[string]string) *debug.BuildInfo {
	bi := &debug.BuildInfo{}
	for k, v := range settings {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return bi
}

func TestStampedValuesWin(t *testing.T) {
	got := resolve("v0.1.0", "683f145", "2026-09-23T09:43:11Z",
		recorded(map[string]string{"vcs.revision": "somethingelse"}))
	want := Info{Version: "v0.1.0", Revision: "683f145", BuiltAt: "2026-09-23T09:43:11Z"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAnUnstampedBuildTakesTheCommitGoRecorded(t *testing.T) {
	got := resolve("", "", "", recorded(map[string]string{"vcs.revision": "683f14528c75"}))
	if got.Version != "dev" || got.Revision != "683f14528c75" {
		t.Fatalf("got %+v, want version dev at the recorded commit", got)
	}
}

// vcs.time is when the commit was made. Showing it as the build time would
// date a binary built today by a commit from last month.
func TestTheCommitTimeIsNotPassedOffAsTheBuildTime(t *testing.T) {
	got := resolve("", "", "", recorded(map[string]string{
		"vcs.revision": "683f14528c75",
		"vcs.time":     "2026-08-01T10:00:00Z",
	}))
	if got.BuiltAt != "" {
		t.Fatalf("got built_at %q, want it left unknown", got.BuiltAt)
	}
}

func TestABuildWithNothingRecordedIsDev(t *testing.T) {
	if got := resolve("", "", "", nil); got != (Info{Version: "dev"}) {
		t.Fatalf("got %+v, want only version dev", got)
	}
}
