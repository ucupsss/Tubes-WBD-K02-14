package explain

import (
	"strings"
	"testing"
)

// Output EXPLAIN (FORMAT JSON) tanpa ANALYZE: Hash Join antara Seq Scan dan Index Scan.
const nestedJoinPlan = `[{"Plan": {
	"Node Type": "Hash Join", "Join Type": "Inner", "Startup Cost": 10.5, "Total Cost": 120.25,
	"Plan Rows": 500, "Plan Width": 64, "Hash Cond": "(o.customer_id = c.id)",
	"Plans": [
		{"Node Type": "Seq Scan", "Relation Name": "orders", "Alias": "o", "Startup Cost": 0, "Total Cost": 80,
		 "Plan Rows": 5000, "Plan Width": 32, "Filter": "(total > 100)"},
		{"Node Type": "Hash", "Startup Cost": 8, "Total Cost": 8, "Plan Rows": 100, "Plan Width": 32,
		 "Plans": [
			{"Node Type": "Index Scan", "Relation Name": "customers", "Index Name": "customers_pkey",
			 "Scan Direction": "Forward", "Startup Cost": 0.29, "Total Cost": 8, "Plan Rows": 100,
			 "Plan Width": 32, "Index Cond": "(id < 100)"}
		 ]}
	]},
	"Planning Time": 0.42}]`

// EXPLAIN ANALYZE: Nested Loop yang menghabiskan hampir semua waktu di Seq Scan dalam (loops=50),
// dengan estimasi 1 baris padahal aktual 400 baris per loop.
const analyzedPlan = `[{"Plan": {
	"Node Type": "Nested Loop", "Join Type": "Inner", "Startup Cost": 0, "Total Cost": 300,
	"Plan Rows": 50, "Plan Width": 8,
	"Actual Startup Time": 0.02, "Actual Total Time": 41.0, "Actual Rows": 20000, "Actual Loops": 1,
	"Plans": [
		{"Node Type": "Index Only Scan", "Relation Name": "a", "Index Name": "a_pkey",
		 "Startup Cost": 0, "Total Cost": 5, "Plan Rows": 50, "Plan Width": 4,
		 "Actual Startup Time": 0.01, "Actual Total Time": 0.5, "Actual Rows": 50, "Actual Loops": 1},
		{"Node Type": "Seq Scan", "Relation Name": "b", "Startup Cost": 0, "Total Cost": 5, "Plan Rows": 1,
		 "Plan Width": 4, "Filter": "(b.a_id = a.id)", "Rows Removed by Filter": 9600,
		 "Actual Startup Time": 0.1, "Actual Total Time": 0.78, "Actual Rows": 400, "Actual Loops": 50}
	]},
	"Planning Time": 0.1, "Execution Time": 42.0}]`

func TestParsePlan_NestedJoin(t *testing.T) {
	p, err := ParsePlan([]byte(nestedJoinPlan))
	if err != nil {
		t.Fatal(err)
	}
	if p.Analyzed || p.PlanningMS != 0.42 || p.ExecutionMS != 0 {
		t.Fatalf("tanpa ANALYZE: analyzed=%v planning=%v exec=%v", p.Analyzed, p.PlanningMS, p.ExecutionMS)
	}
	root := p.Root
	if root.NodeType != "Hash Join" || root.JoinType != "Inner" || root.TotalCost != 120.25 || root.Condition != "(o.customer_id = c.id)" {
		t.Fatalf("root salah: %+v", root)
	}
	if len(root.Children) != 2 || len(root.Children[1].Children) != 1 {
		t.Fatalf("struktur pohon salah: %+v", root.Children)
	}
	seq := root.Children[0]
	if seq.NodeType != "Seq Scan" || seq.RelationName != "orders" || seq.Condition != "(total > 100)" || seq.PlanRows != 5000 {
		t.Fatalf("seq scan salah: %+v", seq)
	}
	idx := root.Children[1].Children[0]
	if idx.NodeType != "Index Scan" || idx.IndexName != "customers_pkey" || idx.Condition != "(id < 100)" {
		t.Fatalf("index scan salah: %+v", idx)
	}
	if strings.Join(p.ScanSummary, "|") != "Seq Scan on orders|Index Scan using customers_pkey on customers" {
		t.Fatalf("ringkasan scan: %v", p.ScanSummary)
	}
	if strings.Join(p.JoinSummary, "|") != "Hash Join (Inner)" {
		t.Fatalf("ringkasan join: %v", p.JoinSummary)
	}
	if p.SlowNodes != 0 || p.Misestimates != 0 {
		t.Fatal("tanpa angka aktual tidak boleh ada penanda lambat/meleset")
	}
}

