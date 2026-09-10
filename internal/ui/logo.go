package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"

	"github.com/nccapo/stvena/assets/branding"
)

var stvenaIcon = sync.OnceValue(func() image.Image {
	img, err := png.Decode(bytes.NewReader(branding.Icon))
	if err != nil {
		panic("decode embedded Stvena icon: " + err.Error())
	}
	return img
})

// renderLogo uses two image pixels per cell to preserve the square mark's
// proportions without requiring a terminal-specific image protocol.
func renderLogo(size int) []string {
	img := stvenaIcon()
	bounds := img.Bounds()
	pixel := func(x, y int) (string, bool) {
		r, g, b, a := img.At(bounds.Min.X+(2*x+1)*bounds.Dx()/(2*size), bounds.Min.Y+(2*y+1)*bounds.Dy()/(2*size)).RGBA()
		// Leave transparent parts on the terminal's own background. RGBA's
		// premultiplied colors retain the soft edges of the original mark.
		return fmt.Sprintf("%d;%d;%d", r>>8, g>>8, b>>8), a >= 0x1000
	}
	rows := make([]string, size/2)
	for y := range rows {
		var row strings.Builder
		for x := 0; x < size; x++ {
			top, topVisible := pixel(x, y*2)
			bottom, bottomVisible := pixel(x, y*2+1)
			row.WriteString(reset)
			switch {
			case topVisible && bottomVisible:
				fmt.Fprintf(&row, "\x1b[38;2;%sm\x1b[48;2;%sm▀", top, bottom)
			case topVisible:
				fmt.Fprintf(&row, "\x1b[38;2;%sm▀", top)
			case bottomVisible:
				fmt.Fprintf(&row, "\x1b[38;2;%sm▄", bottom)
			default:
				row.WriteByte(' ')
			}
		}
		rows[y] = row.String() + reset
	}
	return rows
}
