package main

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterConfigFlagUsesConfUsage(t *testing.T) {
	fs := flag.NewFlagSet("wukongim", flag.ContinueOnError)

	configPath := registerConfigFlag(fs)

	require.NotNil(t, configPath)
	require.Equal(t, "path to wukongim.conf file", fs.Lookup("config").Usage)
}
