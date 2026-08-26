package pebbleopt

import (
	"fmt"
	"testing"
)

func TestMemTableMB(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		// Unset is the case every existing deployment is in, and it must keep
		// the size panmail has always opened with.
		{name: "unset keeps the default", env: "", want: DefaultMemTableMB},
		{name: "a plain value is used", env: "16", want: 16},
		{name: "the recommended value", env: "8", want: 8},

		// Clamped rather than refused: a gateway that will not start because a
		// tuning knob is out of range is worse than one that starts tuned to
		// the nearest value that works.
		{name: "zero is raised to the minimum", env: "0", want: minMemTableMB},
		{name: "negative is raised to the minimum", env: "-5", want: minMemTableMB},
		{name: "absurd is capped", env: "100000", want: maxMemTableMB},

		{name: "nonsense falls back", env: "sixteen", want: DefaultMemTableMB},
		{name: "a float falls back", env: "16.5", want: DefaultMemTableMB},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(EnvMemTableMB, tc.env)
			}
			if got := MemTableMB(); got != tc.want {
				t.Errorf("MemTableMB() = %d; want %d", got, tc.want)
			}
		})
	}
}

func TestMemTableSizeIsInBytes(t *testing.T) {
	t.Setenv(EnvMemTableMB, "16")
	if got, want := MemTableSize(), uint64(16<<20); got != want {
		t.Errorf("MemTableSize() = %d; want %d bytes", got, want)
	}
}

// The default is what every store opened with before this knob existed.
func TestTheDefaultIsUnchanged(t *testing.T) {
	if got := MemTableSize(); got != 64<<20 {
		t.Errorf("default = %s; want the 64 MiB the stores have always used",
			fmt.Sprintf("%d", got))
	}
}
