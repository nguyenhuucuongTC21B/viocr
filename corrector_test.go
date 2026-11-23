package main

// corrector_test.go — kiểm tra M4: từ điển âm tiết + quy tắc can thiệp.

import (
	"strings"
	"testing"
)

func TestIsValidSyllable(t *testing.T) {
	good := []string{"của", "yếu", "tướng", "gì", "việt", "hành", "chính", "quyền", "Nguyễn"}
	bad := []string{"kxyz", "qqq", "zzzà"}
	for _, s := range good {
		if !IsValidSyllable(s) {
			t.Errorf("'%s' phải là âm tiết hợp lệ", s)
		}
	}
	for _, s := range bad {
		if IsValidSyllable(s) {
			t.Errorf("'%s' không được coi là hợp lệ", s)
		}
	}
}

func TestCandidateForMisspelled(t *testing.T) {
	// Lưới dấu tiếng Việt dày: "quyẻn" có nhiều ứng viên ngang điểm
	// (quyến/quyền/quyễn/quyện đều khác dấu thanh 0.6) → quy tắc BẢO THỦ
	// (ties > 1 → không sửa) phải nhảy vào, không sửa mù quáng.
	best, _, ties := getCorrector().candidates("quyẻn")
	if best == "" || ties < 2 {
		t.Fatalf("kỳ vọng nhiều ứng viên ngang điểm cho 'quyẻn', nhận %q ties=%d", best, ties)
	}
}

func TestCorrectTextFixesUniqueCandidate(t *testing.T) {
	// "củas" — dư ký tự cuối: ứng viên duy nhất "của" (xóa 1 ký tự) → sửa được
	var log []Correction
	out := CorrectText("củas yếu", 0.4, 1, 0, &log)
	if !strings.Contains(out, "của") {
		t.Fatalf("không sửa được 'củas' → 'của': %q", out)
	}
	if len(log) != 1 || log[0].Orig != "củas" || log[0].Fixed != "của" {
		t.Fatalf("log hiệu chỉnh sai: %+v", log)
	}
}

func TestCorrectTextProtectsNumbers(t *testing.T) {
	var log []Correction
	out := CorrectText("Văn bản số 12/2026 nhàn công", 0.4, 1, 0, &log)
	if !strings.Contains(out, "12/2026") {
		t.Fatalf("mã hiệu bị sửa: %q", out)
	}
}

func TestWeightedEDToneVsVowel(t *testing.T) {
	// khác dấu thanh cùng chữ gốc: chi phí thấp hơn khác dấu hình
	c1 := weightedED("á", "à", 1.0)  // sắc vs huyền (0.6)
	c2 := weightedED("ă", "â", 1.0)  // breve vs circumflex (0.75)
	c3 := weightedED("đ", "d", 1.0)  // 0.8
	if c1 >= c2 {
		t.Errorf("nhầm dấu thanh (%.2f) phải rẻ hơn nhầm dấu hình (%.2f)", c1, c2)
	}
	if c3 != 0.8 {
		t.Errorf("đ/d chi phí phải 0.8, nhận %.2f", c3)
	}
}
