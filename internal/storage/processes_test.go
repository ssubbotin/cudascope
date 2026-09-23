package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

func TestAProcessKeepsItsCommandLine(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()
	cmdline := "/venv/bin/python train.py --fold 3 --api-key ***"

	if err := db.WriteGPUProcesses([]collector.GPUProcess{
		{NodeID: "local", GPUID: 0, Timestamp: now, PID: 100, Name: "python", Cmdline: cmdline, GPUMem: 2048},
	}); err != nil {
		t.Fatalf("write processes: %v", err)
	}

	one, err := db.GetGPUProcesses(0, "local")
	if err != nil {
		t.Fatalf("processes of gpu 0: %v", err)
	}
	all, err := db.GetAllGPUProcesses()
	if err != nil {
		t.Fatalf("all processes: %v", err)
	}
	for _, procs := range [][]collector.GPUProcess{one, all} {
		if len(procs) != 1 || procs[0].Cmdline != cmdline {
			t.Fatalf("want the command line back, got %+v", procs)
		}
	}
}

// Rows written before the column existed hold NULL there, and so do rows an
// older agent sends without the field.
func TestAProcessWithoutACommandLineReadsAsEmpty(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	if _, err := db.conn.Exec(
		`INSERT INTO gpu_processes (ts, node_id, gpu_id, pid, name, gpu_mem) VALUES (?, 'local', 0, 100, 'python', 2048)`,
		now); err != nil {
		t.Fatalf("insert: %v", err)
	}

	one, err := db.GetGPUProcesses(0, "")
	if err != nil {
		t.Fatalf("processes of gpu 0: %v", err)
	}
	all, err := db.GetAllGPUProcesses()
	if err != nil {
		t.Fatalf("all processes: %v", err)
	}
	for _, procs := range [][]collector.GPUProcess{one, all} {
		if len(procs) != 1 || procs[0].Cmdline != "" || procs[0].Name != "python" {
			t.Fatalf("want the row with no command line, got %+v", procs)
		}
	}
}
