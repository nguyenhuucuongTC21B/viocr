package main

// corrector.go — M4 (Vietnamese Corrector): hiệu chỉnh tiếng Việt CÓ KIỂM SOÁT.
// Thiết kế theo tài liệu v3.0 mục 4.4:
//   - Từ điển âm tiết tiếng Việt (assets/vi_syllables.txt, ~6.6k âm tiết,
//     sinh từ từ điển chính tả hunspell vi — nguồn công khai).
//   - Khoảng cách sửa có TRỌNG SỐ dấu (cặp â/ă, ê/é gần nhau hơn vì hình + vị
//     trí dấu gần nhau; đ/d, x/s nhầm phổ biến của OCR).
//   - Quy tắc can thiệp BA điều kiện đồng thời: âm tiết vô nghĩa (hoặc tin cậy
//     thấp) + tồn tại ứng viên hợp lệ trong bán kính sửa nhỏ + ứng viên KHÔNG
//     thuộc nhóm miễn sửa (chứa số, mã hiệu, IN HOA toàn phần...).
//   - Mọi chỗ sửa đều ghi log (Correction) để GUI v3 liệt kê xem lại.

import (
	"embed"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

//go:embed assets/vi_syllables.txt
var syllFS embed.FS

type corrector struct {
	syll    map[string]struct{} // âm tiết hợp lệ (lowercase)
	sorted  []string            // danh sách đã sắp (để lọc ứng viên theo độ dài)
	reSyll  *regexp.Regexp      // tách token chữ
}

var corInstance *corrector

// getCorrector — nạp từ điển một lần duy nhất.
func getCorrector() *corrector {
	if corInstance != nil {
		return corInstance
	}
	b, err := syllFS.ReadFile("assets/vi_syllables.txt")
	if err != nil {
		corInstance = &corrector{syll: map[string]struct{}{}}
		return corInstance
	}
	c := &corrector{
		syll:   map[string]struct{}{},
		reSyll: regexp.MustCompile(`^[\p{L}]+$`),
	}
	for _, line := range strings.Split(string(b), "\n") {
		w := strings.TrimSpace(line)
		if w == "" {
			continue
		}
		c.syll[strings.ToLower(w)] = struct{}{}
	}
	c.sorted = make([]string, 0, len(c.syll))
	for w := range c.syll {
		c.sorted = append(c.sorted, w)
	}
	corInstance = c
	return c
}

// IsValidSyllable — chuỗi có phải âm tiết tiếng Việt hợp lệ (bỏ qua hoa/thường)?
func IsValidSyllable(s string) bool {
	c := getCorrector()
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return true // khoảng trắng rỗng không phải lỗi
	}
	_, ok := c.syll[s]
	return ok
}

// ValidSyllableRatio — tỉ lệ token chữ hợp lệ trong chuỗi (dùng để chấm điểm
// trang ở Pha 1 và phát hiện "PDF chữ giả").
func ValidSyllableRatio(s string) float64 {
	c := getCorrector()
	toks := tokenWords(s)
	if len(toks) == 0 {
		return 1
	}
	ok := 0
	for _, t := range toks {
		if _, good := c.syll[strings.ToLower(t)]; good {
			ok++
		}
	}
	return float64(ok) / float64(len(toks))
}

