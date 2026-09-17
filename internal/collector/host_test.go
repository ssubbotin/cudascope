package collector

import "testing"

// Network counters are unsigned and they do go backwards: an interface
// reset, a namespace change, a driver reload. The difference then wraps to
// something near 1.8e19, and dividing that by the elapsed seconds stores a
// rate nothing ever transferred. The live database had 196 such rows.
func TestARateIsRefusedWhenTheCounterWentBackwards(t *testing.T) {
	if _, ok := netRate(500, 1_000_000, 5); ok {
		t.Fatal("a counter that went backwards produced a rate")
	}
}

func TestAnOrdinaryDifferenceBecomesARate(t *testing.T) {
	rate, ok := netRate(1_000_000, 500_000, 5)
	if !ok {
		t.Fatal("an ordinary difference was refused")
	}
	if rate != 100_000 {
		t.Fatalf("rate = %d, want 100000", rate)
	}
}

func TestNoElapsedTimeMeansNoRate(t *testing.T) {
	if _, ok := netRate(1_000_000, 500_000, 0); ok {
		t.Fatal("a zero interval produced a rate")
	}
}

func TestAnUnchangedCounterIsAZeroRate(t *testing.T) {
	rate, ok := netRate(1_000, 1_000, 5)
	if !ok || rate != 0 {
		t.Fatalf("an idle interface: rate %d, ok %v", rate, ok)
	}
}
