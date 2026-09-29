/* Lifecycle Model Plugin used to observe the plugin lifecycle from tests.
 *
 * Params:
 *   clean_file: path of a file that Clean creates when it is called
 *   fail_clean: if "true", Clean returns an error (after creating clean_file)
 *
 * Process echoes the Training flag it received in ModelResults.Data.
 */

package main

import (
	"context"
	"errors"
	"os"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type lifecycleModel struct {
	cleanFile string
	failClean bool
}

func NewPlugin(cfg waceapi.PluginConfig) (waceapi.ModelPlugin, error) {
	return &lifecycleModel{
		cleanFile: cfg.Params["clean_file"],
		failClean: cfg.Params["fail_clean"] == "true",
	}, nil
}

func (m *lifecycleModel) Process(ctx context.Context, input waceapi.ModelInput) (waceapi.ModelResults, error) {
	return waceapi.ModelResults{
		ProbAttack: 0.0,
		Data:       map[string]bool{"training": input.Training},
	}, nil
}

func (m *lifecycleModel) Reload(cfg waceapi.PluginConfig) error {
	return nil
}

func (m *lifecycleModel) Clean() error {
	if m.cleanFile != "" {
		if err := os.WriteFile(m.cleanFile, nil, 0644); err != nil {
			return err
		}
	}
	if m.failClean {
		return errors.New("clean failed")
	}
	return nil
}
