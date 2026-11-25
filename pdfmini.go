package main

// pdfmini.go — M1 (PDF Intelligence): bộ trích LỚP CHỮ của PDF thuần Go,
// không phụ thuộc ngoài. Phục vụ "đường Text": trang PDF sinh từ Word có sẵn
// chuỗi ký tự + font + tọa độ — đọc trực tiếp cho CER ≈ 0 thay vì OCR lại.
//
// Phạm vi hỗ trợ (đủ cho PDF xuất từ Word/LibreOffice/reportlab):
//   - xref bảng cổ điển LẪN xref stream (PDF 1.5+) qua quét "N G obj" + ObjStm
//   - stream FlateDecode; mã hóa (encrypted) báo lỗi rõ ràng
//   - toán tử văn bản: BT/ET Tf Td TD Tm T* TL Tj TJ ' " (cả CTM qua cm)
//   - ToUnicode CMap (beginbfchar/beginbfrange) → Unicode; thiếu CMap thì
//     rớt về Latin-1, tỉ lệ âm tiết hợp lệ thấp → trang bị đánh dấu NeedsOCR.

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ═══════════════════ Quét object ═══════════════════

var reObjHead = regexp.MustCompile(`(?:^|[\s>\r\n])(\d{1,10})\s+(\d{1,5})\s+obj\b`)
var reWs = regexp.MustCompile(`\s+`)

type pdfObj struct {
	num       int
	raw       []byte // phần trước stream (dict/mảng/...) hoặc toàn bộ
	stream    []byte // dữ liệu stream ĐÃ GIẢI NÉP (nil nếu không phải stream)
	hasStream bool
}

type pdfFile struct {
	objs    map[int]*pdfObj
	encrypt bool
}

// parsePDF quét toàn bộ object trong file (kể cả object nằm trong ObjStm).
// Cách quét "thô nhưng bền": không phụ thuộc xref — chịu được xref hỏng,
// xref stream, startxref lệch. Chi phí O(file) một lượt.
func parsePDF(data []byte) (*pdfFile, error) {
	pf := &pdfFile{objs: map[int]*pdfObj{}}
	if bytes.Contains(data, []byte("/Encrypt")) {
		// kiểm tra kỹ hơn: chỉ báo lỗi nếu /Encrypt nằm trong dict (tránh chuỗi ngẫu nhiên)
		if m := regexp.MustCompile(`/Encrypt\s+\d+\s+\d+\s+R`).FindIndex(data); m != nil {
			return nil, fmt.Errorf("PDF có mật mã (/Encrypt) — chưa hỗ trợ, hãy lưu lại PDF không mã hóa")
		}
	}
	for _, loc := range reObjHead.FindAllSubmatchIndex(data, -1) {
		num, _ := strconv.Atoi(string(data[loc[2]:loc[3]]))
		start := loc[1]
		endStream := bytes.Index(data[start:], []byte("endobj"))
		if endStream < 0 {
			continue
		}
		body := data[start : start+endStream]
		obj := &pdfObj{num: num, raw: body}
		// có stream? — tìm "stream" KHÔNG nằm trong chuỗi "(...stream...)"
		if si := findStreamKeyword(body); si >= 0 {
			obj.hasStream = true
			sdata := body[si:]
			sdata = bytes.TrimLeft(sdata, " \t\r\n")
			// bỏ \r\n sau "stream"
			sdata = bytes.TrimPrefix(sdata, []byte("\r\n"))
			sdata = bytes.TrimPrefix(sdata, []byte("\n"))
			// /Length có thể là tham chiếu — thử giải, không được thì tìm endstream
			rawStream := takeStreamByLength(obj.raw[:si], sdata)
			obj.stream = inflateStream(obj.raw[:si], rawStream)
			obj.raw = obj.raw[:si] // phần dict
		}
		pf.objs[num] = obj
	}
	// ObjStm: nạp object nằm trong object stream
	for _, o := range pf.objs {
		if o.hasStream && isObjStm(o) {
			pf.expandObjStm(o)
		}
	}
	if len(pf.objs) == 0 {
		return nil, fmt.Errorf("không tìm thấy object PDF nào — file hỏng hoặc không phải PDF")
	}
	return pf, nil
}

// findStreamKeyword tìm từ khóa "stream" ngoài chuỗi ký tự/Comment.
func findStreamKeyword(b []byte) int {
	depth := 0 // trong chuỗi (...)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if depth > 0 {
			if c == '\\' {
				i++
			} else if c == '(' {
				depth++
			} else if c == ')' {
				depth--
			}
			continue
		}
		switch c {
		case '(':
			depth = 1
		case '%': // comment tới hết dòng
			for i < len(b) && b[i] != '\n' {
				i++
			}
		case 's':
			if i+6 < len(b) && bytes.Equal(b[i:i+6], []byte("stream")) {
				nc := b[i+6]
				if nc == ' ' || nc == '\n' || nc == '\r' || nc == '\t' {
					return i + 6
				}
			}
		}
	}
	return -1
}

// takeStreamByLength cắt đúng /Length byte. LƯU Ý QUAN TRỌNG: nếu /Length là
// tham chiếu gián tiếp (/Length 27 0 R — Aspose.Words luôn ghi vậy), số "27"
// là SỐ OBJECT chứ không phải độ dài — cắt nhầm sẽ phá toàn bộ stream (lỗi
// thật trên "Nhận xét nơi cư trú 2026": cả trang rớt về 0 run). Khi đó quét
// tới "endstream" gần nhất.
func takeStreamByLength(dictPart, sdata []byte) []byte {
	m := regexp.MustCompile(`/Length\s+(\d+)(\s+\d+\s+R)?\b`).FindSubmatch(dictPart)
	if m != nil && len(m[2]) == 0 { // Length trực tiếp (không có "N 0 R" phía sau)
		n, err := strconv.Atoi(string(m[1]))
		if err == nil && n >= 0 && n <= len(sdata) {
			return sdata[:n]
		}
	}
	if e := bytes.Index(sdata, []byte("endstream")); e >= 0 {
		return bytes.TrimRight(sdata[:e], "\r\n")
	}
	return sdata
}