// tokenWords — các token thuần chữ trong chuỗi.
func tokenWords(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if unicode.IsLetter(r) {
			cur += string(r)
		} else if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// ═══════════════ Khoảng cách sửa có trọng số dấu ═══════════════

// toneSet — các dấu thanh (hoặc không dấu). Hai âm tiết cùng phần gốc, khác
// dấu thanh → chi phí 0.6 (OCR hay nhầm dấu).
// vowelMarks — dấu móm/khẩu (horn/circumflex/breve) → chi phí 0.75.
func runeCost(a, b rune) float64 {
	if a == b {
		return 0
	}
	ta, ba := decompose(a)
	tb, bb := decompose(b)
	if ba == bb {
		// cùng chữ gốc
		if isToneMark(ta) && isToneMark(tb) {
			return 0.6 // khác dấu thanh
		}
		return 0.75 // khác dấu hình (ă vs â, ơ vs ớ...)
	}
	la, lb := unicode.ToLower(a), unicode.ToLower(b)
	// cặp nhầm phổ biến của OCR tiếng Việt
	switch {
	case pairIn(la, lb, 'd', 'đ'):
		return 0.8
	case pairIn(la, lb, 'x', 's'), pairIn(la, lb, 'i', 'l'), pairIn(la, lb, 'n', 'm'),
		pairIn(la, lb, 'r', 'n'), pairIn(la, lb, 'u', 'v'), pairIn(la, lb, 'ố', 'ộ'):
		return 0.85
	}
	return 1.0
}

func pairIn(a, b, x, y rune) bool {
	return (a == x && b == y) || (a == y && b == x)
}

// decompose tách rune thành (phần dấu combining, chữ gốc) qua chuẩn NFD.
func decompose(r rune) (marks string, base rune) {
	s := norm.NFD.String(strings.ToLower(string(r)))
	d := []rune(s)
	base = d[0]
	for _, m := range d[1:] {
		marks += string(m)
	}
	return marks, base
}

func isToneMark(marks string) bool {
	// dấu thanh: sắc huyền hỏi ngã nặng (U+0301,0300,0309,0303,0323)
	for _, m := range marks {
		switch m {
		case 0x0301, 0x0300, 0x0309, 0x0303, 0x0323:
			return true
		}
	}
	return false
}

// weightedED — khoảng cách sửa có trọng số giữa hai âm tiết (đã lowercase).
// Ngưỡng cắt: trả về sớm nếu vượt limit.
func weightedED(a, b string, limit float64) float64 {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if float64(absI(la-lb)) > limit+0.01 {
		return limit + 1
	}
	// DP hàng trước
	prev := make([]float64, lb+1)
	cur := make([]float64, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = float64(j)
	}
	for i := 1; i <= la; i++ {
		cur[0] = float64(i)
		best := cur[0]
		for j := 1; j <= lb; j++ {
			sub := prev[j-1] + runeCost(ra[i-1], rb[j-1])
			del := prev[j] + 1.0
			ins := cur[j-1] + 1.0
			cur[j] = minF(sub, minF(del, ins))
			if cur[j] < best {
				best = cur[j]
			}
		}
		if best > limit+0.01 {
			return limit + 1 // cắt sớm
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

// ═══════════════ Quy tắc can thiệp + whitelist ═══════════════

const (
	corrMaxCost    = 1.0  // bán kính sửa: ứng viên phải có chi phí ≤ 1.0
	corrLowConf    = 0.75 // tin cậy dưới ngưỡng → cho phép sửa cả âm tiết "hợp lệ"
	corrMinTokenLen = 2   // âm tiết 1 ký tự không sửa
)

// reWhitelist — nhóm miễn sửa: mã hiệu, số liệu, ký hiệu la tinh lẻ.
var reWhitelist = regexp.MustCompile(`(?i)(\d|[a-z])`)

// isProtectable — token miễn sửa (chứa số/chữ la tinh lẻ, IN HOA toàn phần,
// quá ngắn, hoặc có ký tự đặc biệt).
func isProtectable(tok string) bool {
	if len([]rune(tok)) < corrMinTokenLen {
		return true
	}
	if strings.ToUpper(tok) == tok && strings.ToLower(tok) != tok {
		return true // IN HOA toàn phần: có thể là mã hiệu (VKS, QĐ...)
	}
	if reWhitelist.MatchString(tok) && !hasVietLetter(tok) {
		return true // chứa số/chữ latin không dấu → mã hiệu kiểu "số: 12/2026"
	}
	return false
}

// hasVietLetter — có ký tự tiếng Việt có dấu (không phải latin cơ bản).
func hasVietLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && r > 0x7F {
			return true
		}
	}
	return false
}

// candidates — mọi ứng viên hợp lệ trong bán kính sửa, trả về (tốt nhất,
// chi phí tốt nhất, số ứng viên cùng chi phí tốt nhất).
func (c *corrector) candidates(tok string) (string, float64, int) {
	tl := strings.ToLower(tok)
	best := ""
	bestCost := corrMaxCost + 0.01
	ties := 0
	for _, s := range c.sorted {
		if absI(len([]rune(s))-len([]rune(tl))) > 2 {
			continue
		}
		cost := weightedED(tl, s, corrMaxCost)
		if cost <= corrMaxCost {
			if cost < bestCost-1e-9 {
				bestCost = cost
				best = s
				ties = 1
			} else if cost <= bestCost+1e-9 && s != best {
				ties++
			}
		}
	}
	return best, bestCost, ties
}

// fixToken — trả về (kết quả, đã sửa?, ứng viên — để log).
// Quy tắc alpha: CHỈ sửa âm tiết VÔ NGHĨA (không có trong từ điển) và chỉ khi
// ứng viên hợp lệ là DUY NHẤT trong bán kính sửa. Âm tiết hợp lệ — kể cả khi
// tin cậy OCR thấp — không bị đụng tới (phòng sửa sai tên riêng/mã hiệu; việc
// sửa theo ngữ cảnh và thống kê dấu là nội dung v3.0-beta).
func fixToken(tok string, conf float64) (string, bool) {
	_ = conf // alpha: bỏ qua tin cậy — chỉ loại âm tiết vô nghĩa
	if !getCorrector().reSyll.MatchString(tok) || isProtectable(tok) {
		return tok, false
	}
	if IsValidSyllable(tok) {
		return tok, false
	}
	best, cost, ties := getCorrector().candidates(tok)
	if best == "" || cost > corrMaxCost || strings.EqualFold(best, tok) {
		return tok, false // không có ứng viên, hoặc ứng viên chính là token
	}
	if ties > 1 {
		// nhiều ứng viên ngang điểm → rủi ro sửa sai, bỏ qua (bảo thủ)
		return tok, false
	}
	// giữ HOA/thường: token gốc viết hoa đầu → ứng viên viết hoa đầu
	return matchCase(tok, best), true
}

// matchCase — giữ kiểu hoa/thường của token gốc.
func matchCase(orig, fixed string) string {
	if orig == strings.ToUpper(orig) {
		return strings.ToUpper(fixed)
	}
	runes := []rune(orig)
	fr := []rune(fixed)
	if len(runes) > 0 && unicode.IsUpper(runes[0]) && len(fr) > 0 {
		fr[0] = unicode.ToUpper(fr[0])
	}
	return string(fr)
}

// CorrectText — hiệu chỉnh một chuỗi, ghi log các chỗ sửa vào out.
// Trả về chuỗi đã sửa. sepConf là độ tin cậy gán cho token không lấy được
// từ run (mặc định 1.0 với đường Text).
func CorrectText(s string, conf float64, page, blockIdx int, out *[]Correction) string {
	if s == "" {
		return s
	}
	// tách giữ nguyên dấu câu và khoảng trắng
	var b strings.Builder
	cur := ""
	flush := func() {
		if cur == "" {
			return
		}
		fixed, changed := fixToken(cur, conf)
		if changed && out != nil {
			*out = append(*out, Correction{
				Page: page, Orig: cur, Fixed: fixed, Conf: conf, Block: blockIdx,
			})
		}
		b.WriteString(fixed)
		cur = ""
	}
	for _, r := range s {
		if unicode.IsLetter(r) {
			cur += string(r)
		} else {
			flush()
			b.WriteRune(r)
		}
	}
	flush()
	return b.String()
}

// CorrectRunSlice — hiệu chỉnh text của một slice Run (đường Text: conf=1).
func CorrectRunSlice(runs []Run, page, blockIdx int, out *[]Correction) {
	for i := range runs {
		runs[i].Text = CorrectText(runs[i].Text, runs[i].Conf, page, blockIdx, out)
	}
}
