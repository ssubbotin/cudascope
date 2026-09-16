package storage

import (
	"fmt"
	"strings"
	"time"
)

// A metric series is stored at three resolutions, and a chart asks for a
// window that has to be answered from one of them. Two things decide which:
//
//   - How far back the window starts. Raw rows live for a day, minute rows
//     for a month. Choosing by width alone, as this used to, drew an empty
//     chart for every narrow window older than raw retention: the half hour
//     that interests you three days later is exactly the query that returned
//     nothing.
//   - How many points the answer would contain. A month of minute rows is
//     43200 points per GPU, drawn onto a thousand pixels and sent on every
//     refresh. Beyond maxPoints the rows are folded into buckets, keeping
//     the peak of each one.
type seriesColumn struct {
	sel string // how to read the value from one row
	agg string // how to fold a bucket of rows into one value
}

// plain is a column read as it is and folded by averaging.
func avgCol(name string) seriesColumn {
	return seriesColumn{sel: name, agg: "AVG(" + name + ")"}
}

// peakCol keeps the largest value in the bucket: utilization, temperature
// and memory are read to find the worst moment, not the typical one.
func peakCol(name string) seriesColumn {
	return seriesColumn{sel: name, agg: "MAX(" + name + ")"}
}

// intAvgCol averages into an integer column.
func intAvgCol(name string) seriesColumn {
	return seriesColumn{sel: name, agg: "CAST(AVG(" + name + ") AS INTEGER)"}
}

// constCol is a value the tier does not store, reported as zero.
func constCol(value string) seriesColumn {
	return seriesColumn{sel: value, agg: value}
}

// keyCol is part of the grouping, so it reads the same either way.
func keyCol(expr string) seriesColumn {
	return seriesColumn{sel: expr, agg: expr}
}

// seriesTier is one stored resolution of a series.
type seriesTier struct {
	table string
	step  int64 // seconds between stored rows

	// maxSpan is the widest window this tier answers; retention is how far
	// back its rows survive.
	maxSpan   int64
	retention func(Options) time.Duration

	keys    []string // grouping expressions, in scan order after ts
	columns []seriesColumn
}

// series is one metric family across its tiers, finest first.
type series struct {
	tiers []seriesTier
}

const (
	spanHour  = 3600
	spanMonth = 30 * 24 * 3600
)

func rawRetention(o Options) time.Duration { return o.RawRetention }
func m1Retention(o Options) time.Duration  { return o.M1Retention }
func forever(Options) time.Duration        { return 1<<63 - 1 }

// pick chooses the tier that can answer a window, and the bucket width to
// fold its rows into. A bucket equal to the tier's own step means no folding.
func (s series) pick(opts Options, from, to int64) (seriesTier, int64) {
	span := to - from
	if span < 0 {
		span = 0
	}
	age := time.Now().Unix() - from

	chosen := s.tiers[len(s.tiers)-1]
	for _, tier := range s.tiers {
		if span <= tier.maxSpan && age <= int64(tier.retention(opts).Seconds()) {
			chosen = tier
			break
		}
	}

	return chosen, bucketFor(span, chosen.step, opts.MaxPoints)
}

// bucketFor returns the width to fold rows into so the answer stays within
// maxPoints. Zero maxPoints means no cap.
func bucketFor(span, step int64, maxPoints int) int64 {
	if maxPoints <= 0 || span <= 0 {
		return step
	}
	if span/step <= int64(maxPoints) {
		return step
	}

	// Buckets are aligned to absolute time, so a window can reach into one
	// bucket at each end beyond the whole ones it covers. Dividing by one
	// less than the cap leaves room for that, which is what makes the cap a
	// cap rather than a target.
	divisor := int64(maxPoints - 1)
	if divisor < 1 {
		divisor = 1
	}

	bucket := (span + divisor - 1) / divisor // round up
	if remainder := bucket % step; remainder != 0 {
		bucket += step - remainder // align buckets with stored rows
	}
	if bucket < step {
		bucket = step
	}
	return bucket
}