// inflateStream giải FlateDecode (zlib) và ASCII85Decode+FlateDecode; filter
// khác trả về nguyên bản (caller sẽ bỏ qua stream không đọc được).
func inflateStream(dictPart, sdata []byte) []byte {
	if bytes.Contains(dictPart, []byte("/ASCII85Decode")) {
		sdata = ascii85Decode(sdata)
	}
	if bytes.Contains(dictPart, []byte("/FlateDecode")) {
		r, err := zlib.NewReader(bytes.NewReader(sdata))
		if err != nil {
			return nil
		}
		out, err := io.ReadAll(io.LimitReader(r, 64<<20))
		if err != nil && len(out) == 0 {
			return nil
		}
		return out
	}
	if bytes.Contains(dictPart, []byte("/ASCII85Decode")) {
		return sdata // đã giải ASCII85 (không có Flate)
	}
	return sdata // không nén hoặc filter khác
}

// ascii85Decode — giải ASCII85 (bộ 4 byte ← 5 ký tự; 'z' = nhóm 0; ~> hết).
func ascii85Decode(s []byte) []byte {
	var out []byte
	group := make([]byte, 0, 5)
	for _, c := range s {
		switch {
		case c == 'z' && len(group) == 0:
			out = append(out, 0, 0, 0, 0)
		case c == '~': // kết thúc ~> — bỏ phần còn lại
			return out
		case c == ' ' || c == '\n' || c == '\r' || c == '\t':
			continue
		case c >= '!' && c <= 'u':
			group = append(group, c)
			if len(group) == 5 {
				out = append(out, a85Group(group)...)
				group = group[:0]
			}
		}
	}
	if len(group) > 1 { // nhóm cuối chưa đủ 5 — pad bằng 'u' (110)
		for len(group) < 5 {
			group = append(group, 'u')
		}
		full := a85Group(group)
		out = append(out, full[:len(group)-1]...)
	}
	return out
}

func a85Group(g []byte) []byte {
	v := uint32(0)
	for _, c := range g {
		v = v*85 + uint32(c-'!')
	}
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func isObjStm(o *pdfObj) bool {
	return bytes.Contains(o.raw, []byte("/ObjStm"))
}

// expandObjStm giải nén object stream: /N object, bảng "sốobj offset" ở /First.
func (pf *pdfFile) expandObjStm(o *pdfObj) {
	n := intFromDict(o.raw, "/N")
	first := intFromDict(o.raw, "/First")
	if o.stream == nil || n <= 0 || first <= 0 || first > len(o.stream) {
		return
	}
	head := reWs.Split(strings.TrimSpace(string(o.stream[:first])), -1)
	type pair struct{ num, off int }
	var pairs []pair
	for i := 0; i+1 < len(head) && i/2 < n; i += 2 {
		a, e1 := strconv.Atoi(head[i])
		b, e2 := strconv.Atoi(head[i+1])
		if e1 != nil || e2 != nil {
			return
		}
		pairs = append(pairs, pair{a, b})
	}
	for i, pr := range pairs {
		end := len(o.stream)
		if i+1 < len(pairs) {
			end = first + pairs[i+1].off
		}
		s := first + pr.off
		if s > len(o.stream) || end > len(o.stream) || s > end {
			continue
		}
		pf.objs[pr.num] = &pdfObj{num: pr.num, raw: o.stream[s:end]}
	}
}

func intFromDict(raw []byte, key string) int {
	m := regexp.MustCompile(regexp.QuoteMeta(key) + `\s+(-?\d+)`).FindSubmatch(raw)
	if m == nil {
		return -1
	}
	v, _ := strconv.Atoi(string(m[1]))
	return v
}

// ═══════════════════ Mini-parser giá trị PDF ═══════════════════

// pdfValue — giá trị đơn giản: name "x", number, string, ref (num trong R),
// array, dict. Dùng cho điều hướng cấu trúc, không phải parser đầy đủ.
type pdfValue struct {
	kind byte // 'n' name, 'd' number, 's' string, 'r' ref, 'a' array, 'm' dict
	num  float64
	str  string
	list []pdfValue
	dict map[string]pdfValue
	ref  int
}

func (v pdfValue) name() string {
	if v.kind == 'n' {
		return v.str
	}
	return ""
}
func (v pdfValue) get(key string) pdfValue {
	if v.kind == 'm' {
		return v.dict[key]
	}
	return pdfValue{}
}

// parseObjectAt phân tích giá trị ở đầu raw (dict/mảng/số/name/ref/string).
func parseObjectAt(raw []byte) pdfValue {
	t := &tok{b: raw}
	t.skip()
	return t.value()
}

type tok struct {
	b []byte
	i int
}

func (t *tok) skip() {
	for t.i < len(t.b) {
		c := t.b[t.i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			t.i++
		} else if c == '%' {
			for t.i < len(t.b) && t.b[t.i] != '\n' {
				t.i++
			}
		} else {
			return
		}
	}
}

func (t *tok) peek() byte {
	t.skip()
	if t.i < len(t.b) {
		return t.b[t.i]
	}
	return 0
}

func (t *tok) value() pdfValue {
	c := t.peek()
	if c == 0 {
		return pdfValue{}
	}
	switch {
	case c == '/':
		t.i++
		s := t.i
		for t.i < len(t.b) && !isDelim(t.b[t.i]) {
			t.i++
		}
		return pdfValue{kind: 'n', str: string(t.b[s:t.i])}
	case c == '(':
		str, _ := t.literal()
		return pdfValue{kind: 's', str: str}
	case c == '<' && t.i+1 < len(t.b) && t.b[t.i+1] == '<':
		return t.dictV()
	case c == '<':
		t.i++
		s := t.i
		for t.i < len(t.b) && t.b[t.i] != '>' {
			t.i++
		}
		if t.i < len(t.b) {
			t.i++
		}
		return pdfValue{kind: 's', str: decodeHexStr(string(t.b[s:t.i]))}
	case c == '[':
		t.i++
		var arr []pdfValue
		for {
			p := t.peek()
			if p == 0 || p == ']' {
				if p == ']' {
					t.i++
				}
				break
			}
			arr = append(arr, t.value())
		}
		return pdfValue{kind: 'a', list: arr}
	case c == '-' || c == '+' || (c >= '0' && c <= '9') || c == '.':
		s := t.i
		if c == '-' || c == '+' {
			t.i++
		}
		for t.i < len(t.b) && (isDigit(t.b[t.i]) || t.b[t.i] == '.') {
			t.i++
		}
		numStr := string(t.b[s:t.i])
		// thử dạng tham chiếu "N G R" (số đứng trước 2 token nữa)
		save := t.i
		t.skip()
		w2 := t.word()
		t.skip()
		w3 := t.word()
		if n1, e1 := strconv.Atoi(numStr); e1 == nil {
			if n2, e2 := strconv.Atoi(w2); e2 == nil && w3 == "R" {
				return pdfValue{kind: 'r', ref: n1, num: float64(n2)}
			}
		}
		t.i = save
		f, _ := strconv.ParseFloat(numStr, 64)
		return pdfValue{kind: 'd', num: f}
	default:
		// từ khóa: true false null
		s := t.i
		for t.i < len(t.b) && !isDelim(t.b[t.i]) {
			t.i++
		}
		return pdfValue{kind: 'n', str: string(t.b[s:t.i])}
	}
}

func (t *tok) word() string {
	s := t.i
	for t.i < len(t.b) && !isDelim(t.b[t.i]) {
		t.i++
	}
	return string(t.b[s:t.i])
}

func (t *tok) dictV() pdfValue {
	t.i += 2 // ">><"? bỏ "<<"
	d := map[string]pdfValue{}
	for {
		p := t.peek()
		if p == 0 {
			break
		}
		if p == '>' && t.i+1 < len(t.b) && t.b[t.i+1] == '>' {
			t.i += 2
			break
		}
		if p != '/' {
			t.i++
			continue
		}
		key := t.value().name()
		v := t.value()
		// giá trị sau key có thể là mảng [[...][...]] v.v. — lưu như thường
		d[key] = v
	}
	return pdfValue{kind: 'm', dict: d}
}

// literal đọc chuỗi ( ) có escape, trả chuỗi UTF-8 (byte gốc).
func (t *tok) literal() (string, bool) {
	t.i++ // bỏ '('
	var out []byte
	depth := 1
	for t.i < len(t.b) {
		c := t.b[t.i]
		if c == '\\' {
			t.i++
			if t.i >= len(t.b) {
				break
			}
			e := t.b[t.i]
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '(', ')', '\\':
				out = append(out, e)
			case '\r':
				if t.i+1 < len(t.b) && t.b[t.i+1] == '\n' {
					t.i++
				}
			case '\n':
				// tiếp dòng
			default:
				if e >= '0' && e <= '7' {
					oct := 0
					digits := 0
					for digits < 3 && t.i < len(t.b) && t.b[t.i] >= '0' && t.b[t.i] <= '7' {
						oct = oct*8 + int(t.b[t.i]-'0')
						t.i++
						digits++
					}
					t.i--
					out = append(out, byte(oct&0xFF))
				} else {
					out = append(out, e)
				}
			}
			t.i++
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				t.i++
				return string(out), true
			}
		}
		out = append(out, c)
		t.i++
	}
	return string(out), true
}

