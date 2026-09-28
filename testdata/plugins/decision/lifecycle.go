/* Lifecycle Decision Plugin used to observe the plugin lifecycle from tests.
 *
 * Params:
 *   clean_file: path of a file that Clean creates when it is called
 *   fail_clean: if "true", Clean returns an error (after creating clean_file)
 *
 * CheckResults never blocks and echoes the Training flag it received in
 * DecisionResult.Data.
 */

package main

import (
	"errors"
	"os"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type lifecycleDecision struct {
	cleanFile string
	failClean bool
}

func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.DecisionPlugin, error) {
	return &lifecycleDecision{
		cleanFile: params["clean_file"],
		failClean: params["fail_clean"] == "true",
	}, nil
}

func (d *lifecycleDecision) CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	return waceapi.DecisionResult{
		Block: false,
		Data:  map[string]bool{"training": decisionInput.Training},
	}, nil
}

func (d *lifecycleDecision) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (d *lifecycleDecision) Clean() error {
	if d.cleanFile != "" {
		if err := os.WriteFile(d.cleanFile, nil, 0644); err != nil {
			return err
		}
	}
	if d.failClean {
		return errors.New("clean failed")
	}
	return nil
}