// query builds the SELECT for a tier, folding rows when bucket exceeds the
// tier's own step.
func (t seriesTier) query(bucket int64, where string) string {
	var sb strings.Builder

	folded := bucket > t.step
	sb.WriteString("SELECT ")
	if folded {
		fmt.Fprintf(&sb, "(ts / %d) * %d", bucket, bucket)
	} else {
		sb.WriteString("ts")
	}

	for _, c := range t.columns {
		sb.WriteString(", ")
		if folded {
			sb.WriteString(c.agg)
		} else {
			sb.WriteString(c.sel)
		}
	}

	fmt.Fprintf(&sb, " FROM %s WHERE %s", t.table, where)

	if folded {
		sb.WriteString(" GROUP BY 1")
		for _, key := range t.keys {
			sb.WriteString(", ")
			sb.WriteString(key)
		}
	}

	sb.WriteString(" ORDER BY 1")
	return sb.String()
}

// --- GPU ---------------------------------------------------------------

var gpuSeries = series{tiers: []seriesTier{
	{
		table: "gpu_metrics_raw", step: 1, maxSpan: spanHour, retention: rawRetention,
		keys: []string{"COALESCE(node_id, 'local')", "gpu_id"},
		columns: []seriesColumn{
			keyCol("COALESCE(node_id, 'local')"), keyCol("gpu_id"),
			peakCol("gpu_util"), avgCol("mem_util"), peakCol("mem_used"),
			peakCol("temperature"), intAvgCol("fan_speed"),
			avgCol("power_draw"), peakCol("power_limit"),
			intAvgCol("clock_gfx"), intAvgCol("clock_mem"),
			intAvgCol("pcie_tx"), intAvgCol("pcie_rx"),
			{sel: "pstate", agg: "MIN(pstate)"}, // P0 is the busiest state
			peakCol("encoder_util"), peakCol("decoder_util"),
			// A bucket is throttled if any sample in it was, and the counters
			// only ever grow, so the largest is the one that was true last.
			peakCol("throttle_reasons"), peakCol("ecc_corrected"), peakCol("ecc_uncorrected"),
		},
	},
	{
		table: "gpu_metrics_1m", step: 60, maxSpan: spanMonth, retention: m1Retention,
		keys: []string{"COALESCE(node_id, 'local')", "gpu_id"},
		columns: []seriesColumn{
			keyCol("COALESCE(node_id, 'local')"), keyCol("gpu_id"),
			peakCol("gpu_util_max"), avgCol("mem_util_avg"),
			{sel: "CAST(mem_used_max AS INTEGER)", agg: "CAST(MAX(mem_used_max) AS INTEGER)"},
			peakCol("temperature_max"),
			{sel: "CAST(fan_speed_avg AS INTEGER)", agg: "CAST(AVG(fan_speed_avg) AS INTEGER)"},
			avgCol("power_draw_avg"), constCol("0"),
			{sel: "CAST(clock_gfx_avg AS INTEGER)", agg: "CAST(AVG(clock_gfx_avg) AS INTEGER)"},
			{sel: "CAST(clock_mem_avg AS INTEGER)", agg: "CAST(AVG(clock_mem_avg) AS INTEGER)"},
			{sel: "CAST(pcie_tx_avg AS INTEGER)", agg: "CAST(AVG(pcie_tx_avg) AS INTEGER)"},
			{sel: "CAST(pcie_rx_avg AS INTEGER)", agg: "CAST(AVG(pcie_rx_avg) AS INTEGER)"},
			constCol("0"), constCol("0"), constCol("0"),
			// The rollups predate these columns: the tiers carry no throttle
			// mask or ECC counts, and reporting zero here says "not stored",
			// the same way pstate and the codecs already do.
			constCol("0"), constCol("0"), constCol("0"),
		},
	},
	{
		table: "gpu_metrics_1h", step: 3600, maxSpan: 1<<62 - 1, retention: forever,
		keys: []string{"COALESCE(node_id, 'local')", "gpu_id"},
		columns: []seriesColumn{
			keyCol("COALESCE(node_id, 'local')"), keyCol("gpu_id"),
			peakCol("gpu_util_max"), avgCol("mem_util_avg"),
			{sel: "CAST(mem_used_max AS INTEGER)", agg: "CAST(MAX(mem_used_max) AS INTEGER)"},
			peakCol("temperature_max"), constCol("0"),
			avgCol("power_draw_avg"), constCol("0"),
			constCol("0"), constCol("0"), constCol("0"), constCol("0"),
			constCol("0"), constCol("0"), constCol("0"),
			constCol("0"), constCol("0"), constCol("0"),
		},
	},
}}

// --- Host --------------------------------------------------------------