func decodeHexStr(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if len(s)%2 == 1 {
		s += "0"
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(b); i++ {
		hi := hexVal(s[2*i])
		lo := hexVal(s[2*i+1])
		b[i] = byte(hi<<4 | lo)
	}
	return string(b)
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '/', '(', ')', '<', '>', '[', ']', '{', '}', '%':
		return true
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// resolve theo tham chiếu N G R → object (raw hoặc stream).
func (pf *pdfFile) resolve(v pdfValue) *pdfObj {
	if v.kind == 'r' {
		return pf.objs[v.ref]
	}
	return nil
}

// objBytes — nội dung logic của object: raw của dict, stream nếu là stream.
func (pf *pdfFile) objBytes(v pdfValue) ([]byte, *pdfObj) {
	o := pf.resolve(v)
	if o == nil {
		return nil, nil
	}
	if o.hasStream && o.stream != nil {
		return o.stream, o
	}
	return o.raw, o
}

// ═══════════════════ ToUnicode CMap ═══════════════════

// cmap — bảng mã glyph → chuỗi Unicode của một font.
type cmap struct {
	codeLen int
	m       map[int]string
	hi, lo  map[int]string // fallback riêng không cần — gộp vào m
}

func newCMap() *cmap { return &cmap{codeLen: 1, m: map[int]string{}} }

// parseCMap phân tích stream ToUnicode (bfchar/bfrange).
func parseCMap(data []byte) *cmap {
	cm := newCMap()
	text := string(data)
	// độ dài mã = số byte của hex NGUỒN. Quét cả bfchar LẪN bfrange (hex lo/hi
	// là mã nguồn): WeasyPrint dùng bfrange thuần cho Identity-H — nếu chỉ quét
	// bfchar thì codeLen=1 và cả trang thành rác Latin-1. Hex ĐÍCH của bfchar
	// (UTF-16, có thể nhiều đơn vị) không được dùng để dò.
	reHexPair := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	reRange1 := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	reRange2 := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*\[([^\]]*)\]`)
	for _, blk := range regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`).FindAllStringSubmatch(text, -1) {
		for _, m := range reHexPair.FindAllStringSubmatch(blk[1], -1) {
			if l := len(m[1]) / 2; l > cm.codeLen && l <= 8 {
				cm.codeLen = l
			}
		}
	}
	for _, blk := range regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`).FindAllStringSubmatch(text, -1) {
		for _, m := range reRange1.FindAllStringSubmatch(blk[1], -1) {
			if l := len(m[1]) / 2; l > cm.codeLen && l <= 8 {
				cm.codeLen = l
			}
		}
		for _, m := range reRange2.FindAllStringSubmatch(blk[1], -1) {
			if l := len(m[1]) / 2; l > cm.codeLen && l <= 8 {
				cm.codeLen = l
			}
		}
	}
	// bfchar
	for _, blk := range regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`).FindAllStringSubmatch(text, -1) {
		for _, m := range regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`).FindAllStringSubmatch(blk[1], -1) {
			src, err := strconv.ParseUint(m[1], 16, 64)
			if err != nil {
				continue
			}
			cm.m[int(src)] = utf16HexToString(m[2])
		}
	}
	// bfrange 2 dạng: <lo> <hi> <dstStart> | <lo> <hi> [ <d1> <d2> ... ]
	for _, blk := range regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`).FindAllStringSubmatch(text, -1) {
		body := blk[1]
		for _, m := range reRange1.FindAllStringSubmatch(body, -1) {
			lo, e1 := strconv.ParseUint(m[1], 16, 64)
			hi, e2 := strconv.ParseUint(m[2], 16, 64)
			if e1 != nil || e2 != nil || hi < lo || hi-lo > 65535 {
				continue
			}
			start := utf16HexToString(m[3])
			for c := int(lo); c <= int(hi); c++ {
				cm.m[c] = shiftUTF16(start, c-int(lo))
			}
		}
		for _, m := range reRange2.FindAllStringSubmatch(body, -1) {
			lo, e1 := strconv.ParseUint(m[1], 16, 64)
			hi, e2 := strconv.ParseUint(m[2], 16, 64)
			if e1 != nil || e2 != nil {
				continue
			}
			vals := regexp.MustCompile(`<([0-9A-Fa-f]+)>`).FindAllStringSubmatch(m[3], -1)
			for i, vv := range vals {
				if int(lo)+i > int(hi) {
					break
				}
				cm.m[int(lo)+i] = utf16HexToString(vv[1])
			}
		}
	}
	return cm
}

