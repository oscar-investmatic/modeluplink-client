//go:build ignore

// gen_icon writes icon.png: the same mark as the Mac app icon (macos/icon.swift),
// a lime paper plane inside a faint orbit on a dark rounded tile, rendered at
// 512×512 with 4× supersampling so it stays smooth at every hicolor size.
//
//	go run gen_icon.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size    = 512
	samples = 4
)

var (
	tile  = [3]float64{0.055, 0.08, 0.065}
	green = [3]float64{0.66, 1, 0.31}
)

type point struct{ x, y float64 }

// The Mac icon uses AppKit's y-up coordinates; y is flipped here.
var plane = []point{{0.24, 1 - 0.54}, {0.77, 1 - 0.74}, {0.58, 1 - 0.23}, {0.47, 1 - 0.43}}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/samples) / size
					y := (float64(py) + (float64(sy)+0.5)/samples) / size
					cr, cg, cb, ca := shade(x, y)
					r, g, b, a = r+cr*ca, g+cg*ca, b+cb*ca, a+ca
				}
			}
			n := float64(samples * samples)
			if a > 0 {
				r, g, b = r/a, g/a, b/a
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), uint8(a / n * 255)})
		}
	}
	f, err := os.Create("icon.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

// shade returns the colour and coverage at a unit-square coordinate.
func shade(x, y float64) (r, g, b, a float64) {
	if !inRoundedRect(x, y, 0.04, 0.92, 0.2) {
		return 0, 0, 0, 0
	}
	r, g, b, a = tile[0], tile[1], tile[2], 1
	if d := math.Hypot(x-0.5, y-0.5); math.Abs(d-0.31) < 0.006 {
		r, g, b = mix(r, green[0], 0.25), mix(g, green[1], 0.25), mix(b, green[2], 0.25)
	}
	if inPolygon(x, y, plane) || math.Hypot(x-0.255, y-(1-0.265)) < 0.035 {
		r, g, b = green[0], green[1], green[2]
	}
	return r, g, b, a
}

func mix(base, over, t float64) float64 { return base*(1-t) + over*t }

func inRoundedRect(x, y, inset, side, radius float64) bool {
	cx := math.Max(math.Abs(x-0.5)-(side/2-radius), 0)
	cy := math.Max(math.Abs(y-0.5)-(side/2-radius), 0)
	_ = inset
	return math.Hypot(cx, cy) <= radius
}

func inPolygon(x, y float64, poly []point) bool {
	inside := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		pi, pj := poly[i], poly[j]
		if (pi.y > y) != (pj.y > y) && x < (pj.x-pi.x)*(y-pi.y)/(pj.y-pi.y)+pi.x {
			inside = !inside
		}
	}
	return inside
}
