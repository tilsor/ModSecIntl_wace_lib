/* Deadline Decision Plugin that blocks when its context has a deadline
 */

package main

import (
	"context"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type deadlineDecision struct{}

func NewPlugin(cfg waceapi.PluginConfig) (waceapi.DecisionPlugin, error) {
	return &deadlineDecision{}, nil
}

func (d *deadlineDecision) CheckResults(ctx context.Context, decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	_, ok := ctx.Deadline()
	return waceapi.DecisionResult{Block: ok}, nil
}

func (d *deadlineDecision) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (d *deadlineDecision) Clean() error {
	return nil
}