// utf16HexToString: hex → UTF-16BE → Go string.
func utf16HexToString(h string) string {
	b := []byte(decodeHexStr(h))
	if len(b)%2 == 1 {
		b = append(b, 0)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(u))
}

// shiftUTF16 — tăng điểm mã của chuỗi UTF-16 (dùng cho bfrange).
func shiftUTF16(s string, d int) string {
	u := utf16.Encode([]rune(s))
	if len(u) == 0 {
		return s
	}
	u[len(u)-1] = uint16(int(u[len(u)-1]) + d)
	return string(utf16.Decode(u))
}

// decodeStr giải mã 1 chuỗi nội dung theo cmap của font hiện hành.
func (cm *cmap) decodeStr(s string) string {
	if cm == nil {
		return s // Latin-1/UTF-8 thô
	}
	out := ""
	for i := 0; i < len(s); {
		code := 0
		n := 0
		for n < cm.codeLen && i+n < len(s) {
			code = code<<8 | int(s[i+n])
			n++
		}
		i += n
		if v, ok := cm.m[code]; ok {
			out += v
		} else if n == 1 {
			c := rune(s[i-1])
			if c < 32 {
				c = ' ' // byte điều khiển trong subset — coi như khoảng trắng
			}
			out += string(c)
		} else {
			out += " " // mã không có trong cmap — không chèn FFFD làm hỏng chấm điểm
		}
	}
	return out
}

// ═══════════════════ Cây trang ═══════════════════

type pdfPageInfo struct {
	W, H     float64
	Contents [][]byte             // các content stream đã giải nén
	Fonts    map[string]*fontInfo // key trong Tf → font
	Rotate   int
}

type fontInfo struct {
	baseName string
	cm       *cmap
	bold     bool
	// widths — bảng bề rộng glyph thật (đơn vị 1/1000 cỡ chữ): font đơn →
	// /Widths+/FirstChar; font CID → /W của DescendantFonts. Có bảng → tính
	// tiến vị trí CHÍNH XÁC sau Tj (Aspose/Word không ghi Td giữa các từ);
	// không có → ước lượng theo nhóm ký tự.
	widths map[int]float64
}

func (pf *pdfFile) pages() []*pdfObj {
	var out []*pdfObj
	// ưu tiên: catalog → Pages → Kids
	for _, o := range pf.objs {
		if bytes.Contains(o.raw, []byte("/Catalog")) {
			v := parseObjectAt(o.raw)
			if pr := v.get("Pages"); pr.kind == 'r' {
				if pg := pf.objs[pr.ref]; pg != nil && bytes.Contains(pg.raw, []byte("/Pages")) {
					return pf.collectPages(pg, out, 0)
				}
			}
		}
	}
	// fallback: object Pages có Kids lớn nhất
	var best *pdfObj
	for _, o := range pf.objs {
		if o.hasStream || bytes.Contains(o.raw, []byte("/ObjStm")) {
			continue
		}
		if bytes.Contains(o.raw, []byte("/Pages")) && bytes.Contains(o.raw, []byte("/Kids")) {
			if best == nil || len(o.raw) > len(best.raw) {
				best = o
			}
		}
	}
	if best != nil {
		return pf.collectPages(best, out, 0)
	}
	// fallback cuối: mọi object /Type/Page
	for _, o := range pf.objs {
		if rePageObj.Match(o.raw) {
			out = append(out, o)
		}
	}
	return out
}

var rePageObj = regexp.MustCompile(`/Type\s*/Page\b`)
var reMediaBox = regexp.MustCompile(`/MediaBox\s*\[\s*([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s*\]`)
var reRotate = regexp.MustCompile(`/Rotate\s+(-?\d+)`)

