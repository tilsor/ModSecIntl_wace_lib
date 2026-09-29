/* Wrong Check Decision Plugin whose NewPlugin returns the concrete type instead of
 * waceapi.DecisionPlugin, so the function type assertion in the plugin manager fails
 */

package main

import (
	"context"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type wrongCheckDecision struct{}

// NewPlugin has the wrong return type
func NewPlugin(cfg waceapi.PluginConfig) (*wrongCheckDecision, error) {
	return &wrongCheckDecision{}, nil
}

func (d *wrongCheckDecision) CheckResults(ctx context.Context, decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	return waceapi.DecisionResult{Block: false}, nil
}

func (d *wrongCheckDecision) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (d *wrongCheckDecision) Clean() error {
	return nil
}
