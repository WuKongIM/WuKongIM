//go:build !(darwin || linux)

package mqttowner

import "os"

// lockDir fails closed where no exclusive process lock is available, so no
// crashed boot can be inferred.
func lockDir(string) (*os.File, error) { return nil, errRetirement }
