/* Error Req model plugin that raises an error in Process
 */

package main

import (
	"context"
	"errors"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type errorReqModel struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.ModelPlugin, error) {
	return &errorReqModel{}, nil
}

// Process always returns an error
func (m *errorReqModel) Process(ctx context.Context, input waceapi.ModelInput) (waceapi.ModelResults, error) {
	result := waceapi.ModelResults{
		ProbAttack: 0.0,
		Data:       make(map[string]interface{}),
	}
	return result, errors.New("Some error")
}

func (m *errorReqModel) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (m *errorReqModel) Clean() error {
	return nil
}
