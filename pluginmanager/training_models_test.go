package pluginmanager

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"reflect"
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

func prefillStatusFile(t *testing.T, path string, status TrainingStatus) {
	t.Helper()
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("prefillStatusFile marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("prefillStatusFile write: %v", err)
	}
}

func readStatusFile(t *testing.T, path string) TrainingStatus {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readStatusFile read %s: %v", path, err)
	}
	var s TrainingStatus
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("readStatusFile unmarshal: %v", err)
	}
	return s
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

// ── CollectionStatus JSON ────────────────────────────────────────────────────

func TestCollectionStatusJSON(t *testing.T) {
	cases := []struct {
		status CollectionStatus
		json   string
	}{
		{Collecting, `"collecting"`},
		{Ready, `"ready"`},
		{Done, `"done"`},
		{Error, `"error"`},
	}
	for _, tc := range cases {
		t.Run(tc.json, func(t *testing.T) {
			data, err := json.Marshal(tc.status)
			if err != nil {
				t.Fatalf("Marshal(%v): %v", tc.status, err)
			}
			if string(data) != tc.json {
				t.Errorf("Marshal(%v) = %s, want %s", tc.status, data, tc.json)
			}
			var got CollectionStatus
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal(%s): %v", data, err)
			}
			if got != tc.status {
				t.Errorf("Unmarshal(%s) = %v, want %v", data, got, tc.status)
			}
		})
	}
	t.Run("invalid_string", func(t *testing.T) {
		var s CollectionStatus
		if err := json.Unmarshal([]byte(`"unknown"`), &s); err == nil {
			t.Error("expected error for unknown status string, got nil")
		}
	})
}

// ── loadStatus ───────────────────────────────────────────────────────────────

