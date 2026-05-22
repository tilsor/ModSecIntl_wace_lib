package pluginmanager

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

func countFileLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("countFileLines: open %s: %v", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
	}
	return n
}

func prefillResultsFile(t *testing.T, path string, count int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("prefillResultsFile: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := 0; i < count; i++ {
		if err := enc.Encode(waceapi.ModelResults{ProbAttack: float64(i) * 0.1}); err != nil {
			t.Fatalf("prefillResultsFile encode: %v", err)
		}
	}
}

// TestHandleTrainingModelWritesData verifies that handleTrainingModel writes
// each received result as a JSON line and exits after consuming MaxSamples.
func TestHandleTrainingModelWritesData(t *testing.T) {
	path := t.TempDir() + "/results.ndjson"
	td := configstore.TrainingData{MaxSamples: 3, ResultFilePath: path}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)

	for i := 0; i < 3; i++ {
		tc <- waceapi.ModelResults{ProbAttack: float64(i) * 0.5}
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after consuming MaxSamples")
	}

	if n := countFileLines(t, path); n != 3 {
		t.Errorf("file has %d lines, want 3", n)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open results: %v", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for i := 0; i < 3; i++ {
		var r waceapi.ModelResults
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode record %d: %v", i, err)
		}
		if want := float64(i) * 0.5; r.ProbAttack != want {
			t.Errorf("record %d: ProbAttack=%f, want %f", i, r.ProbAttack, want)
		}
	}
}

// TestHandleTrainingModelAlreadyFull verifies that handleTrainingModel exits
// immediately without reading from the channel when the file already contains
// MaxSamples lines.
func TestHandleTrainingModelAlreadyFull(t *testing.T) {
	path := t.TempDir() + "/results.ndjson"
	prefillResultsFile(t, path, 5)

	td := configstore.TrainingData{MaxSamples: 5, ResultFilePath: path}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("goroutine did not exit immediately when file was already full")
	}
	if n := countFileLines(t, path); n != 5 {
		t.Errorf("file has %d lines after full-file run, want 5 (no new writes)", n)
	}
}

// TestHandleTrainingModelResumesFromExisting verifies that handleTrainingModel
// counts existing lines and reads only the remaining samples needed, reaching
// MaxSamples total without re-writing previously collected data.
func TestHandleTrainingModelResumesFromExisting(t *testing.T) {
	path := t.TempDir() + "/results.ndjson"
	prefillResultsFile(t, path, 2)

	td := configstore.TrainingData{MaxSamples: 5, ResultFilePath: path}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)

	for i := 0; i < 3; i++ {
		tc <- waceapi.ModelResults{ProbAttack: float64(i)}
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after consuming remaining samples")
	}
	if n := countFileLines(t, path); n != 5 {
		t.Errorf("file has %d lines, want 5 (2 existing + 3 new)", n)
	}
}

// TestHandleTrainingModelCancellation verifies that handleTrainingModel stops
// reading from the channel and exits when the context is cancelled before
// MaxSamples are consumed.
func TestHandleTrainingModelCancellation(t *testing.T) {
	path := t.TempDir() + "/results.ndjson"
	td := configstore.TrainingData{MaxSamples: 10, ResultFilePath: path}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	done := make(chan struct{})
	go func() {
		(&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)
		close(done)
	}()

	tc <- waceapi.ModelResults{ProbAttack: 0.1}
	tc <- waceapi.ModelResults{ProbAttack: 0.2}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goroutine did not exit after context cancellation")
	}
	if n := countFileLines(t, path); n != 2 {
		t.Errorf("file has %d lines, want 2 (written before cancellation)", n)
	}
}

// TestHandleTrainingModelFileOpenError verifies that handleTrainingModel
// returns gracefully (without panicking) when the result file cannot be opened.
func TestHandleTrainingModelFileOpenError(t *testing.T) {
	td := configstore.TrainingData{MaxSamples: 3, ResultFilePath: "/nonexistent/dir/results.ndjson"}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	done := make(chan struct{})
	go func() {
		(&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleTrainingModel did not return on file open error")
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("cancel should have been called when returning on file error")
	}
}
