/* No Check Decision Plugin that implements waceapi.DecisionPlugin but exports no NewPlugin function
 */

package main

import (
	"context"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type noCheckDecision struct{}

var _ waceapi.DecisionPlugin = (*noCheckDecision)(nil)

func (d *noCheckDecision) CheckResults(ctx context.Context, decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	return waceapi.DecisionResult{Block: false}, nil
}

func (d *noCheckDecision) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (d *noCheckDecision) Clean() error {
	return nil
}
