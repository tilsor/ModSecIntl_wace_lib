/* No Init model plugin that has no InitPlugin function
 */

package main

import pm "github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"

// Process always returns 0 probability of attack
func Process(input pm.ModelInput) (pm.ModelResults, error) {
	result := pm.ModelResults{
		ProbAttack: 0.0,
		Data:       make(map[string]interface{}),
	}
	return result, nil
}
