/* Deadline Model Plugin that reports whether its context has a deadline:
it returns 1 probability of attack when it has one, 0 otherwise, and the
time left until the deadline as its data
*/

package main

import (
	"context"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type deadlineModel struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.ModelPlugin, error) {
	return &deadlineModel{}, nil
}

func (m *deadlineModel) Process(ctx context.Context, input waceapi.ModelInput) (waceapi.ModelResults, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return waceapi.ModelResults{ProbAttack: 0}, nil
	}
	return waceapi.ModelResults{ProbAttack: 1, Data: time.Until(deadline)}, nil
}

// Reload reloads the plugin (does nothing in this case)
func (m *deadlineModel) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (m *deadlineModel) Clean() error {
	return nil
}