// collectPages duyệt Kids đệ quy (giới hạn sâu để tránh vòng).
func (pf *pdfFile) collectPages(node *pdfObj, out []*pdfObj, depth int) []*pdfObj {
	if node == nil || depth > 32 {
		return out
	}
	v := parseObjectAt(node.raw)
	kids := v.get("Kids")
	if kids.kind == 'a' {
		for _, k := range kids.list {
			if k.kind == 'r' {
				out = pf.collectPages(pf.objs[k.ref], out, depth+1)
			}
		}
		return out
	}
	if rePageObj.Match(node.raw) {
		out = append(out, node)
	}
	return out
}

// resourcesForPage — Resources của trang (nếu kế thừa từ cha thì đã nằm trong
// raw của trang sau khi Word export; fallback: tìm /Resources trong node cha
// được làm ở caller).
func pageResources(pf *pdfFile, page *pdfObj) pdfValue {
	v := parseObjectAt(page.raw)
	res := v.get("Resources")
	if res.kind == 'r' {
		if o := pf.resolve(res); o != nil {
			return parseObjectAt(o.raw)
		}
	}
	return res
}

// buildPageInfo — dựng thông tin 1 trang: kích thước, fonts, contents.
func (pf *pdfFile) buildPageInfo(page *pdfObj) (*pdfPageInfo, error) {
	raw := page.raw
	info := &pdfPageInfo{Fonts: map[string]*fontInfo{}}
	if m := reMediaBox.FindSubmatch(raw); m != nil {
		x0, _ := strconv.ParseFloat(string(m[1]), 64)
		y0, _ := strconv.ParseFloat(string(m[2]), 64)
		x1, _ := strconv.ParseFloat(string(m[3]), 64)
		y1, _ := strconv.ParseFloat(string(m[4]), 64)
		info.W, info.H = x1-x0, y1-y0
	} else {
		info.W, info.H = 595, 842 // A4 mặc định
	}
	if m := reRotate.FindSubmatch(raw); m != nil {
		info.Rotate, _ = strconv.Atoi(string(m[1]))
	}
	// Contents: ref đơn hoặc mảng
	v := parseObjectAt(raw)
	contents := v.get("Contents")
	var refs []pdfValue
	switch contents.kind {
	case 'r':
		refs = []pdfValue{contents}
	case 'a':
		refs = contents.list
	}
	for _, rv := range refs {
		if b, _ := pf.objBytes(rv); b != nil {
			info.Contents = append(info.Contents, b)
		}
	}
	// Fonts
	res := pageResources(pf, page)
	fdict := res.get("Font")
	if fdict.kind == 'r' {
		if o := pf.resolve(fdict); o != nil {
			fdict = parseObjectAt(o.raw)
		}
	}
	if fdict.kind == 'm' {
		for key, fv := range fdict.dict {
			var fo *pdfObj
			if fv.kind == 'r' {
				fo = pf.resolve(fv)
			} else if fv.kind == 'm' {
				fo = &pdfObj{raw: rawDictBytes(fv)}
			}
			if fo == nil {
				continue
			}
			fi := &fontInfo{}
			fv2 := parseObjectAt(fo.raw)
			if bf := fv2.get("BaseFont"); bf.kind == 'n' {
				fi.baseName = bf.name()
			} else {
				fi.baseName = key
			}
			fi.bold = strings.Contains(fi.baseName, "Bold") || strings.Contains(fi.baseName, "bold")
			if tu := fv2.get("ToUnicode"); tu.kind == 'r' {
				if o := pf.resolve(tu); o != nil && o.hasStream && o.stream != nil {
					fi.cm = parseCMap(o.stream)
				}
			}
			fi.widths = pf.loadFontWidths(fv2)
			info.Fonts[key] = fi
		}
	}
	return info, nil
}

// loadFontWidths — nạp bảng bề rộng glyph: font đơn (WinAnsi) từ /Widths+
// /FirstChar; font CID (Identity-H) từ /W của DescendantFonts[0], hai dạng
// `c [w1 w2 ...]` và `c1 c2 w`. Trả nil nếu không có (dùng ước lượng).
func (pf *pdfFile) loadFontWidths(fv2 pdfValue) map[int]float64 {
	w := map[int]float64{}
	// 1) font đơn: /Widths [w0 w1 ...] + /FirstChar n
	if ws := fv2.get("Widths"); ws.kind == 'a' {
		first := 0
		if fc := fv2.get("FirstChar"); fc.kind == 'd' {
			first = int(fc.num)
		}
		for i, el := range ws.list {
			if el.kind == 'd' {
				w[first+i] = el.num
			}
		}
		if len(w) > 0 {
			return w
		}
	}
	// 2) font CID: DescendantFonts [ref] → /W [...]
	df := fv2.get("DescendantFonts")
	var fo *pdfObj
	switch df.kind {
	case 'a':
		if len(df.list) > 0 && df.list[0].kind == 'r' {
			fo = pf.resolve(df.list[0])
		}
	case 'r':
		fo = pf.resolve(df)
	}
	if fo == nil {
		return nil
	}
	dv := parseObjectAt(fo.raw)
	wv := dv.get("W")
	if wv.kind != 'a' {
		return nil
	}
	i := 0
	for i < len(wv.list) {
		a := wv.list[i]
		if a.kind != 'd' {
			i++
			continue
		}
		// dạng 1: c [w1 w2 ...]
		if i+1 < len(wv.list) && wv.list[i+1].kind == 'a' {
			c := int(a.num)
			for k, el := range wv.list[i+1].list {
				if el.kind == 'd' {
					w[c+k] = el.num
				}
			}
			i += 2
			continue
		}
		// dạng 2: c1 c2 w
		if i+2 < len(wv.list) && wv.list[i+1].kind == 'd' && wv.list[i+2].kind == 'd' {
			c1, c2 := int(a.num), int(wv.list[i+1].num)
			wd := wv.list[i+2].num
			if c2 >= c1 && c2-c1 <= 65535 {
				for c := c1; c <= c2; c++ {
					w[c] = wd
				}
			}
			i += 3
			continue
		}
		i++
	}
	if len(w) == 0 {
		return nil
	}
	return w
}

