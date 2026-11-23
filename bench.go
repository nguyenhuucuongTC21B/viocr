package main

// bench.go — M6 (Bench): đo chất lượng bằng số, không cảm tính.
//   CER — Character Error Rate: khoảng cách Levenshtein ký tự / số ký tự gốc
//   WER — Word Error Rate: tương tự ở mức từ
//   TS  — Table Structure: tỉ lệ bảng có lưới hàng–cột khớp (dung sai ô ≤ 0.02 CER)
//   LFS — Layout Fidelity Score: 1 − khoảng cách chuỗi loại khối / độ dài
// Điểm lưu JSON theo phiên bản để so sánh giữa các bản phát hành.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// levenshtein — khoảng cách Levenshtein trên rune.
func levenshtein(a, b []rune) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j-1] + cost
			if prev[j]+1 < m {
				m = prev[j] + 1
			}
			if cur[j-1]+1 < m {
				m = cur[j-1] + 1
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

// normalizeText — chuẩn hóa trước khi chấm: gọn khoảng trắng, hợp nhất loại
// quote/dash, bỏ ký tự điều khiển.
func normalizeText(s string) string {
	s = strings.NewReplacer(
		"\u00A0", " ", "\u2019", "'", "\u2018", "'",
		"\u201C", `"`, "\u201D", `"`, "\u2013", "-", "\u2014", "-",
	).Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// CER — tỉ lệ ký tự sai (0..1) giữa bản gốc và bản xuất.
func CER(gold, out string) float64 {
	g := []rune(normalizeText(gold))
	o := []rune(normalizeText(out))
	if len(g) == 0 {
		if len(o) == 0 {
			return 0
		}
		return 1
	}
	return float64(levenshtein(g, o)) / float64(len(g))
}

// WER — tỉ lệ từ sai (0..1).
func WER(gold, out string) float64 {
	g := strings.Fields(normalizeText(gold))
	o := strings.Fields(normalizeText(out))
	if len(g) == 0 {
		if len(o) == 0 {
			return 0
		}
		return 1
	}
	return float64(levenshteinStr(g, o)) / float64(len(g))
}

func levenshteinStr(a, b []string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j-1] + cost
			if prev[j]+1 < m {
				m = prev[j] + 1
			}
			if cur[j-1]+1 < m {
				m = cur[j-1] + 1
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// TS — Table Structure accuracy: so 2 LDM, đếm bảng khớp lưới.
// Khớp: cùng số hàng, cùng số cột mỗi hàng, CER ô ≤ 0.02 cho mọi ô.
func TS(gold, out *Document) float64 {
	gt := collectTables(gold)
	ot := collectTables(out)
	if len(gt) == 0 {
		return 1 // không có bảng nào để chấm — coi như đạt
	}
	if len(ot) == 0 {
		return 0
	}
	ok := 0
	for _, g := range gt {
		for _, o := range ot {
			if tablesEqual(g, o) {
				ok++
				break
			}
		}
	}
	return float64(ok) / float64(len(gt))
}

func collectTables(d *Document) []*TableBlock {
	var out []*TableBlock
	for _, p := range d.Pages {
		for _, b := range p.Blocks {
			if b.Kind == BlockTable && b.Table != nil {
				out = append(out, b.Table)
			}
		}
	}
	return out
}

func tablesEqual(a, b *TableBlock) bool {
	if len(a.Grid) != len(b.Grid) {
		return false
	}
	for i := range a.Grid {
		if len(a.Grid[i]) != len(b.Grid[i]) {
			return false
		}
		for j := range a.Grid[i] {
			if CER(a.Grid[i][j].Text, b.Grid[i][j].Text) > 0.02 {
				return false
			}
		}
	}
	return true
}

// LFS — Layout Fidelity Score: so chuỗi loại khối theo thứ tự đọc.
func LFS(gold, out *Document) float64 {
	gk := blockKindSeq(gold)
	ok := blockKindSeq(out)
	if len(gk) == 0 {
		return 1
	}
	d := levenshteinStr(gk, ok)
	return 1 - float64(d)/float64(len(gk))
}

func blockKindSeq(d *Document) []string {
	var out []string
	for _, p := range d.Pages {
		for _, b := range p.Blocks {
			switch b.Kind {
			case BlockFooter, BlockHeader:
				// đầu/cuối trang lặp — không đưa vào LFS (nhiễu)
			default:
				out = append(out, b.Kind.String())
			}
		}
	}
	return out
}

// ═══════════════ Chạy bench trên thư mục gold/out ═══════════════

// benchPair — một cặp file chấm điểm.
type benchPair struct {
	Name string  `json:"name"`
	CER  float64 `json:"cer"`
	WER  float64 `json:"wer"`
}

// benchResult — kết quả tổng của 1 lần chạy.
type benchResult struct {
	Version  string      `json:"version"`
	Pairs    []benchPair `json:"pairs"`
	MeanCER  float64     `json:"mean_cer"`
	MeanWER  float64     `json:"mean_wer"`
	TS       *float64    `json:"ts,omitempty"`  // có gold .ldm.json mới có
	LFS      *float64    `json:"lfs,omitempty"`
}

// runBenchDir — thư mục bench gồm gold/ và out/ cùng tên file .txt
// (tuỳ chọn gold/*.ldm.json so out/*.ldm.json để có TS/LFS).
// Kết quả in ra stdout và lưu bench_result.json cạnh thư mục.
func runBenchDir(dir string) error {
	goldDir := filepath.Join(dir, "gold")
	outDir := filepath.Join(dir, "out")
	entries, err := os.ReadDir(goldDir)
	if err != nil {
		return fmt.Errorf("không đọc được %s: %w (cấu trúc: <dir>/gold/*.txt + <dir>/out/*.txt)", goldDir, err)
	}
	res := benchResult{Version: appVersion}
	sumCER, sumWER := 0.0, 0.0
	n := 0
	names := []string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		gb, err1 := os.ReadFile(filepath.Join(goldDir, name))
		ob, err2 := os.ReadFile(filepath.Join(outDir, name))
		if err1 != nil || err2 != nil {
			continue
		}
		p := benchPair{Name: name, CER: CER(string(gb), string(ob)), WER: WER(string(gb), string(ob))}
		res.Pairs = append(res.Pairs, p)
		sumCER += p.CER
		sumWER += p.WER
		n++
	}
	if n > 0 {
		res.MeanCER = sumCER / float64(n)
		res.MeanWER = sumWER / float64(n)
	}
	// TS/LFS nếu có cặp .ldm.json
	var goldLDMDocs, outLDMDocs []*Document
	gj, _ := filepath.Glob(filepath.Join(goldDir, "*.ldm.json"))
	for _, gpath := range gj {
		base := filepath.Base(gpath)
		opath := filepath.Join(outDir, base)
		gd, err1 := LoadJSON(gpath)
		od, err2 := LoadJSON(opath)
		if err1 != nil || err2 != nil {
			continue
		}
		goldLDMDocs = append(goldLDMDocs, gd)
		outLDMDocs = append(outLDMDocs, od)
	}
	if len(goldLDMDocs) > 0 {
		tsSum := 0.0
		lfsSum := 0.0
		for i := range goldLDMDocs {
			tsSum += TS(goldLDMDocs[i], outLDMDocs[i])
			lfsSum += LFS(goldLDMDocs[i], outLDMDocs[i])
		}
		ts := tsSum / float64(len(goldLDMDocs))
		lfs := lfsSum / float64(len(goldLDMDocs))
		res.TS = &ts
		res.LFS = &lfs
	}

	// in báo cáo
	fmt.Printf("viocr-bench %s — %d cặp file trong %s\n", appVersion, n, dir)
	for _, p := range res.Pairs {
		fmt.Printf("  %-40s CER %6.2f%%  WER %6.2f%%\n", p.Name, p.CER*100, p.WER*100)
	}
	if n > 0 {
		fmt.Printf("TỔNG: CER %.2f%% | WER %.2f%%", res.MeanCER*100, res.MeanWER*100)
		if res.TS != nil {
			fmt.Printf(" | TS %.1f%%", *res.TS*100)
		}
		if res.LFS != nil {
			fmt.Printf(" | LFS %.1f%%", *res.LFS*100)
		}
		fmt.Println()
	} else {
		fmt.Println("Không tìm thấy cặp file nào để chấm.")
	}
	// lưu kết quả JSON
	if b, err := json.MarshalIndent(res, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "bench_result.json"), b, 0o644)
	}
	return nil
}
