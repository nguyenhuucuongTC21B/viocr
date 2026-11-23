package main

// util.go — tiện ích dùng chung của v3.0 (ldm/pdfmini/layout/docx/bench).

import (
	"math"
	"strconv"
	"strings"
)

func itoa(n int) string { return strconv.Itoa(n) }

func joinStrings(list []string, sep string) string { return strings.Join(list, sep) }

func sqrtF(x float64) float64 { return math.Sqrt(x) }

func fmtNum(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
