/* Trivial Test Decision Plugin that blocks only if the WAF says so
 */

package main

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

type testDecision struct {
	logger atomic.Pointer[slog.Logger]
}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.DecisionPlugin, error) {
	cfg.Logger.Warn("NewPlugin", "params", cfg.Params)
	d := &testDecision{}
	d.logger.Store(cfg.Logger)
	return d, nil
}

// CheckResults returns true (block traffic) if WAF says so, and false
// in other case.
func (d *testDecision) CheckResults(ctx context.Context, decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	modelRes := decisionInput.Results
	modelWeight := decisionInput.ModelWeight
	wafData := decisionInput.WAFdata
	transactionID := decisionInput.TransactionId

	d.logger.Load().Warn("CheckResults", waceapi.LogKeyTxID, transactionID,
		"model_results", modelRes, "model_weights", modelWeight, "waf_data", wafData)

	if len(wafData.Scores) != 0 {
		as := wafData.Scores["anomalyscore"]
		it := wafData.Scores["inboundthreshold"]
		if as >= it { // modsec wants to block
			return waceapi.DecisionResult{Block: true}, nil
		}
	}
	return waceapi.DecisionResult{Block: false}, nil
}

// Reload reloads the plugin (does nothing in this case)
func (d *testDecision) Reload(cfg waceapi.PluginConfig) error {
	d.logger.Store(cfg.Logger)
	return nil
}

func (d *testDecision) Clean() error {
	return nil
}