var hostSeries = series{tiers: []seriesTier{
	{
		table: "host_metrics_raw", step: 1, maxSpan: spanHour, retention: rawRetention,
		keys: []string{"node_id"},
		columns: []seriesColumn{
			keyCol("node_id"),
			peakCol("cpu_percent"), peakCol("mem_used"), peakCol("mem_total"),
			peakCol("disk_used"), peakCol("disk_total"),
			intAvgCol("net_rx"), intAvgCol("net_tx"),
			peakCol("load_1m"), avgCol("load_5m"), avgCol("load_15m"),
		},
	},
	{
		table: "host_metrics_1m", step: 60, maxSpan: spanMonth, retention: m1Retention,
		keys: []string{"node_id"},
		columns: []seriesColumn{
			keyCol("node_id"),
			peakCol("cpu_percent_max"),
			{sel: "CAST(mem_used_max AS INTEGER)", agg: "CAST(MAX(mem_used_max) AS INTEGER)"},
			peakCol("mem_total"), peakCol("disk_used"), peakCol("disk_total"),
			{sel: "CAST(net_rx_avg AS INTEGER)", agg: "CAST(AVG(net_rx_avg) AS INTEGER)"},
			{sel: "CAST(net_tx_avg AS INTEGER)", agg: "CAST(AVG(net_tx_avg) AS INTEGER)"},
			peakCol("load_1m_max"), constCol("0"), constCol("0"),
		},
	},
	{
		table: "host_metrics_1h", step: 3600, maxSpan: 1<<62 - 1, retention: forever,
		keys: []string{"node_id"},
		columns: []seriesColumn{
			keyCol("node_id"),
			peakCol("cpu_percent_max"),
			{sel: "CAST(mem_used_max AS INTEGER)", agg: "CAST(MAX(mem_used_max) AS INTEGER)"},
			peakCol("mem_total"), constCol("0"), constCol("0"),
			constCol("0"), constCol("0"),
			peakCol("load_1m_max"), constCol("0"), constCol("0"),
		},
	},
}}

// --- vLLM --------------------------------------------------------------

var vllmSeries = series{tiers: []seriesTier{
	{
		table: "vllm_metrics_raw", step: 1, maxSpan: spanHour, retention: rawRetention,
		keys: []string{"node_id", "model_name"},
		columns: []seriesColumn{
			keyCol("node_id"), keyCol("model_name"),
			peakCol("requests_running"), peakCol("requests_waiting"),
			peakCol("kv_cache_usage"),
			peakCol("generation_tokens_total"), peakCol("prompt_tokens_total"),
			avgCol("ttft_avg"), avgCol("tpot_avg"),
			peakCol("token_throughput"), avgCol("prefix_cache_hit_rate"),
			peakCol("num_preemptions"),
		},
	},
	{
		table: "vllm_metrics_1m", step: 60, maxSpan: spanMonth, retention: m1Retention,
		keys: []string{"node_id", "model_name"},
		columns: []seriesColumn{
			keyCol("node_id"), keyCol("model_name"),
			{sel: "CAST(requests_running_max AS INTEGER)", agg: "CAST(MAX(requests_running_max) AS INTEGER)"},
			{sel: "CAST(requests_waiting_max AS INTEGER)", agg: "CAST(MAX(requests_waiting_max) AS INTEGER)"},
			peakCol("kv_cache_usage_max"),
			peakCol("generation_tokens_total"), peakCol("prompt_tokens_total"),
			avgCol("ttft_avg"), avgCol("tpot_avg"),
			peakCol("token_throughput_max"), avgCol("prefix_cache_hit_rate_avg"),
			peakCol("num_preemptions"),
		},
	},
	{
		table: "vllm_metrics_1h", step: 3600, maxSpan: 1<<62 - 1, retention: forever,
		keys: []string{"node_id", "model_name"},
		columns: []seriesColumn{
			keyCol("node_id"), keyCol("model_name"),
			{sel: "CAST(requests_running_max AS INTEGER)", agg: "CAST(MAX(requests_running_max) AS INTEGER)"},
			{sel: "CAST(requests_waiting_max AS INTEGER)", agg: "CAST(MAX(requests_waiting_max) AS INTEGER)"},
			peakCol("kv_cache_usage_max"),
			peakCol("generation_tokens_total"), peakCol("prompt_tokens_total"),
			avgCol("ttft_avg"), avgCol("tpot_avg"),
			peakCol("token_throughput_max"), avgCol("prefix_cache_hit_rate_avg"),
			peakCol("num_preemptions"),
		},
	},
}}
