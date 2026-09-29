/* Error Check Decision Plugin that always returns an error from CheckResults
 */

package main

import (
	"context"
	"errors"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type errorCheckDecision struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.DecisionPlugin, error) {
	return &errorCheckDecision{}, nil
}

// CheckResults always returns an error
func (d *errorCheckDecision) CheckResults(ctx context.Context, decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	return waceapi.DecisionResult{}, errors.New("Some error")
}

func (d *errorCheckDecision) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (d *errorCheckDecision) Clean() error {
	return nil
}