// rawDictBytes — tái dựng bytes dict từ giá trị đã parse (cho font inline).
func rawDictBytes(v pdfValue) []byte {
	var b bytes.Buffer
	b.WriteString("<< ")
	for k, val := range v.dict {
		b.WriteString("/" + k + " ")
		switch val.kind {
		case 'n':
			b.WriteString("/" + val.str + " ")
		case 'd':
			b.WriteString(fmtNum(val.num) + " ")
		case 'r':
			b.WriteString(strconv.Itoa(val.ref) + " 0 R ")
		case 's':
			b.WriteString("(" + val.str + ") ")
		}
	}
	b.WriteString(">>")
	return b.Bytes()
}

// ═══════════════════ Bộ thông dịch content stream ═══════════════════

// matrix 6 phần tử [a b c d e f].
type mtx [6]float64

func mulM(m, n mtx) mtx {
	return mtx{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m mtx) apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

func (m mtx) scale() float64 {
	sx := m[0]*m[0] + m[1]*m[1]
	sy := m[2]*m[2] + m[3]*m[3]
	return sqrtF((sx + sy) / 2)
}

// extractRuns — thông dịch content streams, trả về các Run (tọa độ top-left,
// quy về hệ 72dpi của trang).
func extractRuns(info *pdfPageInfo) []Run {
	var runs []Run
	ident := mtx{1, 0, 0, 1, 0, 0}
	ctm := ident
	ctmStack := []mtx{}
	for _, cs := range info.Contents {
		tm := ident
		// Tlm — ma trận DÒNG (text line matrix, PDF spec 9.4.3): Td/TD/T* dịch
		// TỔNG ĐỐI VỚI Tlm (không phải vị trí hiện hành tm đã tự tiến sau Tj);
		// Tm đặt cả Tlm lẫn tm. Nhầm 2 ma trận này làm chữ trôi (lỗi thật trên
		// "Nhận xét nơi cư trú 2026": Aspose định vị từng từ bằng Td).
		tlm := ident
		fontKey := ""
		fontSize := 0.0
		leading := 0.0
		inText := false
		st := &contentTok{b: cs}
		var stack []pdfValue // toán hạng tích lũy giữa các toán tử
		for {
			tokv, op := st.next()
			if op == "" {
				if tokv.kind == 0 {
					break // hết stream
				}
				stack = append(stack, tokv)
				continue
			}
			switch op {
			case "q":
				ctmStack = append(ctmStack, ctm)
				stack = stack[:0]
			case "Q":
				if len(ctmStack) > 0 {
					ctm = ctmStack[len(ctmStack)-1]
					ctmStack = ctmStack[:len(ctmStack)-1]
				}
				stack = stack[:0]
			case "cm":
				if len(stack) >= 6 {
					ctm = mulM(readMtx(stack[len(stack)-6:]), ctm)
				}
				stack = stack[:0]
			case "BT":
				inText = true
				tm = ident
				tlm = ident // BT reset cả ma trận dòng (spec 9.4.3)
				stack = stack[:0]
			case "ET":
				inText = false
				stack = stack[:0]
			case "Tf":
				if len(stack) >= 2 {
					if f, ok := asFloat(stack[len(stack)-1]); ok {
						fontSize = f
					}
					if stack[len(stack)-2].kind == 'n' {
						fontKey = stack[len(stack)-2].str
					}
				}
				stack = stack[:0]
			case "TL":
				if f, ok := asFloat(last(stack, 1)); ok {
					leading = f
				}
				stack = stack[:0]
			case "Td":
				if f1, f2, ok := pair(stack); ok {
					tlm = mulM(mtx{1, 0, 0, 1, f1, f2}, tlm)
					tm = tlm
				}
				stack = stack[:0]
			case "TD":
				if f1, f2, ok := pair(stack); ok {
					leading = -f2
					tlm = mulM(mtx{1, 0, 0, 1, f1, f2}, tlm)
					tm = tlm
				}
				stack = stack[:0]
			case "Tm":
				if len(stack) >= 6 {
					tm = readMtx(stack[len(stack)-6:])
					tlm = tm
				}
				stack = stack[:0]
			case "T*":
				tlm = mulM(mtx{1, 0, 0, 1, 0, -leading}, tlm)
				tm = tlm
				stack = stack[:0]
			case "'":
				tlm = mulM(mtx{1, 0, 0, 1, 0, -leading}, tlm)
				tm = tlm
				if inText && len(stack) >= 1 {
					if sv := last(stack, 1); sv.kind == 's' {
						r := makeRun(sv.str, info, tm, ctm, fontKey, fontSize)
						runs = append(runs, r)
						tm = mulM(mtx{1, 0, 0, 1, estWidth(sv.str, info.Fonts[fontKey], fontSize), 0}, tm)
					}
				}
				stack = stack[:0]
			case "\"":
				if inText && len(stack) >= 3 {
					if sv := last(stack, 1); sv.kind == 's' {
						runs = append(runs, makeRun(sv.str, info, tm, ctm, fontKey, fontSize))
					}
				}
				stack = stack[:0]
			case "Tj":
				if inText && len(stack) >= 1 {
					if sv := last(stack, 1); sv.kind == 's' {
						r := makeRun(sv.str, info, tm, ctm, fontKey, fontSize)
						runs = append(runs, r)
						// tiến vị trí: PDF không tự dịch sau Tj — producer ghi đè
						// vị trí bằng Td/Tm giữa các từ, nhưng Aspose/Word KHÔNG
						// (dựa vào advance của glyph). Không tiến → mọi từ cùng
						// x0 → tách dòng/sai khoảng cách (lỗi thật trên "Nhận
						// xét nơi cư trú 2026").
						tm = mulM(mtx{1, 0, 0, 1, estWidth(sv.str, info.Fonts[fontKey], fontSize), 0}, tm)
					}
				}
				stack = stack[:0]
			case "TJ":
				if inText {
					if arr := lastArray(stack); arr != nil {
						s, adv := showTJ(*arr, info, fontKey, fontSize)
						if debugRawRuns {
							println("TJ font=", fontKey, "raw=", hexPreview(s))
						}
						runs = append(runs, makeRunDecoded(s, info, tm, ctm, fontKey, fontSize))
						tm = mulM(mtx{1, 0, 0, 1, adv, 0}, tm)
					}
				}
				stack = stack[:0]
			case "[":
				// bắt đầu mảng: đánh dấu bằng sentinel
				stack = append(stack, pdfValue{kind: 'x'})
			case "]":
				// gom phần tử sau sentinel gần nhất thành mảng
				k := len(stack) - 1
				for k >= 0 && stack[k].kind != 'x' {
					k--
				}
				if k < 0 {
					stack = stack[:0]
				} else {
					arr := pdfValue{kind: 'a', list: append([]pdfValue(nil), stack[k+1:]...)}
					stack = append(stack[:k], arr)
				}
			case "gs", "Do", "BI", "g", "G", "rg", "RG", "k", "K", "w", "J", "j", "M", "d", "ri", "i":
				stack = stack[:0] // trạng thái đồ họa không liên quan văn bản
			}
		}
	}
	return runs
}

// lastArray — giá trị mảng gần đỉnh stack (đần dần quét từ trên xuống).
func lastArray(stack []pdfValue) *pdfValue {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind == 'a' {
			return &stack[i]
		}
		if stack[i].kind == 's' {
			return nil // chuỗi đứng sau mảng → mảng đã bị tiêu thụ
		}
	}
	return nil
}

