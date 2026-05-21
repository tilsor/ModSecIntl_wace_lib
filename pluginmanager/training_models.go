package pluginmanager

import (
	"bufio"
	"context"
	"encoding/json"
	"os"

	"github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

func (p *PluginManager) handleTrainingModel(modelID string, td configstore.TrainingData, ctx context.Context, cancel context.CancelFunc, tc chan waceapi.ModelResults) {
	defer cancel()
	logger := logging.Get()
	logger.Printf(logging.INFO, "Model %s | Handling training data\n", modelID)

	f, err := os.OpenFile(td.ResultFilePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		logger.Printf(logging.ERROR, "Model %s | Error handling data: %s", modelID, err.Error())
		return
	}
	defer f.Close()

	lineCount := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineCount++
	}
	if err := scanner.Err(); err != nil {
		logger.Printf(logging.ERROR, "Model %s | Error handling data: %s", modelID, err.Error())
		return
	}

	encoder := json.NewEncoder(f)

	if lineCount >= td.MaxSamples {
		logger.Printf(logging.INFO, "Model %s | The maximum number of samples has already been written.\n", modelID)
		return
	} else {
		logger.Printf(logging.INFO, "Model %s | Previously amount of samples written %d\n", modelID, lineCount)
	}

	for i := 0; i < td.MaxSamples-lineCount; i++ {
		select {
		case data := <-tc:
			logger.Printf(logging.DEBUG, "Model %s | Recieved data %v", modelID, data)
			if err := encoder.Encode(data); err != nil {
				logger.Printf(logging.ERROR, "Model %s | Error writing data: %s", modelID, err.Error())
				return
			}
		case <-ctx.Done():
			logger.Printf(logging.DEBUG, "Model %s | Training cancelled\n", modelID)
			return
		}
	}

	logger.Printf(logging.INFO, "Model %s | Data collection for training completed.\n", modelID)
}

// ProcessTraining is in charge of calling the model plugin with id modelID
func (p *PluginManager) ProcessTraining(modelID, transactionID string, payload waceapi.HTTPPayload, t configstore.ModelPluginType) {
	logger := logging.Get()

	mp, exists := p.modelPlugins[modelID]
	if !exists {
		logger.TPrintf(logging.ERROR, transactionID, "Model %s not found", modelID)
		return
	}

	res, err := p.modelProcess(modelID, mp, waceapi.ModelInput{TransactionId: transactionID, Payload: payload}, t)
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
