package explain

import "github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"

// Ambang penanda pada plan (didokumentasikan di README, D16).
const (
	// SlowShare: node ditandai lambat bila waktu eksklusifnya ≥ 30% dari waktu eksekusi statement...
	SlowShare = 0.30
	// SlowMinMS: ...dan minimal 1 ms, supaya query yang sangat cepat tidak ditandai.
	SlowMinMS = 1.0
	// MisestimateFactor: estimasi baris dianggap meleset bila berbeda ≥ 10× dari baris aktual.
	MisestimateFactor = 10.0
)

var ErrNotAnalyzable = apperr.Conflict("Hanya migration draft atau gagal yang dapat dianalisis").WithReason("not_analyzable")

// Node adalah satu node execution plan beserta anak-anaknya.
type Node struct {
	NodeType      string `json:"node_type"`
	Operation     string `json:"operation,omitempty"` // Insert/Update/Delete/Merge pada node ModifyTable
	RelationName  string `json:"relation_name,omitempty"`
	Schema        string `json:"schema,omitempty"`
	Alias         string `json:"alias,omitempty"`
	IndexName     string `json:"index_name,omitempty"`
	JoinType      string `json:"join_type,omitempty"`
	Strategy      string `json:"strategy,omitempty"`
	ScanDirection string `json:"scan_direction,omitempty"`
	Condition     string `json:"condition,omitempty"` // Index Cond / Hash Cond / Merge Cond / Join Filter / Filter, yang pertama ada

	StartupCost float64 `json:"startup_cost"`
	TotalCost   float64 `json:"total_cost"`
	PlanRows    float64 `json:"plan_rows"`
	PlanWidth   int     `json:"plan_width"`

	// Terisi hanya untuk EXPLAIN ANALYZE.
	ActualStartupTime float64 `json:"actual_startup_time"`
	ActualTotalTime   float64 `json:"actual_total_time"`
	ActualRows        float64 `json:"actual_rows"`
	ActualLoops       float64 `json:"actual_loops"`
	RowsRemoved       float64 `json:"rows_removed"`

	// Turunan, dihitung oleh annotate.
	ExclusiveMS   float64 `json:"exclusive_ms"` // waktu node sendiri (tanpa anak), dikali loops
	Slow          bool    `json:"slow"`
	Misestimated  bool    `json:"misestimated"`
	EstimateRatio float64 `json:"estimate_ratio"` // aktual / estimasi (≥ 1 berarti estimasi terlalu kecil)

	Children []Node `json:"children,omitempty"`
}

// Plan adalah hasil EXPLAIN satu statement.
type Plan struct {
	Root         Node     `json:"root"`
	PlanningMS   float64  `json:"planning_ms"`
	ExecutionMS  float64  `json:"execution_ms"`
	Analyzed     bool     `json:"analyzed"`
	SlowNodes    int      `json:"slow_nodes"`
	Misestimates int      `json:"misestimates"`
	ScanSummary  []string `json:"scan_summary"` // mis. "Seq Scan on orders", "Index Scan using idx_x on orders"
	JoinSummary  []string `json:"join_summary"` // mis. "Hash Join (Inner)"
}

// StatementResult adalah hasil analisis satu statement dalam script migration.
type StatementResult struct {
	Index int    `json:"index"`
	SQL   string `json:"sql"`
	Kind  string `json:"kind"`
	Plan  *Plan  `json:"plan,omitempty"`  // nil bila statement tidak punya plan (DDL) atau gagal
	Note  string `json:"note,omitempty"`  // penjelasan untuk statement tanpa plan
	Error string `json:"error,omitempty"` // pesan error Postgres (aman ditampilkan)
}

// Report adalah hasil analisis seluruh script. Semua perubahan sudah di-rollback.
type Report struct {
	Analyze    bool              `json:"analyze"`
	Statements []StatementResult `json:"statements"`
	Failed     int               `json:"failed"`
}
