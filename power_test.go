package main

import "testing"

func TestParseLowPowerMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pmset   string
		want    bool
		wantErr bool
	}{
		{name: "off", pmset: "Currently in use:\n standby              1\n lowpowermode         0\n womp 1\n", want: false},
		{name: "on", pmset: "Currently in use:\n lowpowermode         1\n", want: true},
		{name: "missing", pmset: "Currently in use:\n standby              1\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLowPowerMode(tc.pmset)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBusySinceWraps(t *testing.T) {
	prev := cpuSample{user: ^uint32(0) - 9, idle: 100}
	cur := cpuSample{user: 10, idle: 180}
	// 20 busy ticks across the wrap, 80 idle.
	if got := cur.busySince(prev); got != 0.2 {
		t.Fatalf("got %v, want 0.2", got)
	}
}
