package server

import (
	"fmt"
	"testing"
)

func TestSameFrames(t *testing.T) {

	tests := make([]string, 0, 100)
	tests = append(tests, "1", "2", "3", "4")

	panic(fmt.Sprintf("%s", tests[4:5]))
}
