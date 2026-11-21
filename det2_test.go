package main

import "testing"

func TestFindBoxesSynthetic(t *testing.T) {
	// prob map 128x896 với 1 "dòng chữ" chữ nhật ở giữa
	ph, pw := 128, 896
	prob := make([]float32, ph*pw)
	for y := 50; y < 80; y++ {
		for x := 40; x < 700; x++ {
			prob[y*pw+x] = 0.95
		}
	}
	p := &DetParams{LimitSide: 960, LimitType: "max", Thresh: 0.3, BoxThresh: 0.5, Unclip: 1.6, MinSize: 3}
	boxes := FindBoxes(prob, ph, pw, 137, 900, p)
	t.Logf("boxes=%d", len(boxes))
	for _, b := range boxes {
		t.Logf("  box=%v score=%.3f", b.Box, b.Score)
	}
	if len(boxes) == 0 {
		t.Fatal("không tìm thấy box nào trong prob map tổng hợp")
	}
}
