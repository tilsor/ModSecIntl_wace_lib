/* Wrong Init model plugin that has wrong InitPlugin type
 */

package main

// InitPlugin intitalizes the plugins (does nothing in this case)
func InitPlugin() error {
	return nil
}

// Process always returns 0 probability of attack
func Process() (float64, error) {
	return 0.0, nil
}
