package lyrics

import "testing"

func TestParseLRC(t *testing.T) {
	raw := "[ar:Someone]\n[ti:A Song]\n\n[00:12.34]First line\n[00:15.00][01:02.500]Repeated line\n[bad]not a timestamp\n"
	lines := ParseLRC(raw)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %+v", len(lines), lines)
	}
	if lines[0].TimeSec != 12.34 || lines[0].Text != "First line" {
		t.Errorf("line 0 = %+v", lines[0])
	}
	if lines[1].TimeSec != 15.0 || lines[1].Text != "Repeated line" {
		t.Errorf("line 1 = %+v", lines[1])
	}
	if lines[2].TimeSec != 62.5 || lines[2].Text != "Repeated line" {
		t.Errorf("line 2 = %+v", lines[2])
	}
}

func TestActiveIndex(t *testing.T) {
	lines := []Line{{TimeSec: 0}, {TimeSec: 10}, {TimeSec: 20}}
	cases := []struct {
		pos  float64
		want int
	}{
		{pos: -1, want: -1},
		{pos: 0, want: 0},
		{pos: 5, want: 0},
		{pos: 10, want: 1},
		{pos: 25, want: 2},
	}
	for _, c := range cases {
		if got := ActiveIndex(lines, c.pos); got != c.want {
			t.Errorf("ActiveIndex(%v) = %d, want %d", c.pos, got, c.want)
		}
	}
}

func TestActiveIndexEmpty(t *testing.T) {
	if got := ActiveIndex(nil, 5); got != -1 {
		t.Errorf("ActiveIndex(nil, 5) = %d, want -1", got)
	}
}
