package scheduler

import (
	"testing"
	"time"

	"github.com/lizozom/whatsapp-nagger/internal/db"
)

// fixedNow returns a clock that always reports the given UTC instant.
func fixedNow(s string) func() time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return t }
}

// --- ShouldFireDigest ---

func TestShouldFireDigest_FiresWhenHourMatchesAndNotSentToday(t *testing.T) {
	g := db.Group{
		ID: "120363SCHED01@g.us", Timezone: "Asia/Jerusalem",
		DigestHour: 9, DigestHourSet: true,
	}
	// 2026-05-02 09:30 Asia/Jerusalem = 2026-05-02 06:30 UTC.
	now := mustParse(t, "2026-05-02T06:30:00Z")
	fire, today := ShouldFireDigest(g, "", now)
	if !fire {
		t.Error("expected to fire (hour 9 IDT, never sent)")
	}
	if today != "2026-05-02" {
		t.Errorf("today: got %q, want 2026-05-02", today)
	}
}

func TestShouldFireDigest_SkipsWhenAlreadySentToday(t *testing.T) {
	g := db.Group{
		ID: "120363SCHED02@g.us", Timezone: "Asia/Jerusalem",
		DigestHour: 9, DigestHourSet: true,
	}
	now := mustParse(t, "2026-05-02T06:30:00Z")
	fire, _ := ShouldFireDigest(g, "2026-05-02", now)
	if fire {
		t.Error("should not re-fire when last_digest_date == today")
	}
}

func TestShouldFireDigest_SkipsOutsideHour(t *testing.T) {
	g := db.Group{
		ID: "120363SCHED03@g.us", Timezone: "Asia/Jerusalem",
		DigestHour: 9, DigestHourSet: true,
	}
	// 10:30 IDT — outside the digest hour window.
	now := mustParse(t, "2026-05-02T07:30:00Z")
	fire, _ := ShouldFireDigest(g, "", now)
	if fire {
		t.Error("should not fire outside digest hour")
	}
}

func TestShouldFireDigest_TimezoneRespected(t *testing.T) {
	// Same UTC instant, different group timezones, different decisions.
	now := mustParse(t, "2026-05-02T06:00:00Z") // 09:00 IDT, 02:00 EST, 08:00 CEST
	cases := []struct {
		name string
		tz   string
		hour int
		want bool
	}{
		{"IDT group at 9", "Asia/Jerusalem", 9, true},
		{"NY group at 2", "America/New_York", 2, true},
		{"Berlin group at 8", "Europe/Berlin", 8, true},
		{"IDT group at 10 — too early", "Asia/Jerusalem", 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := db.Group{ID: "g", Timezone: tc.tz, DigestHour: tc.hour, DigestHourSet: true}
			fire, _ := ShouldFireDigest(g, "", now)
			if fire != tc.want {
				t.Errorf("got %v, want %v", fire, tc.want)
			}
		})
	}
}

func TestShouldFireDigest_NoHourSetSkips(t *testing.T) {
	g := db.Group{ID: "g", Timezone: "Asia/Jerusalem", DigestHourSet: false}
	fire, _ := ShouldFireDigest(g, "", time.Now())
	if fire {
		t.Error("should not fire when DigestHourSet=false")
	}
}

func TestShouldFireDigest_NoTimezoneSkips(t *testing.T) {
	g := db.Group{ID: "g", DigestHour: 9, DigestHourSet: true}
	fire, _ := ShouldFireDigest(g, "", time.Now())
	if fire {
		t.Error("should not fire when timezone is empty")
	}
}

func TestShouldFireDigest_InvalidTimezoneSkips(t *testing.T) {
	g := db.Group{ID: "g", Timezone: "Asia/Atlantis", DigestHour: 9, DigestHourSet: true}
	fire, _ := ShouldFireDigest(g, "", time.Now())
	if fire {
		t.Error("should not fire with invalid IANA timezone")
	}
}

// --- ShouldFireNag ---

func TestShouldFireNag_FiresAtHour(t *testing.T) {
	g := db.Group{ID: "g", Timezone: "Asia/Jerusalem"}
	now := mustParse(t, "2026-05-02T15:30:00Z") // 18:30 IDT
	fire, today := ShouldFireNag(g, "", 18, now)
	if !fire {
		t.Error("expected fire at hour 18 IDT")
	}
	if today != "2026-05-02" {
		t.Errorf("today: got %q", today)
	}
}

func TestShouldFireNag_SkipsAlreadyFiredToday(t *testing.T) {
	g := db.Group{ID: "g", Timezone: "Asia/Jerusalem"}
	now := mustParse(t, "2026-05-02T15:30:00Z")
	fire, _ := ShouldFireNag(g, "2026-05-02", 18, now)
	if fire {
		t.Error("should not re-fire same day")
	}
}

func TestShouldFireNag_PerGroupTimezoneIndependent(t *testing.T) {
	// One UTC instant; each group decides based on its own TZ.
	now := mustParse(t, "2026-05-02T18:30:00Z")
	groups := []db.Group{
		{ID: "tlv", Timezone: "Asia/Jerusalem"}, // 21:30
		{ID: "ber", Timezone: "Europe/Berlin"},  // 20:30
		{ID: "nyc", Timezone: "America/New_York"}, // 14:30
	}
	wants := map[string]bool{"tlv": true, "ber": false, "nyc": false}
	for _, g := range groups {
		fire, _ := ShouldFireNag(g, "", 21, now)
		if fire != wants[g.ID] {
			t.Errorf("%s at %s: got fire=%v, want %v", g.ID, g.Timezone, fire, wants[g.ID])
		}
	}
}

// --- helpers ---

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// silence unused-import warning if fixedNow goes unused in this file.
var _ = fixedNow