func TestLoadStatus(t *testing.T) {
	validStatus := TrainingStatus{
		CollectedSamples: 5,
		MinSamples:       3,
		MaxSamples:       10,
		Status:           Ready,
		CreatedAt:        time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	}

	cases := []struct {
		name    string
		setup   func(t *testing.T) string
		want    *TrainingStatus
		wantErr bool
	}{
		{
			name:  "empty path",
			setup: func(t *testing.T) string { return "" },
			want:  nil,
		},
		{
			name:  "missing file",
			setup: func(t *testing.T) string { return t.TempDir() + "/nonexistent.json" },
			want:  nil,
		},
		{
			name: "valid file",
			setup: func(t *testing.T) string {
				p := t.TempDir() + "/status.json"
				prefillStatusFile(t, p, validStatus)
				return p
			},
			want: &validStatus,
		},
		{
			name: "invalid JSON",
			setup: func(t *testing.T) string {
				p := t.TempDir() + "/status.json"
				if err := os.WriteFile(p, []byte("not-json"), 0644); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return p
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup(t)
			got, err := loadStatus(path)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// ── writeStatus ──────────────────────────────────────────────────────────────

func TestWriteStatus(t *testing.T) {
	t.Run("empty path is a no-op", func(t *testing.T) {
		if err := writeStatus(TrainingStatus{Status: Done}, ""); err != nil {
			t.Errorf("writeStatus(\"\") = %v, want nil", err)
		}
	})

	t.Run("atomic rename leaves no .tmp file", func(t *testing.T) {
		path := t.TempDir() + "/status.json"
		s := TrainingStatus{CollectedSamples: 7, MaxSamples: 10, Status: Collecting}
		if err := writeStatus(s, path); err != nil {
			t.Fatalf("writeStatus: %v", err)
		}
		if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
			t.Error(".tmp file still exists after writeStatus")
		}
		got, err := loadStatus(path)
		if err != nil {
			t.Fatalf("loadStatus after writeStatus: %v", err)
		}
		// Strip UpdatedAt (set dynamically) before comparing the rest.
		gotNoTime := *got
		gotNoTime.UpdatedAt = time.Time{}
		if !reflect.DeepEqual(gotNoTime, s) {
			t.Errorf("status mismatch: got %+v, want %+v", gotNoTime, s)
		}
	})

	t.Run("sets UpdatedAt on write", func(t *testing.T) {
		path := t.TempDir() + "/status.json"
		before := time.Now()
		if err := writeStatus(TrainingStatus{}, path); err != nil {
			t.Fatalf("writeStatus: %v", err)
		}
		got := readStatusFile(t, path)
		if got.UpdatedAt.Before(before) {
			t.Errorf("UpdatedAt %v is before write started at %v", got.UpdatedAt, before)
		}
	})

	t.Run("error on nonexistent directory", func(t *testing.T) {
		if err := writeStatus(TrainingStatus{}, "/nonexistent/dir/status.json"); err == nil {
			t.Error("expected error writing to nonexistent directory, got nil")
		}
	})
}

// ── handleTrainingModel status integration ───────────────────────────────────

func TestHandleTrainingModelStatus(t *testing.T) {
	cases := []struct {
		name           string
		setup          func(t *testing.T, dir string) configstore.TrainingData
		samples        int
		wantStatusFile bool
		// CreatedAt/UpdatedAt are zeroed before comparison; only deterministic fields matter.
		wantStatus TrainingStatus
	}{
		{
			name: "full run produces Done",
			setup: func(t *testing.T, dir string) configstore.TrainingData {
				return configstore.TrainingData{
					MaxSamples:           4,
					MinSamples:           2,
					ResultFilePath:       dir + "/results.ndjson",
					StatusFilePath:       dir + "/status.json",
					StatusUpdateInterval: 2,
				}
			},
			samples:        4,
			wantStatusFile: true,
			wantStatus:     TrainingStatus{CollectedSamples: 4, MinSamples: 2, MaxSamples: 4, Status: Done},
		},
		{
			name: "already full produces Done immediately",
			setup: func(t *testing.T, dir string) configstore.TrainingData {
				resultPath := dir + "/results.ndjson"
				prefillResultsFile(t, resultPath, 5)
				return configstore.TrainingData{
					MaxSamples:     5,
					ResultFilePath: resultPath,
					StatusFilePath: dir + "/status.json",
				}
			},
			samples:        0,
			wantStatusFile: true,
			wantStatus:     TrainingStatus{CollectedSamples: 5, MaxSamples: 5, Status: Done},
		},
		{
			name: "empty status path creates no status file",
			setup: func(t *testing.T, dir string) configstore.TrainingData {
				return configstore.TrainingData{
					MaxSamples:     2,
					ResultFilePath: dir + "/results.ndjson",
					// StatusFilePath intentionally empty
				}
			},
			samples:        2,
			wantStatusFile: false,
		},
		{
			name: "zero update interval still writes final Done",
			setup: func(t *testing.T, dir string) configstore.TrainingData {
				return configstore.TrainingData{
					MaxSamples:           3,
					ResultFilePath:       dir + "/results.ndjson",
					StatusFilePath:       dir + "/status.json",
					StatusUpdateInterval: 0,
				}
			},
			samples:        3,
			wantStatusFile: true,
			wantStatus:     TrainingStatus{CollectedSamples: 3, MaxSamples: 3, Status: Done},
		},
		{
			name: "MinSamples equal to MaxSamples produces Done not Ready",
			setup: func(t *testing.T, dir string) configstore.TrainingData {
				return configstore.TrainingData{
					MaxSamples:     3,
					MinSamples:     3,
					ResultFilePath: dir + "/results.ndjson",
					StatusFilePath: dir + "/status.json",
				}
			},
			samples:        3,
			wantStatusFile: true,
			wantStatus:     TrainingStatus{CollectedSamples: 3, MinSamples: 3, MaxSamples: 3, Status: Done},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			td := tc.setup(t, dir)
			ctx, cancel := context.WithCancel(context.Background())
			ch := make(chan waceapi.ModelResults)
			go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, ch)

			for i := 0; i < tc.samples; i++ {
				ch <- waceapi.ModelResults{ProbAttack: float64(i) * 0.1}
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("goroutine did not exit in time")
			}

			if !tc.wantStatusFile {
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("ReadDir: %v", err)
				}
				for _, e := range entries {
					if e.Name() != "results.ndjson" {
						t.Errorf("unexpected file in temp dir: %s", e.Name())
					}
				}
				return
			}

			s := readStatusFile(t, td.StatusFilePath)
			if s.CreatedAt.IsZero() {
				t.Error("CreatedAt should not be zero")
			}
			if s.UpdatedAt.IsZero() {
				t.Error("UpdatedAt should not be zero")
			}
			got := s
			got.CreatedAt = time.Time{}
			got.UpdatedAt = time.Time{}
			if !reflect.DeepEqual(got, tc.wantStatus) {
				t.Errorf("status mismatch:\ngot  %+v\nwant %+v", got, tc.wantStatus)
			}
		})
	}
}

// ── Restart behavior ─────────────────────────────────────────────────────────

func TestHandleTrainingModelRestartSkipsTerminal(t *testing.T) {
	cases := []struct {
		name   string
		status TrainingStatus
	}{
		{
			name:   "skips when Done",
			status: TrainingStatus{Status: Done, MaxSamples: 5, CreatedAt: time.Now()},
		},
		{
			name:   "skips when Error",
			status: TrainingStatus{Status: Error, MaxSamples: 5, ErrorMsg: "previous encoder failure", CreatedAt: time.Now()},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			resultPath := dir + "/results.ndjson"
			statusPath := dir + "/status.json"
			prefillStatusFile(t, statusPath, tc.status)

			td := configstore.TrainingData{
				MaxSamples:     5,
				ResultFilePath: resultPath,
				StatusFilePath: statusPath,
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				(&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, make(chan waceapi.ModelResults))
			}()

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("handleTrainingModel should return immediately when status is %v", tc.status.Status)
			}
			if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
				t.Errorf("result file should not have been created when status is %v", tc.status.Status)
			}
		})
	}
}

