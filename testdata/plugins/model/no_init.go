/* No Init model plugin that implements waceapi.ModelPlugin but exports no NewPlugin function
 */

package main

import (
	"context"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type noInitModel struct{}

var _ waceapi.ModelPlugin = (*noInitModel)(nil)

func (m *noInitModel) Process(ctx context.Context, input waceapi.ModelInput) (waceapi.ModelResults, error) {
	return waceapi.ModelResults{ProbAttack: 0.0}, nil
}

func (m *noInitModel) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (m *noInitModel) Clean() error {
	return nil
}
