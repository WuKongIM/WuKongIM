//go:build windows
// +build windows

package wknet

func GetMaxOpenFiles() int {
	return 1024 * 1024 * 2
}
