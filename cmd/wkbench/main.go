package main

import (
	"fmt"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: wkbench <run|worker|validate|doctor|report>")
		return 1
	}
	switch args[0] {
	case "validate", "doctor", "run", "worker", "report":
		fmt.Fprintf(os.Stderr, "%s is not implemented yet\n", args[0])
		return 6
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		return 1
	}
}