// makeRun — tạo Run từ chuỗi glyph: giải mã theo font, tính vị trí + cỡ.
// debugRawRuns — bật in chuỗi glyph gốc để chẩn đoán (chỉ dùng khi test).
var debugRawRuns bool

// hexPreview — biểu diễn hex của chuỗi byte (chẩn đoán).
func hexPreview(s string) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(s)*3)
	for i := 0; i < len(s) && i < 60; i++ {
		out = append(out, digits[s[i]>>4], digits[s[i]&0xf], ' ')
	}
	return string(out)
}

func makeRun(rawStr string, info *pdfPageInfo, tm, ctm mtx, fontKey string, fontSize float64) Run {
	fi := info.Fonts[fontKey]
	if debugRawRuns {
		println("makeRun font=", fontKey, "raw=", hexPreview(rawStr))
	}
	text := rawStr
	if fi != nil && fi.cm != nil {
		text = fi.cm.decodeStr(rawStr)
	} else if fi == nil || fi.cm == nil {
		text = latin1ToUTF8(rawStr)
	}
	return buildRun(text, info, tm, ctm, fontKey, fontSize)
}

// estWidth — bề rộng chuỗi glyph (đơn vị không gian chữ). Có bảng /Widths
// thật (font nhúng — Aspose luôn ghi) → tính CHÍNH XÁC theo mã glyph; không
// có → ước lượng theo nhóm ký tự.
func estWidth(rawStr string, fi *fontInfo, fontSize float64) float64 {
	if fi != nil && fi.widths != nil {
		adv := 0.0
		if fi.cm != nil {
			b := []byte(rawStr)
			cl := fi.cm.codeLen
			for j := 0; j < len(b); j += cl {
				end := j + cl
				if end > len(b) {
					end = len(b)
				}
				code := 0
				for _, x := range b[j:end] {
					code = code<<8 | int(x)
				}
				if w, ok := fi.widths[code]; ok {
					adv += w
				} else {
					adv += 500
				}
			}
		} else {
			for _, x := range []byte(rawStr) {
				if w, ok := fi.widths[int(x)]; ok {
					adv += w
				} else {
					adv += 500
				}
			}
		}
		return adv / 1000 * fontSize
	}
	// heuristic: không có metrics — ước lượng theo nhóm ký tự
	const (
		narrow = 0.30 // i l j t f . , ' ' ( ) [ ] | ! dấu thanh
		wide   = 0.80 // m w M W @ —
		normal = 0.52
	)
	if fi != nil && fi.cm != nil {
		rawStr = fi.cm.decodeStr(rawStr)
	} else if fi == nil || fi.cm == nil {
		rawStr = latin1ToUTF8(rawStr)
	}
	w := 0.0
	for _, r := range rawStr {
		switch {
		case r == ' ' || r == '\t':
			w += 0.30
		case strings.ContainsRune("iljtf.,'!|[]()", r):
			w += narrow
		case strings.ContainsRune("mwMW@", r):
			w += wide
		default:
			w += normal
		}
	}
	return w * fontSize
}

// makeRunDecoded — dựng Run từ văn bản ĐÃ decode (dùng cho TJ: showTJ decode
// từng chuỗi thành phần; decode lại lần nữa sẽ hỏng chữ).
func makeRunDecoded(text string, info *pdfPageInfo, tm, ctm mtx, fontKey string, fontSize float64) Run {
	return buildRun(text, info, tm, ctm, fontKey, fontSize)
}

// buildRun — dựng Run từ văn bản đã decode + trạng thái text hiện hành.
func buildRun(text string, info *pdfPageInfo, tm, ctm mtx, fontKey string, fontSize float64) Run {
	fi := info.Fonts[fontKey]
	mm := mulM(tm, ctm)
	x, y := mm.apply(0, 0)
	size := fontSize
	if size <= 0 {
		size = 12
	}
	size *= mm.scale()
	// y PDF từ đáy trang → đổi top-left
	yTop := info.H - y
	// ước lượng bề rộng: 0.5×size×ký tự (không có metrics; đủ dùng cho layout)
	w := 0.5 * size * float64(len([]rune(text)))
	if w < size*0.5 {
		w = size * 0.5
	}
	bold := false
	italic := false
	if fi != nil {
		bold, italic = fi.bold, strings.Contains(fi.baseName, "Italic") || strings.Contains(fi.baseName, "Oblique")
	}
	return Run{
		Text:   text,
		Font:   cleanFontName(fi),
		SizePt: round1(size),
		Bold:   bold,
		Italic: italic,
		X0:     x, Y0: yTop - size,
		X1: x + w, Y1: yTop,
		Conf: 1.0,
	}
}

