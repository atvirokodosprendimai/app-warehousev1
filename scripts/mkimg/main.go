// Command mkimg writes a small valid PNG, so the smoke test can upload a real
// image rather than bytes that merely claim to be one.
//
// The server sniffs the content type from the bytes instead of trusting the
// upload's own Content-Type header, so a fake would be rejected — which is the
// behaviour being relied on, and therefore the behaviour worth exercising.
package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		os.Stderr.WriteString("usage: mkimg <path>\n")
		os.Exit(2)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 32), G: uint8(y * 32), B: 128, A: 255})
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