// TestHandleTrainingModelRestartPreservesCreatedAt verifies that when the
// handler resumes from an existing status file, it carries over the original
// CreatedAt timestamp rather than using time.Now().
func TestHandleTrainingModelRestartPreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	statusPath := dir + "/status.json"
	fixedCreatedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	prefillStatusFile(t, statusPath, TrainingStatus{
		Status:           Collecting,
		CollectedSamples: 0,
		MaxSamples:       3,
		CreatedAt:        fixedCreatedAt,
	})

	td := configstore.TrainingData{
		MaxSamples:     3,
		ResultFilePath: dir + "/results.ndjson",
		StatusFilePath: statusPath,
	}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)

	for i := 0; i < 3; i++ {
		tc <- waceapi.ModelResults{}
	}
	<-ctx.Done()

	s := readStatusFile(t, statusPath)
	if !s.CreatedAt.Equal(fixedCreatedAt) {
		t.Errorf("CreatedAt = %v, want %v (should be preserved from previous run)", s.CreatedAt, fixedCreatedAt)
	}
}

// TestHandleTrainingModelRestartResumesWhenCollecting verifies that the handler
// continues collecting from where it left off when the status file shows
// Status=Collecting and the result file has partially-collected data.
func TestHandleTrainingModelRestartResumesWhenCollecting(t *testing.T) {
	dir := t.TempDir()
	resultPath := dir + "/results.ndjson"
	statusPath := dir + "/status.json"

	prefillResultsFile(t, resultPath, 2)
	prefillStatusFile(t, statusPath, TrainingStatus{
		Status:           Collecting,
		CollectedSamples: 2,
		MaxSamples:       5,
		CreatedAt:        time.Now().Add(-time.Minute),
	})

	td := configstore.TrainingData{
		MaxSamples:     5,
		ResultFilePath: resultPath,
		StatusFilePath: statusPath,
	}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)

	for i := 0; i < 3; i++ {
		tc <- waceapi.ModelResults{}
	}
	<-ctx.Done()

	if n := countFileLines(t, resultPath); n != 5 {
		t.Errorf("result file has %d lines, want 5 (2 existing + 3 new)", n)
	}
	s := readStatusFile(t, statusPath)
	if s.Status != Done {
		t.Errorf("Status = %v, want Done after completion", s.Status)
	}
}

// TestHandleTrainingModelRestartResumesWhenReady verifies that Status=Ready is
// not treated as terminal — the handler must continue collecting the remaining
// samples when the result file still has room.
func TestHandleTrainingModelRestartResumesWhenReady(t *testing.T) {
	dir := t.TempDir()
	resultPath := dir + "/results.ndjson"
	statusPath := dir + "/status.json"

	prefillResultsFile(t, resultPath, 3)
	prefillStatusFile(t, statusPath, TrainingStatus{
		Status:           Ready,
		CollectedSamples: 3,
		MinSamples:       3,
		MaxSamples:       5,
		CreatedAt:        time.Now().Add(-time.Minute),
	})

	td := configstore.TrainingData{
		MaxSamples:     5,
		MinSamples:     3,
		ResultFilePath: resultPath,
		StatusFilePath: statusPath,
	}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)

	for i := 0; i < 2; i++ {
		tc <- waceapi.ModelResults{}
	}
	<-ctx.Done()

	if n := countFileLines(t, resultPath); n != 5 {
		t.Errorf("result file has %d lines, want 5 (3 existing + 2 new)", n)
	}
	s := readStatusFile(t, statusPath)
	if s.Status != Done {
		t.Errorf("Status = %v, want Done after completion", s.Status)
	}
}

// TestHandleTrainingModelCorruptedStatusFileProceedsFresh verifies that a
// status file containing invalid JSON is treated as absent: collection starts
// fresh and overwrites the file with valid content on completion.
func TestHandleTrainingModelCorruptedStatusFileProceedsFresh(t *testing.T) {
	dir := t.TempDir()
	statusPath := dir + "/status.json"

	if err := os.WriteFile(statusPath, []byte("not-json"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	td := configstore.TrainingData{
		MaxSamples:     2,
		ResultFilePath: dir + "/results.ndjson",
		StatusFilePath: statusPath,
	}
	ctx, cancel := context.WithCancel(context.Background())
	tc := make(chan waceapi.ModelResults)
	go (&PluginManager{}).handleTrainingModel("test", td, ctx, cancel, tc)

	for i := 0; i < 2; i++ {
		tc <- waceapi.ModelResults{}
	}
	<-ctx.Done()

	if n := countFileLines(t, td.ResultFilePath); n != 2 {
		t.Errorf("result file has %d lines, want 2", n)
	}
	s := readStatusFile(t, statusPath)
	if s.Status != Done {
		t.Errorf("Status = %v, want Done after fresh run", s.Status)
	}
}
