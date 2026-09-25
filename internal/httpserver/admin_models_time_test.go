package httpserver

import (
	"testing"
	"time"
)

func TestAdminValueStringUsesConfiguredTimeZoneAndDjangoDateStyle(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		utc  time.Time
		want string
	}{
		{time.Date(2020, 2, 3, 1, 5, 0, 0, time.UTC), "Feb. 3, 2020, 4:05 a.m."},
		{time.Date(2020, 3, 2, 21, 0, 0, 0, time.UTC), "March 3, 2020, midnight"},
		{time.Date(2020, 9, 3, 9, 0, 0, 0, time.UTC), "Sept. 3, 2020, noon"},
		{time.Date(2020, 12, 3, 13, 5, 0, 0, time.UTC), "Dec. 3, 2020, 4:05 p.m."},
	} {
		if got := adminValueString(test.utc, moscow); got != test.want {
			t.Errorf("admin date %s = %q, want %q", test.utc, got, test.want)
		}
	}
}