// showTJ — mảng TJ: decode TỪNG chuỗi qua cmap rồi mới ghép; số âm lớn → chèn
// khoảng trắng giữa các đoạn ĐÃ decode; đồng thời tính TỔNG bề rộng cần tiến
// (mỗi chuỗi + mỗi số kerning×cỡ/1000). Không được chèn byte vào giữa chuỗi
// CID 2-byte — lệch cặp byte làm hỏng cả phần sau (lỗi thật trên Quy chế 274:
// kerning ≤ −100 khiến cả dòng thành rác).
func showTJ(arr pdfValue, info *pdfPageInfo, fontKey string, fontSize float64) (string, float64) {
	fi := info.Fonts[fontKey]
	out := ""
	adv := 0.0
	for _, v := range arr.list {
		switch v.kind {
		case 's':
			t := v.str
			if fi != nil && fi.cm != nil {
				t = fi.cm.decodeStr(v.str)
			} else {
				t = latin1ToUTF8(v.str)
			}
			out += t
			adv += estWidth(v.str, fi, fontSize) // bề rộng theo MÃ gốc
		case 'd':
			adv += v.num / 1000 * fontSize
			if v.num <= -100 {
				out += " "
			}
		}
	}
	return out, adv
}

func cleanFontName(fi *fontInfo) string {
	if fi == nil {
		return ""
	}
	n := fi.baseName
	if i := strings.Index(n, "+"); i >= 0 { // bỏ prefix subset "ABCDEF+"
		n = n[i+1:]
	}
	// bỏ hậu tố "-MT"/"MT" (PostScript name của Microsoft fonts) — KHÔNG dùng
	// strings.Trim với cutset: "Times..." bị ăn mất chữ "T" đầu!
	n = strings.TrimSuffix(n, "-MT")
	return strings.TrimSuffix(n, "MT")
}

func latin1ToUTF8(s string) string {
	// PDF string byte → Latin-1 (xấp xỉ); đã là UTF-8 hợp lệ thì giữ nguyên
	if isUTF8(s) {
		return s
	}
	r := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		r[i] = rune(s[i])
	}
	return string(r)
}

func isUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// ═══════════════════ Tokenizer content stream ═══════════════════

type contentTok struct {
	b []byte
	i int
}

// next — trả (giá trị cuối cùng trước toán tử, toán tử). Stack giá trị được
// quản lý bởi caller qua biến stack; ở đây trả từng "viên": value hoặc op.
// Đơn giản hóa: next() trả (pdfValue nếu là giá trị, op nếu là toán tử).
func (t *contentTok) next() (pdfValue, string) {
	for t.i < len(t.b) {
		c := t.b[t.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			t.i++
		case c == '%':
			for t.i < len(t.b) && t.b[t.i] != '\n' {
				t.i++
			}
		case c == '(':
			st := &tok{b: t.b, i: t.i}
			s, _ := st.literal()
			t.i = st.i
			return pdfValue{kind: 's', str: s}, ""
		case c == '<' && t.i+1 < len(t.b) && t.b[t.i+1] != '<':
			t.i++
			s := t.i
			for t.i < len(t.b) && t.b[t.i] != '>' {
				t.i++
			}
			hexs := string(t.b[s:t.i])
			if t.i < len(t.b) {
				t.i++
			}
			return pdfValue{kind: 's', str: decodeHexStr(hexs)}, ""
		case c == '<': // dict inline trong stream (hiếm) — bỏ qua tới >>
			t.i += 2
			for t.i < len(t.b) && !(t.b[t.i] == '>' && t.i+1 < len(t.b) && t.b[t.i+1] == '>') {
				t.i++
			}
			t.i += 2
		case c == '[' || c == ']':
			t.i++
			return pdfValue{}, string(c)
		case c == '/' || isDigit(c) || c == '-' || c == '+' || c == '.':
			st := &tok{b: t.b, i: t.i}
			v := st.value()
			t.i = st.i
			return v, ""
		default:
			s := t.i
			for t.i < len(t.b) && !isDelim(t.b[t.i]) {
				t.i++
			}
			return pdfValue{}, string(t.b[s:t.i])
		}
	}
	return pdfValue{}, ""
}

// readMtx, asFloat, last, pair — tiện ích stack.
func readMtx(list []pdfValue) mtx {
	var m mtx
	for i := 0; i < 6 && i < len(list); i++ {
		m[i], _ = asFloat(list[i])
	}
	return m
}

func asFloat(v pdfValue) (float64, bool) {
	if v.kind == 'd' {
		return v.num, true
	}
	return 0, false
}

func last(stack []pdfValue, n int) pdfValue {
	if len(stack) >= n {
		return stack[len(stack)-n]
	}
	return pdfValue{}
}

func pair(stack []pdfValue) (float64, float64, bool) {
	if len(stack) >= 2 {
		a, ok1 := asFloat(stack[len(stack)-2])
		b, ok2 := asFloat(stack[len(stack)-1])
		return a, b, ok1 && ok2
	}
	return 0, 0, false
}

// ═══════════════════ API chính ═══════════════════

// rawPage — trang thô trước layout.
type rawPage struct {
	W, H float64
	Runs []Run
}

// ExtractPDFPages — trích lớp chữ toàn file: trả danh sách trang thô theo thứ tự.
func ExtractPDFPages(data []byte) ([]rawPage, error) {
	pf, err := parsePDF(data)
	if err != nil {
		return nil, err
	}
	var out []rawPage
	for _, po := range pf.pages() {
		info, err := pf.buildPageInfo(po)
		if err != nil {
			continue
		}
		out = append(out, rawPage{W: info.W, H: info.H, Runs: extractRuns(info)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("PDF không có trang nào đọc được")
	}
	return out, nil
}
