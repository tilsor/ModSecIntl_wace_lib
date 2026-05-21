package pluginmanager

import (
	"context"

	"github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

func (p *PluginManager) handleTrainingModel(modelID string, td configstore.TrainingData, ctx context.Context, cancel context.CancelFunc, tc chan waceapi.ModelResults) {
	defer cancel()
	logger := logging.Get()
	logger.Printf(logging.DEBUG, "handling training model %s\n", modelID)
	for i := 0; i < td.MaxSamples; i++ {
		select {
		case data := <-tc:
			logger.Printf(logging.DEBUG, "training model %s, recieved data %v", modelID, data)
		case <-ctx.Done():
			logger.Printf(logging.DEBUG, "training model %s cancelled\n", modelID)
			return
		}
	}
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
