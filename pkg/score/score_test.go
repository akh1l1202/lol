package score

import "testing"

func TestDayScore(t *testing.T) {
	cases := []struct {
		name string
		agg  dayAgg
		want int
	}{
		{"no usage", dayAgg{total: 0, distracting: 0}, 0},
		{"all focus", dayAgg{total: 3600, distracting: 0}, 100},
		{"all distracting", dayAgg{total: 3600, distracting: 3600}, 0},
		{"half and half", dayAgg{total: 3600, distracting: 1800}, 50},
		{"three quarters focus", dayAgg{total: 4000, distracting: 1000}, 75},
	}
	for _, tc := range cases {
		if got := dayScore(tc.agg); got != tc.want {
			t.Errorf("%s: dayScore(%+v) = %d, want %d", tc.name, tc.agg, got, tc.want)
		}
	}
}
