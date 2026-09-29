/* Error Init model plugin that raises an error in NewPlugin
 */

package main

import (
	"errors"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

// NewPlugin always fails
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.ModelPlugin, error) {
	return nil, errors.New("Some error")
}
