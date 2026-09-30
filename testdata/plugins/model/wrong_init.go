/* Wrong Init model plugin whose NewPlugin returns the concrete type instead of
 * waceapi.ModelPlugin, so the function type assertion in the plugin manager fails
 */

package main

import (
	"context"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type wrongInitModel struct{}

// NewPlugin has the wrong return type
func NewPlugin(cfg waceapi.PluginConfig) (*wrongInitModel, error) {
	return &wrongInitModel{}, nil
}

func (m *wrongInitModel) Process(ctx context.Context, input waceapi.ModelInput) (waceapi.ModelResults, error) {
	return waceapi.ModelResults{ProbAttack: 0.0}, nil
}

func (m *wrongInitModel) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (m *wrongInitModel) Clean() error {
	return nil
}
