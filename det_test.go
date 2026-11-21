package main

import "testing"

func TestDetPreDims(t *testing.T) {
	img := &RGB{W: 900, H: 137, Data: make([]uint8, 900*137*3)}
	p := &DetParams{LimitSide: 960, LimitType: "max", Thresh: 0.3, BoxThresh: 0.5, Unclip: 1.6, MinSize: 3}
	x, nh, nw := DetPreprocess(img, p)
	t.Logf("nh=%d nw=%d len=%d", nh, nw, len(x))
	if nh != 128 || nw != 896 {
		t.Fatalf("kỳ vọng 128x896, nhận %dx%d", nh, nw)
	}
}