func TestParsePlan_AnalyzeFlags(t *testing.T) {
	p, err := ParsePlan([]byte(analyzedPlan))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Analyzed || p.ExecutionMS != 42.0 {
		t.Fatalf("analyzed=%v exec=%v", p.Analyzed, p.ExecutionMS)
	}
	loop := p.Root
	inner := loop.Children[1]
	outer := loop.Children[0]

	// Waktu eksklusif inner = 0.78 ms × 50 loops = 39 ms (≥ 30% dari 42 ms) → lambat.
	if inner.ExclusiveMS < 38.9 || inner.ExclusiveMS > 39.1 || !inner.Slow {
		t.Fatalf("inner: exclusive=%v slow=%v, want ~39 ms & lambat", inner.ExclusiveMS, inner.Slow)
	}
	// Nested Loop sendiri: 41 − 0.5 − 39 = 1.5 ms → tidak lambat.
	if loop.Slow || loop.ExclusiveMS < 1.4 || loop.ExclusiveMS > 1.6 {
		t.Fatalf("loop: exclusive=%v slow=%v", loop.ExclusiveMS, loop.Slow)
	}
	if outer.Slow || outer.Misestimated {
		t.Fatalf("outer tidak boleh ditandai: %+v", outer)
	}
	// Estimasi 1 baris/loop, aktual 400 → meleset 400×. Root: est 50, aktual 20000 → meleset 400×.
	if !inner.Misestimated || inner.EstimateRatio != 400 || !loop.Misestimated {
		t.Fatalf("estimasi meleset tidak terdeteksi: inner=%v (%v) root=%v", inner.Misestimated, inner.EstimateRatio, loop.Misestimated)
	}
	if p.SlowNodes != 1 || p.Misestimates != 2 {
		t.Fatalf("SlowNodes=%d Misestimates=%d, want 1 & 2", p.SlowNodes, p.Misestimates)
	}
	if inner.RowsRemoved != 9600 {
		t.Fatalf("rows removed = %v", inner.RowsRemoved)
	}
}

func TestParsePlan_Thresholds(t *testing.T) {
	// Query sangat cepat: node memakan 100% waktu tetapi < 1 ms → tidak ditandai lambat.
	fast := `[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "t", "Plan Rows": 10,
		"Actual Total Time": 0.05, "Actual Rows": 12, "Actual Loops": 1}, "Execution Time": 0.06}]`
	p, err := ParsePlan([]byte(fast))
	if err != nil {
		t.Fatal(err)
	}
	if p.Root.Slow || p.Root.Misestimated {
		t.Fatalf("query cepat & estimasi dekat tidak boleh ditandai: %+v", p.Root)
	}

	// Estimasi terlalu besar (1000 vs 1) juga meleset; node tidak pernah dieksekusi (loops 0) tidak dinilai.
	over := `[{"Plan": {"Node Type": "Append", "Plan Rows": 1000, "Actual Total Time": 0.1, "Actual Rows": 1, "Actual Loops": 1,
		"Plans": [{"Node Type": "Seq Scan", "Relation Name": "x", "Plan Rows": 500, "Actual Total Time": 0, "Actual Rows": 0, "Actual Loops": 0}]},
		"Execution Time": 0.2}]`
	p, err = ParsePlan([]byte(over))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Root.Misestimated || p.Root.Children[0].Misestimated {
		t.Fatalf("root harus meleset (over-estimate), anak tanpa eksekusi tidak dinilai: %+v", p.Root)
	}
}

func TestParsePlan_ModifyTableIsNotAScan(t *testing.T) {
	in := `[{"Plan": {"Node Type": "ModifyTable", "Operation": "Insert", "Relation Name": "t",
		"Plans": [{"Node Type": "Seq Scan", "Relation Name": "src"}]}}]`
	p, err := ParsePlan([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if p.Root.Operation != "Insert" || strings.Join(p.ScanSummary, "|") != "Seq Scan on src" {
		t.Fatalf("operation=%q scan=%v", p.Root.Operation, p.ScanSummary)
	}
}

func TestParsePlan_Invalid(t *testing.T) {
	for _, in := range []string{``, `{}`, `[]`, `not json`} {
		if _, err := ParsePlan([]byte(in)); err == nil {
			t.Fatalf("input %q harus error", in)
		}
	}
}
