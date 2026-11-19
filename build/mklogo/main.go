// mklogo cuts the DRISHTI "D" emblem out of NOC-LOGO4.png, makes the dark background transparent and
// writes the window icon (build/icon.ico) and the header logo (server/web/logo.png).
//
//	go run ./build/mklogo            (run from the project folder)
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

// the emblem inside the 1408 x 768 picture: a square around the D
const cx, cy, half = 705, 355, 262

func main() {
	f, err := os.Open("NOC-LOGO4.png")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	side := 2 * half
	// crop, and turn the near-black background into transparency (soft edge)
	crop := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			r, g, b, _ := src.At(cx-half+x, cy-half+y).RGBA()
			R, G, B := float64(r>>8), float64(g>>8), float64(b>>8)
			m := R
			if G > m {
				m = G
			}
			if B > m {
				m = B
			}
			a := (m - 26) / 46
			if a < 0 {
				a = 0
			}
			if a > 1 {
				a = 1
			}
			crop.SetNRGBA(x, y, color.NRGBA{uint8(R), uint8(G), uint8(B), uint8(a*255 + 0.5)})
		}
	}
	pix := func(n int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, n, n))
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				x0, x1, y0, y1 := x*side/n, (x+1)*side/n, y*side/n, (y+1)*side/n
				var r, g, b, a, cnt float64
				for sy := y0; sy < y1; sy++ {
					for sx := x0; sx < x1; sx++ {
						c := crop.NRGBAAt(sx, sy)
						w := float64(c.A)
						r, g, b, a, cnt = r+float64(c.R)*w, g+float64(c.G)*w, b+float64(c.B)*w, a+w, cnt+1
					}
				}
				if a > 0 {
					img.SetNRGBA(x, y, color.NRGBA{uint8(r/a + 0.5), uint8(g/a + 0.5), uint8(b/a + 0.5), uint8(a/cnt + 0.5)})
				}
			}
		}
		return img
	}
	scaled := func(n int) []byte {
		var buf bytes.Buffer
		png.Encode(&buf, pix(n))
		return buf.Bytes()
	}
	// small icon sizes are stored as plain bitmaps (every Windows part reads those); 256 px as PNG
	bmp := func(n int) []byte {
		img := pix(n)
		mask := ((n + 31) / 32) * 4
		b := make([]byte, 40+n*n*4+mask*n)
		binary.LittleEndian.PutUint32(b[0:], 40)
		binary.LittleEndian.PutUint32(b[4:], uint32(n))
		binary.LittleEndian.PutUint32(b[8:], uint32(2*n))
		binary.LittleEndian.PutUint16(b[12:], 1)
		binary.LittleEndian.PutUint16(b[14:], 32)
		binary.LittleEndian.PutUint32(b[20:], uint32(n*n*4+mask*n))
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				c := img.NRGBAAt(x, n-1-y)
				o := 40 + (y*n+x)*4
				b[o], b[o+1], b[o+2], b[o+3] = c.B, c.G, c.R, c.A
			}
		}
		return b
	}
	sizes := []int{16, 24, 32, 48, 64, 256}
	var imgs [][]byte
	for _, n := range sizes {
		if n < 256 {
			imgs = append(imgs, bmp(n))
		} else {
			imgs = append(imgs, scaled(n))
		}
	}
	head := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(head[2:], 1)
	binary.LittleEndian.PutUint16(head[4:], uint16(len(sizes)))
	off := len(head)
	for i, n := range sizes {
		e := head[6+16*i:]
		if n < 256 {
			e[0], e[1] = byte(n), byte(n)
		}
		binary.LittleEndian.PutUint16(e[4:], 1)
		binary.LittleEndian.PutUint16(e[6:], 32)
		binary.LittleEndian.PutUint32(e[8:], uint32(len(imgs[i])))
		binary.LittleEndian.PutUint32(e[12:], uint32(off))
		off += len(imgs[i])
	}
	ico := head
	for _, b := range imgs {
		ico = append(ico, b...)
	}
	must(os.WriteFile("build/icon.ico", ico, 0644))
	must(os.WriteFile("build/icon256.png", scaled(256), 0644))
	must(os.WriteFile("server/web/logo.png", scaled(96), 0644))
	fmt.Println("icon.ico", len(ico), "bytes; server/web/logo.png written")
}

func must(err error) {
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
