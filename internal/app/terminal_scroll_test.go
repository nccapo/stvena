package app

import (
	"fmt"
	"testing"
)

func TestAgentTerminalResizeDuringScrollSequence(t *testing.T) {
	// An IDE panel change can shrink a 21-row terminal to 15 rows while a
	// margin-setting escape sequence is only partly received from the PTY.
	sequence := "\x1b[1;21r\x1b[H\x1bM"
	for split := 0; split <= len(sequence); split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			a := newAgentTerminal("test", 80, 21)
			t.Cleanup(func() { _ = a.virtual.Close() })
			_, _ = a.virtual.Write([]byte(sequence[:split]))
			a.virtual.Resize(50, 15)
			_, _ = a.virtual.Write([]byte(sequence[split:] + "\x1b[Halive"))
			for x, want := range "alive" {
				if cell := a.virtual.CellAt(x, 0); cell == nil || cell.Content != string(want) {
					t.Fatalf("cell %d = %v, want %q", x, cell, want)
				}
			}
		})
	}
}

func TestAgentTerminalScrollAfterResize(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		for _, horizontal := range []bool{false, true} {
			for _, operation := range []string{"\x1bM", "\x1b[L", "\x1b[M", "\x1b[S", "\x1b[T"} {
				t.Run(fmt.Sprintf("alternate=%t/horizontal=%t/operation=%q", alternate, horizontal, operation), func(t *testing.T) {
					a := newAgentTerminal("test", 80, 40)
					t.Cleanup(func() { _ = a.virtual.Close() })
					write := func(data string) {
						t.Helper()
						if _, err := a.virtual.Write([]byte(data)); err != nil {
							t.Fatal(err)
						}
					}
					if alternate {
						write("\x1b[?1049h")
					}
					if horizontal {
						write("\x1b[?69h")
					}
					a.virtual.Resize(50, 26)
					// Output queued before the resize still uses the old dimensions.
					margins := "\x1b[1;40r"
					if horizontal {
						margins = "\x1b[1;80s"
					}
					// PTY reads can split an escape sequence at any byte.
					for i := range margins {
						write(margins[i : i+1])
					}
					write("\x1b[H" + operation + "alive")
					for x, want := range "alive" {
						cell := a.virtual.CellAt(x, 0)
						if cell == nil || cell.Content != string(want) {
							t.Fatalf("cell %d = %v, want %q", x, cell, want)
						}
					}
				})
			}
		}
	}
}

func TestAgentTerminalScrollMarginsPreserveContent(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
		want           [5]string
	}{
		{
			name:     "stale bottom is clamped",
			sequence: "\x1b[2;99r\x1b[2;1H\x1bM",
			want:     [5]string{"AAAAAAAA", "        ", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD"},
		},
		{
			name:     "stale right is clamped",
			sequence: "\x1b[?69h\x1b[3;99s\x1b[1;3H\x1bM",
			want:     [5]string{"AA      ", "BBAAAAAA", "CCBBBBBB", "DDCCCCCC", "EEDDDDDD"},
		},
		{
			name:     "valid margins retain outside rows",
			sequence: "\x1b[2;4r\x1b[2;1H\x1bM",
			want:     [5]string{"AAAAAAAA", "        ", "BBBBBBBB", "CCCCCCCC", "EEEEEEEE"},
		},
		{
			name:     "missing defaults reset margins",
			sequence: "\x1b[2;4r\x1b[r\x1b[H\x1bM",
			want:     [5]string{"        ", "AAAAAAAA", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD"},
		},
		{
			name:     "zero defaults reset margins",
			sequence: "\x1b[?69h\x1b[3;6s\x1b[0;0s\x1b[2;4r\x1b[0;0r\x1b[H\x1bM",
			want:     [5]string{"        ", "AAAAAAAA", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD"},
		},
		{
			name:     "out of bounds start leaves previous margins",
			sequence: "\x1b[2;4r\x1b[50;99r\x1b[2;1H\x1bM",
			want:     [5]string{"AAAAAAAA", "        ", "BBBBBBBB", "CCCCCCCC", "EEEEEEEE"},
		},
		{
			name:     "cursor save still works with margin mode off",
			sequence: "\x1b[3;4H\x1b[s\x1b[H\x1b8X",
			want:     [5]string{"AAAAAAAA", "BBBBBBBB", "CCCXCCCC", "DDDDDDDD", "EEEEEEEE"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAgentTerminal("test", 8, 5)
			t.Cleanup(func() { _ = a.virtual.Close() })
			for y, row := range []string{"AAAAAAAA", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD", "EEEEEEEE"} {
				_, _ = a.virtual.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s", y+1, row)))
			}
			_, _ = a.virtual.Write([]byte(tc.sequence))
			for y, row := range tc.want {
				for x, want := range row {
					cell := a.virtual.CellAt(x, y)
					if cell == nil || cell.Content != string(want) {
						t.Fatalf("cell (%d,%d) = %v, want %q", x, y, cell, want)
					}
				}
			}
		})
	}
}
