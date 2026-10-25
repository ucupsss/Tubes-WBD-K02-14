package explain

import (
	"encoding/json"
	"fmt"
	"math"
)

// rawNode mengikuti nama field EXPLAIN (FORMAT JSON) PostgreSQL.
type rawNode struct {
	NodeType            string    `json:"Node Type"`
	Operation           string    `json:"Operation"`
	RelationName        string    `json:"Relation Name"`
	Schema              string    `json:"Schema"`
	Alias               string    `json:"Alias"`
	IndexName           string    `json:"Index Name"`
	JoinType            string    `json:"Join Type"`
	Strategy            string    `json:"Strategy"`
	ScanDirection       string    `json:"Scan Direction"`
	IndexCond           string    `json:"Index Cond"`
	HashCond            string    `json:"Hash Cond"`
	MergeCond           string    `json:"Merge Cond"`
	JoinFilter          string    `json:"Join Filter"`
	Filter              string    `json:"Filter"`
	RecheckCond         string    `json:"Recheck Cond"`
	StartupCost         float64   `json:"Startup Cost"`
	TotalCost           float64   `json:"Total Cost"`
	PlanRows            float64   `json:"Plan Rows"`
	PlanWidth           int       `json:"Plan Width"`
	ActualStartupTime   *float64  `json:"Actual Startup Time"`
	ActualTotalTime     *float64  `json:"Actual Total Time"`
	ActualRows          *float64  `json:"Actual Rows"`
	ActualLoops         *float64  `json:"Actual Loops"`
	RowsRemovedByFilter float64   `json:"Rows Removed by Filter"`
	Plans               []rawNode `json:"Plans"`
}

type rawPlan struct {
	Plan          rawNode  `json:"Plan"`
	PlanningTime  *float64 `json:"Planning Time"`
	ExecutionTime *float64 `json:"Execution Time"`
}

// ParsePlan mengubah output EXPLAIN (FORMAT JSON) menjadi Plan beranotasi.
func ParsePlan(data []byte) (Plan, error) {
	var raw []rawPlan
	if err := json.Unmarshal(data, &raw); err != nil {
		return Plan{}, fmt.Errorf("explain.ParsePlan: %w", err)
	}
	if len(raw) == 0 {
		return Plan{}, fmt.Errorf("explain.ParsePlan: plan kosong")
	}
	r := raw[0]
	p := Plan{Root: convert(r.Plan)}
	if r.PlanningTime != nil {
		p.PlanningMS = *r.PlanningTime
	}
	if r.ExecutionTime != nil {
		p.ExecutionMS = *r.ExecutionTime
		p.Analyzed = true
	}
	annotate(&p.Root, &p, p.ExecutionMS)
	summarize(p.Root, &p)
	return p, nil
}

func convert(r rawNode) Node {
	n := Node{
		NodeType:      r.NodeType,
		Operation:     r.Operation,
		RelationName:  r.RelationName,
		Schema:        r.Schema,
		Alias:         r.Alias,
		IndexName:     r.IndexName,
		JoinType:      r.JoinType,
		Strategy:      r.Strategy,
		ScanDirection: r.ScanDirection,
		Condition:     firstNonEmpty(r.IndexCond, r.HashCond, r.MergeCond, r.JoinFilter, r.RecheckCond, r.Filter),
		StartupCost:   r.StartupCost,
		TotalCost:     r.TotalCost,
		PlanRows:      r.PlanRows,
		PlanWidth:     r.PlanWidth,
		RowsRemoved:   r.RowsRemovedByFilter,
	}
	if r.ActualTotalTime != nil {
		n.ActualTotalTime = *r.ActualTotalTime
	}
	if r.ActualStartupTime != nil {
		n.ActualStartupTime = *r.ActualStartupTime
	}
	if r.ActualRows != nil {
		n.ActualRows = *r.ActualRows
	}
	if r.ActualLoops != nil {
		n.ActualLoops = *r.ActualLoops
	}
	for _, c := range r.Plans {
		n.Children = append(n.Children, convert(c))
	}
	return n
}

// annotate menghitung waktu eksklusif, lalu menandai node lambat dan estimasi yang meleset.
// Hanya berlaku untuk EXPLAIN ANALYZE (tanpa ANALYZE tidak ada angka aktual).
func annotate(n *Node, p *Plan, executionMS float64) {
	for i := range n.Children {
		annotate(&n.Children[i], p, executionMS)
	}
	if !p.Analyzed {
		return
	}
	loops := math.Max(n.ActualLoops, 1)
	total := n.ActualTotalTime * loops
	for _, c := range n.Children {
		total -= c.ActualTotalTime * math.Max(c.ActualLoops, 1)
	}
	n.ExclusiveMS = math.Max(total, 0)
	if executionMS > 0 && n.ExclusiveMS >= SlowMinMS && n.ExclusiveMS >= SlowShare*executionMS {
		n.Slow = true
		p.SlowNodes++
	}
	// PlanRows dan ActualRows sama-sama per loop.
	if n.ActualLoops > 0 {
		est := math.Max(n.PlanRows, 1)
		act := math.Max(n.ActualRows, 1)
		n.EstimateRatio = act / est
		if n.EstimateRatio >= MisestimateFactor || n.EstimateRatio <= 1/MisestimateFactor {
			n.Misestimated = true
			p.Misestimates++
		}
	}
}

func summarize(n Node, p *Plan) {
	switch {
	case n.NodeType == "ModifyTable":
		// INSERT/UPDATE/DELETE pada table tujuan, bukan scan.
	case n.RelationName != "" && n.IndexName != "":
		p.ScanSummary = append(p.ScanSummary, fmt.Sprintf("%s using %s on %s", n.NodeType, n.IndexName, n.RelationName))
	case n.RelationName != "":
		p.ScanSummary = append(p.ScanSummary, fmt.Sprintf("%s on %s", n.NodeType, n.RelationName))
	case n.JoinType != "":
		p.JoinSummary = append(p.JoinSummary, fmt.Sprintf("%s (%s)", n.NodeType, n.JoinType))
	}
	for _, c := range n.Children {
		summarize(c, p)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
