package pluginmanager

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/tilsor/ModSecIntl_logging/logging"
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

func (p *PluginManager) handleTraining(ID string, td configstore.TrainingData, ctx context.Context, cancel context.CancelFunc, tc chan any, kind string) {
	defer cancel()
	logger := logging.Get()
	logger.Printf(logging.INFO, "%s %s | Handling training data\n", kind, ID)

	// Check existing status file before doing any work.
	createdAt := time.Now()
	existing, err := loadStatus(td.StatusFilePath)
	if err != nil {
		logger.Printf(logging.ERROR, "%s %s | Error loading status file: %s", kind, ID, err.Error())
	}
	if existing != nil {
		if existing.Status == Done || existing.Status == Error {
			logger.Printf(logging.INFO, "%s %s | Training already %s, skipping collection\n", kind, ID, existing.Status)
			return
		}
		createdAt = existing.CreatedAt
	}

	f, err := os.OpenFile(td.ResultFilePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		logger.Printf(logging.ERROR, "%s %s | Error handling data: %s", kind, ID, err.Error())
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
		logger.Printf(logging.ERROR, "%s %s | Error handling data: %s", kind, ID, err.Error())
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
		logger.Printf(logging.INFO, "%s %s | The maximum number of samples has already been written.\n", kind, ID)
		status.Status = Done
		if err := writeStatus(status, td.StatusFilePath); err != nil {
			logger.Printf(logging.ERROR, "%s %s | Error writing status: %s", kind, ID, err.Error())
		}
		return
	}

	logger.Printf(logging.INFO, "%s %s | Previously amount of samples written %d\n", kind, ID, collectedSamples)
	if collectedSamples >= td.MinSamples {
		status.Status = Ready
	} else {
		status.Status = Collecting
	}
	if err := writeStatus(status, td.StatusFilePath); err != nil {
		logger.Printf(logging.ERROR, "%s %s | Error writing status: %s", kind, ID, err.Error())
	}

	for collectedSamples < td.MaxSamples {
		select {
		case data := <-tc:
			logger.Printf(logging.DEBUG, "%s %s | Recieved data %v", kind, ID, data)
			if err := encoder.Encode(data); err != nil {
				logger.Printf(logging.ERROR, "%s %s | Error writing data: %s", kind, ID, err.Error())
				status.Status = Error
				status.ErrorMsg = err.Error()
				if err := writeStatus(status, td.StatusFilePath); err != nil {
					logger.Printf(logging.ERROR, "%s %s | Error writing status: %s", kind, ID, err.Error())
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
					logger.Printf(logging.ERROR, "%s %s | Error writing status: %s", kind, ID, err.Error())
				}
			}
		case <-ctx.Done():
			logger.Printf(logging.DEBUG, "%s %s | Training cancelled\n", kind, ID)
			return
		}
	}

	logger.Printf(logging.INFO, "%s %s | Data collection for training completed.\n", kind, ID)
}

// ProcessTraining is in charge of calling the model plugin with id modelID
func (p *PluginManager) ProcessTraining(modelID, transactionID string, payload waceapi.HTTPPayload, t configstore.ModelPluginType) {
	logger := logging.Get()

	mp, exists := p.modelPlugins[modelID]
	if !exists {
		logger.TPrintf(logging.ERROR, transactionID, "Model %s not found", modelID)
		return
	}

	res, err := p.modelProcess(modelID, mp, waceapi.ModelInput{TransactionId: transactionID, Payload: payload, TrainingMode: true}, t)
	if err != nil {
		logger.TPrintf(logging.ERROR, transactionID, "Error processing model %s: %s", modelID, err.Error())
		return
	}

	select {
	case mp.trainingChannel <- res:
	case <-mp.trainingCtx.Done():
		logger.TPrintf(logging.DEBUG, transactionID, "training cancelled for model %s, dropping result", modelID)
	}
}
