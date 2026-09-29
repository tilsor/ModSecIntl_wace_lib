/* Nil Init model plugin whose NewPlugin returns a nil plugin without an error
 */

package main

import (
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

// NewPlugin returns neither a plugin nor an error
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.ModelPlugin, error) {
	return nil, nil
}
