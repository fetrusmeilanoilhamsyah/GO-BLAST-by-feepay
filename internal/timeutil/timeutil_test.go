package timeutil_test

import (
	"testing"
	"time"

	"goblast/internal/timeutil"
)

func TestTimeutil(t *testing.T) {
	loc := timeutil.JakartaLocation()
	if loc == nil {
		t.Fatal("JakartaLocation() returned nil")
	}

	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != 7*3600 {
		t.Fatalf("expected offset 25200 (WIB), got %d", offset)
	}

	start := timeutil.GetJakartaStartOfDay()
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 || start.Nanosecond() != 0 {
		t.Fatalf("expected start of day to be 00:00:00.000, got %02d:%02d:%02d.%d",
			start.Hour(), start.Minute(), start.Second(), start.Nanosecond())
	}
	if start.Location().String() != "Asia/Jakarta" {
		t.Fatalf("expected start of day location to be Asia/Jakarta, got %s", start.Location().String())
	}

	nextMidnight := timeutil.GetNextJakartaMidnight()
	if nextMidnight.Hour() != 0 || nextMidnight.Minute() != 0 || nextMidnight.Second() != 0 {
		t.Fatalf("expected next midnight to be 00:00:00, got %v", nextMidnight)
	}
	if !nextMidnight.After(start) {
		t.Fatalf("expected next midnight (%v) to be after start of today (%v)", nextMidnight, start)
	}
	diff := nextMidnight.Sub(start)
	if diff != 24*time.Hour {
		t.Fatalf("expected difference 24h, got %v", diff)
	}
}
