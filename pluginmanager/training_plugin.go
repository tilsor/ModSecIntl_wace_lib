package pluginmanager

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

// CollectionStatus defines the training data collection status
type CollectionStatus int

const (
	Collecting CollectionStatus = iota
	Ready
	Done
	Error
)

// pluginKind defines type of plugin used
type pluginKind int

const (
	modelKind pluginKind = iota
	decisionKind
)

// String returns the string representation of the plugin kind
func (t pluginKind) String() string {
	switch t {
	case modelKind:
		return "model"
	case decisionKind:
		return "decision"
	default:
		return "unknown"
	}
}

// logValue returns the plugin type value used in log attributes
func (t pluginKind) logValue() string {
	switch t {
	case modelKind:
		return waceapi.LogValueModelPluginType
	case decisionKind:
		return waceapi.LogValueDecisionPluginType
	default:
		return "unknown"
	}
}

// String returns the string representation of a status
func (t CollectionStatus) String() string {
	switch t {
	case Collecting:
		return "collecting"
	case Ready:
		return "ready"
	case Done:
		return "done"
	case Error:
		return "error"
	default:
		return "collecting"
	}
}

// StringToCollectionStatus converts a string to the corresponding model plugin type
func StringToCollectionStatus(textType string) (CollectionStatus, error) {
	switch textType {
	case "collecting":
		return Collecting, nil
	case "ready":
		return Ready, nil
	case "done":
		return Done, nil
	case "error":
		return Error, nil
	}
	return -1, fmt.Errorf("invalid data collection status %s", textType)
}

// MarshalJSON encodes CollectionStatus as its string representation.
func (s CollectionStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON decodes CollectionStatus from its string representation.
func (s *CollectionStatus) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	status, err := StringToCollectionStatus(str)
	if err != nil {
		return err
	}
	*s = status
	return nil
}

type TrainingStatus struct {
	CollectedSamples int              `json:"collected_samples"`
	MinSamples       int              `json:"min_samples"`
	MaxSamples       int              `json:"max_samples"`
	Status           CollectionStatus `json:"status"`
	ErrorMsg         string           `json:"error,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

// loadStatus reads and parses the status file. Returns nil without error if the file does not exist.
func loadStatus(filePath string) (*TrainingStatus, error) {
	if filePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var status TrainingStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// writeStatus marshals status to a .tmp file then atomically renames it to filePath,
// producing a single filesystem event for fsnotify watchers.
func writeStatus(status TrainingStatus, filePath string) error {
	if filePath == "" {
		return nil
	}
	status.UpdatedAt = time.Now()
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filePath)
}

func (p *PluginManager) handleTraining(ID string, td configstore.TrainingData, ctx context.Context, cancel context.CancelFunc, tc chan any, kind pluginKind) {
	defer cancel()
	// Collection can outlive a logger change, so the logger is checked on
	// every record. It is derived again only when it changed, because With
	// allocates even when the level is disabled.
	var base, current *slog.Logger
	logger := func() *slog.Logger {
		if l := p.getLogger(); l != base {
			base, current = l, l.With(waceapi.LogKeyPluginType, kind.logValue(), waceapi.LogKeyPlugin, ID)
		}
		return current
	}
	logger().Info("handling training data")

	// Check existing status file before doing any work.
	createdAt := time.Now()
	existing, err := loadStatus(td.StatusFilePath)
	if err != nil {
		logger().Error("cannot load training status file", "path", td.StatusFilePath, "error", err)
	}
	if existing != nil {
		if existing.Status == Done || existing.Status == Error {
			logger().Info("training already finished, skipping collection", "training.status", existing.Status)
			return
		}
		createdAt = existing.CreatedAt
	}

	f, err := os.OpenFile(td.ResultFilePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		logger().Error("cannot open training result file", "path", td.ResultFilePath, "error", err)
		return
	}
	defer f.Close()

	collectedSamples := 0
	// This has a 64KB line limit
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		collectedSamples++
	}
	if err := scanner.Err(); err != nil {
		logger().Error("cannot read training result file", "path", td.ResultFilePath, "error", err)
		return
	}

	encoder := json.NewEncoder(f)

	status := TrainingStatus{
		CollectedSamples: collectedSamples,
		MinSamples:       td.MinSamples,
		MaxSamples:       td.MaxSamples,
		CreatedAt:        createdAt,
	}

	if collectedSamples >= td.MaxSamples {
		logger().Info("maximum number of samples already written", "samples.collected", collectedSamples, "samples.max", td.MaxSamples)
		status.Status = Done
		if err := writeStatus(status, td.StatusFilePath); err != nil {
			logger().Error("cannot write training status", "path", td.StatusFilePath, "error", err)
		}
		return
	}

	logger().Info("previously written samples", "samples.collected", collectedSamples)
	if collectedSamples >= td.MinSamples {
		status.Status = Ready
	} else {
		status.Status = Collecting
	}
	if err := writeStatus(status, td.StatusFilePath); err != nil {
		logger().Error("cannot write training status", "path", td.StatusFilePath, "error", err)
	}

	for collectedSamples < td.MaxSamples {
		select {
		case data := <-tc:
			logger().Debug("received training data", "data", data)
			if err := encoder.Encode(data); err != nil {
				logger().Error("cannot write training data", "path", td.ResultFilePath, "error", err)
				status.Status = Error
				status.ErrorMsg = err.Error()
				if err := writeStatus(status, td.StatusFilePath); err != nil {
					logger().Error("cannot write training status", "path", td.StatusFilePath, "error", err)
				}
				return
			}
			collectedSamples++
			status.CollectedSamples = collectedSamples
			switch collectedSamples {
			case td.MaxSamples:
				status.Status = Done
			case td.MinSamples:
				status.Status = Ready
			}
			if collectedSamples == td.MinSamples || collectedSamples == td.MaxSamples || (td.StatusUpdateInterval > 0 && collectedSamples%td.StatusUpdateInterval == 0) {
				if err := writeStatus(status, td.StatusFilePath); err != nil {
					logger().Error("cannot write training status", "path", td.StatusFilePath, "error", err)
				}
			}
		case <-ctx.Done():
			logger().Debug("training cancelled")
			return
		}
	}

	logger().Info("training data collection completed")
}

// ProcessTraining is in charge of calling the model plugin with id modelID
func (p *PluginManager) ProcessTraining(modelID, transactionID string, payload waceapi.HTTPPayload, t configstore.ModelPluginType) {
	logger := p.getLogger().With(waceapi.LogKeyTxID, transactionID,
		waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
		waceapi.LogKeyPlugin, modelID)

	p.modelMutex.RLock()
	mp, exists := p.modelPlugins[modelID]
	p.modelMutex.RUnlock()
	if !exists {
		logger.Error("plugin not found")
		return
	}

	if mp.trainingCtx == nil {
		logger.Debug("training not started, dropping request")
		return
	}

	// TODO: receive the context from Analyze; the training result is
	// collected after the request, so it must not be cancelled with it
	res, err := p.modelProcess(context.TODO(), modelID, mp, waceapi.ModelInput{TransactionId: transactionID, Payload: payload, Training: true}, t)
	if err != nil {
		logger.Error("cannot process training request", "error", err)
		return
	}

	select {
	case mp.trainingChannel <- res:
	case <-mp.trainingCtx.Done():
		logger.Debug("training cancelled, dropping result")
	}
}
