// Package trayicon holds the app's tray icon.
//
// The icon is drawn rather than shipped as a file. An image file in the repo is
// one more thing a build has to remember to keep in step with the app, and a
// tray icon that is empty because a generation step was skipped is invisible:
// the app runs and there is nothing in the notification area to click. Drawing
// it in Go means the binary carries it whatever the build did.
//
// The design follows the interface: grayscale, square corners, and a mark that
// says what the app does. Three bars of rising height is a speaker's output
// scale, and the highest one is filled to say audio is being sent - which is the
// one thing this app exists to do.
package trayicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// Size is the icon's side in pixels, drawn square.
//
// 32 is what a Windows notification-area icon is at standard DPI, and it is
// drawn at that size rather than scaled: a tray icon that is drawn small and
// blown up is a blurry smear.
const Size = 32

// PNG is the tray icon as PNG bytes, which is what a tray takes.
func PNG() []byte {
	var buf bytes.Buffer
	// A failure here is a bug in the drawing below, not a runtime condition:
	// the encoder is given an image of a fixed size in memory and cannot fail on
	// it. A nil icon on a tray is invisible, so it is worth a panic in a build
	// check rather than a silent nil.
	if err := png.Encode(&buf, draw()); err != nil {
		panic("tray icon: " + err.Error())
	}
	return buf.Bytes()
}

// ink and ground are the two tones of the icon: a filled block and its
// background. Grayscale, per the app's own design.
var (
	ink    = color.NRGBA{A: 0xFF, R: 0x10, G: 0x10, B: 0x13}
	ground = color.NRGBA{A: 0xFF, R: 0xFF, G: 0xFF, B: 0xFF}
)

// draw renders the icon: a square, three rising bars, corners square.
func draw() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, Size, Size))
	// The field is white: a tray icon sits on whatever the taskbar's colour is,
	// and a transparent one there reads as a hole. A filled square is what makes
	// the mark legible on both a light and a dark taskbar, which a transparent
	// outline is not.
	for y := 0; y < Size; y++ {
		for x := 0; x < Size; x++ {
			img.SetNRGBA(x, y, ground)
		}
	}

	// Three bars, each 5px wide with 3px between them, centred across the 32.
	// Their heights rise left to right, so the group reads as a scale.
	const barWidth = 5
	const gap = 3
	const base = 26 // the line the bars stand on
	heights := []int{8, 14, 20}

	x := (Size - (barWidth*3 + gap*2)) / 2
	for _, h := range heights {
		for dx := 0; dx < barWidth; dx++ {
			for dy := 0; dy < h; dy++ {
				img.SetNRGBA(x+dx, base-dy, ink)
			}
		}
		x += barWidth + gap
	}

	// The baseline the bars stand on, so the mark is grounded rather than
	// floating. One pixel, the same ink.
	for dx := 0; dx < barWidth*3+gap*2; dx++ {
		img.SetNRGBA((Size-(barWidth*3+gap*2))/2+dx, base+1, ink)
	}
	return img
}
