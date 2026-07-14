// Command wkclouddiagnosis validates the strict cloud Diagnosis Result contract.
package main

import (
	"fmt"
	"os"

	cloudanalysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "validate" {
		fmt.Fprintln(os.Stderr, "usage: wkclouddiagnosis validate FILE")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[2])
	if err == nil {
		_, err = cloudanalysis.DecodeDiagnosisResult(file)
		_ = file.Close()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid Diagnosis Result: %v\n", err)
		os.Exit(1)
	}
}
