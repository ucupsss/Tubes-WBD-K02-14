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

var ErrNotAnalyzable = apperr.Conflict("Hanya migration draft atau gagal yang dapat dianalisis")

// Node adalah satu node execution plan beserta anak-anaknya.
type Node struct {
	NodeType      string
	Operation     string // Insert/Update/Delete/Merge pada node ModifyTable
	RelationName  string
	Schema        string
	Alias         string
	IndexName     string
	JoinType      string
	Strategy      string
	ScanDirection string
	Condition     string // Index Cond / Hash Cond / Merge Cond / Join Filter / Filter, yang pertama ada

	StartupCost float64
	TotalCost   float64
	PlanRows    float64
	PlanWidth   int

	// Terisi hanya untuk EXPLAIN ANALYZE.
	ActualStartupTime float64
	ActualTotalTime   float64
	ActualRows        float64
	ActualLoops       float64
	RowsRemoved       float64

	// Turunan, dihitung oleh annotate.
	ExclusiveMS   float64 // waktu node sendiri (tanpa anak), dikali loops
	Slow          bool
	Misestimated  bool
	EstimateRatio float64 // aktual / estimasi (≥ 1 berarti estimasi terlalu kecil)

	Children []Node
}

// Plan adalah hasil EXPLAIN satu statement.
type Plan struct {
	Root         Node
	PlanningMS   float64
	ExecutionMS  float64
	Analyzed     bool
	SlowNodes    int
	Misestimates int
	ScanSummary  []string // mis. "Seq Scan on orders", "Index Scan using idx_x on orders"
	JoinSummary  []string // mis. "Hash Join (Inner)"
}

// StatementResult adalah hasil analisis satu statement dalam script migration.
type StatementResult struct {
	Index int
	SQL   string
	Kind  string
	Plan  *Plan  // nil bila statement tidak punya plan (DDL) atau gagal
	Note  string // penjelasan untuk statement tanpa plan
	Error string // pesan error Postgres (aman ditampilkan)
}

// Report adalah hasil analisis seluruh script. Semua perubahan sudah di-rollback.
type Report struct {
	Analyze    bool
	Statements []StatementResult
	Failed     int
}
