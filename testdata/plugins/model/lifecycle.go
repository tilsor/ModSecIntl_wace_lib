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
	"errors"
	"os"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type lifecycleModel struct {
	cleanFile string
	failClean bool
}

func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	return &lifecycleModel{
		cleanFile: params["clean_file"],
		failClean: params["fail_clean"] == "true",
	}, nil
}

func (m *lifecycleModel) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	return waceapi.ModelResults{
		ProbAttack: 0.0,
		Data:       map[string]bool{"training": input.Training},
	}, nil
}

func (m *lifecycleModel) Reload(params map[string]string, meter metric.Meter) error {
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
