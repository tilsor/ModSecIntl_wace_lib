package wace

import (
	"fmt"

	"github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

// queueInput is the input of the queues of the async and remote model
// plugins, encoded the first time it is sent.
type queueInput struct {
	transactionID string
	payload       waceapi.HTTPPayload
	encoded       bool
	data          []byte
	err           error
}

// encode returns the encoded input, encoding it on the first call.
func (q *queueInput) encode() ([]byte, error) {
	if !q.encoded {
		q.data, q.err = pluginmanager.EncodeModelInput(q.transactionID, q.payload)
		q.encoded = true
	}
	return q.data, q.err
}

// sendToQueue sends input to the queue of the model plugin with id
// modelID. When it cannot be sent the model plugin will never answer,
// so the failure is reported on status instead of being waited for
// until the timeout.
func sendToQueue(modelID string, input *queueInput, status chan pluginmanager.ModelStatus) {
	data, err := input.encode()
	if err == nil {
		err = pm.PublishModelInput(modelID, data)
	}
	if err != nil {
		// status has room for one status per model plugin
		status <- pluginmanager.ModelStatus{ModelID: modelID, Err: fmt.Errorf("cannot send to the model plugin queue: %w", err)}
	}
}
