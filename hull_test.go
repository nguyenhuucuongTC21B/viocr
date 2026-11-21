package main

import "testing"

func TestHullRect(t *testing.T) {
	var pts [][2]float64
	for y := 50; y < 80; y++ {
		for x := 40; x < 700; x++ {
			pts = append(pts, [2]float64{float64(x), float64(y)})
		}
	}
	hull := convexHull(pts)
	t.Logf("hull len=%d", len(hull))
	for _, p := range hull {
		t.Logf("  (%v,%v)", p[0], p[1])
	}
	r := minAreaRect(pts)
	t.Logf("rect=%v", r)
	if r == nil || r[2] < 600 || r[3] < 25 {
		t.Fatalf("rect sai")
	}
}
